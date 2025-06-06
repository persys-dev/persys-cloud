FROM golang:1.23

# Install CFSSL
RUN go install github.com/cloudflare/cfssl/cmd/cfssl@latest && \
    go install github.com/cloudflare/cfssl/cmd/cfssljson@latest

WORKDIR /app
COPY config/ca-config.json .
COPY config/ca-csr.json .
COPY config/cfssl-csr.json .

# Create certs directory
RUN mkdir -p /app/certs

# Create init script
COPY init.sh /app/
RUN chmod +x /app/init.sh

EXPOSE 8888

# Use init script as entrypoint
ENTRYPOINT ["/app/init.sh"]