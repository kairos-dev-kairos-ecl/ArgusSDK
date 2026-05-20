// Package splunk implements the Mode 2 output connector: OCSF-translated signal
// delivery to a Splunk HTTP Event Collector (HEC) endpoint.
package splunk

import (
	"context"
	"fmt"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
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

	// TLS controls certificate verification. InsecureSkipVerify should only
	// be used in development; it has no effect when Endpoint uses http://.
	TLS TLSConfig

	// MaxBatchEvents is the maximum number of OCSF events per HEC request.
	// Splunk HEC accepts up to 10 MB per request. Default: 1000.
	MaxBatchEvents int

	// ChannelID enables HEC indexer acknowledgement. Leave empty to disable.
	ChannelID string
}

// TLSConfig controls TLS verification for the HEC connection.
type TLSConfig struct {
	CACert             string
	InsecureSkipVerify bool // do not set true in production
}

// Connector delivers OCSF-encoded signal events to a Splunk HEC endpoint.
//
// Implementation TODO (not in this scaffold):
//   - net/http client with configurable TLS and keepalive
//   - POST to <Endpoint>/services/collector/event with Authorization: Splunk <Token>
//   - JSON body: newline-delimited {"event":<ocsf_json>,"index":"...","sourcetype":"..."} records
//   - Parse {"text":"Success","code":0} response; surface non-zero codes as errors
//   - Respect HEC channel acknowledgement if ChannelID is set
type Connector struct {
	cfg Config
	// client *http.Client  // wired in during implementation
}

// New creates a Splunk HEC connector. Call Connect before sending.
func New(cfg Config) *Connector {
	if cfg.SourceType == "" {
		cfg.SourceType = "argus:ocsf"
	}
	if cfg.MaxBatchEvents == 0 {
		cfg.MaxBatchEvents = 1000
	}
	return &Connector{cfg: cfg}
}

// Name implements connector.Connector.
func (c *Connector) Name() string { return "splunk_hec" }

// Connect validates the HEC endpoint is reachable.
func (c *Connector) Connect(_ context.Context) error {
	if c.cfg.Endpoint == "" {
		return fmt.Errorf("splunk_hec: endpoint is required")
	}
	if c.cfg.Token == "" {
		return fmt.Errorf("splunk_hec: token is required")
	}
	// TODO: HTTP GET <Endpoint>/services/collector/health, verify 200
	return nil
}

// Send posts OCSF-encoded events to the HEC endpoint.
// batch.UseOCSF must be true; translation has already occurred.
func (c *Connector) Send(_ context.Context, batch *connector.SignalBatch) (*connector.DeliveryAck, error) {
	// TODO: build newline-delimited HEC payload, POST, check response code
	return &connector.DeliveryAck{BatchID: batch.BatchID, Status: "delivered"}, nil
}

// Health calls the HEC health endpoint (/services/collector/health).
func (c *Connector) Health(_ context.Context) error {
	// TODO: GET <Endpoint>/services/collector/health
	return nil
}

// Close releases the HTTP client idle connections.
func (c *Connector) Close() error {
	// TODO: client.CloseIdleConnections()
	return nil
}
