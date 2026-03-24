# Overview

A high-throughput, concurrent data pipeline that ingests thousands of global news RSS feeds, legislative APIs, and a real-time WebSocket firehose in a streaming architecture. Go handles all ingestion, routing, and orchestration. A Python microservice handles AI-driven entity extraction and policy classification over **gRPC + Protobufs**. Designed to demonstrate distributed systems thinking, cross-language binary RPC, and production-grade engineering practices to top recruiters.

> **Recruiter Signal:** This project demonstrates a worker pool pattern, message queue decoupling, binary-serialized gRPC RPC, zero-alloc object pooling with `sync.Pool`, ML request collapsing, and async database writes — all in one coherent streaming system targeting **30,000 events/sec per node**.
> 

---

# Tech Stack

| Layer | Technology | Purpose |
| --- | --- | --- |
| Ingestion | Go (goroutines, channels, sync.Pool) | RSS polling + Mastodon WebSocket firehose; zero-alloc Article reuse |
| Buffer | Redis Streams | Decoupled queue, backpressure handling |
| RPC Layer | gRPC + Protobufs | 4–5x throughput vs HTTP/JSON; binary serialization |
| AI Service | Python 3.12 + gRPC | Batched NER + topic classification via HuggingFace |
| Data Store | PostgreSQL | Structured, analyzed policy intel |
| Container | Docker Compose | Single-command local dev environment |
| Config | `.env`  • Viper | 12-factor app config management |

---

# Performance Targets

| Optimization | Throughput Lift | P99 Latency Reduction | CPU/Mem Efficiency |
| --- | --- | --- | --- |
| `sync.Pool` (Zero-Alloc) | 2x – 3x | 80% (removes GC spikes) | Massive (low RAM churn) |
| Protobufs vs. JSON | 4x – 5x | 60% (faster SerDe) | High (lower network IO) |
| Lock-Free Ring Buffer | 1.5x – 2x | 40% (no mutex contention) | Medium |
| `io_uring` / epoll | 2x – 4x | 50% (fewer syscalls) | High (kernel efficiency) |
| **Combined Total** | **~15x – 25x** | **~90% lower jitter** | **~5x–10x denser** |

> **The Result:** A standard Go app struggles at 5k–10k events/sec on a single core. With these optimizations, the target is **30,000+ events/sec per node**.
> 

---

# Directory Structure

```
policy-intel-engine/
├── cmd/
│   └── engine/
│       └── main.go
├── internal/
│   ├── fetcher/
│   │   ├── worker_pool.go     # Goroutine pool, rate limiting, retries
│   │   ├── rss_parser.go      # XML RSS → Article struct (uses sync.Pool)
│   │   ├── stream.go          # Mastodon WebSocket firehose reader
│   │   └── sources.go         # Seed URL registry (50+ feeds)
│   ├── queue/
│   │   ├── producer.go        # Push raw articles to Redis Stream
│   │   └── consumer.go        # Pull from Redis, fan out to AI service
│   └── processor/
│       ├── ai_client.go       # gRPC client for Python AnalyzeBatch RPC
│       ├── batcher.go         # Request collapsing: 50ms window, 100-article batches
│       └── db_writer.go       # Bulk INSERT into PostgreSQL
├── pkg/
│   └── models/
│       └── models.go
├── proto/
│   ├── analysis.proto         # Protobuf schema
│   └── gen/                   # Auto-generated Go + Python stubs
├── ai_service/
│   ├── main.py
│   ├── nlp_model.py
│   └── requirements.txt
├── migrations/
│   └── 001_init.sql
├── docker-compose.yml
├── Makefile
└── PRD.md
```

---

# Data Model

```sql
CREATE TABLE raw_articles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_url  TEXT NOT NULL,
    title       TEXT,
    body        TEXT,
    fetched_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE policy_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    raw_article_id  UUID REFERENCES raw_articles(id),
    entities        JSONB,
    topic           TEXT,
    confidence      FLOAT,
    summary         TEXT,
    analyzed_at     TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_policy_events_topic ON policy_events(topic);
CREATE INDEX idx_policy_events_analyzed_at ON policy_events(analyzed_at DESC);
```

---

# Phase 1 — Concurrent Ingestion Engine (Go)

**Goal:** Goroutine pool + WebSocket firehose, both feeding the same channel. Zero-alloc via `sync.Pool`.

## Worker Pool with sync.Pool

```go
type WorkerPool struct {
    NumWorkers  int
    Jobs        chan string
    Results     chan models.Article
    ArticlePool *sync.Pool         // zero-alloc reuse
    HTTPTimeout time.Duration
}
// In each goroutine:
// article := wp.ArticlePool.Get().(*models.Article)
// article.Reset()
// ... populate ...
// wp.Results <- *article       // copy value into channel
// wp.ArticlePool.Put(article)  // return pointer to pool
```

> **Interview answer:** “sync.Pool reduces heap allocations to near-zero for hot-path objects. The GC never sees them, so P99 latency stays flat at 30k/sec — no stop-the-world spikes.”
> 

## Streaming Source: Mastodon Firehose

RSS is pull-based and poll-limited — it cannot sustain 30k events/sec. The Mastodon public streaming API (`wss://mastodon.social/api/v1/streaming/public`) provides a push-based WebSocket firehose. Use it to simulate high-velocity ingestion in development.

```go
type StreamReader struct {
    URL     string
    Pool    *sync.Pool
    Results chan<- models.Article
}
func (s *StreamReader) Connect(ctx context.Context) error
func (s *StreamReader) ReadLoop(ctx context.Context)
```

## Seed Sources

- Reuters RSS (world, politics)
- BBC World Service
- AP News
- UN Press Releases
- [GovTrack.us](http://GovTrack.us) bill activity
- State Department Press Releases
- Congressional Record RSS
- Al Jazeera English
- Deutsche Welle
- South China Morning Post
- **Mastodon public firehose (WebSocket — high-throughput simulation)**

## Claude Code Prompt for Phase 1

> *"Write `internal/fetcher/worker_pool.go` in Go. Implement a `WorkerPool` struct with `NumWorkers int`, `Jobs chan string`, `Results chan models.Article`, and `ArticlePool* sync.Pool`. The` Start(ctx context.Context)` method should launch N goroutines. Each goroutine gets an Article from the pool, populates it, sends a copy to Results, then returns the pointer to the pool. On HTTP 429, implement exponential backoff (max 3 retries). On context cancellation, drain and close gracefully. No global state."*
> 

---

# Phase 2 — Message Queue (Go + Redis)

**Goal:** Decouple fetching from processing via Redis Streams with consumer groups.

## Producer Contract

```go
type Producer struct {
    RedisClient *redis.Client
    StreamKey   string
}
func (p *Producer) Run(ctx context.Context, articles <-chan models.Article) error
```

## Consumer Contract

```go
type Consumer struct {
    RedisClient   *redis.Client
    StreamKey     string
    GroupName     string
    ConsumerName  string
    BatchSize     int64
}
func (c *Consumer) Run(ctx context.Context, out chan<- models.Article) error
```

## Claude Code Prompt for Phase 2

> *"Write `internal/queue/producer.go`. Implement a `Producer` that reads `models.Article` values from a Go channel and writes them to a Redis Stream using `XADD articles:raw*  body <json>`. Use the` go-redis/v9 `library. Handle serialization errors by logging and continuing. The` Run` method blocks until the input channel is closed or context is cancelled."*
> 

---

# Phase 3 — Python AI Microservice (gRPC + Batched Inference)

**Goal:** A gRPC service accepting batches of 100 articles. Protobufs = 4–5x throughput over HTTP/JSON. Batching = ~95% GPU utilization vs ~5% for single-article requests.

## Protobuf Schema (`proto/analysis.proto`)

```protobuf
syntax = "proto3";
package analysis;

message ArticleRequest {
  string text       = 1;
  string source_url = 2;
}
message AnalysisResult {
  map<string, StringList> entities = 1;
  string topic      = 2;
  float  confidence = 3;
  string summary    = 4;
}
message StringList { repeated string values = 1; }
message BatchRequest  { repeated ArticleRequest articles = 1; }
message BatchResponse { repeated AnalysisResult results  = 1; }

service AnalysisService {
  rpc AnalyzeBatch (BatchRequest) returns (BatchResponse);
}
```

Generate stubs: `protoc --go_out=proto/gen --python_out=ai_service/gen proto/analysis.proto`

## NLP Stack

- **NER:** `dslim/bert-base-NER` via HuggingFace
- **Topic Classification:** `facebook/bart-large-mnli` zero-shot
- **Topics:** Economic, Security, Healthcare, Environment, Elections, Diplomacy
- **Summarization:** OpenAI gpt-4o-mini OR `facebook/bart-large-cnn` locally
- **Batch size:** 100 articles per forward pass

## requirements.txt

```
fastapi==0.115.0
uvicorn[standard]==0.30.0
transformers==4.44.0
torch==2.4.0
pydantic==2.8.0
grpcio==1.65.0
grpcio-tools==1.65.0
openai==1.40.0
```

## Claude Code Prompt for Phase 3

> *"Write `ai_service/main.py`. Create a gRPC server implementing `AnalysisService` from `proto/analysis.proto`. Implement `AnalyzeBatch`: receives a `BatchRequest` of up to 100 articles, runs them through HuggingFace NER (`dslim/bert-base-NER`) and zero-shot classifier (`facebook/bart-large-mnli`) in a single batched forward pass, returns a `BatchResponse`. Load both models at startup as singletons. Expose a `/health` FastAPI endpoint on port 8001 alongside gRPC on port 50051."*
> 

---

# Phase 4 — Fan-Out Orchestrator (Go)

**Goal:** Consumers pull from Redis, collapse 100 articles per 50ms window into a single gRPC `BatchRequest`, then bulk-insert results into PostgreSQL.

## Batcher: Request Collapsing (`internal/processor/batcher.go`)

```go
// Batcher collapses N individual articles into 1 AnalyzeBatch RPC call.
// WindowDuration: 50ms. BatchSize: 100.
// This maximizes GPU utilization on the Python side.
type Batcher struct {
    WindowDuration time.Duration
    BatchSize      int
    Input          <-chan models.Article
    AIClient       *AIClient
}
func (b *Batcher) Run(ctx context.Context, out chan<- []models.AnalysisResult) error
```

## AI Client (gRPC)

```go
type AIClient struct {
    Conn   *grpc.ClientConn
    Client proto.AnalysisServiceClient
}
func NewAIClient(target string) (*AIClient, error)
func (c *AIClient) AnalyzeBatch(ctx context.Context, articles []models.Article) ([]models.AnalysisResult, error)
```

## DB Writer

```go
type DBWriter struct {
    DB            *pgxpool.Pool
    BatchSize     int
    FlushInterval time.Duration
}
func (w *DBWriter) Run(ctx context.Context, events <-chan models.PolicyEvent) error
```

## Claude Code Prompt for Phase 4

> *"Write `internal/processor/batcher.go`. Implement a `Batcher` that reads `models.Article` values from a channel. Accumulate for up to 50ms OR until 100 are collected, then call `AIClient.AnalyzeBatch`. Emit each `[]models.AnalysisResult` to an output channel. Handle partial batches on context cancellation. Use `time.NewTimer` for the window, resetting after each flush."*
> 

---

# docker-compose.yml

```yaml
version: '3.9'

services:
  go-engine:
    build: .
    depends_on: [redis, postgres, ai-service]
    environment:
      - REDIS_URL=redis://redis:6379
      - POSTGRES_URL=postgres://policy:policy@postgres:5432/policydb
      - AI_SERVICE_GRPC=ai-service:50051
    restart: on-failure

  ai-service:
    build: ./ai_service
    ports:
      - "50051:50051"   # gRPC
      - "8001:8001"     # /health
    environment:
      - OPENAI_API_KEY=${OPENAI_API_KEY}
    deploy:
      resources:
        reservations:
          memory: 2G

  redis:
    image: redis:7-alpine
    ports: ["6379:6379"]

  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: policy
      POSTGRES_PASSWORD: policy
      POSTGRES_DB: policydb
    ports: ["5432:5432"]
    volumes:
      - pgdata:/var/lib/postgresql/data
      - ./migrations:/docker-entrypoint-initdb.d

volumes:
  pgdata:
```

---

# Makefile

```makefile
.PHONY: proto up down build test seed lint

proto:
	protoc --go_out=proto/gen --python_out=ai_service/gen proto/analysis.proto

up:
	docker compose up --build -d

down:
	docker compose down -v

build:
	go build ./cmd/engine/...

test:
	go test ./... -race -count=1

seed:
	go run ./cmd/seed/main.go

lint:
	golangci-lint run ./...
```

---

# Stretch Goals (Post-MVP)

- **WebSocket Dashboard:** Go HTTP server + React frontend streaming new PolicyEvents in real-time.
- **Crisis Trigger Detection:** Rule engine that fires a webhook if same country + “Security” topic appears 5+ times in 10 minutes (Redis sliding window).
- **CLI Query Tool:** Go CLI (`cobra`) for querying policy_events and pretty-printing results.
- **Prometheus Metrics:** Expose `/metrics` — articles/sec, queue depth, gRPC latency p99, DB write latency.
- **Lock-Free Ring Buffer:** Replace Redis queue with an in-process lock-free ring buffer for ultra-low-latency scenarios (eliminates network hop).

---

# Recruiter Talking Points

- **Concurrency model:** "I used Go’s goroutine pool with a semaphore pattern to bound concurrent HTTP connections. For high-throughput simulation I added a Mastodon WebSocket firehose reader that feeds the same channel as the RSS workers."
- **Zero-alloc ingestion:** "At 30k/sec, allocating a new struct per event triggers constant GC. I used `sync.Pool` to reuse Article objects — allocations stay off the heap, GC pressure drops to near-zero, P99 stays flat."
- **Binary RPC:** "I replaced HTTP/JSON with gRPC + Protobufs. Protobufs are 4–5x faster to serialize than JSON and the payload is ~3x smaller. The `.proto` file also makes the interface between Go and Python explicit and versioned."
- **ML batching:** "Python’s BERT model was the slowest link. My Go Batcher collapses 100 articles into one `AnalyzeBatch` RPC every 50ms. A batched BERT forward pass uses ~95% of available vector compute vs ~5% for single-article requests."
- **Backpressure:** "Redis Streams with consumer groups decouple ingestion from AI processing. If Python is slow, the queue absorbs the spike without data loss."
- **Bulk writes:** "The DB writer uses `pgx CopyFrom` (PostgreSQL’s binary COPY protocol) — roughly 10–50x faster than individual INSERTs for high-volume workloads."