package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/persys-dev/persys-cloud/persys-forgery/internal/build"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/certmanager"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/db"
	forgeryv1 "github.com/persys-dev/persys-cloud/persys-forgery/internal/forgeryv1"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/grpcapi"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/operator"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/queue"
	"github.com/persys-dev/persys-cloud/persys-forgery/utils"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := utils.LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	if err := db.InitMySQL(cfg.MySQLDSN); err != nil {
		log.Fatalf("failed to connect to MySQL: %v", err)
	}

	workspaceDir := cfg.Build.Workspace
	orchestrator := build.NewOrchestrator(workspaceDir)
	orchestrator.SetOperatorClient(&operator.Client{})
	orchestrator.SetDockerClient(&build.Docker{})

	go queue.StartRedisWorker(cfg, orchestrator)
	go queue.StartWebhookWorker(cfg)

	certMgr, err := certmanager.NewFromConfig(cfg, logrus.New())
	if err != nil {
		log.Fatalf("failed to initialize cert manager: %v", err)
	}
	if err := certMgr.Start(ctx); err != nil {
		log.Fatalf("failed to start cert manager: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{Addr: cfg.Redis.Addr, Password: cfg.Redis.Password, DB: cfg.Redis.DB})
	grpcSvc := grpcapi.NewService(cfg, db.DB, rdb, cfg.Redis.WebhookQueueKey, cfg.Redis.BuildQueueKey)

	lis, err := net.Listen("tcp", cfg.GRPC.Addr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", cfg.GRPC.Addr, err)
	}

	grpcOpts := []grpc.ServerOption{}
	if cfg.TLS.Enabled {
		tlsCfg, err := loadServerTLSConfig(cfg)
		if err != nil {
			log.Fatalf("failed to load TLS config: %v", err)
		}
		grpcOpts = append(grpcOpts, grpc.Creds(credentials.NewTLS(tlsCfg)))
	}

	grpcServer := grpc.NewServer(grpcOpts...)
	forgeryv1.RegisterForgeryControlServer(grpcServer, grpcSvc)
	reflection.Register(grpcServer)

	go func() {
		log.Printf("starting forgery gRPC server on %s", cfg.GRPC.Addr)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("forgery gRPC server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down forgery")

	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer shutdownCancel()
	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-shutdownCtx.Done():
		grpcServer.Stop()
	}
}

func loadServerTLSConfig(cfg *utils.Config) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.TLS.CertPath, cfg.TLS.KeyPath)
	if err != nil {
		return nil, err
	}
	caBytes, err := os.ReadFile(cfg.TLS.CAPath)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, errors.New("failed to append CA certificate")
	}
	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
	}
	if cfg.TLS.RequireClientCert {
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return tlsCfg, nil
}
