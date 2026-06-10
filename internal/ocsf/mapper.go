// Package ocsf maps ArgusSDK signals to OCSF (Open Cybersecurity Schema Framework)
// v1.3 event objects. OCSF translation is the exclusive responsibility of this
// package; no connector implementation imports OCSF types directly.
//
// # Layer → class_uid mapping (locked 2026-05-20, verified against ocsf-schema v1.3.0)
//
//	L1  Hardware         → 1004 Memory Activity      (GPU/CPU utilisation = memory-intensive compute)
//	L2  Model Weights    → 1005 Module Activity       (weight loading = module load event)
//	L3  Tokenizer        → 6003 API Activity          (tokenizer is an internal API boundary)
//	L4  Transformer      → 1004 Memory Activity       (attention / KV-cache = memory operations)
//	L5  Output Decoding  → 6003 API Activity          (sampling logits = API processing step)
//	L6  Safety           → 2004 Detection Finding     (content filter = detection engine result)
//	L7  RAG Retrieval    → 6005 Datastore Activity    (vector/semantic query = datastore query)
//	L8  Agents           → 6003 API Activity          (tool calls = API CRUD operations)
//	L9  API Gateway      → 4002 HTTP Activity         (genuine HTTP boundary events)
//	L10 Application      → 6001 Web Resources Activity(user-facing application events)
//	L_Decision           → 2004 Detection Finding     (policy enforcement = detection output)
//
// NOTE: class_uid 2001 (Security Finding) is DEPRECATED since OCSF 1.1.0.
// Splunk ES 7.x+ and Microsoft Sentinel ASIM drop events with class_uid 2001.
// Use 2004 (Detection Finding) for all Argus security/policy signals.
//
// NOTE: class_uid 1001 is File System Activity (NOT a generic "System Activity").
// Do not use 1001 for hardware or compute signals.
package ocsf

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// ClassUID is an OCSF v1.3 event class identifier.
// Values verified against github.com/ocsf/ocsf-schema tag v1.3.0.
type ClassUID int

const (
	// System Activity category (category_uid=1)
	ClassFileActivity    ClassUID = 1001 // File System Activity — do NOT use for hardware signals
	ClassMemoryActivity  ClassUID = 1004 // Memory Activity — L1 hardware, L4 transformer
	ClassModuleActivity  ClassUID = 1005 // Module Activity — L2 model weight loading
	ClassProcessActivity ClassUID = 1007 // Process Activity — fallback for L1/L4 without memory context

	// Findings category (category_uid=2)
	// IMPORTANT: 2001 (Security Finding) is DEPRECATED since OCSF 1.1.0. Do not use.
	ClassDetectionFinding ClassUID = 2004 // Detection Finding — L6 safety, LDecision

	// Network Activity category (category_uid=4)
	ClassNetworkActivity ClassUID = 4001 // Network Activity (generic)
	ClassHTTPActivity    ClassUID = 4002 // HTTP Activity — L9 API gateway

	// Application Activity category (category_uid=6)
	ClassWebResourcesActivity ClassUID = 6001 // Web Resources Activity — L10 application
	ClassAPIActivity          ClassUID = 6003 // API Activity — L3 tokenizer, L5 output, L8 agents
	ClassDatastoreActivity    ClassUID = 6005 // Datastore Activity — L7 RAG retrieval
)

// classMeta holds the derived fields required on every OCSF event for a given class.
var classMetaMap = map[ClassUID]classMeta{
	ClassMemoryActivity:       {categoryUID: 1, categoryName: "System Activity", className: "Memory Activity"},
	ClassModuleActivity:       {categoryUID: 1, categoryName: "System Activity", className: "Module Activity"},
	ClassProcessActivity:      {categoryUID: 1, categoryName: "System Activity", className: "Process Activity"},
	ClassDetectionFinding:     {categoryUID: 2, categoryName: "Findings", className: "Detection Finding"},
	ClassHTTPActivity:         {categoryUID: 4, categoryName: "Network Activity", className: "HTTP Activity"},
	ClassWebResourcesActivity: {categoryUID: 6, categoryName: "Application Activity", className: "Web Resources Activity"},
	ClassAPIActivity:          {categoryUID: 6, categoryName: "Application Activity", className: "API Activity"},
	ClassDatastoreActivity:    {categoryUID: 6, categoryName: "Application Activity", className: "Datastore Activity"},
}

type classMeta struct {
	categoryUID  int
	categoryName string
	className    string
}

// Event is a fully OCSF v1.3 compliant event envelope.
// All required fields are present per the ocsf-schema v1.3.0 specification.
type Event struct {
	// Classification — all required per classification.json
	ClassUID     ClassUID `json:"class_uid"`
	ClassName    string   `json:"class_name,omitempty"`
	CategoryUID  int      `json:"category_uid"`  // required — derived from class
	CategoryName string   `json:"category_name,omitempty"`
	ActivityID   int      `json:"activity_id"`   // required — 0=Unknown, 99=Other
	ActivityName string   `json:"activity_name,omitempty"`
	TypeUID      int      `json:"type_uid"`      // required — class_uid*100 + activity_id
	TypeName     string   `json:"type_name,omitempty"`

	// Occurrence — required per occurrence.json
	Time int64 `json:"time"` // Unix milliseconds

	// Severity — required
	SeverityID int    `json:"severity_id"`
	Severity   string `json:"severity,omitempty"`

	// Status (recommended)
	StatusID *int   `json:"status_id,omitempty"`
	Status   string `json:"status,omitempty"`

	// Metadata — required object
	Metadata EventMetadata `json:"metadata"`

	// Class-specific primary objects (populated per layer in Map())
	// HTTP Activity (4002): HttpRequest + HttpResponse + DstEndpoint required
	HttpRequest  *HttpRequest  `json:"http_request,omitempty"`
	HttpResponse *HttpResponse `json:"http_response,omitempty"`

	// API Activity (6003): Api + Actor + SrcEndpoint required
	Api *ApiObject `json:"api,omitempty"`

	// Detection Finding (2004): FindingInfo required
	FindingInfo *FindingInfo `json:"finding_info,omitempty"`

	// Network endpoints (required for 4001/4002; recommended for 6003/6005)
	SrcEndpoint *NetworkEndpoint `json:"src_endpoint,omitempty"`
	DstEndpoint *NetworkEndpoint `json:"dst_endpoint,omitempty"`

	// host profile fields (required for system-category classes 1xxx)
	Device *Device `json:"device,omitempty"`

	// security_control profile fields (applied to L6, LDecision, and all system classes)
	ActionID      *int   `json:"action_id,omitempty"` // pointer — omit when profile not applied
	Action        string `json:"action,omitempty"`
	DispositionID *int   `json:"disposition_id,omitempty"`

	// Actor (required for 6003, 6005, and all system-category classes)
	// actor.app_uid = AppID, actor.app_name = agent identifier
	// NOTE: actor.agent_uid does NOT exist in the OCSF schema — do not add it
	Actor *Actor `json:"actor,omitempty"`

	// Raw Argus signal data preserved for analysts.
	// OCSF canonical key is "unmapped" (NOT "unmapped_data").
	Unmapped map[string]interface{} `json:"unmapped,omitempty"`
}

// EventMetadata is the required OCSF metadata block.
// Fields: version (required), product (required), uid and logged_time (optional).
type EventMetadata struct {
	Version    string    `json:"version"`               // "1.3.0" — required
	Product    Product   `json:"product"`               // required
	UID        string    `json:"uid,omitempty"`         // maps to signal_id (optional)
	LoggedTime time.Time `json:"logged_time,omitempty"` // optional
}

// Product identifies the originating product in metadata.
// vendor_name is required; name is required by convention.
type Product struct {
	VendorName string `json:"vendor_name"` // required
	Name       string `json:"name"`        // required by convention
	Version    string `json:"version,omitempty"`
}

// Actor satisfies the actor object required by API Activity, Datastore Activity,
// and all system-category classes.
// at_least_one constraint: process, user, session, app_name, app_uid.
// Use app_uid = AppID; app_name = agent identifier.
type Actor struct {
	AppUID  string `json:"app_uid,omitempty"`  // maps to AppID
	AppName string `json:"app_name,omitempty"` // maps to agent instance_name
}

// NetworkEndpoint satisfies src_endpoint / dst_endpoint.
// at_least_one of: ip, uid, name, hostname, svc_name, instance_uid — must be set.
type NetworkEndpoint struct {
	Hostname string `json:"hostname,omitempty"`
	IP       string `json:"ip,omitempty"`
	SvcName  string `json:"svc_name,omitempty"` // logical service name
	Port     int    `json:"port,omitempty"`
	Name     string `json:"name,omitempty"`
}

// Device is required by the host profile (all system-category classes 1xxx).
// at_least_one of: hostname, ip, uid, mac — must be set.
type Device struct {
	Hostname string `json:"hostname,omitempty"`
	IP       string `json:"ip,omitempty"`
	TypeID   int    `json:"type_id,omitempty"` // 9=Virtual (for cloud/containerised inference)
	Type     string `json:"type,omitempty"`
	UID      string `json:"uid,omitempty"`
}

// FindingInfo is required for class 2004 Detection Finding.
// uid and title are both required.
type FindingInfo struct {
	UID           string    `json:"uid"`             // required — use signal_id
	Title         string    `json:"title"`           // required — human-readable summary
	Desc          string    `json:"desc,omitempty"`
	Analytic      *Analytic `json:"analytic,omitempty"` // strongly recommended for Splunk/Sentinel
	FirstSeenTime *int64    `json:"first_seen_time,omitempty"`
	LastSeenTime  *int64    `json:"last_seen_time,omitempty"`
	Types         []string  `json:"types,omitempty"`
	SrcURL        string    `json:"src_url,omitempty"`
}

// Analytic is recommended inside FindingInfo for Splunk ES and Microsoft Sentinel
// ASIM compatibility. type_id is required within the analytic object.
type Analytic struct {
	TypeID   int    `json:"type_id"` // required: 1=Rule, 2=Behavioral, 3=Statistical, 4=ML/DL
	Type     string `json:"type,omitempty"`
	UID      string `json:"uid,omitempty"`  // rule_id / classifier_id
	Name     string `json:"name,omitempty"` // rule or model name
	Desc     string `json:"desc,omitempty"`
	Version  string `json:"version,omitempty"`
	Category string `json:"category,omitempty"`
}

// ApiObject is required for class 6003 API Activity.
// operation is required.
type ApiObject struct {
	Operation string       `json:"operation"`         // required — tool name, method name
	Request   *ApiRequest  `json:"request,omitempty"`
	Response  *ApiResponse `json:"response,omitempty"`
	Service   *ApiService  `json:"service,omitempty"`
	Version   string       `json:"version,omitempty"`
}

// ApiRequest carries per-request identifiers.
type ApiRequest struct {
	UID  string `json:"uid,omitempty"`  // request ID / span_id
	Body string `json:"body,omitempty"` // do not populate — PII risk
}

// ApiResponse carries the response code / status.
type ApiResponse struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// ApiService identifies the service providing the API.
type ApiService struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// HttpRequest is required for class 4002 HTTP Activity.
// url and method are required.
type HttpRequest struct {
	URL    string `json:"url"`    // required
	Method string `json:"method"` // required
	UID    string `json:"uid,omitempty"`
}

// HttpResponse is required for class 4002 HTTP Activity.
// code is required.
type HttpResponse struct {
	Code    int    `json:"code"`             // required
	Message string `json:"message,omitempty"`
}

// Mapper translates ArgusSDK signals to OCSF v1.3 events.
// Stateless; a single Mapper instance is safe for concurrent use.
type Mapper struct {
	productVersion string
	agentHostname  string // used to populate device.hostname for system-class events
}

// NewMapper creates a Mapper. productVersion is embedded in every event's metadata.
// agentHostname is used to satisfy the host profile device requirement for system-class events.
func NewMapper(productVersion, agentHostname string) *Mapper {
	if productVersion == "" {
		productVersion = "dev"
	}
	return &Mapper{productVersion: productVersion, agentHostname: agentHostname}
}

// Map converts a single ArgusSDK signal to an OCSF v1.3 Event.
// Returns an error only if the signal layer is unrecognised.
func (m *Mapper) Map(s signal.Signal) (*Event, error) {
	classUID, err := layerToClassUID(s.Layer)
	if err != nil {
		return nil, err
	}

	meta, ok := classMetaMap[classUID]
	if !ok {
		return nil, fmt.Errorf("no class metadata for class_uid %d", classUID)
	}

	activityID := layerToActivityID(s.Layer)
	severityID := int(s.Severity)
	if severityID < 1 || severityID > 5 {
		severityID = 1 // default to Informational
	}

	// F13 / T-03-18: OCSF validators may reject activity_id=99 without a name.
	// Set ActivityName="Other" when activity_id==99 (per OCSF spec for "Other" activities).
	activityName := ""
	if activityID == 99 {
		activityName = "Other"
	}

	ev := &Event{
		ClassUID:     classUID,
		ClassName:    meta.className,
		CategoryUID:  meta.categoryUID,
		CategoryName: meta.categoryName,
		ActivityID:   activityID,
		ActivityName: activityName,
		TypeUID:      int(classUID)*100 + activityID, // required: class_uid*100 + activity_id
		Time:         s.Timestamp.UnixMilli(),
		SeverityID:   severityID,
		Metadata: EventMetadata{
			Version: "1.3.0",
			Product: Product{
				VendorName: "Argus",
				Name:       "Argus SDK",
				Version:    m.productVersion,
			},
			UID: s.SignalID,
			// NOTE(F17): time.Now() makes Map non-deterministic for golden tests.
			// Accepted/deferred 2026-06-10 review — clock injection is an API change
			// touching all connectors. See locked decision 5 in 03-03-SUMMARY.md.
			LoggedTime: time.Now().UTC(),
		},
		Actor: &Actor{
			AppUID:  s.AppID,
			AppName: "argus-agent",
		},
		Unmapped: map[string]interface{}{
			"trace_id":    s.TraceID,
			"span_id":     s.SpanID,
			"layer":       int(s.Layer),
			"category":    s.Category,
			"sdk_version": s.SDKVersion,
		},
	}

	// Attach raw layer context for analyst fidelity.
	if len(s.ContextJSON) > 0 {
		var raw interface{}
		if err := json.Unmarshal(s.ContextJSON, &raw); err == nil {
			ev.Unmapped["context"] = raw
		}
	}

	// Populate class-specific required fields.
	m.populateClassFields(ev, s)

	return ev, nil
}

// extractCtx unmarshals contextJSON into a map[string]interface{}.
// Returns nil if contextJSON is nil, empty, or cannot be parsed.
// This is a defensive helper; callers must check for nil before use.
func extractCtx(contextJSON []byte) map[string]interface{} {
	if len(contextJSON) == 0 {
		return nil
	}
	var ctx map[string]interface{}
	if err := json.Unmarshal(contextJSON, &ctx); err != nil {
		return nil
	}
	return ctx
}

// populateClassFields sets the class-specific required objects on the event.
// ContextJSON is parsed per class to extract precise field values where available,
// falling back to safe defaults when keys are absent or parsing fails.
func (m *Mapper) populateClassFields(ev *Event, s signal.Signal) {
	switch ev.ClassUID {
	case ClassMemoryActivity, ClassModuleActivity, ClassProcessActivity:
		// System-category classes: host profile requires device; security_control requires action_id.
		ev.Device = &Device{
			Hostname: m.agentHostname,
			TypeID:   9, // Virtual (cloud/container default)
		}
		allowed := 1
		ev.ActionID = &allowed // 1=Allowed (observed-only, no enforcement)

	case ClassDetectionFinding:
		// Detection Finding (2004): finding_info is required.
		// Analytic type_id=4 (ML/DL) for safety classifiers; type_id=1 (Rule) for policy decisions.
		analyticTypeID := 1 // Rule (default — policy decisions)
		if s.Layer == signal.L6Safety {
			analyticTypeID = 4 // ML/DL — safety classifiers are ML models
		}
		ev.FindingInfo = &FindingInfo{
			UID:   s.SignalID,
			Title: fmt.Sprintf("%s: %s", layerName(s.Layer), s.Category),
			Analytic: &Analytic{
				TypeID: analyticTypeID,
				Name:   s.Category,
			},
		}
		// action_id from security_control profile: default to Allowed; context parsing will override.
		allowed := 1
		ev.ActionID = &allowed

	case ClassHTTPActivity:
		// HTTP Activity (4002): http_request, http_response, dst_endpoint required.
		// Parse ContextJSON for url, method, and status_code; fall back to defaults if absent.
		ctx := extractCtx(s.ContextJSON)

		url := s.Category // fallback: use category as URL placeholder
		method := "POST"  // fallback default method
		statusCode := 200 // fallback default status

		if ctx != nil {
			if v, ok := ctx["url"]; ok {
				if urlStr, ok := v.(string); ok && urlStr != "" {
					url = urlStr
				}
			}
			if v, ok := ctx["method"]; ok {
				if methodStr, ok := v.(string); ok && methodStr != "" {
					method = methodStr
				}
			}
			if v, ok := ctx["status_code"]; ok {
				if code, ok := v.(float64); ok {
					statusCode = int(code)
				}
			}
		}

		ev.HttpRequest = &HttpRequest{
			URL:    url,
			Method: method,
			UID:    s.SpanID,
		}
		ev.HttpResponse = &HttpResponse{Code: statusCode}
		ev.DstEndpoint = &NetworkEndpoint{SvcName: s.AppID}

	case ClassAPIActivity:
		// API Activity (6003): api + actor (already set) + src_endpoint required.
		ev.Api = &ApiObject{
			Operation: s.Category,
			Request:   &ApiRequest{UID: s.SpanID},
			Service:   &ApiService{Name: layerServiceName(s.Layer)},
		}
		ev.SrcEndpoint = &NetworkEndpoint{SvcName: s.AppID}

	case ClassDatastoreActivity:
		// Datastore Activity (6005): actor (set) + src_endpoint + at_least_one of database/databucket/table.
		// Parse ContextJSON.vector_index → databucket.name stored in Unmapped for now.
		// TODO: promote to first-class databucket object when OCSF databucket type is added to Event.
		ev.SrcEndpoint = &NetworkEndpoint{SvcName: s.AppID}
		ctx := extractCtx(s.ContextJSON)
		if ctx != nil {
			if v, ok := ctx["vector_index"]; ok {
				if name, ok := v.(string); ok && name != "" {
					ev.Unmapped["databucket_name"] = name
				}
			}
		}

	case ClassWebResourcesActivity:
		// Web Resources Activity (6001): web_resources required.
		// Parse ContextJSON for resource_url and store in Unmapped.
		// Note: web_resources object is deferred pending OCSF extension work.
		ctx := extractCtx(s.ContextJSON)
		if ctx != nil {
			if v, ok := ctx["resource_url"]; ok {
				if urlStr, ok := v.(string); ok && urlStr != "" {
					ev.Unmapped["web_resource_url"] = urlStr
				}
			}
		}
	}
}

// MapBatch converts a slice of signals to OCSF events. Errors are collected
// per signal; the batch continues even if individual signals fail to map.
func (m *Mapper) MapBatch(signals []signal.Signal) ([]*Event, []error) {
	events := make([]*Event, 0, len(signals))
	var errs []error
	for _, s := range signals {
		ev, err := m.Map(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("signal %s: %w", s.SignalID, err))
			continue
		}
		events = append(events, ev)
	}
	return events, errs
}

// layerToClassUID returns the verified OCSF v1.3 class_uid for a signal layer.
func layerToClassUID(l signal.Layer) (ClassUID, error) {
	switch l {
	case signal.L1Hardware:
		return ClassMemoryActivity, nil // 1004
	case signal.L2ModelWeights:
		return ClassModuleActivity, nil // 1005
	case signal.L3Tokenizer:
		return ClassAPIActivity, nil // 6003
	case signal.L4Transformer:
		return ClassMemoryActivity, nil // 1004
	case signal.L5OutputDecoding:
		return ClassAPIActivity, nil // 6003
	case signal.L6Safety:
		return ClassDetectionFinding, nil // 2004
	case signal.L7RAGRetrieval:
		return ClassDatastoreActivity, nil // 6005
	case signal.L8Agents:
		return ClassAPIActivity, nil // 6003
	case signal.L9APIGateway:
		return ClassHTTPActivity, nil // 4002
	case signal.L10Application:
		return ClassWebResourcesActivity, nil // 6001
	case signal.LDecision:
		return ClassDetectionFinding, nil // 2004
	default:
		return 0, fmt.Errorf("unknown layer %d", l)
	}
}

// layerToActivityID returns the OCSF activity_id for each layer.
// Verified against OCSF v1.3 activity enum values per class.
func layerToActivityID(l signal.Layer) int {
	switch l {
	case signal.L1Hardware:
		return 7 // Memory Activity: Read (sampling GPU/CPU metrics)
	case signal.L2ModelWeights:
		return 1 // Module Activity: Load
	case signal.L3Tokenizer:
		return 2 // API Activity: Read (tokenizer reads input text)
	case signal.L4Transformer:
		return 7 // Memory Activity: Read (KV-cache / attention memory ops)
	case signal.L5OutputDecoding:
		return 2 // API Activity: Read (decoder samples logits)
	case signal.L6Safety:
		return 1 // Detection Finding: Create (new finding per inference)
	case signal.L7RAGRetrieval:
		return 4 // Datastore Activity: Query
	case signal.L8Agents:
		return 99 // API Activity: Other (tool-specific; override from context)
	case signal.L9APIGateway:
		return 6 // HTTP Activity: Post (default; override from context.method)
	case signal.L10Application:
		return 2 // Web Resources Activity: Read (default)
	case signal.LDecision:
		return 1 // Detection Finding: Create
	default:
		return 0 // Unknown
	}
}

func layerName(l signal.Layer) string {
	names := map[signal.Layer]string{
		signal.L1Hardware: "L1 Hardware", signal.L2ModelWeights: "L2 Model Weights",
		signal.L3Tokenizer: "L3 Tokenizer", signal.L4Transformer: "L4 Transformer",
		signal.L5OutputDecoding: "L5 Output Decoding", signal.L6Safety: "L6 Safety",
		signal.L7RAGRetrieval: "L7 RAG Retrieval", signal.L8Agents: "L8 Agents",
		signal.L9APIGateway: "L9 API Gateway", signal.L10Application: "L10 Application",
		signal.LDecision: "L Decision",
	}
	if n, ok := names[l]; ok {
		return n
	}
	return fmt.Sprintf("Layer %d", l)
}

func layerServiceName(l signal.Layer) string {
	switch l {
	case signal.L3Tokenizer:
		return "tokenizer"
	case signal.L5OutputDecoding:
		return "output-decoder"
	case signal.L8Agents:
		return "agent-orchestrator"
	default:
		return "argus-sdk"
	}
}
