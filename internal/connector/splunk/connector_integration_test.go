//go:build integration

// Package splunk_test provides a documented gated smoke test for the Splunk HEC connector.
//
// # Splunk HEC Integration Approach — SC-10 Fallback
//
// The official Splunk Enterprise container image (splunk/splunk) is large (~2 GB) and
// requires accepting a Splunk license at startup via an environment variable; it is not
// suitable for fully-automated ephemeral container tests in CI without additional setup.
// The open-source Splunk Connect for Kubernetes HEC stub images do exist but are not
// officially maintained with a consistent image tag suitable for pinning.
//
// Per locked decision 6 / SC-10 fallback, this file implements the **documented
// env-gated smoke test** variant: if SPLUNK_HEC_ENDPOINT and SPLUNK_HEC_TOKEN are
// set (e.g. pointing to a pre-existing Splunk instance or a self-hosted test stack),
// the test connects and sends a real batch. When the variables are unset the test
// t.Skips cleanly, matching the "degraded but not failed" behaviour required for the
// unattended integration suite.
//
// To run against a real Splunk HEC:
//
//	export SPLUNK_HEC_ENDPOINT=https://splunk.example.com:8088
//	export SPLUNK_HEC_TOKEN=<your-hec-token>
//	go test -tags=integration ./internal/connector/splunk/ -v -timeout 120s
//
// CI gate: these env vars are injected as secrets in the GitHub Actions integration
// job when a Splunk HEC target is configured. When the vars are absent CI runs the
// test suite but t.Skip fires, which is a pass for the integration job.
package splunk_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/splunk"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// hecEndpointEnv and hecTokenEnv are the environment variable names for the Splunk HEC
// smoke test. Set both to run against a real HEC endpoint.
const (
	hecEndpointEnv = "SPLUNK_HEC_ENDPOINT"
	hecTokenEnv    = "SPLUNK_HEC_TOKEN"
)

// splunkEnvOrSkip returns the values of SPLUNK_HEC_ENDPOINT and SPLUNK_HEC_TOKEN,
// or calls t.Skip when either is unset. This is the SC-10 fallback guard.
func splunkEnvOrSkip(t *testing.T) (endpoint, token string) {
	t.Helper()
	endpoint = os.Getenv(hecEndpointEnv)
	token = os.Getenv(hecTokenEnv)
	if endpoint == "" || token == "" {
		t.Skipf("set %s and %s to run Splunk HEC smoke tests", hecEndpointEnv, hecTokenEnv)
	}
	return endpoint, token
}

// TestSplunkHEC_DeliverBatch connects to a real Splunk HEC endpoint (env-gated) and
// sends a single-signal SignalBatch, asserting a delivered ack (SC-10).
func TestSplunkHEC_DeliverBatch(t *testing.T) {
	endpoint, token := splunkEnvOrSkip(t)

	cfg := splunk.Config{
		Endpoint:       endpoint,
		Token:          token,
		SourceType:     "argus:ocsf",
		MaxBatchEvents: 1000,
	}
	c := splunk.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	now := time.Now()
	batch := &connector.SignalBatch{
		BatchID:    "inttest-splunk-001",
		InstanceID: "test-instance",
		GroupID:    "test-group",
		ReceivedAt: now,
		UseOCSF:    true,
		Signals: []signal.Signal{
			{
				SignalID:   "sig-splunk-1",
				Layer:      signal.L7RAGRetrieval,
				Category:   "retrieval.search",
				Severity:   signal.SeverityMedium,
				AppID:      "splunk-integration-test",
				AppVersion: "1.0.0",
				SDKVersion: "0.1.0",
				Env:        "test",
				Timestamp:  now,
			},
		},
	}

	ack, err := c.Send(ctx, batch)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if ack == nil {
		t.Fatal("Send returned nil ack")
	}
	if ack.Status != "delivered" {
		t.Errorf("expected ack.Status==%q, got %q (error: %s)", "delivered", ack.Status, ack.Error)
	}
	t.Logf("splunk ack: batch_id=%s status=%s", ack.BatchID, ack.Status)
}

// TestSplunkHEC_BadToken_FailedAck asserts that a bad HEC token causes Connect to return
// a non-nil error (the health check probes /services/collector/health which returns 401
// on a bad token). This preserves the Phase 3 delivery contract (F6/T-04-21):
// failed delivery always yields a non-nil error.
//
// This test requires SPLUNK_HEC_ENDPOINT to be set but deliberately uses a wrong token
// so that the error path is exercised without real data delivery.
func TestSplunkHEC_BadToken_FailedAck(t *testing.T) {
	endpoint := os.Getenv(hecEndpointEnv)
	if endpoint == "" {
		t.Skipf("set %s to run Splunk HEC bad-token test", hecEndpointEnv)
	}

	cfg := splunk.Config{
		Endpoint:       endpoint,
		Token:          "bad-token-intentionally-wrong",
		MaxBatchEvents: 1000,
	}
	c := splunk.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Connect must return a non-nil error because the health check will fail with 401.
	if err := c.Connect(ctx); err != nil {
		// This is the expected path — Connect rejected the bad token via health check.
		t.Logf("Connect correctly rejected bad token: %v", err)
		return
	}
	defer func() { _ = c.Close() }()

	// If Connect did not fail (e.g. the HEC health endpoint allows unauthenticated health
	// checks), attempt a Send which must return a failed ack + non-nil error.
	now := time.Now()
	batch := &connector.SignalBatch{
		BatchID:    "inttest-splunk-badtoken-001",
		InstanceID: "test-instance",
		GroupID:    "test-group",
		ReceivedAt: now,
		UseOCSF:    true,
		Signals: []signal.Signal{
			{
				SignalID:  "sig-badtoken",
				Timestamp: now,
			},
		},
	}

	ack, err := c.Send(ctx, batch)
	if err == nil {
		t.Error("expected non-nil error from Send with bad HEC token")
	}
	if ack != nil && ack.Status != "failed" {
		t.Errorf("expected ack.Status==%q with bad token, got %q", "failed", ack.Status)
	}
	t.Logf("splunk bad-token result: ack=%v err=%v", ack, err)
}
