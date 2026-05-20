// Package syslog implements a syslog output connector that emits signals in
// CEF (Common Event Format) over RFC 5424 syslog. Adapted from the XDR
// internal/notify/adapters/syslog.go notifier; domain type updated from
// NotificationRequest to connector.SignalBatch.
package syslog

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
)

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
//   CEF:0|Argus|SDK|1.0|<signal_id>|<category>|<cef_severity>|layer=<layer> app_id=<app_id> trace_id=<trace_id>
//
// Implementation TODO (not in this scaffold):
//   - github.com/RackSec/srslog (same library as XDR) or stdlib net.Dial for plain TCP/UDP
//   - One CEF message per signal (syslog is not a batch protocol)
//   - TLS dial using crypto/tls when Transport == TransportTLS
//   - Reconnect on write error (TCP/TLS connections can drop)
type Connector struct {
	cfg  Config
	conn net.Conn // TCP or UDP connection; nil for lazy-dial UDP
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

// Connect dials the syslog server.
func (c *Connector) Connect(_ context.Context) error {
	if c.cfg.Server == "" {
		return fmt.Errorf("syslog: server address is required")
	}
	// TODO: net.Dial or tls.Dial based on c.cfg.Transport, store in c.conn
	return nil
}

// Send formats each signal in the batch as a CEF syslog message and writes it.
func (c *Connector) Send(_ context.Context, batch *connector.SignalBatch) (*connector.DeliveryAck, error) {
	// TODO: iterate batch.Signals, build CEF string, write via srslog or c.conn
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
