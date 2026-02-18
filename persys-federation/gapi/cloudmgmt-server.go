package gapi

import (
	"github.com/persys-dev/persys-cloud/persys-federation/config"
	pb "github.com/persys-dev/persys-cloud/persys-federation/proto"
	"github.com/persys-dev/persys-cloud/persys-federation/services"
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
