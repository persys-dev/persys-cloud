// Command vault-manager bootstraps Vault for Persys Cloud: it initializes
// and unseals Vault if needed, sets up the PKI CA chain, provisions
// per-service AppRoles and policies, then serves a gRPC API so other
// services can fetch or rotate their credentials at runtime.
//
// On first run the unseal key(s) and auth credentials are written to
// --bootstrap-file so subsequent restarts (of vault-manager or of Vault
// itself) can recover without operator intervention.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	vault "github.com/hashicorp/vault/api"
	"github.com/sirupsen/logrus"

	"github.com/persys-dev/persys-cloud/vault-manager/internal/approle"
	"github.com/persys-dev/persys-cloud/vault-manager/internal/bootstrap"
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

	// Sealed Vault is fine — we unseal from bootstrap state next.
	vaultclient.WaitUntilReady(cfg.VaultAddr)

	workClient, state, err := recoverOrBootstrap(cfg)
	if err != nil {
		config.Log.Fatal(err)
	}

	if err := provision(workClient, cfg); err != nil {
		config.Log.Fatal(err)
	}

	// Persist latest state after successful provision.
	if err := bootstrap.Save(cfg.BootstrapFile, state); err != nil {
		config.Log.Fatalf("save bootstrap state to %s: %v", cfg.BootstrapFile, err)
	}
	config.Log.Printf("Bootstrap state saved to %s", cfg.BootstrapFile)

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

// recoverOrBootstrap is the restart-safe entry point.
//
//  1. Load bootstrap file if present.
//  2. Uninitialized Vault → init, unseal, persist keys + root token.
//  3. Initialized + sealed → unseal with stored keys.
//  4. Authenticate: manager AppRole (preferred) → stored root token → VAULT_ROOT_TOKEN.
//  5. --secure without manager creds → hand off, revoke root, persist manager creds.
func recoverOrBootstrap(cfg *config.Config) (*vault.Client, *bootstrap.State, error) {
	state, err := bootstrap.Load(cfg.BootstrapFile)
	if err != nil && !errors.Is(err, bootstrap.ErrNotFound) {
		return nil, nil, fmt.Errorf("load bootstrap state from %s: %w", cfg.BootstrapFile, err)
	}
	if state == nil {
		state = &bootstrap.State{}
		config.Log.Printf("No bootstrap state at %s (first run or missing volume)", cfg.BootstrapFile)
	} else {
		config.Log.Printf("Loaded bootstrap state from %s (unseal_keys=%d manager=%v root=%v)",
			cfg.BootstrapFile, len(state.UnsealKeys), state.HasManagerCreds(), state.RootToken != "")
	}

	initialized, err := vaultclient.IsInitialized(cfg.VaultAddr)
	if err != nil {
		return nil, nil, err
	}

	if !initialized {
		return firstTimeInit(cfg, state)
	}

	if err := vaultclient.EnsureUnsealed(cfg.VaultAddr, state.UnsealKeys); err != nil {
		return nil, nil, err
	}

	client, err := authenticate(cfg, state)
	if err != nil {
		return nil, nil, err
	}

	if cfg.Secure && !state.HasManagerCreds() {
		config.Log.Println("--secure enabled: creating bootstrap AppRole and switching off root token")
		handoff, err := vaultclient.SwitchToSecure(client, cfg)
		if err != nil {
			return nil, nil, err
		}
		state.ManagerRoleID = handoff.RoleID
		state.ManagerSecretID = handoff.SecretID
		state.RootToken = ""
		client = handoff.Client
	}

	return client, state, nil
}

func firstTimeInit(cfg *config.Config, state *bootstrap.State) (*vault.Client, *bootstrap.State, error) {
	config.Log.Println("Vault not initialized. Initializing...")
	initResult, err := vaultclient.Initialize(cfg.VaultAddr)
	if err != nil {
		return nil, nil, err
	}

	fmt.Println("Vault initialized credentials (also saved to bootstrap file):")
	fmt.Printf("unseal_keys: %v\n", initResult.UnsealKeys)
	fmt.Printf("root_token: %s\n", initResult.RootToken)

	if err := vaultclient.UnsealAll(cfg.VaultAddr, initResult.UnsealKeys); err != nil {
		return nil, nil, err
	}
	config.Log.Println("Vault initialized and unsealed.")

	state.UnsealKeys = initResult.UnsealKeys
	state.RootToken = initResult.RootToken

	// Persist immediately so a crash between init and provision is recoverable.
	if err := bootstrap.Save(cfg.BootstrapFile, state); err != nil {
		return nil, nil, fmt.Errorf("save bootstrap state after init to %s: %w", cfg.BootstrapFile, err)
	}
	config.Log.Printf("Bootstrap state saved to %s", cfg.BootstrapFile)

	client, err := vaultclient.New(cfg.VaultAddr, initResult.RootToken)
	if err != nil {
		return nil, nil, err
	}

	if cfg.Secure {
		config.Log.Println("--secure enabled: creating bootstrap AppRole and switching off root token")
		handoff, err := vaultclient.SwitchToSecure(client, cfg)
		if err != nil {
			return nil, nil, err
		}
		state.ManagerRoleID = handoff.RoleID
		state.ManagerSecretID = handoff.SecretID
		state.RootToken = ""
		client = handoff.Client

		if err := bootstrap.Save(cfg.BootstrapFile, state); err != nil {
			return nil, nil, fmt.Errorf("save bootstrap state after secure handoff: %w", err)
		}
	}

	return client, state, nil
}

// authenticate picks the best available credential source.
//
// Priority:
//  1. Manager AppRole from bootstrap file (restart after --secure)
//  2. Root token from bootstrap file
//  3. VAULT_ROOT_TOKEN environment variable
func authenticate(cfg *config.Config, state *bootstrap.State) (*vault.Client, error) {
	if state.HasManagerCreds() {
		config.Log.Println("Authenticating with stored manager AppRole credentials")
		client, err := vaultclient.LoginAppRole(cfg.VaultAddr, state.ManagerRoleID, state.ManagerSecretID)
		if err != nil {
			return nil, fmt.Errorf("manager AppRole login: %w", err)
		}
		return client, nil
	}

	rootToken := strings.TrimSpace(state.RootToken)
	if rootToken == "" {
		rootToken = strings.TrimSpace(os.Getenv("VAULT_ROOT_TOKEN"))
	}
	if rootToken == "" {
		return nil, fmt.Errorf(
			"vault is initialized but no credentials available: "+
				"ensure %s contains unseal_keys and root_token/manager creds, "+
				"or set VAULT_ROOT_TOKEN (file missing usually means the volume was not persisted)",
			cfg.BootstrapFile,
		)
	}

	config.Log.Println("Authenticating with root token")
	return vaultclient.New(cfg.VaultAddr, rootToken)
}

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
