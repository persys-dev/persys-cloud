# Example Client Specs

These files are designed for `examples/client/main.go` and showcase all major fields for every workload type.

## Included files

Full specs:

- `container-spec.json`
- `compose-spec.json`
- `vm-spec.json`

Minimal specs:

- `container-spec-minimal.json`
- `compose-spec-minimal.json`
- `vm-spec-minimal.json`

Raw helper files:

- `compose.yaml`
- `compose-minimal.yaml`
- `cloud-init.yaml`
- `cloud-init-minimal.yaml`

Scripts:

- `../scripts/encode-compose.sh`
- `../scripts/quick-test.sh`

## How to run with full specs

Container:

```bash
go run ./examples/client \
  -insecure \
  -action apply \
  -type container \
  -id demo-container \
  -spec-file ./examples/client/specs/container-spec.json
```

Compose:

```bash
go run ./examples/client \
  -insecure \
  -action apply \
  -type compose \
  -id demo-compose \
  -spec-file ./examples/client/specs/compose-spec.json
```

VM:

```bash
go run ./examples/client \
  -insecure \
  -action apply \
  -type vm \
  -id demo-vm \
  -spec-file ./examples/client/specs/vm-spec.json
```

## How to run with minimal specs

Container:

```bash
go run ./examples/client \
  -insecure \
  -action apply \
  -type container \
  -id demo-container-min \
  -spec-file ./examples/client/specs/container-spec-minimal.json
```

Compose:

```bash
go run ./examples/client \
  -insecure \
  -action apply \
  -type compose \
  -id demo-compose-min \
  -spec-file ./examples/client/specs/compose-spec-minimal.json
```

VM:

```bash
go run ./examples/client \
  -insecure \
  -action apply \
  -type vm \
  -id demo-vm-min \
  -spec-file ./examples/client/specs/vm-spec-minimal.json
```

VM with raw cloud-init file:

```bash
go run ./examples/client \
  -insecure \
  -action apply \
  -type vm \
  -id demo-vm \
  -vm-cloud-init-file ./examples/client/specs/cloud-init.yaml
```

## Compose base64 helper

```bash
# Print base64 to stdout
./examples/client/scripts/encode-compose.sh ./examples/client/specs/compose.yaml

# Update compose-spec.json in place
./examples/client/scripts/encode-compose.sh \
  ./examples/client/specs/compose.yaml \
  ./examples/client/specs/compose-spec.json

# Update minimal compose spec from minimal yaml
./examples/client/scripts/encode-compose.sh \
  ./examples/client/specs/compose-minimal.yaml \
  ./examples/client/specs/compose-spec-minimal.json
```

## Quick smoke test

Run container + compose + VM quick cycle:

```bash
./examples/client/scripts/quick-test.sh --client-arg -insecure
```

Skip VM in environments without libvirt/KVM:

```bash
./examples/client/scripts/quick-test.sh --skip-vm --client-arg -insecure
```

Pass custom client connection args (repeat `--client-arg`):

```bash
./examples/client/scripts/quick-test.sh \
  --skip-vm \
  --client-arg -server \
  --client-arg localhost:50051 \
  --client-arg -insecure
```

## Notes

- `-spec-file` is type-specific and should contain the spec object only:
  - `container-spec*.json` maps to `ContainerSpec`
  - `compose-spec*.json` maps to `ComposeSpec`
  - `vm-spec*.json` maps to `VMSpec`
- The client still uses `-revision` and `-desired-state` from CLI flags for the top-level request.
- For compose, `compose_yaml` must be base64-encoded YAML content.
