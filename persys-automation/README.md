# persys-automation

Policy-driven automation service for Persys control-plane actions.

## Current implementation scope

This is the first implementation slice from `DESIGN_SPEC.md`:

- Deterministic policy engine
- gRPC API for policy CRUD/evaluation
- Periodic policy evaluation loop
- Postgres-backed persistent policy and audit store
- Postgres advisory-lock leader election (single active evaluator)
- Prometheus signal ingestion (`/api/v1/query`)
- Scheduler gRPC integration for policy actions
- Scheduler suggestion route integration (`SubmitAutomationSuggestion`)
- Audit log of policy evaluations and dispatch outcomes
- Shared `pkg/certmanager` integration for Vault-issued mTLS cert lifecycle

All mutations are performed through scheduler APIs only.

## Implemented gRPC API

`AutomationControl`:

- `CreatePolicy`
- `ListPolicies`
- `EnablePolicy`
- `DisablePolicy`
- `EvaluateNow`
- `ListAuditLog`

Proto: `api/proto/automation.proto`

## Policy contracts

### Condition expression (`condition_expression`)

Prometheus threshold:

```json
{"mode":"promql","query":"sum(rate(container_cpu_usage_seconds_total[5m]))","operator":">","threshold":75}
```

Cluster summary threshold:

```json
{"mode":"cluster_summary","field":"failed_workloads","operator":">=","threshold":3}
```

Cron schedule:

```json
{"mode":"cron","expression":"0 1 * * *"}
```

### Action expression (`action_expression`)

Set desired state:

```json
{"type":"set_desired_state","desired_state":"Running"}
```

Retry workload:

```json
{"type":"retry_workload"}
```

## Known gap (explicit)

Replica autoscaling is not yet dispatchable because current scheduler gRPC contract does not expose a native replica update operation. In this slice, `scale_replicas` evaluates but is intentionally not executed.

## Run

```bash
cd persys-automation
go run ./cmd/automation
```

## Key environment variables

- `AUTOMATION_GRPC_ADDR` (default `0.0.0.0`)
- `AUTOMATION_GRPC_PORT` (default `8091`)
- `AUTOMATION_METRICS_PORT` (default `8092`)
- `AUTOMATION_EVAL_INTERVAL` (default `30s`)
- `AUTOMATION_PROMETHEUS_URL` (default `http://localhost:9090`)
- `AUTOMATION_SCHEDULER_ADDR` (default `localhost:8085`)
- `AUTOMATION_SCHEDULER_TLS_ENABLED` (default `true`)
- `AUTOMATION_SCHEDULER_TLS_CA`
- `AUTOMATION_CLIENT_TLS_CERT`
- `AUTOMATION_CLIENT_TLS_KEY`
- `AUTOMATION_SERVER_TLS_ENABLED` (default `false`)
- `AUTOMATION_SERVER_TLS_CA`
- `AUTOMATION_SERVER_TLS_CERT`
- `AUTOMATION_SERVER_TLS_KEY`
- `AUTOMATION_VAULT_ENABLED` (default `true`)
- `AUTOMATION_VAULT_ADDR` (default `http://localhost:8200`)
- `AUTOMATION_VAULT_AUTH_METHOD` (`token|approle`)
- `AUTOMATION_VAULT_TOKEN`
- `AUTOMATION_VAULT_APPROLE_ROLE_ID`
- `AUTOMATION_VAULT_APPROLE_SECRET_ID`
- `AUTOMATION_VAULT_PKI_MOUNT` (default `pki`)
- `AUTOMATION_VAULT_PKI_ROLE` (default `persys-automation`)
- `AUTOMATION_VAULT_CERT_TTL` (default `24h`)
- `AUTOMATION_VAULT_SERVICE_NAME` (default `persys-automation`)
- `AUTOMATION_VAULT_SERVICE_DOMAIN`
- `AUTOMATION_VAULT_RETRY_INTERVAL` (default `1m`)
- `AUTOMATION_STORE_BACKEND` (`postgres` or `memory`, default `postgres`)
- `AUTOMATION_POSTGRES_DSN` (required for `postgres` backend)
- `AUTOMATION_LEADER_ELECTION_ENABLED` (default `true`)
- `AUTOMATION_LEADER_ELECTION_LOCK_ID` (default `771001`)
- `AUTOMATION_LEADER_ELECTION_POLL_INTERVAL` (default `5s`)
- `AUTOMATION_FORGERY_REDIS_ENABLED` (default `false`)
- `AUTOMATION_FORGERY_REDIS_ADDR` (default `localhost:6379`)
- `AUTOMATION_FORGERY_REDIS_PASSWORD`
- `AUTOMATION_FORGERY_REDIS_DB` (default `0`)
- `AUTOMATION_FORGERY_PIPELINE_KEY` (default `pipeline_status`)

## TLS + Vault requirements

When Vault-managed TLS is enabled and TLS is active (`AUTOMATION_SCHEDULER_TLS_ENABLED=true` or `AUTOMATION_SERVER_TLS_ENABLED=true`), client/server cert and CA paths must be unified:

- `AUTOMATION_CLIENT_TLS_CERT == AUTOMATION_SERVER_TLS_CERT`
- `AUTOMATION_CLIENT_TLS_KEY == AUTOMATION_SERVER_TLS_KEY`
- `AUTOMATION_SCHEDULER_TLS_CA == AUTOMATION_SERVER_TLS_CA`

## Next integration step

Add scheduler gRPC support for explicit workload replica updates, then wire `scale_replicas` dispatch in the automation action dispatcher.
Delete workload:

```json
{"type":"delete_workload"}
```

Scale replicas suggestion:

```json
{"type":"scale_replicas","desired_replicas":6,"replica_delta":2}
```

Actions are submitted as scheduler suggestions. Scheduler is authoritative and may reject any suggestion.
