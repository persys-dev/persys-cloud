package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/google/uuid"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/gin-gonic/gin"
)

type LogMessage struct {
	Microservice string    `json:"microservice"`
	Level        string    `json:"level"`
	Message      string    `json:"message"`
	Timestamp    time.Time `json:"timestamp"`
}

func sendToElasticsearch(client *elasticsearch.Client, index string, message LogMessage) error {
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}

	req := esapi.IndexRequest{
		Index:      index,
		DocumentID: uuid.New().String(),
		Body:       bytes.NewReader(body),
		Refresh:    "true",
	}

	res, err := req.Do(context.Background(), client)
	if err != nil {
		return err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(res.Body)

	if res.IsError() {
		return fmt.Errorf("failed to index document (status code: %d)", res.StatusCode)
	}

	return nil
}

func main() {
	// Initialize Elasticsearch client
	cfg := elasticsearch.Config{
		Addresses: []string{"http://localhost:9200"},
	}
	client, err := elasticsearch.NewClient(cfg)
	if err != nil {
		log.Fatalf("Error creating Elasticsearch client: %s", err)
	}

	// Create Gin router
	router := gin.Default()

	// Define routes
	router.POST("/log", func(c *gin.Context) {
		// Parse log message from request body
		var message LogMessage
		err := c.BindJSON(&message)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Send log message to Elasticsearch
		err = sendToElasticsearch(client, "audit-service", message)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Return success response
		c.JSON(http.StatusOK, gin.H{"message": "Log message sent to Elasticsearch"})
	})

	// Start HTTP server
	err = router.Run(":8080")
	if err != nil {
		log.Fatalf("Error starting HTTP server: %s", err)
	}
}
