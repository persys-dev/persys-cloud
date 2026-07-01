package identity

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/persys-dev/persys-cloud/pkg/certmanager"
	"github.com/persys-dev/persys-cloud/sdk/options"
	"github.com/sirupsen/logrus"
)

type Provider interface {
	TLSConfig(ctx context.Context) (*tls.Config, error)
	Close() error
}

type certManagerProvider struct {
	mgr      *certmanager.Manager
	certPath string
	keyPath  string
	caPath   string
}

func NewVaultProvider(ctx context.Context, id options.IdentityOptions, sdkOpts *options.Options) (Provider, error) {
	cmCfg := certmanager.Config{
		TLSEnabled:       true,
		VaultEnabled:     true,
		VaultAddr:        id.VaultAddr,
		VaultAuthMethod:  "approle",
		VaultPKIMount:    id.PKIMount,
		VaultPKIRole:     id.PKIRole,
		VaultServiceName: id.ServiceName,
		VaultCertTTL:     parseTTL(id.TTL),
		TLSCertPath:      sdkOpts.TLSCertPath,
		TLSKeyPath:       sdkOpts.TLSKeyPath,
		TLSCAPath:        sdkOpts.TLSCAPath,
		VaultManagerAddr: sdkOpts.VaultManagerAddr,
		BindHost:         os.Getenv("PERSYS_BIND_HOST"),
	}

	if cmCfg.TLSCertPath == "" {
		tmp := os.TempDir()
		cmCfg.TLSCertPath = tmp + "/persys-sdk-cert.pem"
		cmCfg.TLSKeyPath = tmp + "/persys-sdk-key.pem"
		cmCfg.TLSCAPath = tmp + "/persys-sdk-ca.pem"
	}

	logger := logrus.New()
	logger.SetOutput(os.Stderr)
	mgr := certmanager.NewManager(cmCfg, logger)

	if err := mgr.Start(ctx); err != nil {
		return nil, fmt.Errorf("certmanager start: %w", err)
	}

	return &certManagerProvider{
		mgr:      mgr,
		certPath: cmCfg.TLSCertPath,
		keyPath:  cmCfg.TLSKeyPath,
		caPath:   cmCfg.TLSCAPath,
	}, nil
}

func (p *certManagerProvider) TLSConfig(_ context.Context) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(p.certPath, p.keyPath)
	if err != nil {
		return nil, fmt.Errorf("load cert from certmanager: %w", err)
	}
	caPEM, err := os.ReadFile(p.caPath)
	if err != nil {
		return nil, fmt.Errorf("read CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("failed to parse CA PEM")
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
	}, nil
}

func (p *certManagerProvider) Close() error { return nil }

func parseTTL(s string) time.Duration {
	if s == "" {
		s = "24h"
	}
	d, _ := time.ParseDuration(s)
	return d
}

// Insecure + plain providers (unchanged from original)
type insecureProvider struct{}
func NewInsecureProvider() Provider { return &insecureProvider{} }
func (p *insecureProvider) TLSConfig(_ context.Context) (*tls.Config, error) {
	return &tls.Config{InsecureSkipVerify: true}, nil
}
func (p *insecureProvider) Close() error { return nil }

type plainTLSProvider struct{}
func (p *plainTLSProvider) TLSConfig(_ context.Context) (*tls.Config, error) {
	return &tls.Config{MinVersion: tls.VersionTLS12}, nil
}
func (p *plainTLSProvider) Close() error { return nil }

func NewProvider(ctx context.Context, opts *options.Options) (Provider, error) {
	if opts == nil || opts.Insecure {
		return NewInsecureProvider(), nil
	}
	if opts.UseCertManager {
		return NewVaultProvider(ctx, opts.Identity, opts)
	}
	return &plainTLSProvider{}, nil
}