package main

import (
	"context"
	"github.com/persys-dev/persys-devops/cloud-mgmt/config"
	"github.com/persys-dev/persys-devops/cloud-mgmt/gapi"
	"github.com/persys-dev/persys-devops/cloud-mgmt/internal/cloud-provider/persys"
	pb "github.com/persys-dev/persys-devops/cloud-mgmt/proto"
	"github.com/persys-dev/persys-devops/cloud-mgmt/services"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"log"
	"net"
)

var (
	ctx         context.Context
	mongoclient *mongo.Client

	cloudCollection *mongo.Collection
	cloudService    services.CloudService
)

func init() {
	config, err := config.ReadConfig()
	if err != nil {
		log.Fatal("Could not load environment variables", err)
	}

	ctx = context.TODO()

	// Connect to MongoDB
	mongoconn := options.Client().ApplyURI(config.MongoURI)
	mongoclient, err := mongo.Connect(ctx, mongoconn)

	if err != nil {
		log.Fatalf("error: %v", err)
	}

	if err := mongoclient.Ping(ctx, readpref.Primary()); err != nil {
		log.Fatalf("error: %v", err)
	}

	log.Println("MongoDB successfully connected...")

	// Collections
	cloudCollection = mongoclient.Database("cloud-mgmt").Collection("environment")

	// Cluster creation test method ----- >> THIS WILL BE MOVED SOON!!!!
	err = persys.CreateCluster()
	if err != nil {
		log.Printf("could not make a persys cluster for user because : %v", err)
	}

	//cloudService = services.NewAuthService(authCollection, ctx)
}

func startGrpcServer(config config.Config) {
	cloudServer, err := gapi.NewGrpcCloudServer(config, cloudService)
	if err != nil {
		log.Fatal("cannot create grpc authServer: ", err)
	}

	grpcServer := grpc.NewServer()

	pb.RegisterCloudMgmtServiceServer(grpcServer, cloudServer)

	// gRPC reflection registration for evans
	reflection.Register(grpcServer)

	listener, err := net.Listen("tcp", "config.GrpcServerAddress")
	if err != nil {
		log.Fatal("cannot create grpc server: ", err)
	}

	log.Printf("start gRPC server on %s", listener.Addr().String())
	err = grpcServer.Serve(listener)
	if err != nil {
		log.Fatal("cannot create grpc server: ", err)
	}
}

func main() {
	cnf, _ := config.ReadConfig()
	startGrpcServer(*cnf)
}
