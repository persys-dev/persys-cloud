APP=persys-forgery
BINARY=bin/$(APP)
DOCKER_IMAGE=persys-forgery:latest

.PHONY: all build test docker-build docker-run clean

all: build

build:
	go build -o $(BINARY) ./cmd/main.go

test:
	go test -v ./tests/...

docker-build:
	docker build -t $(DOCKER_IMAGE) .

docker-run:
	docker run --rm -p 8080:8080 -v $(PWD)/config.yaml:/app/config.yaml $(DOCKER_IMAGE)

clean:
	rm -rf bin/ 