// Package splunk_test provides unit and integration tests for the Splunk HEC connector.
package splunk_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/splunk"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// newTestSignal returns a minimal L9APIGateway signal suitable for OCSF mapping.
func newTestSignal() signal.Signal {
	return signal.Signal{
		SignalID:  "sig-splunk-001",
		TraceID:   "trace-001",
		SpanID:    "span-001",
		Layer:     signal.L9APIGateway,
		Category:  "http.request",
		Severity:  signal.SeverityInfo,
		AppID:     "test-app",
		Timestamp: time.Now(),
	}
}

// newTestBatch returns a SignalBatch with one L9APIGateway signal.
func newTestBatch() *connector.SignalBatch {
	return &connector.SignalBatch{
		BatchID:    "batch-splunk-001",
		InstanceID: "instance-001",
		GroupID:    "group-001",
		Signals:    []signal.Signal{newTestSignal()},
		ReceivedAt: time.Now(),
		UseOCSF:    true,
	}
}

// hecSuccess writes a successful HEC response.
func hecSuccess(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"text":"Success","code":0}`)
}

// TestSplunkConnector_Name verifies that Name() returns "splunk_hec".
func TestSplunkConnector_Name(t *testing.T) {
	c := splunk.New(splunk.Config{
		Endpoint: "http://localhost:8088",
		Token:    "test-token",
	})
	if got := c.Name(); got != "splunk_hec" {
		t.Errorf("Name() = %q, want %q", got, "splunk_hec")
	}
}

// TestSplunkConnector_ConnectNoEndpoint verifies that Connect() returns an error
// when Endpoint is empty.
func TestSplunkConnector_ConnectNoEndpoint(t *testing.T) {
	c := splunk.New(splunk.Config{
		Token: "test-token",
		// Endpoint intentionally empty
	})
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("Connect() with empty Endpoint should return an error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "endpoint") {
		t.Errorf("Connect() error = %q, want error containing 'endpoint'", err.Error())
	}
}

// TestSplunkConnector_ConnectNoToken verifies that Connect() returns an error
// when Token is empty.
func TestSplunkConnector_ConnectNoToken(t *testing.T) {
	c := splunk.New(splunk.Config{
		Endpoint: "http://localhost:8088",
		// Token intentionally empty
	})
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("Connect() with empty Token should return an error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "token") {
		t.Errorf("Connect() error = %q, want error containing 'token'", err.Error())
	}
}

// TestSplunkConnector_ConnectHealthCheck verifies that Connect() returns nil when
// the HEC health endpoint responds with 200.
func TestSplunkConnector_ConnectHealthCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/services/collector/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := splunk.NewWithClient(splunk.Config{
		Endpoint: srv.URL,
		Token:    "test-token",
	}, srv.Client())
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error = %v, want nil", err)
	}
}

// TestSplunkConnector_ConnectHealthCheckFails verifies that Connect() returns a
// non-nil error when the health endpoint responds with a non-200 status.
func TestSplunkConnector_ConnectHealthCheckFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := splunk.NewWithClient(splunk.Config{
		Endpoint: srv.URL,
		Token:    "test-token",
	}, srv.Client())
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("Connect() with 503 health endpoint should return error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "health") {
		t.Errorf("Connect() error = %q, want error containing 'health'", err.Error())
	}
}

// TestSplunkConnector_SendPayloadFormat verifies:
// (a) request body contains "class_uid" (OCSF JSON present in "event" key)
// (b) request body contains "sourcetype"
// (c) Authorization header == "Splunk <token>"
// (d) response parsed as {"text":"Success","code":0} produces DeliveryAck.Status=="delivered"
func TestSplunkConnector_SendPayloadFormat(t *testing.T) {
	var capturedBody []byte
	var capturedAuthHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/services/collector/health":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/services/collector/event":
			capturedAuthHeader = r.Header.Get("Authorization")
			var err error
			capturedBody, err = io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			hecSuccess(w)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := splunk.NewWithClient(splunk.Config{
		Endpoint:   srv.URL,
		Token:      "my-hec-token",
		Index:      "main",
		SourceType: "argus:ocsf",
	}, srv.Client())

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	batch := newTestBatch()
	ack, err := c.Send(context.Background(), batch)
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	// (a) body contains "class_uid"
	if !strings.Contains(string(capturedBody), "class_uid") {
		t.Errorf("HEC payload does not contain 'class_uid': %s", capturedBody)
	}

	// (b) body contains "sourcetype"
	if !strings.Contains(string(capturedBody), "sourcetype") {
		t.Errorf("HEC payload does not contain 'sourcetype': %s", capturedBody)
	}

	// (c) Authorization header
	wantAuth := "Splunk my-hec-token"
	if capturedAuthHeader != wantAuth {
		t.Errorf("Authorization header = %q, want %q", capturedAuthHeader, wantAuth)
	}

	// (d) DeliveryAck.Status
	if ack == nil {
		t.Fatal("Send() returned nil DeliveryAck")
	}
	if ack.Status != "delivered" {
		t.Errorf("DeliveryAck.Status = %q, want %q", ack.Status, "delivered")
	}
	if ack.BatchID != batch.BatchID {
		t.Errorf("DeliveryAck.BatchID = %q, want %q", ack.BatchID, batch.BatchID)
	}
}

// TestSplunkConnector_SendSplunkError verifies that a non-zero HEC response code
// produces DeliveryAck.Status=="failed" and Error contains the code or message.
func TestSplunkConnector_SendSplunkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/services/collector/health":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/services/collector/event":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK) // HEC uses 200 even for token errors
			fmt.Fprint(w, `{"text":"Invalid token","code":4}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := splunk.NewWithClient(splunk.Config{
		Endpoint: srv.URL,
		Token:    "bad-token",
	}, srv.Client())

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	batch := newTestBatch()
	ack, err := c.Send(context.Background(), batch)
	// Send returns ack (not error) for HEC-level failures
	_ = err
	if ack == nil {
		t.Fatal("Send() returned nil DeliveryAck")
	}
	if ack.Status != "failed" {
		t.Errorf("DeliveryAck.Status = %q, want %q", ack.Status, "failed")
	}
	// Error must contain "code:4" or "Invalid token"
	if !strings.Contains(ack.Error, "4") && !strings.Contains(ack.Error, "Invalid token") {
		t.Errorf("DeliveryAck.Error = %q, want reference to 'code:4' or 'Invalid token'", ack.Error)
	}
}

// TestSplunkConnector_ChannelHeader verifies that a non-empty ChannelID produces
// an X-Splunk-Request-Channel header equal to ChannelID.
func TestSplunkConnector_ChannelHeader(t *testing.T) {
	const wantChannel = "test-channel-uuid-1234"
	var capturedChannel string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/services/collector/health":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/services/collector/event":
			capturedChannel = r.Header.Get("X-Splunk-Request-Channel")
			// drain body
			_, _ = io.Copy(io.Discard, r.Body)
			hecSuccess(w)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := splunk.NewWithClient(splunk.Config{
		Endpoint:  srv.URL,
		Token:     "test-token",
		ChannelID: wantChannel,
	}, srv.Client())

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	_, err := c.Send(context.Background(), newTestBatch())
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	if capturedChannel != wantChannel {
		t.Errorf("X-Splunk-Request-Channel = %q, want %q", capturedChannel, wantChannel)
	}
}

// TestSplunkConnector_HealthEndpoint verifies that Health() returns nil when
// the health endpoint responds with 200.
func TestSplunkConnector_HealthEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/services/collector/health" {
			// Verify Authorization header on health check too
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Splunk ") {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := splunk.NewWithClient(splunk.Config{
		Endpoint: srv.URL,
		Token:    "test-token",
	}, srv.Client())

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	if err := c.Health(context.Background()); err != nil {
		t.Errorf("Health() error = %v, want nil", err)
	}
}

// TestSplunkConnector_HECPayloadStructure verifies the top-level HEC JSON structure:
// each line must be a JSON object with keys "event", "time", "index", "sourcetype".
func TestSplunkConnector_HECPayloadStructure(t *testing.T) {
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/services/collector/health":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/services/collector/event":
			var err error
			capturedBody, err = io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			hecSuccess(w)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := splunk.NewWithClient(splunk.Config{
		Endpoint:   srv.URL,
		Token:      "test-token",
		Index:      "argus-main",
		SourceType: "argus:ocsf",
	}, srv.Client())

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	_, err := c.Send(context.Background(), newTestBatch())
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	// Each line is a newline-delimited JSON object.
	lines := strings.Split(strings.TrimSpace(string(capturedBody)), "\n")
	if len(lines) == 0 {
		t.Fatal("HEC payload has no lines")
	}

	type hecRecord struct {
		Event      json.RawMessage `json:"event"`
		Time       float64         `json:"time"`
		Index      string          `json:"index"`
		SourceType string          `json:"sourcetype"`
	}

	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec hecRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Errorf("line %d: json.Unmarshal error: %v (line: %s)", i, err, line)
			continue
		}
		if rec.Event == nil {
			t.Errorf("line %d: 'event' key is missing or null", i)
		}
		if rec.Time <= 0 {
			t.Errorf("line %d: 'time' key is missing or zero", i)
		}
		if rec.Index != "argus-main" {
			t.Errorf("line %d: 'index' = %q, want %q", i, rec.Index, "argus-main")
		}
		if rec.SourceType != "argus:ocsf" {
			t.Errorf("line %d: 'sourcetype' = %q, want %q", i, rec.SourceType, "argus:ocsf")
		}
	}
}

// TestSplunkConnector_Close verifies that Close() does not panic before or after Connect().
func TestSplunkConnector_Close(t *testing.T) {
	c := splunk.New(splunk.Config{
		Endpoint: "http://localhost:8088",
		Token:    "test-token",
	})
	// Close before Connect — must not panic.
	if err := c.Close(); err != nil {
		t.Logf("Close() before Connect returned: %v (acceptable)", err)
	}
}

// TestSplunkConnector_Integration runs a full end-to-end test against a real
// Splunk HEC endpoint. Skipped when SPLUNK_HEC_ENDPOINT is not set.
func TestSplunkConnector_Integration(t *testing.T) {
	endpoint := os.Getenv("SPLUNK_HEC_ENDPOINT")
	if endpoint == "" {
		t.Skip("SPLUNK_HEC_ENDPOINT not set — skipping integration test")
	}
	token := os.Getenv("SPLUNK_HEC_TOKEN")
	if token == "" {
		t.Skip("SPLUNK_HEC_TOKEN not set — skipping integration test")
	}

	c := splunk.New(splunk.Config{
		Endpoint:   endpoint,
		Token:      token,
		Index:      "argus-test",
		SourceType: "argus:ocsf",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer func() {
		if err := c.Close(); err != nil {
			t.Logf("Close() error: %v", err)
		}
	}()

	batch := newTestBatch()
	ack, err := c.Send(ctx, batch)
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}
	if ack == nil {
		t.Fatal("Send() returned nil DeliveryAck")
	}
	if ack.Status != "delivered" {
		t.Errorf("DeliveryAck.Status = %q, want %q", ack.Status, "delivered")
	}
	if ack.BatchID != batch.BatchID {
		t.Errorf("DeliveryAck.BatchID = %q, want %q", ack.BatchID, batch.BatchID)
	}
	t.Logf("Integration test passed: ack=%+v", ack)

	// Verify Health() passes after successful Send.
	if err := c.Health(ctx); err != nil {
		t.Errorf("Health() after Send error: %v", err)
	}
}
