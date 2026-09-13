package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	StrategySliding       = "sliding"
	StrategyFacts         = "facts"
	StrategyBranching     = "branching"
	defaultWindowMessages = 8
)

type ContextStrategy interface {
	Name() string
	Build(ContextInput) (ContextOutput, error)
	TrimStoredHistory([]chatMessage) ([]chatMessage, error)
}

type ContextInput struct {
	System      string
	History     []chatMessage
	Facts       map[string]string
	Branches    BranchState
	UserPrompt  string
	BranchID    string
	BranchTitle string
}

type ContextOutput struct {
	Messages []chatMessage
	Metadata ContextMetadata
}

type ContextMetadata struct {
	Strategy           string
	WindowMessages     int
	FactsCount         int
	BranchID           string
	BranchTitle        string
	CheckpointMessages int
	BranchMessages     int
	HistoryMessages    int
	ContextMessages    int
}

func (m ContextMetadata) MarshalJSON() ([]byte, error) {
	type contextMetadataJSON struct {
		Strategy           string  `json:"strategy"`
		WindowMessages     *int    `json:"window_messages,omitempty"`
		FactsCount         *int    `json:"facts_count,omitempty"`
		BranchID           *string `json:"branch_id,omitempty"`
		BranchTitle        *string `json:"branch_title,omitempty"`
		CheckpointMessages *int    `json:"checkpoint_messages,omitempty"`
		BranchMessages     *int    `json:"branch_messages,omitempty"`
		HistoryMessages    int     `json:"history_messages"`
		ContextMessages    int     `json:"context_messages"`
	}

	out := contextMetadataJSON{
		Strategy:        m.Strategy,
		HistoryMessages: m.HistoryMessages,
		ContextMessages: m.ContextMessages,
	}
	if m.Strategy == StrategySliding {
		out.WindowMessages = &m.WindowMessages
	}
	if m.Strategy == StrategyFacts {
		out.FactsCount = &m.FactsCount
	}
	if m.Strategy == StrategyBranching {
		out.BranchID = &m.BranchID
		out.BranchTitle = &m.BranchTitle
		out.CheckpointMessages = &m.CheckpointMessages
		out.BranchMessages = &m.BranchMessages
	}
	return json.Marshal(out)
}

type SlidingWindowStrategy struct {
	WindowMessages int
}

type FactsStrategy struct {
}

type BranchingStrategy struct {
}

func NewContextStrategy(name string, windowMessages int) (ContextStrategy, error) {
	switch normalizeStrategyName(name) {
	case StrategySliding:
		if err := validateWindowMessages(windowMessages); err != nil {
			return nil, err
		}
		return NewSlidingWindowStrategy(windowMessages), nil
	case StrategyFacts:
		return NewFactsStrategy(), nil
	case StrategyBranching:
		return NewBranchingStrategy(), nil
	default:
		return nil, fmt.Errorf("неизвестная стратегия контекста: %q", name)
	}
}

func NewSlidingWindowStrategy(windowMessages int) SlidingWindowStrategy {
	return SlidingWindowStrategy{WindowMessages: windowMessages}
}

func NewFactsStrategy() FactsStrategy {
	return FactsStrategy{}
}

func NewBranchingStrategy() BranchingStrategy {
	return BranchingStrategy{}
}

func normalizeStrategyName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return StrategySliding
	}
	return name
}

func validateWindowMessages(windowMessages int) error {
	if windowMessages < 0 {
		return errors.New("размер окна не может быть отрицательным")
	}
	return nil
}

func (s SlidingWindowStrategy) Name() string {
	return StrategySliding
}

func (s SlidingWindowStrategy) Build(input ContextInput) (ContextOutput, error) {
	if err := validateWindowMessages(s.WindowMessages); err != nil {
		return ContextOutput{}, err
	}

	system := strings.TrimSpace(input.System)
	userPrompt := strings.TrimSpace(input.UserPrompt)
	history := cloneMessages(input.History)
	if len(history) > s.WindowMessages {
		history = history[len(history)-s.WindowMessages:]
	}

	messages := make([]chatMessage, 0, len(history)+2)
	if system != "" {
		messages = append(messages, chatMessage{Role: "system", Content: system})
	}
	messages = append(messages, history...)
	if userPrompt != "" {
		messages = append(messages, chatMessage{Role: "user", Content: userPrompt})
	}

	return ContextOutput{
		Messages: messages,
		Metadata: ContextMetadata{
			Strategy:        s.Name(),
			WindowMessages:  s.WindowMessages,
			HistoryMessages: len(input.History),
			ContextMessages: len(messages),
		},
	}, nil
}

func (s SlidingWindowStrategy) TrimStoredHistory(messages []chatMessage) ([]chatMessage, error) {
	if err := validateWindowMessages(s.WindowMessages); err != nil {
		return nil, err
	}

	messages = cloneMessages(messages)
	if len(messages) > s.WindowMessages {
		messages = messages[len(messages)-s.WindowMessages:]
	}
	return messages, nil
}

func (s FactsStrategy) Name() string {
	return StrategyFacts
}

func (s FactsStrategy) Build(input ContextInput) (ContextOutput, error) {
	system := strings.TrimSpace(input.System)
	userPrompt := strings.TrimSpace(input.UserPrompt)

	factsBlock := formatFactsBlock(input.Facts)
	messages := make([]chatMessage, 0, 3)
	if system != "" {
		messages = append(messages, chatMessage{Role: "system", Content: system})
	}
	if factsBlock != "" {
		messages = append(messages, chatMessage{Role: "system", Content: factsBlock})
	}
	if userPrompt != "" {
		messages = append(messages, chatMessage{Role: "user", Content: userPrompt})
	}

	return ContextOutput{
		Messages: messages,
		Metadata: ContextMetadata{
			Strategy:        s.Name(),
			FactsCount:      countFacts(input.Facts),
			HistoryMessages: len(input.History),
			ContextMessages: len(messages),
		},
	}, nil
}

func (s FactsStrategy) TrimStoredHistory(messages []chatMessage) ([]chatMessage, error) {
	return cloneMessages(messages), nil
}

func (s BranchingStrategy) Name() string {
	return StrategyBranching
}

func (s BranchingStrategy) Build(input ContextInput) (ContextOutput, error) {
	system := strings.TrimSpace(input.System)
	userPrompt := strings.TrimSpace(input.UserPrompt)
	branches := normalizeBranchState(ConversationState{
		Messages: input.History,
		Branches: input.Branches,
	}).Branches
	branch := activeBranch(branches)

	messages := make([]chatMessage, 0, len(branches.Checkpoint)+len(branch.Messages)+2)
	if system != "" {
		messages = append(messages, chatMessage{Role: "system", Content: system})
	}
	messages = append(messages, branches.Checkpoint...)
	messages = append(messages, branch.Messages...)
	if userPrompt != "" {
		messages = append(messages, chatMessage{Role: "user", Content: userPrompt})
	}

	return ContextOutput{
		Messages: messages,
		Metadata: ContextMetadata{
			Strategy:           s.Name(),
			BranchID:           branch.ID,
			BranchTitle:        branch.Title,
			CheckpointMessages: len(branches.Checkpoint),
			BranchMessages:     len(branch.Messages),
			HistoryMessages:    len(branches.Checkpoint) + len(branch.Messages),
			ContextMessages:    len(messages),
		},
	}, nil
}

func (s BranchingStrategy) TrimStoredHistory(messages []chatMessage) ([]chatMessage, error) {
	return cloneMessages(messages), nil
}

func countFacts(facts map[string]string) int {
	count := 0
	for key, value := range facts {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			count++
		}
	}
	return count
}

func formatFactsBlock(facts map[string]string) string {
	if len(facts) == 0 {
		return ""
	}

	keys := make([]string, 0, len(facts))
	for key, value := range facts {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)

	var builder strings.Builder
	builder.WriteString("Sticky facts:\n")
	for _, key := range keys {
		builder.WriteString("- ")
		builder.WriteString(key)
		builder.WriteString(": ")
		builder.WriteString(strings.TrimSpace(facts[key]))
		builder.WriteByte('\n')
	}
	return strings.TrimRight(builder.String(), "\n")
}
