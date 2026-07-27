BINARY      := persys-meter
CMD         := ./cmd/meter
BIN_DIR     := bin
IMAGE       ?= persys-dev/persys-meter
TAG         ?= latest
GO          ?= go

.PHONY: all
all: build

.PHONY: build
build: ## Build the persys-meter binary into bin/
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY) $(CMD)

.PHONY: run
run: ## Run persys-meter directly with `go run` (uses env vars / config defaults)
	$(GO) run $(CMD)

.PHONY: test
test: ## Run the test suite with race detection and coverage
	$(GO) test ./... -race -cover

.PHONY: tidy
tidy: ## Resolve/verify module dependencies and generate go.sum
	$(GO) mod tidy

.PHONY: fmt
fmt: ## Format all Go source
	gofmt -l -w .

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: lint
lint: fmt vet ## Format + vet (add golangci-lint here if/when it's adopted repo-wide)

.PHONY: docker
docker: ## Build the container image
	docker build -t $(IMAGE):$(TAG) .

.PHONY: docker-push
docker-push: docker ## Build and push the container image
	docker push $(IMAGE):$(TAG)

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := build
