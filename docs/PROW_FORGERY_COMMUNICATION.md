# Prow-Forgery Communication via Redis

## Architecture Overview

```
GitHub Webhook → API Gateway → Prow → Redis → Forgery
```

1. **API Gateway** receives GitHub webhooks and forwards them to Prow
2. **Prow** processes webhooks and queues build requests to Forgery via Redis
3. **Forgery** consumes build requests from Redis and executes builds

## JSON Contract

### Build Queue Message (Redis)

The message sent from Prow to Forgery via Redis follows this structure:

```json
{
  "id": "unique-build-id",
  "project_name": "org/repo",
  "type": "dockerfile|compose|pipeline",
  "source": "https://github.com/org/repo.git",
  "commit_hash": "abc123def456",
  "branch": "main",
  "strategy": "local|operator|prow",
  "push_artifact": true,
  "webhook_data": {
    "event_type": "push|pull_request|tag",
    "repository": "org/repo",
    "sender": "username",
    "ref": "refs/heads/main",
    "before": "old-commit",
    "after": "new-commit",
    "pull_request": {
      "number": 123,
      "title": "Feature PR",
      "state": "open"
    }
  },
  "metadata": {
    "triggered_by": "webhook",
    "correlation_id": "correlation-uuid",
    "processed_by": "prow"
  },
  "created_at": "2024-01-01T00:00:00Z"
}
```

### Field Descriptions

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | Yes | Unique build identifier |
| `project_name` | string | Yes | Repository name (org/repo format) |
| `type` | string | Yes | Build type: dockerfile, compose, pipeline |
| `source` | string | Yes | Git repository URL |
| `commit_hash` | string | Yes | Git commit hash to build |
| `branch` | string | No | Git branch name |
| `strategy` | string | Yes | Build strategy: local, operator, prow |
| `push_artifact` | boolean | No | Whether to push built artifacts |
| `webhook_data` | object | No | Original webhook information |
| `metadata` | object | No | Additional metadata for tracing |
| `created_at` | timestamp | No | Message creation timestamp |

## Implementation Components

### 1. Prow Build Service (`prow/internal/services/build_service.go`)

- Processes webhook payloads from API Gateway
- Creates build queue messages
- Sends messages to Redis queue `forge:builds`

### 2. Prow Webhook Handler (`prow/internal/api/webhook.go`)

- Receives webhooks from API Gateway
- Uses BuildService to process and queue builds
- Returns success/error responses

### 3. Forgery Redis Worker (`persys-forgery/internal/queue/worker.go`)

- Consumes messages from Redis queue `forge:builds`
- Unmarshals into `models.BuildRequest`
- Executes builds using the orchestrator

### 4. Enhanced BuildRequest Model (`persys-forgery/internal/models/build.go`)

- Extended to include webhook data and metadata
- Compatible with the Redis message format
- Supports traceability and correlation

## Redis Configuration

### Queue Details
- **Queue Name**: `forge:builds`
- **Direction**: Prow → Forgery (LPush → BLPop)
- **Message Format**: JSON string
- **Priority**: FIFO (First In, First Out)

### Redis Connection
```go
rdb := redis.NewClient(&redis.Options{
    Addr:     "localhost:6379",
    Password: "",
    DB:       0,
})
```

## Message Flow

1. **GitHub Event**: Push/Pull Request to repository
2. **API Gateway**: Receives webhook, forwards to Prow
3. **Prow**: 
   - Receives webhook at `/webhook/github`
   - Processes with `BuildService.ProcessWebhook()`
   - Creates `BuildQueueMessage`
   - Pushes to Redis: `LPush("forge:builds", messageJSON)`
4. **Forgery**:
   - Worker consumes: `BLPop("forge:builds")`
   - Unmarshals to `BuildRequest`
   - Executes build with `orchestrator.BuildWithStrategy()`

## Error Handling

- **Redis Connection**: Retry logic in worker
- **Message Parsing**: Log errors, continue processing
- **Build Failures**: Handled by Forgery orchestrator
- **Webhook Validation**: Required field checks in Prow

## Testing

Use the test script `test-redis-communication.go` to verify:
- Message serialization/deserialization
- Redis queue operations
- JSON contract compatibility

## Configuration

### Prow Configuration
```yaml
redis:
  addr: "localhost:6379"
  password: ""
  db: 0
```

### Forgery Configuration
```yaml
redis:
  addr: "localhost:6379"
  password: ""
  db: 0
```

## Security Considerations

- Webhook signature verification in API Gateway
- Redis authentication (if needed)
- Input validation in all components
- Correlation IDs for request tracing 