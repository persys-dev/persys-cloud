# Stage 1: Build the application
FROM golang:1.25-alpine AS builder
WORKDIR /src

# go.mod/go.sum first for layer caching. Both are needed because
# persys-scheduler/go.mod does: replace github.com/persys-dev/persys-cloud/pkg => ../pkg
COPY pkg/go.mod pkg/go.sum ./pkg/
COPY persys-scheduler/go.mod persys-scheduler/go.sum ./persys-scheduler/

WORKDIR /src/persys-scheduler
RUN go mod download

WORKDIR /src
COPY pkg ./pkg
COPY persys-scheduler ./persys-scheduler

WORKDIR /src/persys-scheduler
RUN CGO_ENABLED=0 GOOS=linux go build -o persys-scheduler ./cmd/scheduler

# Stage 2: Create the final image
FROM alpine:latest
WORKDIR /root/
COPY --from=builder /src/persys-scheduler/persys-scheduler .
EXPOSE 8084
CMD ["./persys-scheduler"]