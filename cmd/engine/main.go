package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"gavel/internal/fetcher"
	"gavel/internal/queue"
	"gavel/pkg/models"
)

const (
	numWorkers    = 25
	resultsBuf    = 2000
	processedBuf  = 2000
	pollInterval  = 5 * time.Minute
	reconnectWait = 5 * time.Second
	logInterval   = 10 * time.Second
	streamKey     = "articles:raw"
	groupName     = "gavel-processors"
	consumerBatch = int64(50)
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	redisClient := newRedisClient()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatalf("[engine] redis ping: %v", err)
	}

	// Phase 1: ingestion → results channel
	results := make(chan models.Article, resultsBuf)
	pool := fetcher.NewWorkerPool(numWorkers, results)
	stream := fetcher.NewStreamReader(pool.ArticlePool, results)

	// Phase 2: results → Redis Stream → processed channel
	producer := queue.NewProducer(redisClient, streamKey)

	hostname, _ := os.Hostname()
	consumer := queue.NewConsumer(redisClient, streamKey, groupName, hostname, consumerBatch)
	processed := make(chan models.Article, processedBuf)

	var wg sync.WaitGroup

	// Source URL feeder
	wg.Add(1)
	go func() {
		defer wg.Done()
		feedJobs(ctx, pool.Jobs)
	}()

	// RSS worker pool
	wg.Add(1)
	go func() {
		defer wg.Done()
		pool.Start(ctx)
	}()

	// Mastodon WebSocket firehose
	wg.Add(1)
	go func() {
		defer wg.Done()
		runStream(ctx, stream)
	}()

	// Redis producer: ingestion → stream
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := producer.Run(ctx, results); err != nil && ctx.Err() == nil {
			log.Printf("[producer] exited: %v", err)
		}
	}()

	// Redis consumer: stream → processed channel
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := consumer.Run(ctx, processed); err != nil && ctx.Err() == nil {
			log.Printf("[consumer] exited: %v", err)
		}
	}()

	// Drain processed — Phase 3 will hand these to the AI batcher.
	drainProcessed(ctx, processed)

	wg.Wait()
	log.Println("[engine] shutdown complete")
}

func newRedisClient() *redis.Client {
	url := os.Getenv("REDIS_URL")
	if url == "" {
		url = "redis://localhost:6379"
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		log.Fatalf("[engine] invalid REDIS_URL: %v", err)
	}
	return redis.NewClient(opt)
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

// drainProcessed logs throughput. Phase 3 replaces this with the AI batcher.
func drainProcessed(ctx context.Context, processed <-chan models.Article) {
	var total int64
	ticker := time.NewTicker(logInterval)
	defer ticker.Stop()

	for {
		select {
		case _, ok := <-processed:
			if !ok {
				return
			}
			total++
		case <-ticker.C:
			log.Printf("[engine] articles processed: %d", total)
		case <-ctx.Done():
			for len(processed) > 0 {
				<-processed
				total++
			}
			log.Printf("[engine] shutdown. total processed: %d", total)
			return
		}
	}
}
