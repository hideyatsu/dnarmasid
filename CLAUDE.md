# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

DnarMasID is a **gold price automation platform** — a distributed Go microservices system that scrapes daily Antam gold prices, generates AI social media content, and sends notifications via Telegram.

All inter-service communication uses **Redis Queue** (LPUSH/BRPOP). No REST/gRPC between services.

## Core Commands

```bash
# Build & run everything
docker compose up --build

# With local AI (OLLAMA) — scraper, AI generator, and infra
docker compose -f docker-compose.yml -f docker-compose.ai.yml up --build

# Run a single service (after infra is up)
docker compose up mysql redis       # start infrastructure first
docker compose up scraper           # run one service

# Watch logs
docker compose logs -f ai-generator

# Trigger scrape manually
docker compose exec redis redis-cli LPUSH job.scrape '{"triggered_at":"manual","source":"dev"}'

# MySQL console
docker compose exec mysql mysql -u dnarmasid -psecret dnarmasid_db
```

## Architecture

```
Scheduler (cron)
  └─► job.scrape ───────────────────────────────────────────────────┐
       Scraper                                                        │
       ├─► gold.scraped.ai         ──► AI Generator ──► content.ready ─┤
       ├─► gold.scraped.media      ──► Media Generator ──► media.ready ┤
       └─► gold.scraped.telegram  ─────────────────────────────────────┤
                                                                         ▼
                                                              Telegram Bot ──► Admin
```

**Redis queue keys** (defined in `shared/queue/redis.go`):
- `job.scrape` — scheduler → scraper
- `gold.scraped.{ai,media,telegram}` — scraper fans out to 3 services
- `content.ready` — ai-generator → telegram-bot
- `media.ready` — media-generator → telegram-bot
- `scrape.failed` — scraper error reports

## Service Entry Points

Each service (`services/*/`) has its own `main.go` with graceful shutdown (SIGINT/SIGTERM) and a blocking Redis BRPOP event loop. To add a new queue consumer, follow this pattern.

## Key Shared Packages

| Package | Purpose |
|---|---|
| `shared/config/config.go` | Environment variable loader. All services use this. |
| `shared/models/models.go` | GORM models and event struct definitions (used across all services). |
| `shared/queue/redis.go` | Redis client + queue key constants. |
| `shared/db/mysql.go` | GORM MySQL connection (auto-migrates on startup). |

## Configuration

All config via `.env` — no hardcoded values. Timezone is hardcoded to `Asia/Jakarta` (WIB, UTC+7) in `config.go`. Supported AI providers: `ollama` or `gemini`.

## Known Issues

- **Scraper CSS selectors** in `services/scraper/antam.go` must be kept in sync with the current HTML structure of logammulia.com.
- **Twitter/X API** is paid ($100/mo) — caption is generated but posting is manual.
- **Video generation** is a placeholder (FFmpeg not yet implemented).

## Code Conventions

- Code comments in **Indonesian**.
- Failed pipeline stages publish to dedicated failure queues (e.g., `scrape.failed`) instead of crashing.
- GORM auto-migration runs on every service startup.
- Scraper has a dev fallback mode (`SCRAPE_MODE=dummy`) when the target site is unreachable.
