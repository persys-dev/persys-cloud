# persys-forgery

`persys-forgery` is the internal build orchestrator service.

## Responsibilities

- Manage project definitions and build settings.
- Store GitHub credentials/secrets through Vault-integrated flow.
- Accept validated webhook events from gateway over gRPC.
- Decide build behavior and enqueue build work.
- Run build workers and publish pipeline/build status events.

## Transport and Exposure

- Internal only.
- gRPC + mTLS on `:8087`.
- No public HTTP ingress in runtime path.

## Dependencies

- MySQL for persistent project/build data.
- Redis for webhook/build/pipeline queues.
- Vault for certificate management and secret integration.

## Config

File: `config.yaml`

Important sections:

- `mysql_dsn`
- `redis` (`build_queue_key`, `webhook_queue_key`, `pipeline_status_queue`)
- `grpc.addr`
- `tls`
- `vault`

## gRPC Surface (ForgeryControl)

Includes:

- `UpsertProject`
- `GetProject` / `ListProjects` / `DeleteProject`
- `TriggerBuild`
- `ForwardWebhook`
- GitHub credential/repository/webhook helpers

## Run

```bash
cd persys-forgery
go run ./cmd/main.go
```

## Build

```bash
cd persys-forgery
go build ./cmd/main.go
```
