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
	RootToken string
	UnsealKey string
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

// WaitUntilReady blocks until Vault responds to a health check.
func WaitUntilReady(client *vault.Client) {
	for {
		_, err := client.Sys().Health()
		if err == nil {
			return
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
// root token and unseal key.
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
		RootToken: data.RootToken,
		UnsealKey: unsealKeys[0],
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
