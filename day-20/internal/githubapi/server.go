package githubapi

import (
	"ai-challenge/day-20/internal/domain"
	c "ai-challenge/day-20/internal/mcpcontract"
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
)

const RepositoryTool = "github_get_repository"
const ReleaseTool = "github_get_latest_release"

func NewServer(client *Client) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "day20-github-mcp", Version: "1.0.0"}, nil)
	open := true
	input := c.Object(map[string]any{"owner": c.String(1, 100), "repository": c.String(1, 100)}, "owner", "repository")
	mcp.AddTool(s, &mcp.Tool{Name: RepositoryTool, Title: "Get GitHub repository", Description: "Fetch current public repository metadata from GitHub REST API.", InputSchema: input, OutputSchema: repositoryOut(), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &open}}, func(ctx context.Context, _ *mcp.CallToolRequest, in domain.RepositoryInput) (*mcp.CallToolResult, domain.RepositoryOutput, error) {
		out, err := client.Repository(ctx, in)
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, domain.RepositoryOutput{OK: false, Error: domain.Failure("github_error", err)}, nil
		}
		return nil, out, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: ReleaseTool, Title: "Get latest GitHub release", Description: "Fetch latest public release; no release is a successful found=false result.", InputSchema: input, OutputSchema: releaseOut(), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &open}}, func(ctx context.Context, _ *mcp.CallToolRequest, in domain.RepositoryInput) (*mcp.CallToolResult, domain.ReleaseOutput, error) {
		out, err := client.LatestRelease(ctx, in)
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, domain.ReleaseOutput{OK: false, Found: false, Error: domain.Failure("github_error", err)}, nil
		}
		return nil, out, nil
	})
	return s
}
func Handler(client *Client) http.Handler {
	mux := http.NewServeMux()
	s := NewServer(client)
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 1 << 20, PropagateRequestCancellation: true}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "day20-github-mcp"})
	})
	return mux
}
func repositoryOut() map[string]any {
	return c.Object(map[string]any{"ok": map[string]any{"type": "boolean"}, "owner": c.String(1, 100), "repository": c.String(1, 100), "full_name": c.String(1, 220), "url": c.String(1, 2048), "description": c.String(0, 5000), "language": c.String(0, 100), "default_branch": c.String(0, 300), "stars": map[string]any{"type": "integer", "minimum": 0}, "forks": map[string]any{"type": "integer", "minimum": 0}, "open_issues": map[string]any{"type": "integer", "minimum": 0}, "updated_at": c.String(0, 100), "source": c.String(1, 100), "fetched_at": map[string]any{"type": "string", "format": "date-time"}, "error": c.Error()}, "ok", "stars", "forks", "open_issues")
}
func releaseOut() map[string]any {
	return c.Object(map[string]any{"ok": map[string]any{"type": "boolean"}, "owner": c.String(1, 100), "repository": c.String(1, 100), "found": map[string]any{"type": "boolean"}, "tag": c.String(0, 300), "name": c.String(0, 500), "url": c.String(0, 2048), "published_at": c.String(0, 100), "draft": map[string]any{"type": "boolean"}, "prerelease": map[string]any{"type": "boolean"}, "source": c.String(1, 100), "fetched_at": map[string]any{"type": "string", "format": "date-time"}, "error": c.Error()}, "ok", "found", "draft", "prerelease")
}
