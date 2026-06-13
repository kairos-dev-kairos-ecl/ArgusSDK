// Package buffer implements the local signal buffer used during XDR connectivity
// loss. Signals are written to a write-ahead log (WAL) so that no data is lost
// across agent restarts or unexpected termination.
//
// # WAL record format
//
// Each record is: [1-byte status][4-byte big-endian uint32 length][gob payload].
// Status 0x00 = live; 0xFF = consumed. markConsumed writes ONLY the status byte,
// leaving the length prefix intact for skip-on-read. This resolves F1 (>=16 MB
// payload corruption) and F2 (data race) from the 2026-06-10 review.
//
// # Multi-segment drain
//
// On every drain attempt, ALL wal-<unix>-<seq>.seg files in cfg.Dir are
// enumerated oldest-first and drained in order. Fully-consumed non-active
// segments are deleted. When total segment size exceeds MaxSizeMB, the oldest
// non-active segment is evicted and its live record count added to countDropped.
// This resolves F3 (rotated segments stranded forever) from the 2026-06-10 review.
//
// # Backoff
//
// Backoff waits happen in drainLoop/Flush AFTER all file handles are closed —
// never inside the streamRecords callback. This resolves F14 from the review.
//
// # Nil-drain guard
//
// Start and Flush return an error immediately if drain is nil. This resolves F5
// (agent.stop nil-drain panic) from the 2026-06-10 review.
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

// errDrainFailed is a sentinel returned by drainOnce when the drain callback
// fails; callers compute backoff and retry outside the stream.
var errDrainFailed = fmt.Errorf("drain failed")

// Config holds buffer tuning parameters. The mapstructure tags bind the
// snake_case keys used in agent.yaml (loaded via viper) to these fields.
type Config struct {
	// Dir is the directory where WAL segment files are written.
	// The agent creates it if it does not exist.
	Dir string `mapstructure:"dir"`

	// MaxSizeMB is the maximum total disk space the buffer may use.
	// When exceeded, the oldest segments are dropped. Default: 256.
	MaxSizeMB int `mapstructure:"max_size_mb"`

	// FlushInterval is how often the agent attempts to drain the buffer
	// to the configured output connectors when connectivity is restored. Default: 5s.
	FlushInterval time.Duration `mapstructure:"flush_interval"`

	// DrainOnReconnect enables eager drain as soon as a connector reports healthy.
	// If false, drain is governed solely by FlushInterval. Default: true.
	DrainOnReconnect bool `mapstructure:"drain_on_reconnect"`

	// BackoffBase is the initial reconnect backoff duration. Default: 2s.
	BackoffBase time.Duration `mapstructure:"backoff_base"`

	// BackoffMax is the maximum backoff duration after repeated failures. Default: 5m.
	BackoffMax time.Duration `mapstructure:"backoff_max"`

	// BackoffJitter is the maximum random jitter added per backoff step. Default: 30s.
	BackoffJitter time.Duration `mapstructure:"backoff_jitter"`
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

	// segMaxBytes is the per-segment rotation threshold.
	// Initialized from cfg.MaxSizeMB; overridable in white-box tests.
	segMaxBytes int64

	// totalMaxBytes is the total budget across all segments.
	// Initialized from cfg.MaxSizeMB; overridable in white-box tests.
	totalMaxBytes int64

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
	totalBytes := int64(cfg.MaxSizeMB) * 1024 * 1024
	// Per-segment threshold = 1/4 of total budget, minimum 1 MB.
	segBytes := totalBytes / 4
	if segBytes < 1024*1024 {
		segBytes = 1024 * 1024
	}
	return &Buffer{
		cfg:           cfg,
		segMaxBytes:   segBytes,
		totalMaxBytes: totalBytes,
	}
}

// SetSegMaxBytesForTest overrides the per-segment rotation threshold.
// This is an exported test hook for white-box tests in package buffer_test.
func (b *Buffer) SetSegMaxBytesForTest(n int64) {
	b.segMu.Lock()
	b.segMaxBytes = n
	b.segMu.Unlock()
}

// SetTotalMaxBytesForTest overrides the total segment budget.
// This is an exported test hook for white-box tests in package buffer_test.
func (b *Buffer) SetTotalMaxBytesForTest(n int64) {
	b.segMu.Lock()
	b.totalMaxBytes = n
	b.segMu.Unlock()
}

// ActiveSegPathForTest returns the current active segment path (may be empty).
// This is an exported test hook for white-box tests in package buffer_test.
func (b *Buffer) ActiveSegPathForTest() string {
	b.segMu.Lock()
	defer b.segMu.Unlock()
	return b.segPath
}

// Write appends a batch to the WAL. Returns error only on unrecoverable I/O failure.
// Write is safe for concurrent calls from multiple collector goroutines.
//
// F2 fix: segMu is held for the ENTIRE write (open/create, appendRecord, size check,
// rotation). This removes the unlock-then-use window that caused the data race on b.seg.
func (b *Buffer) Write(batch *connector.SignalBatch) error {
	if b.closed.Load() {
		return fmt.Errorf("buffer is closed")
	}

	b.segMu.Lock()
	defer b.segMu.Unlock()

	// Open or create the active segment.
	if b.seg == nil {
		if err := os.MkdirAll(b.cfg.Dir, 0700); err != nil {
			return fmt.Errorf("buffer: create dir %s: %w", b.cfg.Dir, err)
		}
		p := segmentPath(b.cfg.Dir)
		seg, err := openSegment(p)
		if err != nil {
			return fmt.Errorf("buffer: open segment: %w", err)
		}
		b.seg = seg
		b.segPath = p
	}

	n, err := appendRecord(b.seg, batch)
	if err != nil {
		return fmt.Errorf("buffer: append record: %w", err)
	}

	b.countBatches.Add(1)
	b.countBytes.Add(int64(n))

	// Rotate segment if over per-segment size limit.
	size, sizeErr := segmentSizeBytes(b.segPath)
	if sizeErr == nil && size > b.segMaxBytes {
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

	// F3 total enforcement: if total segment size exceeds budget, evict the oldest
	// non-active segment and add its live record count to countDropped.
	b.enforceTotal()

	return nil
}

// enforceTotal must be called under segMu. It evicts the oldest non-active
// segment(s) until total size is within totalMaxBytes.
func (b *Buffer) enforceTotal() {
	for {
		refs, err := listSegments(b.cfg.Dir)
		if err != nil || len(refs) == 0 {
			return
		}

		var total int64
		for _, r := range refs {
			sz, _ := segmentSizeBytes(r.path)
			total += sz
		}
		if total <= b.totalMaxBytes {
			return
		}

		// Evict the oldest non-active segment.
		for _, r := range refs {
			if r.path == b.segPath {
				continue // skip active segment
			}
			// Count live records before deletion for drop accounting.
			live, _ := countLiveRecords(r.path)
			if err := os.Remove(r.path); err == nil {
				b.countDropped.Add(int64(live))
			}
			break // remove one at a time and re-evaluate
		}
	}
}

// Start begins the drain loop in a background goroutine.
// Returns an error if drain is nil (F5 fix).
func (b *Buffer) Start(ctx context.Context, drain func(ctx context.Context, batch *connector.SignalBatch) error) error {
	if drain == nil {
		return fmt.Errorf("buffer: Start requires a non-nil drain func")
	}
	go b.drainLoop(ctx, drain)
	return nil
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
			err := b.drainOnce(ctx, drain)
			if err == nil {
				attempt = 0
			} else {
				// F14 fix: backoff waits happen here, AFTER drainOnce returns
				// (all file handles closed), never inside the streamRecords callback.
				attempt++
				wait := b.backoffWait(attempt)
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
			}
		}
	}
}

// drainOnce enumerates ALL wal-*.seg segments oldest-first and drains each.
// On drain callback success: marks the record consumed; continues.
// On drain callback failure: returns errDrainFailed immediately (no sleep — F14).
// After each segment: if no live records remain and it is not the active segment,
// the segment file is removed.
func (b *Buffer) drainOnce(ctx context.Context, drain func(context.Context, *connector.SignalBatch) error) error {
	refs, err := listSegments(b.cfg.Dir)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		return nil
	}

	b.segMu.Lock()
	activePath := b.segPath
	b.segMu.Unlock()

	for _, ref := range refs {
		path := ref.path
		var drainErr error

		// Open the segment once for marking consumed records (reuse across all records
		// in this segment to avoid repeated open/seek/close overhead on Windows).
		markFile, markErr := os.OpenFile(path, os.O_RDWR, 0600)
		if markErr != nil {
			// Cannot open for marking — skip this segment.
			continue
		}

		streamErr := streamRecords(path, func(offset int64, batch *connector.SignalBatch) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if err := drain(ctx, batch); err != nil {
				drainErr = err
				return errDrainFailed // abort the stream immediately (F14: no sleep here)
			}
			_ = markConsumedAt(markFile, offset)
			return nil
		})

		// Sync and close the mark file (all handles must be closed before delete/rename on Windows).
		_ = markFile.Sync()
		_ = markFile.Close()

		// Handle stream errors.
		if streamErr == errDrainFailed {
			return errDrainFailed
		}
		if streamErr != nil {
			// ctx cancelled or real I/O error — propagate.
			return streamErr
		}
		if drainErr != nil {
			return errDrainFailed
		}

		// Check if the segment can be deleted (all records consumed, not active).
		if path != activePath {
			live, err := countLiveRecords(path)
			if err == nil && live == 0 {
				_ = os.Remove(path)
			}
		}
	}
	return nil
}

// backoffWait computes the backoff duration for the given attempt number.
func (b *Buffer) backoffWait(attempt int) time.Duration {
	shift := attempt
	if shift > 20 {
		shift = 20
	}
	wait := b.cfg.BackoffBase * (1 << uint(shift))
	if b.cfg.BackoffMax > 0 && wait > b.cfg.BackoffMax {
		wait = b.cfg.BackoffMax
	}
	if b.cfg.BackoffJitter > 0 {
		wait += time.Duration(rand.Int63n(int64(b.cfg.BackoffJitter)))
	}
	return wait
}

// Flush synchronously drains all buffered batches. Intended for graceful shutdown.
// Returns an error if drain is nil (F5 fix).
func (b *Buffer) Flush(ctx context.Context, drain func(context.Context, *connector.SignalBatch) error) error {
	if drain == nil {
		return fmt.Errorf("buffer: Flush requires a non-nil drain func")
	}
	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := b.drainOnce(ctx, drain)
		if err == nil {
			// Check if any live records remain across all segments.
			refs, _ := listSegments(b.cfg.Dir)
			anyLive := false
			for _, ref := range refs {
				n, _ := countLiveRecords(ref.path)
				if n > 0 {
					anyLive = true
					break
				}
			}
			if !anyLive {
				return nil
			}
			continue
		}
		if err == errDrainFailed {
			// F14 fix: backoff wait happens here after all handles closed.
			attempt++
			wait := b.backoffWait(attempt)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			continue
		}
		return err
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
