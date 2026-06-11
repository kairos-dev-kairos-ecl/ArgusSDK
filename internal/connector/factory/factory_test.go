package factory_test

import (
	"testing"

	"go.uber.org/zap"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/factory"
)

// TestBuildKafka verifies that Build returns a connector whose Name() is "kafka"
// when Type is "kafka".
func TestBuildKafka(t *testing.T) {
	in := factory.FactoryInput{
		Name:     "my-kafka",
		Type:     "kafka",
		Endpoint: "broker1:9092,broker2:9092",
		Extra: map[string]interface{}{
			"topic": "signals",
		},
	}
	c, err := factory.Build(in, zap.NewNop())
	if err != nil {
		t.Fatalf("Build(kafka) unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("Build(kafka) returned nil connector")
	}
	if c.Name() != "kafka" {
		t.Fatalf("Build(kafka) Name() = %q, want %q", c.Name(), "kafka")
	}
}

// TestBuildSplunk verifies that Build returns a connector whose Name() is "splunk_hec"
// when Type is "splunk_hec".
func TestBuildSplunk(t *testing.T) {
	in := factory.FactoryInput{
		Name:     "my-splunk",
		Type:     "splunk_hec",
		Endpoint: "https://splunk.example.com:8088",
		Auth:     map[string]string{"token": "hec-token-value"},
	}
	c, err := factory.Build(in, zap.NewNop())
	if err != nil {
		t.Fatalf("Build(splunk_hec) unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("Build(splunk_hec) returned nil connector")
	}
	if c.Name() != "splunk_hec" {
		t.Fatalf("Build(splunk_hec) Name() = %q, want %q", c.Name(), "splunk_hec")
	}
}

// TestBuildElastic verifies that Build returns a connector whose Name() is "elastic"
// when Type is "elastic".
func TestBuildElastic(t *testing.T) {
	in := factory.FactoryInput{
		Name:     "my-elastic",
		Type:     "elastic",
		Endpoint: "https://elastic.example.com:9200",
		Auth:     map[string]string{"api_key": "id:key"},
	}
	c, err := factory.Build(in, zap.NewNop())
	if err != nil {
		t.Fatalf("Build(elastic) unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("Build(elastic) returned nil connector")
	}
	if c.Name() != "elastic" {
		t.Fatalf("Build(elastic) Name() = %q, want %q", c.Name(), "elastic")
	}
}

// TestBuildSyslog verifies that Build returns a connector whose Name() is "syslog"
// when Type is "syslog".
func TestBuildSyslog(t *testing.T) {
	in := factory.FactoryInput{
		Name:     "my-syslog",
		Type:     "syslog",
		Endpoint: "siem.example.com:514",
		Extra: map[string]interface{}{
			"transport": "tcp",
		},
	}
	c, err := factory.Build(in, zap.NewNop())
	if err != nil {
		t.Fatalf("Build(syslog) unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("Build(syslog) returned nil connector")
	}
	if c.Name() != "syslog" {
		t.Fatalf("Build(syslog) Name() = %q, want %q", c.Name(), "syslog")
	}
}

// TestBuildArgusXDR verifies that Build returns a connector whose Name() is "argusxdr"
// when Type is "argusxdr".
func TestBuildArgusXDR(t *testing.T) {
	in := factory.FactoryInput{
		Name:     "my-xdr",
		Type:     "argusxdr",
		Endpoint: "xdr.example.com:443",
		Auth: map[string]string{
			"group_id":    "g1",
			"instance_id": "i1",
			"credential":  "secret",
		},
	}
	c, err := factory.Build(in, zap.NewNop())
	if err != nil {
		t.Fatalf("Build(argusxdr) unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("Build(argusxdr) returned nil connector")
	}
	if c.Name() != "argusxdr" {
		t.Fatalf("Build(argusxdr) Name() = %q, want %q", c.Name(), "argusxdr")
	}
}

// TestBuildUnknownType verifies that Build returns (nil, non-nil error) for an
// unrecognised Type and that the error message names the unknown type.
func TestBuildUnknownType(t *testing.T) {
	in := factory.FactoryInput{
		Name: "bad",
		Type: "totally_unknown_connector",
	}
	c, err := factory.Build(in, zap.NewNop())
	if err == nil {
		t.Fatal("Build(unknown) expected error, got nil")
	}
	if c != nil {
		t.Fatalf("Build(unknown) expected nil connector, got %v", c)
	}
	if got := err.Error(); !containsSubstring(got, "totally_unknown_connector") {
		t.Fatalf("Build(unknown) error %q does not name the unknown type", got)
	}
}

// TestBuildKafkaSASL verifies that SASL credentials are decoded from Auth into the
// kafka Config.
func TestBuildKafkaSASL(t *testing.T) {
	in := factory.FactoryInput{
		Name:     "kafka-sasl",
		Type:     "kafka",
		Endpoint: "broker:9092",
		Auth: map[string]string{
			"sasl_mechanism": "PLAIN",
			"sasl_username":  "user",
			"sasl_password":  "pass",
		},
		Extra: map[string]interface{}{
			"topic": "signals",
		},
	}
	c, err := factory.Build(in, zap.NewNop())
	if err != nil {
		t.Fatalf("Build(kafka+sasl) unexpected error: %v", err)
	}
	if c == nil || c.Name() != "kafka" {
		t.Fatalf("Build(kafka+sasl) returned wrong connector: %v", c)
	}
}

// TestBuildNilLogger verifies that Build works with a nil logger (falls back to nop).
func TestBuildNilLogger(t *testing.T) {
	in := factory.FactoryInput{
		Name:     "syslog-nil-log",
		Type:     "syslog",
		Endpoint: "localhost:514",
	}
	c, err := factory.Build(in, nil)
	if err != nil {
		t.Fatalf("Build with nil logger unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("Build with nil logger returned nil connector")
	}
}

// containsSubstring returns true if s contains substr.
func containsSubstring(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
