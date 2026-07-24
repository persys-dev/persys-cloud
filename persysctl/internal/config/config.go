package config

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	APIEndpoint  string
	PrivateKey   *rsa.PrivateKey
	PublicKeyPEM string
	APIVersion   string
	ClusterID    string
	Transport         string
	GRPCEndpoint      string
	GRPCInsecure      bool
	GRPCTarget        string
	RPCTimeoutSeconds int

	// Certificate settings
	CACertPath   string
	CertPath     string
	KeyPath      string
	CFSSLApiURL  string
	CommonName   string
	Organization string

	// Vault certificate manager
	VaultEnabled       bool
	VaultManagerAddr   string
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
}

var logger *log.Logger

func InitLogger(verbose bool) {
	if verbose {
		logger = log.New(os.Stdout, "persys-cli: ", log.LstdFlags)
	} else {
		logger = log.New(os.Stdout, "", 0)
	}
}

func Logf(format string, v ...interface{}) {
	if logger != nil {
		logger.Printf(format, v...)
	}
}

func GetConfig() Config {
	cfg := Config{
		APIEndpoint: viper.GetString("api_endpoint"),
	}
	if cfg.APIEndpoint == "" {
		cfg.APIEndpoint = "http://localhost:8084"
		log.Printf("WARNING! No api_endpoint found in your config.yaml; defaulting to: %v", cfg.APIEndpoint)
	}

	cfg.APIVersion = viper.GetString("api_version")
	if cfg.APIVersion == "" {
		cfg.APIVersion = "v2"
		log.Printf("WARNING! No api_version found in your config.yaml; defaulting to: %v", cfg.APIVersion)
	}

	cfg.ClusterID = viper.GetString("cluster_id")
	if cfg.ClusterID == "" {
		cfg.ClusterID = "persys-genesis-a"
		log.Printf("WARNING! No cluster_id found in your config.yaml; some operations may fail.")
	}

	cfg.Transport = strings.ToLower(viper.GetString("transport"))
	if cfg.Transport == "" {
		cfg.Transport = "grpc"
	}

	cfg.GRPCEndpoint = viper.GetString("grpc_endpoint")
	if cfg.GRPCEndpoint == "" {
		cfg.GRPCEndpoint = "localhost:8085"
	}

	cfg.GRPCInsecure = viper.GetBool("grpc_insecure")
	cfg.GRPCTarget = strings.ToLower(viper.GetString("grpc_target"))
	if cfg.GRPCTarget == "" {
		cfg.GRPCTarget = "scheduler"
	}

	cfg.RPCTimeoutSeconds = viper.GetInt("rpc_timeout_seconds")
	if cfg.RPCTimeoutSeconds <= 0 {
		cfg.RPCTimeoutSeconds = 20
	}

	// Certificate settings
	cfg.CACertPath = viper.GetString("ca_cert_path")
	cfg.CertPath = viper.GetString("cert_path")
	cfg.KeyPath = viper.GetString("key_path")
	cfg.CFSSLApiURL = viper.GetString("cfssl_api_url")
	cfg.CommonName = viper.GetString("common_name")
	cfg.Organization = viper.GetString("organization")

	// Vault certificate settings
	cfg.VaultEnabled = boolWithEnv("vault_enabled", "PERSYS_VAULT_ENABLED", true)
	cfg.VaultAddr = strings.TrimSpace(stringWithEnv("vault_addr", "PERSYS_VAULT_ADDR"))
	if cfg.VaultAddr == "" {
		cfg.VaultAddr = "http://localhost:8200"
	}
	cfg.VaultAuthMethod = strings.ToLower(strings.TrimSpace(stringWithEnv("vault_auth_method", "PERSYS_VAULT_AUTH_METHOD")))
	if cfg.VaultAuthMethod == "" {
		cfg.VaultAuthMethod = "token"
	}
	cfg.VaultToken = strings.TrimSpace(stringWithEnv("vault_token", "PERSYS_VAULT_TOKEN"))
	cfg.VaultAppRoleID = strings.TrimSpace(stringWithEnv("vault_approle_role_id", "PERSYS_VAULT_APPROLE_ROLE_ID"))
	cfg.VaultAppSecretID = strings.TrimSpace(stringWithEnv("vault_approle_secret_id", "PERSYS_VAULT_APPROLE_SECRET_ID"))
	cfg.VaultPKIMount = strings.TrimSpace(stringWithEnv("vault_pki_mount", "PERSYS_VAULT_PKI_MOUNT"))
	if cfg.VaultPKIMount == "" {
		cfg.VaultPKIMount = "pki"
	}
	cfg.VaultPKIRole = strings.TrimSpace(stringWithEnv("vault_pki_role", "PERSYS_VAULT_PKI_ROLE"))
	if cfg.VaultPKIRole == "" {
		cfg.VaultPKIRole = "persysctl"
	}
	cfg.VaultCertTTL = durationOr(stringWithEnv("vault_cert_ttl", "PERSYS_VAULT_CERT_TTL"), 24*time.Hour)
	cfg.VaultServiceName = strings.TrimSpace(stringWithEnv("vault_service_name", "PERSYS_VAULT_SERVICE_NAME"))
	if cfg.VaultServiceName == "" {
		cfg.VaultServiceName = "persysctl"
	}
	cfg.VaultServiceDomain = strings.TrimSpace(stringWithEnv("vault_service_domain", "PERSYS_VAULT_SERVICE_DOMAIN"))
	cfg.VaultRetryInterval = durationOr(stringWithEnv("vault_retry_interval", "PERSYS_VAULT_RETRY_INTERVAL"), time.Minute)
	cfg.VaultManagerAddr = strings.TrimSpace(stringWithEnv("vault_manager_addr", "PERSYS_VAULT_MANAGER_ADDR"))

	// Set default paths if not specified
	if cfg.CACertPath == "" {
		cfg.CACertPath = filepath.Join(os.Getenv("HOME"), ".persys", "ca.pem")
	}
	if cfg.CertPath == "" {
		cfg.CertPath = filepath.Join(os.Getenv("HOME"), ".persys", "persysctl.pem")
	}
	if cfg.KeyPath == "" {
		cfg.KeyPath = filepath.Join(os.Getenv("HOME"), ".persys", "persysctl-key.pem")
	}

	privKeyStr := viper.GetString("private_key")
	if privKeyStr != "" {
		block, _ := pem.Decode([]byte(privKeyStr))
		if block == nil {
			log.Fatal("Failed to decode private key")
		}
		privKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			log.Fatal("Failed to parse private key: ", err)
		}
		cfg.PrivateKey = privKey

		pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
		if err != nil {
			log.Fatal("Failed to marshal public key: ", err)
		}
		pemBlock := &pem.Block{Type: "PUBLIC KEY", Bytes: pubKeyBytes}
		pemBuf := new(bytes.Buffer)
		err = pem.Encode(pemBuf, pemBlock)
		if err != nil {
			log.Fatal("Failed to encode public key: ", err)
		}
		cfg.PublicKeyPEM = hex.EncodeToString(pemBuf.Bytes())
	}

	return cfg
}

func durationOr(v string, fallback time.Duration) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func stringWithEnv(key, envName string) string {
	v := strings.TrimSpace(viper.GetString(key))
	if v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv(envName))
}

func boolWithEnv(key, envName string, fallback bool) bool {
	if viper.IsSet(key) {
		return viper.GetBool(key)
	}
	v := strings.TrimSpace(os.Getenv(envName))
	if v == "" {
		return fallback
	}
	return strings.EqualFold(v, "true")
}
