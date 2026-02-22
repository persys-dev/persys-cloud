package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultConfigPath        = "config.yaml"
	defaultClusterConfigPath = "cluster.yaml"
)

type Config struct {
	ServiceName string          `yaml:"service_name"`
	App         AppConfig       `yaml:"app"`
	Database    DatabaseConfig  `yaml:"database"`
	TLS         TLSConfig       `yaml:"tls"`
	Vault       VaultConfig     `yaml:"vault"`
	CoreDNS     CoreDNSConfig   `yaml:"core_dns"`
	Prow        ProwConfig      `yaml:"prow"`
	Scheduler   SchedulerConfig `yaml:"scheduler"`
	GitHub      GitHubConfig    `yaml:"github"`
	Webhook     WebhookConfig   `yaml:"webhook"`
	Forgery     ForgeryConfig   `yaml:"forgery"`
	Log         LogConfig       `yaml:"log"`
	Telemetry   TelemetryConfig `yaml:"telemetry"`
}

type AppConfig struct {
	HTTPAddr         string            `yaml:"http_addr"`
	HTTPAddrPublic   string            `yaml:"http_addr_public"`
	GRPCAddr         string            `yaml:"grpc_addr"`
	Storage          string            `yaml:"storage"`
	Metadata         map[string]string `yaml:"metadata"`
	OAuthRedirectURL string            `yaml:"oauth_redirect_url"`
}

type DatabaseConfig struct {
	MongoURI    string   `yaml:"mongo_uri"`
	Collections []string `yaml:"collections"`
	Name        string   `yaml:"name"`
}

type TLSConfig struct {
	Enabled           bool   `yaml:"enabled"`
	CertPath          string `yaml:"cert_path"`
	KeyPath           string `yaml:"key_path"`
	CAPath            string `yaml:"ca_path"`
	RequireClientCert bool   `yaml:"require_client_cert"`
}

type VaultConfig struct {
	Enabled       bool   `yaml:"enabled"`
	Addr          string `yaml:"addr"`
	AuthMethod    string `yaml:"auth_method"`
	Token         string `yaml:"token"`
	AppRoleID     string `yaml:"approle_id"`
	AppSecretID   string `yaml:"approle_secret_id"`
	PKIMount      string `yaml:"pki_mount"`
	PKIRole       string `yaml:"pki_role"`
	CertTTL       string `yaml:"cert_ttl"`
	RetryInterval string `yaml:"retry_interval"`
	ServiceName   string `yaml:"service_name"`
	ServiceDomain string `yaml:"service_domain"`
	BindHost      string `yaml:"bind_host"`
}

type ProwConfig struct {
	SchedulerAddr   string `yaml:"scheduler_addr"`
	EnableProxy     bool   `yaml:"enable_proxy"`
	DiscoveryDomain string `yaml:"discovery_domain"`
	DiscoverySvc    string `yaml:"discovery_service"`
}

type CoreDNSConfig struct {
	Addr string `yaml:"addr"`
}

type SchedulerConfig struct {
	DefaultClusterID     string            `yaml:"default_cluster_id"`
	HealthPath           string            `yaml:"health_path"`
	HealthCheckInterval  string            `yaml:"health_check_interval"`
	DiscoveryInterval    string            `yaml:"discovery_interval"`
	RequestTimeout       string            `yaml:"request_timeout"`
	Clusters             []ClusterConfig   `yaml:"clusters"`
	RepositoryClusterMap map[string]string `yaml:"repository_cluster_map"`
}

type ClusterConfig struct {
	ID              string                    `yaml:"id"`
	Name            string                    `yaml:"name"`
	RoutingStrategy string                    `yaml:"routing_strategy"`
	Schedulers      []SchedulerInstanceConfig `yaml:"schedulers"`
}

type SchedulerInstanceConfig struct {
	ID       string `yaml:"id"`
	Address  string `yaml:"address"`
	IsLeader bool   `yaml:"is_leader"`
}

type GitHubConfig struct {
	WebHookURL    string `yaml:"webhook_url"`
	DefaultSecret string `yaml:"default_secret"`
	Auth          struct {
		ClientID     string `yaml:"client_id"`
		ClientSecret string `yaml:"client_secret"`
	} `yaml:"auth"`
}

type WebhookConfig struct {
	PublicPath         string            `yaml:"public_path"`
	ReplayTTL          string            `yaml:"replay_ttl"`
	RepositorySecrets  map[string]string `yaml:"repository_secrets"`
	ForwardRetries     int               `yaml:"forward_retries"`
	ForwardBaseBackoff string            `yaml:"forward_base_backoff"`
}

type ForgeryConfig struct {
	GRPCAddr          string `yaml:"grpc_addr"`
	GRPCServerName    string `yaml:"grpc_server_name"`
	WebhookForwardURL string `yaml:"webhook_forward_url"`
}

type LogConfig struct {
	LokiEndpoint string `yaml:"loki_endpoint"`
	Level        string `yaml:"level"`
}

type TelemetryConfig struct {
	OTLPEndpoint string `yaml:"otlp_endpoint"`
}

func LoadConfig() (*Config, error) {
	path := strings.TrimSpace(os.Getenv("PERSYS_GATEWAY_CONFIG"))
	if path == "" {
		path = defaultConfigPath
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	if err := cfg.loadClusterConfig(); err != nil {
		return nil, err
	}

	cfg.applyDefaults()
	cfg.applyEnvOverrides()
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if strings.TrimSpace(c.ServiceName) == "" {
		c.ServiceName = "persys-gateway"
	}
	if strings.TrimSpace(c.App.HTTPAddr) == "" {
		c.App.HTTPAddr = ":8551"
	}
	if strings.TrimSpace(c.App.HTTPAddrPublic) == "" {
		c.App.HTTPAddrPublic = ":8585"
	}
	if strings.TrimSpace(c.Webhook.PublicPath) == "" {
		c.Webhook.PublicPath = "/webhooks/github"
	}
	if strings.TrimSpace(c.Scheduler.HealthPath) == "" {
		c.Scheduler.HealthPath = "/health"
	}
	if strings.TrimSpace(c.Scheduler.HealthCheckInterval) == "" {
		c.Scheduler.HealthCheckInterval = "15s"
	}
	if strings.TrimSpace(c.Scheduler.RequestTimeout) == "" {
		c.Scheduler.RequestTimeout = "10s"
	}
	if strings.TrimSpace(c.Scheduler.DiscoveryInterval) == "" {
		c.Scheduler.DiscoveryInterval = "30s"
	}
	if strings.TrimSpace(c.Webhook.ReplayTTL) == "" {
		c.Webhook.ReplayTTL = "5m"
	}
	if strings.TrimSpace(c.Webhook.ForwardBaseBackoff) == "" {
		c.Webhook.ForwardBaseBackoff = "1s"
	}
	if c.Webhook.ForwardRetries <= 0 {
		c.Webhook.ForwardRetries = 5
	}
	if strings.TrimSpace(c.Forgery.WebhookForwardURL) == "" {
		c.Forgery.WebhookForwardURL = "https://persys-forgery:8080/internal/webhooks/github"
	}
	if strings.TrimSpace(c.Forgery.GRPCAddr) == "" {
		c.Forgery.GRPCAddr = "persys-forgery:8087"
	}
	if strings.TrimSpace(c.Forgery.GRPCServerName) == "" {
		c.Forgery.GRPCServerName = "persys-forgery.persys.local"
	}
	if strings.TrimSpace(c.App.OAuthRedirectURL) == "" {
		c.App.OAuthRedirectURL = "http://localhost:8585/auth"
	}
	if strings.TrimSpace(c.Vault.AuthMethod) == "" {
		c.Vault.AuthMethod = "approle"
	}
	if strings.TrimSpace(c.Vault.CertTTL) == "" {
		c.Vault.CertTTL = "24h"
	}
	if strings.TrimSpace(c.Vault.RetryInterval) == "" {
		c.Vault.RetryInterval = "30s"
	}
	if strings.TrimSpace(c.Vault.ServiceName) == "" {
		c.Vault.ServiceName = c.ServiceName
	}
}

func (c *Config) loadClusterConfig() error {
	path := strings.TrimSpace(os.Getenv("PERSYS_GATEWAY_CLUSTER_CONFIG"))
	if path == "" {
		path = defaultClusterConfigPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read cluster config %q: %w", path, err)
	}

	var overlay struct {
		Scheduler SchedulerConfig `yaml:"scheduler"`
	}
	if err := yaml.Unmarshal(data, &overlay); err != nil {
		return fmt.Errorf("parse cluster config %q: %w", path, err)
	}

	if strings.TrimSpace(overlay.Scheduler.DefaultClusterID) != "" {
		c.Scheduler.DefaultClusterID = overlay.Scheduler.DefaultClusterID
	}
	if len(overlay.Scheduler.RepositoryClusterMap) > 0 {
		c.Scheduler.RepositoryClusterMap = overlay.Scheduler.RepositoryClusterMap
	}
	if len(overlay.Scheduler.Clusters) > 0 {
		c.Scheduler.Clusters = overlay.Scheduler.Clusters
	}
	return nil
}

func (c *Config) validate() error {
	if strings.TrimSpace(c.Database.MongoURI) == "" {
		return fmt.Errorf("database.mongo_uri is required")
	}
	if strings.TrimSpace(c.Database.Name) == "" {
		return fmt.Errorf("database.name is required")
	}
	if strings.TrimSpace(c.TLS.CertPath) == "" || strings.TrimSpace(c.TLS.KeyPath) == "" || strings.TrimSpace(c.TLS.CAPath) == "" {
		return fmt.Errorf("tls.cert_path, tls.key_path and tls.ca_path are required")
	}
	return nil
}

func (c *Config) applyEnvOverrides() {
	// Database
	c.Database.MongoURI = envOrFile("PERSYS_GATEWAY_MONGO_URI", c.Database.MongoURI)
	if v := envOrFile("PERSYS_GATEWAY_DB_NAME", ""); v != "" {
		c.Database.Name = v
	}

	// TLS paths
	c.TLS.CertPath = envOrFile("PERSYS_GATEWAY_TLS_CERT_PATH", c.TLS.CertPath)
	c.TLS.KeyPath = envOrFile("PERSYS_GATEWAY_TLS_KEY_PATH", c.TLS.KeyPath)
	c.TLS.CAPath = envOrFile("PERSYS_GATEWAY_TLS_CA_PATH", c.TLS.CAPath)

	// Vault
	c.Vault.Addr = envOrFile("PERSYS_GATEWAY_VAULT_ADDR", c.Vault.Addr)
	c.Vault.AuthMethod = envOrFile("PERSYS_GATEWAY_VAULT_AUTH_METHOD", c.Vault.AuthMethod)
	c.Vault.Token = envOrFile("PERSYS_GATEWAY_VAULT_TOKEN", c.Vault.Token)
	c.Vault.AppRoleID = envOrFile("PERSYS_GATEWAY_VAULT_ROLE_ID", c.Vault.AppRoleID)
	c.Vault.AppSecretID = envOrFile("PERSYS_GATEWAY_VAULT_SECRET_ID", c.Vault.AppSecretID)

	// GitHub secrets
	c.GitHub.DefaultSecret = envOrFile("PERSYS_GATEWAY_GITHUB_WEBHOOK_SECRET", c.GitHub.DefaultSecret)
	c.GitHub.Auth.ClientID = envOrFile("PERSYS_GATEWAY_GITHUB_CLIENT_ID", c.GitHub.Auth.ClientID)
	c.GitHub.Auth.ClientSecret = envOrFile("PERSYS_GATEWAY_GITHUB_CLIENT_SECRET", c.GitHub.Auth.ClientSecret)

	// Forgery routing
	c.Forgery.GRPCAddr = envOrFile("PERSYS_GATEWAY_FORGERY_GRPC_ADDR", c.Forgery.GRPCAddr)
	c.Forgery.GRPCServerName = envOrFile("PERSYS_GATEWAY_FORGERY_GRPC_SERVER_NAME", c.Forgery.GRPCServerName)
}

func envOrFile(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	if file := strings.TrimSpace(os.Getenv(key + "_FILE")); file != "" {
		b, err := os.ReadFile(file)
		if err == nil {
			if v := strings.TrimSpace(string(b)); v != "" {
				return v
			}
		}
	}
	return fallback
}
