# persys-gateway

`persys-gateway` is the edge and cluster-routing API for Persys Compute.

## Responsibilities

- Public HTTP ingress.
- OAuth/session handling for GitHub login flow (managed deployments only — see Deployment Modes).
- GitHub webhook signature + replay validation.
- Multi-cluster scheduler pool routing, with automatic failover across scheduler replicas.
- Dynamic HTTP-to-gRPC bridging for cluster control (workloads/nodes) and forgery (CI/CD), via gRPC reflection with a compiled-in fallback — see Dynamic API Surface.
- Enforce mTLS for internal calls.

## Non-Responsibilities

- Does not run build pipelines.
- Does not push images.
- Does not perform scheduler-side build actions.

## Deployment Modes

Set via `deployment.mode` in `config.yaml` (or left unset):

- **`self-hosted`** (default) — no GitHub OAuth app required. `/auth/*` and
  `/github/*` routes aren't mounted at all. mTLS is the only trust
  boundary for cluster-control and forgery routes. No database is
  required — see Database below.
- **`managed`** — GitHub OAuth mounts, cluster-control/forgery routes
  require a verified user JWT (or mTLS), and a database is required at
  startup (fails fast if `database.dsn` is empty).

`GET /health` reports the active `deployment_mode` and `database_enabled`
so this is always visible at runtime, not just inferred from config.

## Database

Postgres, via `internal/store` — **optional in self-hosted mode**. Leave
`database.dsn` unset and the gateway runs with no database at all: the
only things that ever touch it (OAuth login/session storage, webhook
delivery audit trail) either aren't mounted in self-hosted mode or
degrade gracefully to in-memory-only behavior. Managed mode requires it.

Schema is three tables (`users`, `oauth_sessions`, `webhook_events`),
applied as idempotent `CREATE TABLE IF NOT EXISTS` on every startup —
no separate migration command. See `internal/store/schema.sql` for what
each table is for and what was deliberately *not* carried over from an
earlier MongoDB-based version.

## Ports

From `config.yaml`:
- mTLS API: `:8551`
- public webhook API: `:8585`
- debug/pprof: `:6060`

## Config

Primary config files:
- `config.yaml`
- `cluster.yaml` (scheduler clusters and routing)
- `catalog.yaml` (optional — see Dynamic API Surface; absence is normal)

Important sections:
- `deployment.mode` — see Deployment Modes
- `app.jwt_secret` — required in managed mode, auto-generated with a
  startup warning in self-hosted (won't survive a restart unless set)
- `database.dsn` — required in managed mode, optional in self-hosted
- `tls`, `vault`
- `scheduler` + `core_dns`
- `webhook`
- `forgery.grpc_addr`, `forgery.grpc_server_name`

Key environment variable overrides (see `config/config.go` for the full
list): `PERSYS_GATEWAY_CONFIG`, `PERSYS_GATEWAY_JWT_SECRET`,
`PERSYS_GATEWAY_POSTGRES_DSN`, `PERSYS_GATEWAY_CATALOG`.

## Dynamic API Surface

Cluster-control (workloads/nodes) and forgery (CI/CD) routes are not
hand-written per RPC. `internal/grpcbridge` discovers methods via gRPC
reflection against the live backend, falling back to the compiled-in
proto descriptor if the backend doesn't support reflection yet — so a
new RPC on either backend is reachable with zero gateway code changes,
and works against existing deployments unmodified either way.

The full, current list of stable paths is `internal/router/bindings.go`.
Anything not given a stable alias there is still callable at the generic
`/clusters/:cluster_id/rpc/<Service>/<Method>` path, and every method
(aliased or not) is listed at runtime:

```
GET /clusters/:cluster_id/rpc/_meta
GET /clusters/:cluster_id/forgery/rpc/_meta
```

## Key Routes

Public:
- `POST /webhooks/github`

mTLS API:
- `GET /health`
- `GET /clusters`
- `GET /clusters/:cluster_id`
- `POST /clusters/:cluster_id/workloads/schedule`
- `GET /clusters/:cluster_id/workloads`
- `GET /clusters/:cluster_id/nodes`
- `GET /clusters/:cluster_id/cluster/metrics`
- `POST /clusters/:cluster_id/forgery/projects/upsert`
- `POST /clusters/:cluster_id/forgery/builds/trigger`
- `POST /clusters/:cluster_id/forgery/webhooks/test`

Managed mode only:
- `GET /auth/login`
- `GET /auth/` (OAuth callback)
- `GET /github/list/repos`

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

After pulling dependency changes (e.g. the Postgres migration), run
`go mod tidy` once to settle `go.sum`.