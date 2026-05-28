package connector

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// TLSClientConfig holds the parameters for building a *tls.Config.
// All connectors should call NewTLSConfig with their TLS settings instead of
// constructing a tls.Config directly; this guarantees TLS 1.3 minimum and
// prevents InsecureSkipVerify from being set.
type TLSClientConfig struct {
	// CACert is the path to a PEM-encoded CA certificate file.
	// When non-empty the returned config uses this CA as the root of trust
	// instead of the system pool. An empty string uses system roots.
	CACert string

	// CertFile is the path to a PEM-encoded client certificate for mTLS.
	// Both CertFile and KeyFile must be non-empty to enable mTLS.
	CertFile string

	// KeyFile is the path to a PEM-encoded private key for mTLS.
	// Both CertFile and KeyFile must be non-empty to enable mTLS.
	KeyFile string
}

// NewTLSConfig builds a *tls.Config from cfg.
//
// Guarantees:
//   - MinVersion is always tls.VersionTLS13.
//   - InsecureSkipVerify is never set (left as false zero value).
//   - Custom CA: loads PEM from CACert, errors if PEM cannot be parsed.
//   - mTLS: loads CertFile+KeyFile when both are provided.
func NewTLSConfig(cfg TLSClientConfig) (*tls.Config, error) {
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		// InsecureSkipVerify is deliberately left at its zero value (false).
	}

	if cfg.CACert != "" {
		pemData, err := os.ReadFile(cfg.CACert)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA cert file %s: %w", cfg.CACert, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemData) {
			return nil, fmt.Errorf("failed to parse CA cert from %s", cfg.CACert)
		}
		tlsCfg.RootCAs = pool
	}

	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load mTLS key pair (%s, %s): %w", cfg.CertFile, cfg.KeyFile, err)
		}
		tlsCfg.Certificates = append(tlsCfg.Certificates, cert)
	}

	return tlsCfg, nil
}
