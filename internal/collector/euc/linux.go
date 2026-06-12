//go:build linux

// Package euc — Linux EUC OSCollector implementation.
//
// Implements the OSCollector interface using:
//   1. eBPF kprobes on tcp_v4_connect and tcp_v6_connect to surface outbound
//      TCP connection events in real time. Requires CAP_BPF + CAP_PERFMON.
//      Degrades gracefully (log + return nil from Start) when caps are absent.
//   2. gopsutil/v4 connection-table polling to detect local inference runtime
//      ports (Ollama 11434, LM Studio 1234, etc.) without any privilege.
//      This path continues to produce euc.local_inference observations even
//      when the eBPF path degrades.
//
// Low-privilege contract: no process enumeration, no file monitoring, no full
// packet/flow capture, no AI API payload inspection. Capture connection
// METADATA only (5-tuple at connect time). ProcessName is best-effort via the
// connecting task's own comm/PID from the eBPF event; Username is intentionally
// left empty (would require privilege escalation to resolve). SC-6, T-06-01,
// ASVS V4.
package euc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	gnet "github.com/shirou/gopsutil/v4/net"
	"go.uber.org/zap"
)

const (
	// taskCommLen matches the kernel's TASK_COMM_LEN constant (16).
	taskCommLen = 16

	// maxAddrBytes is the size of the address fields in the kernel event struct.
	maxAddrBytes = 16

	// connectEventSize is the fixed size of the kernel connect_event struct:
	//   pid   uint32   4 bytes
	//   dport uint16   2 bytes
	//   af    uint16   2 bytes
	//   saddr [16]byte 16 bytes
	//   daddr [16]byte 16 bytes
	//   comm  [16]byte 16 bytes
	//   Total: 56 bytes
	connectEventSize = 56

	// localPollInterval is the ticker interval for the gopsutil local-port sampler.
	localPollInterval = 2 * time.Second

	// localDedupeTTL controls how long a seen (port) key is suppressed from
	// re-emission. When a connection persists across polls, we re-emit after
	// this TTL so the channel stays alive.
	localDedupeTTL = 30 * time.Second

	// maxCommLen caps the comm string length for untrusted-input bounding (V5 / T-06-03).
	maxCommLen = taskCommLen
)

// connectEvent mirrors the kernel struct connect_event from bpf/tcpconnect.c.
// Fields must match the layout exactly as the kernel writes them into the ring
// buffer (little-endian on amd64/arm64, big-endian on ppc64/s390x — handled
// by binary.NativeEndian below).
type connectEvent struct {
	PID   uint32
	DPort uint16
	AF    uint16
	SAddr [maxAddrBytes]byte
	DAddr [maxAddrBytes]byte
	Comm  [taskCommLen]byte
}

// linuxCollector is the Linux OSCollector implementation.
type linuxCollector struct {
	cfg    Config
	log    *zap.Logger
	once   sync.Once // ensures Close is idempotent
	cancel context.CancelFunc

	// eBPF handles — nil when degraded (no caps).
	objs  *tcpconnectObjects
	kp4   link.Link
	kp6   link.Link
	rbRdr *ringbuf.Reader

	// local-port dedup: maps port → last-seen time.
	dedupMu sync.Mutex
	dedup   map[int]time.Time
}

// newOSCollector returns the Linux eBPF + gopsutil OSCollector.
func newOSCollector(cfg Config) OSCollector {
	logger, err := zap.NewProduction()
	if err != nil {
		// Fall back to a no-op logger; production always succeeds.
		logger = zap.NewNop()
	}
	return &linuxCollector{
		cfg:   cfg,
		log:   logger.With(zap.String("collector", "euc.linux")),
		dedup: make(map[int]time.Time),
	}
}

// Start begins OS-level monitoring and sends Observations on out.
//
// Step 1: Load the eBPF objects embedded in the binary (no clang required).
//   - On failure (EPERM, EACCES, verifier error, kernel too old): log a warning
//     and continue WITHOUT the eBPF path — the gopsutil path still runs.
//
// Step 2: Attach kprobes to tcp_v4_connect and tcp_v6_connect (Pitfall 1: both).
//   - Treat each attach individually: if one succeeds, keep it. If both fail,
//     close objects and continue without eBPF.
//
// Step 3: Open a ring-buffer reader on the Events map; spawn readLoop.
//
// Step 4: Always spawn the gopsutil local-port sampler (needs no caps).
//
// Start always returns nil — never propagate a capture-init error that would
// abort agent startup (Degrade contract: T-06-01, T-06-05).
func (c *linuxCollector) Start(ctx context.Context, out chan<- Observation) error {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	// --- Step 1: Load eBPF objects ----------------------------------------
	objs := &tcpconnectObjects{}
	if err := loadTcpconnectObjects(objs, nil); err != nil {
		c.log.Warn("eBPF load failed; degrading to connection-table-only path",
			zap.Error(err))
		// Start gopsutil path only.
		go c.localPortLoop(ctx, out)
		return nil
	}
	c.objs = objs

	// --- Step 2: Attach kprobes -------------------------------------------
	var kp4Err, kp6Err error
	c.kp4, kp4Err = link.Kprobe("tcp_v4_connect", objs.HandleTcpV4Connect, nil)
	c.kp6, kp6Err = link.Kprobe("tcp_v6_connect", objs.HandleTcpV6Connect, nil)

	if kp4Err != nil && kp6Err != nil {
		// Both attaches failed — degrade.
		c.log.Warn("kprobe attach failed for both tcp_v4_connect and tcp_v6_connect; degrading",
			zap.NamedError("v4_err", kp4Err),
			zap.NamedError("v6_err", kp6Err))
		objs.Close()
		c.objs = nil
		go c.localPortLoop(ctx, out)
		return nil
	}
	if kp4Err != nil {
		c.log.Warn("kprobe attach failed for tcp_v4_connect; IPv4 monitoring degraded",
			zap.Error(kp4Err))
	}
	if kp6Err != nil {
		c.log.Warn("kprobe attach failed for tcp_v6_connect; IPv6 monitoring degraded",
			zap.Error(kp6Err))
	}

	// --- Step 3: Open ring-buffer reader + readLoop -----------------------
	rd, err := ringbuf.NewReader(objs.Events)
	if err != nil {
		c.log.Warn("ringbuf reader creation failed; degrading to connection-table-only path",
			zap.Error(err))
		c.closeKprobes()
		objs.Close()
		c.objs = nil
		go c.localPortLoop(ctx, out)
		return nil
	}
	c.rbRdr = rd

	go c.readLoop(ctx, rd, out)

	// --- Step 4: Always run gopsutil local-port sampler -------------------
	go c.localPortLoop(ctx, out)

	return nil
}

// readLoop reads events from the eBPF ring buffer, decodes them, filters
// against the configured AI endpoints and local inference ports, and forwards
// matching Observations on out.
//
// It exits cleanly when ctx is cancelled or the reader is closed.
func (c *linuxCollector) readLoop(ctx context.Context, rd *ringbuf.Reader, out chan<- Observation) {
	for {
		// Check cancellation first.
		select {
		case <-ctx.Done():
			return
		default:
		}

		rec, err := rd.Read()
		if err != nil {
			if errors.Is(err, ringbuf.ErrClosed) || errors.Is(err, os.ErrClosed) {
				return
			}
			// Transient read error — continue.
			c.log.Debug("ringbuf read error", zap.Error(err))
			continue
		}

		obs, ok := c.decodeEvent(rec.RawSample)
		if !ok {
			continue
		}

		select {
		case out <- obs:
		case <-ctx.Done():
			return
		}
	}
}

// decodeEvent parses a raw ring-buffer sample into an Observation.
// Returns (obs, true) when the event matches the configured watch lists,
// (Observation{}, false) otherwise or on parse errors.
//
// T-06-03: treat all decoded fields as untrusted input.
func (c *linuxCollector) decodeEvent(raw []byte) (Observation, bool) {
	if len(raw) < connectEventSize {
		return Observation{}, false
	}

	var ev connectEvent
	if err := binary.Read(bytes.NewReader(raw), binary.NativeEndian, &ev); err != nil {
		return Observation{}, false
	}

	// Decode remote host.
	remoteHost := decodeAddr(ev.DAddr, ev.AF)
	if remoteHost == "" {
		return Observation{}, false
	}

	// Decode remote port (big-endian from the kernel).
	remotePort := int(ntohs(ev.DPort))

	// Extract comm string (untrusted; bound to maxCommLen, V5 / T-06-03).
	comm := boundedCString(ev.Comm[:], maxCommLen)

	// Check whether remote host matches the AI endpoint watch list.
	if matchHost(remoteHost, c.cfg.AIEndpoints) {
		return Observation{
			ConnectedHost: remoteHost,
			LocalPort:     remotePort,
			IsLocal:       false,
			ProcessName:   comm,
		}, true
	}

	// Check whether the local port matches the local-inference watch list.
	if isLocalInferencePort(remotePort, c.cfg.LocalInferencePorts) {
		return Observation{
			ConnectedHost: remoteHost,
			LocalPort:     remotePort,
			IsLocal:       true,
			ProcessName:   comm,
		}, true
	}

	return Observation{}, false
}

// localPortLoop is the gopsutil-based local inference port sampler.
// It requires NO capabilities and continues to run even when the eBPF path
// degrades (T-06-05).  It polls the TCP connection table periodically,
// matches listening/established sockets against cfg.LocalInferencePorts, and
// emits deduplicated Observations on out.
func (c *linuxCollector) localPortLoop(ctx context.Context, out chan<- Observation) {
	ticker := time.NewTicker(localPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			conns, err := gnet.ConnectionsWithContext(ctx, "tcp")
			if err != nil {
				c.log.Debug("gopsutil connection table read failed", zap.Error(err))
				continue
			}

			now := time.Now()
			c.dedupMu.Lock()
			// Expire stale dedup entries.
			for port, ts := range c.dedup {
				if now.Sub(ts) > localDedupeTTL {
					delete(c.dedup, port)
				}
			}

			for i := range conns {
				conn := &conns[i]
				lport := int(conn.Laddr.Port)
				if !isLocalInferencePort(lport, c.cfg.LocalInferencePorts) {
					continue
				}
				// Dedup: skip if seen recently.
				if ts, seen := c.dedup[lport]; seen && now.Sub(ts) < localDedupeTTL {
					continue
				}
				c.dedup[lport] = now
				c.dedupMu.Unlock()

				obs := Observation{
					ConnectedHost: loopbackHost(conn),
					LocalPort:     lport,
					IsLocal:       true,
				}
				select {
				case out <- obs:
				case <-ctx.Done():
					c.dedupMu.Lock() // re-acquire before deferred Unlock
					goto done
				}
				c.dedupMu.Lock()
			}
			c.dedupMu.Unlock()
		}
	}
done:
	c.dedupMu.Unlock()
}

// loopbackHost derives the loopback host string from a gopsutil connection.
// Returns "127.0.0.1" for IPv4, "::1" for IPv6, falling back to "localhost".
func loopbackHost(conn *gnet.ConnectionStat) string {
	if conn.Laddr.IP != "" {
		ip := net.ParseIP(conn.Laddr.IP)
		if ip != nil {
			if ip.To4() != nil {
				return "127.0.0.1"
			}
			return "::1"
		}
	}
	return "localhost"
}

// Close stops monitoring and releases all OS-level resources.
// Safe to call multiple times (guarded by sync.Once).
func (c *linuxCollector) Close() error {
	var closeErr error
	c.once.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}

		// Close ring-buffer reader first so readLoop exits.
		if c.rbRdr != nil {
			if err := c.rbRdr.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				closeErr = fmt.Errorf("ringbuf reader close: %w", err)
			}
			c.rbRdr = nil
		}

		c.closeKprobes()

		if c.objs != nil {
			c.objs.Close()
			c.objs = nil
		}
	})
	return closeErr
}

// closeKprobes closes the kprobe links, ignoring already-closed errors.
func (c *linuxCollector) closeKprobes() {
	if c.kp4 != nil {
		_ = c.kp4.Close()
		c.kp4 = nil
	}
	if c.kp6 != nil {
		_ = c.kp6.Close()
		c.kp6 = nil
	}
}

// decodeAddr converts a raw address field + address family into a string.
// Returns "" for unknown address families or all-zero addresses.
func decodeAddr(addr [maxAddrBytes]byte, af uint16) string {
	const (
		afINET  = 2
		afINET6 = 10
	)
	switch af {
	case afINET:
		ip := net.IP(addr[:4])
		if ip.Equal(net.IPv4zero) {
			return ""
		}
		return ip.String()
	case afINET6:
		ip := net.IP(addr[:16])
		if ip.Equal(net.IPv6zero) {
			return ""
		}
		return ip.String()
	default:
		return ""
	}
}

// ntohs converts a big-endian uint16 (network byte order from the kernel)
// to host byte order.
func ntohs(v uint16) uint16 {
	b := [2]byte{byte(v >> 8), byte(v)}
	return binary.BigEndian.Uint16(b[:])
}

// boundedCString converts a null-terminated byte slice to a string, bounded to
// maxLen bytes to prevent over-long untrusted input (V5 / T-06-03).
func boundedCString(b []byte, maxLen int) string {
	if maxLen > 0 && len(b) > maxLen {
		b = b[:maxLen]
	}
	// Trim at first null byte.
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
