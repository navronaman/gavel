package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"gavel/internal/fetcher"
	"gavel/internal/processor"
	"gavel/internal/queue"
	"gavel/pkg/models"
)

const (
	numWorkers    = 25
	resultsBuf    = 2000
	processedBuf  = 2000
	batchedBuf    = 500
	pollInterval  = 5 * time.Minute
	reconnectWait = 5 * time.Second
	streamKey     = "articles:raw"
	groupName     = "gavel-processors"
	consumerBatch = int64(50)
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// ── Infrastructure clients ─────────────────────────────────────────────────
	redisClient := mustRedis(ctx)
	pgPool := mustPostgres(ctx)
	defer pgPool.Close()

	aiClient, err := processor.NewAIClient(getenv("AI_SERVICE_GRPC", "localhost:50051"))
	if err != nil {
		log.Fatalf("[engine] ai client: %v", err)
	}
	defer aiClient.Close()

	// ── Channels ───────────────────────────────────────────────────────────────
	results := make(chan models.Article, resultsBuf)   // ingestion → Redis producer
	processed := make(chan models.Article, processedBuf) // Redis consumer → batcher
	batched := make(chan models.ProcessedArticle, batchedBuf) // batcher → db writer

	// ── Component wiring ───────────────────────────────────────────────────────
	pool := fetcher.NewWorkerPool(numWorkers, results)
	stream := fetcher.NewStreamReader(pool.ArticlePool, results)

	prod := queue.NewProducer(redisClient, streamKey)

	hostname, _ := os.Hostname()
	cons := queue.NewConsumer(redisClient, streamKey, groupName, hostname, consumerBatch)

	batcher := processor.NewBatcher(processed, aiClient)
	writer := processor.NewDBWriter(pgPool)

	var wg sync.WaitGroup

	// Phase 1 — ingestion
	wg.Add(1)
	go func() { defer wg.Done(); feedJobs(ctx, pool.Jobs) }()

	wg.Add(1)
	go func() { defer wg.Done(); pool.Start(ctx) }()

	wg.Add(1)
	go func() { defer wg.Done(); runStream(ctx, stream) }()

	// Phase 2 — Redis queue
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := prod.Run(ctx, results); err != nil && ctx.Err() == nil {
			log.Printf("[producer] exited: %v", err)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := cons.Run(ctx, processed); err != nil && ctx.Err() == nil {
			log.Printf("[consumer] exited: %v", err)
		}
	}()

	// Phase 4 — fan-out orchestrator
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := batcher.Run(ctx, batched); err != nil && ctx.Err() == nil {
			log.Printf("[batcher] exited: %v", err)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := writer.Run(ctx, batched); err != nil && ctx.Err() == nil {
			log.Printf("[db-writer] exited: %v", err)
		}
	}()

	wg.Wait()
	log.Println("[engine] shutdown complete")
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func mustRedis(ctx context.Context) *redis.Client {
	opt, err := redis.ParseURL(getenv("REDIS_URL", "redis://localhost:6379"))
	if err != nil {
		log.Fatalf("[engine] invalid REDIS_URL: %v", err)
	}
	client := redis.NewClient(opt)
	if err := client.Ping(ctx).Err(); err != nil {
		log.Fatalf("[engine] redis ping: %v", err)
	}
	return client
}

func mustPostgres(ctx context.Context) *pgxpool.Pool {
	pool, err := pgxpool.New(ctx, getenv("POSTGRES_URL", "postgres://policy:policy@localhost:5432/policydb"))
	if err != nil {
		log.Fatalf("[engine] postgres connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("[engine] postgres ping: %v", err)
	}
	return pool
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func feedJobs(ctx context.Context, jobs chan<- string) {
	send := func() {
		for _, url := range fetcher.SeedSources {
			select {
			case jobs <- url:
			case <-ctx.Done():
				return
			}
		}
	}
	send()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			send()
		case <-ctx.Done():
			return
		}
	}
}

func runStream(ctx context.Context, s *fetcher.StreamReader) {
	for {
		if err := s.Connect(ctx); err != nil {
			log.Printf("[stream] disconnected: %v", err)
		}
		select {
		case <-time.After(reconnectWait):
		case <-ctx.Done():
			return
		}
	}
}
