package resilience

import (
	"context"
	"sync"
	"time"
)

// TokenBucket implements a token bucket rate limiter
type TokenBucket struct {
	mu              sync.Mutex
	rate            float64       // Tokens per second
	maxTokens       float64       // Maximum bucket capacity
	tokens          float64       // Current tokens in bucket
	lastTokenTime   time.Time     // Last time tokens were refilled
	totalAllowed    int64
	totalDenied     int64
}

// NewTokenBucket creates a new token bucket rate limiter
// rate: tokens per second (e.g., 10000 for 10K/sec)
// maxTokens: max burst size (e.g., 1000 for 1K burst)
func NewTokenBucket(rate float64, maxTokens float64) *TokenBucket {
	return &TokenBucket{
		rate:          rate,
		maxTokens:     maxTokens,
		tokens:        maxTokens, // Start at full capacity
		lastTokenTime: time.Now(),
	}
}

// Allow checks if an operation is allowed (non-blocking)
func (tb *TokenBucket) Allow() bool {
	return tb.AllowN(1)
}

// AllowN checks if N operations are allowed (non-blocking)
func (tb *TokenBucket) AllowN(n float64) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	// Refill tokens based on elapsed time
	now := time.Now()
	elapsed := now.Sub(tb.lastTokenTime).Seconds()
	tb.tokens += elapsed * tb.rate

	if tb.tokens > tb.maxTokens {
		tb.tokens = tb.maxTokens
	}

	tb.lastTokenTime = now

	// Check if we have enough tokens
	if tb.tokens >= n {
		tb.tokens -= n
		tb.totalAllowed++
		return true
	}

	tb.totalDenied++
	return false
}

// WaitN waits until N tokens are available (blocking)
func (tb *TokenBucket) WaitN(ctx context.Context, n float64) error {
	for {
		if tb.AllowN(n) {
			return nil
		}

		// Wait a bit before trying again
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// Wait waits until 1 token is available (blocking)
func (tb *TokenBucket) Wait(ctx context.Context) error {
	return tb.WaitN(ctx, 1)
}

// Metrics returns rate limiter metrics
func (tb *TokenBucket) Metrics() map[string]interface{} {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	return map[string]interface{}{
		"current_tokens": tb.tokens,
		"max_tokens":     tb.maxTokens,
		"rate_per_sec":   tb.rate,
		"total_allowed":  tb.totalAllowed,
		"total_denied":   tb.totalDenied,
	}
}

// Reset resets the rate limiter to full capacity
func (tb *TokenBucket) Reset() {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.tokens = tb.maxTokens
	tb.lastTokenTime = time.Now()
}

// RateLimiterPool manages multiple rate limiters per resource
type RateLimiterPool struct {
	mu        sync.RWMutex
	limiters  map[string]*TokenBucket
	rate      float64
	maxTokens float64
}

// NewRateLimiterPool creates a new rate limiter pool
func NewRateLimiterPool(rate float64, maxTokens float64) *RateLimiterPool {
	return &RateLimiterPool{
		limiters:  make(map[string]*TokenBucket),
		rate:      rate,
		maxTokens: maxTokens,
	}
}

// GetLimiter gets or creates a limiter for the given resource
func (rp *RateLimiterPool) GetLimiter(resource string) *TokenBucket {
	rp.mu.RLock()
	if limiter, ok := rp.limiters[resource]; ok {
		rp.mu.RUnlock()
		return limiter
	}
	rp.mu.RUnlock()

	rp.mu.Lock()
	defer rp.mu.Unlock()

	// Double-check after acquiring lock
	if limiter, ok := rp.limiters[resource]; ok {
		return limiter
	}

	limiter := NewTokenBucket(rp.rate, rp.maxTokens)
	rp.limiters[resource] = limiter
	return limiter
}

// Allow checks if operation is allowed for the resource
func (rp *RateLimiterPool) Allow(resource string) bool {
	return rp.GetLimiter(resource).Allow()
}

// AllowN checks if N operations are allowed
func (rp *RateLimiterPool) AllowN(resource string, n float64) bool {
	return rp.GetLimiter(resource).AllowN(n)
}
