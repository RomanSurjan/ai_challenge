package githubapi

import (
	"ai-challenge/day-20/internal/domain"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRepositoryReleaseAndNoRelease(t *testing.T) {
	token := "secret-test-token"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("token not sent")
		}
		if strings.HasSuffix(r.URL.Path, "releases/latest") {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(`{"full_name":"o/r","html_url":"https://github.com/o/r","description":"d","language":"Go","default_branch":"main","stargazers_count":3,"forks_count":2,"open_issues_count":1,"updated_at":"2026-01-01T00:00:00Z"}`))
	}))
	defer s.Close()
	c := NewClient(s.Client(), token)
	c.BaseURL = s.URL
	c.Now = func() time.Time { return time.Unix(1, 0) }
	repo, err := c.Repository(context.Background(), domain.RepositoryInput{Owner: "o", Repository: "r"})
	if err != nil || repo.FullName != "o/r" {
		t.Fatalf("%+v %v", repo, err)
	}
	rel, err := c.LatestRelease(context.Background(), domain.RepositoryInput{Owner: "o", Repository: "r"})
	if err != nil || rel.Found {
		t.Fatalf("%+v %v", rel, err)
	}
	if strings.Contains(errString(err), token) {
		t.Fatal("token leaked")
	}
}
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
