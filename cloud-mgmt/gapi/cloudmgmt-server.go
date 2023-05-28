package gapi

//import (
//	//"github.com/miladhzzzz/milx-cloud-init/api-gateway/services"
//	"github.com/miladhzzzz/milx-cloud-init/cloud-mgmt/config"
//	pb "github.com/miladhzzzz/milx-cloud-init/cloud-mgmt/proto"
//	//"github.com/miladhzzzz/milx-cloud-init/cloud-mgmt/services"
//	"go.mongodb.org/mongo-driver/mongo"
//)
//
//type CloudServer struct {
//	pb.CloudMgmtServiceServer
//	config         config
//	authService    services
//	userCollection *mongo.Collection
//}
//
//func NewGrpcCloudServer(config config.Config, authService services.AuthService, userCollection *mongo.Collection) (*CloudServer, error) {
//
//	cloudServer := &CloudServer{
//		config:         config,
//		authService:    authService,
//		userCollection: userCollection,
//	}
//
//	return cloudServer, nil
//}
