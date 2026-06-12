//go:build llmlocal

package llmsignal

import (
	"context"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/collector/euc"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// TestEUCLocalInferenceDetection (Goal 3) validates the EUC collector's
// Shadow-AI signal shaping for a *genuinely running* local inference runtime.
//
// CAVEAT: the platform OS collectors (linux.go/windows.go/darwin.go) are
// currently stubs — nothing yet captures real OS network events. So this test
// supplies the Observation that a real OS collector would produce (built from a
// live TCP probe of the running Ollama/vLLM port) and asserts the collector
// converts it into a correct euc.local_inference signal. When a real OS collector
// lands, only the source of the Observation changes — these assertions still hold.
func TestEUCLocalInferenceDetection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Find a real, listening local-inference endpoint from the reachable backends.
	backends := availableBackends(ctx, t)
	var obs euc.Observation
	found := false
	for _, b := range backends {
		host, port := hostPort(t, b.Endpoint())
		if dialable(net.JoinHostPort(host, port)) {
			obs = euc.Observation{
				ConnectedHost: host,
				LocalPort:     atoiPort(port),
				IsLocal:       true,
				ProcessName:   b.Name(),
				Username:      "test-user",
			}
			found = true
			t.Logf("detected local inference runtime %q at %s:%s", b.Name(), host, port)
			break
		}
	}
	if !found {
		t.Skip("no local inference port was TCP-dialable (backends reachable only via proxy?)")
	}

	// Feed the observation through the real EUC collector via the OS-collector seam.
	coll := euc.New(euc.Config{AppID: "euc-app", Env: "test"}, newFakeOSCollector(obs))
	out := make(chan signal.Batch, 4)
	if err := coll.Start(ctx, out); err != nil {
		t.Fatalf("euc collector start: %v", err)
	}
	defer coll.Close()

	var batch signal.Batch
	select {
	case batch = <-out:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for EUC signal")
	}

	if len(batch.Signals) != 1 {
		t.Fatalf("expected 1 EUC signal, got %d", len(batch.Signals))
	}
	sig := batch.Signals[0]

	if sig.Category != "euc.local_inference" {
		t.Errorf("category: got %q, want %q", sig.Category, "euc.local_inference")
	}
	if sig.Layer != signal.L9APIGateway {
		t.Errorf("layer: got %v, want L9APIGateway", sig.Layer)
	}
	if sig.AppID != "euc-app" {
		t.Errorf("app_id: got %q, want %q", sig.AppID, "euc-app")
	}

	// ContextJSON must record the detected local-inference connection.
	var cc struct {
		ConnectedHost string `json:"connected_host"`
		LocalPort     int    `json:"local_port"`
		IsLocal       bool   `json:"is_local"`
		ProcessName   string `json:"process_name"`
	}
	if err := decodeJSON(sig.ContextJSON, &cc); err != nil {
		t.Fatalf("ContextJSON parse: %v", err)
	}
	if !cc.IsLocal {
		t.Error("expected is_local=true for a local-inference observation")
	}
	if cc.LocalPort != obs.LocalPort {
		t.Errorf("local_port: got %d, want %d", cc.LocalPort, obs.LocalPort)
	}
	if cc.ProcessName != obs.ProcessName {
		t.Errorf("process_name: got %q, want %q", cc.ProcessName, obs.ProcessName)
	}
}

func hostPort(t *testing.T, endpoint string) (string, string) {
	t.Helper()
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse endpoint %q: %v", endpoint, err)
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return host, port
}

func dialable(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 1*time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func atoiPort(p string) int {
	n := 0
	for _, r := range p {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
