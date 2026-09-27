package pipeline

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	ServerName    = "day19-composition-mcp"
	ServerVersion = "1.0.0"
)

func NewServer(wikipedia *WikipediaClient, saver *Saver) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: ServerVersion}, &mcp.ServerOptions{
		Instructions: "Three independent tools form a model-orchestrated research pipeline: search, then summarize, then save_to_file. Do not skip or combine stages.",
	})
	registerTools(server, wikipedia, saver)
	return server
}

func registerTools(server *mcp.Server, wikipedia *WikipediaClient, saver *Saver) {
	openWorld, closedWorld, nonDestructive := true, false, false
	mcp.AddTool(server, &mcp.Tool{
		Name: SearchToolName, Title: "Search Wikipedia",
		Description: "Search the public Wikipedia API and return real introductory article text. This must be the first pipeline step. Pass its query and documents unchanged to summarize.",
		InputSchema: searchInputSchema(), OutputSchema: searchOutputSchema(),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &openWorld},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input SearchInput) (*mcp.CallToolResult, SearchOutput, error) {
		result, err := wikipedia.Search(ctx, input)
		if err != nil {
			return toolFailure(), SearchOutput{OK: false, Documents: []Document{}, Error: failure(errorCode(err), err)}, nil
		}
		return nil, result, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: SummarizeToolName, Title: "Summarize search documents",
		Description: "Create a deterministic extractive summary only from the exact structured documents returned by search. This tool does not access the network.",
		InputSchema: summarizeInputSchema(), OutputSchema: summarizeOutputSchema(),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
	}, func(_ context.Context, _ *mcp.CallToolRequest, input SummarizeInput) (*mcp.CallToolResult, SummarizeOutput, error) {
		result, err := Summarize(input)
		if err != nil {
			return toolFailure(), SummarizeOutput{OK: false, Sources: []Source{}, Error: failure("invalid_argument", err)}, nil
		}
		return nil, result, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: SaveToolName, Title: "Save Markdown summary",
		Description: "Save the exact summary and sources returned by summarize as a Markdown file inside /data/outputs. This must be the final pipeline step.",
		InputSchema: saveInputSchema(), OutputSchema: saveOutputSchema(),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true, DestructiveHint: &nonDestructive, OpenWorldHint: &closedWorld},
	}, func(_ context.Context, _ *mcp.CallToolRequest, input SaveInput) (*mcp.CallToolResult, SaveOutput, error) {
		result, err := saver.Save(input)
		if err != nil {
			return toolFailure(), SaveOutput{OK: false, Sources: []Source{}, Error: failure(errorCode(err), err)}, nil
		}
		return nil, result, nil
	})
}

func Handler(wikipedia *WikipediaClient, saver *Saver) http.Handler {
	mux := http.NewServeMux()
	server := NewServer(wikipedia, saver)
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 1 << 20, PropagateRequestCancellation: true,
	}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": ServerName})
	})
	return mux
}

func toolFailure() *mcp.CallToolResult { return &mcp.CallToolResult{IsError: true} }

func errorCode(err error) string {
	if err == nil {
		return "internal_error"
	}
	message := err.Error()
	if len(message) >= 9 && message[:9] == "Wikipedia" {
		return "upstream_error"
	}
	return "invalid_argument"
}

func obj(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required}
}

func stringProp(min, max int) map[string]any {
	return map[string]any{"type": "string", "minLength": min, "maxLength": max}
}

func documentSchema() map[string]any {
	return obj(map[string]any{
		"title": stringProp(1, 500), "url": map[string]any{"type": "string", "minLength": 1, "maxLength": 2048, "format": "uri"},
		"text": stringProp(1, 100000),
	}, "title", "url", "text")
}

func sourceSchema() map[string]any {
	return obj(map[string]any{
		"title": stringProp(1, 500), "url": map[string]any{"type": "string", "minLength": 1, "maxLength": 2048, "format": "uri"},
	}, "title", "url")
}

func errorSchema() map[string]any {
	return obj(map[string]any{"code": stringProp(1, 100), "message": stringProp(1, 1000)}, "code", "message")
}

func searchInputSchema() map[string]any {
	return obj(map[string]any{
		"query":    stringProp(1, 300),
		"language": map[string]any{"type": "string", "enum": []string{"ru", "en"}, "default": "ru"},
		"limit":    map[string]any{"type": "integer", "minimum": 1, "maximum": 5},
	}, "query", "limit")
}

func searchOutputSchema() map[string]any {
	return obj(map[string]any{
		"ok": map[string]any{"type": "boolean"}, "query": stringProp(1, 300), "source": stringProp(1, 100),
		"fetched_at": map[string]any{"type": "string", "format": "date-time"},
		"documents":  map[string]any{"type": "array", "maxItems": 5, "items": documentSchema()},
		"count":      map[string]any{"type": "integer", "minimum": 0, "maximum": 5}, "error": errorSchema(),
	}, "ok", "documents", "count")
}

func summarizeInputSchema() map[string]any {
	return obj(map[string]any{
		"query": stringProp(1, 300), "documents": map[string]any{"type": "array", "maxItems": 5, "items": documentSchema()},
		"max_sentences": map[string]any{"type": "integer", "minimum": 1, "maximum": 10},
	}, "query", "documents", "max_sentences")
}

func summarizeOutputSchema() map[string]any {
	return obj(map[string]any{
		"ok": map[string]any{"type": "boolean"}, "query": stringProp(1, 300), "summary": map[string]any{"type": "string", "maxLength": 100000},
		"sources":         map[string]any{"type": "array", "maxItems": 5, "items": sourceSchema()},
		"input_documents": map[string]any{"type": "integer", "minimum": 0, "maximum": 5},
		"sentence_count":  map[string]any{"type": "integer", "minimum": 0, "maximum": 10},
		"warning":         map[string]any{"type": "string", "maxLength": 1000}, "error": errorSchema(),
	}, "ok", "sources", "input_documents", "sentence_count")
}

func saveInputSchema() map[string]any {
	return obj(map[string]any{
		"filename": map[string]any{"type": "string", "minLength": 4, "maxLength": 123, "pattern": `^[A-Za-z0-9][A-Za-z0-9._-]{0,119}\.md$`},
		"content":  stringProp(1, MaxFileContent), "sources": map[string]any{"type": "array", "maxItems": 5, "items": sourceSchema()},
		"query": stringProp(1, 300),
	}, "filename", "content", "sources", "query")
}

func saveOutputSchema() map[string]any {
	return obj(map[string]any{
		"ok": map[string]any{"type": "boolean"}, "path": stringProp(1, 4096), "filename": stringProp(4, 123),
		"bytes": map[string]any{"type": "integer", "minimum": 0}, "sha256": map[string]any{"type": "string", "pattern": `^[a-f0-9]{64}$`},
		"saved_at": map[string]any{"type": "string", "format": "date-time"},
		"sources":  map[string]any{"type": "array", "maxItems": 5, "items": sourceSchema()}, "error": errorSchema(),
	}, "ok", "bytes", "sources")
}
