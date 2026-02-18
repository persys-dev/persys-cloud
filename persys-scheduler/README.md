# Persys Scheduler

`persys-scheduler` is the scheduler/control-plane service for Persys workloads.

## Responsibilities

- Accept node registration + heartbeat over gRPC.
- Persist node/workload state in etcd.
- Select nodes for workloads based on status, labels, capabilities, and available resources.
- Reconcile desired state (`Running`/`Stopped`/`Deleted`) with actual state.
- Track retries, assignments, reconciliation records, and events.

## API

Proto: `api/proto/control.proto`
Service: `persys.control.v1.AgentControl`

Implemented RPCs include:

- `RegisterNode`
- `Heartbeat`
- `ApplyWorkload`
- `DeleteWorkload`
- `RetryWorkload`
- `ListNodes`
- `GetNode`
- `ListWorkloads`
- `GetWorkload`
- `GetClusterSummary`

## Default ports

- Metrics + health HTTP: `:8084`
- gRPC control plane: `:8085`

Health endpoints:

- `GET /health`
- `GET /metrics`

## Local run

Start etcd first, then run scheduler.

```bash
./hack/start-etcd-docker.sh
cd persys-scheduler
go run ./cmd/scheduler -insecure
```

`-insecure` disables mTLS for local testing.

## Key environment variables

See `sample.env`.

Most used:

- `ETCD_ENDPOINTS` (default `localhost:2379`)
- `DOMAIN` (default `persys.local`)
- `GRPC_PORT` (default `8085`)
- `METRICS_PORT` (default `8084`)

For mTLS mode:

- `CA_FILE`
- `CFSSL_API_URL`
- `CERT_COMMON_NAME`
- `CERT_ORGANIZATION`

## Build and test

```bash
cd persys-scheduler
go build ./cmd/scheduler
go test ./...
```
