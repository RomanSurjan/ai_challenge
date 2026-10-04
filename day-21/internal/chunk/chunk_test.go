package chunk

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"ai-challenge/day-21/internal/document"
)

func TestFixedChunkingSizeOverlapAndUTF8(t *testing.T) {
	var values []string
	for i := 0; i < 12; i++ {
		values = append(values, fmt.Sprintf("слово%d", i))
	}
	doc := testDoc("one.md", "markdown", strings.Join(values, " "))
	chunks, err := Build([]document.Document{doc}, Fixed, Config{TargetWords: 5, OverlapWords: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 4 {
		t.Fatalf("got %d chunks, want 4", len(chunks))
	}
	wantCounts := []int{5, 5, 5, 3}
	for i, item := range chunks {
		if item.WordCount != wantCounts[i] || !utf8.ValidString(item.Text) || item.Section != "Document" {
			t.Fatalf("chunk %d = %#v", i, item)
		}
	}
	if got := strings.Fields(chunks[1].Text)[:2]; strings.Join(got, " ") != "слово3 слово4" {
		t.Fatalf("overlap = %q", got)
	}
}

func TestStructuralMarkdownPreservesHeadingPath(t *testing.T) {
	text := "intro\n# Root\nroot text\n## Child\nchild text\n# Next\nnext text\n"
	chunks, err := Build([]document.Document{testDoc("guide.md", "markdown", text)}, Structural, Config{TargetWords: 100, OverlapWords: 10})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Preamble", "Root", "Root > Child", "Next"}
	if len(chunks) != len(want) {
		t.Fatalf("got sections %#v", chunks)
	}
	for i := range want {
		if chunks[i].Section != want[i] {
			t.Fatalf("section %d = %q, want %q", i, chunks[i].Section, want[i])
		}
	}
}

func TestStructuralGoUsesDeclarations(t *testing.T) {
	text := `package demo

import "fmt"

type Server struct{}

const Version = "1"

func Run() { fmt.Println(Version) }

func (Server) Stop() {}
`
	chunks, err := Build([]document.Document{testDoc("server.go", "go", text)}, Structural, Config{TargetWords: 100, OverlapWords: 10})
	if err != nil {
		t.Fatal(err)
	}
	sections := make(map[string]bool)
	for _, item := range chunks {
		sections[item.Section] = true
	}
	for _, want := range []string{"package demo", "type Server", "const Version", "func Run", "method (Server).Stop"} {
		if !sections[want] {
			t.Errorf("missing section %q in %#v", want, sections)
		}
	}
}

func TestChunkIDsMetadataAndFileBoundaries(t *testing.T) {
	docs := []document.Document{
		testDoc("a.txt", "text", "alpha beta gamma delta"),
		testDoc("b.txt", "text", "one two three four"),
	}
	first, err := Build(docs, Fixed, Config{TargetWords: 3, OverlapWords: 1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(docs, Fixed, Config{TargetWords: 3, OverlapWords: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i := range first {
		if first[i].ChunkID != second[i].ChunkID {
			t.Fatalf("unstable ID at %d", i)
		}
		if strings.Contains(first[i].Text, "alpha") && first[i].Source != "a.txt" {
			t.Fatal("chunk crossed a file boundary")
		}
		if strings.TrimSpace(first[i].Text) == "" || first[i].Source == "" || first[i].Title == "" || first[i].Section == "" {
			t.Fatalf("missing metadata: %#v", first[i])
		}
	}
}

func testDoc(source, kind, text string) document.Document {
	return document.Document{Source: source, Title: source, Type: kind, Text: text, CharCount: utf8.RuneCountInString(text), WordCount: len(strings.Fields(text)), SHA256: "hash-" + source}
}
