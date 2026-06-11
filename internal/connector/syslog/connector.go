// Package syslog implements a syslog output connector that emits signals in
// CEF (Common Event Format) over RFC 5424 syslog. Adapted from the XDR
// internal/notify/adapters/syslog.go notifier; domain type updated from
// NotificationRequest to connector.SignalBatch.
package syslog

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// serverHostname extracts the host portion from a host:port address string.
// Returns the full address if it cannot be parsed.
func serverHostname(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// Transport is the network transport used to reach the syslog server.
type Transport string

const (
	TransportUDP Transport = "udp"
	TransportTCP Transport = "tcp"
	TransportTLS Transport = "tls" // TCP + TLS
)

// Config holds syslog destination parameters.
type Config struct {
	// Server is the syslog endpoint in host:port form (e.g. "siem.example.com:514").
	Server string

	// Transport is "udp" | "tcp" | "tls". Default: "udp".
	Transport Transport

	// Facility is the RFC 5424 facility code (0–23). Default: 16 (LOCAL0).
	Facility int

	// AppName is the SYSLOG-MSG APP-NAME field. Default: "argus-agent".
	AppName string

	// TLS configures transport security when Transport is "tls".
	TLS TLSConfig
}

// TLSConfig controls TLS for the syslog TCP connection.
type TLSConfig struct {
	CACert   string
	CertFile string
	KeyFile  string
}

// Connector delivers per-signal CEF messages to a syslog server.
//
// CEF format per signal:
//
//	CEF:0|Argus|SDK|1.0|<signal_id>|<category>|<cef_severity>|layer=<layer> app_id=<app_id> trace_id=<trace_id>
//
// Severity mapping (Signal Severity → CEF severity integer 0–10):
//
//	Unspecified → 0, Info → 1, Low → 3, Medium → 5, High → 7, Critical → 9
//
// TLS uses connector.NewTLSConfig (TLS 1.3 minimum; no direct tls.Config construction).
type Connector struct {
	cfg  Config
	conn net.Conn // TCP or TLS connection; nil for lazy-dial UDP
}

// New creates a syslog connector with the given config. Call Connect before sending.
func New(cfg Config) *Connector {
	if cfg.Transport == "" {
		cfg.Transport = TransportUDP
	}
	if cfg.AppName == "" {
		cfg.AppName = "argus-agent"
	}
	if cfg.Facility == 0 {
		cfg.Facility = 16 // LOCAL0
	}
	return &Connector{cfg: cfg}
}

// Name implements connector.Connector.
func (c *Connector) Name() string { return "syslog" }

// dialTimeout is the default dial timeout for TCP/TLS connections.
const dialTimeout = 10 * time.Second

// Connect dials the syslog server and stores the connection in c.conn.
//
//   - TransportTCP: dials via net.DialTimeout("tcp", server, timeout).
//   - TransportTLS: builds *tls.Config via connector.NewTLSConfig (guaranteeing TLS 1.3),
//     then dials with a net.Dialer and tls.Client. A direct tls.Config{} literal is
//     never constructed here (locked decision 4 / threat T-04-04).
//   - TransportUDP: dials via net.Dial("udp", server).
func (c *Connector) Connect(_ context.Context) error {
	if c.cfg.Server == "" {
		return fmt.Errorf("syslog: server address is required")
	}

	switch c.cfg.Transport {
	case TransportTCP:
		conn, err := net.DialTimeout("tcp", c.cfg.Server, dialTimeout)
		if err != nil {
			return fmt.Errorf("syslog: TCP dial %s: %w", c.cfg.Server, err)
		}
		c.conn = conn

	case TransportTLS:
		// Build TLS config via the project helper — never tls.Config{} directly.
		tlsCfg, err := connector.NewTLSConfig(connector.TLSClientConfig{
			CACert:   c.cfg.TLS.CACert,
			CertFile: c.cfg.TLS.CertFile,
			KeyFile:  c.cfg.TLS.KeyFile,
		})
		if err != nil {
			return fmt.Errorf("syslog: build TLS config: %w", err)
		}
		// Set ServerName from the server address so TLS verification has a
		// hostname to validate against. This is required by Go's TLS client
		// when dialing by IP address; it is NOT InsecureSkipVerify (T-04-04).
		if tlsCfg.ServerName == "" {
			tlsCfg.ServerName = serverHostname(c.cfg.Server)
		}
		dialer := &net.Dialer{Timeout: dialTimeout}
		rawConn, err := dialer.Dial("tcp", c.cfg.Server)
		if err != nil {
			return fmt.Errorf("syslog: TCP dial for TLS %s: %w", c.cfg.Server, err)
		}
		tlsConn := tls.Client(rawConn, tlsCfg)
		if err := tlsConn.Handshake(); err != nil {
			rawConn.Close()
			return fmt.Errorf("syslog: TLS handshake %s: %w", c.cfg.Server, err)
		}
		c.conn = tlsConn

	default: // TransportUDP and any unrecognised transport
		conn, err := net.Dial("udp", c.cfg.Server)
		if err != nil {
			return fmt.Errorf("syslog: UDP dial %s: %w", c.cfg.Server, err)
		}
		c.conn = conn
	}

	return nil
}

// Send formats each signal in the batch as a CEF syslog message and writes it
// to the established connection. Each message is newline-terminated.
//
// Delivery contract (locked decision 9 / threat T-04-06):
//   - If c.conn is nil (Connect not called or failed), returns (failed ack, non-nil error).
//   - On the first write error, aborts immediately and returns (failed ack, non-nil error).
//   - On full success, returns (delivered ack with echoed BatchID, nil error).
//
// UseOCSF is ignored — syslog emits CEF directly from signal fields.
func (c *Connector) Send(_ context.Context, batch *connector.SignalBatch) (*connector.DeliveryAck, error) {
	if c.conn == nil {
		return &connector.DeliveryAck{
			BatchID:   batch.BatchID,
			Status:    "failed",
			Error:     "syslog: not connected (call Connect first)",
			Timestamp: time.Now(),
		}, fmt.Errorf("syslog: not connected — call Connect before Send")
	}

	for i, s := range batch.Signals {
		msg := buildCEF(s, c.cfg.AppName) + "\n"
		if _, err := fmt.Fprint(c.conn, msg); err != nil {
			return &connector.DeliveryAck{
				BatchID:   batch.BatchID,
				Status:    "failed",
				Error:     fmt.Sprintf("syslog: write error at signal %d: %v", i, err),
				Timestamp: time.Now(),
			}, fmt.Errorf("syslog: write error at signal %d/%d: %w", i, len(batch.Signals), err)
		}
	}

	return &connector.DeliveryAck{
		BatchID:   batch.BatchID,
		Status:    "delivered",
		Timestamp: time.Now(),
	}, nil
}

// Health checks TCP connectivity to the syslog server.
func (c *Connector) Health(_ context.Context) error {
	if c.cfg.Server == "" {
		return fmt.Errorf("syslog: not configured")
	}
	network := string(c.cfg.Transport)
	if c.cfg.Transport == TransportTLS {
		network = "tcp"
	}
	conn, err := net.DialTimeout(network, c.cfg.Server, 5*time.Second)
	if err != nil {
		return fmt.Errorf("syslog: server unreachable: %w", err)
	}
	conn.Close()
	return nil
}

// Close closes the persistent connection (TCP/TLS only).
func (c *Connector) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// cefSeverity maps signal.Severity to a CEF severity integer (0–10 band).
//
// Mapping:
//
//	Unspecified → 0
//	Info        → 1
//	Low         → 3
//	Medium      → 5
//	High        → 7
//	Critical    → 9
func cefSeverity(sev signal.Severity) int {
	switch sev {
	case signal.SeverityInfo:
		return 1
	case signal.SeverityLow:
		return 3
	case signal.SeverityMedium:
		return 5
	case signal.SeverityHigh:
		return 7
	case signal.SeverityCritical:
		return 9
	default: // SeverityUnspecified or unknown
		return 0
	}
}

// sanitizeCEFField replaces characters that would break CEF header field parsing.
// The CEF header uses '|' as field separator; '=' separates extension keys/values.
// Newlines are stripped to prevent log injection (threat T-04-05).
func sanitizeCEFField(s string) string {
	// Replace pipe characters with a safe substitute to preserve the 7-field structure.
	s = strings.ReplaceAll(s, "|", "/")
	// Strip all newline variants to prevent CEF record injection.
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// sanitizeCEFExtValue replaces characters that would break CEF extension parsing.
// Extension values must not contain unescaped '=' or '\n'.
func sanitizeCEFExtValue(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "=", "_")
	return s
}

// buildCEF formats a signal.Signal as a single CEF line (without the trailing newline).
//
// Output format:
//
//	CEF:0|Argus|SDK|1.0|<SignalID>|<Category>|<cefSeverity>|layer=<int> app_id=<AppID> trace_id=<TraceID>
//
// Field values are sanitised to prevent CEF header injection (pipe/newline characters).
// Extension key=value pairs use sanitised values (threat T-04-05).
func buildCEF(s signal.Signal, appName string) string {
	return fmt.Sprintf("CEF:0|Argus|SDK|1.0|%s|%s|%d|layer=%d app_id=%s trace_id=%s",
		sanitizeCEFField(s.SignalID),
		sanitizeCEFField(s.Category),
		cefSeverity(s.Severity),
		int(s.Layer),
		sanitizeCEFExtValue(s.AppID),
		sanitizeCEFExtValue(s.TraceID),
	)
}

// writeFile writes data to path (used by tests for cert files).
func writeFile(path string, data []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}
