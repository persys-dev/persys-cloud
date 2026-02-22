package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	vault "github.com/hashicorp/vault/api"
)

const (
	defaultVaultAddr            = "https://vault:8200"
	defaultPKIRootMount         = "pki"
	defaultPKIIntermediateMount = "pki_int"
	defaultRootCommonName       = "Persys Cloud Root CA"
	defaultIntCommonName        = "Persys Cloud Intermediate CA"
	defaultManagerRoleName      = "vault-manager-bootstrap"
	defaultManagerPolicyName    = "vault-manager-bootstrap-policy"
	defaultServicesCSV          = "persys-gateway,persys-scheduler,persysctl,compute-agent,persys-forgery,persys-services"
)

var (
	vaultAddr            = defaultVaultAddr
	pkiRootMount         = defaultPKIRootMount
	pkiIntermediateMount = defaultPKIIntermediateMount
	rootCommonName       = defaultRootCommonName
	intCommonName        = defaultIntCommonName
	managerRoleName      = defaultManagerRoleName
	managerPolicyName    = defaultManagerPolicyName
	serviceNames         = []string{
		"persys-gateway",
		"persys-scheduler",
		"persysctl",
		"compute-agent",
		"persys-forgery",
		"persys-services",
	}
)

type serviceSecret struct {
	Service  string
	RoleID   string
	SecretID string
	Token    string
}

type vaultInitResult struct {
	RootToken string
	UnsealKey string
}

func main() {
	var servicesCSV string
	secure := flag.Bool("secure", false, "use AppRole for all further provisioning and revoke root token")
	flag.StringVar(&vaultAddr, "vault-addr", defaultVaultAddr, "Vault API address")
	flag.StringVar(&pkiRootMount, "pki-root-mount", defaultPKIRootMount, "PKI root mount path")
	flag.StringVar(&pkiIntermediateMount, "pki-int-mount", defaultPKIIntermediateMount, "PKI intermediate mount path")
	flag.StringVar(&rootCommonName, "root-cn", defaultRootCommonName, "Root CA common name")
	flag.StringVar(&intCommonName, "intermediate-cn", defaultIntCommonName, "Intermediate CA common name")
	flag.StringVar(&managerRoleName, "manager-role", defaultManagerRoleName, "Bootstrap manager AppRole name")
	flag.StringVar(&managerPolicyName, "manager-policy", defaultManagerPolicyName, "Bootstrap manager policy name")
	flag.StringVar(&servicesCSV, "services", defaultServicesCSV, "Comma-separated service names to provision")
	flag.Parse()

	serviceNames = parseServiceNames(servicesCSV)
	if len(serviceNames) == 0 {
		log.Fatal("no valid services found in --services")
	}

	baseClient, err := newVaultClient("")
	if err != nil {
		log.Fatal(err)
	}

	waitForVault(baseClient)

	initialized, err := isInitialized()
	if err != nil {
		log.Fatal(err)
	}

	var bootstrapToken string
	if !initialized {
		log.Println("Vault not initialized. Initializing...")
		initResult, err := initializeVault()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("Vault initialized credentials (store securely):")
		fmt.Printf("unseal_key: %s\n", initResult.UnsealKey)
		fmt.Printf("root_token: %s\n", initResult.RootToken)
		if err := unsealVault(initResult.UnsealKey); err != nil {
			log.Fatal(err)
		}
		bootstrapToken = initResult.RootToken
		log.Println("Vault initialized and unsealed.")
	}

	rootToken := strings.TrimSpace(os.Getenv("VAULT_ROOT_TOKEN"))
	if bootstrapToken != "" {
		rootToken = bootstrapToken
	}
	if rootToken == "" {
		log.Fatal("VAULT_ROOT_TOKEN required when Vault is already initialized")
	}

	rootClient, err := newVaultClient(rootToken)
	if err != nil {
		log.Fatal(err)
	}

	workClient := rootClient
	if *secure {
		log.Println("--secure enabled: creating bootstrap AppRole and switching off root token")
		workClient, err = switchToSecureClient(rootClient)
		if err != nil {
			log.Fatal(err)
		}
	}

	if err := ensurePKI(workClient); err != nil {
		log.Fatal(err)
	}
	if err := ensureRootAndIntermediateCA(workClient); err != nil {
		log.Fatal(err)
	}
	if err := ensureServicePKIRoles(workClient); err != nil {
		log.Fatal(err)
	}
	if err := ensurePolicies(workClient); err != nil {
		log.Fatal(err)
	}
	if err := ensureAppRoleAuth(workClient); err != nil {
		log.Fatal(err)
	}
	if err := ensureAppRoles(workClient); err != nil {
		log.Fatal(err)
	}

	secrets, err := gatherSecrets(workClient)
	if err != nil {
		log.Fatal(err)
	}

	printSecrets(secrets)
	log.Println("Vault bootstrap complete.")
}

func newVaultClient(token string) (*vault.Client, error) {
	cfg := vault.DefaultConfig()
	cfg.Address = vaultAddr
	client, err := vault.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	if token != "" {
		client.SetToken(token)
	}
	return client, nil
}

func waitForVault(client *vault.Client) {
	for {
		_, err := client.Sys().Health()
		if err == nil {
			return
		}
		log.Println("Waiting for Vault...")
		time.Sleep(2 * time.Second)
	}
}

func isInitialized() (bool, error) {
	resp, err := http.Get(vaultAddr + "/v1/sys/init")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	var data struct {
		Initialized bool `json:"initialized"`
	}
	err = json.NewDecoder(resp.Body).Decode(&data)
	return data.Initialized, err
}

func initializeVault() (*vaultInitResult, error) {
	body := map[string]interface{}{
		"secret_shares":    1,
		"secret_threshold": 1,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("PUT", vaultAddr+"/v1/sys/init", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("vault init failed with status %s", resp.Status)
	}

	var data struct {
		RootToken     string   `json:"root_token"`
		KeysBase64    []string `json:"keys_base64"`
		UnsealKeysB64 []string `json:"unseal_keys_b64"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode vault init response: %w", err)
	}

	unsealKeys := data.UnsealKeysB64
	if len(unsealKeys) == 0 {
		unsealKeys = data.KeysBase64
	}
	if data.RootToken == "" || len(unsealKeys) == 0 || strings.TrimSpace(unsealKeys[0]) == "" {
		return nil, errors.New("vault init response missing root token or unseal key")
	}

	return &vaultInitResult{
		RootToken: data.RootToken,
		UnsealKey: unsealKeys[0],
	}, nil
}

func unsealVault(unsealKey string) error {
	body := map[string]interface{}{
		"key": unsealKey,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", vaultAddr+"/v1/sys/unseal", bytes.NewBuffer(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("vault unseal failed with status %s", resp.Status)
	}
	return nil
}

func switchToSecureClient(rootClient *vault.Client) (*vault.Client, error) {
	if err := ensureAppRoleAuth(rootClient); err != nil {
		return nil, err
	}
	if err := ensureManagerPolicy(rootClient); err != nil {
		return nil, err
	}

	_, err := rootClient.Logical().Write("auth/approle/role/"+managerRoleName, map[string]interface{}{
		"token_policies": []string{managerPolicyName},
		"token_ttl":      "1h",
		"token_max_ttl":  "4h",
	})
	if err != nil {
		return nil, fmt.Errorf("ensure manager approle: %w", err)
	}

	roleID, secretID, err := fetchRoleAndSecret(rootClient, managerRoleName)
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

	secureClient, err := newVaultClient(loginSecret.Auth.ClientToken)
	if err != nil {
		return nil, err
	}

	if _, err := rootClient.Logical().Write("auth/token/revoke-self", nil); err != nil {
		return nil, fmt.Errorf("failed to revoke root token: %w", err)
	}
	log.Println("Root token revoked after secure AppRole handoff.")

	return secureClient, nil
}

func ensurePKI(client *vault.Client) error {
	if err := ensurePKIMount(client, pkiRootMount, "87600h"); err != nil {
		return err
	}
	if err := ensurePKIMount(client, pkiIntermediateMount, "43800h"); err != nil {
		return err
	}
	return nil
}

func ensurePKIMount(client *vault.Client, path string, maxTTL string) error {
	mounts, err := client.Sys().ListMounts()
	if err != nil {
		return err
	}

	if _, ok := mounts[path+"/"]; ok {
		log.Printf("PKI mount %q already enabled.", path)
		return nil
	}

	if err := client.Sys().Mount(path, &vault.MountInput{Type: "pki"}); err != nil {
		return fmt.Errorf("enable PKI mount %q: %w", path, err)
	}
	if err := client.Sys().TuneMount(path, vault.MountConfigInput{MaxLeaseTTL: maxTTL}); err != nil {
		return fmt.Errorf("tune PKI mount %q: %w", path, err)
	}

	log.Printf("PKI mount %q enabled.", path)
	return nil
}

func ensureRootAndIntermediateCA(client *vault.Client) error {
	hasRoot, err := hasCACert(client, pkiRootMount)
	if err != nil {
		return err
	}
	if !hasRoot {
		_, err := client.Logical().Write(pkiRootMount+"/root/generate/internal", map[string]interface{}{
			"common_name": rootCommonName,
			"ttl":         "87600h",
		})
		if err != nil {
			return fmt.Errorf("generate root CA: %w", err)
		}
		log.Println("Generated Persys Cloud root CA.")
	} else {
		log.Println("Persys Cloud root CA already present.")
	}
	if err := ensureDefaultIssuer(client, pkiRootMount); err != nil {
		return fmt.Errorf("ensure default issuer on %q: %w", pkiRootMount, err)
	}

	hasIntermediate, err := hasCACert(client, pkiIntermediateMount)
	if err != nil {
		return err
	}
	if hasIntermediate {
		log.Println("Persys Cloud intermediate CA already present.")
		return nil
	}

	csrSecret, err := client.Logical().Write(pkiIntermediateMount+"/intermediate/generate/internal", map[string]interface{}{
		"common_name": intCommonName,
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

	signed, err := client.Logical().Write(pkiRootMount+"/root/sign-intermediate", map[string]interface{}{
		"csr":         csr,
		"format":      "pem_bundle",
		"ttl":         "43800h",
		"common_name": intCommonName,
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

	if _, err := client.Logical().Write(pkiIntermediateMount+"/intermediate/set-signed", map[string]interface{}{"certificate": cert}); err != nil {
		return fmt.Errorf("set signed intermediate: %w", err)
	}

	_, _ = client.Logical().Write(pkiIntermediateMount+"/config/urls", map[string]interface{}{
		"issuing_certificates":    fmt.Sprintf("%s/v1/%s/ca", vaultAddr, pkiIntermediateMount),
		"crl_distribution_points": fmt.Sprintf("%s/v1/%s/crl", vaultAddr, pkiIntermediateMount),
	})
	if err := ensureDefaultIssuer(client, pkiIntermediateMount); err != nil {
		return fmt.Errorf("ensure default issuer on %q: %w", pkiIntermediateMount, err)
	}

	log.Println("Generated Persys Cloud intermediate CA.")
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
	cfg, err := client.Logical().Read(mount + "/config/issuers")
	if err == nil && cfg != nil && cfg.Data != nil {
		if def, _ := cfg.Data["default"].(string); strings.TrimSpace(def) != "" {
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

func ensureServicePKIRoles(client *vault.Client) error {
	for _, svc := range serviceNames {
		_, err := client.Logical().Write(pkiRootMount+"/roles/"+svc, map[string]interface{}{
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
	log.Println("Service PKI roles ensured.")
	return nil
}

func ensurePolicies(client *vault.Client) error {
	for _, svc := range serviceNames {
		policyName := servicePolicyName(svc)
		policy := fmt.Sprintf(`path "%s/issue/%s" {
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
`, pkiRootMount, svc, pkiRootMount, pkiRootMount, pkiRootMount)

		if err := client.Sys().PutPolicy(policyName, policy); err != nil {
			return fmt.Errorf("ensure policy %q: %w", policyName, err)
		}
	}
	log.Println("Service policies ensured.")
	return nil
}

func ensureManagerPolicy(client *vault.Client) error {
	policy := fmt.Sprintf(`path "sys/mounts" {
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
`, pkiRootMount)

	policy += fmt.Sprintf(`
path "%s/*" {
  capabilities = ["create", "read", "update", "delete", "list"]
}
`, pkiIntermediateMount)

	return client.Sys().PutPolicy(managerPolicyName, policy)
}

func ensureAppRoleAuth(client *vault.Client) error {
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

	log.Println("AppRole auth method enabled.")
	return nil
}

func ensureAppRoles(client *vault.Client) error {
	for _, svc := range serviceNames {
		_, err := client.Logical().Write("auth/approle/role/"+svc, map[string]interface{}{
			"token_policies":     []string{servicePolicyName(svc)},
			"token_ttl":          "1h",
			"token_max_ttl":      "4h",
			"secret_id_ttl":      "24h",
			"secret_id_num_uses": 0,
		})
		if err != nil {
			return fmt.Errorf("ensure AppRole %q: %w", svc, err)
		}
	}
	log.Println("Service AppRoles ensured.")
	return nil
}

func gatherSecrets(client *vault.Client) ([]serviceSecret, error) {
	items := make([]serviceSecret, 0, len(serviceNames))
	for _, svc := range serviceNames {
		roleID, secretID, err := fetchRoleAndSecret(client, svc)
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

		items = append(items, serviceSecret{
			Service:  svc,
			RoleID:   roleID,
			SecretID: secretID,
			Token:    loginSecret.Auth.ClientToken,
		})
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Service < items[j].Service })
	return items, nil
}

func fetchRoleAndSecret(client *vault.Client, roleName string) (string, string, error) {
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

func servicePolicyName(service string) string {
	return service + "-policy"
}

func parseServiceNames(csv string) []string {
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

func printSecrets(items []serviceSecret) {
	fmt.Println("\n=== Vault Provisioning Secrets ===")
	for _, s := range items {
		fmt.Printf("\n[%s]\n", s.Service)
		fmt.Printf("role_id: %s\n", s.RoleID)
		fmt.Printf("secret_id: %s\n", s.SecretID)
		fmt.Printf("token: %s\n", s.Token)
	}
	fmt.Println("\nKeep these values secure. Some may not be retrievable later.")
}
