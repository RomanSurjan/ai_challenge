package chatservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"ai-challenge/day-25/internal/agent"
	"ai-challenge/day-25/internal/chat"
	"ai-challenge/day-25/internal/chatstore"
	"ai-challenge/day-25/internal/evidence"
	"ai-challenge/day-25/internal/memory"
	"ai-challenge/day-25/internal/queryresolver"
)

type Mode string

const (
	Stateless  Mode = "stateless"
	History    Mode = "history"
	TaskMemory Mode = "task-memory"
)

func (m Mode) Valid() bool { return m == Stateless || m == History || m == TaskMemory }

type Service struct {
	Store           chatstore.SessionStore
	RAG             *agent.Agent
	MemoryExtractor memory.Extractor
	Resolver        queryresolver.Detailed
	MemoryValidator memory.Validator
	Limits          chat.Limits
	Now             func() time.Time
}

type TurnResult struct {
	Session chat.Session    `json:"session"`
	Record  chat.TurnRecord `json:"turn"`
	RAG     agent.Result    `json:"rag"`
}

func (s *Service) Ask(ctx context.Context, sessionID string, mode Mode, content string) (TurnResult, error) {
	startedTotal := time.Now()
	if s == nil || s.Store == nil || s.RAG == nil {
		return TurnResult{}, fmt.Errorf("store and RAG agent are required")
	}
	if !mode.Valid() {
		return TurnResult{}, fmt.Errorf("mode must be stateless, history, or task-memory")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return TurnResult{}, fmt.Errorf("message is required")
	}
	limits := s.Limits
	if limits.MaxRecentTurns == 0 {
		limits = chat.DefaultLimits()
	}
	if utf8.RuneCountInString(content) > limits.MaxMessageRunes {
		return TurnResult{}, fmt.Errorf("message has %d Unicode characters; maximum is %d", utf8.RuneCountInString(content), limits.MaxMessageRunes)
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}

	loadStarted := time.Now()
	session, err := s.Store.Load(ctx, sessionID)
	if errors.Is(err, chatstore.ErrNotFound) {
		session, err = s.Store.Create(ctx, sessionID)
	}
	if err != nil {
		return TurnResult{}, err
	}
	loadMS := time.Since(loadStarted).Milliseconds()
	versionBefore := session.Version
	if len(session.Messages)+2 > limits.MaxSessionMessages {
		return TurnResult{}, fmt.Errorf("session message limit %d would be exceeded", limits.MaxSessionMessages)
	}
	turnNumber := len(session.Turns) + 1
	userID := nextMessageID(session.Messages, chat.RoleUser)
	userMessage := chat.Message{ID: userID, Turn: turnNumber, Role: chat.RoleUser, Content: content, CreatedAt: now}

	// First atomic commit guarantees that a model/runtime failure cannot lose the user turn.
	session.Messages = append(session.Messages, userMessage)
	session.Version++
	saveStarted := time.Now()
	if err := s.Store.Save(ctx, versionBefore, session); err != nil {
		return TurnResult{}, err
	}
	saveMS := time.Since(saveStarted).Milliseconds()

	stateBefore := cloneState(session.TaskState)
	historyPool := append([]chat.Message(nil), session.Messages[:len(session.Messages)-1]...)
	bounded := chat.BoundedHistory(historyPool, limits)
	record := chat.TurnRecord{
		Turn: turnNumber, Mode: string(mode), UserMessageID: userID, CurrentUserMessage: content,
		BoundedHistory: bounded, TaskStateBefore: stateBefore, RawMemoryUpdate: []string{},
		MemoryValidationAttempts: []chat.MemoryAttempt{}, TaskStateAfter: cloneState(stateBefore),
		SessionVersionBefore: versionBefore, Timing: chat.StageTiming{SessionLoadMS: loadMS, SessionSaveMS: saveMS},
	}
	for _, item := range bounded {
		record.PromptHistoryRunes += utf8.RuneCountInString(item.Content)
	}

	if mode == TaskMemory {
		memoryStarted := time.Now()
		update := memory.Update(ctx, s.MemoryExtractor, s.MemoryValidator, memory.Request{Current: userMessage, State: stateBefore}, session.Messages)
		record.Timing.MemoryExtractMS = time.Since(memoryStarted).Milliseconds()
		record.MemoryValidationAttempts = update.Attempts
		for _, attempt := range update.Attempts {
			record.RawMemoryUpdate = append(record.RawMemoryUpdate, attempt.Raw)
		}
		record.MemoryUpdateFailed = update.Failed
		record.TaskStateAfter = cloneState(update.State)
		record.Tokens.MemoryPrompt = update.Usage.PromptTokens
		record.Tokens.MemoryCompletion = update.Usage.CompletionTokens
	}

	resolution := chat.ResolutionTrace{Result: chat.Resolution{SearchQuery: content, UsedTurnIDs: []string{}, UsedMemoryIDs: []string{}}}
	if mode != Stateless && !(mode == TaskMemory && isMemoryRecap(content)) {
		resolverStarted := time.Now()
		if s.Resolver == nil {
			resolution.Fallback = true
			resolution.Error = "query resolver is not configured"
		} else {
			resolved, usage := s.Resolver.ResolveDetailed(ctx, userMessage, bounded, record.TaskStateAfter)
			resolution = resolved
			record.Tokens.ResolverPrompt = usage.PromptTokens
			record.Tokens.ResolverCompletion = usage.CompletionTokens
		}
		record.Timing.ResolverMS = time.Since(resolverStarted).Milliseconds()
	}
	record.Resolution = resolution

	userSources := []evidence.UserSource{}
	if mode == TaskMemory {
		userSources = verifiedUserSources(session.ID, record.TaskStateAfter, session.Messages)
	}
	var ragResult agent.Result
	if mode == TaskMemory && isMemoryRecap(content) {
		ragResult = memoryOnlyResult(content, record.TaskStateAfter, userSources)
	} else {
		promptContext := ""
		if mode != Stateless {
			promptContext = conversationPrompt(bounded, mode, record.TaskStateAfter)
		}
		result, askErr := s.RAG.AskResolved(ctx, content, resolution.Result.SearchQuery, promptContext, userSources, agent.Strict)
		if askErr != nil {
			ragResult = agent.Result{Mode: agent.Strict, Status: agent.StatusError, OriginalQuestion: content, SearchQuery: resolution.Result.SearchQuery, Answer: "Не знаю: произошла ошибка локального RAG pipeline.", ClarificationQuestion: "Повторите вопрос или проверьте доступность локальных моделей.", AbstentionReason: agent.ReasonRuntimeError, Error: askErr.Error(), Sources: []evidence.Source{}, Citations: []evidence.Citation{}, Claims: []evidence.Claim{}}
		} else {
			ragResult = result
		}
	}
	rawRAG, marshalErr := json.Marshal(ragResult)
	if marshalErr != nil {
		return TurnResult{}, marshalErr
	}
	record.RAGResult = rawRAG
	record.Status = ragResult.Status
	record.AbstentionReason = ragResult.AbstentionReason
	record.Answer = ragResult.Answer
	record.ClarificationQuestion = ragResult.ClarificationQuestion
	record.Timing.EmbeddingMS = ragResult.Timing.RetrievalMS
	record.Timing.RerankMS = ragResult.Timing.FilterRerankMS
	record.Timing.GenerationMS = ragResult.Timing.GenerationMS
	record.Timing.AnswerValidateMS = ragResult.Timing.ValidationMS
	record.Timing.JudgeMS = ragResult.Timing.JudgeMS
	record.Tokens.AnswerPrompt = ragResult.Tokens.Generator.PromptTokens
	record.Tokens.AnswerCompletion = ragResult.Tokens.Generator.CompletionTokens
	record.Tokens.JudgePrompt = ragResult.Tokens.Judge.PromptTokens
	record.Tokens.JudgeCompletion = ragResult.Tokens.Judge.CompletionTokens
	record.FinalContextRunes = ragResult.ContextRunes
	stateJSON, _ := json.Marshal(record.TaskStateAfter)
	record.TaskStateRunes = utf8.RuneCount(stateJSON)

	assistant := chat.Message{ID: nextMessageID(session.Messages, chat.RoleAssistant), Turn: turnNumber, Role: chat.RoleAssistant, Content: ragResult.Answer, CreatedAt: now}
	record.AssistantMessageID = assistant.ID
	session.Messages = append(session.Messages, assistant)
	if mode == TaskMemory {
		session.TaskState = cloneState(record.TaskStateAfter)
	}
	record.PersistedStatus = "committed"
	session.Version++
	record.SessionVersionAfter = session.Version
	record.Timing.TotalMS = time.Since(startedTotal).Milliseconds()
	session.Turns = append(session.Turns, record)
	saveStarted = time.Now()
	if err := s.Store.Save(ctx, session.Version-1, session); err != nil {
		return TurnResult{Session: session, Record: record, RAG: ragResult}, fmt.Errorf("assistant result not committed (user turn remains persisted): %w", err)
	}
	record.Timing.SessionSaveMS += time.Since(saveStarted).Milliseconds()
	// Persist the measured final save time without another version bump is not
	// safe; the returned/evaluation record carries the precise measurement.
	session.Turns[len(session.Turns)-1].Timing = record.Timing
	return TurnResult{Session: session, Record: record, RAG: ragResult}, nil
}

func nextMessageID(messages []chat.Message, role chat.Role) string {
	count := 1
	for _, message := range messages {
		if message.Role == role {
			count++
		}
	}
	prefix := "U"
	if role == chat.RoleAssistant {
		prefix = "A"
	}
	return fmt.Sprintf("%s%d", prefix, count)
}

func cloneState(state chat.TaskState) chat.TaskState {
	data, _ := json.Marshal(state)
	var out chat.TaskState
	_ = json.Unmarshal(data, &out)
	return out
}

func verifiedUserSources(sessionID string, state chat.TaskState, messages []chat.Message) []evidence.UserSource {
	turns := map[string]chat.Message{}
	for _, message := range messages {
		turns[message.ID] = message
	}
	out := make([]evidence.UserSource, 0)
	for _, item := range chat.ActiveItems(state) {
		turn, ok := turns[item.SourceTurnID]
		if !ok || turn.Role != chat.RoleUser || item.UserQuote == "" || !strings.Contains(turn.Content, item.UserQuote) {
			continue
		}
		out = append(out, evidence.UserSource{ID: fmt.Sprintf("U%d", len(out)+1), Source: "conversation:" + sessionID, Section: fmt.Sprintf("user turn %d", turn.Turn), ChunkID: "user-turn-" + turn.ID, Quote: item.UserQuote, MemoryID: item.ID, SourceTurnID: turn.ID})
	}
	return out
}

func conversationPrompt(history []chat.Message, mode Mode, state chat.TaskState) string {
	var b strings.Builder
	b.WriteString("Bounded recent history:\n")
	for _, message := range history {
		fmt.Fprintf(&b, "[%s role=%s] %s\n", message.ID, message.Role, message.Content)
	}
	if mode == TaskMemory {
		b.WriteString("Active validated task state:\n")
		for _, item := range chat.ActiveItems(state) {
			fmt.Fprintf(&b, "[%s kind=%s source_turn=%s] %s\n", item.ID, item.Kind, item.SourceTurnID, item.Value)
		}
	}
	return b.String()
}

func isMemoryRecap(message string) bool {
	n := strings.ToLower(message)
	patterns := []string{"наша цель", "нашей целью", "напомни цель", "какие ограничения", "наши ограничения", "какие термины", "что мы решили", "ограничение:", "термин:", "уточнение:", "решение:", "больше не учитывай"}
	for _, pattern := range patterns {
		if strings.Contains(n, pattern) {
			return true
		}
	}
	return false
}

func memoryOnlyResult(message string, state chat.TaskState, sources []evidence.UserSource) agent.Result {
	return buildMemoryOnly(message, state, sources)
}

func buildMemoryOnly(message string, state chat.TaskState, sources []evidence.UserSource) agent.Result {
	want := chat.KindGoal
	n := strings.ToLower(message)
	if strings.Contains(n, "ограничен") {
		want = chat.KindConstraint
	} else if strings.Contains(n, "термин") {
		want = chat.KindTerm
	} else if strings.Contains(n, "решил") {
		want = chat.KindDecision
	} else if strings.Contains(n, "решени") {
		want = chat.KindDecision
	} else if strings.Contains(n, "уточнен") {
		want = chat.KindClarification
	}
	items := chat.ActiveItems(state)
	byMemory := map[string]evidence.UserSource{}
	for _, source := range sources {
		byMemory[source.MemoryID] = source
	}
	var chosen []chat.MemoryItem
	for _, item := range items {
		if item.Kind == want {
			chosen = append(chosen, item)
		}
	}
	if len(chosen) == 0 {
		return agent.Result{Mode: agent.Strict, Status: agent.StatusInsufficientContext, OriginalQuestion: message, SearchQuery: message, Answer: "Не знаю: пользователь ещё не зафиксировал это в task memory.", ClarificationQuestion: "Сформулируйте цель, ограничение, термин или решение явно.", AbstentionReason: "memory_not_found", AnswerModelCalled: false, Sources: []evidence.Source{}, Citations: []evidence.Citation{}, Claims: []evidence.Claim{}}
	}
	var answerParts []string
	result := agent.Result{Mode: agent.Strict, Status: "memory_only", OriginalQuestion: message, SearchQuery: message, AnswerModelCalled: false, Sources: []evidence.Source{}, Citations: []evidence.Citation{}, Claims: []evidence.Claim{}}
	for i, item := range chosen {
		source, ok := byMemory[item.ID]
		if !ok {
			continue
		}
		text := item.Value
		answerParts = append(answerParts, fmt.Sprintf("%s [%s]", text, source.ID))
		claimID := fmt.Sprintf("C%d", i+1)
		result.Claims = append(result.Claims, evidence.Claim{ID: claimID, Text: text, SourceIDs: []string{source.ID}, Kind: "memory"})
		result.Sources = append(result.Sources, evidence.Source{ID: source.ID, Kind: "task_memory", Source: source.Source, Section: source.Section, ChunkID: source.ChunkID, Quote: source.Quote, MemoryID: source.MemoryID, SourceTurnID: source.SourceTurnID})
		result.Citations = append(result.Citations, evidence.Citation{SourceID: source.ID, ClaimIDs: []string{claimID}, Quote: source.Quote, ExactMatch: true})
	}
	if len(answerParts) == 0 {
		return agent.Result{Mode: agent.Strict, Status: agent.StatusError, OriginalQuestion: message, SearchQuery: message, Answer: "Не знаю: provenance task memory повреждён.", ClarificationQuestion: "Повторно зафиксируйте нужное значение.", AbstentionReason: agent.ReasonRuntimeError, AnswerModelCalled: false, Sources: []evidence.Source{}, Citations: []evidence.Citation{}, Claims: []evidence.Claim{}}
	}
	label := "Зафиксировано"
	if want == chat.KindGoal {
		label = "Цель"
	} else if want == chat.KindConstraint {
		label = "Активные ограничения"
	}
	result.Answer = label + ": " + strings.Join(answerParts, "; ") + "."
	result.AnswerRunes = utf8.RuneCountInString(result.Answer)
	return result
}
