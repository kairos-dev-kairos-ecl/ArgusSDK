// Package agent implements the core argus-agent lifecycle: configuration loading,
// component wiring, graceful start and shutdown.
package agent

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/auth"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/buffer"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/collector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/collector/euc"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/collector/llm"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/factory"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/secrets"
	pkgsignal "github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// Config is the top-level agent configuration, loaded from YAML via Viper and
// optionally overridden by ARGUS_SDK_* environment variables.
type Config struct {
	Agent         AgentConfig         `mapstructure:"agent"`
	Auth          AuthConfig          `mapstructure:"auth"`
	Ingest        IngestConfig        `mapstructure:"ingest"`
	Buffer        buffer.Config       `mapstructure:"buffer"`
	Outputs       []OutputConfig      `mapstructure:"outputs"`
	TLS           TLSConfig           `mapstructure:"tls"`
	Logging       LoggingConfig       `mapstructure:"logging"`
	Observability ObservabilityConfig `mapstructure:"observability"`
}

// AgentConfig holds agent identity settings.
type AgentConfig struct {
	GroupID      string `mapstructure:"group_id"`
	InstanceName string `mapstructure:"instance_name"`
	Mode         string `mapstructure:"mode"` // "local" | "remote"

	// XDREndpoint is the XDR base URL for registration + credential refresh
	// (e.g. "https://xdr.company.com:8080"). It is required in mode: remote.
	// This is NOT the per-output signal submission endpoint (cfg.Outputs[i].Endpoint)
	// — those route signal batches; this endpoint governs instance identity.
	XDREndpoint string `mapstructure:"xdr_endpoint"`
}

// AuthConfig holds credential state. InstallToken is consumed once at registration;
// thereafter InstanceID and Credential govern signal submission.
type AuthConfig struct {
	InstallToken string `mapstructure:"install_token"` // one-time; cleared after registration
	InstanceID   string `mapstructure:"instance_id"`   // populated by XDR after registration
	Credential   string `mapstructure:"credential"`    // rotating signal-submission credential
}

// IngestConfig configures the local listener for instrumentation libraries
// and the EUC (Shadow AI) watch list.
type IngestConfig struct {
	Listen ListenConfig `mapstructure:"listen"`
	EUC    EUCConfig    `mapstructure:"euc"`
}

// ListenConfig specifies the addresses the agent binds for inbound signals.
type ListenConfig struct {
	GRPC string `mapstructure:"grpc"`
	Unix string `mapstructure:"unix"`
}

// EUCConfig drives the Shadow AI watch list. Both fields are hot-reloadable via
// SIGHUP (see internal/agent/reload.go); the same keys are read at startup and
// on reload so the two paths never diverge.
type EUCConfig struct {
	// AIEndpoints is the list of hostnames to watch for outbound AI-service access
	// (e.g. "api.openai.com", "api.anthropic.com").
	AIEndpoints []string `mapstructure:"ai_endpoints"`

	// LocalInferencePorts lists local inference runtime ports to detect
	// (Ollama 11434, LM Studio 1234, vLLM 8000, etc.).
	LocalInferencePorts []int `mapstructure:"local_inference_ports"`
}

// OutputConfig configures a single output connector.
type OutputConfig struct {
	Name     string                 `mapstructure:"name"`
	Type     string                 `mapstructure:"type"` // "argusxdr" | "kafka" | "splunk_hec" | "syslog" | "elastic"
	Endpoint string                 `mapstructure:"endpoint"`
	OCSF     bool                   `mapstructure:"ocsf"`
	TLS      TLSConfig              `mapstructure:"tls"`
	Auth     map[string]string      `mapstructure:"auth"`
	Extra    map[string]interface{} `mapstructure:",remain"`
}

// TLSConfig enforces TLS 1.3 as the minimum; no downgrade option is exposed.
type TLSConfig struct {
	Enabled    bool   `mapstructure:"enabled"`
	MinVersion string `mapstructure:"min_version"` // always "1.3"; field present for documentation only
	CACert     string `mapstructure:"ca_cert"`
}

// LoggingConfig controls structured logging output.
type LoggingConfig struct {
	Level  string `mapstructure:"level"`  // "debug" | "info" | "warn" | "error"
	Format string `mapstructure:"format"` // "json" | "console"
}

// shutdownTimeout is the maximum time stop() allows for buffer.Flush before
// giving up.
const shutdownTimeout = 30 * time.Second

// Agent wires all components together and manages their lifecycle.
type Agent struct {
	cfg        *Config
	logger     *zap.Logger
	collectors []collector.Collector
	dispatcher *connector.Dispatcher
	buffer     *buffer.Buffer
	cancel     context.CancelFunc

	// registry holds all connected output connectors.
	registry *connector.ConnectorRegistry

	// obs is the operational HTTP server (/healthz, /readyz, /metrics).
	// nil when observability is disabled in config.
	obs *obsServer

	// instanceID is resolved once at start() via resolveIdentity.
	instanceID string

	// store is the secrets.Store used for encrypted identity persistence.
	// It is non-nil only in mode: remote (mode: local never constructs a store).
	store *secrets.Store

	// resolveIdentityFn is an injectable seam used by tests to drive registration
	// against an in-package mock without a live XDR or ARGUS_MASTER_KEY.
	// When nil, start() calls the default resolveIdentity implementation.
	resolveIdentityFn func(ctx context.Context) (string, error)

	// ocsfTargets are the connector names that receive OCSF-translated batches
	// (UseOCSF=true). These are outputs where cfg.Outputs[i].OCSF=true AND
	// cfg.Outputs[i].Type != "argusxdr".
	ocsfTargets []string

	// nonOCSFTargets are connector names that receive raw batches (UseOCSF=false).
	// argusxdr is always in this group regardless of the OCSF field (locked decision 5).
	nonOCSFTargets []string

	// ingestCh is the shared channel from collectors to the ingest loop.
	ingestCh chan pkgsignal.Batch

	// drain is the function passed to buffer.Start and buffer.Flush.
	// It delivers a *connector.SignalBatch through the dispatcher.
	// Stored on the agent so stop() can call Flush with the same drain.
	drain func(ctx context.Context, b *connector.SignalBatch) error

	// wg tracks the ingest loop goroutine so stop() can wait for it to exit.
	wg sync.WaitGroup

	// configPath is the path to the agent configuration file, set by SetReloadSources.
	configPath string

	// atomicLevel is the live zap.AtomicLevel for hot-reload, set by SetReloadSources.
	atomicLevel zap.AtomicLevel

	// eucCollector is a reference to the EUC collector for hot-reload watch list updates,
	// populated during start().
	eucCollector *euc.Collector
}

// New creates an Agent from the provided config. Components are wired but not started.
func New(cfg *Config, logger *zap.Logger) (*Agent, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Agent{cfg: cfg, logger: logger}, nil
}

// SetReloadSources sets the configuration file path and live AtomicLevel for SIGHUP hot-reload.
// Must be called before Run().
func (a *Agent) SetReloadSources(configPath string, level zap.AtomicLevel) {
	a.configPath = configPath
	a.atomicLevel = level
}

// Start initializes and starts all components, deriving the agent's lifecycle
// context from ctx. It returns once startup completes (or fails); it does not
// block. Use Stop to shut down. This is the entry point for the Windows service
// handler; console/Unix deployments use Run instead.
func (a *Agent) Start(ctx context.Context) error {
	cctx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	if err := a.start(cctx); err != nil {
		cancel()
		return fmt.Errorf("agent start: %w", err)
	}
	return nil
}

// Stop gracefully shuts the agent down: drains the buffer, closes collectors and
// connectors, and cancels the lifecycle context. Safe to call once after Start
// or Run.
func (a *Agent) Stop() error {
	return a.stop()
}

// ReloadConfig re-applies the bounded set of hot-reloadable settings (EUC watch
// list + log level). Exposed so platform service handlers can trigger a reload.
func (a *Agent) ReloadConfig() error {
	return a.reloadConfig()
}

// Run starts the agent, blocks until a SIGINT/SIGTERM is received (reloading on
// SIGHUP), then shuts down. This is the console/Unix entry point.
func (a *Agent) Run() error {
	if err := a.Start(context.Background()); err != nil {
		return err
	}

	// Block until OS signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	for {
		sig := <-sigCh
		switch sig {
		case syscall.SIGHUP:
			if err := a.reloadConfig(); err != nil {
				a.logger.Error("config reload failed", zap.Error(err))
			}
		case syscall.SIGINT, syscall.SIGTERM:
			a.logger.Info("shutdown signal received")
			return a.Stop()
		}
	}
}

// start initialises and starts all components.
func (a *Agent) start(ctx context.Context) error {
	a.logger.Info("argus-agent starting",
		zap.String("mode", a.cfg.Agent.Mode),
		zap.String("instance_name", a.cfg.Agent.InstanceName))

	// 1. Registration: resolve InstanceID (persisted-state-first, mode-aware).
	var resolveErr error
	if a.resolveIdentityFn != nil {
		// Test seam: use the injected resolver (no live XDR, no env var needed).
		a.instanceID, resolveErr = a.resolveIdentityFn(ctx)
	} else {
		a.instanceID, resolveErr = a.resolveIdentity(ctx)
	}
	if resolveErr != nil {
		return fmt.Errorf("start: %w", resolveErr)
	}
	a.logger.Info("instance identity resolved", zap.String("instance_id", a.instanceID))

	// 2. Build ConnectorRegistry from cfg.Outputs.
	if len(a.cfg.Outputs) == 0 {
		return fmt.Errorf("start: no outputs configured — at least one output is required")
	}

	// Initialise registry if not already set by a test seam.
	if a.registry == nil {
		a.registry = connector.NewConnectorRegistry(a.logger)
	}

	for _, o := range a.cfg.Outputs {
		in := factory.FactoryInput{
			Name:     o.Name,
			Type:     o.Type,
			Endpoint: o.Endpoint,
			OCSF:     o.OCSF,
			TLS: factory.FactoryTLSInput{
				Enabled:    o.TLS.Enabled,
				MinVersion: o.TLS.MinVersion,
				CACert:     o.TLS.CACert,
			},
			Auth:  o.Auth,
			Extra: o.Extra,
		}
		c, err := factory.Build(in, a.logger)
		if err != nil {
			return fmt.Errorf("start: build connector %q: %w", o.Name, err)
		}
		if err := c.Connect(ctx); err != nil {
			return fmt.Errorf("start: connect %q: %w", o.Name, err)
		}
		if err := a.registry.Register(c); err != nil {
			return fmt.Errorf("start: register %q: %w", o.Name, err)
		}

		// Classify into OCSF partition (locked decision 5):
		// argusxdr is always non-OCSF regardless of the OCSF field.
		if o.OCSF && o.Type != "argusxdr" {
			a.ocsfTargets = append(a.ocsfTargets, o.Name)
		} else {
			a.nonOCSFTargets = append(a.nonOCSFTargets, o.Name)
		}
	}

	a.logger.Info("connector registry built",
		zap.Int("total", len(a.cfg.Outputs)),
		zap.Strings("ocsf", a.ocsfTargets),
		zap.Strings("non_ocsf", a.nonOCSFTargets))

	// 3. Create Dispatcher.
	disp, err := connector.NewDispatcher(connector.DefaultDispatchConfig(), a.registry, a.logger)
	if err != nil {
		return fmt.Errorf("start: create dispatcher: %w", err)
	}
	a.dispatcher = disp

	// 4. Create Buffer and drain func; call buffer.Start.
	a.buffer = buffer.New(a.cfg.Buffer)
	a.drain = func(ctx context.Context, b *connector.SignalBatch) error {
		return a.deliver(ctx, b)
	}
	if err := a.buffer.Start(ctx, a.drain); err != nil {
		return fmt.Errorf("start: buffer start: %w", err)
	}

	// 4b. Start the observability server (liveness up immediately, readiness
	// gated until startup completes). Disabled via config.observability.disabled.
	if !a.cfg.Observability.Disabled {
		a.obs = newObsServer(a.cfg.Observability.Addr, func() map[string]uint64 {
			if a.dispatcher == nil {
				return nil
			}
			return a.dispatcher.Stats()
		}, a.logger)
		a.obs.setReady(false)
		if err := a.obs.start(); err != nil {
			return fmt.Errorf("start: observability: %w", err)
		}
	}

	// 5. Build collectors from config.
	// LLM gRPC collector — always started when a GRPC listen address is configured.
	if a.cfg.Ingest.Listen.GRPC != "" {
		llmCfg := llm.Config{
			GRPCAddr:   a.cfg.Ingest.Listen.GRPC,
			UnixSocket: a.cfg.Ingest.Listen.Unix,
		}
		a.collectors = append(a.collectors, llm.New(llmCfg))
	}

	// EUC collector — build-tag-selected OSCollector (eBPF/Linux, ETW/Windows,
	// sampler/darwin) with the noop as the universal fallback for unprivileged
	// environments and unsupported targets.
	eucCfg := euc.Config{
		AppID:               a.cfg.Agent.GroupID,
		Env:                 a.cfg.Agent.Mode,
		AIEndpoints:         a.cfg.Ingest.EUC.AIEndpoints,
		LocalInferencePorts: a.cfg.Ingest.EUC.LocalInferencePorts,
	}
	a.eucCollector = euc.New(eucCfg, euc.NewOSCollector(eucCfg))
	a.collectors = append(a.collectors, a.eucCollector)

	// 6. Start ingest loop goroutine.
	a.ingestCh = make(chan pkgsignal.Batch, 256)
	a.wg.Add(1)
	go a.ingestLoop(a.cfg)

	// 7. Start all collectors with the shared ingest channel.
	for _, c := range a.collectors {
		if err := c.Start(ctx, a.ingestCh); err != nil {
			return fmt.Errorf("start: collector %q: %w", c.Name(), err)
		}
	}

	// All connectors are connected and collectors are running — signal readiness.
	if a.obs != nil {
		a.obs.setReady(true)
	}

	a.logger.Info("argus-agent started",
		zap.String("instance_id", a.instanceID),
		zap.Int("collectors", len(a.collectors)))

	return nil
}

// ─── identity resolution ──────────────────────────────────────────────────────

// resolveIdentity resolves the agent's InstanceID using the following precedence:
//
//  1. If a persisted state file exists (mode: remote only), load InstanceID + Credential
//     from the encrypted secrets.Store and reuse them (idempotent restart — no Register call).
//  2. If cfg.Auth.InstanceID is pre-set, short-circuit registration (and persist in remote mode).
//  3. Otherwise call the mode-appropriate registrar (ensureInstance), persist on success
//     (remote mode), and clear the consumed InstallToken.
//
// Mode-aware gating (WARNING 4):
//   - mode: local  → uses localRegistrar; no secrets.Store is constructed; ARGUS_MASTER_KEY not required.
//   - mode: remote → requires ARGUS_MASTER_KEY (or injected key); NewStore is called here.
//
// On ErrInstallTokenConsumed with an existing persisted InstanceID the persisted identity
// is kept and the error is NOT returned — the stored state takes precedence.
func (a *Agent) resolveIdentity(ctx context.Context) (string, error) {
	cfg := a.cfg

	if cfg.Agent.Mode == "remote" {
		// Remote mode requires an explicit XDR endpoint.
		if cfg.Agent.XDREndpoint == "" {
			return "", fmt.Errorf("remote mode requires agent.xdr_endpoint to be set")
		}

		// Build (or reuse) the secrets store — requires ARGUS_MASTER_KEY.
		if a.store == nil {
			store, err := secrets.NewStore(auth.StateFile, nil) // nil → reads ARGUS_MASTER_KEY
			if err != nil {
				return "", fmt.Errorf("secrets store: %w", err)
			}
			a.store = store
		}

		// 1. Persisted-state-first: reuse a previously registered InstanceID.
		existing, ok, err := auth.LoadIdentity(a.store)
		if err != nil {
			return "", fmt.Errorf("load persisted identity: %w", err)
		}
		if ok {
			// Reuse persisted InstanceID; update in-memory credential.
			cfg.Auth.InstanceID = existing.InstanceID
			cfg.Auth.Credential = existing.Credential
			return existing.InstanceID, nil
		}

		// 2. Pre-set InstanceID short-circuits registration (persist for future restarts).
		if cfg.Auth.InstanceID != "" {
			id := auth.Identity{
				GroupID:    cfg.Agent.GroupID,
				InstanceID: cfg.Auth.InstanceID,
				Credential: cfg.Auth.Credential,
			}
			if saveErr := auth.SaveIdentity(a.store, id); saveErr != nil {
				a.logger.Warn("failed to persist pre-set identity", zap.Error(saveErr))
			}
			return cfg.Auth.InstanceID, nil
		}

		// 3. Register via remote adapter.
		innerRegistrar := auth.NewRemoteRegistrar(cfg.Agent.XDREndpoint, nil)
		adapter := NewRemoteRegistrarAdapter(innerRegistrar, cfg.Agent.InstanceName, agentVersion)
		instanceID, regErr := ensureInstance(ctx, cfg, adapter)
		if regErr != nil {
			// On token-consumed with a persisted identity: keep it (T-05-07 mitigation).
			// (We already checked ok==false above, so no persisted state exists here.)
			return "", regErr
		}

		// Persist the newly registered Identity.
		id := auth.Identity{
			GroupID:    cfg.Agent.GroupID,
			InstanceID: instanceID,
			Credential: adapter.LastCredential(),
		}
		if saveErr := auth.SaveIdentity(a.store, id); saveErr != nil {
			return "", fmt.Errorf("persist identity: %w", saveErr)
		}
		// Clear the consumed install token.
		cfg.Auth.InstallToken = ""
		cfg.Auth.Credential = adapter.LastCredential()
		return instanceID, nil
	}

	// mode: local — no store, no master key required.
	// 2. Pre-set InstanceID short-circuits.
	if cfg.Auth.InstanceID != "" {
		return cfg.Auth.InstanceID, nil
	}
	// 3. Register via local deterministic registrar.
	return ensureInstance(ctx, cfg, &localRegistrar{})
}

// agentVersion is the build-time version string embedded by the linker.
// Default "dev" is used when no -ldflags version is supplied.
var agentVersion = "dev"

// ─── RefreshCredential ────────────────────────────────────────────────────────

// RefreshCredential rotates the agent credential by calling the XDR
// credential-refresh endpoint and atomically replacing the stored credential
// via the secrets store.
//
// It is a reachable runtime entry point. Automatic refresh-before-expiry
// scheduling is deferred to Phase 7 — only the call path is wired here.
//
// Returns an error when:
//   - mode != remote
//   - cfg.Agent.XDREndpoint is empty
//   - no persisted Identity exists in the secrets store
//   - the XDR refresh endpoint returns an error
func (a *Agent) RefreshCredential(ctx context.Context) error {
	if a.cfg.Agent.Mode != "remote" {
		return fmt.Errorf("RefreshCredential: only supported in mode: remote")
	}
	if a.cfg.Agent.XDREndpoint == "" {
		return fmt.Errorf("RefreshCredential: agent.xdr_endpoint is empty")
	}
	if a.store == nil {
		return fmt.Errorf("RefreshCredential: secrets store not initialised (call after start())")
	}

	id, ok, err := auth.LoadIdentity(a.store)
	if err != nil {
		return fmt.Errorf("RefreshCredential: load identity: %w", err)
	}
	if !ok {
		return fmt.Errorf("RefreshCredential: no persisted identity found — agent must register first")
	}

	refresher := auth.NewRemoteCredentialRefresher(a.cfg.Agent.XDREndpoint, nil)
	newCred, err := refresher.Refresh(ctx, id)
	if err != nil {
		return fmt.Errorf("RefreshCredential: refresh failed: %w", err)
	}

	if err := auth.ReplaceCredential(a.store, newCred); err != nil {
		return fmt.Errorf("RefreshCredential: persist new credential: %w", err)
	}
	a.cfg.Auth.Credential = newCred
	return nil
}

// ingestLoop reads signal.Batch from the ingest channel and converts each to
// one or more connector.SignalBatch values, dispatching a separate DispatchJob
// per non-empty OCSF group.
//
// Per-OCSF-group dispatch (locked decision 5 + N5):
//   - nonOCSFTargets receive a DispatchJob with UseOCSF=false
//   - ocsfTargets receive a DispatchJob with UseOCSF=true
//
// On Enqueue failure (queue full or dispatcher shutting down) the affected batch
// is Written to the WAL buffer so no data is lost (T-04-15 mitigation).
func (a *Agent) ingestLoop(cfg *Config) {
	defer a.wg.Done()
	for src := range a.ingestCh {
		base := connector.SignalBatch{
			BatchID:    connector.NewBatchID(),
			InstanceID: a.instanceID,
			GroupID:    cfg.Agent.GroupID,
			AppID:      src.AppID,
			Env:        src.Env,
			ReceivedAt: time.Now(),
			Signals:    src.Signals,
		}

		if len(a.nonOCSFTargets) > 0 {
			sb := base
			sb.UseOCSF = false
			if err := a.dispatcher.Enqueue(&connector.DispatchJob{
				Batch:   &sb,
				Targets: a.nonOCSFTargets,
			}); err != nil {
				// Queue full or dispatcher shutting down — write to WAL buffer.
				if a.buffer != nil {
					_ = a.buffer.Write(&sb)
				}
			}
		}

		if len(a.ocsfTargets) > 0 {
			sb := base
			sb.UseOCSF = true
			if err := a.dispatcher.Enqueue(&connector.DispatchJob{
				Batch:   &sb,
				Targets: a.ocsfTargets,
			}); err != nil {
				if a.buffer != nil {
					_ = a.buffer.Write(&sb)
				}
			}
		}
	}
}

// deliver routes a *connector.SignalBatch (from the WAL buffer) back through the
// dispatcher. It enqueues a DispatchJob targeting the group that matches the
// batch's UseOCSF flag. Returns a non-nil error when the queue is full so the
// buffer retries (Phase 3 delivery contract, locked decision 3).
func (a *Agent) deliver(ctx context.Context, b *connector.SignalBatch) error {
	var targets []string
	if b.UseOCSF {
		targets = a.ocsfTargets
	} else {
		targets = a.nonOCSFTargets
	}
	if len(targets) == 0 {
		// No targets for this UseOCSF group — use all registered connectors as fallback.
		targets = a.registry.List()
	}
	if err := a.dispatcher.Enqueue(&connector.DispatchJob{Batch: b, Targets: targets}); err != nil {
		return err // non-nil → buffer retries
	}
	return nil
}

// stop drains in-flight work and closes all components.
func (a *Agent) stop() error {
	// 0. Flip readiness off and shut the observability server down first so probes
	// see "not ready" immediately and load balancers stop routing during drain.
	if a.obs != nil {
		a.obs.setReady(false)
		obsCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := a.obs.stop(obsCtx); err != nil {
			a.logger.Warn("observability server shutdown error", zap.Error(err))
		}
		cancel()
	}

	// 1. Cancel the agent context so collectors and the buffer drain loop exit.
	if a.cancel != nil {
		a.cancel()
	}

	// 2. Stop collectors (no new signals enter ingestCh after this).
	a.logger.Info("stopping collectors")
	for _, c := range a.collectors {
		if err := c.Close(); err != nil {
			a.logger.Warn("collector close error", zap.String("name", c.Name()), zap.Error(err))
		}
	}

	// 3. Close ingestCh and wait for the ingest loop to drain the remaining
	// batches it has already received (they will be enqueued or buffered).
	if a.ingestCh != nil {
		close(a.ingestCh)
	}
	a.wg.Wait()

	// 4. Flush the buffer with the real drain func (SC-4, T-04-16 mitigation).
	// The F5 nil-drain path is intentionally removed — stop() always has a.drain
	// set from start().
	if a.buffer != nil && a.drain != nil {
		flushCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := a.buffer.Flush(flushCtx, a.drain); err != nil {
			a.logger.Warn("buffer flush error on shutdown", zap.Error(err))
		}
		if err := a.buffer.Close(); err != nil {
			a.logger.Warn("buffer close error", zap.Error(err))
		}
	}

	// 5. Close the dispatcher after the buffer is flushed.
	if a.dispatcher != nil {
		if err := a.dispatcher.Close(); err != nil {
			a.logger.Warn("dispatcher close error", zap.Error(err))
		}
	}

	a.logger.Info("argus-agent stopped")
	return nil
}
