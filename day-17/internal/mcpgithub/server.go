package mcpgithub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"ai-challenge/day-17/internal/githubapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	ServerName            = "day17-github-mcp"
	ServerVersion         = "1.0.0"
	ToolName              = "get_github_repository"
	ownerPatternText      = `^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`
	repositoryPatternText = `^[A-Za-z0-9._-]+$`
)

var (
	ownerPattern      = regexp.MustCompile(ownerPatternText)
	repositoryPattern = regexp.MustCompile(repositoryPatternText)
)

type RepositoryInput struct {
	Owner      string `json:"owner"`
	Repository string `json:"repository"`
}

type RepositoryOutput = githubapi.Repository

func ValidateInput(input RepositoryInput) error {
	owner := strings.TrimSpace(input.Owner)
	repository := strings.TrimSpace(input.Repository)
	if owner == "" || repository == "" {
		return errors.New("owner and repository are required")
	}
	if !ownerPattern.MatchString(owner) {
		return errors.New("owner must be a valid GitHub account name")
	}
	if len(repository) > 100 || !repositoryPattern.MatchString(repository) {
		return errors.New("repository must be a valid GitHub repository name")
	}
	return nil
}

func NewServer(api githubapi.Service) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: ServerName, Version: ServerVersion},
		&mcp.ServerOptions{Instructions: "Use get_github_repository to retrieve current public GitHub repository metadata."},
	)
	readOnly, openWorld := true, true
	mcp.AddTool(server, &mcp.Tool{
		Name: ToolName, Title: "GitHub repository details",
		Description: "Get current metadata and statistics for a public GitHub repository. Use this when a user asks about a repository's description, language, stars, forks, issues, license, branch, topics, or update activity.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"owner":      map[string]any{"type": "string", "minLength": 1, "maxLength": 39, "pattern": ownerPatternText, "description": "GitHub account or organization, for example modelcontextprotocol."},
				"repository": map[string]any{"type": "string", "minLength": 1, "maxLength": 100, "pattern": repositoryPatternText, "description": "Repository name without the owner, for example go-sdk."},
			},
			"required": []string{"owner", "repository"}, "additionalProperties": false,
		},
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, IdempotentHint: true, OpenWorldHint: &openWorld},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input RepositoryInput) (*mcp.CallToolResult, RepositoryOutput, error) {
		if err := ValidateInput(input); err != nil {
			return nil, RepositoryOutput{}, err
		}
		result, err := api.GetRepository(ctx, githubapi.Query{Owner: strings.TrimSpace(input.Owner), Repository: strings.TrimSpace(input.Repository)})
		if err != nil {
			return nil, RepositoryOutput{}, err
		}
		return nil, result, nil
	})
	return server
}

func Handler(api githubapi.Service) http.Handler {
	mux := http.NewServeMux()
	server := NewServer(api)
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 1 << 20, PropagateRequestCancellation: true},
	))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": ServerName})
	})
	return mux
}
