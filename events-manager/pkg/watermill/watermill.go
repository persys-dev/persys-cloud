package watermill

import (
	"context"
	"encoding/json"
	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill-kafka/v2/pkg/kafka"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/message/router/middleware"
	"github.com/ThreeDotsLabs/watermill/message/router/plugin"
	"github.com/persys-dev/persys-cloud/events-manager/config"
	"github.com/persys-dev/persys-cloud/events-manager/models"
	"log"
	"time"
)

var (
	cnf, _       = config.ReadConfig()
	brokers      = []string{cnf.KafkaBroker}
	consumeTopic = "events"
	publishTopic = "events-processed"
	Logger       = watermill.NewStdLogger(
		true,
		false,
	)
	marshaler     = kafka.DefaultMarshaler{}
	publisher     = CreatePublisher()
	subscriber    = CreateSubscriber("handler_1")
	contextCancel context.CancelFunc
)

type processedEvent struct {
	ProcessedID string    `json:"processed_id"`
	Time        time.Time `json:"time"`
}

func Test() {
	defer cancelContext()
	router, err := message.NewRouter(message.RouterConfig{}, Logger)
	if err != nil {
		panic(err)
	}
	router.AddPlugin(plugin.SignalsHandler)
	router.AddMiddleware(middleware.Recoverer)
	router.AddHandler(
		"handler_1",
		consumeTopic,
		subscriber,
		publishTopic,
		publisher,
		func(msg *message.Message) ([]*message.Message, error) {
			consumedPayload := models.Event{}
			err := json.Unmarshal(msg.Payload, &consumedPayload)
			if err != nil {
				return nil, err
			}
			log.Printf("received event %+v", consumedPayload)
			newPayload, err := json.Marshal(processedEvent{
				ProcessedID: consumedPayload.ID.String(),
				Time:        time.Now(),
			})
			if err != nil {
				return nil, err
			}
			newMessage := message.NewMessage(watermill.NewUUID(), newPayload)
			return []*message.Message{newMessage}, nil
		},
	)
	go simulateEvents()
	if err := router.Run(context.Background()); err != nil {
		panic(err)
	}
}

func CreatePublisher() message.Publisher {
	kafkaPublisher, err := kafka.NewPublisher(
		kafka.PublisherConfig{
			Brokers:   brokers,
			Marshaler: marshaler,
		},
		Logger,
	)
	if err != nil {
		panic(err)
	}
	return kafkaPublisher
}

func KafkaProduce(payload *models.Event, destination string) {
	pay, erc := json.Marshal(payload)

	if erc != nil {
		log.Println(erc)
		return
	}
	err := publisher.Publish(destination, message.NewMessage(
		watermill.NewUUID(),
		pay,
	))
	if err != nil {
		panic(err)
	}
}

func CreateSubscriber(consumerGroup string) message.Subscriber {
	kafkaSubscriber, err := kafka.NewSubscriber(
		kafka.SubscriberConfig{
			Brokers:       brokers,
			Unmarshaler:   marshaler,
			ConsumerGroup: consumerGroup,
		},
		Logger,
	)
	if err != nil {
		panic(err)
	}
	return kafkaSubscriber
}

func simulateEvents() {
	i := 0
	for {
		select {
		case <-context.Background().Done():
			return
		default:
			e := models.Event{
				Destination: "",
			}
			payload, err := json.Marshal(e)
			if err != nil {
				panic(err)
			}
			err = publisher.Publish(consumeTopic, message.NewMessage(
				watermill.NewUUID(),
				payload,
			))
			if err != nil {
				panic(err)
			}
			i++
			time.Sleep(time.Second)
		}
	}
}

func cancelContext() {
	contextCancel()
}

func init() {
	rootCtx, contextCancel := context.WithCancel(context.Background())
	contextCancel = contextCancel // to avoid unused declaration warning
	rootCtx = rootCtx
	// Use rootCtx in your code as needed
	rootCtx, contextCancel = context.WithCancel(context.Background())
}
