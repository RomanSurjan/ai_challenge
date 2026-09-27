package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesEndpointListsAndReadsMarkdownOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "summary.md"), []byte("# Safe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("hidden"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := Handler(nil, dir)
	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/files", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "summary.md") || strings.Contains(list.Body.String(), "secret.txt") {
		t.Fatalf("list response: %d %s", list.Code, list.Body.String())
	}
	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/api/files/summary.md", nil))
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), "# Safe") {
		t.Fatalf("read response: %d %s", read.Code, read.Body.String())
	}
}

func TestSafeReadNameRejectsTraversal(t *testing.T) {
	for _, name := range []string{"../x.md", "/tmp/x.md", `a\\b.md`, "a/b.md", "..md", "x.txt", ""} {
		if safeReadName(name) {
			t.Fatalf("accepted %q", name)
		}
	}
	if !safeReadName("report-1.md") {
		t.Fatal("safe Markdown filename rejected")
	}
}
