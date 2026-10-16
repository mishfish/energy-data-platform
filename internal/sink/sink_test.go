package sink

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// fakeConsumer hands out records in order and records commits. When it runs out it waits for the
// poll timeout like a real consumer, which advances the synctest clock.
type fakeConsumer struct {
	mu        sync.Mutex
	records   []Record
	commits   [][]Offset
	commitErr error
	log       *[]string
}

func (c *fakeConsumer) Poll(timeout time.Duration) (*Record, error) {
	c.mu.Lock()
	if len(c.records) == 0 {
		c.mu.Unlock()
		time.Sleep(timeout)
		return nil, nil
	}
	r := c.records[0]
	c.records = c.records[1:]
	c.mu.Unlock()
	return &r, nil
}

func (c *fakeConsumer) Commit(offsets []Offset) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.log = append(*c.log, "commit")
	if c.commitErr != nil {
		return c.commitErr
	}
	slices.SortFunc(offsets, func(a, b Offset) int { return int(a.Partition - b.Partition) })
	c.commits = append(c.commits, offsets)
	return nil
}

func (c *fakeConsumer) committed() [][]Offset {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.commits)
}

// fakeDecoder: a record value "poison" fails, nil is a tombstone, anything else is a row.
type fakeDecoder struct{}

func (fakeDecoder) Decode(r Record) (Row, error) {
	switch string(r.Value) {
	case "":
		return Row{}, errSkip
	case "poison":
		return Row{}, errors.New("unknown schema id")
	}
	return Row{Table: "day_ahead_price", LSN: uint64(r.Offset)}, nil
}

// fakeWriter fails the first failures writes and records the rest.
type fakeWriter struct {
	mu       sync.Mutex
	failures int
	written  [][]Row
	log      *[]string
}

func (w *fakeWriter) Write(_ context.Context, rows []Row) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failures > 0 {
		w.failures--
		*w.log = append(*w.log, "write_error")
		return errors.New("clickhouse unavailable")
	}
	*w.log = append(*w.log, "write")
	w.written = append(w.written, slices.Clone(rows))
	return nil
}

func records(partition int32, offsets ...int64) []Record {
	out := make([]Record, len(offsets))
	for i, o := range offsets {
		out[i] = Record{Topic: "energy.public.day_ahead_price", Partition: partition, Offset: o, Value: []byte("row")}
	}
	return out
}

func newTestSink(t *testing.T, consumer *fakeConsumer, writer *fakeWriter) *Sink {
	t.Helper()
	metrics, err := NewMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	return &Sink{
		Consumer:      consumer,
		Decoder:       fakeDecoder{},
		Writer:        writer,
		Metrics:       metrics,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		BatchSize:     3,
		FlushInterval: 2 * time.Second,
		RetryBase:     time.Second,
		RetryMax:      4 * time.Second,
	}
}

// runFor runs the sink for d of fake time, then stops it with a shutdown budget of 10 s.
func runFor(t *testing.T, s *Sink, d time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	shutdownCtx, cancelShutdown := context.WithTimeout(context.WithoutCancel(t.Context()), d+10*time.Second)
	defer cancelShutdown()
	errCh := make(chan error, 1)
	go func() { errCh <- s.Run(ctx, shutdownCtx) }()
	select {
	case err := <-errCh:
		cancel()
		return err
	case <-time.After(d):
	}
	cancel()
	return <-errCh
}

func TestSinkCommitsOnlyAfterWrite(t *testing.T) {
	tests := []struct {
		name        string
		records     []Record
		failures    int
		wantLog     []string
		wantCommits [][]Offset
		wantRows    int
	}{
		{
			name:    "flush by size, then the rest on shutdown",
			records: records(0, 10, 11, 12, 13),
			wantLog: []string{"write", "commit", "write", "commit"},
			wantCommits: [][]Offset{
				{{"energy.public.day_ahead_price", 0, 13}},
				{{"energy.public.day_ahead_price", 0, 14}},
			},
			wantRows: 4,
		},
		{
			name:        "flush by interval",
			records:     records(0, 10),
			wantLog:     []string{"write", "commit"},
			wantCommits: [][]Offset{{{"energy.public.day_ahead_price", 0, 11}}},
			wantRows:    1,
		},
		{
			name:        "write retried until it succeeds, commit only after",
			records:     records(0, 10, 11, 12),
			failures:    2,
			wantLog:     []string{"write_error", "write_error", "write", "commit"},
			wantCommits: [][]Offset{{{"energy.public.day_ahead_price", 0, 13}}},
			wantRows:    3,
		},
		{
			name:    "offsets per partition",
			records: append(records(0, 10, 11), records(1, 5)...),
			wantLog: []string{"write", "commit"},
			wantCommits: [][]Offset{{
				{"energy.public.day_ahead_price", 0, 12},
				{"energy.public.day_ahead_price", 1, 6},
			}},
			wantRows: 3,
		},
		{
			name: "tombstone advances the offset without a row",
			records: []Record{
				{Topic: "energy.public.day_ahead_price", Partition: 0, Offset: 10, Value: []byte("row")},
				{Topic: "energy.public.day_ahead_price", Partition: 0, Offset: 11},
			},
			wantLog:     []string{"write", "commit"},
			wantCommits: [][]Offset{{{"energy.public.day_ahead_price", 0, 12}}},
			wantRows:    1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var log []string
				consumer := &fakeConsumer{records: tt.records, log: &log}
				writer := &fakeWriter{failures: tt.failures, log: &log}
				s := newTestSink(t, consumer, writer)

				if err := runFor(t, s, time.Minute); err != nil {
					t.Fatalf("Run: %v", err)
				}
				if !slices.Equal(log, tt.wantLog) {
					t.Errorf("sequence %v, want %v", log, tt.wantLog)
				}
				if got := consumer.committed(); !slices.EqualFunc(got, tt.wantCommits, slices.Equal) {
					t.Errorf("commits %v, want %v", got, tt.wantCommits)
				}
				var rows int
				for _, batch := range writer.written {
					rows += len(batch)
				}
				if rows != tt.wantRows {
					t.Errorf("wrote %d rows, want %d", rows, tt.wantRows)
				}
			})
		})
	}
}

func TestSinkPoisonStopsWithoutCommit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var log []string
		consumer := &fakeConsumer{log: &log, records: []Record{
			{Topic: "energy.public.day_ahead_price", Partition: 0, Offset: 10, Value: []byte("row")},
			{Topic: "energy.public.day_ahead_price", Partition: 0, Offset: 11, Value: []byte("poison")},
			{Topic: "energy.public.day_ahead_price", Partition: 0, Offset: 12, Value: []byte("row")},
		}}
		s := newTestSink(t, consumer, &fakeWriter{log: &log})

		err := runFor(t, s, time.Minute)
		if err == nil {
			t.Fatal("Run returned no error for a poison message")
		}
		// Nothing after the last commit may be committed: a restart must re-read the poison record.
		if len(consumer.committed()) != 0 || len(log) != 0 {
			t.Errorf("poison message led to %v", log)
		}
	})
}

func TestSinkCommitErrorIsNotFatal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var log []string
		consumer := &fakeConsumer{records: records(0, 10, 11, 12), commitErr: errors.New("not a group member"), log: &log}
		writer := &fakeWriter{log: &log}
		s := newTestSink(t, consumer, writer)

		if err := runFor(t, s, time.Minute); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if want := []string{"write", "commit"}; !slices.Equal(log, want) {
			t.Errorf("sequence %v, want %v", log, want)
		}
	})
}

func TestSinkDropBatchOnRevoke(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var log []string
		consumer := &fakeConsumer{records: records(0, 10, 11), log: &log}
		s := newTestSink(t, consumer, &fakeWriter{log: &log})
		s.reset()
		for range 2 {
			r, _ := consumer.Poll(time.Millisecond)
			if err := s.add(*r); err != nil {
				t.Fatalf("add: %v", err)
			}
		}

		s.DropBatch()
		if err := s.flush(t.Context()); err != nil {
			t.Fatalf("flush: %v", err)
		}
		if len(log) != 0 {
			t.Errorf("dropped batch was still written or committed: %v", log)
		}
	})
}

func TestSinkShutdownAbandonsFailingWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var log []string
		consumer := &fakeConsumer{records: records(0, 10), log: &log}
		writer := &fakeWriter{failures: 1000, log: &log}
		s := newTestSink(t, consumer, writer)

		if err := runFor(t, s, 5*time.Second); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(consumer.committed()) != 0 {
			t.Errorf("committed %v although no write succeeded", consumer.committed())
		}
	})
}

func TestObserveStats(t *testing.T) {
	metrics, err := NewMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	stats := `{"topics": {"energy.public.actual_load": {"partitions": {
		"0": {"consumer_lag": 42}, "1": {"consumer_lag": -1}, "-1": {"consumer_lag": 7}}}}}`
	if err := metrics.ObserveStats(stats); err != nil {
		t.Fatalf("ObserveStats: %v", err)
	}
	if got := testGauge(t, metrics.lag.WithLabelValues("energy.public.actual_load", "0")); got != 42 {
		t.Errorf("lag of partition 0 = %v, want 42", got)
	}
	if n := countSeries(t, metrics.lag); n != 1 {
		t.Errorf("%d lag series, want 1 (unknown lag and the internal partition are skipped)", n)
	}
	if err := metrics.ObserveStats("not json"); err == nil {
		t.Error("no error for invalid stats")
	}
}
