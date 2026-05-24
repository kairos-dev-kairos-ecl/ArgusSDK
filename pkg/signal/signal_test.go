package signal

import (
	"testing"
	"time"

	sdkv1 "github.com/kairos-dev-kairos-ecl/ArgusSDK/gen/go/sdk/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestFromProtoRoundTrip(t *testing.T) {
	// Test 1 — FromProto identity round-trip: all 14 fields populated
	t.Run("AllFieldsPopulated", func(t *testing.T) {
		now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
		p := &sdkv1.SDKSignal{
			SignalId:       "01ARZ3NDEKTSV4RRFFQ69G5FAV",
			TraceId:        "trace-abc-123",
			SpanId:         "span-def-456",
			ParentSpanId:   "span-parent-789",
			Layer:          5,
			Category:       "agent.tool_call",
			Severity:       3,
			Timestamp:      timestamppb.New(now),
			DurationMs:     42.5,
			ContextJson:    []byte(`{"key":"value"}`),
			SessionId:      "session-111",
			ConversationId: "conv-222",
			UserId:         "hashed-user-333",
			AppVersion:     "2.0.0",
		}

		s := FromProto(p)

		if s.SignalID != p.SignalId {
			t.Errorf("SignalID: got %q, want %q", s.SignalID, p.SignalId)
		}
		if s.TraceID != p.TraceId {
			t.Errorf("TraceID: got %q, want %q", s.TraceID, p.TraceId)
		}
		if s.SpanID != p.SpanId {
			t.Errorf("SpanID: got %q, want %q", s.SpanID, p.SpanId)
		}
		if s.ParentSpanID != p.ParentSpanId {
			t.Errorf("ParentSpanID: got %q, want %q", s.ParentSpanID, p.ParentSpanId)
		}
		if s.Layer != Layer(p.Layer) {
			t.Errorf("Layer: got %v, want %v", s.Layer, Layer(p.Layer))
		}
		if s.Category != p.Category {
			t.Errorf("Category: got %q, want %q", s.Category, p.Category)
		}
		if s.Severity != Severity(p.Severity) {
			t.Errorf("Severity: got %v, want %v", s.Severity, Severity(p.Severity))
		}
		if !s.Timestamp.Equal(now) {
			t.Errorf("Timestamp: got %v, want %v", s.Timestamp, now)
		}
		if s.DurationMS != p.DurationMs {
			t.Errorf("DurationMS: got %v, want %v", s.DurationMS, p.DurationMs)
		}
		if string(s.ContextJSON) != string(p.ContextJson) {
			t.Errorf("ContextJSON: got %q, want %q", s.ContextJSON, p.ContextJson)
		}
		if s.SessionID != p.SessionId {
			t.Errorf("SessionID: got %q, want %q", s.SessionID, p.SessionId)
		}
		if s.ConversationID != p.ConversationId {
			t.Errorf("ConversationID: got %q, want %q", s.ConversationID, p.ConversationId)
		}
		if s.UserID != p.UserId {
			t.Errorf("UserID: got %q, want %q", s.UserID, p.UserId)
		}
		if s.AppVersion != p.AppVersion {
			t.Errorf("AppVersion: got %q, want %q", s.AppVersion, p.AppVersion)
		}
	})

	// Test 2 — FromProto nil timestamp guard: no panic, zero time returned
	t.Run("NilTimestamp", func(t *testing.T) {
		p := &sdkv1.SDKSignal{
			SignalId:  "signal-nil-ts",
			Timestamp: nil,
		}
		// Must not panic
		s := FromProto(p)
		var zero time.Time
		if !s.Timestamp.Equal(zero) {
			t.Errorf("Timestamp: got %v, want zero time", s.Timestamp)
		}
	})

	// Test 3 — Batch.ToProto round-trip
	t.Run("BatchToProto", func(t *testing.T) {
		now := time.Date(2024, 6, 1, 8, 0, 0, 0, time.UTC)
		sig := Signal{
			SignalID:       "sig-001",
			TraceID:        "trace-xyz",
			SpanID:         "span-xyz",
			ParentSpanID:   "span-parent",
			Layer:          L7RAGRetrieval,
			Category:       "retrieval.search",
			Severity:       SeverityMedium,
			Timestamp:      now,
			DurationMS:     10.0,
			ContextJSON:    []byte(`{"query":"hello"}`),
			SessionID:      "sess-abc",
			ConversationID: "conv-abc",
			UserID:         "user-hashed",
			AppVersion:     "1.2.3",
		}
		b := Batch{
			AppID:   "test-app",
			Env:     "dev",
			Signals: []Signal{sig},
		}

		pb := b.ToProto("batch-123", "1.0.0")

		if pb.BatchId != "batch-123" {
			t.Errorf("BatchId: got %q, want %q", pb.BatchId, "batch-123")
		}
		if pb.AppId != "test-app" {
			t.Errorf("AppId: got %q, want %q", pb.AppId, "test-app")
		}
		if pb.Env != "dev" {
			t.Errorf("Env: got %q, want %q", pb.Env, "dev")
		}
		if pb.SdkVersion != "1.0.0" {
			t.Errorf("SdkVersion: got %q, want %q", pb.SdkVersion, "1.0.0")
		}
		if len(pb.Signals) != 1 {
			t.Fatalf("Signals: got %d, want 1", len(pb.Signals))
		}

		ps := pb.Signals[0]
		if ps.SignalId != sig.SignalID {
			t.Errorf("Signals[0].SignalId: got %q, want %q", ps.SignalId, sig.SignalID)
		}
		if ps.Layer != int32(sig.Layer) {
			t.Errorf("Signals[0].Layer: got %v, want %v", ps.Layer, int32(sig.Layer))
		}
		if ps.Severity != int32(sig.Severity) {
			t.Errorf("Signals[0].Severity: got %v, want %v", ps.Severity, int32(sig.Severity))
		}
		if ps.Timestamp == nil {
			t.Fatal("Signals[0].Timestamp: got nil, want non-nil")
		}
		if !ps.Timestamp.AsTime().Equal(now) {
			t.Errorf("Signals[0].Timestamp: got %v, want %v", ps.Timestamp.AsTime(), now)
		}
	})

	// Test 4 — FromProto does NOT set AppID, Env, or SDKVersion
	t.Run("NoAppIDEnvSDKVersion", func(t *testing.T) {
		p := &sdkv1.SDKSignal{
			SignalId: "sig-check-fields",
			// SDKSignal has no app_id, env, or sdk_version fields
		}
		s := FromProto(p)
		if s.AppID != "" {
			t.Errorf("AppID: got %q, want empty string", s.AppID)
		}
		if s.Env != "" {
			t.Errorf("Env: got %q, want empty string", s.Env)
		}
		if s.SDKVersion != "" {
			t.Errorf("SDKVersion: got %q, want empty string", s.SDKVersion)
		}
	})
}
