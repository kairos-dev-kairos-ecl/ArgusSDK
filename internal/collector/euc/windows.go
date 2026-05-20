//go:build windows

package euc

import "context"

// windowsCollector watches AI service connections on Windows using WFP
// (Windows Filtering Platform) callouts or ETW (Event Tracing for Windows)
// network provider events.
//
// Implementation TODO:
//   - Use WFP callouts via golang.org/x/sys/windows to intercept TCP connect
//     events, filtered to the configured AI endpoint list
//   - Alternatively, consume Microsoft-Windows-NDIS-PacketCapture ETW provider
//     for DNS resolution events targeting known AI hostnames
//   - Detect local inference runtimes by scanning listening ports (netstat-equivalent)
//     against LocalInferencePorts without general process enumeration
//   - Run as a low-privilege service; do not request SeDebugPrivilege
type windowsCollector struct {
	cfg Config
}

// newOSCollector returns the Windows WFP/ETW-based network observer.
func newOSCollector(cfg Config) OSCollector {
	return &windowsCollector{cfg: cfg}
}

func (c *windowsCollector) Start(_ context.Context, _ chan<- Observation) error {
	// TODO: register WFP callout or ETW session, forward events as Observations
	return nil
}

func (c *windowsCollector) Close() error {
	// TODO: deregister callout / close ETW session
	return nil
}
