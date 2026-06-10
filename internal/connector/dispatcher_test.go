package connector

// dispatcher_test.go — tests for Dispatcher counter wiring (F6, plan 03-02).
// Uses package-internal access (same package) to access unexported types.

import (
	"context"
	"testing"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
	"go.uber.org/zap"
)

// fakeConnector is a minimal Connector implementation for testing.
type fakeConnector struct {
	name    string
	succeed bool // true => Send returns delivered ack; false => failed ack + error
}

func (f *fakeConnector) Name() string  { return f.name }
func (f *fakeConnector) Close() error  { return nil }
func (f *fakeConnector) Connect(_ context.Context) error { return nil }
func (f *fakeConnector) Health(_ context.Context) error  { return nil }

func (f *fakeConnector) Send(_ context.Context, batch *SignalBatch) (*DeliveryAck, error) {
	if f.succeed {
		return &DeliveryAck{BatchID: batch.BatchID, Status: "delivered", Timestamp: time.Now()}, nil
	}
	return &DeliveryAck{BatchID: batch.BatchID, Status: "failed", Error: "fake failure", Timestamp: time.Now()},
		context.DeadlineExceeded // any non-nil error
}

// TestDispatcher_CountersWired (F6): registry with two fake connectors — one that
// always succeeds and one that always fails. Enqueue one job targeting both; after
// processing (poll Stats with timeout), Stats() must return accepted==1, delivered==1, failed==1.
func TestDispatcher_CountersWired(t *testing.T) {
	logger := zap.NewNop()
	reg := NewConnectorRegistry(logger)

	good := &fakeConnector{name: "good", succeed: true}
	bad := &fakeConnector{name: "bad", succeed: false}

	if err := reg.Register(good); err != nil {
		t.Fatalf("Register good: %v", err)
	}
	if err := reg.Register(bad); err != nil {
		t.Fatalf("Register bad: %v", err)
	}

	cfg := &DispatchConfig{
		WorkerCount:     1,
		QueueCapacity:   10,
		SendTimeout:     5 * time.Second,
		ShutdownTimeout: 5 * time.Second,
	}
	d, err := NewDispatcher(cfg, reg, logger)
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	defer func() { _ = d.Close() }()

	batch := &SignalBatch{
		BatchID:  "test-batch-001",
		Signals:  []signal.Signal{{SignalID: "s1"}},
		ReceivedAt: time.Now(),
	}
	job := &DispatchJob{
		Batch:   batch,
		Targets: []string{"good", "bad"},
	}

	if err := d.Enqueue(job); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Poll Stats() until accepted==1 and delivered+failed==2 (or timeout).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stats := d.Stats()
		if stats["accepted"] == 1 && stats["delivered"] == 1 && stats["failed"] == 1 {
			return // test passes
		}
		time.Sleep(10 * time.Millisecond)
	}

	stats := d.Stats()
	t.Errorf("Stats() after processing = %v, want accepted=1 delivered=1 failed=1", stats)
}

// TestDispatcher_AcceptedNotIncrementedOnFullQueue: fill a capacity-2 queue, then
// a third Enqueue must fail and accepted must stay at 2 (not 3).
func TestDispatcher_AcceptedNotIncrementedOnFullQueue(t *testing.T) {
	logger := zap.NewNop()
	reg := NewConnectorRegistry(logger)

	// Use a connector that blocks Send so the queue stays full.
	blocker := &blockingConnector{name: "blocker", block: make(chan struct{})}
	if err := reg.Register(blocker); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// WorkerCount=0 means no workers drain the queue — but Dispatcher requires at least 1.
	// Use WorkerCount=1 with a blocking Send so the in-flight job occupies the worker
	// and the remaining queue capacity fills up.
	cfg := &DispatchConfig{
		WorkerCount:     1,
		QueueCapacity:   2,
		SendTimeout:     10 * time.Second,
		ShutdownTimeout: 5 * time.Second,
	}
	d, err := NewDispatcher(cfg, reg, logger)
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	defer func() {
		close(blocker.block) // unblock so shutdown drains
		_ = d.Close()
	}()

	batch := func() *SignalBatch {
		return &SignalBatch{BatchID: "b", Signals: []signal.Signal{{SignalID: "s"}}, ReceivedAt: time.Now()}
	}

	// Enqueue job 1 — worker picks it up and blocks; accepted becomes 1.
	if err := d.Enqueue(&DispatchJob{Batch: batch(), Targets: []string{"blocker"}}); err != nil {
		t.Fatalf("job1 Enqueue failed: %v", err)
	}

	// Wait for the worker to dequeue job 1 and block in Send.
	time.Sleep(50 * time.Millisecond)

	// Fill the remaining queue (capacity=2; worker holds 1; 2 slots now free).
	if err := d.Enqueue(&DispatchJob{Batch: batch(), Targets: []string{"blocker"}}); err != nil {
		t.Fatalf("job2 Enqueue failed: %v", err)
	}
	if err := d.Enqueue(&DispatchJob{Batch: batch(), Targets: []string{"blocker"}}); err != nil {
		t.Fatalf("job3 Enqueue failed: %v", err)
	}

	// Queue is now full; this enqueue must fail.
	errFull := d.Enqueue(&DispatchJob{Batch: batch(), Targets: []string{"blocker"}})
	if errFull == nil {
		t.Error("Enqueue on full queue should return error, got nil")
	}

	// accepted must be 3 (only the 3 successful enqueues); failed enqueue must not increment.
	stats := d.Stats()
	if stats["accepted"] != 3 {
		t.Errorf("Stats()[accepted] = %d, want 3 (failed enqueue must not increment accepted)", stats["accepted"])
	}
}

// blockingConnector is a Connector whose Send blocks until the block channel is closed.
type blockingConnector struct {
	name  string
	block chan struct{}
}

func (b *blockingConnector) Name() string  { return b.name }
func (b *blockingConnector) Close() error  { return nil }
func (b *blockingConnector) Connect(_ context.Context) error { return nil }
func (b *blockingConnector) Health(_ context.Context) error  { return nil }

func (b *blockingConnector) Send(ctx context.Context, batch *SignalBatch) (*DeliveryAck, error) {
	select {
	case <-b.block:
	case <-ctx.Done():
	}
	return &DeliveryAck{BatchID: batch.BatchID, Status: "delivered", Timestamp: time.Now()}, nil
}
