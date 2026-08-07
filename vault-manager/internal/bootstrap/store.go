// Package bootstrap persists the credentials needed to recover after a
// vault-manager (or Vault itself) restart: unseal keys and either a root
// token or the bootstrap-manager AppRole credentials.
package bootstrap

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// State is the on-disk recovery record written after a successful bootstrap.
type State struct {
	UnsealKeys []string `json:"unseal_keys"`

	// RootToken is kept when running without --secure. Cleared after a
	// successful --secure handoff so the file never holds a live root token.
	RootToken string `json:"root_token,omitempty"`

	// Manager AppRole credentials used when --secure is enabled (and on
	// subsequent restarts of a previously secured deployment).
	ManagerRoleID   string `json:"manager_role_id,omitempty"`
	ManagerSecretID string `json:"manager_secret_id,omitempty"`
}

// ErrNotFound is returned by Load when the bootstrap file does not exist.
var ErrNotFound = errors.New("bootstrap state file not found")

// Load reads and decodes the bootstrap state from path.
// Returns ErrNotFound if the file does not exist.
func Load(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Save writes state to path atomically (tmp + rename) with restrictive perms.
func Save(path string, s *State) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	tmp := path + ".tmp"
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// HasManagerCreds reports whether the state holds usable manager AppRole credentials.
func (s *State) HasManagerCreds() bool {
	return s != nil && s.ManagerRoleID != "" && s.ManagerSecretID != ""
}

// HasUnsealKeys reports whether the state holds at least one unseal key.
func (s *State) HasUnsealKeys() bool {
	return s != nil && len(s.UnsealKeys) > 0
}
