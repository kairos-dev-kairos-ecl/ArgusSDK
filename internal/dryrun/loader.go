package dryrun

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// inputSignal is the JSON shape in the input file. Field names are snake_case
// so the file is easy to hand-edit without knowing Go struct names.
type inputSignal struct {
	SignalID       string  `json:"signal_id"`
	TraceID        string  `json:"trace_id,omitempty"`
	SpanID         string  `json:"span_id,omitempty"`
	ParentSpanID   string  `json:"parent_span_id,omitempty"`
	Layer          int32   `json:"layer"`    // numeric: 1=L1Hardware … 11=LDecision
	Category       string  `json:"category,omitempty"`
	Severity       int32   `json:"severity"` // numeric: 1=Info … 5=Critical
	AppID          string  `json:"app_id,omitempty"`
	AppVersion     string  `json:"app_version,omitempty"`
	SDKVersion     string  `json:"sdk_version,omitempty"`
	Env            string  `json:"env,omitempty"`
	Timestamp      string  `json:"timestamp,omitempty"` // RFC3339; defaults to now if empty
	DurationMS     float32 `json:"duration_ms,omitempty"`
	ContextJSON    string  `json:"context_json,omitempty"` // raw JSON object as string
	SessionID      string  `json:"session_id,omitempty"`
	ConversationID string  `json:"conversation_id,omitempty"`
	UserID         string  `json:"user_id,omitempty"` // hashed
}

// LoadSignals reads a JSON file containing an array of signal objects and
// returns the equivalent []signal.Signal ready for the ArgusSDK pipeline.
func LoadSignals(path string) ([]signal.Signal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var raw []inputSignal
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	out := make([]signal.Signal, len(raw))
	for i, r := range raw {
		ts := time.Now().UTC()
		if r.Timestamp != "" {
			if parsed, err := time.Parse(time.RFC3339, r.Timestamp); err == nil {
				ts = parsed
			}
		}
		var ctxJSON []byte
		if r.ContextJSON != "" {
			ctxJSON = []byte(r.ContextJSON)
		}
		out[i] = signal.Signal{
			SignalID:       r.SignalID,
			TraceID:        r.TraceID,
			SpanID:         r.SpanID,
			ParentSpanID:   r.ParentSpanID,
			Layer:          signal.Layer(r.Layer),
			Category:       r.Category,
			Severity:       signal.Severity(r.Severity),
			AppID:          r.AppID,
			AppVersion:     r.AppVersion,
			SDKVersion:     r.SDKVersion,
			Env:            r.Env,
			Timestamp:      ts,
			DurationMS:     r.DurationMS,
			ContextJSON:    ctxJSON,
			SessionID:      r.SessionID,
			ConversationID: r.ConversationID,
			UserID:         r.UserID,
		}
	}
	return out, nil
}
