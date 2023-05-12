package main

import (
	"context"
	"github.com/miladhzzzz/milx-cloud-init/cloud-mgmt/pkg/mongodb"
	proto "github.com/miladhzzzz/milx-cloud-init/cloud-mgmt/proto"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/grpc"
	"log"
	"net"
)

type server struct {
	proto.CloudMgmtServiceServer
}

func (*server) services(ctx context.Context, req *proto.ServicesRequest) (*proto.ServicesResponse, error) {
	dbc, err := mongodbHandler.Dbc()
	q := dbc.Database("cloud-mgmt").Collection("environments")

	if err != nil {

	}
	userID := req.UserID

	find := q.FindOne(context.Background(), bson.M{"userID": userID})

	if find.Err() == mongo.ErrNoDocuments {
		// initiate creating cloud environment
	}

	var resp *bson.M
	if err := find.Decode(&resp); err != nil {
		// error handle
	}

	//fmt.Println(userID)

	response := &proto.ServicesResponse{
		UserID: req.UserID,
		Persys: "",
		Aws:    "",
		Azure:  "",
		Gcp:    "",
		State:  "",
	}

	return response, nil

}

func gRPC() {
	lis, err := net.Listen("tcp", "0.0.0.0:5008")

	if err != nil {
		log.Fatalf("failed to listed: %v", err)
	}

	s := grpc.NewServer()
	log.Printf("gRPC server listening on : %v", "5008")
	proto.RegisterCloudMgmtServiceServer(s, &server{})

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to server: %v", err)
	}
}

func main() {
	gRPC()
}
