package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"ai-challenge/day-18/internal/agent"
	"ai-challenge/day-18/internal/mcpcurrency"
	"ai-challenge/day-18/internal/mcpgithub"
)

type Agent interface {
	AskWithHistory(ctx context.Context, prompt string, history []agent.Message) (agent.Response, error)
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

func Handler(chatAgent Agent, connector mcpgithub.Connector) http.Handler {
	mux := http.NewServeMux()
	sessions := newConversationStore()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, chatPageHTML)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "day18-agent-app"})
	})
	mux.HandleFunc("GET /api/monitors", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		result, err := loadDashboard(ctx, connector)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"connected": false, "error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("GET /api/chat/history", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		conversation := sessions.get(w, r)
		conversation.mu.Lock()
		messages := displayHistory(conversation.history())
		conversation.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": messages})
	})
	mux.HandleFunc("DELETE /api/chat/session", func(w http.ResponseWriter, r *http.Request) {
		sessions.reset(w, r)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
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
		conversation := sessions.get(w, r)
		conversation.mu.Lock()
		result, err := chatAgent.AskWithHistory(r.Context(), input.Message, conversation.history())
		if err != nil {
			conversation.mu.Unlock()
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(ChatResponse{Error: err.Error()})
			return
		}
		conversation.appendTurn(result.Turn)
		conversation.mu.Unlock()
		_ = json.NewEncoder(w).Encode(ChatResponse{
			Content: result.Content, Usage: result.Usage, FinishReason: result.FinishReason, Trace: result.Trace,
		})
	})
	return securityHeaders(mux)
}

func loadDashboard(ctx context.Context, connector mcpgithub.Connector) (map[string]any, error) {
	if connector == nil {
		return nil, errors.New("MCP connector is not configured")
	}
	session, err := connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	repositoryMonitors, err := loadMonitorSummaries(ctx, session, mcpgithub.ListToolName, mcpgithub.SummaryToolName)
	if err != nil {
		return nil, err
	}
	currencyMonitors, err := loadMonitorSummaries(ctx, session, mcpcurrency.ListToolName, mcpcurrency.SummaryToolName)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"connected":           true,
		"repository_monitors": repositoryMonitors,
		"currency_monitors":   currencyMonitors,
		"monitor_count":       len(repositoryMonitors) + len(currencyMonitors),
		"refreshed_at":        time.Now().UTC(),
	}, nil
}

type toolSession interface {
	CallTool(context.Context, string, map[string]any) (mcpgithub.ToolResult, error)
}

func loadMonitorSummaries(ctx context.Context, session toolSession, listTool, summaryTool string) ([]any, error) {
	listed, err := session.CallTool(ctx, listTool, map[string]any{})
	if err != nil {
		return nil, err
	}
	if listed.IsError {
		return nil, errors.New(listed.Text)
	}
	encoded, err := json.Marshal(listed.Structured)
	if err != nil {
		return nil, err
	}
	var list struct {
		Monitors []struct {
			ScheduleID string `json:"schedule_id"`
		} `json:"monitors"`
	}
	if err := json.Unmarshal(encoded, &list); err != nil {
		return nil, err
	}
	summaries := make([]any, 0, len(list.Monitors))
	for _, monitor := range list.Monitors {
		summary, err := session.CallTool(ctx, summaryTool, map[string]any{"schedule_id": monitor.ScheduleID})
		if err != nil {
			return nil, err
		}
		if summary.IsError {
			continue
		}
		summaries = append(summaries, summary.Structured)
	}
	return summaries, nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
