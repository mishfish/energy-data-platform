package sink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Record is a consumed Kafka message.
type Record struct {
	Topic     string
	Partition int32
	Offset    int64
	Value     []byte // nil for a tombstone
}

// Offset is the next offset to consume from a partition, i.e. what a commit stores.
type Offset struct {
	Topic     string
	Partition int32
	Next      int64
}

// Consumer is the part of the Kafka consumer the sink needs.
type Consumer interface {
	// Poll returns the next record, or nil if none arrived within the timeout. Transient broker
	// errors are handled inside; an error here is fatal.
	Poll(timeout time.Duration) (*Record, error)
	// Commit stores the given offsets for the consumer group.
	Commit(offsets []Offset) error
}

// Decoder turns a record into a row; errSkip means the record carries none.
type Decoder interface {
	Decode(r Record) (Row, error)
}

// Writer writes rows to ClickHouse. A write is idempotent: rows that are already stored collapse
// by key and LSN.
type Writer interface {
	Write(ctx context.Context, rows []Row) error
}

// Sink consumes, batches, writes, then commits. Delivery is at-least-once (ADR 0026): a crash at
// any point re-reads from the last commit, and the redelivered rows collapse in ClickHouse.
type Sink struct {
	Consumer Consumer
	Decoder  Decoder
	Writer   Writer
	Metrics  *Metrics
	Logger   *slog.Logger

	BatchSize     int           // flush when this many records are buffered
	FlushInterval time.Duration // or when the oldest buffered record is this old

	// Write retry backoff. Writes are retried until they succeed or the sink is stopped, so a
	// ClickHouse outage stalls the sink (lag grows, alert fires) instead of skipping data.
	RetryBase time.Duration
	RetryMax  time.Duration

	batch    []Row
	offsets  map[partitionKey]int64
	deadline time.Time
}

type partitionKey struct {
	topic     string
	partition int32
}

const pollTimeout = 100 * time.Millisecond

// Run consumes until ctx is cancelled or a fatal error occurs. On cancellation it flushes the
// buffered batch once (bounded by shutdownCtx) and returns nil.
//
// A record that cannot be decoded stops the sink with an error and is not committed (fail-fast,
// ADR 0028): restarting re-reads it, so nothing is skipped silently.
func (s *Sink) Run(ctx context.Context, shutdownCtx context.Context) error {
	s.reset()
	for {
		if ctx.Err() != nil {
			if err := s.flush(shutdownCtx); err != nil {
				s.Logger.Warn("final flush failed; uncommitted records will be redelivered", "err", err)
			}
			return nil
		}

		rec, err := s.Consumer.Poll(pollTimeout)
		if err != nil {
			return fmt.Errorf("poll: %w", err)
		}
		if rec != nil {
			if err := s.add(*rec); err != nil {
				return err
			}
		}
		if s.due() {
			if err := s.flush(ctx); err != nil && ctx.Err() == nil {
				return err
			}
		}
	}
}

// DropBatch discards buffered records without committing them. The Kafka consumer calls it when
// partitions are revoked: the new owner re-reads from the last commit, and anything this sink has
// already written collapses on the second write.
func (s *Sink) DropBatch() {
	if len(s.offsets) > 0 {
		s.Logger.Info("partitions revoked, dropping uncommitted batch", "records", len(s.batch))
	}
	s.reset()
}

func (s *Sink) add(rec Record) error {
	row, err := s.Decoder.Decode(rec)
	switch {
	case errors.Is(err, errSkip):
		s.Metrics.records.WithLabelValues(rec.Topic, "skipped").Inc()
	case err != nil:
		s.Metrics.records.WithLabelValues(rec.Topic, "poison").Inc()
		return fmt.Errorf("poison message %s[%d]@%d: %w", rec.Topic, rec.Partition, rec.Offset, err)
	default:
		s.batch = append(s.batch, row)
		s.Metrics.records.WithLabelValues(rec.Topic, "decoded").Inc()
	}
	if len(s.offsets) == 0 {
		s.deadline = time.Now().Add(s.FlushInterval)
	}
	s.offsets[partitionKey{rec.Topic, rec.Partition}] = rec.Offset + 1
	return nil
}

func (s *Sink) due() bool {
	return len(s.offsets) > 0 && (len(s.batch) >= s.BatchSize || !time.Now().Before(s.deadline))
}

// flush writes the batch, retrying until it succeeds or ctx ends, then commits its offsets.
// The order is the delivery guarantee: commit never runs before the write it covers.
func (s *Sink) flush(ctx context.Context) error {
	if len(s.offsets) == 0 {
		return nil
	}
	if err := s.writeWithRetry(ctx); err != nil {
		return err
	}

	offsets := make([]Offset, 0, len(s.offsets))
	for k, next := range s.offsets {
		offsets = append(offsets, Offset{Topic: k.topic, Partition: k.partition, Next: next})
	}
	if err := s.Consumer.Commit(offsets); err != nil {
		// The rows are written; without the commit they are redelivered later and collapse.
		// Not fatal: typically the group rebalanced while the write was retried.
		s.Metrics.batches.WithLabelValues("commit_error").Inc()
		s.Logger.Warn("commit failed after a successful write; records will be redelivered", "err", err)
		s.reset()
		return nil
	}
	s.Metrics.batches.WithLabelValues("success").Inc()
	s.Metrics.lastCommit.SetToCurrentTime()
	s.reset()
	return nil
}

func (s *Sink) writeWithRetry(ctx context.Context) error {
	delay := s.RetryBase
	for attempt := 1; ; attempt++ {
		started := time.Now()
		err := s.Writer.Write(ctx, s.batch)
		s.Metrics.writeDuration.Observe(time.Since(started).Seconds())
		if err == nil {
			return nil
		}
		s.Metrics.batches.WithLabelValues("write_error").Inc()
		s.Logger.Error("batch write failed, retrying", "attempt", attempt, "rows", len(s.batch), "retry_in", delay.String(), "err", err)

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("write abandoned after %d attempts: %w", attempt, err)
		case <-timer.C:
		}
		delay = min(2*delay, s.RetryMax)
	}
}

func (s *Sink) reset() {
	s.batch = s.batch[:0]
	s.offsets = make(map[partitionKey]int64)
}
