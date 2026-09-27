package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"ai-challenge/day-19/internal/mcpclient"
	"ai-challenge/day-19/internal/pipeline"
)

const SystemPrompt = `Ты — агент исследовательского пайплайна. Для любого запроса найти информацию, подготовить краткую сводку и сохранить её ты обязан самостоятельно выбрать MCP tools через tool_choice=auto и вызвать ровно три отдельных инструмента строго последовательно: search → summarize → save_to_file. Никогда не пропускай этапы и не имитируй их текстом. Обычно запрашивай 3 документа в search; используй до 5 только если это явно полезно пользователю. Передай query и полный массив documents из результата search в summarize без потери, изменения или выдумывания данных. У каждого document разрешены только поля title, url и text: никогда не добавляй url_note, description, source или другие поля. Затем передай query, summary и sources из результата summarize в save_to_file без изменения; у каждого source разрешены только title и url. Используй имя .md из запроса пользователя либо безопасное описательное имя. После успешного сохранения сообщи абсолютный путь, SHA-256 и использованные источники. Основывай финальный ответ только на результатах MCP.`

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
	connector mcpclient.Connector
}

func New(cfg Config, llm LLM, connector mcpclient.Connector) *Agent {
	if cfg.Model == "" {
		cfg.Model = "deepseek-chat"
	}
	if cfg.System == "" {
		cfg.System = SystemPrompt
	}
	if cfg.Timeout < 75*time.Second {
		cfg.Timeout = 90 * time.Second
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 8192
	}
	if cfg.MaxToolRounds <= 0 {
		cfg.MaxToolRounds = 6
	}
	if cfg.MaxToolCalls <= 0 {
		cfg.MaxToolCalls = 6
	}
	return &Agent{cfg: cfg, llm: llm, connector: connector}
}

func (a *Agent) Ask(ctx context.Context, prompt string) (Response, error) {
	return a.AskWithHistory(ctx, prompt, nil)
}

func (a *Agent) AskWithHistory(ctx context.Context, prompt string, history []Message) (Response, error) {
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
	if err := validateToolSet(mcpTools); err != nil {
		return Response{}, err
	}
	tools := make([]ToolDefinition, len(mcpTools))
	names := make([]string, len(mcpTools))
	for i, tool := range mcpTools {
		tools[i] = ToolDefinition{Type: "function", Function: FunctionDefinition{Name: tool.Name, Description: tool.Description, Parameters: tool.InputSchema}}
		names[i] = tool.Name
	}
	trace := []TraceEvent{{Stage: "tools/list", Message: "MCP tools discovered: " + strings.Join(names, " → "), Result: mcpTools}}
	messages := make([]Message, 0, len(history)+8)
	messages = append(messages, Message{Role: "system", Content: a.cfg.System})
	messages = append(messages, history...)
	turnStart := len(messages)
	messages = append(messages, Message{Role: "user", Content: prompt})
	state := &pipelineState{}
	totalCalls := 0
	retriedInvalidArguments := false
	retriedSchemaArguments := false
	var usage *TokenUsage

	for round := 0; round <= a.cfg.MaxToolRounds; round++ {
		completion, err := a.llm.Complete(ctx, ChatRequest{Model: a.cfg.Model, Messages: messages, Temperature: a.cfg.Temperature, MaxTokens: a.cfg.MaxTokens, Tools: tools, ToolChoice: "auto", Stream: false})
		if err != nil {
			return Response{}, err
		}
		usage = completion.Usage
		choice := completion.Choices[0]
		if len(choice.Message.ToolCalls) == 0 {
			content := strings.TrimSpace(choice.Message.Content)
			if content == "" {
				return Response{}, errors.New("model returned neither content nor tool calls")
			}
			if state.next != 3 {
				return Response{}, fmt.Errorf("model ended before completing pipeline; next required tool is %s", state.expected())
			}
			choice.Message.Role = "assistant"
			choice.Message.Content = content
			messages = append(messages, choice.Message)
			trace = append(trace, TraceEvent{Stage: "final", Message: "Model produced the final answer after all three MCP results."})
			return Response{Content: content, Usage: usage, FinishReason: choice.FinishReason, Trace: trace, Turn: append([]Message(nil), messages[turnStart:]...)}, nil
		}
		if round == a.cfg.MaxToolRounds {
			return Response{}, fmt.Errorf("tool round limit exceeded (%d)", a.cfg.MaxToolRounds)
		}
		decodedArguments := make([]map[string]any, len(choice.Message.ToolCalls))
		var invalidCall *ToolCall
		var decodeErr error
		for i := range choice.Message.ToolCalls {
			call := &choice.Message.ToolCalls[i]
			decodedArguments[i], decodeErr = decodeArguments(call.Function.Arguments)
			if decodeErr != nil {
				invalidCall = call
				break
			}
		}
		if decodeErr != nil {
			if retriedInvalidArguments {
				return Response{}, fmt.Errorf("decode arguments for %s after retry: %w", invalidCall.Function.Name, decodeErr)
			}
			retriedInvalidArguments = true
			trace = append(trace, TraceEvent{Stage: "model/invalid_tool_call", Message: "The model returned truncated or invalid JSON arguments; requesting one corrected tool call.", Tool: invalidCall.Function.Name, ToolCallID: invalidCall.ID, Result: map[string]any{"code": "invalid_tool_arguments", "message": decodeErr.Error()}})
			messages = append(messages, Message{Role: "system", Content: fmt.Sprintf("Твой предыдущий вызов %s содержал оборванный или некорректный JSON. Повтори только следующий обязательный вызов %s с полным валидным JSON. Не сокращай и не изменяй данные предыдущего MCP-результата.", invalidCall.Function.Name, state.expected())})
			continue
		}
		choice.Message.Role = "assistant"
		messages = append(messages, choice.Message)
		for i, call := range choice.Message.ToolCalls {
			totalCalls++
			if totalCalls > a.cfg.MaxToolCalls {
				return Response{}, fmt.Errorf("tool call limit exceeded (%d)", a.cfg.MaxToolCalls)
			}
			arguments := decodedArguments[i]
			if err := state.validate(call.Function.Name, arguments); err != nil {
				var correctable *toolArgumentsError
				if !errors.As(err, &correctable) || retriedSchemaArguments {
					return Response{}, err
				}
				retriedSchemaArguments = true
				payload := map[string]any{"ok": false, "error": map[string]any{"code": "invalid_argument", "message": correctable.Error()}}
				encoded, _ := json.Marshal(payload)
				trace = append(trace, TraceEvent{Stage: "agent/argument_error", Message: "The model added fields or values forbidden by the strict tool schema; the error is returned with the original tool_call_id for one corrected attempt.", Tool: call.Function.Name, ToolCallID: call.ID, Arguments: arguments, Result: payload})
				messages = append(messages, Message{Role: "tool", ToolCallID: call.ID, Content: string(encoded)})
				continue
			}
			trace = append(trace, TraceEvent{Stage: "model/tool_call", Message: "The model selected the next MCP pipeline tool.", Tool: call.Function.Name, ToolCallID: call.ID, Arguments: arguments})
			result, err := session.CallTool(ctx, call.Function.Name, arguments)
			if err != nil {
				return Response{}, err
			}
			content, publicResult, err := toolResultContent(result)
			if err != nil {
				return Response{}, err
			}
			stage, message := "mcp/tools/call", "Structured MCP result is returned to the model with its original tool_call_id."
			if result.IsError {
				stage, message = "mcp/tool_error", "Structured MCP error is returned to the model for correction."
			}
			trace = append(trace, TraceEvent{Stage: stage, Message: message, Tool: call.Function.Name, ToolCallID: call.ID, Result: publicResult})
			messages = append(messages, Message{Role: "tool", ToolCallID: call.ID, Content: content})
			if !result.IsError {
				if err := state.accept(call.Function.Name, publicResult); err != nil {
					return Response{}, err
				}
			}
		}
	}
	return Response{}, errors.New("agent loop ended unexpectedly")
}

func validateToolSet(tools []mcpclient.Tool) error {
	want := map[string]bool{pipeline.SearchToolName: false, pipeline.SummarizeToolName: false, pipeline.SaveToolName: false}
	for _, tool := range tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			return fmt.Errorf("required MCP tool %q is missing", name)
		}
	}
	return nil
}

type pipelineState struct {
	next    int
	search  pipeline.SearchOutput
	summary pipeline.SummarizeOutput
}

type toolArgumentsError struct {
	tool string
	err  error
}

func (e *toolArgumentsError) Error() string {
	return fmt.Sprintf("invalid %s arguments: %v; retry using only fields declared in the tool schema", e.tool, e.err)
}

func (e *toolArgumentsError) Unwrap() error { return e.err }

func (s *pipelineState) expected() string {
	return []string{pipeline.SearchToolName, pipeline.SummarizeToolName, pipeline.SaveToolName, "final answer"}[s.next]
}

func (s *pipelineState) validate(name string, arguments map[string]any) error {
	if name != s.expected() {
		return fmt.Errorf("invalid pipeline order: expected %s, model selected %s", s.expected(), name)
	}
	switch name {
	case pipeline.SummarizeToolName:
		var input pipeline.SummarizeInput
		if err := remarshal(arguments, &input); err != nil {
			return &toolArgumentsError{tool: pipeline.SummarizeToolName, err: err}
		}
		if input.Query != s.search.Query || !reflect.DeepEqual(input.Documents, s.search.Documents) {
			return errors.New("summarize must receive the exact query and documents returned by search")
		}
	case pipeline.SaveToolName:
		var input pipeline.SaveInput
		if err := remarshal(arguments, &input); err != nil {
			return &toolArgumentsError{tool: pipeline.SaveToolName, err: err}
		}
		if input.Query != s.summary.Query || input.Content != s.summary.Summary || !reflect.DeepEqual(input.Sources, s.summary.Sources) {
			return errors.New("save_to_file must receive the exact query, summary, and sources returned by summarize")
		}
	}
	return nil
}

func (s *pipelineState) accept(name string, result any) error {
	switch name {
	case pipeline.SearchToolName:
		if err := remarshal(result, &s.search); err != nil {
			return fmt.Errorf("decode search result: %w", err)
		}
		if !s.search.OK {
			return errors.New("search returned an unsuccessful structured result")
		}
	case pipeline.SummarizeToolName:
		if err := remarshal(result, &s.summary); err != nil {
			return fmt.Errorf("decode summarize result: %w", err)
		}
		if !s.summary.OK {
			return errors.New("summarize returned an unsuccessful structured result")
		}
	case pipeline.SaveToolName:
		var output pipeline.SaveOutput
		if err := remarshal(result, &output); err != nil {
			return fmt.Errorf("decode save result: %w", err)
		}
		if !output.OK {
			return errors.New("save_to_file returned an unsuccessful structured result")
		}
	}
	s.next++
	return nil
}

func remarshal(value any, target any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
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

func toolResultContent(result mcpclient.ToolResult) (string, any, error) {
	if result.Structured != nil {
		encoded, err := json.Marshal(result.Structured)
		if err != nil {
			return "", nil, err
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
