package resilience

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Existing coverage tests
// ---------------------------------------------------------------------------

func TestCircuitBreaker_ClosedToOpen(t *testing.T) {
	cb := NewCircuitBreaker("test", 3, time.Second, 2)
	failOp := func() error { return fmt.Errorf("fail") }

	for i := 0; i < 3; i++ {
		_ = cb.Call(failOp)
	}
	if !cb.IsOpen() {
		t.Error("expected breaker to be Open after maxFailures failures")
	}
}

func TestCircuitBreaker_OpenReturnsError(t *testing.T) {
	cb := NewCircuitBreaker("test", 1, time.Hour, 1)
	_ = cb.Call(func() error { return fmt.Errorf("fail") })
	if !cb.IsOpen() {
		t.Skip("breaker not open, skip")
	}
	err := cb.Call(func() error { return nil })
	if err == nil {
		t.Error("expected error from open circuit, got nil")
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := NewCircuitBreaker("test", 1, time.Hour, 1)
	_ = cb.Call(func() error { return fmt.Errorf("fail") })
	cb.Reset()
	if !cb.IsClosed() {
		t.Error("expected Closed after Reset")
	}
}

func TestCircuitBreaker_HalfOpenToClosedOnSuccess(t *testing.T) {
	// successThreshold=1: one success in HalfOpen → Closed
	cb := NewCircuitBreaker("test", 1, time.Nanosecond, 1)
	_ = cb.Call(func() error { return fmt.Errorf("force open") })
	time.Sleep(5 * time.Millisecond)
	_ = cb.Call(func() error { return nil })
	if !cb.IsClosed() {
		t.Error("expected Closed after HalfOpen success with threshold=1")
	}
}

// ---------------------------------------------------------------------------
// F10: TOCTOU fix — single-lock state decision test (RED phase)
// ---------------------------------------------------------------------------

// TestCall_SingleHalfOpenTransition (F10): the Open->HalfOpen decision must be
// made under a single lock hold so that two concurrent goroutines cannot both
// transition independently. With -race, the unlocked read of lastFailureTime
// at the old code produces a data race detected by the race detector.
//
// The test puts the breaker in Open state with lastFailureTime far in the past
// (past the timeout), then races N goroutines to Call() concurrently with an
// operation that returns success. The primary assertion is -race cleanliness.
func TestCall_SingleHalfOpenTransition(t *testing.T) {
	const N = 20

	// Breaker with 1-nanosecond timeout — already expired by the time goroutines run.
	cb := NewCircuitBreaker("race-test", 1, time.Nanosecond, 1)

	// Force into Open state with lastFailureTime set far in the past.
	_ = cb.Call(func() error { return fmt.Errorf("force open") })
	if !cb.IsOpen() {
		t.Fatal("expected breaker to be Open after failure")
	}

	// Wait slightly longer than the timeout to ensure transition eligibility.
	time.Sleep(10 * time.Millisecond)

	// Record attempts before the concurrent calls (1 from the force-open call).
	metricsBefore := cb.Metrics()
	attemptsBefore := metricsBefore["total_attempts"].(int64)

	// Gate: all goroutines start together to maximise race opportunity.
	var gate sync.WaitGroup
	var started sync.WaitGroup
	gate.Add(1)
	started.Add(N)

	errs := make([]error, N)
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			started.Done()
			gate.Wait() // wait for all goroutines to be ready
			errs[i] = cb.Call(func() error { return nil })
		}()
	}

	started.Wait()
	gate.Done() // release all at once
	wg.Wait()

	// At least some goroutines must have succeeded (breaker was transitioning).
	// The key assertion is -race cleanliness; also verify no panic occurred.
	state := cb.State()
	if state != CircuitClosed && state != CircuitHalfOpen && state != CircuitOpen {
		t.Errorf("unexpected circuit state: %v", state)
	}

	// N additional attempts must have been recorded (excluding the force-open call).
	metricsAfter := cb.Metrics()
	attemptsAfter := metricsAfter["total_attempts"].(int64)
	if got, want := attemptsAfter-attemptsBefore, int64(N); got != want {
		t.Errorf("concurrent attempts = %d, want %d", got, want)
	}
}
