package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/miladhzzzz/milx-cloud-init/events-manager/config"
	mongodbHandler "github.com/miladhzzzz/milx-cloud-init/events-manager/pkg/mongodb"
	"github.com/miladhzzzz/milx-cloud-init/events-manager/pkg/opentelemtry"
	"github.com/miladhzzzz/milx-cloud-init/events-manager/pkg/watermill"
	proto "github.com/miladhzzzz/milx-cloud-init/events-manager/proto"
	"google.golang.org/grpc"
	"log"
	"net"
	"time"
)

type server struct {
	proto.GithubServiceServer
}

type event struct {
	RepoID      int64  `json:"repoID"`
	UserID      int64  `json:"userID"`
	EventID     int64  `json:"eventID"`
	GitURL      string `json:"gitURL"`
	Commit      string `json:"commit"`
	AccessToken string `json:"accessTokne"`
	Private     bool   `json:"private"`
	State       string `json:"state"`
	CreatedAt   string `json:"createdAt"`
	Directory   string `json:"directory"`
}

// Clone gRPC request processor
func (*server) Clone(ctx context.Context, req *proto.CloneRequest) (*proto.CloneResponse, error) {
	res := &proto.CloneResponse{
		JobID:  req.EventID,
		Status: "ok",
	}

	go jobInit(req)

	return res, nil
}

// grpcServer is starting grpc server to listen on any address
func grpcServer() {
	cnf, err := config.ReadConfig()

	lis, err := net.Listen("tcp", cnf.GrpcAddr)

	if err != nil {
		fmt.Printf("failed to listed: %v", err)
	}

	s := grpc.NewServer()
	fmt.Printf("gRPC server listening on : %v", "5006")
	proto.RegisterGithubServiceServer(s, &server{})

	if err := s.Serve(lis); err != nil {
		fmt.Printf("failed to server: %v", err)
	}
}

// kafkaProducer is producing messages to "events" topic in kafka.
func kafkaProducer(event *event) {
	payload, err := json.Marshal(event)
	if err != nil {
		log.Printf("cannot marshal json err : %v", err)
		return
	}
	go watermill.KafkaProduce(payload)
	fmt.Printf("sent payload to kafka topic : events %v", event)
}

// jobInit is producing a job and sending it to the other workers in our app.
func jobInit(request *proto.CloneRequest) {
	dbc, erp := mongodbHandler.Dbc()
	if erp != nil {

	}
	q := dbc.Database("events-manager").Collection("jobs")

	//commit, directory, err := Gits(request.GitURL, request.Private, request.AccessToken)
	//if err != nil {
	//	fmt.Println(err)
	//	return
	//}
	//if commit == nil {
	//	fmt.Println("commit is nil!")
	//	return
	//}
	events := event{
		RepoID:      request.RepoID,
		UserID:      request.Userid,
		EventID:     request.EventID,
		GitURL:      request.GitURL,
		Commit:      "commit.Hash.String()",
		AccessToken: request.AccessToken,
		Private:     request.Private,
		State:       "Build",
		Directory:   "directory",
		CreatedAt:   time.Now().String(),
	}
	res, erc := q.InsertOne(context.TODO(), events)
	if erc == nil {
		fmt.Println(res.InsertedID)
	}
	kafkaProducer(&events)
}

func main() {
	/*
		OPEN TRACER And Error handling !!!!! << important
	*/

	cleanup := opentelemtry.InitTracer()

	//defer errorhandler.ErrHandler()

	defer cleanup(context.Background())

	/*
		GRPC STUFF
	*/
	grpcServer()

	//res, err := callTheye()
	//
	//CheckIfError(err)
	//fmt.Println(res)

}
