package trigger_grpc

import (
	"context"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/models"
	em "github.com/miladhzzzz/milx-cloud-init/api-gateway/pkg/grpc-clients/events-manager"
	pb "github.com/miladhzzzz/milx-cloud-init/api-gateway/pkg/grpc-clients/events-manager/pb"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"log"
	"time"
)

// EventsManagerClient Replace with your gRPC client and events manager service
type EventsManagerClient struct{}

func (c *EventsManagerClient) SendRepoData(repoData *models.Repos) {

	con := em.InitGmClient()

	res, err := con.Clone(context.TODO(), &pb.CloneRequest{
		RepoID:      repoData.RepoID,
		GitURL:      repoData.GitURL,
		Name:        repoData.Name,
		Owner:       repoData.Owner,
		Userid:      repoData.UserID,
		Private:     repoData.Private,
		AccessToken: repoData.AccessToken,
		WebhookURL:  repoData.WebhookURL,
		EventID:     repoData.EventID,
	})

	if err != nil {
		log.Fatal(err)
	}

	log.Printf("sent: %v", res)
}

func watchRepoChanges(client *mongo.Client, eventsManagerClient *EventsManagerClient) {
	ctx := context.Background()

	// Set up change stream
	collection := client.Database("your_database_name").Collection("repos")
	matchStage := bson.D{{"$match", bson.D{{"updateDescription.updatedFields.webhook", bson.D{{"$exists", true}}}}}}
	changeStream, err := collection.Watch(ctx, mongo.Pipeline{matchStage})
	if err != nil {
		log.Fatal(err)
	}
	defer changeStream.Close(ctx)

	// Listen for changes and send gRPC calls
	for changeStream.Next(ctx) {
		var changeEvent bson.M
		if err := changeStream.Decode(&changeEvent); err != nil {
			log.Println("Error decoding change event:", err)
			continue
		}

		updatedFields := changeEvent["updateDescription"].(bson.M)["updatedFields"].(bson.M)
		webhook := updatedFields["webhook"].(string)
		// TODO : change this
		if webhook != "" {
			eventsManagerClient.SendRepoData(&models.Repos{})
		}
	}
}

func StartgRPCtrigger() {
	// Set up MongoDB connection
	client, err := mongo.NewClient(options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = client.Connect(ctx)
	if err != nil {
		log.Fatal(err)
	}

	// Set up gRPC client
	eventsManagerClient := &EventsManagerClient{}

	// Watch for repo changes
	watchRepoChanges(client, eventsManagerClient)
}
