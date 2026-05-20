// Package agent implements the core argus-agent lifecycle: configuration loading,
// component wiring, graceful start and shutdown.
package agent

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/buffer"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/collector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
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
	Type     string                 `mapstructure:"type"` // "argusxdr" | "kafka" | "splunk_hec" | "syslog"
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

// Agent wires all components together and manages their lifecycle.
type Agent struct {
	cfg        *Config
	logger     *zap.Logger
	collectors []collector.Collector
	dispatcher *connector.Dispatcher
	buffer     *buffer.Buffer
	cancel     context.CancelFunc
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

	// TODO: registration flow if InstanceID is empty
	// TODO: wire ConnectorRegistry from cfg.Outputs
	// TODO: create Dispatcher
	// TODO: create Buffer, call buffer.Start(ctx, dispatcher drain func)
	// TODO: create and start collectors

	return nil
}

// stop drains in-flight work and closes all components.
func (a *Agent) stop() error {
	a.logger.Info("stopping collectors")
	for _, c := range a.collectors {
		if err := c.Close(); err != nil {
			a.logger.Warn("collector close error", zap.String("name", c.Name()), zap.Error(err))
		}
	}

	if a.buffer != nil {
		flushCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = a.buffer.Flush(flushCtx, nil) // TODO: pass real drain func
		_ = a.buffer.Close()
	}

	if a.dispatcher != nil {
		if err := a.dispatcher.Close(); err != nil {
			a.logger.Warn("dispatcher close error", zap.Error(err))
		}
	}

	a.logger.Info("argus-agent stopped")
	return nil
}
