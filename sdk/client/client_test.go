package client

import (
	"testing"

	"github.com/persys-dev/persys-cloud/sdk/options"
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

func TestLoadTLSConfigInsecure(t *testing.T) {
	cfg, err := LoadTLSConfig(&options.Options{Insecure: true})
	if err != nil {
		t.Fatalf("LoadTLSConfig() error = %v", err)
	}
	if cfg == nil || !cfg.InsecureSkipVerify {
		t.Fatal("expected insecure TLS config")
	}
}
