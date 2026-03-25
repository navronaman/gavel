package models

import "time"

// Article holds a single ingested news item. Designed for reuse via sync.Pool —
// always call Reset() before populating a recycled pointer.
type Article struct {
	SourceURL string
	Title     string
	Body      string
	FetchedAt time.Time
}

// Reset clears all fields so a pooled *Article can be safely reused.
func (a *Article) Reset() {
	a.SourceURL = ""
	a.Title = ""
	a.Body = ""
	a.FetchedAt = time.Time{}
}

// AnalysisResult holds the output from the Python AI service for a single article.
type AnalysisResult struct {
	Entities   map[string][]string
	Topic      string
	Confidence float32
	Summary    string
}

// ProcessedArticle pairs an Article with its AI analysis result. It is the
// unit of work flowing from the Batcher to the DBWriter.
type ProcessedArticle struct {
	Article Article
	Result  AnalysisResult
}

// PolicyEvent is the final enriched record written to PostgreSQL.
type PolicyEvent struct {
	RawArticleID string
	Entities     map[string][]string
	Topic        string
	Confidence   float32
	Summary      string
	AnalyzedAt   time.Time
}
