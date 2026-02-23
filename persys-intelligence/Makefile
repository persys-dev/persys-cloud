GO ?= go
BIN_DIR ?= bin

.PHONY: build test clean proto

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/persys-intelligence ./cmd/intelligence

test:
	$(GO) test ./...

proto:
	@echo "No protobuf definitions in persys-intelligence yet"

clean:
	rm -rf $(BIN_DIR)
