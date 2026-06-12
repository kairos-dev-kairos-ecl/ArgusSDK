package euc

import (
	"strings"
	"testing"
)

// TestMatchHost covers all behavior cases for the matchHost helper.
func TestMatchHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		host      string
		endpoints []string
		want      bool
	}{
		// Exact match, case-insensitive
		{
			name:      "exact match lowercase",
			host:      "api.openai.com",
			endpoints: []string{"api.openai.com"},
			want:      true,
		},
		{
			name:      "exact match case-insensitive host mixed-case",
			host:      "API.OpenAI.com",
			endpoints: []string{"api.openai.com"},
			want:      true,
		},
		{
			name:      "exact match case-insensitive endpoint mixed-case",
			host:      "api.openai.com",
			endpoints: []string{"API.OpenAI.COM"},
			want:      true,
		},

		// Domain-suffix match (label boundary)
		{
			name:      "domain suffix match — subdomain of endpoint",
			host:      "chat.api.openai.com",
			endpoints: []string{"openai.com"},
			want:      true,
		},
		{
			name:      "domain suffix match — direct subdomain",
			host:      "sub.openai.com",
			endpoints: []string{"openai.com"},
			want:      true,
		},
		{
			name:      "domain suffix match — multi-level subdomain",
			host:      "a.b.c.openai.com",
			endpoints: []string{"openai.com"},
			want:      true,
		},

		// Boundary check — substring must be on a label boundary
		{
			name:      "no match — suffix but not label boundary (notopenai.com)",
			host:      "notopenai.com",
			endpoints: []string{"openai.com"},
			want:      false,
		},
		{
			name:      "no match — infix substring",
			host:      "openai.com.evil.net",
			endpoints: []string{"openai.com"},
			want:      false,
		},
		{
			name:      "no match — different TLD",
			host:      "api.openai.org",
			endpoints: []string{"openai.com"},
			want:      false,
		},
		{
			name:      "no match — unrelated host",
			host:      "example.com",
			endpoints: []string{"openai.com"},
			want:      false,
		},

		// Empty / malformed host (untrusted input — must not panic, must return false)
		{
			name:      "empty host returns false",
			host:      "",
			endpoints: []string{"openai.com"},
			want:      false,
		},
		{
			name:      "over-length host returns false",
			host:      strings.Repeat("a", 300), // > 253-char DNS max
			endpoints: []string{"openai.com"},
			want:      false,
		},

		// Trailing-dot host normalisation
		{
			name:      "trailing-dot host normalises and matches (exact)",
			host:      "api.openai.com.",
			endpoints: []string{"api.openai.com"},
			want:      true,
		},
		{
			name:      "trailing-dot host normalises and matches (suffix)",
			host:      "sub.openai.com.",
			endpoints: []string{"openai.com"},
			want:      true,
		},

		// Empty endpoints list
		{
			name:      "no endpoints — always false",
			host:      "api.openai.com",
			endpoints: []string{},
			want:      false,
		},
		{
			name:      "nil endpoints — always false",
			host:      "api.openai.com",
			endpoints: nil,
			want:      false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := matchHost(tc.host, tc.endpoints)
			if got != tc.want {
				t.Errorf("matchHost(%q, %v) = %v; want %v", tc.host, tc.endpoints, got, tc.want)
			}
		})
	}
}

// TestMatchPort covers all behavior cases for the matchPort helper.
func TestMatchPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		port  int
		ports []int
		want  bool
	}{
		{
			name:  "port present in list",
			port:  11434,
			ports: []int{11434, 8080},
			want:  true,
		},
		{
			name:  "second port present",
			port:  8080,
			ports: []int{11434, 8080},
			want:  true,
		},
		{
			name:  "port not in list",
			port:  9999,
			ports: []int{11434},
			want:  false,
		},
		{
			name:  "empty list — always false",
			port:  11434,
			ports: []int{},
			want:  false,
		},
		{
			name:  "nil list — always false",
			port:  11434,
			ports: nil,
			want:  false,
		},
		{
			name:  "zero port not in list",
			port:  0,
			ports: []int{11434, 8080},
			want:  false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := matchPort(tc.port, tc.ports)
			if got != tc.want {
				t.Errorf("matchPort(%d, %v) = %v; want %v", tc.port, tc.ports, got, tc.want)
			}
		})
	}
}

// TestIsLocalInferencePort covers the local-inference-port helper.
// It mirrors matchPort semantics (same logic, different semantic label).
func TestIsLocalInferencePort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		port  int
		ports []int
		want  bool
	}{
		{
			name:  "ollama default port is local",
			port:  11434,
			ports: []int{11434, 1234, 8000},
			want:  true,
		},
		{
			name:  "lm studio default port is local",
			port:  1234,
			ports: []int{11434, 1234, 8000},
			want:  true,
		},
		{
			name:  "vllm default port is local",
			port:  8000,
			ports: []int{11434, 1234, 8000},
			want:  true,
		},
		{
			name:  "random port is not local",
			port:  9999,
			ports: []int{11434, 1234, 8000},
			want:  false,
		},
		{
			name:  "empty list — not local",
			port:  11434,
			ports: []int{},
			want:  false,
		},
		{
			name:  "nil list — not local",
			port:  11434,
			ports: nil,
			want:  false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isLocalInferencePort(tc.port, tc.ports)
			if got != tc.want {
				t.Errorf("isLocalInferencePort(%d, %v) = %v; want %v", tc.port, tc.ports, got, tc.want)
			}
		})
	}
}
