package euc

import "strings"

// maxHostLen is the maximum accepted length for a hostname string.
// RFC 1035 limits the total length of a DNS name to 253 characters;
// we reject anything longer to bound untrusted input (V5 / T-06-00a).
const maxHostLen = 253

// normalizeHost lowercases a host string and strips a single trailing dot
// (the FQDN terminator) so that "openai.com." and "openai.com" compare equal.
// It does NOT validate overall DNS syntax; callers are expected to reject
// empty or over-length hosts before calling matchHost.
func normalizeHost(host string) string {
	host = strings.ToLower(host)
	if strings.HasSuffix(host, ".") {
		host = host[:len(host)-1]
	}
	return host
}

// matchHost reports whether host matches any entry in endpoints.
//
// A match occurs when, after case-folding, the host either:
//   - equals the endpoint exactly, or
//   - ends with "."+endpoint (label-boundary domain-suffix match).
//
// The host is treated as untrusted input (V5 / T-06-00a):
//   - empty strings are rejected (return false),
//   - strings longer than maxHostLen (253) are rejected (return false),
//   - trailing dots are stripped before comparison.
//
// The suffix check uses a "."+endpoint prefix to prevent substring
// over-matching — e.g. "notopenai.com" must NOT match endpoint "openai.com"
// (T-06-00b).
func matchHost(host string, endpoints []string) bool {
	// Reject empty or over-length input (untrusted; V5 / T-06-00a).
	if len(host) == 0 || len(host) > maxHostLen {
		return false
	}

	h := normalizeHost(host)
	if h == "" {
		return false
	}

	for _, ep := range endpoints {
		if ep == "" {
			continue
		}
		e := normalizeHost(ep)
		if e == "" {
			continue
		}
		// Exact match.
		if h == e {
			return true
		}
		// Label-boundary suffix match: host must end with "."+endpoint.
		// This prevents "notopenai.com" matching endpoint "openai.com".
		if strings.HasSuffix(h, "."+e) {
			return true
		}
	}
	return false
}

// matchPort reports whether port is contained in ports.
// Returns false for a nil or empty ports slice.
func matchPort(port int, ports []int) bool {
	for _, p := range ports {
		if p == port {
			return true
		}
	}
	return false
}

// isLocalInferencePort reports whether port is one of the configured local
// inference runtime ports (e.g. Ollama 11434, LM Studio 1234, vLLM 8000).
//
// This is the helper used by every platform implementation for the
// loopback/local path; the name is stable so linux.go, windows.go, and
// darwin.go can all call it directly.
//
// Semantically identical to matchPort; the separate name makes call-sites
// self-documenting and decouples future per-path divergence without a
// shared-file ownership conflict.
func isLocalInferencePort(port int, ports []int) bool {
	return matchPort(port, ports)
}
