package githubapi

import (
	"ai-challenge/day-20/internal/domain"
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

const maxBody = 2 << 20

type Client struct {
	HTTPClient     *http.Client
	BaseURL, Token string
	Now            func() time.Time
}

func NewClient(client *http.Client, token string) *Client {
	if client == nil {
		client = &http.Client{Timeout: 12 * time.Second}
	}
	return &Client{HTTPClient: client, BaseURL: "https://api.github.com", Token: strings.TrimSpace(token), Now: time.Now}
}
func validate(in domain.RepositoryInput) (domain.RepositoryInput, error) {
	in.Owner = strings.TrimSpace(in.Owner)
	in.Repository = strings.TrimSpace(in.Repository)
	if in.Owner == "" || in.Repository == "" || len(in.Owner) > 100 || len(in.Repository) > 100 || strings.ContainsAny(in.Owner+in.Repository, "/\\") {
		return in, errors.New("owner and repository must be simple non-empty names")
	}
	return in, nil
}
func (c *Client) get(ctx context.Context, path string, target any) (int, error) {
	base, err := url.Parse(strings.TrimRight(c.BaseURL, "/"))
	if err != nil {
		return 0, err
	}
	base.Path += path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "day20-orchestration-mcp/1.0")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("GitHub request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return resp.StatusCode, err
	}
	if len(body) > maxBody {
		return resp.StatusCode, errors.New("GitHub response exceeds size limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}
	if err = json.Unmarshal(body, target); err != nil {
		return resp.StatusCode, fmt.Errorf("decode GitHub response: %w", err)
	}
	return resp.StatusCode, nil
}
func (c *Client) Repository(ctx context.Context, in domain.RepositoryInput) (domain.RepositoryOutput, error) {
	in, err := validate(in)
	if err != nil {
		return domain.RepositoryOutput{}, err
	}
	var raw struct {
		FullName        string `json:"full_name"`
		HTMLURL         string `json:"html_url"`
		Description     string `json:"description"`
		Language        string `json:"language"`
		DefaultBranch   string `json:"default_branch"`
		UpdatedAt       string `json:"updated_at"`
		StargazersCount int    `json:"stargazers_count"`
		ForksCount      int    `json:"forks_count"`
		OpenIssuesCount int    `json:"open_issues_count"`
	}
	_, err = c.get(ctx, "/repos/"+url.PathEscape(in.Owner)+"/"+url.PathEscape(in.Repository), &raw)
	if err != nil {
		return domain.RepositoryOutput{}, err
	}
	now := c.Now().UTC()
	return domain.RepositoryOutput{OK: true, Owner: in.Owner, Repository: in.Repository, FullName: raw.FullName, URL: raw.HTMLURL, Description: raw.Description, Language: raw.Language, DefaultBranch: raw.DefaultBranch, Stars: raw.StargazersCount, Forks: raw.ForksCount, OpenIssues: raw.OpenIssuesCount, UpdatedAt: raw.UpdatedAt, Source: "GitHub REST API", FetchedAt: &now}, nil
}
func (c *Client) LatestRelease(ctx context.Context, in domain.RepositoryInput) (domain.ReleaseOutput, error) {
	in, err := validate(in)
	if err != nil {
		return domain.ReleaseOutput{}, err
	}
	var raw struct {
		Tag        string `json:"tag_name"`
		Name       string `json:"name"`
		URL        string `json:"html_url"`
		Published  string `json:"published_at"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	status, err := c.get(ctx, "/repos/"+url.PathEscape(in.Owner)+"/"+url.PathEscape(in.Repository)+"/releases/latest", &raw)
	now := c.Now().UTC()
	if status == http.StatusNotFound {
		return domain.ReleaseOutput{OK: true, Owner: in.Owner, Repository: in.Repository, Found: false, Source: "GitHub REST API", FetchedAt: &now}, nil
	}
	if err != nil {
		return domain.ReleaseOutput{}, err
	}
	return domain.ReleaseOutput{OK: true, Owner: in.Owner, Repository: in.Repository, Found: true, Tag: raw.Tag, Name: raw.Name, URL: raw.URL, PublishedAt: raw.Published, Draft: raw.Draft, Prerelease: raw.Prerelease, Source: "GitHub REST API", FetchedAt: &now}, nil
}
