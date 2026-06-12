//go:build llmlocal

package llmsignal

import (
	"context"
	"testing"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/ocsf"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// TestConnectorWireOutput (Goal 4) takes signals extracted from a real LLM call
// and asserts the two delivery wire formats carry the interaction faithfully:
//   - Mode 1 (argusxdr): signal.Batch.ToProto — the exact proto argusxdr.Send marshals.
//   - Mode 2 (kafka/splunk/elastic): ocsf.Mapper.Map — the OCSF event the others emit.
func TestConnectorWireOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	backend := availableBackends(ctx, t)[0]
	res, err := backend.Chat(ctx, "Name one color.")
	if err != nil {
		t.Fatalf("%s prompt pass failed: %v", backend.Name(), err)
	}
	batch := ExtractBatch("wire-app", "prod", res)

	t.Run("argusxdr_proto", func(t *testing.T) {
		// This is precisely the marshal argusxdr.sendChunk performs.
		proto := batch.ToProto("wire-batch-1", "llmsignal-test/1")

		if proto.GetAppId() != "wire-app" {
			t.Errorf("proto AppId: got %q, want %q", proto.GetAppId(), "wire-app")
		}
		if proto.GetEnv() != "prod" {
			t.Errorf("proto Env: got %q, want %q", proto.GetEnv(), "prod")
		}
		if len(proto.GetSignals()) != 1 {
			t.Fatalf("proto signals: got %d, want 1", len(proto.GetSignals()))
		}
		ps := proto.GetSignals()[0]
		if ps.GetCategory() != CategoryChatCompletion {
			t.Errorf("proto signal category: got %q, want %q", ps.GetCategory(), CategoryChatCompletion)
		}
		if ps.GetLayer() != int32(signal.L4Transformer) {
			t.Errorf("proto signal layer: got %d, want %d", ps.GetLayer(), int32(signal.L4Transformer))
		}
		if ps.GetDurationMs() <= 0 {
			t.Errorf("proto signal duration_ms: got %v, want > 0", ps.GetDurationMs())
		}
		cc, err := decodeContext(ps.GetContextJson())
		if err != nil {
			t.Fatalf("proto context_json parse: %v", err)
		}
		if cc.Backend != backend.Name() || cc.Model == "" {
			t.Errorf("proto context_json provenance lost: backend=%q model=%q", cc.Backend, cc.Model)
		}
	})

	t.Run("ocsf_event", func(t *testing.T) {
		mapper := ocsf.NewMapper("llmsignal-test/1", "test-host")
		sig := batch.Signals[0]
		// ToProto leaves Timestamp; ensure a sane time for OCSF Time field.
		if sig.Timestamp.IsZero() {
			sig.Timestamp = time.Now()
		}
		ev, err := mapper.Map(sig)
		if err != nil {
			t.Fatalf("ocsf Map: %v", err)
		}
		if ev == nil {
			t.Fatal("expected non-nil OCSF event")
		}
		if ev.ClassUID == 0 {
			t.Error("expected a non-zero OCSF class_uid")
		}
		if got, _ := ev.Unmapped["category"].(string); got != CategoryChatCompletion {
			t.Errorf("ocsf unmapped category: got %v, want %q", ev.Unmapped["category"], CategoryChatCompletion)
		}
		if _, ok := ev.Unmapped["context"]; !ok {
			t.Error("expected raw LLM context attached to OCSF event under unmapped.context")
		}
		t.Logf("ocsf: class_uid=%d class=%s category=%s", ev.ClassUID, ev.ClassName, ev.CategoryName)
	})
}
