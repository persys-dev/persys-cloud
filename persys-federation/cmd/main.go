package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"github.com/persys-dev/persys-cloud/persys-federation/config"
	"github.com/persys-dev/persys-cloud/persys-federation/gapi"
	"github.com/persys-dev/persys-cloud/persys-federation/internal/providers"
	pb "github.com/persys-dev/persys-cloud/persys-federation/proto"
	"github.com/persys-dev/persys-cloud/persys-federation/services"
	"github.com/persys-dev/persys-cloud/persys-federation/utils"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var (
	ctx         context.Context
	mongoClient *mongo.Client
	redisClient *redis.Client
	cloudDB     *mongo.Database
	cloudSvc    services.CloudService
)

func init() {
	config, err := config.ReadConfig()
	if err != nil {
		utils.AuditLog(fmt.Sprintf("Failed to load config: %v", err))
		log.Fatal(err)
	}

	ctx = context.Background()

	// Initialize MongoDB
	mongoConn := options.Client().ApplyURI(config.MongoURI).
		SetRetryWrites(true).
		SetRetryReads(true)
	mongoClient, err = mongo.Connect(ctx, mongoConn)
	if err != nil {
		utils.AuditLog(fmt.Sprintf("MongoDB connection failed: %v", err))
		log.Fatal(err)
	}
	if err := mongoClient.Ping(ctx, readpref.Primary()); err != nil {
		utils.AuditLog(fmt.Sprintf("MongoDB ping failed: %v", err))
		log.Fatal(err)
	}
	cloudDB = mongoClient.Database("persys-federation")

	// Initialize Redis
	redisClient = redis.NewClient(&redis.Options{
		Addr:     getenvDefault("REDIS_ADDR", "localhost:6379"),
		Password: getenvDefault("REDIS_PASSWORD", ""),
	})
	if _, err := redisClient.Ping(ctx).Result(); err != nil {
		utils.AuditLog(fmt.Sprintf("Redis connection failed: %v", err))
		log.Fatal(err)
	}

	// Initialize Tracing
	tp, err := initTracer(getenvDefault("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4318"))
	if err != nil {
		utils.AuditLog(fmt.Sprintf("Tracer initialization failed: %v", err))
		log.Fatal(err)
	}
	otel.SetTracerProvider(tp)

	// Initialize Cloud Service
	providerMgr := providers.NewManager(config, redisClient)
	cloudSvc = services.NewCloudService(cloudDB, ctx, providerMgr)
}

func initTracer(endpoint string) (*trace.TracerProvider, error) {
	opts := []otlptracehttp.Option{}
	if strings.HasPrefix(endpoint, "http://") {
		endpoint = strings.TrimPrefix(endpoint, "http://")
		opts = append(opts, otlptracehttp.WithInsecure())
	} else if strings.HasPrefix(endpoint, "https://") {
		endpoint = strings.TrimPrefix(endpoint, "https://")
	} else {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	exporter, err := otlptracehttp.New(context.Background(), append(opts, otlptracehttp.WithEndpoint(endpoint))...)
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
	grpcServer := grpc.NewServer()
	cloudServer, err := gapi.NewGrpcCloudServer(*config, cloudSvc)
	if err != nil {
		utils.AuditLog(fmt.Sprintf("Failed to create gRPC server: %v", err))
		log.Fatal(err)
	}

	pb.RegisterCloudMgmtServiceServer(grpcServer, cloudServer)
	reflection.Register(grpcServer)

	listener, err := net.Listen("tcp", config.GrpcAddr)
	if err != nil {
		utils.AuditLog(fmt.Sprintf("Failed to start gRPC listener: %v", err))
		log.Fatal(err)
	}

	log.Printf("gRPC server started on %s", config.GrpcAddr)
	if err := grpcServer.Serve(listener); err != nil {
		utils.AuditLog(fmt.Sprintf("gRPC server failed: %v", err))
		log.Fatal(err)
	}
}

func getenvDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func main() {
	config, err := config.ReadConfig()
	if err != nil {
		log.Fatal(err)
	}
	startGrpcServer(config)
}
