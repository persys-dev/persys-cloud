package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	GRPCAddr        string
	GRPCPort        int
	MetricsPort     int
	EvalInterval    time.Duration
	PrometheusURL   string
	SchedulerAddr   string
	SchedulerTLS    bool
	SchedulerCAPath string
	ClientCertPath  string
	ClientKeyPath   string
	ServerTLS       bool
	ServerCAPath    string
	ServerCertPath  string
	ServerKeyPath   string
	InsecureSkipTLS bool

	VaultEnabled       bool
	VaultAddr          string
	VaultAuthMethod    string
	VaultToken         string
	VaultAppRoleID     string
	VaultAppSecretID   string
	VaultPKIMount      string
	VaultPKIRole       string
	VaultCertTTL       time.Duration
	VaultServiceName   string
	VaultServiceDomain string
	VaultRetryInterval time.Duration

	ForgeryRedisEnabled bool
	ForgeryRedisAddr    string
	ForgeryRedisPass    string
	ForgeryRedisDB      int
	ForgeryPipelineKey  string

	StoreBackend               string
	PostgresDSN                string
	LeaderElectionEnabled      bool
	LeaderElectionLockID       int64
	LeaderElectionPollInterval time.Duration
}

func Load() (*Config, error) {
	cfg := &Config{
		GRPCAddr:        envOr("AUTOMATION_GRPC_ADDR", "0.0.0.0"),
		GRPCPort:        envIntOr("AUTOMATION_GRPC_PORT", 8091),
		MetricsPort:     envIntOr("AUTOMATION_METRICS_PORT", 8092),
		EvalInterval:    envDurationOr("AUTOMATION_EVAL_INTERVAL", 30*time.Second),
		PrometheusURL:   envOr("AUTOMATION_PROMETHEUS_URL", "http://localhost:9090"),
		SchedulerAddr:   envOr("AUTOMATION_SCHEDULER_ADDR", "localhost:8085"),
		SchedulerTLS:    envBoolOr("AUTOMATION_SCHEDULER_TLS_ENABLED", true),
		SchedulerCAPath: envOr("AUTOMATION_SCHEDULER_TLS_CA", "/etc/persys/certs/persys_scheduler/ca.pem"),
		ClientCertPath:  envOr("AUTOMATION_CLIENT_TLS_CERT", "/etc/persys/certs/persys_automation/persys_automation.crt"),
		ClientKeyPath:   envOr("AUTOMATION_CLIENT_TLS_KEY", "/etc/persys/certs/persys_automation/persys_automation-key.key"),
		ServerTLS:       envBoolOr("AUTOMATION_SERVER_TLS_ENABLED", false),
		ServerCAPath:    envOr("AUTOMATION_SERVER_TLS_CA", "/etc/persys/certs/persys_scheduler/ca.pem"),
		ServerCertPath:  envOr("AUTOMATION_SERVER_TLS_CERT", "/etc/persys/certs/persys_automation/persys_automation.crt"),
		ServerKeyPath:   envOr("AUTOMATION_SERVER_TLS_KEY", "/etc/persys/certs/persys_automation/persys_automation-key.key"),
		InsecureSkipTLS: envBoolOr("AUTOMATION_TLS_INSECURE_SKIP_VERIFY", false),
		VaultEnabled:    envBoolOr("AUTOMATION_VAULT_ENABLED", true),
		VaultAddr:       envOr("AUTOMATION_VAULT_ADDR", "http://localhost:8200"),
		VaultAuthMethod: strings.ToLower(envOr("AUTOMATION_VAULT_AUTH_METHOD", "approle")),
		VaultToken:      strings.TrimSpace(os.Getenv("AUTOMATION_VAULT_TOKEN")),
		VaultAppRoleID:  strings.TrimSpace(os.Getenv("AUTOMATION_VAULT_APPROLE_ROLE_ID")),
		VaultAppSecretID: strings.TrimSpace(
			os.Getenv("AUTOMATION_VAULT_APPROLE_SECRET_ID"),
		),
		VaultPKIMount:      envOr("AUTOMATION_VAULT_PKI_MOUNT", "pki"),
		VaultPKIRole:       envOr("AUTOMATION_VAULT_PKI_ROLE", "persys-automation"),
		VaultCertTTL:       envDurationOr("AUTOMATION_VAULT_CERT_TTL", 24*time.Hour),
		VaultServiceName:   envOr("AUTOMATION_VAULT_SERVICE_NAME", "persys-automation"),
		VaultServiceDomain: strings.TrimSpace(os.Getenv("AUTOMATION_VAULT_SERVICE_DOMAIN")),
		VaultRetryInterval: envDurationOr("AUTOMATION_VAULT_RETRY_INTERVAL", time.Minute),
		ForgeryRedisEnabled: envBoolOr(
			"AUTOMATION_FORGERY_REDIS_ENABLED",
			false,
		),
		ForgeryRedisAddr:   envOr("AUTOMATION_FORGERY_REDIS_ADDR", "localhost:6379"),
		ForgeryRedisPass:   strings.TrimSpace(os.Getenv("AUTOMATION_FORGERY_REDIS_PASSWORD")),
		ForgeryRedisDB:     envIntOr("AUTOMATION_FORGERY_REDIS_DB", 1),
		ForgeryPipelineKey: envOr("AUTOMATION_FORGERY_PIPELINE_KEY", "pipeline_status"),
		StoreBackend:       envOr("AUTOMATION_STORE_BACKEND", "postgres"),
		PostgresDSN: envOr(
			"AUTOMATION_POSTGRES_DSN",
			"postgres://automation:automation@localhost:5432/persys_automation?sslmode=disable",
		),
		LeaderElectionEnabled:      envBoolOr("AUTOMATION_LEADER_ELECTION_ENABLED", true),
		LeaderElectionLockID:       envInt64Or("AUTOMATION_LEADER_ELECTION_LOCK_ID", 771001),
		LeaderElectionPollInterval: envDurationOr("AUTOMATION_LEADER_ELECTION_POLL_INTERVAL", 5*time.Second),
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.GRPCPort < 1 || c.GRPCPort > 65535 {
		return fmt.Errorf("invalid AUTOMATION_GRPC_PORT: %d", c.GRPCPort)
	}
	if c.MetricsPort < 1 || c.MetricsPort > 65535 {
		return fmt.Errorf("invalid AUTOMATION_METRICS_PORT: %d", c.MetricsPort)
	}
	if strings.TrimSpace(c.SchedulerAddr) == "" {
		return fmt.Errorf("AUTOMATION_SCHEDULER_ADDR is required")
	}
	switch strings.ToLower(strings.TrimSpace(c.StoreBackend)) {
	case "memory":
	case "postgres":
		if strings.TrimSpace(c.PostgresDSN) == "" {
			return fmt.Errorf("AUTOMATION_POSTGRES_DSN is required when using postgres backend")
		}
	default:
		return fmt.Errorf("unsupported AUTOMATION_STORE_BACKEND: %s", c.StoreBackend)
	}
	if c.SchedulerTLS {
		if strings.TrimSpace(c.SchedulerCAPath) == "" || strings.TrimSpace(c.ClientCertPath) == "" || strings.TrimSpace(c.ClientKeyPath) == "" {
			return fmt.Errorf("scheduler TLS enabled but CA/cert/key paths are missing")
		}
	}
	if c.ServerTLS {
		if strings.TrimSpace(c.ServerCAPath) == "" || strings.TrimSpace(c.ServerCertPath) == "" || strings.TrimSpace(c.ServerKeyPath) == "" {
			return fmt.Errorf("server TLS enabled but CA/cert/key paths are missing")
		}
	}
	tlsEnabled := c.SchedulerTLS || c.ServerTLS
	if c.VaultEnabled && tlsEnabled {
		switch c.VaultAuthMethod {
		case "token":
			if strings.TrimSpace(c.VaultToken) == "" {
				return fmt.Errorf("vault token auth selected but AUTOMATION_VAULT_TOKEN is empty")
			}
		case "approle":
			if strings.TrimSpace(c.VaultAppRoleID) == "" || strings.TrimSpace(c.VaultAppSecretID) == "" {
				return fmt.Errorf("vault approle auth selected but role_id/secret_id is missing")
			}
		default:
			return fmt.Errorf("unsupported AUTOMATION_VAULT_AUTH_METHOD=%q", c.VaultAuthMethod)
		}
		// Shared cert manager writes one cert/key/ca bundle. Require unified paths.
		if c.ClientCertPath != c.ServerCertPath || c.ClientKeyPath != c.ServerKeyPath || c.SchedulerCAPath != c.ServerCAPath {
			return fmt.Errorf("vault-managed TLS requires unified client/server cert and CA paths")
		}
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envBoolOr(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return strings.EqualFold(v, "true")
}

func envIntOr(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envInt64Or(key string, fallback int64) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func envDurationOr(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second
	}
	return fallback
}
