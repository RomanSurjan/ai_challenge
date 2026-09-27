package mcpdemo

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	ServerName    = "day16-mcp-server"
	ServerVersion = "1.0.0"
	ToolName      = "server_status"
)

// StatusInput is intentionally empty: the status tool needs no arguments.
type StatusInput struct{}

// StatusOutput is the structured result returned by server_status.
type StatusOutput struct {
	Service string `json:"service" jsonschema:"MCP service name"`
	Status  string `json:"status" jsonschema:"Current service status"`
	Version string `json:"version" jsonschema:"MCP service version"`
}

// NewServer builds the MCP server and registers its tools.
func NewServer() *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: ServerName, Version: ServerVersion},
		&mcp.ServerOptions{
			Instructions: "Use server_status to check that the demo MCP server is ready.",
		},
	)

	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolName,
		Title:       "Server status",
		Description: "Returns the current status and version of the demo MCP server.",
	}, func(context.Context, *mcp.CallToolRequest, StatusInput) (*mcp.CallToolResult, StatusOutput, error) {
		return nil, StatusOutput{
			Service: ServerName,
			Status:  "ok",
			Version: ServerVersion,
		}, nil
	})

	return server
}

// Handler exposes both the MCP endpoint and a conventional container health check.
func Handler() http.Handler {
	mux := http.NewServeMux()
	server := NewServer()

	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{
			Stateless:                    true,
			JSONResponse:                 true,
			MaxRequestBodyBytes:          1 << 20,
			PropagateRequestCancellation: true,
		},
	))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	return mux
}
