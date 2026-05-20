// Package signal defines the public types that Python/TypeScript instrumentation
// libraries use when submitting signals to the local argus-agent over gRPC or
// Unix socket. These types are the wire contract between the SDK libs and the
// agent; they do NOT reference any XDR-internal packages.
package signal

import "time"

// Layer mirrors the 10-layer LLM taxonomy from the ArgusSignal proto.
// Values are kept numerically identical so proto enum casts are zero-cost.
type Layer int32

const (
	LayerUnspecified  Layer = 0
	L1Hardware        Layer = 1
	L2ModelWeights    Layer = 2
	L3Tokenizer       Layer = 3
	L4Transformer     Layer = 4
	L5OutputDecoding  Layer = 5
	L6Safety          Layer = 6
	L7RAGRetrieval    Layer = 7
	L8Agents          Layer = 8
	L9APIGateway      Layer = 9
	L10Application    Layer = 10
	LDecision         Layer = 11
)

// Severity mirrors the ArgusSignal proto Severity enum.
type Severity int32

const (
	SeverityUnspecified Severity = 0
	SeverityInfo        Severity = 1
	SeverityLow         Severity = 2
	SeverityMedium      Severity = 3
	SeverityHigh        Severity = 4
	SeverityCritical    Severity = 5
)

// Signal is the normalised representation of a signal received from an
// instrumentation library. The agent enriches this before forwarding to outputs.
type Signal struct {
	// Identity
	SignalID     string // ULID assigned by the emitting library
	TraceID      string
	SpanID       string
	ParentSpanID string // empty if root span

	// Classification
	Layer    Layer
	Category string   // e.g. "retrieval.search", "agent.tool_call"
	Severity Severity

	// Source metadata (set by the lib, verified by the agent)
	AppID      string
	AppVersion string
	SDKVersion string
	Env        string // dev | staging | prod

	// Temporal
	Timestamp  time.Time
	DurationMS float32

	// Layer-specific payload as a raw JSON blob.
	// The agent validates structure per-layer; libs that pre-encode context
	// avoid repeated serialisation on the hot path.
	ContextJSON []byte

	// Optional relationship fields
	SessionID      string
	ConversationID string
	UserID         string // hashed
}

// Batch is the unit of work the agent accepts from instrumentation libs.
// A batch MUST contain signals from the same AppID and Env.
type Batch struct {
	AppID   string
	Env     string
	Signals []Signal
}
