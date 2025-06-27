#!/bin/bash

# Check if certificates exist in the mounted volume
if [ ! -f /app/certs/ca.pem ] || [ ! -f /app/certs/ca-key.pem ]; then
    echo "Generating new certificates..."
    
    # Generate CA certificate
    cfssl gencert -initca ca-csr.json | cfssljson -bare /app/certs/ca
    
    # Generate CFSSL server certificate
    cfssl gencert -ca=/app/certs/ca.pem -ca-key=/app/certs/ca-key.pem \
        -config=ca-config.json -profile=server cfssl-csr.json | cfssljson -bare /app/certs/cfssl
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
    -tls-key=/app/certs/cfssl-key.pem \
    # -db-config=db-config.json