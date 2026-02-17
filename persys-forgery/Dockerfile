# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o persys-forge ./cmd/main.go

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /app/persys-forge /app/persys-forge
COPY config.yaml /app/config.yaml
EXPOSE 8080
CMD ["/app/persys-forge"] 