//go:build integration

package sink_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"
	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry/serde"
	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry/serde/avrov2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/testcontainers/testcontainers-go"
	tcclickhouse "github.com/testcontainers/testcontainers-go/modules/clickhouse"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/ClickHouse/clickhouse-go/v2"

	"github.com/mishfish/energy-data-platform/internal/chdb"
	"github.com/mishfish/energy-data-platform/internal/sink"
)

const (
	priceTopic = "energy.public.day_ahead_price"
	loadTopic  = "energy.public.actual_load"
)

// Change events shaped like Debezium's (ADR 0022), with the schema registered explicitly like
// Debezium does: the row record is defined once and reused by before and after.
type priceImage struct {
	BiddingZone     string `avro:"bidding_zone"`
	DeliveryStart   string `avro:"delivery_start"`
	Resolution      int64  `avro:"resolution"`
	PriceEURMWh     string `avro:"price_eur_mwh"`
	SourceCreatedAt string `avro:"source_created_at"`
}

type loadImage struct {
	BiddingZone     string `avro:"bidding_zone"`
	DeliveryStart   string `avro:"delivery_start"`
	Resolution      int64  `avro:"resolution"`
	LoadMW          string `avro:"load_mw"`
	SourceCreatedAt string `avro:"source_created_at"`
}

type source struct {
	LSN *int64 `avro:"lsn"`
}

type priceEvent struct {
	Before *priceImage `avro:"before"`
	After  *priceImage `avro:"after"`
	Source source      `avro:"source"`
	Op     string      `avro:"op"`
}

type loadEvent struct {
	Before *loadImage `avro:"before"`
	After  *loadImage `avro:"after"`
	Source source     `avro:"source"`
	Op     string     `avro:"op"`
}

// envelopeSchema returns the Avro schema of a change event of a table whose amount column is amount.
func envelopeSchema(table, amount string) string {
	return `{"type": "record", "name": "Envelope", "namespace": "energy.public.` + table + `", "fields": [
	  {"name": "before", "type": ["null", {"type": "record", "name": "Value", "fields": [
	    {"name": "bidding_zone", "type": "string"},
	    {"name": "delivery_start", "type": "string"},
	    {"name": "resolution", "type": "long"},
	    {"name": "` + amount + `", "type": "string"},
	    {"name": "source_created_at", "type": "string"}]}], "default": null},
	  {"name": "after", "type": ["null", "Value"], "default": null},
	  {"name": "source", "type": {"type": "record", "name": "Source", "fields": [
	    {"name": "lsn", "type": ["null", "long"], "default": null}]}},
	  {"name": "op", "type": "string"}]}`
}

func at(lsn int64) source { return source{LSN: &lsn} }

func price(slot, amount string) *priceImage {
	return &priceImage{"10YNL----------L", slot, 900_000_000, amount, "2026-10-01T11:02:37.000000Z"}
}

type stack struct {
	brokers     string
	registryURL string
	chDSN       string
}

func startStack(t *testing.T) stack {
	t.Helper()
	ctx := t.Context()
	var s stack
	var wg sync.WaitGroup
	errs := make(chan error, 3)

	wg.Go(func() {
		k, err := tckafka.Run(ctx, "confluentinc/confluent-local:8.2.4")
		testcontainers.CleanupContainer(t, k)
		if err != nil {
			errs <- fmt.Errorf("kafka: %w", err)
			return
		}
		brokers, err := k.Brokers(ctx)
		if err != nil {
			errs <- fmt.Errorf("kafka brokers: %w", err)
			return
		}
		s.brokers = brokers[0]
	})
	wg.Go(func() {
		r, err := testcontainers.Run(ctx, "quay.io/apicurio/apicurio-registry:3.3.3",
			testcontainers.WithExposedPorts("8080/tcp", "9000/tcp"),
			testcontainers.WithWaitStrategy(wait.ForHTTP("/health/ready").WithPort("9000/tcp").WithStartupTimeout(2*time.Minute)),
		)
		testcontainers.CleanupContainer(t, r)
		if err != nil {
			errs <- fmt.Errorf("registry: %w", err)
			return
		}
		endpoint, err := r.PortEndpoint(ctx, "8080/tcp", "http")
		if err != nil {
			errs <- fmt.Errorf("registry endpoint: %w", err)
			return
		}
		s.registryURL = endpoint + "/apis/ccompat/v7"
	})
	wg.Go(func() {
		c, err := tcclickhouse.Run(ctx, "clickhouse/clickhouse-server:26.8.28.14",
			tcclickhouse.WithDatabase("energy"), tcclickhouse.WithUsername("energy"), tcclickhouse.WithPassword("energy"))
		testcontainers.CleanupContainer(t, c)
		if err != nil {
			errs <- fmt.Errorf("clickhouse: %w", err)
			return
		}
		dsn, err := c.ConnectionString(ctx)
		if err != nil {
			errs <- fmt.Errorf("clickhouse dsn: %w", err)
			return
		}
		s.chDSN = dsn
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if err := chdb.Migrate(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), s.chDSN); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

// produce writes the change history of the scenario, including a redelivered snapshot. The load
// topic is created after the price events are delivered and beforeLoad, if set, has returned.
func produce(t *testing.T, s stack, beforeLoad func()) {
	t.Helper()
	client, err := schemaregistry.NewClient(schemaregistry.NewConfig(s.registryURL))
	if err != nil {
		t.Fatalf("registry client: %v", err)
	}
	for topic, amount := range map[string]string{priceTopic: "price_eur_mwh", loadTopic: "load_mw"} {
		info := schemaregistry.SchemaInfo{Schema: envelopeSchema(tableName(topic), amount), SchemaType: "AVRO"}
		if _, err := client.Register(topic+"-value", info, false); err != nil {
			t.Fatalf("register schema for %s: %v", topic, err)
		}
	}
	conf := avrov2.NewSerializerConfig()
	conf.AutoRegisterSchemas = false
	conf.UseLatestVersion = true
	ser, err := avrov2.NewSerializer(client, serde.ValueSerde, conf)
	if err != nil {
		t.Fatalf("serializer: %v", err)
	}
	p, err := kafka.NewProducer(&kafka.ConfigMap{"bootstrap.servers": s.brokers, "message.timeout.ms": 30_000})
	if err != nil {
		t.Fatalf("producer: %v", err)
	}
	defer p.Close()
	createTopics(t, p, priceTopic)

	deliveries := make(chan kafka.Event, 16)
	sent := 0
	send := func(topic, key string, msg any) {
		t.Helper()
		var value []byte
		if msg != nil {
			if value, err = ser.Serialize(topic, msg); err != nil {
				t.Fatalf("serialize: %v", err)
			}
		}
		tp := topic
		if err := p.Produce(&kafka.Message{TopicPartition: kafka.TopicPartition{Topic: &tp, Partition: kafka.PartitionAny}, Key: []byte(key), Value: value}, deliveries); err != nil {
			t.Fatalf("produce: %v", err)
		}
		sent++
	}

	const slotA, slotB = "2026-10-02T07:00:00.000000Z", "2026-10-02T10:45:00.000000Z"
	send(priceTopic, slotA, &priceEvent{After: price(slotA, "109.95"), Source: at(100), Op: "r"})
	send(priceTopic, slotB, &priceEvent{After: price(slotB, "-0.80"), Source: at(100), Op: "r"})
	send(priceTopic, slotA, &priceEvent{Before: price(slotA, "109.95"), After: price(slotA, "122.45"), Source: at(250), Op: "u"})
	send(priceTopic, slotB, &priceEvent{Before: price(slotB, "-0.80"), Source: at(300), Op: "d"})
	send(priceTopic, slotB, nil) // tombstone after the delete
	// At-least-once: the snapshot is delivered again after the changes.
	send(priceTopic, slotA, &priceEvent{After: price(slotA, "109.95"), Source: at(100), Op: "r"})
	send(priceTopic, slotB, &priceEvent{After: price(slotB, "-0.80"), Source: at(100), Op: "r"})
	awaitDeliveries := func() {
		t.Helper()
		for ; sent > 0; sent-- {
			if m := (<-deliveries).(*kafka.Message); m.TopicPartition.Error != nil {
				t.Fatalf("delivery to %s: %v", *m.TopicPartition.Topic, m.TopicPartition.Error)
			}
		}
	}
	awaitDeliveries()

	if beforeLoad != nil {
		beforeLoad()
	}
	createTopics(t, p, loadTopic)
	send(loadTopic, slotA, &loadEvent{After: &loadImage{"10YNL----------L", slotA, 900_000_000, "13663.500", "2026-10-03T06:14:52.000000Z"}, Source: at(110), Op: "r"})
	awaitDeliveries()
}

// createTopics creates the CDC topics up front: broker auto-creation is off in the compose stack
// too, where Connect creates them.
func createTopics(t *testing.T, p *kafka.Producer, topics ...string) {
	t.Helper()
	admin, err := kafka.NewAdminClientFromProducer(p)
	if err != nil {
		t.Fatalf("admin client: %v", err)
	}
	defer admin.Close()
	specs := make([]kafka.TopicSpecification, len(topics))
	for i, topic := range topics {
		specs[i] = kafka.TopicSpecification{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}
	}
	results, err := admin.CreateTopics(t.Context(), specs)
	if err != nil {
		t.Fatalf("create topics: %v", err)
	}
	for _, r := range results {
		if r.Error.Code() != kafka.ErrNoError {
			t.Fatalf("create topic %s: %v", r.Topic, r.Error)
		}
	}
}

func tableName(topic string) string { return topic[len("energy.public."):] }

// countingWriter counts the rows written through it.
type countingWriter struct {
	sink.Writer
	rows atomic.Int64
}

func (w *countingWriter) Write(ctx context.Context, rows []sink.Row) error {
	if err := w.Writer.Write(ctx, rows); err != nil {
		return err
	}
	w.rows.Add(int64(len(rows)))
	return nil
}

// crashBeforeCommit simulates a sink that dies after its first write: the commit never happens.
type crashBeforeCommit struct {
	sink.Consumer
	crash context.CancelFunc
}

func (c *crashBeforeCommit) Commit([]sink.Offset) error {
	c.crash()
	return errors.New("process killed before commit")
}

// runSink runs a sink of the given group until it has written wantRows rows, it stops on its own,
// or limit passes. whileRunning, if set, runs on the test goroutine once the sink has started.
func runSink(t *testing.T, s stack, group string, wrapConsumer func(sink.Consumer, context.CancelFunc) sink.Consumer,
	whileRunning func(), wantRows int64, limit time.Duration) *countingWriter {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	metrics, err := sink.NewMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	decoder, err := sink.NewAvroDecoder(s.registryURL)
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	chWriter, err := sink.NewClickHouseWriter(t.Context(), s.chDSN)
	if err != nil {
		t.Fatalf("writer: %v", err)
	}
	defer func() { _ = chWriter.Close() }()
	writer := &countingWriter{Writer: chWriter}

	snk := &sink.Sink{Decoder: decoder, Writer: writer, Metrics: metrics, Logger: logger,
		BatchSize: 100, FlushInterval: 500 * time.Millisecond, RetryBase: 100 * time.Millisecond, RetryMax: time.Second}
	consumer, err := sink.NewKafkaConsumer(s.brokers, group, []string{priceTopic, loadTopic}, metrics, logger, snk.DropBatch)
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	defer func() { _ = consumer.Close() }()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	snk.Consumer = consumer
	if wrapConsumer != nil {
		snk.Consumer = wrapConsumer(consumer, cancel)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- snk.Run(ctx, context.WithoutCancel(t.Context())) }()
	deadline := time.Now().Add(limit)
	if whileRunning != nil {
		whileRunning()
	}
	for ctx.Err() == nil && (wantRows == 0 || writer.rows.Load() < wantRows) && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("sink: %v", err)
	}
	return writer
}

// state returns the current state of both tables as ClickHouse reads it with FINAL.
func state(t *testing.T, dsn string) map[string]string {
	t.Helper()
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = conn.Close() }()
	rows, err := conn.Query(t.Context(), `
		SELECT 'price ' || toString(delivery_start), toString(price_eur_mwh) FROM day_ahead_price FINAL
		UNION ALL
		SELECT 'load ' || toString(delivery_start), toString(load_mw) FROM actual_load FINAL`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	got := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[k] = v
	}
	return got
}

var wantState = map[string]string{
	"price 2026-10-02 07:00:00.000": "122.45",  // revision wins over the replayed snapshot
	"load 2026-10-02 07:00:00.000":  "13663.5", // toString(Decimal) drops trailing zeros; slot B of the prices is deleted
}

func sameState(got map[string]string) bool {
	if len(got) != len(wantState) {
		return false
	}
	for k, v := range wantState {
		if got[k] != v {
			return false
		}
	}
	return true
}

// The scenarios of ADR 0026 against real Kafka, Apicurio and ClickHouse.
func TestSinkEndToEnd(t *testing.T) {
	s := startStack(t)
	produce(t, s, nil)
	const events = 7 // all records except the tombstone carry a row

	t.Run("loads the current state and commits", func(t *testing.T) {
		w := runSink(t, s, "first", nil, nil, events, 90*time.Second)
		if w.rows.Load() != events {
			t.Fatalf("wrote %d rows, want %d", w.rows.Load(), events)
		}
		if got := state(t, s.chDSN); !sameState(got) {
			t.Errorf("state %v, want %v", got, wantState)
		}
	})

	t.Run("a restart of the same group reads nothing again", func(t *testing.T) {
		// The first run committed everything it wrote, including on shutdown.
		if w := runSink(t, s, "first", nil, nil, 0, 15*time.Second); w.rows.Load() != 0 {
			t.Errorf("restart wrote %d rows, want 0", w.rows.Load())
		}
	})

	t.Run("killed between write and commit, the redelivery collapses", func(t *testing.T) {
		crashed := runSink(t, s, "second",
			func(c sink.Consumer, cancel context.CancelFunc) sink.Consumer {
				return &crashBeforeCommit{Consumer: c, crash: cancel}
			},
			nil, 0, 90*time.Second)
		if crashed.rows.Load() == 0 {
			t.Fatal("the crashing sink wrote nothing")
		}

		// Nothing was committed for "second", so a restart gets every record again.
		w := runSink(t, s, "second", nil, nil, events, 90*time.Second)
		if w.rows.Load() != events {
			t.Errorf("restart wrote %d rows, want all %d redelivered", w.rows.Load(), events)
		}
		if got := state(t, s.chDSN); !sameState(got) {
			t.Errorf("state after redelivery %v, want %v", got, wantState)
		}
	})
}

// On a fresh stack the sink subscribes before Debezium creates the topics, and they appear one by
// one. librdkafka finds the first quickly, but a topic created after the first assignment waits for
// the next periodic metadata refresh, 5 minutes by default; the sink must pick it up in seconds.
func TestSinkPicksUpTopicsCreatedAfterSubscribing(t *testing.T) {
	s := startStack(t)
	const events = 7
	var loadCreated time.Time
	w := runSink(t, s, "late", nil, func() {
		time.Sleep(3 * time.Second) // the sink subscribes to topics that do not exist yet
		produce(t, s, func() {
			// Create the load topic only once the sink consumes prices, i.e. after its first assignment.
			deadline := time.Now().Add(60 * time.Second)
			for state(t, s.chDSN)["price 2026-10-02 07:00:00.000"] != "122.45" {
				if time.Now().After(deadline) {
					t.Fatal("the sink did not consume the price topic")
				}
				time.Sleep(500 * time.Millisecond)
			}
		})
		loadCreated = time.Now()
	}, events, 2*time.Minute)

	if w.rows.Load() != events {
		t.Fatalf("wrote %d rows, want %d; the load topic was not picked up within %s",
			w.rows.Load(), events, time.Since(loadCreated).Round(time.Second))
	}
	if took := time.Since(loadCreated); took > 30*time.Second {
		t.Errorf("the load topic was picked up %s after it appeared, want under 30s", took.Round(time.Second))
	}
	if got := state(t, s.chDSN); !sameState(got) {
		t.Errorf("state %v, want %v", got, wantState)
	}
}
