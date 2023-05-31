package trigger_grpc

import (
	"context"
	"github.com/persys-dev/persys-cloud/api-gateway/config"
	"github.com/persys-dev/persys-cloud/api-gateway/models"
	em "github.com/persys-dev/persys-cloud/api-gateway/pkg/grpc-clients/events-manager"
	pb "github.com/persys-dev/persys-cloud/api-gateway/pkg/grpc-clients/events-manager/pb"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"log"
	"time"
)

var (
	cnf, _ = config.ReadConfig()
)

// EventsManagerClient Replace with your gRPC client and events manager service
type EventsManagerClient struct{}

func (c *EventsManagerClient) SendRepoData(repoData *models.Repos) {

	con := em.InitEventClient()

	res, err := con.PublishEvent(context.TODO(), &pb.EventMessage{
		Id:                "1",
		ServiceName:       "api-gateway",
		OriginService:     "api-gateway",
		EventType:         "pipeline_add",
		Payload:           []byte("nil"),
		Origin:            "ci-service",
		Username:          repoData.Name,
		GithubRepoUrl:     repoData.GitURL,
		GithubAccessToken: repoData.AccessToken,
		UserId:            string(repoData.UserID),
	})

	if err != nil {
		log.Printf("events-manager: %v ", err)
		return
	}

	log.Printf("sent: %v", res.String())
}

func watchRepoChanges(client *mongo.Client, eventsManagerClient *EventsManagerClient) {
	ctx := context.Background()

	// Set up change stream
	collection := client.Database("api-gateway").Collection("repos")
	matchStage := bson.D{{"$match", bson.D{{"updateDescription.updatedFields.webhookURL", bson.D{{"$exists", true}}}}}}
	changeStream, err := collection.Watch(ctx, mongo.Pipeline{matchStage}, options.ChangeStream().SetFullDocument(options.UpdateLookup))
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

		doc := changeEvent["fullDocument"].(bson.M)
		//fmt.Print(doc)
		name := doc["name"]
		githubRepo := doc["gitURL"]
		githubToken := doc["accessToken"]

		updatedFields := changeEvent["updateDescription"].(bson.M)["updatedFields"].(bson.M)
		webhook := updatedFields["webhookURL"].(string)

		if webhook != "" {
			log.Println("sent event to events-manager using grpc")
			eventsManagerClient.SendRepoData(&models.Repos{
				RepoID:      0,
				GitURL:      githubRepo.(string),
				Name:        name.(string),
				Owner:       "",
				UserID:      0,
				Private:     false,
				AccessToken: githubToken.(string),
				WebhookURL:  "",
				EventID:     0,
				CreatedAt:   "",
			})
		}
	}
}

func StartgRPCtrigger() {
	// Set up MongoDB connection
	client, err := mongo.NewClient(options.Client().ApplyURI(cnf.MongoURI))
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
