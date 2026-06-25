# Persys Go SDK

The Persys Go SDK is the reusable client library for Persys Cloud. It keeps `persysctl` thin by centralizing transport setup, certificate handling, manifest ingestion, and GitOps watching.

```go
c, err := sdk.New(sdk.DefaultOptions())
if err != nil {
    return err
}
defer c.Close()
```

## Packages

- `client`: core HTTP/gRPC client implementation.
- `options`: configuration options and defaults.
- `ingestion`: YAML, JSON, Docker Compose, and Git URL conversion helpers.
- `gitops`: local directory and remote repository watch loops.
- `types`: SDK-only non-protobuf data structures.

All protobuf request and response types are imported from `github.com/persys-dev/persys-cloud/pkg/scheduler/controlv1`.
