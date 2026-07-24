package tests

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-gateway/config"
	"github.com/persys-dev/persys-cloud/persys-gateway/controllers"
	"github.com/persys-dev/persys-cloud/persys-gateway/internal/store"
	"github.com/persys-dev/persys-cloud/persys-gateway/routes"
	"github.com/persys-dev/persys-cloud/persys-gateway/services"
	"github.com/stretchr/testify/assert"
)

var (
	redirectUri         = "http://localhost:8551/auth"
	AuthRouteController routes.AuthRouteController
	ctx                 = context.TODO()
)

// TestAuthRoute previously connected to a hardcoded MongoDB Atlas cluster
// with a username/password committed directly in this file. That
// credential was live and public in the repo; if this is your database,
// rotate it now regardless of this fix. The test now requires
// PERSYS_TEST_POSTGRES_DSN to be set and skips (not fails) otherwise, so
// running the suite doesn't require, or leak, real database credentials.
func TestAuthRoute(t *testing.T) {
	dsn := os.Getenv("PERSYS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PERSYS_TEST_POSTGRES_DSN not set — skipping integration test that requires a real Postgres instance")
	}

	db, err := store.New(ctx, dsn, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)

	testJWTSecret := []byte("test-only-secret-not-used-in-production")

	githubService := services.NewGithubService(&config.Config{}, &tls.Config{})
	authService := services.NewAuthService(db, ctx, testJWTSecret)
	authController := controllers.NewAuthController(
		authService, ctx, githubService, db,
		"test-client-id", "test-client-secret", testJWTSecret,
	)
	AuthRouteController = routes.NewAuthRouteController(authController, redirectUri)

	router := gin.Default()
	router.Use(gin.Logger())
	rg := router.Group("")

	rg.GET("/", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"status": "success", "message": "value"})
	})

	AuthRouteController.AuthRoute(rg)

	t.Run("Test LoginHandler", func(t *testing.T) {
		req, err := http.NewRequest("GET", "/api/auth/login", nil)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})
	t.Run("Test Cli", func(t *testing.T) {
		req, err := http.NewRequest("POST", "/api/auth/cli", nil)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})
	t.Run("Test Auth Middleware", func(t *testing.T) {
		req, err := http.NewRequest("GET", "/api/auth/", nil)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		req.Header.Set("Authorization", "Bearer valid_token")
		router.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})
	t.Run("Test Invalid Token Middleware", func(t *testing.T) {
		req, err := http.NewRequest("GET", "/api/auth/", nil)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		req.Header.Set("Authorization", "Bearer invalid_token")
		router.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}
