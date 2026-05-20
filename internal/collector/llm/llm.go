// Package llm implements the LLM signal collector: a gRPC server that receives
// signal batches from instrumentation libraries (Python/TypeScript) running in
// the same application. The collector listens on a configurable address and
// translates received proto messages to internal signal.Batch values.
package llm

import (
	"context"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/collector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// Config holds listener configuration for the LLM collector.
type Config struct {
	// GRPCAddr is the TCP address the gRPC server listens on.
	// Default: "127.0.0.1:5002"
	GRPCAddr string

	// UnixSocket is the path to an optional Unix domain socket.
	// If set, the server also listens here (lower overhead than loopback TCP).
	UnixSocket string

	// MaxConcurrentStreams caps per-connection gRPC streams. Default: 256.
	MaxConcurrentStreams uint32

	// MaxRecvMsgSize caps the maximum inbound gRPC message size in bytes.
	// Default: 4 MB.
	MaxRecvMsgSize int
}

// Collector accepts signal batches from Python/TypeScript instrumentation libs
// over a local gRPC listener and pushes them onto the shared ingest channel.
//
// Implementation TODO (not in this scaffold):
//   - Register an IngestService gRPC server using the ArgusSignal proto stubs
//   - Translate proto ArgusSignal → pkg/signal.Signal in the handler
//   - Apply basic validation (signal_id, app_id, layer not unspecified)
//   - Emit valid signals on the out channel; log and discard invalid ones
//   - Instrument with Prometheus counters: received_total, invalid_total, queue_depth
type Collector struct {
	cfg Config
	// server *grpc.Server  // wired in during implementation
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

// Start launches the gRPC listener and begins forwarding received batches.
func (c *Collector) Start(_ context.Context, _ chan<- signal.Batch) error {
	// TODO: create grpc.Server, register IngestServiceServer, listen on GRPCAddr
	// TODO: if UnixSocket != "", also listen on the socket path
	return nil
}

// Health returns nil if the gRPC listener is accepting connections.
func (c *Collector) Health(_ context.Context) error {
	// TODO: check server state
	return nil
}

// Close gracefully stops the gRPC server and waits for in-flight RPCs to finish.
func (c *Collector) Close() error {
	// TODO: server.GracefulStop()
	return nil
}

// ensure Collector satisfies the collector.Collector interface at compile time.
var _ collector.Collector = (*Collector)(nil)
