package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"ai-challenge/day-25/internal/agent"
	"ai-challenge/day-25/internal/chat"
	"ai-challenge/day-25/internal/chatservice"
	"ai-challenge/day-25/internal/chatstore"
	"ai-challenge/day-25/internal/evidence"
	"ai-challenge/day-25/internal/judging"
)

const sessionCookie = "day25_session"

var availableModes = []chatservice.Mode{chatservice.Stateless, chatservice.History, chatservice.TaskMemory}

type TurnService interface {
	Ask(context.Context, string, chatservice.Mode, string) (chatservice.TurnResult, error)
}

type SessionStore interface {
	Create(context.Context, string) (chat.Session, error)
	Load(context.Context, string) (chat.Session, error)
	List(context.Context) ([]chat.SessionSummary, error)
	Reset(context.Context, string) error
}

type Config struct {
	DefaultMode    chatservice.Mode
	RequestTimeout time.Duration
	CookieSecure   bool
	CookieTTL      time.Duration
	MaxBodyBytes   int64
	Logger         *log.Logger
	IDGenerator    func() (string, error)
}

type server struct {
	service TurnService
	store   SessionStore
	config  Config
	locks   sessionLocks
}

type sessionLocks struct {
	mu    sync.Mutex
	items map[string]*lockEntry
}

type lockEntry struct {
	mu   sync.Mutex
	refs int
}

func (l *sessionLocks) acquire(id string) func() {
	l.mu.Lock()
	if l.items == nil {
		l.items = make(map[string]*lockEntry)
	}
	entry := l.items[id]
	if entry == nil {
		entry = &lockEntry{}
		l.items[id] = entry
	}
	entry.refs++
	l.mu.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		l.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(l.items, id)
		}
		l.mu.Unlock()
	}
}

func Handler(service TurnService, store SessionStore, config Config) http.Handler {
	if !config.DefaultMode.Valid() {
		config.DefaultMode = chatservice.TaskMemory
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 2 * time.Minute
	}
	if config.CookieTTL <= 0 || config.CookieTTL > 30*24*time.Hour {
		config.CookieTTL = 7 * 24 * time.Hour
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = 64 << 10
	}
	if config.Logger == nil {
		config.Logger = log.Default()
	}
	if config.IDGenerator == nil {
		config.IDGenerator = randomSessionID
	}
	s := &server{service: service, store: store, config: config, locks: sessionLocks{items: make(map[string]*lockEntry)}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.root)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/bootstrap", s.bootstrap)
	mux.HandleFunc("GET /api/sessions", s.listSessions)
	mux.HandleFunc("POST /api/sessions", s.createSession)
	mux.HandleFunc("POST /api/sessions/select", s.selectSession)
	mux.HandleFunc("GET /api/sessions/{id}", s.getSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.deleteSession)
	mux.HandleFunc("POST /api/chat", s.chat)
	return securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), config.RequestTimeout)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	}))
}

func (s *server) root(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, chatPageHTML)
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "day25-rag-web"})
}

type bootstrapResponse struct {
	CurrentSessionID string                `json:"current_session_id"`
	Sessions         []chat.SessionSummary `json:"sessions"`
	Messages         []chat.Message        `json:"messages"`
	TaskState        chat.TaskState        `json:"task_state"`
	LastTurn         *turnView             `json:"last_turn,omitempty"`
	SelectedMode     chatservice.Mode      `json:"selected_mode"`
	AvailableModes   []chatservice.Mode    `json:"available_modes"`
}

func (s *server) bootstrap(w http.ResponseWriter, r *http.Request) {
	id, session, err := s.currentSession(w, r)
	if err != nil {
		s.internalError(w, "bootstrap", err)
		return
	}
	items, err := s.store.List(r.Context())
	if err != nil {
		s.internalError(w, "list sessions for bootstrap", err)
		return
	}
	writeJSON(w, http.StatusOK, bootstrapResponse{CurrentSessionID: id, Sessions: items, Messages: session.Messages, TaskState: session.TaskState, LastTurn: lastTurn(session), SelectedMode: selectedMode(session, s.config.DefaultMode), AvailableModes: availableModes})
}

func (s *server) listSessions(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.List(r.Context())
	if err != nil {
		s.internalError(w, "list sessions", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": items})
}

type sessionRequest struct {
	SessionID string `json:"session_id"`
}

func (s *server) createSession(w http.ResponseWriter, r *http.Request) {
	var input sessionRequest
	if !s.decode(w, r, &input) {
		return
	}
	id := strings.TrimSpace(input.SessionID)
	if id == "" {
		var err error
		id, err = s.config.IDGenerator()
		if err != nil {
			s.internalError(w, "generate session ID", err)
			return
		}
	}
	if err := chatstore.ValidateID(id); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_session_id", "Некорректный ID сессии.")
		return
	}
	release := s.locks.acquire(id)
	defer release()
	session, err := s.store.Create(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "create session", err)
		return
	}
	s.setSessionCookie(w, id)
	items, err := s.store.List(r.Context())
	if err != nil {
		s.internalError(w, "list sessions after create", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"current_session_id": id, "sessions": items, "session": sessionViewFrom(session)})
}

func (s *server) selectSession(w http.ResponseWriter, r *http.Request) {
	var input sessionRequest
	if !s.decode(w, r, &input) {
		return
	}
	id := strings.TrimSpace(input.SessionID)
	if err := chatstore.ValidateID(id); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_session_id", "Некорректный ID сессии.")
		return
	}
	session, err := s.store.Load(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "select session", err)
		return
	}
	s.setSessionCookie(w, id)
	writeJSON(w, http.StatusOK, map[string]any{"current_session_id": id, "session": sessionViewFrom(session)})
}

func (s *server) getSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := chatstore.ValidateID(id); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_session_id", "Некорректный ID сессии.")
		return
	}
	session, err := s.store.Load(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "load session", err)
		return
	}
	writeJSON(w, http.StatusOK, sessionViewFrom(session))
}

func (s *server) deleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := chatstore.ValidateID(id); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_session_id", "Некорректный ID сессии.")
		return
	}
	preferredID := ""
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != id && chatstore.ValidateID(cookie.Value) == nil {
		preferredID = cookie.Value
	}
	release := s.locks.acquire(id)
	if err := s.store.Reset(r.Context(), id); err != nil {
		release()
		s.writeStoreError(w, "delete session", err)
		return
	}
	release()
	currentID, current, err := s.chooseSessionAfterDelete(r.Context(), id, preferredID)
	if err != nil {
		s.internalError(w, "select session after delete", err)
		return
	}
	s.setSessionCookie(w, currentID)
	items, err := s.store.List(r.Context())
	if err != nil {
		s.internalError(w, "list sessions after delete", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted_session_id": id, "current_session_id": currentID, "sessions": items, "session": sessionViewFrom(current)})
}

type chatRequest struct {
	SessionID string `json:"session_id"`
	Mode      string `json:"mode"`
	Message   string `json:"message"`
}

type errorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type chatResponse struct {
	SessionID             string              `json:"session_id"`
	Mode                  chatservice.Mode    `json:"mode"`
	Answer                string              `json:"answer"`
	Status                string              `json:"status"`
	ClarificationQuestion string              `json:"clarification_question"`
	Sources               []evidence.Source   `json:"sources"`
	Citations             []evidence.Citation `json:"citations"`
	Claims                []evidence.Claim    `json:"claims"`
	TaskState             chat.TaskState      `json:"task_state"`
	Resolution            resolutionView      `json:"resolution"`
	MemoryUpdateFailed    bool                `json:"memory_update_failed"`
	Latency               chat.StageTiming    `json:"latency"`
	TokenUsage            chat.TurnTokens     `json:"token_usage"`
	PersistedStatus       string              `json:"persisted_status"`
	AbstentionReason      string              `json:"abstention_reason"`
	Trace                 *turnView           `json:"trace,omitempty"`
	Error                 *errorInfo          `json:"error,omitempty"`
}

func (s *server) chat(w http.ResponseWriter, r *http.Request) {
	var input chatRequest
	if !s.decode(w, r, &input) {
		return
	}
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.Message = strings.TrimSpace(input.Message)
	mode := chatservice.Mode(strings.ToLower(strings.TrimSpace(input.Mode)))
	if err := chatstore.ValidateID(input.SessionID); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_session_id", "Некорректный ID сессии.")
		return
	}
	if !mode.Valid() {
		writeAPIError(w, http.StatusBadRequest, "invalid_mode", "Режим должен быть stateless, history или task-memory.")
		return
	}
	if input.Message == "" {
		writeAPIError(w, http.StatusBadRequest, "empty_message", "Сообщение не может быть пустым.")
		return
	}
	limits := chat.DefaultLimits()
	if utf8.RuneCountInString(input.Message) > limits.MaxMessageRunes {
		writeAPIError(w, http.StatusBadRequest, "message_too_long", "Сообщение превышает допустимую длину.")
		return
	}
	release := s.locks.acquire(input.SessionID)
	defer release()
	storedBefore, err := s.store.Load(r.Context(), input.SessionID)
	if err != nil {
		s.writeStoreError(w, "load chat session", err)
		return
	}
	if len(storedBefore.Messages)+2 > limits.MaxSessionMessages {
		writeAPIError(w, http.StatusBadRequest, "session_message_limit", "Сессия достигла лимита сообщений. Создайте новый чат.")
		return
	}
	result, err := s.service.Ask(r.Context(), input.SessionID, mode, input.Message)
	if err != nil {
		s.logError("chat turn", err)
		status, code, message := mapTurnError(err)
		persisted := "unchanged"
		if stored, loadErr := s.store.Load(context.Background(), input.SessionID); loadErr == nil && len(stored.Messages) > 0 && stored.Messages[len(stored.Messages)-1].Role == chat.RoleUser {
			persisted = "user_only"
		}
		writeJSON(w, status, chatResponse{SessionID: input.SessionID, Mode: mode, Status: "error", Sources: []evidence.Source{}, Citations: []evidence.Citation{}, Claims: []evidence.Claim{}, PersistedStatus: persisted, Error: &errorInfo{Code: code, Message: message}})
		return
	}
	// Reload from the store because FileStore.Save updates UpdatedAt on its own
	// copy and because the store is the sole authoritative representation.
	stored, loadErr := s.store.Load(r.Context(), input.SessionID)
	if loadErr != nil {
		s.internalError(w, "reload committed turn", loadErr)
		return
	}
	s.setSessionCookie(w, input.SessionID)
	response := chatResponseFrom(input.SessionID, mode, stored, result)
	if result.RAG.Status == agent.StatusError {
		response.Error = &errorInfo{Code: "model_runtime", Message: "Локальная модель не смогла завершить запрос. Сохранённое состояние можно повторно загрузить."}
		writeJSON(w, http.StatusBadGateway, response)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

type sessionView struct {
	ID        string         `json:"id"`
	Version   int64          `json:"version"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Messages  []chat.Message `json:"messages"`
	TaskState chat.TaskState `json:"task_state"`
	LastTurn  *turnView      `json:"last_turn,omitempty"`
}

type resolutionView struct {
	OriginalQuestion string   `json:"original_question"`
	SearchQuery      string   `json:"search_query"`
	Fallback         bool     `json:"fallback"`
	UsedTurnIDs      []string `json:"used_turn_ids"`
	UsedMemoryIDs    []string `json:"used_memory_ids"`
}

type validationAttemptView struct {
	Attempt int              `json:"attempt"`
	Errors  []evidence.Error `json:"errors"`
	Valid   bool             `json:"valid"`
}

type memoryAttemptView struct {
	Attempt int      `json:"attempt"`
	Errors  []string `json:"errors"`
	Valid   bool     `json:"valid"`
}

type judgeView struct {
	ClaimID   string `json:"claim_id"`
	Verdict   string `json:"verdict"`
	Reason    string `json:"reason"`
	LatencyMS int64  `json:"latency_ms"`
}

type turnView struct {
	Turn                     int                     `json:"turn"`
	Mode                     string                  `json:"mode"`
	Status                   string                  `json:"status"`
	AbstentionReason         string                  `json:"abstention_reason"`
	ClarificationQuestion    string                  `json:"clarification_question"`
	Resolution               resolutionView          `json:"resolution"`
	Gate                     agent.Gate              `json:"gate"`
	Pipeline                 agent.PipelineConfig    `json:"pipeline"`
	Sources                  []evidence.Source       `json:"sources"`
	Citations                []evidence.Citation     `json:"citations"`
	Claims                   []evidence.Claim        `json:"claims"`
	ValidationAttempts       []validationAttemptView `json:"validation_attempts"`
	MemoryValidationAttempts []memoryAttemptView     `json:"memory_validation_attempts"`
	Entailment               []judgeView             `json:"entailment"`
	MemoryUpdateFailed       bool                    `json:"memory_update_failed"`
	Latency                  chat.StageTiming        `json:"latency"`
	TokenUsage               chat.TurnTokens         `json:"token_usage"`
	PersistedStatus          string                  `json:"persisted_status"`
	SessionVersionBefore     int64                   `json:"session_version_before"`
	SessionVersionAfter      int64                   `json:"session_version_after"`
	PromptHistoryRunes       int                     `json:"prompt_history_runes"`
	TaskStateRunes           int                     `json:"task_state_runes"`
	FinalContextRunes        int                     `json:"final_context_runes"`
}

func sessionViewFrom(session chat.Session) sessionView {
	return sessionView{ID: session.ID, Version: session.Version, CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt, Messages: session.Messages, TaskState: session.TaskState, LastTurn: lastTurn(session)}
}

func lastTurn(session chat.Session) *turnView {
	if len(session.Turns) == 0 {
		return nil
	}
	record := session.Turns[len(session.Turns)-1]
	return projectTurn(record, decodeRAG(record.RAGResult))
}

func decodeRAG(raw json.RawMessage) agent.Result {
	var result agent.Result
	_ = json.Unmarshal(raw, &result)
	return result
}

func projectTurn(record chat.TurnRecord, rag agent.Result) *turnView {
	validation := make([]validationAttemptView, 0, len(rag.ValidationAttempts))
	for _, item := range rag.ValidationAttempts {
		validation = append(validation, validationAttemptView{Attempt: item.Attempt, Errors: item.Errors, Valid: item.Valid})
	}
	memoryValidation := make([]memoryAttemptView, 0, len(record.MemoryValidationAttempts))
	for _, item := range record.MemoryValidationAttempts {
		memoryValidation = append(memoryValidation, memoryAttemptView{Attempt: item.Attempt, Errors: append([]string(nil), item.Errors...), Valid: item.Valid})
	}
	entailment := make([]judgeView, 0, len(rag.Entailment))
	for _, item := range rag.Entailment {
		entailment = append(entailment, judgeViewFrom(item))
	}
	sources := safeSources(rag.Sources)
	return &turnView{
		Turn: record.Turn, Mode: record.Mode, Status: record.Status, AbstentionReason: record.AbstentionReason, ClarificationQuestion: record.ClarificationQuestion,
		Resolution: resolutionView{OriginalQuestion: record.CurrentUserMessage, SearchQuery: record.Resolution.Result.SearchQuery, Fallback: record.Resolution.Fallback, UsedTurnIDs: nonNil(record.Resolution.Result.UsedTurnIDs), UsedMemoryIDs: nonNil(record.Resolution.Result.UsedMemoryIDs)},
		Gate:       rag.Gate, Pipeline: rag.Pipeline, Sources: sources, Citations: nonNil(rag.Citations), Claims: nonNil(rag.Claims), ValidationAttempts: validation, MemoryValidationAttempts: memoryValidation, Entailment: entailment,
		MemoryUpdateFailed: record.MemoryUpdateFailed, Latency: record.Timing, TokenUsage: record.Tokens, PersistedStatus: record.PersistedStatus, SessionVersionBefore: record.SessionVersionBefore, SessionVersionAfter: record.SessionVersionAfter,
		PromptHistoryRunes: record.PromptHistoryRunes, TaskStateRunes: record.TaskStateRunes, FinalContextRunes: record.FinalContextRunes,
	}
}

func judgeViewFrom(item judging.Result) judgeView {
	return judgeView{ClaimID: item.ClaimID, Verdict: item.Verdict, Reason: item.Reason, LatencyMS: item.LatencyMS}
}

func chatResponseFrom(id string, mode chatservice.Mode, stored chat.Session, result chatservice.TurnResult) chatResponse {
	trace := projectTurn(result.Record, result.RAG)
	return chatResponse{SessionID: id, Mode: mode, Answer: result.Record.Answer, Status: result.Record.Status, ClarificationQuestion: result.Record.ClarificationQuestion, Sources: safeSources(result.RAG.Sources), Citations: nonNil(result.RAG.Citations), Claims: nonNil(result.RAG.Claims), TaskState: stored.TaskState, Resolution: trace.Resolution, MemoryUpdateFailed: result.Record.MemoryUpdateFailed, Latency: result.Record.Timing, TokenUsage: result.Record.Tokens, PersistedStatus: result.Record.PersistedStatus, AbstentionReason: result.Record.AbstentionReason, Trace: trace}
}

func safeSources(items []evidence.Source) []evidence.Source {
	out := make([]evidence.Source, 0, len(items))
	for _, item := range items {
		if isAbsoluteAnyPlatform(item.Source) {
			item.Source = baseAnyPlatform(item.Source)
		}
		out = append(out, item)
	}
	return out
}

func isAbsoluteAnyPlatform(value string) bool {
	if filepath.IsAbs(value) || strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, "//") {
		return true
	}
	runes := []rune(value)
	return len(runes) >= 3 && unicode.IsLetter(runes[0]) && runes[1] == ':' && (runes[2] == '\\' || runes[2] == '/')
}

func baseAnyPlatform(value string) string {
	value = strings.TrimRight(value, `/\`)
	if index := strings.LastIndexAny(value, `/\`); index >= 0 {
		return value[index+1:]
	}
	return filepath.Base(value)
}

func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

func selectedMode(session chat.Session, fallback chatservice.Mode) chatservice.Mode {
	if len(session.Turns) > 0 {
		mode := chatservice.Mode(session.Turns[len(session.Turns)-1].Mode)
		if mode.Valid() {
			return mode
		}
	}
	return fallback
}

func (s *server) currentSession(w http.ResponseWriter, r *http.Request) (string, chat.Session, error) {
	if cookie, err := r.Cookie(sessionCookie); err == nil && chatstore.ValidateID(cookie.Value) == nil {
		if session, loadErr := s.store.Load(r.Context(), cookie.Value); loadErr == nil {
			s.setSessionCookie(w, cookie.Value)
			return cookie.Value, session, nil
		} else if !errors.Is(loadErr, chatstore.ErrNotFound) {
			return "", chat.Session{}, loadErr
		}
	}
	items, err := s.store.List(r.Context())
	if err != nil {
		return "", chat.Session{}, err
	}
	if len(items) > 0 {
		session, err := s.store.Load(r.Context(), items[0].ID)
		if err != nil {
			return "", chat.Session{}, err
		}
		s.setSessionCookie(w, session.ID)
		return session.ID, session, nil
	}
	id, err := s.config.IDGenerator()
	if err != nil {
		return "", chat.Session{}, err
	}
	if err := chatstore.ValidateID(id); err != nil {
		return "", chat.Session{}, fmt.Errorf("generated session ID: %w", err)
	}
	session, err := s.store.Create(r.Context(), id)
	if err != nil {
		return "", chat.Session{}, err
	}
	s.setSessionCookie(w, id)
	return id, session, nil
}

func (s *server) chooseSessionAfterDelete(ctx context.Context, deleted, preferred string) (string, chat.Session, error) {
	if preferred != "" && preferred != deleted {
		session, err := s.store.Load(ctx, preferred)
		if err == nil {
			return preferred, session, nil
		}
		if !errors.Is(err, chatstore.ErrNotFound) {
			return "", chat.Session{}, err
		}
	}
	items, err := s.store.List(ctx)
	if err != nil {
		return "", chat.Session{}, err
	}
	for _, item := range items {
		if item.ID != deleted {
			session, err := s.store.Load(ctx, item.ID)
			return item.ID, session, err
		}
	}
	id, err := s.config.IDGenerator()
	if err != nil {
		return "", chat.Session{}, err
	}
	if err := chatstore.ValidateID(id); err != nil {
		return "", chat.Session{}, err
	}
	session, err := s.store.Create(ctx, id)
	return id, session, err
}

func (s *server) setSessionCookie(w http.ResponseWriter, id string) {
	maxAge := int(s.config.CookieTTL / time.Second)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: id, Path: "/", HttpOnly: true, Secure: s.config.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: maxAge, Expires: time.Now().Add(s.config.CookieTTL)})
}

func randomSessionID() (string, error) {
	data := make([]byte, 18)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	id := "s_" + base64.RawURLEncoding.EncodeToString(data)
	if err := chatstore.ValidateID(id); err != nil {
		return "", err
	}
	return id, nil
}

func (s *server) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "application/json" {
			writeAPIError(w, http.StatusBadRequest, "invalid_content_type", "Ожидается application/json.")
			return false
		}
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.config.MaxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Тело запроса слишком большое.")
		} else {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "Некорректный JSON-запрос.")
		}
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Тело запроса слишком большое.")
		} else {
			writeAPIError(w, http.StatusBadRequest, "trailing_json", "Разрешён только один JSON-объект.")
		}
		return false
	}
	return true
}

func (s *server) writeStoreError(w http.ResponseWriter, operation string, err error) {
	s.logError(operation, err)
	switch {
	case errors.Is(err, chatstore.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "session_not_found", "Сессия не найдена.")
	case errors.Is(err, chatstore.ErrAlreadyExists):
		writeAPIError(w, http.StatusConflict, "session_exists", "Сессия с таким ID уже существует.")
	case errors.Is(err, chatstore.ErrVersionConflict):
		writeAPIError(w, http.StatusConflict, "version_conflict", "Сессия изменилась параллельно. Перезагрузите её.")
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		writeAPIError(w, http.StatusGatewayTimeout, "timeout", "Запрос не завершился вовремя.")
	default:
		writeAPIError(w, http.StatusInternalServerError, "internal", "Внутренняя ошибка хранилища.")
	}
}

func mapTurnError(err error) (int, string, string) {
	switch {
	case errors.Is(err, chatstore.ErrVersionConflict):
		return http.StatusConflict, "version_conflict", "Сессия изменилась параллельно. Сохранённое состояние будет перезагружено."
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return http.StatusGatewayTimeout, "timeout", "Локальная модель не ответила вовремя. Сохранённое состояние будет перезагружено."
	default:
		return http.StatusInternalServerError, "turn_failed", "Не удалось завершить turn. Сохранённое состояние будет перезагружено."
	}
}

func (s *server) internalError(w http.ResponseWriter, operation string, err error) {
	s.logError(operation, err)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		writeAPIError(w, http.StatusGatewayTimeout, "timeout", "Запрос не завершился вовремя.")
		return
	}
	writeAPIError(w, http.StatusInternalServerError, "internal", "Внутренняя ошибка сервера.")
}

func (s *server) logError(operation string, err error) {
	s.config.Logger.Printf("%s: %v", operation, err)
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": errorInfo{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
