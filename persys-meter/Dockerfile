# syntax=docker/dockerfile:1.7

# --- build stage ---------------------------------------------------------
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
# CGO disabled: clickhouse-go's native protocol driver is pure Go, so a
# static binary is possible and preferable for a distroless runtime image.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/persys-meter ./cmd/meter

# --- runtime stage --------------------------------------------------------
# distroless static + nonroot: no shell, no package manager, runs as an
# unprivileged user by default - minimal attack surface for a service that
# handles usage/billing data.
FROM alpine:latest

COPY --from=builder /out/persys-meter /persys-meter

# 9091: health/readiness/Prometheus metrics (METER_HEALTH_ADDR)
# 9092: query API (METER_API_ADDR)
EXPOSE 9091 9092

ENTRYPOINT ["/persys-meter"]
