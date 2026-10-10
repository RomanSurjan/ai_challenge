package generation

import (
	"context"
	"testing"
)

func TestExactBudgetAndFallback(t *testing.T) {
	s := DefaultSettings()
	s.ContextWindow = 8192
	count, method, _, e := Budget(context.Background(), DefaultModel, SystemPrompt, "очень длинный текст", s, func(context.Context, string, string, string) (int, error) { return 8000, nil })
	if e == nil || method != "exact_loaded_runner_chatml" || count != 8000 {
		t.Fatal("did not reserve answer budget")
	}
	s.MaxTokens = 100
	_, _, _, e = Budget(context.Background(), DefaultModel, SystemPrompt, "text", s, func(context.Context, string, string, string) (int, error) { return 8000, nil })
	if e != nil {
		t.Fatal(e)
	}
	if ChatML("sys", "question") != "<|im_start|>system\nsys<|im_end|>\n<|im_start|>user\nquestion<|im_end|>\n<|im_start|>assistant\n" {
		t.Fatal("ChatML mismatch")
	}
}
