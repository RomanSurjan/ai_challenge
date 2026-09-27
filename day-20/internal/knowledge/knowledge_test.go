package knowledge

import (
	"ai-challenge/day-20/internal/domain"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWikipediaClientAndSummarize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing user agent")
		}
		_, _ = w.Write([]byte(`{"query":{"pages":[{"title":"MCP","fullurl":"https://en.wikipedia.org/wiki/MCP","extract":"MCP is a protocol. Dr. Smith described it. MCP connects tools."}]}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client())
	c.BaseURL = srv.URL
	c.Now = func() time.Time { return time.Unix(1, 0) }
	out, err := c.Search(context.Background(), domain.SearchInput{Query: "MCP", Language: "en", Limit: 1})
	if err != nil || out.Count != 1 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	sum, err := Summarize(domain.SummarizeInput{Query: "MCP", Documents: out.Documents, MaxSentences: 2})
	if err != nil || sum.SentenceCount != 2 || !strings.Contains(sum.Summary, "MCP") {
		t.Fatalf("sum=%+v err=%v", sum, err)
	}
	again, _ := Summarize(domain.SummarizeInput{Query: "MCP", Documents: append(out.Documents, out.Documents...), MaxSentences: 3})
	if strings.Count(again.Summary, "MCP is a protocol") != 1 {
		t.Fatalf("duplicates not removed: %s", again.Summary)
	}
	parts := splitSentences("Dr. Smith wrote this. Т. е. граница сохранена.")
	if len(parts) != 2 || !strings.Contains(parts[0], "Dr. Smith") || !strings.Contains(parts[1], "Т. е.") {
		t.Fatalf("abbreviation split failed: %#v", parts)
	}
}
func TestWikipediaFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{{"http", "x", 500}, {"json", "{", 200}} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			c := NewClient(s.Client())
			c.BaseURL = s.URL
			if _, err := c.Search(context.Background(), domain.SearchInput{Query: "x", Limit: 1}); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	if _, err := validateSearch(domain.SearchInput{Query: "", Limit: 9}); err == nil {
		t.Fatal("expected validation error")
	}
}
