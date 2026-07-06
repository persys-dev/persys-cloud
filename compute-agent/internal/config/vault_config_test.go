package config

import (
	"testing"
	"time"
)

func TestValidate_VaultTokenAuthRequiresToken(t *testing.T) {
	cfg := &Config{
		GRPCPort:       50051,
		StateStorePath: "/tmp/state.db",
		DockerEnabled:  true,
		TLSEnabled:     true,
		TLSCertPath:    "/tmp/cert.pem",
		TLSKeyPath:     "/tmp/key.pem",
		TLSCAPath:      "/tmp/ca.pem",

		VaultEnabled:       true,
		VaultAddr:          "http://127.0.0.1:8200",
		VaultAuthMethod:    "token",
		VaultPKIMount:      "pki",
		VaultPKIRole:       "compute-agent",
		VaultCertTTL:       time.Hour,
		VaultRetryInterval: time.Minute,
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected vault token auth validation error")
	}
}

func TestValidate_VaultAppRoleAuthRequiresCredentials(t *testing.T) {
	cfg := &Config{
		GRPCPort:       50051,
		StateStorePath: "/tmp/state.db",
		DockerEnabled:  true,
		TLSEnabled:     true,
		TLSCertPath:    "/tmp/cert.pem",
		TLSKeyPath:     "/tmp/key.pem",
		TLSCAPath:      "/tmp/ca.pem",

		VaultEnabled:       true,
		VaultAddr:          "http://127.0.0.1:8200",
		VaultAuthMethod:    "approle",
		VaultPKIMount:      "pki",
		VaultPKIRole:       "compute-agent",
		VaultCertTTL:       time.Hour,
		VaultRetryInterval: time.Minute,
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected vault approle auth validation error")
	}
}
