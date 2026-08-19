package services

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/persys-dev/persys-cloud/pkg/certmanager"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// LiveClientTLSConfig returns a tls.Config that reloads the client keypair from
// disk on every handshake (GetClientCertificate) and uses the given CA pool.
// Call this once after certmanager has written certs; subsequent ForceRotate
// writes are picked up on the next handshake without rebuilding the config.
func LiveClientTLSConfig(certPath, keyPath, caPath string) (*tls.Config, error) {
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read CA %s: %w", caPath, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("invalid CA bundle at %s", caPath)
	}
	// Validate keypair exists now.
	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
		return nil, fmt.Errorf("load client keypair: %w", err)
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    pool,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			cert, err := tls.LoadX509KeyPair(certPath, keyPath)
			if err != nil {
				return nil, err
			}
			return &cert, nil
		},
	}, nil
}

// dialGRPCTLS dials addr with tlsCfg. When mgr is non-nil, cert-related TLS
// failures trigger ForceRotate and up to 3 dial attempts.
func dialGRPCTLS(ctx context.Context, addr string, tlsCfg *tls.Config, mgr *certmanager.Manager, extra ...grpc.DialOption) (*grpc.ClientConn, error) {
	if tlsCfg == nil {
		return nil, fmt.Errorf("dial %s: tls config is nil", addr)
	}
	doDial := func(ctx context.Context) (*grpc.ClientConn, error) {
		opts := []grpc.DialOption{
			grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
			grpc.WithBlock(),
		}
		opts = append(opts, extra...)
		return grpc.DialContext(ctx, addr, opts...)
	}

	if mgr == nil {
		return doDial(ctx)
	}

	var conn *grpc.ClientConn
	err := certmanager.WithCertRetry(ctx, mgr, 3, func(ctx context.Context) error {
		c, err := doDial(ctx)
		if err != nil {
			return err
		}
		conn = c
		return nil
	})
	return conn, err
}
