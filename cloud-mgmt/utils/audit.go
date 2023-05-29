package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

var auditURL = "http://localhost:8080"

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

func AuditLog(message string) {

	payload := &LogMessage{
		Microservice: "cloud-mgmt",
		Level:        "Debug",
		Message:      message,
		Timestamp:    time.Time{},
	}

	data, err := json.Marshal(payload)

	if err != nil {
		log.Fatalf("error marshaling json: %v", err)
	}

	resp, err := http.Post(auditURL+"/log", "application/json", bytes.NewBuffer(data))

	if err != nil {
		fmt.Printf("error sending log: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("error sending log: %v", err)
	}

}
