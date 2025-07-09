#!/bin/bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
API_GATEWAY_URL="${API_GATEWAY_URL:-https://localhost:8551}"
PROW_SCHEDULER_URL="${PROW_SCHEDULER_URL:-https://localhost:8085}"
PERSYS_AGENT_URL="${PERSYS_AGENT_URL:-http://localhost:8080}"
TEST_TIMEOUT=30
RETRY_INTERVAL=2

# Test results
PASSED=0
FAILED=0
TOTAL=0

# Helper functions
log_info() {
    echo -e "${BLUE}ℹ️  $1${NC}"
}

log_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

log_error() {
    echo -e "${RED}❌ $1${NC}"
}

log_warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

# Wait for service to be ready
wait_for_service() {
    local name=$1
    local url=$2
    local max_retries=30
    
    log_info "Waiting for $name to be ready..."
    
    for i in $(seq 1 $max_retries); do
        if curl -s -f "$url/health" > /dev/null 2>&1; then
            log_success "$name is ready"
            return 0
        fi
        sleep $RETRY_INTERVAL
    done
    
    log_error "$name failed to start"
    return 1
}

# Run a test
run_test() {
    local test_name=$1
    local test_func=$2
    
    TOTAL=$((TOTAL + 1))
    log_info "Running test: $test_name"
    
    local start_time=$(date +%s)
    
    if $test_func; then
        local end_time=$(date +%s)
        local duration=$((end_time - start_time))
        log_success "$test_name passed (${duration}s)"
        PASSED=$((PASSED + 1))
    else
        local end_time=$(date +%s)
        local duration=$((end_time - start_time))
        log_error "$test_name failed (${duration}s)"
        FAILED=$((FAILED + 1))
    fi
}

# Test functions

test_service_health() {
    log_info "Testing service health endpoints..."
    
    # Test API Gateway
    if ! curl -s -f "$API_GATEWAY_URL/health" > /dev/null; then
        log_error "API Gateway health check failed"
        return 1
    fi
    
    # Test Prow Scheduler
    if ! curl -s -f "$PROW_SCHEDULER_URL/health" > /dev/null; then
        log_error "Prow Scheduler health check failed"
        return 1
    fi
    
    # Test Persys Agent
    if ! curl -s -f "$PERSYS_AGENT_URL/health" > /dev/null; then
        log_error "Persys Agent health check failed"
        return 1
    fi
    
    log_success "All services are healthy"
    return 0
}

test_workload_creation() {
    log_info "Testing workload creation..."
    
    local workload_data='{
        "name": "test-nginx",
        "type": "docker-container",
        "image": "nginx:alpine",
        "ports": ["8080:80"]
    }'
    
    local response=$(curl -s -w "%{http_code}" -X POST \
        -H "Content-Type: application/json" \
        -d "$workload_data" \
        "$API_GATEWAY_URL/api/v1/workloads")
    
    local status_code="${response: -3}"
    local response_body="${response%???}"
    
    if [ "$status_code" != "201" ]; then
        log_error "Workload creation failed with status $status_code: $response_body"
        return 1
    fi
    
    # Extract workload ID from response
    local workload_id=$(echo "$response_body" | jq -r '.workloadId // empty')
    if [ -z "$workload_id" ]; then
        log_error "No workload ID in response"
        return 1
    fi
    
    log_success "Workload created with ID: $workload_id"
    return 0
}

test_workload_execution() {
    log_info "Testing workload execution..."
    
    # Wait for workload to be executed
    sleep 10
    
    # Check if container is running
    local containers=$(curl -s "$PERSYS_AGENT_URL/api/v1/containers")
    local test_container=$(echo "$containers" | jq -r '.[] | select(.name == "test-nginx") | .status')
    
    if [ "$test_container" != "running" ]; then
        log_error "Test container is not running (status: $test_container)"
        return 1
    fi
    
    log_success "Test container is running"
    return 0
}

test_metrics_endpoint() {
    log_info "Testing metrics endpoint..."
    
    if ! curl -s -f "$API_GATEWAY_URL/metrics" > /dev/null; then
        log_error "Metrics endpoint failed"
        return 1
    fi
    
    log_success "Metrics endpoint is accessible"
    return 0
}

test_workload_listing() {
    log_info "Testing workload listing..."
    
    local workloads=$(curl -s "$API_GATEWAY_URL/api/v1/workloads")
    local workload_count=$(echo "$workloads" | jq '. | length')
    
    if [ "$workload_count" -eq 0 ]; then
        log_warning "No workloads found"
    else
        log_success "Found $workload_count workload(s)"
    fi
    
    return 0
}

test_reconciliation_stats() {
    log_info "Testing reconciliation statistics..."
    
    if ! curl -s -f "$PROW_SCHEDULER_URL/api/v1/reconciliation/stats" > /dev/null; then
        log_error "Reconciliation stats endpoint failed"
        return 1
    fi
    
    log_success "Reconciliation stats are accessible"
    return 0
}

test_invalid_workload() {
    log_info "Testing invalid workload rejection..."
    
    local invalid_workload='{
        "name": "invalid",
        "type": "invalid-type"
    }'
    
    local response=$(curl -s -w "%{http_code}" -X POST \
        -H "Content-Type: application/json" \
        -d "$invalid_workload" \
        "$API_GATEWAY_URL/api/v1/workloads")
    
    local status_code="${response: -3}"
    
    if [ "$status_code" != "400" ]; then
        log_error "Invalid workload was not rejected (status: $status_code)"
        return 1
    fi
    
    log_success "Invalid workload properly rejected"
    return 0
}

test_async_execution() {
    log_info "Testing async execution..."
    
    local start_time=$(date +%s)
    
    local workload_data='{
        "name": "async-test",
        "type": "docker-container",
        "image": "nginx:alpine"
    }'
    
    local response=$(curl -s -w "%{http_code}" -X POST \
        -H "Content-Type: application/json" \
        -d "$workload_data" \
        "$API_GATEWAY_URL/api/v1/workloads")
    
    local end_time=$(date +%s)
    local duration=$((end_time - start_time))
    
    if [ "$duration" -gt 5 ]; then
        log_error "Async execution took too long (${duration}s)"
        return 1
    fi
    
    log_success "Async execution responded quickly (${duration}s)"
    return 0
}

# Main test execution
main() {
    echo "🚀 Starting Persys Cloud E2E Tests..."
    echo "======================================"
    
    # Wait for services to be ready
    wait_for_service "API Gateway" "$API_GATEWAY_URL"
    wait_for_service "Prow Scheduler" "$PROW_SCHEDULER_URL"
    wait_for_service "Persys Agent" "$PERSYS_AGENT_URL"
    
    echo ""
    echo "📋 Running test scenarios..."
    echo "============================"
    
    # Run tests
    run_test "Service Health" test_service_health
    run_test "Workload Creation" test_workload_creation
    run_test "Workload Execution" test_workload_execution
    run_test "Metrics Endpoint" test_metrics_endpoint
    run_test "Workload Listing" test_workload_listing
    run_test "Reconciliation Stats" test_reconciliation_stats
    run_test "Invalid Workload" test_invalid_workload
    run_test "Async Execution" test_async_execution
    
    echo ""
    echo "📊 Test Results"
    echo "==============="
    echo "Total tests: $TOTAL"
    echo "Passed: $PASSED"
    echo "Failed: $FAILED"
    
    if [ $FAILED -eq 0 ]; then
        log_success "All tests passed! 🎉"
        exit 0
    else
        log_error "Some tests failed! 💥"
        exit 1
    fi
}

# Check dependencies
check_dependencies() {
    local deps=("curl" "jq")
    
    for dep in "${deps[@]}"; do
        if ! command -v "$dep" &> /dev/null; then
            log_error "Required dependency '$dep' is not installed"
            exit 1
        fi
    done
}

# Run main function
check_dependencies
main 