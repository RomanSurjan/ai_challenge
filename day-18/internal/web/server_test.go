package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-challenge/day-18/internal/agent"
)

type historyAgent struct{ histories [][]agent.Message }

func (f *historyAgent) AskWithHistory(_ context.Context, prompt string, history []agent.Message) (agent.Response, error) {
	f.histories = append(f.histories, append([]agent.Message(nil), history...))
	answer := "answer to " + prompt
	return agent.Response{Content: answer, Turn: []agent.Message{{Role: "user", Content: prompt}, {Role: "assistant", Content: answer}}}, nil
}

func TestChatSessionPassesHistoryToNextRequestAndRestoresIt(t *testing.T) {
	chatAgent := &historyAgent{}
	handler := Handler(chatAgent, nil)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"first"}`)))
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	cookies := first.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%#v", cookies)
	}

	secondRequest := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"second"}`))
	secondRequest.AddCookie(cookies[0])
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, secondRequest)
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	if len(chatAgent.histories) != 2 || len(chatAgent.histories[0]) != 0 || len(chatAgent.histories[1]) != 2 || chatAgent.histories[1][0].Content != "first" {
		t.Fatalf("histories=%#v", chatAgent.histories)
	}

	historyRequest := httptest.NewRequest(http.MethodGet, "/api/chat/history", nil)
	historyRequest.AddCookie(cookies[0])
	history := httptest.NewRecorder()
	handler.ServeHTTP(history, historyRequest)
	var payload struct {
		Messages []historyMessage `json:"messages"`
	}
	if err := json.Unmarshal(history.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Messages) != 4 || payload.Messages[0].Content != "first" || payload.Messages[2].Content != "second" {
		t.Fatalf("messages=%#v", payload.Messages)
	}
}
