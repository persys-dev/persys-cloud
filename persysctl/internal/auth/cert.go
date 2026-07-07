package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type CertificateManager struct {
	CACertPath   string
	CertPath     string
	KeyPath      string
	CFSSLApiURL  string
	CommonName   string
	Organization string
}

func NewCertificateManager(caCertPath, certPath, keyPath, cfsslApiURL, commonName, organization string) *CertificateManager {
	return &CertificateManager{
		CACertPath:   caCertPath,
		CertPath:     certPath,
		KeyPath:      keyPath,
		CFSSLApiURL:  cfsslApiURL,
		CommonName:   commonName,
		Organization: organization,
	}
}

func (cm *CertificateManager) EnsureCertificate() error {
	// Check if cert and key exist and are valid
	if cm.certAndKeyExist() {
		valid, err := cm.isCertValid()
		if err == nil && valid {
			return nil
		}
	}
	// Generate new key and CSR
	key, csr, err := cm.generateKeyAndCSR()
	if err != nil {
		return err
	}
	// Request cert from CFSSL
	certPEM, err := cm.requestCertFromCFSSL(csr)
	if err != nil {
		return err
	}
	// Store cert and key
	if err := os.WriteFile(cm.CertPath, certPEM, 0600); err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(cm.KeyPath, keyPEM, 0600); err != nil {
		return err
	}
	return nil
}

func (cm *CertificateManager) certAndKeyExist() bool {
	if _, err := os.Stat(cm.CertPath); err != nil {
		return false
	}
	if _, err := os.Stat(cm.KeyPath); err != nil {
		return false
	}
	return true
}

func (cm *CertificateManager) isCertValid() (bool, error) {
	certPEM, err := os.ReadFile(cm.CertPath)
	if err != nil {
		return false, err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false, fmt.Errorf("failed to decode cert PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, err
	}
	return time.Now().Before(cert.NotAfter), nil
}

func (cm *CertificateManager) generateKeyAndCSR() (*rsa.PrivateKey, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	csrTemplate := x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   cm.CommonName,
			Organization: []string{cm.Organization},
		},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &csrTemplate, key)
	if err != nil {
		return nil, nil, err
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	return key, csrPEM, nil
}

type cfsslSignRequest struct {
	CertificateRequest string `json:"certificate_request"`
}
type cfsslSignResponse struct {
	Success bool `json:"success"`
	Result  struct {
		Certificate string `json:"certificate"`
	} `json:"result"`
}

func (cm *CertificateManager) requestCertFromCFSSL(csrPEM []byte) ([]byte, error) {
	// Load CA cert for HTTPS connection to CFSSL
	caCert, err := os.ReadFile(cm.CACertPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA cert: %w", err)
	}
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to append CA cert to pool")
	}

	// Create TLS config with CA
	tlsConfig := &tls.Config{
		RootCAs:    caCertPool,
		MinVersion: tls.VersionTLS12,
	}

	// Create HTTP client with TLS config
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
		Timeout: 10 * time.Second,
	}

	// Prepare request
	payload := cfsslSignRequest{CertificateRequest: string(csrPEM)}
	data, _ := json.Marshal(payload)
	resp, err := httpClient.Post(cm.CFSSLApiURL+"/api/v1/cfssl/sign", "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to send request to CFSSL: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var cfsslResp cfsslSignResponse
	if err := json.Unmarshal(body, &cfsslResp); err != nil {
		return nil, fmt.Errorf("failed to parse CFSSL response: %w", err)
	}
	if !cfsslResp.Success {
		return nil, fmt.Errorf("CFSSL sign failed: %s", string(body))
	}
	return []byte(cfsslResp.Result.Certificate), nil
}

func (cm *CertificateManager) LoadCACert() ([]byte, error) {
	return os.ReadFile(cm.CACertPath)
}

// GetTLSConfig returns a TLS configuration for the client
func (cm *CertificateManager) GetTLSConfig() (*tls.Config, error) {
	// Load client certificate and key
	cert, err := tls.LoadX509KeyPair(cm.CertPath, cm.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate: %w", err)
	}

	// Load CA certificate
	caCert, err := cm.LoadCACert()
	if err != nil {
		return nil, fmt.Errorf("failed to load CA certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to append CA certificate to pool")
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}
