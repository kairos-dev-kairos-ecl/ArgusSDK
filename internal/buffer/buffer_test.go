// Black-box tests for Buffer (package buffer_test).
package buffer_test

import (
	"context"
	"fmt"
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
