.PHONY: help test test-docker test-script test-go clean

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
	@echo "  clean         - Clean up test environment"
	@echo ""

# Default test target
test: test-script

# Run tests using Docker Compose
test-docker:
	@echo "🐳 Running E2E tests with Docker Compose..."
	docker compose -f docker-compose.test.yml up --build --abort-on-container-exit --exit-code-from test-client
	docker compose -f docker-compose.test.yml down

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

# Clean up test environment
clean:
	@echo "🧹 Cleaning up test environment..."
	docker compose -f docker-compose.test.yml down -v 2>/dev/null || true
	rm -rf test-configs
	rm -f test-runner
	docker ps -a --filter "name=persys-cloud-test" --format "{{.ID}}" | xargs -r docker rm -f
	docker images --filter "label=persys-cloud-test" --format "{{.ID}}" | xargs -r docker rmi -f
	@echo "✅ Cleanup complete!" 