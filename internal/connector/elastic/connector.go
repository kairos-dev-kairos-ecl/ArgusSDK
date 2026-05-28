// Package elastic implements the Mode 2 output connector for Elasticsearch and
// OpenSearch clusters, using the /_bulk NDJSON API with API key authentication.
//
// Security properties:
//   - TLS is built via connector.NewTLSConfig, which enforces TLS 1.3 minimum
//     and never sets InsecureSkipVerify.
//   - The API key is carried in the Authorization header and is never logged.
//   - Only the endpoint URL and index name are logged; credentials are not.
//   - The /_bulk response is parsed into a fixed struct; errors:true is treated
//     as a delivery failure. No eval or dynamic dispatch occurs.
package elastic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/ocsf"
)

// TLSConfig holds the TLS certificate parameters for the Elasticsearch connection.
type TLSConfig struct {
	// CACert is an optional path to a PEM-encoded CA certificate.
	// Empty uses system roots.
	CACert string

	// CertFile and KeyFile are optional paths for mTLS client authentication.
	CertFile string
	KeyFile  string
}

// Config holds the parameters for connecting to an Elasticsearch or OpenSearch cluster.
type Config struct {
	// Endpoint is the base URL of the cluster, e.g. "https://localhost:9200".
	Endpoint string

	// APIKey is the raw "id:key" string. The connector base64-encodes it for the
	// Authorization header; callers must NOT pre-encode the value.
	APIKey string

	// Index is the target index name. Defaults to "argus-signals".
	Index string

	// TLS controls certificate verification.
	TLS TLSConfig

	// MaxBatchDocs is the maximum number of documents per /_bulk call.
	// Defaults to 500.
	MaxBatchDocs int
}

// bulkResponse is the top-level JSON structure returned by the /_bulk API.
type bulkResponse struct {
	Took   int  `json:"took"`
	Errors bool `json:"errors"`
}

// clusterInfoResponse is the JSON structure returned by GET /.
type clusterInfoResponse struct {
	ClusterName string `json:"cluster_name"`
}

// clusterHealthResponse is the JSON structure returned by GET /_cluster/health.
type clusterHealthResponse struct {
	Status string `json:"status"`
}

// Connector delivers OCSF-encoded signal events to an Elasticsearch or OpenSearch
// cluster using the /_bulk API with NDJSON format and API key authentication.
// Use New to construct; call Connect before calling Send or Health.
type Connector struct {
	cfg          Config
	client       *http.Client
	mapper       *ocsf.Mapper
	apiKeyHeader string // pre-computed "ApiKey <base64(id:key)>"
}

// New creates an Elastic connector with the given configuration.
// Call Connect before sending.
func New(cfg Config) *Connector {
	if cfg.Index == "" {
		cfg.Index = "argus-signals"
	}
	if cfg.MaxBatchDocs == 0 {
		cfg.MaxBatchDocs = 500
	}

	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "argus-agent"
	}

	apiKeyHeader := ""
	if cfg.APIKey != "" {
		encoded := base64.StdEncoding.EncodeToString([]byte(cfg.APIKey))
		apiKeyHeader = "ApiKey " + encoded
	}

	return &Connector{
		cfg:          cfg,
		mapper:       ocsf.NewMapper("argus-sdk", hostname),
		apiKeyHeader: apiKeyHeader,
	}
}

// NewWithClient creates a Connector with an injected *http.Client.
// Intended for unit tests that need to supply an httptest.Server client.
func NewWithClient(cfg Config, client *http.Client) *Connector {
	c := New(cfg)
	c.client = client
	return c
}

// Name implements connector.Connector. Returns "elastic".
func (c *Connector) Name() string { return "elastic" }

// Connect validates configuration, builds an HTTP client with TLS 1.3 enforced,
// and verifies the cluster is reachable by issuing GET <Endpoint>/ and parsing
// the cluster_name field from the response. Returns error if Endpoint or APIKey
// is empty, TLS configuration fails, or the server returns a non-200 status.
func (c *Connector) Connect(ctx context.Context) error {
	if c.cfg.Endpoint == "" {
		return fmt.Errorf("elastic: Endpoint is required")
	}
	if c.cfg.APIKey == "" {
		return fmt.Errorf("elastic: APIKey is required")
	}

	// Build HTTP client with TLS 1.3 minimum if one was not injected by tests.
	if c.client == nil {
		tlsCfg, err := connector.NewTLSConfig(connector.TLSClientConfig{
			CACert:   c.cfg.TLS.CACert,
			CertFile: c.cfg.TLS.CertFile,
			KeyFile:  c.cfg.TLS.KeyFile,
		})
		if err != nil {
			return fmt.Errorf("elastic: building TLS config: %w", err)
		}
		c.client = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: tlsCfg,
			},
		}
	}

	// Verify cluster reachability via GET /.
	url := c.cfg.Endpoint + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("elastic: building cluster-info request: %w", err)
	}
	req.Header.Set("Authorization", c.apiKeyHeader)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("elastic: cluster-info request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("elastic: cluster-info returned status %d: %s", resp.StatusCode, string(body))
	}

	var info clusterInfoResponse
	if err := json.Unmarshal(body, &info); err != nil {
		return fmt.Errorf("elastic: parsing cluster-info response: %w", err)
	}

	return nil
}

// Send builds an NDJSON /_bulk payload from the signals in batch, posts it to
// <Endpoint>/_bulk with API key authentication, and returns a DeliveryAck.
//
// Each signal is converted to an OCSF Event via the mapper, then projected to
// a flat ECS document with the following field mapping:
//
//	@timestamp    ← time.UnixMilli(ev.Time).UTC().Format(time.RFC3339Nano)
//	event.code    ← strconv.Itoa(int(ev.ClassUID))
//	event.category ← []string{ev.CategoryName}
//	event.kind    ← "event"
//	event.severity ← severityIDToECSText(ev.SeverityID)
//	agent.name    ← ev.Metadata.Product.VendorName
//	event.module  ← ev.Metadata.Product.Name
//	event.uid     ← ev.Metadata.UID
//
// Signals that fail OCSF mapping are skipped. An empty payload (all skipped)
// returns Status="delivered". Returns Status="failed" if the bulk response
// contains errors:true.
func (c *Connector) Send(ctx context.Context, batch *connector.SignalBatch) (*connector.DeliveryAck, error) {
	if c.client == nil {
		return &connector.DeliveryAck{
			BatchID:   batch.BatchID,
			Status:    "failed",
			Error:     "connector not connected",
			Timestamp: time.Now(),
		}, fmt.Errorf("elastic: Send called before Connect")
	}

	limit := c.cfg.MaxBatchDocs
	signals := batch.Signals
	if limit > 0 && len(signals) > limit {
		signals = signals[:limit]
	}

	var buf bytes.Buffer
	actionLine := []byte(`{"index":{"_index":"` + c.cfg.Index + `"}}` + "\n")

	for _, s := range signals {
		ev, err := c.mapper.Map(s)
		if err != nil {
			// Skip unmappable signals; do not fail the whole batch.
			continue
		}

		doc := ocsfToECS(ev)
		docBytes, err := json.Marshal(doc)
		if err != nil {
			continue
		}

		buf.Write(actionLine)
		buf.Write(docBytes)
		buf.WriteByte('\n')
	}

	if buf.Len() == 0 {
		// All signals were unmappable — return delivered for an empty payload.
		return &connector.DeliveryAck{
			BatchID:   batch.BatchID,
			Status:    "delivered",
			Timestamp: time.Now(),
		}, nil
	}

	url := c.cfg.Endpoint + "/_bulk"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return &connector.DeliveryAck{
			BatchID:   batch.BatchID,
			Status:    "failed",
			Error:     err.Error(),
			Timestamp: time.Now(),
		}, fmt.Errorf("elastic: building /_bulk request: %w", err)
	}
	req.Header.Set("Authorization", c.apiKeyHeader)
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return &connector.DeliveryAck{
			BatchID:   batch.BatchID,
			Status:    "failed",
			Error:     err.Error(),
			Timestamp: time.Now(),
		}, fmt.Errorf("elastic: posting to /_bulk: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return &connector.DeliveryAck{
			BatchID:   batch.BatchID,
			Status:    "failed",
			Error:     fmt.Sprintf("reading /_bulk response: %v", err),
			Timestamp: time.Now(),
		}, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &connector.DeliveryAck{
			BatchID:   batch.BatchID,
			Status:    "failed",
			Error:     fmt.Sprintf("/_bulk returned status %d: %s", resp.StatusCode, string(bodyBytes)),
			Timestamp: time.Now(),
		}, nil
	}

	var bulkResp bulkResponse
	if err := json.Unmarshal(bodyBytes, &bulkResp); err != nil {
		return &connector.DeliveryAck{
			BatchID:   batch.BatchID,
			Status:    "failed",
			Error:     fmt.Sprintf("elastic: unparseable /_bulk response: %s", string(bodyBytes)),
			Timestamp: time.Now(),
		}, nil
	}

	if bulkResp.Errors {
		return &connector.DeliveryAck{
			BatchID:   batch.BatchID,
			Status:    "failed",
			Error:     "elastic: /_bulk response contains errors",
			Timestamp: time.Now(),
		}, nil
	}

	return &connector.DeliveryAck{
		BatchID:   batch.BatchID,
		Status:    "delivered",
		Timestamp: time.Now(),
	}, nil
}

// Health issues GET <Endpoint>/_cluster/health and returns nil when the cluster
// status is "green" or "yellow". Returns a non-nil error for "red" or non-200 HTTP status.
func (c *Connector) Health(ctx context.Context) error {
	if c.client == nil {
		return fmt.Errorf("elastic: connector not connected")
	}

	url := c.cfg.Endpoint + "/_cluster/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("elastic: building health request: %w", err)
	}
	req.Header.Set("Authorization", c.apiKeyHeader)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("elastic: health check failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("elastic: /_cluster/health returned status %d", resp.StatusCode)
	}

	var health clusterHealthResponse
	if err := json.Unmarshal(body, &health); err != nil {
		return fmt.Errorf("elastic: parsing health response: %w", err)
	}

	switch health.Status {
	case "green", "yellow":
		return nil
	case "red":
		return fmt.Errorf("elastic: cluster health is red")
	default:
		return fmt.Errorf("elastic: unknown cluster health status %q", health.Status)
	}
}

// Close releases the HTTP client's idle connections. No-ops if the client is nil.
func (c *Connector) Close() error {
	if c.client != nil {
		c.client.CloseIdleConnections()
	}
	return nil
}

// ocsfToECS projects an OCSF Event to a flat ECS document (map[string]interface{}).
// The mapping covers the 8 key ECS fields required for Elasticsearch indexing:
//
//	@timestamp     ← time.UnixMilli(ev.Time).UTC().Format(time.RFC3339Nano)
//	event.code     ← strconv.Itoa(int(ev.ClassUID))
//	event.category ← []string{ev.CategoryName}
//	event.kind     ← "event"
//	event.severity ← severityIDToECSText(ev.SeverityID)
//	agent.name     ← ev.Metadata.Product.VendorName
//	event.module   ← ev.Metadata.Product.Name
//	event.uid      ← ev.Metadata.UID
func ocsfToECS(ev *ocsf.Event) map[string]interface{} {
	ts := time.UnixMilli(ev.Time).UTC().Format(time.RFC3339Nano)

	doc := map[string]interface{}{
		"@timestamp":     ts,
		"event.code":     strconv.Itoa(int(ev.ClassUID)),
		"event.category": []string{ev.CategoryName},
		"event.kind":     "event",
		"event.severity": severityIDToECSText(ev.SeverityID),
		"agent.name":     ev.Metadata.Product.VendorName,
		"event.module":   ev.Metadata.Product.Name,
		"event.uid":      ev.Metadata.UID,
	}

	return doc
}

// severityIDToECSText converts an OCSF severity_id to the ECS text severity string.
// OCSF severity_id values: 1=Informational, 2=Low, 3=Medium, 4=High, 5=Critical.
func severityIDToECSText(id int) string {
	switch id {
	case 1:
		return "informational"
	case 2:
		return "low"
	case 3:
		return "medium"
	case 4:
		return "high"
	case 5:
		return "critical"
	default:
		return "unknown"
	}
}
