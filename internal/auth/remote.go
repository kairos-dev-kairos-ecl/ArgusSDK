// Package auth — remote XDR registration client.
//
// This file implements the real HTTP client for the XDR Mode-1 registration
// and credential-refresh endpoints, plus the typed errors the client surfaces.
//
// Documented XDR endpoints (see auth.go package doc):
//
//	POST /api/v1/sdk/register          body: RegistrationRequest JSON → RegistrationResponse JSON
//	POST /api/v1/sdk/credential/refresh body: {GroupID,InstanceID,Credential} JSON → {Credential} JSON
//
// Transport: stdlib net/http only; TLS 1.3 floor via connector.NewTLSConfig.
// No new module is introduced.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
)

// ─── Typed errors ─────────────────────────────────────────────────────────────

// ErrInstallTokenConsumed is returned when the XDR endpoint signals that the
// install token has already been used (HTTP 409 Conflict or 401 Unauthorized on
// the register path). The caller must not overwrite a good persisted InstanceID
// on receiving this error.
var ErrInstallTokenConsumed = errors.New("auth: install token already consumed")

// httpStatusError is returned when the XDR endpoint responds with a non-2xx
// status code that is not otherwise mapped to a sentinel error.
type httpStatusError struct {
	Path   string
	Status int
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("auth: XDR %s returned HTTP %d", e.Path, e.Status)
}

// ─── Wire structs ─────────────────────────────────────────────────────────────
// auth.RegistrationRequest / auth.RegistrationResponse lack json struct tags,
// so we define private wire structs that mirror them and add the tags explicitly.
// The constructors convert between the public and private shapes field-by-field.

type registerRequest struct {
	GroupID      string `json:"group_id"`
	InstanceName string `json:"instance_name"`
	InstallToken string `json:"install_token"`
	AgentVersion string `json:"agent_version"`
	Hostname     string `json:"hostname"`
	Platform     string `json:"platform"`
}

type registerResponse struct {
	InstanceID string `json:"instance_id"`
	Credential string `json:"credential"`
}

type refreshRequest struct {
	GroupID    string `json:"group_id"`
	InstanceID string `json:"instance_id"`
	Credential string `json:"credential"`
}

type refreshResponse struct {
	Credential string `json:"credential"`
}

// ─── remoteRegistrar ──────────────────────────────────────────────────────────

type remoteRegistrar struct {
	baseURL string
	client  *http.Client
}

// NewRemoteRegistrar returns an auth.Registrar that POSTs to baseURL over the
// supplied httpClient. When httpClient is nil a default is constructed whose
// Transport uses connector.NewTLSConfig (TLS 1.3 floor) and a 30-second timeout.
func NewRemoteRegistrar(baseURL string, httpClient *http.Client) Registrar {
	if httpClient == nil {
		httpClient = defaultHTTPClient()
	}
	return &remoteRegistrar{baseURL: baseURL, client: httpClient}
}

func (r *remoteRegistrar) Register(ctx context.Context, req RegistrationRequest) (*RegistrationResponse, error) {
	wireReq := registerRequest{
		GroupID:      req.GroupID,
		InstanceName: req.InstanceName,
		InstallToken: req.InstallToken,
		AgentVersion: req.AgentVersion,
		Hostname:     req.Hostname,
		Platform:     req.Platform,
	}
	var wireResp registerResponse
	path := "/api/v1/sdk/register"
	status, err := postJSON(ctx, r.client, r.baseURL+path, wireReq, &wireResp)
	if err != nil {
		return nil, fmt.Errorf("auth: register transport error: %w", err)
	}
	if status == http.StatusConflict || status == http.StatusUnauthorized {
		return nil, ErrInstallTokenConsumed
	}
	if status < 200 || status >= 300 {
		return nil, &httpStatusError{Path: path, Status: status}
	}
	return &RegistrationResponse{
		InstanceID: wireResp.InstanceID,
		Credential: wireResp.Credential,
	}, nil
}

// ─── remoteCredentialRefresher ────────────────────────────────────────────────

type remoteCredentialRefresher struct {
	baseURL string
	client  *http.Client
}

// NewRemoteCredentialRefresher returns an auth.CredentialRefresher that POSTs
// to baseURL over the supplied httpClient. A nil httpClient gets the TLS 1.3
// default built by defaultHTTPClient (same as NewRemoteRegistrar).
func NewRemoteCredentialRefresher(baseURL string, httpClient *http.Client) CredentialRefresher {
	if httpClient == nil {
		httpClient = defaultHTTPClient()
	}
	return &remoteCredentialRefresher{baseURL: baseURL, client: httpClient}
}

func (r *remoteCredentialRefresher) Refresh(ctx context.Context, id Identity) (string, error) {
	wireReq := refreshRequest{
		GroupID:    id.GroupID,
		InstanceID: id.InstanceID,
		Credential: id.Credential,
	}
	var wireResp refreshResponse
	path := "/api/v1/sdk/credential/refresh"
	status, err := postJSON(ctx, r.client, r.baseURL+path, wireReq, &wireResp)
	if err != nil {
		return "", fmt.Errorf("auth: refresh transport error: %w", err)
	}
	if status < 200 || status >= 300 {
		return "", &httpStatusError{Path: path, Status: status}
	}
	return wireResp.Credential, nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// postJSON marshals body to JSON, POSTs it to url with ctx, defers body close,
// and on 2xx decodes the response into out (when out != nil). It returns the
// HTTP status code so the caller can map specific codes to typed errors.
// A transport-level error returns (0, err).
func postJSON(ctx context.Context, client *http.Client, url string, body any, out any) (int, error) {
	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(body); err != nil {
		return 0, fmt.Errorf("json encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, buf)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if out != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if decErr := json.NewDecoder(resp.Body).Decode(out); decErr != nil {
			return resp.StatusCode, fmt.Errorf("json decode: %w", decErr)
		}
	}
	return resp.StatusCode, nil
}

// defaultHTTPClient builds an *http.Client whose Transport enforces TLS 1.3
// via connector.NewTLSConfig with system roots.
func defaultHTTPClient() *http.Client {
	tlsCfg, err := connector.NewTLSConfig(connector.TLSClientConfig{})
	if err != nil {
		// NewTLSConfig with an empty config only fails if the system cert pool is
		// unavailable — extremely unlikely and non-recoverable. Panic is acceptable
		// here as it indicates a broken runtime environment, not a user error.
		panic(fmt.Sprintf("auth: failed to build default TLS config: %v", err))
	}
	tr := &http.Transport{TLSClientConfig: tlsCfg}
	return &http.Client{Transport: tr, Timeout: 30 * time.Second}
}
