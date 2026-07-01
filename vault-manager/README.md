# vault-manager

`vault-manager` bootstraps Vault for Persys Cloud environments. It brings a
fresh Vault instance up to a usable state — initialized, unsealed, with a
full PKI chain and per-service AppRole credentials — and then stays running
as a gRPC service so other Persys Cloud components can fetch or rotate
their own credentials without touching Vault Environment Variables directly.

## Responsibilities

- **Initialize and unseal Vault** on first run (single key-share setup),
  or pick up an already-initialized Vault via `VAULT_ROOT_TOKEN`.
- **Provision the PKI chain**: mounts the root and intermediate PKI
  secrets engines, generates the root CA, signs and installs the
  intermediate CA, and configures default issuers.
- **Create per-service PKI roles** so each service can request leaf
  certificates scoped to its own name.
- **Enable AppRole auth** and create one AppRole per service, each bound
  to a least-privilege ACL policy (issue certs, read CA/CRL — nothing
  else).
- **Optionally hand off from the root token** (`--secure`): provisions a
  scoped bootstrap-manager AppRole, logs in as it, and revokes the root
  token so the rest of provisioning — and the live gRPC server — never
  hold root privileges.
- **Serve a gRPC API** (`VaultManagerService`) so services can fetch their
  AppRole credentials at runtime or rotate their `secret_id` without a
  human touching Vault.

## Project layout

```
vault-manager/
├── cmd/
│   └── vault-manager/
│       └── main.go            # entrypoint: flag parsing, bootstrap orchestration
└── internal/
    ├── config/                # defaults, CLI flags, shared logger
    ├── vaultclient/           # Vault client lifecycle: connect, init, unseal, secure handoff
    ├── pki/                   # PKI mounts, CA chain, per-service PKI roles
    ├── policy/                # ACL policies (per-service + bootstrap manager)
    ├── approle/                # AppRole auth, credential issuance/rotation
    ├── server/                # gRPC API: handlers, logging interceptor, server startup
    └── vaultmanagerv1/        # generated protobuf/gRPC code (VaultManagerService)
```

## Run

```bash
cd vault-manager
go run ./cmd/vault-manager --vault-addr=http://localhost:8200
```

On first run against an uninitialized Vault, the unseal key and root token
are printed to stdout — store them immediately, they are not recoverable
afterward. On subsequent runs against an already-initialized Vault, set
`VAULT_ROOT_TOKEN` in the environment instead:

```bash
VAULT_ROOT_TOKEN=hvs.xxxxx go run ./cmd/vault-manager --vault-addr=http://localhost:8200
```

To provision once with root and then drop root privileges for the life of
the process:

```bash
go run ./cmd/vault-manager --vault-addr=http://localhost:8200 --secure
```

## CLI flags

| Flag                | Default                                                                                                                                        | Description                                            |
| -------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------- |
| `--vault-addr`       | `https://vault:8200`                                                                                                                            | Vault API address                                       |
| `--pki-root-mount`   | `pki`                                                                                                                                            | Root PKI secrets engine mount path                       |
| `--pki-int-mount`    | `pki_int`                                                                                                                                        | Intermediate PKI secrets engine mount path                |
| `--root-cn`          | `Persys Cloud Root CA`                                                                                                                           | Common name for the root CA                              |
| `--intermediate-cn`  | `Persys Cloud Intermediate CA`                                                                                                                   | Common name for the intermediate CA                      |
| `--manager-role`     | `vault-manager-bootstrap`                                                                                                                        | AppRole name used for the `--secure` bootstrap handoff    |
| `--manager-policy`   | `vault-manager-bootstrap-policy`                                                                                                                 | ACL policy name for the bootstrap manager AppRole         |
| `--services`         | `persys-gateway,persys-scheduler,persysctl,compute-agent,persys-forgery,persys-services,persys-automation,persys-intelligence,persys-sdk`        | Comma-separated list of services to provision             |
| `--secure`           | `false`                                                                                                                                          | Provision a bootstrap AppRole and revoke the root token after setup |

## Environment variables

| Variable           | Required when                              | Description                                                  |
| ------------------- | --------------------------------------------- | ---------------------------------------------------------------- |
| `VAULT_ROOT_TOKEN`  | Vault is already initialized                  | Root token used for provisioning when no fresh init occurs        |

## What gets created in Vault

For each service in `--services` (default 9 platform services):

- A PKI role at `<pki-root-mount>/roles/<service>` (EC P-256 keys, 72h
  default TTL, 720h max TTL, any name allowed for internal cert issuance).
- An ACL policy named `<service>-policy`, granting:
  - `update` on `<pki-root-mount>/issue/<service>`
  - `read` on `<pki-root-mount>/cert/ca`, `cert/ca_chain`, and `crl`
- An AppRole at `auth/approle/role/<service>`, bound to that policy
  (1h token TTL, 4h max TTL, 24h secret_id TTL, unlimited secret_id uses).

If `--secure` is set, an additional bootstrap-manager AppRole and a
broader policy (mount/auth/policy management plus full PKI access) are
created, used once to hand off from the root token, then the root token
is revoked.

## gRPC API

`vault-manager` listens on `:50069` and exposes `VaultManagerService`
(defined in `internal/vaultmanagerv1`):

- **`GetServiceCredentials(service_name)`** — returns the service's
  current `role_id` and a freshly generated `secret_id`, plus an
  `expires_at` Unix timestamp (720h from issuance).
- **`RotateServiceSecretID(service_name)`** — identical behavior to
  `GetServiceCredentials`; every call mints a new `secret_id`, so calling
  either RPC rotates the credential. They're exposed as two RPCs for
  clarity of intent at the call site (initial fetch vs. explicit
  rotation), not because the underlying operation differs.

Every RPC is wrapped in a logging interceptor that emits structured JSON
logs (method, status code, duration, and any error) for each call.

## Docker Compose

In docker compose, this is used by the `vault-manager` profile in
`infra/docker/docker-compose.yml`.

## Operational notes

- Single key-share initialization (`secret_shares: 1`, `secret_threshold:
  1`) is intended for local/dev environments. Production Vault deployments
  should use Shamir's Secret Sharing with multiple key holders or
  auto-unseal via a cloud KMS instead.
- The printed root token and unseal key during first-run initialization
  are the only time they're surfaced — capture and store them securely
  immediately (e.g., in your team's secrets manager), since Vault does not
  let you retrieve them again.
- `secret_id_num_uses: 0` (unlimited) on service AppRoles means a leaked
  `secret_id` can be reused until its 24h TTL expires; rotate via the
  gRPC API or by re-running `vault-manager` if a leak is suspected.