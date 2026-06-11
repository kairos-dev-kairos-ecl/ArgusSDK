// Package factory provides the connector factory that maps a neutral,
// agent-independent description of an output (Type, Endpoint, OCSF, TLS,
// Auth, Extra) to a constructed, registered connector.Connector.
//
// Design invariants (locked decisions 1):
//   - This package MUST NOT import internal/agent — doing so would create an
//     agent↔connector import cycle.
//   - FactoryInput is defined here so the agent can populate it by importing
//     internal/connector/factory without connector having to import agent.
//   - This package lives in internal/connector/factory (not in internal/connector)
//     because the sub-connectors (kafka, splunk, elastic, syslog, argusxdr) all
//     import internal/connector for SignalBatch/Connector types; putting the factory
//     in internal/connector would create an internal/connector ↔ kafka import cycle.
//
// Threat mitigations:
//   - T-04-01: closed type switch; unknown Type returns error, never nil/default connector.
//   - T-04-02: factory never logs Auth or Extra values; only Name/Type are logged.
//   - T-04-03: TLS always flows through connector.NewTLSConfig at Connect time (TLS 1.3);
//     factory never calls NewTLSConfig and never sets InsecureSkipVerify.
package factory

import (
	"fmt"
	"strings"

	"github.com/mitchellh/mapstructure"
	"go.uber.org/zap"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/argusxdr"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/elastic"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/kafka"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/splunk"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/syslog"
)

// FactoryInput is a neutral description of an output destination.
// It mirrors the shape of agent.OutputConfig but lives in internal/connector/factory
// so the agent can populate it without creating an agent↔connector import cycle
// (locked decision 1).
// All fields are agent-readable; Build decodes them into typed connector Configs.
type FactoryInput struct {
	// Name is an operator-assigned human-readable label (e.g. "prod-kafka").
	Name string

	// Type selects the connector implementation.
	// Recognised values: "kafka", "splunk_hec", "elastic", "syslog", "argusxdr".
	Type string

	// Endpoint is the primary network address for the connector.
	// Interpretation is type-specific:
	//   kafka      — comma-separated broker list (e.g. "b1:9092,b2:9092")
	//   splunk_hec — full HEC URL (e.g. "https://splunk.example.com:8088")
	//   elastic    — cluster base URL (e.g. "https://elastic.example.com:9200")
	//   syslog     — host:port (e.g. "siem.example.com:514")
	//   argusxdr   — gRPC host:port (e.g. "xdr.example.com:443")
	Endpoint string

	// OCSF indicates whether signals should be translated to OCSF before delivery.
	// The agent sets this per-output (argusxdr is always false; all others true).
	OCSF bool

	// TLS carries transport security settings.
	TLS FactoryTLSInput

	// Auth carries credential key-value pairs. Interpretation is type-specific:
	//   kafka      — sasl_mechanism, sasl_username, sasl_password
	//   splunk_hec — token
	//   elastic    — api_key
	//   argusxdr   — group_id, instance_id, credential
	// Build MUST NOT log any Auth values (T-04-02).
	Auth map[string]string

	// Extra carries connector-specific parameters not covered by the common fields.
	// Decoded into the connector's typed Config via mapstructure.
	// Build MUST NOT log any Extra values (T-04-02).
	Extra map[string]interface{}
}

// FactoryTLSInput is the TLS sub-struct for FactoryInput.
// It mirrors agent.TLSConfig and maps to each connector's TLSConfig.
type FactoryTLSInput struct {
	Enabled    bool
	MinVersion string // informational; connectors use TLS 1.3 unconditionally via NewTLSConfig
	CACert     string
	CertFile   string
	KeyFile    string
}

// Build constructs a Connector for the given FactoryInput.
//
// The function performs the following steps for each type:
//  1. Decode in.Extra into the connector's typed Config via mapstructure (weak decoding).
//  2. Overlay explicit fields from in.Endpoint, in.Auth, and in.TLS into the Config.
//  3. Call the connector package's New constructor and return the result.
//
// Build does NOT call Connect — the agent calls Connect during start() wiring.
// On an unrecognised in.Type, Build returns (nil, error) naming the unknown type
// (T-04-01 mitigation — no nil/default connector is ever returned silently).
func Build(in FactoryInput, logger *zap.Logger) (connector.Connector, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	// T-04-02: log only Name and Type — never Auth or Extra.
	logger.Debug("building connector", zap.String("name", in.Name), zap.String("type", in.Type))

	switch in.Type {
	case "kafka":
		return buildKafka(in)
	case "splunk_hec":
		return buildSplunk(in)
	case "elastic":
		return buildElastic(in)
	case "syslog":
		return buildSyslog(in)
	case "argusxdr":
		return buildArgusXDR(in)
	default:
		// T-04-01: closed switch; unknown type always errors — no nil/default connector returned.
		return nil, fmt.Errorf("connector factory: unknown output type %q", in.Type)
	}
}

// buildKafka decodes Extra into kafka.Config, then overlays Endpoint (split on
// commas into Brokers), Auth (SASL credentials), and TLS.
func buildKafka(in FactoryInput) (connector.Connector, error) {
	var cfg kafka.Config
	if err := decodeExtra(in.Extra, &cfg); err != nil {
		return nil, fmt.Errorf("connector factory: kafka: decoding extra: %w", err)
	}

	// Endpoint → Brokers (comma-separated).
	if in.Endpoint != "" {
		parts := strings.Split(in.Endpoint, ",")
		trimmed := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				trimmed = append(trimmed, s)
			}
		}
		if len(trimmed) > 0 {
			cfg.Brokers = trimmed
		}
	}

	// Auth → SASL.
	if v := in.Auth["sasl_mechanism"]; v != "" {
		cfg.SASL.Mechanism = v
	}
	if v := in.Auth["sasl_username"]; v != "" {
		cfg.SASL.Username = v
	}
	if v := in.Auth["sasl_password"]; v != "" {
		cfg.SASL.Password = v
	}

	// TLS.
	cfg.TLS = kafka.TLSConfig{
		Enabled:  in.TLS.Enabled,
		CACert:   in.TLS.CACert,
		CertFile: in.TLS.CertFile,
		KeyFile:  in.TLS.KeyFile,
	}

	return kafka.New(cfg), nil
}

// buildSplunk decodes Extra into splunk.Config, overlays Endpoint and Auth token.
func buildSplunk(in FactoryInput) (connector.Connector, error) {
	var cfg splunk.Config
	if err := decodeExtra(in.Extra, &cfg); err != nil {
		return nil, fmt.Errorf("connector factory: splunk_hec: decoding extra: %w", err)
	}

	// Endpoint.
	if in.Endpoint != "" {
		cfg.Endpoint = in.Endpoint
	}

	// Auth → Token.
	if v := in.Auth["token"]; v != "" {
		cfg.Token = v
	}

	// TLS — splunk only uses CACert (InsecureSkipVerify is never propagated per T-04-03).
	cfg.TLS = splunk.TLSConfig{
		CACert: in.TLS.CACert,
	}

	return splunk.New(cfg), nil
}

// buildElastic decodes Extra into elastic.Config, overlays Endpoint, APIKey, and TLS.
func buildElastic(in FactoryInput) (connector.Connector, error) {
	var cfg elastic.Config
	if err := decodeExtra(in.Extra, &cfg); err != nil {
		return nil, fmt.Errorf("connector factory: elastic: decoding extra: %w", err)
	}

	// Endpoint.
	if in.Endpoint != "" {
		cfg.Endpoint = in.Endpoint
	}

	// Auth → APIKey.
	if v := in.Auth["api_key"]; v != "" {
		cfg.APIKey = v
	}

	// TLS.
	cfg.TLS = elastic.TLSConfig{
		CACert:   in.TLS.CACert,
		CertFile: in.TLS.CertFile,
		KeyFile:  in.TLS.KeyFile,
	}

	return elastic.New(cfg), nil
}

// buildSyslog decodes Extra into syslog.Config, overlays Server (= Endpoint) and Transport.
func buildSyslog(in FactoryInput) (connector.Connector, error) {
	var cfg syslog.Config
	if err := decodeExtra(in.Extra, &cfg); err != nil {
		return nil, fmt.Errorf("connector factory: syslog: decoding extra: %w", err)
	}

	// Endpoint → Server.
	if in.Endpoint != "" {
		cfg.Server = in.Endpoint
	}

	// Extra["transport"] → Transport.
	if v, ok := in.Extra["transport"].(string); ok && v != "" {
		cfg.Transport = syslog.Transport(v)
	}

	// TLS.
	cfg.TLS = syslog.TLSConfig{
		CACert:   in.TLS.CACert,
		CertFile: in.TLS.CertFile,
		KeyFile:  in.TLS.KeyFile,
	}

	return syslog.New(cfg), nil
}

// buildArgusXDR decodes Extra into argusxdr.Config, overlays Endpoint, Auth, and TLS.
func buildArgusXDR(in FactoryInput) (connector.Connector, error) {
	var cfg argusxdr.Config
	if err := decodeExtra(in.Extra, &cfg); err != nil {
		return nil, fmt.Errorf("connector factory: argusxdr: decoding extra: %w", err)
	}

	// Endpoint.
	if in.Endpoint != "" {
		cfg.Endpoint = in.Endpoint
	}

	// Auth → AuthConfig.
	cfg.Auth = argusxdr.AuthConfig{
		GroupID:    in.Auth["group_id"],
		InstanceID: in.Auth["instance_id"],
		Credential: in.Auth["credential"],
	}

	// TLS.
	cfg.TLS = argusxdr.TLSConfig{
		Enabled:  in.TLS.Enabled,
		CACert:   in.TLS.CACert,
		CertFile: in.TLS.CertFile,
		KeyFile:  in.TLS.KeyFile,
	}

	return argusxdr.New(cfg), nil
}

// decodeExtra decodes src (the Extra map) into dst using mapstructure with weak
// decoding enabled. Weak decoding allows string→int and similar coercions that
// are common in YAML/env config pipelines. Returns nil if src is nil or empty.
func decodeExtra(src map[string]interface{}, dst interface{}) error {
	if len(src) == 0 {
		return nil
	}
	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		WeaklyTypedInput: true,
		Result:           dst,
		TagName:          "mapstructure",
	})
	if err != nil {
		return err
	}
	return dec.Decode(src)
}
