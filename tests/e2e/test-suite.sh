#!/bin/sh

set -eu

SCHEDULER_METRICS_URL="${SCHEDULER_METRICS_URL:-http://localhost:8084}"
SCHEDULER_GRPC_ADDR="${SCHEDULER_GRPC_ADDR:-localhost:8085}"
AGENT_METRICS_URL="${AGENT_METRICS_URL:-http://compute-agent:8080}"
TEST_NODE_ID="${TEST_NODE_ID:-e2e-node-1}"
TEST_NODE_ENDPOINT="${TEST_NODE_ENDPOINT:-compute-agent:50051}"
TEST_WORKLOAD_ID="${TEST_WORKLOAD_ID:-e2e-workload-1}"
RETRY_INTERVAL="${RETRY_INTERVAL:-2}"
MAX_RETRIES="${MAX_RETRIES:-40}"

log() {
  printf '%s\n' "$1"
}

wait_for_http() {
  name="$1"
  url="$2"
  i=1
  while [ "$i" -le "$MAX_RETRIES" ]; do
    if curl -sSf "$url" >/dev/null 2>&1; then
      log "✅ $name ready: $url"
      return 0
    fi
    sleep "$RETRY_INTERVAL"
    i=$((i + 1))
  done
  log "❌ timeout waiting for $name: $url"
  return 1
}

run_smoke() {
  ./smoke-client -scheduler "$SCHEDULER_GRPC_ADDR" "$@"
}

run_smoke_capture() {
  ./smoke-client -scheduler "$SCHEDULER_GRPC_ADDR" "$@" 2>&1
}

assert_metric_present() {
  metric_name="$1"
  if ! curl -sSf "$SCHEDULER_METRICS_URL/metrics" | grep -q "$metric_name"; then
    log "❌ metric not found: $metric_name"
    return 1
  fi
  log "✅ metric found: $metric_name"
}

assert_contains() {
  haystack="$1"
  needle="$2"
  name="$3"
  if ! printf '%s' "$haystack" | grep -Fq "$needle"; then
    log "❌ assertion failed: $name (missing: $needle)"
    return 1
  fi
  log "✅ assertion passed: $name"
}

wait_workload_terminal() {
  desired_regex="$1"
  i=1
  while [ "$i" -le "$MAX_RETRIES" ]; do
    output="$(run_smoke_capture -op get-workload -workload-id "$TEST_WORKLOAD_ID" || true)"
    if printf '%s' "$output" | grep -Eq "$desired_regex"; then
      log "✅ workload reached expected state pattern: $desired_regex"
      return 0
    fi
    sleep "$RETRY_INTERVAL"
    i=$((i + 1))
  done
  log "❌ workload did not reach expected state pattern: $desired_regex"
  return 1
}

main() {
  log "Starting Persys Scheduler E2E suite (with compute-agent)"

  wait_for_http "scheduler health" "$SCHEDULER_METRICS_URL/health"
  wait_for_http "scheduler metrics" "$SCHEDULER_METRICS_URL/metrics"
  wait_for_http "compute-agent health" "$AGENT_METRICS_URL/health"

  log "Registering node and sending heartbeat"
  run_smoke -op register-node -node-id "$TEST_NODE_ID" -node-endpoint "$TEST_NODE_ENDPOINT" -supported-types "container,compose,vm"
  run_smoke -op heartbeat -node-id "$TEST_NODE_ID"
  list_nodes="$(run_smoke_capture -op list-nodes)"
  assert_contains "$list_nodes" "$TEST_NODE_ID" "node is listed"
  get_node="$(run_smoke_capture -op get-node -node-id "$TEST_NODE_ID")"
  assert_contains "$get_node" "$TEST_NODE_ENDPOINT" "node endpoint persisted"

  log "Applying workload and validating lifecycle visibility"
  run_smoke -op apply-container \
    -node-id "$TEST_NODE_ID" \
    -workload-id "$TEST_WORKLOAD_ID" \
    -container-image "busybox:latest" \
    -container-cmd "sh,-c,sleep 60" \
    -w-cpu 100 \
    -w-mem 128 \
    -w-disk 1

  wait_workload_terminal '"status": "(Running|Pending|Unknown)"'

  get_workload="$(run_smoke_capture -op get-workload -workload-id "$TEST_WORKLOAD_ID")"
  assert_contains "$get_workload" "$TEST_WORKLOAD_ID" "workload retrievable"
  list_workloads="$(run_smoke_capture -op list-workloads -filter-node-id "$TEST_NODE_ID")"
  assert_contains "$list_workloads" "$TEST_WORKLOAD_ID" "workload appears in list"
  cluster_summary="$(run_smoke_capture -op cluster-summary)"
  assert_contains "$cluster_summary" "\"totalNodes\": 1" "cluster summary node count"
  assert_contains "$cluster_summary" "\"totalWorkloads\": 1" "cluster summary workload count"

  log "Deleting workload and validating state transition"
  run_smoke -op delete-workload -workload-id "$TEST_WORKLOAD_ID"
  wait_workload_terminal '"status": "(Deleting|Deleted)"|not found'

  log "Validating exported scheduler metrics"
  assert_metric_present "persys_scheduler_grpc_server_requests_total"
  assert_metric_present "persys_scheduler_grpc_server_request_duration_seconds"
  assert_metric_present "persys_scheduler_agent_rpc_requests_total"
  assert_metric_present "persys_scheduler_agent_rpc_duration_seconds"
  assert_metric_present "persys_scheduler_nodes_status"
  assert_metric_present "persys_scheduler_workloads_status"
  assert_metric_present "persys_scheduler_reconciliation_results_total"

  log "E2E scheduler + compute-agent suite passed"
}

main
