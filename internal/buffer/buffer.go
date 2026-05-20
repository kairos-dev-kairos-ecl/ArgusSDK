// Package buffer implements the local signal buffer used during XDR connectivity
// loss. Signals are written to a flat file with a write-ahead log (WAL) so that
// no data is lost across agent restarts or unexpected termination.
//
// Design constraints:
//   - Max size is configurable (default 256 MB); once full, the oldest segments
//     are dropped and a metric counter is incremented.
//   - On reconnect, the buffer drains in order with exponential backoff + jitter
//     so a fleet of agents does not simultaneously hammer the ingest endpoint.
//   - The WAL is append-only; committed entries are truncated after successful
//     delivery acknowledgement.
//   - File format: length-prefixed protobuf records (4-byte big-endian uint32 length
//     followed by serialised SignalBatch bytes). This allows streaming reads without
//     loading the entire file into memory.
package buffer

import (
	"context"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
)

// Config holds buffer tuning parameters.
type Config struct {
	// Dir is the directory where WAL segment files are written.
	// The agent creates it if it does not exist.
	Dir string

	// MaxSizeMB is the maximum total disk space the buffer may use.
	// When exceeded, the oldest segments are dropped. Default: 256.
	MaxSizeMB int

	// FlushInterval is how often the agent attempts to drain the buffer
	// to the configured output connectors when connectivity is restored. Default: 5s.
	FlushInterval time.Duration

	// DrainOnReconnect enables eager drain as soon as a connector reports healthy.
	// If false, drain is governed solely by FlushInterval. Default: true.
	DrainOnReconnect bool

	// BackoffBase is the initial reconnect backoff duration. Default: 2s.
	BackoffBase time.Duration

	// BackoffMax is the maximum backoff duration after repeated failures. Default: 5m.
	BackoffMax time.Duration

	// BackoffJitter is the maximum random jitter added per backoff step. Default: 30s.
	BackoffJitter time.Duration
}

// DefaultConfig returns a Config with production-safe defaults.
func DefaultConfig() Config {
	return Config{
		Dir:              "argus-buffer",
		MaxSizeMB:        256,
		FlushInterval:    5 * time.Second,
		DrainOnReconnect: true,
		BackoffBase:      2 * time.Second,
		BackoffMax:       5 * time.Minute,
		BackoffJitter:    30 * time.Second,
	}
}

// Buffer manages WAL-backed signal storage and drain-on-reconnect logic.
//
// Implementation TODO (not in this scaffold):
//   - WAL writer: append length-prefixed proto records to the active segment file
//   - WAL reader: stream records from the oldest unconfirmed segment
//   - Segment rotation: create a new segment file when the active one exceeds a
//     configured segment size (e.g. 32 MB) for bounded memory on read
//   - Drain loop: on flush tick or health-change event, read oldest records and
//     call Dispatcher.Enqueue; on DeliveryAck, truncate or delete the segment
//   - Backoff: exponential with jitter (use resilience.TokenBucket to rate-limit
//     drain bursts so reconnected agents do not overwhelm XDR)
//   - Metrics: buffered_bytes_total, dropped_batches_total, drain_lag_seconds
type Buffer struct {
	cfg Config
}

// New creates a Buffer. Call Start to begin the drain loop.
func New(cfg Config) *Buffer {
	if cfg.MaxSizeMB == 0 {
		cfg.MaxSizeMB = 256
	}
	if cfg.FlushInterval == 0 {
		cfg.FlushInterval = 5 * time.Second
	}
	return &Buffer{cfg: cfg}
}

// Write appends a batch to the WAL. Returns error only on unrecoverable I/O failure.
// Write is safe for concurrent calls from multiple collector goroutines.
func (b *Buffer) Write(_ *connector.SignalBatch) error {
	// TODO: serialise batch, append length-prefixed record to active WAL segment
	return nil
}

// Start begins the drain loop in a background goroutine.
// The loop reads buffered batches and delivers them via the provided drain function.
// drain is called with each batch; a nil error return triggers WAL truncation.
func (b *Buffer) Start(ctx context.Context, drain func(ctx context.Context, batch *connector.SignalBatch) error) {
	go b.drainLoop(ctx, drain)
}

func (b *Buffer) drainLoop(ctx context.Context, drain func(context.Context, *connector.SignalBatch) error) {
	_ = drain
	ticker := time.NewTicker(b.cfg.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// TODO: read oldest unconfirmed segments, call drain, truncate on success
		}
	}
}

// Flush synchronously drains all buffered batches. Intended for graceful shutdown.
func (b *Buffer) Flush(ctx context.Context, drain func(context.Context, *connector.SignalBatch) error) error {
	_ = drain
	// TODO: iterate all unconfirmed WAL records, call drain sequentially
	return nil
}

// Close stops the drain loop and closes open file handles.
func (b *Buffer) Close() error {
	// TODO: close active segment file handle
	return nil
}

// Stats returns current buffer metrics for Prometheus exposure.
func (b *Buffer) Stats() map[string]int64 {
	// TODO: return actual counters from WAL state
	return map[string]int64{
		"buffered_batches": 0,
		"buffered_bytes":   0,
		"dropped_batches":  0,
	}
}
