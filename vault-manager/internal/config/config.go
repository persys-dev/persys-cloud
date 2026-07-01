// Package config centralizes vault-manager's runtime settings: defaults,
// CLI flag parsing, and the shared bootstrap logger.
package config

import (
	"flag"
	"strings"

	"github.com/sirupsen/logrus"
)

const (
	DefaultVaultAddr            = "https://vault:8200"
	DefaultPKIRootMount         = "pki"
	DefaultPKIIntermediateMount = "pki_int"
	DefaultRootCommonName       = "Persys Cloud Root CA"
	DefaultIntCommonName        = "Persys Cloud Intermediate CA"
	DefaultManagerRoleName      = "vault-manager-bootstrap"
	DefaultManagerPolicyName    = "vault-manager-bootstrap-policy"
	DefaultServicesCSV          = "persys-gateway,persys-scheduler,persysctl,compute-agent,persys-forgery,persys-services,persys-automation,persys-intelligence,persys-sdk"
	GRPCListenAddr              = ":50069"
)

// Log is the shared logger used for bootstrap progress output across packages.
var Log = logrus.New()

// Config holds every runtime-tunable value for the vault-manager bootstrap process.
type Config struct {
	VaultAddr            string
	PKIRootMount         string
	PKIIntermediateMount string
	RootCommonName       string
	IntCommonName        string
	ManagerRoleName      string
	ManagerPolicyName    string
	ServiceNames         []string
	Secure               bool
}

// ParseFlags parses CLI flags into a Config. It calls flag.Parse() itself,
// so it must only be invoked once, from main.
func ParseFlags() *Config {
	cfg := &Config{}
	var servicesCSV string

	flag.BoolVar(&cfg.Secure, "secure", false, "use AppRole for all further provisioning and revoke root token")
	flag.StringVar(&cfg.VaultAddr, "vault-addr", DefaultVaultAddr, "Vault API address")
	flag.StringVar(&cfg.PKIRootMount, "pki-root-mount", DefaultPKIRootMount, "PKI root mount path")
	flag.StringVar(&cfg.PKIIntermediateMount, "pki-int-mount", DefaultPKIIntermediateMount, "PKI intermediate mount path")
	flag.StringVar(&cfg.RootCommonName, "root-cn", DefaultRootCommonName, "Root CA common name")
	flag.StringVar(&cfg.IntCommonName, "intermediate-cn", DefaultIntCommonName, "Intermediate CA common name")
	flag.StringVar(&cfg.ManagerRoleName, "manager-role", DefaultManagerRoleName, "Bootstrap manager AppRole name")
	flag.StringVar(&cfg.ManagerPolicyName, "manager-policy", DefaultManagerPolicyName, "Bootstrap manager policy name")
	flag.StringVar(&servicesCSV, "services", DefaultServicesCSV, "Comma-separated service names to provision")
	flag.Parse()

	cfg.ServiceNames = ParseServiceNames(servicesCSV)
	return cfg
}

// ParseServiceNames splits a comma-separated list, trims whitespace, drops
// empties, and de-duplicates while preserving order.
func ParseServiceNames(csv string) []string {
	parts := strings.Split(csv, ",")
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}
