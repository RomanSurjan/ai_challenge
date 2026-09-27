package web

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
	"sync"

	"ai-challenge/day-18/internal/agent"
)

const (
	sessionCookieName    = "day18_agent_session"
	maxConversationTurns = 12
	maxConversationCount = 1024
)

var sessionIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type conversation struct {
	mu    sync.Mutex
	turns [][]agent.Message
}

func (c *conversation) history() []agent.Message {
	var result []agent.Message
	for _, turn := range c.turns {
		result = append(result, turn...)
	}
	return result
}

func (c *conversation) appendTurn(turn []agent.Message) {
	c.turns = append(c.turns, append([]agent.Message(nil), turn...))
	if len(c.turns) > maxConversationTurns {
		c.turns = c.turns[len(c.turns)-maxConversationTurns:]
	}
}

type conversationStore struct {
	mu       sync.Mutex
	sessions map[string]*conversation
}

func newConversationStore() *conversationStore {
	return &conversationStore{sessions: make(map[string]*conversation)}
}

func (s *conversationStore) get(w http.ResponseWriter, r *http.Request) *conversation {
	id := ""
	if cookie, err := r.Cookie(sessionCookieName); err == nil && sessionIDPattern.MatchString(cookie.Value) {
		id = cookie.Value
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != "" {
		if existing := s.sessions[id]; existing != nil {
			return existing
		}
	}
	if len(s.sessions) >= maxConversationCount {
		for stale := range s.sessions {
			delete(s.sessions, stale)
			break
		}
	}
	id = newSessionID()
	created := &conversation{}
	s.sessions[id] = created
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, MaxAge: 86400})
	return created
}

func (s *conversationStore) reset(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && sessionIDPattern.MatchString(cookie.Value) {
		s.mu.Lock()
		delete(s.sessions, cookie.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, MaxAge: -1})
}

func newSessionID() string {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(value)
}

type historyMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func displayHistory(messages []agent.Message) []historyMessage {
	result := make([]historyMessage, 0, len(messages))
	for _, message := range messages {
		if message.Content != "" && (message.Role == "user" || message.Role == "assistant") {
			result = append(result, historyMessage{Role: message.Role, Content: message.Content})
		}
	}
	return result
}
