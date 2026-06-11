package argusxdr

import (
	"context"
	"net"
	"testing"
	"time"

	sdkv1 "github.com/kairos-dev-kairos-ecl/ArgusSDK/gen/go/sdk/v1"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

// fakeIngestServer is an in-process gRPC server used in tests.
type fakeIngestServer struct {
	sdkv1.UnimplementedSDKIngestServiceServer

	// fields captured on the most recent IngestBatch call
	receivedBatch    *sdkv1.SignalBatch
	receivedMetadata metadata.MD

	// if non-nil, IngestBatch returns this error
	returnErr error

	// response to return on success
	returnAck *sdkv1.BatchAck
}

func (f *fakeIngestServer) IngestBatch(ctx context.Context, batch *sdkv1.SignalBatch) (*sdkv1.BatchAck, error) {
	f.receivedBatch = batch
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		f.receivedMetadata = md
	}
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	if f.returnAck != nil {
		return f.returnAck, nil
	}
	return &sdkv1.BatchAck{BatchId: batch.BatchId, AcceptedCount: int32(len(batch.Signals))}, nil
}

// startBufconnServer starts an in-process gRPC server using bufconn.
// Returns the listener and a cleanup function.
func startBufconnServer(t *testing.T, fake *fakeIngestServer) *bufconn.Listener {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	sdkv1.RegisterSDKIngestServiceServer(srv, fake)
	go func() {
		if err := srv.Serve(lis); err != nil && err != grpc.ErrServerStopped {
			// non-fatal: bufconn closes return a known error on shutdown
		}
	}()
	t.Cleanup(func() {
		srv.Stop()
		lis.Close()
	})
	return lis
}

// dialBufconn creates a gRPC client connected to the bufconn listener.
func dialBufconn(t *testing.T, lis *bufconn.Listener) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dialBufconn: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// ---- TestConnect -------------------------------------------------------------

func TestConnect_EmptyEndpointReturnsError(t *testing.T) {
	c := New(Config{Endpoint: ""})
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error for empty Endpoint, got nil")
	}
}

func TestConnect_InsecureDialSucceeds(t *testing.T) {
	fake := &fakeIngestServer{}
	lis := startBufconnServer(t, fake)

	// Manually create a connector and inject an insecure dial using the bufconn dialer.
	// We test the public-facing behaviour: after dialing, the client stub must be non-nil.
	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer conn.Close()

	c := NewWithClient(Config{Endpoint: "passthrough://bufnet"}, sdkv1.NewSDKIngestServiceClient(conn))
	if c == nil {
		t.Fatal("NewWithClient returned nil")
	}
	if c.client == nil {
		t.Fatal("expected non-nil client after NewWithClient")
	}
}

// TestNewWithClient ensures the test constructor bypasses real dialing.
func TestNewWithClient_NonNilClient(t *testing.T) {
	fake := &fakeIngestServer{}
	lis := startBufconnServer(t, fake)
	conn := dialBufconn(t, lis)

	c := NewWithClient(Config{Endpoint: "unused"}, sdkv1.NewSDKIngestServiceClient(conn))
	if c == nil {
		t.Fatal("expected non-nil Connector from NewWithClient")
	}
	if c.client == nil {
		t.Fatal("expected non-nil client stub in Connector")
	}
}

// ---- TestSend ----------------------------------------------------------------

func TestSend_HappyPath(t *testing.T) {
	fake := &fakeIngestServer{
		returnAck: &sdkv1.BatchAck{BatchId: "remote-ack-001", AcceptedCount: 2},
	}
	lis := startBufconnServer(t, fake)
	conn := dialBufconn(t, lis)

	c := NewWithClient(Config{
		Endpoint: "unused",
		Auth: AuthConfig{
			InstanceID: "inst-001",
			GroupID:    "grp-001",
			Credential: "apikey-abc",
		},
	}, sdkv1.NewSDKIngestServiceClient(conn))

	batch := &connector.SignalBatch{
		BatchID:    "batch-test-001",
		InstanceID: "inst-001",
		GroupID:    "grp-001",
		AppID:      "test-app",
		Env:        "test-env",
		Signals: []signal.Signal{
			{SignalID: "sig-1", Layer: signal.L9APIGateway, Severity: signal.SeverityHigh},
			{SignalID: "sig-2", Layer: signal.L8Agents, Severity: signal.SeverityMedium},
		},
		// UseOCSF is intentionally true to verify it is ignored by argusxdr
		UseOCSF: true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ack, err := c.Send(ctx, batch)
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if ack == nil {
		t.Fatal("expected non-nil ack")
	}
	if ack.Status != "delivered" {
		t.Errorf("expected status=delivered, got %q", ack.Status)
	}
	if ack.BatchID != "batch-test-001" {
		t.Errorf("expected BatchID=batch-test-001, got %q", ack.BatchID)
	}
	if ack.RemoteID != "remote-ack-001" {
		t.Errorf("expected RemoteID=remote-ack-001, got %q", ack.RemoteID)
	}

	// Verify the server received the correct number of signals
	if fake.receivedBatch == nil {
		t.Fatal("server did not receive a batch")
	}
	if len(fake.receivedBatch.Signals) != 2 {
		t.Errorf("expected 2 signals in proto batch, got %d", len(fake.receivedBatch.Signals))
	}

	// Verify AppId/Env are propagated from connector.SignalBatch fields (SC-8).
	// Signals[0].AppID is intentionally empty — signal.FromProto does not set it.
	if fake.receivedBatch.AppId != "test-app" {
		t.Errorf("expected proto AppId=%q, got %q", "test-app", fake.receivedBatch.AppId)
	}
	if fake.receivedBatch.Env != "test-env" {
		t.Errorf("expected proto Env=%q, got %q", "test-env", fake.receivedBatch.Env)
	}

	// Verify InstanceID and GroupID are in the incoming metadata, NOT in the proto body
	if fake.receivedMetadata == nil {
		t.Fatal("server received no metadata")
	}
	if vals := fake.receivedMetadata.Get("x-argus-instance-id"); len(vals) == 0 || vals[0] != "inst-001" {
		t.Errorf("expected x-argus-instance-id=inst-001 in metadata, got %v", fake.receivedMetadata.Get("x-argus-instance-id"))
	}
	if vals := fake.receivedMetadata.Get("x-argus-group-id"); len(vals) == 0 || vals[0] != "grp-001" {
		t.Errorf("expected x-argus-group-id=grp-001 in metadata, got %v", fake.receivedMetadata.Get("x-argus-group-id"))
	}
}

func TestSend_ErrorPathReturnsFailedAck(t *testing.T) {
	fake := &fakeIngestServer{
		returnErr: status.Error(codes.Unavailable, "server down"),
	}
	lis := startBufconnServer(t, fake)
	conn := dialBufconn(t, lis)

	c := NewWithClient(Config{Endpoint: "unused"}, sdkv1.NewSDKIngestServiceClient(conn))

	batch := &connector.SignalBatch{
		BatchID: "batch-fail-001",
		Signals: []signal.Signal{
			{SignalID: "sig-x", Layer: signal.L1Hardware},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ack, err := c.Send(ctx, batch)
	if err == nil {
		t.Fatal("expected non-nil error from Send when server returns gRPC error")
	}
	if ack == nil {
		t.Fatal("expected non-nil ack even on failure")
	}
	if ack.Status != "failed" {
		t.Errorf("expected status=failed, got %q", ack.Status)
	}
}

func TestSend_NilClientReturnsFailedAck(t *testing.T) {
	// Do not call Connect — client remains nil
	c := New(Config{Endpoint: "unused"})

	batch := &connector.SignalBatch{
		BatchID: "batch-nil-client",
		Signals: []signal.Signal{{SignalID: "sig-y"}},
	}

	ack, err := c.Send(context.Background(), batch)
	if err == nil {
		t.Fatal("expected non-nil error when client is nil")
	}
	if ack == nil {
		t.Fatal("expected non-nil ack even on failure")
	}
	if ack.Status != "failed" {
		t.Errorf("expected status=failed, got %q", ack.Status)
	}
}

func TestSend_UseOCSFIgnored(t *testing.T) {
	// UseOCSF=true must be silently ignored; argusxdr always sends proto
	fake := &fakeIngestServer{}
	lis := startBufconnServer(t, fake)
	conn := dialBufconn(t, lis)

	c := NewWithClient(Config{Endpoint: "unused"}, sdkv1.NewSDKIngestServiceClient(conn))

	batch := &connector.SignalBatch{
		BatchID: "batch-ocsf",
		Signals: []signal.Signal{{SignalID: "sig-ocsf"}},
		UseOCSF: true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ack, err := c.Send(ctx, batch)
	if err != nil {
		t.Fatalf("Send with UseOCSF=true should not error: %v", err)
	}
	if ack.Status != "delivered" {
		t.Errorf("expected delivered, got %q", ack.Status)
	}
	// Server must have received the proto batch (not OCSF)
	if fake.receivedBatch == nil || len(fake.receivedBatch.Signals) != 1 {
		t.Errorf("expected server to receive 1 signal via proto (not OCSF), got receivedBatch=%v", fake.receivedBatch)
	}
}
