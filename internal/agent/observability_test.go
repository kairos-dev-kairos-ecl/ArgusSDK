package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEncodeMetrics_PrometheusFormat(t *testing.T) {
	stats := map[string]uint64{
		"accepted":  5,
		"delivered": 4,
		"failed":    1,
		"dropped":   0,
	}
	got := encodeMetrics(stats)

	// Deterministic ordering: keys are sorted, so accepted precedes delivered.
	wantContains := []string{
		"# HELP argus_dispatch_accepted_total Signal batches accepted by the dispatcher for delivery.",
		"# TYPE argus_dispatch_accepted_total counter",
		"argus_dispatch_accepted_total 5",
		"# TYPE argus_dispatch_delivered_total counter",
		"argus_dispatch_delivered_total 4",
		"argus_dispatch_failed_total 1",
		"argus_dispatch_dropped_total 0",
	}
	for _, w := range wantContains {
		if !strings.Contains(got, w) {
			t.Errorf("encodeMetrics output missing %q\n---\n%s", w, got)
		}
	}

	// Sorted order: accepted < delivered < dropped < failed.
	idxAccepted := strings.Index(got, "argus_dispatch_accepted_total 5")
	idxDelivered := strings.Index(got, "argus_dispatch_delivered_total 4")
	if idxAccepted == -1 || idxDelivered == -1 || idxAccepted > idxDelivered {
		t.Errorf("expected accepted counter before delivered counter (sorted), got:\n%s", got)
	}
}

func TestEncodeMetrics_NilStats(t *testing.T) {
	if got := encodeMetrics(nil); got != "" {
		t.Errorf("expected empty output for nil stats, got %q", got)
	}
}

func TestEncodeMetrics_UnknownKeyFallbackHelp(t *testing.T) {
	got := encodeMetrics(map[string]uint64{"custom": 7})
	if !strings.Contains(got, "# HELP argus_dispatch_custom_total Dispatcher counter custom.") {
		t.Errorf("expected fallback HELP line for unknown key, got:\n%s", got)
	}
	if !strings.Contains(got, "argus_dispatch_custom_total 7") {
		t.Errorf("expected value line for unknown key, got:\n%s", got)
	}
}

func TestHandleHealthz_AlwaysOK(t *testing.T) {
	o := newObsServer("", nil, nil)
	rec := httptest.NewRecorder()
	o.handleHealthz(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ok") {
		t.Errorf("expected body to contain 'ok', got %q", rec.Body.String())
	}
}

func TestHandleReadyz_GatedOnReadyFlag(t *testing.T) {
	o := newObsServer("", nil, nil)

	// Not ready yet → 503.
	rec := httptest.NewRecorder()
	o.handleReadyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 before ready, got %d", rec.Code)
	}

	// Flip ready → 200.
	o.setReady(true)
	rec = httptest.NewRecorder()
	o.handleReadyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 after setReady(true), got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ready") {
		t.Errorf("expected body to contain 'ready', got %q", rec.Body.String())
	}
}

func TestHandleMetrics_RendersStats(t *testing.T) {
	o := newObsServer("", func() map[string]uint64 {
		return map[string]uint64{"delivered": 42}
	}, nil)

	rec := httptest.NewRecorder()
	o.handleMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("expected text/plain content type, got %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "argus_dispatch_delivered_total 42") {
		t.Errorf("expected delivered counter in body, got:\n%s", rec.Body.String())
	}
}

func TestHandleMetrics_NilStatsFn(t *testing.T) {
	o := newObsServer("", nil, nil)
	rec := httptest.NewRecorder()
	o.handleMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 even with nil statsFn, got %d", rec.Code)
	}
}

func TestNewObsServer_DefaultAddr(t *testing.T) {
	o := newObsServer("", nil, nil)
	if o.srv.Addr != defaultObservabilityAddr {
		t.Errorf("expected default addr %q, got %q", defaultObservabilityAddr, o.srv.Addr)
	}
}
