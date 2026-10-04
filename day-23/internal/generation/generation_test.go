package generation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOllamaChatRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string    `json:"model"`
			Messages []message `json:"messages"`
			Stream   bool      `json:"stream"`
			Think    bool      `json:"think"`
			Options  struct {
				Temperature float64 `json:"temperature"`
				NumPredict  int     `json:"num_predict"`
				Seed        int     `json:"seed"`
			} `json:"options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != DefaultModel || len(body.Messages) != 2 || body.Messages[0].Role != "system" || body.Messages[1].Content != "prompt" {
			t.Fatalf("unexpected chat request: %+v", body)
		}
		if body.Stream || body.Think || body.Options.Temperature != 0 || body.Options.NumPredict != 123 || body.Options.Seed != 0 {
			t.Fatalf("unexpected deterministic options: %+v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":           map[string]string{"role": "assistant", "content": "ответ"},
			"prompt_eval_count": 7, "eval_count": 3,
		})
	}))
	defer server.Close()
	settings := DefaultSettings()
	settings.MaxTokens = 123
	response, err := NewOllama(server.URL, time.Second).Generate(context.Background(), DefaultModel, "prompt", settings)
	if err != nil {
		t.Fatal(err)
	}
	if response.Text != "ответ" || response.Usage.PromptTokens != 7 || response.Usage.CompletionTokens != 3 {
		t.Fatalf("unexpected response: %+v", response)
	}
}
