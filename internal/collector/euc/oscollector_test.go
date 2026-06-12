package euc

import (
	"context"
	"testing"
	"time"
)

// TestNewOSCollector_NonNilAndDegradeSafe verifies that NewOSCollector always
// returns a non-nil OSCollector and that Start + Close are degrade-safe on the
// host platform without special privileges. The impl may be the real collector
// degrading gracefully or the noop fallback — both satisfy the contract.
func TestNewOSCollector_NonNilAndDegradeSafe(t *testing.T) {
	t.Parallel()

	cfg := Config{
		AIEndpoints:         []string{"api.openai.com"},
		LocalInferencePorts: []int{11434},
		AppID:               "test",
		Env:                 "test",
	}

	impl := NewOSCollector(cfg)
	if impl == nil {
		t.Fatal("NewOSCollector returned nil; expected a non-nil OSCollector")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	out := make(chan Observation, 8)

	if err := impl.Start(ctx, out); err != nil {
		t.Errorf("Start returned non-nil error: %v", err)
	}

	<-ctx.Done()

	if err := impl.Close(); err != nil {
		t.Errorf("Close returned non-nil error: %v", err)
	}
}
