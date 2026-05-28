// Package elastic_test contains unit and integration tests for the Elastic connector.
package elastic_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/elastic"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// makeSignal creates a minimal valid signal for testing.
func makeSignal() signal.Signal {
	return signal.Signal{
		SignalID:   "test-signal-001",
		Layer:      signal.L9APIGateway,
		Category:   "test-category",
		Severity:   signal.SeverityMedium,
		Timestamp:  time.Now().UTC(),
		AppID:      "test-app",
		TraceID:    "trace-001",
		SpanID:     "span-001",
		SDKVersion: "1.0.0",
	}
}

// makeBatch creates a SignalBatch with n copies of the test signal.
func makeBatch(n int) *connector.SignalBatch {
	sigs := make([]signal.Signal, n)
	for i := range sigs {
		sigs[i] = makeSignal()
	}
	return &connector.SignalBatch{
		BatchID:    "batch-001",
		InstanceID: "instance-001",
		Signals:    sigs,
		UseOCSF:    true,
	}
}

// TestElasticConnector_Name asserts Name() returns "elastic".
func TestElasticConnector_Name(t *testing.T) {
	c := elastic.New(elastic.Config{
		Endpoint: "https://localhost:9200",
		APIKey:   "id:key",
	})
	if c.Name() != "elastic" {
		t.Fatalf("expected Name()=%q, got %q", "elastic", c.Name())
	}
}

// TestElasticConnector_ConnectNoEndpoint asserts Connect() returns error on empty Endpoint.
func TestElasticConnector_ConnectNoEndpoint(t *testing.T) {
	c := elastic.New(elastic.Config{APIKey: "id:key"})
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error for empty Endpoint, got nil")
	}
}

// TestElasticConnector_ConnectNoAPIKey asserts Connect() returns error on empty APIKey.
func TestElasticConnector_ConnectNoAPIKey(t *testing.T) {
	c := elastic.New(elastic.Config{Endpoint: "https://localhost:9200"})
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error for empty APIKey, got nil")
	}
}

// TestElasticConnector_ConnectClusterInfo asserts Connect() succeeds when GET / returns cluster info.
func TestElasticConnector_ConnectClusterInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"test","cluster_name":"argus-test","version":{"number":"8.13.0"}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := elastic.NewWithClient(elastic.Config{
		Endpoint: srv.URL,
		APIKey:   "id:key",
		Index:    "argus-signals",
	}, srv.Client())
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("unexpected Connect error: %v", err)
	}
}

// TestElasticConnector_ConnectFails asserts Connect() returns error containing "401" on 401.
func TestElasticConnector_ConnectFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"security_exception"}`))
	}))
	defer srv.Close()

	c := elastic.NewWithClient(elastic.Config{
		Endpoint: srv.URL,
		APIKey:   "id:key",
	}, srv.Client())
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected error to contain '401', got %q", err.Error())
	}
}

// TestElasticConnector_SendBulkFormat verifies the /_bulk request body and auth header.
func TestElasticConnector_SendBulkFormat(t *testing.T) {
	var capturedBody []byte
	var capturedAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"test","cluster_name":"argus-test","version":{"number":"8.13.0"}}`))
		case "/_bulk":
			capturedAuth = r.Header.Get("Authorization")
			body, _ := io.ReadAll(r.Body)
			capturedBody = body
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"took":3,"errors":false,"items":[{"index":{"result":"created","status":201}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := elastic.NewWithClient(elastic.Config{
		Endpoint: srv.URL,
		APIKey:   "id:key",
		Index:    "argus-signals",
	}, srv.Client())

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	ack, err := c.Send(context.Background(), makeBatch(1))
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if ack == nil {
		t.Fatal("expected non-nil DeliveryAck")
	}

	// Verify Authorization header starts with "ApiKey "
	if !strings.HasPrefix(capturedAuth, "ApiKey ") {
		t.Fatalf("expected Authorization to start with 'ApiKey ', got %q", capturedAuth)
	}

	// Verify NDJSON body: must contain at least 2 lines (action + document)
	lines := strings.Split(strings.TrimSpace(string(capturedBody)), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 NDJSON lines, got %d: %q", len(lines), string(capturedBody))
	}

	// Verify action line contains "_index"
	if !strings.Contains(lines[0], "_index") {
		t.Fatalf("expected action line to contain '_index', got %q", lines[0])
	}

	// Verify document line contains "@timestamp"
	if !strings.Contains(lines[1], "@timestamp") {
		t.Fatalf("expected document line to contain '@timestamp', got %q", lines[1])
	}

	// Verify document line contains event code (class_uid-derived field)
	if !strings.Contains(lines[1], "event.code") && !strings.Contains(lines[1], "class_uid") {
		t.Fatalf("expected document line to contain ECS 'event.code' or 'class_uid', got %q", lines[1])
	}
}

// TestElasticConnector_SendBulkError asserts DeliveryAck.Status=="failed" when errors:true.
func TestElasticConnector_SendBulkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"test","cluster_name":"argus-test","version":{"number":"8.13.0"}}`))
		case "/_bulk":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"took":5,"errors":true,"items":[{"index":{"error":{"type":"mapper_parsing_exception","reason":"failed to parse"},"status":400}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := elastic.NewWithClient(elastic.Config{
		Endpoint: srv.URL,
		APIKey:   "id:key",
		Index:    "argus-signals",
	}, srv.Client())

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	ack, err := c.Send(context.Background(), makeBatch(1))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if ack.Status != "failed" {
		t.Fatalf("expected Status=%q, got %q", "failed", ack.Status)
	}
}

// TestElasticConnector_SendBulkSuccess asserts DeliveryAck.Status=="delivered" on success.
func TestElasticConnector_SendBulkSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"test","cluster_name":"argus-test","version":{"number":"8.13.0"}}`))
		case "/_bulk":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"took":3,"errors":false,"items":[{"index":{"result":"created","status":201}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := elastic.NewWithClient(elastic.Config{
		Endpoint: srv.URL,
		APIKey:   "id:key",
		Index:    "argus-signals",
	}, srv.Client())

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	ack, err := c.Send(context.Background(), makeBatch(1))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if ack.Status != "delivered" {
		t.Fatalf("expected Status=%q, got %q", "delivered", ack.Status)
	}
}

// TestElasticConnector_Health asserts Health() returns nil when status is "green".
func TestElasticConnector_Health(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"test","cluster_name":"argus-test","version":{"number":"8.13.0"}}`))
		case "/_cluster/health":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"cluster_name":"argus-test","status":"green"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := elastic.NewWithClient(elastic.Config{
		Endpoint: srv.URL,
		APIKey:   "id:key",
	}, srv.Client())

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("expected nil for green status, got %v", err)
	}
}

// TestElasticConnector_HealthYellow asserts Health() returns nil when status is "yellow".
func TestElasticConnector_HealthYellow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"test","cluster_name":"argus-test","version":{"number":"8.13.0"}}`))
		case "/_cluster/health":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"cluster_name":"argus-test","status":"yellow"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := elastic.NewWithClient(elastic.Config{
		Endpoint: srv.URL,
		APIKey:   "id:key",
	}, srv.Client())

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("expected nil for yellow status, got %v", err)
	}
}

// TestElasticConnector_HealthRed asserts Health() returns non-nil error when status is "red".
func TestElasticConnector_HealthRed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"test","cluster_name":"argus-test","version":{"number":"8.13.0"}}`))
		case "/_cluster/health":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"cluster_name":"argus-test","status":"red"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := elastic.NewWithClient(elastic.Config{
		Endpoint: srv.URL,
		APIKey:   "id:key",
	}, srv.Client())

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if err := c.Health(context.Background()); err == nil {
		t.Fatal("expected non-nil error for red status, got nil")
	}
}

// TestElasticConnector_Integration runs an end-to-end test against a real Elasticsearch
// endpoint. Skipped when ELASTIC_ENDPOINT is not set.
func TestElasticConnector_Integration(t *testing.T) {
	endpoint := os.Getenv("ELASTIC_ENDPOINT")
	if endpoint == "" {
		t.Skip("ELASTIC_ENDPOINT not set — skipping integration test")
	}

	apiKey := os.Getenv("ELASTIC_API_KEY")
	if apiKey == "" {
		t.Skip("ELASTIC_API_KEY not set — skipping integration test")
	}

	index := os.Getenv("ELASTIC_INDEX")
	if index == "" {
		index = "argus-signals-test"
	}

	c := elastic.New(elastic.Config{
		Endpoint: endpoint,
		APIKey:   apiKey,
		Index:    index,
	})

	ctx := context.Background()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer func() { _ = c.Close() }()

	if err := c.Health(ctx); err != nil {
		t.Fatalf("Health check failed: %v", err)
	}

	ack, err := c.Send(ctx, makeBatch(3))
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if ack.Status != "delivered" {
		t.Fatalf("expected Status=%q, got %q (error: %s)", "delivered", ack.Status, ack.Error)
	}
}

// TestElasticConnector_ECSMapping verifies that the document sent to /_bulk
// contains the expected ECS fields derived from the OCSF event.
func TestElasticConnector_ECSMapping(t *testing.T) {
	var capturedDoc map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"test","cluster_name":"argus-test","version":{"number":"8.13.0"}}`))
		case "/_bulk":
			body, _ := io.ReadAll(r.Body)
			lines := strings.Split(strings.TrimSpace(string(body)), "\n")
			if len(lines) >= 2 {
				_ = json.Unmarshal([]byte(lines[1]), &capturedDoc)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"took":1,"errors":false,"items":[{"index":{"result":"created","status":201}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := elastic.NewWithClient(elastic.Config{
		Endpoint: srv.URL,
		APIKey:   "id:key",
		Index:    "argus-signals",
	}, srv.Client())

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	_, err := c.Send(context.Background(), makeBatch(1))
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Verify required ECS fields are present.
	requiredFields := []string{"@timestamp", "event.code", "event.category", "event.severity", "agent.name", "event.uid"}
	for _, field := range requiredFields {
		if _, ok := capturedDoc[field]; !ok {
			t.Errorf("expected ECS field %q not found in document; doc=%v", field, capturedDoc)
		}
	}
}
