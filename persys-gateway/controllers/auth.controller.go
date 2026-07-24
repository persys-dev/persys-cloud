package controllers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	jwtlib "github.com/dgrijalva/jwt-go"
	"github.com/dgrijalva/jwt-go/request"
	"github.com/gin-gonic/gin"
	"github.com/google/go-github/github"
	"github.com/persys-dev/persys-cloud/persys-gateway/internal/store"
	"github.com/persys-dev/persys-cloud/persys-gateway/models"
	"github.com/persys-dev/persys-cloud/persys-gateway/services"
	"github.com/persys-dev/persys-cloud/persys-gateway/utils"
	"golang.org/x/oauth2"
	oauth2gh "golang.org/x/oauth2/github"
)

// AuthController previously depended on three package-level mutable
// variables: `conf` (*oauth2.Config, written by Setup, read by every
// request), `state` (the last-issued CSRF token, overwritten on every
// login attempt — a real race under concurrent logins), and `cnf` (a
// second, independent config.LoadConfig() call happening at package
// init, racing whatever main.go's own load did). All three are gone:
// oauthConfig and jwtSecret are receiver fields set once at construction,
// and the CSRF token lives in Postgres (oauth_sessions table) keyed by
// the token itself, not remembered in a Go variable at all.
type AuthController struct {
	authService   services.AuthService
	githubService services.GithubService
	ctx           context.Context
	store         *store.Store

	oauthConfig *oauth2.Config
	jwtSecret   []byte
}

func NewAuthController(
	authService services.AuthService,
	ctx context.Context,
	githubService services.GithubService,
	st *store.Store,
	githubClientID string,
	githubClientSecret string,
	jwtSecret []byte,
) AuthController {
	return AuthController{
		authService:   authService,
		githubService: githubService,
		ctx:           ctx,
		store:         st,
		oauthConfig: &oauth2.Config{
			ClientID:     githubClientID,
			ClientSecret: githubClientSecret,
			Endpoint:     oauth2gh.Endpoint,
		},
		jwtSecret: jwtSecret,
	}
}

func (ac *AuthController) Cli() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req *models.CliReq

		err := c.BindJSON(&req)
		if err != nil {
			return
		}

		ac.authService.CliLogin(req)
	}
}

func (ac *AuthController) Auth() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		gitCode := ctx.Query("code")
		idempotencyID := ctx.Query("state")

		if ctx.Request.Header.Get("Authorization") == "" && gitCode == "" {
			ctx.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		if ctx.Request.Header.Get("Authorization") != "" {
			_, err := request.ParseFromRequest(ctx.Request, request.OAuth2Extractor, func(token *jwtlib.Token) (interface{}, error) {
				return ac.jwtSecret, nil
			})

			if err != nil {
				ctx.AbortWithError(http.StatusUnauthorized, err)
				return
			}
		}

		if gitCode != "" {
			if err := ac.store.ValidateAndConsumeState(ctx.Request.Context(), idempotencyID); err != nil {
				ctx.AbortWithError(http.StatusUnauthorized, fmt.Errorf("invalid oauth state: %v", err))
				return
			}

			tok, err := ac.oauthConfig.Exchange(context.Background(), ctx.Query("code"))
			if err != nil {
				ctx.AbortWithError(http.StatusBadRequest, fmt.Errorf("failed to do exchange: %v", err))
				return
			}
			client := github.NewClient(ac.oauthConfig.Client(context.Background(), tok))
			user, _, err := client.Users.Get(context.Background(), "")
			if err != nil {
				ctx.AbortWithError(http.StatusBadRequest, fmt.Errorf("failed to get user: %v", err))
				return
			}
			persysToken, _ := utils.GenerateToken(user, ac.jwtSecret)

			data := models.UserInput{
				Login:       stringFromPointer(user.Login),
				Name:        stringFromPointer(user.Name),
				Email:       stringFromPointer(user.Email),
				Company:     stringFromPointer(user.Company),
				URL:         stringFromPointer(user.URL),
				GithubToken: tok.AccessToken,
				UserID:      *user.ID,
				PersysToken: persysToken,
				State:       idempotencyID,
				CreatedAt:   time.Now().String(),
			}

			_ = ac.githubService.SetAccessToken(&models.DBResponse{
				Login:       data.Login,
				GithubToken: data.GithubToken,
				UserID:      data.UserID,
			})

			status, _ := ac.authService.SignInUser(&data)

			ctx.JSON(http.StatusOK, status)
		}
	}
}

func (ac *AuthController) LoginHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		state := utils.RandToken()
		if err := ac.store.StoreOAuthState(c.Request.Context(), state, 10*time.Minute); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start login"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"URL": ac.oauthConfig.AuthCodeURL(state)})
	}
}

// Setup finishes wiring the OAuth redirect URL and scopes now that
// they're known (both come from config, resolved once in main.go).
// ClientID/ClientSecret are already set from the constructor — Setup no
// longer re-reads config itself, which is what caused the double
// config.LoadConfig() call this replaces.
func (ac *AuthController) Setup(redirectURL string, scopes []string) {
	ac.oauthConfig.RedirectURL = redirectURL
	ac.oauthConfig.Scopes = scopes
}

func stringFromPointer(strPtr *string) (res string) {
	if strPtr == nil {
		res = ""
		return res
	}
	res = *strPtr
	return res
}
