package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func main() {
	cfg := loadConfig()
	client := &http.Client{Timeout: 10 * time.Second}

	must(waitForHTTP(cfg.schedulerMetricsURL+"/health", client, cfg.maxRetries, cfg.retryInterval))
	must(waitForHTTP(cfg.schedulerMetricsURL+"/metrics", client, cfg.maxRetries, cfg.retryInterval))
	must(waitForHTTP(cfg.agentMetricsURL+"/health", client, cfg.maxRetries, cfg.retryInterval))
	must(waitForRegisteredNode(cfg))

	logStep("apply workload")
	must(applyWorkloadWithRetry(cfg))

	var workload getWorkloadResponse
	must(poll(cfg.maxRetries, cfg.retryInterval, func() (bool, error) {
		out, err := runSmokeCapture(cfg.schedulerGRPCAddr, "-op", "get-workload", "-workload-id", cfg.testWorkloadID)
		if err != nil {
			return false, nil
		}
		if err := unmarshalSmokeJSON(out, &workload); err != nil {
			return false, nil
		}
		s := strings.ToLower(strings.TrimSpace(workload.Workload.Status))
		return s == "running" || s == "pending" || s == "unknown", nil
	}))

	listOut, err := runSmokeCapture(cfg.schedulerGRPCAddr, "-op", "list-workloads")
	must(err)
	_ = listOut
	must(waitForWorkloadInList(cfg))

	summaryOut, err := runSmokeCapture(cfg.schedulerGRPCAddr, "-op", "cluster-summary")
	if err != nil {
		failf("cluster-summary failed: %v", err)
	}
	var summary clusterSummaryResponse
	must(unmarshalSmokeJSON(summaryOut, &summary))
	if summary.TotalWorkloads < 1 {
		failf("cluster summary reported total_workloads=%d, want >=1", summary.TotalWorkloads)
	}

	logStep("delete workload")
	must(runSmoke(cfg.schedulerGRPCAddr, "-op", "delete-workload", "-workload-id", cfg.testWorkloadID))
	must(poll(cfg.maxRetries, cfg.retryInterval, func() (bool, error) {
		out, err := runSmokeCapture(cfg.schedulerGRPCAddr, "-op", "get-workload", "-workload-id", cfg.testWorkloadID)
		if err != nil {
			return true, nil // NotFound is acceptable terminal state.
		}
		var r getWorkloadResponse
		if err := unmarshalSmokeJSON(out, &r); err != nil {
			return false, nil
		}
		s := strings.ToLower(strings.TrimSpace(r.Workload.Status))
		return s == "deleting" || s == "deleted", nil
	}))

	body, err := getBody(cfg.schedulerMetricsURL+"/metrics", client)
	must(err)
	required := []string{
		"persys_scheduler_grpc_server_requests_total",
		"persys_scheduler_grpc_server_request_duration_seconds",
		"persys_scheduler_agent_rpc_requests_total",
		"persys_scheduler_agent_rpc_duration_seconds",
		"persys_scheduler_nodes_status",
		"persys_scheduler_workloads_status",
		"persys_scheduler_reconciliation_results_total",
	}
	for _, metric := range required {
		if !strings.Contains(body, metric) {
			failf("expected metric %q not found", metric)
		}
	}

	fmt.Println("E2E scheduler + compute-agent suite passed")
}

type config struct {
	schedulerMetricsURL string
	schedulerGRPCAddr   string
	agentMetricsURL     string
	testWorkloadID      string
	retryInterval       time.Duration
	maxRetries          int
}

type getWorkloadResponse struct {
	Workload workloadView `json:"workload"`
}

type listWorkloadsResponse struct {
	Workloads []workloadView `json:"workloads"`
}

type workloadView struct {
	WorkloadID string `json:"workload_id"`
	Status     string `json:"status"`
}

type clusterSummaryResponse struct {
	TotalWorkloads int32 `json:"total_workloads"`
}

func loadConfig() config {
	return config{
		schedulerMetricsURL: getenvDefault("SCHEDULER_METRICS_URL", "http://localhost:8084"),
		schedulerGRPCAddr:   getenvDefault("SCHEDULER_GRPC_ADDR", "localhost:8085"),
		agentMetricsURL:     getenvDefault("AGENT_METRICS_URL", "http://compute-agent:8080"),
		testWorkloadID:      getenvDefault("TEST_WORKLOAD_ID", "e2e-workload-1"),
		retryInterval:       getenvDurationDefault("RETRY_INTERVAL", 2*time.Second),
		maxRetries:          getenvIntDefault("MAX_RETRIES", 40),
	}
}

func applyWorkloadWithRetry(cfg config) error {
	return poll(cfg.maxRetries, cfg.retryInterval, func() (bool, error) {
		out, err := runSmokeCapture(cfg.schedulerGRPCAddr,
			"-op", "apply-container",
			"-workload-id", cfg.testWorkloadID,
			"-container-image", "busybox:latest",
			"-container-cmd", "sh,-c,sleep 60",
			"-w-cpu", "100",
			"-w-mem", "128",
			"-w-disk", "1",
		)
		if err != nil {
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "no suitable node") || strings.Contains(msg, "cannot place workload") {
				return false, nil
			}
			return false, err
		}
		lower := strings.ToLower(out)
		if strings.Contains(lower, "success=true") {
			return true, nil
		}
		if strings.Contains(lower, "no suitable node") || strings.Contains(lower, "cannot place workload") {
			return false, nil
		}
		return false, fmt.Errorf("apply-container did not report success: %s", out)
	})
}

func waitForRegisteredNode(cfg config) error {
	logStep("wait for compute-agent node registration")
	return poll(cfg.maxRetries, cfg.retryInterval, func() (bool, error) {
		out, err := runSmokeCapture(cfg.schedulerGRPCAddr, "-op", "cluster-summary")
		if err != nil {
			return false, nil
		}
		var summary struct {
			TotalNodes int32 `json:"total_nodes"`
		}
		if err := unmarshalSmokeJSON(out, &summary); err != nil {
			return false, nil
		}
		return summary.TotalNodes >= 1, nil
	})
}

func waitForWorkloadInList(cfg config) error {
	return poll(cfg.maxRetries, cfg.retryInterval, func() (bool, error) {
		out, err := runSmokeCapture(cfg.schedulerGRPCAddr, "-op", "list-workloads")
		if err != nil {
			return false, nil
		}
		var listResp listWorkloadsResponse
		if err := unmarshalSmokeJSON(out, &listResp); err != nil {
			return false, nil
		}
		if containsWorkload(listResp.Workloads, cfg.testWorkloadID) {
			return true, nil
		}
		return false, nil
	})
}

func runSmoke(schedulerAddr string, args ...string) error {
	_, err := runSmokeCapture(schedulerAddr, args...)
	return err
}

func runSmokeCapture(schedulerAddr string, args ...string) (string, error) {
	cmdArgs := append([]string{"-scheduler", schedulerAddr}, args...)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "./smoke-client", cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String() + "\n" + stderr.String())
	if err != nil {
		return out, fmt.Errorf("smoke-client %v failed: %w\n%s", args, err, out)
	}
	return out, nil
}

func waitForHTTP(url string, client *http.Client, retries int, interval time.Duration) error {
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

func poll(retries int, interval time.Duration, f func() (bool, error)) error {
	for i := 0; i < retries; i++ {
		ok, err := f()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("timeout while polling condition")
}

func unmarshalSmokeJSON(out string, target interface{}) error {
	raw, err := extractJSONObject(out)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), target)
}

func extractJSONObject(s string) (string, error) {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start == -1 || end == -1 || end <= start {
		return "", fmt.Errorf("no JSON object found in output: %s", s)
	}
	return s[start : end+1], nil
}

func containsWorkload(workloads []workloadView, workloadID string) bool {
	for _, w := range workloads {
		if strings.TrimSpace(w.WorkloadID) == workloadID {
			return true
		}
	}
	return false
}

func getenvIntDefault(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func getenvDurationDefault(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if s, err := strconv.Atoi(v); err == nil {
		return time.Duration(s) * time.Second
	}
	return fallback
}

func getenvDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func logStep(msg string) {
	log.Printf("==> %s", msg)
}

func must(err error) {
	if err != nil {
		failf("%v", err)
	}
}

func failf(format string, args ...interface{}) {
	log.Fatalf(format, args...)
}
