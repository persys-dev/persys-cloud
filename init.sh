#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export GOCACHE="${GOCACHE:-$ROOT_DIR/.cache/go-build}"
export GOMODCACHE="${GOMODCACHE:-$ROOT_DIR/.cache/go-mod}"

SERVICES=(
  "compute-agent"
  "persys-scheduler"
  "persysctl"
  "persys-gateway"
  "persys-federation"
  "persys-forgery"
  "persys-operator"
  "vault-mtls-mock"
)

SELECTED_SERVICES=("${SERVICES[@]}")
SKIP_DEPS=0
SKIP_CERTS=0
SKIP_BUILD=0

usage() {
  cat <<'EOF'
Usage: ./init.sh [options]

Options:
  --skip-deps                 Skip dependency download
  --skip-certs                Skip certificate generation
  --skip-build                Skip service build
  --services a,b,c            Limit operations to specific services
  -h, --help                  Show this help

Examples:
  ./init.sh
  ./init.sh --services persys-scheduler,persys-gateway
  ./init.sh --skip-build
EOF
}

contains_service() {
  local wanted="$1"
  local item
  for item in "${SERVICES[@]}"; do
    if [[ "$item" == "$wanted" ]]; then
      return 0
    fi
  done
  return 1
}

parse_services_arg() {
  local raw="$1"
  local parsed=()
  IFS=',' read -r -a parsed <<<"$raw"
  if [[ "${#parsed[@]}" -eq 0 ]]; then
    echo "No services were provided to --services" >&2
    exit 1
  fi

  SELECTED_SERVICES=()
  local service
  for service in "${parsed[@]}"; do
    if ! contains_service "$service"; then
      echo "Unknown service: $service" >&2
      echo "Allowed services: ${SERVICES[*]}" >&2
      exit 1
    fi
    SELECTED_SERVICES+=("$service")
  done
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-deps)
      SKIP_DEPS=1
      shift
      ;;
    --skip-certs)
      SKIP_CERTS=1
      shift
      ;;
    --skip-build)
      SKIP_BUILD=1
      shift
      ;;
    --services)
      [[ $# -lt 2 ]] && { echo "--services requires a value" >&2; exit 1; }
      parse_services_arg "$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if ! command -v go >/dev/null 2>&1; then
  echo "Go is required but was not found in PATH." >&2
  exit 1
fi

echo "==> Persys Cloud initialization"
echo "Root: $ROOT_DIR"
echo "Services: ${SELECTED_SERVICES[*]}"
echo "GOCACHE: $GOCACHE"
echo "GOMODCACHE: $GOMODCACHE"

mkdir -p "$GOCACHE" "$GOMODCACHE"

if [[ -d "$ROOT_DIR/.git" ]] && command -v git >/dev/null 2>&1; then
  echo "==> Syncing git submodules"
  git -C "$ROOT_DIR" submodule update --init --recursive
fi

if [[ "$SKIP_DEPS" -eq 0 ]]; then
  echo "==> Downloading dependencies"
  for service in "${SELECTED_SERVICES[@]}"; do
    echo "   - $service"
    (cd "$ROOT_DIR/$service" && go mod download)
  done
fi

if [[ "$SKIP_CERTS" -eq 0 ]]; then
  echo "==> Generating certificates"
  "$ROOT_DIR/generate-certs.sh"
fi

if [[ "$SKIP_BUILD" -eq 0 ]]; then
  echo "==> Building services"
  for service in "${SELECTED_SERVICES[@]}"; do
    make -C "$ROOT_DIR" --no-print-directory "build-$service"
  done
fi

echo "Initialization complete."
