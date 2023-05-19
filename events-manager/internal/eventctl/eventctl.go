package eventctl

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/segmentio/kafka-go"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"
)

// Report represents the message format in the <servicename>-reports topic
type Report struct {
	ServiceName string `json:"service_name"`
	Status      string `json:"status"`
	FailCount   int    `json:"fail_count"`
}

// EventProcessor struct holds the attributes required for processing events
type EventProcessor struct {
	Logger      *log.Logger
	Consumer    *kafka.Reader
	Producer    *kafka.Writer
	Topic       string
	NotifyTopic string
}

// NewEventProcessor creates a new EventProcessor instance
func NewEventProcessor(logger *log.Logger, reader *kafka.Reader, writer *kafka.Writer, topic, notifyTopic string) *EventProcessor {
	return &EventProcessor{
		Logger:      logger,
		Consumer:    reader,
		Producer:    writer,
		Topic:       topic,
		NotifyTopic: notifyTopic,
	}
}

// Process reads messages from the topic and sends notifications as needed
func (ep *EventProcessor) Process(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			ep.Logger.Println("Event processor stopped")
			return
		default:
			msg, err := ep.Consumer.ReadMessage(ctx)
			if err != nil {
				ep.Logger.Printf("Error reading message: %v\n", err)
				continue
			}
			var report Report
			err = json.Unmarshal(msg.Value, &report)
			if err != nil {
				ep.Logger.Printf("Error unmarshaling message: %v\n", err)
				continue
			}
			if report.Status == "failed" && report.FailCount >= 3 {
				notificationMessage := []byte("Notification message goes here")
				err = ep.Producer.WriteMessages(ctx, kafka.Message{
					Topic: ep.NotifyTopic,
					Value: notificationMessage,
				})
				if err != nil {
					ep.Logger.Printf("Error sending notification message: %v\n", err)
					continue
				}
				ep.Logger.Println("Sent notification message")
			} else if report.Status == "success" {
				enrichedMessage := []byte("Enriched message goes here")
				err = ep.Producer.WriteMessages(ctx, kafka.Message{
					Topic: "<cd-service>",
					Value: enrichedMessage,
				})
				if err != nil {
					ep.Logger.Printf("Error sending enriched message: %v\n", err)
					continue
				}
				ep.Logger.Println("Sent enriched message")
			}
		}
	}
}

// StartEventProcessor starts the event processor in a new goroutine
func StartEventProcessor(ctx context.Context, logger *log.Logger, brokerURL, topic, notifyTopic string) error {
	logger.Println("Starting event processor...")
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:   strings.Split(brokerURL, ","),
		Topic:     topic,
		Partition: 0,
		MinBytes:  10e3,
		MaxBytes:  10e6,
	})
	writer := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  strings.Split(brokerURL, ","),
		Topic:    notifyTopic,
		Balancer: &kafka.LeastBytes{},
	})
	ep := NewEventProcessor(logger, reader, writer, topic, notifyTopic)
	go ep.Process(ctx)
	logger.Println("Event processor started")
	return nil
}

// StopEventProcessor stops the event processor gracefully
func StopEventProcessor(logger *log.Logger, writer *kafka.Writer, cancelFunc context.CancelFunc) {
	logger.Println("Shutting down event processor...")
	err := writer.Close()
	if err != nil {
		logger.Printf("Error closing Kafka writer: %v\n", err)
	}
	cancelFunc()
	logger.Println("Event processor stopped")
}

// WaitForInterrupt blocks until an interrupt signal is received, then cancels the context and returns
func WaitForInterrupt(ctx context.Context, logger *log.Logger) {
	logger.Println("Waiting for interrupt signal...")
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
	logger.Println("Interrupt signal received, stopping...")
	cancelFunc := ctx.Value("cancel").(context.CancelFunc)
	StopEventProcessor(logger, nil, cancelFunc)
}

// PublishMessage sends a JSON-encoded message to the specified Kafka topic
func PublishMessage(ctx context.Context, logger *log.Logger, brokerURL, topic string, message interface{}) error {
	messageBytes, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("error marshaling message: %v", err)
	}
	writer := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  strings.Split(brokerURL, ","),
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
	})
	err = writer.WriteMessages(ctx, kafka.Message{
		Value: messageBytes,
	})
	if err != nil {
		return fmt.Errorf("error sending message: %v", err)
	}
	logger.Println("Sent message to Kafka")
	return nil
}
