package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

var url = "http://localhost:8080"

type LogMessage struct {
	Microservice string    `json:"microservice"`
	Level        string    `json:"level"`
	Message      string    `json:"message"`
	Timestamp    time.Time `json:"timestamp"`
}

func SendLogMessage(url string, message LogMessage) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal log message: %v", err)
	}

	resp, err := http.Post(url+"/log", "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("failed to send log message: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to send log message, status code: %d", resp.StatusCode)
	}

	return nil
}

func LogError(message string) {
	data := LogMessage{
		Microservice: "api-gateway",
		Level:        "ERROR",
		Message:      message,
		Timestamp:    time.Now(),
	}

	payload, err := json.Marshal(data)

	if err != nil {
		log.Printf("error: %v", err)
	}

	resp, err := http.Post(url+"/log", "application/json", bytes.NewBuffer(payload))

	if err != nil {
		log.Printf("send log error: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Errorf("failed to send log message, status code: %d", resp.StatusCode)
		return
	}

}
