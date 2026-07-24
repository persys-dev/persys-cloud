package services

import (
	"context"

	jwtlib "github.com/dgrijalva/jwt-go"
	"github.com/dgrijalva/jwt-go/request"
	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-gateway/internal/store"
	"github.com/persys-dev/persys-cloud/persys-gateway/models"
)

type AuthServiceImpl struct {
	store     *store.Store
	ctx       context.Context
	jwtSecret []byte
}

func NewAuthService(st *store.Store, ctx context.Context, jwtSecret []byte) AuthService {
	return &AuthServiceImpl{store: st, ctx: ctx, jwtSecret: jwtSecret}
}

func (uc *AuthServiceImpl) ReadUserData(ctx *gin.Context) (*models.DBResponse, error) {
	data, err := request.ParseFromRequest(ctx.Request, request.OAuth2Extractor, func(token *jwtlib.Token) (interface{}, error) {
		return uc.jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}
	claims := data.Claims.(jwtlib.MapClaims)
	userID := int64(claims["UserID"].(float64))
	return uc.store.FindUserByID(ctx.Request.Context(), userID)
}

func (uc *AuthServiceImpl) CliLogin(req *models.CliReq) (*models.DBResponse, error) {
	return uc.store.FindUserByState(uc.ctx, req.State)
}

func (uc *AuthServiceImpl) CheckUser() {
	//TODO implement me
	panic("implement me")
}

// SignInUser creates or updates a user record. Delegates to
// store.UpsertUser, whose Postgres ON CONFLICT clause makes this
// atomic — no separate exists-check-then-branch, which is exactly what
// let the old Mongo implementation silently create duplicate user rows
// on every subsequent login (the insert ran unconditionally regardless
// of which branch was taken, relying on a unique index that was never
// actually created to catch it).
func (uc *AuthServiceImpl) SignInUser(user *models.UserInput) (*models.DBResponse, error) {
	return uc.store.UpsertUser(uc.ctx, user)
}

func (a *AuthServiceImpl) IsAuthenticated(ctx *gin.Context) bool {
	_, err := request.ParseFromRequest(ctx.Request, request.OAuth2Extractor, func(token *jwtlib.Token) (interface{}, error) {
		return a.jwtSecret, nil
	})
	return err == nil
}
