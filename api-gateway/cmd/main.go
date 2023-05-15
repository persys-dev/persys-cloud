package main

import (
	"context"
	"fmt"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/config"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/controllers"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/internal/trigger-grpc"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/routes"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/services"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"log"
	"net/http"
)

var (
	cnf, _                = config.ReadConfig()
	serviceName           = "persys-api-gateway"
	mySuperSecretPassword = "unicornsAreAwesome"
	webhookUrl            = cnf.WebHookURL    // Demo deployment on azure
	webhookSecret         = cnf.WebHookSecret // SECURITY ISSUE *** CHANGE THIS!

	server      *gin.Engine
	ctx         context.Context
	mongoclient *mongo.Client

	authCollection      *mongo.Collection
	authService         services.AuthService
	AuthController      controllers.AuthController
	AuthRouteController routes.AuthRouteController

	//👇 Create the Github Variables
	githubService services.GithubService
	//GithubController      controllers.g
	GithubCollection *mongo.Collection
	//GithubRouteController routes.PostRouteController
)

func init() {

	//cnf, _ = config.ReadConfig()

	ctx = context.TODO()

	// Connect to MongoDB
	mongoconn := options.Client().ApplyURI(cnf.MongoURI)
	mongoclient, err := mongo.Connect(ctx, mongoconn)

	if err != nil {
		panic(err)
	}

	if err := mongoclient.Ping(ctx, readpref.Primary()); err != nil {
		panic(err)
	}

	fmt.Println("MongoDB successfully connected...")

	// Collections
	authCollection = mongoclient.Database("api-gateway").Collection("users")
	githubService = services.NewGithubService(GithubCollection, ctx)
	authService = services.NewAuthService(authCollection, ctx)
	AuthController = controllers.NewAuthController(authService, ctx, githubService, authCollection)
	AuthRouteController = routes.NewAuthRouteController(AuthController)

	//UserController = controllers.NewUserController(userService)
	//UserRouteController = routes.NewRouteUserController(UserController)
	//
	//// 👇 Instantiate the Constructors
	//postCollection = mongoclient.Database("golang_mongodb").Collection("posts")
	//postService = services.NewPostService(postCollection, ctx)
	//PostController = controllers.NewPostController(postService)
	//PostRouteController = routes.NewPostControllerRoute(PostController)

	server = gin.Default()

}

func main() {

	//cleanup := opentelemtry.InitTracer()
	//
	//	//defer errorhandler.ErrHandler()
	//
	//defer cleanup(context.Background())

	//defer mongoclient.Disconnect(ctx)

	//// 👇 Instantiate event processor

	// starting grpc trigger mechanism that calls events-manager service over gRPC
	go trigger_grpc.StartgRPCtrigger()
	// starting gin http server
	startGinServer()
	//startGrpcServer(config)

}

func startGinServer() {

	corsConfig := cors.DefaultConfig()
	corsConfig.AllowOrigins = []string{"http://localhost:8551"}
	corsConfig.AllowCredentials = true

	server.Use(cors.New(corsConfig))
	server.Use(otelgin.Middleware(serviceName))

	router := server.Group("")
	router.GET("/healthchecker", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"status": "success", "message": "value"})
	})

	AuthRouteController.AuthRoute(router)
	//gRouteController.UserRoute(router, userService)
	// 👇 Post Route
	//PostRouteController.PostRoute(router)
	log.Fatal(server.Run(cnf.HttpAddr))
}
