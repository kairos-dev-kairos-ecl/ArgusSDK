// Package auth — identity persistence.
//
// This file provides encrypted load/save of Identity (GroupID + InstanceID +
// Credential) to the agent-state.json file via secrets.Store (AES-256-GCM).
// The on-disk file is never written in plaintext.
//
// The conventional state file path is the auth.StateFile constant ("agent-state.json").
// Callers pass a *secrets.Store already opened against that path (or any test path).
//
// Write-then-rename atomicity:
//
//	secrets.Store.SaveSecrets uses O_CREATE|O_WRONLY|O_TRUNC on a .tmp file,
//	then os.Rename to the final path. This is atomic on POSIX and best-effort
//	on Windows — do NOT claim hard Windows atomicity.
package auth

import (
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/secrets"
)

// Fixed map keys used inside the encrypted agent-state.json.
const (
	keyGroupID    = "group_id"
	keyInstanceID = "instance_id"
	keyCredential = "credential"
)

// LoadIdentity reads the encrypted Identity from the secrets store.
//
// Returns (Identity, true, nil) when a non-empty InstanceID is present.
// Returns (zero, false, nil) when the state file is absent or has no InstanceID.
// Any decryption or format error is propagated as a non-nil error.
func LoadIdentity(store *secrets.Store) (Identity, bool, error) {
	m, err := store.LoadSecrets()
	if err != nil {
		return Identity{}, false, err
	}
	id := Identity{
		GroupID:    m[keyGroupID],
		InstanceID: m[keyInstanceID],
		Credential: m[keyCredential],
	}
	if id.InstanceID == "" {
		return Identity{}, false, nil
	}
	return id, true, nil
}

// SaveIdentity persists the Identity to the secrets store (encrypted).
//
// It starts from the existing map (so unrelated keys survive), overlays the
// three Identity fields (only non-empty values are set to avoid blanking a
// field with an empty string), and calls SaveSecrets to write the whole map.
//
// The write uses secrets.Store's write-then-rename path: atomic on POSIX,
// best-effort on Windows.
func SaveIdentity(store *secrets.Store, id Identity) error {
	m, err := store.LoadSecrets()
	if err != nil {
		return err
	}
	if id.GroupID != "" {
		m[keyGroupID] = id.GroupID
	}
	if id.InstanceID != "" {
		m[keyInstanceID] = id.InstanceID
	}
	if id.Credential != "" {
		m[keyCredential] = id.Credential
	}
	return store.SaveSecrets(m)
}

// ReplaceCredential overwrites only the Credential entry in the encrypted store,
// leaving InstanceID and GroupID unchanged.
//
// It loads the current map, sets the credential key, then calls SaveSecrets —
// reusing secrets.Store's write-then-rename path (atomic on POSIX, best-effort
// on Windows). No additional atomic logic is needed.
func ReplaceCredential(store *secrets.Store, newCredential string) error {
	m, err := store.LoadSecrets()
	if err != nil {
		return err
	}
	m[keyCredential] = newCredential
	return store.SaveSecrets(m)
}
