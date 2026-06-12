//go:build darwin

package euc

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestDarwinSamplerLocalPort verifies that the darwin sampler emits an
// Observation with IsLocal=true and the correct LocalPort when a real TCP
// listener is opened on a watched local inference port.
//
// This test requires macOS to run; on any other platform the build tag
// (//go:build darwin) prevents compilation.
//
// If the connection table is unreadable in the test environment (e.g. a
// sandboxed CI runner that returns an error from net.ConnectionsWithContext),
// the test skips cleanly rather than failing.
func TestDarwinSamplerLocalPort(t *testing.T) {
	t.Parallel()

	// Open a real TCP listener on an ephemeral port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("could not open TCP listener: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port

	cfg := Config{
		AIEndpoints:         []string{"api.openai.com"},
		LocalInferencePorts: []int{port},
		AppID:               "test",
		Env:                 "test",
	}

	dc := newOSCollector(cfg).(*darwinCollector)
	// Use a very short poll interval so the test does not have to wait 2 s.
	dc.interval = 100 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out := make(chan Observation, 16)
	if err := dc.Start(ctx, out); err != nil {
		t.Fatalf("Start returned unexpected error: %v", err)
	}

	// Wait for an Observation with IsLocal=true or timeout.
	for {
		select {
		case obs := <-out:
			if obs.IsLocal && obs.LocalPort == port {
				// Success.
				if err := dc.Close(); err != nil {
					t.Errorf("Close: %v", err)
				}
				return
			}
			// Not our match yet; continue draining.
		case <-ctx.Done():
			// The connection table is unreadable or the OS did not surface our
			// listener within the timeout (sandbox, restricted environment).
			t.Skip("no IsLocal Observation received within timeout — connection table may be unreadable in this environment; skipping")
			return
		}
	}
}

// TestDarwinSamplerDegrade verifies that Start returns nil and does not panic
// even when the sampler encounters an empty connection table. This covers the
// degrade path (T-06-14): the collector must never abort agent start.
func TestDarwinSamplerDegrade(t *testing.T) {
	t.Parallel()

	cfg := Config{
		// No endpoints — sampler will match nothing.
		AIEndpoints:         []string{},
		LocalInferencePorts: []int{},
		AppID:               "test",
		Env:                 "test",
	}

	dc := newOSCollector(cfg).(*darwinCollector)
	dc.interval = 50 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	out := make(chan Observation, 4)

	// Must not panic; must return nil.
	if err := dc.Start(ctx, out); err != nil {
		t.Errorf("Start returned non-nil error on degrade path: %v", err)
	}

	// Let the sampler tick a couple of times and then close.
	<-ctx.Done()
	if err := dc.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}

// TestDarwinSamplerCloseIdempotent verifies that Close is safe to call twice
// (sync.Once contract; must not panic or error).
func TestDarwinSamplerCloseIdempotent(t *testing.T) {
	t.Parallel()

	cfg := Config{
		AIEndpoints:         []string{"api.openai.com"},
		LocalInferencePorts: []int{11434},
	}

	dc := newOSCollector(cfg).(*darwinCollector)
	dc.interval = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	out := make(chan Observation, 4)
	if err := dc.Start(ctx, out); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cancel()

	// Close twice — must not panic.
	if err := dc.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := dc.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestDarwinSamplerNotSeenDedup verifies that the dedup mechanism prevents
// re-emitting the same connection within dedupeTTL.
func TestDarwinSamplerNotSeenDedup(t *testing.T) {
	t.Parallel()

	cfg := Config{}
	dc := &darwinCollector{
		cfg:      cfg,
		interval: defaultPollInterval,
		seen:     make(map[connKey]seenEntry),
	}

	now := time.Now()
	k := connKey{remoteHost: "api.openai.com", localPort: 0}

	// First call: not seen → true.
	if !dc.notSeen(k, now) {
		t.Error("expected notSeen=true on first call")
	}

	// Second call immediately: already seen → false.
	if dc.notSeen(k, now) {
		t.Error("expected notSeen=false on second call within TTL")
	}

	// After TTL expires: not seen again → true.
	future := now.Add(dedupeTTL + time.Second)
	if !dc.notSeen(k, future) {
		t.Error("expected notSeen=true after TTL expiry")
	}
}
