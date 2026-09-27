package agent

import (
	"context"
	"strings"
	"testing"

	"ai-challenge/day-17/internal/mcpgithub"
)

type fakeLLM struct {
	responses []ChatResponse
	requests  []ChatRequest
}

func (f *fakeLLM) Complete(_ context.Context, request ChatRequest) (ChatResponse, error) {
	f.requests = append(f.requests, request)
	response := f.responses[0]
	f.responses = f.responses[1:]
	return response, nil
}

type fakeConnector struct{ session *fakeSession }

func (f fakeConnector) Connect(context.Context) (mcpgithub.Session, error) { return f.session, nil }

type fakeSession struct {
	name      string
	arguments map[string]any
}

func (f *fakeSession) ListTools(context.Context) ([]mcpgithub.Tool, error) {
	return []mcpgithub.Tool{{
		Name: "get_github_repository", Description: "Get current public GitHub repository metadata.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"owner": map[string]any{"type": "string"}, "repository": map[string]any{"type": "string"},
		}, "required": []string{"owner", "repository"}, "additionalProperties": false},
	}}, nil
}

func (f *fakeSession) CallTool(_ context.Context, name string, arguments map[string]any) (mcpgithub.ToolResult, error) {
	f.name, f.arguments = name, arguments
	return mcpgithub.ToolResult{Structured: map[string]any{
		"full_name": "modelcontextprotocol/go-sdk", "language": "Go", "stars": 5153,
		"default_branch": "main", "rate_limit_remaining": 57, "source": "GitHub REST API",
	}}, nil
}

func (*fakeSession) Close() error { return nil }

func TestAgentToolCallLoopReturnsModelAnswerBasedOnMCPResult(t *testing.T) {
	llm := &fakeLLM{responses: []ChatResponse{
		{Choices: []Choice{{FinishReason: "tool_calls", Message: Message{ToolCalls: []ToolCall{{
			ID: "call-1", Type: "function", Function: ToolCallFunction{Name: "get_github_repository", Arguments: `{"owner":"modelcontextprotocol","repository":"go-sdk"}`},
		}}}}}},
		{Choices: []Choice{{FinishReason: "stop", Message: Message{Content: "modelcontextprotocol/go-sdk написан на Go и имеет 5153 звезды."}}}},
	}}
	session := &fakeSession{}
	chatAgent := New(Config{Model: "fake-model"}, llm, fakeConnector{session: session})

	result, err := chatAgent.Ask(context.Background(), "Расскажи об актуальном состоянии репозитория modelcontextprotocol/go-sdk")
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if session.name != "get_github_repository" || session.arguments["owner"] != "modelcontextprotocol" || session.arguments["repository"] != "go-sdk" {
		t.Fatalf("tool call = %q %#v", session.name, session.arguments)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("LLM request count = %d", len(llm.requests))
	}
	second := llm.requests[1]
	toolMessage := second.Messages[len(second.Messages)-1]
	if len(second.Messages) < 4 || toolMessage.Role != "tool" || toolMessage.ToolCallID != "call-1" || !strings.Contains(toolMessage.Content, "5153") || !strings.Contains(toolMessage.Content, "GitHub REST API") {
		t.Fatalf("tool result was not sent to model: %+v", second.Messages)
	}
	if llm.requests[0].ToolChoice != "auto" || len(llm.requests[0].Tools) != 1 || llm.requests[0].Tools[0].Function.Name != "get_github_repository" {
		t.Fatalf("first model request does not contain MCP tool with auto choice: %+v", llm.requests[0])
	}
	if !strings.Contains(result.Content, "5153") {
		t.Fatalf("final answer does not use tool result: %q", result.Content)
	}
	if len(result.Trace) != 4 || result.Trace[1].Stage != "model/tool_call" || result.Trace[2].Stage != "mcp/tools/call" {
		t.Fatalf("unexpected trace: %+v", result.Trace)
	}
	if result.Trace[0].Result == nil {
		t.Fatal("tools/list trace does not include schemas")
	}
}
