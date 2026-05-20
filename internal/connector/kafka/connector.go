// Package kafka implements the Mode 2 output connector: OCSF-translated signal
// delivery to an Apache Kafka topic. The OCSF mapping is applied by the
// internal/ocsf package before the batch reaches this connector.
package kafka

import (
	"context"
	"fmt"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
)

// Config holds Kafka connection and topic parameters.
type Config struct {
	// Brokers is a list of Kafka broker addresses (host:port).
	Brokers []string

	// Topic is the destination topic for OCSF-encoded signal events.
	Topic string

	// TLS configures transport security.
	TLS TLSConfig

	// SASL configures authentication (PLAIN, SCRAM-SHA-256, SCRAM-SHA-512).
	SASL SASLConfig

	// MaxBatchBytes caps the uncompressed size of a Kafka produce request. Default: 1MB.
	MaxBatchBytes int

	// CompressionCodec is "none" | "gzip" | "snappy" | "lz4" | "zstd". Default: "snappy".
	CompressionCodec string

	// RequiredAcks controls producer durability: 0=none, 1=leader, -1=all. Default: 1.
	RequiredAcks int
}

// TLSConfig controls TLS for Kafka.
type TLSConfig struct {
	Enabled  bool
	CACert   string
	CertFile string
	KeyFile  string
}

// SASLConfig controls SASL authentication for Kafka.
type SASLConfig struct {
	Mechanism string // "PLAIN" | "SCRAM-SHA-256" | "SCRAM-SHA-512"
	Username  string
	Password  string
}

// Connector publishes OCSF-encoded signal batches to a Kafka topic.
//
// Implementation TODO (not in this scaffold):
//   - Use github.com/segmentio/kafka-go or github.com/confluentinc/confluent-kafka-go
//   - JSON-encode the OCSF events (already translated before Send is called)
//   - Partition key: batch.InstanceID for ordered per-agent delivery
//   - Record headers: x-argus-batch-id, x-argus-instance-id
type Connector struct {
	cfg Config
	// writer *kafka.Writer  // wired in during implementation
}

// New creates a Kafka connector. Call Connect before sending.
func New(cfg Config) *Connector {
	if cfg.CompressionCodec == "" {
		cfg.CompressionCodec = "snappy"
	}
	if cfg.RequiredAcks == 0 {
		cfg.RequiredAcks = 1
	}
	if cfg.MaxBatchBytes == 0 {
		cfg.MaxBatchBytes = 1 << 20 // 1 MB
	}
	return &Connector{cfg: cfg}
}

// Name implements connector.Connector.
func (c *Connector) Name() string { return "kafka" }

// Connect validates broker reachability and creates the producer.
func (c *Connector) Connect(_ context.Context) error {
	if len(c.cfg.Brokers) == 0 {
		return fmt.Errorf("kafka: at least one broker is required")
	}
	// TODO: create kafka.Writer, dial brokers, validate topic exists
	return nil
}

// Send publishes each OCSF-encoded event as a separate Kafka message.
// batch.UseOCSF must be true; the ocsf.Mapper has already translated signals.
func (c *Connector) Send(_ context.Context, batch *connector.SignalBatch) (*connector.DeliveryAck, error) {
	// TODO: marshal each signal as OCSF JSON, produce to c.cfg.Topic
	return &connector.DeliveryAck{BatchID: batch.BatchID, Status: "delivered"}, nil
}

// Health checks broker connectivity.
func (c *Connector) Health(_ context.Context) error {
	// TODO: dial one broker, issue metadata request
	return nil
}

// Close flushes pending messages and closes the producer.
func (c *Connector) Close() error {
	// TODO: writer.Close()
	return nil
}
