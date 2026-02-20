package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// Runtime flags
	Insecure bool

	// Scheduler network
	GRPCAddr    string
	GRPCPort    int
	MetricsPort int
	ExternalIP  string

	// etcd / discovery
	EtcdEndpoints          []string
	Domain                 string
	AgentsDiscoveryDomain  string
	SchedulerShardKey      string
	SchedulerAdvertiseIP   string
	SchedulerAdvertisePort int

	// TLS
	TLSEnabled  bool
	TLSCAPath   string
	TLSCertPath string
	TLSKeyPath  string

	// Vault certificate manager
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

	// Scheduler -> agent gRPC
	SchedulerAgentTLSEnabled         bool
	SchedulerAgentStatusPollInterval time.Duration
	SchedulerAgentApplyTimeout       time.Duration
	SchedulerAgentVMApplyTimeout     time.Duration
	SchedulerAgentDeleteTimeout      time.Duration
	SchedulerAgentRPCTimeout         time.Duration

	// Reconciliation / drift
	SchedulerReconcileInterval    time.Duration
	SchedulerDriftDetectInterval  time.Duration
	SchedulerNodeUnavailableGrace time.Duration
	SchedulerReapplyGuard         time.Duration
	SchedulerMissingGracePeriod   time.Duration

	// Logging / telemetry
	LogLevel       string
	LogFormat      string
	OTLPEndpoint   string
	JaegerEndpoint string
	OTLPInsecure   bool
}

func Load(insecureFlag bool) (*Config, error) {
	grpcPort := envIntOr("GRPC_PORT", 8085)
	metricsPort := envIntOr("METRICS_PORT", 8084)
	advertisePort := envIntOr("SCHEDULER_ADVERTISE_PORT", grpcPort)

	cfg := &Config{
		Insecure: insecureFlag,

		GRPCAddr:    envOr("PERSYS_GRPC_ADDR", "0.0.0.0"),
		GRPCPort:    grpcPort,
		MetricsPort: metricsPort,
		ExternalIP: envOr("PERSYS_EXTERNAL_IP", ""),

		EtcdEndpoints:          splitCSV(envOr("ETCD_ENDPOINTS", "localhost:2379")),
		Domain:                 envOr("DOMAIN", "persys.local"),
		AgentsDiscoveryDomain:  envOr("AGENTS_DISCOVERY_DOMAIN", "agents.persys.cloud"),
		SchedulerShardKey:      envOr("SCHEDULER_SHARD_KEY", "genesis"),
		SchedulerAdvertiseIP:   strings.TrimSpace(os.Getenv("SCHEDULER_ADVERTISE_IP")),
		SchedulerAdvertisePort: advertisePort,

		TLSEnabled:  !insecureFlag,
		TLSCAPath:   envOr("PERSYS_TLS_CA", "/etc/persys/certs/persys_scheduler/ca.pem"),
		TLSCertPath: envOr("PERSYS_TLS_CERT", "/etc/persys/certs/persys_scheduler/persys_scheduler.crt"),
		TLSKeyPath:  envOr("PERSYS_TLS_KEY", "/etc/persys/certs/persys_scheduler/persys_scheduler-key.key"),

		VaultEnabled:       envBoolOr("PERSYS_VAULT_ENABLED", true),
		VaultAddr:          envOr("PERSYS_VAULT_ADDR", "http://localhost:8200"),
		VaultAuthMethod:    strings.ToLower(envOr("PERSYS_VAULT_AUTH_METHOD", "token")),
		VaultToken:         strings.TrimSpace(os.Getenv("PERSYS_VAULT_TOKEN")),
		VaultAppRoleID:     strings.TrimSpace(os.Getenv("PERSYS_VAULT_APPROLE_ROLE_ID")),
		VaultAppSecretID:   strings.TrimSpace(os.Getenv("PERSYS_VAULT_APPROLE_SECRET_ID")),
		VaultPKIMount:      envOr("PERSYS_VAULT_PKI_MOUNT", "pki"),
		VaultPKIRole:       envOr("PERSYS_VAULT_PKI_ROLE", "persys-scheduler"),
		VaultCertTTL:       envDurationOr("PERSYS_VAULT_CERT_TTL", 24*time.Hour),
		VaultServiceName:   envOr("PERSYS_VAULT_SERVICE_NAME", "persys-scheduler"),
		VaultServiceDomain: strings.TrimSpace(os.Getenv("PERSYS_VAULT_SERVICE_DOMAIN")),
		VaultRetryInterval: envDurationOr("PERSYS_VAULT_RETRY_INTERVAL", time.Minute),

		SchedulerAgentTLSEnabled:         envBoolOr("SCHEDULER_AGENT_TLS_ENABLED", !insecureFlag),
		SchedulerAgentStatusPollInterval: envDurationOrFlexibleSeconds("SCHEDULER_AGENT_STATUS_POLL_INTERVAL", time.Second),
		SchedulerAgentApplyTimeout:       envDurationOrFlexibleSeconds("SCHEDULER_AGENT_APPLY_TIMEOUT", 45*time.Second),
		SchedulerAgentVMApplyTimeout:     envDurationOrFlexibleSeconds("SCHEDULER_AGENT_VM_APPLY_TIMEOUT", 240*time.Second),
		SchedulerAgentDeleteTimeout:      envDurationOrFlexibleSeconds("SCHEDULER_AGENT_DELETE_TIMEOUT", 60*time.Second),
		SchedulerAgentRPCTimeout:         envDurationOrFlexibleSeconds("SCHEDULER_AGENT_RPC_TIMEOUT", 10*time.Second),

		SchedulerReconcileInterval:    envDurationOrFlexibleSeconds("SCHEDULER_RECONCILE_INTERVAL", 5*time.Second),
		SchedulerDriftDetectInterval:  envDurationOrFlexibleSeconds("SCHEDULER_DRIFT_DETECT_INTERVAL", 300*time.Second),
		SchedulerNodeUnavailableGrace: envDurationOrFlexibleSeconds("SCHEDULER_NODE_UNAVAILABLE_GRACE", 3*time.Minute),
		SchedulerReapplyGuard:         envDurationOrFlexibleSeconds("SCHEDULER_REAPPLY_GUARD", 45*time.Second),
		SchedulerMissingGracePeriod:   envDurationOrFlexibleSeconds("SCHEDULER_MISSING_GRACE_PERIOD", 10*time.Second),

		LogLevel:       envOr("LOG_LEVEL", "info"),
		LogFormat:      envOr("LOG_FORMAT", "json"),
		OTLPEndpoint:   strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
		JaegerEndpoint: strings.TrimSpace(os.Getenv("JAEGER_ENDPOINT")),
		OTLPInsecure:   envBoolOr("OTEL_EXPORTER_OTLP_INSECURE", true),
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.GRPCPort < 1 || c.GRPCPort > 65535 {
		return fmt.Errorf("invalid GRPC_PORT: %d", c.GRPCPort)
	}
	if c.MetricsPort < 1 || c.MetricsPort > 65535 {
		return fmt.Errorf("invalid METRICS_PORT: %d", c.MetricsPort)
	}
	if c.SchedulerAdvertisePort < 1 || c.SchedulerAdvertisePort > 65535 {
		return fmt.Errorf("invalid SCHEDULER_ADVERTISE_PORT: %d", c.SchedulerAdvertisePort)
	}
	if len(c.EtcdEndpoints) == 0 {
		return fmt.Errorf("at least one ETCD endpoint is required")
	}
	for _, ep := range c.EtcdEndpoints {
		if strings.TrimSpace(ep) == "" {
			return fmt.Errorf("ETCD endpoints contain empty value")
		}
	}
	if c.TLSEnabled {
		if c.TLSCertPath == "" || c.TLSKeyPath == "" || c.TLSCAPath == "" {
			return fmt.Errorf("TLS enabled but cert/key/ca paths are missing")
		}
	}
	if c.VaultEnabled && c.TLSEnabled {
		switch c.VaultAuthMethod {
		case "token":
			if strings.TrimSpace(c.VaultToken) == "" {
				return fmt.Errorf("vault token auth selected but PERSYS_VAULT_TOKEN is empty")
			}
		case "approle":
			if strings.TrimSpace(c.VaultAppRoleID) == "" || strings.TrimSpace(c.VaultAppSecretID) == "" {
				return fmt.Errorf("vault approle auth selected but role_id/secret_id is missing")
			}
		default:
			return fmt.Errorf("unsupported PERSYS_VAULT_AUTH_METHOD=%q", c.VaultAuthMethod)
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

func envDurationOr(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

// envDurationOrFlexibleSeconds supports "15s" and "15" (seconds).
func envDurationOrFlexibleSeconds(key string, fallback time.Duration) time.Duration {
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

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
