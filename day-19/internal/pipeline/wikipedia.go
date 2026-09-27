package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultWikipediaURL = "https://%s.wikipedia.org/w/api.php"
	maxWikipediaBody    = 2 << 20
	maxDocumentRunes    = 2000
)

type WikipediaClient struct {
	HTTPClient *http.Client
	BaseURL    string
	Now        func() time.Time
}

func NewWikipediaClient(client *http.Client) *WikipediaClient {
	if client == nil {
		client = &http.Client{Timeout: 12 * time.Second}
	}
	return &WikipediaClient{HTTPClient: client, Now: time.Now}
}

func validateSearch(input SearchInput) (SearchInput, error) {
	input.Query = cleanSpace(input.Query)
	input.Language = strings.ToLower(strings.TrimSpace(input.Language))
	if input.Language == "" {
		input.Language = "ru"
	}
	if input.Query == "" || len([]rune(input.Query)) > 300 {
		return input, errors.New("query must contain 1 to 300 characters")
	}
	if input.Language != "ru" && input.Language != "en" {
		return input, errors.New("language must be ru or en")
	}
	if input.Limit < 1 || input.Limit > 5 {
		return input, errors.New("limit must be between 1 and 5")
	}
	return input, nil
}

func (c *WikipediaClient) Search(ctx context.Context, input SearchInput) (SearchOutput, error) {
	input, err := validateSearch(input)
	if err != nil {
		return SearchOutput{}, err
	}
	endpoint := c.BaseURL
	if endpoint == "" {
		endpoint = fmt.Sprintf(defaultWikipediaURL, input.Language)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return SearchOutput{}, fmt.Errorf("build Wikipedia URL: %w", err)
	}
	params := parsed.Query()
	params.Set("action", "query")
	params.Set("generator", "search")
	params.Set("gsrsearch", input.Query)
	params.Set("gsrlimit", fmt.Sprint(input.Limit))
	params.Set("prop", "extracts|info")
	params.Set("exintro", "1")
	params.Set("explaintext", "1")
	params.Set("inprop", "url")
	params.Set("format", "json")
	params.Set("formatversion", "2")
	parsed.RawQuery = params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return SearchOutput{}, fmt.Errorf("create Wikipedia request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "day19-mcp-composition/1.0 (educational project)")
	response, err := c.HTTPClient.Do(req)
	if err != nil {
		return SearchOutput{}, fmt.Errorf("Wikipedia request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxWikipediaBody+1))
	if err != nil {
		return SearchOutput{}, fmt.Errorf("read Wikipedia response: %w", err)
	}
	if len(body) > maxWikipediaBody {
		return SearchOutput{}, errors.New("Wikipedia response exceeds size limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return SearchOutput{}, fmt.Errorf("Wikipedia returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		BatchComplete bool `json:"batchcomplete"`
		Query         *struct {
			Pages []struct {
				Title   string `json:"title"`
				FullURL string `json:"fullurl"`
				Extract string `json:"extract"`
			} `json:"pages"`
		} `json:"query"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return SearchOutput{}, fmt.Errorf("decode Wikipedia response: %w", err)
	}
	if len(payload.Error) > 0 && string(payload.Error) != "null" {
		return SearchOutput{}, errors.New("Wikipedia response contains an API error")
	}
	if payload.Query == nil {
		return SearchOutput{}, errors.New("Wikipedia response has no query object")
	}
	documents := make([]Document, 0, len(payload.Query.Pages))
	for _, page := range payload.Query.Pages {
		title := truncateRunes(cleanSpace(page.Title), 500)
		text := limitDocumentText(cleanSpace(page.Extract))
		link := strings.TrimSpace(page.FullURL)
		if title == "" || text == "" || link == "" {
			continue
		}
		if len([]rune(link)) > 2048 {
			continue
		}
		if u, parseErr := url.Parse(link); parseErr != nil || u.Scheme != "https" || !strings.HasSuffix(strings.ToLower(u.Hostname()), ".wikipedia.org") {
			continue
		}
		documents = append(documents, Document{Title: title, URL: link, Text: text})
	}
	now := time.Now().UTC()
	if c.Now != nil {
		now = c.Now().UTC()
	}
	return SearchOutput{OK: true, Query: input.Query, Source: WikipediaSource, FetchedAt: &now, Documents: documents, Count: len(documents)}, nil
}

func cleanSpace(value string) string { return strings.Join(strings.Fields(value), " ") }

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func limitDocumentText(value string) string {
	runes := []rune(value)
	if len(runes) <= maxDocumentRunes {
		return value
	}
	for i := maxDocumentRunes - 1; i >= maxDocumentRunes/2; i-- {
		switch runes[i] {
		case '.', '!', '?', '…':
			return strings.TrimSpace(string(runes[:i+1]))
		}
	}
	return strings.TrimSpace(string(runes[:maxDocumentRunes]))
}
