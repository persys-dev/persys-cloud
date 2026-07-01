package client_test

// import (
// 	"testing"

// 	"github.com/persys-dev/persys-cloud/sdk/client"
// 	"github.com/persys-dev/persys-cloud/sdk/options"
// )

// func TestNew_NilOptions_UsesDefaults(t *testing.T) {
// 	// With insecure mode the identity provider is a no-op, allowing the
// 	// test to run without a live Vault instance.
// 	opts := options.WithInsecure(options.DefaultOptions())
// 	c, err := client.New(opts)
// 	if err != nil {
// 		t.Fatalf("New() with insecure defaults: %v", err)
// 	}
// 	defer c.Close()
// }

// func TestNew_HTTPTransport(t *testing.T) {
// 	opts := options.WithInsecure(options.DefaultOptions())
// 	opts.Transport = options.TransportHTTP
// 	opts.APIEndpoint = "https://localhost:8443"

// 	c, err := client.New(opts)
// 	if err != nil {
// 		t.Fatalf("New() HTTP transport: %v", err)
// 	}
// 	defer c.Close()

// 	if c.Transport() != options.TransportHTTP {
// 		t.Errorf("expected transport %q, got %q", options.TransportHTTP, c.Transport())
// 	}
// }

// func TestNew_UnsupportedTransport_ReturnsError(t *testing.T) {
// 	opts := options.WithInsecure(options.DefaultOptions())
// 	opts.Transport = "websocket"

// 	_, err := client.New(opts)
// 	if err == nil {
// 		t.Fatal("expected error for unsupported transport, got nil")
// 	}
// }
