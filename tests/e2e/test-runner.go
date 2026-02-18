package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	metricsURL := getenvDefault("SCHEDULER_METRICS_URL", "http://localhost:8084")
	client := &http.Client{Timeout: 10 * time.Second}

	if err := waitFor(metricsURL+"/health", client, 30, 2*time.Second); err != nil {
		panic(fmt.Sprintf("health check failed: %v", err))
	}
	if err := waitFor(metricsURL+"/metrics", client, 30, 2*time.Second); err != nil {
		panic(fmt.Sprintf("metrics endpoint failed: %v", err))
	}

	body, err := getBody(metricsURL+"/metrics", client)
	if err != nil {
		panic(fmt.Sprintf("metrics fetch failed: %v", err))
	}

	required := []string{
		"go_gc_duration_seconds",
		"process_cpu_seconds_total",
	}
	for _, metric := range required {
		if !strings.Contains(body, metric) {
			panic(fmt.Sprintf("expected metric %q not found", metric))
		}
	}

	fmt.Println("✅ Go-based E2E probe passed")
}

func waitFor(url string, client *http.Client, retries int, interval time.Duration) error {
	for i := 0; i < retries; i++ {
		resp, err := client.Get(url)
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			return nil
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("timeout waiting for %s", url)
}

func getBody(url string, client *http.Client) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status: %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func getenvDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
