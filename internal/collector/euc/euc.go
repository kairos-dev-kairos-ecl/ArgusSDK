// Package euc implements the EUC (End User Computing) signal collector.
// Its sole purpose is Shadow AI observability: detecting AI service access
// on enterprise endpoints without duplicating standard EDR telemetry.
//
// The OS-specific implementations (linux.go, windows.go, darwin.go) satisfy
// this interface using platform-appropriate mechanisms:
//   - Linux:   eBPF-based DNS/network observer
//   - Windows: WFP (Windows Filtering Platform) / ETW network events
//   - macOS:   Network Extension framework
//
// If an EDR agent is already installed, argus-agent must remain invisible
// to it: low privilege, no process enumeration, no general network capture.
package euc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/collector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// Config holds EUC collector configuration.
type Config struct {
	// AIEndpoints is the list of hostnames to watch for AI service connections.
	// This list is config-driven and hot-reloadable; do not hardcode values here.
	// Example entries: "api.openai.com", "api.anthropic.com", "api.cohere.com"
	AIEndpoints []string

	// LocalInferencePorts lists ports used by local AI runtimes to detect.
	// Ollama default: 11434, LM Studio default: 1234.
	LocalInferencePorts []int

	// AppID is the app_id stamped on EUC signals (set by the agent from config).
	AppID string

	// Env is the environment label (dev/staging/prod).
	Env string
}

// Observation is a single AI-service access event detected by the OS collector.
// The platform-specific implementations produce Observations; the Collector
// wraps them as signal.Signal values before forwarding.
type Observation struct {
	// ConnectedHost is the hostname resolved or connected to.
	ConnectedHost string

	// LocalPort is the local port if a local inference runtime was detected.
	LocalPort int

	// IsLocal is true when the observation is a local inference process
	// (Ollama, LM Studio, etc.) rather than an outbound connection.
	IsLocal bool

	// ProcessName is the process that made the connection (best-effort).
	ProcessName string

	// Username is the OS user that owns the process (best-effort).
	Username string
}

// OSCollector is the interface each platform must implement.
// The exported Collector (below) wraps an OSCollector.
type OSCollector interface {
	// Start begins monitoring and sends Observations on the provided channel.
	Start(ctx context.Context, obs chan<- Observation) error

	// Close stops monitoring and releases OS-level resources.
	Close() error
}

// Collector wraps an OSCollector and translates Observations to signal.Batch
// values emitted on the shared ingest channel.
type Collector struct {
	cfg  Config
	impl OSCollector
}

// New creates an EUC collector wrapping the provided OS-specific implementation.
// Call newOSCollector() from the appropriate build-tag file to obtain impl.
func New(cfg Config, impl OSCollector) *Collector {
	return &Collector{cfg: cfg, impl: impl}
}

// Name implements collector.Collector.
func (c *Collector) Name() string { return "euc" }

// Start begins OS-level monitoring and converts observations to signals.
func (c *Collector) Start(ctx context.Context, out chan<- signal.Batch) error {
	obs := make(chan Observation, 256)
	if err := c.impl.Start(ctx, obs); err != nil {
		return err
	}
	go c.fanOut(ctx, obs, out)
	return nil
}

// observationContext is the structure encoded as ContextJSON on each EUC signal.
// It captures the network-observable fields relevant to Shadow AI detection.
// Per T-04-14: intentional capture for Shadow AI observability; no general capture.
type observationContext struct {
	ConnectedHost string `json:"connected_host"`
	LocalPort     int    `json:"local_port"`
	IsLocal       bool   `json:"is_local"`
	ProcessName   string `json:"process_name"`
	Username      string `json:"username"`
}

// generateSignalID returns a 16-byte random hex string suitable for use as a
// SignalID. It avoids adding a ULID dependency as per the locked decision.
func generateSignalID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// fanOut converts Observations to signal.Batch and sends them on out.
// Each Observation produces exactly one signal.Batch with one signal.Signal.
//
// Layer choice: L9APIGateway is correct for EUC signals — these observations
// sit at the network/gateway boundary between a user process and an AI API
// (or local inference daemon), which maps to the API Gateway layer in the
// 10-layer LLM taxonomy.
func (c *Collector) fanOut(ctx context.Context, obs <-chan Observation, out chan<- signal.Batch) {
	for {
		select {
		case <-ctx.Done():
			return
		case o, ok := <-obs:
			if !ok {
				return
			}

			// Derive category from observation type.
			category := "euc.ai_access"
			if o.IsLocal {
				category = "euc.local_inference"
			}

			// Marshal observation metadata as ContextJSON.
			// T-04-14: intentional connection metadata capture for Shadow AI observability.
			ctxData := observationContext{
				ConnectedHost: o.ConnectedHost,
				LocalPort:     o.LocalPort,
				IsLocal:       o.IsLocal,
				ProcessName:   o.ProcessName,
				Username:      o.Username,
			}
			ctxJSON, _ := json.Marshal(ctxData)

			sig := signal.Signal{
				SignalID:    generateSignalID(),
				Layer:       signal.L9APIGateway,
				Category:    category,
				Severity:    signal.SeverityInfo,
				AppID:       c.cfg.AppID,
				Env:         c.cfg.Env,
				Timestamp:   time.Now(),
				ContextJSON: ctxJSON,
			}

			batch := signal.Batch{
				AppID:   c.cfg.AppID,
				Env:     c.cfg.Env,
				Signals: []signal.Signal{sig},
			}

			select {
			case out <- batch:
			case <-ctx.Done():
				return
			}
		}
	}
}

// Health returns nil if the OS collector is running.
func (c *Collector) Health(ctx context.Context) error {
	return nil
}

// Close stops the OS collector.
func (c *Collector) Close() error {
	return c.impl.Close()
}

// ensure Collector satisfies collector.Collector at compile time.
var _ collector.Collector = (*Collector)(nil)
