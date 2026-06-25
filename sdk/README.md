# Persys Go SDK

The **Persys Go SDK** is the official Go client library for interacting with Persys Cloud.

It provides a unified interface for building Persys-aware tools, controllers, operators, automation systems, and internal services by handling the common platform concerns:

- API communication
    
- gRPC transport management
    
- TLS and certificate configuration
    
- authentication setup
    
- resource manifest ingestion
    
- GitOps synchronization
    
- Persys resource serialization
    

The SDK is designed to keep higher-level applications lightweight. Tools such as `persysctl`, automation agents, and external controllers use this package instead of implementing their own Persys API clients.

---

## Design Goals

The SDK follows a few core principles:

### Thin Clients

Applications using the SDK should focus on intent:

```go
client.Apply(ctx, manifest)
```

rather than managing:

- connections
    
- protobuf encoding
    
- certificates
    
- retries
    
- transport configuration
    

---

### Native Persys API Access

The SDK exposes Persys Cloud APIs directly using generated protobuf types.

All scheduler control API types are imported from:

```
github.com/persys-dev/persys-cloud/pkg/scheduler/controlv1
```

Example:

```go
import controlv1 "github.com/persys-dev/persys-cloud/pkg/scheduler/controlv1"
```

This keeps the SDK aligned with the Persys control plane API.

---

# Installation

```bash
go get github.com/persys-dev/persys-go-sdk
```

---

# Quick Start

Create a client using the default configuration:

```go
package main

import (
    "context"

    sdk "github.com/persys-dev/persys-go-sdk"
)

func main() {
    client, err := sdk.New(sdk.DefaultOptions())
    if err != nil {
        panic(err)
    }

    defer client.Close()

    ctx := context.Background()

    resources, err := client.List(ctx)
    if err != nil {
        panic(err)
    }

    _ = resources
}
```

The SDK automatically configures:

- API endpoints
    
- TLS
    
- certificates
    
- connection lifecycle
    
- authentication providers
    

---

# Architecture

The SDK is divided into focused packages:

```
persys-go-sdk
│
├── client
│   ├── API client
│   ├── gRPC transport
│   ├── authentication
│   └── connection lifecycle
│
├── options
│   ├── client configuration
│   ├── TLS options
│   ├── endpoints
│   └── defaults
│
├── ingestion
│   ├── YAML parser
│   ├── JSON parser
│   ├── Docker Compose converter
│   └── Git source loader
│
├── gitops
│   ├── repository watchers
│   ├── filesystem watchers
│   └── reconciliation loops
│
└── types
    └── SDK-specific models
```

---

# Packages

## client

The core SDK package.

Responsible for communication with the Persys control plane.

Provides:

- gRPC connection management
    
- API calls
    
- authentication
    
- certificate loading
    
- request handling
    

Example:

```go
client, err := sdk.New(opts)

vm, err := client.GetVM(
    ctx,
    "production-api",
)
```

---

## options

Contains SDK configuration.

Example:

```go
opts := sdk.Options{
    Endpoint: "scheduler.persys.local:443",
    TLS: sdk.TLSOptions{
        Enabled: true,
    },
}

client, err := sdk.New(opts)
```

Defaults can be loaded using:

```go
sdk.DefaultOptions()
```

---

## ingestion

Converts external configuration formats into Persys resources.

Supported inputs:

- YAML manifests
    
- JSON manifests
    
- Docker Compose files
    
- Git repositories
    

Example:

```go
resources, err := ingestion.FromYAML(
    data,
)
```

The ingestion layer allows Persys to accept existing deployment formats without requiring users to rewrite configuration.

---

## gitops

Provides GitOps synchronization primitives.

It watches:

- local directories
    
- remote repositories
    

and triggers reconciliation when changes occur.

Example:

```go
watcher := gitops.NewWatcher(
    repo,
    handler,
)

watcher.Start(ctx)
```

Typical usage:

```
Git repository
       |
       v
 GitOps watcher
       |
       v
 Manifest ingestion
       |
       v
 Persys API
       |
       v
 Scheduler reconciliation
```

---

# Certificates and Security

The SDK manages Persys cluster certificates and secure communication.

It supports:

- CA certificates
    
- client certificates
    
- mutual TLS
    
- secure gRPC connections
    

Applications should not manually create gRPC connections.

Instead:

```go
client, err := sdk.New(opts)
```

will configure the correct transport.

---

# Resource Flow

A typical SDK workflow:

```
User Input
    |
    v
Ingestion Layer
    |
    v
Persys Types
    |
    v
SDK Client
    |
    v
Scheduler API
    |
    v
Persys Control Plane
```

---

# Usage in Persys Components

The SDK is used by:

## persysctl

Command-line interface.

Responsibilities:

- user interaction
    
- command parsing
    
- output formatting
    

The SDK handles:

- API communication
    
- manifests
    
- authentication
    

---

## Controllers

External controllers can use the SDK to:

- watch resources
    
- create workloads
    
- update state
    
- reconcile desired state
    

---

## Automation

Automation services can use:

- GitOps integration
    
- ingestion
    
- API access
    

without implementing Persys protocols.

---

# Versioning

The SDK follows Persys API compatibility.

Changes to protobuf APIs may require corresponding SDK updates.

Recommended dependency:

```go
require (
    github.com/persys-dev/persys-go-sdk vX.Y.Z
)
```

---

# Closing Connections

SDK clients should always be closed:

```go
defer client.Close()
```

This releases:

- gRPC connections
    
- watchers
    
- background workers
    

---

# Philosophy

The Persys Go SDK exists to make Persys automation feel like a native Go platform.

Applications should describe **what they want**, while the SDK handles **how Persys communicates and reconciles it**.
