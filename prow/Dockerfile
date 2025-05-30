# Stage 1: Build the application
FROM golang:1.24-alpine AS builder

# Set the working directory
WORKDIR /app

# Copy go.mod and go.sum to download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the application code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o scheduler ./cmd/scheduler

# Stage 2: Create the final image
FROM alpine:latest

# Set the working directory
WORKDIR /root/

# Copy the binary from the builder stage
COPY --from=builder /app/scheduler .

# Expose the port the scheduler listens on
EXPOSE 8084

# Command to run the scheduler
CMD ["./scheduler"]