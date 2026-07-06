#!/usr/bin/env bash
set -euo pipefail

# Quick smoke test for example client + agent.
# - Applies container, compose, and (optionally) VM workloads using minimal specs.
# - Fetches status and action history.
# - Cleans up created workloads.
#
# Usage:
#   ./examples/client/scripts/quick-test.sh [--skip-vm] [--client-arg "..."]...
#
# Examples:
#   ./examples/client/scripts/quick-test.sh --client-arg -insecure
#   ./examples/client/scripts/quick-test.sh --skip-vm --client-arg -insecure
#   ./examples/client/scripts/quick-test.sh --client-arg -server --client-arg localhost:50051 --client-arg -insecure

skip_vm="false"
client_args=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-vm)
      skip_vm="true"
      shift
      ;;
    --client-arg)
      if [[ $# -lt 2 ]]; then
        echo "missing value for --client-arg" >&2
        exit 1
      fi
      client_args+=("$2")
      shift 2
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

if [[ ${#client_args[@]} -eq 0 ]]; then
  client_args=(-insecure)
fi

timestamp="$(date +%s)"
container_id="quick-container-${timestamp}"
compose_id="quick-compose-${timestamp}"
vm_id="quick-vm-${timestamp}"

cleanup_ids=("$container_id" "$compose_id")
if [[ "$skip_vm" != "true" ]]; then
  cleanup_ids+=("$vm_id")
fi

cleanup() {
  set +e
  for wid in "${cleanup_ids[@]}"; do
    go run ./examples/client -action delete -id "$wid" "${client_args[@]}" >/dev/null 2>&1 || true
  done
}
trap cleanup EXIT

run_client() {
  go run ./examples/client "$@" "${client_args[@]}"
}

echo "==> Health check"
run_client -action health

echo "==> Apply container: $container_id"
run_client -action apply -type container -id "$container_id" -spec-file ./examples/client/specs/container-spec-minimal.json -wait-timeout 90s
run_client -action status -id "$container_id"
run_client -action list-actions -id "$container_id"

echo "==> Apply compose: $compose_id"
run_client -action apply -type compose -id "$compose_id" -spec-file ./examples/client/specs/compose-spec-minimal.json -wait-timeout 90s
run_client -action status -id "$compose_id"
run_client -action list-actions -id "$compose_id"

if [[ "$skip_vm" != "true" ]]; then
  echo "==> Apply VM: $vm_id"
  run_client -action apply -type vm -id "$vm_id" -spec-file ./examples/client/specs/vm-spec-minimal.json -wait-timeout 240s
  run_client -action status -id "$vm_id"
  run_client -action list-actions -id "$vm_id"
fi

echo "==> Cleanup"
for wid in "${cleanup_ids[@]}"; do
  run_client -action delete -id "$wid"
done

echo "Quick test completed successfully."

