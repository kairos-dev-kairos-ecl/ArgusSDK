// Package splunk implements the Mode 2 output connector: OCSF-translated signal
// delivery to a Splunk HTTP Event Collector (HEC) endpoint.
//
// Security properties:
//   - TLS is built via connector.NewTLSConfig which enforces TLS 1.3 minimum
//     and never sets InsecureSkipVerify.
//   - HEC token is set in the Authorization header; it is never logged.
//   - Only the endpoint URL is logged; credentials are not.
package splunk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/ocsf"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// Config holds HEC connection parameters.
type Config struct {
	// Endpoint is the full HEC URL, e.g. "https://splunk.company.com:8088".
	Endpoint string

	// Token is the Splunk HEC authentication token.
	Token string

	// Index is the target Splunk index. Empty uses the HEC token default.
	Index string

	// Source and SourceType override HEC defaults.
	Source     string
	SourceType string // Default: "argus:ocsf"

	// TLS controls certificate verification. InsecureSkipVerify has no effect —
	// it is retained for struct compatibility only. The HTTP client is always
	// built via connector.NewTLSConfig which enforces TLS 1.3 and never sets
	// InsecureSkipVerify. If InsecureSkipVerify is true a warning is logged.
	TLS TLSConfig

	// MaxBatchEvents is the maximum number of OCSF events per HEC request.
	// Splunk HEC accepts up to 10 MB per request. Default: 1000.
	MaxBatchEvents int

	// ChannelID enables HEC indexer acknowledgement. Leave empty to disable.
	ChannelID string
}

// TLSConfig controls TLS certificate configuration for the HEC connection.
// InsecureSkipVerify is retained for struct compatibility but is ignored —
// NewTLSConfig never sets InsecureSkipVerify, enforcing TLS 1.3 at minimum.
type TLSConfig struct {
	CACert             string
	InsecureSkipVerify bool // ignored — present for struct compatibility only; see package doc
}

// hecResponse is the JSON response body returned by Splunk HEC.
type hecResponse struct {
	Text string `json:"text"`
	Code int    `json:"code"`
}

// Connector delivers OCSF-encoded signal events to a Splunk HEC endpoint.
// Use New to construct; call Connect before Send.
type Connector struct {
	cfg    Config
	client *http.Client
	mapper *ocsf.Mapper
}

// New creates a Splunk HEC connector. Call Connect before sending.
func New(cfg Config) *Connector {
	if cfg.SourceType == "" {
		cfg.SourceType = "argus:ocsf"
	}
	if cfg.MaxBatchEvents == 0 {
		cfg.MaxBatchEvents = 1000
	}

	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "argus-agent"
	}

	return &Connector{
		cfg:    cfg,
		mapper: ocsf.NewMapper("argus-sdk", hostname),
	}
}

// NewWithClient creates a Connector with a pre-built *http.Client.
// This constructor is package-internal and intended for unit tests that need to
// inject an httptest.Server client to avoid TLS certificate complexity.
func NewWithClient(cfg Config, client *http.Client) *Connector {
	c := New(cfg)
	c.client = client
	return c
}

// Name implements connector.Connector.
func (c *Connector) Name() string { return "splunk_hec" }

// Connect validates the HEC endpoint and token, builds the HTTP client with
// TLS 1.3 enforced via connector.NewTLSConfig, and verifies the HEC health
// endpoint returns 200. Returns error if endpoint or token is empty, TLS
// configuration fails, or the health check fails.
func (c *Connector) Connect(ctx context.Context) error {
	if c.cfg.Endpoint == "" {
		return fmt.Errorf("splunk_hec: endpoint is required")
	}
	if c.cfg.Token == "" {
		return fmt.Errorf("splunk_hec: token is required")
	}

	// Warn (but do not reject) if caller set InsecureSkipVerify — it is ignored.
	if c.cfg.TLS.InsecureSkipVerify {
		log.Printf("splunk_hec: WARNING: TLS.InsecureSkipVerify is set but ignored — connector always enforces TLS 1.3 certificate verification (endpoint: %s)", c.cfg.Endpoint)
	}

	// Build HTTP client with TLS 1.3 minimum if one was not injected by tests.
	if c.client == nil {
		tlsCfg, err := connector.NewTLSConfig(connector.TLSClientConfig{
			CACert: c.cfg.TLS.CACert,
		})
		if err != nil {
			return fmt.Errorf("splunk_hec: building TLS config: %w", err)
		}
		c.client = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: tlsCfg,
			},
		}
	}

	// Health check — verify HEC is reachable and accepting requests.
	if err := c.healthCheck(ctx); err != nil {
		return err
	}
	return nil
}

// Send delivers the batch to Splunk HEC using ceil(n/limit) sequential chunk
// requests (F7). Each chunk is a newline-delimited NDJSON HEC payload.
//
// Locked delivery contract (F6, locked decision 3):
//   - Any non-delivered outcome returns a non-nil error AND a failed ack.
//   - Abort on the first failed chunk; remaining signals count as failed (F7, locked decision 4).
//   - Signals that fail OCSF mapping are skipped; an entirely-unmappable batch
//     returns Status="delivered" with no HEC POST (existing behaviour preserved).
func (c *Connector) Send(ctx context.Context, batch *connector.SignalBatch) (*connector.DeliveryAck, error) {
	if c.client == nil {
		err := fmt.Errorf("splunk_hec: Send called before Connect")
		return &connector.DeliveryAck{
			BatchID:   batch.BatchID,
			Status:    "failed",
			Error:     "connector not connected",
			Timestamp: time.Now(),
		}, err
	}

	signals := batch.Signals
	limit := c.cfg.MaxBatchEvents

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

		if err := c.sendChunk(ctx, batch.BatchID, chunk, i+1, total); err != nil {
			detail := fmt.Sprintf("chunk %d/%d: %v", i+1, total, err)
			return &connector.DeliveryAck{
				BatchID:   batch.BatchID,
				Status:    "failed",
				Error:     detail,
				Timestamp: time.Now(),
			}, fmt.Errorf("splunk_hec: %s", detail)
		}
	}

	return &connector.DeliveryAck{
		BatchID:   batch.BatchID,
		Status:    "delivered",
		Timestamp: time.Now(),
	}, nil
}

// sendChunk builds and POSTs a single NDJSON HEC payload for the given signals.
// It returns nil only when the HTTP status is 2xx AND hecResp.Code == 0.
// Every failure path returns a descriptive non-nil error (F6 contract).
// Signals that fail OCSF mapping are skipped; if all signals are unmappable
// the chunk is empty and no POST is issued (returns nil — not a failure).
func (c *Connector) sendChunk(ctx context.Context, batchID string, signals []signal.Signal, chunkNum, total int) error {
	var buf bytes.Buffer
	for _, s := range signals {
		ev, err := c.mapper.Map(s)
		if err != nil {
			// Skip unmappable signals; do not fail the whole chunk.
			continue
		}

		eventJSON, err := json.Marshal(ev)
		if err != nil {
			continue
		}

		// HEC record: {"event":<ocsf_json>,"time":<unix_float>,"index":"...","sourcetype":"..."}
		record := struct {
			Event      json.RawMessage `json:"event"`
			Time       float64         `json:"time"`
			Index      string          `json:"index,omitempty"`
			SourceType string          `json:"sourcetype,omitempty"`
			Source     string          `json:"source,omitempty"`
		}{
			Event:      json.RawMessage(eventJSON),
			Time:       float64(s.Timestamp.UnixMilli()) / 1000.0,
			Index:      c.cfg.Index,
			SourceType: c.cfg.SourceType,
			Source:     c.cfg.Source,
		}

		recBytes, err := json.Marshal(record)
		if err != nil {
			continue
		}
		buf.Write(recBytes)
		buf.WriteByte('\n')
	}

	if buf.Len() == 0 {
		// All signals in this chunk were unmappable — skip the POST.
		return nil
	}

	url := c.cfg.Endpoint + "/services/collector/event"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Splunk "+c.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.ChannelID != "" {
		req.Header.Set("X-Splunk-Request-Channel", c.cfg.ChannelID)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("posting to HEC: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading HEC response: %w", err)
	}

	// Explicit non-2xx HTTP status check (F6: was missing before).
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HEC returned HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var hecResp hecResponse
	if err := json.Unmarshal(bodyBytes, &hecResp); err != nil {
		return fmt.Errorf("unparseable HEC response: %s", string(bodyBytes))
	}

	if hecResp.Code != 0 {
		return fmt.Errorf("%s (code:%d)", hecResp.Text, hecResp.Code)
	}

	return nil
}

// Health calls GET <Endpoint>/services/collector/health and returns nil on 200.
// Called by the Dispatcher on a heartbeat interval and after failures.
func (c *Connector) Health(ctx context.Context) error {
	return c.healthCheck(ctx)
}

// Close releases the HTTP client idle connections.
func (c *Connector) Close() error {
	if c.client != nil {
		c.client.CloseIdleConnections()
	}
	return nil
}

// healthCheck issues GET <Endpoint>/services/collector/health and returns an
// error if the response status is not 200.
func (c *Connector) healthCheck(ctx context.Context) error {
	url := c.cfg.Endpoint + "/services/collector/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("splunk_hec: building health request: %w", err)
	}
	req.Header.Set("Authorization", "Splunk "+c.cfg.Token)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("splunk_hec: health check failed: %w", err)
	}
	defer resp.Body.Close()
	// Drain body to allow connection reuse.
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("splunk_hec: HEC health check failed: status %d", resp.StatusCode)
	}
	return nil
}
