package pipeline

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"ai-challenge/day-19/internal/mcpclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPContractAllTools(t *testing.T) {
	wikiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"query":{"pages":[]}}`)) }))
	defer wikiServer.Close()
	wiki := NewWikipediaClient(wikiServer.Client())
	wiki.BaseURL = wikiServer.URL
	connector := mcpclient.InMemoryConnector{Server: NewServer(wiki, NewSaver(t.TempDir()))}
	session, err := connector.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 3 {
		t.Fatalf("got %d tools", len(tools))
	}
	want := map[string]bool{SearchToolName: false, SummarizeToolName: false, SaveToolName: false}
	for _, tool := range tools {
		want[tool.Name] = true
		input, ok := tool.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("%s input schema type %T", tool.Name, tool.InputSchema)
		}
		if input["additionalProperties"] != false {
			t.Errorf("%s input additionalProperties", tool.Name)
		}
		if _, exists := input["required"]; !exists || input["required"] == nil {
			t.Errorf("%s invalid required", tool.Name)
		}
		output, ok := tool.OutputSchema.(map[string]any)
		if !ok || output["additionalProperties"] != false {
			t.Errorf("%s output schema not strict", tool.Name)
		}
		if tool.Annotations == nil {
			t.Errorf("%s annotations missing", tool.Name)
		}
		annotations, ok := tool.Annotations.(*mcp.ToolAnnotations)
		if !ok {
			t.Fatalf("%s annotations type %T", tool.Name, tool.Annotations)
		}
		if annotations.OpenWorldHint == nil {
			t.Errorf("%s openWorldHint missing", tool.Name)
		} else if tool.Name == SearchToolName && !*annotations.OpenWorldHint {
			t.Error("search must be open-world")
		} else if tool.Name != SearchToolName && *annotations.OpenWorldHint {
			t.Errorf("%s must be closed-world", tool.Name)
		}
		if (tool.Name != SaveToolName) != annotations.ReadOnlyHint {
			t.Errorf("%s readOnlyHint=%v", tool.Name, annotations.ReadOnlyHint)
		}
		assertStrictObjects(t, tool.Name+" input", input)
		assertStrictObjects(t, tool.Name+" output", output)
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing %s", name)
		}
	}
	search, err := session.CallTool(context.Background(), SearchToolName, map[string]any{"query": "x", "limit": 1})
	if err != nil || search.IsError {
		t.Fatalf("search call: %+v %v", search, err)
	}
	summary, err := session.CallTool(context.Background(), SummarizeToolName, map[string]any{"query": "x", "documents": []any{}, "max_sentences": 1})
	if err != nil || summary.IsError {
		t.Fatalf("summary call: %+v %v", summary, err)
	}
	saved, err := session.CallTool(context.Background(), SaveToolName, map[string]any{"filename": "x.md", "content": "summary", "sources": []any{}, "query": "x"})
	if err != nil || saved.IsError {
		t.Fatalf("save call: %+v %v", saved, err)
	}
}

func assertStrictObjects(t *testing.T, path string, schema any) {
	t.Helper()
	switch value := schema.(type) {
	case map[string]any:
		if value["type"] == "object" {
			if value["additionalProperties"] != false {
				t.Errorf("%s is not strict", path)
			}
			if required, exists := value["required"]; !exists || required == nil {
				t.Errorf("%s has missing/null required", path)
			}
		}
		for key, child := range value {
			assertStrictObjects(t, path+"/"+key, child)
		}
	case []any:
		for i, child := range value {
			assertStrictObjects(t, fmt.Sprintf("%s/%d", path, i), child)
		}
	}
}
