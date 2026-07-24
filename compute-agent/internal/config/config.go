package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/persys-dev/persys-cloud/compute-agent/internal/node"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Config holds all agent configuration
type Config struct {
	// Server configuration
	GRPCAddr    string `mapstructure:"grpc_addr"`
	GRPCPort    int    `mapstructure:"grpc_port"`
	MetricsPort int    `mapstructure:"metrics_port"`

	// TLS/mTLS configuration
	TLSEnabled  bool   `mapstructure:"tls_enabled"`
	TLSCertPath string `mapstructure:"tls_cert_path"`
	TLSKeyPath  string `mapstructure:"tls_key_path"`
	TLSCAPath   string `mapstructure:"tls_ca_path"`

	// Vault certificate manager configuration
	VaultEnabled       bool          `mapstructure:"vault_enabled"`
	VaultManagerAddr   string        `mapstructure:"vault_manager_addr"`
	VaultAddr          string        `mapstructure:"vault_addr"`
	VaultAuthMethod    string        `mapstructure:"vault_auth_method"`
	VaultToken         string        `mapstructure:"vault_token"`
	VaultAppRoleID     string        `mapstructure:"vault_approle_role_id"`
	VaultAppSecretID   string        `mapstructure:"vault_approle_secret_id"`
	VaultPKIMount      string        `mapstructure:"vault_pki_mount"`
	VaultPKIRole       string        `mapstructure:"vault_pki_role"`
	VaultCertTTL       time.Duration `mapstructure:"vault_cert_ttl"`
	VaultServiceName   string        `mapstructure:"vault_service_name"`
	VaultServiceDomain string        `mapstructure:"vault_service_domain"`
	VaultRetryInterval time.Duration `mapstructure:"vault_retry_interval"`

	// State store configuration
	StateStorePath string `mapstructure:"state_store_path"`

	// Runtime configuration
	DockerEnabled  bool   `mapstructure:"docker_enabled"`
	DockerEndpoint string `mapstructure:"docker_endpoint"`
	ComposeEnabled bool   `mapstructure:"compose_enabled"`
	ComposeBinary  string `mapstructure:"compose_binary"`
	VMEnabled      bool   `mapstructure:"vm_enabled"`
	LibvirtURI     string `mapstructure:"libvirt_uri"`

	// Managed storage provider configuration
	StorageLocalRoot    string `mapstructure:"storage_local_root"`
	StorageNFSStageDir  string `mapstructure:"storage_nfs_stage_dir"`
	StorageNFSServer    string `mapstructure:"storage_nfs_server"`
	StorageNFSExport    string `mapstructure:"storage_nfs_export"`
	StorageNFSOptions   string `mapstructure:"storage_nfs_options"`
	StorageCephStageDir string `mapstructure:"storage_ceph_stage_dir"`
	StorageCephCluster  string `mapstructure:"storage_ceph_cluster"`
	StorageCephPool     string `mapstructure:"storage_ceph_pool"`
	StorageCephUser     string `mapstructure:"storage_ceph_user"`
	StorageCephKeyring  string `mapstructure:"storage_ceph_keyring"`

	// Reconciliation configuration
	ReconcileInterval time.Duration `mapstructure:"reconcile_interval"`
	ReconcileEnabled  bool          `mapstructure:"reconcile_enabled"`

	// Logging
	LogLevel string `mapstructure:"log_level"`

	// Agent metadata
	NodeID     string            `mapstructure:"node_id"`
	Version    string            `mapstructure:"version"`
	NodeRegion string            `mapstructure:"node_region"`
	NodeEnv    string            `mapstructure:"node_env"`
	NodeLabels map[string]string `mapstructure:"node_labels"`

	// Scheduler control-plane configuration
	SchedulerAddr       string `mapstructure:"scheduler_addr"`
	SchedulerInsecure   bool   `mapstructure:"scheduler_insecure"`
	SchedulerTLSEnabled bool
	AgentGRPCEndpoint   string `mapstructure:"agent_grpc_endpoint"`

	// OpenTelemetry configuration
	OTELExporterEndpoint string `mapstructure:"otlp_endpoint"`
}

var (
	fs = pflag.NewFlagSet("compute-agent", pflag.ContinueOnError)
)

// Load loads configuration.
//
// Precedence (highest to lowest), per key:
//
//  1. PERSYS_NODE_LABELS parsing (handled explicitly, see below)
//  2. environment variables (PERSYS_*)
//  3. config file (agent_config.yaml), if one is found
//  4. built-in defaults (defaultConfig())
//
// This is Viper's own override > flag > env > config > default precedence.
// The important bit that makes it actually work is registerDefaults: Viper's
// AutomaticEnv only kicks in, during Unmarshal, for keys it already knows
// about (from a config file, an explicit BindEnv, or a SetDefault). A key
// with no default and no config-file entry is invisible to Unmarshal even if
// the matching PERSYS_* env var is set. Registering every field's default
// up front is what lets "no config file -> use env, else use default" and
// "config file present -> only fill in what env didn't set" both fall out of
// Viper's normal per-key resolution, instead of us re-implementing it by
// hand (which is what the old cfg = defaultConfig() wholesale-replace, and
// the applyMinimalDefaults zero-value merge, both did - and did buggily).
func Load() (*Config, error) {
	v := viper.New()

	v.SetConfigName("agent_config")
	v.SetConfigType("yaml")
	v.SetEnvPrefix("PERSYS")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))

	// Register every field's default so Viper knows about the key and will
	// resolve it as env > config file > default, instead of silently
	// ignoring the env var because Unmarshal never saw the key.
	registerDefaults(v, defaultConfig())

	// state_store_path historically also accepted PERSYS_STATE_PATH.
	if err := v.BindEnv("state_store_path", "PERSYS_STATE_PATH", "PERSYS_STATE_STORE_PATH"); err != nil {
		return nil, fmt.Errorf("bind state_store_path env: %w", err)
	}

	// PERSYS_NODE_LABELS is a comma-separated key=value list, not something
	// Viper can cast on its own. Decode it by hand and Set() it - Set()
	// outranks every other source, which is what we want: an explicit label
	// list on the env should never be partially clobbered by a config file.
	if labelsEnv := os.Getenv("PERSYS_NODE_LABELS"); labelsEnv != "" {
		v.Set("node_labels", parseLabelsEnv(labelsEnv))
	}

	// Bind CLI flag safely (Load can be called more than once, e.g. in tests).
	if fs.Lookup("config") == nil {
		fs.String("config", "", "Path to config file")
	}
	if err := fs.Parse(os.Args[1:]); err != nil && err != pflag.ErrHelp {
		return nil, fmt.Errorf("parse flags: %w", err)
	}

	// Determine config file location.
	var configFile string
	if f := fs.Lookup("config").Value.String(); f != "" {
		configFile = f
	} else if f = os.Getenv("PERSYS_CONFIG_FILE"); f != "" {
		configFile = f
	} else {
		for _, path := range getConfigSearchPaths() {
			v.AddConfigPath(path)
		}
	}

	configSrc := "defaults + env"

	if configFile != "" {
		// An explicitly-named file must exist and parse - fail loudly if not.
		v.SetConfigFile(configFile)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("failed to read specified config file %s: %w", configFile, err)
		}
		configSrc = configFile
	} else if err := v.ReadInConfig(); err == nil {
		// No file was named, but Viper found one on the search path.
		configSrc = v.ConfigFileUsed()
	} else if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
		// Found a file but couldn't parse it - that's a real error.
		return nil, fmt.Errorf("config file error: %w", err)
	} else {
		fmt.Println("ℹ️ No config file found → using ENV + defaults")
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	// --- Post-processing: derived fields that don't come from any source ---

	cfg.SchedulerTLSEnabled = !cfg.SchedulerInsecure

	if cfg.NodeID == "" {
		cfg.NodeID = generateNodeID()
	}

	if cfg.NodeLabels == nil {
		cfg.NodeLabels = make(map[string]string)
	}
	cfg.NodeLabels = mergeWithDefaultLabels(cfg.NodeLabels)
	cfg.NodeLabels = parseNodeLabels(cfg.NodeRegion, cfg.NodeEnv, cfg.NodeLabels)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	fmt.Printf("✅ Config loaded from: %s | NodeID: %s\n", configSrc, cfg.NodeID)
	return cfg, nil
}

// registerDefaults tells Viper about every configurable key and its default
// value. It must run before ReadInConfig/Unmarshal: this is what makes
// AutomaticEnv actually apply per-key during Unmarshal (see the comment on
// Load), and it's also what makes a partial/empty config file behave as
// "fill in the blanks" rather than clobbering everything else with zero
// values.
func registerDefaults(v *viper.Viper, def *Config) {
	v.SetDefault("grpc_addr", def.GRPCAddr)
	v.SetDefault("grpc_port", def.GRPCPort)
	v.SetDefault("metrics_port", def.MetricsPort)

	v.SetDefault("tls_enabled", def.TLSEnabled)
	v.SetDefault("tls_cert_path", def.TLSCertPath)
	v.SetDefault("tls_key_path", def.TLSKeyPath)
	v.SetDefault("tls_ca_path", def.TLSCAPath)

	v.SetDefault("vault_enabled", def.VaultEnabled)
	v.SetDefault("vault_manager_addr", def.VaultManagerAddr)
	v.SetDefault("vault_addr", def.VaultAddr)
	v.SetDefault("vault_auth_method", def.VaultAuthMethod)
	v.SetDefault("vault_token", def.VaultToken)
	v.SetDefault("vault_approle_role_id", def.VaultAppRoleID)
	v.SetDefault("vault_approle_secret_id", def.VaultAppSecretID)
	v.SetDefault("vault_pki_mount", def.VaultPKIMount)
	v.SetDefault("vault_pki_role", def.VaultPKIRole)
	v.SetDefault("vault_cert_ttl", def.VaultCertTTL)
	v.SetDefault("vault_service_name", def.VaultServiceName)
	v.SetDefault("vault_service_domain", def.VaultServiceDomain)
	v.SetDefault("vault_retry_interval", def.VaultRetryInterval)

	v.SetDefault("state_store_path", def.StateStorePath)

	v.SetDefault("docker_enabled", def.DockerEnabled)
	v.SetDefault("docker_endpoint", def.DockerEndpoint)
	v.SetDefault("compose_enabled", def.ComposeEnabled)
	v.SetDefault("compose_binary", def.ComposeBinary)
	v.SetDefault("vm_enabled", def.VMEnabled)
	v.SetDefault("libvirt_uri", def.LibvirtURI)

	v.SetDefault("storage_local_root", def.StorageLocalRoot)
	v.SetDefault("storage_nfs_stage_dir", def.StorageNFSStageDir)
	v.SetDefault("storage_nfs_server", def.StorageNFSServer)
	v.SetDefault("storage_nfs_export", def.StorageNFSExport)
	v.SetDefault("storage_nfs_options", def.StorageNFSOptions)
	v.SetDefault("storage_ceph_stage_dir", def.StorageCephStageDir)
	v.SetDefault("storage_ceph_cluster", def.StorageCephCluster)
	v.SetDefault("storage_ceph_pool", def.StorageCephPool)
	v.SetDefault("storage_ceph_user", def.StorageCephUser)
	v.SetDefault("storage_ceph_keyring", def.StorageCephKeyring)

	v.SetDefault("reconcile_interval", def.ReconcileInterval)
	v.SetDefault("reconcile_enabled", def.ReconcileEnabled)

	v.SetDefault("log_level", def.LogLevel)

	v.SetDefault("node_id", def.NodeID)
	v.SetDefault("version", def.Version)
	v.SetDefault("node_region", def.NodeRegion)
	v.SetDefault("node_env", def.NodeEnv)
	v.SetDefault("node_labels", def.NodeLabels)

	v.SetDefault("scheduler_addr", def.SchedulerAddr)
	v.SetDefault("scheduler_insecure", def.SchedulerInsecure)
	v.SetDefault("agent_grpc_endpoint", def.AgentGRPCEndpoint)

	v.SetDefault("otlp_endpoint", def.OTELExporterEndpoint)
}

// getConfigSearchPaths returns possible locations for agent_config.yaml
func getConfigSearchPaths() []string {
	paths := []string{"/etc/persys"}
	if os.Geteuid() != 0 {
		if home, err := os.UserHomeDir(); err == nil {
			paths = append(paths, filepath.Join(home, ".persys"))
		}
	}
	paths = append(paths, ".")
	return paths
}

// defaultConfig returns a Config with sensible defaults
func defaultConfig() *Config {
	return &Config{
		GRPCAddr:    "0.0.0.0",
		GRPCPort:    50051,
		MetricsPort: 8089,

		TLSEnabled:  true,
		TLSCertPath: "/etc/persys/certs/agent/compute-agent.pem",
		TLSKeyPath:  "/etc/persys/certs/agent/compute-agent-key.pem",
		TLSCAPath:   "/etc/persys/certs/agent/ca.pem",

		VaultEnabled:       false,
		VaultManagerAddr:   "vault-manager:50069",
		VaultAddr:          "http://vault:8200",
		VaultAuthMethod:    "approle",
		VaultPKIMount:      "pki",
		VaultPKIRole:       "compute-agent",
		VaultCertTTL:       24 * time.Hour,
		VaultRetryInterval: 2 * time.Minute,

		StateStorePath: "/var/lib/persys/state.db",

		DockerEnabled:  true,
		DockerEndpoint: "unix:///var/run/docker.sock",
		ComposeEnabled: true,
		ComposeBinary:  "docker compose",
		VMEnabled:      true,
		LibvirtURI:     "qemu:///system",

		StorageLocalRoot:    "/var/lib/persys/volumes/local",
		StorageNFSStageDir:  "/var/lib/persys/volumes/nfs",
		StorageCephStageDir: "/var/lib/persys/volumes/ceph-rbd",

		ReconcileInterval: 30 * time.Second,
		ReconcileEnabled:  true,

		LogLevel: "info",

		SchedulerAddr:     "persys-scheduler:8085",
		SchedulerInsecure: false,
	}
}

// Validate ensures config correctness
func (c *Config) Validate() error {
	if c.GRPCPort < 1 || c.GRPCPort > 65535 {
		return fmt.Errorf("invalid GRPC port: %d", c.GRPCPort)
	}
	if c.MetricsPort < 1 || c.MetricsPort > 65535 {
		return fmt.Errorf("invalid metrics port: %d", c.MetricsPort)
	}
	if c.TLSEnabled {
		if c.TLSCertPath == "" || c.TLSKeyPath == "" || c.TLSCAPath == "" {
			return fmt.Errorf("TLS enabled but certificate paths not configured")
		}
	}
	if c.VaultEnabled {
		if !c.TLSEnabled {
			return fmt.Errorf("vault requires TLS enabled")
		}
		if c.VaultAddr == "" {
			return fmt.Errorf("vault enabled but addr is empty")
		}
		switch strings.ToLower(c.VaultAuthMethod) {
		case "token":
			if c.VaultToken == "" {
				return fmt.Errorf("vault token auth selected but token is empty")
			}
		case "approle":
			if c.VaultAppRoleID == "" || c.VaultAppSecretID == "" {
				// return fmt.Errorf("vault approle auth selected but role_id/secret_id missing")
			}
		default:
			return fmt.Errorf("unsupported vault auth method %q", c.VaultAuthMethod)
		}
		if c.VaultCertTTL <= 0 {
			return fmt.Errorf("vault cert TTL must be positive")
		}
		if c.VaultRetryInterval <= 0 {
			return fmt.Errorf("vault retry interval must be positive")
		}
	}
	if !c.DockerEnabled && !c.ComposeEnabled && !c.VMEnabled {
		return fmt.Errorf("at least one runtime must be enabled")
	}
	if c.SchedulerAddr == "" {
		return fmt.Errorf("scheduler address cannot be empty")
	}
	return nil
}

// mergeWithDefaultLabels adds os/arch labels if not already present.
func mergeWithDefaultLabels(labels map[string]string) map[string]string {
	defaults := map[string]string{
		"os":   runtime.GOOS,
		"arch": runtime.GOARCH,
	}
	for k, v := range defaults {
		if _, exists := labels[k]; !exists {
			labels[k] = v
		}
	}
	return labels
}

// parseNodeLabels merges region/env (region/env take precedence over
// whatever was already in raw, since they're the canonical fields).
func parseNodeLabels(region, env string, raw map[string]string) map[string]string {
	if raw == nil {
		raw = make(map[string]string)
	}
	if region != "" {
		raw["region"] = region
	}
	if env != "" {
		raw["env"] = env
	}
	return raw
}

// generateNodeID creates unique node identifier
func generateNodeID() string {
	id, err := node.GenerateUniqueNodeID()
	if err != nil {
		id = getHostname()
	}
	return id
}

func getHostname() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "unknown"
}

// parseLabelsEnv parses comma-separated key=value pairs, skips invalid ones.
func parseLabelsEnv(s string) map[string]string {
	labels := make(map[string]string)
	if s == "" {
		return labels
	}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		if idx := strings.Index(pair, "="); idx > 0 {
			k := strings.TrimSpace(pair[:idx])
			val := strings.TrimSpace(pair[idx+1:])
			if k != "" && val != "" {
				labels[k] = val
			}
		}
	}
	return labels
}
