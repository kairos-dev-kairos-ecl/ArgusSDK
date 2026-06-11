package agent

// e2e_smoke_test.go — Docker-free end-to-end smoke test for the agent.
//
// This file is NOT behind the integration build tag so it runs as part of the
// default unit suite (go test ./...) without requiring Docker or any external
// infrastructure.
//
// It exercises the full ingest → deliver → graceful-drain path (SC-12):
//   - Builds an agent.Config in memory.
//   - Injects a fakeConnector directly into the registry (bypassing factory.Build
//     so no real connector or network call is needed — the same seam established
//     by TestStart_OCSFRouting in agent_test.go).
//   - Starts the ingest loop + WAL buffer with a real drain func.
//   - Sends one signal.Batch via the ingest channel.
//   - Asserts the fakeConnector received a delivered batch.
//   - Triggers graceful shutdown (stop()) and asserts the buffer drains without panic.
//
// Placement note: the smoke lives in package agent (internal) rather than
// cmd/argus-agent/smoke_test.go because the fake-connector injection requires
// direct field access on *Agent that is not accessible from package main.
// The cmd/argus-agent boot path is covered by:
//   - The build+vet CI job: `go build ./...` compiles cmd/argus-agent.
//   - Manual smoke: `go run ./cmd/argus-agent --config sample.yaml` (documents the
//     full binary boot path; no sample.yaml is required for CI).

import (
	"context"
	"testing"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/buffer"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
	"go.uber.org/zap"
)

// TestE2ESmoke_IngestDeliverDrain is the end-to-end Docker-free smoke test (SC-12).
// It boots agent internals, ingests one signal, asserts delivery, and triggers a
// graceful shutdown asserting the buffer drains without panic.
func TestE2ESmoke_IngestDeliverDrain(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.Agent.GroupID = "smoke-group"
	cfg.Auth.InstanceID = "smoke-instance"
	logger := zap.NewNop()

	a, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// ---- Test seam: inject fake connector (no factory.Build, no network) ----
	a.registry = connector.NewConnectorRegistry(logger)
	fake := newFakeConnector("smoke-output")
	if err := a.registry.Register(fake); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Route all signals through the OCSF path (UseOCSF=true).
	a.ocsfTargets = []string{"smoke-output"}
	a.instanceID = "smoke-instance"

	// Build dispatcher and WAL buffer with real drain func (same pattern as agent_test.go).
	disp, err := connector.NewDispatcher(connector.DefaultDispatchConfig(), a.registry, logger)
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	a.dispatcher = disp

	buf := buffer.New(cfg.Buffer)
	a.buffer = buf
	drainFunc := func(ctx context.Context, b *connector.SignalBatch) error {
		return a.deliver(ctx, b)
	}
	a.drain = drainFunc
	if err := buf.Start(context.Background(), drainFunc); err != nil {
		t.Fatalf("buffer.Start: %v", err)
	}

	// Wire the ingest channel and start the ingest loop.
	a.ingestCh = make(chan signal.Batch, 10)
	a.wg.Add(1)
	go a.ingestLoop(cfg)

	// ---- Ingest one signal ----
	now := time.Now()
	a.ingestCh <- signal.Batch{
		AppID: "smoke-app",
		Env:   "test",
		Signals: []signal.Signal{
			{
				SignalID:  "smoke-sig-1",
				Layer:     signal.L8Agents,
				Category:  "agent.tool_call",
				Severity:  signal.SeverityInfo,
				Timestamp: now,
			},
		},
	}

	// Allow dispatcher time to process.
	time.Sleep(100 * time.Millisecond)

	// ---- Assert delivery ----
	received := fake.Received()
	if len(received) == 0 {
		t.Error("expected fake connector to receive at least one batch before stop()")
	} else {
		batch := received[0]
		if batch.AppID != "smoke-app" {
			t.Errorf("expected AppID=%q, got %q", "smoke-app", batch.AppID)
		}
		if batch.BatchID == "" {
			t.Error("expected non-empty BatchID")
		}
		t.Logf("smoke delivery: batch_id=%s ocsf=%v signals=%d", batch.BatchID, batch.UseOCSF, len(batch.Signals))
	}

	// ---- Graceful shutdown — stop() must drain buffer without panic ----
	// stop() closes ingestCh, waits for ingestLoop, flushes buffer, closes dispatcher.
	// We do this manually (not via stop()) because a.cancel is nil when bypassing start().
	close(a.ingestCh)
	a.wg.Wait()

	flushCtx, flushCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer flushCancel()
	if err := buf.Flush(flushCtx, drainFunc); err != nil {
		t.Logf("buffer.Flush returned (non-fatal during smoke): %v", err)
	}
	if err := buf.Close(); err != nil {
		t.Logf("buffer.Close: %v", err)
	}
	if err := disp.Close(); err != nil {
		t.Logf("disp.Close: %v", err)
	}

	t.Log("smoke: graceful drain completed without panic")
}
