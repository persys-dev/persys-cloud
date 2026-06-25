package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	controlv1 "github.com/persys-dev/persys-cloud/pkg/scheduler/controlv1"
	"github.com/persys-dev/persys-cloud/sdk/options"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestNewGRPCInsecure(t *testing.T) {
	c, err := New(&options.Options{Transport: options.TransportGRPC, GRPCEndpoint: "localhost:50051", Insecure: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if c == nil {
		t.Fatal("New() returned nil client")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestNewRejectsUnknownTransport(t *testing.T) {
	_, err := New(&options.Options{Transport: "bogus"})
	if err == nil {
		t.Fatal("New() expected error for unknown transport")
	}
}

func TestNewHTTPTransport(t *testing.T) {
	c, err := New(&options.Options{Transport: options.TransportHTTP, APIEndpoint: "http://localhost:8080"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if c == nil {
		t.Fatal("New() returned nil client")
	}
}

func TestHTTPApplyWorkloadUsesGatewayEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/workloads/schedule" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		data, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(&controlv1.ApplyWorkloadResponse{Success: true})
		if err != nil {
			t.Fatalf("marshal response: %v", err)
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()

	c, err := New(&options.Options{Transport: options.TransportHTTP, APIEndpoint: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	resp, err := c.ApplyWorkload(context.Background(), &controlv1.ApplyWorkloadRequest{WorkloadId: "workload-1"})
	if err != nil {
		t.Fatalf("ApplyWorkload() error = %v", err)
	}
	if !resp.GetSuccess() {
		t.Fatal("ApplyWorkload() returned unsuccessful response")
	}
}

func TestDefaultOptionsDoNotRequireCertificatePaths(t *testing.T) {
	opts := options.DefaultOptions()
	if opts.UseCertManager {
		t.Fatal("DefaultOptions() should not require certmanager certificate paths")
	}
	if _, err := New(opts); err != nil {
		t.Fatalf("New(DefaultOptions()) error = %v", err)
	}
}

func TestLoadTLSConfigInsecure(t *testing.T) {
	cfg, err := LoadTLSConfig(&options.Options{Insecure: true})
	if err != nil {
		t.Fatalf("LoadTLSConfig() error = %v", err)
	}
	if cfg == nil || !cfg.InsecureSkipVerify {
		t.Fatal("expected insecure TLS config")
	}
}
