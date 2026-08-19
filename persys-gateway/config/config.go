package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultConfigPath        = "config.yaml"
	defaultClusterConfigPath = "cluster.yaml"
)

type Config struct {
	ServiceName     string                `yaml:"service_name"`
	App             AppConfig             `yaml:"app"`
	Database        DatabaseConfig        `yaml:"database"`
	TLS             TLSConfig             `yaml:"tls"`
	Vault           VaultConfig           `yaml:"vault"`
	CoreDNS         CoreDNSConfig         `yaml:"core_dns"`
	Deployment      DeploymentConfig      `yaml:"deployment"`
	LegacyScheduler LegacySchedulerConfig `yaml:"legacy_scheduler"`
	Scheduler       SchedulerConfig       `yaml:"scheduler"`
	GitHub          GitHubConfig          `yaml:"github"`
	Webhook         WebhookConfig         `yaml:"webhook"`
	Forgery         ForgeryConfig         `yaml:"forgery"`
	Automation      AutomationConfig      `yaml:"automation"`
	Intelligence    IntelligenceConfig    `yaml:"intelligence"`
	Log             LogConfig             `yaml:"log"`
	Telemetry       TelemetryConfig       `yaml:"telemetry"`
}

// DeploymentMode is the single toggle deciding whether the gateway runs
// as a self-hosted, single-tenant control plane (default — the
// open-source posture, no GitHub OAuth app required) or as the backend
// for a managed, multi-tenant offering (GitHub OAuth mounts, JWT auth is
// enforced on customer-facing routes, cluster ownership is checked).
type DeploymentMode string

const (
	DeploymentSelfHosted DeploymentMode = "self-hosted"
	DeploymentManaged    DeploymentMode = "managed"
)

type DeploymentConfig struct {
	Mode DeploymentMode `yaml:"mode"`
}

// LegacySchedulerConfig is a fallback path, used only when the scheduler
// pool (SchedulerConfig.Clusters) can't resolve a candidate — e.g. a
// single-scheduler deployment that hasn't set up cluster.yaml at all.
// Renamed from ProwConfig, which didn't describe anything about what
// this actually does.
type LegacySchedulerConfig struct {
	FallbackAddr    string `yaml:"fallback_addr"`
	ProxyEnabled    bool   `yaml:"proxy_enabled"`
	DiscoveryDomain string `yaml:"discovery_domain"`
	DiscoverySvc    string `yaml:"discovery_service"`
}

type AppConfig struct {
	HTTPAddr         string            `yaml:"http_addr"`
	HTTPAddrPublic   string            `yaml:"http_addr_public"`
	GRPCAddr         string            `yaml:"grpc_addr"`
	Storage          string            `yaml:"storage"`
	Metadata         map[string]string `yaml:"metadata"`
	OAuthRedirectURL string            `yaml:"oauth_redirect_url"`
	// JWTSecret signs/verifies user session tokens. Never hardcode this —
	// set PERSYS_GATEWAY_JWT_SECRET (or app.jwt_secret in config.yaml,
	// not recommended for anything but local dev). If left empty in
	// self-hosted mode, a random secret is generated at boot (fine: self
	// -hosted's default auth mode doesn't depend on it — see
	// catalog.AuthMode.Resolve). Required to be set explicitly in managed
	// mode; LoadConfig fails fast otherwise.
	JWTSecret string `yaml:"jwt_secret"`
}

type DatabaseConfig struct {
	// DSN is a standard Postgres connection string, e.g.
	// "postgres://persys:persys@localhost:5432/persys_gateway?sslmode=disable".
	// Empty is valid in self-hosted mode — see Enabled. Always set this
	// via PERSYS_GATEWAY_POSTGRES_DSN in managed mode, where it's
	// required (LoadConfig fails fast otherwise).
	DSN string `yaml:"dsn"`
	// MaxConns bounds the connection pool. Defaults to 10.
	MaxConns int32 `yaml:"max_conns"`
}

// Enabled reports whether a database is configured at all. False is a
// normal, fully-supported state in self-hosted mode: user/session/OAuth
// storage is only touched by code paths that only mount in managed mode
// (see routes gating in cmd/main.go), and webhook.service.go's audit
// persistence already no-ops when its store is nil, falling back to
// in-memory-only replay tracking.
func (d DatabaseConfig) Enabled() bool {
	return strings.TrimSpace(d.DSN) != ""
}

type TLSConfig struct {
	Enabled           bool   `yaml:"enabled"`
	CertPath          string `yaml:"cert_path"`
	KeyPath           string `yaml:"key_path"`
	CAPath            string `yaml:"ca_path"`
	RequireClientCert bool   `yaml:"require_client_cert"`
}

type VaultConfig struct {
	Enabled       bool          `yaml:"enabled"`
	ManagerAddr   string        `yaml:"manager_addr"`
	Addr          string        `yaml:"addr"`
	AuthMethod    string        `yaml:"auth_method"`
	Token         string        `yaml:"token"`
	AppRoleID     string        `yaml:"approle_id"`
	AppSecretID   string        `yaml:"approle_secret_id"`
	PKIMount      string        `yaml:"pki_mount"`
	PKIRole       string        `yaml:"pki_role"`
	CertTTL       time.Duration `yaml:"cert_ttl"`
	RetryInterval time.Duration `yaml:"retry_interval"`
	ServiceName   string        `yaml:"service_name"`
	ServiceDomain string        `yaml:"service_domain"`
	BindHost      string        `yaml:"bind_host"`
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

type AutomationConfig struct {
	GRPCAddr       string `yaml:"grpc_addr"`
	GRPCServerName string `yaml:"grpc_server_name"`
	RequestTimeout string `yaml:"request_timeout"`
}

type IntelligenceConfig struct {
	HTTPAddr       string `yaml:"http_addr"`
	RequestTimeout string `yaml:"request_timeout"`
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

	cfg.applyEnvOverrides()

	// Deliberately checked BEFORE applyDefaults fills in an auto-generated
	// JWT secret: in managed (multi-tenant) mode, a secret that resets on
	// every restart would silently invalidate every customer session, and
	// a secret nobody chose is a worse security posture than refusing to
	// start. Self-hosted mode doesn't have this problem — its default
	// auth mode never depends on the JWT secret in the first place.
	if cfg.Deployment.Mode == DeploymentManaged && strings.TrimSpace(cfg.App.JWTSecret) == "" {
		return nil, fmt.Errorf("app.jwt_secret (or PERSYS_GATEWAY_JWT_SECRET) is required when deployment.mode is %q", DeploymentManaged)
	}
	// Same reasoning as the JWT secret check above: managed mode has a
	// real, ongoing need for user/session storage (OAuth login,
	// cluster ownership down the line), so a missing database there is
	// a startup-time misconfiguration, not something to silently paper
	// over. Self-hosted has no such requirement — see
	// DatabaseConfig.Enabled and the comment in applyDefaults.
	if cfg.Deployment.Mode == DeploymentManaged && strings.TrimSpace(cfg.Database.DSN) == "" {
		return nil, fmt.Errorf("database.dsn (or PERSYS_GATEWAY_POSTGRES_DSN) is required when deployment.mode is %q", DeploymentManaged)
	}

	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if strings.TrimSpace(c.ServiceName) == "" {
		c.ServiceName = "persys-gateway"
	}
	if strings.TrimSpace(string(c.Deployment.Mode)) == "" {
		c.Deployment.Mode = DeploymentSelfHosted
	}
	if strings.TrimSpace(c.App.JWTSecret) == "" {
		// Only reachable here in self-hosted mode — LoadConfig already
		// failed fast for managed mode with no secret set. A random
		// secret is fine for self-hosted: its default auth resolution
		// (catalog.AuthMode.Resolve) doesn't route customer-facing
		// routes through JWT verification in the first place. It does
		// mean any token issued before a restart stops verifying after
		// one, which is acceptable for a single-operator deployment but
		// worth knowing about.
		secret, err := randomHexSecret(32)
		if err != nil {
			log.Fatalf("failed to generate a random JWT secret: %v", err)
		}
		c.App.JWTSecret = secret
		log.Printf("WARNING: app.jwt_secret not set — generated a random secret for this process only. " +
			"Set PERSYS_GATEWAY_JWT_SECRET explicitly if you need sessions to survive a restart.")
	}
	if strings.TrimSpace(c.App.HTTPAddr) == "" {
		c.App.HTTPAddr = ":8551"
	}
	if strings.TrimSpace(c.App.HTTPAddrPublic) == "" {
		c.App.HTTPAddrPublic = ":8585"
	}
	// No default DSN. An empty database.dsn is a real, supported state
	// for self-hosted: user/session/OAuth storage is only touched by
	// code paths (/auth/*, /github/*) that only mount in managed mode,
	// and webhook.service.go already degrades to in-memory-only replay
	// tracking with no DB — see DatabaseConfig.Enabled and main.go.
	// Managed mode requires a DSN and fails fast at LoadConfig if it's
	// missing (see below, mirroring the JWT secret check).
	if c.Database.MaxConns <= 0 {
		c.Database.MaxConns = 10
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
	if strings.TrimSpace(c.Automation.GRPCAddr) == "" {
		c.Automation.GRPCAddr = "persys-automation:8091"
	}
	if strings.TrimSpace(c.Automation.GRPCServerName) == "" {
		c.Automation.GRPCServerName = "persys-automation.persys.local"
	}
	if strings.TrimSpace(c.Automation.RequestTimeout) == "" {
		c.Automation.RequestTimeout = "15s"
	}
	if strings.TrimSpace(c.Intelligence.HTTPAddr) == "" {
		c.Intelligence.HTTPAddr = "http://persys-intelligence:8093"
	}
	if strings.TrimSpace(c.Intelligence.RequestTimeout) == "" {
		c.Intelligence.RequestTimeout = "10s"
	}
	if strings.TrimSpace(c.App.OAuthRedirectURL) == "" {
		c.App.OAuthRedirectURL = "http://persys-gateway:8585/auth"
	}
	if strings.TrimSpace(c.Vault.AuthMethod) == "" {
		c.Vault.AuthMethod = "approle"
	}
	if strings.TrimSpace(c.Vault.ManagerAddr) == "" {
		c.Vault.ManagerAddr = "vault-manager:50069"
	}
	if c.Vault.CertTTL == time.Duration(0) {
		c.Vault.CertTTL = 24 * time.Hour
	}
	if c.Vault.RetryInterval == time.Duration(0) {
		c.Vault.RetryInterval = 30 * time.Second
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
	if strings.TrimSpace(c.TLS.CertPath) == "" || strings.TrimSpace(c.TLS.KeyPath) == "" || strings.TrimSpace(c.TLS.CAPath) == "" {
		return fmt.Errorf("tls.cert_path, tls.key_path and tls.ca_path are required")
	}
	return nil
}

func (c *Config) applyEnvOverrides() {
	// Auth
	c.App.JWTSecret = envOrFile("PERSYS_GATEWAY_JWT_SECRET", c.App.JWTSecret)

	// Database
	c.Database.DSN = envOrFile("PERSYS_GATEWAY_POSTGRES_DSN", c.Database.DSN)

	// TLS paths
	c.TLS.CertPath = envOrFile("PERSYS_GATEWAY_TLS_CERT_PATH", c.TLS.CertPath)
	c.TLS.KeyPath = envOrFile("PERSYS_GATEWAY_TLS_KEY_PATH", c.TLS.KeyPath)
	c.TLS.CAPath = envOrFile("PERSYS_GATEWAY_TLS_CA_PATH", c.TLS.CAPath)

	// Vault
	c.Vault.Addr = envOrFile("PERSYS_GATEWAY_VAULT_ADDR", c.Vault.Addr)
	c.Vault.ManagerAddr = envOrFile("PERSYS_VAULT_MANAGER_ADDR", c.Vault.ManagerAddr)
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

	// Automation routing
	c.Automation.GRPCAddr = envOrFile("PERSYS_GATEWAY_AUTOMATION_GRPC_ADDR", c.Automation.GRPCAddr)
	c.Automation.GRPCServerName = envOrFile("PERSYS_GATEWAY_AUTOMATION_GRPC_SERVER_NAME", c.Automation.GRPCServerName)

	// Intelligence routing
	c.Intelligence.HTTPAddr = envOrFile("PERSYS_GATEWAY_INTELLIGENCE_HTTP_ADDR", c.Intelligence.HTTPAddr)

	// Telemetry
	c.Telemetry.OTLPEndpoint = envOrFile("PERSYS_GATEWAY_OTLP_ENDPOINT", c.Telemetry.OTLPEndpoint)
	if c.Telemetry.OTLPEndpoint == "" {
		c.Telemetry.OTLPEndpoint = envOrFile("OTEL_EXPORTER_OTLP_ENDPOINT", c.Telemetry.OTLPEndpoint)
	}
	if c.Telemetry.OTLPEndpoint == "" {
		c.Telemetry.OTLPEndpoint = envOrFile("OTEL_EXPORTER_JAEGER_ENDPOINT", c.Telemetry.OTLPEndpoint)
	}
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

func randomHexSecret(numBytes int) (string, error) {
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
