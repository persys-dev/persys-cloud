# Persys Cloud End-to-End Testing Strategy

## Overview

This document outlines comprehensive end-to-end testing strategies for the persys-cloud system, covering the complete workflow from client request through workload execution, monitoring, and reconciliation.

## 🎯 Testing Objectives

1. **Full System Integration**: Test all components working together
2. **Authentication Flow**: Verify mTLS, HMAC, and OAuth authentication
3. **Service Discovery**: Test CoreDNS-based scheduler discovery
4. **Workload Lifecycle**: Complete workflow from creation to execution
5. **Reconciliation**: Verify self-healing capabilities
6. **Error Handling**: Test failure scenarios and recovery
7. **Performance**: Validate system performance under load

## 🏗️ Test Architecture

### Test Environment Setup
```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Test Client   │    │  API Gateway    │    │  Prow Scheduler │
│   (persys-cli)  │───▶│   (mTLS)        │───▶│   (CoreDNS)     │
└─────────────────┘    └─────────────────┘    └─────────────────┘
                                │                       │
                                ▼                       ▼
                       ┌─────────────────┐    ┌─────────────────┐
                       │   CoreDNS       │    │  Persys Agent   │
                       │   (Discovery)   │    │   (HMAC)        │
                       └─────────────────┘    └─────────────────┘
                                                       │
                                                       ▼
                                              ┌─────────────────┐
                                              │   Docker        │
                                              │   (Containers)  │
                                              └─────────────────┘
```

## 📋 Test Scenarios

### 1. **Basic Workflow Test**
**Objective**: Verify complete happy path workflow

**Steps**:
1. Start all system components
2. Register persys-agent node
3. Create workload via API gateway
4. Verify workload scheduling
5. Verify container execution
6. Verify monitoring and status updates
7. Verify reconciliation (if needed)

**Expected Results**:
- Workload successfully created and executed
- Container running with correct configuration
- Status properly tracked in etcd
- Monitoring working correctly

### 2. **Authentication Flow Test**
**Objective**: Verify all authentication mechanisms

**Steps**:
1. Test mTLS between client and API gateway
2. Test mTLS between API gateway and prow
3. Test HMAC between prow and persys-agent
4. Test OAuth for GitHub integration
5. Test invalid authentication scenarios

**Expected Results**:
- Valid requests succeed
- Invalid requests properly rejected
- Authentication errors logged appropriately

### 3. **Service Discovery Test**
**Objective**: Verify CoreDNS-based discovery

**Steps**:
1. Start CoreDNS with etcd backend
2. Register prow scheduler in CoreDNS
3. Test API gateway discovery
4. Test multiple scheduler scenarios
5. Test discovery failure fallback

**Expected Results**:
- Schedulers discovered automatically
- Fallback to configured address works
- Multiple schedulers handled correctly

### 4. **Async Execution Test**
**Objective**: Verify async workload execution

**Steps**:
1. Submit long-running workload
2. Verify immediate response
3. Monitor background execution
4. Verify container creation
5. Check logs and status updates

**Expected Results**:
- Immediate response received
- No HTTP timeouts
- Background execution successful
- Status updates received

### 5. **Reconciliation Test**
**Objective**: Verify self-healing capabilities

**Steps**:
1. Create workload and let it run
2. Manually stop container
3. Wait for reconciliation cycle
4. Verify container restarted
5. Test multiple failure scenarios

**Expected Results**:
- Container automatically restarted
- Reconciliation statistics updated
- Error handling working correctly

### 6. **Error Handling Test**
**Objective**: Verify system resilience

**Steps**:
1. Test invalid workload configurations
2. Test network failures
3. Test agent unavailability
4. Test etcd failures
5. Test resource exhaustion

**Expected Results**:
- Errors handled gracefully
- System remains stable
- Proper error messages returned
- Recovery mechanisms work

### 7. **Performance Test**
**Objective**: Verify system performance

**Steps**:
1. Submit multiple concurrent workloads
2. Monitor system resource usage
3. Test with different workload types
4. Measure response times
5. Test scaling scenarios

**Expected Results**:
- System handles concurrent load
- Response times within acceptable limits
- Resource usage reasonable
- No memory leaks or performance degradation

## 🛠️ Implementation Approaches

### Approach 1: **Docker Compose Integration Tests**

**Pros**:
- Easy to set up and tear down
- Consistent environment
- Good for CI/CD integration

**Cons**:
- Limited to containerized components
- May not catch all edge cases

**Implementation**:
```yaml
# docker-compose.test.yml
version: '3.8'
services:
  test-client:
    build: ./test-client
    depends_on:
      - api-gateway
      - prow-scheduler
      - persys-agent
      - coredns
      - etcd
  
  api-gateway:
    build: ./api-gateway
    environment:
      - COREDNS_ADDR=coredns:53
    depends_on:
      - coredns
      - etcd
  
  prow-scheduler:
    build: ./prow
    environment:
      - ETCD_ENDPOINTS=etcd:2379
      - DOMAIN=test.local
    depends_on:
      - etcd
  
  persys-agent:
    build: ./persys-agent
    environment:
      - AGENT_SECRET=test-secret
    depends_on:
      - docker-daemon
  
  coredns:
    image: coredns/coredns
    command: ["-conf", "/etc/coredns/Corefile"]
    volumes:
      - ./test-configs/coredns:/etc/coredns
  
  etcd:
    image: quay.io/coreos/etcd
    command: ["etcd", "--advertise-client-urls=http://0.0.0.0:2379", "--listen-client-urls=http://0.0.0.0:2379"]
```

### Approach 2: **Kubernetes-Based Tests**

**Pros**:
- Production-like environment
- Better resource management
- Scalability testing

**Cons**:
- More complex setup
- Higher resource requirements

**Implementation**:
```yaml
# k8s-test-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: persys-cloud-test
spec:
  replicas: 1
  selector:
    matchLabels:
      app: persys-cloud-test
  template:
    metadata:
      labels:
        app: persys-cloud-test
    spec:
      containers:
      - name: test-runner
        image: persys-cloud/test-runner
        env:
        - name: API_GATEWAY_URL
          value: "https://api-gateway:8551"
        - name: PROW_SCHEDULER_URL
          value: "https://prow-scheduler:8085"
```

### Approach 3: **Script-Based Tests**

**Pros**:
- Simple and flexible
- Easy to debug
- Language agnostic

**Cons**:
- Manual setup required
- Less automated

**Implementation**:
```bash
#!/bin/bash
# test-suite.sh

set -e

echo "Starting Persys Cloud E2E Tests..."

# Start infrastructure
docker-compose -f docker-compose.test.yml up -d

# Wait for services to be ready
./scripts/wait-for-services.sh

# Run test scenarios
./tests/basic-workflow.sh
./tests/authentication.sh
./tests/service-discovery.sh
./tests/async-execution.sh
./tests/reconciliation.sh
./tests/error-handling.sh
./tests/performance.sh

# Cleanup
docker-compose -f docker-compose.test.yml down

echo "E2E Tests completed successfully!"
```

### Approach 4: **Go-Based Test Framework**

**Pros**:
- Type-safe
- Good integration with existing codebase
- Comprehensive testing capabilities

**Cons**:
- More development time
- Go-specific

**Implementation**:
```go
// e2e_test.go
package e2e

import (
    "testing"
    "time"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/suite"
)

type E2ETestSuite struct {
    suite.Suite
    apiGateway *APIGatewayClient
    prowScheduler *ProwSchedulerClient
    persysAgent *PersysAgentClient
}

func (suite *E2ETestSuite) SetupSuite() {
    suite.apiGateway = NewAPIGatewayClient("https://localhost:8551")
    suite.prowScheduler = NewProwSchedulerClient("https://localhost:8085")
    suite.persysAgent = NewPersysAgentClient("http://localhost:8080")
}

func (suite *E2ETestSuite) TestBasicWorkflow() {
    // Test complete workflow
    workload := &Workload{
        Name: "test-workload",
        Type: "docker-container",
        Image: "nginx:alpine",
    }
    
    result, err := suite.apiGateway.CreateWorkload(workload)
    assert.NoError(suite.T(), err)
    assert.NotEmpty(suite.T(), result.WorkloadID)
    
    // Wait for execution
    time.Sleep(10 * time.Second)
    
    // Verify container running
    containers, err := suite.persysAgent.ListContainers()
    assert.NoError(suite.T(), err)
    assert.Contains(suite.T(), containers, result.WorkloadID)
}

func TestE2ESuite(t *testing.T) {
    suite.Run(t, new(E2ETestSuite))
}
```

## 🧪 Test Data Management

### Test Workloads
```yaml
# test-workloads.yaml
workloads:
  - name: "simple-nginx"
    type: "docker-container"
    image: "nginx:alpine"
    ports: ["8080:80"]
    
  - name: "complex-app"
    type: "docker-compose"
    composeDir: "./test-apps/complex-app"
    envVars:
      DB_HOST: "localhost"
      API_KEY: "test-key"
      
  - name: "git-repo"
    type: "git-compose"
    gitRepo: "https://github.com/test/repo"
    branch: "main"
```

### Test Scenarios
```yaml
# test-scenarios.yaml
scenarios:
  basic_workflow:
    description: "Complete happy path workflow"
    steps:
      - create_workload
      - verify_scheduling
      - verify_execution
      - verify_monitoring
      
  reconciliation:
    description: "Self-healing test"
    steps:
      - create_workload
      - wait_for_running
      - stop_container
      - wait_for_reconciliation
      - verify_restarted
      
  error_handling:
    description: "Error scenarios"
    steps:
      - create_invalid_workload
      - verify_error_response
      - verify_system_stability
```

## 📊 Test Metrics and Reporting

### Key Metrics
- **Response Time**: API response times
- **Throughput**: Workloads per second
- **Success Rate**: Percentage of successful operations
- **Error Rate**: Percentage of failed operations
- **Resource Usage**: CPU, memory, disk usage
- **Reconciliation Time**: Time to detect and fix issues

### Test Reports
```json
{
  "testRun": {
    "id": "test-run-123",
    "timestamp": "2024-01-15T10:30:00Z",
    "duration": "15m30s",
    "scenarios": [
      {
        "name": "basic_workflow",
        "status": "PASSED",
        "duration": "2m15s",
        "metrics": {
          "responseTime": "1.2s",
          "successRate": "100%"
        }
      }
    ],
    "summary": {
      "totalScenarios": 7,
      "passed": 6,
      "failed": 1,
      "overallStatus": "PASSED"
    }
  }
}
```

## 🚀 Recommended Implementation Plan

### Phase 1: Basic Integration Tests (Week 1-2)
1. Set up Docker Compose test environment
2. Implement basic workflow test
3. Test authentication flows
4. Create simple test client

### Phase 2: Comprehensive Tests (Week 3-4)
1. Add reconciliation tests
2. Implement error handling tests
3. Add performance benchmarks
4. Create test reporting

### Phase 3: CI/CD Integration (Week 5-6)
1. Integrate with GitHub Actions
2. Add automated test runs
3. Implement test result notifications
4. Create test dashboards

### Phase 4: Advanced Testing (Week 7-8)
1. Add chaos engineering tests
2. Implement load testing
3. Add security testing
4. Create production-like test environments

## 🎯 Success Criteria

### Functional Criteria
- ✅ All test scenarios pass consistently
- ✅ Authentication flows work correctly
- ✅ Service discovery functions properly
- ✅ Reconciliation handles failures
- ✅ Error handling is robust

### Performance Criteria
- ✅ Response times < 5 seconds
- ✅ Throughput > 10 workloads/minute
- ✅ Success rate > 95%
- ✅ Resource usage within limits
- ✅ No memory leaks

### Reliability Criteria
- ✅ Tests run consistently
- ✅ No flaky test results
- ✅ Proper cleanup after tests
- ✅ Clear error reporting
- ✅ Comprehensive logging

## 📝 Next Steps

1. **Choose Implementation Approach**: Select the most suitable testing approach
2. **Set Up Test Environment**: Create the necessary infrastructure
3. **Implement Core Tests**: Start with basic workflow tests
4. **Add Comprehensive Coverage**: Expand to all scenarios
5. **Integrate with CI/CD**: Automate test execution
6. **Monitor and Improve**: Continuously enhance test coverage

This testing strategy ensures comprehensive validation of the persys-cloud system's functionality, performance, and reliability. 