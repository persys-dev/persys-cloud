package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr string
	HTTPPort int

	MetricsAddr string
	MetricsPort int

	ServerTLS      bool
	ServerCertPath string
	ServerKeyPath  string
	ServerCAPath   string

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

	ModelProvider string
	ModelEndpoint string
	ModelAPIKey   string
	ModelName     string
	Mode          string

	InferenceTimeout          time.Duration
	InferenceRateLimitPerSec  int
	InferenceFailureThreshold int
	InferenceCooldown         time.Duration

	PolicyMinConfidence float64
	PolicyMaxRisk       float64
	DefaultWorkload     string
}

func Load() (*Config, error) {
	cfg := &Config{
		HTTPAddr:    envOr("INTELLIGENCE_HTTP_ADDR", "0.0.0.0"),
		HTTPPort:    envIntOr("INTELLIGENCE_HTTP_PORT", 8093),
		MetricsAddr: envOr("INTELLIGENCE_METRICS_ADDR", "0.0.0.0"),
		MetricsPort: envIntOr("INTELLIGENCE_METRICS_PORT", 8094),

		ServerTLS:      envBoolOr("INTELLIGENCE_SERVER_TLS_ENABLED", false),
		ServerCertPath: envOr("INTELLIGENCE_SERVER_TLS_CERT", "/etc/persys/certs/persys_intelligence/persys_intelligence.crt"),
		ServerKeyPath:  envOr("INTELLIGENCE_SERVER_TLS_KEY", "/etc/persys/certs/persys_intelligence/persys_intelligence-key.key"),
		ServerCAPath:   envOr("INTELLIGENCE_SERVER_TLS_CA", "/etc/persys/certs/persys_scheduler/ca.pem"),

		VaultEnabled:       envBoolOr("INTELLIGENCE_VAULT_ENABLED", true),
		VaultAddr:          envOr("INTELLIGENCE_VAULT_ADDR", "http://localhost:8200"),
		VaultAuthMethod:    strings.ToLower(envOr("INTELLIGENCE_VAULT_AUTH_METHOD", "approle")),
		VaultToken:         strings.TrimSpace(os.Getenv("INTELLIGENCE_VAULT_TOKEN")),
		VaultAppRoleID:     strings.TrimSpace(os.Getenv("INTELLIGENCE_VAULT_APPROLE_ROLE_ID")),
		VaultAppSecretID:   strings.TrimSpace(os.Getenv("INTELLIGENCE_VAULT_APPROLE_SECRET_ID")),
		VaultPKIMount:      envOr("INTELLIGENCE_VAULT_PKI_MOUNT", "pki"),
		VaultPKIRole:       envOr("INTELLIGENCE_VAULT_PKI_ROLE", "persys-intelligence"),
		VaultCertTTL:       envDurationOr("INTELLIGENCE_VAULT_CERT_TTL", 24*time.Hour),
		VaultServiceName:   envOr("INTELLIGENCE_VAULT_SERVICE_NAME", "persys-intelligence"),
		VaultServiceDomain: strings.TrimSpace(os.Getenv("INTELLIGENCE_VAULT_SERVICE_DOMAIN")),
		VaultRetryInterval: envDurationOr("INTELLIGENCE_VAULT_RETRY_INTERVAL", time.Minute),

		ModelProvider: strings.ToLower(envOr("INTELLIGENCE_MODEL_PROVIDER", "mock")),
		ModelEndpoint: strings.TrimSpace(os.Getenv("INTELLIGENCE_MODEL_ENDPOINT")),
		ModelAPIKey:   strings.TrimSpace(os.Getenv("INTELLIGENCE_MODEL_API_KEY")),
		ModelName:     strings.TrimSpace(os.Getenv("INTELLIGENCE_MODEL_NAME")),
		Mode:          strings.ToLower(envOr("INTELLIGENCE_MODE", "advisory")),

		InferenceTimeout:          envDurationOr("INTELLIGENCE_INFERENCE_TIMEOUT", 3*time.Second),
		InferenceRateLimitPerSec:  envIntOr("INTELLIGENCE_INFERENCE_RATE_LIMIT_PER_SEC", 5),
		InferenceFailureThreshold: envIntOr("INTELLIGENCE_INFERENCE_FAILURE_THRESHOLD", 3),
		InferenceCooldown:         envDurationOr("INTELLIGENCE_INFERENCE_COOLDOWN", 30*time.Second),

		PolicyMinConfidence: envFloatOr("INTELLIGENCE_POLICY_MIN_CONFIDENCE", 0.70),
		PolicyMaxRisk:       envFloatOr("INTELLIGENCE_POLICY_MAX_RISK", 0.60),
		DefaultWorkload:     envOr("INTELLIGENCE_DEFAULT_WORKLOAD", "unknown-workload"),
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.HTTPPort < 1 || c.HTTPPort > 65535 {
		return fmt.Errorf("invalid INTELLIGENCE_HTTP_PORT: %d", c.HTTPPort)
	}
	if c.MetricsPort < 1 || c.MetricsPort > 65535 {
		return fmt.Errorf("invalid INTELLIGENCE_METRICS_PORT: %d", c.MetricsPort)
	}
	switch c.Mode {
	case "advisory", "policy-gated-auto", "human-approval":
	default:
		return fmt.Errorf("unsupported INTELLIGENCE_MODE: %s", c.Mode)
	}
	switch c.ModelProvider {
	case "mock", "disabled", "openai", "local", "fine-tuned":
	default:
		return fmt.Errorf("unsupported INTELLIGENCE_MODEL_PROVIDER: %s", c.ModelProvider)
	}
	if c.ModelProvider == "openai" || c.ModelProvider == "local" || c.ModelProvider == "fine-tuned" {
		if c.ModelEndpoint == "" {
			return fmt.Errorf("INTELLIGENCE_MODEL_ENDPOINT is required for provider %s", c.ModelProvider)
		}
		if c.ModelName == "" {
			return fmt.Errorf("INTELLIGENCE_MODEL_NAME is required for provider %s", c.ModelProvider)
		}
	}
	if c.InferenceTimeout <= 0 {
		return fmt.Errorf("INTELLIGENCE_INFERENCE_TIMEOUT must be > 0")
	}
	if c.InferenceRateLimitPerSec <= 0 {
		return fmt.Errorf("INTELLIGENCE_INFERENCE_RATE_LIMIT_PER_SEC must be > 0")
	}
	if c.InferenceFailureThreshold <= 0 {
		return fmt.Errorf("INTELLIGENCE_INFERENCE_FAILURE_THRESHOLD must be > 0")
	}
	if c.PolicyMinConfidence < 0 || c.PolicyMinConfidence > 1 {
		return fmt.Errorf("INTELLIGENCE_POLICY_MIN_CONFIDENCE must be in [0,1]")
	}
	if c.PolicyMaxRisk < 0 || c.PolicyMaxRisk > 1 {
		return fmt.Errorf("INTELLIGENCE_POLICY_MAX_RISK must be in [0,1]")
	}
	if c.ServerTLS {
		if strings.TrimSpace(c.ServerCertPath) == "" || strings.TrimSpace(c.ServerKeyPath) == "" || strings.TrimSpace(c.ServerCAPath) == "" {
			return fmt.Errorf("server TLS enabled but cert/key/ca paths are missing")
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

func envFloatOr(key string, fallback float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseFloat(v, 64)
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
