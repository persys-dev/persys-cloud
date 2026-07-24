package utils

import (
	"crypto/rand"
	"encoding/base64"
	"time"

	jwtlib "github.com/dgrijalva/jwt-go"
	"github.com/golang/glog"
	"github.com/google/go-github/github"
)

// GenerateToken signs a session token for user with secret. secret comes
// from config.Config.App.JWTSecret (env-sourced, never hardcoded — see
// config/config.go). Previously this was a literal string,
// "unicornsAreAwesome", committed in a public repo; anyone with the
// source could mint a valid token for any user ID.
func GenerateToken(user *github.User, secret []byte) (tok string, err error) {
	token := jwtlib.New(jwtlib.GetSigningMethod("HS256"))
	token.Claims = jwtlib.MapClaims{
		"Name":   user.Login,
		"UserID": user.ID,
		"exp":    time.Now().Add(time.Hour * 1).Unix(),
	}
	tokenString, err := token.SignedString(secret)
	if err != nil {
		return "", err
	}
	return tokenString, nil
}

func RandToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		glog.Fatalf("[Gin-OAuth] Failed to read rand: %v\n", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}
