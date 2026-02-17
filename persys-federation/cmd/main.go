package main

import (
	"context"
	"log"
	"net"

	"github.com/persys-dev/persys-cloud/cloud-mgmt/config"
	"github.com/persys-dev/persys-cloud/cloud-mgmt/gapi"
	"github.com/persys-dev/persys-cloud/cloud-mgmt/internal/providers"
	"github.com/persys-dev/persys-cloud/cloud-mgmt/proto"
	"github.com/persys-dev/persys-cloud/cloud-mgmt/services"
	"github.com/persys-dev/persys-cloud/cloud-mgmt/utils"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"
)

var (
	ctx         context.Context
	mongoClient *mongo.Client
	redisClient *redis.Client
	cloudColl   *mongo.Collection
	cloudSvc    services.CloudService
)

func init() {
	config, err := config.ReadConfig()
	if err != nil {
		utils.AuditLog("Failed to load config: %v", err)
		log.Fatal(err)
	}

	ctx = context.Background()

	// Initialize MongoDB
	mongoConn := options.Client().ApplyURI(config.MongoURI).
		SetRetryWrites(true).
		SetRetryReads(true)
	mongoClient, err = mongo.Connect(ctx, mongoConn)
	if err != nil {
		utils.AuditLog("MongoDB connection failed: %v", err)
		log.Fatal(err)
	}
	if err := mongoClient.Ping(ctx, readpref.Primary()); err != nil {
		utils.AuditLog("MongoDB ping failed: %v", err)
		log.Fatal(err)
	}
	cloudColl = mongoClient.Database("cloud-mgmt").Collection("environments")

	// Initialize Redis
	redisClient = redis.NewClient(&redis.Options{
		Addr:     config.RedisAddr,
		Password: config.RedisPassword,
	})
	if _, err := redisClient.Ping(ctx).Result(); err != nil {
		utils.AuditLog("Redis connection failed: %v", err)
		log.Fatal(err)
	}

	// Initialize Tracing
	tp, err := initTracer(config.JaegerEndpoint)
	if err != nil {
		utils.AuditLog("Tracer initialization failed: %v", err)
		log.Fatal(err)
	}
	otel.SetTracerProvider(tp)

	// Initialize Cloud Service
	providerMgr := providers.NewManager(config, redisClient)
	cloudSvc = services.NewCloudService(cloudColl, ctx, providerMgr)
}

func initTracer(endpoint string) (*trace.TracerProvider, error) {
	exporter, err := jaeger.New(jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(endpoint)))
	if err != nil {
		return nil, err
	}
	tp := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("cloud-mgmt"),
		)),
	)
	return tp, nil
}

func startGrpcServer(config *config.Config) {
	creds, err := credentials.NewServerTLSFromFile(config.TLSCert, config.TLSKey)
	if err != nil {
		utils.AuditLog("Failed to load TLS credentials: %v", err)
		log.Fatal(err)
	}

	grpcServer := grpc.NewServer(grpc.Creds(creds))
	cloudServer, err := gapi.NewGrpcCloudServer(config, cloudSvc)
	if err != nil {
		utils.AuditLog("Failed to create gRPC server: %v", err)
		log.Fatal(err)
	}

	pb.RegisterCloudMgmtServiceServer(grpcServer, cloudServer)
	reflection.Register(grpcServer)

	listener, err := net.Listen("tcp", config.GrpcServerAddress)
	if err != nil {
		utils.AuditLog("Failed to start gRPC listener: %v", err)
		log.Fatal(err)
	}

	log.Printf("gRPC server started on %s", config.GrpcServerAddress)
	if err := grpcServer.Serve(listener); err != nil {
		utils.AuditLog("gRPC server failed: %v", err)
		log.Fatal(err)
	}
}

func main() {
	config, err := config.ReadConfig()
	if err != nil {
		log.Fatal(err)
	}
	startGrpcServer(config)
}