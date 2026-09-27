package knowledge

import (
	"ai-challenge/day-20/internal/domain"
	c "ai-challenge/day-20/internal/mcpcontract"
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
)

const SearchTool = "knowledge_search"
const SummarizeTool = "knowledge_summarize"

func NewServer(client *Client) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "day20-knowledge-mcp", Version: "1.0.0"}, nil)
	open, closed := true, false
	mcp.AddTool(s, &mcp.Tool{Name: SearchTool, Title: "Search Wikipedia", Description: "Search public Wikipedia and return documents for exact handoff to knowledge_summarize.", InputSchema: searchIn(), OutputSchema: searchOut(), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &open}}, func(ctx context.Context, _ *mcp.CallToolRequest, in domain.SearchInput) (*mcp.CallToolResult, domain.SearchOutput, error) {
		out, err := client.Search(ctx, in)
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, domain.SearchOutput{OK: false, Documents: []domain.Document{}, Error: domain.Failure("upstream_error", err)}, nil
		}
		return nil, out, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: SummarizeTool, Title: "Summarize Wikipedia documents", Description: "Deterministic extractive summary from the exact documents supplied; no network.", InputSchema: summarizeIn(), OutputSchema: summarizeOut(), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed}}, func(_ context.Context, _ *mcp.CallToolRequest, in domain.SummarizeInput) (*mcp.CallToolResult, domain.SummarizeOutput, error) {
		out, err := Summarize(in)
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, domain.SummarizeOutput{OK: false, Sources: []domain.Source{}, Error: domain.Failure("invalid_argument", err)}, nil
		}
		return nil, out, nil
	})
	return s
}
func Handler(client *Client) http.Handler {
	mux := http.NewServeMux()
	s := NewServer(client)
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 2 << 20, PropagateRequestCancellation: true}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "day20-knowledge-mcp"})
	})
	return mux
}
func searchIn() map[string]any {
	return c.Object(map[string]any{"query": c.String(1, 300), "language": map[string]any{"type": "string", "enum": []string{"ru", "en"}, "default": "ru"}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 5}}, "query", "limit")
}
func searchOut() map[string]any {
	return c.Object(map[string]any{"ok": map[string]any{"type": "boolean"}, "query": c.String(1, 300), "source": c.String(1, 100), "fetched_at": map[string]any{"type": "string", "format": "date-time"}, "documents": map[string]any{"type": "array", "maxItems": 5, "items": c.Document()}, "count": map[string]any{"type": "integer", "minimum": 0, "maximum": 5}, "error": c.Error()}, "ok", "documents", "count")
}
func summarizeIn() map[string]any {
	return c.Object(map[string]any{"query": c.String(1, 300), "documents": map[string]any{"type": "array", "maxItems": 5, "items": c.Document()}, "max_sentences": map[string]any{"type": "integer", "minimum": 1, "maximum": 10}}, "query", "documents", "max_sentences")
}
func summarizeOut() map[string]any {
	return c.Object(map[string]any{"ok": map[string]any{"type": "boolean"}, "query": c.String(1, 300), "summary": c.String(0, 100000), "sources": map[string]any{"type": "array", "maxItems": 5, "items": c.Source()}, "input_documents": map[string]any{"type": "integer", "minimum": 0, "maximum": 5}, "sentence_count": map[string]any{"type": "integer", "minimum": 0, "maximum": 10}, "warning": c.String(0, 1000), "error": c.Error()}, "ok", "sources", "input_documents", "sentence_count")
}
