# Persys Scheduler E2E Tests

This suite validates scheduler behavior against the real `compute-agent` submodule.

## Scope

The tests cover:

- Scheduler startup (`/health`, `/metrics`)
- Compute-agent startup (`/health`)
- Scheduler gRPC control API lifecycle through `cmd/smoke-client`
  - `RegisterNode`
  - `Heartbeat`
  - `ApplyWorkload` (container)
  - `GetWorkload`
  - `ListWorkloads`
  - `GetClusterSummary`
  - `DeleteWorkload`
- Scheduler Prometheus metrics exposure for gRPC, outbound agent RPC, reconciliation, node and workload state gauges

## Quick start

### Docker-based (recommended)

```bash
cd tests/e2e
make test-docker
```

This starts:

- `etcd`
- `compute-agent` (real runtime node service from `compute-agent/`)
- `persys-scheduler` (with `-insecure`)
- `test-client` (runs `test-suite.sh`)

### Script-only (against existing local scheduler)

```bash
cd tests/e2e
SCHEDULER_METRICS_URL=http://localhost:8084 \
SCHEDULER_GRPC_ADDR=localhost:8085 \
AGENT_METRICS_URL=http://localhost:8080 \
TEST_NODE_ENDPOINT=localhost:50051 \
./test-suite.sh
```

## Environment variables

- `SCHEDULER_METRICS_URL` (default `http://localhost:8084`)
- `SCHEDULER_GRPC_ADDR` (default `localhost:8085`)
- `AGENT_METRICS_URL` (default `http://compute-agent:8080`)
- `TEST_NODE_ID` (default `e2e-node-1`)
- `TEST_NODE_ENDPOINT` (default `compute-agent:50051`)
- `TEST_WORKLOAD_ID` (default `e2e-workload-1`)
- `RETRY_INTERVAL` (default `2`)
- `MAX_RETRIES` (default `40`)

## Notes

- Docker-based tests run `compute-agent` with `PERSYS_TLS_ENABLED=false` for plaintext gRPC compatibility with the scheduler E2E mode.
- The compose file mounts `/var/run/docker.sock` into `compute-agent` so container workloads can be applied during tests.
