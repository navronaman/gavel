package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"gavel/pkg/models"
)

const blockTimeout = 2 * time.Second

// Consumer pulls Article values from a Redis Stream using consumer groups,
// providing backpressure: if downstream processing is slow the stream absorbs
// the spike without data loss.
type Consumer struct {
	RedisClient  *redis.Client
	StreamKey    string
	GroupName    string
	ConsumerName string
	BatchSize    int64
}

// NewConsumer constructs a Consumer. consumerName should be unique per
// instance (e.g. hostname) to avoid message contention within the group.
func NewConsumer(client *redis.Client, streamKey, groupName, consumerName string, batchSize int64) *Consumer {
	return &Consumer{
		RedisClient:  client,
		StreamKey:    streamKey,
		GroupName:    groupName,
		ConsumerName: consumerName,
		BatchSize:    batchSize,
	}
}

// Run creates the consumer group (idempotent) then blocks reading from the
// stream, sending Articles to out. It acknowledges each message only after a
// successful send. Returns when ctx is cancelled.
func (c *Consumer) Run(ctx context.Context, out chan<- models.Article) error {
	if err := c.ensureGroup(ctx); err != nil {
		return fmt.Errorf("consumer group setup: %w", err)
	}

	log.Printf("[consumer] %s/%s listening on %s", c.GroupName, c.ConsumerName, c.StreamKey)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		streams, err := c.RedisClient.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    c.GroupName,
			Consumer: c.ConsumerName,
			Streams:  []string{c.StreamKey, ">"},
			Count:    c.BatchSize,
			Block:    blockTimeout,
		}).Result()

		if err != nil {
			if errors.Is(err, redis.Nil) || isTimeout(err) {
				continue // no messages in this window
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("[consumer] read error: %v", err)
			continue
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				a, err := unmarshal(msg)
				if err != nil {
					log.Printf("[consumer] unmarshal %s: %v", msg.ID, err)
					c.ack(ctx, msg.ID)
					continue
				}

				select {
				case out <- a:
					c.ack(ctx, msg.ID)
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
}

func (c *Consumer) ensureGroup(ctx context.Context) error {
	err := c.RedisClient.XGroupCreateMkStream(ctx, c.StreamKey, c.GroupName, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

func (c *Consumer) ack(ctx context.Context, id string) {
	if err := c.RedisClient.XAck(ctx, c.StreamKey, c.GroupName, id).Err(); err != nil {
		log.Printf("[consumer] ack %s: %v", id, err)
	}
}

func unmarshal(msg redis.XMessage) (models.Article, error) {
	body, ok := msg.Values["body"].(string)
	if !ok {
		return models.Article{}, fmt.Errorf("missing body field")
	}
	var a models.Article
	if err := json.Unmarshal([]byte(body), &a); err != nil {
		return models.Article{}, err
	}
	return a, nil
}

func isTimeout(err error) bool {
	return strings.Contains(err.Error(), "i/o timeout") ||
		strings.Contains(err.Error(), "context deadline")
}
