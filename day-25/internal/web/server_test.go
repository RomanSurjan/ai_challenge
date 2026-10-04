package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ai-challenge/day-25/internal/agent"
	"ai-challenge/day-25/internal/chat"
	"ai-challenge/day-25/internal/chatservice"
	"ai-challenge/day-25/internal/chatstore"
	"ai-challenge/day-25/internal/evidence"
	"ai-challenge/day-25/internal/judging"
)

type serviceFunc func(context.Context, string, chatservice.Mode, string) (chatservice.TurnResult, error)

func (f serviceFunc) Ask(ctx context.Context, id string, mode chatservice.Mode, message string) (chatservice.TurnResult, error) {
	return f(ctx, id, mode, message)
}

func testConfig() Config {
	var mu sync.Mutex
	next := 0
	return Config{
		DefaultMode:    chatservice.TaskMemory,
		RequestTimeout: 2 * time.Second,
		CookieSecure:   true,
		CookieTTL:      time.Hour,
		MaxBodyBytes:   64 << 10,
		Logger:         log.New(io.Discard, "", 0),
		IDGenerator: func() (string, error) {
			mu.Lock()
			defer mu.Unlock()
			next++
			return fmt.Sprintf("generated-session-%d", next), nil
		},
	}
}

func mustStore(t *testing.T) *chatstore.FileStore {
	t.Helper()
	store, err := chatstore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func mustCreateSession(t *testing.T, store *chatstore.FileStore, id string) chat.Session {
	t.Helper()
	session, err := store.Create(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func jsonRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func assertSecurityHeaders(t *testing.T, response *http.Response) {
	t.Helper()
	checks := map[string]string{
		"Content-Security-Policy": "default-src 'self'",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
	}
	for name, want := range checks {
		if got := response.Header.Get(name); !strings.Contains(got, want) {
			t.Errorf("%s = %q, want substring %q", name, got, want)
		}
	}
}

func assertAPIError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, status, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON", got)
	}
	var payload struct {
		Error errorInfo `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, recorder.Body.String())
	}
	if payload.Error.Code != code || strings.TrimSpace(payload.Error.Message) == "" {
		t.Fatalf("error = %+v, want code %q and a safe message", payload.Error, code)
	}
}

func TestRootHealthSecurityHeadersAndContentTypes(t *testing.T) {
	handler := Handler(nil, nil, testConfig())

	root := httptest.NewRecorder()
	handler.ServeHTTP(root, httptest.NewRequest(http.MethodGet, "/", nil))
	if root.Code != http.StatusOK {
		t.Fatalf("root status=%d body=%s", root.Code, root.Body.String())
	}
	if got := root.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("root Content-Type=%q", got)
	}
	if !strings.Contains(strings.ToLower(root.Body.String()), "<!doctype html") {
		t.Fatal("root did not serve the embedded application")
	}
	assertSecurityHeaders(t, root.Result())

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", health.Code, health.Body.String())
	}
	if got := health.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("health Content-Type=%q", got)
	}
	var payload map[string]string
	if err := json.Unmarshal(health.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "ok" || payload["service"] != "day25-rag-web" {
		t.Fatalf("health payload=%v", payload)
	}
	assertSecurityHeaders(t, health.Result())
}

func TestStrictJSONValidationBodyLimitAndUnsafeIDs(t *testing.T) {
	store := mustStore(t)
	mustCreateSession(t, store, "safe")
	service := serviceFunc(func(context.Context, string, chatservice.Mode, string) (chatservice.TurnResult, error) {
		t.Fatal("invalid request reached chat service")
		return chatservice.TurnResult{}, nil
	})
	handler := Handler(service, store, testConfig())

	tests := []struct {
		name, method, target, body, code string
		status                           int
	}{
		{name: "malformed JSON", method: http.MethodPost, target: "/api/sessions", body: `{`, status: http.StatusBadRequest, code: "invalid_json"},
		{name: "unknown field", method: http.MethodPost, target: "/api/sessions", body: `{"session_id":"new","extra":true}`, status: http.StatusBadRequest, code: "invalid_json"},
		{name: "trailing JSON", method: http.MethodPost, target: "/api/sessions", body: `{"session_id":"new"}{"session_id":"other"}`, status: http.StatusBadRequest, code: "trailing_json"},
		{name: "empty message", method: http.MethodPost, target: "/api/chat", body: `{"session_id":"safe","mode":"history","message":"  "}`, status: http.StatusBadRequest, code: "empty_message"},
		{name: "invalid mode", method: http.MethodPost, target: "/api/chat", body: `{"session_id":"safe","mode":"omniscient","message":"hello"}`, status: http.StatusBadRequest, code: "invalid_mode"},
		{name: "unsafe chat ID", method: http.MethodPost, target: "/api/chat", body: `{"session_id":"../secret","mode":"history","message":"hello"}`, status: http.StatusBadRequest, code: "invalid_session_id"},
		{name: "unsafe create ID", method: http.MethodPost, target: "/api/sessions", body: `{"session_id":"a/b"}`, status: http.StatusBadRequest, code: "invalid_session_id"},
		{name: "unsafe select ID", method: http.MethodPost, target: "/api/sessions/select", body: `{"session_id":".."}`, status: http.StatusBadRequest, code: "invalid_session_id"},
		{name: "encoded slash traversal", method: http.MethodGet, target: "/api/sessions/%2e%2e%2fsecret", status: http.StatusBadRequest, code: "invalid_session_id"},
		{name: "encoded absolute path", method: http.MethodDelete, target: "/api/sessions/%2ftmp%2fsecret", status: http.StatusBadRequest, code: "invalid_session_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var request *http.Request
			if tt.body == "" {
				request = httptest.NewRequest(tt.method, tt.target, nil)
			} else {
				request = jsonRequest(tt.method, tt.target, tt.body)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			assertAPIError(t, recorder, tt.status, tt.code)
		})
	}

	badContentType := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(`{"session_id":"new"}`))
	badContentType.Header.Set("Content-Type", "text/plain")
	badContentTypeRecorder := httptest.NewRecorder()
	handler.ServeHTTP(badContentTypeRecorder, badContentType)
	assertAPIError(t, badContentTypeRecorder, http.StatusBadRequest, "invalid_content_type")

	config := testConfig()
	config.MaxBodyBytes = 48
	smallHandler := Handler(service, store, config)
	tooLarge := httptest.NewRecorder()
	smallHandler.ServeHTTP(tooLarge, jsonRequest(http.MethodPost, "/api/chat", `{"session_id":"safe","mode":"history","message":"`+strings.Repeat("x", 100)+`"}`))
	assertAPIError(t, tooLarge, http.StatusRequestEntityTooLarge, "request_too_large")

	messageTooLong := httptest.NewRecorder()
	handler.ServeHTTP(messageTooLong, jsonRequest(http.MethodPost, "/api/chat", `{"session_id":"safe","mode":"history","message":"`+strings.Repeat("x", chat.DefaultLimits().MaxMessageRunes+1)+`"}`))
	assertAPIError(t, messageTooLong, http.StatusBadRequest, "message_too_long")
}

func TestSessionLifecycleCookieAttributesAndReuse(t *testing.T) {
	store := mustStore(t)
	config := testConfig()
	handler := Handler(nil, store, config)

	generated := httptest.NewRecorder()
	handler.ServeHTTP(generated, jsonRequest(http.MethodPost, "/api/sessions", `{"session_id":""}`))
	if generated.Code != http.StatusCreated {
		t.Fatalf("generated create status=%d body=%s", generated.Code, generated.Body.String())
	}
	cookies := generated.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%#v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != sessionCookie || cookie.Value != "generated-session-1" || cookie.Path != "/" || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != int(time.Hour/time.Second) || cookie.Expires.IsZero() {
		t.Fatalf("cookie=%#v", cookie)
	}
	if err := chatstore.ValidateID(cookie.Value); err != nil {
		t.Fatalf("cookie ID is unsafe: %v", err)
	}

	alpha := httptest.NewRecorder()
	handler.ServeHTTP(alpha, jsonRequest(http.MethodPost, "/api/sessions", `{"session_id":"alpha"}`))
	if alpha.Code != http.StatusCreated {
		t.Fatalf("alpha create status=%d body=%s", alpha.Code, alpha.Body.String())
	}

	selected := httptest.NewRecorder()
	handler.ServeHTTP(selected, jsonRequest(http.MethodPost, "/api/sessions/select", `{"session_id":"generated-session-1"}`))
	if selected.Code != http.StatusOK || len(selected.Result().Cookies()) != 1 || selected.Result().Cookies()[0].Value != cookie.Value {
		t.Fatalf("select status=%d cookies=%#v body=%s", selected.Code, selected.Result().Cookies(), selected.Body.String())
	}

	bootstrapRequest := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	bootstrapRequest.AddCookie(cookie)
	bootstrap := httptest.NewRecorder()
	handler.ServeHTTP(bootstrap, bootstrapRequest)
	if bootstrap.Code != http.StatusOK {
		t.Fatalf("bootstrap status=%d body=%s", bootstrap.Code, bootstrap.Body.String())
	}
	var initial bootstrapResponse
	if err := json.Unmarshal(bootstrap.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if initial.CurrentSessionID != cookie.Value || len(initial.Sessions) != 2 || len(bootstrap.Result().Cookies()) != 1 || bootstrap.Result().Cookies()[0].Value != cookie.Value {
		t.Fatalf("bootstrap=%+v cookies=%#v", initial, bootstrap.Result().Cookies())
	}

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"alpha"`) || !strings.Contains(list.Body.String(), `"generated-session-1"`) {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}

	loaded := httptest.NewRecorder()
	handler.ServeHTTP(loaded, httptest.NewRequest(http.MethodGet, "/api/sessions/alpha", nil))
	if loaded.Code != http.StatusOK {
		t.Fatalf("load status=%d body=%s", loaded.Code, loaded.Body.String())
	}
	var view sessionView
	if err := json.Unmarshal(loaded.Body.Bytes(), &view); err != nil || view.ID != "alpha" {
		t.Fatalf("view=%+v err=%v", view, err)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/sessions/alpha", nil)
	deleteRequest.AddCookie(cookie)
	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, deleteRequest)
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"deleted_session_id":"alpha"`) {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	if got := deleted.Result().Cookies(); len(got) != 1 || got[0].Value != cookie.Value {
		t.Fatalf("deleting a background session changed the selected cookie: %#v", got)
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/sessions/alpha", nil))
	assertAPIError(t, missing, http.StatusNotFound, "session_not_found")
	if _, err := store.Load(context.Background(), "generated-session-1"); err != nil {
		t.Fatalf("deleting alpha damaged another session: %v", err)
	}
}

func TestBootstrapRecoversHistoryTaskStateAndTraceAfterRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := chatstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	session := mustCreateSession(t, store, "recover")
	goal := chat.MemoryItem{ID: "Mgoal", Kind: chat.KindGoal, Value: "audit persistence", SourceTurnID: "U1", UserQuote: "Goal: audit persistence"}
	record := chat.TurnRecord{
		Turn: 1, Mode: string(chatservice.TaskMemory), UserMessageID: "U1", AssistantMessageID: "A1", CurrentUserMessage: "Goal: audit persistence",
		TaskStateAfter: chat.TaskState{Goal: &goal}, Resolution: chat.ResolutionTrace{Result: chat.Resolution{SearchQuery: "audit persistence", UsedTurnIDs: []string{"U1"}, UsedMemoryIDs: []string{"Mgoal"}}},
		Status: agent.StatusAnswered, Answer: "saved", PersistedStatus: "committed", SessionVersionBefore: 1, SessionVersionAfter: 2,
	}
	ragJSON, _ := json.Marshal(agent.Result{Status: agent.StatusAnswered, Sources: []evidence.Source{}, Citations: []evidence.Citation{}, Claims: []evidence.Claim{}})
	record.RAGResult = ragJSON
	session.Messages = []chat.Message{{ID: "U1", Turn: 1, Role: chat.RoleUser, Content: "Goal: audit persistence"}, {ID: "A1", Turn: 1, Role: chat.RoleAssistant, Content: "saved"}}
	session.TaskState = record.TaskStateAfter
	session.Turns = []chat.TurnRecord{record}
	session.Version++
	if err := store.Save(context.Background(), 1, session); err != nil {
		t.Fatal(err)
	}

	firstHandler := Handler(nil, store, testConfig())
	first := httptest.NewRecorder()
	firstHandler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil))
	if first.Code != http.StatusOK || len(first.Result().Cookies()) != 1 {
		t.Fatalf("first bootstrap status=%d cookies=%#v body=%s", first.Code, first.Result().Cookies(), first.Body.String())
	}

	restartedStore, err := chatstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	restartedHandler := Handler(nil, restartedStore, testConfig())
	restartRequest := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	restartRequest.AddCookie(first.Result().Cookies()[0])
	restarted := httptest.NewRecorder()
	restartedHandler.ServeHTTP(restarted, restartRequest)
	if restarted.Code != http.StatusOK {
		t.Fatalf("restart bootstrap status=%d body=%s", restarted.Code, restarted.Body.String())
	}
	var payload bootstrapResponse
	if err := json.Unmarshal(restarted.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.CurrentSessionID != "recover" || len(payload.Messages) != 2 || payload.Messages[0].Content != "Goal: audit persistence" || payload.TaskState.Goal == nil || payload.TaskState.Goal.ID != "Mgoal" || payload.LastTurn == nil || payload.LastTurn.Resolution.SearchQuery != "audit persistence" {
		t.Fatalf("restart lost persistent state: %+v", payload)
	}
}

type serviceCall struct {
	ID      string
	Mode    chatservice.Mode
	Message string
}

type detailedService struct {
	store *chatstore.FileStore
	mu    sync.Mutex
	calls []serviceCall
}

func (s *detailedService) Ask(ctx context.Context, id string, mode chatservice.Mode, message string) (chatservice.TurnResult, error) {
	s.mu.Lock()
	s.calls = append(s.calls, serviceCall{ID: id, Mode: mode, Message: message})
	s.mu.Unlock()

	session, err := s.store.Load(ctx, id)
	if err != nil {
		return chatservice.TurnResult{}, err
	}
	version := session.Version
	oldConstraint := chat.MemoryItem{ID: "Mold", Kind: chat.KindConstraint, Value: "old constraint", SourceTurnID: "U0", UserQuote: "Old constraint"}
	goal := chat.MemoryItem{ID: "Mgoal", Kind: chat.KindGoal, Value: "audit Artifact MCP", SourceTurnID: "U1", UserQuote: "Our goal is an audit"}
	state := chat.TaskState{Goal: &goal, Constraints: []chat.MemoryItem{}, Terms: []chat.MemoryItem{}, Decisions: []chat.MemoryItem{}, Clarifications: []chat.MemoryItem{}, OpenQuestions: []chat.MemoryItem{}, History: []chat.MemoryItem{oldConstraint}}
	sources := []evidence.Source{
		{ID: "S1", Kind: "corpus", Source: "/private/secret/repository/store.go", Section: "Save", ChunkID: "chunk-1", Quote: "atomic rename preserves the committed file"},
		{ID: "U1", Kind: "task_memory", Source: "conversation:" + id, Section: "user turn 1", ChunkID: "user-turn-U1", Quote: goal.UserQuote, MemoryID: goal.ID, SourceTurnID: "U1"},
	}
	claims := []evidence.Claim{{ID: "C1", Text: "The audit uses an atomic save", Kind: "mixed", SourceIDs: []string{"S1", "U1"}}}
	citations := []evidence.Citation{{SourceID: "S1", ClaimIDs: []string{"C1"}, Quote: sources[0].Quote, ExactMatch: true}, {SourceID: "U1", ClaimIDs: []string{"C1"}, Quote: goal.UserQuote, ExactMatch: true}}
	rag := agent.Result{
		Status:               agent.StatusAnswered,
		OriginalQuestion:     message,
		SearchQuery:          "resolved audit query",
		Gate:                 agent.Gate{Score: .9, Threshold: .55, Passed: true},
		Pipeline:             agent.PipelineConfig{CandidateK: 20, FinalK: 5, MinSimilarity: .45, AnswerMinRelevance: .55},
		RawStructuredOutputs: []string{"raw-structured-secret"},
		ValidationAttempts:   []agent.ValidationAttempt{{Attempt: 1, Valid: true}},
		Answer:               "<script>alert('answer')</script> grounded answer",
		Claims:               claims,
		Sources:              sources,
		Citations:            citations,
		Entailment:           []judging.Result{{ClaimID: "C1", Verdict: judging.Supported, Reason: "supported", Error: "judge-internal-secret"}},
		Error:                "rag-internal-secret /private/model/socket",
	}
	rawRAG, _ := json.Marshal(rag)
	record := chat.TurnRecord{
		Turn: 1, Mode: string(mode), UserMessageID: "U1", AssistantMessageID: "A1", CurrentUserMessage: message,
		RawMemoryUpdate: []string{"raw-memory-secret"}, MemoryValidationAttempts: []chat.MemoryAttempt{{Attempt: 1, Raw: "memory-attempt-raw-secret", Errors: []string{"retry used"}, Valid: false}},
		TaskStateAfter: state, Resolution: chat.ResolutionTrace{Raw: "resolver-raw-secret", Result: chat.Resolution{SearchQuery: "resolved audit query", UsedTurnIDs: []string{"U1"}, UsedMemoryIDs: []string{"Mgoal"}}, Fallback: true, Error: "resolver-internal-secret"},
		RAGResult: rawRAG, Status: agent.StatusAnswered, Answer: rag.Answer, MemoryUpdateFailed: true, Timing: chat.StageTiming{TotalMS: 42}, Tokens: chat.TurnTokens{AnswerPrompt: 10, AnswerCompletion: 4}, PersistedStatus: "committed",
		SessionVersionBefore: version, SessionVersionAfter: version + 1,
	}
	session.Messages = append(session.Messages, chat.Message{ID: "U1", Turn: 1, Role: chat.RoleUser, Content: message}, chat.Message{ID: "A1", Turn: 1, Role: chat.RoleAssistant, Content: rag.Answer})
	session.TaskState = state
	session.Turns = append(session.Turns, record)
	session.Version++
	if err := s.store.Save(ctx, version, session); err != nil {
		return chatservice.TurnResult{}, err
	}
	return chatservice.TurnResult{Session: session, Record: record, RAG: rag}, nil
}

func TestChatPassesModeAndReturnsSanitizedSourcesStateAndTrace(t *testing.T) {
	store := mustStore(t)
	mustCreateSession(t, store, "safe")
	service := &detailedService{store: store}
	handler := Handler(service, store, testConfig())

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, jsonRequest(http.MethodPost, "/api/chat", `{"session_id":"safe","mode":"history","message":"  inspect persistence  "}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("chat status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	service.mu.Lock()
	calls := append([]serviceCall(nil), service.calls...)
	service.mu.Unlock()
	if len(calls) != 1 || calls[0].ID != "safe" || calls[0].Mode != chatservice.History || calls[0].Message != "inspect persistence" {
		t.Fatalf("calls=%+v", calls)
	}

	var response chatResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SessionID != "safe" || response.Mode != chatservice.History || response.Status != agent.StatusAnswered || response.Answer != "<script>alert('answer')</script> grounded answer" || len(response.Sources) != 2 || response.Sources[0].ID != "S1" || response.Sources[0].Source != "store.go" || response.Sources[1].ID != "U1" || response.TaskState.Goal == nil || len(response.TaskState.History) != 1 || response.Resolution.SearchQuery != "resolved audit query" || len(response.Resolution.UsedMemoryIDs) != 1 || !response.MemoryUpdateFailed || response.Latency.TotalMS != 42 || response.TokenUsage.AnswerPrompt != 10 || response.PersistedStatus != "committed" || response.Trace == nil || len(response.Trace.MemoryValidationAttempts) != 1 || len(response.Trace.Entailment) != 1 {
		t.Fatalf("response lost browser diagnostics: %+v", response)
	}

	body := recorder.Body.String()
	for _, secret := range []string{"/private/secret", "/private/model", "raw-structured-secret", "rag-internal-secret", "raw-memory-secret", "memory-attempt-raw-secret", "resolver-raw-secret", "resolver-internal-secret", "judge-internal-secret"} {
		if strings.Contains(body, secret) {
			t.Errorf("response leaked %q: %s", secret, body)
		}
	}
	if strings.Contains(body, "<script>") {
		t.Fatalf("JSON response did not HTML-escape model text: %s", body)
	}
}

func TestSafeSourcesHidesAbsolutePathsFromEveryPlatform(t *testing.T) {
	items := safeSources([]evidence.Source{
		{ID: "S1", Source: "/private/repository/store.go"},
		{ID: "S2", Source: `C:\private\repository\store.go`},
		{ID: "S3", Source: `\\server\share\store.go`},
		{ID: "S4", Source: "day-20/internal/artifact/store.go"},
	})
	want := []string{"store.go", "store.go", "store.go", "day-20/internal/artifact/store.go"}
	for i := range want {
		if items[i].Source != want[i] {
			t.Fatalf("source %d = %q, want %q", i, items[i].Source, want[i])
		}
	}
}

type gateService struct {
	mu          sync.Mutex
	active      map[string]int
	maxByID     map[string]int
	totalActive int
	maxTotal    int
	entered     chan string
	release     <-chan struct{}
}

func newGateService(release <-chan struct{}) *gateService {
	return &gateService{active: make(map[string]int), maxByID: make(map[string]int), entered: make(chan string, 8), release: release}
}

func (s *gateService) Ask(ctx context.Context, id string, mode chatservice.Mode, message string) (chatservice.TurnResult, error) {
	s.mu.Lock()
	s.active[id]++
	if s.active[id] > s.maxByID[id] {
		s.maxByID[id] = s.active[id]
	}
	s.totalActive++
	if s.totalActive > s.maxTotal {
		s.maxTotal = s.totalActive
	}
	s.mu.Unlock()
	s.entered <- id
	select {
	case <-s.release:
	case <-ctx.Done():
		return chatservice.TurnResult{}, ctx.Err()
	}
	s.mu.Lock()
	s.active[id]--
	s.totalActive--
	s.mu.Unlock()
	return chatservice.TurnResult{Record: chat.TurnRecord{Mode: string(mode), Status: agent.StatusInsufficientContext, Answer: "done", PersistedStatus: "committed"}, RAG: agent.Result{Status: agent.StatusInsufficientContext, Sources: []evidence.Source{}, Citations: []evidence.Citation{}, Claims: []evidence.Claim{}}}, nil
}

func asyncChat(handler http.Handler, sessionID string) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, jsonRequest(http.MethodPost, "/api/chat", fmt.Sprintf(`{"session_id":%q,"mode":"history","message":"hello"}`, sessionID)))
		done <- recorder
	}()
	return done
}

func TestSameSessionRequestsSerialize(t *testing.T) {
	store := mustStore(t)
	mustCreateSession(t, store, "same")
	release := make(chan struct{})
	service := newGateService(release)
	handler := Handler(service, store, testConfig())

	first := asyncChat(handler, "same")
	select {
	case id := <-service.entered:
		if id != "same" {
			t.Fatalf("entered=%q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("first request did not enter service")
	}
	second := asyncChat(handler, "same")
	select {
	case id := <-service.entered:
		t.Fatalf("second same-session request entered concurrently: %q", id)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	for i, done := range []<-chan *httptest.ResponseRecorder{first, second} {
		select {
		case recorder := <-done:
			if recorder.Code != http.StatusOK {
				t.Fatalf("request %d status=%d body=%s", i+1, recorder.Code, recorder.Body.String())
			}
		case <-time.After(time.Second):
			t.Fatalf("request %d did not finish", i+1)
		}
	}
	service.mu.Lock()
	max := service.maxByID["same"]
	service.mu.Unlock()
	if max != 1 {
		t.Fatalf("same-session max concurrency=%d, want 1", max)
	}
}

func TestDifferentSessionsRunIndependently(t *testing.T) {
	store := mustStore(t)
	mustCreateSession(t, store, "one")
	mustCreateSession(t, store, "two")
	release := make(chan struct{})
	service := newGateService(release)
	handler := Handler(service, store, testConfig())
	one := asyncChat(handler, "one")
	two := asyncChat(handler, "two")

	seen := make(map[string]bool)
	for len(seen) < 2 {
		select {
		case id := <-service.entered:
			seen[id] = true
		case <-time.After(time.Second):
			t.Fatalf("different sessions did not enter independently; seen=%v", seen)
		}
	}
	close(release)
	for _, done := range []<-chan *httptest.ResponseRecorder{one, two} {
		select {
		case recorder := <-done:
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		case <-time.After(time.Second):
			t.Fatal("request did not finish")
		}
	}
	service.mu.Lock()
	maxTotal := service.maxTotal
	service.mu.Unlock()
	if maxTotal < 2 {
		t.Fatalf("different-session max concurrency=%d, want at least 2", maxTotal)
	}
}

func TestFrontendUsesSafeDOMRenderingAndContainsRequiredControls(t *testing.T) {
	lower := strings.ToLower(chatPageHTML)
	for _, forbidden := range []string{"innerhtml", "insertadjacenthtml", "document.write", "http://", "https://"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("frontend contains forbidden %q", forbidden)
		}
	}
	for _, marker := range []string{
		"/api/bootstrap", "/api/chat", "textcontent", "ctrlkey", "metakey", "delete-dialog", "delete-confirm", "showmodal", "stateless", "history", "task-memory", "источники", "память", "trace", "метрики", "task memory", "query resolver", "retrieval", "gate", "structured answer", "quote validation", "judge", "@media",
	} {
		if !strings.Contains(lower, marker) {
			t.Errorf("frontend is missing required marker %q", marker)
		}
	}
}
