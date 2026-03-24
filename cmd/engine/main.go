package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"gavel/internal/fetcher"
	"gavel/pkg/models"
)

const (
	numWorkers   = 25
	resultsBuf   = 2000
	pollInterval = 5 * time.Minute
	reconnectWait = 5 * time.Second
	logInterval  = 10 * time.Second
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	results := make(chan models.Article, resultsBuf)

	pool := fetcher.NewWorkerPool(numWorkers, results)
	stream := fetcher.NewStreamReader(pool.ArticlePool, results)

	var wg sync.WaitGroup

	// Feed source URLs into the worker pool on a poll interval.
	wg.Add(1)
	go func() {
		defer wg.Done()
		feedJobs(ctx, pool.Jobs)
	}()

	// Start the RSS worker pool.
	wg.Add(1)
	go func() {
		defer wg.Done()
		pool.Start(ctx)
	}()

	// Start the Mastodon WebSocket firehose with automatic reconnect.
	wg.Add(1)
	go func() {
		defer wg.Done()
		runStream(ctx, stream)
	}()

	// Drain results — Phase 2 will hand these off to the Redis producer.
	drainResults(ctx, results)

	wg.Wait()
	log.Println("[engine] shutdown complete")
}

// feedJobs pushes every seed URL into the worker pool immediately, then
// re-pushes on every pollInterval tick.
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

// runStream connects to the Mastodon firehose and reconnects on any error.
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

// drainResults consumes the shared results channel, logging throughput every
// logInterval. In Phase 2 this will be replaced by the Redis producer.
func drainResults(ctx context.Context, results <-chan models.Article) {
	var total int64
	ticker := time.NewTicker(logInterval)
	defer ticker.Stop()

	for {
		select {
		case _, ok := <-results:
			if !ok {
				return
			}
			total++
		case <-ticker.C:
			log.Printf("[engine] articles ingested: %d", total)
		case <-ctx.Done():
			// Drain any buffered articles before exiting.
			for len(results) > 0 {
				<-results
				total++
			}
			log.Printf("[engine] shutdown. total articles ingested: %d", total)
			return
		}
	}
}
