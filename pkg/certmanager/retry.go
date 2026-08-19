package certmanager

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// IsCertRelatedTLSError reports whether err is (or wraps) a TLS/x509 failure
// that is likely to be fixed by rotating or reloading local certificate
// material. Non-TLS errors (connection refused, timeouts, HTTP 5xx, etc.)
// return false so callers do not stampede Vault on unrelated failures.
func IsCertRelatedTLSError(err error) bool {
	if err == nil {
		return false
	}

	var (
		unknownAuth x509.UnknownAuthorityError
		hostnameErr x509.HostnameError
		invalidCert x509.CertificateInvalidError
	)
	if errors.As(err, &unknownAuth) ||
		errors.As(err, &hostnameErr) ||
		errors.As(err, &invalidCert) {
		return true
	}

	msg := strings.ToLower(err.Error())
	for _, n := range []string{
		"certificate",
		"tls:",
		"x509:",
		"bad certificate",
		"certificate required",
		"certificate signed by unknown authority",
		"expired certificate",
		"authentication handshake failed",
		"remote error: tls:",
	} {
		if strings.Contains(msg, n) {
			return true
		}
	}
	return false
}

// WithCertRetry runs fn. On cert-related TLS failures it calls m.ForceRotate
// and retries up to maxAttempts times with a short linear backoff.
// Non-TLS errors are returned immediately. maxAttempts <= 0 defaults to 3.
//
// Typical use (gRPC dial, one-shot RPC, custom client):
//
//	err := certmanager.WithCertRetry(ctx, mgr, 3, func(ctx context.Context) error {
//	    return dialOrCall(ctx)
//	})
func WithCertRetry(ctx context.Context, m *Manager, maxAttempts int, fn func(context.Context) error) error {
	if m == nil {
		return errors.New("certmanager: WithCertRetry called with nil Manager")
	}
	if fn == nil {
		return errors.New("certmanager: WithCertRetry called with nil fn")
	}
	if maxAttempts <= 0 {
		maxAttempts = 3
	}

	var last error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		last = fn(ctx)
		if last == nil {
			return nil
		}
		if !IsCertRelatedTLSError(last) {
			return last
		}

		m.logger.WithError(last).WithField("attempt", attempt).
			Warn("TLS error; forcing certificate rotation before retry")

		if err := m.ForceRotate(ctx); err != nil {
			m.logger.WithError(err).Warn("ForceRotate failed; still retrying")
		}

		if attempt == maxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt) * 300 * time.Millisecond):
		}
	}
	return fmt.Errorf("certmanager: call failed after %d attempts: %w", maxAttempts, last)
}

// CertRetryTransport is an http.RoundTripper that retries requests when the
// underlying RoundTrip fails with a cert-related TLS error. On each such
// failure it calls Manager.ForceRotate and optionally invokes AfterRotate
// (e.g. to reload on-disk material into a tls.Config holder and close idle
// connections).
//
// Base must be non-nil. AfterRotate may be nil. MaxAttempts <= 0 defaults to 3.
//
// Request bodies are only retried when req.GetBody is set (httputil.ReverseProxy
// and most standard clients set this). Otherwise the transport returns the
// TLS error without retrying to avoid sending a consumed body.
type CertRetryTransport struct {
	Base        http.RoundTripper
	Manager     *Manager
	MaxAttempts int
	// AfterRotate is called after a successful or attempted ForceRotate,
	// before the next attempt. Use it to Reload cert files and
	// CloseIdleConnections on the underlying *http.Transport.
	AfterRotate func()
}

func (t *CertRetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.Base == nil {
		return nil, errors.New("certmanager: CertRetryTransport.Base is nil")
	}
	max := t.MaxAttempts
	if max <= 0 {
		max = 3
	}

	var last error
	for attempt := 1; attempt <= max; attempt++ {
		resp, err := t.Base.RoundTrip(req)
		if err == nil {
			return resp, nil
		}
		last = err

		if !IsCertRelatedTLSError(err) {
			return nil, err
		}

		if t.Manager != nil {
			t.Manager.logger.WithError(err).WithField("attempt", attempt).
				Warn("HTTP TLS error; forcing certificate rotation before retry")
			if ferr := t.Manager.ForceRotate(req.Context()); ferr != nil {
				t.Manager.logger.WithError(ferr).Warn("ForceRotate failed; still retrying")
			}
		}
		if t.AfterRotate != nil {
			t.AfterRotate()
		}

		if attempt == max {
			break
		}

		if req.Body != nil && req.GetBody == nil {
			return nil, fmt.Errorf("certmanager: tls retry aborted (request body not replayable): %w", last)
		}
		if req.GetBody != nil {
			body, berr := req.GetBody()
			if berr != nil {
				return nil, fmt.Errorf("certmanager: tls retry aborted (GetBody): %v (last: %w)", berr, last)
			}
			req.Body = body
		}

		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(time.Duration(attempt) * 300 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("certmanager: upstream failed after %d tls retries: %w", max, last)
}