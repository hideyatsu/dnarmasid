# Migration Plan: Redis List → Asynq

## Overview

Migrate DnarMasID from simple Redis BRPOP pattern to Asynq for reliable job processing with retry, scheduling, and monitoring.

## Current State

```
┌─────────────┐    LPUSH     ┌─────────────┐    BRPOP    ┌─────────────┐
│  Scheduler  │ ──────────▶  │ Redis List  │ ◀────────── │   Worker    │
│  (cron)     │              │ job.scrape  │             │  (scraper)  │
└─────────────┘              └─────────────┘             └─────────────┘
```

**Problems:**
- Job lost if worker crashes mid-process
- No retry mechanism
- No scheduled jobs (relies on external cron)
- No visibility into queue state
- No dead letter queue for failed jobs

## Target State

```
┌─────────────┐   Enqueue    ┌─────────────┐   Process   ┌─────────────┐
│  Scheduler  │ ──────────▶  │   Asynq     │ ──────────▶ │   Worker    │
│  (Go app)   │              │   Broker    │             │  (handler)  │
└─────────────┘              └─────────────┘             └─────────────┘
                                   │
                                   ▼
                             ┌─────────────┐
                             │  Asynqmon   │  (Web UI)
                             │  :8080      │
                             └─────────────┘
```

## Migration Phases

### Phase 1: Add Asynq Infrastructure (Non-Breaking)

**Goal:** Add Asynq alongside existing Redis List, no behavior change.

**Tasks:**
1. Add `github.com/hibiken/asynq` dependency
2. Create `internal/queue/asynq.go` with client/server setup
3. Create task types in `internal/tasks/`
4. Add asynqmon container to docker-compose.yml
5. Add feature flag `USE_ASYNQ=false` (default off)

**Files to create:**
```
internal/
├── queue/
│   └── asynq.go          # Client + Server factory
├── tasks/
│   ├── types.go          # Task type constants
│   ├── scrape.go         # TypeScrape handler
│   ├── generate_ai.go    # TypeGenerateAI handler
│   ├── generate_media.go # TypeGenerateMedia handler
│   └── upload.go         # TypeUpload handler
```

**docker-compose.yml addition:**
```yaml
asynqmon:
  image: hibiken/asynqmon:latest
  container_name: dnarmasid-asynqmon
  ports:
    - "8080:8080"
  environment:
    REDIS_ADDR: redis:6379
  depends_on:
    - redis
  networks:
    - dnarmasid-net
```

### Phase 2: Dual-Write Mode

**Goal:** Write to both Redis List and Asynq, process from Redis List only.

**Tasks:**
1. Modify scheduler to enqueue to both systems
2. Monitor Asynq queue via asynqmon (jobs accumulate but not processed)
3. Verify task payloads are correct
4. Add metrics/logging for comparison

**Code change (scheduler):**
```go
// Dual-write: old + new
if err := redisClient.LPush(ctx, "job.scrape", payload).Err(); err != nil {
    log.Printf("Redis List push failed: %v", err)
}

if cfg.UseAsynq {
    task := asynq.NewTask(tasks.TypeScrape, payload)
    if _, err := asynqClient.Enqueue(task); err != nil {
        log.Printf("Asynq enqueue failed: %v", err)
    }
}
```

### Phase 3: Shadow Processing

**Goal:** Process from Asynq in shadow mode (log only, no side effects).

**Tasks:**
1. Enable Asynq server with shadow handlers
2. Shadow handlers log what they would do, but don't execute
3. Compare shadow logs with actual Redis List processing
4. Verify parity

### Phase 4: Cutover

**Goal:** Switch primary processing to Asynq.

**Tasks:**
1. Set `USE_ASYNQ=true`
2. Stop Redis List consumers
3. Monitor Asynq processing via asynqmon
4. Keep Redis List as fallback (read-only)

### Phase 5: Cleanup

**Goal:** Remove Redis List code.

**Tasks:**
1. Remove BRPOP consumers
2. Remove dual-write code
3. Remove feature flag
4. Update documentation

## Task Definitions

### TypeScrape
```go
const TypeScrape = "scrape"

type ScrapePayload struct {
    Source      string `json:"source"`
    TriggeredAt string `json:"triggered_at"`
}

func HandleScrapeTask(ctx context.Context, t *asynq.Task) error {
    var p ScrapePayload
    if err := json.Unmarshal(t.Payload(), &p); err != nil {
        return fmt.Errorf("unmarshal payload: %w", err)
    }
    // ... scrape logic
    return nil
}
```

### TypeGenerateAI
```go
const TypeGenerateAI = "generate:ai"

type GenerateAIPayload struct {
    PriceEventID string `json:"price_event_id"`
    Provider     string `json:"provider"`
    Model        string `json:"model"`
}
```

### TypeGenerateMedia
```go
const TypeGenerateMedia = "generate:media"

type GenerateMediaPayload struct {
    PriceEventID string `json:"price_event_id"`
    Template     string `json:"template"`
}
```

### TypeUpload
```go
const TypeUpload = "upload"

type UploadPayload struct {
    MediaID   string   `json:"media_id"`
    Platforms []string `json:"platforms"`
}
```

## Queue Configuration

```go
func NewServer(redisAddr string) *asynq.Server {
    return asynq.NewServer(
        asynq.RedisClientOpt{Addr: redisAddr},
        asynq.Config{
            Concurrency: 10,
            Queues: map[string]int{
                "critical": 6,  // scrape tasks
                "default":  3,  // generate tasks
                "low":      1,  // upload tasks
            },
            RetryDelayFunc: func(n int, e error, t *asynq.Task) time.Duration {
                return time.Duration(n) * time.Minute // 1m, 2m, 3m...
            },
        },
    )
}
```

## Retry Policy

| Task Type | Max Retry | Backoff | Dead Letter After |
|-----------|-----------|---------|-------------------|
| scrape | 3 | 1m, 2m, 3m | 3 failures |
| generate:ai | 5 | 30s, 1m, 2m, 4m, 8m | 5 failures |
| generate:media | 3 | 1m, 2m, 3m | 3 failures |
| upload | 5 | 1m, 2m, 4m, 8m, 16m | 5 failures |

## Scheduled Jobs (Cron)

Replace external cron with Asynq scheduler:

```go
scheduler := asynq.NewScheduler(redisOpt, nil)

// Morning scrape: 09:00 WIB
scheduler.Register("0 9 * * *", asynq.NewTask(tasks.TypeScrape, nil),
    asynq.Queue("critical"))

// Evening scrape: 18:00 WIB  
scheduler.Register("0 18 * * *", asynq.NewTask(tasks.TypeScrape, nil),
    asynq.Queue("critical"))

scheduler.Run()
```

## Monitoring

### Asynqmon Dashboard
- URL: http://localhost:8080
- Features:
  - Queue depth
  - Active/Pending/Scheduled/Retry/Dead counts
  - Task details and payloads
  - Manual retry/delete

### Metrics (Optional)
```go
// Prometheus metrics
asynq.NewServer(redisOpt, asynq.Config{
    // ...
}).Use(asynqprometheus.NewMiddleware())
```

## Rollback Plan

If issues arise after cutover:

1. Set `USE_ASYNQ=false`
2. Restart scheduler (will write to Redis List only)
3. Restart workers (will consume from Redis List)
4. Investigate Asynq issues via asynqmon

## Timeline Estimate

| Phase | Duration | Risk |
|-------|----------|------|
| Phase 1: Infrastructure | 1-2 days | Low |
| Phase 2: Dual-Write | 1 day | Low |
| Phase 3: Shadow | 2-3 days | Medium |
| Phase 4: Cutover | 1 day | Medium |
| Phase 5: Cleanup | 1 day | Low |

**Total: ~1 week**

## Dependencies

```go
require (
    github.com/hibiken/asynq v0.24.1
)
```

## Environment Variables

```env
# Feature flag
USE_ASYNQ=false

# Asynq config (optional, defaults shown)
ASYNQ_CONCURRENCY=10
ASYNQ_RETRY_MAX=3
```

## Testing Checklist

- [ ] Unit tests for task handlers
- [ ] Integration test: enqueue → process → verify
- [ ] Retry test: simulate failure, verify retry
- [ ] Dead letter test: exhaust retries, verify DLQ
- [ ] Scheduler test: verify cron triggers
- [ ] Asynqmon access test
- [ ] Rollback test: disable flag, verify Redis List works

---

**Author:** Chiper MK-2  
**Date:** 2026-05-22  
**Status:** Draft
