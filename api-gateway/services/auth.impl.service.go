package services

import (
	"context"
	"errors"
	"fmt"
	jwtlib "github.com/dgrijalva/jwt-go"
	"github.com/dgrijalva/jwt-go/request"
	"github.com/gin-gonic/gin"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/models"
	"time"

	//"github.com/wpcodevo/golang-mongodb/utils"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type AuthServiceImpl struct {
	collection *mongo.Collection
	ctx        context.Context
}

func (uc *AuthServiceImpl) ReadUserData(ctx *gin.Context) (*models.DBResponse, error) {

	var result *models.DBResponse
	data, err := request.ParseFromRequest(ctx.Request, request.OAuth2Extractor, func(token *jwtlib.Token) (interface{}, error) {
		b := []byte("unicornsAreAwesome")
		return b, nil
	})

	if err != nil {
		return nil, err
	}

	user := data.Claims.(jwtlib.MapClaims)

	UserID := user["UserID"].(float64)

	res := uc.collection.FindOne(uc.ctx, bson.M{"userid": UserID})

	if res.Err() == mongo.ErrNoDocuments {
		return nil, res.Err()
	}

	err = res.Decode(&result)

	if err != nil {
		return nil, err
	}

	return result, nil

}

func (uc *AuthServiceImpl) CliLogin(req *models.CliReq) (*models.DBResponse, error) {
	res := uc.collection.FindOne(uc.ctx, bson.M{"state": req.State})

	var result *models.DBResponse

	if res.Err() != mongo.ErrNoDocuments {
		err := res.Decode(&result)
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	return nil, res.Err()
}

func (uc *AuthServiceImpl) CheckUser() {
	//TODO implement me
	panic("implement me")
}

func NewAuthService(collection *mongo.Collection, ctx context.Context) AuthService {
	return &AuthServiceImpl{collection, ctx}
}

func (uc *AuthServiceImpl) SignInUser(user *models.UserInput) (*models.DBResponse, error) {

	// check if a user exists
	check := uc.collection.FindOne(uc.ctx, bson.M{"userID": user.UserID})

	if check.Err() != mongo.ErrNoDocuments {
		update := uc.collection.FindOneAndUpdate(uc.ctx, bson.M{"userid": user.UserID},
			bson.M{"$set": bson.M{
				"updatedat":   time.Now().String(),
				"persystoken": user.PersysToken,
				"githubtoken": user.GithubToken,
				"state":       user.State,
			}})
		fmt.Print(update)
	}

	res, err := uc.collection.InsertOne(uc.ctx, &user)

	if err != nil {
		if er, ok := err.(mongo.WriteException); ok && er.WriteErrors[0].Code == 11000 {
			return nil, errors.New("user with that email already exist")
		}
		return nil, err
	}

	// Create a unique index for the email field
	//opt := options.Index()
	//opt.SetUnique(true)
	//index := mongo.IndexModel{Keys: bson.M{"email": 1}, Options: opt}

	//if _, err := uc.collection.Indexes().CreateOne(uc.ctx, index); err != nil {
	//	return nil, errors.New("could not create index for email")
	//}

	var newUser *models.DBResponse
	query := bson.M{"_id": res.InsertedID}

	err = uc.collection.FindOne(uc.ctx, query).Decode(&newUser)
	if err != nil {
		return nil, err
	}

	return newUser, nil
}
