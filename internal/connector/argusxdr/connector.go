// Package argusxdr implements the Mode 1 output connector: signal delivery to
// an ArgusXDR instance using the existing ArgusSignal protobuf over gRPC.
// No OCSF translation occurs in this path — the proto schema is the wire format.
package argusxdr

import (
	"context"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
)

// Config holds connection parameters for the ArgusXDR connector.
type Config struct {
	// Endpoint is the gRPC address of the XDR ingest service (host:port).
	Endpoint string

	// TLS configures transport security. Mandatory for remote mode (non-localhost).
	TLS TLSConfig

	// Auth carries the credentials stamped on every batch.
	Auth AuthConfig

	// MaxBatchSize is the maximum number of signals per gRPC call. Default: 500.
	MaxBatchSize int
}

// TLSConfig controls TLS behaviour for the gRPC connection.
type TLSConfig struct {
	Enabled  bool
	CACert   string // path to custom CA PEM; empty = system roots
	CertFile string // client cert for mTLS (optional)
	KeyFile  string // client key for mTLS (optional)
}

// AuthConfig carries the three-part SDK auth identity.
type AuthConfig struct {
	GroupID    string
	InstanceID string // populated after registration
	Credential string // rotating API credential
}

// Connector delivers signal batches to an ArgusXDR ingest endpoint via gRPC.
//
// Implementation TODO (not in this scaffold):
//   - grpc.Dial with TLS and keepalive
//   - proto-marshal ArgusSignal batch via generated IngestServiceClient
//   - X-Argus-API-Key header injection via grpc.WithPerRPCCredentials
//   - exponential backoff on transient errors, surfaced to caller for CB wrapping
type Connector struct {
	cfg    Config
	// client IngestServiceClient  // wired in during implementation
}

// New creates an ArgusXDR connector. Call Connect before sending.
func New(cfg Config) *Connector {
	return &Connector{cfg: cfg}
}

// Name implements connector.Connector.
func (c *Connector) Name() string { return "argusxdr" }

// Connect establishes the gRPC connection to XDR.
func (c *Connector) Connect(_ context.Context) error {
	// TODO: grpc.DialContext with TLS options, store client stub
	return nil
}

// Send marshals the batch as ArgusSignal proto and streams it to XDR ingest.
// batch.UseOCSF must be false; Mode 1 never translates to OCSF.
func (c *Connector) Send(_ context.Context, batch *connector.SignalBatch) (*connector.DeliveryAck, error) {
	// TODO: proto-marshal batch.Signals → []ArgusSignal, call IngestService.BatchIngest
	return &connector.DeliveryAck{BatchID: batch.BatchID, Status: "delivered"}, nil
}

// Health pings the XDR health endpoint.
func (c *Connector) Health(_ context.Context) error {
	// TODO: call HealthService.Check
	return nil
}

// Close tears down the gRPC connection.
func (c *Connector) Close() error {
	// TODO: conn.Close()
	return nil
}
