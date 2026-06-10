package resilience

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// CircuitState represents the state of a circuit breaker
type CircuitState int32

const (
	// CircuitClosed means the circuit is operating normally
	CircuitClosed CircuitState = iota
	// CircuitOpen means the circuit is broken and failing fast
	CircuitOpen
	// CircuitHalfOpen means the circuit is attempting recovery
	CircuitHalfOpen
)

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	name            string
	maxFailures     int
	maxAttempts     int // Half-open attempts before reset/reopen
	timeout         time.Duration
	successThreshold int

	mu               sync.RWMutex
	state            CircuitState
	failures         int
	successes        int
	lastFailureTime  time.Time
	lastStateChange time.Time

	// Metrics
	totalTrips      int64
	totalResets     int64
	totalRecovers   int64
	totalAttempts   int64
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(name string, maxFailures int, timeout time.Duration, successThreshold int) *CircuitBreaker {
	return &CircuitBreaker{
		name:             name,
		maxFailures:      maxFailures,
		maxAttempts:      3,
		timeout:          timeout,
		successThreshold: successThreshold,
		state:            CircuitClosed,
		lastStateChange: time.Now(),
	}
}

// Call executes the operation and updates circuit breaker state.
//
// F10 / T-03-14 mitigated: a single cb.mu.Lock() covers the full Open state
// check, the timeout comparison (time.Since(cb.lastFailureTime)), and the
// Open->HalfOpen transition decision. No guarded field is read while the mutex
// is unlocked, eliminating the TOCTOU window where two concurrent goroutines
// could both observe state==Open and both independently transition to HalfOpen.
func (cb *CircuitBreaker) Call(operation func() error) error {
	cb.mu.Lock()
	// F10: read state AND lastFailureTime under the same lock. Transition to
	// HalfOpen only if we are the goroutine that wins the single lock hold.
	if cb.state == CircuitOpen {
		if time.Since(cb.lastFailureTime) > cb.timeout {
			// We hold the lock — transition to HalfOpen exclusively.
			cb.state = CircuitHalfOpen
			cb.successes = 0
			cb.failures = 0
		} else {
			cb.mu.Unlock()
			return fmt.Errorf("circuit breaker %s is open", cb.name)
		}
	}
	cb.mu.Unlock()

	atomic.AddInt64(&cb.totalAttempts, 1)

	// Execute operation (no mutex held during the user-supplied operation).
	err := operation()

	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.failures++
		cb.lastFailureTime = time.Now()

		// In CLOSED state: check if failure count exceeds limit
		if cb.state == CircuitClosed {
			if cb.failures >= cb.maxFailures {
				cb.state = CircuitOpen
				atomic.AddInt64(&cb.totalTrips, 1)
				cb.lastStateChange = time.Now()
			}
		}

		// In HALF-OPEN state: any failure means reopen
		if cb.state == CircuitHalfOpen {
			cb.state = CircuitOpen
			cb.failures = 0
			cb.successes = 0
			cb.lastStateChange = time.Now()
		}

		return err
	}

	// Operation succeeded
	if cb.state == CircuitClosed {
		cb.failures = 0 // Reset failures on success
		return nil
	}

	// In HALF-OPEN state: check if we should close
	if cb.state == CircuitHalfOpen {
		cb.successes++
		if cb.successes >= cb.successThreshold {
			cb.state = CircuitClosed
			cb.failures = 0
			cb.successes = 0
			atomic.AddInt64(&cb.totalRecovers, 1)
			cb.lastStateChange = time.Now()
		}
	}

	return nil
}

// State returns the current circuit breaker state
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Reset manually resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = CircuitClosed
	cb.failures = 0
	cb.successes = 0
	atomic.AddInt64(&cb.totalResets, 1)
	cb.lastStateChange = time.Now()
}

// IsOpen returns whether the circuit breaker is open
func (cb *CircuitBreaker) IsOpen() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state == CircuitOpen
}

// IsClosed returns whether the circuit breaker is closed
func (cb *CircuitBreaker) IsClosed() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state == CircuitClosed
}

// IsHalfOpen returns whether the circuit breaker is half-open
func (cb *CircuitBreaker) IsHalfOpen() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state == CircuitHalfOpen
}

// Metrics returns circuit breaker metrics
func (cb *CircuitBreaker) Metrics() map[string]interface{} {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	state := "CLOSED"
	if cb.state == CircuitOpen {
		state = "OPEN"
	} else if cb.state == CircuitHalfOpen {
		state = "HALF_OPEN"
	}

	return map[string]interface{}{
		"state":       state,
		"failures":    cb.failures,
		"successes":   cb.successes,
		"total_trips": atomic.LoadInt64(&cb.totalTrips),
		"total_resets": atomic.LoadInt64(&cb.totalResets),
		"total_recovers": atomic.LoadInt64(&cb.totalRecovers),
		"total_attempts": atomic.LoadInt64(&cb.totalAttempts),
	}
}
