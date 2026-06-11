//go:build integration

// Package elastic_test provides real-infrastructure integration tests for the Elasticsearch connector.
//
// These tests start an Elasticsearch container via testcontainers-go and exercise the
// full /_bulk delivery path including the multi-chunk (>MaxBatchDocs) case from F7.
// They are gated behind the `integration` build tag and skip cleanly when Docker is
// unavailable so the default unit suite stays fast and Docker-free.
//
// Run with:
//
//	go test -tags=integration ./internal/connector/elastic/ -v -timeout 300s
//
// CI runs this via `make test-int`.
package elastic_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	tcelastic "github.com/testcontainers/testcontainers-go/modules/elasticsearch"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/elastic"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
	tc "github.com/testcontainers/testcontainers-go"
)

// buildSignals builds n minimal signal.Signal values for testing.
func buildSignals(n int) []signal.Signal {
	now := time.Now()
	sigs := make([]signal.Signal, n)
	for i := range sigs {
		sigs[i] = signal.Signal{
			SignalID:   fmt.Sprintf("sig-%04d", i),
			Layer:      signal.L7RAGRetrieval,
			Category:   "retrieval.search",
			Severity:   signal.SeverityMedium,
			AppID:      "elastic-integration-test",
			AppVersion: "1.0.0",
			SDKVersion: "0.1.0",
			Env:        "test",
			Timestamp:  now,
		}
	}
	return sigs
}

// authHeader builds the Authorization header value for the given raw "id:key" API key.
func authHeader(apiKey string) string {
	return "ApiKey " + base64.StdEncoding.EncodeToString([]byte(apiKey))
}

// docCount issues GET <endpoint>/<index>/_count and returns the count.
func docCount(ctx context.Context, endpoint, index, auth string) (int, error) {
	url := fmt.Sprintf("%s/%s/_count", endpoint, index)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("parse _count response (status %d): %s", resp.StatusCode, body)
	}
	return result.Count, nil
}

// refreshIndex forces an index refresh so documents are immediately visible to _count.
func refreshIndex(ctx context.Context, endpoint, index, auth string) {
	url := fmt.Sprintf("%s/%s/_refresh", endpoint, index)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	req.Header.Set("Authorization", auth)
	client := &http.Client{Timeout: 10 * time.Second}
	_, _ = client.Do(req) //nolint:errcheck
}

// TestElasticConnector_DeliverBatch starts an Elasticsearch container and sends a
// single-chunk batch, asserting delivered ack and at least one indexed document (SC-9).
func TestElasticConnector_DeliverBatch(t *testing.T) {
	// Skip cleanly if Docker is unavailable.
	tc.SkipIfProviderIsNotHealthy(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Use Elasticsearch 7 (no mandatory TLS/auth by default) to keep the test simple.
	esContainer, err := tcelastic.Run(ctx, "docker.elastic.co/elasticsearch/elasticsearch:7.17.10")
	if err != nil {
		t.Skipf("elasticsearch container start failed (Docker may be unavailable): %v", err)
	}
	defer func() {
		if tErr := esContainer.Terminate(ctx); tErr != nil {
			t.Logf("elasticsearch container terminate: %v", tErr)
		}
	}()

	endpoint := esContainer.Settings.Address
	t.Logf("elasticsearch endpoint: %s", endpoint)

	// ES 7 without xpack ignores the Authorization header; the connector still requires
	// a non-empty APIKey at New() time so we pass a valid "id:key" format string.
	const rawAPIKey = "test-id:test-key"
	const indexName = "argus-inttest-deliver"

	cfg := elastic.Config{
		Endpoint:     endpoint,
		APIKey:       rawAPIKey,
		Index:        indexName,
		MaxBatchDocs: 500,
	}
	c := elastic.New(cfg)

	connectCtx, connectCancel := context.WithTimeout(ctx, 30*time.Second)
	defer connectCancel()
	if err := c.Connect(connectCtx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	sigs := buildSignals(5)
	batch := &connector.SignalBatch{
		BatchID:    "inttest-elastic-001",
		InstanceID: "test-instance",
		GroupID:    "test-group",
		ReceivedAt: time.Now(),
		UseOCSF:    true,
		Signals:    sigs,
	}

	sendCtx, sendCancel := context.WithTimeout(ctx, 60*time.Second)
	defer sendCancel()

	ack, err := c.Send(sendCtx, batch)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if ack == nil {
		t.Fatal("Send returned nil ack")
	}
	if ack.Status != "delivered" {
		t.Errorf("expected ack.Status==%q, got %q (error: %s)", "delivered", ack.Status, ack.Error)
	}

	// Force a refresh so docs are searchable.
	auth := authHeader(rawAPIKey)
	refreshIndex(ctx, endpoint, indexName, auth)

	// Verify documents were indexed.
	count, err := docCount(ctx, endpoint, indexName, auth)
	if err != nil {
		t.Logf("doc count check failed (non-fatal): %v", err)
	} else {
		t.Logf("indexed document count: %d", count)
		if count == 0 {
			t.Error("expected at least one document indexed")
		}
	}
}

// TestElasticConnector_MultiChunk exercises the F7 sequential chunking path by sending
// a batch larger than MaxBatchDocs. With MaxBatchDocs=3 and 7 signals the connector
// issues 3 sequential /_bulk calls (3+3+1). Asserts delivered ack (SC-9, F7).
func TestElasticConnector_MultiChunk(t *testing.T) {
	tc.SkipIfProviderIsNotHealthy(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	esContainer, err := tcelastic.Run(ctx, "docker.elastic.co/elasticsearch/elasticsearch:7.17.10")
	if err != nil {
		t.Skipf("elasticsearch container start failed (Docker may be unavailable): %v", err)
	}
	defer func() {
		if tErr := esContainer.Terminate(ctx); tErr != nil {
			t.Logf("elasticsearch container terminate: %v", tErr)
		}
	}()

	endpoint := esContainer.Settings.Address

	// Set MaxBatchDocs=3 so a 7-signal batch is split into 3 chunks (3+3+1) — exercises F7.
	const maxDocs = 3
	const rawAPIKey = "test-id:test-key"
	const indexName = "argus-inttest-multichunk"

	cfg := elastic.Config{
		Endpoint:     endpoint,
		APIKey:       rawAPIKey,
		Index:        indexName,
		MaxBatchDocs: maxDocs,
	}
	c := elastic.New(cfg)

	connectCtx, connectCancel := context.WithTimeout(ctx, 30*time.Second)
	defer connectCancel()
	if err := c.Connect(connectCtx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	sigs := buildSignals(7)
	batch := &connector.SignalBatch{
		BatchID:    "inttest-elastic-multichunk-001",
		InstanceID: "test-instance",
		GroupID:    "test-group",
		ReceivedAt: time.Now(),
		UseOCSF:    true,
		Signals:    sigs,
	}

	sendCtx, sendCancel := context.WithTimeout(ctx, 90*time.Second)
	defer sendCancel()

	ack, err := c.Send(sendCtx, batch)
	if err != nil {
		t.Fatalf("Send (multi-chunk): %v", err)
	}
	if ack.Status != "delivered" {
		t.Errorf("expected ack.Status==%q, got %q (error: %s)", "delivered", ack.Status, ack.Error)
	}
	t.Logf("multi-chunk ack: batch_id=%s status=%s", ack.BatchID, ack.Status)

	auth := authHeader(rawAPIKey)
	refreshIndex(ctx, endpoint, indexName, auth)

	count, err := docCount(ctx, endpoint, indexName, auth)
	if err != nil {
		t.Logf("doc count check failed (non-fatal): %v", err)
	} else {
		t.Logf("multi-chunk indexed document count: %d (expected ~7)", count)
	}
}

// TestElasticConnector_FailedDelivery_UnconnectedConnector exercises the error path:
// Send on a connector that was never connected must return a failed ack and non-nil error
// (Phase 3 delivery contract — F6). This test does not require Docker.
func TestElasticConnector_FailedDelivery_UnconnectedConnector(t *testing.T) {
	cfg := elastic.Config{
		Endpoint:     "http://localhost:9999",
		APIKey:       "test-id:test-key",
		Index:        "irrelevant",
		MaxBatchDocs: 500,
	}
	c := elastic.New(cfg)
	// Intentionally do NOT call Connect.

	batch := &connector.SignalBatch{
		BatchID:    "inttest-elastic-fail-001",
		InstanceID: "test-instance",
		GroupID:    "test-group",
		ReceivedAt: time.Now(),
		UseOCSF:    true,
		Signals:    buildSignals(1),
	}

	ack, err := c.Send(context.Background(), batch)
	if err == nil {
		t.Error("expected non-nil error from Send on unconnected connector")
	}
	if ack == nil {
		t.Fatal("expected non-nil ack even on failure")
	}
	if ack.Status != "failed" {
		t.Errorf("expected ack.Status==%q, got %q", "failed", ack.Status)
	}
	t.Logf("elastic failure ack: status=%s error=%s", ack.Status, ack.Error)
}
