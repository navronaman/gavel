package fetcher

import (
	"bytes"
	"encoding/xml"
	"html"
	"io"
	"strings"
	"time"

	"gavel/pkg/models"
)

// ---- RSS 2.0 structs --------------------------------------------------------

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
}

// ---- Atom structs ------------------------------------------------------------

type atomFeed struct {
	XMLName xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Entries []atomEntry `xml:"http://www.w3.org/2005/Atom entry"`
}

type atomEntry struct {
	Title     string     `xml:"http://www.w3.org/2005/Atom title"`
	Links     []atomLink `xml:"http://www.w3.org/2005/Atom link"`
	Content   string     `xml:"http://www.w3.org/2005/Atom content"`
	Summary   string     `xml:"http://www.w3.org/2005/Atom summary"`
	Published string     `xml:"http://www.w3.org/2005/Atom published"`
	Updated   string     `xml:"http://www.w3.org/2005/Atom updated"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

// ---- Public API -------------------------------------------------------------

// Parse detects RSS 2.0 or Atom format and returns articles. It does not use
// sync.Pool — pooling happens in the WorkerPool so articles are sent by value.
func Parse(r io.Reader, sourceURL string) ([]models.Article, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	peek := data
	if len(peek) > 512 {
		peek = peek[:512]
	}

	if bytes.Contains(peek, []byte("<feed")) {
		return parseAtom(data, sourceURL)
	}
	return parseRSS(data, sourceURL)
}

func parseRSS(data []byte, sourceURL string) ([]models.Article, error) {
	var feed rssFeed
	if err := xml.Unmarshal(data, &feed); err != nil {
		return nil, err
	}

	articles := make([]models.Article, 0, len(feed.Channel.Items))
	for _, item := range feed.Channel.Items {
		body := stripHTML(item.Description)
		if body == "" {
			body = strings.TrimSpace(item.Title)
		}
		articles = append(articles, models.Article{
			SourceURL: sourceURL,
			Title:     strings.TrimSpace(item.Title),
			Body:      body,
			FetchedAt: parseTime(item.PubDate),
		})
	}
	return articles, nil
}

func parseAtom(data []byte, sourceURL string) ([]models.Article, error) {
	var feed atomFeed
	if err := xml.Unmarshal(data, &feed); err != nil {
		return nil, err
	}

	articles := make([]models.Article, 0, len(feed.Entries))
	for _, entry := range feed.Entries {
		body := stripHTML(entry.Content)
		if body == "" {
			body = stripHTML(entry.Summary)
		}
		if body == "" {
			body = strings.TrimSpace(entry.Title)
		}

		pub := entry.Published
		if pub == "" {
			pub = entry.Updated
		}

		articles = append(articles, models.Article{
			SourceURL: sourceURL,
			Title:     strings.TrimSpace(entry.Title),
			Body:      body,
			FetchedAt: parseTime(pub),
		})
	}
	return articles, nil
}

// ---- Helpers ----------------------------------------------------------------

var timeFormats = []string{
	time.RFC1123Z,
	time.RFC1123,
	time.RFC3339,
	"Mon, 02 Jan 2006 15:04:05 -0700",
	"Mon, 02 Jan 2006 15:04:05 MST",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02",
}

func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, f := range timeFormats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Now().UTC()
}

// stripHTML removes HTML tags and unescapes HTML entities.
func stripHTML(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(html.UnescapeString(b.String()))
}
