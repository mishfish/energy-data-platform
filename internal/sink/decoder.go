package sink

import (
	"fmt"
	"strings"

	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"
	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry/serde"
	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry/serde/avrov2"
)

// AvroDecoder decodes Avro change events in Confluent wire format, fetching writer schemas and
// their references from the registry's Confluent-compatible API (ADR 0025, 0028).
type AvroDecoder struct {
	deserializer *avrov2.Deserializer
}

// NewAvroDecoder returns a decoder for the registry at registryURL, e.g.
// http://apicurio:8080/apis/ccompat/v7. Schemas are cached after the first fetch.
func NewAvroDecoder(registryURL string) (*AvroDecoder, error) {
	client, err := schemaregistry.NewClient(schemaregistry.NewConfig(registryURL))
	if err != nil {
		return nil, fmt.Errorf("schema registry client: %w", err)
	}
	d, err := avrov2.NewDeserializer(client, serde.ValueSerde, avrov2.NewDeserializerConfig())
	if err != nil {
		return nil, fmt.Errorf("avro deserializer: %w", err)
	}
	return &AvroDecoder{deserializer: d}, nil
}

// Decode returns the row carried by r, or errSkip for a tombstone.
func (d *AvroDecoder) Decode(r Record) (Row, error) {
	if r.Value == nil {
		return Row{}, errSkip
	}
	var ev envelope
	if err := d.deserializer.DeserializeInto(r.Topic, r.Value, &ev); err != nil {
		return Row{}, fmt.Errorf("decode avro: %w", err)
	}
	return toRow(tableOf(r.Topic), ev)
}

// tableOf maps a Debezium topic (<prefix>.<schema>.<table>) to its table name.
func tableOf(topic string) string {
	return topic[strings.LastIndex(topic, ".")+1:]
}
