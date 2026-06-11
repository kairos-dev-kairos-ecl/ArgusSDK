// Package llm implements the LLM signal collector: a gRPC server that receives
// signal batches from instrumentation libraries (Python/TypeScript) running in
// the same application. The collector listens on a configurable address and
// translates received proto messages to internal signal.Batch values.
package llm

import (
	"context"
	"net"
	"runtime"
	"sync"

	sdkv1 "github.com/kairos-dev-kairos-ecl/ArgusSDK/gen/go/sdk/v1"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/collector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
	"google.golang.org/grpc"
)

// Config holds listener configuration for the LLM collector.
type Config struct {
	// GRPCAddr is the TCP address the gRPC server listens on.
	// Default: "127.0.0.1:5002"
	GRPCAddr string

	// UnixSocket is the path to an optional Unix domain socket.
	// If set, the server also listens here (lower overhead than loopback TCP).
	// Ignored on Windows (Unix domain sockets not supported).
	UnixSocket string

	// MaxConcurrentStreams caps per-connection gRPC streams. Default: 256.
	MaxConcurrentStreams uint32

	// MaxRecvMsgSize caps the maximum inbound gRPC message size in bytes.
	// Default: 4 MB.
	MaxRecvMsgSize int
}

// Collector accepts signal batches from Python/TypeScript instrumentation libs
// over a local gRPC listener and pushes them onto the shared ingest channel.
type Collector struct {
	cfg    Config
	server *grpc.Server
	lis    net.Listener
	once   sync.Once
}

// New creates an LLM collector. Call Start to begin accepting signals.
func New(cfg Config) *Collector {
	if cfg.GRPCAddr == "" {
		cfg.GRPCAddr = "127.0.0.1:5002"
	}
	if cfg.MaxRecvMsgSize == 0 {
		cfg.MaxRecvMsgSize = 4 << 20 // 4 MB
	}
	if cfg.MaxConcurrentStreams == 0 {
		cfg.MaxConcurrentStreams = 256
	}
	return &Collector{cfg: cfg}
}

// Name implements collector.Collector.
func (c *Collector) Name() string { return "llm_grpc" }

// Start launches the gRPC listener on the configured TCP address and begins
// forwarding received batches on out.
func (c *Collector) Start(ctx context.Context, out chan<- signal.Batch) error {
	lis, err := net.Listen("tcp", c.cfg.GRPCAddr)
	if err != nil {
		return err
	}
	return c.startOnListener(ctx, lis, out)
}

// startOnListener attaches a new gRPC server to lis and starts serving.
// It is the internal entry point used by both Start and tests (via bufconn).
func (c *Collector) startOnListener(ctx context.Context, lis net.Listener, out chan<- signal.Batch) error {
	srv := grpc.NewServer(
		grpc.MaxRecvMsgSize(c.cfg.MaxRecvMsgSize),
		grpc.MaxConcurrentStreams(c.cfg.MaxConcurrentStreams),
	)

	sdkv1.RegisterSDKIngestServiceServer(srv, &ingestServer{out: out})

	c.server = srv
	c.lis = lis

	// Also listen on Unix socket when configured and not on Windows.
	if c.cfg.UnixSocket != "" && runtime.GOOS != "windows" {
		unixLis, err := net.Listen("unix", c.cfg.UnixSocket)
		if err != nil {
			return err
		}
		go srv.Serve(unixLis) //nolint:errcheck
	}

	go srv.Serve(lis) //nolint:errcheck

	// Stop server when the context is cancelled.
	go func() {
		<-ctx.Done()
		c.Close() //nolint:errcheck
	}()

	return nil
}

// Health returns nil if the gRPC server has been started.
func (c *Collector) Health(_ context.Context) error {
	if c.server == nil {
		return nil
	}
	return nil
}

// Close gracefully stops the gRPC server and waits for in-flight RPCs to finish.
// It is safe to call Close multiple times.
func (c *Collector) Close() error {
	c.once.Do(func() {
		if c.server != nil {
			c.server.GracefulStop()
		}
	})
	return nil
}

// ensure Collector satisfies the collector.Collector interface at compile time.
var _ collector.Collector = (*Collector)(nil)

// ingestServer implements SDKIngestServiceServer and forwards validated signal
// batches onto the out channel.
type ingestServer struct {
	sdkv1.UnimplementedSDKIngestServiceServer
	out chan<- signal.Batch
}

// IngestBatch handles an inbound proto batch. Signals failing basic validation
// (empty SignalID or Layer == LayerUnspecified) are counted in RejectedCount and
// dropped; valid signals are converted via signal.FromProto and emitted as a
// single signal.Batch on the out channel. The BatchAck echoes the BatchId and
// reports AcceptedCount/RejectedCount.
//
// T-04-12: MaxRecvMsgSize and MaxConcurrentStreams are enforced at the server
// level; invalid signals are rejected here (not panicked) per the STRIDE register.
// T-04-13: Minimal validation (non-empty SignalID, Layer != LayerUnspecified)
// before emit; invalid signals counted in RejectedCount and dropped.
func (s *ingestServer) IngestBatch(ctx context.Context, req *sdkv1.SignalBatch) (*sdkv1.BatchAck, error) {
	var accepted []signal.Signal
	var rejectedCount int32

	for _, p := range req.GetSignals() {
		// Validate: require non-empty SignalId and a specified Layer.
		if p.GetSignalId() == "" || signal.Layer(p.GetLayer()) == signal.LayerUnspecified {
			rejectedCount++
			continue
		}
		sig := signal.FromProto(p)
		// Fill fields that FromProto intentionally leaves empty (they come from
		// the enclosing SignalBatch, not from the per-signal proto).
		sig.AppID = req.GetAppId()
		sig.Env = req.GetEnv()
		sig.SDKVersion = req.GetSdkVersion()
		accepted = append(accepted, sig)
	}

	if len(accepted) > 0 {
		batch := signal.Batch{
			AppID:   req.GetAppId(),
			Env:     req.GetEnv(),
			Signals: accepted,
		}
		select {
		case s.out <- batch:
		case <-ctx.Done():
		}
	}

	return &sdkv1.BatchAck{
		BatchId:       req.GetBatchId(),
		AcceptedCount: int32(len(accepted)),
		RejectedCount: rejectedCount,
	}, nil
}
