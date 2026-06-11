package euc

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// fakeOSCollector sends a fixed list of observations then closes the channel.
type fakeOSCollector struct {
	observations []Observation
	closeCalled  bool
}

func (f *fakeOSCollector) Start(ctx context.Context, obs chan<- Observation) error {
	go func() {
		for _, o := range f.observations {
			select {
			case obs <- o:
			case <-ctx.Done():
				return
			}
		}
		// Signal end-of-observations by closing.
		close(obs)
	}()
	return nil
}

func (f *fakeOSCollector) Close() error {
	f.closeCalled = true
	return nil
}

// TestFanOut_AIAccess verifies that a non-local Observation produces a batch
// with Category="euc.ai_access" and a populated ContextJSON.
func TestFanOut_AIAccess(t *testing.T) {
	obs := Observation{
		ConnectedHost: "api.openai.com",
		LocalPort:     0,
		IsLocal:       false,
		ProcessName:   "python3",
		Username:      "alice",
	}

	cfg := Config{AppID: "myapp", Env: "test"}
	impl := &fakeOSCollector{observations: []Observation{obs}}
	c := New(cfg, impl)

	out := make(chan signal.Batch, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := c.Start(ctx, out); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var batch signal.Batch
	select {
	case batch = <-out:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for batch")
	}

	if len(batch.Signals) != 1 {
		t.Fatalf("len(batch.Signals) = %d; want 1", len(batch.Signals))
	}
	sig := batch.Signals[0]

	if sig.Category != "euc.ai_access" {
		t.Errorf("Category = %q; want %q", sig.Category, "euc.ai_access")
	}
	if sig.AppID != "myapp" {
		t.Errorf("AppID = %q; want %q", sig.AppID, "myapp")
	}
	if sig.Env != "test" {
		t.Errorf("Env = %q; want %q", sig.Env, "test")
	}
	if sig.Layer != signal.L9APIGateway {
		t.Errorf("Layer = %v; want L9APIGateway", sig.Layer)
	}
	if sig.Severity != signal.SeverityInfo {
		t.Errorf("Severity = %v; want SeverityInfo", sig.Severity)
	}
	if sig.SignalID == "" {
		t.Error("SignalID is empty; want a generated ID")
	}
	if sig.Timestamp.IsZero() {
		t.Error("Timestamp is zero; want time.Now()")
	}
	if len(sig.ContextJSON) == 0 {
		t.Fatal("ContextJSON is empty; want JSON object")
	}
	var ctx2 map[string]interface{}
	if err := json.Unmarshal(sig.ContextJSON, &ctx2); err != nil {
		t.Fatalf("ContextJSON invalid JSON: %v", err)
	}
	if ctx2["connected_host"] != "api.openai.com" {
		t.Errorf("ContextJSON.connected_host = %v; want api.openai.com", ctx2["connected_host"])
	}
	if ctx2["process_name"] != "python3" {
		t.Errorf("ContextJSON.process_name = %v; want python3", ctx2["process_name"])
	}
	if ctx2["username"] != "alice" {
		t.Errorf("ContextJSON.username = %v; want alice", ctx2["username"])
	}

	if batch.AppID != "myapp" {
		t.Errorf("batch.AppID = %q; want %q", batch.AppID, "myapp")
	}
	if batch.Env != "test" {
		t.Errorf("batch.Env = %q; want %q", batch.Env, "test")
	}
}

// TestFanOut_LocalInference verifies Category="euc.local_inference" for local observations.
func TestFanOut_LocalInference(t *testing.T) {
	obs := Observation{
		ConnectedHost: "",
		LocalPort:     11434,
		IsLocal:       true,
		ProcessName:   "ollama",
		Username:      "bob",
	}

	cfg := Config{AppID: "myapp", Env: "test"}
	impl := &fakeOSCollector{observations: []Observation{obs}}
	c := New(cfg, impl)

	out := make(chan signal.Batch, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := c.Start(ctx, out); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case batch := <-out:
		if len(batch.Signals) != 1 {
			t.Fatalf("len(batch.Signals) = %d; want 1", len(batch.Signals))
		}
		if batch.Signals[0].Category != "euc.local_inference" {
			t.Errorf("Category = %q; want %q", batch.Signals[0].Category, "euc.local_inference")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for batch")
	}
}

// TestFanOut_CtxCancel verifies that fanOut exits on context cancellation.
func TestFanOut_CtxCancel(t *testing.T) {
	// A blocking fake that never sends any observations — fanOut should
	// exit when the context is cancelled.
	blockingFake := &blockingOSCollector{}

	cfg := Config{AppID: "myapp", Env: "test"}
	c := New(cfg, blockingFake)

	out := make(chan signal.Batch, 8)
	ctx, cancel := context.WithCancel(context.Background())

	if err := c.Start(ctx, out); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Cancel and ensure no batch was emitted.
	cancel()

	// Give goroutine time to exit.
	time.Sleep(50 * time.Millisecond)

	select {
	case batch := <-out:
		t.Errorf("unexpected batch after ctx cancel: %+v", batch)
	default:
	}
}

// TestNoopOSCollector verifies that NewNoopOSCollector is exported and functional.
func TestNoopOSCollector(t *testing.T) {
	noop := NewNoopOSCollector()
	if noop == nil {
		t.Fatal("NewNoopOSCollector() returned nil")
	}

	obs := make(chan Observation, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := noop.Start(ctx, obs); err != nil {
		t.Errorf("noop.Start: %v", err)
	}
	if err := noop.Close(); err != nil {
		t.Errorf("noop.Close: %v", err)
	}
	// No observations should be emitted.
	select {
	case o := <-obs:
		t.Errorf("noop emitted unexpected observation: %+v", o)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestCollector_EndToEnd verifies the full collector chain using
// NewNoopOSCollector — the canonical end-to-end path on Windows.
func TestCollector_EndToEnd_Noop(t *testing.T) {
	cfg := Config{AppID: "e2e-app", Env: "dev"}
	c := New(cfg, NewNoopOSCollector())

	out := make(chan signal.Batch, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := c.Start(ctx, out); err != nil {
		t.Fatalf("Start with noop: %v", err)
	}
	// The noop collector emits nothing; no batch should arrive.
	select {
	case b := <-out:
		t.Errorf("unexpected batch from noop collector: %+v", b)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

// blockingOSCollector is an OSCollector that never sends any observations.
type blockingOSCollector struct{}

func (b *blockingOSCollector) Start(ctx context.Context, obs chan<- Observation) error {
	// Start a goroutine that just waits for context cancellation.
	go func() { <-ctx.Done() }()
	return nil
}

func (b *blockingOSCollector) Close() error { return nil }
