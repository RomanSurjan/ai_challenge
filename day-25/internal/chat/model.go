package chat

import (
	"encoding/json"
	"time"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	ID        string    `json:"id"`
	Turn      int       `json:"turn"`
	Role      Role      `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type MemoryKind string

const (
	KindGoal          MemoryKind = "goal"
	KindConstraint    MemoryKind = "constraint"
	KindTerm          MemoryKind = "term"
	KindDecision      MemoryKind = "decision"
	KindClarification MemoryKind = "clarification"
	KindOpenQuestion  MemoryKind = "open_question"
)

type MemoryItem struct {
	ID           string     `json:"id"`
	Kind         MemoryKind `json:"kind"`
	Value        string     `json:"value"`
	SourceTurnID string     `json:"source_turn_id"`
	UserQuote    string     `json:"user_quote"`
	CreatedAt    time.Time  `json:"created_at"`
	Supersedes   string     `json:"supersedes,omitempty"`
}

type TaskState struct {
	Goal           *MemoryItem  `json:"goal,omitempty"`
	Constraints    []MemoryItem `json:"constraints"`
	Terms          []MemoryItem `json:"terms"`
	Decisions      []MemoryItem `json:"decisions"`
	Clarifications []MemoryItem `json:"clarifications"`
	OpenQuestions  []MemoryItem `json:"open_questions"`
	History        []MemoryItem `json:"superseded_history"`
}

type MemoryOperation struct {
	Op           string     `json:"op"`
	Kind         MemoryKind `json:"kind,omitempty"`
	Value        string     `json:"value,omitempty"`
	SourceTurnID string     `json:"source_turn_id,omitempty"`
	UserQuote    string     `json:"user_quote,omitempty"`
	Supersedes   string     `json:"supersedes,omitempty"`
}

type MemoryAttempt struct {
	Attempt int      `json:"attempt"`
	Raw     string   `json:"raw"`
	Errors  []string `json:"errors"`
	Valid   bool     `json:"valid"`
}

type Resolution struct {
	SearchQuery   string   `json:"search_query"`
	UsedTurnIDs   []string `json:"used_turn_ids"`
	UsedMemoryIDs []string `json:"used_memory_ids"`
}

type ResolutionTrace struct {
	Raw      string     `json:"raw"`
	Result   Resolution `json:"result"`
	Fallback bool       `json:"fallback"`
	Error    string     `json:"error,omitempty"`
}

type StageTiming struct {
	SessionLoadMS    int64 `json:"session_load_ms"`
	SessionSaveMS    int64 `json:"session_save_ms"`
	MemoryExtractMS  int64 `json:"memory_extraction_ms"`
	MemoryValidateMS int64 `json:"memory_validation_ms"`
	ResolverMS       int64 `json:"query_resolution_ms"`
	EmbeddingMS      int64 `json:"embedding_retrieval_ms"`
	RerankMS         int64 `json:"rerank_ms"`
	GenerationMS     int64 `json:"generation_ms"`
	AnswerValidateMS int64 `json:"answer_validation_ms"`
	JudgeMS          int64 `json:"judge_ms"`
	TotalMS          int64 `json:"total_turn_ms"`
}

type TurnTokens struct {
	MemoryPrompt       int `json:"memory_prompt"`
	MemoryCompletion   int `json:"memory_completion"`
	ResolverPrompt     int `json:"resolver_prompt"`
	ResolverCompletion int `json:"resolver_completion"`
	AnswerPrompt       int `json:"answer_prompt"`
	AnswerCompletion   int `json:"answer_completion"`
	JudgePrompt        int `json:"judge_prompt"`
	JudgeCompletion    int `json:"judge_completion"`
}

type TurnRecord struct {
	Turn                     int             `json:"turn"`
	Mode                     string          `json:"mode"`
	UserMessageID            string          `json:"user_message_id"`
	AssistantMessageID       string          `json:"assistant_message_id,omitempty"`
	CurrentUserMessage       string          `json:"current_user_message"`
	BoundedHistory           []Message       `json:"bounded_history_sent"`
	TaskStateBefore          TaskState       `json:"task_state_before"`
	RawMemoryUpdate          []string        `json:"raw_memory_update"`
	MemoryValidationAttempts []MemoryAttempt `json:"memory_validation_attempts"`
	MemoryUpdateFailed       bool            `json:"memory_update_failed"`
	TaskStateAfter           TaskState       `json:"task_state_after"`
	Resolution               ResolutionTrace `json:"query_resolution"`
	RAGResult                json.RawMessage `json:"rag_result"`
	Status                   string          `json:"status"`
	AbstentionReason         string          `json:"abstention_reason,omitempty"`
	Answer                   string          `json:"answer"`
	ClarificationQuestion    string          `json:"clarification_question,omitempty"`
	Timing                   StageTiming     `json:"latency"`
	Tokens                   TurnTokens      `json:"token_usage"`
	PromptHistoryRunes       int             `json:"prompt_history_runes"`
	TaskStateRunes           int             `json:"task_state_runes"`
	FinalContextRunes        int             `json:"final_context_runes"`
	PersistedStatus          string          `json:"persisted_status"`
	SessionVersionBefore     int64           `json:"session_version_before"`
	SessionVersionAfter      int64           `json:"session_version_after"`
}

type Session struct {
	ID        string       `json:"id"`
	Version   int64        `json:"version"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
	Messages  []Message    `json:"messages"`
	TaskState TaskState    `json:"task_state"`
	Turns     []TurnRecord `json:"turns"`
}

type SessionSummary struct {
	ID           string    `json:"id"`
	Version      int64     `json:"version"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	TurnCount    int       `json:"turn_count"`
}

type Limits struct {
	MaxRecentTurns     int `json:"maximum_recent_turns"`
	MaxHistoryRunes    int `json:"maximum_history_unicode_runes"`
	MaxTaskStateItems  int `json:"maximum_task_state_entries"`
	MaxSessionMessages int `json:"maximum_session_messages"`
	MaxMessageRunes    int `json:"maximum_message_unicode_runes"`
}

func DefaultLimits() Limits {
	return Limits{MaxRecentTurns: 8, MaxHistoryRunes: 6000, MaxTaskStateItems: 64, MaxSessionMessages: 500, MaxMessageRunes: 12000}
}
