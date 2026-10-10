package experiment

import (
	"ai-challenge/day-29/internal/generation"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Recorder struct {
	Runtime *Runtime
	Calls   []Call
}

func (g *Recorder) Generate(ctx context.Context, model, prompt string, s generation.Settings) (generation.Response, error) {
	c := Call{RequestedModel: model, Messages: []Message{{"system", s.System}, {"user", prompt}}, InputUpperBound: generation.InputBound(s.System, prompt)}
	c.MessagesHash = Hash(c.Messages)
	c.SchemaHash = Hash(s.JSONSchema)
	c.Payload = map[string]any{"model": model, "messages": c.Messages, "stream": false, "think": false, "format": s.JSONSchema, "options": map[string]any{"temperature": s.Temperature, "num_predict": s.MaxTokens, "num_ctx": s.ContextWindow, "seed": s.Seed}}
	started := time.Now()
	n, method, counterErr, e := generation.Budget(ctx, model, s.System, prompt, s, g.Runtime.Tokenizer)
	c.InputTokens = n
	c.BudgetMethod = method
	c.TokenizerError = counterErr
	if e == nil {
		b, _ := json.Marshal(c.Payload)
		req, _ := http.NewRequestWithContext(ctx, "POST", g.Runtime.Base+"/api/chat", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		var resp *http.Response
		resp, e = g.Runtime.Client.Do(req)
		if e == nil {
			body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
			resp.Body.Close()
			e = err
			if json.Valid(body) {
				c.Raw = append(json.RawMessage(nil), body...)
			}
			if resp.StatusCode != 200 {
				e = fmt.Errorf("Ollama HTTP %d: %s", resp.StatusCode, string(body))
			}
			if e == nil {
				var v struct {
					Model    string  `json:"model"`
					Message  Message `json:"message"`
					Prompt   int     `json:"prompt_eval_count"`
					Count    int     `json:"eval_count"`
					Eval     int64   `json:"eval_duration"`
					Load     int64   `json:"load_duration"`
					PromptNS int64   `json:"prompt_eval_duration"`
					Reason   string  `json:"done_reason"`
					Error    string  `json:"error"`
				}
				e = json.Unmarshal(body, &v)
				c.ActualModel = v.Model
				c.PromptCount = v.Prompt
				c.EvalTokens = v.Count
				c.EvalNS = v.Eval
				c.LoadNS = v.Load
				c.PromptNS = v.PromptNS
				c.DoneReason = v.Reason
				if v.Eval > 0 {
					c.TokensPerSecond = float64(v.Count) * 1e9 / float64(v.Eval)
				}
				c.Response = generation.Response{Text: strings.TrimSpace(v.Message.Content), Usage: generation.Usage{PromptTokens: v.Prompt, CompletionTokens: v.Count}}
				if v.Error != "" {
					e = fmt.Errorf("%s", v.Error)
				}
				if c.BudgetMethod == "exact_loaded_runner_chatml" && v.Prompt != c.InputTokens {
					e = fmt.Errorf("tokenizer_mismatch: prompt_eval_count %d != exact tokenized input %d; possible truncation", v.Prompt, c.InputTokens)
				}
				if v.Prompt > c.InputUpperBound {
					e = fmt.Errorf("token budget calibration failed: actual exceeds upper bound")
				}
			}
		}
	}
	if e == nil && c.Response.Text == "" {
		e = fmt.Errorf("empty Ollama answer")
	}
	c.WallMS = time.Since(started).Milliseconds()
	if e != nil {
		c.Error = e.Error()
	}
	g.Calls = append(g.Calls, c)
	return c.Response, e
}
