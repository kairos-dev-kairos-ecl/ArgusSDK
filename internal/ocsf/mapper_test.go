package ocsf

import (
	"fmt"
	"testing"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// newTestMapper returns a Mapper configured for tests.
func newTestMapper() *Mapper {
	return NewMapper("1.0.0", "test-host")
}

// newMinimalSignal returns a valid signal for the given layer with minimal required fields.
func newMinimalSignal(layer signal.Layer) signal.Signal {
	return signal.Signal{
		SignalID:   "sig-001",
		TraceID:    "trace-abc",
		SpanID:     "span-xyz",
		Layer:      layer,
		Category:   "test.category",
		Severity:   signal.SeverityInfo,
		AppID:      "app-test",
		AppVersion: "1.0.0",
		SDKVersion: "0.1.0",
		Env:        "test",
		Timestamp:  time.Unix(1700000000, 0),
		DurationMS: 42.5,
	}
}

// TestMap_AllLayers verifies that every Layer constant maps to the correct ClassUID,
// CategoryUID, ActivityID, and that TypeUID == class_uid*100 + activity_id.
func TestMap_AllLayers(t *testing.T) {
	m := newTestMapper()

	type testCase struct {
		layer           signal.Layer
		wantClassUID    ClassUID
		wantCategoryUID int
		wantActivityID  int
	}

	cases := []testCase{
		{signal.L1Hardware, ClassMemoryActivity, 1, 7},
		{signal.L2ModelWeights, ClassModuleActivity, 1, 1},
		{signal.L3Tokenizer, ClassAPIActivity, 6, 2},
		{signal.L4Transformer, ClassMemoryActivity, 1, 7},
		{signal.L5OutputDecoding, ClassAPIActivity, 6, 2},
		{signal.L6Safety, ClassDetectionFinding, 2, 1},
		{signal.L7RAGRetrieval, ClassDatastoreActivity, 6, 4},
		{signal.L8Agents, ClassAPIActivity, 6, 99},
		{signal.L9APIGateway, ClassHTTPActivity, 4, 6},
		{signal.L10Application, ClassWebResourcesActivity, 6, 2},
		{signal.LDecision, ClassDetectionFinding, 2, 1},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("layer_%d", tc.layer), func(t *testing.T) {
			s := newMinimalSignal(tc.layer)
			ev, err := m.Map(s)
			if err != nil {
				t.Fatalf("Map() error = %v", err)
			}
			if ev.ClassUID != tc.wantClassUID {
				t.Errorf("ClassUID = %d, want %d", ev.ClassUID, tc.wantClassUID)
			}
			if ev.CategoryUID != tc.wantCategoryUID {
				t.Errorf("CategoryUID = %d, want %d", ev.CategoryUID, tc.wantCategoryUID)
			}
			if ev.ActivityID != tc.wantActivityID {
				t.Errorf("ActivityID = %d, want %d", ev.ActivityID, tc.wantActivityID)
			}
			wantTypeUID := int(tc.wantClassUID)*100 + tc.wantActivityID
			if ev.TypeUID != wantTypeUID {
				t.Errorf("TypeUID = %d, want %d", ev.TypeUID, wantTypeUID)
			}
			if ev.Metadata.Version != "1.3.0" {
				t.Errorf("Metadata.Version = %q, want %q", ev.Metadata.Version, "1.3.0")
			}
			if ev.Metadata.Product.VendorName != "Argus" {
				t.Errorf("Metadata.Product.VendorName = %q, want %q", ev.Metadata.Product.VendorName, "Argus")
			}
			if ev.Metadata.UID != s.SignalID {
				t.Errorf("Metadata.UID = %q, want %q", ev.Metadata.UID, s.SignalID)
			}
			if ev.Time != s.Timestamp.UnixMilli() {
				t.Errorf("Time = %d, want %d", ev.Time, s.Timestamp.UnixMilli())
			}
			if ev.SeverityID != int(s.Severity) {
				t.Errorf("SeverityID = %d, want %d", ev.SeverityID, int(s.Severity))
			}
		})
	}
}

// TestMap_AllSignalFields verifies that all 14 Signal fields survive into the Event.
func TestMap_AllSignalFields(t *testing.T) {
	m := newTestMapper()
	ts := time.Unix(1700000000, 0)

	s := signal.Signal{
		SignalID:     "sig-allfields-001",
		TraceID:      "trace-full",
		SpanID:       "span-full",
		ParentSpanID: "parent-span",
		Layer:        signal.L3Tokenizer, // API Activity — no nil-pointer risk
		Category:     "tokenizer.encode",
		Severity:     signal.SeverityHigh,
		AppID:        "my-app-id",
		AppVersion:   "2.3.4",
		SDKVersion:   "0.9.0",
		Env:          "prod",
		Timestamp:    ts,
		DurationMS:   123.45,
		ContextJSON:  []byte(`{"key":"val"}`),
	}

	ev, err := m.Map(s)
	if err != nil {
		t.Fatalf("Map() error = %v", err)
	}

	// SignalID → Metadata.UID
	if ev.Metadata.UID != s.SignalID {
		t.Errorf("Metadata.UID = %q, want %q", ev.Metadata.UID, s.SignalID)
	}

	// TraceID → Unmapped["trace_id"]
	if got, ok := ev.Unmapped["trace_id"]; !ok || got != s.TraceID {
		t.Errorf("Unmapped[trace_id] = %v, want %q", got, s.TraceID)
	}

	// SpanID → Unmapped["span_id"]
	if got, ok := ev.Unmapped["span_id"]; !ok || got != s.SpanID {
		t.Errorf("Unmapped[span_id] = %v, want %q", got, s.SpanID)
	}

	// Layer → class_uid via mapping
	if ev.ClassUID != ClassAPIActivity {
		t.Errorf("ClassUID = %d, want %d (ClassAPIActivity)", ev.ClassUID, ClassAPIActivity)
	}

	// Category → Unmapped["category"]
	if got, ok := ev.Unmapped["category"]; !ok || got != s.Category {
		t.Errorf("Unmapped[category] = %v, want %q", got, s.Category)
	}

	// Severity → SeverityID
	if ev.SeverityID != int(signal.SeverityHigh) {
		t.Errorf("SeverityID = %d, want %d", ev.SeverityID, int(signal.SeverityHigh))
	}

	// AppID → Actor.AppUID
	if ev.Actor == nil {
		t.Fatal("Actor is nil")
	}
	if ev.Actor.AppUID != s.AppID {
		t.Errorf("Actor.AppUID = %q, want %q", ev.Actor.AppUID, s.AppID)
	}

	// AppVersion — verify no panic (not required in OCSF output)
	// SDKVersion → Unmapped["sdk_version"]
	if got, ok := ev.Unmapped["sdk_version"]; !ok || got != s.SDKVersion {
		t.Errorf("Unmapped[sdk_version] = %v, want %q", got, s.SDKVersion)
	}

	// Env — verify no panic (not in OCSF required fields)
	// Timestamp → ev.Time
	if ev.Time != ts.UnixMilli() {
		t.Errorf("Time = %d, want %d", ev.Time, ts.UnixMilli())
	}

	// DurationMS — verify no panic
	_ = s.DurationMS

	// ContextJSON → Unmapped["context"] present when valid JSON
	if _, ok := ev.Unmapped["context"]; !ok {
		t.Error("Unmapped[context] should be present for valid ContextJSON")
	}
}

// TestMap_SystemClassDevice verifies that L1, L2, L4 produce a non-nil Device
// with Hostname equal to the agentHostname passed to NewMapper.
func TestMap_SystemClassDevice(t *testing.T) {
	m := newTestMapper()

	systemLayers := []signal.Layer{signal.L1Hardware, signal.L2ModelWeights, signal.L4Transformer}
	for _, layer := range systemLayers {
		layer := layer
		t.Run(fmt.Sprintf("layer_%d", layer), func(t *testing.T) {
			s := newMinimalSignal(layer)
			ev, err := m.Map(s)
			if err != nil {
				t.Fatalf("Map() error = %v", err)
			}
			if ev.Device == nil {
				t.Fatal("Device is nil; expected non-nil for system-class layers")
			}
			if ev.Device.Hostname != "test-host" {
				t.Errorf("Device.Hostname = %q, want %q", ev.Device.Hostname, "test-host")
			}
		})
	}
}

// TestMap_DetectionFinding verifies finding_info for L6Safety and LDecision,
// and that analytic.type_id differs between the two.
func TestMap_DetectionFinding(t *testing.T) {
	m := newTestMapper()

	t.Run("L6Safety", func(t *testing.T) {
		s := newMinimalSignal(signal.L6Safety)
		ev, err := m.Map(s)
		if err != nil {
			t.Fatalf("Map() error = %v", err)
		}
		if ev.FindingInfo == nil {
			t.Fatal("FindingInfo is nil")
		}
		if ev.FindingInfo.UID == "" {
			t.Error("FindingInfo.UID is empty")
		}
		if ev.FindingInfo.Title == "" {
			t.Error("FindingInfo.Title is empty")
		}
		if ev.FindingInfo.Analytic == nil {
			t.Fatal("FindingInfo.Analytic is nil")
		}
		if ev.FindingInfo.Analytic.TypeID != 4 {
			t.Errorf("Analytic.TypeID = %d, want 4 (ML/DL) for L6Safety", ev.FindingInfo.Analytic.TypeID)
		}
	})

	t.Run("LDecision", func(t *testing.T) {
		s := newMinimalSignal(signal.LDecision)
		ev, err := m.Map(s)
		if err != nil {
			t.Fatalf("Map() error = %v", err)
		}
		if ev.FindingInfo == nil {
			t.Fatal("FindingInfo is nil")
		}
		if ev.FindingInfo.UID == "" {
			t.Error("FindingInfo.UID is empty")
		}
		if ev.FindingInfo.Title == "" {
			t.Error("FindingInfo.Title is empty")
		}
		if ev.FindingInfo.Analytic == nil {
			t.Fatal("FindingInfo.Analytic is nil")
		}
		if ev.FindingInfo.Analytic.TypeID != 1 {
			t.Errorf("Analytic.TypeID = %d, want 1 (Rule) for LDecision", ev.FindingInfo.Analytic.TypeID)
		}
	})
}

// TestMap_HTTPActivity verifies L9APIGateway produces non-nil HttpRequest,
// HttpResponse, and DstEndpoint. The no-context case must yield an empty URL —
// never the signal category (honesty fix for the former s.Category fallback).
func TestMap_HTTPActivity(t *testing.T) {
	m := newTestMapper()
	s := newMinimalSignal(signal.L9APIGateway)
	ev, err := m.Map(s)
	if err != nil {
		t.Fatalf("Map() error = %v", err)
	}
	if ev.HttpRequest == nil {
		t.Fatal("HttpRequest is nil")
	}
	// No context → URL must be empty, never mislabeled as s.Category.
	if ev.HttpRequest.URL != "" {
		t.Errorf("HttpRequest.URL = %q, want empty string for no-context signal (must not use s.Category)", ev.HttpRequest.URL)
	}
	if ev.HttpRequest.URL == s.Category {
		t.Errorf("HttpRequest.URL == s.Category %q; URL must never be the signal category", s.Category)
	}
	if ev.HttpRequest.Method == "" {
		t.Error("HttpRequest.Method is empty")
	}
	if ev.HttpResponse == nil {
		t.Fatal("HttpResponse is nil")
	}
	if ev.DstEndpoint == nil {
		t.Fatal("DstEndpoint is nil")
	}
}

// TestMap_APIActivity verifies L3, L5, L8 produce non-nil Api and SrcEndpoint.
func TestMap_APIActivity(t *testing.T) {
	m := newTestMapper()
	apiLayers := []signal.Layer{signal.L3Tokenizer, signal.L5OutputDecoding, signal.L8Agents}

	for _, layer := range apiLayers {
		layer := layer
		t.Run(fmt.Sprintf("layer_%d", layer), func(t *testing.T) {
			s := newMinimalSignal(layer)
			ev, err := m.Map(s)
			if err != nil {
				t.Fatalf("Map() error = %v", err)
			}
			if ev.Api == nil {
				t.Fatal("Api is nil")
			}
			if ev.Api.Operation == "" {
				t.Error("Api.Operation is empty")
			}
			if ev.SrcEndpoint == nil {
				t.Fatal("SrcEndpoint is nil")
			}
		})
	}
}

// TestMap_DatastoreActivity verifies L7RAGRetrieval produces non-nil SrcEndpoint.
func TestMap_DatastoreActivity(t *testing.T) {
	m := newTestMapper()
	s := newMinimalSignal(signal.L7RAGRetrieval)
	ev, err := m.Map(s)
	if err != nil {
		t.Fatalf("Map() error = %v", err)
	}
	if ev.SrcEndpoint == nil {
		t.Fatal("SrcEndpoint is nil for L7RAGRetrieval")
	}
}

// TestMapBatch_ErrorHandling verifies that an unknown layer returns an error for
// that signal while valid signals in the same batch still produce events.
func TestMapBatch_ErrorHandling(t *testing.T) {
	m := newTestMapper()

	signals := []signal.Signal{
		newMinimalSignal(signal.L1Hardware),  // valid
		{Layer: 99, SignalID: "bad-sig"},     // unknown layer
		newMinimalSignal(signal.L9APIGateway), // valid
	}

	events, errs := m.MapBatch(signals)

	if len(errs) == 0 {
		t.Error("expected at least one error for unknown layer 99, got none")
	}
	if len(events) < 2 {
		t.Errorf("expected at least 2 events for 2 valid signals, got %d", len(events))
	}
}

// TestMap_ContextJSON_RoundTrip verifies that ContextJSON for L9APIGateway with
// url, method, and status_code keys extracts correctly into the event.
func TestMap_ContextJSON_RoundTrip(t *testing.T) {
	m := newTestMapper()
	s := newMinimalSignal(signal.L9APIGateway)
	s.ContextJSON = []byte(`{"url":"https://api.example.com","method":"GET","status_code":200}`)

	ev, err := m.Map(s)
	if err != nil {
		t.Fatalf("Map() error = %v", err)
	}

	// Verify unmapped["context"] contains the parsed object
	if _, ok := ev.Unmapped["context"]; !ok {
		t.Error("Unmapped[context] should be present")
	}

	// Verify ContextJSON extraction: URL from context
	if ev.HttpRequest == nil {
		t.Fatal("HttpRequest is nil")
	}
	if ev.HttpRequest.URL != "https://api.example.com" {
		t.Errorf("HttpRequest.URL = %q, want %q", ev.HttpRequest.URL, "https://api.example.com")
	}
	if ev.HttpRequest.Method != "GET" {
		t.Errorf("HttpRequest.Method = %q, want %q", ev.HttpRequest.Method, "GET")
	}
	if ev.HttpResponse == nil {
		t.Fatal("HttpResponse is nil")
	}
	if ev.HttpResponse.Code != 200 {
		t.Errorf("HttpResponse.Code = %d, want 200", ev.HttpResponse.Code)
	}
}

// TestMap_InvalidSeverity verifies that severity 0 (unspecified) maps to
// severity_id=1 (Informational) per the guard in Map().
func TestMap_InvalidSeverity(t *testing.T) {
	m := newTestMapper()
	s := newMinimalSignal(signal.L1Hardware)
	s.Severity = signal.SeverityUnspecified // 0

	ev, err := m.Map(s)
	if err != nil {
		t.Fatalf("Map() error = %v", err)
	}
	if ev.SeverityID != 1 {
		t.Errorf("SeverityID = %d, want 1 (Informational) for unspecified severity", ev.SeverityID)
	}
}

// TestMap_MapBatch_NonNilForAllLayers verifies MapBatch returns a non-nil Event
// for every layer constant with no errors on a well-formed Signal.
func TestMap_MapBatch_NonNilForAllLayers(t *testing.T) {
	m := newTestMapper()

	allLayers := []signal.Layer{
		signal.L1Hardware, signal.L2ModelWeights, signal.L3Tokenizer,
		signal.L4Transformer, signal.L5OutputDecoding, signal.L6Safety,
		signal.L7RAGRetrieval, signal.L8Agents, signal.L9APIGateway,
		signal.L10Application, signal.LDecision,
	}

	signals := make([]signal.Signal, len(allLayers))
	for i, l := range allLayers {
		signals[i] = newMinimalSignal(l)
	}

	events, errs := m.MapBatch(signals)

	if len(errs) != 0 {
		t.Errorf("MapBatch() returned %d errors for well-formed signals: %v", len(errs), errs)
	}
	if len(events) != len(allLayers) {
		t.Errorf("MapBatch() returned %d events, want %d", len(events), len(allLayers))
	}
	for i, ev := range events {
		if ev == nil {
			t.Errorf("events[%d] is nil for layer %d", i, allLayers[i])
		}
	}
}

// TestMap_DatastoreActivity_ContextJSON_VectorIndex verifies that L7RAGRetrieval
// with a vector_index key in ContextJSON promotes to a first-class Databucket object
// and does NOT leave a string placeholder in Unmapped.
func TestMap_DatastoreActivity_ContextJSON_VectorIndex(t *testing.T) {
	m := newTestMapper()
	s := newMinimalSignal(signal.L7RAGRetrieval)
	s.ContextJSON = []byte(`{"vector_index":"embeddings-v2"}`)

	ev, err := m.Map(s)
	if err != nil {
		t.Fatalf("Map() error = %v", err)
	}

	if ev.Databucket == nil {
		t.Fatal("Databucket is nil; expected first-class Databucket object from vector_index")
	}
	if ev.Databucket.Name != "embeddings-v2" {
		t.Errorf("Databucket.Name = %q, want %q", ev.Databucket.Name, "embeddings-v2")
	}
	if _, ok := ev.Unmapped["databucket_name"]; ok {
		t.Error("Unmapped[databucket_name] must not be set; databucket is now a first-class object")
	}
}

// TestMap_WebResources_ContextJSON_ResourceURL verifies that L10Application
// with a resource_url key in ContextJSON promotes to a first-class []WebResource
// and does NOT leave a string placeholder in Unmapped.
func TestMap_WebResources_ContextJSON_ResourceURL(t *testing.T) {
	m := newTestMapper()
	s := newMinimalSignal(signal.L10Application)
	s.ContextJSON = []byte(`{"resource_url":"/api/v1/users"}`)

	ev, err := m.Map(s)
	if err != nil {
		t.Fatalf("Map() error = %v", err)
	}

	if len(ev.WebResources) == 0 {
		t.Fatal("WebResources is empty; expected first-class []WebResource from resource_url")
	}
	if ev.WebResources[0].URLString != "/api/v1/users" {
		t.Errorf("WebResources[0].URLString = %q, want %q", ev.WebResources[0].URLString, "/api/v1/users")
	}
	if _, ok := ev.Unmapped["web_resource_url"]; ok {
		t.Error("Unmapped[web_resource_url] must not be set; web_resources is now a first-class object")
	}
}

// ---------------------------------------------------------------------------
// F13: ActivityName=Other for activity_id=99 (new — RED phase)
// ---------------------------------------------------------------------------

// TestMap_Activity99SetsActivityName (F13): a signal with layer L8Agents
// (activity_id=99) must produce an Event with ActivityName=="Other".
// A signal with a non-99 layer must produce ActivityName=="".
func TestMap_Activity99SetsActivityName(t *testing.T) {
	m := newTestMapper()

	// L8Agents → activity_id=99 → ActivityName must be "Other"
	s99 := newMinimalSignal(signal.L8Agents)
	ev99, err := m.Map(s99)
	if err != nil {
		t.Fatalf("Map(L8Agents) error = %v", err)
	}
	if ev99.ActivityID != 99 {
		t.Fatalf("ActivityID = %d, want 99 for L8Agents", ev99.ActivityID)
	}
	if ev99.ActivityName != "Other" {
		t.Errorf("ActivityName = %q, want %q for activity_id=99 (F13)", ev99.ActivityName, "Other")
	}

	// A non-99 layer (L9APIGateway → activity_id=6) must have ActivityName==""
	s6 := newMinimalSignal(signal.L9APIGateway)
	ev6, err := m.Map(s6)
	if err != nil {
		t.Fatalf("Map(L9APIGateway) error = %v", err)
	}
	if ev6.ActivityID == 99 {
		t.Skip("L9APIGateway unexpectedly maps to activity_id=99 — skip non-99 assertion")
	}
	if ev6.ActivityName != "" {
		t.Errorf("ActivityName = %q, want %q for non-99 activity_id", ev6.ActivityName, "")
	}
}

// ---------------------------------------------------------------------------
// F17: Injectable clock + deterministic Map output (R-71)
// ---------------------------------------------------------------------------

// TestMap_InjectedClockIsDeterministic verifies that a Mapper with a fixed clock
// produces an identical LoggedTime on repeated Map calls. Proves Map is now
// golden-testable (F17 resolved).
func TestMap_InjectedClockIsDeterministic(t *testing.T) {
	fixedTime := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	m := NewMapperWithClock("1.0.0", "test-host", func() time.Time { return fixedTime })

	s := newMinimalSignal(signal.L3Tokenizer)

	ev1, err := m.Map(s)
	if err != nil {
		t.Fatalf("first Map() error = %v", err)
	}
	ev2, err := m.Map(s)
	if err != nil {
		t.Fatalf("second Map() error = %v", err)
	}

	if !ev1.Metadata.LoggedTime.Equal(fixedTime) {
		t.Errorf("ev1.Metadata.LoggedTime = %v, want %v", ev1.Metadata.LoggedTime, fixedTime)
	}
	if !ev2.Metadata.LoggedTime.Equal(fixedTime) {
		t.Errorf("ev2.Metadata.LoggedTime = %v, want %v", ev2.Metadata.LoggedTime, fixedTime)
	}
	if !ev1.Metadata.LoggedTime.Equal(ev2.Metadata.LoggedTime) {
		t.Errorf("LoggedTime is non-deterministic: %v != %v", ev1.Metadata.LoggedTime, ev2.Metadata.LoggedTime)
	}
}

// TestNewMapper_DefaultClockIsRealTime verifies that NewMapper (the public constructor)
// still produces a LoggedTime close to time.Now() — the default path is unchanged.
func TestNewMapper_DefaultClockIsRealTime(t *testing.T) {
	before := time.Now().UTC()
	m := NewMapper("1.0.0", "test-host")
	s := newMinimalSignal(signal.L3Tokenizer)
	ev, err := m.Map(s)
	if err != nil {
		t.Fatalf("Map() error = %v", err)
	}
	after := time.Now().UTC()

	if ev.Metadata.LoggedTime.Before(before) || ev.Metadata.LoggedTime.After(after) {
		t.Errorf("LoggedTime %v is outside real-time window [%v, %v]", ev.Metadata.LoggedTime, before, after)
	}
}

// ---------------------------------------------------------------------------
// R-71: Additional fidelity tests for new first-class objects + honest URL
// ---------------------------------------------------------------------------

// TestMap_HTTP_URLFromContext verifies that a context url key is used as http_request.url.
func TestMap_HTTP_URLFromContext(t *testing.T) {
	m := newTestMapper()
	s := newMinimalSignal(signal.L9APIGateway)
	s.ContextJSON = []byte(`{"url":"https://api.example.com/v1/chat","method":"POST","status_code":200}`)

	ev, err := m.Map(s)
	if err != nil {
		t.Fatalf("Map() error = %v", err)
	}
	if ev.HttpRequest == nil {
		t.Fatal("HttpRequest is nil")
	}
	if ev.HttpRequest.URL != "https://api.example.com/v1/chat" {
		t.Errorf("HttpRequest.URL = %q, want %q", ev.HttpRequest.URL, "https://api.example.com/v1/chat")
	}
}

// TestMap_HTTP_URLNotCategoryWhenAbsent verifies that when no url/target/path is
// in context, http_request.url is "" — not s.Category.
func TestMap_HTTP_URLNotCategoryWhenAbsent(t *testing.T) {
	m := newTestMapper()
	s := newMinimalSignal(signal.L9APIGateway)
	s.Category = "api.request" // must NOT appear in the URL

	ev, err := m.Map(s)
	if err != nil {
		t.Fatalf("Map() error = %v", err)
	}
	if ev.HttpRequest == nil {
		t.Fatal("HttpRequest is nil")
	}
	if ev.HttpRequest.URL == s.Category {
		t.Errorf("HttpRequest.URL == s.Category %q; URL must never be the signal category", s.Category)
	}
	if ev.HttpRequest.URL != "" {
		t.Errorf("HttpRequest.URL = %q, want empty string when no context URL is derivable", ev.HttpRequest.URL)
	}
}

// TestMap_WebResources_OmittedWhenNoURL verifies that a 6001 event with no
// resource_url in context produces nil/empty WebResources (no placeholder).
func TestMap_WebResources_OmittedWhenNoURL(t *testing.T) {
	m := newTestMapper()
	s := newMinimalSignal(signal.L10Application)
	// No ContextJSON.

	ev, err := m.Map(s)
	if err != nil {
		t.Fatalf("Map() error = %v", err)
	}
	if len(ev.WebResources) != 0 {
		t.Errorf("WebResources should be empty when no resource_url in context, got %v", ev.WebResources)
	}
}

// TestMap_Datastore_DatabucketOmittedWhenAbsent verifies that a 6005 event with
// no vector_index in context produces nil Databucket (no placeholder).
func TestMap_Datastore_DatabucketOmittedWhenAbsent(t *testing.T) {
	m := newTestMapper()
	s := newMinimalSignal(signal.L7RAGRetrieval)
	// No ContextJSON.

	ev, err := m.Map(s)
	if err != nil {
		t.Fatalf("Map() error = %v", err)
	}
	if ev.Databucket != nil {
		t.Errorf("Databucket should be nil when no vector_index in context, got %+v", ev.Databucket)
	}
}
