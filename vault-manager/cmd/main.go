// Command vault-manager bootstraps Vault for Persys Cloud: it initializes
// and unseals Vault if needed, sets up the PKI CA chain, provisions
// per-service AppRoles and policies, then serves a gRPC API so other
// services can fetch or rotate their credentials at runtime.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	vault "github.com/hashicorp/vault/api"
	"github.com/sirupsen/logrus"

	"github.com/persys-dev/persys-cloud/vault-manager/internal/approle"
	"github.com/persys-dev/persys-cloud/vault-manager/internal/config"
	"github.com/persys-dev/persys-cloud/vault-manager/internal/pki"
	"github.com/persys-dev/persys-cloud/vault-manager/internal/policy"
	"github.com/persys-dev/persys-cloud/vault-manager/internal/server"
	"github.com/persys-dev/persys-cloud/vault-manager/internal/vaultclient"
)

func main() {
	cfg := config.ParseFlags()
	if len(cfg.ServiceNames) == 0 {
		config.Log.Fatal("no valid services found in --services")
	}

	baseClient, err := vaultclient.New(cfg.VaultAddr, "")
	if err != nil {
		config.Log.Fatal(err)
	}
	vaultclient.WaitUntilReady(baseClient)

	rootToken, err := bootstrapOrUnseal(cfg)
	if err != nil {
		config.Log.Fatal(err)
	}

	rootClient, err := vaultclient.New(cfg.VaultAddr, rootToken)
	if err != nil {
		config.Log.Fatal(err)
	}

	workClient := rootClient
	if cfg.Secure {
		config.Log.Println("--secure enabled: creating bootstrap AppRole and switching off root token")
		workClient, err = vaultclient.SwitchToSecure(rootClient, cfg)
		if err != nil {
			config.Log.Fatal(err)
		}
	}

	if err := provision(workClient, cfg); err != nil {
		config.Log.Fatal(err)
	}

	secrets, err := approle.GatherSecrets(workClient, cfg)
	if err != nil {
		config.Log.Fatal(err)
	}

	grpcLogger := logrus.New()
	grpcLogger.SetFormatter(&logrus.JSONFormatter{})

	go func() {
		if err := server.Start(workClient, config.GRPCListenAddr, grpcLogger); err != nil {
			config.Log.Fatalf("gRPC server failed: %v", err)
		}
	}()

	approle.PrintSummary(secrets)
	config.Log.Println("Vault bootstrap complete.")

	waitForShutdown()
}

// bootstrapOrUnseal initializes and unseals Vault if it hasn't been set up
// yet, then returns the root token to use for provisioning: the freshly
// generated one, or VAULT_ROOT_TOKEN if Vault was already initialized.
func bootstrapOrUnseal(cfg *config.Config) (string, error) {
	initialized, err := vaultclient.IsInitialized(cfg.VaultAddr)
	if err != nil {
		return "", err
	}

	if !initialized {
		config.Log.Println("Vault not initialized. Initializing...")
		initResult, err := vaultclient.Initialize(cfg.VaultAddr)
		if err != nil {
			return "", err
		}
		fmt.Println("Vault initialized credentials (store securely):")
		fmt.Printf("unseal_key: %s\n", initResult.UnsealKey)
		fmt.Printf("root_token: %s\n", initResult.RootToken)
		if err := vaultclient.Unseal(cfg.VaultAddr, initResult.UnsealKey); err != nil {
			return "", err
		}
		config.Log.Println("Vault initialized and unsealed.")
		return initResult.RootToken, nil
	}

	rootToken := strings.TrimSpace(os.Getenv("VAULT_ROOT_TOKEN"))
	if rootToken == "" {
		return "", fmt.Errorf("VAULT_ROOT_TOKEN required when Vault is already initialized")
	}
	return rootToken, nil
}

// provision ensures the PKI chain, service policies, and AppRoles all exist.
func provision(client *vault.Client, cfg *config.Config) error {
	if err := pki.Ensure(client, cfg); err != nil {
		return err
	}
	if err := pki.EnsureCAs(client, cfg); err != nil {
		return err
	}
	if err := pki.EnsureServiceRoles(client, cfg); err != nil {
		return err
	}
	if err := policy.EnsureServicePolicies(client, cfg); err != nil {
		return err
	}
	if err := approle.EnsureAuthMethod(client); err != nil {
		return err
	}
	if err := approle.EnsureServiceRoles(client, cfg); err != nil {
		return err
	}
	return nil
}

func waitForShutdown() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	config.Log.Println("VaultManager shutting down...")
}
