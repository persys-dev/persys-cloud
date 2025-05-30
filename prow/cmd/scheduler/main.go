package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/persys-dev/prow/internal/api"
	"github.com/persys-dev/prow/internal/scheduler"
)

func main() {
	// Initialize scheduler
	sched, err := scheduler.NewScheduler()
	if err != nil {
		log.Fatalf("Failed to initialize scheduler: %v", err)
	}
	defer sched.Close()

	// Set up context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start node monitoring
	go sched.MonitorNodes(ctx)

	// Start Workload Monitoring
	workloadMonitor := scheduler.NewMonitor(sched)

	go workloadMonitor.StartMonitoring()

	// Set up Gin router
	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())
	// r.Use(api.AuthMiddleware())

	// Register API handlers
	api.SetupHandlers(r, sched)

	// Start server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8084"
	}

	server := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Starting scheduler on port %s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	// Perform graceful shutdown
	log.Println("Shutting down server...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	cancel() // Stop MonitorNodes
	log.Println("Server stopped")
}