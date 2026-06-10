package connector

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// TLS tests
// ---------------------------------------------------------------------------

func TestNewTLSConfig_DefaultsTLS13(t *testing.T) {
	cfg, err := NewTLSConfig(TLSClientConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Errorf("expected MinVersion TLS 1.3 (%d), got %d", tls.VersionTLS13, cfg.MinVersion)
	}
}

func TestNewTLSConfig_NoInsecureSkipVerify(t *testing.T) {
	cfg, err := NewTLSConfig(TLSClientConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify must never be set to true by NewTLSConfig")
	}
}

func TestNewTLSConfig_CustomCA(t *testing.T) {
	// Generate a self-signed CA certificate.
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}

	// Write CA PEM to a temp file.
	f, err := os.CreateTemp("", "test-ca-*.pem")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(f.Name())
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: caDER}); err != nil {
		t.Fatalf("encode PEM: %v", err)
	}
	f.Close()

	cfg, err := NewTLSConfig(TLSClientConfig{CACert: f.Name()})
	if err != nil {
		t.Fatalf("NewTLSConfig with custom CA: %v", err)
	}
	if cfg.RootCAs == nil {
		t.Error("expected non-nil RootCAs when CACert is provided")
	}
}

func TestNewTLSConfig_BadCA(t *testing.T) {
	// Write invalid PEM content.
	f, err := os.CreateTemp("", "bad-ca-*.pem")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(f.Name())
	f.WriteString("this is not a valid PEM certificate")
	f.Close()

	_, err = NewTLSConfig(TLSClientConfig{CACert: f.Name()})
	if err == nil {
		t.Error("expected error for invalid CA PEM, got nil")
	}
}

func TestNewTLSConfig_mTLS(t *testing.T) {
	// Generate a self-signed cert + key for mTLS.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "Test Client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	certFile, err := os.CreateTemp("", "test-cert-*.pem")
	if err != nil {
		t.Fatalf("create cert file: %v", err)
	}
	defer os.Remove(certFile.Name())
	pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	certFile.Close()

	keyFile, err := os.CreateTemp("", "test-key-*.pem")
	if err != nil {
		t.Fatalf("create key file: %v", err)
	}
	defer os.Remove(keyFile.Name())
	pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	keyFile.Close()

	cfg, err := NewTLSConfig(TLSClientConfig{
		CertFile: certFile.Name(),
		KeyFile:  keyFile.Name(),
	})
	if err != nil {
		t.Fatalf("NewTLSConfig mTLS: %v", err)
	}
	if len(cfg.Certificates) != 1 {
		t.Errorf("expected 1 certificate for mTLS, got %d", len(cfg.Certificates))
	}
}

// ---------------------------------------------------------------------------
// Config tests
// ---------------------------------------------------------------------------

func TestLoadConnectorsConfig_Valid(t *testing.T) {
	yamlContent := `connectors:
  - enabled: true
    type: kafka
    settings:
      brokers:
        - "localhost:9092"
      topic: "signals"
`
	f, err := os.CreateTemp("", "connectors-*.yaml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(f.Name())
	f.WriteString(yamlContent)
	f.Close()

	cfg, err := LoadConnectorsConfig(f.Name())
	if err != nil {
		t.Fatalf("LoadConnectorsConfig: %v", err)
	}
	if len(cfg.Connectors) != 1 {
		t.Fatalf("expected 1 connector, got %d", len(cfg.Connectors))
	}
	c := cfg.Connectors[0]
	if c.Type != "kafka" {
		t.Errorf("expected type 'kafka', got %q", c.Type)
	}
	if !c.Enabled {
		t.Error("expected Enabled=true")
	}
}

func TestLoadConnectorsConfig_Missing(t *testing.T) {
	_, err := LoadConnectorsConfig("/tmp/definitely-does-not-exist-argus-test.yaml")
	if err == nil {
		t.Error("expected error for missing config file, got nil")
	}
}

func TestWatcher_CallsOnChange(t *testing.T) {
	yamlContent := `connectors:
  - enabled: true
    type: splunk_hec
    settings:
      endpoint: "https://splunk.example.com:8088"
`
	f, err := os.CreateTemp("", "watch-*.yaml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(f.Name())
	f.WriteString(yamlContent)
	f.Close()

	called := make(chan *ConnectorsFileConfig, 1)
	w, err := NewWatcher(f.Name(), func(cfg *ConnectorsFileConfig) {
		select {
		case called <- cfg:
		default:
		}
	}, nil) // nil logger → zap.NewNop()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)

	// Give the watcher time to start.
	time.Sleep(100 * time.Millisecond)

	// Modify the file to trigger the watcher.
	updatedYAML := `connectors:
  - enabled: true
    type: kafka
    settings:
      brokers:
        - "broker:9092"
`
	if err := os.WriteFile(f.Name(), []byte(updatedYAML), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	select {
	case cfg := <-called:
		if cfg == nil {
			t.Error("onChange called with nil config")
		}
	case <-time.After(2 * time.Second):
		t.Error("onChange was not called within 2 seconds after file write")
	}
}

// ---------------------------------------------------------------------------
// F9: atomic-rename watcher tests (new — RED phase)
// ---------------------------------------------------------------------------

// TestWatcher_AtomicRenameTriggersReload (F9): an atomic-rename save (write temp +
// rename over target) must trigger onChange with the new config.
func TestWatcher_AtomicRenameTriggersReload(t *testing.T) {
	initialYAML := `connectors:
  - enabled: true
    type: splunk_hec
    settings:
      endpoint: "https://splunk.example.com:8088"
`
	// Create the initial target file.
	target, err := os.CreateTemp("", "watch-target-*.yaml")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	targetPath := target.Name()
	target.WriteString(initialYAML)
	target.Close()
	defer os.Remove(targetPath)

	called := make(chan *ConnectorsFileConfig, 1)
	w, err := NewWatcher(targetPath, func(cfg *ConnectorsFileConfig) {
		select {
		case called <- cfg:
		default:
		}
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)

	// Give the watcher time to start.
	time.Sleep(150 * time.Millisecond)

	// Atomic-rename save: write new YAML to sibling temp, Close(), then Rename over target.
	updatedYAML := `connectors:
  - enabled: true
    type: kafka
    settings:
      brokers:
        - "broker:9092"
`
	tmp, err := os.CreateTemp("", "watch-tmp-*.yaml")
	if err != nil {
		t.Fatalf("create tmp: %v", err)
	}
	tmpPath := tmp.Name()
	tmp.WriteString(updatedYAML)
	tmp.Close() // Windows: must close before rename
	defer os.Remove(tmpPath)

	if err := os.Rename(tmpPath, targetPath); err != nil {
		t.Fatalf("rename: %v", err)
	}

	select {
	case cfg := <-called:
		if cfg == nil {
			t.Fatal("onChange called with nil config after atomic rename")
		}
		if len(cfg.Connectors) == 0 || cfg.Connectors[0].Type != "kafka" {
			t.Errorf("expected kafka connector after reload, got %+v", cfg)
		}
	case <-time.After(3 * time.Second):
		t.Error("onChange was not called within 3 seconds after atomic rename")
	}
}

// TestWatcher_IgnoresSiblingFiles (F9): writing a different file in the same
// directory must NOT trigger onChange.
func TestWatcher_IgnoresSiblingFiles(t *testing.T) {
	initialYAML := `connectors:
  - enabled: true
    type: splunk_hec
    settings:
      endpoint: "https://splunk.example.com:8088"
`
	target, err := os.CreateTemp("", "watch-main-*.yaml")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	targetPath := target.Name()
	target.WriteString(initialYAML)
	target.Close()
	defer os.Remove(targetPath)

	called := make(chan struct{}, 1)
	w, err := NewWatcher(targetPath, func(_ *ConnectorsFileConfig) {
		select {
		case called <- struct{}{}:
		default:
		}
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)

	time.Sleep(150 * time.Millisecond)

	// Write a different file in the same directory.
	sibling, err := os.CreateTemp("", "sibling-*.yaml")
	if err != nil {
		t.Fatalf("create sibling: %v", err)
	}
	sibling.WriteString("connectors: []")
	sibling.Close()
	defer os.Remove(sibling.Name())

	// Wait briefly — onChange must NOT be called.
	select {
	case <-called:
		t.Error("onChange was called for a sibling file (must be ignored)")
	case <-time.After(500 * time.Millisecond):
		// Correct: no onChange for unrelated files.
	}
}

// TestWatcher_MalformedYAMLKeepsPrevious (F9/T-02-03 regression): overwriting the
// target with malformed YAML must not call onChange; a subsequent valid write fires.
func TestWatcher_MalformedYAMLKeepsPrevious(t *testing.T) {
	validYAML := `connectors:
  - enabled: true
    type: splunk_hec
    settings:
      endpoint: "https://splunk.example.com:8088"
`
	target, err := os.CreateTemp("", "watch-malformed-*.yaml")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	targetPath := target.Name()
	target.WriteString(validYAML)
	target.Close()
	defer os.Remove(targetPath)

	called := make(chan *ConnectorsFileConfig, 2)
	w, err := NewWatcher(targetPath, func(cfg *ConnectorsFileConfig) {
		select {
		case called <- cfg:
		default:
		}
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)

	time.Sleep(150 * time.Millisecond)

	// Write malformed YAML (unclosed brace — genuinely invalid) — onChange must NOT fire.
	if err := os.WriteFile(targetPath, []byte("connectors:\n  - {invalid"), 0o600); err != nil {
		t.Fatalf("write malformed: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if len(called) != 0 {
		t.Error("onChange was called after malformed YAML write — must not be")
	}

	// Write valid YAML — onChange must fire.
	updatedYAML := `connectors:
  - enabled: true
    type: kafka
    settings:
      brokers:
        - "kafka:9092"
`
	if err := os.WriteFile(targetPath, []byte(updatedYAML), 0o600); err != nil {
		t.Fatalf("write valid: %v", err)
	}
	select {
	case cfg := <-called:
		if cfg == nil || len(cfg.Connectors) == 0 || cfg.Connectors[0].Type != "kafka" {
			t.Errorf("expected kafka connector after valid reload, got %+v", cfg)
		}
	case <-time.After(2 * time.Second):
		t.Error("onChange not called after valid YAML write following malformed write")
	}
}
