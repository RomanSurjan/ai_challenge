package chatstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ai-challenge/day-25/internal/chat"
)

func TestValidateID(t *testing.T) {
	for _, id := range []string{"ok", "artifact-audit_1", "A9"} {
		if err := ValidateID(id); err != nil {
			t.Fatalf("ValidateID(%q): %v", id, err)
		}
	}
	for _, id := range []string{"", "../x", "a/b", `a\b`, "/tmp/x", "..", ".hidden"} {
		if err := ValidateID(id); err == nil {
			t.Fatalf("ValidateID(%q) unexpectedly succeeded", id)
		}
	}
}

func TestFileStoreIsolationReloadVersionConflictAndReset(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := store.Create(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Create(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	a.Messages = append(a.Messages, chat.Message{ID: "U1", Turn: 1, Role: chat.RoleUser, Content: "привет"})
	a.Version++
	if err := store.Save(ctx, 1, a); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := restarted.Load(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != 2 || len(loaded.Messages) != 1 || loaded.Messages[0].ID != "U1" {
		t.Fatalf("unexpected reload: %+v", loaded)
	}
	other, err := restarted.Load(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	if other.Version != b.Version || len(other.Messages) != 0 {
		t.Fatalf("session isolation failed: %+v", other)
	}
	stale := loaded
	stale.Version = 3
	newer := loaded
	newer.Version = 3
	newer.Messages = append(newer.Messages, chat.Message{ID: "A1", Turn: 1, Role: chat.RoleAssistant, Content: "ответ"})
	if err := restarted.Save(ctx, 2, newer); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Save(ctx, 2, stale); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("want version conflict, got %v", err)
	}
	preserved, _ := restarted.Load(ctx, "a")
	if len(preserved.Messages) != 2 || preserved.Messages[1].ID != "A1" {
		t.Fatalf("failed save damaged previous session: %+v", preserved.Messages)
	}
	if err := restarted.Reset(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Load(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	if _, err := restarted.Load(ctx, "b"); err != nil {
		t.Fatalf("reset removed another session: %v", err)
	}
}

func TestAtomicSavePermissionsAndJSONRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, _ := New(dir)
	session, _ := store.Create(ctx, "unicode")
	session.Messages = []chat.Message{
		{ID: "U1", Turn: 1, Role: chat.RoleUser, Content: "ёж 🦔"},
		{ID: "A1", Turn: 1, Role: chat.RoleAssistant, Content: "ответ"},
	}
	session.Version++
	if err := store.Save(ctx, 1, session); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "unicode.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("session permissions too broad: %o", info.Mode().Perm())
	}
	loaded, err := store.Load(ctx, "unicode")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Messages[0].Content != "ёж 🦔" || loaded.Messages[0].Turn > loaded.Messages[1].Turn {
		t.Fatalf("round trip/order failed: %+v", loaded.Messages)
	}
}

func TestBoundedHistoryUsesWholeUnicodeMessages(t *testing.T) {
	messages := []chat.Message{{ID: "U1", Role: chat.RoleUser, Content: "12345"}, {ID: "A1", Role: chat.RoleAssistant, Content: "ёж"}, {ID: "U2", Role: chat.RoleUser, Content: "🦔🦔"}}
	got := chat.BoundedHistory(messages, chat.Limits{MaxRecentTurns: 2, MaxHistoryRunes: 4})
	if len(got) != 2 || got[0].Content != "ёж" || got[1].Content != "🦔🦔" {
		t.Fatalf("unexpected bounded history: %+v", got)
	}
}
