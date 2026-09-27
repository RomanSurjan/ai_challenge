package web

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"ai-challenge/day-20/internal/agent"
)

type conversation struct {
	mu       sync.Mutex
	messages []agent.Message
}

type conversationStore struct {
	mu       sync.Mutex
	sessions map[string]*conversation
}

func newConversationStore() *conversationStore {
	return &conversationStore{sessions: map[string]*conversation{}}
}

func (s *conversationStore) get(w http.ResponseWriter, r *http.Request) *conversation {
	id := ""
	if cookie, err := r.Cookie("day19_session"); err == nil {
		id = cookie.Value
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.sessions[id]; id != "" && existing != nil {
		return existing
	}
	var raw [24]byte
	_, _ = rand.Read(raw[:])
	id = hex.EncodeToString(raw[:])
	created := &conversation{}
	s.sessions[id] = created
	http.SetCookie(w, &http.Cookie{Name: "day19_session", Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 86400})
	return created
}

func (s *conversationStore) reset(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("day19_session"); err == nil {
		s.mu.Lock()
		delete(s.sessions, cookie.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "day19_session", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0)})
}

func (c *conversation) history() []agent.Message { return append([]agent.Message(nil), c.messages...) }
func (c *conversation) appendTurn(messages []agent.Message) {
	c.messages = append(c.messages, messages...)
}
