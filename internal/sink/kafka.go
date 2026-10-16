package sink

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

// statsInterval is how often librdkafka reports statistics, which carry the consumer lag.
const statsInterval = 15 * time.Second

// KafkaConsumer adapts a confluent-kafka-go consumer to the Consumer interface. Offsets are
// committed explicitly only (enable.auto.commit=false); auto offset store is off as well, so
// librdkafka never commits on its own, not even on close.
type KafkaConsumer struct {
	consumer *kafka.Consumer
	metrics  *Metrics
	logger   *slog.Logger
	onRevoke func()
}

// NewKafkaConsumer subscribes the group to topics. onRevoke runs, on the polling goroutine, when
// partitions are taken away, before the next owner can start consuming them.
func NewKafkaConsumer(brokers, groupID string, topics []string, metrics *Metrics, logger *slog.Logger, onRevoke func()) (*KafkaConsumer, error) {
	c, err := kafka.NewConsumer(&kafka.ConfigMap{
		"bootstrap.servers":        brokers,
		"group.id":                 groupID,
		"enable.auto.commit":       false,
		"enable.auto.offset.store": false,
		// A new group starts from the beginning: the topics hold the snapshot.
		"auto.offset.reset":      "earliest",
		"statistics.interval.ms": int(statsInterval / time.Millisecond),
	})
	if err != nil {
		return nil, fmt.Errorf("create consumer: %w", err)
	}
	kc := &KafkaConsumer{consumer: c, metrics: metrics, logger: logger, onRevoke: onRevoke}
	if err := c.SubscribeTopics(topics, kc.rebalance); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("subscribe to %v: %w", topics, err)
	}
	return kc, nil
}

func (kc *KafkaConsumer) rebalance(_ *kafka.Consumer, ev kafka.Event) error {
	switch e := ev.(type) {
	case kafka.AssignedPartitions:
		kc.logger.Info("partitions assigned", "partitions", fmt.Sprint(e.Partitions))
	case kafka.RevokedPartitions:
		kc.logger.Info("partitions revoked", "partitions", fmt.Sprint(e.Partitions))
		kc.onRevoke()
	}
	return nil
}

// Poll implements Consumer.
func (kc *KafkaConsumer) Poll(timeout time.Duration) (*Record, error) {
	switch e := kc.consumer.Poll(int(timeout / time.Millisecond)).(type) {
	case *kafka.Message:
		if e.TopicPartition.Error != nil {
			return nil, fmt.Errorf("message error: %w", e.TopicPartition.Error)
		}
		return &Record{
			Topic:     *e.TopicPartition.Topic,
			Partition: e.TopicPartition.Partition,
			Offset:    int64(e.TopicPartition.Offset),
			Value:     e.Value,
		}, nil
	case kafka.Error:
		if e.IsFatal() {
			return nil, fmt.Errorf("fatal consumer error: %w", e)
		}
		// Broker down, connection reset and the like: librdkafka retries on its own.
		kc.logger.Warn("consumer error", "err", e)
	case *kafka.Stats:
		if err := kc.metrics.ObserveStats(e.String()); err != nil {
			kc.logger.Warn("ignoring librdkafka stats", "err", err)
		}
	}
	return nil, nil
}

// Commit implements Consumer.
func (kc *KafkaConsumer) Commit(offsets []Offset) error {
	tps := make([]kafka.TopicPartition, len(offsets))
	for i, o := range offsets {
		topic := o.Topic
		tps[i] = kafka.TopicPartition{Topic: &topic, Partition: o.Partition, Offset: kafka.Offset(o.Next)}
	}
	committed, err := kc.consumer.CommitOffsets(tps)
	if err != nil {
		return fmt.Errorf("commit offsets: %w", err)
	}
	for _, tp := range committed {
		if tp.Error != nil {
			return fmt.Errorf("commit %s[%d]: %w", *tp.Topic, tp.Partition, tp.Error)
		}
	}
	return nil
}

// Close leaves the group. Uncommitted records stay uncommitted.
func (kc *KafkaConsumer) Close() error { return kc.consumer.Close() }
