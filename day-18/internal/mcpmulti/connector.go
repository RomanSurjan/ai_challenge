package mcpmulti

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"ai-challenge/day-18/internal/mcpgithub"
)

type Target struct {
	Name      string
	Connector mcpgithub.Connector
}

type Connector struct {
	Targets []Target
}

func New(targets ...Target) *Connector {
	return &Connector{Targets: append([]Target(nil), targets...)}
}

func (c *Connector) Connect(ctx context.Context) (mcpgithub.Session, error) {
	if c == nil || len(c.Targets) == 0 {
		return nil, errors.New("at least one MCP server is required")
	}
	result := &session{routes: make(map[string]mcpgithub.Session)}
	for _, target := range c.Targets {
		name := strings.TrimSpace(target.Name)
		if name == "" || target.Connector == nil {
			_ = result.Close()
			return nil, errors.New("every MCP target requires a name and connector")
		}
		remote, err := target.Connector.Connect(ctx)
		if err != nil {
			_ = result.Close()
			return nil, fmt.Errorf("connect to MCP %s: %w", name, err)
		}
		result.sessions = append(result.sessions, remote)
		tools, err := remote.ListTools(ctx)
		if err != nil {
			_ = result.Close()
			return nil, fmt.Errorf("list tools from MCP %s: %w", name, err)
		}
		for _, tool := range tools {
			if _, exists := result.routes[tool.Name]; exists {
				_ = result.Close()
				return nil, fmt.Errorf("duplicate MCP tool name %q", tool.Name)
			}
			tool.MCPServer = name
			result.routes[tool.Name] = remote
			result.tools = append(result.tools, tool)
		}
	}
	sort.Slice(result.tools, func(i, j int) bool { return result.tools[i].Name < result.tools[j].Name })
	return result, nil
}

type session struct {
	sessions []mcpgithub.Session
	tools    []mcpgithub.Tool
	routes   map[string]mcpgithub.Session
}

func (s *session) ListTools(context.Context) ([]mcpgithub.Tool, error) {
	return append([]mcpgithub.Tool(nil), s.tools...), nil
}

func (s *session) CallTool(ctx context.Context, name string, arguments map[string]any) (mcpgithub.ToolResult, error) {
	remote, ok := s.routes[name]
	if !ok {
		return mcpgithub.ToolResult{}, fmt.Errorf("MCP tool %q is not registered", name)
	}
	return remote.CallTool(ctx, name, arguments)
}

func (s *session) Close() error {
	errs := make([]error, 0, len(s.sessions))
	for i := len(s.sessions) - 1; i >= 0; i-- {
		if err := s.sessions[i].Close(); err != nil {
			errs = append(errs, err)
		}
	}
	s.sessions = nil
	return errors.Join(errs...)
}
