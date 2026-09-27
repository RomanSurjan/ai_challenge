package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"ai-challenge/day-17/internal/mcpgithub"
)

const (
	defaultMaxToolRounds = 4
	defaultMaxToolCalls  = 8
)

type Config struct {
	Model         string
	System        string
	Timeout       time.Duration
	MaxTokens     int
	Temperature   float64
	MaxToolRounds int
	MaxToolCalls  int
}

type Agent struct {
	cfg       Config
	llm       LLM
	connector mcpgithub.Connector
}

func New(cfg Config, llm LLM, connector mcpgithub.Connector) *Agent {
	if cfg.Model == "" {
		cfg.Model = "deepseek-chat"
	}
	if cfg.System == "" {
		cfg.System = "You are a helpful assistant. Use available tools whenever fresh external data is needed. Base the final answer on tool results and never invent tool output."
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 1024
	}
	if cfg.MaxToolRounds <= 0 {
		cfg.MaxToolRounds = defaultMaxToolRounds
	}
	if cfg.MaxToolCalls <= 0 {
		cfg.MaxToolCalls = defaultMaxToolCalls
	}
	return &Agent{cfg: cfg, llm: llm, connector: connector}
}

func (a *Agent) Ask(ctx context.Context, prompt string) (Response, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return Response{}, errors.New("message cannot be empty")
	}
	if a.llm == nil || a.connector == nil {
		return Response{}, errors.New("agent dependencies are not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()

	session, err := a.connector.Connect(ctx)
	if err != nil {
		return Response{}, err
	}
	defer session.Close()
	mcpTools, err := session.ListTools(ctx)
	if err != nil {
		return Response{}, err
	}
	if len(mcpTools) == 0 {
		return Response{}, errors.New("MCP server returned no tools")
	}
	tools := make([]ToolDefinition, len(mcpTools))
	names := make([]string, len(mcpTools))
	for i, tool := range mcpTools {
		tools[i] = ToolDefinition{Type: "function", Function: FunctionDefinition{
			Name: tool.Name, Description: tool.Description, Parameters: tool.InputSchema,
		}}
		names[i] = tool.Name
	}
	trace := []TraceEvent{{Stage: "tools/list", Message: "MCP tools discovered: " + strings.Join(names, ", "), Result: mcpTools}}
	messages := []Message{{Role: "system", Content: a.cfg.System}, {Role: "user", Content: prompt}}
	totalCalls := 0

	for round := 0; round <= a.cfg.MaxToolRounds; round++ {
		request := ChatRequest{
			Model: a.cfg.Model, Messages: messages, Temperature: a.cfg.Temperature,
			MaxTokens: a.cfg.MaxTokens, Thinking: &ThinkingConfig{Type: "disabled"},
			Tools: tools, ToolChoice: "auto", Stream: false,
		}
		completion, err := a.llm.Complete(ctx, request)
		if err != nil {
			return Response{}, err
		}
		choice := completion.Choices[0]
		if len(choice.Message.ToolCalls) == 0 {
			content := strings.TrimSpace(choice.Message.Content)
			if content == "" {
				return Response{}, errors.New("model returned neither content nor tool calls")
			}
			trace = append(trace, TraceEvent{Stage: "final", Message: "Model produced the final answer from the conversation and tool results."})
			return Response{Content: content, Usage: completion.Usage, FinishReason: choice.FinishReason, Trace: trace}, nil
		}
		if round == a.cfg.MaxToolRounds {
			return Response{}, fmt.Errorf("tool round limit exceeded (%d)", a.cfg.MaxToolRounds)
		}
		choice.Message.Role = "assistant"
		messages = append(messages, choice.Message)
		for _, call := range choice.Message.ToolCalls {
			totalCalls++
			if totalCalls > a.cfg.MaxToolCalls {
				return Response{}, fmt.Errorf("tool call limit exceeded (%d)", a.cfg.MaxToolCalls)
			}
			arguments, err := decodeArguments(call.Function.Arguments)
			if err != nil {
				return Response{}, fmt.Errorf("decode arguments for %s: %w", call.Function.Name, err)
			}
			trace = append(trace, TraceEvent{Stage: "model/tool_call", Message: "The model selected an MCP tool.", Tool: call.Function.Name, Arguments: arguments})
			result, err := session.CallTool(ctx, call.Function.Name, arguments)
			if err != nil {
				return Response{}, err
			}
			content, publicResult, err := toolResultContent(result)
			if err != nil {
				return Response{}, err
			}
			stage := "mcp/tools/call"
			message := "MCP returned structured API data; it is now sent back to the model."
			if result.IsError {
				stage = "mcp/tool_error"
				message = "MCP returned a tool error; it is sent back to the model for correction."
			}
			trace = append(trace, TraceEvent{Stage: stage, Message: message, Tool: call.Function.Name, Result: publicResult})
			messages = append(messages, Message{Role: "tool", ToolCallID: call.ID, Content: content})
		}
	}
	return Response{}, errors.New("agent loop ended unexpectedly")
}

func decodeArguments(raw string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var arguments map[string]any
	if err := decoder.Decode(&arguments); err != nil {
		return nil, err
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return arguments, nil
}

func toolResultContent(result mcpgithub.ToolResult) (string, any, error) {
	if result.Structured != nil {
		encoded, err := json.Marshal(result.Structured)
		if err != nil {
			return "", nil, fmt.Errorf("encode structured MCP result: %w", err)
		}
		return string(encoded), result.Structured, nil
	}
	if result.Text == "" {
		return "", nil, errors.New("MCP tool returned an empty result")
	}
	var decoded any
	if json.Unmarshal([]byte(result.Text), &decoded) == nil {
		return result.Text, decoded, nil
	}
	return result.Text, result.Text, nil
}
