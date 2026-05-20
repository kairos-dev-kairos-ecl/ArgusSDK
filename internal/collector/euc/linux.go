//go:build linux

package euc

import "context"

// linuxCollector watches AI service connections on Linux using eBPF-based
// DNS/network event capture. It requires CAP_BPF or CAP_SYS_ADMIN.
//
// Implementation TODO:
//   - Use github.com/cilium/ebpf to attach a kprobe/tracepoint on DNS resolution
//     and TCP connect syscalls, filtered to the configured AI endpoint list
//   - Publish Observation events for each resolved AI hostname or local port listen
//   - Do NOT attach to general process or file events — EDR overlap risk
type linuxCollector struct {
	cfg Config
	// prog *ebpf.Program  // wired in during implementation
}

// newOSCollector returns the Linux eBPF-based network observer.
func newOSCollector(cfg Config) OSCollector {
	return &linuxCollector{cfg: cfg}
}

func (c *linuxCollector) Start(_ context.Context, _ chan<- Observation) error {
	// TODO: load eBPF program, attach to network hooks, forward events
	return nil
}

func (c *linuxCollector) Close() error {
	// TODO: detach eBPF program, close perf buffer
	return nil
}
