package euc

// NewOSCollector returns the build-tag-selected platform OSCollector:
//   - Linux:  eBPF kprobe observer (cilium/ebpf) + gopsutil local-port path
//   - Windows: ETW Kernel-Network observer + gopsutil local-port path
//   - macOS:  gopsutil no-root established-connection sampler
//
// Each platform impl degrades internally (emits nothing, never crashes) when the
// required capability or privilege is absent. NewNoopOSCollector() is the
// universal fallback for any future build target without a platform impl (SC-5).
func NewOSCollector(cfg Config) OSCollector {
	impl := newOSCollector(cfg)
	if impl == nil {
		return NewNoopOSCollector()
	}
	return impl
}
