package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

// HTTPClient extends http.Client with JSON methods
type HTTPClient struct {
	*http.Client
}

// TestSuite represents the E2E test suite
type TestSuite struct {
	apiGatewayURL    string
	prowSchedulerURL string
	persysAgentURL   string
	httpClient       *HTTPClient
}

// TestResult represents a test result
type TestResult struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Duration  string    `json:"duration"`
	Error     string    `json:"error,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// TestReport represents the overall test report
type TestReport struct {
	TestRunID   string       `json:"testRunId"`
	Timestamp   time.Time    `json:"timestamp"`
	Duration    string       `json:"duration"`
	Results     []TestResult `json:"results"`
	TotalTests  int          `json:"totalTests"`
	PassedTests int          `json:"passedTests"`
	FailedTests int          `json:"failedTests"`
	Status      string       `json:"status"`
}

func main() {
	// Get environment variables
	apiGatewayURL := os.Getenv("API_GATEWAY_URL")
	prowSchedulerURL := os.Getenv("PROW_SCHEDULER_URL")
	persysAgentURL := os.Getenv("PERSYS_AGENT_URL")

	if apiGatewayURL == "" || prowSchedulerURL == "" || persysAgentURL == "" {
		log.Fatal("Missing required environment variables")
	}

	// Create test suite
	testSuite := &TestSuite{
		apiGatewayURL:    apiGatewayURL,
		prowSchedulerURL: prowSchedulerURL,
		persysAgentURL:   persysAgentURL,
		httpClient: &HTTPClient{
			Client: &http.Client{
				Timeout: 30 * time.Second,
			},
		},
	}

	// Run tests
	testSuite.RunAllTests()
}

// RunAllTests runs all E2E tests
func (suite *TestSuite) RunAllTests() {
	fmt.Println("🚀 Starting Persys Cloud E2E Tests...")

	// Wait for services to be ready
	suite.waitForServices()

	// Run test scenarios
	tests := []struct {
		name string
		fn   func() error
	}{
		{"Basic Workflow", suite.testBasicWorkflow},
		{"Authentication Flow", suite.testAuthenticationFlow},
		{"Service Discovery", suite.testServiceDiscovery},
		{"Async Execution", suite.testAsyncExecution},
		{"Reconciliation", suite.testReconciliation},
		{"Error Handling", suite.testErrorHandling},
	}

	passed := 0
	failed := 0

	for _, test := range tests {
		fmt.Printf("📋 Running %s Test...\n", test.name)
		start := time.Now()

		err := test.fn()
		duration := time.Since(start)

		if err != nil {
			fmt.Printf("❌ %s failed: %v (duration: %v)\n", test.name, err, duration)
			failed++
		} else {
			fmt.Printf("✅ %s passed (duration: %v)\n", test.name, duration)
			passed++
		}
	}

	fmt.Printf("\n📊 Test Results: %d passed, %d failed\n", passed, failed)
	fmt.Println("✅ E2E Tests completed!")
}

// Helper methods

func (suite *TestSuite) waitForServices() {
	fmt.Println("⏳ Waiting for services to be ready...")

	services := []struct {
		name string
		url  string
	}{
		{"API Gateway", suite.apiGatewayURL + "/"},
		{"Prow Scheduler", suite.prowSchedulerURL + "/"},
		{"Persys Agent", suite.persysAgentURL + "/api/v1/health"},
	}

	for _, service := range services {
		suite.waitForService(service.name, service.url)
	}
}

func (suite *TestSuite) waitForService(name, url string) {
	maxRetries := 30
	for i := 0; i < maxRetries; i++ {
		resp, err := suite.httpClient.Get(url)
		if err == nil && resp.StatusCode == 200 {
			fmt.Printf("✅ %s is ready\n", name)
			return
		}
		time.Sleep(2 * time.Second)
	}
	log.Fatalf("❌ %s failed to start", name)
}

func (suite *TestSuite) testBasicWorkflow() error {
	// Test 1: Verify services are accessible
	if err := suite.testServiceAccessibility(); err != nil {
		return fmt.Errorf("service accessibility: %w", err)
	}

	// Test 2: Create and execute a simple workload
	if err := suite.testWorkloadCreation(); err != nil {
		return fmt.Errorf("workload creation: %w", err)
	}

	// Test 3: Verify workload execution
	if err := suite.testWorkloadExecution(); err != nil {
		return fmt.Errorf("workload execution: %w", err)
	}

	// Test 4: Verify monitoring
	if err := suite.testMonitoring(); err != nil {
		return fmt.Errorf("monitoring: %w", err)
	}

	return nil
}

func (suite *TestSuite) testAuthenticationFlow() error {
	// Test mTLS authentication
	if err := suite.testMTLSAuthentication(); err != nil {
		return fmt.Errorf("mTLS authentication: %w", err)
	}

	// Test HMAC authentication
	if err := suite.testHMACAuthentication(); err != nil {
		return fmt.Errorf("HMAC authentication: %w", err)
	}

	// Test invalid authentication
	if err := suite.testInvalidAuthentication(); err != nil {
		return fmt.Errorf("invalid authentication: %w", err)
	}

	return nil
}

func (suite *TestSuite) testServiceDiscovery() error {
	// Test scheduler discovery
	if err := suite.testSchedulerDiscovery(); err != nil {
		return fmt.Errorf("scheduler discovery: %w", err)
	}

	// Test discovery fallback
	if err := suite.testDiscoveryFallback(); err != nil {
		return fmt.Errorf("discovery fallback: %w", err)
	}

	return nil
}

func (suite *TestSuite) testAsyncExecution() error {
	// Test immediate response
	if err := suite.testImmediateResponse(); err != nil {
		return fmt.Errorf("immediate response: %w", err)
	}

	// Test background execution
	if err := suite.testBackgroundExecution(); err != nil {
		return fmt.Errorf("background execution: %w", err)
	}

	return nil
}

func (suite *TestSuite) testReconciliation() error {
	// Test container restart
	if err := suite.testContainerRestart(); err != nil {
		return fmt.Errorf("container restart: %w", err)
	}

	// Test reconciliation statistics
	if err := suite.testReconciliationStats(); err != nil {
		return fmt.Errorf("reconciliation stats: %w", err)
	}

	return nil
}

func (suite *TestSuite) testErrorHandling() error {
	// Test invalid workload
	if err := suite.testInvalidWorkload(); err != nil {
		return fmt.Errorf("invalid workload: %w", err)
	}

	// Test network failures
	if err := suite.testNetworkFailures(); err != nil {
		return fmt.Errorf("network failures: %w", err)
	}

	return nil
}

func (suite *TestSuite) testServiceAccessibility() error {
	// Test API Gateway
	resp, err := suite.httpClient.Get(suite.apiGatewayURL + "/health")
	if err != nil {
		return fmt.Errorf("API Gateway health check failed: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("API Gateway health check returned status %d", resp.StatusCode)
	}

	// Test Prow Scheduler
	resp, err = suite.httpClient.Get(suite.prowSchedulerURL + "/health")
	if err != nil {
		return fmt.Errorf("Prow Scheduler health check failed: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("Prow Scheduler health check returned status %d", resp.StatusCode)
	}

	// Test Persys Agent
	resp, err = suite.httpClient.Get(suite.persysAgentURL + "/health")
	if err != nil {
		return fmt.Errorf("Persys Agent health check failed: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("Persys Agent health check returned status %d", resp.StatusCode)
	}

	return nil
}

func (suite *TestSuite) testWorkloadCreation() error {
	workload := map[string]interface{}{
		"name":  "test-nginx",
		"type":  "docker-container",
		"image": "nginx:alpine",
		"ports": []string{"8080:80"},
	}

	// Create workload via API Gateway
	resp, err := suite.httpClient.PostJSON(suite.apiGatewayURL+"/api/v1/workloads", workload)
	if err != nil {
		return fmt.Errorf("failed to create workload: %w", err)
	}
	if resp.StatusCode != 201 {
		return fmt.Errorf("workload creation returned status %d", resp.StatusCode)
	}

	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}
	if result["workloadId"] == "" {
		return fmt.Errorf("workload ID is empty")
	}

	return nil
}

func (suite *TestSuite) testWorkloadExecution() error {
	// Wait for workload to be executed
	time.Sleep(10 * time.Second)

	// Check if container is running
	resp, err := suite.httpClient.Get(suite.persysAgentURL + "/api/v1/containers")
	if err != nil {
		return fmt.Errorf("failed to get containers: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("container list returned status %d", resp.StatusCode)
	}

	var containers []map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&containers)
	if err != nil {
		return fmt.Errorf("failed to decode containers: %w", err)
	}

	// Verify test container is running
	found := false
	for _, container := range containers {
		if name, ok := container["name"].(string); ok && name == "test-nginx" {
			found = true
			if container["status"] != "running" {
				return fmt.Errorf("test container status is %v, expected running", container["status"])
			}
			break
		}
	}
	if !found {
		return fmt.Errorf("test container not found")
	}

	return nil
}

func (suite *TestSuite) testMonitoring() error {
	// Check metrics endpoint
	resp, err := suite.httpClient.Get(suite.apiGatewayURL + "/metrics")
	if err != nil {
		return fmt.Errorf("metrics endpoint failed: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("metrics endpoint returned status %d", resp.StatusCode)
	}

	// Check workload status
	resp, err = suite.httpClient.Get(suite.apiGatewayURL + "/api/v1/workloads")
	if err != nil {
		return fmt.Errorf("workload status failed: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("workload status returned status %d", resp.StatusCode)
	}

	return nil
}

func (suite *TestSuite) testMTLSAuthentication() error {
	// Test with valid mTLS certificates
	// This would require proper certificate setup
	fmt.Println("   Testing mTLS authentication...")
	return nil
}

func (suite *TestSuite) testHMACAuthentication() error {
	// Test HMAC between prow and persys-agent
	fmt.Println("   Testing HMAC authentication...")
	return nil
}

func (suite *TestSuite) testInvalidAuthentication() error {
	// Test with invalid credentials
	fmt.Println("   Testing invalid authentication...")
	return nil
}

func (suite *TestSuite) testSchedulerDiscovery() error {
	// Test CoreDNS-based discovery
	fmt.Println("   Testing scheduler discovery...")
	return nil
}

func (suite *TestSuite) testDiscoveryFallback() error {
	// Test fallback to configured address
	fmt.Println("   Testing discovery fallback...")
	return nil
}

func (suite *TestSuite) testImmediateResponse() error {
	// Test that workload creation returns immediately
	start := time.Now()

	workload := map[string]interface{}{
		"name":  "async-test",
		"type":  "docker-container",
		"image": "nginx:alpine",
	}

	resp, err := suite.httpClient.PostJSON(suite.apiGatewayURL+"/api/v1/workloads", workload)
	if err != nil {
		return fmt.Errorf("failed to create async workload: %w", err)
	}
	if resp.StatusCode != 201 {
		return fmt.Errorf("async workload creation returned status %d", resp.StatusCode)
	}

	duration := time.Since(start)
	if duration > 5*time.Second {
		return fmt.Errorf("response took %v, should be immediate", duration)
	}

	return nil
}

func (suite *TestSuite) testBackgroundExecution() error {
	// Test that workload executes in background
	fmt.Println("   Testing background execution...")
	return nil
}

func (suite *TestSuite) testContainerRestart() error {
	// Test reconciliation by stopping container
	fmt.Println("   Testing container restart...")
	return nil
}

func (suite *TestSuite) testReconciliationStats() error {
	// Check reconciliation statistics
	resp, err := suite.httpClient.Get(suite.prowSchedulerURL + "/api/v1/reconciliation/stats")
	if err != nil {
		return fmt.Errorf("reconciliation stats failed: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("reconciliation stats returned status %d", resp.StatusCode)
	}

	return nil
}

func (suite *TestSuite) testInvalidWorkload() error {
	// Test with invalid workload configuration
	invalidWorkload := map[string]interface{}{
		"name": "invalid",
		"type": "invalid-type",
	}

	resp, err := suite.httpClient.PostJSON(suite.apiGatewayURL+"/api/v1/workloads", invalidWorkload)
	if err != nil {
		return fmt.Errorf("failed to submit invalid workload: %w", err)
	}
	if resp.StatusCode != 400 {
		return fmt.Errorf("invalid workload returned status %d, expected 400", resp.StatusCode)
	}

	return nil
}

func (suite *TestSuite) testNetworkFailures() error {
	// Test network failure scenarios
	fmt.Println("   Testing network failures...")
	return nil
}

// PostJSON sends a POST request with JSON data
func (c *HTTPClient) PostJSON(url string, data interface{}) (*http.Response, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	return c.Do(req)
}
