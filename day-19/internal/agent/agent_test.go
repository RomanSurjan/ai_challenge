package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"ai-challenge/day-19/internal/mcpclient"
	"ai-challenge/day-19/internal/pipeline"
)

type scriptedLLM struct {
	t           *testing.T
	step        int
	documents   []pipeline.Document
	summary     pipeline.SummarizeOutput
	requests    []ChatRequest
	corrupt     bool
	invalidOnce bool
	invalidSent bool
	unknownOnce bool
	unknownSent bool
}

func (f *scriptedLLM) Complete(_ context.Context, request ChatRequest) (ChatResponse, error) {
	f.requests = append(f.requests, request)
	if request.ToolChoice != "auto" {
		f.t.Fatalf("tool_choice=%q", request.ToolChoice)
	}
	if len(request.Tools) != 3 {
		f.t.Fatalf("got %d tools", len(request.Tools))
	}
	call := func(id, name string, args any) ChatResponse {
		encoded, _ := json.Marshal(args)
		return ChatResponse{Choices: []Choice{{
			Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
				ID: id, Type: "function", Function: ToolCallFunction{Name: name, Arguments: string(encoded)},
			}}},
			FinishReason: "tool_calls",
		}}}
	}
	switch f.step {
	case 0:
		f.step++
		return call("call-search", pipeline.SearchToolName, map[string]any{"query": "Model Context Protocol", "language": "en", "limit": 2}), nil
	case 1:
		if f.unknownSent {
			assertLastToolID(f.t, request.Messages, "call-summary-unknown")
		} else {
			assertLastToolID(f.t, request.Messages, "call-search")
		}
		if f.invalidOnce && !f.invalidSent {
			f.invalidSent = true
			return ChatResponse{Choices: []Choice{{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-summary-broken", Type: "function", Function: ToolCallFunction{Name: pipeline.SummarizeToolName, Arguments: `{"query":"Model Context Protocol","documents":[`}}}}, FinishReason: "length"}}}, nil
		}
		if f.unknownOnce && !f.unknownSent {
			f.unknownSent = true
			documents := make([]map[string]any, len(f.documents))
			for i, document := range f.documents {
				documents[i] = map[string]any{"title": document.Title, "url": document.URL, "text": document.Text}
			}
			documents[0]["url_note"] = "for display only"
			return call("call-summary-unknown", pipeline.SummarizeToolName, map[string]any{"query": "Model Context Protocol", "documents": documents, "max_sentences": 2}), nil
		}
		docs := f.documents
		if f.corrupt {
			docs = []pipeline.Document{}
		}
		f.step++
		return call("call-summary", pipeline.SummarizeToolName, pipeline.SummarizeInput{Query: "Model Context Protocol", Documents: docs, MaxSentences: 2}), nil
	case 2:
		assertLastToolID(f.t, request.Messages, "call-summary")
		f.step++
		return call("call-save", pipeline.SaveToolName, pipeline.SaveInput{Filename: "mcp-summary.md", Query: f.summary.Query, Content: f.summary.Summary, Sources: f.summary.Sources}), nil
	case 3:
		assertLastToolID(f.t, request.Messages, "call-save")
		f.step++
		return ChatResponse{Choices: []Choice{{Message: Message{Role: "assistant", Content: "Готово: файл сохранён, путь и SHA-256 подтверждены."}, FinishReason: "stop"}}}, nil
	default:
		return ChatResponse{}, fmt.Errorf("unexpected model call")
	}
}

func assertLastToolID(t *testing.T, messages []Message, want string) {
	t.Helper()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "tool" {
			if messages[i].ToolCallID != want {
				t.Fatalf("last tool message=%+v, want id %s", messages[i], want)
			}
			return
		}
	}
	t.Fatalf("no tool message found; want id %s", want)
}

func testPipeline(t *testing.T, corrupt bool) (*Agent, *scriptedLLM, string) {
	t.Helper()
	docs := []pipeline.Document{{Title: "Model Context Protocol", URL: "https://en.wikipedia.org/wiki/Model_Context_Protocol", Text: "Model Context Protocol is an open standard. It connects AI applications to external data sources and tools. Another fact appears here."}}
	wikiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"query":{"pages":[{"title":"Model Context Protocol","fullurl":"https://en.wikipedia.org/wiki/Model_Context_Protocol","extract":"Model Context Protocol is an open standard. It connects AI applications to external data sources and tools. Another fact appears here."}]}}`))
	}))
	t.Cleanup(wikiServer.Close)
	wiki := pipeline.NewWikipediaClient(wikiServer.Client())
	wiki.BaseURL = wikiServer.URL
	wiki.Now = func() time.Time { return time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC) }
	expectedSummary, err := pipeline.Summarize(pipeline.SummarizeInput{Query: "Model Context Protocol", Documents: docs, MaxSentences: 2})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	saver := pipeline.NewSaver(dir)
	saver.Now = func() time.Time { return time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC) }
	llm := &scriptedLLM{t: t, documents: docs, summary: expectedSummary, corrupt: corrupt}
	connector := mcpclient.InMemoryConnector{Server: pipeline.NewServer(wiki, saver)}
	return New(Config{Timeout: 75 * time.Second}, llm, connector), llm, dir
}

func TestAgentModelSelectedPipelineAndExactTransfer(t *testing.T) {
	a, llm, dir := testPipeline(t, false)
	result, err := a.Ask(context.Background(), "Найди MCP, сделай сводку и сохрани.")
	if err != nil {
		t.Fatal(err)
	}
	if llm.step != 4 || result.Content == "" {
		t.Fatalf("incomplete loop: step=%d response=%+v", llm.step, result)
	}
	var order, ids []string
	for _, event := range result.Trace {
		if event.Stage == "model/tool_call" {
			order = append(order, event.Tool)
			ids = append(ids, event.ToolCallID)
		}
	}
	if !reflect.DeepEqual(order, []string{pipeline.SearchToolName, pipeline.SummarizeToolName, pipeline.SaveToolName}) {
		t.Fatalf("order=%v", order)
	}
	if !reflect.DeepEqual(ids, []string{"call-search", "call-summary", "call-save"}) {
		t.Fatalf("ids=%v", ids)
	}
	path := filepath.Join(dir, "mcp-summary.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	var saved pipeline.SaveOutput
	for _, event := range result.Trace {
		if event.Stage == "mcp/tools/call" && event.Tool == pipeline.SaveToolName {
			encoded, _ := json.Marshal(event.Result)
			if err := json.Unmarshal(encoded, &saved); err != nil {
				t.Fatal(err)
			}
		}
	}
	if saved.SHA256 != hash || saved.Path != path {
		t.Fatalf("saved=%+v hash=%s", saved, hash)
	}
	// The summary result and save arguments must be byte-for-byte equal.
	var summary string
	var saveContent string
	for _, event := range result.Trace {
		if event.Stage == "mcp/tools/call" && event.Tool == pipeline.SummarizeToolName {
			encoded, _ := json.Marshal(event.Result)
			var out pipeline.SummarizeOutput
			_ = json.Unmarshal(encoded, &out)
			summary = out.Summary
		}
		if event.Stage == "model/tool_call" && event.Tool == pipeline.SaveToolName {
			saveContent, _ = event.Arguments["content"].(string)
		}
	}
	if summary == "" || saveContent != summary {
		t.Fatalf("summary transfer changed: %q != %q", saveContent, summary)
	}
}

func TestAgentRejectsChangedDataBeforeNextTool(t *testing.T) {
	a, _, _ := testPipeline(t, true)
	_, err := a.Ask(context.Background(), "pipeline")
	if err == nil || !strings.Contains(err.Error(), "exact query and documents") {
		t.Fatalf("err=%v", err)
	}
}

func TestAgentRetriesOneTruncatedToolCall(t *testing.T) {
	a, llm, _ := testPipeline(t, false)
	llm.invalidOnce = true
	result, err := a.Ask(context.Background(), "pipeline")
	if err != nil {
		t.Fatal(err)
	}
	if !llm.invalidSent || llm.step != 4 {
		t.Fatalf("retry was not completed: invalid=%v step=%d", llm.invalidSent, llm.step)
	}
	found := false
	for _, event := range result.Trace {
		if event.Stage == "model/invalid_tool_call" && event.Tool == pipeline.SummarizeToolName {
			found = true
		}
	}
	if !found {
		t.Fatal("invalid tool-call retry is missing from trace")
	}
}

func TestAgentReturnsUnknownFieldErrorForOneCorrectedAttempt(t *testing.T) {
	a, llm, _ := testPipeline(t, false)
	llm.unknownOnce = true
	result, err := a.Ask(context.Background(), "pipeline")
	if err != nil {
		t.Fatal(err)
	}
	if !llm.unknownSent || llm.step != 4 {
		t.Fatalf("schema correction was not completed: unknown=%v step=%d", llm.unknownSent, llm.step)
	}
	found := false
	for _, event := range result.Trace {
		if event.Stage == "agent/argument_error" && event.Tool == pipeline.SummarizeToolName && event.ToolCallID == "call-summary-unknown" {
			found = true
		}
	}
	if !found {
		t.Fatal("strict-schema correction is missing from trace")
	}
}

func TestAgentRequiresOrderAndCompletion(t *testing.T) {
	bad := &fixedLLM{responses: []ChatResponse{{Choices: []Choice{{Message: Message{ToolCalls: []ToolCall{{ID: "x", Type: "function", Function: ToolCallFunction{Name: pipeline.SaveToolName, Arguments: `{"filename":"x.md","content":"x","sources":[],"query":"x"}`}}}}}}}}}
	a, _, _ := testPipeline(t, false)
	a.llm = bad
	_, err := a.Ask(context.Background(), "pipeline")
	if err == nil || !strings.Contains(err.Error(), "invalid pipeline order") {
		t.Fatalf("err=%v", err)
	}
}

type fixedLLM struct {
	responses []ChatResponse
	index     int
}

func (f *fixedLLM) Complete(context.Context, ChatRequest) (ChatResponse, error) {
	r := f.responses[f.index]
	f.index++
	return r, nil
}
