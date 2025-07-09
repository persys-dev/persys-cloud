.PHONY: help test test-docker test-script test-go clean setup

# Default target
help:
	@echo "Persys Cloud E2E Testing"
	@echo "========================"
	@echo ""
	@echo "Available targets:"
	@echo "  test          - Run all E2E tests (default: script-based)"
	@echo "  test-docker   - Run E2E tests using Docker Compose"
	@echo "  test-script   - Run E2E tests using bash script"
	@echo "  test-go       - Run E2E tests using Go test runner"
	@echo "  setup         - Set up test environment"
	@echo "  clean         - Clean up test environment"
	@echo ""

# Default test target
test: test-script

# Run tests using Docker Compose
test-docker:
	@echo "🐳 Running E2E tests with Docker Compose..."
	docker-compose -f docker-compose.test.yml up --build --abort-on-container-exit --exit-code-from test-client
	docker-compose -f docker-compose.test.yml down

# Run tests using bash script
test-script:
	@echo "📜 Running E2E tests with bash script..."
	@chmod +x test-suite.sh
	./test-suite.sh

# Run tests using Go test runner
test-go:
	@echo "🐹 Running E2E tests with Go test runner..."
	@if [ ! -f test-runner ]; then \
		echo "Building test runner..."; \
		go build -o test-runner test-runner.go; \
	fi
	./test-runner

# Set up test environment
setup:
	@echo "🔧 Setting up test environment..."
	
	# Create test config directories
	mkdir -p test-configs/{api-gateway,prow,persys-agent,test-client,coredns,prometheus,grafana}
	
	# Create CoreDNS configuration
	@cat > test-configs/coredns/Corefile << 'EOF'
. {
    etcd test.local {
        stubzones
        path /skydns
        endpoint http://etcd:2379
    }
    forward . /etc/resolv.conf
    cache
    log
}
EOF
	
	# Create Prometheus configuration
	@cat > test-configs/prometheus/prometheus.yml << 'EOF'
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: 'api-gateway'
    static_configs:
      - targets: ['api-gateway:8085']
  
  - job_name: 'prow-scheduler'
    static_configs:
      - targets: ['prow-scheduler:8085']
  
  - job_name: 'persys-agent'
    static_configs:
      - targets: ['persys-agent:8080']
EOF
	
	# Create Grafana datasource configuration
	@mkdir -p test-configs/grafana/datasources
	@cat > test-configs/grafana/datasources/prometheus.yml << 'EOF'
apiVersion: 1

datasources:
  - name: Prometheus
    type: prometheus
    access: proxy
    url: http://prometheus:9090
    isDefault: true
EOF
	
	@echo "✅ Test environment setup complete!"

# Clean up test environment
clean:
	@echo "🧹 Cleaning up test environment..."
	
	# Stop and remove containers
	docker-compose -f docker-compose.test.yml down -v 2>/dev/null || true
	
	# Remove test artifacts
	rm -rf test-configs
	rm -f test-runner
	
	# Remove test containers and images
	docker ps -a --filter "name=persys-cloud-test" --format "{{.ID}}" | xargs -r docker rm -f
	docker images --filter "label=persys-cloud-test" --format "{{.ID}}" | xargs -r docker rmi -f
	
	@echo "✅ Cleanup complete!"

# Generate test certificates (for mTLS testing)
certs:
	@echo "🔐 Generating test certificates..."
	
	# Create certificate directory
	mkdir -p test-configs/certs
	
	# Generate CA certificate
	openssl req -x509 -newkey rsa:4096 -keyout test-configs/certs/ca-key.pem \
		-out test-configs/certs/ca-cert.pem -days 365 -nodes \
		-subj "/C=US/ST=Test/L=Test/O=Persys/OU=Test/CN=TestCA"
	
	# Generate server certificate
	openssl req -newkey rsa:4096 -keyout test-configs/certs/server-key.pem \
		-out test-configs/certs/server-req.pem -nodes \
		-subj "/C=US/ST=Test/L=Test/O=Persys/OU=Test/CN=localhost"
	
	openssl x509 -req -in test-configs/certs/server-req.pem \
		-CA test-configs/certs/ca-cert.pem -CAkey test-configs/certs/ca-key.pem \
		-CAcreateserial -out test-configs/certs/server-cert.pem -days 365
	
	# Generate client certificate
	openssl req -newkey rsa:4096 -keyout test-configs/certs/client-key.pem \
		-out test-configs/certs/client-req.pem -nodes \
		-subj "/C=US/ST=Test/L=Test/O=Persys/OU=Test/CN=test-client"
	
	openssl x509 -req -in test-configs/certs/client-req.pem \
		-CA test-configs/certs/ca-cert.pem -CAkey test-configs/certs/ca-key.pem \
		-CAcreateserial -out test-configs/certs/client-cert.pem -days 365
	
	@echo "✅ Test certificates generated!"

# Run specific test scenarios
test-basic:
	@echo "📋 Running basic workflow test..."
	API_GATEWAY_URL=$(API_GATEWAY_URL) PROW_SCHEDULER_URL=$(PROW_SCHEDULER_URL) PERSYS_AGENT_URL=$(PERSYS_AGENT_URL) ./test-suite.sh

test-auth:
	@echo "🔐 Running authentication test..."
	# Add authentication-specific tests here

test-reconciliation:
	@echo "🔄 Running reconciliation test..."
	# Add reconciliation-specific tests here

# Performance testing
test-performance:
	@echo "⚡ Running performance tests..."
	# Add performance tests here

# Load testing
test-load:
	@echo "📈 Running load tests..."
	# Add load tests here

# Security testing
test-security:
	@echo "🔒 Running security tests..."
	# Add security tests here

# Show test environment status
status:
	@echo "📊 Test Environment Status"
	@echo "=========================="
	@echo "Docker Compose services:"
	@docker-compose -f docker-compose.test.yml ps 2>/dev/null || echo "No services running"
	@echo ""
	@echo "Test configuration:"
	@echo "API Gateway URL: $(API_GATEWAY_URL)"
	@echo "Prow Scheduler URL: $(PROW_SCHEDULER_URL)"
	@echo "Persys Agent URL: $(PERSYS_AGENT_URL)" 