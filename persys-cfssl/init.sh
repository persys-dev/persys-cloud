#!/bin/bash

# Check if certificates exist in the mounted volume
if [ ! -f /app/certs/ca.pem ] || [ ! -f /app/certs/ca-key.pem ] || [ ! -f /app/certs/cfssl.pem ] || [ ! -f /app/certs/cfssl-key.pem ]; then
    echo "Generating new certificates..."
    # Generate CA and server certificates
    cfssl gencert -initca ca-csr.json | cfssljson -bare ca && \
    cfssl gencert -ca=ca.pem -ca-key=ca-key.pem -config=ca-config.json -profile=default cfssl-csr.json | cfssljson -bare cfssl && \
    mv ca.pem ca-key.pem cfssl.pem cfssl-key.pem /app/certs/
    echo "Certificates generated successfully"
else
    echo "Using existing certificates"
fi

# Start CFSSL server
exec cfssl serve \
    -address=0.0.0.0 \
    -port=8888 \
    -ca=/app/certs/ca.pem \
    -ca-key=/app/certs/ca-key.pem \
    -config=ca-config.json \
    -tls-cert=/app/certs/cfssl.pem \
    -tls-key=/app/certs/cfssl-key.pem 