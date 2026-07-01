// Package policy manages Vault ACL policies: one per provisioned service,
// plus the broader policy used by the bootstrap manager AppRole.
package policy

import (
	"fmt"

	vault "github.com/hashicorp/vault/api"

	"github.com/persys-dev/persys-cloud/vault-manager/internal/config"
)

// EnsureServicePolicies creates (or updates) one ACL policy per configured
// service, scoped to issuing certificates from the root PKI mount.
func EnsureServicePolicies(client *vault.Client, cfg *config.Config) error {
	for _, svc := range cfg.ServiceNames {
		policyName := ServiceName(svc)
		policyDoc := fmt.Sprintf(`path "%s/issue/%s" {
  capabilities = ["update"]
}

path "%s/cert/ca" {
  capabilities = ["read"]
}

path "%s/cert/ca_chain" {
  capabilities = ["read"]
}

path "%s/crl" {
  capabilities = ["read"]
}
`, cfg.PKIRootMount, svc, cfg.PKIRootMount, cfg.PKIRootMount, cfg.PKIRootMount)

		if err := client.Sys().PutPolicy(policyName, policyDoc); err != nil {
			return fmt.Errorf("ensure policy %q: %w", policyName, err)
		}
	}
	config.Log.Println("Service policies ensured.")
	return nil
}

// EnsureManagerPolicy creates the broad ACL policy used by the bootstrap
// manager AppRole when running in --secure mode.
func EnsureManagerPolicy(client *vault.Client, cfg *config.Config) error {
	policyDoc := fmt.Sprintf(`path "sys/mounts" {
  capabilities = ["read"]
}

path "sys/mounts/*" {
  capabilities = ["create", "read", "update", "delete", "list", "sudo"]
}

path "sys/auth" {
  capabilities = ["read"]
}

path "sys/auth/*" {
  capabilities = ["create", "read", "update", "delete", "list", "sudo"]
}

path "sys/policies/acl/*" {
  capabilities = ["create", "read", "update", "delete", "list"]
}

path "%s/*" {
  capabilities = ["create", "read", "update", "delete", "list"]
}

path "auth/approle/*" {
  capabilities = ["create", "read", "update", "delete", "list"]
}
`, cfg.PKIRootMount)

	policyDoc += fmt.Sprintf(`
path "%s/*" {
  capabilities = ["create", "read", "update", "delete", "list"]
}
`, cfg.PKIIntermediateMount)

	return client.Sys().PutPolicy(cfg.ManagerPolicyName, policyDoc)
}

// ServiceName returns the ACL policy name for a given service.
func ServiceName(service string) string {
	return service + "-policy"
}
