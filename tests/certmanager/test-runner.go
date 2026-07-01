package main

import (
	"context"
	"time"

	"github.com/persys-dev/persys-cloud/pkg/certmanager"
	"github.com/sirupsen/logrus"
)

type Config struct {
	TLSEnabled         bool
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

func initCertificates(ctx context.Context, cfg *Config) error {
	certCfg := certmanager.Config{
		TLSEnabled: 		cfg.TLSEnabled,
		VaultManagerAddr: 	cfg.VaultManagerAddr,
		VaultEnabled:       cfg.VaultEnabled,
		VaultAddr:          cfg.VaultAddr,
		VaultAuthMethod:    cfg.VaultAuthMethod,
		VaultToken:         cfg.VaultToken,
		VaultAppRoleID:     cfg.VaultAppRoleID,
		VaultAppSecretID:   cfg.VaultAppSecretID,
		VaultPKIMount:      cfg.VaultPKIMount,
		VaultPKIRole:       cfg.VaultPKIRole,
		VaultCertTTL:       cfg.VaultCertTTL,
		VaultServiceName:   cfg.VaultServiceName,
		VaultServiceDomain: cfg.VaultServiceDomain,
		VaultRetryInterval: cfg.VaultRetryInterval,

	}
	logger := logrus.New()
	manager := certmanager.NewManager(certCfg, logger)
	return manager.Start(ctx)
}

func main() {
	config := Config{
		TLSEnabled: true,
		VaultEnabled: true, 
		VaultManagerAddr: "localhost:50069",
		VaultAddr: "http://localhost:8200", 
		VaultAuthMethod: "approle",  
		VaultPKIMount: "pki", 
		VaultPKIRole: "persys-services", 
		VaultCertTTL: 24, 
		VaultServiceName: "persys-services", 
		VaultServiceDomain: "example.com", 
		VaultRetryInterval: 1,
	}
	
	logger := logrus.New()
	
	ctx := context.Background()
	err := initCertificates(ctx, &config)
	if err != nil {
		logger.Fatalf("Failed to initialize certificates: %v", err)
	}
}