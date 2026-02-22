# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS builder
WORKDIR /app

# Copy go.mod and go.sum to download dependencies
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -o persys-forgery ./cmd/main.go

FROM alpine:3.19

WORKDIR /app

COPY --from=builder /app/persys-forgery /app/persys-forgery
COPY config.yaml /app/config.yaml

EXPOSE 8080

CMD ["/app/persys-forgery"] 