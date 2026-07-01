// Package server exposes vault-manager's gRPC API, letting other Persys
// Cloud services fetch or rotate their AppRole credentials at runtime.
package server

import (
	"context"
	"net"
	"time"

	vault "github.com/hashicorp/vault/api"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/persys-dev/persys-cloud/vault-manager/internal/approle"
	pb "github.com/persys-dev/persys-cloud/vault-manager/internal/vaultmanagerv1"
)

// credentialTTL is the lifetime advertised to callers for issued credentials.
const credentialTTL = 720 * time.Hour

type vaultManagerServer struct {
	vaultClient *vault.Client
	logger      *logrus.Logger
	pb.UnimplementedVaultManagerServiceServer
}

func (s *vaultManagerServer) GetServiceCredentials(ctx context.Context, req *pb.GetServiceCredentialsRequest) (*pb.ServiceCredentialsResponse, error) {
	if req.ServiceName == "" {
		return nil, status.Error(codes.InvalidArgument, "service_name required")
	}
	roleID, secretID, err := approle.FetchRoleAndSecret(s.vaultClient, req.ServiceName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &pb.ServiceCredentialsResponse{
		RoleId:    roleID,
		SecretId:  secretID,
		ExpiresAt: time.Now().Add(credentialTTL).Unix(),
	}, nil
}

func (s *vaultManagerServer) RotateServiceSecretID(ctx context.Context, req *pb.RotateServiceSecretIDRequest) (*pb.ServiceCredentialsResponse, error) {
	if req.ServiceName == "" {
		return nil, status.Error(codes.InvalidArgument, "service_name required")
	}
	roleID, secretID, err := approle.FetchRoleAndSecret(s.vaultClient, req.ServiceName) // generates new SecretID
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &pb.ServiceCredentialsResponse{
		RoleId:    roleID,
		SecretId:  secretID,
		ExpiresAt: time.Now().Add(credentialTTL).Unix(),
	}, nil
}

func loggingInterceptor(logger *logrus.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		start := time.Now()

		logger.WithFields(logrus.Fields{
			"method":  info.FullMethod,
			"request": req,
		}).Info("grpc request started")

		resp, err := handler(ctx, req)

		duration := time.Since(start)
		code := status.Code(err)

		if err != nil {
			logger.WithFields(logrus.Fields{
				"method":   info.FullMethod,
				"code":     code.String(),
				"duration": duration,
				"error":    err,
			}).Error("grpc request failed")
		} else {
			logger.WithFields(logrus.Fields{
				"method":   info.FullMethod,
				"code":     code.String(),
				"duration": duration,
			}).Info("grpc request completed")
		}

		return resp, err
	}
}

// Start blocks, serving the VaultManager gRPC API on addr until the
// listener fails.
func Start(client *vault.Client, addr string, logger *logrus.Logger) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(loggingInterceptor(logger)),
	)

	pb.RegisterVaultManagerServiceServer(
		grpcServer,
		&vaultManagerServer{
			vaultClient: client,
			logger:      logger,
		},
	)

	logger.WithField("addr", addr).Info("VaultManager gRPC listening")

	return grpcServer.Serve(lis)
}
