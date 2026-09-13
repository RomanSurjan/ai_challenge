package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type MessageStore interface {
	Load(ctx context.Context) ([]chatMessage, error)
	Save(ctx context.Context, messages []chatMessage) error
}

type ConversationStore interface {
	LoadState(ctx context.Context) (ConversationState, error)
	SaveState(ctx context.Context, state ConversationState) error
}

type ConversationState struct {
	Messages []chatMessage
	Facts    map[string]string
	Branches BranchState
}

type BranchState struct {
	ActiveBranchID string               `json:"active_branch_id"`
	Checkpoint     []chatMessage        `json:"checkpoint,omitempty"`
	Items          []ConversationBranch `json:"items,omitempty"`
}

type ConversationBranch struct {
	ID       string        `json:"id"`
	Title    string        `json:"title"`
	Messages []chatMessage `json:"messages,omitempty"`
}

type JSONMessageStore struct {
	path string
	mu   sync.Mutex
}

const currentHistoryVersion = 3

var supportedHistoryVersions = map[int]struct{}{
	1: {},
	2: {},
	3: {},
}

type historyFile struct {
	Version  int               `json:"version"`
	Messages []chatMessage     `json:"messages"`
	Facts    map[string]string `json:"facts,omitempty"`
	Branches BranchState       `json:"branches,omitempty"`
	Updated  time.Time         `json:"updated_at"`
}

func NewJSONMessageStore(path string) *JSONMessageStore {
	return &JSONMessageStore{path: path}
}

func (s *JSONMessageStore) Load(ctx context.Context) ([]chatMessage, error) {
	state, err := s.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	return state.Messages, nil
}

func (s *JSONMessageStore) Save(ctx context.Context, messages []chatMessage) error {
	return s.SaveState(ctx, ConversationState{Messages: messages})
}

func (s *JSONMessageStore) LoadState(ctx context.Context) (ConversationState, error) {
	if s == nil || s.path == "" {
		return ConversationState{}, nil
	}
	if err := ctx.Err(); err != nil {
		return ConversationState{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return ConversationState{}, nil
		}
		return ConversationState{}, fmt.Errorf("не удалось прочитать историю: %w", err)
	}
	if len(data) == 0 {
		return ConversationState{}, nil
	}

	var file historyFile
	if err := json.Unmarshal(data, &file); err != nil {
		return ConversationState{}, fmt.Errorf("не удалось разобрать историю: %w", err)
	}
	if !isSupportedHistoryVersion(file.Version) {
		return ConversationState{}, fmt.Errorf("неподдерживаемая версия истории: %d (поддерживаются: 1, 2, 3)", file.Version)
	}
	if err := validateMessages(file.Messages); err != nil {
		return ConversationState{}, fmt.Errorf("история содержит некорректные данные: %w", err)
	}
	if err := validateFacts(file.Facts); err != nil {
		return ConversationState{}, fmt.Errorf("facts содержат некорректные данные: %w", err)
	}
	facts := RecoverFactsFromMessages(file.Facts, file.Messages)
	if err := validateFacts(facts); err != nil {
		return ConversationState{}, fmt.Errorf("facts содержат некорректные данные: %w", err)
	}
	if err := validateBranches(file.Branches); err != nil {
		return ConversationState{}, fmt.Errorf("branches содержат некорректные данные: %w", err)
	}

	return ConversationState{
		Messages: cloneMessages(file.Messages),
		Facts:    facts,
		Branches: cloneBranchState(file.Branches),
	}, nil
}

func (s *JSONMessageStore) SaveState(ctx context.Context, state ConversationState) error {
	if s == nil || s.path == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateMessages(state.Messages); err != nil {
		return err
	}
	if err := validateFacts(state.Facts); err != nil {
		return err
	}
	if err := validateBranches(state.Branches); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("не удалось создать папку истории: %w", err)
		}
	}

	file := historyFile{
		Version:  currentHistoryVersion,
		Messages: cloneMessages(state.Messages),
		Facts:    cloneFacts(state.Facts),
		Branches: cloneBranchState(state.Branches),
		Updated:  time.Now().UTC(),
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("не удалось собрать историю: %w", err)
	}
	data = append(data, '\n')

	tmpPath := fmt.Sprintf("%s.tmp.%d", s.path, os.Getpid())
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("не удалось записать временный файл истории: %w", err)
	}
	defer os.Remove(tmpPath)

	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("не удалось заменить файл истории: %w", err)
	}
	return nil
}

func isSupportedHistoryVersion(version int) bool {
	_, ok := supportedHistoryVersions[version]
	return ok
}

func validateMessages(messages []chatMessage) error {
	for i, message := range messages {
		if message.Role != "user" && message.Role != "assistant" {
			return fmt.Errorf("некорректная роль в истории на позиции %d: %q", i, message.Role)
		}
		if message.Content == "" {
			return fmt.Errorf("пустое сообщение в истории на позиции %d", i)
		}
	}
	return nil
}

func validateFacts(facts map[string]string) error {
	for key, value := range facts {
		if key == "" {
			return fmt.Errorf("пустой ключ")
		}
		if value == "" {
			return fmt.Errorf("пустое значение для ключа %q", key)
		}
	}
	return nil
}

func validateBranches(branches BranchState) error {
	if err := validateMessages(branches.Checkpoint); err != nil {
		return fmt.Errorf("checkpoint: %w", err)
	}
	if len(branches.Items) == 0 {
		if branches.ActiveBranchID != "" {
			return fmt.Errorf("active branch %q задана без списка веток", branches.ActiveBranchID)
		}
		return nil
	}

	seen := make(map[string]struct{}, len(branches.Items))
	for i, branch := range branches.Items {
		if branch.ID == "" {
			return fmt.Errorf("пустой id ветки на позиции %d", i)
		}
		if _, exists := seen[branch.ID]; exists {
			return fmt.Errorf("дублирующийся id ветки %q", branch.ID)
		}
		seen[branch.ID] = struct{}{}
		if branch.Title == "" {
			return fmt.Errorf("пустой title ветки %q", branch.ID)
		}
		if err := validateMessages(branch.Messages); err != nil {
			return fmt.Errorf("ветка %q: %w", branch.ID, err)
		}
	}
	if branches.ActiveBranchID == "" {
		return fmt.Errorf("active branch не задана")
	}
	if _, exists := seen[branches.ActiveBranchID]; !exists {
		return fmt.Errorf("active branch %q не найдена", branches.ActiveBranchID)
	}
	return nil
}

func cloneMessages(messages []chatMessage) []chatMessage {
	if len(messages) == 0 {
		return nil
	}
	copied := make([]chatMessage, len(messages))
	copy(copied, messages)
	return copied
}

func cloneBranchState(branches BranchState) BranchState {
	return BranchState{
		ActiveBranchID: branches.ActiveBranchID,
		Checkpoint:     cloneMessages(branches.Checkpoint),
		Items:          cloneBranches(branches.Items),
	}
}

func cloneBranches(branches []ConversationBranch) []ConversationBranch {
	if len(branches) == 0 {
		return nil
	}
	copied := make([]ConversationBranch, len(branches))
	for i, branch := range branches {
		copied[i] = ConversationBranch{
			ID:       branch.ID,
			Title:    branch.Title,
			Messages: cloneMessages(branch.Messages),
		}
	}
	return copied
}

func cloneFacts(facts map[string]string) map[string]string {
	if len(facts) == 0 {
		return nil
	}
	copied := make(map[string]string, len(facts))
	for key, value := range facts {
		copied[key] = value
	}
	return copied
}
