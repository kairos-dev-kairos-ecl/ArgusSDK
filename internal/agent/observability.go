package agent

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// defaultObservabilityAddr is the bind address used when ObservabilityConfig.Addr
// is empty. It binds loopback by default so metrics are not exposed off-host
// unless the operator explicitly opts in to a routable address.
const defaultObservabilityAddr = "127.0.0.1:9090"

// ObservabilityConfig configures the agent's operational HTTP surface
// (/healthz, /readyz, /metrics).
type ObservabilityConfig struct {
	// Disabled turns the observability server off entirely. The server is on by
	// default (zero value = enabled) since liveness/readiness probes are part of
	// the expected production deployment.
	Disabled bool `mapstructure:"disabled"`
	// Addr is the bind address (host:port). Defaults to 127.0.0.1:9090.
	Addr string `mapstructure:"addr"`
}

// obsServer exposes liveness, readiness, and metrics over HTTP. It holds no
// business state — readiness is a single atomic flag flipped by the agent once
// startup completes, and metrics are pulled on demand from statsFn.
type obsServer struct {
	srv     *http.Server
	logger  *zap.Logger
	ready   atomic.Bool
	statsFn func() map[string]uint64
}

// newObsServer builds the observability server bound to addr. statsFn supplies
// the dispatcher counters for /metrics; it may return nil (rendered as zero
// counters) before the dispatcher exists.
func newObsServer(addr string, statsFn func() map[string]uint64, logger *zap.Logger) *obsServer {
	if addr == "" {
		addr = defaultObservabilityAddr
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	o := &obsServer{logger: logger, statsFn: statsFn}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", o.handleHealthz)
	mux.HandleFunc("/readyz", o.handleReadyz)
	mux.HandleFunc("/metrics", o.handleMetrics)

	o.srv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return o
}

// setReady marks the agent ready (or not) for the /readyz probe.
func (o *obsServer) setReady(ready bool) { o.ready.Store(ready) }

// handleHealthz reports process liveness. It returns 200 unconditionally once
// the server is accepting connections — a successful response means the process
// is up and the HTTP loop is running.
func (o *obsServer) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

// handleReadyz reports readiness to receive and deliver signals. It returns 200
// only after the agent has fully started (all connectors connected); otherwise
// 503 so load balancers / k8s hold traffic until the agent is wired.
func (o *obsServer) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if o.ready.Load() {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("not ready\n"))
}

// handleMetrics renders the dispatcher counters in the Prometheus text
// exposition format. No client library is used — the format is small and
// stable, so it is hand-rolled to avoid a new dependency.
func (o *obsServer) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	var stats map[string]uint64
	if o.statsFn != nil {
		stats = o.statsFn()
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(encodeMetrics(stats)))
}

// metricHelp documents each dispatcher counter for the # HELP line.
var metricHelp = map[string]string{
	"accepted":  "Signal batches accepted by the dispatcher for delivery.",
	"delivered": "Signal batches successfully delivered to a connector.",
	"failed":    "Signal batches that failed delivery.",
	"dropped":   "Signal batches dropped (queue full or shutdown).",
}

// encodeMetrics serialises the dispatcher stats map into Prometheus text format.
// Output is deterministic (keys sorted) so it is stable to assert against.
// Each counter is emitted as argus_dispatch_<key>_total with HELP and TYPE lines.
func encodeMetrics(stats map[string]uint64) string {
	keys := make([]string, 0, len(stats))
	for k := range stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		name := "argus_dispatch_" + k + "_total"
		help := metricHelp[k]
		if help == "" {
			help = "Dispatcher counter " + k + "."
		}
		fmt.Fprintf(&b, "# HELP %s %s\n", name, help)
		fmt.Fprintf(&b, "# TYPE %s counter\n", name)
		fmt.Fprintf(&b, "%s %d\n", name, stats[k])
	}
	return b.String()
}

// start binds the listener and serves in a background goroutine. Binding happens
// synchronously so a bind failure (port in use) is returned to the caller rather
// than lost in the goroutine.
func (o *obsServer) start() error {
	ln, err := net.Listen("tcp", o.srv.Addr)
	if err != nil {
		return fmt.Errorf("observability: listen %s: %w", o.srv.Addr, err)
	}
	o.logger.Info("observability server listening", zap.String("addr", o.srv.Addr))
	go func() {
		if err := o.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			o.logger.Warn("observability server stopped", zap.Error(err))
		}
	}()
	return nil
}

// stop gracefully shuts the server down within the supplied context deadline.
func (o *obsServer) stop(ctx context.Context) error {
	return o.srv.Shutdown(ctx)
}
