//go:build integration

// Package kafka_test provides real-infrastructure integration tests for the Kafka connector.
//
// These tests start a Kafka container via testcontainers-go (confluentinc/confluent-local).
// They are gated behind the `integration` build tag and skip cleanly when Docker is
// unavailable so the default unit suite stays fast and Docker-free.
//
// Run with:
//
//	go test -tags=integration ./internal/connector/kafka/ -v -timeout 300s
//
// CI runs this via `make test-int`.
package kafka_test

import (
	"context"
	"errors"
	"testing"
	"time"

	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/kafka"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
	tc "github.com/testcontainers/testcontainers-go"
)

// createTopic pre-provisions a topic on the test broker. The connector does not
// auto-create topics (production topics are provisioned by operators), so the
// integration test must create the topic it produces to.
func createTopic(ctx context.Context, t *testing.T, brokers []string, topic string) {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		t.Fatalf("admin client: %v", err)
	}
	defer cl.Close()

	adm := kadm.NewClient(cl)
	resp, err := adm.CreateTopics(ctx, 1, 1, nil, topic)
	if err != nil {
		t.Fatalf("create topic request: %v", err)
	}
	for _, r := range resp {
		if r.Err != nil && !errors.Is(r.Err, kerr.TopicAlreadyExists) {
			t.Fatalf("create topic %q: %v", r.Topic, r.Err)
		}
	}
}

// TestKafkaConnector_DeliverMultiSignalBatch starts a Kafka container, connects a
// kafka.Connector, and sends a multi-signal SignalBatch with UseOCSF=true.
// Asserts DeliveryAck.Status=="delivered" (SC-9).
func TestKafkaConnector_DeliverMultiSignalBatch(t *testing.T) {
	// Skip cleanly if Docker is unavailable — never a hard failure without infra.
	tc.SkipIfProviderIsNotHealthy(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Start Kafka container using KRaft mode (no Zookeeper).
	kc, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.5.0")
	if err != nil {
		t.Skipf("kafka container start failed (Docker may be unavailable): %v", err)
	}
	defer func() {
		if tErr := kc.Terminate(ctx); tErr != nil {
			t.Logf("kafka container terminate: %v", tErr)
		}
	}()

	brokers, err := kc.Brokers(ctx)
	if err != nil {
		t.Fatalf("kafka brokers: %v", err)
	}
	t.Logf("kafka brokers: %v", brokers)

	const topic = "argus-integration-test"

	// Provision the topic before producing (the connector never auto-creates).
	createTopic(ctx, t, brokers, topic)

	cfg := kafka.Config{
		Brokers: brokers,
		Topic:   topic,
	}
	c := kafka.New(cfg)

	connectCtx, connectCancel := context.WithTimeout(ctx, 30*time.Second)
	defer connectCancel()
	if err := c.Connect(connectCtx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	// Build a multi-signal batch with UseOCSF=true.
	now := time.Now()
	batch := &connector.SignalBatch{
		BatchID:    "inttest-kafka-001",
		InstanceID: "test-instance",
		GroupID:    "test-group",
		ReceivedAt: now,
		UseOCSF:    true,
		Signals: []signal.Signal{
			{
				SignalID:   "sig-1",
				Layer:      signal.L7RAGRetrieval,
				Category:   "retrieval.search",
				Severity:   signal.SeverityMedium,
				AppID:      "integration-test-app",
				AppVersion: "1.0.0",
				SDKVersion: "0.1.0",
				Env:        "test",
				Timestamp:  now,
			},
			{
				SignalID:   "sig-2",
				Layer:      signal.L8Agents,
				Category:   "agent.tool_call",
				Severity:   signal.SeverityHigh,
				AppID:      "integration-test-app",
				AppVersion: "1.0.0",
				SDKVersion: "0.1.0",
				Env:        "test",
				Timestamp:  now,
			},
			{
				SignalID:   "sig-3",
				Layer:      signal.L10Application,
				Category:   "llm.completion",
				Severity:   signal.SeverityInfo,
				AppID:      "integration-test-app",
				AppVersion: "1.0.0",
				SDKVersion: "0.1.0",
				Env:        "test",
				Timestamp:  now,
			},
		},
	}

	sendCtx, sendCancel := context.WithTimeout(ctx, 60*time.Second)
	defer sendCancel()
	ack, err := c.Send(sendCtx, batch)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if ack == nil {
		t.Fatal("Send returned nil ack")
	}
	if ack.Status != "delivered" {
		t.Errorf("expected ack.Status==%q, got %q (error: %s)", "delivered", ack.Status, ack.Error)
	}
	t.Logf("kafka ack: batch_id=%s status=%s", ack.BatchID, ack.Status)
}

// TestKafkaConnector_FailedDelivery_ClosedConnector exercises the error path:
// Send on a connector that was never connected must return a failed ack and non-nil error
// (Phase 3 delivery contract — F6).
func TestKafkaConnector_FailedDelivery_ClosedConnector(t *testing.T) {
	// This test does NOT require Docker — it exercises the connector's pre-Connect guard.
	// The integration build tag is present so it runs alongside the Docker tests but
	// does not need a running container.
	cfg := kafka.Config{
		Brokers: []string{"localhost:9999"},
		Topic:   "nonexistent",
	}
	c := kafka.New(cfg)
	// Intentionally do NOT call Connect.

	batch := &connector.SignalBatch{
		BatchID:    "inttest-kafka-fail-001",
		InstanceID: "test-instance",
		GroupID:    "test-group",
		ReceivedAt: time.Now(),
		UseOCSF:    true,
		Signals:    []signal.Signal{{SignalID: "sig-fail", Timestamp: time.Now()}},
	}

	ack, err := c.Send(context.Background(), batch)
	if err == nil {
		t.Error("expected non-nil error from Send on unconnected connector")
	}
	if ack == nil {
		t.Fatal("expected non-nil ack even on failure")
	}
	if ack.Status != "failed" {
		t.Errorf("expected ack.Status==%q, got %q", "failed", ack.Status)
	}
	t.Logf("kafka failure ack: status=%s error=%s", ack.Status, ack.Error)
}
