package euc

import "testing"

func TestMergeAIEndpoints_IncludesCatalogAndExtra(t *testing.T) {
	got := MergeAIEndpoints([]string{"internal-llm.corp.example", "openai.com"})

	// A few catalog entries must always be present.
	for _, want := range []string{"anthropic.com", "cursor.sh", "githubcopilot.com"} {
		if !contains(got, want) {
			t.Errorf("merged catalog missing built-in %q", want)
		}
	}
	// Operator-supplied extra is included.
	if !contains(got, "internal-llm.corp.example") {
		t.Errorf("merged catalog missing operator endpoint")
	}
	// Duplicate (openai.com is already in the catalog) appears once.
	n := 0
	for _, h := range got {
		if h == "openai.com" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("openai.com appears %d times, want 1 (deduped)", n)
	}
}

// TestMergeAIEndpoints_MatchesCodingTools verifies the catalog actually matches
// the real subdomains AI coding tools resolve, via matchHost suffix logic.
func TestMergeAIEndpoints_MatchesCodingTools(t *testing.T) {
	cat := MergeAIEndpoints(nil)
	cases := []string{
		"api.openai.com",
		"api.anthropic.com",
		"api2.cursor.sh",
		"server.codeium.com",
		"api.githubcopilot.com",
		"copilot-proxy.githubusercontent.com",
	}
	for _, host := range cases {
		if !matchHost(host, cat) {
			t.Errorf("catalog does not match %q (cloud-AI tool would go undetected)", host)
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
