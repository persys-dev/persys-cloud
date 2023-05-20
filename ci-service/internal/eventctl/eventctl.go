package eventctl

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/message/router/middleware"
	"github.com/ThreeDotsLabs/watermill/message/router/plugin"
	"github.com/miladhzzzz/milx-cloud-init/ci-service/models"
	"github.com/miladhzzzz/milx-cloud-init/ci-service/pkg/docker"
	"github.com/miladhzzzz/milx-cloud-init/ci-service/pkg/manifest"
	"github.com/miladhzzzz/milx-cloud-init/ci-service/pkg/watermill"
	"github.com/miladhzzzz/milx-cloud-init/ci-service/utils"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"path/filepath"
)

var (
	serviceName  = "ci-service"
	consumeTopic = "ci-service"
	publisher    = watermill.CreatePublisher()
	subscriber   = watermill.CreateSubscriber("ci_handler")
	publishTopic = "ci-service-reports"
)

func KafkaEventProcessor() {

	consumedPayload := models.Event{}

	router, err := message.NewRouter(message.RouterConfig{}, watermill.Logger)
	if err != nil {
		panic(err)
	}

	router.AddPlugin(plugin.SignalsHandler)
	router.AddMiddleware(middleware.Recoverer)

	router.AddHandler(
		"ci_handler",
		consumeTopic,
		subscriber,
		publishTopic,
		publisher,
		func(msg *message.Message) ([]*message.Message, error) {
			erp := json.Unmarshal(msg.Payload, &consumedPayload)
			if erp != nil {
				// When a handler returns an error, the default behavior is to send a Nack (negative-acknowledgement).
				// The message will be processed again.
				//
				// You can change the default behaviour by using middlewares, like Retry or PoisonQueue.
				// You can also implement your own middleware.
				return nil, err
			}
			fmt.Printf("consumed message: %v", consumedPayload)

			filePath, err := utils.DownloadRepo("", "")

			if err != nil {

			}

			files, err := manifest.ScanDocker(filePath)

			if err != nil {
				return nil, err
			}

			for _, file := range files {
				// IMPORTANT THIS ONLY WORKS FOR WINDOWS ENVIRONMENTS !!!! THIS IS FOR DEVELOPMENT ONLY !!!
				path := filepath.Dir(file)
				//fmt.Println(path)

				// TODO : we need to tag and push image
				err = docker.ImageBuild(path, "persys:latest")

				newPayload, err := json.Marshal(models.Report{
					ServiceName: serviceName,
					JobID:       primitive.ObjectID{},
					JobAction:   "Docker-Build",
					NextAction:  "CD",
					Output:      json.RawMessage(err.Error()),
					Status:      "Success",
					FailCount:   0,
				})

				// TODO: generate k8s yaml files and upload to blob-service

				if err != nil {
					return nil, err
				}

				newMessage := message.NewMessage(watermill.NewULID(), newPayload)
				return []*message.Message{newMessage}, nil
			}
			return nil, nil
		})

	//router.AddHandler()

	if err := router.Run(context.Background()); err != nil {
		panic(err)
	}

}
