package config

import (
	"os"
	"testing"
	"time"
)

func clearEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		_ = os.Unsetenv(k)
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t,
		"GRPC_PORT", "METRICS_PORT", "ETCD_ENDPOINTS", "DOMAIN",
		"AGENTS_DISCOVERY_DOMAIN", "SCHEDULER_SHARD_KEY",
		"PERSYS_VAULT_ENABLED", "PERSYS_VAULT_AUTH_METHOD", "PERSYS_VAULT_TOKEN",
		"SCHEDULER_AGENT_STATUS_POLL_INTERVAL", "SCHEDULER_AGENT_APPLY_TIMEOUT",
		"SCHEDULER_RECONCILE_INTERVAL",
	)

	cfg, err := Load(false)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.GRPCPort != 8085 {
		t.Fatalf("unexpected GRPCPort: %d", cfg.GRPCPort)
	}
	if cfg.MetricsPort != 8084 {
		t.Fatalf("unexpected MetricsPort: %d", cfg.MetricsPort)
	}
	if len(cfg.EtcdEndpoints) != 1 || cfg.EtcdEndpoints[0] != "localhost:2379" {
		t.Fatalf("unexpected etcd endpoints: %#v", cfg.EtcdEndpoints)
	}
	if cfg.SchedulerReconcileInterval != 5*time.Second {
		t.Fatalf("unexpected reconcile interval: %s", cfg.SchedulerReconcileInterval)
	}
}

func TestLoadDurationSupportsSecondsInt(t *testing.T) {
	t.Setenv("SCHEDULER_AGENT_RPC_TIMEOUT", "15")
	cfg, err := Load(false)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.SchedulerAgentRPCTimeout != 15*time.Second {
		t.Fatalf("expected 15s, got %s", cfg.SchedulerAgentRPCTimeout)
	}
}

func TestValidateVaultTokenModeRequiresToken(t *testing.T) {
	t.Setenv("PERSYS_VAULT_ENABLED", "true")
	t.Setenv("PERSYS_VAULT_AUTH_METHOD", "token")
	t.Setenv("PERSYS_VAULT_TOKEN", "")

	_, err := Load(false)
	if err == nil {
		t.Fatalf("expected validation error for empty token")
	}
}

func TestValidateVaultAppRoleModeRequiresCredentials(t *testing.T) {
	t.Setenv("PERSYS_VAULT_ENABLED", "true")
	t.Setenv("PERSYS_VAULT_AUTH_METHOD", "approle")
	t.Setenv("PERSYS_VAULT_APPROLE_ROLE_ID", "")
	t.Setenv("PERSYS_VAULT_APPROLE_SECRET_ID", "")

	_, err := Load(false)
	if err == nil {
		t.Fatalf("expected validation error for missing approle credentials")
	}
}
