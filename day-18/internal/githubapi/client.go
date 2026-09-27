package githubapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL   = "https://api.github.com"
	APIVersion       = "2026-03-10"
	UserAgent        = "ai-challenge-day18/1.0"
	defaultTimeout   = 12 * time.Second
	maxResponseBytes = int64(2 << 20)
)

var (
	ErrNotFound  = errors.New("GitHub repository not found")
	ErrRateLimit = errors.New("GitHub API rate limit exceeded")
	ErrPrivate   = errors.New("private GitHub repositories are not supported")
)

type APIError struct {
	StatusCode         int
	Message            string
	RateLimitRemaining string
	RateLimitReset     string
	RetryAfter         string
	Kind               error
}

func (e *APIError) Error() string {
	detail := e.Message
	if e.Kind == ErrRateLimit {
		detail = strings.TrimSpace(strings.Join([]string{detail, headerDetail("reset", e.RateLimitReset), headerDetail("retry_after", e.RetryAfter)}, " "))
	}
	if detail == "" {
		detail = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("GitHub API returned HTTP %d: %s", e.StatusCode, detail)
}

func (e *APIError) Unwrap() error { return e.Kind }

type Service interface {
	GetRepository(context.Context, Query) (Repository, error)
}

type Query struct {
	Owner      string
	Repository string
}

type Repository struct {
	FullName           string   `json:"full_name"`
	Description        string   `json:"description"`
	HTMLURL            string   `json:"html_url"`
	Language           string   `json:"language"`
	DefaultBranch      string   `json:"default_branch"`
	Stars              int      `json:"stars"`
	Forks              int      `json:"forks"`
	OpenIssues         int      `json:"open_issues"`
	Archived           bool     `json:"archived"`
	License            string   `json:"license"`
	Topics             []string `json:"topics"`
	CreatedAt          string   `json:"created_at"`
	UpdatedAt          string   `json:"updated_at"`
	PushedAt           string   `json:"pushed_at"`
	RateLimitRemaining int      `json:"rate_limit_remaining"`
	Source             string   `json:"source"`
}

type Client struct {
	HTTPClient       *http.Client
	BaseURL          string
	Token            string
	Timeout          time.Duration
	MaxResponseBytes int64
}

func NewClient(client *http.Client, token string) *Client {
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{
		HTTPClient: client, BaseURL: DefaultBaseURL, Token: strings.TrimSpace(token),
		Timeout: defaultTimeout, MaxResponseBytes: maxResponseBytes,
	}
}

type repositoryResponse struct {
	FullName      string  `json:"full_name"`
	Description   *string `json:"description"`
	HTMLURL       string  `json:"html_url"`
	Language      *string `json:"language"`
	DefaultBranch string  `json:"default_branch"`
	Stars         int     `json:"stargazers_count"`
	Forks         int     `json:"forks_count"`
	OpenIssues    int     `json:"open_issues_count"`
	Archived      bool    `json:"archived"`
	License       *struct {
		SPDXID string `json:"spdx_id"`
		Name   string `json:"name"`
	} `json:"license"`
	Topics    []string `json:"topics"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
	PushedAt  string   `json:"pushed_at"`
	Private   bool     `json:"private"`
}

func (c *Client) GetRepository(ctx context.Context, query Query) (Repository, error) {
	owner := strings.TrimSpace(query.Owner)
	repository := strings.TrimSpace(query.Repository)
	if owner == "" || repository == "" {
		return Repository{}, errors.New("owner and repository are required")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	endpoint, err := url.Parse(c.BaseURL)
	if err != nil {
		return Repository{}, fmt.Errorf("build GitHub API URL: %w", err)
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/repos/" + owner + "/" + repository

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Repository{}, fmt.Errorf("create GitHub API request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", APIVersion)
	req.Header.Set("User-Agent", UserAgent)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	response, err := c.HTTPClient.Do(req)
	if err != nil {
		return Repository{}, fmt.Errorf("GitHub API request: %w", err)
	}
	defer response.Body.Close()
	limit := c.MaxResponseBytes
	if limit <= 0 {
		limit = maxResponseBytes
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return Repository{}, fmt.Errorf("read GitHub API response: %w", err)
	}
	if int64(len(body)) > limit {
		return Repository{}, fmt.Errorf("GitHub API response exceeds %d bytes", limit)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Repository{}, responseError(response, body)
	}
	var decoded repositoryResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return Repository{}, fmt.Errorf("decode GitHub API response: %w", err)
	}
	if decoded.FullName == "" || decoded.HTMLURL == "" || decoded.DefaultBranch == "" || decoded.CreatedAt == "" || decoded.UpdatedAt == "" {
		return Repository{}, errors.New("GitHub API returned an incomplete repository")
	}
	if decoded.Private {
		return Repository{}, ErrPrivate
	}

	license := "unknown"
	if decoded.License != nil {
		license = decoded.License.SPDXID
		if license == "" || license == "NOASSERTION" {
			license = decoded.License.Name
		}
	}
	remainingHeader := strings.TrimSpace(response.Header.Get("X-RateLimit-Remaining"))
	remaining, err := strconv.Atoi(remainingHeader)
	if err != nil || remaining < 0 {
		return Repository{}, errors.New("GitHub API returned an invalid X-RateLimit-Remaining header")
	}
	topics := decoded.Topics
	if topics == nil {
		topics = []string{}
	}
	return Repository{
		FullName: decoded.FullName, Description: stringValue(decoded.Description), HTMLURL: decoded.HTMLURL,
		Language: stringValue(decoded.Language), DefaultBranch: decoded.DefaultBranch, Stars: decoded.Stars,
		Forks: decoded.Forks, OpenIssues: decoded.OpenIssues, Archived: decoded.Archived, License: license,
		Topics: topics, CreatedAt: decoded.CreatedAt, UpdatedAt: decoded.UpdatedAt, PushedAt: decoded.PushedAt,
		RateLimitRemaining: remaining, Source: "GitHub REST API",
	}, nil
}

func responseError(response *http.Response, body []byte) error {
	message := conciseBody(body)
	var envelope struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &envelope) == nil && strings.TrimSpace(envelope.Message) != "" {
		message = strings.TrimSpace(envelope.Message)
	}
	kind := error(nil)
	remaining := strings.TrimSpace(response.Header.Get("X-RateLimit-Remaining"))
	if response.StatusCode == http.StatusNotFound {
		kind = ErrNotFound
	} else if response.StatusCode == http.StatusTooManyRequests || remaining == "0" {
		kind = ErrRateLimit
	}
	return &APIError{
		StatusCode: response.StatusCode, Message: message, Kind: kind,
		RateLimitRemaining: remaining,
		RateLimitReset:     strings.TrimSpace(response.Header.Get("X-RateLimit-Reset")),
		RetryAfter:         strings.TrimSpace(response.Header.Get("Retry-After")),
	}
}

func headerDetail(name, value string) string {
	if value == "" {
		return ""
	}
	return name + "=" + value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func conciseBody(body []byte) string {
	value := strings.TrimSpace(string(body))
	if len(value) > 300 {
		return value[:300] + "…"
	}
	if value == "" {
		return "empty response body"
	}
	return value
}
