package services

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	gootelhttp "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
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

// getContainerIP returns the primary IP address of the container
func (cm *CertificateManager) getContainerIP() (string, error) {
	// Try to get IP from environment variable first (common in Docker/K8s)
	if ip := os.Getenv("POD_IP"); ip != "" {
		return ip, nil
	}
	if ip := os.Getenv("CONTAINER_IP"); ip != "" {
		return ip, nil
	}

	// Fallback: get primary IP from network interfaces
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", fmt.Errorf("failed to get interface addresses: %w", err)
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String(), nil
			}
		}
	}

	return "", fmt.Errorf("no suitable IP address found")
}

func (cm *CertificateManager) generateKeyAndCSR() (*rsa.PrivateKey, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	// Get container IP address
	containerIP, err := cm.getContainerIP()
	if err != nil {
		fmt.Printf("Warning: could not get container IP: %v, using fallback hosts\n", err)
		containerIP = ""
	}

	// Define SANs for api-gateway - include container IP
	hosts := []string{"api-gateway", "localhost", "127.0.0.1"}
	if containerIP != "" {
		hosts = append(hosts, containerIP)
	}

	var dnsNames []string
	var ipAddresses []net.IP
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			ipAddresses = append(ipAddresses, ip)
		} else {
			dnsNames = append(dnsNames, h)
		}
	}

	// Create SAN extension
	sanBytes, err := asn1.Marshal(struct {
		DNSNames    []string `asn1:"tag:2,optional"`
		IPAddresses []net.IP `asn1:"tag:7,optional"`
	}{
		DNSNames:    dnsNames,
		IPAddresses: ipAddresses,
	})
	if err != nil {
		return nil, nil, err
	}

	sanExt := pkix.Extension{
		Id:    []int{2, 5, 29, 17}, // OID for subjectAltName
		Value: sanBytes,
	}

	csrTemplate := x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   cm.CommonName,
			Organization: []string{cm.Organization},
		},
		ExtraExtensions: []pkix.Extension{sanExt},
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &csrTemplate, key)
	if err != nil {
		return nil, nil, err
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	return key, csrPEM, nil
}

type cfsslSignRequest struct {
	CertificateRequest string   `json:"certificate_request"`
	Hosts              []string `json:"hosts"`
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
	caCertPool.AppendCertsFromPEM(caCert)

	// Get container IP and add hosts for SANs
	containerIP, err := cm.getContainerIP()
	if err != nil {
		fmt.Printf("Warning: could not get container IP for CFSSL request: %v\n", err)
		containerIP = ""
	}

	hosts := []string{"api-gateway", "localhost", "127.0.0.1"}
	if containerIP != "" {
		hosts = append(hosts, containerIP)
	}

	// Prepare request body
	reqBody := cfsslSignRequest{
		CertificateRequest: string(csrPEM),
		Hosts:              hosts,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal CFSSL sign request: %w", err)
	}

	// Instrumented HTTP client
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: caCertPool},
	}
	otelTransport := gootelhttp.NewTransport(transport)
	client := &http.Client{
		Transport: otelTransport,
		Timeout:   10 * time.Second,
	}

	// Start a span for the cert request
	tr := otel.Tracer("api-gateway/certmanager")
	ctx, span := tr.Start(context.Background(), "requestCertFromCFSSL")
	defer span.End()

	req, err := http.NewRequestWithContext(ctx, "POST", cm.CFSSLApiURL+"/api/v1/cfssl/sign", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create CFSSL request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to request cert from CFSSL: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read CFSSL response: %w", err)
	}

	var signResp cfsslSignResponse
	if err := json.Unmarshal(respBody, &signResp); err != nil {
		return nil, fmt.Errorf("failed to decode CFSSL response: %w", err)
	}
	if !signResp.Success {
		return nil, fmt.Errorf("CFSSL sign failed: %s", string(respBody))
	}

	return []byte(signResp.Result.Certificate), nil
}

func (cm *CertificateManager) LoadCACert() ([]byte, error) {
	return os.ReadFile(cm.CACertPath)
}
