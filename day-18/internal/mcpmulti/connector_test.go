package mcpmulti

import (
	"context"
	"strings"
	"testing"

	"ai-challenge/day-18/internal/mcpgithub"
)

type fakeConnector struct{ session mcpgithub.Session }

func (f fakeConnector) Connect(context.Context) (mcpgithub.Session, error) { return f.session, nil }

type fakeSession struct {
	tools  []mcpgithub.Tool
	calls  []string
	closed bool
}

func (f *fakeSession) ListTools(context.Context) ([]mcpgithub.Tool, error) { return f.tools, nil }
func (f *fakeSession) CallTool(_ context.Context, name string, _ map[string]any) (mcpgithub.ToolResult, error) {
	f.calls = append(f.calls, name)
	return mcpgithub.ToolResult{Structured: map[string]any{"tool": name}}, nil
}
func (f *fakeSession) Close() error { f.closed = true; return nil }

func TestConnectorAggregatesAndRoutesTools(t *testing.T) {
	github := &fakeSession{tools: []mcpgithub.Tool{{Name: "schedule_repository_monitor"}}}
	currency := &fakeSession{tools: []mcpgithub.Tool{{Name: "convert_currency"}}}
	connector := New(
		Target{Name: "github", Connector: fakeConnector{github}},
		Target{Name: "currency", Connector: fakeConnector{currency}},
	)
	session, err := connector.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tools, err := session.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "convert_currency" || tools[0].MCPServer != "currency" || tools[1].MCPServer != "github" {
		t.Fatalf("tools = %#v", tools)
	}
	if _, err := session.CallTool(context.Background(), "convert_currency", nil); err != nil {
		t.Fatal(err)
	}
	if len(currency.calls) != 1 || len(github.calls) != 0 {
		t.Fatalf("currency=%v github=%v", currency.calls, github.calls)
	}
	if _, err := session.CallTool(context.Background(), "missing", nil); err == nil {
		t.Fatal("missing tool must fail")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if !github.closed || !currency.closed {
		t.Fatal("all sessions must close")
	}
}

func TestConnectorRejectsDuplicateToolNames(t *testing.T) {
	one := &fakeSession{tools: []mcpgithub.Tool{{Name: "duplicate"}}}
	two := &fakeSession{tools: []mcpgithub.Tool{{Name: "duplicate"}}}
	_, err := New(Target{Name: "one", Connector: fakeConnector{one}}, Target{Name: "two", Connector: fakeConnector{two}}).Connect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "duplicate MCP tool") {
		t.Fatalf("err = %v", err)
	}
	if !one.closed || !two.closed {
		t.Fatal("sessions must close after catalog failure")
	}
}
