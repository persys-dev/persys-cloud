package vaultclient

import (
	"errors"
	"fmt"

	vault "github.com/hashicorp/vault/api"

	"github.com/persys-dev/persys-cloud/vault-manager/internal/approle"
	"github.com/persys-dev/persys-cloud/vault-manager/internal/config"
	"github.com/persys-dev/persys-cloud/vault-manager/internal/policy"
)

// SwitchToSecure provisions a bootstrap AppRole, logs in with it, then
// revokes the root token so all further provisioning runs without root
// privileges.
func SwitchToSecure(rootClient *vault.Client, cfg *config.Config) (*vault.Client, error) {
	if err := approle.EnsureAuthMethod(rootClient); err != nil {
		return nil, err
	}
	if err := policy.EnsureManagerPolicy(rootClient, cfg); err != nil {
		return nil, err
	}

	_, err := rootClient.Logical().Write("auth/approle/role/"+cfg.ManagerRoleName, map[string]interface{}{
		"token_policies": []string{cfg.ManagerPolicyName},
		"token_ttl":      "1h",
		"token_max_ttl":  "4h",
	})
	if err != nil {
		return nil, fmt.Errorf("ensure manager approle: %w", err)
	}

	roleID, secretID, err := approle.FetchRoleAndSecret(rootClient, cfg.ManagerRoleName)
	if err != nil {
		return nil, err
	}

	loginSecret, err := rootClient.Logical().Write("auth/approle/login", map[string]interface{}{
		"role_id":   roleID,
		"secret_id": secretID,
	})
	if err != nil {
		return nil, fmt.Errorf("approle login for manager failed: %w", err)
	}
	if loginSecret == nil || loginSecret.Auth == nil || loginSecret.Auth.ClientToken == "" {
		return nil, errors.New("approle login for manager returned empty token")
	}

	secureClient, err := New(cfg.VaultAddr, loginSecret.Auth.ClientToken)
	if err != nil {
		return nil, err
	}

	if _, err := rootClient.Logical().Write("auth/token/revoke-self", nil); err != nil {
		return nil, fmt.Errorf("failed to revoke root token: %w", err)
	}
	config.Log.Println("Root token revoked after secure AppRole handoff.")

	return secureClient, nil
}
