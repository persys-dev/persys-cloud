package utils

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type RedisConfig struct {
	Addr                string `yaml:"addr"`
	Password            string `yaml:"password"`
	DB                  int    `yaml:"db"`
	BuildQueueKey       string `yaml:"build_queue_key"`
	WebhookQueueKey     string `yaml:"webhook_queue_key"`
	PipelineStatusQueue string `yaml:"pipeline_status_queue"`
}

type BuildConfig struct {
	Workspace string `yaml:"workspace"`
}

type GRPCConfig struct {
	Addr string `yaml:"addr"`
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
	KVPathPrefix  string `yaml:"kv_path_prefix"`
}

type GitHubConfig struct {
	WebhookURL    string `yaml:"webhook_url"`
	DefaultSecret string `yaml:"default_secret"`
}

type Config struct {
	MySQLDSN string       `yaml:"mysql_dsn"`
	Redis    RedisConfig  `yaml:"redis"`
	Build    BuildConfig  `yaml:"build"`
	GRPC     GRPCConfig   `yaml:"grpc"`
	TLS      TLSConfig    `yaml:"tls"`
	Vault    VaultConfig  `yaml:"vault"`
	GitHub   GitHubConfig `yaml:"github"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	cfg.applyEnvOverrides()
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.GRPC.Addr == "" {
		c.GRPC.Addr = ":8087"
	}
	if c.Redis.BuildQueueKey == "" {
		c.Redis.BuildQueueKey = "forge:builds"
	}
	if c.Redis.WebhookQueueKey == "" {
		c.Redis.WebhookQueueKey = "forge:webhooks"
	}
	if c.Redis.PipelineStatusQueue == "" {
		c.Redis.PipelineStatusQueue = "forge:pipeline-status"
	}
	if c.Build.Workspace == "" {
		c.Build.Workspace = "/tmp/forge-builds"
	}
	if c.Vault.AuthMethod == "" {
		c.Vault.AuthMethod = "token"
	}
	if c.Vault.CertTTL == "" {
		c.Vault.CertTTL = "24h"
	}
	if c.Vault.RetryInterval == "" {
		c.Vault.RetryInterval = "30s"
	}
	if c.Vault.ServiceName == "" {
		c.Vault.ServiceName = "persys-forgery"
	}
	if c.Vault.KVPathPrefix == "" {
		c.Vault.KVPathPrefix = "secret/data/persys/forgery/github"
	}
}

func (c *Config) applyEnvOverrides() {
	c.MySQLDSN = envOrFile("PERSYS_FORGERY_MYSQL_DSN", c.MySQLDSN)
	c.Redis.Addr = envOrFile("PERSYS_FORGERY_REDIS_ADDR", c.Redis.Addr)
	c.Redis.Password = envOrFile("PERSYS_FORGERY_REDIS_PASSWORD", c.Redis.Password)
	c.GRPC.Addr = envOrFile("PERSYS_FORGERY_GRPC_ADDR", c.GRPC.Addr)

	c.TLS.CertPath = envOrFile("PERSYS_FORGERY_TLS_CERT_PATH", c.TLS.CertPath)
	c.TLS.KeyPath = envOrFile("PERSYS_FORGERY_TLS_KEY_PATH", c.TLS.KeyPath)
	c.TLS.CAPath = envOrFile("PERSYS_FORGERY_TLS_CA_PATH", c.TLS.CAPath)

	c.Vault.Addr = envOrFile("PERSYS_FORGERY_VAULT_ADDR", c.Vault.Addr)
	c.Vault.AuthMethod = envOrFile("PERSYS_FORGERY_VAULT_AUTH_METHOD", c.Vault.AuthMethod)
	c.Vault.Token = envOrFile("PERSYS_FORGERY_VAULT_TOKEN", c.Vault.Token)
	c.Vault.AppRoleID = envOrFile("PERSYS_FORGERY_VAULT_ROLE_ID", c.Vault.AppRoleID)
	c.Vault.AppSecretID = envOrFile("PERSYS_FORGERY_VAULT_SECRET_ID", c.Vault.AppSecretID)

	c.GitHub.DefaultSecret = envOrFile("PERSYS_FORGERY_GITHUB_WEBHOOK_SECRET", c.GitHub.DefaultSecret)
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
