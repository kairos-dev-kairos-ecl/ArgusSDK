package dryrun

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/ocsf"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

//go:embed schema/ocsf_base_event.json
var embeddedSchemaBytes []byte

// schemaNode is a minimal JSON Schema Draft-07 node covering the subset of
// keywords used in schema/ocsf_base_event.json.
type schemaNode struct {
	Type       string                `json:"type,omitempty"`
	Required   []string              `json:"required,omitempty"`
	Properties map[string]schemaNode `json:"properties,omitempty"`
	Minimum    *float64              `json:"minimum,omitempty"`
	Maximum    *float64              `json:"maximum,omitempty"`
	Const      interface{}           `json:"const,omitempty"`
	MinLength  *int                  `json:"minLength,omitempty"`
}

func loadEmbeddedSchema() (*schemaNode, error) {
	var node schemaNode
	if err := json.Unmarshal(embeddedSchemaBytes, &node); err != nil {
		return nil, fmt.Errorf("parse embedded OCSF schema: %w", err)
	}
	return &node, nil
}

// evalSchemaNode recursively validates a JSON value against a schema node.
// Returns a list of human-readable error strings (field path + message).
func evalSchemaNode(v interface{}, node *schemaNode, path string) []string {
	var errs []string
	switch node.Type {
	case "integer":
		n, ok := v.(float64) // JSON numbers always decode as float64
		if !ok {
			return append(errs, fmt.Sprintf("%s: expected integer, got %T", path, v))
		}
		if node.Minimum != nil && n < *node.Minimum {
			errs = append(errs, fmt.Sprintf("%s: minimum %.0f, got %.0f", path, *node.Minimum, n))
		}
		if node.Maximum != nil && n > *node.Maximum {
			errs = append(errs, fmt.Sprintf("%s: maximum %.0f, got %.0f", path, *node.Maximum, n))
		}

	case "string":
		s, ok := v.(string)
		if !ok {
			return append(errs, fmt.Sprintf("%s: expected string, got %T", path, v))
		}
		if node.Const != nil {
			want, _ := node.Const.(string)
			if s != want {
				errs = append(errs, fmt.Sprintf("%s: const violation — got %q, want %q", path, s, want))
			}
		}
		if node.MinLength != nil && len(s) < *node.MinLength {
			errs = append(errs, fmt.Sprintf("%s: minLength %d, got %d", path, *node.MinLength, len(s)))
		}

	case "object":
		m, ok := v.(map[string]interface{})
		if !ok {
			return append(errs, fmt.Sprintf("%s: expected object, got %T", path, v))
		}
		for _, req := range node.Required {
			if _, exists := m[req]; !exists {
				errs = append(errs, fmt.Sprintf("%s.%s: required field missing", path, req))
			}
		}
		for prop, propSchema := range node.Properties {
			val, exists := m[prop]
			if !exists {
				continue // absence already caught by required check above
			}
			ps := propSchema
			errs = append(errs, evalSchemaNode(val, &ps, path+"."+prop)...)
		}
	}
	return errs
}

// ── Proto validation ─────────────────────────────────────────────────────────

// validateProto checks each signal's required fields and enum ranges against
// what the proto layer expects before serialisation.
func validateProto(signals []signal.Signal) []ValidationError {
	var errs []ValidationError
	for i, s := range signals {
		sid := s.SignalID
		if s.SignalID == "" {
			errs = append(errs, ValidationError{i, sid, "proto", "signal_id",
				"required — must be a non-empty ULID"})
		}
		if s.Layer < signal.L1Hardware || s.Layer > signal.LDecision {
			errs = append(errs, ValidationError{i, sid, "proto", "layer",
				fmt.Sprintf("invalid value %d — must be 1 (L1Hardware) … 11 (LDecision)", s.Layer)})
		}
		if s.Severity < signal.SeverityInfo || s.Severity > signal.SeverityCritical {
			errs = append(errs, ValidationError{i, sid, "proto", "severity",
				fmt.Sprintf("invalid value %d — must be 1 (Info) … 5 (Critical)", s.Severity)})
		}
		if s.Timestamp.IsZero() {
			errs = append(errs, ValidationError{i, sid, "proto", "timestamp",
				"required — must be a non-zero time"})
		}
		if len(s.ContextJSON) > 0 && !json.Valid(s.ContextJSON) {
			errs = append(errs, ValidationError{i, sid, "proto", "context_json",
				"must be valid JSON when present"})
		}
	}
	return errs
}

// ── OCSF validation ──────────────────────────────────────────────────────────

// validateOCSFBatch runs both structural and JSON-schema checks on each
// non-nil OCSF event.
func validateOCSFBatch(events []*ocsf.Event, signals []signal.Signal) []ValidationError {
	schema, schemaErr := loadEmbeddedSchema()

	var errs []ValidationError
	for i, ev := range events {
		if ev == nil {
			continue
		}
		sid := signalID(signals, i)
		errs = append(errs, validateOCSFStructural(ev, i, sid)...)
		if schemaErr == nil {
			errs = append(errs, validateOCSFSchema(ev, schema, i, sid)...)
		}
	}
	return errs
}

// validateOCSFStructural checks invariants that are derivable from the typed
// Event struct without re-serialising to JSON (fast path).
func validateOCSFStructural(ev *ocsf.Event, idx int, sid string) []ValidationError {
	var errs []ValidationError

	wantTypeUID := int(ev.ClassUID)*100 + ev.ActivityID
	if ev.TypeUID != wantTypeUID {
		errs = append(errs, ValidationError{idx, sid, "ocsf_structural", "type_uid",
			fmt.Sprintf("got %d, want class_uid(%d)*100+activity_id(%d)=%d",
				ev.TypeUID, ev.ClassUID, ev.ActivityID, wantTypeUID)})
	}
	if ev.CategoryUID == 0 {
		errs = append(errs, ValidationError{idx, sid, "ocsf_structural", "category_uid", "must be non-zero"})
	}
	if ev.Time == 0 {
		errs = append(errs, ValidationError{idx, sid, "ocsf_structural", "time", "required — must be non-zero Unix milliseconds"})
	}
	if ev.SeverityID == 0 {
		errs = append(errs, ValidationError{idx, sid, "ocsf_structural", "severity_id", "required — must be 1–6"})
	}
	if ev.Metadata.Version != "1.3.0" {
		errs = append(errs, ValidationError{idx, sid, "ocsf_structural", "metadata.version",
			fmt.Sprintf("got %q, want \"1.3.0\"", ev.Metadata.Version)})
	}
	if ev.Metadata.Product.VendorName == "" {
		errs = append(errs, ValidationError{idx, sid, "ocsf_structural", "metadata.product.vendor_name", "required non-empty string"})
	}

	// Class-specific required fields
	switch ev.ClassUID {
	case ocsf.ClassHTTPActivity:
		if ev.HttpRequest == nil {
			errs = append(errs, ValidationError{idx, sid, "ocsf_structural", "http_request", "required for ClassHTTPActivity (4002)"})
		}
		if ev.HttpResponse == nil {
			errs = append(errs, ValidationError{idx, sid, "ocsf_structural", "http_response", "required for ClassHTTPActivity (4002)"})
		}
		if ev.DstEndpoint == nil {
			errs = append(errs, ValidationError{idx, sid, "ocsf_structural", "dst_endpoint", "required for ClassHTTPActivity (4002)"})
		}
	case ocsf.ClassDetectionFinding:
		if ev.FindingInfo == nil {
			errs = append(errs, ValidationError{idx, sid, "ocsf_structural", "finding_info", "required for ClassDetectionFinding (2004)"})
		} else {
			if ev.FindingInfo.UID == "" {
				errs = append(errs, ValidationError{idx, sid, "ocsf_structural", "finding_info.uid", "required"})
			}
			if ev.FindingInfo.Title == "" {
				errs = append(errs, ValidationError{idx, sid, "ocsf_structural", "finding_info.title", "required"})
			}
		}
	case ocsf.ClassAPIActivity:
		if ev.Api == nil {
			errs = append(errs, ValidationError{idx, sid, "ocsf_structural", "api", "required for ClassAPIActivity (6003)"})
		}
	case ocsf.ClassMemoryActivity, ocsf.ClassModuleActivity:
		if ev.Device == nil {
			errs = append(errs, ValidationError{idx, sid, "ocsf_structural", "device",
				fmt.Sprintf("required for system-category class %d", ev.ClassUID)})
		}
	}

	return errs
}

// validateOCSFSchema marshals the event to JSON and evaluates it against the
// embedded OCSF base-event schema.
func validateOCSFSchema(ev *ocsf.Event, schema *schemaNode, idx int, sid string) []ValidationError {
	data, err := json.Marshal(ev)
	if err != nil {
		return []ValidationError{{idx, sid, "ocsf_schema", "json_marshal", err.Error()}}
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return []ValidationError{{idx, sid, "ocsf_schema", "json_unmarshal", err.Error()}}
	}

	raw := evalSchemaNode(m, schema, "event")
	errs := make([]ValidationError, len(raw))
	for i, msg := range raw {
		errs[i] = ValidationError{idx, sid, "ocsf_schema", "schema", msg}
	}
	return errs
}
