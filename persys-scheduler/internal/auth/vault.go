package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	vault "github.com/hashicorp/vault/api"
	"github.com/sirupsen/logrus"
)

const (
	rotationFractionNumerator   = 80
	rotationFractionDenominator = 100
	minRotationWait             = 30 * time.Second
)

type Config struct {
	TLSEnabled  bool
	ExternalIP  string
	TLSCertPath string
	TLSKeyPath  string
	TLSCAPath   string

	VaultEnabled       bool
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

	BindHost string
}

type Manager struct {
	cfg    Config
	logger *logrus.Entry

	mu      sync.RWMutex
	current certMeta
}

type certMeta struct {
	notBefore time.Time
	notAfter  time.Time
}

func NewManager(cfg Config, logger *logrus.Logger) *Manager {
	return &Manager{
		cfg:    cfg,
		logger: logger.WithField("component", "vault-cert-manager"),
	}
}

func (m *Manager) Validate() error {
	if !m.cfg.TLSEnabled {
		return nil
	}
	if !m.cfg.VaultEnabled {
		return nil
	}
	if strings.TrimSpace(m.cfg.VaultAddr) == "" {
		return fmt.Errorf("vault is enabled but PERSYS_VAULT_ADDR is empty")
	}
	if strings.TrimSpace(m.cfg.VaultPKIMount) == "" || strings.TrimSpace(m.cfg.VaultPKIRole) == "" {
		return fmt.Errorf("vault is enabled but PKI mount/role is not configured")
	}
	switch strings.ToLower(strings.TrimSpace(m.cfg.VaultAuthMethod)) {
	case "token":
		if strings.TrimSpace(m.cfg.VaultToken) == "" {
			return fmt.Errorf("vault token auth selected but PERSYS_VAULT_TOKEN is empty")
		}
	case "approle":
		if strings.TrimSpace(m.cfg.VaultAppRoleID) == "" || strings.TrimSpace(m.cfg.VaultAppSecretID) == "" {
			return fmt.Errorf("vault approle auth selected but role_id/secret_id is missing")
		}
	default:
		return fmt.Errorf("unsupported vault auth method %q (expected token|approle)", m.cfg.VaultAuthMethod)
	}
	if m.cfg.VaultCertTTL <= 0 {
		return fmt.Errorf("vault cert TTL must be positive")
	}
	if m.cfg.VaultRetryInterval <= 0 {
		return fmt.Errorf("vault retry interval must be positive")
	}
	return nil
}

func (m *Manager) Start(ctx context.Context) error {
	if !m.cfg.TLSEnabled {
		return nil
	}
	if !m.cfg.VaultEnabled {
		m.logger.Info("Vault cert manager disabled; using manual certificate files")
		return nil
	}
	if err := m.Validate(); err != nil {
		return err
	}
	if existingMeta, ok := m.loadExistingCertMeta(); ok {
		m.mu.Lock()
		m.current = existingMeta
		m.mu.Unlock()
		m.logger.WithFields(logrus.Fields{
			"not_before": existingMeta.notBefore.UTC().Format(time.RFC3339),
			"not_after":  existingMeta.notAfter.UTC().Format(time.RFC3339),
		}).Info("Using existing valid certificate from disk")
		go m.rotationLoop(ctx)
		return nil
	}

	cli, err := m.newVaultClient()
	if err != nil {
		if m.manualCertAvailable() {
			m.logger.WithError(err).Warn("Vault unavailable on startup, falling back to manual certificates")
			go m.recoveryLoop(ctx)
			return nil
		}
		return fmt.Errorf("vault unavailable and no manual cert fallback found: %w", err)
	}

	if err := m.issueAndPersist(ctx, cli); err != nil {
		if m.manualCertAvailable() {
			m.logger.WithError(err).Warn("Vault certificate issuance failed, using manual certificates")
			go m.recoveryLoop(ctx)
			return nil
		}
		return fmt.Errorf("vault issuance failed and no manual cert fallback found: %w", err)
	}

	go m.rotationLoop(ctx)
	return nil
}

func (m *Manager) rotationLoop(ctx context.Context) {
	for {
		renewAt := m.nextRenewAt()
		wait := time.Until(renewAt)
		if wait < minRotationWait {
			wait = minRotationWait
		}

		m.logger.WithField("next_rotation", renewAt.UTC().Format(time.RFC3339)).Info("Next certificate rotation scheduled")

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		cli, err := m.newVaultClient()
		if err != nil {
			m.logger.WithError(err).Warn("Vault not reachable during rotation window; retrying later")
			continue
		}
		if err := m.issueAndPersist(ctx, cli); err != nil {
			m.logger.WithError(err).Warn("Certificate rotation failed; retrying later")
			continue
		}
	}
}

func (m *Manager) recoveryLoop(ctx context.Context) {
	ticker := time.NewTicker(m.cfg.VaultRetryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		cli, err := m.newVaultClient()
		if err != nil {
			m.logger.WithError(err).Debug("Vault still unavailable while running on fallback certs")
			continue
		}
		if err := m.issueAndPersist(ctx, cli); err != nil {
			m.logger.WithError(err).Warn("Vault recovered but certificate issuance still failing")
			continue
		}

		m.logger.Info("Vault certificate provisioning recovered; enabling rotation loop")
		go m.rotationLoop(ctx)
		return
	}
}

func (m *Manager) newVaultClient() (*vault.Client, error) {
	conf := vault.DefaultConfig()
	conf.Address = m.cfg.VaultAddr

	client, err := vault.NewClient(conf)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(strings.TrimSpace(m.cfg.VaultAuthMethod)) {
	case "token":
		client.SetToken(m.cfg.VaultToken)
		if _, err := client.Auth().Token().LookupSelf(); err != nil {
			return nil, fmt.Errorf("token auth validation failed: %w", err)
		}
	case "approle":
		secret, err := client.Logical().Write("auth/approle/login", map[string]interface{}{
			"role_id":   m.cfg.VaultAppRoleID,
			"secret_id": m.cfg.VaultAppSecretID,
		})
		if err != nil {
			return nil, fmt.Errorf("approle login failed: %w", err)
		}
		if secret == nil || secret.Auth == nil || secret.Auth.ClientToken == "" {
			return nil, errors.New("approle login returned empty client token")
		}
		client.SetToken(secret.Auth.ClientToken)
	default:
		return nil, fmt.Errorf("unsupported vault auth method: %s", m.cfg.VaultAuthMethod)
	}

	return client, nil
}

func (m *Manager) issueAndPersist(ctx context.Context, client *vault.Client) error {
	dnsSANs, ipSANs := m.detectSANs()
	payload := map[string]interface{}{
		"common_name": m.cfg.VaultServiceName,
		"ttl":         m.cfg.VaultCertTTL.String(),
	}
	if len(dnsSANs) > 0 {
		payload["alt_names"] = strings.Join(dnsSANs, ",")
	}
	if len(ipSANs) > 0 {
		payload["ip_sans"] = strings.Join(ipSANs, ",")
	}

	path := fmt.Sprintf("%s/issue/%s", strings.Trim(m.cfg.VaultPKIMount, "/"), m.cfg.VaultPKIRole)
	secret, err := client.Logical().WriteWithContext(ctx, path, payload)
	if err != nil {
		return err
	}
	if secret == nil || secret.Data == nil {
		return errors.New("empty response from vault issue endpoint")
	}

	certPEM := asString(secret.Data["certificate"])
	keyPEM := asString(secret.Data["private_key"])
	issuingCA := asString(secret.Data["issuing_ca"])
	caChain := parseCAChain(secret.Data["ca_chain"])

	if certPEM == "" || keyPEM == "" {
		return errors.New("vault response missing certificate or private key")
	}

	combinedCA := combineCA(issuingCA, caChain)
	if combinedCA == "" {
		return errors.New("vault response missing CA chain")
	}

	notBefore, notAfter, err := certValidity(certPEM, keyPEM)
	if err != nil {
		return err
	}

	if err := writeCertBundleAtomic(m.cfg.TLSCertPath, certPEM, m.cfg.TLSKeyPath, keyPEM, m.cfg.TLSCAPath, combinedCA); err != nil {
		return err
	}

	m.mu.Lock()
	m.current = certMeta{
		notBefore: notBefore,
		notAfter:  notAfter,
	}
	m.mu.Unlock()

	m.logger.WithFields(logrus.Fields{
		"not_before": notBefore.UTC().Format(time.RFC3339),
		"not_after":  notAfter.UTC().Format(time.RFC3339),
		"dns_sans":   strings.Join(dnsSANs, ","),
		"ip_sans":    strings.Join(ipSANs, ","),
	}).Info("Issued and installed certificate from Vault")

	return nil
}

func (m *Manager) detectSANs() ([]string, []string) {
	dnsSet := map[string]struct{}{}
	ipSet := map[string]struct{}{}
	addDNS := func(s string) {
		s = strings.TrimSpace(strings.ToLower(s))
		if s != "" {
			dnsSet[s] = struct{}{}
		}
	}
	addIP := func(s string) {
		s = strings.TrimSpace(s)
		if ip := net.ParseIP(s); ip != nil {
			ipSet[ip.String()] = struct{}{}
		}
	}

	service := strings.TrimSpace(m.cfg.VaultServiceName)
	if service == "" {
		service = "persys-scheduler"
	}
	addDNS(service)
	addDNS("localhost")
	addIP("127.0.0.1")
	addIP("::1")
	if m.cfg.ExternalIP != "" {
		addIP(m.cfg.ExternalIP)
	}

	if host, err := os.Hostname(); err == nil {
		addDNS(host)
	}

	domain := strings.Trim(strings.ToLower(m.cfg.VaultServiceDomain), ".")
	if domain != "" {
		addDNS(service + "." + domain)
		if host, err := os.Hostname(); err == nil {
			short := strings.Split(host, ".")[0]
			addDNS(short + "." + domain)
		}
	}

	if bindHost := strings.TrimSpace(m.cfg.BindHost); bindHost != "" && bindHost != "0.0.0.0" {
		if ip := net.ParseIP(bindHost); ip != nil {
			addIP(ip.String())
		} else {
			addDNS(bindHost)
		}
	}

	if u, err := url.Parse(m.cfg.VaultAddr); err == nil {
		host := u.Hostname()
		if ip := net.ParseIP(host); ip != nil {
			addIP(ip.String())
		}
	}

	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP == nil || ipNet.IP.IsLoopback() {
				continue
			}
			addIP(ipNet.IP.String())
		}
	}

	dnsSANs := make([]string, 0, len(dnsSet))
	for s := range dnsSet {
		dnsSANs = append(dnsSANs, s)
	}
	ipSANs := make([]string, 0, len(ipSet))
	for s := range ipSet {
		ipSANs = append(ipSANs, s)
	}
	return dnsSANs, ipSANs
}

func (m *Manager) manualCertAvailable() bool {
	if _, err := tls.LoadX509KeyPair(m.cfg.TLSCertPath, m.cfg.TLSKeyPath); err != nil {
		return false
	}
	caPEM, err := os.ReadFile(m.cfg.TLSCAPath)
	if err != nil {
		return false
	}
	pool := x509.NewCertPool()
	return pool.AppendCertsFromPEM(caPEM)
}

func (m *Manager) loadExistingCertMeta() (certMeta, bool) {
	if !m.manualCertAvailable() {
		return certMeta{}, false
	}
	keyPair, err := tls.LoadX509KeyPair(m.cfg.TLSCertPath, m.cfg.TLSKeyPath)
	if err != nil || len(keyPair.Certificate) == 0 {
		return certMeta{}, false
	}
	leaf, err := x509.ParseCertificate(keyPair.Certificate[0])
	if err != nil {
		return certMeta{}, false
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return certMeta{}, false
	}
	return certMeta{notBefore: leaf.NotBefore, notAfter: leaf.NotAfter}, true
}

func (m *Manager) nextRenewAt() time.Time {
	m.mu.RLock()
	meta := m.current
	m.mu.RUnlock()

	if meta.notAfter.IsZero() || meta.notBefore.IsZero() || !meta.notAfter.After(meta.notBefore) {
		return time.Now().Add(m.cfg.VaultRetryInterval)
	}

	lifetime := meta.notAfter.Sub(meta.notBefore)
	rotationPoint := meta.notBefore.Add(lifetime * rotationFractionNumerator / rotationFractionDenominator)
	if rotationPoint.Before(time.Now()) {
		return time.Now().Add(minRotationWait)
	}
	return rotationPoint
}

func certValidity(certPEM, keyPEM string) (time.Time, time.Time, error) {
	keyPair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parse issued keypair: %w", err)
	}
	if len(keyPair.Certificate) == 0 {
		return time.Time{}, time.Time{}, errors.New("issued keypair contains no certificate")
	}
	leaf, err := x509.ParseCertificate(keyPair.Certificate[0])
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parse issued leaf certificate: %w", err)
	}
	return leaf.NotBefore, leaf.NotAfter, nil
}

func combineCA(issuingCA string, chain []string) string {
	parts := make([]string, 0, 1+len(chain))
	if trimmed := strings.TrimSpace(issuingCA); trimmed != "" {
		parts = append(parts, trimmed)
	}
	for _, c := range chain {
		if trimmed := strings.TrimSpace(c); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, "\n")
}

func parseCAChain(v interface{}) []string {
	switch raw := v.(type) {
	case []interface{}:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if s := strings.TrimSpace(asString(item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if s := strings.TrimSpace(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		s := strings.TrimSpace(asString(v))
		if s == "" {
			return nil
		}
		return []string{s}
	}
}

func asString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

func writeAtomic(path, contents string, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-cert-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(contents); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmpName, path)
}

func writeCertBundleAtomic(certPath, certPEM, keyPath, keyPEM, caPath, caPEM string) error {
	// Validate full bundle before writing so we don't publish an unusable pair.
	if _, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); err != nil {
		return fmt.Errorf("invalid cert/key pair: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(caPEM)) {
		return errors.New("invalid CA PEM")
	}

	if err := writeAtomic(keyPath, keyPEM, 0o600); err != nil {
		return err
	}
	if err := writeAtomic(certPath, certPEM, 0o644); err != nil {
		return err
	}
	if err := writeAtomic(caPath, caPEM, 0o644); err != nil {
		return err
	}
	return nil
}
