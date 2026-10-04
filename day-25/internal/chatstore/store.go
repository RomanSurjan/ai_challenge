package chatstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"ai-challenge/day-25/internal/chat"
)

var (
	ErrNotFound        = errors.New("session not found")
	ErrAlreadyExists   = errors.New("session already exists")
	ErrVersionConflict = errors.New("session version conflict")
	safeID             = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
)

type SessionStore interface {
	Create(ctx context.Context, id string) (chat.Session, error)
	Load(ctx context.Context, id string) (chat.Session, error)
	Save(ctx context.Context, expectedVersion int64, session chat.Session) error
	List(ctx context.Context) ([]chat.SessionSummary, error)
	Reset(ctx context.Context, id string) error
}

type FileStore struct {
	dir string
	mu  sync.Mutex
}

func New(dir string) (*FileStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("store-dir is required")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve store-dir: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("create store-dir: %w", err)
	}
	if err := os.Chmod(abs, 0o700); err != nil {
		return nil, fmt.Errorf("protect store-dir: %w", err)
	}
	return &FileStore{dir: abs}, nil
}

func ValidateID(id string) error {
	if !safeID.MatchString(id) || filepath.IsAbs(id) || strings.Contains(id, "..") || strings.ContainsAny(id, `/\\`) {
		return fmt.Errorf("unsafe session ID %q: use 1-64 ASCII letters, digits, '_' or '-'", id)
	}
	return nil
}

func (s *FileStore) path(id string) (string, error) {
	if err := ValidateID(id); err != nil {
		return "", err
	}
	p := filepath.Join(s.dir, id+".json")
	rel, err := filepath.Rel(s.dir, p)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("session path escapes store-dir")
	}
	return p, nil
}

func emptyState() chat.TaskState {
	return chat.TaskState{Constraints: []chat.MemoryItem{}, Terms: []chat.MemoryItem{}, Decisions: []chat.MemoryItem{}, Clarifications: []chat.MemoryItem{}, OpenQuestions: []chat.MemoryItem{}, History: []chat.MemoryItem{}}
}

func (s *FileStore) Create(ctx context.Context, id string) (chat.Session, error) {
	if err := ctx.Err(); err != nil {
		return chat.Session{}, err
	}
	p, err := s.path(id)
	if err != nil {
		return chat.Session{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(p); err == nil {
		return chat.Session{}, ErrAlreadyExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return chat.Session{}, err
	}
	now := time.Now().UTC()
	session := chat.Session{ID: id, Version: 1, CreatedAt: now, UpdatedAt: now, Messages: []chat.Message{}, TaskState: emptyState(), Turns: []chat.TurnRecord{}}
	if err := atomicWriteJSON(s.dir, p, session); err != nil {
		return chat.Session{}, err
	}
	return session, nil
}

func (s *FileStore) Load(ctx context.Context, id string) (chat.Session, error) {
	if err := ctx.Err(); err != nil {
		return chat.Session{}, err
	}
	p, err := s.path(id)
	if err != nil {
		return chat.Session{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return readSession(p, id)
}

func readSession(path, id string) (chat.Session, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return chat.Session{}, ErrNotFound
	}
	if err != nil {
		return chat.Session{}, err
	}
	defer f.Close()
	var session chat.Session
	decoder := json.NewDecoder(io.LimitReader(f, 64<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&session); err != nil {
		return chat.Session{}, fmt.Errorf("decode session %q: %w", id, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return chat.Session{}, fmt.Errorf("decode session %q: trailing JSON", id)
	}
	if session.ID != id || session.Version < 1 {
		return chat.Session{}, fmt.Errorf("session %q has invalid identity or version", id)
	}
	for i := 1; i < len(session.Messages); i++ {
		if session.Messages[i].Turn < session.Messages[i-1].Turn {
			return chat.Session{}, fmt.Errorf("session %q has non-deterministic message order", id)
		}
	}
	return session, nil
}

func (s *FileStore) Save(ctx context.Context, expectedVersion int64, session chat.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := s.path(session.ID)
	if err != nil {
		return err
	}
	if session.Version != expectedVersion+1 {
		return fmt.Errorf("new version %d must equal expected+1 (%d)", session.Version, expectedVersion+1)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := readSession(p, session.ID)
	if err != nil {
		return err
	}
	if current.Version != expectedVersion {
		return fmt.Errorf("%w: expected %d, found %d", ErrVersionConflict, expectedVersion, current.Version)
	}
	if len(session.Messages) < len(current.Messages) || len(session.Turns) < len(current.Turns) {
		return fmt.Errorf("save would remove persisted session history")
	}
	session.UpdatedAt = time.Now().UTC()
	return atomicWriteJSON(s.dir, p, session)
}

func (s *FileStore) List(ctx context.Context) ([]chat.SessionSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	out := make([]chat.SessionSummary, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if ValidateID(id) != nil {
			continue
		}
		session, err := readSession(filepath.Join(s.dir, entry.Name()), id)
		if err != nil {
			return nil, err
		}
		out = append(out, chat.SessionSummary{ID: id, Version: session.Version, UpdatedAt: session.UpdatedAt, MessageCount: len(session.Messages), TurnCount: len(session.Turns)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *FileStore) Reset(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := s.path(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(p); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	} else {
		return err
	}
}

func atomicWriteJSON(dir, path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".session-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err == nil {
		err = d.Sync()
		_ = d.Close()
	}
	if err != nil {
		return err
	}
	ok = true
	return nil
}
