package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ai-challenge/day-19/internal/agent"
)

type Agent interface {
	AskWithHistory(context.Context, string, []agent.Message) (agent.Response, error)
}

type chatRequest struct {
	Message string `json:"message"`
}

type chatResponse struct {
	Content      string             `json:"content,omitempty"`
	Usage        *agent.TokenUsage  `json:"usage,omitempty"`
	FinishReason string             `json:"finish_reason,omitempty"`
	Trace        []agent.TraceEvent `json:"trace,omitempty"`
	Error        string             `json:"error,omitempty"`
}

type fileInfo struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Bytes      int64     `json:"bytes"`
	ModifiedAt time.Time `json:"modified_at"`
}

func Handler(chatAgent Agent, outputDir string) http.Handler {
	mux := http.NewServeMux()
	sessions := newConversationStore()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, chatPageHTML)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "day19-agent-app"})
	})
	mux.HandleFunc("GET /api/chat/history", func(w http.ResponseWriter, r *http.Request) {
		conversation := sessions.get(w, r)
		conversation.mu.Lock()
		history := displayHistory(conversation.history())
		conversation.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"messages": history})
	})
	mux.HandleFunc("DELETE /api/chat/session", func(w http.ResponseWriter, r *http.Request) {
		sessions.reset(w, r)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/chat", func(w http.ResponseWriter, r *http.Request) {
		var input chatRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeJSON(w, http.StatusBadRequest, chatResponse{Error: "invalid JSON request"})
			return
		}
		input.Message = strings.TrimSpace(input.Message)
		if input.Message == "" {
			writeJSON(w, http.StatusBadRequest, chatResponse{Error: "message cannot be empty"})
			return
		}
		conversation := sessions.get(w, r)
		conversation.mu.Lock()
		result, err := chatAgent.AskWithHistory(r.Context(), input.Message, conversation.history())
		if err == nil {
			conversation.appendTurn(result.Turn)
		}
		conversation.mu.Unlock()
		if err != nil {
			writeJSON(w, http.StatusBadGateway, chatResponse{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, chatResponse{Content: result.Content, Usage: result.Usage, FinishReason: result.FinishReason, Trace: result.Trace})
	})
	mux.HandleFunc("GET /api/files", func(w http.ResponseWriter, _ *http.Request) {
		files, err := listFiles(outputDir)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"files": files})
	})
	mux.HandleFunc("GET /api/files/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !safeReadName(name) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid filename"})
			return
		}
		path := filepath.Join(outputDir, name)
		file, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot open file"})
			return
		}
		defer file.Close()
		content, err := io.ReadAll(io.LimitReader(file, 1<<20))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot read file"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "path": path, "content": string(content)})
	})
	return securityHeaders(mux)
}

func displayHistory(messages []agent.Message) []map[string]string {
	result := make([]map[string]string, 0)
	for _, message := range messages {
		if (message.Role == "user" || message.Role == "assistant") && strings.TrimSpace(message.Content) != "" {
			result = append(result, map[string]string{"role": message.Role, "content": message.Content})
		}
	}
	return result
}

func safeReadName(name string) bool {
	return name != "" && strings.HasSuffix(name, ".md") && !filepath.IsAbs(name) && filepath.Base(name) == name && !strings.Contains(name, "..") && !strings.ContainsAny(name, `/\\`)
}

func listFiles(dir string) ([]fileInfo, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []fileInfo{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list output files: %w", err)
	}
	result := make([]fileInfo, 0)
	for _, entry := range entries {
		if entry.IsDir() || !safeReadName(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		result = append(result, fileInfo{Name: entry.Name(), Path: filepath.Join(dir, entry.Name()), Bytes: info.Size(), ModifiedAt: info.ModTime().UTC()})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ModifiedAt.After(result[j].ModifiedAt) })
	return result, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
