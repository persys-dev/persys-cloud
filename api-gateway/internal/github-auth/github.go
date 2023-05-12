package github_auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/gob"
	"fmt"
	jwtlib "github.com/dgrijalva/jwt-go"
	"github.com/dgrijalva/jwt-go/request"
	"github.com/gin-gonic/gin"
	"github.com/golang/glog"
	"github.com/google/go-github/github"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/config"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/pkg/mongodb"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	oauth2gh "golang.org/x/oauth2/github"
)

type Credentials struct {
	ClientID     string `json:"clientid"`
	ClientSecret string `json:"secret"`
}

type AuthUser struct {
	Login       string `json:"login"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	Company     string `json:"company"`
	URL         string `json:"url"`
	GithubToken string `json:"githubToken"`
	UserID      int64  `json:"userID"`
	PersysToken string `json:"persysToken"`
	State       string `json:"state"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

var (
	cnf, _                = config.ReadConfig()
	conf                  *oauth2.Config
	state                 string
	mysupersecretpassword = "unicornsAreAwesome"
)

func randToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		glog.Fatalf("[Gin-OAuth] Failed to read rand: %v\n", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func Setup(redirectURL string, scopes []string) {
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

func LoginHandler(ctx *gin.Context) {
	state = randToken()
	ctx.JSON(http.StatusOK, gin.H{"URL": GetLoginURL(state)})
}

func GetLoginURL(state string) string {
	return conf.AuthCodeURL(state)
}

func init() {
	gob.Register(AuthUser{})
}

// Auth TODO Security flaw data over exposure !! FIX
func Auth(secret string) gin.HandlerFunc {
	dbc, err := mongodbHandler.Dbc()
	if err != nil {

	}

	q := dbc.Database("api-gateway").Collection("users")
	return func(ctx *gin.Context) {

		gitCode := ctx.Query("code")
		idempotencyID := ctx.Query("state")

		if ctx.Request.Header.Get("Authorization") == "" && gitCode == "" {
			ctx.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		if ctx.Request.Header.Get("Authorization") != "" {
			tkn, err := request.ParseFromRequest(ctx.Request, request.OAuth2Extractor, func(token *jwtlib.Token) (interface{}, error) {
				b := []byte(secret)
				return b, nil
			})

			status := checkUser(tkn)

			if status == false {
				//fmt.Println("status is false")
				ctx.AbortWithStatus(401)
				return
			}
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
			persysToken, err := generateToken(user)

			data := AuthUser{
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

			go getGithubRepo(client)

			status := createUser(&data)

			if status == "Found" {
				update := q.FindOneAndUpdate(context.Background(), bson.M{"userid": data.UserID},
					bson.M{"$set": bson.M{
						"updatedat":   time.Now().String(),
						"persystoken": data.PersysToken,
						"githubtoken": data.GithubToken,
						"state":       idempotencyID,
					}})
				if update != nil {
					//fmt.Println(update.DecodeBytes())
					ctx.JSON(http.StatusOK, data)
				}
				return
			}
			if status == "Created" {
				ctx.JSON(http.StatusOK, data)
			}
			if status == "" {
				ctx.AbortWithStatus(400)
			}
		}
	}
}

func createUser(data *AuthUser) (status string) {
	dbc, err := mongodbHandler.Dbc()
	q := dbc.Database("api-gateway").Collection("users")
	find := q.FindOne(context.TODO(), bson.M{"userid": data.UserID})

	//fmt.Println(find.DecodeBytes())

	if find.Err() == nil {
		return "Found"
	}

	insert, err := dbc.Database("api-gateway").Collection("users").InsertOne(context.TODO(), &data)

	if err != nil {
		fmt.Println(err.Error())
		return ""
	}
	//fmt.Println(res.InsertedID)

	if insert.InsertedID != nil {
		return "Created"
	}

	return ""
}

func checkUser(token *jwtlib.Token) (status bool) {
	dbc, err := mongodbHandler.Dbc()
	q := dbc.Database("api-gateway").Collection("users")
	res := q.FindOne(context.Background(), bson.M{"persystoken": token.Raw})
	if err != nil {

	}
	//fmt.Println(res.DecodeBytes())

	if res.Err() != nil {
		return false
	}
	return true
}

func generateToken(user *github.User) (tok string, err error) {
	// Create the token
	token := jwtlib.New(jwtlib.GetSigningMethod("HS256"))
	// Set some claims
	token.Claims = jwtlib.MapClaims{
		"Name":   user.Login,
		"UserID": user.ID,
		"exp":    time.Now().Add(time.Hour * 1).Unix(),
	}
	// Sign and get the complete encoded token as a string
	tokenString, err := token.SignedString([]byte(mysupersecretpassword))
	if err != nil {
		return "", err
	}
	return tokenString, nil
}

func connectGithub(ctx *gin.Context) *github.Client {
	user := ReadUserData(ctx, mysupersecretpassword)
	dbc, err := mongodbHandler.Dbc()
	q := dbc.Database("api-gateway").Collection("users")
	data := q.FindOne(context.Background(), bson.M{"login": user.Login})
	var datas *AuthUser

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

func ReadUserData(ctx *gin.Context, secret string) (userData *AuthUser) {
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

func getGithubRepo(client *github.Client) {
	dbc, err := mongodbHandler.Dbc()
	q := dbc.Database("api-gateway").Collection("repos")
	repos, _, err := client.Repositories.List(context.Background(), "", &github.RepositoryListOptions{})

	test := q.FindOne(context.Background(), bson.M{"repoID": repos[1].ID})
	if err != nil {

	}
	if test.Err() == mongo.ErrNoDocuments {
		for _, repo := range repos {
			data := bson.M{
				"repoID":      repo.ID,
				"gitURL":      repo.GetCloneURL(),
				"name":        repo.Name,
				"owner":       repo.Owner.Login,
				"userid":      repo.Owner.ID,
				"private":     repo.Private,
				"accessToken": "",
				"webhookURL":  "",
				"eventID":     "",
			}
			dbm, err := q.InsertOne(context.TODO(), &data)
			if err != nil {
				return
			}
			fmt.Println(dbm.InsertedID)
		}
	}
	return
}

func SetRepoWebhook(ctx *gin.Context, repoName, webhookURL, secret string) {
	user := ReadUserData(ctx, mysupersecretpassword)

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

func stringFromPointer(strPtr *string) (res string) {
	if strPtr == nil {
		res = ""
		return res
	}
	res = *strPtr
	return res
}
