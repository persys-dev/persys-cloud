package main

import (
	"context"
	"fmt"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"github.com/miladhzzzz/milx-cloud-init/events-manager/config"
	"github.com/miladhzzzz/milx-cloud-init/events-manager/internal/eventctl"
	"github.com/miladhzzzz/milx-cloud-init/events-manager/models"
	"github.com/miladhzzzz/milx-cloud-init/events-manager/pkg/opentelemtry"
	"github.com/miladhzzzz/milx-cloud-init/events-manager/pkg/watermill"
	pb "github.com/miladhzzzz/milx-cloud-init/events-manager/proto"
	"github.com/miladhzzzz/milx-cloud-init/events-manager/utils"
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

	ctx := context.TODO()
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
	eventsCollection = mongoclient.Database("").Collection("")
	messagesCollection = mongoclient.Database("").Collection("")
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
	destination := grpcMsg.ServiceName

	// Extract the additional fields from the gRPC message.
	username := grpcMsg.Username
	githubRepoURL := grpcMsg.GithubRepoUrl
	githubAccessToken := grpcMsg.GithubAccessToken
	userID := grpcMsg.UserId

	// clone user repo using blob-service driver
	utils.CloneRepo(grpcMsg.GithubRepoUrl, "", grpcMsg.GithubAccessToken)

	// Create a new Event instance and populate it with the extracted fields.
	e := &models.Event{
		Origin:            origin,
		Destination:       destination,
		EventType:         grpcMsg.EventType,
		Payload:           eventData,
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
	watermill.KafkaProduce(eventData, destination)
	log.Printf("Published message to topic %v", destination)

	return nil, nil
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
