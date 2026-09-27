package agent

import (
	"ai-challenge/day-20/internal/artifact"
	"ai-challenge/day-20/internal/domain"
	"ai-challenge/day-20/internal/githubapi"
	"ai-challenge/day-20/internal/knowledge"
	"ai-challenge/day-20/internal/mcpclient"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"
)

const SystemPrompt = `Ты — агент-оркестратор трёх независимых MCP-серверов. Всегда самостоятельно решай, какие инструменты нужны и в каком порядке их вызывать, через tool_choice=auto. Для простого запроса используй только нужные инструменты: Wikipedia — knowledge_search и при необходимости knowledge_summarize; GitHub — github_get_repository или github_get_latest_release; файлы — artifact_list_files или artifact_read_file. Для полного исследования собери knowledge summary, repository и latest release; независимые Knowledge и GitHub операции можно выполнять в выбранном тобой порядке. Соблюдай только зависимости данных: knowledge_summarize получает точный результат knowledge_search, artifact_build_report получает точные результаты knowledge и GitHub, artifact_save_to_file получает точный результат artifact_build_report. После сохранения дай финальный ответ с путём и SHA-256. Можно повторять независимые поисковые вызовы, если это полезно. Не добавляй полей вне JSON Schema и не изменяй structured results при передаче.`

type Config struct {
	Model, System                          string
	Timeout                                time.Duration
	MaxTokens, MaxToolRounds, MaxToolCalls int
	Temperature                            float64
}
type Agent struct {
	cfg       Config
	llm       LLM
	connector mcpclient.Connector
}

func New(cfg Config, llm LLM, c mcpclient.Connector) *Agent {
	if cfg.Model == "" {
		cfg.Model = "deepseek-chat"
	}
	if cfg.System == "" {
		cfg.System = SystemPrompt
	}
	if cfg.Timeout < 90*time.Second {
		cfg.Timeout = 90 * time.Second
	}
	if cfg.MaxTokens < 1 {
		cfg.MaxTokens = 16384
	}
	if cfg.MaxToolRounds < 1 {
		cfg.MaxToolRounds = 18
	}
	if cfg.MaxToolCalls < 1 {
		cfg.MaxToolCalls = 24
	}
	return &Agent{cfg: cfg, llm: llm, connector: c}
}
func (a *Agent) Ask(ctx context.Context, p string) (Response, error) {
	return a.AskWithHistory(ctx, p, nil)
}
func (a *Agent) AskWithHistory(ctx context.Context, p string, h []Message) (Response, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return Response{}, errors.New("message cannot be empty")
	}
	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()
	s, err := a.connector.Connect(ctx)
	if err != nil {
		return Response{}, err
	}
	defer s.Close()
	catalog, err := s.ListTools(ctx)
	if err != nil {
		return Response{}, err
	}
	tools := make([]ToolDefinition, len(catalog))
	for i, t := range catalog {
		tools[i] = ToolDefinition{Type: "function", Function: FunctionDefinition{Name: t.Name, Description: t.Description, Parameters: t.InputSchema}}
	}
	trace := []TraceEvent{{Stage: "tools/list", Message: "Объединённый каталог зарегистрирован; каждый инструмент связан с MCP-владельцем.", Result: catalog}}
	messages := append([]Message{{Role: "system", Content: a.cfg.System}}, h...)
	start := len(messages)
	messages = append(messages, Message{Role: "user", Content: p})
	state := &flowState{}
	calls := 0
	cache := make(map[string]cachedResult)
	forceFinal := false
	badJSON, badSchema, badDependency := false, false, false
	for round := 0; round <= a.cfg.MaxToolRounds; round++ {
		if round == a.cfg.MaxToolRounds && !forceFinal {
			forceFinal = true
			messages = append(messages, Message{Role: "system", Content: "Лимит раундов исчерпан. Не вызывай инструменты; сформируй лучший финальный ответ только по уже полученным результатам."})
			trace = append(trace, TraceEvent{Stage: "agent/round_budget", Message: "Достигнут лимит раундов; модель переведена в режим финального ответа без tools."})
		}
		requestTools, toolChoice := tools, "auto"
		if forceFinal {
			requestTools, toolChoice = nil, ""
		}
		r, err := a.llm.Complete(ctx, ChatRequest{Model: a.cfg.Model, Messages: messages, Temperature: a.cfg.Temperature, MaxTokens: a.cfg.MaxTokens, Tools: requestTools, ToolChoice: toolChoice, Stream: false})
		if err != nil {
			return Response{}, err
		}
		choice := r.Choices[0]
		if len(choice.Message.ToolCalls) == 0 {
			content := strings.TrimSpace(choice.Message.Content)
			if content == "" {
				return Response{}, errors.New("model returned no content")
			}
			messages = append(messages, choice.Message)
			trace = append(trace, TraceEvent{Stage: "final", Message: "Финальный ответ сформирован после завершения выбранного flow."})
			return Response{Content: content, Usage: r.Usage, FinishReason: choice.FinishReason, Trace: trace, Turn: append([]Message(nil), messages[start:]...)}, nil
		}
		if forceFinal {
			return Response{}, errors.New("model returned tool calls after tools were disabled")
		}
		decoded := make([]map[string]any, len(choice.Message.ToolCalls))
		var decodeErr error
		var broken ToolCall
		for i, c := range choice.Message.ToolCalls {
			decoded[i], decodeErr = decode(c.Function.Arguments)
			if decodeErr != nil {
				broken = c
				break
			}
		}
		if decodeErr != nil {
			if badJSON {
				return Response{}, fmt.Errorf("invalid tool JSON after retry: %w", decodeErr)
			}
			badJSON = true
			payload := map[string]any{"code": "invalid_tool_arguments", "message": decodeErr.Error()}
			trace = append(trace, TraceEvent{Stage: "model/invalid_tool_call", Message: "Оборванный JSON возвращён модели для одного исправления.", Tool: broken.Function.Name, ToolCallID: broken.ID, Result: payload})
			messages = append(messages, Message{Role: "assistant", ToolCalls: []ToolCall{broken}}, Message{Role: "tool", ToolCallID: broken.ID, Content: mustJSON(payload)})
			continue
		}
		choice.Message.Role = "assistant"
		messages = append(messages, choice.Message)
		for i, c := range choice.Message.ToolCalls {
			args := decoded[i]
			if err := state.validate(c.Function.Name, args); err != nil {
				var se *schemaError
				var de *dependencyError
				if errors.As(err, &se) && !badSchema {
					badSchema = true
					payload := map[string]any{"code": "invalid_argument", "message": err.Error()}
					trace = append(trace, TraceEvent{Stage: "agent/argument_error", Message: "Строгая схема отклонила аргументы; разрешён один повтор.", Tool: c.Function.Name, ToolCallID: c.ID, Arguments: args, Result: payload})
					messages = append(messages, Message{Role: "tool", ToolCallID: c.ID, Content: mustJSON(payload)})
					continue
				}
				if errors.As(err, &de) && !badDependency {
					badDependency = true
					payload := map[string]any{"code": "invalid_dependency", "message": err.Error(), "requirement": de.requirement}
					owner, _ := s.Owner(c.Function.Name)
					trace = append(trace, TraceEvent{Stage: "agent/dependency_error", Message: "Для инструмента не хватает точного результата-зависимости; модель сама выбирает, как исправить flow.", Tool: c.Function.Name, ToolCallID: c.ID, Arguments: args, Result: payload, MCPServer: owner})
					messages = append(messages, Message{Role: "tool", ToolCallID: c.ID, Content: mustJSON(payload)})
					continue
				}
				return Response{}, err
			}
			owner, _ := s.Owner(c.Function.Name)
			trace = append(trace, TraceEvent{Stage: "model/tool_call", Message: "Модель выбрала следующий инструмент.", Tool: c.Function.Name, ToolCallID: c.ID, Arguments: args, MCPServer: owner, Transition: state.transition(c.Function.Name)})
			key := toolCacheKey(c.Function.Name, args)
			if cached, ok := cache[key]; ok && isCacheable(c.Function.Name) {
				trace = append(trace, TraceEvent{Stage: "agent/cache_hit", Message: "Идентичный read-only вызов уже выполнен; модели возвращён сохранённый structured result с новым tool_call_id.", Tool: c.Function.Name, ToolCallID: c.ID, Arguments: args, Result: cached.public, MCPServer: owner})
				messages = append(messages, Message{Role: "tool", ToolCallID: c.ID, Content: cached.content})
				if err := state.accept(c.Function.Name, cached.public); err != nil {
					return Response{}, err
				}
				continue
			}
			if calls >= a.cfg.MaxToolCalls {
				payload := map[string]any{"code": "tool_call_budget_exhausted", "message": "Лимит реальных MCP-вызовов исчерпан; сформируй финальный ответ по уже полученным результатам.", "executed_calls": calls, "max_calls": a.cfg.MaxToolCalls}
				trace = append(trace, TraceEvent{Stage: "agent/tool_budget", Message: "Новый MCP-вызов не выполнен; модель переведена в режим финального ответа без tools.", Tool: c.Function.Name, ToolCallID: c.ID, Arguments: args, Result: payload, MCPServer: owner})
				messages = append(messages, Message{Role: "tool", ToolCallID: c.ID, Content: mustJSON(payload)})
				forceFinal = true
				continue
			}
			calls++
			out, err := s.CallTool(ctx, c.Function.Name, args)
			if err != nil {
				return Response{}, err
			}
			public := out.Structured
			if public == nil {
				public = out.Text
			}
			content := mustJSON(public)
			stage := "mcp/tools/call"
			if out.IsError {
				stage = "mcp/tool_error"
			}
			trace = append(trace, TraceEvent{Stage: stage, Message: "Structured result возвращён модели с исходным tool_call_id.", Tool: c.Function.Name, ToolCallID: c.ID, Result: public, MCPServer: owner})
			messages = append(messages, Message{Role: "tool", ToolCallID: c.ID, Content: content})
			if !out.IsError {
				if isCacheable(c.Function.Name) {
					cache[key] = cachedResult{public: public, content: content}
				}
				if err := state.accept(c.Function.Name, public); err != nil {
					return Response{}, err
				}
			}
		}
	}
	return Response{}, errors.New("agent loop ended")
}

type schemaError struct{ err error }

func (e *schemaError) Error() string { return e.err.Error() }

type cachedResult struct {
	public  any
	content string
}

func toolCacheKey(name string, arguments map[string]any) string {
	encoded, _ := json.Marshal(arguments)
	return name + "\x00" + string(encoded)
}

func isCacheable(name string) bool { return name != artifact.SaveTool }

type dependencyError struct {
	requirement string
}

func (e *dependencyError) Error() string {
	return "missing or changed tool dependency: " + e.requirement
}

type flowState struct {
	searches   []domain.SearchOutput
	summaries  []domain.SummarizeOutput
	repos      []domain.RepositoryOutput
	releases   []domain.ReleaseOutput
	reports    []domain.ReportOutput
	lastServer string
}

func (s *flowState) transition(name string) string {
	current := serverName(name)
	if s.lastServer == "" || s.lastServer == current {
		return current
	}
	return s.lastServer + " → " + current
}

func serverName(name string) string {
	switch name {
	case knowledge.SearchTool, knowledge.SummarizeTool:
		return "Knowledge MCP"
	case githubapi.RepositoryTool, githubapi.ReleaseTool:
		return "GitHub MCP"
	default:
		return "Artifact MCP"
	}
}
func (s *flowState) validate(name string, a map[string]any) error {
	switch name {
	case knowledge.SummarizeTool:
		var in domain.SummarizeInput
		if err := strict(a, &in); err != nil {
			return &schemaError{err}
		}
		if !matchesSearch(s.searches, in) {
			return &dependencyError{requirement: "knowledge_summarize must receive the exact query and documents from any completed knowledge_search"}
		}
	case githubapi.ReleaseTool:
		var in domain.RepositoryInput
		if err := strict(a, &in); err != nil {
			return &schemaError{err}
		}
	case artifact.BuildTool:
		var in domain.ReportInput
		if err := strict(a, &in); err != nil {
			return &schemaError{err}
		}
		if !contains(s.summaries, in.Knowledge) || !reflect.DeepEqual(in.KnowledgeSources, in.Knowledge.Sources) {
			return &dependencyError{requirement: "artifact_build_report knowledge and knowledge_sources must exactly match a completed knowledge_summarize result"}
		}
		if !contains(s.repos, in.Repository) {
			return &dependencyError{requirement: "artifact_build_report repository must exactly match a completed github_get_repository result"}
		}
		if !contains(s.releases, in.LatestRelease) {
			return &dependencyError{requirement: "artifact_build_report latest_release must exactly match a completed github_get_latest_release result"}
		}
	case artifact.SaveTool:
		var in domain.SaveInput
		if err := strict(a, &in); err != nil {
			return &schemaError{err}
		}
		if !matchesReport(s.reports, in) {
			return &dependencyError{requirement: "artifact_save_to_file must receive exact Markdown and sources from a completed artifact_build_report"}
		}
	}
	s.lastServer = serverName(name)
	return nil
}
func (s *flowState) accept(name string, v any) error {
	switch name {
	case knowledge.SearchTool:
		var out domain.SearchOutput
		if err := strict(v, &out); err != nil {
			return err
		}
		s.searches = append(s.searches, out)
	case knowledge.SummarizeTool:
		var out domain.SummarizeOutput
		if err := strict(v, &out); err != nil {
			return err
		}
		s.summaries = append(s.summaries, out)
	case githubapi.RepositoryTool:
		var out domain.RepositoryOutput
		if err := strict(v, &out); err != nil {
			return err
		}
		s.repos = append(s.repos, out)
	case githubapi.ReleaseTool:
		var out domain.ReleaseOutput
		if err := strict(v, &out); err != nil {
			return err
		}
		s.releases = append(s.releases, out)
	case artifact.BuildTool:
		var out domain.ReportOutput
		if err := strict(v, &out); err != nil {
			return err
		}
		s.reports = append(s.reports, out)
	case artifact.SaveTool:
		var out domain.SaveOutput
		if err := strict(v, &out); err != nil {
			return err
		}
		if !savedHashMatches(s.reports, out) {
			return errors.New("saved SHA-256 differs from built Markdown")
		}
	}
	return nil
}

func matchesSearch(searches []domain.SearchOutput, input domain.SummarizeInput) bool {
	for _, search := range searches {
		if input.Query == search.Query && reflect.DeepEqual(input.Documents, search.Documents) {
			return true
		}
	}
	return false
}

func contains[T any](values []T, target T) bool {
	for _, value := range values {
		if reflect.DeepEqual(value, target) {
			return true
		}
	}
	return false
}

func matchesReport(reports []domain.ReportOutput, input domain.SaveInput) bool {
	for _, report := range reports {
		if input.Markdown == report.Markdown && reflect.DeepEqual(input.Sources, report.Sources) {
			return true
		}
	}
	return false
}

func savedHashMatches(reports []domain.ReportOutput, saved domain.SaveOutput) bool {
	for _, report := range reports {
		if report.SHA256 == saved.SHA256 && reflect.DeepEqual(report.Sources, saved.Sources) {
			return true
		}
	}
	return false
}
func strict(v, target any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	return d.Decode(target)
}
func decode(raw string) (map[string]any, error) {
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	var v map[string]any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	var x any
	if err := d.Decode(&x); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return v, nil
}
func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
