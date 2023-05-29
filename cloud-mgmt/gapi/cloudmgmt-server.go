package gapi

import (
	"github.com/persys-dev/persys-devops/cloud-mgmt/config"
	pb "github.com/persys-dev/persys-devops/cloud-mgmt/proto"
	"github.com/persys-dev/persys-devops/cloud-mgmt/services"
)

type CloudServer struct {
	pb.CloudMgmtServiceServer
	config       config.Config
	cloudService services.CloudService
}

func NewGrpcCloudServer(config config.Config, cloudService services.CloudService) (*CloudServer, error) {

	cloudServer := &CloudServer{
		config:       config,
		cloudService: cloudService,
	}

	return cloudServer, nil
}
