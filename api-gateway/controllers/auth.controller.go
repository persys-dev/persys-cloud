package controllers

import (
	"context"
	"fmt"
	jwtlib "github.com/dgrijalva/jwt-go"
	"github.com/dgrijalva/jwt-go/request"
	"github.com/gin-gonic/gin"
	"github.com/google/go-github/github"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/config"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/models"
	mongodbHandler "github.com/miladhzzzz/milx-cloud-init/api-gateway/pkg/mongodb"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/services"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/utils"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/oauth2"
	oauth2gh "golang.org/x/oauth2/github"
	"net/http"
	"time"
)

var (
	conf                  *oauth2.Config
	state                 string
	users                 *models.UserInput
	repos                 *models.Repos
	mySuperSecretPassword = "unicornsAreAwesome"
	cnf, _                = config.ReadConfig()
)

type Credentials struct {
	ClientID     string `json:"clientid"`
	ClientSecret string `json:"secret"`
}

type AuthController struct {
	authService   services.AuthService
	githubService services.GithubService
	//userService services.UserService
	ctx        context.Context
	collection *mongo.Collection
}

func NewAuthController(authService services.AuthService, ctx context.Context, githubService services.GithubService, collection *mongo.Collection) AuthController {
	return AuthController{authService: authService, githubService: githubService, ctx: ctx}
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
				b := []byte(mySuperSecretPassword)
				return b, nil
			})

			//status := checkUser(tkn)
			//
			//if status == false {
			//	//fmt.Println("status is false")
			//	ctx.AbortWithStatus(401)
			//	return
			//}
			if err != nil {
				ctx.AbortWithError(401, err)
				return
			}
		}

		if gitCode != "" {
			tok, err := conf.Exchange(context.Background(), ctx.Query("code"))
			if err != nil {
				ctx.AbortWithError(http.StatusBadRequest, fmt.Errorf("Failed to do exchange: %v", err))
				return
			}
			client := github.NewClient(conf.Client(context.Background(), tok))
			user, _, err := client.Users.Get(context.Background(), "")
			//client.Repositories.List(context.Background(), "", &github-auth.RepositoryListOptions{})
			if err != nil {
				ctx.AbortWithError(http.StatusBadRequest, fmt.Errorf("Failed to get user: %v", err))
				return
			}
			persysToken, err := utils.GenerateToken(user)

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

			ac.githubService.GetRepos(client)

			status, _ := ac.authService.SignInUser(&data)

			ctx.JSON(http.StatusOK, status)
		}
	}
}

func connectGithub(ctx *gin.Context) *github.Client {
	user := ReadUserData(ctx, mySuperSecretPassword)
	dbc, err := mongodbHandler.Dbc()
	q := dbc.Database("api-gateway").Collection("users")
	data := q.FindOne(context.Background(), bson.M{"login": user.Login})
	var datas *models.UserInput

	if err = data.Decode(&datas); err != nil {

	}
	tok := datas.GithubToken
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: tok},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)
	return client
}

func ReadUserData(ctx *gin.Context, secret string) (userData *models.UserInput) {
	dbc, err := mongodbHandler.Dbc()
	data, err := request.ParseFromRequest(ctx.Request, request.OAuth2Extractor, func(token *jwtlib.Token) (interface{}, error) {
		b := []byte(secret)
		return b, nil
	})
	if err != nil {
		//ctx.AbortWithStatus(http.StatusBadRequest)
		return nil
	}
	q := dbc.Database("api-gateway").Collection("users")
	user := data.Claims.(jwtlib.MapClaims)
	//username := user["Name"].(string)
	UserID := user["UserID"].(float64)
	dbres := q.FindOne(context.Background(), bson.M{"userid": UserID})

	err = dbres.Decode(&userData)

	if err != nil {
		return nil
	}
	return
}

func SetRepoWebhook(ctx *gin.Context, repoName, webhookURL, secret string) {
	user := ReadUserData(ctx, mySuperSecretPassword)

	name := "web"
	active := true
	client := connectGithub(ctx)
	//config := map[string]interface{}("URL": webhookURL, "content-type": "json", "secret": secret)
	_, _, err := client.Repositories.CreateHook(context.Background(), user.Login, repoName, &github.Hook{
		Name:   &name,
		Events: []string{"push"},
		Active: &active,
		Config: map[string]interface{}{"url": webhookURL, "content-type": "json", "insecure-ssl": "0", "secret": secret},
	})
	if err != nil {
		fmt.Println(err)
		ctx.AbortWithStatus(400)
	}

}

func CreateFork(ctx *gin.Context, owner, repo string) {
	client := connectGithub(ctx)

	_, r, err := client.Repositories.CreateFork(context.Background(), owner, repo, &github.RepositoryCreateForkOptions{})
	if err != nil {
		ctx.JSON(http.StatusOK, err)
		return
	}
	if r.Status == "202" {
		ctx.JSON(http.StatusOK, "successfully forked your repo!")
	}

}

func (ac *AuthController) LoginHandler() gin.HandlerFunc {

	return func(c *gin.Context) {
		state = utils.RandToken()
		c.JSON(http.StatusOK, gin.H{"URL": GetLoginURL(state)})
	}
	//ac.authService.SignInUser()

}

func (ac *AuthController) Setup(redirectURL string, scopes []string) {
	// IMPORTANT SECURITY ISSUE
	c := Credentials{
		ClientID:     cnf.ClientID,
		ClientSecret: cnf.ClientSecret,
	}

	conf = &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		RedirectURL:  redirectURL,
		Scopes:       scopes,
		Endpoint:     oauth2gh.Endpoint,
	}
}

func GetLoginURL(state string) string {
	return conf.AuthCodeURL(state)
}

func stringFromPointer(strPtr *string) (res string) {
	if strPtr == nil {
		res = ""
		return res
	}
	res = *strPtr
	return res
}
