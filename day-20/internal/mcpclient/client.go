package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"sort"
	"strings"
	"time"
)

type Tool struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	InputSchema  any    `json:"input_schema"`
	OutputSchema any    `json:"output_schema,omitempty"`
	Annotations  any    `json:"annotations,omitempty"`
	MCPServer    string `json:"mcp_server"`
	Endpoint     string `json:"endpoint"`
}
type ToolResult struct {
	Structured any
	Text       string
	IsError    bool
}
type Session interface {
	ListTools(context.Context) ([]Tool, error)
	CallTool(context.Context, string, map[string]any) (ToolResult, error)
	Owner(string) (string, bool)
	Close() error
}
type Connector interface {
	Connect(context.Context) (Session, error)
}
type Target struct {
	Name, Endpoint string
	Connector      Connector
}
type MultiConnector struct{ Targets []Target }

func NewMulti(targets ...Target) *MultiConnector {
	return &MultiConnector{Targets: append([]Target(nil), targets...)}
}
func (c *MultiConnector) Connect(ctx context.Context) (Session, error) {
	if len(c.Targets) == 0 {
		return nil, errors.New("at least one MCP target is required")
	}
	type item struct {
		target  Target
		session Session
		tools   []Tool
		err     error
	}
	ch := make(chan item, len(c.Targets))
	for _, t := range c.Targets {
		go func(t Target) {
			s, e := t.Connector.Connect(ctx)
			var tools []Tool
			if e == nil {
				tools, e = s.ListTools(ctx)
			}
			ch <- item{t, s, tools, e}
		}(t)
	}
	result := &multiSession{routes: map[string]Session{}, owners: map[string]string{}}
	for range c.Targets {
		x := <-ch
		if x.err != nil {
			if x.session != nil {
				_ = x.session.Close()
			}
			_ = result.Close()
			return nil, fmt.Errorf("connect/list MCP %s: %w", x.target.Name, x.err)
		}
		result.sessions = append(result.sessions, x.session)
		for _, tool := range x.tools {
			if _, ok := result.routes[tool.Name]; ok {
				_ = result.Close()
				return nil, fmt.Errorf("duplicate MCP tool name %q", tool.Name)
			}
			tool.MCPServer = x.target.Name
			tool.Endpoint = x.target.Endpoint
			result.routes[tool.Name] = x.session
			result.owners[tool.Name] = x.target.Name
			result.tools = append(result.tools, tool)
		}
	}
	sort.Slice(result.tools, func(i, j int) bool { return result.tools[i].Name < result.tools[j].Name })
	return result, nil
}

type multiSession struct {
	sessions []Session
	tools    []Tool
	routes   map[string]Session
	owners   map[string]string
}

func (s *multiSession) ListTools(context.Context) ([]Tool, error) {
	return append([]Tool(nil), s.tools...), nil
}
func (s *multiSession) Owner(n string) (string, bool) { v, ok := s.owners[n]; return v, ok }
func (s *multiSession) CallTool(ctx context.Context, n string, a map[string]any) (ToolResult, error) {
	r, ok := s.routes[n]
	if !ok {
		return ToolResult{}, fmt.Errorf("MCP tool %q is not registered", n)
	}
	return r.CallTool(ctx, n, a)
}
func (s *multiSession) Close() error {
	var es []error
	for i := len(s.sessions) - 1; i >= 0; i-- {
		es = append(es, s.sessions[i].Close())
	}
	return errors.Join(es...)
}

type RemoteConnector struct {
	Endpoint   string
	HTTPClient *http.Client
}

func NewRemote(endpoint string, timeout time.Duration) *RemoteConnector {
	return &RemoteConnector{Endpoint: endpoint, HTTPClient: &http.Client{Timeout: timeout}}
}
func (c *RemoteConnector) Connect(ctx context.Context) (Session, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "day20-agent", Version: "1.0.0"}, nil)
	s, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: c.Endpoint, HTTPClient: c.HTTPClient, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return nil, err
	}
	return &remoteSession{session: s, endpoint: c.Endpoint}, nil
}

type remoteSession struct {
	session  *mcp.ClientSession
	endpoint string
	tools    []Tool
}

func (s *remoteSession) Close() error                  { return s.session.Close() }
func (s *remoteSession) Owner(n string) (string, bool) { return "", false }
func (s *remoteSession) ListTools(ctx context.Context) ([]Tool, error) {
	if s.tools != nil {
		return append([]Tool(nil), s.tools...), nil
	}
	p := &mcp.ListToolsParams{}
	for {
		page, err := s.session.ListTools(ctx, p)
		if err != nil {
			return nil, err
		}
		for _, x := range page.Tools {
			s.tools = append(s.tools, Tool{Name: x.Name, Description: x.Description, InputSchema: x.InputSchema, OutputSchema: x.OutputSchema, Annotations: x.Annotations, Endpoint: s.endpoint})
		}
		if page.NextCursor == "" {
			return append([]Tool(nil), s.tools...), nil
		}
		p.Cursor = page.NextCursor
	}
}
func (s *remoteSession) CallTool(ctx context.Context, n string, a map[string]any) (ToolResult, error) {
	r, err := s.session.CallTool(ctx, &mcp.CallToolParams{Name: n, Arguments: a})
	if err != nil {
		return ToolResult{}, err
	}
	text := ""
	for _, c := range r.Content {
		if x, ok := c.(*mcp.TextContent); ok {
			text += x.Text
		}
	}
	if r.StructuredContent != nil && text == "" {
		b, _ := json.Marshal(r.StructuredContent)
		text = string(b)
	}
	return ToolResult{Structured: r.StructuredContent, Text: text, IsError: r.IsError}, nil
}

type InMemoryTarget struct {
	Name   string
	Server *mcp.Server
}
type InMemoryMultiConnector struct{ Targets []InMemoryTarget }

func (c InMemoryMultiConnector) Connect(ctx context.Context) (Session, error) {
	targets := make([]Target, len(c.Targets))
	for i, t := range c.Targets {
		targets[i] = Target{Name: t.Name, Endpoint: "memory://" + strings.ToLower(t.Name), Connector: inMemoryConnector{server: t.Server}}
	}
	return NewMulti(targets...).Connect(ctx)
}

type inMemoryConnector struct{ server *mcp.Server }

func (c inMemoryConnector) Connect(ctx context.Context) (Session, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "day20-test", Version: "1"}, nil)
	ct, st := mcp.NewInMemoryTransports()
	go func() { _ = c.server.Run(context.Background(), st) }()
	s, err := client.Connect(ctx, ct, nil)
	if err != nil {
		return nil, err
	}
	return &remoteSession{session: s, endpoint: "memory://"}, nil
}
