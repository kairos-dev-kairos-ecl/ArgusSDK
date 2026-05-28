// Package dryrun provides a file-based signal injection and recording harness
// for ArgusSDK. It lets you load signals from a JSON file, push them through
// the full proto and OCSF conversion pipeline, validate both representations,
// and write the results to disk — without running any SIEM or Argus instance.
//
// # Quick start
//
//	cfg := dryrun.Config{
//	    InputFile:  "testdata/dryrun/sample-signals.json",
//	    OutputDir:  "/tmp/argus-dryrun",
//	    AppID:      "my-llm-app",
//	    Env:        "dev",
//	    SDKVersion: "0.1.0",
//	}
//	report, err := dryrun.Run(cfg)
//
// # Output files
//
//   - signals-proto.json — protojson-encoded SignalBatch (human-readable)
//   - signals-proto.pb  — binary proto wire format (for tooling)
//   - signals-ocsf.json — OCSF v1.3 JSON array (one event per signal)
//
// # Validation
//
// Three validation stages run on every signal:
//  1. proto — required fields and enum ranges
//  2. ocsf_structural — type_uid = class_uid*100+activity_id; class-specific required fields
//  3. ocsf_schema — OCSF base-event JSON Schema v1.3 (embedded in schema/)
package dryrun

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/ocsf"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// Config drives a single dry-run session.
type Config struct {
	// InputFile is the path to a JSON file containing an array of signal objects.
	// See testdata/dryrun/sample-signals.json for the expected shape.
	InputFile string

	// OutputDir is where output files are written. Created if it does not exist.
	OutputDir string

	// BatchID is stamped on the proto SignalBatch. Auto-generated if empty.
	BatchID string

	// AppID, Env, SDKVersion are batch-level fields applied to signals that
	// omit them in the input file. Per-signal values in the file take precedence.
	AppID      string
	Env        string
	SDKVersion string

	// MapperProductVersion is passed to ocsf.NewMapper. Defaults to "1.0.0".
	MapperProductVersion string

	// MapperAgentHostname is stamped in OCSF device fields. Defaults to os.Hostname().
	MapperAgentHostname string
}

// Report summarises a dry-run session.
type Report struct {
	Total      int // total signals in the input file
	ProtoValid int // signals that passed all proto validation checks
	OCSFValid  int // signals that produced a valid OCSF event

	// Validation errors accumulated across all stages.
	// A signal can appear in multiple stages (e.g. proto and ocsf_structural).
	Errors []ValidationError

	// Absolute paths of the written output files.
	ProtoJSONFile string
	ProtoBinFile  string
	OCSFJSONFile  string
}

// ValidationError describes a single validation failure for one signal.
type ValidationError struct {
	Index   int    // zero-based position in the input file
	SignalID string
	Stage   string // "proto" | "ocsf_structural" | "ocsf_schema"
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("[%s] signal[%d] %q — %s: %s", e.Stage, e.Index, e.SignalID, e.Field, e.Message)
}

// Run executes the full dry-run pipeline. Validation errors are returned inside
// the Report rather than as a Go error. A non-nil error return indicates an I/O
// or configuration failure that prevented the run from completing.
func Run(cfg Config) (*Report, error) {
	if cfg.BatchID == "" {
		cfg.BatchID = fmt.Sprintf("dryrun-%d", time.Now().UnixNano())
	}
	if cfg.MapperProductVersion == "" {
		cfg.MapperProductVersion = "1.0.0"
	}
	if cfg.MapperAgentHostname == "" {
		host, _ := os.Hostname()
		if host == "" {
			host = "argus-dryrun"
		}
		cfg.MapperAgentHostname = host
	}

	if err := os.MkdirAll(cfg.OutputDir, 0750); err != nil {
		return nil, fmt.Errorf("create output dir %s: %w", cfg.OutputDir, err)
	}

	signals, err := LoadSignals(cfg.InputFile)
	if err != nil {
		return nil, fmt.Errorf("load signals: %w", err)
	}

	// Apply batch-level defaults to signals that omit them in the file.
	applyBatchDefaults(signals, cfg)

	mapper := ocsf.NewMapper(cfg.MapperProductVersion, cfg.MapperAgentHostname)

	report := &Report{
		Total:        len(signals),
		ProtoJSONFile: filepath.Join(cfg.OutputDir, "signals-proto.json"),
		ProtoBinFile:  filepath.Join(cfg.OutputDir, "signals-proto.pb"),
		OCSFJSONFile:  filepath.Join(cfg.OutputDir, "signals-ocsf.json"),
	}

	// Stage 1: proto validation
	protoErrs := validateProto(signals)
	report.Errors = append(report.Errors, protoErrs...)
	protoFailSet := indexSet(protoErrs)
	for i := range signals {
		if !protoFailSet[i] {
			report.ProtoValid++
		}
	}

	// Stage 2+3: OCSF mapping then structural + schema validation
	events, mapErrs := mapper.MapBatch(signals)
	for i, mapErr := range mapErrs {
		if mapErr != nil {
			report.Errors = append(report.Errors, ValidationError{
				Index:   i,
				SignalID: signalID(signals, i),
				Stage:   "ocsf_schema",
				Field:   "mapper",
				Message: mapErr.Error(),
			})
		}
	}
	ocsfErrs := validateOCSFBatch(events, signals)
	report.Errors = append(report.Errors, ocsfErrs...)
	ocsfFailSet := indexSet(ocsfErrs)
	// also mark mapper-error signals as failed
	for i, me := range mapErrs {
		if me != nil {
			ocsfFailSet[i] = true
		}
	}
	for i, ev := range events {
		if ev != nil && !ocsfFailSet[i] {
			report.OCSFValid++
		}
	}

	// Write output files (even when there are validation errors — partial output is useful).
	batch := signal.Batch{AppID: cfg.AppID, Env: cfg.Env, Signals: signals}
	protoBatch := batch.ToProto(cfg.BatchID, cfg.SDKVersion)
	if err := writeProto(report.ProtoJSONFile, report.ProtoBinFile, protoBatch); err != nil {
		return report, fmt.Errorf("write proto output: %w", err)
	}
	if err := writeOCSF(report.OCSFJSONFile, events); err != nil {
		return report, fmt.Errorf("write ocsf output: %w", err)
	}

	return report, nil
}

func applyBatchDefaults(signals []signal.Signal, cfg Config) {
	for i := range signals {
		if signals[i].AppID == "" {
			signals[i].AppID = cfg.AppID
		}
		if signals[i].Env == "" {
			signals[i].Env = cfg.Env
		}
		if signals[i].SDKVersion == "" {
			signals[i].SDKVersion = cfg.SDKVersion
		}
	}
}

func signalID(signals []signal.Signal, i int) string {
	if i < len(signals) {
		return signals[i].SignalID
	}
	return ""
}

func indexSet(errs []ValidationError) map[int]bool {
	m := make(map[int]bool, len(errs))
	for _, e := range errs {
		m[e.Index] = true
	}
	return m
}
