package utils

import (
	"crypto/rand"
	"encoding/base64"
	jwtlib "github.com/dgrijalva/jwt-go"
	"github.com/golang/glog"
	"github.com/google/go-github/github"
	"time"
)

func GenerateToken(user *github.User) (tok string, err error) {
	// Create the token
	token := jwtlib.New(jwtlib.GetSigningMethod("HS256"))
	// Set some claims
	token.Claims = jwtlib.MapClaims{
		"Name":   user.Login,
		"UserID": user.ID,
		"exp":    time.Now().Add(time.Hour * 1).Unix(),
	}
	// Sign and get the complete encoded token as a string
	mySuperSecretPassword := "unicornsAreAwesome"

	tokenString, err := token.SignedString([]byte(mySuperSecretPassword))
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
