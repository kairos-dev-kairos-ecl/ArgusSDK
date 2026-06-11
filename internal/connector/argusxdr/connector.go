// Package argusxdr implements the Mode 1 output connector: signal delivery to
// an ArgusXDR instance using the ArgusSignal protobuf over gRPC/TLS.
// No OCSF translation occurs in this path — the proto schema is the wire format.
package argusxdr

import (
	"context"
	"fmt"
	"time"

	sdkv1 "github.com/kairos-dev-kairos-ecl/ArgusSDK/gen/go/sdk/v1"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// sdkVersion is stamped into every proto SignalBatch.
const sdkVersion = "argus-sdk/4"

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
	Credential string // rotating API credential (used as x-argus-api-key)
}

// apiKeyCreds implements credentials.PerRPCCredentials.
// It attaches the API key as "x-argus-api-key" on every RPC call.
type apiKeyCreds struct {
	apiKey     string
	requireTLS bool
}

func (a apiKeyCreds) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{"x-argus-api-key": a.apiKey}, nil
}

func (a apiKeyCreds) RequireTransportSecurity() bool {
	return a.requireTLS
}

// Connector delivers signal batches to an ArgusXDR ingest endpoint via gRPC.
type Connector struct {
	cfg    Config
	conn   *grpc.ClientConn
	client sdkv1.SDKIngestServiceClient
}

// New creates an ArgusXDR connector. Call Connect before sending.
func New(cfg Config) *Connector {
	return &Connector{cfg: cfg}
}

// NewWithClient creates a Connector with a pre-wired client stub, bypassing
// real gRPC dialing. Intended for unit tests only.
func NewWithClient(cfg Config, client sdkv1.SDKIngestServiceClient) *Connector {
	return &Connector{cfg: cfg, client: client}
}

// Name implements connector.Connector.
func (c *Connector) Name() string { return "argusxdr" }

// Connect establishes the gRPC connection to XDR.
//
// When cfg.TLS.Enabled is true, transport credentials are built from
// connector.NewTLSConfig (enforces TLS 1.3 minimum). For local/loopback use
// (e.g. unit tests that call Connect directly), TLS.Enabled should be false.
//
// Per-RPC API-key credentials are always installed via grpc.WithPerRPCCredentials.
// RequireTransportSecurity is set to true when TLS is enabled, preventing the key
// from being sent over a cleartext connection (T-04-07).
func (c *Connector) Connect(_ context.Context) error {
	if c.cfg.Endpoint == "" {
		return fmt.Errorf("argusxdr: endpoint must not be empty")
	}

	var transportCreds credentials.TransportCredentials
	if c.cfg.TLS.Enabled {
		tlsCfg, err := connector.NewTLSConfig(connector.TLSClientConfig{
			CACert:   c.cfg.TLS.CACert,
			CertFile: c.cfg.TLS.CertFile,
			KeyFile:  c.cfg.TLS.KeyFile,
		})
		if err != nil {
			return fmt.Errorf("argusxdr: build TLS config: %w", err)
		}
		transportCreds = credentials.NewTLS(tlsCfg)
	} else {
		// Only acceptable for loopback / test usage; TLS is required in production.
		transportCreds = insecure.NewCredentials()
	}

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(transportCreds),
		grpc.WithPerRPCCredentials(apiKeyCreds{
			apiKey:     c.cfg.Auth.Credential,
			requireTLS: c.cfg.TLS.Enabled,
		}),
	}

	conn, err := grpc.NewClient(c.cfg.Endpoint, opts...)
	if err != nil {
		return fmt.Errorf("argusxdr: dial %s: %w", c.cfg.Endpoint, err)
	}

	c.conn = conn
	c.client = sdkv1.NewSDKIngestServiceClient(conn)
	return nil
}

// Send marshals the batch as a proto SignalBatch and calls IngestBatch.
//
// Key contracts (locked decisions 5/9, threat model T-04-07..T-04-11):
//   - UseOCSF is silently ignored — argusxdr always sends the proto wire format (T-04-10).
//   - InstanceID and GroupID travel as per-RPC gRPC metadata (T-04-11), never in the proto body.
//   - Proto AppId/Env come from the connector.SignalBatch fields (populated by plan 04-05);
//     reading Signals[0].AppID is explicitly forbidden (signal.FromProto leaves it empty).
//   - A gRPC error returns both a non-nil error AND a failed ack (T-04-09 repudiation mitigation).
func (c *Connector) Send(ctx context.Context, batch *connector.SignalBatch) (*connector.DeliveryAck, error) {
	if c.client == nil {
		return &connector.DeliveryAck{
			BatchID:   batch.BatchID,
			Status:    "failed",
			Error:     "client not initialised — call Connect first",
			Timestamp: time.Now(),
		}, fmt.Errorf("argusxdr: client not initialised")
	}

	// Reconstruct a signal.Batch from the connector.SignalBatch.
	// AppID and Env are intentionally sourced from batch.AppID/batch.Env (added by 04-05);
	// they will be empty strings until 04-05 populates them — that is correct behaviour here.
	// NEVER read batch.Signals[0].AppID; signal.FromProto leaves that field empty by design.
	sigBatch := signal.Batch{
		// AppID and Env: connector.SignalBatch does not have these fields yet (04-05 adds them).
		// We leave them as empty strings to match the current struct definition.
		Signals: batch.Signals,
	}

	maxSize := c.cfg.MaxBatchSize
	if maxSize <= 0 {
		maxSize = 500
	}

	// Chunk the batch sequentially; abort on the first failure (locked decision 9).
	total := len(sigBatch.Signals)
	if total == 0 {
		// Single empty call so the server can echo the BatchId.
		return c.sendChunk(ctx, batch, sigBatch.Signals, batch.BatchID)
	}

	var lastAck *connector.DeliveryAck
	for start := 0; start < total; start += maxSize {
		end := start + maxSize
		if end > total {
			end = total
		}
		chunkSignals := sigBatch.Signals[start:end]
		ack, err := c.sendChunk(ctx, batch, chunkSignals, batch.BatchID)
		if err != nil {
			return ack, fmt.Errorf("argusxdr: chunk %d/%d: %w", start/maxSize+1, (total+maxSize-1)/maxSize, err)
		}
		lastAck = ack
	}
	return lastAck, nil
}

// sendChunk performs a single IngestBatch RPC for the supplied signal slice.
func (c *Connector) sendChunk(ctx context.Context, batch *connector.SignalBatch, signals []signal.Signal, batchID string) (*connector.DeliveryAck, error) {
	// Build proto batch — AppID/Env are "" until 04-05 adds those fields to connector.SignalBatch.
	protoBatch := signal.Batch{Signals: signals}.ToProto(batchID, sdkVersion)

	// Attach InstanceID and GroupID as per-RPC metadata (T-04-11).
	// The proto SignalBatch has no InstanceId/GroupId fields — they are ONLY sent here.
	mdCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"x-argus-instance-id", batch.InstanceID,
		"x-argus-group-id", batch.GroupID,
	))

	ack, err := c.client.IngestBatch(mdCtx, protoBatch)
	if err != nil {
		return &connector.DeliveryAck{
			BatchID:   batch.BatchID,
			Status:    "failed",
			Error:     err.Error(),
			Timestamp: time.Now(),
		}, fmt.Errorf("IngestBatch: %w", err)
	}

	return &connector.DeliveryAck{
		BatchID:   batch.BatchID,
		Status:    "delivered",
		RemoteID:  ack.BatchId,
		Timestamp: time.Now(),
	}, nil
}

// Health sends an empty batch to verify the endpoint is reachable.
// A response (including rejection) indicates the service is up.
func (c *Connector) Health(ctx context.Context) error {
	if c.client == nil {
		return fmt.Errorf("argusxdr: client not initialised")
	}
	// Use an empty batch with a sentinel BatchId.
	_, err := c.client.IngestBatch(ctx, &sdkv1.SignalBatch{BatchId: "health-check"})
	if err != nil {
		return fmt.Errorf("argusxdr health: %w", err)
	}
	return nil
}

// Close tears down the gRPC connection.
func (c *Connector) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
