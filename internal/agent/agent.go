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

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/buffer"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/collector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/collector/euc"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/collector/llm"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/factory"
	pkgsignal "github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// Config is the top-level agent configuration, loaded from YAML via Viper and
// optionally overridden by ARGUS_SDK_* environment variables.
type Config struct {
	Agent   AgentConfig   `mapstructure:"agent"`
	Auth    AuthConfig    `mapstructure:"auth"`
	Ingest  IngestConfig  `mapstructure:"ingest"`
	Buffer  buffer.Config `mapstructure:"buffer"`
	Outputs []OutputConfig `mapstructure:"outputs"`
	TLS     TLSConfig     `mapstructure:"tls"`
	Logging LoggingConfig `mapstructure:"logging"`
}

// AgentConfig holds agent identity settings.
type AgentConfig struct {
	GroupID      string `mapstructure:"group_id"`
	InstanceName string `mapstructure:"instance_name"`
	Mode         string `mapstructure:"mode"` // "local" | "remote"
}

// AuthConfig holds credential state. InstallToken is consumed once at registration;
// thereafter InstanceID and Credential govern signal submission.
type AuthConfig struct {
	InstallToken string `mapstructure:"install_token"` // one-time; cleared after registration
	InstanceID   string `mapstructure:"instance_id"`   // populated by XDR after registration
	Credential   string `mapstructure:"credential"`    // rotating signal-submission credential
}

// IngestConfig configures the local listener for instrumentation libraries.
type IngestConfig struct {
	Listen ListenConfig `mapstructure:"listen"`
}

// ListenConfig specifies the addresses the agent binds for inbound signals.
type ListenConfig struct {
	GRPC string `mapstructure:"grpc"`
	Unix string `mapstructure:"unix"`
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

	// instanceID is resolved once at start() via ensureInstance.
	instanceID string

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

// Run starts the agent, blocks until a SIGINT/SIGTERM is received, then shuts down.
// This is the entry point called by cmd/argus-agent/main.go.
func (a *Agent) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	defer cancel()

	if err := a.start(ctx); err != nil {
		return fmt.Errorf("agent start: %w", err)
	}

	// Block until OS signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	// TODO: SIGHUP hot-reload hook — add in a follow-on phase (syscall.SIGHUP)
	<-sigCh

	a.logger.Info("shutdown signal received")
	return a.stop()
}

// start initialises and starts all components.
func (a *Agent) start(ctx context.Context) error {
	a.logger.Info("argus-agent starting",
		zap.String("mode", a.cfg.Agent.Mode),
		zap.String("instance_name", a.cfg.Agent.InstanceName))

	// 1. Registration: resolve InstanceID via localRegistrar.
	id, err := ensureInstance(ctx, a.cfg, &localRegistrar{})
	if err != nil {
		return fmt.Errorf("start: %w", err)
	}
	a.instanceID = id
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

	// 5. Build collectors from config.
	// LLM gRPC collector — always started when a GRPC listen address is configured.
	if a.cfg.Ingest.Listen.GRPC != "" {
		llmCfg := llm.Config{
			GRPCAddr:   a.cfg.Ingest.Listen.GRPC,
			UnixSocket: a.cfg.Ingest.Listen.Unix,
		}
		a.collectors = append(a.collectors, llm.New(llmCfg))
	}

	// EUC collector — started with the noop OS impl (cross-platform seam).
	// A future plan will wire a real OSCollector on Linux/macOS/Windows.
	eucCfg := euc.Config{
		AppID: a.cfg.Agent.GroupID, // populated from agent identity
		Env:   a.cfg.Agent.Mode,   // best-effort env tag from agent mode
	}
	a.collectors = append(a.collectors, euc.New(eucCfg, euc.NewNoopOSCollector()))

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

	a.logger.Info("argus-agent started",
		zap.String("instance_id", a.instanceID),
		zap.Int("collectors", len(a.collectors)))

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
