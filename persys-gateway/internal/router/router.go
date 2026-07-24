// Package router wires the gateway's HTTP surface from two sources:
//
//  1. The service catalog (internal/catalog) — plain reverse-proxy
//     services, registered generically.
//  2. Self-registering controllers implementing Registrar — services
//     that need bespoke logic (ClusterMetaController, webhook HMAC
//     verification, GitHub OAuth) or typed proto translation.
//
// The dynamic RPC surface (internal/grpcbridge) is wired separately in
// main.go since it needs its own mount group and binding list — see
// internal/router/bindings.go.
package router

import (
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/persys-dev/persys-cloud/persys-gateway/internal/authn"
	"github.com/persys-dev/persys-cloud/persys-gateway/internal/catalog"
	"github.com/persys-dev/persys-cloud/persys-gateway/utils"
)

// Registrar is implemented by any controller that owns its own routes.
type Registrar interface {
	Register(rg *gin.RouterGroup, auth *authn.Middleware)
}

type Router struct {
	auth *authn.Middleware
	mode catalog.DeploymentMode
}

func New(auth *authn.Middleware, mode catalog.DeploymentMode) *Router {
	return &Router{auth: auth, mode: mode}
}

// RegisterCatalog builds generic reverse-proxy routes from an optional
// service catalog file. If the file doesn't exist, this is a no-op (info
// log, not fatal) — the catalog is for services added later without any
// gateway code change; not having one yet is a normal, out-of-the-box
// state, not an error.
func (r *Router) RegisterCatalog(rg *gin.RouterGroup, catalogPath string) error {
	if catalogPath == "" {
		return nil
	}
	if _, err := os.Stat(catalogPath); os.IsNotExist(err) {
		return nil
	}
	cat, err := catalog.Load(catalogPath)
	if err != nil {
		return err
	}
	for _, svc := range cat.Services {
		if !svc.Enabled {
			continue
		}
		svc := svc

		group := rg.Group(svc.PathPrefix)
		switch svc.Auth.Resolve(r.mode) {
		case catalog.AuthUser:
			group.Use(r.auth.RequireUser())
		case catalog.AuthMTLS:
			group.Use(r.auth.RequireMTLS())
		case catalog.AuthEither:
			group.Use(r.auth.RequireMTLSOrUser())
		case catalog.AuthNone:
			// intentionally no middleware
		}

		group.Any("/*proxyPath", catalogProxyHandler(svc))
	}
	return nil
}

// RegisterControllers wires any number of self-registering controllers
// onto the given group.
func (r *Router) RegisterControllers(rg *gin.RouterGroup, registrars ...Registrar) {
	for _, reg := range registrars {
		reg.Register(rg, r.auth)
	}
}

// Resolve exposes the deployment-mode auth resolution to callers that
// need to pick a raw gin.HandlerFunc without a full catalog entry (e.g.
// grpcbridge bindings in main.go).
func (r *Router) Resolve(intent catalog.AuthMode) gin.HandlerFunc {
	switch intent.Resolve(r.mode) {
	case catalog.AuthUser:
		return r.auth.RequireUser()
	case catalog.AuthMTLS:
		return r.auth.RequireMTLS()
	case catalog.AuthEither:
		return r.auth.RequireMTLSOrUser()
	default:
		return func(c *gin.Context) { c.Next() }
	}
}

func catalogProxyHandler(svc catalog.Service) gin.HandlerFunc {
	timeout := svc.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return func(c *gin.Context) {
		upstreamPath := c.Param("proxyPath")
		if !svc.StripPrefix {
			upstreamPath = strings.TrimSuffix(svc.PathPrefix, "/") + upstreamPath
		}
		utils.ProxyRequest(c, svc.UpstreamAddr+upstreamPath, &utils.ProxyOptions{
			Timeout: timeout,
			Headers: map[string]string{"X-Forwarded-For": c.ClientIP()},
		})
	}
}
