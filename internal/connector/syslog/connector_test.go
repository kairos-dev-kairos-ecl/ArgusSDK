package syslog

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// ---- helpers ---------------------------------------------------------------

// startTCPListener spins up a local TCP listener and returns its address.
// It accepts one connection, reads lines until EOF, and sends the lines on ch.
func startTCPListener(t *testing.T) (addr string, linesCh <-chan []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ch := make(chan []string, 1)
	go func() {
		defer ln.Close()
		conn, err := ln.Accept()
		if err != nil {
			ch <- nil
			return
		}
		defer conn.Close()
		var lines []string
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}
		ch <- lines
	}()
	return ln.Addr().String(), ch
}

// generateSelfSignedCert returns a self-signed TLS certificate and its PEM-encoded CA.
func generateSelfSignedCert(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-syslog"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privKey.PublicKey, privKey)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(privKey)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return
}

// startTLSListener spins up a local TLS listener. It accepts one connection,
// reads lines until EOF, sends them on the returned channel.
func startTLSListener(t *testing.T, certPEM, keyPEM []byte) (addr string, linesCh <-chan []string) {
	t.Helper()
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("x509 key pair: %v", err)
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatalf("tls listen: %v", err)
	}
	ch := make(chan []string, 1)
	go func() {
		defer ln.Close()
		conn, err := ln.Accept()
		if err != nil {
			ch <- nil
			return
		}
		defer conn.Close()
		var lines []string
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}
		ch <- lines
	}()
	return ln.Addr().String(), ch
}

// sampleSignal returns a Signal with all relevant fields populated for testing.
func sampleSignal() signal.Signal {
	return signal.Signal{
		SignalID:  "01HV000000000000000000TEST",
		TraceID:   "trace-abc",
		SpanID:    "span-001",
		Layer:     signal.L10Application,
		Category:  "agent.tool_call",
		Severity:  signal.SeverityHigh,
		AppID:     "test-app",
		Env:       "test",
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// ---- TestBuildCEF ----------------------------------------------------------

func TestBuildCEF_Format(t *testing.T) {
	s := sampleSignal()
	line := buildCEF(s, "argus-agent")

	// Must start with the CEF header prefix
	if !strings.HasPrefix(line, "CEF:0|Argus|SDK|1.0|") {
		t.Errorf("expected line to start with CEF:0|Argus|SDK|1.0|, got: %q", line)
	}
	// Must contain signal ID
	if !strings.Contains(line, s.SignalID) {
		t.Errorf("CEF line missing SignalID %q: %q", s.SignalID, line)
	}
	// Must contain category
	if !strings.Contains(line, s.Category) {
		t.Errorf("CEF line missing category %q: %q", s.Category, line)
	}
	// Extensions: layer, app_id, trace_id
	if !strings.Contains(line, "layer=10") {
		t.Errorf("CEF line missing layer=10: %q", line)
	}
	if !strings.Contains(line, "app_id=test-app") {
		t.Errorf("CEF line missing app_id=test-app: %q", line)
	}
	if !strings.Contains(line, "trace_id=trace-abc") {
		t.Errorf("CEF line missing trace_id=trace-abc: %q", line)
	}
}

func TestBuildCEF_SeverityMapping(t *testing.T) {
	// Severity map: Info→1, Low→3, Medium→5, High→7, Critical→9
	cases := []struct {
		sev      signal.Severity
		wantSev  string
	}{
		{signal.SeverityInfo, "|1|"},
		{signal.SeverityLow, "|3|"},
		{signal.SeverityMedium, "|5|"},
		{signal.SeverityHigh, "|7|"},
		{signal.SeverityCritical, "|9|"},
		{signal.SeverityUnspecified, "|0|"},
	}
	for _, tc := range cases {
		s := sampleSignal()
		s.Severity = tc.sev
		line := buildCEF(s, "argus-agent")
		if !strings.Contains(line, tc.wantSev) {
			t.Errorf("severity %v: expected %q in line %q", tc.sev, tc.wantSev, line)
		}
	}
}

func TestBuildCEF_NoPipeInjection(t *testing.T) {
	// A signal with pipe characters in category must not break CEF header parsing.
	s := sampleSignal()
	s.Category = "bad|category|injection"
	line := buildCEF(s, "argus-agent")
	// The entire CEF header prefix must still be present
	if !strings.HasPrefix(line, "CEF:0|Argus|SDK|1.0|") {
		t.Errorf("CEF prefix broken by injection: %q", line)
	}
	// The raw pipe-laden category must not appear verbatim in the header fields
	// (it should be escaped or scrubbed — we verify the line has exactly 7 pipes
	// in the CEF header portion before the extension separator)
	headerEnd := strings.Index(line, "| ")
	if headerEnd < 0 {
		headerEnd = len(line)
	}
	header := line[:headerEnd]
	pipeCount := strings.Count(header, "|")
	// A valid 7-field CEF header (CEF:0|V|P|V|ID|CAT|SEV|) has exactly 7 pipes.
	if pipeCount != 7 {
		t.Errorf("CEF header pipe count = %d, want 7: header=%q", pipeCount, header)
	}
}

// ---- TestConnect -----------------------------------------------------------

func TestConnect_EmptyServer(t *testing.T) {
	c := New(Config{Server: "", Transport: TransportTCP})
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error for empty server, got nil")
	}
}

func TestConnect_TCP(t *testing.T) {
	addr, _ := startTCPListener(t)

	c := New(Config{Server: addr, Transport: TransportTCP})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect TCP: %v", err)
	}
	defer c.Close()

	if c.conn == nil {
		t.Fatal("expected c.conn to be non-nil after successful TCP connect")
	}
}

func TestConnect_TLS(t *testing.T) {
	certPEM, keyPEM := generateSelfSignedCert(t)
	addr, _ := startTLSListener(t, certPEM, keyPEM)

	// Write cert/key to temp files for NewTLSConfig
	certFile := t.TempDir() + "/cert.pem"
	keyFile := t.TempDir() + "/key.pem"
	caFile := t.TempDir() + "/ca.pem"

	mustWriteFile(t, certFile, certPEM)
	mustWriteFile(t, keyFile, keyPEM)
	mustWriteFile(t, caFile, certPEM) // self-signed: cert is also the CA

	c := New(Config{
		Server:    addr,
		Transport: TransportTLS,
		TLS: TLSConfig{
			CACert:   caFile,
			CertFile: certFile,
			KeyFile:  keyFile,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect TLS: %v", err)
	}
	defer c.Close()

	if c.conn == nil {
		t.Fatal("expected c.conn to be non-nil after successful TLS connect")
	}
}

// ---- TestSend --------------------------------------------------------------

func TestSend_DeliversCEFLines(t *testing.T) {
	addr, linesCh := startTCPListener(t)

	c := New(Config{Server: addr, Transport: TransportTCP})
	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}

	batch := &connector.SignalBatch{
		BatchID: "batch-001",
		Signals: []signal.Signal{
			sampleSignal(),
			func() signal.Signal {
				s := sampleSignal()
				s.SignalID = "SIG-002"
				s.Severity = signal.SeverityMedium
				return s
			}(),
			func() signal.Signal {
				s := sampleSignal()
				s.SignalID = "SIG-003"
				s.Severity = signal.SeverityCritical
				return s
			}(),
		},
	}

	ack, err := c.Send(ctx, batch)
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	// Close to signal EOF to the listener
	c.Close()

	if ack.Status != "delivered" {
		t.Errorf("expected status=delivered, got %q", ack.Status)
	}
	if ack.BatchID != "batch-001" {
		t.Errorf("expected BatchID=batch-001, got %q", ack.BatchID)
	}

	lines := <-linesCh
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, "CEF:0|Argus|SDK|") {
			t.Errorf("line[%d] does not start with CEF:0|Argus|SDK|: %q", i, line)
		}
	}
}

func TestSend_NilConnReturnsFailedAck(t *testing.T) {
	c := New(Config{Server: "127.0.0.1:0", Transport: TransportTCP})
	// Do NOT call Connect — c.conn is nil

	batch := &connector.SignalBatch{
		BatchID: "batch-fail",
		Signals: []signal.Signal{sampleSignal()},
	}

	ack, err := c.Send(context.Background(), batch)
	if err == nil {
		t.Fatal("expected non-nil error for nil conn, got nil")
	}
	if ack == nil {
		t.Fatal("expected non-nil ack even on failure")
	}
	if ack.Status != "failed" {
		t.Errorf("expected status=failed, got %q", ack.Status)
	}
}

func TestSend_WriteErrorReturnsFailedAck(t *testing.T) {
	addr, _ := startTCPListener(t)

	c := New(Config{Server: addr, Transport: TransportTCP})
	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Close the connection to cause a write error
	c.conn.Close()

	batch := &connector.SignalBatch{
		BatchID: "batch-writefail",
		Signals: []signal.Signal{sampleSignal()},
	}

	ack, err := c.Send(ctx, batch)
	if err == nil {
		t.Fatal("expected non-nil error after write to closed conn")
	}
	if ack == nil {
		t.Fatal("expected non-nil ack even on write failure")
	}
	if ack.Status != "failed" {
		t.Errorf("expected status=failed, got %q", ack.Status)
	}
}

func TestSend_EmptyBatch(t *testing.T) {
	addr, linesCh := startTCPListener(t)

	c := New(Config{Server: addr, Transport: TransportTCP})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}

	ack, err := c.Send(context.Background(), &connector.SignalBatch{
		BatchID: "empty",
		Signals: []signal.Signal{},
	})
	c.Close()

	if err != nil {
		t.Fatalf("empty batch should not error: %v", err)
	}
	if ack.Status != "delivered" {
		t.Errorf("expected delivered, got %q", ack.Status)
	}

	lines := <-linesCh
	if len(lines) != 0 {
		t.Errorf("expected 0 lines for empty batch, got %d", len(lines))
	}
}

// ---- helpers ---------------------------------------------------------------

func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := writeFile(path, data); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
