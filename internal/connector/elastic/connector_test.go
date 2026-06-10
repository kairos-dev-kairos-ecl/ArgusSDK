// Package elastic_test contains unit and integration tests for the Elastic connector.
package elastic_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
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

// makeUnmappableBatch creates a SignalBatch where all signals will fail OCSF mapping.
func makeUnmappableBatch(n int) *connector.SignalBatch {
	sigs := make([]signal.Signal, n)
	for i := range sigs {
		sigs[i] = signal.Signal{
			SignalID:  fmt.Sprintf("bad-%03d", i),
			Layer:     signal.Layer(999), // unknown layer — OCSF mapper will reject
			Category:  "unknown",
			Severity:  signal.SeverityInfo,
			AppID:     "test-app",
			Timestamp: time.Now().UTC(),
		}
	}
	return &connector.SignalBatch{
		BatchID:    "batch-unmappable",
		InstanceID: "instance-001",
		Signals:    sigs,
		UseOCSF:    true,
	}
}

// clusterInfoResponse writes a standard cluster info JSON response.
func clusterInfoOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"name":"test","cluster_name":"argus-test","version":{"number":"8.13.0"}}`))
}

// bulkSuccess writes a successful /_bulk response.
func bulkSuccess(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"took":3,"errors":false,"items":[{"index":{"result":"created","status":201}}]}`))
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
			clusterInfoOK(w)
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
			clusterInfoOK(w)
		case "/_bulk":
			capturedAuth = r.Header.Get("Authorization")
			body, _ := io.ReadAll(r.Body)
			capturedBody = body
			bulkSuccess(w)
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

// TestElasticConnector_SendBulkError asserts DeliveryAck.Status=="failed" and
// non-nil error when errors:true (F6 contract: failed ack implies non-nil error).
func TestElasticConnector_SendBulkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			clusterInfoOK(w)
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
	// F6 contract: errors:true must now produce non-nil error alongside failed ack.
	if err == nil {
		t.Fatal("Send() with errors:true must return non-nil error (F6 contract)")
	}
	if ack == nil {
		t.Fatal("Send() returned nil ack")
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
			clusterInfoOK(w)
		case "/_bulk":
			bulkSuccess(w)
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
			clusterInfoOK(w)
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
			clusterInfoOK(w)
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
			clusterInfoOK(w)
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
			clusterInfoOK(w)
		case "/_bulk":
			body, _ := io.ReadAll(r.Body)
			lines := strings.Split(strings.TrimSpace(string(body)), "\n")
			if len(lines) >= 2 {
				_ = json.Unmarshal([]byte(lines[1]), &capturedDoc)
			}
			bulkSuccess(w)
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

// ---------------------------------------------------------------------------
// F6: failed=>error contract tests (new — RED phase)
// ---------------------------------------------------------------------------

// TestSend_BulkErrorsImpliesError (F6): server returns errors:true; Send must
// return failed ack AND non-nil error.
func TestSend_BulkErrorsImpliesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			clusterInfoOK(w)
		case "/_bulk":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"took":5,"errors":true}`))
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

	// F6 contract: non-nil error required alongside failed ack
	if err == nil {
		t.Error("Send() with errors:true must return non-nil error (F6 contract)")
	}
	if ack == nil {
		t.Fatal("Send() returned nil ack")
	}
	if ack.Status != "failed" {
		t.Errorf("ack.Status = %q, want %q", ack.Status, "failed")
	}
}

// TestSend_Non2xxImpliesError (F6): server returns 503; Send must return
// non-nil error AND failed ack.
func TestSend_Non2xxImpliesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			clusterInfoOK(w)
		case "/_bulk":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`service unavailable`))
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

	if err == nil {
		t.Error("Send() with HTTP 503 must return non-nil error (F6 contract)")
	}
	if ack == nil {
		t.Fatal("Send() returned nil ack")
	}
	if ack.Status != "failed" {
		t.Errorf("ack.Status = %q, want %q", ack.Status, "failed")
	}
}

// ---------------------------------------------------------------------------
// F7: chunking tests (new — RED phase)
// ---------------------------------------------------------------------------

// TestSend_ChunksBatch (F7): MaxBatchDocs=2, 5 signals → exactly 3 POSTs to /_bulk;
// total doc count (action+doc line pairs) across bodies == 5.
func TestSend_ChunksBatch(t *testing.T) {
	var postCount int32
	var allBodies [][]byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			clusterInfoOK(w)
		case "/_bulk":
			atomic.AddInt32(&postCount, 1)
			body, _ := io.ReadAll(r.Body)
			allBodies = append(allBodies, body)
			bulkSuccess(w)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := elastic.NewWithClient(elastic.Config{
		Endpoint:     srv.URL,
		APIKey:       "id:key",
		Index:        "argus-signals",
		MaxBatchDocs: 2,
	}, srv.Client())

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	ack, err := c.Send(context.Background(), makeBatch(5))
	if err != nil {
		t.Fatalf("Send() unexpected error: %v", err)
	}
	if ack.Status != "delivered" {
		t.Errorf("ack.Status = %q, want %q", ack.Status, "delivered")
	}

	// Exactly ceil(5/2) = 3 POSTs
	if got := atomic.LoadInt32(&postCount); got != 3 {
		t.Errorf("POST count = %d, want 3", got)
	}

	// Count total action+doc pairs across all bodies
	totalPairs := 0
	for _, body := range allBodies {
		lines := strings.Split(strings.TrimSpace(string(body)), "\n")
		nonEmpty := 0
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				nonEmpty++
			}
		}
		// Each document contributes 2 lines: action + doc
		totalPairs += nonEmpty / 2
	}
	if totalPairs != 5 {
		t.Errorf("total doc pairs = %d, want 5", totalPairs)
	}
}

// TestSend_ChunkFailureAborts (F7): MaxBatchDocs=2, 6 signals; chunk 2 returns
// errors:true → exactly 2 POSTs only (third never sent), err != nil,
// ack.Error identifies chunk 2 of 3.
func TestSend_ChunkFailureAborts(t *testing.T) {
	var postCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			clusterInfoOK(w)
		case "/_bulk":
			n := atomic.AddInt32(&postCount, 1)
			if n == 1 {
				bulkSuccess(w)
			} else {
				// POST 2 and beyond: bulk errors
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"took":1,"errors":true}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := elastic.NewWithClient(elastic.Config{
		Endpoint:     srv.URL,
		APIKey:       "id:key",
		Index:        "argus-signals",
		MaxBatchDocs: 2,
	}, srv.Client())

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	ack, err := c.Send(context.Background(), makeBatch(6))

	// Must return non-nil error (F6 contract on failed chunk)
	if err == nil {
		t.Error("Send() with chunk 2 failure must return non-nil error")
	}
	if ack == nil {
		t.Fatal("Send() returned nil ack")
	}
	if ack.Status != "failed" {
		t.Errorf("ack.Status = %q, want %q", ack.Status, "failed")
	}

	// Exactly 2 POSTs — chunk 3 was never sent
	if got := atomic.LoadInt32(&postCount); got != 2 {
		t.Errorf("POST count = %d, want 2 (abort on first failed chunk)", got)
	}

	// ack.Error must identify chunk 2 of 3
	if !strings.Contains(ack.Error, "2") || !strings.Contains(ack.Error, "3") {
		t.Errorf("ack.Error = %q, want reference to chunk 2 of 3", ack.Error)
	}
}

// ---------------------------------------------------------------------------
// F4: hostile index injection tests (new — RED phase)
// ---------------------------------------------------------------------------

// TestSend_HostileIndexNameStaysWellFormed (F4): index name containing quote/brace
// injection characters must NOT alter the /_bulk NDJSON structure.
func TestSend_HostileIndexNameStaysWellFormed(t *testing.T) {
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			clusterInfoOK(w)
		case "/_bulk":
			body, _ := io.ReadAll(r.Body)
			capturedBody = body
			bulkSuccess(w)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Hostile index: contains quotes and braces that would break string concatenation.
	hostileIndex := `evil"}},{"delete":{"_index":"x`

	c := elastic.NewWithClient(elastic.Config{
		Endpoint: srv.URL,
		APIKey:   "id:key",
		Index:    hostileIndex,
	}, srv.Client())

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	ack, err := c.Send(context.Background(), makeBatch(1))
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if ack.Status != "delivered" {
		t.Fatalf("expected Status=delivered, got %q", ack.Status)
	}

	// Parse NDJSON: odd lines (0, 2, ...) are action lines, even lines (1, 3, ...) are docs.
	lines := strings.Split(strings.TrimSpace(string(capturedBody)), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 NDJSON lines, got %d", len(lines))
	}

	// Assert no "delete" action appears as a separate JSON document.
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &doc); err != nil {
			t.Errorf("line %d is not valid JSON: %v (line=%q)", i, err, line)
			continue
		}
		if _, hasDelete := doc["delete"]; hasDelete {
			t.Errorf("line %d contains injected 'delete' action: %q", i, line)
		}
	}

	// Assert every action line has exactly one top-level key ("index") and
	// the _index value equals the hostile string verbatim (escaped, not interpreted).
	for i := 0; i < len(lines); i += 2 {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var action map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &action); err != nil {
			t.Errorf("action line %d not valid JSON: %v", i, err)
			continue
		}
		if len(action) != 1 {
			t.Errorf("action line %d has %d top-level keys, want 1: %q", i, len(action), line)
		}
		indexRaw, ok := action["index"]
		if !ok {
			t.Errorf("action line %d missing 'index' key: %q", i, line)
			continue
		}
		var indexObj map[string]string
		if err := json.Unmarshal(indexRaw, &indexObj); err != nil {
			t.Errorf("action line %d index value not valid JSON object: %v", i, err)
			continue
		}
		if got := indexObj["_index"]; got != hostileIndex {
			t.Errorf("action line %d _index = %q, want %q", i, got, hostileIndex)
		}
	}
}

// ---------------------------------------------------------------------------
// F16: APIKey zeroing tests (new — RED phase)
// ---------------------------------------------------------------------------

// TestNew_APIKeyZeroedAfterHeader (F16): after New(), the raw APIKey must be
// cleared from cfg and apiKeyHeader must be correctly computed.
// White-box test — package elastic.
func TestNew_APIKeyZeroedAfterHeader(t *testing.T) {
	c := elastic.New(elastic.Config{
		Endpoint: "https://localhost:9200",
		APIKey:   "id:key",
		Index:    "argus-signals",
	})
	// The exported APIKey field must be empty after New().
	if c.Cfg().APIKey != "" {
		t.Errorf("expected cfg.APIKey to be zeroed after New(), got %q", c.Cfg().APIKey)
	}
	// The pre-computed header must equal the expected base64 encoding.
	import64 := "ApiKey " + "aWQ6a2V5" // base64("id:key") == "aWQ6a2V5"
	if c.APIKeyHeader() != import64 {
		t.Errorf("apiKeyHeader = %q, want %q", c.APIKeyHeader(), import64)
	}
}

// TestConnect_EmptyAPIKeyStillRejected (F16 regression): New with empty APIKey →
// Connect returns error (now via apiKeyHeader == "" check).
func TestConnect_EmptyAPIKeyStillRejected(t *testing.T) {
	c := elastic.New(elastic.Config{
		Endpoint: "https://localhost:9200",
		APIKey:   "",
	})
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error for empty APIKey, got nil")
	}
	if !strings.Contains(err.Error(), "APIKey is required") {
		t.Fatalf("expected 'APIKey is required' error, got %q", err.Error())
	}
}

// TestSend_AllUnmappableStillDelivered (regression): batch where every signal
// fails OCSF mapping → ack delivered, err nil, zero POSTs.
func TestSend_AllUnmappableStillDelivered(t *testing.T) {
	var postCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			clusterInfoOK(w)
		case "/_bulk":
			atomic.AddInt32(&postCount, 1)
			bulkSuccess(w)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := elastic.NewWithClient(elastic.Config{
		Endpoint:     srv.URL,
		APIKey:       "id:key",
		Index:        "argus-signals",
		MaxBatchDocs: 2,
	}, srv.Client())

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	ack, err := c.Send(context.Background(), makeUnmappableBatch(4))

	if err != nil {
		t.Errorf("Send() with all-unmappable batch must return nil error, got: %v", err)
	}
	if ack == nil {
		t.Fatal("Send() returned nil ack")
	}
	if ack.Status != "delivered" {
		t.Errorf("ack.Status = %q, want %q (all-unmappable = empty payload = delivered)", ack.Status, "delivered")
	}
	if n := atomic.LoadInt32(&postCount); n != 0 {
		t.Errorf("POST count = %d, want 0 (no /_bulk POSTs for empty payload)", n)
	}
}
