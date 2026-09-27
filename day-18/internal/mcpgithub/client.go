package mcpgithub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Tool struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	InputSchema  any    `json:"input_schema"`
	OutputSchema any    `json:"output_schema,omitempty"`
	Annotations  any    `json:"annotations,omitempty"`
	MCPServer    string `json:"mcp_server,omitempty"`
}

type ToolResult struct {
	Structured any
	Text       string
	IsError    bool
}

type Session interface {
	ListTools(context.Context) ([]Tool, error)
	CallTool(context.Context, string, map[string]any) (ToolResult, error)
	Close() error
}

type Connector interface {
	Connect(context.Context) (Session, error)
}

type RemoteConnector struct {
	Endpoint   string
	HTTPClient *http.Client
}

func NewRemoteConnector(endpoint string, timeout time.Duration) *RemoteConnector {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &RemoteConnector{Endpoint: endpoint, HTTPClient: &http.Client{Timeout: timeout}}
}

func (c *RemoteConnector) Connect(ctx context.Context) (Session, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "day18-agent", Version: ServerVersion}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: c.Endpoint, HTTPClient: c.HTTPClient, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to MCP server: %w", err)
	}
	return &remoteSession{session: session}, nil
}

type remoteSession struct{ session *mcp.ClientSession }

func (s *remoteSession) Close() error { return s.session.Close() }

func (s *remoteSession) ListTools(ctx context.Context) ([]Tool, error) {
	params := &mcp.ListToolsParams{}
	var tools []Tool
	for {
		page, err := s.session.ListTools(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("MCP tools/list: %w", err)
		}
		for _, item := range page.Tools {
			tools = append(tools, Tool{Name: item.Name, Description: item.Description, InputSchema: item.InputSchema, OutputSchema: item.OutputSchema, Annotations: item.Annotations})
		}
		if page.NextCursor == "" {
			return tools, nil
		}
		params.Cursor = page.NextCursor
	}
}

func (s *remoteSession) CallTool(ctx context.Context, name string, arguments map[string]any) (ToolResult, error) {
	result, err := s.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return ToolResult{}, fmt.Errorf("MCP tools/call %s: %w", name, err)
	}
	text := ""
	for _, content := range result.Content {
		if item, ok := content.(*mcp.TextContent); ok {
			if text != "" {
				text += "\n"
			}
			text += item.Text
		}
	}
	if result.StructuredContent == nil && text == "" {
		return ToolResult{}, errors.New("MCP tool returned no content")
	}
	if result.StructuredContent != nil && text == "" {
		encoded, _ := json.Marshal(result.StructuredContent)
		text = string(encoded)
	}
	return ToolResult{Structured: result.StructuredContent, Text: text, IsError: result.IsError}, nil
}
