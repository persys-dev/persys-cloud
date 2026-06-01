GO ?= go
BIN_DIR ?= bin
PROTO_FILE ?= automation.proto
PROTO_DIR ?= api/proto
INTERNAL_PROTO_OUT ?= internal/automationv1
SHARED_PROTO_OUT ?= ../pkg/automation/automationv1

.PHONY: build test clean proto

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/persys-automation ./cmd/automation

test:
	$(GO) test ./...

proto:
	@mkdir -p $(INTERNAL_PROTO_OUT)
	@mkdir -p $(SHARED_PROTO_OUT)
	cd $(PROTO_DIR) && \
	protoc --go_out=paths=source_relative:../../$(INTERNAL_PROTO_OUT) --go-grpc_out=paths=source_relative:../../$(INTERNAL_PROTO_OUT) $(PROTO_FILE) && \
	protoc --go_out=paths=source_relative:../../$(SHARED_PROTO_OUT) --go-grpc_out=paths=source_relative:../../$(SHARED_PROTO_OUT) $(PROTO_FILE)

clean:
	rm -rf $(BIN_DIR)
