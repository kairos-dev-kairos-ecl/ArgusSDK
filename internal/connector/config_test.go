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
	})
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
