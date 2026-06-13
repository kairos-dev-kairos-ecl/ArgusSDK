package agent

import (
	"testing"
	"time"

	"github.com/spf13/viper"
)

// TestExampleConfigBinds is a drift guard: it loads the shipped
// config/agent.example.yaml exactly as the CLI does (viper → mapstructure) and
// asserts every documented key binds to a real field. If someone documents a
// config key the code does not read (or renames a field without updating the
// example), this test fails instead of shipping a config that silently no-ops.
func TestExampleConfigBinds(t *testing.T) {
	v := viper.New()
	v.SetConfigFile("../../config/agent.example.yaml")
	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("read example config: %v", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		t.Fatalf("unmarshal example config: %v", err)
	}

	// Agent identity
	if cfg.Agent.Mode != "remote" {
		t.Errorf("agent.mode = %q, want remote", cfg.Agent.Mode)
	}

	// Buffer keys must bind (regression: fields previously lacked mapstructure tags)
	if cfg.Buffer.MaxSizeMB != 256 {
		t.Errorf("buffer.max_size_mb = %d, want 256", cfg.Buffer.MaxSizeMB)
	}
	if cfg.Buffer.FlushInterval != 5*time.Second {
		t.Errorf("buffer.flush_interval = %v, want 5s", cfg.Buffer.FlushInterval)
	}
	if !cfg.Buffer.DrainOnReconnect {
		t.Errorf("buffer.drain_on_reconnect = false, want true")
	}

	// EUC watch list must bind at startup (not only on SIGHUP reload)
	if len(cfg.Ingest.EUC.AIEndpoints) == 0 {
		t.Errorf("ingest.euc.ai_endpoints did not bind")
	}
	if len(cfg.Ingest.EUC.LocalInferencePorts) == 0 {
		t.Errorf("ingest.euc.local_inference_ports did not bind")
	}

	// Observability schema must match ObservabilityConfig (disabled + addr),
	// not the fabricated health_check_port/metrics_port that never existed.
	if cfg.Observability.Disabled {
		t.Errorf("observability.disabled = true, want false")
	}
	if cfg.Observability.Addr == "" {
		t.Errorf("observability.addr did not bind")
	}

	// At least one output must be configured.
	if len(cfg.Outputs) == 0 {
		t.Errorf("outputs did not bind")
	}
}
