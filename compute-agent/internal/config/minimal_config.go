package config

import "fmt"

// applyMinimalDefaults fills any missing (zero-value) fields from defaultConfig()
// after the config file + ENV have been unmarshaled.
func applyMinimalDefaults(cfg *Config) {
	def := defaultConfig()

	// === Strings ===
	if cfg.GRPCAddr == "" {
		cfg.GRPCAddr = def.GRPCAddr
	}
	if cfg.TLSCertPath == "" {
		cfg.TLSCertPath = def.TLSCertPath
	}
	if cfg.TLSKeyPath == "" {
		cfg.TLSKeyPath = def.TLSKeyPath
	}
	if cfg.TLSCAPath == "" {
		cfg.TLSCAPath = def.TLSCAPath
	}
	if cfg.VaultManagerAddr == "" {
		cfg.VaultManagerAddr = def.VaultManagerAddr
	}
	if cfg.VaultAddr == "" {
		cfg.VaultAddr = def.VaultAddr
	}
	if cfg.VaultAuthMethod == "" {
		cfg.VaultAuthMethod = def.VaultAuthMethod
	}
	if cfg.VaultToken == "" {
		cfg.VaultToken = def.VaultToken
	}
	if cfg.VaultAppRoleID == "" {
		cfg.VaultAppRoleID = def.VaultAppRoleID
	}
	if cfg.VaultAppSecretID == "" {
		cfg.VaultAppSecretID = def.VaultAppSecretID
	}
	if cfg.VaultPKIMount == "" {
		cfg.VaultPKIMount = def.VaultPKIMount
	}
	if cfg.VaultPKIRole == "" {
		cfg.VaultPKIRole = def.VaultPKIRole
	}
	if cfg.VaultServiceName == "" {
		cfg.VaultServiceName = def.VaultServiceName
	}
	if cfg.VaultServiceDomain == "" {
		cfg.VaultServiceDomain = def.VaultServiceDomain
	}
	if cfg.StateStorePath == "" {
		cfg.StateStorePath = def.StateStorePath
	}
	if cfg.DockerEndpoint == "" {
		cfg.DockerEndpoint = def.DockerEndpoint
		fmt.Printf("DEBUG: DockerEndpoint was empty → set default\n")
	} else {
		fmt.Printf("DEBUG: Keeping DockerEndpoint from config file: %s\n", cfg.DockerEndpoint)
	}
	if cfg.ComposeBinary == "" {
		cfg.ComposeBinary = def.ComposeBinary
	}
	if cfg.LibvirtURI == "" {
		cfg.LibvirtURI = def.LibvirtURI
	}
	if cfg.StorageLocalRoot == "" {
		cfg.StorageLocalRoot = def.StorageLocalRoot
	}
	if cfg.StorageNFSStageDir == "" {
		cfg.StorageNFSStageDir = def.StorageNFSStageDir
	}
	if cfg.StorageNFSServer == "" {
		cfg.StorageNFSServer = def.StorageNFSServer
	}
	if cfg.StorageNFSExport == "" {
		cfg.StorageNFSExport = def.StorageNFSExport
	}
	if cfg.StorageNFSOptions == "" {
		cfg.StorageNFSOptions = def.StorageNFSOptions
	}
	if cfg.StorageCephStageDir == "" {
		cfg.StorageCephStageDir = def.StorageCephStageDir
	}
	if cfg.StorageCephCluster == "" {
		cfg.StorageCephCluster = def.StorageCephCluster
	}
	if cfg.StorageCephPool == "" {
		cfg.StorageCephPool = def.StorageCephPool
	}
	if cfg.StorageCephUser == "" {
		cfg.StorageCephUser = def.StorageCephUser
	}
	if cfg.StorageCephKeyring == "" {
		cfg.StorageCephKeyring = def.StorageCephKeyring
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = def.LogLevel
	}
	if cfg.NodeID == "" {
		cfg.NodeID = def.NodeID
	}
	if cfg.NodeRegion == "" {
		cfg.NodeRegion = def.NodeRegion
	}
	if cfg.NodeEnv == "" {
		cfg.NodeEnv = def.NodeEnv
	}
	if cfg.SchedulerAddr == "" {
		cfg.SchedulerAddr = def.SchedulerAddr
	}
	if cfg.AgentGRPCEndpoint == "" {
		cfg.AgentGRPCEndpoint = def.AgentGRPCEndpoint
	}
	if cfg.OTELExporterEndpoint == "" {
		cfg.OTELExporterEndpoint = def.OTELExporterEndpoint
	}

	// === Integers ===
	if cfg.GRPCPort == 0 {
		cfg.GRPCPort = def.GRPCPort
	}
	if cfg.MetricsPort == 0 {
		cfg.MetricsPort = def.MetricsPort
	}

	// === Booleans ===
	if !cfg.TLSEnabled {
		cfg.TLSEnabled = def.TLSEnabled
	}
	if !cfg.VaultEnabled {
		cfg.VaultEnabled = def.VaultEnabled
	}
	if !cfg.DockerEnabled {
		cfg.DockerEnabled = def.DockerEnabled
	}
	if !cfg.ComposeEnabled {
		cfg.ComposeEnabled = def.ComposeEnabled
	}
	if !cfg.VMEnabled {
		cfg.VMEnabled = def.VMEnabled
	}
	if !cfg.ReconcileEnabled {
		cfg.ReconcileEnabled = def.ReconcileEnabled
	}
	if !cfg.SchedulerInsecure {
		cfg.SchedulerInsecure = def.SchedulerInsecure
	}

	// === Durations ===
	if cfg.VaultCertTTL == 0 {
		cfg.VaultCertTTL = def.VaultCertTTL
	}
	if cfg.VaultRetryInterval == 0 {
		cfg.VaultRetryInterval = def.VaultRetryInterval
	}
	if cfg.ReconcileInterval == 0 {
		cfg.ReconcileInterval = def.ReconcileInterval
	}

}