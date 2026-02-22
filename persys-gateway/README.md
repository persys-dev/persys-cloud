# persys-gateway

`persys-gateway` is the edge and cluster-routing API for Persys Compute.

## Responsibilities

- Public HTTP ingress.
- OAuth/session handling for GitHub login flow.
- GitHub webhook signature + replay validation.
- Multi-cluster scheduler pool routing.
- Proxy HTTP API calls to scheduler gRPC API.
- Forward forgery-related actions to forgery gRPC API.
- Enforce mTLS for internal calls.

## Non-Responsibilities

- Does not run build pipelines.
- Does not push images.
- Does not perform scheduler-side build actions.

## Ports

From `config.yaml`:
- mTLS API: `:8551`
- public webhook API: `:8585`

## Config

Primary config files:
- `config.yaml`
- `cluster.yaml` (scheduler clusters and routing)

Important sections:
- `tls`, `vault`
- `scheduler` + `core_dns`
- `webhook`
- `forgery.grpc_addr`, `forgery.grpc_server_name`

## Key Routes

Public:
- `POST /webhooks/github`

mTLS API:
- `GET /clusters`
- `POST /workloads/schedule`
- `GET /workloads`
- `GET /nodes`
- `GET /cluster/metrics`
- `POST /forgery/projects/upsert`
- `POST /forgery/builds/trigger`
- `POST /forgery/webhooks/test`

Cluster-scoped variants are under `/clusters/:cluster_id/...`.

## Run

```bash
cd persys-gateway
go run ./cmd
```

## Build

```bash
cd persys-gateway
go build ./cmd
```
