# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.25
ARG VERSION=1.0.0

FROM golang:${GO_VERSION}-bookworm AS builder
WORKDIR /src

COPY pkg/go.mod pkg/go.sum ./pkg/
COPY compute-agent/go.mod compute-agent/go.sum ./compute-agent/

WORKDIR /src/compute-agent
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

WORKDIR /src
COPY pkg ./pkg
COPY compute-agent ./compute-agent

WORKDIR /src/compute-agent
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/persys-agent ./cmd/agent

# --- runtime stages unchanged from here down ---
FROM debian:bookworm-slim AS runtime-base
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates docker.io qemu-utils genisoimage libvirt-clients \
    && rm -rf /var/lib/apt/lists/*
RUN useradd -r -u 1000 -g 0 -m -s /usr/sbin/nologin persys && \
    mkdir -p /var/lib/persys /etc/persys/certs && \
    chown -R persys:root /var/lib/persys /etc/persys
COPY --from=builder /out/persys-agent /usr/local/bin/persys-agent
VOLUME ["/var/lib/persys", "/etc/persys"]
EXPOSE 50051 8080
USER persys
ENV PERSYS_STATE_PATH=/var/lib/persys/state.db \
    PERSYS_TLS_CERT=/etc/persys/certs/agent.crt \
    PERSYS_TLS_KEY=/etc/persys/certs/agent.key \
    PERSYS_TLS_CA=/etc/persys/certs/ca.crt \
    PERSYS_LOG_LEVEL=info
ENTRYPOINT ["/usr/local/bin/persys-agent"]

FROM runtime-base AS runtime