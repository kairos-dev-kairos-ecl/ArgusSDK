//go:build llmlocal

package llmsignal

import (
	"context"
	"net"
	"testing"
	"time"

	sdkv1 "github.com/kairos-dev-kairos-ecl/ArgusSDK/gen/go/sdk/v1"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/collector/llm"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestIngestPipelineE2E (Goal 1) exercises the agent's real receive→convert→
// deliver path with signals derived from a live LLM call:
//
//	real prompt pass
//	  → extract signal.Batch (SDK role)
//	  → marshal to proto + send over gRPC to the agent's llm.Collector
//	  → collector converts and emits on the ingest channel
//	  → dispatcher delivers to a fake connector
//	  → assert the delivered batch carries the extracted signal
func TestIngestPipelineE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	backends := availableBackends(ctx, t)
	backend := backends[0] // one backend is enough to prove the pipeline

	// --- Real prompt pass → extracted batch ---
	res, err := backend.Chat(ctx, "Say hi in one short sentence.")
	if err != nil {
		t.Fatalf("%s prompt pass failed: %v", backend.Name(), err)
	}
	batch := ExtractBatch("llm-e2e-app", "test", res)
	proto := batch.ToProto("e2e-batch-1", "llmsignal-test/1")

	// --- Start the agent's LLM collector on a real loopback port ---
	addr := freeAddr(t)
	out := make(chan signal.Batch, 4)
	coll := llm.New(llm.Config{GRPCAddr: addr})
	collCtx, collCancel := context.WithCancel(ctx)
	defer collCancel()
	if err := coll.Start(collCtx, out); err != nil {
		t.Fatalf("collector start: %v", err)
	}
	defer coll.Close()
	waitListening(t, addr)

	// --- Send the proto batch over gRPC, exactly as an SDK would ---
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc dial: %v", err)
	}
	defer conn.Close()
	client := sdkv1.NewSDKIngestServiceClient(conn)

	ack, err := client.IngestBatch(ctx, proto)
	if err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}
	if ack.GetAcceptedCount() != 1 {
		t.Fatalf("expected AcceptedCount=1, got %d (rejected=%d)",
			ack.GetAcceptedCount(), ack.GetRejectedCount())
	}

	// --- Collector emits the converted batch on the ingest channel ---
	var received signal.Batch
	select {
	case received = <-out:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for collector to emit converted batch")
	}
	if len(received.Signals) != 1 {
		t.Fatalf("expected 1 converted signal, got %d", len(received.Signals))
	}
	if received.Signals[0].Category != CategoryChatCompletion {
		t.Errorf("converted signal category: got %q, want %q",
			received.Signals[0].Category, CategoryChatCompletion)
	}

	// --- Deliver through the dispatcher to a fake connector ---
	logger := zap.NewNop()
	registry := connector.NewConnectorRegistry(logger)
	fake := newFakeConnector("e2e-output")
	if err := registry.Register(fake); err != nil {
		t.Fatalf("register: %v", err)
	}
	disp, err := connector.NewDispatcher(connector.DefaultDispatchConfig(), registry, logger)
	if err != nil {
		t.Fatalf("dispatcher: %v", err)
	}
	defer disp.Close()

	sb := &connector.SignalBatch{
		BatchID:    "e2e-deliver-1",
		InstanceID: "inst-e2e",
		AppID:      received.AppID,
		Env:        received.Env,
		ReceivedAt: time.Now(),
		Signals:    received.Signals,
		UseOCSF:    true,
	}
	if err := disp.Enqueue(&connector.DispatchJob{Batch: sb, Targets: []string{"e2e-output"}}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// --- Assert delivery ---
	deadline := time.After(5 * time.Second)
	for {
		if got := fake.received(); len(got) > 0 {
			delivered := got[0]
			if len(delivered.Signals) != 1 {
				t.Fatalf("delivered batch signal count: got %d, want 1", len(delivered.Signals))
			}
			if delivered.AppID != "llm-e2e-app" {
				t.Errorf("delivered AppID: got %q, want %q", delivered.AppID, "llm-e2e-app")
			}
			cc, err := decodeContext(delivered.Signals[0].ContextJSON)
			if err != nil {
				t.Fatalf("delivered ContextJSON parse: %v", err)
			}
			if cc.Backend != backend.Name() {
				t.Errorf("delivered backend: got %q, want %q", cc.Backend, backend.Name())
			}
			t.Logf("delivered e2e: app=%s backend=%s model=%s", delivered.AppID, cc.Backend, cc.Model)
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for fake connector delivery")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// freeAddr returns a currently-free loopback address. There is an inherent
// (tiny) race between closing the probe listener and the collector re-binding;
// acceptable for a local test.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// waitListening blocks until addr accepts TCP connections or the deadline passes.
func waitListening(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("collector at %s never started listening", addr)
}
