package processor

import (
	"context"
	"log"
	"time"

	"gavel/pkg/models"
)

// Batcher collapses individual articles from the Redis consumer into batched
// gRPC calls. A batched BERT forward pass uses ~95% of available vector
// compute vs ~5% for single-article requests.
//
// Window: 50 ms. Batch cap: 100 articles. Whichever fires first triggers a
// flush, maximising both GPU utilisation and latency predictability.
type Batcher struct {
	WindowDuration time.Duration
	BatchSize      int
	Input          <-chan models.Article
	AIClient       *AIClient
}

// NewBatcher returns a Batcher with production defaults.
func NewBatcher(input <-chan models.Article, ai *AIClient) *Batcher {
	return &Batcher{
		WindowDuration: 50 * time.Millisecond,
		BatchSize:      100,
		Input:          input,
		AIClient:       ai,
	}
}

// Run accumulates articles and flushes to the AI service on every full batch
// or window expiry. Each ProcessedArticle emitted to out pairs the original
// Article with its AnalysisResult so the DBWriter can bulk-insert both tables.
// Handles partial batches on context cancellation.
func (b *Batcher) Run(ctx context.Context, out chan<- models.ProcessedArticle) error {
	buf := make([]models.Article, 0, b.BatchSize)
	timer := time.NewTimer(b.WindowDuration)
	defer timer.Stop()

	flush := func() {
		if len(buf) == 0 {
			return
		}
		results, err := b.AIClient.AnalyzeBatch(ctx, buf)
		if err != nil {
			log.Printf("[batcher] AnalyzeBatch error (dropping %d articles): %v", len(buf), err)
			buf = buf[:0]
			return
		}
		for i := range buf {
			if i >= len(results) {
				break
			}
			select {
			case out <- models.ProcessedArticle{Article: buf[i], Result: results[i]}:
			case <-ctx.Done():
				buf = buf[:0]
				return
			}
		}
		buf = buf[:0]
	}

	for {
		select {
		case a, ok := <-b.Input:
			if !ok {
				flush()
				return nil
			}
			buf = append(buf, a)
			if len(buf) >= b.BatchSize {
				drainTimer(timer)
				flush()
				timer.Reset(b.WindowDuration)
			}

		case <-timer.C:
			flush()
			timer.Reset(b.WindowDuration)

		case <-ctx.Done():
			flush()
			return ctx.Err()
		}
	}
}

// drainTimer safely stops a timer and drains its channel so it can be Reset.
func drainTimer(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}
