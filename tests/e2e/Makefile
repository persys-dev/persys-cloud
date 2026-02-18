.PHONY: help test test-docker test-script test-go clean

help:
	@echo "Persys Scheduler E2E"
	@echo "===================="
	@echo "test         - Run docker-compose based scheduler + compute-agent lifecycle suite"
	@echo "test-docker  - Same as test"
	@echo "test-script  - Run shell lifecycle suite against locally-running scheduler/agent"
	@echo "test-go      - Run Go health/metrics probe against locally-running scheduler"
	@echo "clean        - Tear down compose test stack"

test: test-docker

test-docker:
	docker compose -f docker-compose.test.yml up --build --abort-on-container-exit --exit-code-from test-client
	docker compose -f docker-compose.test.yml down

test-script:
	chmod +x test-suite.sh
	./test-suite.sh

test-go:
	go run test-runner.go

clean:
	docker compose -f docker-compose.test.yml down -v 2>/dev/null || true
