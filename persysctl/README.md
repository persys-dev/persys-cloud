# Persysctl Documentation

## Overview

`persysctl` is the command-line interface (CLI) for operating Persys services.
It is built with Go using Cobra (command parsing) and Viper (configuration).

`persysctl` now supports two transport modes:

- `http`: calls `persys-gateway` APIs (gateway then proxies to scheduler/forgery).
- `grpc`: connects directly to scheduler or compute-agent gRPC endpoints.

The CLI can manage workloads, nodes, cluster visibility, scheduler/agent operations, and forgery flows.

## Prerequisites

Before using `persysctl`, ensure:

- Linux/macOS/Windows with Go installed.
- Network access to your target endpoint:
  - gateway HTTP endpoint for `--transport http` (example: `https://localhost:8551`)
  - scheduler/agent gRPC endpoint for `--transport grpc` (example: `localhost:8085`)
- A valid CLI config file (default: `~/.persys/config.yaml`) or equivalent env vars.

## Installation

1. Clone repository and enter CLI directory:

```sh
git clone https://github.com/persys-dev/persys-cloud.git
cd persys-cloud/persysctl
```

2. Build:

```sh
go build -o ./bin/persysctl
```

3. Verify:

```sh
./bin/persysctl --help
```

## Initial Configuration

By default, `persysctl` reads `~/.persys/config.yaml`.

### Step 1: Create config directory

```sh
mkdir -p ~/.persys
```

### Step 2: Create config file

```yaml
api_endpoint: "https://localhost:8551"
api_version: "v2"
cluster_id: "persys-genesis-a"
transport: "http"
grpc_endpoint: "localhost:8085"
grpc_target: "scheduler"
rpc_timeout_seconds: 20

# mTLS certificate paths (used by both HTTP and gRPC TLS clients)
ca_cert_path: "/home/<user>/.persys/ca.pem"
cert_path: "/home/<user>/.persys/persysctl.pem"
key_path: "/home/<user>/.persys/persysctl-key.pem"

# Vault cert manager (enabled by default)
vault_enabled: true
vault_addr: "http://localhost:8200"
vault_auth_method: "approle" # token | approle
vault_token: ""              # required if auth_method=token
vault_approle_role_id: ""    # required if auth_method=approle
vault_approle_secret_id: ""  # required if auth_method=approle
vault_pki_mount: "pki"
vault_pki_role: "persysctl"
vault_cert_ttl: "24h"
vault_service_name: "persysctl"
vault_service_domain: "persys.local"
vault_retry_interval: "1m"
```

Notes:

- `api_endpoint` is used for HTTP mode.
- `grpc_endpoint` + `grpc_target` are used for gRPC mode.
- `grpc_target` can be `scheduler` or `agent`.
- `vault_enabled` defaults to `true`; persysctl expects mTLS-capable endpoints.
- If Vault is disabled, valid files must already exist at `ca_cert_path`, `cert_path`, and `key_path`.

You can also pass a custom config path:

```sh
./bin/persysctl --config /path/to/config.yaml
```

### Step 3: Environment variable overrides

Viper env overrides are supported (for example):

```sh
export PERSYS_API_ENDPOINT="https://localhost:8551"
export PERSYS_TRANSPORT="http"
export PERSYS_GRPC_ENDPOINT="localhost:8085"
export PERSYS_VAULT_ADDR="http://localhost:8200"
export PERSYS_VAULT_AUTH_METHOD="approle"
export PERSYS_VAULT_APPROLE_ROLE_ID="<role-id>"
export PERSYS_VAULT_APPROLE_SECRET_ID="<secret-id>"
```

### Step 4: Secure local config

```sh
chmod 600 ~/.persys/config.yaml
```

## Connecting with mTLS

`persysctl` uses mTLS by default unless explicitly running insecure gRPC mode.

### Certificate identity and SAN requirements

- Server certificates must include SANs matching the hostname/IP you dial.
- Common errors:
  - `x509: cannot validate certificate for <ip> because it doesn't contain any IP SANs`
  - `x509: certificate is not valid for any names, but wanted to match <hostname>`

### Client-side setup

- Use endpoint hostnames that match server cert SANs.
- Ensure CLI CA/cert/key paths are correct.
- Vault-backed client certificate manager is supported and used by default when enabled in config/env.

### Insecure testing mode

For local testing only, gRPC can run without TLS:

```sh
./bin/persysctl --transport grpc --grpc-insecure ...
```

Do not use insecure mode in production.

## Available Commands

Top-level command groups include:

- `workload`
- `node`
- `scheduler`
- `agent`
- `cluster`
- `metrics`
- `forgery`
- `automation`
- `ai`

Use:

```sh
./bin/persysctl <group> --help
./bin/persysctl <group> <command> --help
```

## Current Behavior by Transport

### HTTP transport (`--transport http`)

- Talks to `persys-gateway`.
- Gateway handles cluster routing and proxies scheduler/forgery APIs.
- Recommended for platform-level operations.

Common examples:

```sh
# Cluster routing state from gateway
./bin/persysctl --transport http cluster list

# Workloads and nodes via gateway->scheduler proxy
./bin/persysctl --transport http workload list
./bin/persysctl --transport http node list
```

### gRPC transport (`--transport grpc`)

- Direct gRPC connection to endpoint in `--grpc-endpoint`.
- Target service selected by `--grpc-target` (`scheduler` or `agent`).

Examples:

```sh
# Direct scheduler call
./bin/persysctl --transport grpc --grpc-target scheduler workload list

# Direct agent call (if endpoint is compute-agent)
./bin/persysctl --transport grpc --grpc-target agent workload list
```

## Workload Scheduling Notes

`workload schedule` supports two paths:

1. Legacy flag-based scheduling.
2. `--spec-file` scheduling (recommended for realistic specs).

With `--spec-file`:

- `--id` and `--type` are required.
- Accepted types: `docker-container`, `docker-compose`, `git-compose`, `container`, `compose`, `vm`.
- `--revision` and `--desired-state` control apply semantics.

Behavior differs by target:

- `grpc_target=scheduler`: sends scheduler apply request with translated spec.
- `grpc_target=agent` requires `--transport grpc` and sends agent apply request.

## Forgery Commands (via Gateway HTTP)

Forgery commands are available in HTTP mode and go through gateway to forgery gRPC:

- `forgery upsert-project --spec-file ...`
- `forgery trigger-build --spec-file ...`
- `forgery test-webhook --spec-file ...`

Examples:

```sh
./bin/persysctl --transport http forgery upsert-project --spec-file ./examples/forgery/project-upsert-spec.json
./bin/persysctl --transport http forgery trigger-build --spec-file ./examples/forgery/build-trigger-spec.json
./bin/persysctl --transport http forgery test-webhook --spec-file ./examples/forgery/test-webhook-spec.json
```

## Automation Commands (via Gateway HTTP)

Automation commands manage `persys-automation` policies (scale/schedule/offload/drift/redeploy)
through the gateway's gRPC proxy. Available only in `--transport http` mode.

- `automation create-policy --spec-file ...`
- `automation list-policies [--include-disabled]`
- `automation enable-policy <policy-id>`
- `automation disable-policy <policy-id>`
- `automation evaluate <policy-id>`
- `automation audit-log [--limit N]`

Policy spec file format (`--spec-file`):

```json
{
  "name": "scale-under-load",
  "target_workload": "checkout-service",
  "type": "scale",
  "condition_expression": "cpu_utilization > 0.8",
  "action_expression": "scale_replicas(+1)"
}
```

`type` accepts: `scale`, `schedule`, `offload`, `drift`, `redeploy`.

Examples:

```sh
./bin/persysctl --transport http automation create-policy --spec-file ./examples/automation/scale-policy-spec.json
./bin/persysctl --transport http automation list-policies --include-disabled
./bin/persysctl --transport http automation enable-policy <policy-id>
./bin/persysctl --transport http automation disable-policy <policy-id>
./bin/persysctl --transport http automation evaluate <policy-id>
./bin/persysctl --transport http automation audit-log --limit 50
```

## AI / Intelligence Commands (via Gateway HTTP)

AI commands talk to `persys-intelligence` through the gateway's HTTP proxy. Available
only in `--transport http` mode.

- `ai query "<question>"`
- `ai recommendations [--status pending|approved|rejected|applied] [--workload <name>]`
- `ai approve <recommendation-id>`
- `ai reject <recommendation-id>`
- `ai apply <recommendation-id>`

Examples:

```sh
./bin/persysctl --transport http ai query "why is checkout-service scaling up?"
./bin/persysctl --transport http ai recommendations --status pending
./bin/persysctl --transport http ai approve <recommendation-id>
./bin/persysctl --transport http ai reject <recommendation-id>
./bin/persysctl --transport http ai apply <recommendation-id>
```

## Gateway API Mapping (HTTP mode)

Representative routes used by CLI:

- `GET /clusters`
- `POST /workloads/schedule`
- `GET /workloads`
- `GET /nodes`
- `GET /cluster/metrics`
- `POST /forgery/projects/upsert`
- `POST /forgery/builds/trigger`
- `POST /forgery/webhooks/test`
- `POST /automation/policies`
- `GET /automation/policies`
- `POST /automation/policies/:id/enable`
- `POST /automation/policies/:id/disable`
- `POST /automation/policies/:id/evaluate`
- `GET /automation/audit-log`
- `POST /ai/query`
- `GET /ai/recommendations`
- `GET /ai/recommendations/pending`
- `POST /ai/recommendations/:id/approve`
- `POST /ai/recommendations/:id/reject`
- `POST /ai/recommendations/:id/apply`

## Troubleshooting

### Configuration errors

- Error: `failed to read config: ...`
- Fix: verify file path/format or pass `--config` explicitly.

### Connection errors

- Error: `failed to send request: ...` (HTTP)
- Fix: confirm `api_endpoint` and gateway availability.

- Error: `failed to connect to gRPC endpoint ...` (gRPC)
- Fix: verify endpoint, transport settings, TLS material, and SAN match.

### API errors

- Error: `API returned status ...`
- Fix: inspect returned JSON for upstream gateway/scheduler/forgery error details.

## Best Practices

- Use HTTP mode for normal operator workflows (cluster-aware routing).
- Use direct gRPC mode for low-level scheduler/agent debugging.
- Keep cert/key/CA files protected and rotate regularly.
- Use `--help` at command group level to track available flags and behavior.

## Additional Resources

- Root repository docs: `../README.md`
- Gateway docs: `../persys-gateway/README.md`
- Scheduler docs: `../persys-scheduler/README.md`
- Forgery docs: `../persys-forgery/README.md`
- Compute-agent docs: `../compute-agent/README.md`
