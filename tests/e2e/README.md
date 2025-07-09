# Persys Cloud End-to-End Testing

This directory contains comprehensive end-to-end tests for the persys-cloud system, designed to validate the complete workflow from client request through workload execution, monitoring, and reconciliation.

## 🎯 Overview

The E2E tests cover:

- **Full System Integration**: All components working together
- **Authentication Flow**: mTLS, HMAC, and OAuth authentication
- **Service Discovery**: CoreDNS-based scheduler discovery
- **Workload Lifecycle**: Complete workflow from creation to execution
- **Reconciliation**: Self-healing capabilities
- **Error Handling**: Failure scenarios and recovery
- **Performance**: System performance under load

## 🏗️ Test Architecture

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

## 🚀 Quick Start

### Prerequisites

- Docker and Docker Compose
- Go 1.21+ (for Go-based tests)
- curl and jq (for script-based tests)
- OpenSSL (for certificate generation)

### 1. Set up the test environment

```bash
make setup
```

This creates the necessary configuration files and directories.

### 2. Generate test certificates (optional)

```bash
make certs
```

This generates mTLS certificates for testing authentication.

### 3. Run the tests

Choose one of the following approaches:

#### Option A: Script-based tests (Recommended for quick testing)

```bash
make test-script
```

#### Option B: Docker Compose tests (Recommended for CI/CD)

```bash
make test-docker
```

#### Option C: Go-based tests (Recommended for development)

```bash
make test-go
```

## 📋 Test Scenarios

### 1. Basic Workflow Test
- ✅ Service health checks
- ✅ Workload creation via API Gateway
- ✅ Workload execution on Persys Agent
- ✅ Container status verification
- ✅ Metrics endpoint accessibility

### 2. Authentication Flow Test
- 🔐 mTLS authentication between components
- 🔐 HMAC authentication between Prow and Agent
- 🔐 Invalid authentication rejection

### 3. Service Discovery Test
- 🔍 CoreDNS-based scheduler discovery
- 🔍 Discovery fallback mechanisms
- 🔍 Multiple scheduler scenarios

### 4. Async Execution Test
- ⚡ Immediate response validation
- ⚡ Background execution verification
- ⚡ No HTTP timeout confirmation

### 5. Reconciliation Test
- 🔄 Container restart verification
- 🔄 Reconciliation statistics
- 🔄 Self-healing capabilities

### 6. Error Handling Test
- ⚠️ Invalid workload rejection
- ⚠️ Network failure handling
- ⚠️ System stability verification

## 🛠️ Implementation Approaches

### Approach 1: Docker Compose Integration Tests

**Best for**: CI/CD pipelines, consistent environments

```bash
# Run with Docker Compose
make test-docker
```

**Features**:
- Complete containerized environment
- Automatic service discovery
- Built-in monitoring (Prometheus/Grafana)
- Easy cleanup and isolation

### Approach 2: Script-based Tests

**Best for**: Quick testing, debugging, manual validation

```bash
# Run with bash script
make test-script
```

**Features**:
- Simple and flexible
- Easy to debug
- Language agnostic
- Minimal dependencies

### Approach 3: Go-based Tests

**Best for**: Development, type safety, comprehensive testing

```bash
# Run with Go test runner
make test-go
```

**Features**:
- Type-safe testing
- Good integration with existing codebase
- Comprehensive error handling
- Structured test reporting

## 📊 Test Results and Reporting

### Test Output Example

```
🚀 Starting Persys Cloud E2E Tests...
======================================

⏳ Waiting for services to be ready...
✅ API Gateway is ready
✅ Prow Scheduler is ready
✅ Persys Agent is ready

📋 Running test scenarios...
============================
📋 Running test: Service Health
✅ Service Health passed (2s)
📋 Running test: Workload Creation
✅ Workload Creation passed (3s)
📋 Running test: Workload Execution
✅ Workload Execution passed (12s)
📋 Running test: Metrics Endpoint
✅ Metrics Endpoint passed (1s)
📋 Running test: Workload Listing
✅ Workload Listing passed (1s)
📋 Running test: Reconciliation Stats
✅ Reconciliation Stats passed (1s)
📋 Running test: Invalid Workload
✅ Invalid Workload passed (1s)
📋 Running test: Async Execution
✅ Async Execution passed (2s)

📊 Test Results
===============
Total tests: 8
Passed: 8
Failed: 0

✅ All tests passed! 🎉
```

### Test Metrics

The tests collect and report:

- **Response Time**: API response times
- **Throughput**: Workloads per second
- **Success Rate**: Percentage of successful operations
- **Error Rate**: Percentage of failed operations
- **Resource Usage**: CPU, memory, disk usage
- **Reconciliation Time**: Time to detect and fix issues

## 🔧 Configuration

### Environment Variables

```bash
# Service URLs
API_GATEWAY_URL=https://localhost:8551
PROW_SCHEDULER_URL=https://localhost:8085
PERSYS_AGENT_URL=http://localhost:8080

# Test Configuration
TEST_TIMEOUT=30
RETRY_INTERVAL=2
```

### Test Configuration Files

- `docker-compose.test.yml`: Docker Compose test environment
- `test-configs/`: Configuration files for all services
- `test-suite.sh`: Bash script test runner
- `test-runner.go`: Go-based test runner

## 🧪 Advanced Testing

### Performance Testing

```bash
make test-performance
```

Tests system performance under various loads.

### Load Testing

```bash
make test-load
```

Tests system behavior under high load conditions.

### Security Testing

```bash
make test-security
```

Tests security aspects including authentication and authorization.

### Chaos Engineering

```bash
# Stop a service and verify recovery
docker-compose -f docker-compose.test.yml stop api-gateway
# Wait and verify system recovery
make test-basic
```

## 🐛 Troubleshooting

### Common Issues

1. **Services not starting**
   ```bash
   # Check service logs
   docker-compose -f docker-compose.test.yml logs
   
   # Restart services
   docker-compose -f docker-compose.test.yml restart
   ```

2. **Certificate issues**
   ```bash
   # Regenerate certificates
   make certs
   ```

3. **Network connectivity**
   ```bash
   # Check network connectivity
   docker network ls
   docker network inspect persys-cloud-test
   ```

4. **Test failures**
   ```bash
   # Run with verbose output
   ./test-suite.sh --verbose
   
   # Check individual service health
   curl -f http://localhost:8080/health
   ```

### Debug Mode

```bash
# Enable debug output
DEBUG=1 make test-script

# Run with detailed logging
docker-compose -f docker-compose.test.yml up --build --abort-on-container-exit --exit-code-from test-client --verbose
```

## 📈 Continuous Integration

### GitHub Actions Example

```yaml
name: E2E Tests
on: [push, pull_request]

jobs:
  e2e-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      
      - name: Set up Docker
        uses: docker/setup-buildx-action@v2
      
      - name: Run E2E Tests
        run: |
          cd tests/e2e
          make setup
          make test-docker
      
      - name: Upload test results
        uses: actions/upload-artifact@v3
        with:
          name: test-results
          path: tests/e2e/test-results/
```

## 🔄 Maintenance

### Regular Tasks

1. **Update test dependencies**
   ```bash
   go mod tidy
   docker-compose -f docker-compose.test.yml pull
   ```

2. **Clean up test artifacts**
   ```bash
   make clean
   ```

3. **Update test scenarios**
   - Add new test cases to `test-suite.sh`
   - Update Go test runner with new scenarios
   - Modify Docker Compose configuration as needed

### Test Data Management

- Test workloads are defined in the test scripts
- Test certificates are generated automatically
- Test configurations are version controlled
- Test results are stored in `test-results/` directory

## 📚 Additional Resources

- [E2E Testing Strategy](../docs/e2e-testing-strategy.md): Comprehensive testing strategy
- [System Architecture](../docs/architecture.md): System architecture overview
- [API Documentation](../docs/api.md): API reference
- [Troubleshooting Guide](../docs/troubleshooting.md): Common issues and solutions

## 🤝 Contributing

When adding new tests:

1. Follow the existing test structure
2. Add appropriate error handling
3. Include meaningful test descriptions
4. Update this README if needed
5. Ensure tests are idempotent

## 📄 License

This testing framework is part of the persys-cloud project and follows the same license terms. 