package agent

import (
	"context"
	"strings"
	"testing"

	"ai-challenge/day-18/internal/mcpgithub"
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

type recordedCall struct {
	name      string
	arguments map[string]any
}

type fakeSession struct{ calls []recordedCall }

func (f *fakeSession) ListTools(context.Context) ([]mcpgithub.Tool, error) {
	return []mcpgithub.Tool{
		{Name: mcpgithub.ScheduleToolName, Description: "Create a persistent monitor.", InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{"owner": map[string]any{"type": "string"}, "repository": map[string]any{"type": "string"}, "interval_seconds": map[string]any{"type": "integer"}},
			"required": []string{"owner", "repository", "interval_seconds"}, "additionalProperties": false,
		}},
		{Name: mcpgithub.SummaryToolName, Description: "Get an aggregate summary.", InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{"schedule_id": map[string]any{"type": "string"}}, "required": []string{"schedule_id"}, "additionalProperties": false,
		}},
	}, nil
}

func (f *fakeSession) CallTool(_ context.Context, name string, arguments map[string]any) (mcpgithub.ToolResult, error) {
	f.calls = append(f.calls, recordedCall{name: name, arguments: arguments})
	if name == mcpgithub.ScheduleToolName {
		return mcpgithub.ToolResult{Structured: map[string]any{"ok": true, "schedule_id": "mon_0123456789abcdef0123456789abcdef", "status": "active"}}, nil
	}
	return mcpgithub.ToolResult{Structured: map[string]any{
		"ok": true, "schedule_id": "mon_0123456789abcdef0123456789abcdef", "snapshot_count": 2,
		"stars": map[string]any{"latest": 5153, "delta": 1}, "source": "GitHub REST API",
	}}, nil
}

func (*fakeSession) Close() error { return nil }

func TestAgentSupportsSequentialModelSelectedMCPCalls(t *testing.T) {
	llm := &fakeLLM{responses: []ChatResponse{
		{Choices: []Choice{{FinishReason: "tool_calls", Message: Message{ToolCalls: []ToolCall{{
			ID: "call-create", Type: "function", Function: ToolCallFunction{Name: mcpgithub.ScheduleToolName, Arguments: `{"owner":"modelcontextprotocol","repository":"go-sdk","interval_seconds":60,"max_runs":2}`},
		}}}}}},
		{Choices: []Choice{{FinishReason: "tool_calls", Message: Message{ToolCalls: []ToolCall{{
			ID: "call-summary", Type: "function", Function: ToolCallFunction{Name: mcpgithub.SummaryToolName, Arguments: `{"schedule_id":"mon_0123456789abcdef0123456789abcdef"}`},
		}}}}}},
		{Choices: []Choice{{FinishReason: "stop", Message: Message{Content: "Мониторинг создан; два снимка сохранены, звёзды выросли на одну."}}}},
	}}
	session := &fakeSession{}
	chatAgent := New(Config{Model: "fake-model"}, llm, fakeConnector{session: session})

	result, err := chatAgent.Ask(context.Background(), "Создай мониторинг, затем покажи его сводку")
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if len(session.calls) != 2 || session.calls[0].name != mcpgithub.ScheduleToolName || session.calls[1].name != mcpgithub.SummaryToolName {
		t.Fatalf("tool calls = %+v", session.calls)
	}
	if session.calls[0].arguments["owner"] != "modelcontextprotocol" || session.calls[0].arguments["interval_seconds"].(jsonNumber).String() != "60" {
		t.Fatalf("create arguments = %#v", session.calls[0].arguments)
	}
	if len(llm.requests) != 3 || llm.requests[0].ToolChoice != "auto" || len(llm.requests[0].Tools) != 2 {
		t.Fatalf("LLM requests = %+v", llm.requests)
	}
	firstToolResult := llm.requests[1].Messages[len(llm.requests[1].Messages)-1]
	secondToolResult := llm.requests[2].Messages[len(llm.requests[2].Messages)-1]
	if firstToolResult.Role != "tool" || firstToolResult.ToolCallID != "call-create" || !strings.Contains(firstToolResult.Content, "schedule_id") {
		t.Fatalf("first tool message = %+v", firstToolResult)
	}
	if secondToolResult.Role != "tool" || secondToolResult.ToolCallID != "call-summary" || !strings.Contains(secondToolResult.Content, "5153") {
		t.Fatalf("second tool message = %+v", secondToolResult)
	}
	if result.Content == "" || len(result.Trace) != 6 || result.Trace[5].Stage != "final" {
		t.Fatalf("result = %+v", result)
	}
}

func TestAgentIncludesPreviousConversationAndReturnsCompleteTurn(t *testing.T) {
	llm := &fakeLLM{responses: []ChatResponse{{Choices: []Choice{{FinishReason: "stop", Message: Message{Content: "Помню предыдущий вопрос."}}}}}}
	chatAgent := New(Config{Model: "fake-model"}, llm, fakeConnector{session: &fakeSession{}})
	history := []Message{{Role: "user", Content: "Меня интересует USD/EUR."}, {Role: "assistant", Content: "Хорошо, запомнил."}}
	result, err := chatAgent.AskWithHistory(context.Background(), "Что меня интересует?", history)
	if err != nil {
		t.Fatal(err)
	}
	request := llm.requests[0]
	if len(request.Messages) != 4 || request.Messages[1].Content != history[0].Content || request.Messages[2].Content != history[1].Content || request.Messages[3].Content != "Что меня интересует?" {
		t.Fatalf("messages = %#v", request.Messages)
	}
	if len(result.Turn) != 2 || result.Turn[0].Role != "user" || result.Turn[1].Role != "assistant" || result.Turn[1].Content != result.Content {
		t.Fatalf("turn = %#v", result.Turn)
	}
}

// decodeArguments uses json.Decoder.UseNumber; this narrow interface keeps the
// assertion independent of concrete imports in the production API.
type jsonNumber interface{ String() string }
