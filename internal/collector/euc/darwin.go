//go:build darwin

// Package euc — macOS EUC OSCollector implementation.
//
// # Scope Decision: no-root established-connection sampler; full Network Extension deferred
//
// v1.0 darwin implementation is a NO-ROOT established-connection sampler that
// polls the OS TCP connection table via gopsutil/v4 (net.ConnectionsWithContext).
// It matches established outbound connections against cfg.AIEndpoints and
// listening / loopback-established connections against cfg.LocalInferencePorts,
// then emits euc.Observation values consumed by Collector.fanOut.
//
// The full NEDNSProxyProvider Network Extension is EXPLICITLY DEFERRED to a
// future packaging/signing phase. A real Network Extension requires:
//   - A signed .app bundle (Pitfall 4)
//   - The managed com.apple.developer.networking.networkextension entitlement
//     (Apple-approved; not available to a `go install`-distributed CLI)
//   - Developer-ID signing and notarization
//   - OSSystemExtensionRequest activation with user approval
//
// None of these are satisfiable for a CLI binary distributed via `go install`.
// The sampler honors the low-privilege contract (no root, no entitlements, no
// process enumeration, no packet capture) and produces the same Observation
// shape, making it a correct and shippable v1.0 implementation.
//
// Low-privilege contract preserved: SC-6, T-06-11, ASVS V4.
package euc

import (
	"context"
	"sync"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"
	"go.uber.org/zap"
)

const (
	// defaultPollInterval is how often the sampler polls the connection table.
	defaultPollInterval = 2 * time.Second

	// dedupeTTL is how long a (remoteHost, localPort) key stays in the dedup
	// set before it is re-emitted. This prevents infinite re-emission of a
	// long-lived connection while still detecting a reconnect after a gap.
	dedupeTTL = 30 * time.Second

	// maxConnHostLen is the maximum length we accept for a connection's remote
	// address before we discard it (untrusted OS data; V5 / T-06-13).
	maxConnHostLen = 253
)

// connKey is the dedup map key for an observed connection.
type connKey struct {
	remoteHost string
	localPort  uint32
}

// seenEntry records when a connKey was first emitted.
type seenEntry struct {
	at time.Time
}

// darwinCollector is the macOS no-root established-connection sampler.
//
// It polls gopsutil/v4 net.ConnectionsWithContext for established outbound
// connections and listening/loopback sockets, matches them against the
// configured watch lists, and emits euc.Observation values. No root, no
// entitlements, and no general process enumeration are required.
type darwinCollector struct {
	cfg      Config
	log      *zap.Logger
	interval time.Duration

	cancel context.CancelFunc
	once   sync.Once

	mu   sync.Mutex
	seen map[connKey]seenEntry
}

// newOSCollector returns the macOS no-root established-connection sampler.
func newOSCollector(cfg Config) OSCollector {
	log, err := zap.NewProduction()
	if err != nil {
		// If logger creation fails, use a no-op logger. Collector must not crash.
		log = zap.NewNop()
	}
	return &darwinCollector{
		cfg:      cfg,
		log:      log.Named("euc.darwin"),
		interval: defaultPollInterval,
		seen:     make(map[connKey]seenEntry),
	}
}

// Start begins sampling the macOS TCP connection table.
//
// A ticker goroutine polls net.ConnectionsWithContext at the configured
// interval (default 2 s). For each connection the goroutine:
//  1. Checks whether the remote host matches cfg.AIEndpoints (outbound path).
//  2. Checks whether the local port matches cfg.LocalInferencePorts
//     (local-inference path — established-to-loopback or LISTEN).
//  3. De-duplicates via a time-bounded seen map so a long-lived connection
//     is not re-emitted on every tick.
//  4. Sends an euc.Observation on out, respecting ctx.Done().
//
// If net.ConnectionsWithContext returns an error (e.g. partial table,
// permission issue), a warning is logged and the goroutine continues — it
// never crashes the agent (T-06-14 / degrade pattern).
//
// Start always returns nil; any sampling failure degrades to emitting nothing.
func (c *darwinCollector) Start(ctx context.Context, out chan<- Observation) error {
	ctx, cancel := context.WithCancel(ctx)
	c.once.Do(func() {
		c.cancel = cancel
	})

	go c.sampleLoop(ctx, out)
	return nil
}

// sampleLoop is the ticker goroutine that drives connection table polling.
func (c *darwinCollector) sampleLoop(ctx context.Context, out chan<- Observation) {
	t := time.NewTicker(c.interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.sample(ctx, out)
		}
	}
}

// sample performs one poll of the connection table and emits any new matches.
func (c *darwinCollector) sample(ctx context.Context, out chan<- Observation) {
	conns, err := gnet.ConnectionsWithContext(ctx, "tcp")
	if err != nil {
		// Degrade: log and continue (T-06-14). Own-process sockets still cover
		// local-inference detection on the next successful poll.
		c.log.Warn("darwin: connection table sample failed; degrading",
			zap.Error(err))
		return
	}

	now := time.Now()
	c.evictExpired(now)

	for _, conn := range conns {
		// ------------------------------------------------------------------
		// Outbound path: established connections to watched AI endpoints.
		// ------------------------------------------------------------------
		remoteHost := conn.Raddr.IP
		if len(remoteHost) > 0 && len(remoteHost) <= maxConnHostLen &&
			conn.Status == "ESTABLISHED" &&
			matchHost(remoteHost, c.cfg.AIEndpoints) {

			k := connKey{remoteHost: remoteHost, localPort: 0}
			if c.notSeen(k, now) {
				obs := Observation{
					ConnectedHost: remoteHost,
					IsLocal:       false,
					// ProcessName and Username are best-effort. gopsutil surfaces
					// the Pid field but resolving it to a name requires a
					// per-process lookup — we skip general enumeration (Pitfall 5,
					// T-06-12). Fields left empty is acceptable per contract.
				}
				select {
				case out <- obs:
				case <-ctx.Done():
					return
				}
			}
		}

		// ------------------------------------------------------------------
		// Local-inference path: listening or loopback-established sockets on
		// watched local inference ports (Ollama 11434, LM Studio 1234, ...).
		// ------------------------------------------------------------------
		localPort := int(conn.Laddr.Port)
		status := conn.Status
		isListenOrLoopback := status == "LISTEN" ||
			(status == "ESTABLISHED" && isLoopback(conn.Raddr.IP))

		if isListenOrLoopback && isLocalInferencePort(localPort, c.cfg.LocalInferencePorts) {
			k := connKey{remoteHost: "", localPort: conn.Laddr.Port}
			if c.notSeen(k, now) {
				loopbackHost := conn.Raddr.IP
				if loopbackHost == "" {
					loopbackHost = "127.0.0.1"
				}
				obs := Observation{
					ConnectedHost: loopbackHost,
					LocalPort:     localPort,
					IsLocal:       true,
				}
				select {
				case out <- obs:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

// isLoopback reports whether ip is a loopback address.
// We check common IPv4 and IPv6 loopback literals; this avoids importing
// net.ParseIP just for a string prefix check.
func isLoopback(ip string) bool {
	if ip == "127.0.0.1" || ip == "::1" {
		return true
	}
	// Broader 127.x.x.x check (RFC 5735).
	if len(ip) > 4 && ip[:4] == "127." {
		return true
	}
	return false
}

// notSeen returns true if k has not been seen recently (within dedupeTTL),
// and records it as seen at now.
func (c *darwinCollector) notSeen(k connKey, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.seen[k]; ok && now.Sub(e.at) < dedupeTTL {
		return false
	}
	c.seen[k] = seenEntry{at: now}
	return true
}

// evictExpired removes stale entries from the dedup map.
func (c *darwinCollector) evictExpired(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.seen {
		if now.Sub(e.at) >= dedupeTTL {
			delete(c.seen, k)
		}
	}
}

// Close stops the sampler goroutine. It is safe to call more than once.
func (c *darwinCollector) Close() error {
	c.once.Do(func() {
		// cancel was never set (Start was never called). Nothing to do.
	})
	if c.cancel != nil {
		c.cancel()
	}
	return nil
}
