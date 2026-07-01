// Package approle manages the Vault approle auth method: enabling it,
// creating one role per service, and minting/rotating role/secret-id pairs.
package approle

import (
	"fmt"
	"sort"

	vault "github.com/hashicorp/vault/api"

	"github.com/persys-dev/persys-cloud/vault-manager/internal/config"
	"github.com/persys-dev/persys-cloud/vault-manager/internal/policy"
)

// ServiceSecret bundles the AppRole credentials and resulting client token
// issued for a single service during bootstrap.
type ServiceSecret struct {
	Service  string
	RoleID   string
	SecretID string
	Token    string
}

// EnsureAuthMethod enables the approle auth method if it isn't already mounted.
func EnsureAuthMethod(client *vault.Client) error {
	auths, err := client.Sys().ListAuth()
	if err != nil {
		return err
	}
	if _, ok := auths["approle/"]; ok {
		return nil
	}

	if err := client.Sys().EnableAuthWithOptions("approle", &vault.EnableAuthOptions{Type: "approle"}); err != nil {
		return err
	}

	config.Log.Println("AppRole auth method enabled.")
	return nil
}

// EnsureServiceRoles creates (or updates) one AppRole per configured service.
func EnsureServiceRoles(client *vault.Client, cfg *config.Config) error {
	for _, svc := range cfg.ServiceNames {
		_, err := client.Logical().Write("auth/approle/role/"+svc, map[string]interface{}{
			"token_policies":     []string{policy.ServiceName(svc)},
			"token_ttl":          "1h",
			"token_max_ttl":      "4h",
			"secret_id_ttl":      "24h",
			"secret_id_num_uses": 0,
		})
		if err != nil {
			return fmt.Errorf("ensure AppRole %q: %w", svc, err)
		}
	}
	config.Log.Println("Service AppRoles ensured.")
	return nil
}

// FetchRoleAndSecret reads a role's role_id and mints a fresh secret_id.
// Each call generates a new secret_id, so it also serves as the rotation
// path used by the gRPC API.
func FetchRoleAndSecret(client *vault.Client, roleName string) (string, string, error) {
	roleIDSecret, err := client.Logical().Read("auth/approle/role/" + roleName + "/role-id")
	if err != nil {
		return "", "", fmt.Errorf("read role-id for %q: %w", roleName, err)
	}
	if roleIDSecret == nil || roleIDSecret.Data == nil {
		return "", "", fmt.Errorf("role-id response empty for %q", roleName)
	}
	roleID, _ := roleIDSecret.Data["role_id"].(string)
	if roleID == "" {
		return "", "", fmt.Errorf("role-id missing for %q", roleName)
	}

	secretIDSecret, err := client.Logical().Write("auth/approle/role/"+roleName+"/secret-id", nil)
	if err != nil {
		return "", "", fmt.Errorf("generate secret-id for %q: %w", roleName, err)
	}
	if secretIDSecret == nil || secretIDSecret.Data == nil {
		return "", "", fmt.Errorf("secret-id response empty for %q", roleName)
	}
	secretID, _ := secretIDSecret.Data["secret_id"].(string)
	if secretID == "" {
		return "", "", fmt.Errorf("secret-id missing for %q", roleName)
	}

	return roleID, secretID, nil
}

// GatherSecrets logs in as every configured service and collects its
// AppRole credentials plus the resulting client token, for the bootstrap
// summary printed to the operator.
func GatherSecrets(client *vault.Client, cfg *config.Config) ([]ServiceSecret, error) {
	items := make([]ServiceSecret, 0, len(cfg.ServiceNames))
	for _, svc := range cfg.ServiceNames {
		roleID, secretID, err := FetchRoleAndSecret(client, svc)
		if err != nil {
			return nil, err
		}

		loginSecret, err := client.Logical().Write("auth/approle/login", map[string]interface{}{
			"role_id":   roleID,
			"secret_id": secretID,
		})
		if err != nil {
			return nil, fmt.Errorf("login approle for %q: %w", svc, err)
		}
		if loginSecret == nil || loginSecret.Auth == nil || loginSecret.Auth.ClientToken == "" {
			return nil, fmt.Errorf("empty token from approle login for %q", svc)
		}

		items = append(items, ServiceSecret{
			Service:  svc,
			RoleID:   roleID,
			SecretID: secretID,
			Token:    loginSecret.Auth.ClientToken,
		})
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Service < items[j].Service })
	return items, nil
}

// PrintSummary writes a human-readable dump of provisioned secrets to stdout.
func PrintSummary(items []ServiceSecret) {
	fmt.Println("\n=== Vault Provisioning Secrets ===")
	for _, s := range items {
		fmt.Printf("\n[%s]\n", s.Service)
		fmt.Printf("role_id: %s\n", s.RoleID)
		fmt.Printf("secret_id: %s\n", s.SecretID)
		fmt.Printf("token: %s\n", s.Token)
	}
	fmt.Println("\nKeep these values secure. Some may not be retrievable later.")
}
