.PHONY: all build proto clean test test-unit test-e2e test-ci run docker-build docker-push install deps fmt lint

# Build variables
BINARY_NAME=persys-agent
VERSION?=1.0.0
BUILD_DIR=bin
DOCKER_IMAGE=persys/compute-agent
PROTO_DIR=api/proto
PKG_DIR=pkg/api/v1
CONTROL_PKG_DIR=pkg/control/v1
SHARED_PKG_DIR=../pkg/agent/api/v1
SHARED_CONTROL_PKG_DIR=../pkg/agent/control/v1
PROTOC_SYSTEM_INCLUDE?=/usr/include

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt

all: deps proto build

deps:
	@echo "==> Installing dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy

proto:
	@echo "==> Generating protobuf code..."
	@mkdir -p $(PKG_DIR)
	@mkdir -p $(CONTROL_PKG_DIR)
	@mkdir -p $(SHARED_PKG_DIR)
	@mkdir -p $(SHARED_CONTROL_PKG_DIR)
	cd api/proto && protoc -I. -I../../$(PROTO_DIR) -I$(PROTOC_SYSTEM_INCLUDE) --go_out=../../$(PKG_DIR) --go_opt=paths=source_relative \
		--go-grpc_out=../../$(PKG_DIR) --go-grpc_opt=paths=source_relative \
		agent.proto
	cd api/proto && protoc -I. -I../../$(PROTO_DIR) -I$(PROTOC_SYSTEM_INCLUDE) --go_out=../../$(CONTROL_PKG_DIR) --go_opt=paths=source_relative \
		--go-grpc_out=../../$(CONTROL_PKG_DIR) --go-grpc_opt=paths=source_relative \
		control.proto
	cd api/proto && protoc -I. -I../../$(PROTO_DIR) -I$(PROTOC_SYSTEM_INCLUDE) --go_out=../../$(SHARED_PKG_DIR) --go_opt=paths=source_relative \
		--go-grpc_out=../../$(SHARED_PKG_DIR) --go-grpc_opt=paths=source_relative \
		agent.proto
	cd api/proto && protoc -I. -I../../$(PROTO_DIR) -I$(PROTOC_SYSTEM_INCLUDE) --go_out=../../$(SHARED_CONTROL_PKG_DIR) --go_opt=paths=source_relative \
		--go-grpc_out=../../$(SHARED_CONTROL_PKG_DIR) --go-grpc_opt=paths=source_relative \
		control.proto

build:
	@echo "==> Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME) -v \
		-ldflags "-X main.version=$(VERSION)" \
		./cmd/agent

build-linux:
	@echo "==> Building $(BINARY_NAME) for Linux..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 -v \
		-ldflags "-X main.version=$(VERSION)" \
		./cmd/agent

install: build
	@echo "==> Installing $(BINARY_NAME)..."
	cp $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/

clean:
	@echo "==> Cleaning..."
	$(GOCLEAN)
	rm -rf $(BUILD_DIR)
	rm -rf $(PKG_DIR)
	rm -rf $(CONTROL_PKG_DIR)

test:
	@echo "==> Running all tests..."
	$(MAKE) test-unit
	$(MAKE) test-e2e

test-unit:
	@echo "==> Running unit tests..."
	$(GOTEST) -v -race -coverprofile=coverage.txt -covermode=atomic $$(go list ./... | grep -v '/test/e2e')

test-e2e:
	@echo "==> Running end-to-end tests..."
	$(GOTEST) -v ./test/e2e/...

test-ci:
	@echo "==> Running CI test suite..."
	$(MAKE) test-unit
	$(MAKE) test-e2e

test-integration:
	@echo "==> Running integration tests..."
	$(GOTEST) -v -tags=integration ./test/...

fmt:
	@echo "==> Formatting code..."
	$(GOFMT) ./...

lint:
	@echo "==> Running linters..."
	golangci-lint run

run: build
	@echo "==> Running $(BINARY_NAME)..."
	./$(BUILD_DIR)/$(BINARY_NAME)

docker-build:
	@echo "==> Building Docker image..."
	docker build -t $(DOCKER_IMAGE):$(VERSION) -t $(DOCKER_IMAGE):latest .

docker-push: docker-build
	@echo "==> Pushing Docker image..."
	docker push $(DOCKER_IMAGE):$(VERSION)
	docker push $(DOCKER_IMAGE):latest

# Development helpers
dev-setup:
	@echo "==> Setting up development environment..."
	@go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	@go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

dev-certs:
	@echo "==> Generating development certificates..."
	@mkdir -p dev/certs
	@cd dev/certs && \
		openssl req -x509 -newkey rsa:4096 -keyout ca.key -out ca.crt -days 365 -nodes \
			-subj "/CN=Persys CA" && \
		openssl req -newkey rsa:4096 -keyout agent.key -out agent.csr -nodes \
			-subj "/CN=persys-agent" && \
		openssl x509 -req -in agent.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
			-out agent.crt -days 365 -sha256
	@echo "Certificates generated in dev/certs/"

# Show help
help:
	@echo "Persys Compute Agent - Build Targets"
	@echo ""
	@echo "  make all           - Install deps, generate proto, and build"
	@echo "  make deps          - Install Go dependencies"
	@echo "  make proto         - Generate protobuf code"
	@echo "  make build         - Build the agent binary"
	@echo "  make build-linux   - Build for Linux (cross-compile)"
	@echo "  make install       - Install binary to /usr/local/bin"
	@echo "  make clean         - Clean build artifacts"
	@echo "  make test          - Run unit tests"
	@echo "  make fmt           - Format code"
	@echo "  make lint          - Run linters"
	@echo "  make run           - Build and run the agent"
	@echo "  make docker-build  - Build Docker image"
	@echo "  make docker-push   - Push Docker image"
	@echo "  make dev-setup     - Setup development tools"
	@echo "  make dev-certs     - Generate development certificates"
	@echo ""
