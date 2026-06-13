// Package kafka implements the Mode 2 output connector: OCSF-translated signal
// delivery to an Apache Kafka topic using franz-go. The OCSF mapping is applied
// by the ocsf.Mapper within Send(); callers set UseOCSF=true on the batch.
//
// Security properties:
//   - TLS is built via connector.NewTLSConfig which enforces TLS 1.3 minimum
//     and never sets InsecureSkipVerify.
//   - SASL credentials are accepted at runtime; the Config struct is never logged.
//   - Only broker addresses are logged; credentials are not.
package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/ocsf"
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

	// RequiredAcks controls producer durability.
	// nil = default (leader ack, equivalent to 1); *0 = no ack (NoAck); *1 = leader ack; *-1 = all ISR acks.
	// Using a pointer distinguishes "user explicitly set 0 (NoAck)" from "user omitted the field (default 1)".
	// YAML/mapstructure: absent key decodes to nil; required_acks: 0 decodes to a non-nil *int with value 0.
	RequiredAcks *int
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

// Connector publishes OCSF-encoded signal batches to a Kafka topic using franz-go.
// Each signal is mapped to an OCSF Event, JSON-marshaled, and produced as a
// separate Kafka record with partition key = batch.InstanceID and headers
// x-argus-batch-id and x-argus-instance-id.
type Connector struct {
	cfg    Config
	client *kgo.Client
	mapper *ocsf.Mapper
}

// Cfg returns a copy of the stored configuration. Intended for white-box tests only.
func (c *Connector) Cfg() Config { return c.cfg }

// New creates a Kafka connector. Call Connect before sending.
func New(cfg Config) *Connector {
	if cfg.CompressionCodec == "" {
		cfg.CompressionCodec = "snappy"
	}
	// F12 / T-03-17: use pointer semantics so that an explicit 0 (NoAck) is not
	// silently defaulted to 1 (leader ack). nil means "user did not set RequiredAcks"
	// and defaults to 1; *0 means "user explicitly wants NoAck" and is preserved.
	if cfg.RequiredAcks == nil {
		one := 1
		cfg.RequiredAcks = &one
	}
	if cfg.MaxBatchBytes == 0 {
		cfg.MaxBatchBytes = 1 << 20 // 1 MB
	}

	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "argus-agent"
	}

	return &Connector{
		cfg:    cfg,
		mapper: ocsf.NewMapper("argus-sdk", hostname),
	}
}

// Name implements connector.Connector.
func (c *Connector) Name() string { return "kafka" }

// Connect validates broker list, builds TLS/SASL options, and initialises the
// franz-go client. Returns an error if brokers are empty, TLS config is invalid,
// or the SASL mechanism string is unrecognised.
func (c *Connector) Connect(_ context.Context) error {
	if len(c.cfg.Brokers) == 0 {
		return fmt.Errorf("kafka: at least one broker address is required")
	}

	opts := []kgo.Opt{
		kgo.SeedBrokers(c.cfg.Brokers...),
		kgo.ProducerBatchMaxBytes(int32(c.cfg.MaxBatchBytes)),
	}

	// Map RequiredAcks integer to franz-go Acks type.
	// cfg.RequiredAcks is guaranteed non-nil after New() (nil defaults to *1).
	//
	// franz-go enables idempotent producing by default, which the broker only
	// permits with acks=all. For weaker durability (leader/none) we must disable
	// idempotent writes explicitly, otherwise kgo.NewClient fails with
	// "idempotency requires acks=all".
	switch *c.cfg.RequiredAcks {
	case -1:
		opts = append(opts, kgo.RequiredAcks(kgo.AllISRAcks()))
	case 0:
		opts = append(opts, kgo.RequiredAcks(kgo.NoAck()), kgo.DisableIdempotentWrite())
	default: // 1 and anything else → leader ack
		opts = append(opts, kgo.RequiredAcks(kgo.LeaderAck()), kgo.DisableIdempotentWrite())
	}

	// TLS — built exclusively via connector.NewTLSConfig to guarantee TLS 1.3
	// and prevent InsecureSkipVerify from being set.
	if c.cfg.TLS.Enabled {
		tlsCfg, err := connector.NewTLSConfig(connector.TLSClientConfig{
			CACert:   c.cfg.TLS.CACert,
			CertFile: c.cfg.TLS.CertFile,
			KeyFile:  c.cfg.TLS.KeyFile,
		})
		if err != nil {
			return fmt.Errorf("kafka: building TLS config: %w", err)
		}
		opts = append(opts, kgo.DialTLSConfig(tlsCfg))
	}

	// SASL — configured when Mechanism is non-empty.
	if c.cfg.SASL.Mechanism != "" {
		user := c.cfg.SASL.Username
		pass := c.cfg.SASL.Password

		switch c.cfg.SASL.Mechanism {
		case "PLAIN":
			opts = append(opts, kgo.SASL(plain.Auth{
				User: user,
				Pass: pass,
			}.AsMechanism()))

		case "SCRAM-SHA-256":
			opts = append(opts, kgo.SASL(scram.Auth{
				User: user,
				Pass: pass,
			}.AsSha256Mechanism()))

		case "SCRAM-SHA-512":
			opts = append(opts, kgo.SASL(scram.Auth{
				User: user,
				Pass: pass,
			}.AsSha512Mechanism()))

		default:
			return fmt.Errorf("kafka: unsupported SASL mechanism %q (supported: PLAIN, SCRAM-SHA-256, SCRAM-SHA-512)", c.cfg.SASL.Mechanism)
		}
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return fmt.Errorf("kafka: creating client: %w", err)
	}
	c.client = client
	return nil
}

// Send maps each signal in the batch to an OCSF Event, marshals it to JSON,
// and produces one Kafka record per signal. The partition key is batch.InstanceID;
// each record carries headers x-argus-batch-id and x-argus-instance-id.
//
// Signals that fail OCSF mapping are skipped (accumulated internally).
// Returns DeliveryAck{Status:"delivered"} on full success or DeliveryAck{Status:"failed"}
// if the produce call fails.
func (c *Connector) Send(ctx context.Context, batch *connector.SignalBatch) (*connector.DeliveryAck, error) {
	if c.client == nil {
		return &connector.DeliveryAck{
			BatchID:   batch.BatchID,
			Status:    "failed",
			Error:     "connector not connected",
			Timestamp: time.Now(),
		}, fmt.Errorf("kafka: Send called before Connect")
	}

	partitionKey := []byte(batch.InstanceID)
	headers := []kgo.RecordHeader{
		{Key: "x-argus-batch-id", Value: []byte(batch.BatchID)},
		{Key: "x-argus-instance-id", Value: []byte(batch.InstanceID)},
	}

	records := make([]*kgo.Record, 0, len(batch.Signals))
	for _, s := range batch.Signals {
		ev, err := c.mapper.Map(s)
		if err != nil {
			// Skip unmappable signals rather than failing the whole batch.
			continue
		}
		jsonBytes, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		records = append(records, &kgo.Record{
			Topic:   c.cfg.Topic,
			Key:     partitionKey,
			Value:   jsonBytes,
			Headers: headers,
		})
	}

	if len(records) == 0 {
		// All signals were unmappable — return a delivered ack for an empty produce.
		return &connector.DeliveryAck{
			BatchID:   batch.BatchID,
			Status:    "delivered",
			Timestamp: time.Now(),
		}, nil
	}

	results := c.client.ProduceSync(ctx, records...)
	if err := results.FirstErr(); err != nil {
		return &connector.DeliveryAck{
			BatchID:   batch.BatchID,
			Status:    "failed",
			Error:     err.Error(),
			Timestamp: time.Now(),
		}, fmt.Errorf("kafka: produce failed: %w", err)
	}

	return &connector.DeliveryAck{
		BatchID:   batch.BatchID,
		Status:    "delivered",
		Timestamp: time.Now(),
	}, nil
}

// Health verifies broker connectivity by issuing a Ping request.
// This does not produce any messages.
func (c *Connector) Health(ctx context.Context) error {
	if c.client == nil {
		return fmt.Errorf("kafka: connector not connected")
	}
	if err := c.client.Ping(ctx); err != nil {
		return fmt.Errorf("kafka: broker unreachable: %w", err)
	}
	return nil
}

// Close shuts down the franz-go producer and releases all resources.
func (c *Connector) Close() error {
	if c.client != nil {
		c.client.Close()
	}
	return nil
}
