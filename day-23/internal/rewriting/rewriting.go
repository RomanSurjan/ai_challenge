package rewriting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"ai-challenge/day-23/internal/generation"
)

const SystemPrompt = "Ты переписываешь вопрос в один компактный поисковый запрос. Сохраняй технические имена, сущности и ограничения. Не отвечай на вопрос и не добавляй факты. Верни только JSON вида {\"query\":\"...\"}."

type Result struct {
	OriginalQuestion string           `json:"original_question"`
	Query            string           `json:"query"`
	Applied          bool             `json:"applied"`
	Fallback         bool             `json:"fallback"`
	Error            string           `json:"error,omitempty"`
	LatencyMS        int64            `json:"latency_ms"`
	Usage            generation.Usage `json:"usage"`
}

type Rewriter interface {
	Rewrite(ctx context.Context, question string) (Result, error)
}

type Ollama struct {
	Generator generation.Generator
	Model     string
	Settings  generation.Settings
}

func NewOllama(generator generation.Generator, model string, maxTokens int) *Ollama {
	return &Ollama{Generator: generator, Model: model, Settings: generation.Settings{Temperature: 0, MaxTokens: maxTokens, System: SystemPrompt}}
}

func Prompt(question string) string {
	return "Перепиши только следующий исходный вопрос в поисковый запрос. Не используй никаких внешних данных.\n\nИсходный вопрос:\n" + strings.TrimSpace(question)
}

func (o *Ollama) Rewrite(ctx context.Context, question string) (Result, error) {
	question = strings.TrimSpace(question)
	result := Result{OriginalQuestion: question, Query: question}
	if question == "" {
		return result, fmt.Errorf("question is required")
	}
	if o == nil || o.Generator == nil || strings.TrimSpace(o.Model) == "" || o.Settings.MaxTokens <= 0 {
		return result, fmt.Errorf("rewriter is not configured")
	}
	started := time.Now()
	response, err := o.Generator.Generate(ctx, o.Model, Prompt(question), o.Settings)
	result.LatencyMS = time.Since(started).Milliseconds()
	result.Usage = response.Usage
	if err != nil {
		return result, err
	}
	var payload struct {
		Query string `json:"query"`
	}
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(response.Text)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return result, fmt.Errorf("decode rewrite JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return result, fmt.Errorf("decode rewrite JSON: unexpected trailing content")
	}
	result.Query = strings.TrimSpace(payload.Query)
	if result.Query == "" {
		result.Query = question
		return result, fmt.Errorf("rewrite query is empty")
	}
	result.Applied = result.Query != question
	return result, nil
}

func WithFallback(question string, result Result, err error) Result {
	question = strings.TrimSpace(question)
	if strings.TrimSpace(result.OriginalQuestion) == "" {
		result.OriginalQuestion = question
	}
	if err != nil {
		result.Query = question
		result.Applied = false
		result.Fallback = true
		result.Error = err.Error()
	}
	if strings.TrimSpace(result.Query) == "" {
		result.Query = question
	}
	return result
}
