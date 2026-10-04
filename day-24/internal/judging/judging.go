package judging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"ai-challenge/day-24/internal/evidence"
	"ai-challenge/day-24/internal/generation"
)

const (
	Supported          = "supported"
	PartiallySupported = "partially_supported"
	Unsupported        = "unsupported"
	Unverifiable       = "unverifiable"
)

const SystemPrompt = `Ты проверяешь, подтверждают ли дословные цитаты одно утверждение. Не используй внешние знания. Верни только JSON: {"verdict":"supported|partially_supported|unsupported|unverifiable","reason":"краткая причина"}. supported означает, что цитаты полностью подтверждают claim; partially_supported — подтверждают лишь часть; unsupported — противоречат или не подтверждают; unverifiable — данных недостаточно.`

type Request struct {
	Question string
	Claim    evidence.Claim
	Quotes   []evidence.Citation
}

type Result struct {
	ClaimID   string           `json:"claim_id"`
	Verdict   string           `json:"verdict"`
	Reason    string           `json:"reason"`
	Error     string           `json:"error,omitempty"`
	LatencyMS int64            `json:"latency_ms"`
	Usage     generation.Usage `json:"usage"`
}

type Judge interface {
	Judge(ctx context.Context, request Request) Result
}

type Ollama struct {
	Generator generation.Generator
	Model     string
	Settings  generation.Settings
}

func NewOllama(generator generation.Generator, model string, maxTokens int) *Ollama {
	return &Ollama{Generator: generator, Model: model, Settings: generation.Settings{Temperature: 0, MaxTokens: maxTokens, System: SystemPrompt, JSON: true, JSONSchema: JSONSchema()}}
}

func JSONSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"verdict", "reason"},
		"properties": map[string]any{
			"verdict": map[string]any{"type": "string", "enum": []string{Supported, PartiallySupported, Unsupported, Unverifiable}},
			"reason":  map[string]any{"type": "string", "minLength": 1},
		},
	}
}

func Prompt(request Request) string {
	var b strings.Builder
	b.WriteString("Исходный вопрос:\n")
	b.WriteString(strings.TrimSpace(request.Question))
	b.WriteString("\n\nПроверяемое утверждение:\n")
	b.WriteString(strings.TrimSpace(request.Claim.Text))
	b.WriteString("\n\nДословные цитаты:\n")
	for _, quote := range request.Quotes {
		fmt.Fprintf(&b, "[%s] %s\n", quote.SourceID, quote.Quote)
	}
	return b.String()
}

func (o *Ollama) Judge(ctx context.Context, request Request) Result {
	result := Result{ClaimID: request.Claim.ID, Verdict: Unverifiable}
	if o == nil || o.Generator == nil || strings.TrimSpace(o.Model) == "" || len(request.Quotes) == 0 {
		result.Error = "judge is not configured or claim has no validated quotes"
		return result
	}
	started := time.Now()
	response, err := o.Generator.Generate(ctx, o.Model, Prompt(request), o.Settings)
	result.LatencyMS = time.Since(started).Milliseconds()
	result.Usage = response.Usage
	if err != nil {
		result.Error = err.Error()
		return result
	}
	var payload struct {
		Verdict string `json:"verdict"`
		Reason  string `json:"reason"`
	}
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(response.Text)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		result.Error = fmt.Sprintf("decode judge JSON: %v", err)
		return result
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		result.Error = "decode judge JSON: unexpected trailing content"
		return result
	}
	payload.Verdict = strings.TrimSpace(payload.Verdict)
	if payload.Verdict != Supported && payload.Verdict != PartiallySupported && payload.Verdict != Unsupported && payload.Verdict != Unverifiable {
		result.Error = fmt.Sprintf("invalid judge verdict %q", payload.Verdict)
		return result
	}
	if strings.TrimSpace(payload.Reason) == "" {
		result.Error = "judge reason is empty"
		return result
	}
	result.Verdict = payload.Verdict
	result.Reason = strings.TrimSpace(payload.Reason)
	return result
}
