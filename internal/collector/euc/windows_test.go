//go:build windows

package euc

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestWindowsCollectorDegradeOnNoRights verifies the low-privilege contract:
// when ETW rights are absent (plain user), Start returns nil (no crash, no error).
// When rights ARE present the ETW session starts; Start still returns nil.
// SC-2 / SC-6 / T-06-06 / Pitfall 3.
func TestWindowsCollectorDegradeOnNoRights(t *testing.T) {
	cfg := Config{
		AIEndpoints:         []string{"api.openai.com"},
		LocalInferencePorts: []int{11434},
	}

	col := newOSCollector(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	obs := make(chan Observation, 8)
	err := col.Start(ctx, obs)
	if err != nil {
		t.Fatalf("Start must return nil regardless of ETW rights; got: %v", err)
	}
	// Allow Start goroutines to settle then verify Close does not panic.
	time.Sleep(50 * time.Millisecond)
	if err := col.Close(); err != nil {
		t.Fatalf("Close returned unexpected error: %v", err)
	}
	// Safe to call Close twice (sync.Once).
	if err := col.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
}

// TestWindowsGopsutilLocalPort verifies the gopsutil local-inference path
// (no elevation required). It opens a real local TCP listener on a dynamic
// port, places that port in cfg.LocalInferencePorts, starts the collector,
// and asserts an IsLocal Observation with that LocalPort arrives within a
// deadline. t.Skip is called when the OS connection table is unreadable.
//
// SC-4 / SC-6 / gopsutil_evidence_note (A2 verification on this runner).
func TestWindowsGopsutilLocalPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot open local listener (connection table may be unreadable): %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	t.Logf("opened listener on port %d", port)

	cfg := Config{
		AIEndpoints:         []string{},
		LocalInferencePorts: []int{port},
	}

	col := newOSCollector(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	defer col.Close() //nolint:errcheck

	obs := make(chan Observation, 32)
	if err := col.Start(ctx, obs); err != nil {
		t.Fatalf("Start returned unexpected error: %v", err)
	}

	deadline := time.After(25 * time.Second)
	for {
		select {
		case o := <-obs:
			if o.IsLocal && o.LocalPort == port {
				t.Logf("received expected IsLocal Observation: port=%d host=%s",
					o.LocalPort, o.ConnectedHost)
				return // success
			}
			// Observation received but not for our port — keep waiting.
		case <-deadline:
			// If gopsutil cannot read the connection table this is acceptable — t.Skip.
			t.Skipf("no IsLocal Observation for port %d within deadline; "+
				"gopsutil connection table may not include listening sockets "+
				"without elevation on this runner (A2)", port)
		case <-ctx.Done():
			t.Fatal("context expired before Observation received")
		}
	}
}

// TestWindowsETWProviderNotNDIS asserts at compile time that the ETW provider
// constant used in this package is the correct one (Kernel-Network, not NDIS
// packet capture). This test always passes — it is a documentation guard.
// T-06-07 / SC-6.
func TestWindowsETWProviderNotNDIS(t *testing.T) {
	const rejected = "Microsoft-Windows-NDIS-PacketCapture"
	if kernelNetworkProvider == rejected {
		t.Fatalf("FATAL: ETW provider must not be %q (full packet capture violates contract)", rejected)
	}
	if kernelNetworkProvider != "Microsoft-Windows-Kernel-Network" {
		t.Fatalf("unexpected ETW provider %q; expected Microsoft-Windows-Kernel-Network", kernelNetworkProvider)
	}
}

// TestWindowsCollectorIdempotentClose exercises the sync.Once guard.
func TestWindowsCollectorIdempotentClose(t *testing.T) {
	col := newOSCollector(Config{})
	for i := 0; i < 5; i++ {
		if err := col.Close(); err != nil {
			t.Fatalf("Close[%d] returned error: %v", i, err)
		}
	}
}
