# persysctl examples

This directory contains ready-to-use examples for both scheduling styles:

- Legacy workload file mode: `workload schedule <file>`
- Spec-file mode: `workload schedule --type ... --id ... --spec-file ...`

## Legacy workload files

- `workload.json`
- `celery-worker.json`

Example:

```bash
./bin/persysctl --transport http workload schedule ./examples/workload.json
```

## Spec-file examples (recommended)

- `specs/container-spec.json`
- `specs/compose-spec.json`

Container:

```bash
./bin/persysctl --transport http workload schedule --type container --id demo-c1 --spec-file ./examples/specs/container-spec.json
```

Compose:

```bash
./bin/persysctl --transport http workload schedule --type compose --id demo-compose --spec-file ./examples/specs/compose-spec.json
```
