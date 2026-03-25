package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"gavel/pkg/models"
)

// DBWriter accumulates ProcessedArticles and bulk-inserts them into PostgreSQL
// using pgx CopyFrom — PostgreSQL's binary COPY protocol — which is 10–50x
// faster than individual INSERTs for high-volume workloads.
type DBWriter struct {
	DB            *pgxpool.Pool
	BatchSize     int
	FlushInterval time.Duration
}

// NewDBWriter returns a DBWriter with production defaults.
func NewDBWriter(db *pgxpool.Pool) *DBWriter {
	return &DBWriter{
		DB:            db,
		BatchSize:     500,
		FlushInterval: 500 * time.Millisecond,
	}
}

// Run accumulates ProcessedArticles from the batcher and flushes to Postgres
// on every BatchSize items or FlushInterval expiry.
func (w *DBWriter) Run(ctx context.Context, events <-chan models.ProcessedArticle) error {
	buf := make([]models.ProcessedArticle, 0, w.BatchSize)
	timer := time.NewTimer(w.FlushInterval)
	defer timer.Stop()

	flush := func() {
		if len(buf) == 0 {
			return
		}
		if err := w.bulkInsert(ctx, buf); err != nil {
			log.Printf("[db-writer] bulk insert error: %v", err)
		} else {
			log.Printf("[db-writer] inserted %d policy events", len(buf))
		}
		buf = buf[:0]
	}

	for {
		select {
		case item, ok := <-events:
			if !ok {
				flush()
				return nil
			}
			buf = append(buf, item)
			if len(buf) >= w.BatchSize {
				drainTimer(timer)
				flush()
				timer.Reset(w.FlushInterval)
			}

		case <-timer.C:
			flush()
			timer.Reset(w.FlushInterval)

		case <-ctx.Done():
			flush()
			return ctx.Err()
		}
	}
}

// bulkInsert pre-generates UUIDs in Go, then issues two CopyFrom calls:
// one for raw_articles and one for policy_events.
func (w *DBWriter) bulkInsert(ctx context.Context, items []models.ProcessedArticle) error {
	ids := make([]string, len(items))
	for i := range items {
		ids[i] = uuid.New().String()
	}

	// 1. raw_articles
	_, err := w.DB.CopyFrom(
		ctx,
		pgx.Identifier{"raw_articles"},
		[]string{"id", "source_url", "title", "body", "fetched_at"},
		pgx.CopyFromSlice(len(items), func(i int) ([]any, error) {
			a := items[i].Article
			return []any{ids[i], a.SourceURL, a.Title, a.Body, a.FetchedAt}, nil
		}),
	)
	if err != nil {
		return fmt.Errorf("copy raw_articles: %w", err)
	}

	// 2. policy_events — entities stored as JSONB
	_, err = w.DB.CopyFrom(
		ctx,
		pgx.Identifier{"policy_events"},
		[]string{"raw_article_id", "entities", "topic", "confidence", "summary", "analyzed_at"},
		pgx.CopyFromSlice(len(items), func(i int) ([]any, error) {
			r := items[i].Result
			entJSON, err := json.Marshal(r.Entities)
			if err != nil {
				entJSON = []byte("{}")
			}
			return []any{ids[i], entJSON, r.Topic, r.Confidence, r.Summary, time.Now().UTC()}, nil
		}),
	)
	if err != nil {
		return fmt.Errorf("copy policy_events: %w", err)
	}

	return nil
}
