package judging

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ai-challenge/day-24/internal/evidence"
	"ai-challenge/day-24/internal/generation"
)

type fakeGenerator struct {
	text     string
	err      error
	prompt   string
	settings generation.Settings
}

func (f *fakeGenerator) Generate(_ context.Context, _ string, prompt string, settings generation.Settings) (generation.Response, error) {
	f.prompt = prompt
	f.settings = settings
	if f.err != nil {
		return generation.Response{}, f.err
	}
	return generation.Response{Text: f.text, Usage: generation.Usage{PromptTokens: 3, CompletionTokens: 2}}, nil
}
func request() Request {
	return Request{Question: "Как?", Claim: evidence.Claim{ID: "C1", Text: "Факт"}, Quotes: []evidence.Citation{{SourceID: "S1", ClaimIDs: []string{"C1"}, Quote: "Достаточно длинная точная цитата для проверки утверждения"}}}
}

func TestJudgeSuccessAndPromptIsolation(t *testing.T) {
	gen := &fakeGenerator{text: `{"verdict":"supported","reason":"цитата прямо подтверждает факт"}`}
	got := NewOllama(gen, "model", 64).Judge(context.Background(), request())
	if got.Verdict != Supported || got.Error != "" || got.Usage.PromptTokens != 3 || !gen.settings.JSON {
		t.Fatalf("result=%+v", got)
	}
	for _, forbidden := range []string{"expected_answer", "required_concepts", "expected_sources"} {
		if strings.Contains(gen.prompt, forbidden) {
			t.Fatalf("prompt leaks %s", forbidden)
		}
	}
}
func TestJudgeMalformedOrErrorIsUnverifiable(t *testing.T) {
	for _, gen := range []*fakeGenerator{{text: "bad"}, {err: errors.New("offline")}, {text: `{"verdict":"yes","reason":"x"}`}} {
		got := NewOllama(gen, "model", 64).Judge(context.Background(), request())
		if got.Verdict != Unverifiable || got.Error == "" {
			t.Fatalf("result=%+v", got)
		}
	}
}
