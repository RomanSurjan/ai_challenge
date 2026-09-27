package mcpdemo

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ConnectionResult contains the negotiated server details and its complete tool list.
type ConnectionResult struct {
	ServerName      string
	ServerVersion   string
	ProtocolVersion string
	Tools           []*mcp.Tool
}

// ConnectAndListTools connects to endpoint and follows every tools/list page.
func ConnectAndListTools(ctx context.Context, endpoint string) (*ConnectionResult, error) {
	transport := &mcp.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		DisableStandaloneSSE: true,
	}
	client := mcp.NewClient(
		&mcp.Implementation{Name: "day16-mcp-client", Version: ServerVersion},
		nil,
	)

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to MCP server: %w", err)
	}
	defer session.Close()

	result := &ConnectionResult{}
	if initialized := session.InitializeResult(); initialized != nil {
		result.ProtocolVersion = initialized.ProtocolVersion
		if initialized.ServerInfo != nil {
			result.ServerName = initialized.ServerInfo.Name
			result.ServerVersion = initialized.ServerInfo.Version
		}
	}

	params := &mcp.ListToolsParams{}
	for {
		page, err := session.ListTools(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("list MCP tools: %w", err)
		}
		result.Tools = append(result.Tools, page.Tools...)
		if page.NextCursor == "" {
			break
		}
		params.Cursor = page.NextCursor
	}

	return result, nil
}
