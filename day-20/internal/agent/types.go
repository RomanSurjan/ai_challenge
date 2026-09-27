package agent

import "context"

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type Message struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

type FunctionDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type ToolDefinition struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

type ChatRequest struct {
	Model       string           `json:"model"`
	Messages    []Message        `json:"messages"`
	Temperature float64          `json:"temperature,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
	ToolChoice  string           `json:"tool_choice,omitempty"`
	Stream      bool             `json:"stream"`
}

type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Choice struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type ChatResponse struct {
	Choices []Choice    `json:"choices"`
	Usage   *TokenUsage `json:"usage,omitempty"`
}

type LLM interface {
	Complete(context.Context, ChatRequest) (ChatResponse, error)
}

type TraceEvent struct {
	Stage      string         `json:"stage"`
	Message    string         `json:"message"`
	Tool       string         `json:"tool,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Arguments  map[string]any `json:"arguments,omitempty"`
	Result     any            `json:"result,omitempty"`
	MCPServer  string         `json:"mcp_server,omitempty"`
	Endpoint   string         `json:"endpoint,omitempty"`
	Transition string         `json:"transition,omitempty"`
}

type Response struct {
	Content      string       `json:"content"`
	Usage        *TokenUsage  `json:"usage,omitempty"`
	FinishReason string       `json:"finish_reason,omitempty"`
	Trace        []TraceEvent `json:"trace"`
	Turn         []Message    `json:"-"`
}
