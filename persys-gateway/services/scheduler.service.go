package services

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-gateway/config"
	gootelhttp "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
)

type ProwService struct {
	config        *config.Config
	clientTLS     *tls.Config
	serverTLS     *tls.Config
	httpClient    *http.Client
	schedulers    []string  // List of discovered scheduler addresses
	lastDiscovery time.Time // Last time we discovered schedulers
}

func NewProwService(cfg *config.Config) *ProwService {
	service := &ProwService{
		config: cfg,
	}

	// Load TLS configurations
	if err := service.loadTLSConfigs(); err != nil {
		panic(fmt.Sprintf("Failed to load TLS configs: %v", err))
	}

	// Create HTTP client with TLS and OpenTelemetry instrumentation
	transport := &http.Transport{
		TLSClientConfig: service.clientTLS,
	}
	otelTransport := gootelhttp.NewTransport(transport)

	service.httpClient = &http.Client{
		Transport: otelTransport,
		Timeout:   30 * time.Second,
	}

	// Initial scheduler discovery
	service.DiscoverAndPrintSchedulers()

	return service
}

func (s *ProwService) loadTLSConfigs() error {
	// Load client certificate
	cert, err := tls.LoadX509KeyPair(s.config.TLS.CertPath, s.config.TLS.KeyPath)
	if err != nil {
		return fmt.Errorf("failed to load client certificate: %w", err)
	}

	// Load CA certificate
	caCert, err := os.ReadFile(s.config.TLS.CAPath)
	if err != nil {
		return fmt.Errorf("failed to read CA certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return fmt.Errorf("failed to append CA certificate")
	}

	// Configure client TLS
	s.clientTLS = &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
	}

	// Configure server TLS
	s.serverTLS = &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caCertPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}

	return nil
}

// DiscoverAndPrintSchedulers discovers schedulers and prints them
func (s *ProwService) DiscoverAndPrintSchedulers() {
	coreDNSAddr := s.config.CoreDNS.Addr
	if coreDNSAddr == "" {
		fmt.Println("[ProwService] CoreDNS address not set, skipping scheduler discovery")
		return
	}
	if err := s.DiscoverSchedulers(coreDNSAddr); err != nil {
		fmt.Printf("[ProwService] Failed to discover schedulers: %v\n", err)
	} else {
		fmt.Printf("[ProwService] Discovered schedulers: %v\n", s.schedulers)
	}
	s.lastDiscovery = time.Now()
}

// ProxyRequest handles proxying requests to prow-scheduler
func (s *ProwService) ProxyRequest(ctx context.Context, method, path string, body io.Reader, headers http.Header) (*http.Response, error) {
	if !s.config.Prow.EnableProxy {
		return nil, fmt.Errorf("prow proxy is disabled")
	}

	// Start a span for the proxy request
	tr := otel.Tracer("api-gateway/prow-service")
	ctx, span := tr.Start(ctx, "ProxyRequest")
	defer span.End()

	// Periodically re-discover schedulers (every 1 minute)
	if time.Since(s.lastDiscovery) > time.Minute {
		s.DiscoverAndPrintSchedulers()
	}

	// Use discovered scheduler address if available, fallback to config
	address := s.GetSchedulerAddress()
	url := fmt.Sprintf("https://%s%s", address, path)

	// Create the request
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Copy relevant headers from the original request
	for key, values := range headers {
		if shouldForwardHeader(key) {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	}

	// Execute the request (mTLS is handled by the HTTP client TLS config)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to proxy request: %w", err)
	}

	return resp, nil
}

// shouldForwardHeader determines if a header should be forwarded to prow-scheduler
func shouldForwardHeader(key string) bool {
	// List of headers to forward
	forwardHeaders := map[string]bool{
		"Content-Type":      true,
		"Content-Length":    true,
		"Authorization":     true,
		"User-Agent":        true,
		"Accept":            true,
		"Accept-Encoding":   true,
		"X-Request-ID":      true,
		"X-Forwarded-For":   true,
		"X-Forwarded-Proto": true,
	}

	return forwardHeaders[key] || strings.HasPrefix(key, "X-")
}

// CopyResponse copies response data to the gin context
func (s *ProwService) CopyResponse(resp *http.Response, ctx *gin.Context) error {
	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			ctx.Header(key, value)
		}
	}

	// Copy response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	defer resp.Body.Close()

	// Set status code and send response
	ctx.Data(resp.StatusCode, resp.Header.Get("Content-Type"), body)
	return nil
}

// IsAgentRequest determines if the request is from an agent (for logging purposes only)
func (s *ProwService) IsAgentRequest(path string) bool {
	// Agent-specific endpoints for logging/debugging purposes
	agentPaths := []string{
		"/nodes/register",
		"/nodes/heartbeat",
		"/docker/",
		"/compose/",
	}

	for _, agentPath := range agentPaths {
		if strings.HasPrefix(path, agentPath) {
			return true
		}
	}
	return false
}

// DiscoverSchedulers discovers Prow schedulers using CoreDNS
func (s *ProwService) DiscoverSchedulers(coreDNSAddr string) error {
	if coreDNSAddr == "" {
		return fmt.Errorf("CoreDNS address is required for scheduler discovery")
	}

	// Query CoreDNS for scheduler SRV records
	schedulers, err := s.queryCoreDNSForSchedulers(coreDNSAddr)
	if err != nil {
		return fmt.Errorf("failed to query CoreDNS for schedulers: %w", err)
	}

	if len(schedulers) == 0 {
		return fmt.Errorf("no schedulers found in CoreDNS")
	}

	s.schedulers = schedulers
	return nil
}

// queryCoreDNSForSchedulers queries CoreDNS for scheduler records
func (s *ProwService) queryCoreDNSForSchedulers(coreDNSAddr string) ([]string, error) {
	// Create a DNS resolver
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{
				Timeout: 5 * time.Second,
			}
			return d.DialContext(ctx, "udp", coreDNSAddr)
		},
	}

	// Use domain and service from config if available, else default
	domain := s.config.Prow.DiscoveryDomain
	if domain == "" {
		domain = "persys.local"
	}
	service := s.config.Prow.DiscoveryService
	if service == "" {
		service = "_persys-scheduler"
	}

	// Query for SRV records in the format _persys-scheduler.persys.local (no _tcp)
	srvName := service + "." + domain
	_, srvRecords, err := resolver.LookupSRV(context.Background(), "", "", srvName)
	if err != nil {
		// Try alternative domain
		_, srvRecords, err = resolver.LookupSRV(context.Background(), "", "", service+".local")
		if err != nil {
			return nil, fmt.Errorf("failed to lookup SRV records: %w", err)
		}
	}

	var schedulers []string
	for _, srv := range srvRecords {
		// The target should be the actual IP address or resolvable hostname
		// Look up the target to get the IP address
		ips, err := resolver.LookupHost(context.Background(), srv.Target)
		if err != nil {
			// If we can't resolve the target, skip this record
			fmt.Printf("[ProwService] Warning: cannot resolve SRV target %s: %v\n", srv.Target, err)
			continue
		}

		// Use the first IP address found
		if len(ips) > 0 {
			addr := fmt.Sprintf("%s:%d", ips[0], srv.Port)
			schedulers = append(schedulers, addr)
		}
	}

	// If no SRV records found, try A record lookup
	if len(schedulers) == 0 {
		ips, err := resolver.LookupHost(context.Background(), "persys-scheduler."+domain)
		if err != nil {
			// Try alternative domain
			ips, err = resolver.LookupHost(context.Background(), "persys-scheduler.local")
			if err != nil {
				return nil, fmt.Errorf("failed to lookup scheduler A records: %w", err)
			}
		}

		for _, ip := range ips {
			// Use default port 8085 if not specified
			addr := fmt.Sprintf("%s:8085", ip)
			schedulers = append(schedulers, addr)
		}
	}

	return schedulers, nil
}

// GetSchedulerAddress returns the first available scheduler address
func (s *ProwService) GetSchedulerAddress() string {
	if len(s.schedulers) > 0 {
		return s.schedulers[0]
	}
	// Fallback to configured address
	return s.config.Prow.SchedulerAddr
}

// GetSchedulerAddresses returns all discovered scheduler addresses
func (s *ProwService) GetSchedulerAddresses() []string {
	return s.schedulers
}

// IsProxyEnabled returns whether prow proxying is enabled
func (s *ProwService) IsProxyEnabled() bool {
	return s.config.Prow.EnableProxy
}
