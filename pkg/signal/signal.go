// Package signal defines the public types that Python/TypeScript instrumentation
// libraries use when submitting signals to the local argus-agent over gRPC or
// Unix socket. These types are the wire contract between the SDK libs and the
// agent; they do NOT reference any XDR-internal packages.
package signal

import (
	"time"

	sdkv1 "github.com/kairos-dev-kairos-ecl/ArgusSDK/gen/go/sdk/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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

// FromProto converts a proto SDKSignal to the internal Signal type.
// It maps all 14 SDKSignal fields. Note that AppID, Env, and SDKVersion are
// NOT set by this function — those fields come from the enclosing SignalBatch
// and must be set by the caller after receiving the batch.
//
// If p.Timestamp is nil (proto3 optional message field), Signal.Timestamp is
// left as the zero value (time.Time{}) to avoid a nil-pointer panic.
func FromProto(p *sdkv1.SDKSignal) Signal {
	s := Signal{
		SignalID:       p.SignalId,
		TraceID:        p.TraceId,
		SpanID:         p.SpanId,
		ParentSpanID:   p.ParentSpanId,
		Layer:          Layer(p.Layer),
		Category:       p.Category,
		Severity:       Severity(p.Severity),
		DurationMS:     p.DurationMs,
		ContextJSON:    p.ContextJson,
		SessionID:      p.SessionId,
		ConversationID: p.ConversationId,
		UserID:         p.UserId,
		AppVersion:     p.AppVersion,
		// AppID, Env, SDKVersion intentionally omitted — set from SignalBatch by caller
	}
	if p.Timestamp != nil {
		s.Timestamp = p.Timestamp.AsTime()
	}
	return s
}

// toProtoSignal converts an internal Signal to a proto SDKSignal.
// This is the reverse of FromProto and maps all 14 fields.
func toProtoSignal(s Signal) *sdkv1.SDKSignal {
	return &sdkv1.SDKSignal{
		SignalId:       s.SignalID,
		TraceId:        s.TraceID,
		SpanId:         s.SpanID,
		ParentSpanId:   s.ParentSpanID,
		Layer:          int32(s.Layer),
		Category:       s.Category,
		Severity:       int32(s.Severity),
		Timestamp:      timestamppb.New(s.Timestamp),
		DurationMs:     s.DurationMS,
		ContextJson:    s.ContextJSON,
		SessionId:      s.SessionID,
		ConversationId: s.ConversationID,
		UserId:         s.UserID,
		AppVersion:     s.AppVersion,
	}
}

// ToProto converts a Batch to a proto SignalBatch.
// batchID is the ULID assigned by the caller (echoed in BatchAck.batch_id).
// sdkVersion is the version of the SDK library producing this batch.
func (b Batch) ToProto(batchID, sdkVersion string) *sdkv1.SignalBatch {
	signals := make([]*sdkv1.SDKSignal, len(b.Signals))
	for i, s := range b.Signals {
		signals[i] = toProtoSignal(s)
	}
	return &sdkv1.SignalBatch{
		BatchId:    batchID,
		AppId:      b.AppID,
		Env:        b.Env,
		Signals:    signals,
		SdkVersion: sdkVersion,
	}
}
