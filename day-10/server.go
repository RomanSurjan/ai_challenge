package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

type chatAPIRequest struct {
	Message string `json:"message"`
}

type chatAPIResponse struct {
	Content      string           `json:"content,omitempty"`
	Usage        *tokenUsage      `json:"usage,omitempty"`
	Context      *ContextMetadata `json:"context,omitempty"`
	FinishReason string           `json:"finish_reason,omitempty"`
	Error        string           `json:"error,omitempty"`
}

type historyAPIResponse struct {
	Messages []chatMessage `json:"messages,omitempty"`
	Error    string        `json:"error,omitempty"`
}

type factsAPIResponse struct {
	Facts map[string]string `json:"facts,omitempty"`
	Error string            `json:"error,omitempty"`
}

type branchAPIItem struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	MessageCount int    `json:"message_count"`
	Active       bool   `json:"active"`
}

type branchesAPIResponse struct {
	ActiveBranchID     string          `json:"active_branch_id"`
	Checkpoint         []chatMessage   `json:"checkpoint"`
	CheckpointMessages int             `json:"checkpoint_messages"`
	Branches           []branchAPIItem `json:"branches"`
	Error              string          `json:"error,omitempty"`
}

type createBranchAPIRequest struct {
	Title string `json:"title"`
}

type activeBranchAPIRequest struct {
	ID string `json:"id"`
}

func serveChat(addr string, agent *Agent) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", chatPageHandler)
	mux.HandleFunc("/api/chat", chatAPIHandler(agent))
	mux.HandleFunc("/api/history", historyAPIHandler(agent))
	mux.HandleFunc("/api/facts", factsAPIHandler(agent))
	mux.HandleFunc("/api/branches", branchesAPIHandler(agent))
	mux.HandleFunc("/api/branches/checkpoint", branchCheckpointAPIHandler(agent))
	mux.HandleFunc("/api/branches/active", branchActiveAPIHandler(agent))
	mux.HandleFunc("/api/branches/", branchDeleteAPIHandler(agent))
	mux.HandleFunc("/api/settings", settingsAPIHandler(agent))

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return server.ListenAndServe()
}

func branchesAPIHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		switch r.Method {
		case http.MethodGet:
			branches, err := agent.Branches(r.Context())
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(branchesAPIResponse{Error: err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(formatBranchesAPIResponse(branches))
		case http.MethodPost:
			var input createBranchAPIRequest
			if r.Body != nil {
				if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&input); err != nil && err != io.EOF {
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(branchesAPIResponse{Error: "некорректный JSON-запрос"})
					return
				}
			}
			branches, err := agent.CreateBranch(r.Context(), input.Title)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(branchesAPIResponse{Error: err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(formatBranchesAPIResponse(branches))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(branchesAPIResponse{Error: "method not allowed"})
		}
	}
}

func branchCheckpointAPIHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(branchesAPIResponse{Error: "method not allowed"})
			return
		}
		branches, err := agent.CreateCheckpoint(r.Context())
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(branchesAPIResponse{Error: err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(formatBranchesAPIResponse(branches))
	}
}

func branchActiveAPIHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(branchesAPIResponse{Error: "method not allowed"})
			return
		}
		var input activeBranchAPIRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&input); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(branchesAPIResponse{Error: "некорректный JSON-запрос"})
			return
		}
		branches, err := agent.SwitchBranch(r.Context(), input.ID)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(branchesAPIResponse{Error: err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(formatBranchesAPIResponse(branches))
	}
}

func branchDeleteAPIHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(branchesAPIResponse{Error: "method not allowed"})
			return
		}
		id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/branches/"), "/")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(branchesAPIResponse{Error: "id ветки не задан"})
			return
		}
		branches, err := agent.DeleteBranch(r.Context(), id)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(branchesAPIResponse{Error: err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(formatBranchesAPIResponse(branches))
	}
}

func formatBranchesAPIResponse(branches BranchState) branchesAPIResponse {
	items := make([]branchAPIItem, 0, len(branches.Items))
	for _, branch := range branches.Items {
		items = append(items, branchAPIItem{
			ID:           branch.ID,
			Title:        branch.Title,
			MessageCount: len(branch.Messages),
			Active:       branch.ID == branches.ActiveBranchID,
		})
	}
	checkpoint := cloneMessages(branches.Checkpoint)
	if checkpoint == nil {
		checkpoint = []chatMessage{}
	}
	return branchesAPIResponse{
		ActiveBranchID:     branches.ActiveBranchID,
		Checkpoint:         checkpoint,
		CheckpointMessages: len(branches.Checkpoint),
		Branches:           items,
	}
}

func settingsAPIHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(agent.Settings())
		case http.MethodPost:
			var input RuntimeSettings
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&input); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(apiError{Error: "некорректный JSON-запрос"})
				return
			}
			if err := agent.UpdateSettings(input); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(apiError{Error: err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(agent.Settings())
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(apiError{Error: "method not allowed"})
		}
	}
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

func factsAPIHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(factsAPIResponse{Error: "method not allowed"})
			return
		}

		facts, err := agent.Facts(r.Context())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(factsAPIResponse{Error: err.Error()})
			return
		}

		_ = json.NewEncoder(w).Encode(factsAPIResponse{Facts: facts})
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
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(chatAPIResponse{Error: err.Error()})
			return
		}

		_ = json.NewEncoder(w).Encode(chatAPIResponse{
			Content:      answer.Content,
			Usage:        answer.Usage,
			Context:      &answer.Context,
			FinishReason: answer.FinishReason,
		})
	}
}
