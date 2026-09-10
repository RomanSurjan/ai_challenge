package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type chatAPIRequest struct {
	Message string `json:"message"`
}

type chatAPIResponse struct {
	Content      string       `json:"content,omitempty"`
	Usage        *tokenUsage  `json:"usage,omitempty"`
	TokenReport  *TokenReport `json:"token_report,omitempty"`
	FinishReason string       `json:"finish_reason,omitempty"`
	Error        string       `json:"error,omitempty"`
}

type historyAPIResponse struct {
	Messages []chatMessage `json:"messages,omitempty"`
	Error    string        `json:"error,omitempty"`
}

func serveChat(addr string, agent *Agent) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", chatPageHandler)
	mux.HandleFunc("/api/chat", chatAPIHandler(agent))
	mux.HandleFunc("/api/history", historyAPIHandler(agent))

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return server.ListenAndServe()
}

func chatPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.WriteString(w, chatPageHTML)
}

func historyAPIHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(historyAPIResponse{Error: "method not allowed"})
			return
		}

		messages, err := agent.History(r.Context())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(historyAPIResponse{Error: err.Error()})
			return
		}

		_ = json.NewEncoder(w).Encode(historyAPIResponse{Messages: messages})
	}
}

func chatAPIHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(chatAPIResponse{Error: "method not allowed"})
			return
		}

		var input chatAPIRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&input); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(chatAPIResponse{Error: "некорректный JSON-запрос"})
			return
		}
		input.Message = strings.TrimSpace(input.Message)
		if input.Message == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(chatAPIResponse{Error: "сообщение не может быть пустым"})
			return
		}

		answer, err := agent.Ask(r.Context(), input.Message)
		if err != nil {
			var limitErr *ContextLimitError
			if errors.As(err, &limitErr) {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				_ = json.NewEncoder(w).Encode(chatAPIResponse{
					Error:       err.Error(),
					TokenReport: &limitErr.Report,
				})
				return
			}
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(chatAPIResponse{Error: err.Error()})
			return
		}

		_ = json.NewEncoder(w).Encode(chatAPIResponse{
			Content:      answer.Content,
			Usage:        answer.Usage,
			TokenReport:  &answer.TokenReport,
			FinishReason: answer.FinishReason,
		})
	}
}
