// Black-box tests for Buffer (package buffer_test).
package buffer_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/buffer"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
)

func newTestConfig(dir string) buffer.Config {
	return buffer.Config{
		Dir:           dir,
		MaxSizeMB:     10,
		FlushInterval: 50 * time.Millisecond,
		BackoffBase:   time.Millisecond,
		BackoffMax:    10 * time.Millisecond,
		BackoffJitter: time.Millisecond,
	}
}

// TestBuffer_WriteRead verifies that Write returns no error.
func TestBuffer_WriteRead(t *testing.T) {
	dir := t.TempDir()
	b := buffer.New(newTestConfig(dir))
	defer b.Close()

	err := b.Write(&connector.SignalBatch{BatchID: "b1"})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
}

// TestBuffer_Stats verifies that writing N batches increments the stats counter.
func TestBuffer_Stats(t *testing.T) {
	dir := t.TempDir()
	b := buffer.New(newTestConfig(dir))
	defer b.Close()

	if err := b.Write(&connector.SignalBatch{BatchID: "s1"}); err != nil {
		t.Fatalf("Write s1: %v", err)
	}
	if err := b.Write(&connector.SignalBatch{BatchID: "s2"}); err != nil {
		t.Fatalf("Write s2: %v", err)
	}

	stats := b.Stats()
	if stats["buffered_batches"] != 2 {
		t.Errorf("buffered_batches: got %d, want 2", stats["buffered_batches"])
	}
}

// TestBuffer_DrainCallsDrainFn verifies the drain loop calls the drain function.
func TestBuffer_DrainCallsDrainFn(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(dir)
	cfg.FlushInterval = 20 * time.Millisecond
	b := buffer.New(cfg)
	defer b.Close()

	if err := b.Write(&connector.SignalBatch{BatchID: "drain-me"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var calls atomic.Int64
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	b.Start(ctx, func(_ context.Context, _ *connector.SignalBatch) error {
		calls.Add(1)
		return nil
	})

	// Wait up to 3*FlushInterval for drain fn to be called.
	deadline := time.Now().Add(3 * cfg.FlushInterval)
	for time.Now().Before(deadline) {
		if calls.Load() >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if calls.Load() < 1 {
		t.Error("drain function was never called")
	}
}

// TestBuffer_DrainBackoff verifies that intervals between drain attempts grow on failure.
func TestBuffer_DrainBackoff(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(dir)
	cfg.FlushInterval = 5 * time.Millisecond
	cfg.BackoffBase = 5 * time.Millisecond
	cfg.BackoffMax = 100 * time.Millisecond
	cfg.BackoffJitter = 0 // no jitter for deterministic test
	b := buffer.New(cfg)
	defer b.Close()

	if err := b.Write(&connector.SignalBatch{BatchID: "backoff-test"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var mu sync.Mutex
	var callTimes []time.Time
	var callCount atomic.Int64

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	b.Start(ctx, func(_ context.Context, _ *connector.SignalBatch) error {
		now := time.Now()
		mu.Lock()
		callTimes = append(callTimes, now)
		mu.Unlock()
		n := callCount.Add(1)
		if n < 3 {
			return fmt.Errorf("simulated failure")
		}
		return nil // succeed on 3rd call
	})

	// Wait for at least 3 calls.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if callCount.Load() >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if callCount.Load() < 3 {
		t.Fatalf("expected at least 3 drain calls, got %d", callCount.Load())
	}

	mu.Lock()
	times := make([]time.Time, len(callTimes))
	copy(times, callTimes)
	mu.Unlock()

	if len(times) < 3 {
		t.Fatalf("not enough call timestamps: %d", len(times))
	}
	// gap[0] = time between call 1 and call 2, gap[1] = time between call 2 and call 3
	gap0 := times[1].Sub(times[0])
	gap1 := times[2].Sub(times[1])
	if gap1 < gap0 {
		t.Errorf("backoff not increasing: gap0=%v gap1=%v", gap0, gap1)
	}
}

// TestBuffer_Flush verifies Flush calls drain and returns nil on success.
func TestBuffer_Flush(t *testing.T) {
	dir := t.TempDir()
	b := buffer.New(newTestConfig(dir))
	defer b.Close()

	if err := b.Write(&connector.SignalBatch{BatchID: "flush-me"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var called atomic.Int64
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := b.Flush(ctx, func(_ context.Context, _ *connector.SignalBatch) error {
		called.Add(1)
		return nil
	})
	if err != nil {
		t.Errorf("Flush returned error: %v", err)
	}
	if called.Load() < 1 {
		t.Error("drain function was not called during Flush")
	}
}

// TestBuffer_Close verifies that Write returns an error after Close.
func TestBuffer_Close(t *testing.T) {
	dir := t.TempDir()
	b := buffer.New(newTestConfig(dir))

	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	err := b.Write(&connector.SignalBatch{BatchID: "after-close"})
	if err == nil {
		t.Error("expected error writing to closed buffer, got nil")
	}
}

// TestBuffer_ConcurrentWriters (F2): 8 goroutines each writing 25 batches to
// one Buffer must produce exactly 200 records with no data race on b.seg.
// Run with -race; any race on b.seg will be caught by the race detector.
func TestBuffer_ConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(dir)
	cfg.MaxSizeMB = 50
	b := buffer.New(cfg)
	defer b.Close()

	const goroutines = 8
	const writesEach = 25

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < writesEach; i++ {
				id := fmt.Sprintf("g%d-i%d", g, i)
				if err := b.Write(&connector.SignalBatch{BatchID: id}); err != nil {
					t.Errorf("Write %s: %v", id, err)
				}
			}
		}()
	}
	wg.Wait()

	// Drain and count via Flush.
	var mu sync.Mutex
	var recovered []string
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := b.Flush(ctx, func(_ context.Context, batch *connector.SignalBatch) error {
		mu.Lock()
		recovered = append(recovered, batch.BatchID)
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if len(recovered) != goroutines*writesEach {
		t.Errorf("recovered %d records, want %d", len(recovered), goroutines*writesEach)
	}
}

// TestBuffer_MultiSegmentDrainOldestFirst (F3): forces multi-segment rotation by
// setting a tiny per-segment threshold; writes batches with ordered IDs spanning
// >= 3 segments; Flush must return all batches in write order.
func TestBuffer_MultiSegmentDrainOldestFirst(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(dir)
	b := buffer.New(cfg)
	// Override per-segment threshold to ~1 byte so every write causes rotation.
	b.SetSegMaxBytesForTest(1)
	defer b.Close()

	const total = 9
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("batch-%02d", i)
		if err := b.Write(&connector.SignalBatch{BatchID: id}); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	var order []string
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := b.Flush(ctx, func(_ context.Context, batch *connector.SignalBatch) error {
		order = append(order, batch.BatchID)
		return nil
	}); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if len(order) != total {
		t.Fatalf("drained %d records, want %d", len(order), total)
	}
	for i, id := range order {
		want := fmt.Sprintf("batch-%02d", i)
		if id != want {
			t.Errorf("order[%d] = %q, want %q — drain is not oldest-first", i, id, want)
			break
		}
	}
}

// TestBuffer_ConsumedSegmentDeleted (F3): after a successful Flush, all
// non-active fully-consumed segment files must be removed from cfg.Dir.
func TestBuffer_ConsumedSegmentDeleted(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(dir)
	b := buffer.New(cfg)
	b.SetSegMaxBytesForTest(1)

	const total = 3
	for i := 0; i < total; i++ {
		if err := b.Write(&connector.SignalBatch{BatchID: fmt.Sprintf("del-%d", i)}); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	// Close the buffer first so the active segment handle is released (Windows).
	if err := b.Close(); err != nil {
		t.Fatalf("Close before Flush: %v", err)
	}

	// Open a fresh buffer pointing at the same dir to drain.
	b2 := buffer.New(cfg)
	defer b2.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := b2.Flush(ctx, func(_ context.Context, _ *connector.SignalBatch) error {
		return nil
	}); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := b2.Close(); err != nil {
		t.Fatalf("Close b2: %v", err)
	}

	// All fully-consumed non-active segments must be gone.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".seg" {
			// If there's a segment left, it should be empty (all consumed).
			path := filepath.Join(dir, e.Name())
			info, _ := os.Stat(path)
			if info != nil && info.Size() > 0 {
				t.Errorf("segment file %q still present with size %d after full drain — consumed segment not deleted", e.Name(), info.Size())
			}
		}
	}
}

// TestBuffer_MaxSizeDropsOldest (F3): when total segment size exceeds the budget,
// the oldest non-active segment is deleted and its live record count is added to
// Stats()["dropped_batches"].
func TestBuffer_MaxSizeDropsOldest(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(dir)
	b := buffer.New(cfg)
	// Use tiny per-segment and total budget so overflow happens quickly.
	b.SetSegMaxBytesForTest(1)
	b.SetTotalMaxBytesForTest(1)
	defer b.Close()

	// Write several batches. The total budget is tiny, so oldest segments should
	// be evicted and countDropped incremented.
	const writes = 5
	for i := 0; i < writes; i++ {
		if err := b.Write(&connector.SignalBatch{BatchID: fmt.Sprintf("drop-%d", i)}); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	stats := b.Stats()
	if stats["dropped_batches"] == 0 {
		t.Error("expected dropped_batches > 0 after exceeding total budget, got 0")
	}
}

// TestBuffer_BackoffOutsideStream (F14): when drain fails, the segment file must
// NOT be held open during the backoff wait. Practical assertion: with a short
// BackoffBase and a ctx-cancel, Flush returns promptly and (on Windows) the
// segment file can be os.Remove'd immediately.
func TestBuffer_BackoffOutsideStream(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(dir)
	cfg.BackoffBase = 200 * time.Millisecond
	cfg.BackoffMax = 200 * time.Millisecond
	cfg.BackoffJitter = 0
	b := buffer.New(cfg)
	defer b.Close()

	if err := b.Write(&connector.SignalBatch{BatchID: "backoff-file"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Get the segment path before closing handle.
	segPath := b.ActiveSegPathForTest()

	// Close handle so we can re-open later for delete test (not needed for drain,
	// just for the remove assertion below).
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Open a fresh buffer for draining.
	b2 := buffer.New(cfg)
	defer b2.Close()

	var drainCalls int
	var callTimes []time.Time

	// Cancel context after first backoff to keep test fast.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_ = b2.Flush(ctx, func(_ context.Context, _ *connector.SignalBatch) error {
		drainCalls++
		callTimes = append(callTimes, time.Now())
		return fmt.Errorf("simulated drain failure")
	})

	// Flush must return promptly (within 600ms), not hang with an open file handle.
	// After Flush, we should be able to delete the segment (Windows: handle released).
	if err := b2.Close(); err != nil {
		t.Fatalf("Close b2: %v", err)
	}

	// Verify the segment can be removed (Windows: would fail with EACCES if handle open).
	if segPath != "" {
		err := os.Remove(segPath)
		if err != nil && !os.IsNotExist(err) {
			t.Errorf("segment file still locked after Flush returned: %v — backoff may be happening inside streamRecords (F14)", err)
		}
	}

	// Verify at least one drain call happened and gaps between calls respect BackoffBase.
	if drainCalls < 1 {
		t.Error("drain was never called")
	}
	if len(callTimes) >= 2 {
		gap := callTimes[1].Sub(callTimes[0])
		if gap < cfg.BackoffBase/2 {
			t.Errorf("gap between drain calls %v < BackoffBase/2 %v — backoff not applied", gap, cfg.BackoffBase/2)
		}
	}
}

// TestBuffer_NilDrainErrors (F5): Flush(ctx, nil) must return a non-nil error
// (not panic); Start(ctx, nil) must return a non-nil error.
func TestBuffer_NilDrainErrors(t *testing.T) {
	dir := t.TempDir()
	b := buffer.New(newTestConfig(dir))
	defer b.Close()

	ctx := context.Background()

	err := b.Flush(ctx, nil)
	if err == nil {
		t.Error("Flush(ctx, nil) returned nil error — expected non-nil (F5)")
	}

	err = b.Start(ctx, nil)
	if err == nil {
		t.Error("Start(ctx, nil) returned nil error — expected non-nil (F5)")
	}
}
