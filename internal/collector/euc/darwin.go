//go:build darwin

package euc

import "context"

// darwinCollector watches AI service connections on macOS using the
// Network Extension framework (NEDNSProxyProvider or NEFilterDataProvider).
//
// Implementation TODO:
//   - Use the macOS Network Extension framework via CGo or a pre-compiled
//     helper process to intercept DNS queries and TCP connections to AI endpoints
//   - The helper must be a signed system extension; the main agent process
//     communicates with it over a local XPC or Unix socket
//   - Detect Ollama / LM Studio by listing listening ports via kern.proc sysctl
//     without requesting general process introspection entitlements
//   - Target macOS 12+ (Monterey); Network Extension requires developer entitlements
type darwinCollector struct {
	cfg Config
}

// newOSCollector returns the macOS Network Extension-based network observer.
func newOSCollector(cfg Config) OSCollector {
	return &darwinCollector{cfg: cfg}
}

func (c *darwinCollector) Start(_ context.Context, _ chan<- Observation) error {
	// TODO: launch helper process, open XPC channel, forward DNS/TCP events
	return nil
}

func (c *darwinCollector) Close() error {
	// TODO: terminate helper process, close XPC channel
	return nil
}
