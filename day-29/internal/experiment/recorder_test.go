package experiment

import (
	"ai-challenge/day-29/internal/generation"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecorderActualPayload(t *testing.T) {
	seen := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		if e := json.NewDecoder(r.Body).Decode(&p); e != nil {
			t.Fatal(e)
		}
		o := p["options"].(map[string]any)
		if o["temperature"] != .2 || o["num_predict"] != float64(768) || o["num_ctx"] != float64(8192) || o["seed"] != float64(41) {
			t.Errorf("wrong settings: %v", o)
		}
		seen = true
		json.NewEncoder(w).Encode(map[string]any{"model": "qwen2.5:3b", "message": map[string]string{"content": "{}"}, "eval_count": 10, "eval_duration": 1000000000, "prompt_eval_count": 9, "done_reason": "stop"})
	}))
	defer srv.Close()
	rec := Recorder{Runtime: &Runtime{Base: srv.URL, Client: srv.Client()}}
	s := generation.DefaultSettings()
	s.Temperature = .2
	s.MaxTokens = 768
	s.ContextWindow = 8192
	s.Seed = 41
	_, e := rec.Generate(context.Background(), "qwen2.5:3b", "prompt", s)
	if e != nil || !seen || len(rec.Calls) != 1 || rec.Calls[0].TokensPerSecond != 10 {
		t.Fatalf("payload/measurement failed: %v %+v", e, rec.Calls)
	}
	s.ContextWindow = 10
	_, e = rec.Generate(context.Background(), "qwen2.5:3b", "prompt", s)
	if e == nil {
		t.Fatal("must reject undersized context")
	}
}
func TestProfileSelection(t *testing.T) {
	p, e := LoadProfile("../../configs/baseline.json")
	if e != nil || p.PromptVersion != "baseline-v1" || p.MaxTokens != 512 || p.ContextWindow != 32768 {
		t.Fatalf("baseline drift: %+v %v", p, e)
	}
	q, e := LoadProfile("../../configs/selected.json")
	if e != nil || q.Settings().PromptVersion != q.PromptVersion {
		t.Fatal("selected config not applied")
	}
}
