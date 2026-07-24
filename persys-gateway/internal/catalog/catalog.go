// Package catalog implements a config-driven service registry for the
// gateway. Instead of every backend service requiring a new controller,
// route file, and wiring block in cmd/main.go, HTTP-proxied services are
// described declaratively and registered generically at startup.
//
// Services that need typed proto translation (scheduler, forgery,
// automation) don't belong here — they keep dedicated controllers, but
// self-register into the router via the Registrar interface in router.go
// instead of being named explicitly in main.go.
package catalog

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/persys-dev/persys-cloud/persys-gateway/config"
	"gopkg.in/yaml.v3"
)

// AuthMode controls what the gateway requires before proxying a request.
type AuthMode string

const (
	// AuthNone: no auth check. Only ever appropriate for truly public
	// endpoints (health checks, the GitHub webhook receiver, which
	// authenticates via HMAC signature instead of a bearer token).
	AuthNone AuthMode = "none"

	// AuthMTLS: caller must present a client cert on the mTLS listener.
	// Used for internal service-to-service calls (persysctl node ops,
	// scheduler control plane). No user identity is required or attached.
	// This is also what AuthUser downgrades to in self-hosted mode — see
	// Resolve.
	AuthMTLS AuthMode = "mtls"

	// AuthUser declares "this route touches a customer-owned resource."
	// It does NOT mean a JWT is always required — see Resolve. In
	// self-hosted deployments there's no tenant to protect against, so it
	// resolves to AuthMTLS. In managed deployments it resolves to a real
	// JWT requirement.
	AuthUser AuthMode = "user"

	// AuthEither: mTLS OR a user JWT satisfies the request. Rare —
	// mainly for cluster-registration endpoints that persysctl calls
	// over mTLS during bootstrap, and that a managed dashboard might also
	// call on a user's behalf.
	AuthEither AuthMode = "either"
)

// DeploymentMode is an alias for config.DeploymentMode — the deployment
// toggle has exactly one definition (config/config.go), used by
// config validation (fail fast on missing JWT secret in managed mode),
// the catalog's auth resolution, and grpcbridge binding auth. Defining it
// twice would let the two silently drift.
type DeploymentMode = config.DeploymentMode

const (
	SelfHosted = config.DeploymentSelfHosted
	Managed    = config.DeploymentManaged
)

// Resolve maps a route's declared intent onto what's actually enforced
// for the given deployment mode.
func (a AuthMode) Resolve(mode DeploymentMode) AuthMode {
	if a == AuthUser && mode == SelfHosted {
		return AuthMTLS
	}
	if a == AuthEither && mode == SelfHosted {
		return AuthMTLS
	}
	return a
}

// Service describes one backend that the gateway proxies HTTP requests to.
type Service struct {
	// Name is a unique identifier, used in logs/metrics.
	Name string `yaml:"name"`

	// PathPrefix is the inbound prefix this service owns, e.g. "/ai".
	// All requests under this prefix are proxied.
	PathPrefix string `yaml:"path_prefix"`

	// UpstreamAddr is the base URL of the backend, e.g.
	// "http://persys-intelligence:8093".
	UpstreamAddr string `yaml:"upstream_addr"`

	// StripPrefix, if true, removes PathPrefix before forwarding, so
	// "/ai/query" -> "/query" upstream. If false, the full path is kept.
	StripPrefix bool `yaml:"strip_prefix"`

	// Auth selects the trust model required for this service's routes.
	Auth AuthMode `yaml:"auth"`

	// Timeout bounds the outbound request. Defaults to 10s.
	Timeout time.Duration `yaml:"timeout"`

	// Enabled lets an entry be present but disabled without deleting it.
	Enabled bool `yaml:"enabled"`
}

type Catalog struct {
	Services []Service `yaml:"services"`
}

func Load(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read service catalog %q: %w", path, err)
	}
	var c Catalog
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse service catalog %q: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Catalog) validate() error {
	seen := map[string]bool{}
	for _, svc := range c.Services {
		if strings.TrimSpace(svc.Name) == "" {
			return fmt.Errorf("service catalog: entry missing name")
		}
		if seen[svc.Name] {
			return fmt.Errorf("service catalog: duplicate service name %q", svc.Name)
		}
		seen[svc.Name] = true
		if strings.TrimSpace(svc.PathPrefix) == "" {
			return fmt.Errorf("service catalog: %s missing path_prefix", svc.Name)
		}
		if svc.Enabled && strings.TrimSpace(svc.UpstreamAddr) == "" {
			return fmt.Errorf("service catalog: %s missing upstream_addr", svc.Name)
		}
		switch svc.Auth {
		case AuthNone, AuthMTLS, AuthUser, AuthEither:
		default:
			return fmt.Errorf("service catalog: %s has invalid auth mode %q", svc.Name, svc.Auth)
		}
	}
	return nil
}
