package queue

import (
	"context"
	"encoding/json"
	"log"

	"github.com/redis/go-redis/v9"

	"gavel/pkg/models"
)

// Producer reads Article values from a channel and writes them to a Redis
// Stream via XADD. It decouples fast ingestion from slower AI processing.
type Producer struct {
	RedisClient *redis.Client
	StreamKey   string
}

// NewProducer constructs a Producer for the given Redis client and stream key.
func NewProducer(client *redis.Client, streamKey string) *Producer {
	return &Producer{
		RedisClient: client,
		StreamKey:   streamKey,
	}
}

// Run blocks, reading Articles from articles and publishing each to the Redis
// Stream. Serialization errors are logged and skipped. Returns when the input
// channel is closed or ctx is cancelled.
func (p *Producer) Run(ctx context.Context, articles <-chan models.Article) error {
	for {
		select {
		case a, ok := <-articles:
			if !ok {
				return nil
			}
			if err := p.publish(ctx, a); err != nil {
				log.Printf("[producer] publish error: %v", err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (p *Producer) publish(ctx context.Context, a models.Article) error {
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}

	return p.RedisClient.XAdd(ctx, &redis.XAddArgs{
		Stream: p.StreamKey,
		Values: map[string]any{"body": string(data)},
	}).Err()
}
