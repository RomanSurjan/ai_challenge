package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"ai-challenge/day-17/internal/agent"
)

type Agent interface {
	Ask(ctx context.Context, prompt string) (agent.Response, error)
}

type ChatRequest struct {
	Message string `json:"message"`
}

type ChatResponse struct {
	Content      string             `json:"content,omitempty"`
	Usage        *agent.TokenUsage  `json:"usage,omitempty"`
	FinishReason string             `json:"finish_reason,omitempty"`
	Trace        []agent.TraceEvent `json:"trace,omitempty"`
	Error        string             `json:"error,omitempty"`
}

func Handler(chatAgent Agent) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, chatPageHTML)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "day17-agent-app"})
	})
	mux.HandleFunc("POST /api/chat", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		var input ChatRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(ChatResponse{Error: "invalid JSON request"})
			return
		}
		input.Message = strings.TrimSpace(input.Message)
		if input.Message == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(ChatResponse{Error: "message cannot be empty"})
			return
		}
		result, err := chatAgent.Ask(r.Context(), input.Message)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(ChatResponse{Error: err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(ChatResponse{
			Content: result.Content, Usage: result.Usage, FinishReason: result.FinishReason, Trace: result.Trace,
		})
	})
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
