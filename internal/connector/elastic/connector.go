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
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
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

// Cfg returns a copy of the stored configuration. Intended for white-box tests only.
// NOTE: cfg.APIKey is always "" after New() — zeroed for security (F16).
func (c *Connector) Cfg() Config { return c.cfg }

// APIKeyHeader returns the pre-computed Authorization header value.
// Intended for white-box tests only.
func (c *Connector) APIKeyHeader() string { return c.apiKeyHeader }

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
	// F16: zero the raw APIKey after computing the header — it must not be retained
	// in process memory on the Connector struct after New() returns.
	cfg.APIKey = ""

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
	// F16: check apiKeyHeader (not cfg.APIKey — raw key is zeroed after New()).
	if c.apiKeyHeader == "" {
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

// Send delivers the batch to Elasticsearch using ceil(n/limit) sequential /_bulk
// requests (F7). Each chunk is a newline-delimited NDJSON payload.
//
// Locked delivery contract (F6, locked decision 3):
//   - Any non-delivered outcome returns a non-nil error AND a failed ack.
//   - Abort on the first failed chunk; remaining signals count as failed (F7, locked decision 4).
//   - Signals that fail OCSF mapping are skipped; an entirely-unmappable batch
//     returns Status="delivered" with no /_bulk POST (existing behaviour preserved).
//
// Security: action lines are produced via json.Marshal (F4 — T-03-11 mitigated).
func (c *Connector) Send(ctx context.Context, batch *connector.SignalBatch) (*connector.DeliveryAck, error) {
	if c.client == nil {
		err := fmt.Errorf("elastic: Send called before Connect")
		return &connector.DeliveryAck{
			BatchID:   batch.BatchID,
			Status:    "failed",
			Error:     "connector not connected",
			Timestamp: time.Now(),
		}, err
	}

	signals := batch.Signals
	limit := c.cfg.MaxBatchDocs

	// Determine chunk size; limit <= 0 means one chunk containing all signals.
	chunkSize := len(signals)
	if limit > 0 && limit < chunkSize {
		chunkSize = limit
	}

	// Calculate total number of chunks (ceil(n / chunkSize)).
	total := 1
	if chunkSize > 0 && len(signals) > 0 {
		total = (len(signals) + chunkSize - 1) / chunkSize
	}
	if len(signals) == 0 {
		total = 1
	}

	for i := 0; i < total; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(signals) {
			end = len(signals)
		}
		chunk := signals[start:end]

		if err := c.sendChunk(ctx, chunk); err != nil {
			detail := fmt.Sprintf("chunk %d/%d: %v", i+1, total, err)
			return &connector.DeliveryAck{
				BatchID:   batch.BatchID,
				Status:    "failed",
				Error:     detail,
				Timestamp: time.Now(),
			}, fmt.Errorf("elastic: %s", detail)
		}
	}

	return &connector.DeliveryAck{
		BatchID:   batch.BatchID,
		Status:    "delivered",
		Timestamp: time.Now(),
	}, nil
}

// sendChunk builds and POSTs a single /_bulk NDJSON payload for the given signals.
// It returns nil only when the HTTP status is 2xx AND bulkResp.Errors == false.
// Every failure path returns a descriptive non-nil error (F6 contract).
// Signals that fail OCSF mapping are skipped; if all signals are unmappable
// the chunk is empty and no POST is issued (returns nil — not a failure).
//
// F4: the action line is produced by json.Marshal of a typed struct so that a
// hostile cfg.Index value containing quotes or braces cannot inject /_bulk API
// parameters (T-03-11 mitigated).
func (c *Connector) sendChunk(ctx context.Context, signals []signal.Signal) error {
	var buf bytes.Buffer
	// F4: use json.Marshal to build the action line — cfg.Index is treated as data,
	// never as syntax. A Marshal error on a plain string map is effectively impossible
	// but is handled defensively by returning a failed error.
	actionObj := map[string]interface{}{"index": map[string]string{"_index": c.cfg.Index}}
	actionBytes, err := json.Marshal(actionObj)
	if err != nil {
		return fmt.Errorf("building action line: %w", err)
	}
	actionLine := append(actionBytes, '\n')

	for _, s := range signals {
		ev, err := c.mapper.Map(s)
		if err != nil {
			// Skip unmappable signals; do not fail the whole chunk.
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
		// All signals in this chunk were unmappable — skip the POST.
		return nil
	}

	url := c.cfg.Endpoint + "/_bulk"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return fmt.Errorf("building /_bulk request: %w", err)
	}
	req.Header.Set("Authorization", c.apiKeyHeader)
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("posting to /_bulk: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading /_bulk response: %w", err)
	}

	// Explicit non-2xx HTTP status check (F6: was missing for body-read path before).
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("/_bulk returned HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var bulkResp bulkResponse
	if err := json.Unmarshal(bodyBytes, &bulkResp); err != nil {
		return fmt.Errorf("unparseable /_bulk response: %s", string(bodyBytes))
	}

	if bulkResp.Errors {
		return fmt.Errorf("/_bulk response contains errors")
	}

	return nil
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
