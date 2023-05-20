package eventctl

import (
	"context"
	"encoding/json"
	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/message/router/middleware"
	"github.com/ThreeDotsLabs/watermill/message/router/plugin"
	"github.com/miladhzzzz/milx-cloud-init/events-manager/models"
	wmHelper "github.com/miladhzzzz/milx-cloud-init/events-manager/pkg/watermill"
	"go.mongodb.org/mongo-driver/mongo"
	"log"
	"time"
)

var (
	consumeTopicCd = "cd-service-reports"
	publishTopicCd = "jobs_reports"
	consumeTopicCi = "ci-service-reports"
	publisher      = wmHelper.CreatePublisher()
	subscriber     = wmHelper.CreateSubscriber("events-manager-handler")
	publishTopicCi = "jobs_reports"
)

func KafkaEventProcessor(collection *mongo.Collection) {
	//defer cancelContext()
	router, err := message.NewRouter(message.RouterConfig{}, wmHelper.Logger)
	if err != nil {
		panic(err)
	}

	// CI-service event handler
	router.AddPlugin(plugin.SignalsHandler)
	router.AddMiddleware(middleware.Recoverer)
	router.AddHandler(
		"ci_handler",
		consumeTopicCi,
		subscriber,
		publishTopicCi,
		publisher,
		func(msg *message.Message) ([]*message.Message, error) {
			consumedPayload := models.Event{}
			err := json.Unmarshal(msg.Payload, &consumedPayload)
			if err != nil {
				return nil, err
			}
			log.Printf("received event %+v", consumedPayload)
			newPayload, err := json.Marshal(models.ProcessedEvent{
				ProcessedID: consumedPayload.ID,
				Time:        time.Now(),
			})
			if err != nil {
				return nil, err
			}
			newMessage := message.NewMessage(watermill.NewUUID(), newPayload)
			return []*message.Message{newMessage}, nil
		})

	// CD-service event handler
	router.AddHandler("cd_handler",
		consumeTopicCd,
		subscriber,
		publishTopicCd,
		publisher,
		func(msg *message.Message) ([]*message.Message, error) {
			consumedPayload := models.Event{}
			err := json.Unmarshal(msg.Payload, &consumedPayload)
			if err != nil {
				return nil, err
			}
			log.Printf("received event %+v", consumedPayload)
			newPayload, err := json.Marshal(models.ProcessedEvent{
				ProcessedID: consumedPayload.ID,
				Time:        time.Now(),
			})
			if err != nil {
				return nil, err
			}
			newMessage := message.NewMessage(watermill.NewUUID(), newPayload)
			return []*message.Message{newMessage}, nil
		})

	// Failed Job scheduler adds failed jobs to retry_queue topic to be picked up by services their self

	router.AddHandler("failed_handler",
		"jobs_reports",
		subscriber,
		"retry_queue",
		publisher,
		func(msg *message.Message) ([]*message.Message, error) {
			consumedPayload := models.Report{}
			err := json.Unmarshal(msg.Payload, &consumedPayload)
			if err != nil {
				return nil, err
			}
			log.Printf("received event %+v", consumedPayload)

			if consumedPayload.Status == "Failed" {

				// TODO : add it to mongodb and notify the correct user
				newPayload, err := json.Marshal(models.ProcessedEvent{
					ProcessedID: consumedPayload.JobID,
					Time:        time.Now(),
				})
				if err != nil {
					return nil, err
				}
				newMessage := message.NewMessage(watermill.NewUUID(), newPayload)
				return []*message.Message{newMessage}, nil
			}

			return nil, nil
		})

	// Job Scheduler determines if a job is ready for next steps or not

	if err := router.Run(context.Background()); err != nil {
		panic(err)
	}
}
