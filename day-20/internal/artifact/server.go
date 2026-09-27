package artifact

import (
	"ai-challenge/day-20/internal/domain"
	c "ai-challenge/day-20/internal/mcpcontract"
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
)

const BuildTool = "artifact_build_report"
const SaveTool = "artifact_save_to_file"
const ListTool = "artifact_list_files"
const ReadTool = "artifact_read_file"

func NewServer(store *Store) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "day20-artifact-mcp", Version: "1.0.0"}, nil)
	closed, nonDestructive := false, false
	add := func(t *mcp.Tool, h any) { _ = h }
	_ = add
	mcp.AddTool(s, &mcp.Tool{Name: BuildTool, Title: "Build Markdown report", Description: "Deterministically combine exact knowledge and GitHub results into Markdown; no network.", InputSchema: reportIn(), OutputSchema: reportOut(), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed}}, func(_ context.Context, _ *mcp.CallToolRequest, in domain.ReportInput) (*mcp.CallToolResult, domain.ReportOutput, error) {
		out, err := Build(in)
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, domain.ReportOutput{OK: false, Sources: []domain.Source{}, Sections: []string{}, Error: domain.Failure("invalid_argument", err)}, nil
		}
		return nil, out, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: SaveTool, Title: "Save exact Markdown", Description: "Atomically save the exact Markdown from artifact_build_report inside /data/outputs.", InputSchema: saveIn(), OutputSchema: saveOut(), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true, DestructiveHint: &nonDestructive, OpenWorldHint: &closed}}, func(_ context.Context, _ *mcp.CallToolRequest, in domain.SaveInput) (*mcp.CallToolResult, domain.SaveOutput, error) {
		out, err := store.Save(in)
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, domain.SaveOutput{OK: false, Sources: []domain.Source{}, Error: domain.Failure("invalid_argument", err)}, nil
		}
		return nil, out, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: ListTool, Title: "List Markdown files", Description: "List saved Markdown files in /data/outputs.", InputSchema: c.Object(map[string]any{}), OutputSchema: listOut(), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed}}, func(_ context.Context, _ *mcp.CallToolRequest, _ domain.ListInput) (*mcp.CallToolResult, domain.ListOutput, error) {
		out, err := store.List()
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, domain.ListOutput{OK: false, Files: []domain.FileInfo{}, Error: domain.Failure("storage_error", err)}, nil
		}
		return nil, out, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: ReadTool, Title: "Read Markdown file", Description: "Safely read one Markdown file from /data/outputs.", InputSchema: c.Object(map[string]any{"filename": filename()}, "filename"), OutputSchema: readOut(), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed}}, func(_ context.Context, _ *mcp.CallToolRequest, in domain.ReadInput) (*mcp.CallToolResult, domain.ReadOutput, error) {
		out, err := store.Read(in)
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, domain.ReadOutput{OK: false, Error: domain.Failure("storage_error", err)}, nil
		}
		return nil, out, nil
	})
	return s
}
func Handler(store *Store) http.Handler {
	mux := http.NewServeMux()
	s := NewServer(store)
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 2 << 20, PropagateRequestCancellation: true}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "day20-artifact-mcp"})
	})
	return mux
}
func filename() map[string]any {
	return map[string]any{"type": "string", "minLength": 4, "maxLength": 123, "pattern": `^[A-Za-z0-9][A-Za-z0-9._-]{0,119}\.md$`}
}
func reportIn() map[string]any {
	return c.Object(map[string]any{"topic": c.String(1, 300), "title": c.String(1, 500), "knowledge": summSchema(), "knowledge_sources": map[string]any{"type": "array", "maxItems": 5, "items": c.Source()}, "repository": repoSchema(), "latest_release": releaseSchema()}, "topic", "title", "knowledge", "knowledge_sources", "repository", "latest_release")
}
func summSchema() map[string]any {
	return c.Object(map[string]any{"ok": map[string]any{"type": "boolean"}, "query": c.String(0, 300), "summary": c.String(0, 100000), "sources": map[string]any{"type": "array", "maxItems": 5, "items": c.Source()}, "input_documents": map[string]any{"type": "integer", "minimum": 0}, "sentence_count": map[string]any{"type": "integer", "minimum": 0}, "warning": c.String(0, 1000), "error": c.Error()}, "ok", "sources", "input_documents", "sentence_count")
}
func repoSchema() map[string]any {
	return c.Object(map[string]any{"ok": map[string]any{"type": "boolean"}, "owner": c.String(0, 100), "repository": c.String(0, 100), "full_name": c.String(0, 220), "url": c.String(0, 2048), "description": c.String(0, 5000), "language": c.String(0, 100), "default_branch": c.String(0, 300), "stars": map[string]any{"type": "integer"}, "forks": map[string]any{"type": "integer"}, "open_issues": map[string]any{"type": "integer"}, "updated_at": c.String(0, 100), "source": c.String(0, 100), "fetched_at": map[string]any{"type": "string"}, "error": c.Error()}, "ok", "stars", "forks", "open_issues")
}
func releaseSchema() map[string]any {
	return c.Object(map[string]any{"ok": map[string]any{"type": "boolean"}, "owner": c.String(0, 100), "repository": c.String(0, 100), "found": map[string]any{"type": "boolean"}, "tag": c.String(0, 300), "name": c.String(0, 500), "url": c.String(0, 2048), "published_at": c.String(0, 100), "draft": map[string]any{"type": "boolean"}, "prerelease": map[string]any{"type": "boolean"}, "source": c.String(0, 100), "fetched_at": map[string]any{"type": "string"}, "error": c.Error()}, "ok", "found", "draft", "prerelease")
}
func reportOut() map[string]any {
	return c.Object(map[string]any{"ok": map[string]any{"type": "boolean"}, "markdown": c.String(0, MaxMarkdown), "sources": map[string]any{"type": "array", "maxItems": 12, "items": c.Source()}, "sha256": c.String(0, 64), "sections": map[string]any{"type": "array", "items": c.String(1, 100)}, "error": c.Error()}, "ok", "sources", "sections")
}
func saveIn() map[string]any {
	return c.Object(map[string]any{"filename": filename(), "markdown": c.String(1, MaxMarkdown), "sources": map[string]any{"type": "array", "maxItems": 12, "items": c.Source()}}, "filename", "markdown", "sources")
}
func saveOut() map[string]any {
	return c.Object(map[string]any{"ok": map[string]any{"type": "boolean"}, "path": c.String(0, 4096), "filename": c.String(0, 123), "bytes": map[string]any{"type": "integer", "minimum": 0}, "sha256": c.String(0, 64), "saved_at": map[string]any{"type": "string"}, "sources": map[string]any{"type": "array", "maxItems": 12, "items": c.Source()}, "error": c.Error()}, "ok", "bytes", "sources")
}
func listOut() map[string]any {
	return c.Object(map[string]any{"ok": map[string]any{"type": "boolean"}, "files": map[string]any{"type": "array", "items": c.Object(map[string]any{"name": filename(), "path": c.String(1, 4096), "bytes": map[string]any{"type": "integer"}, "modified_at": map[string]any{"type": "string"}, "sha256": c.String(64, 64)}, "name", "path", "bytes", "modified_at", "sha256")}, "count": map[string]any{"type": "integer"}, "error": c.Error()}, "ok", "files", "count")
}
func readOut() map[string]any {
	return c.Object(map[string]any{"ok": map[string]any{"type": "boolean"}, "filename": c.String(0, 123), "path": c.String(0, 4096), "markdown": c.String(0, MaxMarkdown), "bytes": map[string]any{"type": "integer"}, "sha256": c.String(0, 64), "error": c.Error()}, "ok", "bytes")
}
