package pipeline

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveAtomicSHAAndMarkdown(t *testing.T) {
	dir := t.TempDir()
	fixed := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	saver := NewSaver(dir)
	saver.Now = func() time.Time { return fixed }
	target := filepath.Join(dir, "summary.md")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := saver.Save(SaveInput{Filename: "summary.md", Query: "MCP", Content: "Exact summary.", Sources: []Source{{Title: "Article", URL: "https://en.wikipedia.org/wiki/MCP"}}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := fmt.Sprintf("%x", sha256.Sum256(data))
	if result.Path != target || result.SHA256 != wantHash || result.Bytes != int64(len(data)) {
		t.Fatalf("metadata mismatch: %+v", result)
	}
	text := string(data)
	for _, fragment := range []string{"# Сводка: MCP", "**Поисковый запрос:** MCP", "Exact summary.", "[Article](https://en.wikipedia.org/wiki/MCP)", fixed.Format(time.RFC3339)} {
		if !strings.Contains(text, fragment) {
			t.Errorf("missing %q", fragment)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "summary.md" {
		t.Fatalf("temporary files remain: %+v", entries)
	}
}

func TestSaveRejectsTraversalAndSize(t *testing.T) {
	saver := NewSaver(t.TempDir())
	for _, name := range []string{"../x.md", "/tmp/x.md", `a\\b.md`, "a/b.md", "not-text.txt", "..md"} {
		if _, err := saver.Save(SaveInput{Filename: name, Query: "q", Content: "x"}); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	if _, err := saver.Save(SaveInput{Filename: "large.md", Query: "q", Content: strings.Repeat("x", MaxFileContent+1)}); err == nil {
		t.Fatal("accepted oversized content")
	}
}
