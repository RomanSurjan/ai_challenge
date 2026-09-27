package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWikipediaSuccessAndFixedRequest(t *testing.T) {
	fixed := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing User-Agent")
		}
		query := r.URL.Query()
		for key, value := range map[string]string{"action": "query", "generator": "search", "prop": "extracts|info", "exintro": "1", "explaintext": "1", "inprop": "url", "formatversion": "2", "gsrsearch": "Model Context Protocol", "gsrlimit": "2"} {
			if query.Get(key) != value {
				t.Errorf("%s=%q, want %q", key, query.Get(key), value)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"batchcomplete":true,"query":{"pages":[{"title":" Model Context Protocol ","fullurl":"https://en.wikipedia.org/wiki/Model_Context_Protocol","extract":"First   sentence. Second sentence!"}]}}`))
	}))
	defer server.Close()
	client := NewWikipediaClient(server.Client())
	client.BaseURL = server.URL
	client.Now = func() time.Time { return fixed }
	result, err := client.Search(context.Background(), SearchInput{Query: "  Model   Context Protocol ", Language: "en", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Count != 1 || result.Query != "Model Context Protocol" || result.Source != WikipediaSource {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Documents[0].Text != "First sentence. Second sentence!" {
		t.Fatalf("text not normalized: %q", result.Documents[0].Text)
	}
	if !result.FetchedAt.Equal(fixed) {
		t.Fatalf("fetched_at=%v", result.FetchedAt)
	}
}

func TestWikipediaFailures(t *testing.T) {
	tests := []struct {
		name     string
		handler  http.HandlerFunc
		timeout  time.Duration
		contains string
	}{
		{"http error", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", http.StatusBadGateway) }, time.Second, "HTTP 502"},
		{"invalid json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"query":`)) }, time.Second, "decode"},
		{"invalid structure", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"batchcomplete":true}`)) }, time.Second, "no query"},
		{"body limit", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", maxWikipediaBody+1)))
		}, time.Second, "size limit"},
		{"timeout", func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(80 * time.Millisecond)
			_, _ = w.Write([]byte(`{"query":{"pages":[]}}`))
		}, 10 * time.Millisecond, "Wikipedia request"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			client := NewWikipediaClient(&http.Client{Timeout: test.timeout})
			client.BaseURL = server.URL
			_, err := client.Search(context.Background(), SearchInput{Query: "test", Language: "en", Limit: 1})
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("err=%v, want %q", err, test.contains)
			}
		})
	}
}

func TestSearchValidation(t *testing.T) {
	valid, err := validateSearch(SearchInput{Query: "  тест ", Limit: 1})
	if err != nil || valid.Language != "ru" || valid.Query != "тест" {
		t.Fatalf("default normalization failed: %+v %v", valid, err)
	}
	for _, input := range []SearchInput{{Query: "", Limit: 1}, {Query: "x", Language: "de", Limit: 1}, {Query: "x", Language: "ru", Limit: 0}, {Query: "x", Language: "ru", Limit: 6}} {
		if _, err := validateSearch(input); err == nil {
			t.Fatalf("expected validation error for %+v", input)
		}
	}
}

func TestDocumentTextLimitPreservesSourcePrefix(t *testing.T) {
	long := strings.Repeat("A factual sentence. ", 300)
	limited := limitDocumentText(long)
	if len([]rune(limited)) > maxDocumentRunes {
		t.Fatalf("limited text has %d runes", len([]rune(limited)))
	}
	if !strings.HasPrefix(long, limited) {
		t.Fatal("limited text is not an exact source prefix")
	}
	if !strings.HasSuffix(limited, ".") {
		t.Fatalf("expected sentence boundary, got %q", limited[len(limited)-20:])
	}
	withoutBoundary := strings.Repeat("x", maxDocumentRunes+100)
	if got := limitDocumentText(withoutBoundary); len([]rune(got)) != maxDocumentRunes || !strings.HasPrefix(withoutBoundary, got) {
		t.Fatal("fallback prefix limit failed")
	}
}
