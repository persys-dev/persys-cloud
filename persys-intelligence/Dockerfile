# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy go mod files first for caching
COPY go.mod ./
RUN go mod tidy

# Copy source
COPY . ./

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /bin/persys-intelligence ./cmd/intelligence

# Runtime stage
FROM alpine:latest

# Install CA certificates for TLS + Vault
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /bin/persys-intelligence /usr/local/bin/persys-intelligence

# Create non-root user
RUN adduser -D -u 1000 persys
USER persys

EXPOSE 8093 8094

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8094/health || exit 1

ENTRYPOINT ["persys-intelligence"]