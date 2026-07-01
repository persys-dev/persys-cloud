# certmanager

`certmanager` is Persys Cloud's shared library for service TLS. Any
service that imports it gets automatic certificate issuance, on-disk
persistence, and background rotation backed by Vault's PKI engine —
without talking to Vault directly. Credentials are obtained from
`vault-manager`'s gRPC API rather than being baked into the service's own
config, so a service never needs to hold a long-lived AppRole secret.

## What it does

On `Start()`, the manager:

1. Checks for a valid, identity-matching certificate already on disk and
   reuses it if found (no unnecessary re-issuance on restart).
2. Otherwise, builds a Vault client, authenticates (token or AppRole —
   fetching AppRole credentials from `vault-manager` first if not already
   configured), and issues a fresh certificate from Vault's PKI
   `issue/<role>` endpoint.
3. Detects SANs automatically (service name, hostname, loopback, bind
   host, external IP, local interface IPs, and `service.domain` if a
   domain is configured) and includes them in the issuance request.
4. Validates the returned cert/key pair and CA chain, then writes all
   three files (cert, key, CA bundle) atomically to disk.
5. Starts a background rotation loop that re-issues the certificate at
   80% of its lifetime, with a 30-second floor so rotation never spins
   on an already-expired or zero-lifetime cert.

If Vault is unreachable on startup, the manager falls back to whatever
valid manual certificate already exists at the configured paths, and
retries Vault in the background (`recoveryLoop`) until issuance succeeds,
at which point it hands off to the normal rotation loop.

## Relationship to vault-manager

`certmanager` does not generate or store its own AppRole `secret_id`.
Instead, on every Vault client construction it calls out to
`vault-manager`'s gRPC API (`VaultManagerService`) at `VaultManagerAddr`:

- `GetServiceCredentials` — used for normal credential fetches.
- `RotateServiceSecretID` — same response shape, used when `rotate: true`
  is requested (currently always called with `rotate=false` internally;
  the rotate path is wired but not yet triggered anywhere in this file).

The returned `role_id` / `secret_id` are then used to log into Vault's
`auth/approle/login` endpoint to obtain a short-lived Vault client token,
which is what actually issues the certificate. This means `vault-manager`
must be reachable any time `certmanager` needs to talk to Vault — at
startup, during scheduled rotation, and during recovery polling.

## Configuration

`certmanager.Config` is populated by the embedding service, typically
from environment variables:

| Field                 | Purpose                                                              |
| ------------------------ | ------------------------------------------------------------------------ |
| `TLSEnabled`           | Master switch. If false, `Start()` is a no-op.                        |
| `VaultEnabled`         | If false, `Start()` is a no-op and manual certs are expected instead. |
| `ExternalIP`           | Optional IP SAN to include (e.g. public/floating IP).                  |
| `TLSCertPath`          | Where the issued/leaf certificate is written.                          |
| `TLSKeyPath`           | Where the private key is written.                                      |
| `TLSCAPath`            | Where the combined CA chain is written.                                |
| `VaultManagerAddr`     | `host:port` of `vault-manager`'s gRPC API (e.g. `vault-manager:50069`). |
| `VaultAddr`            | Vault API address.                                                     |
| `VaultAuthMethod`      | `token` or `approle`.                                                  |
| `VaultToken`           | Required if `VaultAuthMethod=token`.                                   |
| `VaultAppRoleID`       | AppRole `role_id`. Auto-populated from `vault-manager` if empty.        |
| `VaultAppSecretID`     | AppRole `secret_id`. Auto-populated from `vault-manager` if empty.      |
| `VaultPKIMount`        | PKI secrets engine mount (e.g. `pki`).                                 |
| `VaultPKIRole`         | PKI role to issue against (matches the service name in `vault-manager`).|
| `VaultCertTTL`         | Requested certificate TTL. Must be positive.                           |
| `VaultServiceName`     | Used as the certificate's common name and as the `vault-manager` lookup key. |
| `VaultServiceDomain`   | Optional domain suffix; adds `service.domain` and `host.domain` SANs.   |
| `VaultRetryInterval`   | Polling interval for the recovery loop. Must be positive.              |
| `BindHost`             | The address the service binds to; added as a SAN (IP or DNS).          |

## Usage

```go
cfg := certmanager.Config{
    TLSEnabled:         true,
    VaultEnabled:        true,
    TLSCertPath:         "/etc/persys/tls/tls.crt",
    TLSKeyPath:          "/etc/persys/tls/tls.key",
    TLSCAPath:           "/etc/persys/tls/ca.crt",
    VaultManagerAddr:    "vault-manager:50069",
    VaultAddr:           "https://vault:8200",
    VaultAuthMethod:     "approle",
    VaultPKIMount:       "pki",
    VaultPKIRole:        "persys-gateway",
    VaultCertTTL:        72 * time.Hour,
    VaultServiceName:    "persys-gateway",
    VaultServiceDomain:  "persys.local",
    VaultRetryInterval:  30 * time.Second,
    BindHost:            "0.0.0.0",
}

mgr := certmanager.NewManager(cfg, logger)
if err := mgr.Start(ctx); err != nil {
    log.Fatalf("cert manager failed to start: %v", err)
}
```

`Start` returns once the first certificate is in place (or once it has
fallen back to manual certs / entered the recovery loop); rotation and
recovery continue in background goroutines tied to `ctx`. Cancel `ctx` to
stop them on shutdown.

Once `Start` returns successfully, load the TLS files from
`TLSCertPath` / `TLSKeyPath` / `TLSCAPath` into your server's TLS config
as you normally would — `certmanager` only manages the files on disk, it
doesn't wrap your listener.

## Validation

`Validate()` (also called internally by `Start`) checks:

- `VaultManagerAddr` and `VaultAddr` are set.
- `VaultPKIMount` and `VaultPKIRole` are set.
- For `token` auth: `VaultToken` is set.
- For `approle` auth: `VaultAppRoleID`/`VaultAppSecretID` are set, or
  can be fetched from `vault-manager`.
- `VaultCertTTL` and `VaultRetryInterval` are positive durations.

Call it standalone during service startup/config validation if you want
to fail fast before `Start()` spins up any background loops.

## Known caveats in the current implementation

- `grpc.Dial` to `vault-manager` is unauthenticated/unencrypted
  (`grpc.WithInsecure()`); TLS for that connection is a known TODO.
- `newVaultClient` always calls `fetchCredentials(ctx, false)`
  unconditionally on every invocation — including for `token` auth, where
  the result is discarded — and ignores the error rather than failing
  fast, only logging a warning before falling through to the existing
  (possibly stale or empty) `VaultAppRoleID`/`VaultAppSecretID`. Fine
  today since `vault-manager` is currently the only source of AppRole
  credentials, but worth tightening if that assumption ever changes.
- `RotateServiceSecretID` is never actually invoked with `rotate=true`
  anywhere in this file — every internal call site passes `false`. Active
  rotation currently relies on `vault-manager` issuing a fresh
  `secret_id` on every `GetServiceCredentials` call rather than this
  package explicitly requesting rotation.