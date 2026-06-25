package client

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/persys-dev/persys-cloud/pkg/certmanager"
	"github.com/persys-dev/persys-cloud/sdk/options"
	"github.com/sirupsen/logrus"
)

// LoadTLSConfig builds a tls.Config from SDK options and the shared Persys certmanager settings.
func LoadTLSConfig(opts *options.Options) (*tls.Config, error) {
	if opts == nil {
		opts = options.DefaultOptions()
	}
	if opts.Insecure {
		return &tls.Config{InsecureSkipVerify: true}, nil
	}
	if !opts.UseCertManager {
		return &tls.Config{MinVersion: tls.VersionTLS12}, nil
	}
	mgr := certmanager.NewManager(certmanager.Config{TLSEnabled: true, TLSCertPath: opts.TLSCertPath, TLSKeyPath: opts.TLSKeyPath, TLSCAPath: opts.TLSCAPath}, logrus.New())
	if err := mgr.Validate(); err != nil {
		return nil, fmt.Errorf("validate certmanager config: %w", err)
	}
	cert, err := tls.LoadX509KeyPair(opts.TLSCertPath, opts.TLSKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load client certificate: %w", err)
	}
	caPEM, err := os.ReadFile(opts.TLSCAPath)
	if err != nil {
		return nil, fmt.Errorf("read CA certificate: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("parse CA certificate %q", opts.TLSCAPath)
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}, RootCAs: pool}, nil
}
