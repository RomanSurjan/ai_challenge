package githubapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const repositoryJSON = `{"full_name":"modelcontextprotocol/go-sdk","description":"Official MCP Go SDK","html_url":"https://github.com/modelcontextprotocol/go-sdk","language":"Go","default_branch":"main","stargazers_count":5153,"forks_count":420,"open_issues_count":88,"archived":false,"private":false,"license":{"spdx_id":"Apache-2.0","name":"Apache License 2.0"},"topics":["go","mcp"],"created_at":"2025-01-01T00:00:00Z","updated_at":"2026-09-26T14:09:51Z","pushed_at":"2026-09-26T13:00:00Z"}`

func TestClientGetRepository(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/modelcontextprotocol/go-sdk" {
			t.Errorf("request = %s %q", r.Method, r.URL.Path)
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("X-GitHub-Api-Version") != APIVersion || r.Header.Get("User-Agent") != UserAgent {
			t.Errorf("GitHub headers = %#v", r.Header)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("X-RateLimit-Remaining", "57")
		_, _ = w.Write([]byte(repositoryJSON))
	}))
	t.Cleanup(server.Close)
	client := NewClient(server.Client(), " test-token ")
	client.BaseURL = server.URL

	result, err := client.GetRepository(context.Background(), Query{Owner: " modelcontextprotocol ", Repository: " go-sdk "})
	if err != nil {
		t.Fatalf("GetRepository() error = %v", err)
	}
	if result.FullName != "modelcontextprotocol/go-sdk" || result.Stars != 5153 || result.License != "Apache-2.0" || result.RateLimitRemaining != 57 || result.Source != "GitHub REST API" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestClientOmitsAuthorizationWithoutTokenAndNormalizesNullableFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("X-RateLimit-Remaining", "59")
		_, _ = w.Write([]byte(`{"full_name":"owner/repo","description":null,"html_url":"https://github.com/owner/repo","language":null,"default_branch":"main","stargazers_count":0,"forks_count":0,"open_issues_count":0,"archived":false,"private":false,"license":null,"topics":null,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","pushed_at":null}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(server.Client(), "")
	client.BaseURL = server.URL

	result, err := client.GetRepository(context.Background(), Query{Owner: "owner", Repository: "repo"})
	if err != nil {
		t.Fatalf("GetRepository() error = %v", err)
	}
	if result.Description != "" || result.Language != "" || result.License != "unknown" || result.PushedAt != "" || result.Topics == nil {
		t.Fatalf("nullable fields were not normalized: %+v", result)
	}
}

func TestClientGetRepositoryReturnsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "58")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(server.Client(), "")
	client.BaseURL = server.URL

	_, err := client.GetRepository(context.Background(), Query{Owner: "missing", Repository: "repo"})
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("GetRepository() error = %v, want ErrNotFound", err)
	}
}

func TestClientGetRepositoryReturnsRateLimitError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1790443899")
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(server.Client(), "")
	client.BaseURL = server.URL

	_, err := client.GetRepository(context.Background(), Query{Owner: "owner", Repository: "repo"})
	if !errors.Is(err, ErrRateLimit) || !strings.Contains(err.Error(), "reset=1790443899") || !strings.Contains(err.Error(), "retry_after=60") {
		t.Fatalf("GetRepository() error = %v, want rate-limit details", err)
	}
}

func TestClientRejectsInvalidJSON(t *testing.T) {
	client, closeServer := testClient(t, http.StatusOK, "57", `{not-json`)
	defer closeServer()
	_, err := client.GetRepository(context.Background(), Query{Owner: "owner", Repository: "repo"})
	if err == nil || !strings.Contains(err.Error(), "decode GitHub API response") {
		t.Fatalf("GetRepository() error = %v, want JSON error", err)
	}
}

func TestClientRejectsIncompleteResponse(t *testing.T) {
	client, closeServer := testClient(t, http.StatusOK, "57", `{"full_name":"owner/repo","html_url":"https://github.com/owner/repo"}`)
	defer closeServer()
	_, err := client.GetRepository(context.Background(), Query{Owner: "owner", Repository: "repo"})
	if err == nil || !strings.Contains(err.Error(), "incomplete repository") {
		t.Fatalf("GetRepository() error = %v, want incomplete response", err)
	}
}

func TestClientRejectsOversizedResponse(t *testing.T) {
	client, closeServer := testClient(t, http.StatusOK, "57", strings.Repeat("x", 65))
	defer closeServer()
	client.MaxResponseBytes = 64
	_, err := client.GetRepository(context.Background(), Query{Owner: "owner", Repository: "repo"})
	if err == nil || !strings.Contains(err.Error(), "exceeds 64 bytes") {
		t.Fatalf("GetRepository() error = %v, want response size error", err)
	}
}

func TestClientRejectsInvalidRateLimitHeader(t *testing.T) {
	client, closeServer := testClient(t, http.StatusOK, "", repositoryJSON)
	defer closeServer()
	_, err := client.GetRepository(context.Background(), Query{Owner: "owner", Repository: "repo"})
	if err == nil || !strings.Contains(err.Error(), "X-RateLimit-Remaining") {
		t.Fatalf("GetRepository() error = %v, want rate header error", err)
	}
}

func TestClientRejectsPrivateRepository(t *testing.T) {
	privateJSON := strings.Replace(repositoryJSON, `"private":false`, `"private":true`, 1)
	client, closeServer := testClient(t, http.StatusOK, "57", privateJSON)
	defer closeServer()
	_, err := client.GetRepository(context.Background(), Query{Owner: "owner", Repository: "repo"})
	if !errors.Is(err, ErrPrivate) {
		t.Fatalf("GetRepository() error = %v, want ErrPrivate", err)
	}
}

func TestClientGetRepositoryHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("cancelled request should not reach the server")
	}))
	t.Cleanup(server.Close)
	client := NewClient(server.Client(), "")
	client.BaseURL = server.URL
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.GetRepository(ctx, Query{Owner: "owner", Repository: "repo"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetRepository() error = %v, want context cancellation", err)
	}
}

func TestClientGetRepositoryTimesOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	t.Cleanup(server.Close)
	client := NewClient(server.Client(), "")
	client.BaseURL = server.URL
	client.Timeout = 20 * time.Millisecond

	_, err := client.GetRepository(context.Background(), Query{Owner: "owner", Repository: "repo"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GetRepository() error = %v, want deadline exceeded", err)
	}
}

func testClient(t *testing.T, status int, remaining, body string) (*Client, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if remaining != "" {
			w.Header().Set("X-RateLimit-Remaining", remaining)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	client := NewClient(server.Client(), "")
	client.BaseURL = server.URL
	return client, server.Close
}
