package config

import (
	"os"
	"testing"
)

func TestValidate_InvalidPort(t *testing.T) {
	cfg := &Config{GRPCPort: 70000, StateStorePath: "/tmp/state.db", DockerEnabled: true}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid port error")
	}
}

func TestValidate_TLSRequiresPaths(t *testing.T) {
	cfg := &Config{GRPCPort: 50051, TLSEnabled: true, StateStorePath: "/tmp/state.db", DockerEnabled: true}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected TLS path validation error")
	}
}

func TestValidate_AtLeastOneRuntime(t *testing.T) {
	cfg := &Config{GRPCPort: 50051, StateStorePath: "/tmp/state.db"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected runtime validation error")
	}
}

func TestLoad_NodeLabelsFromEnv(t *testing.T) {
	t.Setenv("PERSYS_GRPC_PORT", "50051")
	t.Setenv("PERSYS_STATE_PATH", "/tmp/state.db")
	t.Setenv("PERSYS_DOCKER_ENABLED", "true")
	t.Setenv("PERSYS_COMPOSE_ENABLED", "false")
	t.Setenv("PERSYS_VM_ENABLED", "false")
	t.Setenv("PERSYS_TLS_ENABLED", "false")
	t.Setenv("PERSYS_VAULT_ENABLED", "false")
	t.Setenv("PERSYS_NODE_REGION", "us-east-1")
	t.Setenv("PERSYS_NODE_ENV", "prod")
	t.Setenv("PERSYS_NODE_LABELS", "team=platform,zone=use1-az2,invalid,noequal=,=novalue")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.NodeLabels["region"]; got != "us-east-1" {
		t.Fatalf("expected region label, got %q", got)
	}
	if got := cfg.NodeLabels["env"]; got != "prod" {
		t.Fatalf("expected env label, got %q", got)
	}
	if got := cfg.NodeLabels["team"]; got != "platform" {
		t.Fatalf("expected team label, got %q", got)
	}
	if got := cfg.NodeLabels["zone"]; got != "use1-az2" {
		t.Fatalf("expected zone label, got %q", got)
	}
	if _, ok := cfg.NodeLabels["invalid"]; ok {
		t.Fatal("did not expect invalid label key")
	}
}

func TestLoad_SchedulerAddrFromEnv(t *testing.T) {
	for _, k := range []string{
		"PERSYS_GRPC_PORT",
		"PERSYS_STATE_PATH",
		"PERSYS_DOCKER_ENABLED",
		"PERSYS_COMPOSE_ENABLED",
		"PERSYS_VM_ENABLED",
		"PERSYS_TLS_ENABLED",
		"PERSYS_SCHEDULER_ADDR",
		"PERSYS_SCHEDULER_INSECURE",
	} {
		_ = os.Unsetenv(k)
	}
	t.Setenv("PERSYS_GRPC_PORT", "50051")
	t.Setenv("PERSYS_STATE_PATH", "/tmp/state.db")
	t.Setenv("PERSYS_DOCKER_ENABLED", "true")
	t.Setenv("PERSYS_COMPOSE_ENABLED", "false")
	t.Setenv("PERSYS_VM_ENABLED", "false")
	t.Setenv("PERSYS_TLS_ENABLED", "false")
	t.Setenv("PERSYS_VAULT_ENABLED", "false")
	t.Setenv("PERSYS_SCHEDULER_ADDR", "10.0.0.9:8085")
	t.Setenv("PERSYS_SCHEDULER_INSECURE", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SchedulerAddr != "10.0.0.9:8085" {
		t.Fatalf("expected scheduler addr from env, got %q", cfg.SchedulerAddr)
	}
	if !cfg.SchedulerInsecure {
		t.Fatal("expected scheduler insecure mode to be true")
	}
}
