package euc

import "strings"

// defaultAIEndpoints is the built-in catalog of AI-service hostnames the EUC
// collector watches for Shadow-AI detection. Entries are domain suffixes;
// matchHost does label-boundary suffix matching, so "cursor.sh" also matches
// "api2.cursor.sh". Operators extend this list via config
// (ingest.euc.ai_endpoints) without losing these defaults.
//
// Detection is by the hostname seen in DNS queries (and, where available, TLS
// SNI) — not by IP — so these hostnames are matched directly against what the
// endpoint resolves.
var defaultAIEndpoints = []string{
	// Foundation-model APIs and chat surfaces
	"openai.com", "chatgpt.com",
	"anthropic.com", "claude.ai",
	"generativelanguage.googleapis.com", "aiplatform.googleapis.com",
	"gemini.google.com", "bard.google.com",
	"copilot.microsoft.com",
	"api.mistral.ai", "api.cohere.ai", "api.cohere.com",
	"api.perplexity.ai", "api.x.ai", "api.deepseek.com",
	"api.groq.com", "openrouter.ai", "api.together.xyz", "api.fireworks.ai",
	"poe.com", "huggingface.co",

	// AI coding assistants / IDE integrations
	"githubcopilot.com",                            // GitHub Copilot (api.*, copilot-proxy.*)
	"copilot-proxy.githubusercontent.com",          // Copilot proxy
	"cursor.sh", "cursor.com",                      // Cursor
	"codeium.com", "windsurf.com",                  // Codeium / Windsurf
	"sourcegraph.com",                              // Cody
	"tabnine.com",                                  // Tabnine
	"supermaven.com",                               // Supermaven
	"codewhisperer.us-east-1.amazonaws.com",        // Amazon Q / CodeWhisperer
}

// defaultLocalInferencePorts lists local AI-runtime ports detected without any
// network resolution: Ollama (11434), LM Studio (1234), vLLM (8000).
var defaultLocalInferencePorts = []int{11434, 1234, 8000}

// DefaultAIEndpoints returns a copy of the built-in AI-endpoint catalog.
func DefaultAIEndpoints() []string {
	return append([]string{}, defaultAIEndpoints...)
}

// DefaultLocalInferencePorts returns a copy of the built-in local-inference ports.
func DefaultLocalInferencePorts() []int {
	return append([]int{}, defaultLocalInferencePorts...)
}

// MergeAIEndpoints returns the built-in catalog unioned with extra (operator
// config), lowercased and de-duplicated, preserving catalog-first order.
func MergeAIEndpoints(extra []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(defaultAIEndpoints)+len(extra))
	add := func(h string) {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			return
		}
		if _, ok := seen[h]; ok {
			return
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	for _, h := range defaultAIEndpoints {
		add(h)
	}
	for _, h := range extra {
		add(h)
	}
	return out
}
