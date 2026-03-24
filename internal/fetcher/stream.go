package fetcher

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"gavel/pkg/models"
)

const mastodonPublicStream = "wss://mastodon.social/api/v1/streaming/public"

// StreamReader connects to the Mastodon public WebSocket firehose and emits
// Article values to the shared Results channel. It reuses the same sync.Pool
// as WorkerPool to keep allocations off the heap.
type StreamReader struct {
	URL     string
	Pool    *sync.Pool
	Results chan<- models.Article
}

// NewStreamReader constructs a StreamReader that writes into the same results
// channel as the WorkerPool.
func NewStreamReader(pool *sync.Pool, results chan<- models.Article) *StreamReader {
	return &StreamReader{
		URL:     mastodonPublicStream,
		Pool:    pool,
		Results: results,
	}
}

// Connect dials the WebSocket endpoint and blocks until the context is
// cancelled or the connection drops. The caller is responsible for reconnecting.
func (s *StreamReader) Connect(ctx context.Context) error {
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(ctx, s.URL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	log.Printf("[stream] connected to %s", s.URL)

	// Close the connection when ctx is cancelled so ReadMessage unblocks.
	go func() {
		<-ctx.Done()
		conn.WriteMessage(websocket.CloseMessage, //nolint:errcheck
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shutdown"))
		conn.Close()
	}()

	s.readLoop(ctx, conn)
	return nil
}

// mastodonEvent is the outer WebSocket message envelope.
type mastodonEvent struct {
	Event   string `json:"event"`
	Payload string `json:"payload"` // JSON-encoded string, not an object
}

// mastodonStatus is the inner payload for "update" events.
type mastodonStatus struct {
	Content   string `json:"content"`
	URL       string `json:"url"`
	CreatedAt string `json:"created_at"`
}

func (s *StreamReader) readLoop(ctx context.Context, conn *websocket.Conn) {
	for {
		if ctx.Err() != nil {
			return
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Printf("[stream] read error: %v", err)
			return
		}

		var event mastodonEvent
		if err := json.Unmarshal(msg, &event); err != nil || event.Event != "update" {
			continue
		}

		var status mastodonStatus
		if err := json.Unmarshal([]byte(event.Payload), &status); err != nil {
			continue
		}

		body := stripHTML(status.Content)
		if body == "" {
			continue
		}

		a := s.Pool.Get().(*models.Article)
		a.Reset()
		a.SourceURL = status.URL
		a.Body = body
		a.FetchedAt = time.Now().UTC()

		select {
		case s.Results <- *a:
		case <-ctx.Done():
			s.Pool.Put(a)
			return
		}
		s.Pool.Put(a)
	}
}
