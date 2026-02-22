# vault-manager

`vault-manager` bootstraps Vault for local Persys environments.

## Responsibilities

- Initialize and unseal Vault (first run).
- Ensure PKI mounts/issuers and service roles exist.
- Ensure AppRole auth, policies, and role credentials for platform services.

## Run

```bash
cd vault-manager
go run ./main.go --vault-addr=http://localhost:8200
```

In docker compose, this is used by `vault-manager-setup` profile in `infra/docker/docker-compose.yml`.
