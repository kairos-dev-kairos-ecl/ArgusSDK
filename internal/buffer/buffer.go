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
//   - File format: length-prefixed gob records (4-byte big-endian uint32 length
//     followed by serialised SignalBatch bytes). This allows streaming reads without
//     loading the entire file into memory.
package buffer

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
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
type Buffer struct {
	cfg Config

	segMu   sync.Mutex
	seg     *walSegment
	segPath string

	closed atomic.Bool

	countBatches atomic.Int64
	countBytes   atomic.Int64
	countDropped atomic.Int64
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
func (b *Buffer) Write(batch *connector.SignalBatch) error {
	if b.closed.Load() {
		return fmt.Errorf("buffer is closed")
	}

	b.segMu.Lock()
	if b.seg == nil {
		if err := os.MkdirAll(b.cfg.Dir, 0700); err != nil {
			b.segMu.Unlock()
			return fmt.Errorf("buffer: create dir %s: %w", b.cfg.Dir, err)
		}
		p := segmentPath(b.cfg.Dir)
		seg, err := openSegment(p)
		if err != nil {
			b.segMu.Unlock()
			return fmt.Errorf("buffer: open segment: %w", err)
		}
		b.seg = seg
		b.segPath = p
	}
	b.segMu.Unlock()

	n, err := appendRecord(b.seg, batch)
	if err != nil {
		return fmt.Errorf("buffer: append record: %w", err)
	}

	// Rotate segment if over size limit.
	b.segMu.Lock()
	size, sizeErr := segmentSizeBytes(b.segPath)
	if sizeErr == nil && size > int64(b.cfg.MaxSizeMB)*1024*1024 {
		// Close old segment and open a fresh one.
		if b.seg != nil {
			_ = b.seg.file.Sync()
			_ = b.seg.file.Close()
			b.seg = nil
		}
		p := segmentPath(b.cfg.Dir)
		seg, openErr := openSegment(p)
		if openErr == nil {
			b.seg = seg
			b.segPath = p
		}
	}
	b.segMu.Unlock()

	b.countBatches.Add(1)
	b.countBytes.Add(int64(n))
	return nil
}

// Start begins the drain loop in a background goroutine.
// The loop reads buffered batches and delivers them via the provided drain function.
// drain is called with each batch; a nil error return triggers WAL record marking.
func (b *Buffer) Start(ctx context.Context, drain func(ctx context.Context, batch *connector.SignalBatch) error) {
	go b.drainLoop(ctx, drain)
}

func (b *Buffer) drainLoop(ctx context.Context, drain func(context.Context, *connector.SignalBatch) error) {
	ticker := time.NewTicker(b.cfg.FlushInterval)
	defer ticker.Stop()
	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = b.drainOnce(ctx, drain, &attempt)
		}
	}
}

// drainOnce streams all unconsumed records and calls drain on each.
// On success, marks the record consumed and resets attempt.
// On failure, applies exponential backoff.
func (b *Buffer) drainOnce(ctx context.Context, drain func(context.Context, *connector.SignalBatch) error, attempt *int) error {
	b.segMu.Lock()
	path := b.segPath
	b.segMu.Unlock()

	if path == "" {
		return nil
	}

	return streamRecords(path, func(offset int64, batch *connector.SignalBatch) error {
		if err := drain(ctx, batch); err == nil {
			_ = markConsumed(path, offset)
			*attempt = 0
			return nil
		}
		*attempt++
		shift := *attempt
		if shift > 20 {
			shift = 20
		}
		wait := b.cfg.BackoffBase * (1 << shift)
		if wait > b.cfg.BackoffMax {
			wait = b.cfg.BackoffMax
		}
		if b.cfg.BackoffJitter > 0 {
			wait += time.Duration(rand.Int63n(int64(b.cfg.BackoffJitter)))
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		return fmt.Errorf("drain failed, backing off")
	})
}

// Flush synchronously drains all buffered batches. Intended for graceful shutdown.
func (b *Buffer) Flush(ctx context.Context, drain func(context.Context, *connector.SignalBatch) error) error {
	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		b.segMu.Lock()
		path := b.segPath
		b.segMu.Unlock()
		if path == "" {
			return nil
		}
		err := b.drainOnce(ctx, drain, &attempt)
		if err == nil {
			return nil
		}
	}
}

// Close stops the drain loop and closes open file handles.
func (b *Buffer) Close() error {
	b.closed.Store(true)
	b.segMu.Lock()
	defer b.segMu.Unlock()
	if b.seg != nil {
		_ = b.seg.file.Sync()
		_ = b.seg.file.Close()
		b.seg = nil
	}
	return nil
}

// Stats returns current buffer metrics for Prometheus exposure.
func (b *Buffer) Stats() map[string]int64 {
	return map[string]int64{
		"buffered_batches": b.countBatches.Load(),
		"buffered_bytes":   b.countBytes.Load(),
		"dropped_batches":  b.countDropped.Load(),
	}
}
