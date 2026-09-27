package web

import (
	"net/http/httptest"
	"testing"

	"ai-challenge/day-18/internal/agent"
)

func TestConversationKeepsWholeRecentTurns(t *testing.T) {
	conversation := &conversation{}
	for i := 0; i < maxConversationTurns+2; i++ {
		conversation.appendTurn([]agent.Message{{Role: "user", Content: "question"}, {Role: "assistant", Content: "answer"}})
	}
	if len(conversation.turns) != maxConversationTurns {
		t.Fatalf("turns = %d", len(conversation.turns))
	}
	if len(conversation.history()) != maxConversationTurns*2 {
		t.Fatalf("messages = %d", len(conversation.history()))
	}
}

func TestConversationStoreReusesCookieAndCanReset(t *testing.T) {
	store := newConversationStore()
	firstRecorder := httptest.NewRecorder()
	first := store.get(firstRecorder, httptest.NewRequest("GET", "/api/chat/history", nil))
	cookies := firstRecorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName {
		t.Fatalf("cookies = %#v", cookies)
	}
	secondRequest := httptest.NewRequest("GET", "/api/chat/history", nil)
	secondRequest.AddCookie(cookies[0])
	if second := store.get(httptest.NewRecorder(), secondRequest); second != first {
		t.Fatal("cookie must reuse conversation")
	}
	store.reset(httptest.NewRecorder(), secondRequest)
	if len(store.sessions) != 0 {
		t.Fatalf("sessions = %d", len(store.sessions))
	}
}

func TestDisplayHistoryHidesToolProtocolMessages(t *testing.T) {
	messages := displayHistory([]agent.Message{{Role: "user", Content: "hello"}, {Role: "assistant", ToolCalls: []agent.ToolCall{{ID: "call"}}}, {Role: "tool", Content: "secret detail"}, {Role: "assistant", Content: "answer"}})
	if len(messages) != 2 || messages[0].Role != "user" || messages[1].Content != "answer" {
		t.Fatalf("messages = %#v", messages)
	}
}
