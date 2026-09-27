package mcpgithub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ai-challenge/day-17/internal/githubapi"
)

type fakeGitHub struct {
	query githubapi.Query
	err   error
}

func (f *fakeGitHub) GetRepository(_ context.Context, query githubapi.Query) (githubapi.Repository, error) {
	f.query = query
	if f.err != nil {
		return githubapi.Repository{}, f.err
	}
	return githubapi.Repository{
		FullName: "modelcontextprotocol/go-sdk", Description: "Official MCP Go SDK",
		HTMLURL: "https://github.com/modelcontextprotocol/go-sdk", Language: "Go", DefaultBranch: "main",
		Stars: 5153, Forks: 420, OpenIssues: 88, Archived: false, License: "Apache-2.0",
		Topics: []string{"go", "mcp"}, CreatedAt: "2025-01-01T00:00:00Z",
		UpdatedAt: "2026-09-26T14:09:51Z", PushedAt: "2026-09-26T13:00:00Z",
		RateLimitRemaining: 57, Source: "GitHub REST API",
	}, nil
}

func TestValidateInput(t *testing.T) {
	tests := []struct {
		name  string
		input RepositoryInput
		valid bool
	}{
		{"valid", RepositoryInput{Owner: "modelcontextprotocol", Repository: "go-sdk"}, true},
		{"missing owner", RepositoryInput{Repository: "go-sdk"}, false},
		{"missing repository", RepositoryInput{Owner: "owner"}, false},
		{"owner starts with hyphen", RepositoryInput{Owner: "-owner", Repository: "go-sdk"}, false},
		{"owner ends with hyphen", RepositoryInput{Owner: "owner-", Repository: "go-sdk"}, false},
		{"owner too long", RepositoryInput{Owner: strings.Repeat("a", 40), Repository: "go-sdk"}, false},
		{"owner with slash", RepositoryInput{Owner: "bad/owner", Repository: "go-sdk"}, false},
		{"repository with slash", RepositoryInput{Owner: "owner", Repository: "bad/repo"}, false},
		{"repository with space", RepositoryInput{Owner: "owner", Repository: "bad repo"}, false},
		{"repository too long", RepositoryInput{Owner: "owner", Repository: strings.Repeat("r", 101)}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateInput(test.input)
			if (err == nil) != test.valid {
				t.Fatalf("ValidateInput() error = %v, valid = %v", err, test.valid)
			}
		})
	}
}

func TestMCPToolsListContainsGitHubSchema(t *testing.T) {
	httpServer := httptest.NewServer(Handler(&fakeGitHub{}))
	t.Cleanup(httpServer.Close)
	session := connectTestSession(t, httpServer.URL+"/mcp")
	tools, err := session.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name != ToolName {
		t.Fatalf("tools = %+v", tools)
	}
	encodedSchema, err := json.Marshal(tools[0].InputSchema)
	if err != nil {
		t.Fatalf("marshal input schema: %v", err)
	}
	var schema struct {
		Type                 string   `json:"type"`
		AdditionalProperties bool     `json:"additionalProperties"`
		Required             []string `json:"required"`
		Properties           map[string]struct {
			Type        string `json:"type"`
			MinLength   int    `json:"minLength"`
			MaxLength   int    `json:"maxLength"`
			Pattern     string `json:"pattern"`
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(encodedSchema, &schema); err != nil {
		t.Fatalf("decode input schema: %v", err)
	}
	owner, ownerOK := schema.Properties["owner"]
	repository, repositoryOK := schema.Properties["repository"]
	if schema.Type != "object" || schema.AdditionalProperties || strings.Join(schema.Required, ",") != "owner,repository" ||
		!ownerOK || owner.Type != "string" || owner.MinLength != 1 || owner.MaxLength != 39 || owner.Pattern != ownerPatternText || owner.Description == "" ||
		!repositoryOK || repository.Type != "string" || repository.MinLength != 1 || repository.MaxLength != 100 || repository.Pattern != repositoryPatternText || repository.Description == "" {
		t.Fatalf("input schema = %s", encodedSchema)
	}
	if tools[0].OutputSchema == nil {
		t.Fatal("output schema is nil")
	}
	encodedOutput, err := json.Marshal(tools[0].OutputSchema)
	if err != nil {
		t.Fatalf("marshal output schema: %v", err)
	}
	for _, field := range []string{"full_name", "description", "html_url", "language", "default_branch", "stars", "forks", "open_issues", "archived", "license", "topics", "created_at", "updated_at", "pushed_at", "rate_limit_remaining", "source"} {
		if !strings.Contains(string(encodedOutput), `"`+field+`"`) {
			t.Fatalf("output schema is missing %q: %s", field, encodedOutput)
		}
	}
}

func TestMCPToolsCallReturnsStructuredRepository(t *testing.T) {
	api := &fakeGitHub{}
	httpServer := httptest.NewServer(Handler(api))
	t.Cleanup(httpServer.Close)
	session := connectTestSession(t, httpServer.URL+"/mcp")
	result, err := session.CallTool(context.Background(), ToolName, map[string]any{"owner": "modelcontextprotocol", "repository": "go-sdk"})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if result.IsError || result.Structured == nil {
		t.Fatalf("unexpected result: %+v", result)
	}
	encoded, _ := json.Marshal(result.Structured)
	var output RepositoryOutput
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatalf("decode structured result: %v", err)
	}
	if output.FullName != "modelcontextprotocol/go-sdk" || output.Stars != 5153 || output.DefaultBranch != "main" || output.RateLimitRemaining != 57 || api.query.Repository != "go-sdk" {
		t.Fatalf("output = %+v, query = %+v", output, api.query)
	}
}

func TestMCPToolsCallReturnsToolError(t *testing.T) {
	api := &fakeGitHub{err: errors.New("GitHub API returned HTTP 404: Not Found")}
	httpServer := httptest.NewServer(Handler(api))
	t.Cleanup(httpServer.Close)
	session := connectTestSession(t, httpServer.URL+"/mcp")
	result, err := session.CallTool(context.Background(), ToolName, map[string]any{"owner": "missing", "repository": "repo"})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Text, "HTTP 404") {
		t.Fatalf("tool result = %+v, want application error", result)
	}
}

func connectTestSession(t *testing.T, endpoint string) Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	session, err := NewRemoteConnector(endpoint, 5*time.Second).Connect(ctx)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}
