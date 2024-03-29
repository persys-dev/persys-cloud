package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"github.com/persys-dev/persys-cloud/events-manager/config"
	"github.com/persys-dev/persys-cloud/events-manager/internal/eventctl"
	"github.com/persys-dev/persys-cloud/events-manager/models"
	"github.com/persys-dev/persys-cloud/events-manager/pkg/etcd"
	"github.com/persys-dev/persys-cloud/events-manager/pkg/opentelemtry"
	"github.com/persys-dev/persys-cloud/events-manager/pkg/watermill"
	pb "github.com/persys-dev/persys-cloud/events-manager/proto"
	"github.com/persys-dev/persys-cloud/events-manager/utils"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"log"
	"net"
	"time"
)

type server struct {
	pb.EventServiceServer
}

var (
	cnf, _             = config.ReadConfig()
	serviceName        = "events-manager"
	logUrl             = "http://localhost:8080"
	eventsCollection   *mongo.Collection
	messagesCollection *mongo.Collection
)

func init() {

	// initialize ETCD Driver
	etcd, err := etcd.NewEtcdDriver(cnf.EtcdAddr, time.Duration(10*10000))

	if err != nil {
		log.Fatalf("can not connect to ETCD")
	}

	ctx := context.TODO()

	// TESTING ETCD
	err = etcd.Put(ctx, "Milad", "hey")

	if err != nil {
		log.Fatalf(err.Error())
	}

	// Connect to MongoDB
	mongoconn := options.Client().ApplyURI(cnf.MongoURI)
	mongoclient, err := mongo.Connect(ctx, mongoconn)

	if err != nil {
		// Send logs to audit service
		utils.SendLogMessage(logUrl, utils.LogMessage{
			Microservice: serviceName,
			Level:        "DEBUG",
			Message:      err.Error(),
			Timestamp:    time.Time{},
		})

		panic(err)
	}

	// initialize mongodb collections
	eventsCollection = mongoclient.Database("events-manager").Collection("events")
	messagesCollection = mongoclient.Database("events-manager").Collection("messages")

	// this sends logs to audit-service first test
	_ = utils.SendLogMessage(logUrl, utils.LogMessage{
		Microservice: "events-manager",
		Level:        "info",
		Message:      "initialized",
		Timestamp:    time.Time{},
	})

}

// grpcServer is starting grpc server to listen on any address
func grpcServer() {
	cnf, err := config.ReadConfig()
	lis, err := net.Listen("tcp", cnf.GrpcAddr)
	if err != nil {
		utils.SendLogMessage(logUrl, utils.LogMessage{
			Microservice: serviceName,
			Level:        "info",
			Message:      err.Error(),
			Timestamp:    time.Time{},
		})
		log.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer()
	log.Printf("gRPC server listening on: %s", cnf.GrpcAddr)
	pb.RegisterEventServiceServer(s, &server{})
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

// helper Functions

// PublishEvent is the method called when we receive a grpc the processing is done here
func (s *server) PublishEvent(ctx context.Context, grpcMsg *pb.EventMessage) (*emptypb.Empty, error) {

	// Convert the gRPC message to a byte slice.
	eventData, err := proto.Marshal(grpcMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal gRPC message: %v", err)
	}

	// Get the target service to send the event to based on the message metadata.
	origin := grpcMsg.OriginService
	destination := grpcMsg.Origin

	// Extract the additional fields from the gRPC message.
	username := grpcMsg.Username
	githubRepoURL := grpcMsg.GithubRepoUrl
	githubAccessToken := grpcMsg.GithubAccessToken
	userID := grpcMsg.UserId

	// clone user repo using blob-service driver
	cloneData, _ := utils.CloneRepo(grpcMsg.GithubRepoUrl, "", grpcMsg.GithubAccessToken)

	log.Printf("commit data: %v", cloneData)

	pays := json.RawMessage(eventData)

	// Create a new Event instance and populate it with the extracted fields.
	e := &models.Event{
		ID:                primitive.ObjectID{},
		Origin:            origin,
		Destination:       destination,
		EventType:         grpcMsg.EventType,
		Payload:           &pays,
		CreatedAt:         time.Now(),
		Username:          username,
		GithubRepoURL:     githubRepoURL,
		GithubAccessToken: githubAccessToken,
		UserID:            userID,
	}

	// Insert the event into MongoDB.
	result, err := eventsCollection.InsertOne(ctx, e)
	if err != nil {
		return nil, fmt.Errorf("failed to insert event to database: %v", err)
	}
	log.Printf("Inserted event with ID %v", result)

	// Publish the event to the appropriate topic.
	msg := &message.Message{
		UUID:     uuid.NewString(),
		Metadata: message.Metadata{},
		Payload:  eventData,
	}

	// Insert the kafka message into MongoDB.
	result, err = messagesCollection.InsertOne(ctx, msg)
	if err != nil {
		return nil, fmt.Errorf("failed to insert message to database: %v", err)
	}
	log.Printf("Inserted message with UUID %s", msg.UUID)

	// Publish the message to the appropriate topic.
	watermill.KafkaProduce(e, destination)
	log.Printf("Published message to topic %v", destination)

	return &emptypb.Empty{}, nil
}

func main() {
	/*
		OPEN TRACER And Error handling !!!!! << important
	*/
	cleanup := opentelemtry.InitTracer()
	defer cleanup(context.Background())
	/*
		GRPC STUFF
	*/
	go grpcServer()

	/*
	   KAFKA EVENT CONTROLLER
	*/
	go eventctl.KafkaEventProcessor(eventsCollection)

	<-context.Background().Done()
}
