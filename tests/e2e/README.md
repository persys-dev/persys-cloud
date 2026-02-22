# tests/e2e

End-to-end tests for scheduler + compute-agent behavior.

## Scope

- Scheduler health and metrics endpoints.
- Agent health endpoint.
- Workload lifecycle through scheduler gRPC API:
  - apply
  - get/list
  - delete
- Metrics exposure sanity checks.

## Run (Docker)

```bash
cd tests/e2e
make test-docker
```

## Run (against existing local stack)

```bash
cd tests/e2e
SCHEDULER_METRICS_URL=http://localhost:8084 \
SCHEDULER_GRPC_ADDR=localhost:8085 \
AGENT_METRICS_URL=http://localhost:8080 \
go run test-runner.go
```
