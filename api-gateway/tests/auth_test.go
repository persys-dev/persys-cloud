package tests

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/controllers"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/routes"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/services"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"net/http"
	"net/http/httptest"
	"testing"
)

var (
	scopes = []string{
		"repo",
		"write:repo_hook",
		"user",
		// You have to select your own scope from here -> https://developer.github.com/v3/oauth/#scopes
	}
	redirectUri         = "http://localhost:8551/auth"
	GithubCollection    *mongo.Collection
	AuthCollection      *mongo.Collection
	AuthRouteController routes.AuthRouteController
	ctx                 = context.TODO()
)

func TestAuthRoute(t *testing.T) {
	mongoconn := options.Client().ApplyURI("mongodb+srv://miladhzz:hXBfZeTBHvLbu0Fy@cluster0.nlik4mb.mongodb.net/?retryWrites=true&w=majority")
	mongoclient, err := mongo.Connect(ctx, mongoconn)
	if err != nil {
		t.Fatal(err)
	}
	err = mongoclient.Ping(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	GithubCollection = mongoclient.Database("api-gateway").Collection("repos")
	AuthCollection = mongoclient.Database("api-gateway").Collection("users")

	gin.SetMode(gin.TestMode)

	githubService := services.NewGithubService(GithubCollection, ctx)
	authService := services.NewAuthService(AuthCollection, ctx)
	authController := controllers.NewAuthController(authService, ctx, githubService, AuthCollection)
	AuthRouteController = routes.NewAuthRouteController(authController)

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
