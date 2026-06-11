package llm

import (
	"context"
	"net"
	"testing"
	"time"

	sdkv1 "github.com/kairos-dev-kairos-ecl/ArgusSDK/gen/go/sdk/v1"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1 << 20 // 1 MB

// startCollectorOnBufconn creates a collector wired to a bufconn listener,
// starts it, and returns the client connection and the out channel.
// The caller must cancel ctx to stop the server.
func startCollectorOnBufconn(t *testing.T) (*grpc.ClientConn, chan signal.Batch, context.CancelFunc) {
	t.Helper()

	lis := bufconn.Listen(bufSize)
	cfg := Config{
		GRPCAddr:            "127.0.0.1:0", // not used — we replace the listener
		MaxRecvMsgSize:      4 << 20,
		MaxConcurrentStreams: 256,
	}
	c := New(cfg)
	out := make(chan signal.Batch, 16)

	ctx, cancel := context.WithCancel(context.Background())

	if err := c.startOnListener(ctx, lis, out); err != nil {
		cancel()
		lis.Close()
		t.Fatalf("startOnListener: %v", err)
	}

	conn, err := grpc.NewClient(
		"passthrough://bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		cancel()
		t.Fatalf("grpc.NewClient: %v", err)
	}

	t.Cleanup(func() {
		cancel()
		conn.Close()
		c.Close()
	})

	return conn, out, cancel
}

// TestIngestBatch_HappyPath verifies that N valid signals produce one batch on out
// with AcceptedCount=N and AppID/Env populated.
func TestIngestBatch_HappyPath(t *testing.T) {
	conn, out, _ := startCollectorOnBufconn(t)
	client := sdkv1.NewSDKIngestServiceClient(conn)

	req := &sdkv1.SignalBatch{
		BatchId:    "batch-001",
		AppId:      "myapp",
		Env:        "test",
		SdkVersion: "1.2.3",
		Signals: []*sdkv1.SDKSignal{
			{
				SignalId: "sig-1",
				Layer:    int32(signal.L9APIGateway),
				Category: "llm.request",
			},
			{
				SignalId: "sig-2",
				Layer:    int32(signal.L10Application),
				Category: "llm.response",
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ack, err := client.IngestBatch(ctx, req)
	if err != nil {
		t.Fatalf("IngestBatch error: %v", err)
	}

	if ack.BatchId != "batch-001" {
		t.Errorf("ack.BatchId = %q; want %q", ack.BatchId, "batch-001")
	}
	if ack.AcceptedCount != 2 {
		t.Errorf("ack.AcceptedCount = %d; want 2", ack.AcceptedCount)
	}
	if ack.RejectedCount != 0 {
		t.Errorf("ack.RejectedCount = %d; want 0", ack.RejectedCount)
	}

	select {
	case batch := <-out:
		if batch.AppID != "myapp" {
			t.Errorf("batch.AppID = %q; want %q", batch.AppID, "myapp")
		}
		if batch.Env != "test" {
			t.Errorf("batch.Env = %q; want %q", batch.Env, "test")
		}
		if len(batch.Signals) != 2 {
			t.Errorf("len(batch.Signals) = %d; want 2", len(batch.Signals))
		}
		for _, s := range batch.Signals {
			if s.AppID != "myapp" {
				t.Errorf("signal.AppID = %q; want %q", s.AppID, "myapp")
			}
			if s.Env != "test" {
				t.Errorf("signal.Env = %q; want %q", s.Env, "test")
			}
			if s.SDKVersion != "1.2.3" {
				t.Errorf("signal.SDKVersion = %q; want %q", s.SDKVersion, "1.2.3")
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for batch on out channel")
	}
}

// TestIngestBatch_Validation verifies that signals with empty SignalID or
// LayerUnspecified are rejected and not emitted.
func TestIngestBatch_Validation(t *testing.T) {
	conn, out, _ := startCollectorOnBufconn(t)
	client := sdkv1.NewSDKIngestServiceClient(conn)

	req := &sdkv1.SignalBatch{
		BatchId: "batch-002",
		AppId:   "myapp",
		Env:     "test",
		Signals: []*sdkv1.SDKSignal{
			{SignalId: "", Layer: int32(signal.L9APIGateway)},           // invalid: empty signal_id
			{SignalId: "ok", Layer: int32(signal.LayerUnspecified)},     // invalid: layer unspecified
			{SignalId: "sig-valid", Layer: int32(signal.L10Application)}, // valid
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ack, err := client.IngestBatch(ctx, req)
	if err != nil {
		t.Fatalf("IngestBatch error: %v", err)
	}
	if ack.AcceptedCount != 1 {
		t.Errorf("AcceptedCount = %d; want 1", ack.AcceptedCount)
	}
	if ack.RejectedCount != 2 {
		t.Errorf("RejectedCount = %d; want 2", ack.RejectedCount)
	}

	select {
	case batch := <-out:
		if len(batch.Signals) != 1 {
			t.Errorf("len(batch.Signals) = %d; want 1", len(batch.Signals))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for batch on out channel")
	}
}

// TestIngestBatch_AllRejected verifies that if all signals fail validation,
// no batch is sent on out but the RPC still succeeds.
func TestIngestBatch_AllRejected(t *testing.T) {
	conn, out, _ := startCollectorOnBufconn(t)
	client := sdkv1.NewSDKIngestServiceClient(conn)

	req := &sdkv1.SignalBatch{
		BatchId: "batch-003",
		AppId:   "myapp",
		Env:     "test",
		Signals: []*sdkv1.SDKSignal{
			{SignalId: "", Layer: int32(signal.L9APIGateway)},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ack, err := client.IngestBatch(ctx, req)
	if err != nil {
		t.Fatalf("IngestBatch error: %v", err)
	}
	if ack.AcceptedCount != 0 {
		t.Errorf("AcceptedCount = %d; want 0", ack.AcceptedCount)
	}
	if ack.RejectedCount != 1 {
		t.Errorf("RejectedCount = %d; want 1", ack.RejectedCount)
	}

	// No batch should arrive on out since all were rejected.
	select {
	case batch := <-out:
		t.Errorf("unexpected batch on out: %+v", batch)
	case <-time.After(200 * time.Millisecond):
		// expected: no batch
	}
}

// TestClose_Safe verifies that Close can be called multiple times without panic.
func TestClose_Safe(t *testing.T) {
	cfg := Config{GRPCAddr: "127.0.0.1:0"}
	c := New(cfg)
	out := make(chan signal.Batch, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lis := bufconn.Listen(bufSize)
	if err := c.startOnListener(ctx, lis, out); err != nil {
		t.Fatalf("startOnListener: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}
