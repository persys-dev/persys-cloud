FROM golang:1.24-alpine AS build

# Install CFSSL
RUN apk add --no-cache git make && \
    go install github.com/cloudflare/cfssl/cmd/cfssl@latest && \
    go install github.com/cloudflare/cfssl/cmd/cfssljson@latest

FROM alpine:latest

# Install CFSSL
COPY --from=build /go/bin/cfssl /usr/local/bin/
COPY --from=build /go/bin/cfssljson /usr/local/bin/

# Create app directory
WORKDIR /app

# Copy configuration files
COPY config/ca-config.json .
COPY config/ca-csr.json .
COPY config/cfssl-csr.json .
COPY db-config.json .
COPY init.sh .

# Create certs directory and set permissions
RUN mkdir -p /app/certs && \
    chmod +x /app/init.sh

# Expose CFSSL port
EXPOSE 8888

# Use init script as entrypoint
ENTRYPOINT ["/bin/sh", "/app/init.sh"]