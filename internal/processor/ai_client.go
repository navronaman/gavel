package processor

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"gavel/pkg/models"
	pb "gavel/proto/gen"
)

// AIClient wraps the gRPC connection to the Python AnalysisService.
// Protobufs give 4–5x throughput over HTTP/JSON; the binary payload is ~3x
// smaller and the .proto file makes the cross-language contract explicit.
type AIClient struct {
	conn   *grpc.ClientConn
	client pb.AnalysisServiceClient
}

// NewAIClient dials the given target (host:port) and returns a ready client.
func NewAIClient(target string) (*AIClient, error) {
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", target, err)
	}
	return &AIClient{
		conn:   conn,
		client: pb.NewAnalysisServiceClient(conn),
	}, nil
}

// Close releases the underlying gRPC connection.
func (c *AIClient) Close() error {
	return c.conn.Close()
}

// AnalyzeBatch sends a batch of articles to the Python service and returns
// one AnalysisResult per article in the same order.
func (c *AIClient) AnalyzeBatch(ctx context.Context, articles []models.Article) ([]models.AnalysisResult, error) {
	req := &pb.BatchRequest{
		Articles: make([]*pb.ArticleRequest, len(articles)),
	}
	for i, a := range articles {
		req.Articles[i] = &pb.ArticleRequest{
			Text:      a.Title + "\n\n" + a.Body,
			SourceUrl: a.SourceURL,
		}
	}

	resp, err := c.client.AnalyzeBatch(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("AnalyzeBatch RPC: %w", err)
	}

	results := make([]models.AnalysisResult, len(resp.Results))
	for i, r := range resp.Results {
		entities := make(map[string][]string, len(r.Entities))
		for group, list := range r.Entities {
			entities[group] = list.Values
		}
		results[i] = models.AnalysisResult{
			Entities:   entities,
			Topic:      r.Topic,
			Confidence: float32(r.Confidence),
			Summary:    r.Summary,
		}
	}
	return results, nil
}
