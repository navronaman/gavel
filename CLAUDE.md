# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Gavel is a high-throughput distributed data pipeline that ingests global news RSS feeds and a real-time WebSocket firehose, processes articles through a Python AI microservice, and stores structured policy intelligence in PostgreSQL. Go handles ingestion, routing, and orchestration; Python handles batched NLP via gRPC.

**Performance target:** 30,000+ events/sec per node.

## Commands

```bash
make proto    # Regenerate Go + Python gRPC stubs from proto/analysis.proto
make up       # Build and start all services via Docker Compose
make down     # Stop all services and remove volumes
make build    # Compile Go binary (cmd/engine)
make test     # Run all Go tests with race detector: go test ./... -race -count=1
make seed     # Seed test data via cmd/seed/main.go
make lint     # Run golangci-lint
```

**Run a single Go test:**
```bash
go test ./internal/fetcher/... -v -run TestWorkerPool
go test ./internal/queue/... -v -run TestProducer
go test ./internal/processor/... -v -run TestBatcher
```

## Architecture

The pipeline has four sequential phases:

### Phase 1: Concurrent Ingestion (Go — `internal/fetcher/`)
- `worker_pool.go`: Goroutine pool (N workers) polling RSS sources. Uses `sync.Pool` to reuse `*models.Article` allocations, eliminating GC pressure at 30k/sec. HTTP 429 triggers exponential backoff (max 3 retries).
- `stream.go`: Mastodon WebSocket firehose (`wss://mastodon.social/api/v1/streaming/public`) for push-based high-velocity simulation. Both sources feed the same `chan models.Article`.
- `rss_parser.go`: XML → Article struct, also reuses pool objects.
- `sources.go`: Registry of 50+ seed RSS URLs.

### Phase 2: Message Queue (Go + Redis — `internal/queue/`)
- `producer.go`: Reads from the articles channel, serializes to JSON, writes to Redis Stream (`XADD articles:raw`).
- `consumer.go`: Reads from Redis Stream using consumer groups (backpressure: if Python is slow, the queue absorbs spikes without data loss). Fans out to AI processing.

### Phase 3: Python AI Microservice (`ai_service/`)
- gRPC server on port 50051, FastAPI `/health` on port 8001.
- `AnalyzeBatch` RPC accepts up to 100 articles per call. Both models load as singletons at startup:
  - NER: `dslim/bert-base-NER`
  - Topic classification: `facebook/bart-large-mnli` (zero-shot, 6 policy topics)
  - Summarization: OpenAI `gpt-4o-mini` or `facebook/bart-large-cnn`
- Batching ~95% GPU utilization vs ~5% for single-article requests.

### Phase 4: Fan-Out Orchestrator (Go — `internal/processor/`)
- `batcher.go`: Request collapsing — accumulates up to 100 articles over a 50ms window, then fires one `AnalyzeBatch` gRPC call. Uses `time.NewTimer` (reset after each flush), handles partial batches on context cancellation.
- `ai_client.go`: gRPC client wrapping `proto.AnalysisServiceClient`.
- `db_writer.go`: Bulk INSERT into PostgreSQL using `pgx CopyFrom` (10–50x faster than individual INSERTs).

## Key Data Flow

```
RSS/WebSocket → WorkerPool → chan Article → Producer → Redis Stream
                                                              ↓
                                                         Consumer
                                                              ↓
                                                    Batcher (50ms/100-article window)
                                                              ↓
                                                    Python AnalyzeBatch (gRPC)
                                                              ↓
                                                    DBWriter → PostgreSQL
```

## gRPC / Protobuf

Schema lives in `proto/analysis.proto`. Generated stubs go to `proto/gen/` (Go) and `ai_service/gen/` (Python). Always run `make proto` after modifying the `.proto` file.

Key RPC: `AnalysisService.AnalyzeBatch(BatchRequest) → BatchResponse`

## Environment Variables

| Variable | Description |
|---|---|
| `REDIS_URL` | e.g. `redis://redis:6379` |
| `POSTGRES_URL` | e.g. `postgres://policy:policy@postgres:5432/policydb` |
| `AI_SERVICE_GRPC` | e.g. `ai-service:50051` |
| `OPENAI_API_KEY` | Required for gpt-4o-mini summarization |

Config managed via Viper (12-factor). Docker Compose sets these automatically; local dev needs a `.env` file.

## Database Schema

Two tables in `migrations/001_init.sql`:
- `raw_articles`: UUID PK, source_url, title, body, fetched_at
- `policy_events`: UUID PK, FK to raw_articles, entities (JSONB), topic, confidence, summary, analyzed_at

Migrations are auto-applied by Docker Compose via the `postgres` service init script mount.
