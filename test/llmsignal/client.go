//go:build llmlocal

// Package llmsignal is a local, real-infrastructure test suite that drives a
// local LLM runtime (Ollama or vLLM) with real prompt passes and validates the
// full signal lifecycle of argus-agent against the resulting interactions.
//
// It is gated behind the `llmlocal` build tag so it never runs in the default
// unit suite or the Docker integration job — it requires a local LLM server.
// Run it explicitly:
//
//	make test-llm
//	# or
//	go test -tags=llmlocal ./test/llmsignal/... -v
//
// Both Ollama and vLLM expose an OpenAI-compatible /v1 surface, so a single
// client implementation drives either backend. The suite probes each backend
// and skips cleanly when neither is reachable, so it is safe to run anywhere.
//
// Backend selection / overrides (env):
//
//	ARGUS_TEST_OLLAMA_URL    (default http://127.0.0.1:11434)
//	ARGUS_TEST_OLLAMA_MODEL  (default llama3.2)
//	ARGUS_TEST_VLLM_URL      (default http://127.0.0.1:8000)
//	ARGUS_TEST_VLLM_MODEL    (default Qwen/Qwen2.5-0.5B-Instruct)
package llmsignal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

// Client is the single interface both local backends are driven through, so the
// same prompt-pass assertions run against Ollama or vLLM interchangeably.
type Client interface {
	// Name is the backend identifier stamped into extracted signals ("ollama"|"vllm").
	Name() string
	// Model is the model name the client requests.
	Model() string
	// Endpoint is the chat-completions URL used (for signal provenance).
	Endpoint() string
	// Available reports whether the backend is reachable within a short probe timeout.
	Available(ctx context.Context) bool
	// Chat performs one real chat-completion prompt pass.
	Chat(ctx context.Context, prompt string) (*ChatResult, error)
}

// ChatResult is the normalised outcome of a single prompt pass. These fields are
// what the extractor turns into a signal — the suite asserts they survive intact.
type ChatResult struct {
	Backend          string
	Model            string
	Endpoint         string
	Prompt           string
	Content          string
	FinishReason     string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Latency          time.Duration
}

// openAICompatClient drives any OpenAI-compatible /v1 server (Ollama and vLLM
// both qualify). Backend-specific behaviour is limited to the name and defaults.
type openAICompatClient struct {
	name    string
	baseURL string
	model   string
	http    *http.Client
}

// newOllamaClient builds a client for a local Ollama server.
func newOllamaClient() *openAICompatClient {
	return &openAICompatClient{
		name:    "ollama",
		baseURL: envOr("ARGUS_TEST_OLLAMA_URL", "http://127.0.0.1:11434"),
		model:   envOr("ARGUS_TEST_OLLAMA_MODEL", "llama3.2"),
		http:    &http.Client{Timeout: 120 * time.Second},
	}
}

// newVLLMClient builds a client for a local vLLM OpenAI-compatible server.
func newVLLMClient() *openAICompatClient {
	return &openAICompatClient{
		name:    "vllm",
		baseURL: envOr("ARGUS_TEST_VLLM_URL", "http://127.0.0.1:8000"),
		model:   envOr("ARGUS_TEST_VLLM_MODEL", "Qwen/Qwen2.5-0.5B-Instruct"),
		http:    &http.Client{Timeout: 120 * time.Second},
	}
}

func (c *openAICompatClient) Name() string     { return c.name }
func (c *openAICompatClient) Model() string    { return c.model }
func (c *openAICompatClient) Endpoint() string { return c.baseURL + "/v1/chat/completions" }

// Available probes GET /v1/models with a short timeout. Both backends serve it.
func (c *openAICompatClient) Available(ctx context.Context) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, c.baseURL+"/v1/models", nil)
	if err != nil {
		return false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// chatRequest / chatResponse model the subset of the OpenAI chat-completions
// schema the suite relies on.
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Stream      bool          `json:"stream"`
	Temperature float64       `json:"temperature"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// Chat performs one real prompt pass and measures end-to-end latency.
func (c *openAICompatClient) Chat(ctx context.Context, prompt string) (*ChatResult, error) {
	reqBody, err := json.Marshal(chatRequest{
		Model:       c.model,
		Messages:    []chatMessage{{Role: "user", Content: prompt}},
		Stream:      false,
		Temperature: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.Endpoint(), bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s chat request: %w", c.name, err)
	}
	defer resp.Body.Close()
	latency := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s chat returned status %d", c.name, resp.StatusCode)
	}

	var parsed chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("%s chat returned no choices", c.name)
	}

	return &ChatResult{
		Backend:          c.name,
		Model:            parsed.Model,
		Endpoint:         c.Endpoint(),
		Prompt:           prompt,
		Content:          parsed.Choices[0].Message.Content,
		FinishReason:     parsed.Choices[0].FinishReason,
		PromptTokens:     parsed.Usage.PromptTokens,
		CompletionTokens: parsed.Usage.CompletionTokens,
		TotalTokens:      parsed.Usage.TotalTokens,
		Latency:          latency,
	}, nil
}

// availableBackends returns every local backend that is currently reachable.
// When none are up, the calling test is skipped — the suite is safe to run on
// any machine, but only exercises real backends when they exist.
func availableBackends(ctx context.Context, t *testing.T) []Client {
	t.Helper()
	candidates := []Client{newOllamaClient(), newVLLMClient()}
	var up []Client
	for _, c := range candidates {
		if c.Available(ctx) {
			up = append(up, c)
		} else {
			t.Logf("backend %q not reachable at %s — skipping it", c.Name(), c.Endpoint())
		}
	}
	if len(up) == 0 {
		t.Skip("no local LLM backend reachable (start `ollama serve` or a vLLM server, " +
			"or set ARGUS_TEST_OLLAMA_URL / ARGUS_TEST_VLLM_URL)")
	}
	return up
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
