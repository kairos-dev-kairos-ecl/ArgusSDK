//go:build llmlocal

package llmsignal

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// CategoryChatCompletion is the signal category for an LLM chat-completion call.
const CategoryChatCompletion = "llm.chat_completion"

// extractedContext is the per-signal ContextJSON produced from a prompt pass.
// Prompt and completion are truncated to previews — a signal records provenance
// and shape, not full payloads (privacy-preserving, matches the EUC contract).
type extractedContext struct {
	Backend          string `json:"backend"`
	Model            string `json:"model"`
	Endpoint         string `json:"endpoint"`
	PromptPreview    string `json:"prompt_preview"`
	PromptChars      int    `json:"prompt_chars"`
	CompletionPreview string `json:"completion_preview"`
	CompletionChars  int    `json:"completion_chars"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
	FinishReason     string `json:"finish_reason"`
}

const previewLimit = 200

// ExtractSignal converts a real chat-completion result into a normalised
// signal.Signal — this is the role a Python/TypeScript SDK plays in production.
// It is the unit under test for "signal extraction fidelity":
//   - Layer L4Transformer: a chat completion is model inference.
//   - DurationMS is the measured end-to-end latency.
//   - Trace/Span IDs are generated per call so the agent can correlate spans.
//   - ContextJSON carries model/token/finish provenance.
func ExtractSignal(res *ChatResult) signal.Signal {
	ctxJSON, _ := json.Marshal(extractedContext{
		Backend:           res.Backend,
		Model:             res.Model,
		Endpoint:          res.Endpoint,
		PromptPreview:     truncate(res.Prompt, previewLimit),
		PromptChars:       len(res.Prompt),
		CompletionPreview: truncate(res.Content, previewLimit),
		CompletionChars:   len(res.Content),
		PromptTokens:      res.PromptTokens,
		CompletionTokens:  res.CompletionTokens,
		TotalTokens:       res.TotalTokens,
		FinishReason:      res.FinishReason,
	})

	return signal.Signal{
		SignalID:    newID(),
		TraceID:     newID(),
		SpanID:      newID(),
		Layer:       signal.L4Transformer,
		Category:    CategoryChatCompletion,
		Severity:    signal.SeverityInfo,
		DurationMS:  float32(res.Latency.Microseconds()) / 1000.0,
		ContextJSON: ctxJSON,
	}
}

// ExtractBatch wraps one or more results into a signal.Batch for a given app/env.
func ExtractBatch(appID, env string, results ...*ChatResult) signal.Batch {
	sigs := make([]signal.Signal, 0, len(results))
	for _, r := range results {
		sigs = append(sigs, ExtractSignal(r))
	}
	return signal.Batch{AppID: appID, Env: env, Signals: sigs}
}

// decodeContext re-parses a signal's ContextJSON for assertions.
func decodeContext(b []byte) (extractedContext, error) {
	var c extractedContext
	err := json.Unmarshal(b, &c)
	return c, err
}

// decodeJSON is a small assertion helper for unmarshalling arbitrary ContextJSON.
func decodeJSON(b []byte, v interface{}) error {
	return json.Unmarshal(b, v)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
