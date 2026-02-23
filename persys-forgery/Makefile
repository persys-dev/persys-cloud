APP=persys-forgery
BINARY=bin/$(APP)
DOCKER_IMAGE=persys-forgery:latest
PROTO_FILE=forgery.proto
PROTO_DIR=api/proto
INTERNAL_PROTO_OUT=internal/forgeryv1
SHARED_PROTO_OUT=../pkg/forgery/forgeryv1

.PHONY: all build test proto docker-build docker-run clean

all: proto build

build:
	go build -o $(BINARY) ./cmd/main.go

test:
	go test -v ./tests/...

proto:
	@mkdir -p $(INTERNAL_PROTO_OUT)
	@mkdir -p $(SHARED_PROTO_OUT)
	cd $(PROTO_DIR) && \
	protoc --go_out=paths=source_relative:../../$(INTERNAL_PROTO_OUT) --go-grpc_out=paths=source_relative:../../$(INTERNAL_PROTO_OUT) $(PROTO_FILE) && \
	protoc --go_out=paths=source_relative:../../$(SHARED_PROTO_OUT) --go-grpc_out=paths=source_relative:../../$(SHARED_PROTO_OUT) $(PROTO_FILE)

docker-build:
	docker build -t $(DOCKER_IMAGE) .

docker-run:
	docker run --rm -p 8080:8080 -v $(PWD)/config.yaml:/app/config.yaml $(DOCKER_IMAGE)

clean:
	rm -rf bin/ 
