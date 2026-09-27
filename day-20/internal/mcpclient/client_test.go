package mcpclient

import (
	"ai-challenge/day-20/internal/artifact"
	"ai-challenge/day-20/internal/githubapi"
	"ai-challenge/day-20/internal/knowledge"
	"context"
	"testing"
)

func TestInMemoryCatalogRoutesAndStrictSchemas(t *testing.T) {
	c := InMemoryMultiConnector{Targets: []InMemoryTarget{{Name: "Knowledge MCP", Server: knowledge.NewServer(knowledge.NewClient(nil))}, {Name: "GitHub MCP", Server: githubapi.NewServer(githubapi.NewClient(nil, ""))}, {Name: "Artifact MCP", Server: artifact.NewServer(artifact.NewStore(t.TempDir()))}}}
	s, err := c.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tools, err := s.ListTools(context.Background())
	if err != nil || len(tools) != 8 {
		t.Fatalf("tools=%d err=%v", len(tools), err)
	}
	for _, tool := range tools {
		if tool.MCPServer == "" || tool.Endpoint == "" {
			t.Fatalf("missing owner: %+v", tool)
		}
		checkStrict(t, tool.InputSchema)
		checkStrict(t, tool.OutputSchema)
	}
}
func checkStrict(t *testing.T, v any) {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	if m["type"] == "object" && m["additionalProperties"] != false {
		t.Fatalf("non-strict object: %#v", m)
	}
	if r, ok := m["required"]; ok && r == nil {
		t.Fatal("required:null")
	}
	if p, ok := m["properties"].(map[string]any); ok {
		for _, x := range p {
			checkStrict(t, x)
		}
	}
	if x, ok := m["items"]; ok {
		checkStrict(t, x)
	}
}

type stubConnector struct{ s Session }

func (c stubConnector) Connect(context.Context) (Session, error) { return c.s, nil }

type stubSession struct{ tools []Tool }

func (s *stubSession) ListTools(context.Context) ([]Tool, error) { return s.tools, nil }
func (s *stubSession) CallTool(context.Context, string, map[string]any) (ToolResult, error) {
	return ToolResult{}, nil
}
func (s *stubSession) Owner(string) (string, bool) { return "", false }
func (s *stubSession) Close() error                { return nil }
func TestDuplicateToolRejected(t *testing.T) {
	tool := Tool{Name: "same"}
	_, err := NewMulti(Target{Name: "a", Connector: stubConnector{&stubSession{tools: []Tool{tool}}}}, Target{Name: "b", Connector: stubConnector{&stubSession{tools: []Tool{tool}}}}).Connect(context.Background())
	if err == nil {
		t.Fatal("expected duplicate error")
	}
}
