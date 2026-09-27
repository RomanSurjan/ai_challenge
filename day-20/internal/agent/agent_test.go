package agent

import (
	"ai-challenge/day-20/internal/artifact"
	"ai-challenge/day-20/internal/domain"
	"ai-challenge/day-20/internal/githubapi"
	"ai-challenge/day-20/internal/knowledge"
	"ai-challenge/day-20/internal/mcpclient"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"
)

type fakeConnector struct{ s *fakeSession }

func (c fakeConnector) Connect(context.Context) (mcpclient.Session, error) { return c.s, nil }

type fakeSession struct {
	docs    []domain.Document
	summary domain.SummarizeOutput
	repo    domain.RepositoryOutput
	release domain.ReleaseOutput
	report  domain.ReportOutput
	calls   map[string]int
}

func (f *fakeSession) Close() error { return nil }
func (f *fakeSession) Owner(n string) (string, bool) {
	switch n {
	case knowledge.SearchTool, knowledge.SummarizeTool:
		return "Knowledge MCP", true
	case githubapi.RepositoryTool, githubapi.ReleaseTool:
		return "GitHub MCP", true
	default:
		return "Artifact MCP", true
	}
}
func (f *fakeSession) ListTools(context.Context) ([]mcpclient.Tool, error) {
	names := []string{knowledge.SearchTool, knowledge.SummarizeTool, githubapi.RepositoryTool, githubapi.ReleaseTool, artifact.BuildTool, artifact.SaveTool, artifact.ListTool, artifact.ReadTool}
	out := make([]mcpclient.Tool, len(names))
	for i, n := range names {
		owner, _ := f.Owner(n)
		out[i] = mcpclient.Tool{Name: n, Description: n, InputSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}}, MCPServer: owner, Endpoint: "memory://" + owner}
	}
	return out, nil
}
func (f *fakeSession) CallTool(_ context.Context, n string, _ map[string]any) (mcpclient.ToolResult, error) {
	if f.calls == nil {
		f.calls = make(map[string]int)
	}
	f.calls[n]++
	var v any
	switch n {
	case knowledge.SearchTool:
		v = domain.SearchOutput{OK: true, Query: "MCP", Documents: f.docs, Count: len(f.docs)}
	case knowledge.SummarizeTool:
		v = f.summary
	case githubapi.RepositoryTool:
		v = f.repo
	case githubapi.ReleaseTool:
		v = f.release
	case artifact.BuildTool:
		v = f.report
	case artifact.SaveTool:
		v = domain.SaveOutput{OK: true, Path: "/data/outputs/report.md", Filename: "report.md", SHA256: f.report.SHA256, Sources: f.report.Sources}
	}
	return mcpclient.ToolResult{Structured: v}, nil
}

type scripted struct {
	step               int
	f                  *fakeSession
	requests           []ChatRequest
	repeatSearchOnce   bool
	repeatedSearchSent bool
}

func (s *scripted) Complete(_ context.Context, r ChatRequest) (ChatResponse, error) {
	s.requests = append(s.requests, r)
	if s.step == 1 && s.repeatSearchOnce && !s.repeatedSearchSent {
		s.repeatedSearchSent = true
		b, _ := json.Marshal(domain.SearchInput{Query: "MCP", Language: "en", Limit: 1})
		return ChatResponse{Choices: []Choice{{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-repeated-search", Type: "function", Function: ToolCallFunction{Name: knowledge.SearchTool, Arguments: string(b)}}}}, FinishReason: "tool_calls"}}}, nil
	}
	if s.step == 6 {
		s.step++
		return ChatResponse{Choices: []Choice{{Message: Message{Role: "assistant", Content: "done"}, FinishReason: "stop"}}}, nil
	}
	names := []string{knowledge.SearchTool, knowledge.SummarizeTool, githubapi.RepositoryTool, githubapi.ReleaseTool, artifact.BuildTool, artifact.SaveTool}
	args := []any{domain.SearchInput{Query: "MCP", Language: "en", Limit: 1}, domain.SummarizeInput{Query: "MCP", Documents: s.f.docs, MaxSentences: 2}, domain.RepositoryInput{Owner: "modelcontextprotocol", Repository: "go-sdk"}, domain.RepositoryInput{Owner: "modelcontextprotocol", Repository: "go-sdk"}, domain.ReportInput{Topic: "MCP", Title: "Report", Knowledge: s.f.summary, KnowledgeSources: s.f.summary.Sources, Repository: s.f.repo, LatestRelease: s.f.release}, domain.SaveInput{Filename: "report.md", Markdown: s.f.report.Markdown, Sources: s.f.report.Sources}}
	b, _ := json.Marshal(args[s.step])
	id := "call-" + names[s.step]
	resp := ChatResponse{Choices: []Choice{{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: id, Type: "function", Function: ToolCallFunction{Name: names[s.step], Arguments: string(b)}}}}, FinishReason: "tool_calls"}}}
	s.step++
	return resp, nil
}

func TestAgentAllowsModelToRepeatSearchBeforeSummarize(t *testing.T) {
	docs := []domain.Document{{Title: "MCP", URL: "https://example.com", Text: "MCP connects tools."}}
	summary := domain.SummarizeOutput{OK: true, Query: "MCP", Summary: "MCP connects tools.", Sources: []domain.Source{{Title: "MCP", URL: "https://example.com"}}, InputDocuments: 1, SentenceCount: 1}
	repo := domain.RepositoryOutput{OK: true, Owner: "modelcontextprotocol", Repository: "go-sdk", FullName: "modelcontextprotocol/go-sdk", URL: "https://github.com/modelcontextprotocol/go-sdk", Stars: 1}
	release := domain.ReleaseOutput{OK: true, Owner: "modelcontextprotocol", Repository: "go-sdk", Found: false}
	report, _ := artifact.Build(domain.ReportInput{Topic: "MCP", Title: "Report", Knowledge: summary, KnowledgeSources: summary.Sources, Repository: repo, LatestRelease: release})
	fs := &fakeSession{docs: docs, summary: summary, repo: repo, release: release, report: report}
	llm := &scripted{f: fs, repeatSearchOnce: true}
	out, err := New(Config{Timeout: 90 * time.Second}, llm, fakeConnector{fs}).Ask(context.Background(), "full")
	if err != nil {
		t.Fatal(err)
	}
	if !llm.repeatedSearchSent || out.Content != "done" {
		t.Fatalf("recovery did not complete: repeated=%v content=%q", llm.repeatedSearchSent, out.Content)
	}
	searchCalls := 0
	for _, event := range out.Trace {
		if event.Stage == "model/tool_call" && event.Tool == knowledge.SearchTool {
			searchCalls++
		}
		if event.Stage == "agent/dependency_error" {
			t.Fatalf("repeated independent search must not be rejected: %+v", event)
		}
	}
	if searchCalls != 2 {
		t.Fatalf("search calls = %d, want 2", searchCalls)
	}
	if fs.calls[knowledge.SearchTool] != 1 {
		t.Fatalf("real MCP searches = %d, want one plus one cache hit", fs.calls[knowledge.SearchTool])
	}
}

type budgetLLM struct{ attempts int }

func (b *budgetLLM) Complete(_ context.Context, request ChatRequest) (ChatResponse, error) {
	if len(request.Tools) == 0 {
		return ChatResponse{Choices: []Choice{{Message: Message{Role: "assistant", Content: "partial final answer"}, FinishReason: "stop"}}}, nil
	}
	b.attempts++
	arguments, _ := json.Marshal(domain.SearchInput{Query: fmt.Sprintf("query-%d", b.attempts), Language: "en", Limit: 1})
	return ChatResponse{Choices: []Choice{{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: fmt.Sprintf("budget-%d", b.attempts), Type: "function", Function: ToolCallFunction{Name: knowledge.SearchTool, Arguments: string(arguments)}}}}, FinishReason: "tool_calls"}}}, nil
}

func TestToolBudgetProducesFinalAnswerInsteadOfFatalError(t *testing.T) {
	fs := &fakeSession{docs: []domain.Document{{Title: "MCP", URL: "https://example.com", Text: "MCP connects tools."}}}
	llm := &budgetLLM{}
	out, err := New(Config{Timeout: 90 * time.Second, MaxToolCalls: 2, MaxToolRounds: 6}, llm, fakeConnector{fs}).Ask(context.Background(), "keep searching")
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "partial final answer" || fs.calls[knowledge.SearchTool] != 2 {
		t.Fatalf("content=%q calls=%d", out.Content, fs.calls[knowledge.SearchTool])
	}
	found := false
	for _, event := range out.Trace {
		if event.Stage == "agent/tool_budget" && event.ToolCallID == "budget-3" {
			found = true
		}
	}
	if !found {
		t.Fatal("tool budget event missing from trace")
	}
}

type callSpec struct {
	name string
	args any
}

type sequenceLLM struct {
	calls []callSpec
	step  int
}

func (s *sequenceLLM) Complete(_ context.Context, _ ChatRequest) (ChatResponse, error) {
	if s.step == len(s.calls) {
		return ChatResponse{Choices: []Choice{{Message: Message{Role: "assistant", Content: "done"}, FinishReason: "stop"}}}, nil
	}
	call := s.calls[s.step]
	s.step++
	encoded, _ := json.Marshal(call.args)
	return ChatResponse{Choices: []Choice{{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "chosen-" + call.name, Type: "function", Function: ToolCallFunction{Name: call.name, Arguments: string(encoded)}}}}, FinishReason: "tool_calls"}}}, nil
}

func TestModelMayChooseIndependentServersInDifferentOrder(t *testing.T) {
	docs := []domain.Document{{Title: "MCP", URL: "https://example.com", Text: "MCP connects tools."}}
	summary := domain.SummarizeOutput{OK: true, Query: "MCP", Summary: "MCP connects tools.", Sources: []domain.Source{{Title: "MCP", URL: "https://example.com"}}, InputDocuments: 1, SentenceCount: 1}
	repo := domain.RepositoryOutput{OK: true, Owner: "modelcontextprotocol", Repository: "go-sdk", FullName: "modelcontextprotocol/go-sdk", URL: "https://github.com/modelcontextprotocol/go-sdk", Stars: 1}
	release := domain.ReleaseOutput{OK: true, Owner: "modelcontextprotocol", Repository: "go-sdk", Found: false}
	reportInput := domain.ReportInput{Topic: "MCP", Title: "Report", Knowledge: summary, KnowledgeSources: summary.Sources, Repository: repo, LatestRelease: release}
	report, _ := artifact.Build(reportInput)
	fs := &fakeSession{docs: docs, summary: summary, repo: repo, release: release, report: report}
	llm := &sequenceLLM{calls: []callSpec{
		{name: githubapi.RepositoryTool, args: domain.RepositoryInput{Owner: "modelcontextprotocol", Repository: "go-sdk"}},
		{name: githubapi.ReleaseTool, args: domain.RepositoryInput{Owner: "modelcontextprotocol", Repository: "go-sdk"}},
		{name: knowledge.SearchTool, args: domain.SearchInput{Query: "MCP", Language: "en", Limit: 1}},
		{name: knowledge.SummarizeTool, args: domain.SummarizeInput{Query: "MCP", Documents: docs, MaxSentences: 2}},
		{name: artifact.BuildTool, args: reportInput},
		{name: artifact.SaveTool, args: domain.SaveInput{Filename: "report.md", Markdown: report.Markdown, Sources: report.Sources}},
	}}
	out, err := New(Config{Timeout: 90 * time.Second}, llm, fakeConnector{fs}).Ask(context.Background(), "full")
	if err != nil || out.Content != "done" {
		t.Fatalf("model-selected order failed: content=%q err=%v", out.Content, err)
	}
	want := []string{githubapi.RepositoryTool, githubapi.ReleaseTool, knowledge.SearchTool, knowledge.SummarizeTool, artifact.BuildTool, artifact.SaveTool}
	var got []string
	for _, event := range out.Trace {
		if event.Stage == "model/tool_call" {
			got = append(got, event.Tool)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order=%v want=%v", got, want)
	}
}
func TestModelSelectedSixStepFlowAndExactTransfer(t *testing.T) {
	docs := []domain.Document{{Title: "MCP", URL: "https://example.com", Text: "MCP connects tools."}}
	summary := domain.SummarizeOutput{OK: true, Query: "MCP", Summary: "MCP connects tools.", Sources: []domain.Source{{Title: "MCP", URL: "https://example.com"}}, InputDocuments: 1, SentenceCount: 1}
	repo := domain.RepositoryOutput{OK: true, Owner: "modelcontextprotocol", Repository: "go-sdk", FullName: "modelcontextprotocol/go-sdk", URL: "https://github.com/modelcontextprotocol/go-sdk", Stars: 1}
	release := domain.ReleaseOutput{OK: true, Owner: "modelcontextprotocol", Repository: "go-sdk", Found: false}
	report, _ := artifact.Build(domain.ReportInput{Topic: "MCP", Title: "Report", Knowledge: summary, KnowledgeSources: summary.Sources, Repository: repo, LatestRelease: release})
	fs := &fakeSession{docs: docs, summary: summary, repo: repo, release: release, report: report}
	llm := &scripted{f: fs}
	a := New(Config{Timeout: 90 * time.Second}, llm, fakeConnector{fs})
	out, err := a.Ask(context.Background(), "full")
	if err != nil {
		t.Fatal(err)
	}
	var order, ids, owners []string
	for _, e := range out.Trace {
		if e.Stage == "model/tool_call" {
			order = append(order, e.Tool)
			ids = append(ids, e.ToolCallID)
			owners = append(owners, e.MCPServer)
		}
	}
	want := []string{knowledge.SearchTool, knowledge.SummarizeTool, githubapi.RepositoryTool, githubapi.ReleaseTool, artifact.BuildTool, artifact.SaveTool}
	if !reflect.DeepEqual(order, want) || len(ids) != 6 || !reflect.DeepEqual(owners, []string{"Knowledge MCP", "Knowledge MCP", "GitHub MCP", "GitHub MCP", "Artifact MCP", "Artifact MCP"}) {
		t.Fatalf("order=%v ids=%v owners=%v", order, ids, owners)
	}
	for _, r := range llm.requests {
		if r.ToolChoice != "auto" {
			t.Fatal("tool_choice not auto")
		}
	}
}
