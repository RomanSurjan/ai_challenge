package artifact

import (
	"ai-challenge/day-20/internal/domain"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
)

func TestBuildSaveReadListAndSafety(t *testing.T) {
	in := domain.ReportInput{Topic: "MCP", Title: "Report", Knowledge: domain.SummarizeOutput{OK: true, Summary: "Exact summary", Sources: []domain.Source{{Title: "Wiki", URL: "https://example.com"}}}, KnowledgeSources: []domain.Source{{Title: "Wiki", URL: "https://example.com"}}, Repository: domain.RepositoryOutput{OK: true, FullName: "o/r", URL: "https://github.com/o/r", Stars: 1}, LatestRelease: domain.ReleaseOutput{OK: true, Found: false}}
	a, _ := Build(in)
	b, _ := Build(in)
	if a.Markdown != b.Markdown || a.SHA256 != b.SHA256 {
		t.Fatal("report not deterministic")
	}
	s := NewStore(t.TempDir())
	saved, err := s.Save(domain.SaveInput{Filename: "report.md", Markdown: a.Markdown, Sources: a.Sources})
	if err != nil || saved.SHA256 != a.SHA256 {
		t.Fatalf("%+v %v", saved, err)
	}
	data, _ := os.ReadFile(saved.Path)
	if saved.SHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) {
		t.Fatal("hash mismatch")
	}
	read, _ := s.Read(domain.ReadInput{Filename: "report.md"})
	list, _ := s.List()
	if read.Markdown != a.Markdown || list.Count != 1 {
		t.Fatalf("read/list mismatch")
	}
	for _, name := range []string{"../x.md", "/tmp/x.md", `a\\b.md`} {
		if _, err := s.Save(domain.SaveInput{Filename: name, Markdown: "x"}); err == nil {
			t.Fatalf("unsafe accepted: %s", name)
		}
	}
}
