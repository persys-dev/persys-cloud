# persys-intelligence

`persys-intelligence` provides AI-assisted recommendation generation as an advisory layer for the Persys control plane.

It has two surfaces:
- Upstream advisory recommendations for automation
- Interactive read-only reasoning endpoint for `persysctl` via gateway

Implemented from `DESIGN_SPEC.md`:

- Sanitized feature snapshot input contract
- Strict typed LLM output validation
- Resilience controls (timeout, rate limit, circuit breaker)
- Recommendation lifecycle store (`pending|approved|rejected|applied`)
- HTTP APIs:
  - `POST /ai/query`
  - `POST /internal/evaluate`
  - `GET /recommendations`
  - `GET /recommendations/pending`
  - `POST /recommendations/{id}/approve`
  - `POST /recommendations/{id}/reject`
  - `POST /recommendations/{id}/apply`
- Metrics:
  - `recommendations_total`
  - `recommendations_applied_total`
  - `recommendations_rejected_total`
  - `inference_latency_seconds_sum`
  - `inference_latency_seconds_count`
  - `inference_failures`
- Audit fields on recommendations:
  - `reason_code`
  - `input_snapshot`
  - `prompt_hash`
  - `model_version`
  - `decision_outcome`

## Run

```bash
cd persys-intelligence
make build
./bin/persys-intelligence
```

## Main environment variables

- `INTELLIGENCE_HTTP_ADDR` (default `0.0.0.0`)
- `INTELLIGENCE_HTTP_PORT` (default `8093`)
- `INTELLIGENCE_METRICS_ADDR` (default `0.0.0.0`)
- `INTELLIGENCE_METRICS_PORT` (default `8094`)
- `INTELLIGENCE_MODE` (`advisory|policy-gated-auto|human-approval`)
- `INTELLIGENCE_MODEL_PROVIDER` (`mock|disabled|openai|local|fine-tuned`)
- `INTELLIGENCE_MODEL_ENDPOINT` (required for `openai|local|fine-tuned`)
- `INTELLIGENCE_MODEL_API_KEY` (optional, typically required for `openai`)
- `INTELLIGENCE_MODEL_NAME` (required for `openai|local|fine-tuned`)
- `INTELLIGENCE_INFERENCE_TIMEOUT`
- `INTELLIGENCE_INFERENCE_RATE_LIMIT_PER_SEC`
- `INTELLIGENCE_INFERENCE_FAILURE_THRESHOLD`
- `INTELLIGENCE_INFERENCE_COOLDOWN`
- `INTELLIGENCE_POLICY_MIN_CONFIDENCE`
- `INTELLIGENCE_POLICY_MAX_RISK`

TLS can be enabled with:

- `INTELLIGENCE_SERVER_TLS_ENABLED`
- `INTELLIGENCE_SERVER_TLS_CERT`
- `INTELLIGENCE_SERVER_TLS_KEY`

## Evaluate response

`POST /internal/evaluate` now returns:

- `generated`
- `recommendations`
- `inference_status` (`ok|partial|degraded|unavailable`)

## AI Query API

`POST /ai/query` request:

```json
{
  "query": "why is workload demo-c2 slow?",
  "context_scope": "workload",
  "resource_id": "demo-c2"
}
```

Response (structured and read-only):

```json
{
  "diagnosis": "Workload latency is likely driven by sustained CPU saturation.",
  "confidence": 0.81,
  "impact": "high",
  "evidence": ["CPU usage is 97% with increasing trend."],
  "recommended_actions": ["Scale workload replicas with policy gate checks."],
  "requires_human_approval": true,
  "insufficient_data": false,
  "inference_status": "ok",
  "state_snapshot": {}
}
```
