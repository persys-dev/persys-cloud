package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync/atomic"

	"google.golang.org/grpc"
)

var (
	federationGRPCRequestsTotal atomic.Uint64
	federationGRPCErrorsTotal   atomic.Uint64
)

func federationUnaryMetricsInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		federationGRPCRequestsTotal.Add(1)
		resp, err := handler(ctx, req)
		if err != nil {
			federationGRPCErrorsTotal.Add(1)
		}
		return resp, err
	}
}

func startMetricsServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "# TYPE persys_federation_grpc_requests_total counter\n")
		fmt.Fprintf(w, "persys_federation_grpc_requests_total %d\n", federationGRPCRequestsTotal.Load())
		fmt.Fprintf(w, "# TYPE persys_federation_grpc_errors_total counter\n")
		fmt.Fprintf(w, "persys_federation_grpc_errors_total %d\n", federationGRPCErrorsTotal.Load())
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	server := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := server.ListenAndServe(); err != nil && !strings.Contains(err.Error(), "Server closed") {
			log.Printf("federation metrics server failed: %v", err)
		}
	}()
	return server
}
