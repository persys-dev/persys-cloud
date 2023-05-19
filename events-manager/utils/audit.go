package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

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
