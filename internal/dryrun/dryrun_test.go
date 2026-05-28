package dryrun_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/dryrun"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/ocsf"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func writeSignalsFile(t *testing.T, signals []map[string]interface{}) string {
	t.Helper()
	data, err := json.Marshal(signals)
	if err != nil {
		t.Fatalf("marshal signals: %v", err)
	}
	f, err := os.CreateTemp(t.TempDir(), "signals-*.json")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()
	return f.Name()
}

func validSignalMap(id string, layer int) map[string]interface{} {
	return map[string]interface{}{
		"signal_id":  id,
		"layer":      layer,
		"severity":   3,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"app_id":     "test-app",
		"app_version": "0.0.1",
	}
}

// ── LoadSignals ───────────────────────────────────────────────────────────────

func TestLoadSignals_ValidFile(t *testing.T) {
	path := writeSignalsFile(t, []map[string]interface{}{
		validSignalMap("sig-001", 9),
		validSignalMap("sig-002", 6),
	})
	signals, err := dryrun.LoadSignals(path)
	if err != nil {
		t.Fatalf("LoadSignals: %v", err)
	}
	if len(signals) != 2 {
		t.Fatalf("got %d signals, want 2", len(signals))
	}
	if signals[0].SignalID != "sig-001" {
		t.Errorf("signals[0].SignalID = %q, want %q", signals[0].SignalID, "sig-001")
	}
	if signals[1].Layer != signal.L6Safety {
		t.Errorf("signals[1].Layer = %d, want %d (L6Safety)", signals[1].Layer, signal.L6Safety)
	}
}

func TestLoadSignals_TimestampParsed(t *testing.T) {
	ts := "2026-05-28T12:00:00Z"
	path := writeSignalsFile(t, []map[string]interface{}{
		{"signal_id": "s1", "layer": 1, "severity": 1, "timestamp": ts},
	})
	signals, err := dryrun.LoadSignals(path)
	if err != nil {
		t.Fatalf("LoadSignals: %v", err)
	}
	want, _ := time.Parse(time.RFC3339, ts)
	if !signals[0].Timestamp.Equal(want) {
		t.Errorf("Timestamp = %v, want %v", signals[0].Timestamp, want)
	}
}

func TestLoadSignals_ContextJSONPreserved(t *testing.T) {
	ctx := `{"url":"https://api.example.com","method":"GET","status_code":200}`
	path := writeSignalsFile(t, []map[string]interface{}{
		{"signal_id": "s1", "layer": 9, "severity": 2, "context_json": ctx},
	})
	signals, err := dryrun.LoadSignals(path)
	if err != nil {
		t.Fatalf("LoadSignals: %v", err)
	}
	if string(signals[0].ContextJSON) != ctx {
		t.Errorf("ContextJSON = %s, want %s", signals[0].ContextJSON, ctx)
	}
}

func TestLoadSignals_MissingFile(t *testing.T) {
	_, err := dryrun.LoadSignals("/nonexistent/path/signals.json")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadSignals_InvalidJSON(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "bad-*.json")
	f.WriteString("not json")
	f.Close()
	_, err := dryrun.LoadSignals(f.Name())
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

// ── Run (end-to-end) ──────────────────────────────────────────────────────────

func TestRun_SampleSignals(t *testing.T) {
	// Use the committed sample signals file if available; otherwise build one inline.
	samplePath := filepath.Join("..", "..", "testdata", "dryrun", "sample-signals.json")
	if _, err := os.Stat(samplePath); err != nil {
		t.Skip("testdata/dryrun/sample-signals.json not found — skipping end-to-end test")
	}

	outDir := t.TempDir()
	report, err := dryrun.Run(dryrun.Config{
		InputFile:            samplePath,
		OutputDir:            outDir,
		AppID:                "test-app",
		Env:                  "test",
		SDKVersion:           "0.1.0",
		MapperProductVersion: "1.0.0",
		MapperAgentHostname:  "test-host",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if report.Total != 5 {
		t.Errorf("Total = %d, want 5", report.Total)
	}
	if report.ProtoValid != 5 {
		t.Errorf("ProtoValid = %d, want 5; errors: %v", report.ProtoValid, report.Errors)
	}
	if report.OCSFValid != 5 {
		t.Errorf("OCSFValid = %d, want 5; errors: %v", report.OCSFValid, report.Errors)
	}

	// Output files must exist and be non-empty
	for _, path := range []string{report.ProtoJSONFile, report.ProtoBinFile, report.OCSFJSONFile} {
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("output file missing: %s", path)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("output file empty: %s", path)
		}
	}

	// OCSF output must be a valid JSON array
	ocsfData, _ := os.ReadFile(report.OCSFJSONFile)
	var events []map[string]interface{}
	if err := json.Unmarshal(ocsfData, &events); err != nil {
		t.Fatalf("signals-ocsf.json is not valid JSON: %v", err)
	}
	if len(events) != 5 {
		t.Errorf("ocsf events count = %d, want 5", len(events))
	}

	// Every event must have the required base fields
	for i, ev := range events {
		for _, field := range []string{"class_uid", "category_uid", "activity_id", "type_uid", "time", "severity_id", "metadata"} {
			if _, ok := ev[field]; !ok {
				t.Errorf("event[%d] missing required field %q", i, field)
			}
		}
	}
}

func TestRun_ProtoValidation_Errors(t *testing.T) {
	path := writeSignalsFile(t, []map[string]interface{}{
		// missing signal_id
		{"layer": 9, "severity": 3, "timestamp": time.Now().UTC().Format(time.RFC3339)},
		// invalid layer (99)
		{"signal_id": "s2", "layer": 99, "severity": 3, "timestamp": time.Now().UTC().Format(time.RFC3339)},
		// invalid severity (0)
		{"signal_id": "s3", "layer": 1, "severity": 0, "timestamp": time.Now().UTC().Format(time.RFC3339)},
	})

	report, err := dryrun.Run(dryrun.Config{
		InputFile: path,
		OutputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	protoErrs := filterErrors(report.Errors, "proto")
	if len(protoErrs) == 0 {
		t.Error("expected proto validation errors, got none")
	}
	// ProtoValid should be 0 — all three signals fail
	if report.ProtoValid != 0 {
		t.Errorf("ProtoValid = %d, want 0", report.ProtoValid)
	}
}

func TestRun_OCSFTypeUID_Integrity(t *testing.T) {
	// All 5 signal layers present in sample file; check type_uid formula for L9
	path := writeSignalsFile(t, []map[string]interface{}{
		validSignalMap("sig-l9", 9),
	})

	report, err := dryrun.Run(dryrun.Config{
		InputFile:           path,
		OutputDir:           t.TempDir(),
		MapperAgentHostname: "test-host",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	ocsfErrs := filterErrors(report.Errors, "ocsf_structural")
	for _, e := range ocsfErrs {
		if e.Field == "type_uid" {
			t.Errorf("unexpected type_uid error: %v", e)
		}
	}
}

func TestRun_OCSFSchema_RequiredFields(t *testing.T) {
	path := writeSignalsFile(t, []map[string]interface{}{
		validSignalMap("sig-001", int(signal.L9APIGateway)),
	})

	report, err := dryrun.Run(dryrun.Config{
		InputFile:           path,
		OutputDir:           t.TempDir(),
		MapperAgentHostname: "test-host",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	schemaErrs := filterErrors(report.Errors, "ocsf_schema")
	if len(schemaErrs) > 0 {
		t.Errorf("unexpected ocsf_schema errors: %v", schemaErrs)
	}
}

func TestRun_EmbeddedSchemaValid(t *testing.T) {
	// Confirms the embedded schema file is valid JSON by triggering a run
	// that exercises the schema validator path.
	path := writeSignalsFile(t, []map[string]interface{}{
		validSignalMap("sig-001", int(signal.L6Safety)),
	})
	_, err := dryrun.Run(dryrun.Config{
		InputFile:           path,
		OutputDir:           t.TempDir(),
		MapperAgentHostname: "test-host",
	})
	if err != nil {
		t.Fatalf("Run failed (embedded schema may be invalid): %v", err)
	}
}

// ── OCSF event content checks ─────────────────────────────────────────────────

func TestRun_OCSFContent_L9HTTPActivity(t *testing.T) {
	ctx := `{"url":"https://api.example.com/v1/chat","method":"POST","status_code":200}`
	path := writeSignalsFile(t, []map[string]interface{}{
		{
			"signal_id":    "sig-l9",
			"layer":        9,
			"severity":     3,
			"timestamp":    time.Now().UTC().Format(time.RFC3339),
			"context_json": ctx,
		},
	})

	outDir := t.TempDir()
	report, err := dryrun.Run(dryrun.Config{
		InputFile:           path,
		OutputDir:           outDir,
		MapperAgentHostname: "test-host",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	ocsfData, _ := os.ReadFile(report.OCSFJSONFile)
	var events []map[string]interface{}
	if err := json.Unmarshal(ocsfData, &events); err != nil || len(events) == 0 {
		t.Fatalf("OCSF output invalid or empty")
	}
	ev := events[0]

	if int(ev["class_uid"].(float64)) != int(ocsf.ClassHTTPActivity) {
		t.Errorf("class_uid = %v, want %d", ev["class_uid"], ocsf.ClassHTTPActivity)
	}
	if req, ok := ev["http_request"].(map[string]interface{}); !ok {
		t.Error("http_request missing or not an object")
	} else if req["url"] != "https://api.example.com/v1/chat" {
		t.Errorf("http_request.url = %v, want URL from ContextJSON", req["url"])
	}
}

func TestRun_OCSFContent_L6DetectionFinding(t *testing.T) {
	path := writeSignalsFile(t, []map[string]interface{}{
		{
			"signal_id": "sig-l6",
			"layer":     6,
			"severity":  4,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		},
	})

	outDir := t.TempDir()
	report, err := dryrun.Run(dryrun.Config{
		InputFile:           path,
		OutputDir:           outDir,
		MapperAgentHostname: "test-host",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	ocsfData, _ := os.ReadFile(report.OCSFJSONFile)
	var events []map[string]interface{}
	json.Unmarshal(ocsfData, &events)
	if len(events) == 0 {
		t.Fatal("no OCSF events produced")
	}

	ev := events[0]
	if int(ev["class_uid"].(float64)) != int(ocsf.ClassDetectionFinding) {
		t.Errorf("class_uid = %v, want %d (ClassDetectionFinding)", ev["class_uid"], ocsf.ClassDetectionFinding)
	}
	fi, ok := ev["finding_info"].(map[string]interface{})
	if !ok {
		t.Fatal("finding_info missing")
	}
	if fi["uid"] == "" || fi["uid"] == nil {
		t.Error("finding_info.uid must be non-empty")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func filterErrors(errs []dryrun.ValidationError, stage string) []dryrun.ValidationError {
	var out []dryrun.ValidationError
	for _, e := range errs {
		if e.Stage == stage {
			out = append(out, e)
		}
	}
	return out
}
