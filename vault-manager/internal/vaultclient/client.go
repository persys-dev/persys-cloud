// Package vaultclient owns the Vault client lifecycle: connecting,
// waiting for readiness, initializing, unsealing, and handing off from a
// root token to a scoped AppRole identity.
package vaultclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	vault "github.com/hashicorp/vault/api"

	"github.com/persys-dev/persys-cloud/vault-manager/internal/config"
)

// InitResult holds the credentials returned by a fresh Vault initialization.
type InitResult struct {
	RootToken  string
	UnsealKeys []string
}

// New creates a Vault API client pointed at addr, optionally authenticated
// with token.
func New(addr, token string) (*vault.Client, error) {
	cfg := vault.DefaultConfig()
	cfg.Address = addr
	client, err := vault.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	if token != "" {
		client.SetToken(token)
	}
	return client, nil
}

// WaitUntilReady blocks until Vault answers the seal-status endpoint.
// Sealed Vault is considered ready — callers unseal explicitly afterward.
// Using /sys/health is wrong here: sealed nodes return 503 and the API
// client treats that as an error, which would hang forever after a Vault restart.
func WaitUntilReady(addr string) {
	for {
		resp, err := http.Get(addr + "/v1/sys/seal-status")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 500 {
				return
			}
		}
		config.Log.Println("Waiting for Vault...")
		time.Sleep(2 * time.Second)
	}
}

// IsInitialized reports whether the Vault at addr has already been initialized.
func IsInitialized(addr string) (bool, error) {
	resp, err := http.Get(addr + "/v1/sys/init")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	var data struct {
		Initialized bool `json:"initialized"`
	}
	err = json.NewDecoder(resp.Body).Decode(&data)
	return data.Initialized, err
}

// Initialize performs a single-shard Vault initialization and returns the
// root token and unseal key(s).
func Initialize(addr string) (*InitResult, error) {
	body := map[string]interface{}{
		"secret_shares":    1,
		"secret_threshold": 1,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("PUT", addr+"/v1/sys/init", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("vault init failed with status %s", resp.Status)
	}

	var data struct {
		RootToken     string   `json:"root_token"`
		KeysBase64    []string `json:"keys_base64"`
		UnsealKeysB64 []string `json:"unseal_keys_b64"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode vault init response: %w", err)
	}

	unsealKeys := data.UnsealKeysB64
	if len(unsealKeys) == 0 {
		unsealKeys = data.KeysBase64
	}
	if data.RootToken == "" || len(unsealKeys) == 0 || strings.TrimSpace(unsealKeys[0]) == "" {
		return nil, errors.New("vault init response missing root token or unseal key")
	}

	return &InitResult{
		RootToken:  data.RootToken,
		UnsealKeys: unsealKeys,
	}, nil
}

// Unseal submits a single unseal key to Vault.
func Unseal(addr, unsealKey string) error {
	body := map[string]interface{}{
		"key": unsealKey,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", addr+"/v1/sys/unseal", bytes.NewBuffer(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("vault unseal failed with status %s", resp.Status)
	}
	return nil
}

// IsSealed reports whether the Vault at addr is currently sealed.
func IsSealed(addr string) (bool, error) {
	resp, err := http.Get(addr + "/v1/sys/seal-status")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	var out struct {
		Sealed bool `json:"sealed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	return out.Sealed, nil
}

// UnsealAll submits every key in keys until Vault reports unsealed, or
// returns an error if it remains sealed after all keys.
func UnsealAll(addr string, keys []string) error {
	if len(keys) == 0 {
		return errors.New("no unseal keys provided")
	}
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if err := Unseal(addr, key); err != nil {
			return err
		}
		sealed, err := IsSealed(addr)
		if err != nil {
			return err
		}
		if !sealed {
			return nil
		}
	}
	return errors.New("vault still sealed after all keys")
}

// EnsureUnsealed unseals Vault if it is currently sealed, using the given keys.
// It is a no-op when Vault is already unsealed.
func EnsureUnsealed(addr string, keys []string) error {
	sealed, err := IsSealed(addr)
	if err != nil {
		return err
	}
	if !sealed {
		config.Log.Println("Vault is already unsealed.")
		return nil
	}
	if len(keys) == 0 {
		return errors.New("vault is sealed but no unseal keys are available in bootstrap state")
	}
	config.Log.Println("Vault is sealed; unsealing with stored keys...")
	if err := UnsealAll(addr, keys); err != nil {
		return err
	}
	config.Log.Println("Vault unsealed.")
	return nil
}

// LoginAppRole authenticates with role_id/secret_id and returns a client
// holding the resulting token.
func LoginAppRole(addr, roleID, secretID string) (*vault.Client, error) {
	base, err := New(addr, "")
	if err != nil {
		return nil, err
	}
	loginSecret, err := base.Logical().Write("auth/approle/login", map[string]interface{}{
		"role_id":   roleID,
		"secret_id": secretID,
	})
	if err != nil {
		return nil, fmt.Errorf("approle login failed: %w", err)
	}
	if loginSecret == nil || loginSecret.Auth == nil || loginSecret.Auth.ClientToken == "" {
		return nil, errors.New("approle login returned empty token")
	}
	return New(addr, loginSecret.Auth.ClientToken)
}
