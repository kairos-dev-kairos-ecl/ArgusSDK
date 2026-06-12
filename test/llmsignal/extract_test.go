//go:build llmlocal

package llmsignal

import (
	"context"
	"testing"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// TestExtractionFidelity (Goal 2) runs a real prompt pass against every
// available backend and asserts the extracted signal faithfully captures the
// interaction: model, latency, token usage, trace correlation, classification,
// and ContextJSON provenance.
func TestExtractionFidelity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	for _, backend := range availableBackends(ctx, t) {
		backend := backend
		t.Run(backend.Name(), func(t *testing.T) {
			res, err := backend.Chat(ctx, "Reply with exactly one word: hello")
			if err != nil {
				t.Fatalf("%s prompt pass failed: %v", backend.Name(), err)
			}
			if res.Content == "" {
				t.Fatal("expected non-empty completion content")
			}
			t.Logf("%s: model=%s latency=%s tokens(p/c/t)=%d/%d/%d finish=%q",
				backend.Name(), res.Model, res.Latency,
				res.PromptTokens, res.CompletionTokens, res.TotalTokens, res.FinishReason)

			sig := ExtractSignal(res)

			// Identity / correlation.
			if sig.SignalID == "" || sig.TraceID == "" || sig.SpanID == "" {
				t.Errorf("expected non-empty SignalID/TraceID/SpanID, got %q/%q/%q",
					sig.SignalID, sig.TraceID, sig.SpanID)
			}

			// Classification.
			if sig.Layer != signal.L4Transformer {
				t.Errorf("expected Layer L4Transformer, got %v", sig.Layer)
			}
			if sig.Category != CategoryChatCompletion {
				t.Errorf("expected Category %q, got %q", CategoryChatCompletion, sig.Category)
			}
			if sig.Severity != signal.SeverityInfo {
				t.Errorf("expected Severity Info, got %v", sig.Severity)
			}

			// Latency must be a positive measured duration.
			if sig.DurationMS <= 0 {
				t.Errorf("expected positive DurationMS, got %v", sig.DurationMS)
			}

			// ContextJSON provenance.
			cc, err := decodeContext(sig.ContextJSON)
			if err != nil {
				t.Fatalf("ContextJSON did not parse: %v", err)
			}
			if cc.Backend != backend.Name() {
				t.Errorf("ContextJSON backend: got %q, want %q", cc.Backend, backend.Name())
			}
			if cc.Model == "" {
				t.Error("ContextJSON model is empty")
			}
			if cc.Endpoint != backend.Endpoint() {
				t.Errorf("ContextJSON endpoint: got %q, want %q", cc.Endpoint, backend.Endpoint())
			}
			if cc.CompletionChars <= 0 {
				t.Error("expected completion_chars > 0")
			}

			// Token accounting: when the backend reports usage, total must reconcile.
			if cc.TotalTokens > 0 && cc.TotalTokens != cc.PromptTokens+cc.CompletionTokens {
				t.Errorf("token totals do not reconcile: total=%d prompt=%d completion=%d",
					cc.TotalTokens, cc.PromptTokens, cc.CompletionTokens)
			}
			if cc.PromptTokens == 0 && cc.CompletionTokens == 0 {
				t.Logf("note: %s reported no token usage (acceptable — usage is optional)", backend.Name())
			}
		})
	}
}
