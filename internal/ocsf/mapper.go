// Package ocsf maps ArgusSDK signals to OCSF (Open Cybersecurity Schema Framework)
// v1.3 event objects. OCSF translation is the exclusive responsibility of this
// package; no connector implementation imports OCSF types directly.
//
// OCSF class mapping:
//   L1  Hardware         → class_uid 1001 (System Activity)
//   L2  Model Weights    → class_uid 1001 (System Activity)
//   L3  Tokenizer        → class_uid 4001 (Network Activity) — API boundary
//   L4  Transformer      → class_uid 1001 (System Activity)
//   L5  Output Decoding  → class_uid 4001 (Network Activity)
//   L6  Safety           → class_uid 2001 (Security Finding)
//   L7  RAG Retrieval    → class_uid 4001 (Network Activity)
//   L8  Agents           → class_uid 6001 (Application Activity)
//   L9  API Gateway      → class_uid 4001 (Network Activity)
//   L10 Application      → class_uid 6001 (Application Activity)
//   L_Decision           → class_uid 2001 (Security Finding)
//
// These mappings are preliminary and will be refined during implementation.
package ocsf

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// ClassUID is an OCSF event class identifier.
type ClassUID int

const (
	ClassSystemActivity   ClassUID = 1001
	ClassSecurityFinding  ClassUID = 2001
	ClassNetworkActivity  ClassUID = 4001
	ClassApplicationActivity ClassUID = 6001
)

// Severity maps OCSF severity_id values (1=Informational … 5=Critical).
type OCSFSeverity int

// Event is a minimal OCSF v1.3 envelope. Connector implementations JSON-encode
// this struct and submit it to their respective SIEM ingestion APIs.
// Fields are tagged with OCSF canonical names.
type Event struct {
	// Required OCSF fields
	ClassUID   ClassUID    `json:"class_uid"`
	ActivityID int         `json:"activity_id"`
	Time       int64       `json:"time"` // Unix milliseconds
	SeverityID OCSFSeverity `json:"severity_id"`
	Status     string      `json:"status,omitempty"`

	// OCSF metadata object
	Metadata EventMetadata `json:"metadata"`

	// Source / actor
	Actor Actor `json:"actor,omitempty"`

	// Extension: Argus-specific context preserved under unmapped_data
	// so analysts can reach the raw signal without losing fidelity.
	UnmappedData map[string]interface{} `json:"unmapped_data,omitempty"`
}

// EventMetadata is the OCSF metadata block required on every event.
type EventMetadata struct {
	Version   string    `json:"version"`   // OCSF schema version, e.g. "1.3.0"
	Product   Product   `json:"product"`
	Profiles  []string  `json:"profiles,omitempty"`
	UID       string    `json:"uid"` // maps to signal_id
	LoggedAt  time.Time `json:"logged_time"`
}

// Product identifies the originating product in the OCSF metadata.
type Product struct {
	Name    string `json:"name"`
	Vendor  string `json:"vendor_name"`
	Version string `json:"version"`
}

// Actor captures the identity of the entity that produced the signal.
type Actor struct {
	AppID      string `json:"app_uid,omitempty"`
	InstanceID string `json:"agent_uid,omitempty"`
}

// Mapper translates ArgusSDK signals to OCSF events.
// It is stateless; a single Mapper instance is safe for concurrent use.
type Mapper struct {
	productVersion string // argus-agent version string, set at build time
}

// NewMapper creates a Mapper. productVersion is embedded in every event's metadata.
func NewMapper(productVersion string) *Mapper {
	if productVersion == "" {
		productVersion = "dev"
	}
	return &Mapper{productVersion: productVersion}
}

// Map converts a single ArgusSDK signal to an OCSF Event.
// Returns an error only if the signal is so malformed that no mapping is possible.
func (m *Mapper) Map(s signal.Signal) (*Event, error) {
	classUID, err := layerToClassUID(s.Layer)
	if err != nil {
		return nil, err
	}

	ev := &Event{
		ClassUID:   classUID,
		ActivityID: 1, // 1 = "Unknown" / generic; refined per class during implementation
		Time:       s.Timestamp.UnixMilli(),
		SeverityID: argusToOCSFSeverity(s.Severity),
		Metadata: EventMetadata{
			Version: "1.3.0",
			Product: Product{
				Name:    "Argus SDK",
				Vendor:  "Argus",
				Version: m.productVersion,
			},
			UID:      s.SignalID,
			LoggedAt: time.Now().UTC(),
		},
		Actor: Actor{
			AppID: s.AppID,
		},
		UnmappedData: map[string]interface{}{
			"trace_id": s.TraceID,
			"span_id":  s.SpanID,
			"layer":    int(s.Layer),
			"category": s.Category,
		},
	}

	// Attach raw layer context so downstream analysts have full fidelity.
	if len(s.ContextJSON) > 0 {
		var raw interface{}
		if err := json.Unmarshal(s.ContextJSON, &raw); err == nil {
			ev.UnmappedData["context"] = raw
		}
	}

	return ev, nil
}

// MapBatch converts a slice of signals to OCSF events, skipping unmappable ones.
// The second return value collects per-signal errors without aborting the batch.
func (m *Mapper) MapBatch(signals []signal.Signal) ([]*Event, []error) {
	events := make([]*Event, 0, len(signals))
	errs := make([]error, 0)
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

// layerToClassUID returns the OCSF class UID for a given signal layer.
func layerToClassUID(l signal.Layer) (ClassUID, error) {
	switch l {
	case signal.L1Hardware, signal.L2ModelWeights, signal.L4Transformer:
		return ClassSystemActivity, nil
	case signal.L3Tokenizer, signal.L5OutputDecoding, signal.L7RAGRetrieval, signal.L9APIGateway:
		return ClassNetworkActivity, nil
	case signal.L6Safety, signal.LDecision:
		return ClassSecurityFinding, nil
	case signal.L8Agents, signal.L10Application:
		return ClassApplicationActivity, nil
	default:
		return 0, fmt.Errorf("unknown layer %d", l)
	}
}

// argusToOCSFSeverity maps Argus severity to OCSF severity_id (1–5).
func argusToOCSFSeverity(s signal.Severity) OCSFSeverity {
	// OCSF: 1=Informational, 2=Low, 3=Medium, 4=High, 5=Critical
	// Argus: 1=Info, 2=Low, 3=Medium, 4=High, 5=Critical — identical mapping
	if s < 1 || s > 5 {
		return 1 // default to Informational
	}
	return OCSFSeverity(s)
}
