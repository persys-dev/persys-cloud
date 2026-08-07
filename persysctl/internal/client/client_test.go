package client

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/persys-dev/persysctl/internal/config"
)

func TestMeterSummaryGatewayPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/meter/v1/workloads/wl-123/summary" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("from"); got == "" {
			t.Fatal("missing from query")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"workload_id":"wl-123","sample_count":7}`))
	}))
	defer server.Close()

	cli := &Client{cfg: config.Config{Transport: "http", APIEndpoint: server.URL}, httpClient: server.Client()}
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)

	resp, err := cli.MeterSummary("wl-123", from, to)
	if err != nil {
		t.Fatalf("MeterSummary() error = %v", err)
	}
	if resp["workload_id"] != "wl-123" {
		t.Fatalf("MeterSummary() workload_id = %v", resp["workload_id"])
	}
}

func TestWatchGatewayEventsParsesSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events/watch" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: event\ndata: {\"type\":\"workload\",\"workload_id\":\"wl-9\"}\n\n"))
	}))
	defer server.Close()

	cli := &Client{cfg: config.Config{Transport: "http", APIEndpoint: server.URL}, httpClient: server.Client()}
	events, err := cli.WatchGatewayEvents("workload", "wl-9", "", 10)
	if err != nil {
		t.Fatalf("WatchGatewayEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("WatchGatewayEvents() len = %d", len(events))
	}
	if got := events[0]["workload_id"]; got != "wl-9" {
		t.Fatalf("WatchGatewayEvents() workload_id = %v", got)
	}
}

func TestWatchGatewayEventsTimesOutOnIdleStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	cli := &Client{cfg: config.Config{Transport: "http", APIEndpoint: server.URL, RPCTimeoutSeconds: 1}, httpClient: server.Client()}
	start := time.Now()
	events, err := cli.WatchGatewayEvents("", "", "", 20)
	if err != nil {
		t.Fatalf("WatchGatewayEvents() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("WatchGatewayEvents() idle stream returned %d events", len(events))
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("WatchGatewayEvents() took too long: %s", elapsed)
	}
}
