package fetcher

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"gavel/pkg/models"
)

const maxRetries = 3

// WorkerPool runs NumWorkers goroutines, each reading URLs from Jobs, fetching
// and parsing RSS feeds, and sending Article values to Results.
//
// ArticlePool reuses *Article allocations to keep GC pressure near-zero at
// high throughput. Goroutines get a pointer from the pool, populate it, send
// a VALUE copy to Results, then return the pointer to the pool.
type WorkerPool struct {
	NumWorkers  int
	Jobs        chan string
	Results     chan models.Article
	ArticlePool *sync.Pool
	HTTPTimeout time.Duration

	client *http.Client
}

// NewWorkerPool constructs a ready-to-use WorkerPool. Results is shared with
// the StreamReader so both ingestion paths write to the same channel.
func NewWorkerPool(numWorkers int, results chan models.Article) *WorkerPool {
	wp := &WorkerPool{
		NumWorkers:  numWorkers,
		Jobs:        make(chan string, numWorkers*2),
		Results:     results,
		HTTPTimeout: 15 * time.Second,
		ArticlePool: &sync.Pool{
			New: func() any { return &models.Article{} },
		},
	}
	wp.client = &http.Client{Timeout: wp.HTTPTimeout}
	return wp
}

// Start launches NumWorkers goroutines and blocks until all have exited (either
// ctx is cancelled or Jobs is closed).
func (wp *WorkerPool) Start(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < wp.NumWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wp.work(ctx)
		}()
	}
	wg.Wait()
}

func (wp *WorkerPool) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case url, ok := <-wp.Jobs:
			if !ok {
				return
			}
			wp.processURL(ctx, url)
		}
	}
}

func (wp *WorkerPool) processURL(ctx context.Context, url string) {
	body, err := wp.fetchWithRetry(ctx, url)
	if err != nil {
		log.Printf("[fetcher] %s: %v", url, err)
		return
	}
	defer body.Close()

	items, err := Parse(body, url)
	if err != nil {
		log.Printf("[fetcher] parse error %s: %v", url, err)
		return
	}

	for i := range items {
		a := wp.ArticlePool.Get().(*models.Article)
		a.Reset()
		a.SourceURL = items[i].SourceURL
		a.Title = items[i].Title
		a.Body = items[i].Body
		a.FetchedAt = items[i].FetchedAt

		select {
		case wp.Results <- *a: // send value copy; pool retains ownership of pointer
		case <-ctx.Done():
			wp.ArticlePool.Put(a)
			return
		}
		wp.ArticlePool.Put(a)
	}
}

// fetchWithRetry performs an HTTP GET with exponential backoff on HTTP 429.
func (wp *WorkerPool) fetchWithRetry(ctx context.Context, url string) (io.ReadCloser, error) {
	backoff := time.Second
	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "Gavel/1.0")

		resp, err := wp.client.Do(req)
		if err != nil {
			return nil, err
		}

		switch resp.StatusCode {
		case http.StatusOK:
			return resp.Body, nil
		case http.StatusTooManyRequests:
			resp.Body.Close()
			if attempt == maxRetries {
				return nil, fmt.Errorf("rate limited after %d retries: %s", maxRetries, url)
			}
			log.Printf("[fetcher] 429 on %s, retrying in %v", url, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			backoff *= 2
		default:
			resp.Body.Close()
			return nil, fmt.Errorf("status %d: %s", resp.StatusCode, url)
		}
	}
	return nil, fmt.Errorf("max retries exceeded: %s", url)
}
