package gapi

import (
	"context"
	pb "github.com/persys-dev/persys-devops/cloud-mgmt/proto"
)

func (cloud *CloudServer) kubeConfig(ctx context.Context, req *pb.ServicesRequest) (*pb.ServicesResponse, error) {
	// Use services
	//cloud.cloudService

	return nil, nil
}
