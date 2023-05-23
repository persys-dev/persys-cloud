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
	"os"
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

func sendToElasticsearch(client *elasticsearch.Client, index string, file *os.File) error {
	body, err := json.Marshal(file)
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

func elasticsearchAvailable() bool {
	// Check if Elasticsearch is reachable by pinging it
	resp, err := http.Get("http://localhost:9200")
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	// Return true if response status is 200 OK, else false
	return resp.StatusCode == http.StatusOK
}

func main() {

	logFile, err := os.Create("services.log")
	if err != nil {
		log.Fatalf("failed to create log file: %v", err)
	}
	defer logFile.Close()

	// Use the log file for all logging
	gin.DefaultWriter = logFile
	gin.DefaultErrorWriter = logFile

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
		// dump the log message to file
		log.Printf("services-log: %v", message)

		// if elasticsearch isn't available just return a 200 we dumped the message to file
		if elasticsearchAvailable() != true {
			c.JSON(http.StatusOK, gin.H{"message": "dumped to local file for now"})
			return
		}

		// Send log file to Elasticsearch
		err = sendToElasticsearch(client, "audit-service", logFile)
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
