package pipeline

import (
	"strings"
	"testing"
)

func TestSummarizeDeterministicExtractiveAndDeduplicated(t *testing.T) {
	input := SummarizeInput{Query: "Model Context Protocol", MaxSentences: 2, Documents: []Document{
		{Title: "A", URL: "https://en.wikipedia.org/wiki/A", Text: "Unrelated short sentence. Model Context Protocol connects AI applications to external systems. Repeated fact."},
		{Title: "B", URL: "https://en.wikipedia.org/wiki/B", Text: "Repeated fact. The protocol standardizes context exchange for language models."},
	}}
	first, err := Summarize(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Summarize(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Summary != second.Summary {
		t.Fatalf("nondeterministic: %q vs %q", first.Summary, second.Summary)
	}
	if first.SentenceCount != 2 || first.InputDocuments != 2 || len(first.Sources) != 2 {
		t.Fatalf("wrong counts: %+v", first)
	}
	allText := input.Documents[0].Text + " " + input.Documents[1].Text
	for _, sentence := range sentenceBoundary.FindAllString(first.Summary, -1) {
		if !strings.Contains(allText, cleanSpace(sentence)) {
			t.Fatalf("invented sentence: %q", sentence)
		}
	}
	if strings.Count(first.Summary, "Repeated fact.") > 1 {
		t.Fatal("duplicate was not removed")
	}
}

func TestSummarizeEmptyAndDuplicateDocuments(t *testing.T) {
	empty, err := Summarize(SummarizeInput{Query: "topic", MaxSentences: 3, Documents: nil})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Summary != "" || empty.Warning == "" || empty.SentenceCount != 0 {
		t.Fatalf("unexpected empty result: %+v", empty)
	}
	duplicate, err := Summarize(SummarizeInput{Query: "topic", MaxSentences: 3, Documents: []Document{{Title: "A", URL: "https://example/a", Text: "Same sentence."}, {Title: "A copy", URL: "https://example/a", Text: "Same sentence."}}})
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.SentenceCount != 1 || len(duplicate.Sources) != 1 {
		t.Fatalf("duplicates not removed: %+v", duplicate)
	}
}

func TestSummarizeValidation(t *testing.T) {
	for _, input := range []SummarizeInput{{Query: "", MaxSentences: 1}, {Query: "x", MaxSentences: 0}, {Query: "x", MaxSentences: 11}, {Query: "x", MaxSentences: 1, Documents: make([]Document, 6)}} {
		if _, err := Summarize(input); err == nil {
			t.Fatalf("expected error for %+v", input)
		}
	}
}
