package factory_test

import (
	"testing"

	"go.uber.org/zap"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector/factory"
)

// TestBuildKafka verifies that Build returns a connector whose Name() is the
// configured output name (used for dispatch routing), not the connector type.
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
	if c.Name() != "my-kafka" {
		t.Fatalf("Build(kafka) Name() = %q, want output name %q", c.Name(), "my-kafka")
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
	if c.Name() != "my-splunk" {
		t.Fatalf("Build(splunk_hec) Name() = %q, want output name %q", c.Name(), "my-splunk")
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
	if c.Name() != "my-elastic" {
		t.Fatalf("Build(elastic) Name() = %q, want output name %q", c.Name(), "my-elastic")
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
	if c.Name() != "my-syslog" {
		t.Fatalf("Build(syslog) Name() = %q, want output name %q", c.Name(), "my-syslog")
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
	if c.Name() != "my-xdr" {
		t.Fatalf("Build(argusxdr) Name() = %q, want output name %q", c.Name(), "my-xdr")
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
	if c == nil || c.Name() != "kafka-sasl" {
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

// TestBuild_NameOverridesTypeForRouting is a regression guard: the agent's
// dispatcher routes batches to connectors by the configured output name, and the
// registry keys connectors by Name(). If Build returned a connector named after
// its type instead of the output name, an output whose name differs from its
// type (e.g. "kafka-prod" / "kafka") would silently never receive batches
// ("connector not found"). Build must return Name() == the configured name for
// every type.
func TestBuild_NameOverridesTypeForRouting(t *testing.T) {
	cases := []factory.FactoryInput{
		{Name: "kafka-prod", Type: "kafka", Endpoint: "b:9092", Extra: map[string]interface{}{"topic": "t"}},
		{Name: "siem-1", Type: "splunk_hec", Endpoint: "https://s:8088", Auth: map[string]string{"token": "x"}},
		{Name: "es-east", Type: "elastic", Endpoint: "https://e:9200", Auth: map[string]string{"api_key": "a:b"}},
		{Name: "rsyslog", Type: "syslog", Endpoint: "h:514"},
		{Name: "xdr-primary", Type: "argusxdr", Endpoint: "x:443"},
	}
	for _, in := range cases {
		c, err := factory.Build(in, zap.NewNop())
		if err != nil {
			t.Fatalf("Build(%s) error: %v", in.Type, err)
		}
		if c.Name() != in.Name {
			t.Errorf("Build(%s) Name() = %q, want configured output name %q", in.Type, c.Name(), in.Name)
		}
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
