// Package pki manages Vault's PKI secrets engines: mounting the root and
// intermediate engines, generating/signing the CA chain, and creating
// per-service certificate-issuance roles.
package pki

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	vault "github.com/hashicorp/vault/api"

	"github.com/persys-dev/persys-cloud/vault-manager/internal/config"
)

// Ensure mounts the root and intermediate PKI secrets engines if they
// don't already exist.
func Ensure(client *vault.Client, cfg *config.Config) error {
	if err := ensureMount(client, cfg.PKIRootMount, "87600h"); err != nil {
		return err
	}
	if err := ensureMount(client, cfg.PKIIntermediateMount, "43800h"); err != nil {
		return err
	}
	return nil
}

func ensureMount(client *vault.Client, path string, maxTTL string) error {
	mounts, err := client.Sys().ListMounts()
	if err != nil {
		return err
	}

	if _, ok := mounts[path+"/"]; ok {
		config.Log.Printf("PKI mount %q already enabled.", path)
		return nil
	}

	if err := client.Sys().Mount(path, &vault.MountInput{Type: "pki"}); err != nil {
		return fmt.Errorf("enable PKI mount %q: %w", path, err)
	}
	if err := client.Sys().TuneMount(path, vault.MountConfigInput{MaxLeaseTTL: maxTTL}); err != nil {
		return fmt.Errorf("tune PKI mount %q: %w", path, err)
	}

	config.Log.Printf("PKI mount %q enabled.", path)
	return nil
}

// EnsureCAs generates the root CA (if missing) and signs an intermediate CA
// off of it (if missing).
func EnsureCAs(client *vault.Client, cfg *config.Config) error {
	hasRoot, err := hasCACert(client, cfg.PKIRootMount)
	if err != nil {
		return err
	}
	if !hasRoot {
		_, err := client.Logical().Write(cfg.PKIRootMount+"/root/generate/internal", map[string]interface{}{
			"common_name": cfg.RootCommonName,
			"ttl":         "87600h",
		})
		if err != nil {
			return fmt.Errorf("generate root CA: %w", err)
		}
		config.Log.Println("Generated Persys Cloud root CA.")
	} else {
		config.Log.Println("Persys Cloud root CA already present.")
	}
	if err := ensureDefaultIssuer(client, cfg.PKIRootMount); err != nil {
		return fmt.Errorf("ensure default issuer on %q: %w", cfg.PKIRootMount, err)
	}

	hasIntermediate, err := hasCACert(client, cfg.PKIIntermediateMount)
	if err != nil {
		return err
	}
	if hasIntermediate {
		config.Log.Println("Persys Cloud intermediate CA already present.")
		return nil
	}

	csrSecret, err := client.Logical().Write(cfg.PKIIntermediateMount+"/intermediate/generate/internal", map[string]interface{}{
		"common_name": cfg.IntCommonName,
		"ttl":         "43800h",
	})
	if err != nil {
		return fmt.Errorf("generate intermediate CSR: %w", err)
	}
	if csrSecret == nil || csrSecret.Data == nil {
		return errors.New("intermediate CSR response empty")
	}
	csr, _ := csrSecret.Data["csr"].(string)
	if csr == "" {
		return errors.New("intermediate CSR missing in response")
	}

	signed, err := client.Logical().Write(cfg.PKIRootMount+"/root/sign-intermediate", map[string]interface{}{
		"csr":         csr,
		"format":      "pem_bundle",
		"ttl":         "43800h",
		"common_name": cfg.IntCommonName,
	})
	if err != nil {
		return fmt.Errorf("sign intermediate CSR: %w", err)
	}
	if signed == nil || signed.Data == nil {
		return errors.New("signed intermediate response empty")
	}
	cert, _ := signed.Data["certificate"].(string)
	if cert == "" {
		return errors.New("signed intermediate certificate missing in response")
	}

	if _, err := client.Logical().Write(cfg.PKIIntermediateMount+"/intermediate/set-signed", map[string]interface{}{"certificate": cert}); err != nil {
		return fmt.Errorf("set signed intermediate: %w", err)
	}

	_, _ = client.Logical().Write(cfg.PKIIntermediateMount+"/config/urls", map[string]interface{}{
		"issuing_certificates":    fmt.Sprintf("%s/v1/%s/ca", cfg.VaultAddr, cfg.PKIIntermediateMount),
		"crl_distribution_points": fmt.Sprintf("%s/v1/%s/crl", cfg.VaultAddr, cfg.PKIIntermediateMount),
	})
	if err := ensureDefaultIssuer(client, cfg.PKIIntermediateMount); err != nil {
		return fmt.Errorf("ensure default issuer on %q: %w", cfg.PKIIntermediateMount, err)
	}

	config.Log.Println("Generated Persys Cloud intermediate CA.")
	return nil
}

func hasCACert(client *vault.Client, mount string) (bool, error) {
	secret, err := client.Logical().Read(mount + "/cert/ca")
	if err != nil {
		if isNoDefaultIssuerError(err) {
			return false, nil
		}
		return false, err
	}
	if secret == nil || secret.Data == nil {
		return false, nil
	}
	if cert, ok := secret.Data["certificate"].(string); ok && strings.TrimSpace(cert) != "" {
		return true, nil
	}
	return false, nil
}

func isNoDefaultIssuerError(err error) bool {
	var respErr *vault.ResponseError
	if !errors.As(err, &respErr) {
		return false
	}
	if respErr.StatusCode != http.StatusBadRequest {
		return false
	}
	for _, msg := range respErr.Errors {
		if strings.Contains(strings.ToLower(msg), "no default issuer") {
			return true
		}
	}
	return false
}

func ensureDefaultIssuer(client *vault.Client, mount string) error {
	issuerCfg, err := client.Logical().Read(mount + "/config/issuers")
	if err == nil && issuerCfg != nil && issuerCfg.Data != nil {
		if def, _ := issuerCfg.Data["default"].(string); strings.TrimSpace(def) != "" {
			return nil
		}
	}

	issuers, err := client.Logical().List(mount + "/issuers")
	if err != nil {
		return err
	}
	if issuers == nil || issuers.Data == nil {
		return nil
	}

	keysRaw, ok := issuers.Data["keys"]
	if !ok {
		return nil
	}
	keys, ok := keysRaw.([]interface{})
	if !ok || len(keys) == 0 {
		return nil
	}

	firstIssuer, _ := keys[0].(string)
	if strings.TrimSpace(firstIssuer) == "" {
		return nil
	}

	_, err = client.Logical().Write(mount+"/config/issuers", map[string]interface{}{
		"default": firstIssuer,
	})
	return err
}

// EnsureServiceRoles creates (or updates) one PKI role per configured
// service, used to issue leaf certificates.
func EnsureServiceRoles(client *vault.Client, cfg *config.Config) error {
	for _, svc := range cfg.ServiceNames {
		_, err := client.Logical().Write(cfg.PKIRootMount+"/roles/"+svc, map[string]interface{}{
			"allow_any_name":    true,
			"enforce_hostnames": false,
			"max_ttl":           "720h",
			"ttl":               "72h",
			"key_type":          "ec",
			"key_bits":          256,
		})
		if err != nil {
			return fmt.Errorf("ensure PKI role %q: %w", svc, err)
		}
	}
	config.Log.Println("Service PKI roles ensured.")
	return nil
}
