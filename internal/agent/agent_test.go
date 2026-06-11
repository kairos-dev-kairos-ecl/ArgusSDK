package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/buffer"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
	"go.uber.org/zap"
)

// fakeConnector is a test double that records every SignalBatch it receives.
type fakeConnector struct {
	name    string
	mu      sync.Mutex
	batches []*connector.SignalBatch
	// connectCalled is set to true when Connect is called.
	connectCalled bool
}

func newFakeConnector(name string) *fakeConnector {
	return &fakeConnector{name: name}
}

func (f *fakeConnector) Name() string { return f.name }
func (f *fakeConnector) Connect(_ context.Context) error {
	f.mu.Lock()
	f.connectCalled = true
	f.mu.Unlock()
	return nil
}
func (f *fakeConnector) Send(_ context.Context, batch *connector.SignalBatch) (*connector.DeliveryAck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Make a copy so the caller cannot mutate it after Send returns.
	cp := *batch
	f.batches = append(f.batches, &cp)
	return &connector.DeliveryAck{BatchID: batch.BatchID, Status: "delivered"}, nil
}
func (f *fakeConnector) Health(_ context.Context) error { return nil }
func (f *fakeConnector) Close() error                   { return nil }

func (f *fakeConnector) Received() []*connector.SignalBatch {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*connector.SignalBatch, len(f.batches))
	copy(out, f.batches)
	return out
}

// bufferDir returns a temp dir suitable for the WAL buffer.
func bufferDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// minimalConfig returns a Config with all required fields set for testing.
// Outputs and Buffer must be overridden by each test.
func minimalConfig(t *testing.T) *Config {
	t.Helper()
	return &Config{
		Agent: AgentConfig{GroupID: "test-group"},
		Auth:  AuthConfig{InstanceID: "test-instance"},
		Buffer: buffer.Config{
			Dir:           bufferDir(t),
			MaxSizeMB:     16,
			FlushInterval: 50 * time.Millisecond,
		},
	}
}

// TestStart_OCSFRouting verifies that the ingest loop routes signal batches to
// the correct per-OCSF-group connectors and propagates AppID/Env/BatchID.
//
// Setup: two fake connectors injected directly into a.registry —
//   - "xdr"  registered as non-OCSF (a.nonOCSFTargets)
//   - "ocsf" registered as OCSF     (a.ocsfTargets)
//
// A single signal.Batch is fed via a.ingestCh.
// Assertions:
//   - xdr fake received one batch with UseOCSF=false
//   - ocsf fake received one batch with UseOCSF=true
//   - Both batches have AppID="myapp", Env="prod", non-empty BatchID
func TestStart_OCSFRouting(t *testing.T) {
	cfg := minimalConfig(t)
	logger := zap.NewNop()

	a, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Build the registry and partition manually (test seam — bypasses factory).
	a.registry = connector.NewConnectorRegistry(logger)
	xdrFake := newFakeConnector("xdr")
	ocsfFake := newFakeConnector("ocsf")
	if err := a.registry.Register(xdrFake); err != nil {
		t.Fatalf("Register xdr: %v", err)
	}
	if err := a.registry.Register(ocsfFake); err != nil {
		t.Fatalf("Register ocsf: %v", err)
	}
	a.nonOCSFTargets = []string{"xdr"}
	a.ocsfTargets = []string{"ocsf"}
	a.instanceID = "test-instance"

	// Create dispatcher and buffer with a real drain.
	disp, err := connector.NewDispatcher(connector.DefaultDispatchConfig(), a.registry, logger)
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	a.dispatcher = disp

	buf := buffer.New(cfg.Buffer)
	a.buffer = buf
	drainFunc := func(ctx context.Context, b *connector.SignalBatch) error {
		return a.deliver(ctx, b)
	}
	a.drain = drainFunc
	if err := buf.Start(context.Background(), drainFunc); err != nil {
		t.Fatalf("buffer.Start: %v", err)
	}

	// Wire the ingest channel and start the loop.
	a.ingestCh = make(chan signal.Batch, 10)
	a.wg.Add(1)
	go a.ingestLoop(cfg)

	// Feed one batch.
	a.ingestCh <- signal.Batch{
		AppID:   "myapp",
		Env:     "prod",
		Signals: []signal.Signal{{SignalID: "s1"}},
	}

	// Allow time for dispatch.
	time.Sleep(100 * time.Millisecond)

	// Shut down cleanly.
	close(a.ingestCh)
	a.wg.Wait()

	// Verify xdr fake.
	xdrBatches := xdrFake.Received()
	if len(xdrBatches) != 1 {
		t.Fatalf("xdr fake: expected 1 batch, got %d", len(xdrBatches))
	}
	if xdrBatches[0].UseOCSF {
		t.Error("xdr fake: expected UseOCSF=false")
	}
	if xdrBatches[0].AppID != "myapp" {
		t.Errorf("xdr fake: expected AppID=%q, got %q", "myapp", xdrBatches[0].AppID)
	}
	if xdrBatches[0].Env != "prod" {
		t.Errorf("xdr fake: expected Env=%q, got %q", "prod", xdrBatches[0].Env)
	}
	if xdrBatches[0].BatchID == "" {
		t.Error("xdr fake: expected non-empty BatchID")
	}

	// Verify ocsf fake.
	ocsfBatches := ocsfFake.Received()
	if len(ocsfBatches) != 1 {
		t.Fatalf("ocsf fake: expected 1 batch, got %d", len(ocsfBatches))
	}
	if !ocsfBatches[0].UseOCSF {
		t.Error("ocsf fake: expected UseOCSF=true")
	}
	if ocsfBatches[0].AppID != "myapp" {
		t.Errorf("ocsf fake: expected AppID=%q, got %q", "myapp", ocsfBatches[0].AppID)
	}
	if ocsfBatches[0].Env != "prod" {
		t.Errorf("ocsf fake: expected Env=%q, got %q", "prod", ocsfBatches[0].Env)
	}
	if ocsfBatches[0].BatchID == "" {
		t.Error("ocsf fake: expected non-empty BatchID")
	}

	// Cleanup.
	_ = buf.Close()
	_ = disp.Close()
}

// TestStart_EmptyOutputs verifies that start() returns a clear error when
// cfg.Outputs is empty.
func TestStart_EmptyOutputs(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.Outputs = nil
	logger := zap.NewNop()

	a, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = a.start(context.Background())
	if err == nil {
		t.Fatal("expected error for empty Outputs, got nil")
	}
	_ = a.stop()
}

// TestStop_GracefulDrain verifies that stop() calls buffer.Flush with the real
// drain func (SC-4): a batch written directly to the buffer should be delivered
// through the fake connector after stop() is called.
func TestStop_GracefulDrain(t *testing.T) {
	cfg := minimalConfig(t)
	logger := zap.NewNop()

	a, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Set up registry with one fake connector (non-OCSF path).
	a.registry = connector.NewConnectorRegistry(logger)
	fake := newFakeConnector("drain-target")
	if err := a.registry.Register(fake); err != nil {
		t.Fatalf("Register: %v", err)
	}
	a.nonOCSFTargets = []string{"drain-target"}
	a.instanceID = "test-instance"

	disp, err := connector.NewDispatcher(connector.DefaultDispatchConfig(), a.registry, logger)
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	a.dispatcher = disp

	buf := buffer.New(cfg.Buffer)
	a.buffer = buf
	drainFunc := func(ctx context.Context, b *connector.SignalBatch) error {
		return a.deliver(ctx, b)
	}
	a.drain = drainFunc
	if err := buf.Start(context.Background(), drainFunc); err != nil {
		t.Fatalf("buffer.Start: %v", err)
	}

	// Wire ingest channel (even though we won't use it for this test).
	a.ingestCh = make(chan signal.Batch, 10)
	a.wg.Add(1)
	go a.ingestLoop(cfg)

	// Write a batch directly to the buffer to simulate an in-flight record.
	bufferedBatch := &connector.SignalBatch{
		BatchID:    connector.NewBatchID(),
		InstanceID: "test-instance",
		GroupID:    "test-group",
		AppID:      "buf-app",
		Env:        "staging",
		ReceivedAt: time.Now(),
		Signals:    []signal.Signal{{SignalID: "buf-sig"}},
		UseOCSF:    false,
	}
	if err := buf.Write(bufferedBatch); err != nil {
		t.Fatalf("buffer.Write: %v", err)
	}

	// stop() should Flush the buffer before closing.
	if err := a.stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// After Flush, the fake connector should have received the buffered batch.
	received := fake.Received()
	if len(received) == 0 {
		t.Error("expected fake connector to receive buffered batch via Flush, got none")
	}
}
