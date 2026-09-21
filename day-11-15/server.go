package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type chatAPIRequest struct {
	Message  string `json:"message"`
	DialogID string `json:"dialog_id,omitempty"`
}

type chatAPIResponse struct {
	DialogID           string               `json:"dialog_id,omitempty"`
	Content            string               `json:"content,omitempty"`
	Usage              *tokenUsage          `json:"usage,omitempty"`
	FinishReason       string               `json:"finish_reason,omitempty"`
	MemoryUpdates      []MemoryUpdate       `json:"memory_updates,omitempty"`
	TaskState          *TaskState           `json:"task_state,omitempty"`
	CurrentStage       TaskStage            `json:"current_stage,omitempty"`
	RequestedStage     TaskStage            `json:"requested_stage,omitempty"`
	AllowedTransitions []TaskStage          `json:"allowed_transitions,omitempty"`
	ExpectedAction     string               `json:"expected_action,omitempty"`
	Reason             string               `json:"reason,omitempty"`
	Blocked            bool                 `json:"blocked,omitempty"`
	Violations         []InvariantViolation `json:"violations,omitempty"`
	Tool               string               `json:"tool,omitempty"`
	ErrorCategory      string               `json:"error_category,omitempty"`
	ErrorField         string               `json:"error_field,omitempty"`
	JSONType           string               `json:"json_type,omitempty"`
	ValidationRule     string               `json:"validation_rule,omitempty"`
	FSMGuard           bool                 `json:"fsm_guard,omitempty"`
	Error              string               `json:"error,omitempty"`
}

type chatTaskConflictResponse struct {
	DialogID           string      `json:"dialog_id,omitempty"`
	Error              string      `json:"error"`
	TaskState          *TaskState  `json:"task_state"`
	CurrentStage       TaskStage   `json:"current_stage"`
	RequestedStage     TaskStage   `json:"requested_stage,omitempty"`
	AllowedTransitions []TaskStage `json:"allowed_transitions"`
	ExpectedAction     string      `json:"expected_action"`
	Reason             string      `json:"reason"`
	ErrorCategory      string      `json:"error_category"`
	FSMGuard           bool        `json:"fsm_guard"`
}

type invariantsAPIResponse struct {
	Invariants []Invariant `json:"invariants"`
	Error      string      `json:"error,omitempty"`
}

type taskAPIResponse struct {
	DialogID           string      `json:"dialog_id,omitempty"`
	Started            bool        `json:"started"`
	TaskState          *TaskState  `json:"task_state,omitempty"`
	Error              string      `json:"error,omitempty"`
	CurrentStage       TaskStage   `json:"current_stage,omitempty"`
	RequestedStage     TaskStage   `json:"requested_stage,omitempty"`
	AllowedTransitions []TaskStage `json:"allowed_transitions"`
	ExpectedAction     string      `json:"expected_action,omitempty"`
	Reason             string      `json:"reason,omitempty"`
}

type historyAPIResponse struct {
	DialogID string        `json:"dialog_id,omitempty"`
	Messages []chatMessage `json:"messages"`
	Error    string        `json:"error,omitempty"`
}

type memoryAPIResponse struct {
	DialogID  string           `json:"dialog_id,omitempty"`
	Layer     string           `json:"layer,omitempty"`
	Category  string           `json:"category,omitempty"`
	ShortTerm *ShortTermMemory `json:"short_term,omitempty"`
	Working   *WorkingMemory   `json:"working,omitempty"`
	LongTerm  *LongTermMemory  `json:"long_term,omitempty"`
	Error     string           `json:"error,omitempty"`
}

type profileAPIResponse struct {
	Configured bool         `json:"configured"`
	Profile    *UserProfile `json:"profile"`
	Error      string       `json:"error,omitempty"`
}

type dialogsAPIResponse struct {
	DialogID       string           `json:"dialog_id,omitempty"`
	ActiveDialogID string           `json:"active_dialog_id,omitempty"`
	Dialog         *DialogMetadata  `json:"dialog,omitempty"`
	Dialogs        []DialogMetadata `json:"dialogs,omitempty"`
	Error          string           `json:"error,omitempty"`
}

type dialogCreateRequest struct {
	Title string `json:"title,omitempty"`
}

type dialogSelectRequest struct {
	DialogID string `json:"dialog_id"`
}

type workingValueRequest struct {
	Value string `json:"value"`
}

type taskCreateRequest struct {
	Goal           string `json:"goal"`
	CurrentStep    string `json:"current_step,omitempty"`
	ExpectedAction string `json:"expected_action,omitempty"`
}

type taskProgressRequest struct {
	CurrentStep    string `json:"current_step"`
	ExpectedAction string `json:"expected_action"`
}

type taskPlanRequest struct {
	Plan string `json:"plan"`
}

type taskTransitionRequest struct {
	Stage string `json:"stage"`
}

type taskValidationRequest struct {
	Passed  *bool  `json:"passed"`
	Details string `json:"details"`
}

type legacyProfileWriteRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type profileWriteRequest struct {
	Name        string          `json:"name,omitempty"`
	Language    ProfileLanguage `json:"language"`
	Tone        ProfileTone     `json:"tone"`
	Detail      ProfileDetail   `json:"detail"`
	Format      ProfileFormat   `json:"format"`
	Context     string          `json:"context,omitempty"`
	Constraints []string        `json:"constraints,omitempty"`
}

func (request profileWriteRequest) profile() UserProfile {
	return UserProfile{
		Name: request.Name, Language: request.Language, Tone: request.Tone,
		Detail: request.Detail, Format: request.Format, Context: request.Context,
		Constraints: request.Constraints,
	}
}

type decisionWriteRequest struct {
	Statement string `json:"statement"`
	Rationale string `json:"rationale,omitempty"`
}

type knowledgeWriteRequest struct {
	Topic   string `json:"topic"`
	Content string `json:"content"`
}

func serveChat(addr string, agent *Agent) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", chatPageHandler)
	mux.HandleFunc("/api/chat", chatAPIHandler(agent))
	mux.HandleFunc("/api/history", historyAPIHandler(agent))
	mux.HandleFunc("/api/dialogs", dialogsAPIHandler(agent))
	mux.HandleFunc("/api/dialogs/", dialogsAPIHandler(agent))
	mux.HandleFunc("/api/memory/", memoryAPIHandler(agent))
	mux.HandleFunc("/api/task", taskAPIHandler(agent))
	mux.HandleFunc("/api/task/", taskAPIHandler(agent))
	mux.HandleFunc("/api/invariants", invariantsAPIHandler(agent))
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return server.ListenAndServe()
}

func invariantsAPIHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(invariantsAPIResponse{Error: "инварианты доступны только для чтения"})
			return
		}
		set, err := agent.Invariants(r.Context())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(invariantsAPIResponse{Error: "не удалось загрузить инварианты"})
			return
		}
		_ = json.NewEncoder(w).Encode(invariantsAPIResponse{Invariants: set.Invariants})
	}
}

func dialogsAPIHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.URL.Path == "/api/dialogs" {
			switch r.Method {
			case http.MethodGet:
				list, err := agent.Dialogs(r.Context())
				if err != nil {
					writeDialogsError(w, httpStatusForError(err), err.Error())
					return
				}
				_ = json.NewEncoder(w).Encode(dialogsAPIResponse{ActiveDialogID: list.ActiveDialogID, Dialogs: list.Dialogs})
			case http.MethodPost:
				var input dialogCreateRequest
				if err := decodeJSONBody(w, r, &input); err != nil {
					writeDialogsError(w, http.StatusBadRequest, err.Error())
					return
				}
				dialog, err := agent.CreateDialog(r.Context(), input.Title)
				if err != nil {
					writeDialogsError(w, httpStatusForError(err), err.Error())
					return
				}
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(dialogsAPIResponse{DialogID: dialog.ID, ActiveDialogID: dialog.ID, Dialog: &dialog})
			default:
				writeDialogsError(w, http.StatusMethodNotAllowed, "method not allowed")
			}
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/api/dialogs/")
		if path == "active" {
			if r.Method != http.MethodPost {
				writeDialogsError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			var input dialogSelectRequest
			if err := decodeJSONBody(w, r, &input); err != nil {
				writeDialogsError(w, http.StatusBadRequest, err.Error())
				return
			}
			dialog, err := agent.SelectDialog(r.Context(), strings.TrimSpace(input.DialogID))
			if err != nil {
				writeDialogsError(w, httpStatusForError(err), err.Error())
				return
			}
			_ = json.NewEncoder(w).Encode(dialogsAPIResponse{DialogID: dialog.ID, ActiveDialogID: dialog.ID, Dialog: &dialog})
			return
		}

		parts := strings.Split(path, "/")
		if len(parts) == 1 && r.Method == http.MethodGet {
			dialog, err := agent.Dialog(r.Context(), parts[0])
			if err != nil {
				writeDialogsError(w, httpStatusForError(err), err.Error())
				return
			}
			_ = json.NewEncoder(w).Encode(dialogsAPIResponse{DialogID: dialog.ID, Dialog: &dialog})
			return
		}
		if len(parts) == 2 && parts[1] == "history" && r.Method == http.MethodGet {
			writeHistory(w, r, agent, parts[0])
			return
		}
		if (len(parts) == 1 || len(parts) == 2) && r.Method != http.MethodGet {
			writeDialogsError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeDialogsError(w, http.StatusNotFound, "неизвестный endpoint диалогов")
	}
}

func taskAPIHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		path := strings.TrimPrefix(r.URL.Path, "/api/task")
		path = strings.Trim(path, "/")
		dialogID := strings.TrimSpace(r.URL.Query().Get("dialog_id"))
		dialog, err := agent.Dialog(r.Context(), dialogID)
		if err != nil {
			writeTaskError(w, httpStatusForError(err), "", nil, err)
			return
		}

		if path == "" && r.Method == http.MethodGet {
			state, readErr := agent.TaskStateInDialog(r.Context(), dialog.ID)
			if readErr != nil {
				writeTaskError(w, httpStatusForError(readErr), dialog.ID, nil, readErr)
				return
			}
			writeTaskState(w, http.StatusOK, dialog.ID, state)
			return
		}
		if path == "" && r.Method == http.MethodPost {
			var input taskCreateRequest
			if err := decodeJSONBody(w, r, &input); err != nil {
				writeTaskError(w, http.StatusBadRequest, dialog.ID, nil, err)
				return
			}
			if strings.TrimSpace(input.Goal) == "" {
				writeTaskError(w, http.StatusBadRequest, dialog.ID, nil, errors.New("goal не может быть пустым"))
				return
			}
			state, createErr := agent.CreateTaskInDialog(r.Context(), dialog.ID, input.Goal, input.CurrentStep, input.ExpectedAction)
			if createErr != nil {
				writeTaskOperationError(w, r, agent, dialog.ID, "", createErr)
				return
			}
			writeTaskState(w, http.StatusCreated, dialog.ID, &state)
			return
		}
		if r.Method != http.MethodPost {
			writeTaskError(w, http.StatusMethodNotAllowed, dialog.ID, nil, errors.New("method not allowed"))
			return
		}

		var state TaskState
		switch path {
		case "progress":
			var input taskProgressRequest
			if err := decodeJSONBody(w, r, &input); err != nil {
				writeTaskError(w, http.StatusBadRequest, dialog.ID, nil, err)
				return
			}
			if strings.TrimSpace(input.CurrentStep) == "" || strings.TrimSpace(input.ExpectedAction) == "" {
				writeTaskError(w, http.StatusBadRequest, dialog.ID, nil, errors.New("current_step и expected_action не могут быть пустыми"))
				return
			}
			state, err = agent.UpdateTaskProgressInDialog(r.Context(), dialog.ID, input.CurrentStep, input.ExpectedAction)
		case "approve-plan":
			var input taskPlanRequest
			if decodeErr := decodeJSONBody(w, r, &input); decodeErr != nil {
				writeTaskError(w, http.StatusBadRequest, dialog.ID, nil, decodeErr)
				return
			}
			if strings.TrimSpace(input.Plan) == "" {
				writeTaskError(w, http.StatusBadRequest, dialog.ID, nil, errors.New("plan не может быть пустым"))
				return
			}
			state, err = agent.ApproveTaskPlanInDialog(r.Context(), dialog.ID, input.Plan)
		case "transition":
			var input taskTransitionRequest
			if decodeErr := decodeJSONBody(w, r, &input); decodeErr != nil {
				writeTaskError(w, http.StatusBadRequest, dialog.ID, nil, decodeErr)
				return
			}
			stage, parseErr := ParseTaskStage(input.Stage)
			if parseErr != nil {
				writeTaskError(w, http.StatusBadRequest, dialog.ID, nil, parseErr)
				return
			}
			state, err = agent.TransitionTaskInDialog(r.Context(), dialog.ID, stage)
			if err != nil {
				writeTaskOperationError(w, r, agent, dialog.ID, stage, err)
				return
			}
		case "validation":
			var input taskValidationRequest
			if decodeErr := decodeJSONBody(w, r, &input); decodeErr != nil {
				writeTaskError(w, http.StatusBadRequest, dialog.ID, nil, decodeErr)
				return
			}
			if input.Passed == nil || strings.TrimSpace(input.Details) == "" {
				writeTaskError(w, http.StatusBadRequest, dialog.ID, nil, errors.New("passed и details обязательны"))
				return
			}
			state, err = agent.RecordTaskValidationInDialog(r.Context(), dialog.ID, *input.Passed, input.Details)
		case "pause":
			if decodeErr := decodeOptionalEmptyJSON(w, r); decodeErr != nil {
				writeTaskError(w, http.StatusBadRequest, dialog.ID, nil, decodeErr)
				return
			}
			state, err = agent.PauseTaskInDialog(r.Context(), dialog.ID)
		case "resume":
			if decodeErr := decodeOptionalEmptyJSON(w, r); decodeErr != nil {
				writeTaskError(w, http.StatusBadRequest, dialog.ID, nil, decodeErr)
				return
			}
			state, err = agent.ResumeTaskInDialog(r.Context(), dialog.ID)
		default:
			writeTaskError(w, http.StatusNotFound, dialog.ID, nil, errors.New("неизвестная операция задачи"))
			return
		}
		if err != nil {
			writeTaskOperationError(w, r, agent, dialog.ID, "", err)
			return
		}
		writeTaskState(w, http.StatusOK, dialog.ID, &state)
	}
}

func writeTaskState(w http.ResponseWriter, status int, dialogID string, state *TaskState) {
	w.WriteHeader(status)
	response := taskAPIResponse{DialogID: dialogID, Started: state != nil, TaskState: state}
	if state != nil {
		response.CurrentStage = state.Stage
		response.AllowedTransitions = state.AllowedTransitions()
		response.ExpectedAction = state.ExpectedAction
	}
	_ = json.NewEncoder(w).Encode(response)
}

func writeTaskOperationError(w http.ResponseWriter, r *http.Request, agent *Agent, dialogID string, requested TaskStage, err error) {
	state, loadErr := agent.TaskStateInDialog(r.Context(), dialogID)
	if loadErr != nil {
		writeTaskError(w, httpStatusForError(loadErr), dialogID, nil, loadErr)
		return
	}
	status := httpStatusForError(err)
	writeTaskError(w, status, dialogID, state, withRequestedTaskStage(err, requested))
}

func writeTaskError(w http.ResponseWriter, status int, dialogID string, state *TaskState, err error) {
	response := taskAPIResponse{DialogID: dialogID, Started: state != nil, TaskState: state, Error: err.Error(), Reason: err.Error()}
	if state != nil {
		response.CurrentStage = state.Stage
		response.AllowedTransitions = state.AllowedTransitions()
		response.ExpectedAction = state.ExpectedAction
	}
	var stateErr *TaskStateError
	if errors.As(err, &stateErr) {
		response.CurrentStage = stateErr.CurrentStage
		response.RequestedStage = stateErr.RequestedStage
		response.AllowedTransitions = stateErr.AllowedTransitions
		response.ExpectedAction = stateErr.ExpectedAction
		response.Reason = stateErr.Reason
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func withRequestedTaskStage(err error, requested TaskStage) error {
	if requested == "" {
		return err
	}
	var stateErr *TaskStateError
	if !errors.As(err, &stateErr) || stateErr.RequestedStage != "" {
		return err
	}
	copy := *stateErr
	copy.RequestedStage = requested
	return &copy
}

func memoryAPIHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		path := strings.TrimPrefix(r.URL.Path, "/api/memory/")
		if path == "long-term/profile" {
			switch r.Method {
			case http.MethodGet:
				handleProfileRead(w, r, agent)
			case http.MethodPut:
				handleProfileReplace(w, r, agent)
			case http.MethodPost:
				handleLegacyProfileWrite(w, r, agent)
			default:
				writeProfileError(w, http.StatusMethodNotAllowed, "method not allowed")
			}
			return
		}
		switch r.Method {
		case http.MethodGet:
			handleMemoryRead(w, r, agent, path)
		case http.MethodPost:
			handleMemoryWrite(w, r, agent, path)
		default:
			writeMemoryError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}
}

func handleProfileRead(w http.ResponseWriter, r *http.Request, agent *Agent) {
	profile, err := agent.Profile(r.Context())
	if err != nil {
		writeProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(profileAPIResponse{Configured: profile != nil, Profile: profile})
}

func handleProfileReplace(w http.ResponseWriter, r *http.Request, agent *Agent) {
	var input profileWriteRequest
	if err := decodeJSONBody(w, r, &input); err != nil {
		writeProfileError(w, http.StatusBadRequest, err.Error())
		return
	}
	requested := input.profile()
	if err := validateUserProfile(requested); err != nil {
		writeProfileError(w, http.StatusBadRequest, err.Error())
		return
	}
	profile, err := agent.SetProfile(r.Context(), requested)
	if err != nil {
		writeProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(profileAPIResponse{Configured: true, Profile: profile})
}

func handleLegacyProfileWrite(w http.ResponseWriter, r *http.Request, agent *Agent) {
	var input legacyProfileWriteRequest
	if err := decodeJSONBody(w, r, &input); err != nil {
		writeMemoryError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(input.Key) == "" || strings.TrimSpace(input.Value) == "" {
		writeMemoryError(w, http.StatusBadRequest, "key и value не могут быть пустыми")
		return
	}
	memory, err := agent.UpsertProfile(r.Context(), input.Key, input.Value)
	writeLongTermResult(w, memory, err, "profile")
}

func handleMemoryRead(w http.ResponseWriter, r *http.Request, agent *Agent, path string) {
	dialogID := strings.TrimSpace(r.URL.Query().Get("dialog_id"))
	switch path {
	case "short-term":
		dialog, err := agent.Dialog(r.Context(), dialogID)
		if err != nil {
			writeMemoryError(w, httpStatusForError(err), err.Error())
			return
		}
		memory, err := agent.ShortTermMemoryInDialog(r.Context(), dialog.ID)
		if err != nil {
			writeMemoryError(w, httpStatusForError(err), err.Error())
			return
		}
		_ = json.NewEncoder(w).Encode(memoryAPIResponse{DialogID: dialog.ID, Layer: "short-term", ShortTerm: &memory})
	case "working":
		dialog, err := agent.Dialog(r.Context(), dialogID)
		if err != nil {
			writeMemoryError(w, httpStatusForError(err), err.Error())
			return
		}
		memory, err := agent.WorkingMemoryInDialog(r.Context(), dialog.ID)
		if err != nil {
			writeMemoryError(w, httpStatusForError(err), err.Error())
			return
		}
		_ = json.NewEncoder(w).Encode(memoryAPIResponse{DialogID: dialog.ID, Layer: "working", Working: &memory})
	case "long-term":
		memory, err := agent.LongTermMemory(r.Context())
		if err != nil {
			writeMemoryError(w, http.StatusInternalServerError, err.Error())
			return
		}
		_ = json.NewEncoder(w).Encode(memoryAPIResponse{Layer: "long-term", LongTerm: &memory})
	default:
		writeMemoryError(w, http.StatusNotFound, "неизвестный слой памяти")
	}
}

func handleMemoryWrite(w http.ResponseWriter, r *http.Request, agent *Agent, path string) {
	dialogID := strings.TrimSpace(r.URL.Query().Get("dialog_id"))
	switch path {
	case "working/goal", "working/status", "working/notes", "working/results":
		dialog, err := agent.Dialog(r.Context(), dialogID)
		if err != nil {
			writeMemoryError(w, httpStatusForError(err), err.Error())
			return
		}
		var input workingValueRequest
		if err := decodeJSONBody(w, r, &input); err != nil {
			writeMemoryError(w, http.StatusBadRequest, err.Error())
			return
		}
		input.Value = strings.TrimSpace(input.Value)
		if input.Value == "" {
			writeMemoryError(w, http.StatusBadRequest, "value не может быть пустым")
			return
		}
		category := strings.TrimPrefix(path, "working/")
		var memory WorkingMemory
		switch category {
		case "goal":
			memory, err = agent.SetWorkingGoalInDialog(r.Context(), dialog.ID, input.Value)
		case "status":
			status := TaskStatus(input.Value)
			if !validTaskStatus(status) {
				writeMemoryError(w, http.StatusBadRequest, "неподдерживаемый статус рабочей памяти")
				return
			}
			memory, err = agent.SetWorkingStatusInDialog(r.Context(), dialog.ID, status)
		case "notes":
			memory, err = agent.AddWorkingNoteInDialog(r.Context(), dialog.ID, input.Value)
		case "results":
			memory, err = agent.AddWorkingResultInDialog(r.Context(), dialog.ID, input.Value)
		}
		if err != nil {
			writeMemoryError(w, httpStatusForError(err), err.Error())
			return
		}
		_ = json.NewEncoder(w).Encode(memoryAPIResponse{DialogID: dialog.ID, Layer: "working", Category: category, Working: &memory})
	case "long-term/decisions":
		var input decisionWriteRequest
		if err := decodeJSONBody(w, r, &input); err != nil {
			writeMemoryError(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(input.Statement) == "" {
			writeMemoryError(w, http.StatusBadRequest, "statement не может быть пустым")
			return
		}
		memory, err := agent.AddDecision(r.Context(), input.Statement, input.Rationale)
		writeLongTermResult(w, memory, err, "decisions")
	case "long-term/knowledge":
		var input knowledgeWriteRequest
		if err := decodeJSONBody(w, r, &input); err != nil {
			writeMemoryError(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(input.Topic) == "" || strings.TrimSpace(input.Content) == "" {
			writeMemoryError(w, http.StatusBadRequest, "topic и content не могут быть пустыми")
			return
		}
		memory, err := agent.AddKnowledge(r.Context(), input.Topic, input.Content)
		writeLongTermResult(w, memory, err, "knowledge")
	default:
		writeMemoryError(w, http.StatusNotFound, "неизвестная операция памяти")
	}
}

func writeLongTermResult(w http.ResponseWriter, memory LongTermMemory, err error, category string) {
	if err != nil {
		writeMemoryError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(memoryAPIResponse{Layer: "long-term", Category: category, LongTerm: &memory})
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("некорректный JSON-запрос: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("JSON-запрос должен содержать ровно один объект")
	}
	return nil
}

func decodeOptionalEmptyJSON(w http.ResponseWriter, r *http.Request) error {
	if r.ContentLength == 0 {
		return nil
	}
	var input struct{}
	return decodeJSONBody(w, r, &input)
}

func writeMemoryError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(memoryAPIResponse{Error: message})
}

func writeProfileError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(profileAPIResponse{Profile: nil, Error: message})
}

func writeDialogsError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(dialogsAPIResponse{Error: message})
}

func httpStatusForError(err error) int {
	switch {
	case errors.Is(err, ErrInvalidTaskStage):
		return http.StatusBadRequest
	case errors.Is(err, ErrTaskConflict), errors.Is(err, ErrTaskNotStarted):
		return http.StatusConflict
	case errors.Is(err, ErrInvariantViolation):
		return http.StatusConflict
	case errors.Is(err, ErrInvalidDialogID):
		return http.StatusBadRequest
	case errors.Is(err, ErrDialogNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrDialogConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
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
		writeHistory(w, r, agent, strings.TrimSpace(r.URL.Query().Get("dialog_id")))
	}
}

func writeHistory(w http.ResponseWriter, r *http.Request, agent *Agent, dialogID string) {
	dialog, err := agent.Dialog(r.Context(), dialogID)
	if err != nil {
		w.WriteHeader(httpStatusForError(err))
		_ = json.NewEncoder(w).Encode(historyAPIResponse{Error: err.Error()})
		return
	}
	messages, err := agent.HistoryInDialog(r.Context(), dialog.ID)
	if err != nil {
		w.WriteHeader(httpStatusForError(err))
		_ = json.NewEncoder(w).Encode(historyAPIResponse{DialogID: dialog.ID, Error: err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(historyAPIResponse{DialogID: dialog.ID, Messages: messages})
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
		if err := decodeJSONBody(w, r, &input); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(chatAPIResponse{Error: err.Error()})
			return
		}
		input.Message = strings.TrimSpace(input.Message)
		input.DialogID = strings.TrimSpace(input.DialogID)
		if input.Message == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(chatAPIResponse{Error: "сообщение не может быть пустым"})
			return
		}
		answer, err := agent.AskInDialog(r.Context(), input.DialogID, input.Message)
		if err != nil {
			var taskErr *TaskStateError
			if errors.As(err, &taskErr) {
				writeChatTaskConflict(w, r, agent, input.DialogID, taskErr)
				return
			}
			status := httpStatusForError(err)
			message := "не удалось получить ответ агента"
			if errors.Is(err, ErrDeepSeek) {
				status = http.StatusBadGateway
				message = "агент сейчас недоступен"
			}
			if errors.Is(err, ErrMemoryToolCall) {
				status = http.StatusBadGateway
				message = "агент предложил некорректное обновление памяти"
			}
			var invariantErr *InvariantViolationError
			if errors.As(err, &invariantErr) {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(chatAPIResponse{Blocked: true, Violations: invariantErr.Violations, Error: invariantErr.Error()})
				return
			}
			if errors.Is(err, ErrInvalidDialogID) || errors.Is(err, ErrDialogNotFound) || errors.Is(err, ErrDialogConflict) {
				message = err.Error()
			}
			response := chatAPIResponse{Error: message}
			var toolErr *MemoryToolCallError
			if errors.As(err, &toolErr) {
				response.Error = memoryToolPublicMessage(toolErr)
				response.Tool = toolErr.Tool
				response.ErrorCategory = string(toolErr.Category)
				response.ErrorField = toolErr.Field
				response.JSONType = toolErr.JSONType
				response.ValidationRule = toolErr.Rule
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(response)
			return
		}
		_ = json.NewEncoder(w).Encode(chatAPIResponse{DialogID: answer.DialogID, Content: answer.Content, Usage: answer.Usage, FinishReason: answer.FinishReason, MemoryUpdates: answer.MemoryUpdates, TaskState: answer.TaskState})
	}
}

func writeChatTaskConflict(w http.ResponseWriter, r *http.Request, agent *Agent, dialogID string, taskErr *TaskStateError) {
	response := chatTaskConflictResponse{
		Error:              "операция состояния задачи отклонена",
		RequestedStage:     taskErr.RequestedStage,
		AllowedTransitions: append([]TaskStage{}, taskErr.AllowedTransitions...),
		ExpectedAction:     taskErr.ExpectedAction,
		Reason:             taskErr.Reason,
		ErrorCategory:      "fsm_conflict",
		FSMGuard:           true,
	}
	dialog, err := agent.Dialog(r.Context(), dialogID)
	if err == nil {
		response.DialogID = dialog.ID
		state, stateErr := agent.TaskStateInDialog(r.Context(), dialog.ID)
		if stateErr == nil && state != nil {
			copy := *state
			response.TaskState = &copy
			response.CurrentStage = state.Stage
			response.AllowedTransitions = state.AllowedTransitions()
			response.ExpectedAction = state.ExpectedAction
		}
	}
	if response.TaskState == nil {
		response.CurrentStage = taskErr.CurrentStage
	}
	if response.AllowedTransitions == nil {
		response.AllowedTransitions = []TaskStage{}
	}
	if response.ExpectedAction == "" {
		response.ExpectedAction = "Создать задачу перед выполнением task operation"
	}
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(response)
}

func memoryToolPublicMessage(err *MemoryToolCallError) string {
	if err == nil {
		return "агент предложил некорректное обновление памяти"
	}
	base := "агент предложил некорректный вызов инструмента"
	if err.Tool != "" {
		base += " " + err.Tool
	}
	switch err.Category {
	case MemoryToolErrorMalformedJSON:
		return base + ": некорректный JSON"
	case MemoryToolErrorMissingField:
		return base + ": отсутствует обязательное поле"
	case MemoryToolErrorUnknownField:
		return base + ": передано неизвестное поле"
	case MemoryToolErrorInvalidType:
		return base + ": неверный JSON-тип поля"
	case MemoryToolErrorTrailingJSON:
		return base + ": ожидался один JSON-объект"
	case MemoryToolErrorInvalidValue:
		return base + ": некорректное значение поля"
	case MemoryToolErrorTooLong:
		return base + ": значение превышает лимит"
	case MemoryToolErrorSensitive:
		return base + ": обнаружены чувствительные данные"
	case MemoryToolErrorUnknownTool:
		return "агент предложил неизвестный или недоступный инструмент"
	default:
		return base + ": некорректный формат"
	}
}
