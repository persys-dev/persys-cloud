#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   ./examples/client/scripts/encode-compose.sh examples/client/specs/compose.yaml
#   ./examples/client/scripts/encode-compose.sh examples/client/specs/compose.yaml examples/client/specs/compose-spec.json
#
# Output:
# - Prints base64 compose YAML to stdout.
# - If second arg is provided, updates compose_yaml in the target compose spec JSON.

if [[ $# -lt 1 || $# -gt 2 ]]; then
  echo "Usage: $0 <compose-yaml-path> [compose-spec-json-path]" >&2
  exit 1
fi

compose_yaml_path="$1"
compose_spec_json_path="${2:-}"

if [[ ! -f "$compose_yaml_path" ]]; then
  echo "Compose YAML not found: $compose_yaml_path" >&2
  exit 1
fi

encoded="$(base64 -w 0 "$compose_yaml_path")"
echo "$encoded"

if [[ -n "$compose_spec_json_path" ]]; then
  if [[ ! -f "$compose_spec_json_path" ]]; then
    echo "Compose spec JSON not found: $compose_spec_json_path" >&2
    exit 1
  fi

  if command -v jq >/dev/null 2>&1; then
    tmp="$(mktemp)"
    jq --arg value "$encoded" '.compose_yaml = $value' "$compose_spec_json_path" > "$tmp"
    mv "$tmp" "$compose_spec_json_path"
  else
    # Fallback without jq: replace first compose_yaml string value.
    # This expects a standard JSON field like: "compose_yaml": "..."
    escaped="$(printf '%s' "$encoded" | sed 's/[\/&]/\\&/g')"
    sed -Ei "0,/\"compose_yaml\"[[:space:]]*:[[:space:]]*\"[^\"]*\"/s//\"compose_yaml\": \"$escaped\"/" "$compose_spec_json_path"
  fi

  echo "Updated compose_yaml in: $compose_spec_json_path" >&2
fi

