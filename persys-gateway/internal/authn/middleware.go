// Package authn provides the auth middleware used by the dynamic RPC
// bridge and other new-style routes. It deliberately verifies the exact
// same tokens utils.GenerateToken issues (same secret, same dgrijalva/
// jwt-go library, same "UserID" claim) rather than introducing a second
// JWT library or claims schema — the gateway has exactly one session
// token format, used everywhere a bearer token is accepted.
//
// This does NOT replace controllers.AuthController.Auth(), which handles
// the OAuth exchange itself (GitHub code -> token issuance). It replaces
// the *verification* half for routes that don't need the OAuth dance —
// cluster control, forgery, automation — with one addition:
// RequireClusterOwnership, needed once cluster-per-tenant ownership
// exists (see db/migrations when that lands; not wired yet).
package authn

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	jwtlib "github.com/dgrijalva/jwt-go"
	"github.com/dgrijalva/jwt-go/request"
	"github.com/gin-gonic/gin"
)

type contextKey string

const userIDKey contextKey = "persys_user_id"

type Middleware struct {
	secret []byte
}

// New builds the middleware from the same secret used to sign tokens
// (cnf.App.JWTSecret) — see config.Config.App.JWTSecret for how that
// secret is sourced (env var, never hardcoded, fails fast if unset in
// managed mode).
func New(secret []byte) *Middleware {
	return &Middleware{secret: secret}
}

func (m *Middleware) parseAndVerify(r *http.Request) (userID string, err error) {
	token, err := request.ParseFromRequest(r, request.OAuth2Extractor, func(t *jwtlib.Token) (interface{}, error) {
		return m.secret, nil
	})
	if err != nil {
		return "", err
	}
	claims, ok := token.Claims.(jwtlib.MapClaims)
	if !ok || !token.Valid {
		return "", errors.New("invalid token claims")
	}
	raw, ok := claims["UserID"]
	if !ok {
		return "", errors.New("token missing UserID claim")
	}
	switch v := raw.(type) {
	case float64:
		return strconv.FormatInt(int64(v), 10), nil
	case string:
		return v, nil
	default:
		return "", fmt.Errorf("unexpected UserID claim type %T", raw)
	}
}

// RequireUser rejects any request without a valid, non-expired user JWT
// and attaches the verified user ID to the context.
func (m *Middleware) RequireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := m.parseAndVerify(c.Request)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		c.Set(string(userIDKey), userID)
		c.Next()
	}
}

// RequireMTLS rejects any request that didn't present a verified client
// certificate on this connection. Used for internal service-to-service
// routes (persysctl node/cluster ops) that carry no user identity.
func (m *Middleware) RequireMTLS() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !hasVerifiedClientCert(c.Request) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "client certificate required"})
			return
		}
		c.Next()
	}
}

// RequireMTLSOrUser accepts either trust path: a verified client cert
// (service-to-service) or a valid user JWT (managed-mode customer
// traffic).
func (m *Middleware) RequireMTLSOrUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		if hasVerifiedClientCert(c.Request) {
			c.Next()
			return
		}
		userID, err := m.parseAndVerify(c.Request)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "client certificate or bearer token required"})
			return
		}
		c.Set(string(userIDKey), userID)
		c.Next()
	}
}

// ClusterOwnership answers "does this user own this cluster?" — the
// entire multi-tenancy surface for the managed offering, checked once at
// the gateway boundary rather than threaded through every downstream
// service. Not wired into main.go yet (needs the cluster_owners table),
// but the middleware is ready for when it is.
type ClusterOwnership interface {
	Owns(ctx context.Context, userID, clusterID string) (bool, error)
}

// RequireClusterOwnership must run after RequireUser (or
// RequireMTLSOrUser) on any route with a :cluster_id path param
// representing a customer-owned cluster. In self-hosted mode this
// middleware is simply never attached, so it has zero cost and zero
// relevance there.
func (m *Middleware) RequireClusterOwnership(store ClusterOwnership) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := UserID(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		clusterID := c.Param("cluster_id")
		if clusterID == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "cluster_id required"})
			return
		}
		owns, err := store.Owns(c.Request.Context(), userID, clusterID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "ownership check failed"})
			return
		}
		if !owns {
			// 404, not 403 — don't confirm the cluster_id exists to a
			// caller who doesn't own it.
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "cluster not found"})
			return
		}
		c.Next()
	}
}

// UserID returns the verified user ID attached by RequireUser or
// RequireMTLSOrUser, if any.
func UserID(c *gin.Context) (string, bool) {
	v, ok := c.Get(string(userIDKey))
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok && s != ""
}

func hasVerifiedClientCert(r *http.Request) bool {
	return r.TLS != nil && len(r.TLS.VerifiedChains) > 0 && len(r.TLS.PeerCertificates) > 0
}
