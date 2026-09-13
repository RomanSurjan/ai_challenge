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

type StoredHistory struct {
	Messages          []chatMessage
	CompactedMessages int
}

type CompactionAwareMessageStore interface {
	MessageStore
	LoadState(ctx context.Context) (StoredHistory, error)
	SaveState(ctx context.Context, state StoredHistory) error
}

type ResettableMessageStore interface {
	MessageStore
	Reset(ctx context.Context) error
}

type JSONMessageStore struct {
	path string
	mu   sync.Mutex
}

type historyFile struct {
	Version           int           `json:"version"`
	Messages          []chatMessage `json:"messages"`
	CompactedMessages int           `json:"compacted_messages,omitempty"`
	Updated           time.Time     `json:"updated_at"`
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

func (s *JSONMessageStore) LoadState(ctx context.Context) (StoredHistory, error) {
	if s == nil || s.path == "" {
		return StoredHistory{}, nil
	}
	if err := ctx.Err(); err != nil {
		return StoredHistory{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return StoredHistory{}, nil
		}
		return StoredHistory{}, fmt.Errorf("не удалось прочитать историю: %w", err)
	}
	if len(data) == 0 {
		return StoredHistory{}, nil
	}

	var file historyFile
	if err := json.Unmarshal(data, &file); err != nil {
		return StoredHistory{}, fmt.Errorf("не удалось разобрать историю: %w", err)
	}
	if file.Version != 1 {
		return StoredHistory{}, fmt.Errorf("неподдерживаемая версия истории: %d", file.Version)
	}
	if err := validateMessages(file.Messages); err != nil {
		return StoredHistory{}, fmt.Errorf("история содержит некорректные данные: %w", err)
	}
	if file.CompactedMessages < 0 {
		return StoredHistory{}, fmt.Errorf("история содержит некорректное количество сжатых сообщений: %d", file.CompactedMessages)
	}

	return StoredHistory{
		Messages:          cloneMessages(file.Messages),
		CompactedMessages: file.CompactedMessages,
	}, nil
}

func (s *JSONMessageStore) Save(ctx context.Context, messages []chatMessage) error {
	return s.SaveState(ctx, StoredHistory{Messages: messages})
}

func (s *JSONMessageStore) SaveState(ctx context.Context, state StoredHistory) error {
	if s == nil || s.path == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateMessages(state.Messages); err != nil {
		return err
	}
	if state.CompactedMessages < 0 {
		return fmt.Errorf("история содержит некорректное количество сжатых сообщений: %d", state.CompactedMessages)
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
		Version:           1,
		Messages:          cloneMessages(state.Messages),
		CompactedMessages: state.CompactedMessages,
		Updated:           time.Now().UTC(),
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

func (s *JSONMessageStore) Reset(ctx context.Context) error {
	if s == nil || s.path == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("не удалось очистить историю: %w", err)
	}
	return nil
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

func cloneMessages(messages []chatMessage) []chatMessage {
	if len(messages) == 0 {
		return nil
	}
	copied := make([]chatMessage, len(messages))
	copy(copied, messages)
	return copied
}
