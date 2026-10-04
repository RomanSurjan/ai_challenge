package rewriting

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ai-challenge/day-23/internal/generation"
)

type fakeGenerator struct {
	text     string
	err      error
	prompt   string
	settings generation.Settings
}

func (f *fakeGenerator) Generate(_ context.Context, _ string, p string, s generation.Settings) (generation.Response, error) {
	f.prompt = p
	f.settings = s
	if f.err != nil {
		return generation.Response{}, f.err
	}
	return generation.Response{Text: f.text, Usage: generation.Usage{PromptTokens: 4, CompletionTokens: 3}}, nil
}

func TestRewriteSuccessAndPromptIsolation(t *testing.T) {
	g := &fakeGenerator{text: `{"query":"MCP tool_call_id role tool"}`}
	r := NewOllama(g, "qwen", 32)
	got, err := r.Rewrite(context.Background(), "Как сохраняется tool_call_id?")
	if err != nil {
		t.Fatal(err)
	}
	if got.Query != "MCP tool_call_id role tool" || !got.Applied || got.Usage.CompletionTokens != 3 {
		t.Fatalf("got=%+v", got)
	}
	for _, forbidden := range []string{"expected_answer", "required_concepts", "expected_sources", "retrieved context"} {
		if strings.Contains(strings.ToLower(g.prompt), forbidden) {
			t.Fatalf("prompt leaked %q: %s", forbidden, g.prompt)
		}
	}
	if g.settings.Temperature != 0 || g.settings.MaxTokens != 32 || g.settings.System != SystemPrompt {
		t.Fatalf("settings=%+v", g.settings)
	}
}

func TestMalformedAndEmptyRewriteFallback(t *testing.T) {
	for _, text := range []string{"not json", `{"query":""}`, `{"query":"x","extra":1}`} {
		g := &fakeGenerator{text: text}
		r := NewOllama(g, "qwen", 32)
		raw, err := r.Rewrite(context.Background(), "original")
		if err == nil {
			t.Fatalf("accepted %q", text)
		}
		got := WithFallback("original", raw, err)
		if !got.Fallback || got.Query != "original" || got.Error == "" {
			t.Fatalf("fallback=%+v", got)
		}
	}
}

func TestModelErrorFallback(t *testing.T) {
	g := &fakeGenerator{err: errors.New("timeout")}
	r := NewOllama(g, "qwen", 32)
	raw, err := r.Rewrite(context.Background(), "original")
	got := WithFallback("original", raw, err)
	if !got.Fallback || !strings.Contains(got.Error, "timeout") {
		t.Fatalf("got=%+v", got)
	}
}

func TestSameRewriteIsValidButNotApplied(t *testing.T) {
	g := &fakeGenerator{text: `{"query":"same"}`}
	r := NewOllama(g, "qwen", 32)
	got, err := r.Rewrite(context.Background(), "same")
	if err != nil || got.Applied || got.Fallback {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}
