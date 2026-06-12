//go:build windows

package euc

// Windows ETW-based OSCollector for Shadow-AI network observation.
//
// Mechanism: real-time ETW session on Microsoft-Windows-Kernel-Network (connect
// metadata only) + gopsutil local-port sampler for local-inference detection
// without elevation.
//
// Privilege model: ETW realtime sessions on kernel providers require membership
// in Performance Log Users (or Administrator). If the session start is denied,
// Start returns nil so the agent stays up (degrade, never crash). The gopsutil
// local-port path requires NO elevation and runs regardless.
//
// Session cleanup: the deterministic session name "ArgusEUC" is stopped-if-
// existing inside golang-etw's EnableProvider (ERROR_ALREADY_EXISTS path) and
// on Close. Stale sessions from a prior crash can be manually inspected with
//   logman query -ets
// and removed with
//   logman stop ArgusEUC -ets
//
// T-06-06: run as Performance Log Users, not SYSTEM/Admin; ERROR_ACCESS_DENIED → degrade
// T-06-07: Kernel-Network connect metadata only; NDIS-PacketCapture explicitly rejected
// T-06-08: all ETW event fields treated as untrusted input (bounded + validated before use)
// T-06-09: deterministic session name + stop-if-exists + Close tears down
// T-06-10: gopsutil local-port path keeps euc.local_inference flowing even when ETW degrades

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/0xrawsec/golang-etw/etw"
	gnet "github.com/shirou/gopsutil/v4/net"
	"go.uber.org/zap"
	"golang.org/x/sys/windows"
)

const (
	// etwSessionName is the deterministic ETW session name.
	// A stale session from a prior crash is automatically stopped and restarted
	// by golang-etw's EnableProvider (ERROR_ALREADY_EXISTS handling).
	// Manual cleanup: logman stop ArgusEUC -ets
	etwSessionName = "ArgusEUC"

	// kernelNetworkProvider is the ETW provider for TCP/IP connect events
	// (metadata only — remote address, ports, PID). NDIS-PacketCapture is
	// explicitly rejected (T-06-07).
	kernelNetworkProvider = "Microsoft-Windows-Kernel-Network"

	// gopsutilInterval is how often the local-inference port sampler polls
	// the OS connection table (via gopsutil, no elevation required).
	gopsutilInterval = 5 * time.Second

	// maxFieldLen caps untrusted ETW event field lengths before any comparison
	// (V5 / T-06-08).
	maxFieldLen = 512
)

// windowsCollector implements OSCollector on Windows using:
//   - ETW realtime session on Microsoft-Windows-Kernel-Network (connect events)
//   - gopsutil connection-table sampler for local-inference port detection
type windowsCollector struct {
	cfg    Config
	log    *zap.Logger
	once   sync.Once
	cancel context.CancelFunc
}

// newOSCollector returns the Windows ETW + gopsutil OSCollector.
func newOSCollector(cfg Config) OSCollector {
	return &windowsCollector{
		cfg: cfg,
		log: zap.L().With(zap.String("collector", "euc/windows")),
	}
}

// Start begins ETW monitoring (if accessible) and the gopsutil local-port
// sampler (always). Always returns nil — degrade, never crash.
func (c *windowsCollector) Start(ctx context.Context, out chan<- Observation) error {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	// ETW path: requires Performance Log Users / Administrator.
	c.startETW(ctx, out)

	// gopsutil local-inference path: no elevation required, runs regardless.
	go c.gopsutilLoop(ctx, out)

	return nil
}

// startETW sets up the ETW session and consumer. On any access-denied or
// consumer-start failure, logs a warning and returns (degrade, no crash).
func (c *windowsCollector) startETW(ctx context.Context, out chan<- Observation) {
	s := etw.NewRealTimeSession(etwSessionName)

	// EnableProvider starts the session (auto-handles ERROR_ALREADY_EXISTS by
	// stopping the stale session and restarting — see golang-etw producer.go).
	if err := s.EnableProvider(etw.MustParseProvider(kernelNetworkProvider)); err != nil {
		c.log.Warn("ETW provider enable failed; ETW path degraded (no crash)",
			zap.String("provider", kernelNetworkProvider),
			zap.Error(err),
		)
		return
	}

	cons := etw.NewRealTimeConsumer(ctx)
	cons.FromSessions(s)

	cons.EventCallback = func(e *etw.Event) error {
		c.handleETWEvent(e, out, ctx)
		return nil
	}

	if err := cons.Start(); err != nil {
		// Includes ERROR_ACCESS_DENIED (5) — degrade gracefully.
		c.log.Warn("ETW consumer start failed; ETW path degraded (no crash)",
			zap.Error(err),
		)
		_ = s.Stop()
		return
	}

	// Background goroutine: wait for consumer to finish (ctx cancel drives it).
	go func() {
		<-ctx.Done()
		_ = cons.Stop()
		_ = s.Stop()
	}()
}

// handleETWEvent processes a single ETW event from Microsoft-Windows-Kernel-Network.
// ETW event fields are untrusted input — all values are bounded before use (V5 / T-06-08).
func (c *windowsCollector) handleETWEvent(e *etw.Event, out chan<- Observation, ctx context.Context) {
	// PID from the kernel event header (trustworthy, not a string field).
	pid := e.System.Execution.ProcessID

	// Extract remote host/address. Microsoft-Windows-Kernel-Network TcpConnect
	// events may expose the remote address under "daddr" or "DestAddress".
	// We try both field names; if neither is present the event is skipped.
	remoteAddr := boundedField(e, "daddr", "DestAddress", "RemoteAddress")
	if remoteAddr == "" {
		return
	}

	// Extract local port. Field names vary; try common variants.
	localPortStr := boundedField(e, "sport", "SrcPort", "LocalPort")
	localPort := parsePort(localPortStr)

	// If neither the remote host nor the local port is interesting, skip early.
	matched := matchHost(remoteAddr, c.cfg.AIEndpoints) || isLocalInferencePort(localPort, c.cfg.LocalInferencePorts)
	if !matched {
		return
	}

	// Best-effort process name from the event PID (Pitfall 5: only the
	// specific event's PID — no ToolHelp32Snapshot, no global enumeration).
	procName := pidToName(pid)

	obs := Observation{
		ConnectedHost: remoteAddr,
		LocalPort:     localPort,
		IsLocal:       isLocalInferencePort(localPort, c.cfg.LocalInferencePorts),
		ProcessName:   procName,
		Username:      "", // best-effort: empty acceptable (contract)
	}

	select {
	case out <- obs:
	case <-ctx.Done():
	}
}

// gopsutilLoop polls the OS connection table for local-inference ports.
// Uses gopsutil which requires no elevation for own-user sockets (A2).
// De-duplication is done per-poll via a seen set keyed on (laddr:lport).
func (c *windowsCollector) gopsutilLoop(ctx context.Context, out chan<- Observation) {
	ticker := time.NewTicker(gopsutilInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.sampleLocalPorts(ctx, out)
		}
	}
}

// sampleLocalPorts queries the connection table for watched local-inference
// ports and emits Observation{IsLocal:true} for each match.
func (c *windowsCollector) sampleLocalPorts(ctx context.Context, out chan<- Observation) {
	if len(c.cfg.LocalInferencePorts) == 0 {
		return
	}

	conns, err := gnet.ConnectionsWithContext(ctx, "tcp")
	if err != nil {
		c.log.Warn("gopsutil connection table unavailable; local-inference sampler skipped",
			zap.Error(err))
		return
	}

	seen := make(map[string]struct{})
	for i := range conns {
		conn := &conns[i]
		localPort := int(conn.Laddr.Port)
		if !isLocalInferencePort(localPort, c.cfg.LocalInferencePorts) {
			continue
		}

		key := fmt.Sprintf("%s:%d", conn.Laddr.IP, localPort)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		obs := Observation{
			ConnectedHost: net.JoinHostPort("127.0.0.1", "0"),
			LocalPort:     localPort,
			IsLocal:       true,
			ProcessName:   "", // best-effort: PID-to-name skipped here (no process enum)
			Username:      "",
		}

		select {
		case out <- obs:
		case <-ctx.Done():
			return
		}
	}
}

// Close stops the ETW consumer, session, and gopsutil ticker goroutine.
// Safe to call multiple times (sync.Once).
func (c *windowsCollector) Close() error {
	c.once.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}
	})
	return nil
}

// boundedField extracts the first non-empty string value from the ETW event's
// EventData under any of the supplied field names, bounded to maxFieldLen.
// All values are treated as untrusted input (V5 / T-06-08).
func boundedField(e *etw.Event, names ...string) string {
	for _, name := range names {
		if val, ok := e.GetPropertyString(name); ok && val != "" {
			if len(val) > maxFieldLen {
				val = val[:maxFieldLen]
			}
			return strings.TrimSpace(val)
		}
	}
	return ""
}

// parsePort converts a decimal port string to an int. Returns 0 if unparseable.
func parsePort(s string) int {
	if s == "" {
		return 0
	}
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	if err != nil || n < 0 || n > 65535 {
		return 0
	}
	return n
}

// pidToName resolves a PID to a process image name using
// PROCESS_QUERY_LIMITED_INFORMATION (does not require SeDebugPrivilege).
// Returns an empty string on any failure (best-effort, Pitfall 5).
// No general process enumeration; only the matched event's own PID is queried.
func pidToName(pid uint32) string {
	if pid == 0 {
		return ""
	}
	// PROCESS_QUERY_LIMITED_INFORMATION (0x1000) — minimum access to query image name.
	// Does NOT require SeDebugPrivilege; works for own-user processes.
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h) //nolint:errcheck

	// QueryFullProcessImageNameW returns the full path; we extract the base name.
	buf := make([]uint16, syscall.MAX_PATH)
	size := uint32(len(buf))
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")
	r, _, _ := proc.Call(
		uintptr(h),
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if r == 0 {
		return ""
	}

	full := syscall.UTF16ToString(buf[:size])
	// Extract base name only.
	if idx := strings.LastIndexAny(full, `\/`); idx >= 0 {
		return full[idx+1:]
	}
	return full
}
