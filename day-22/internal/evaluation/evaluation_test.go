package evaluation

import (
	"encoding/json"
	"reflect"
	"testing"

	"ai-challenge/day-22/internal/agent"
)

func TestConceptCoverageUnicodeLowercaseAndWhitespace(t *testing.T) {
	concepts := []RequiredConcept{
		{Name: "routing", Alternatives: []string{"ТАБЛИЦА   ВЛАДЕЛЬЦЕВ"}},
		{Name: "session", Alternatives: []string{"MCP-сессия", "сессию-владельца"}},
		{Name: "missing", Alternatives: []string{"нет такого"}},
	}
	score := ConceptCoverage("Используется таблица\nвладельцев и MCP-СЕССИЯ.", concepts)
	if score.Matched != 2 || score.Total != 3 || score.All || len(score.Missing) != 1 || score.Missing[0] != "missing" {
		t.Fatalf("unexpected coverage: %+v", score)
	}
}

func TestSourceRecall(t *testing.T) {
	if got := SourceRecall([]string{"a", "b", "c"}, []string{"b", "b", "c", "x"}); got != 2.0/3.0 {
		t.Fatalf("SourceRecall() = %v", got)
	}
}

func TestEvaluationReportJSONRoundTrip(t *testing.T) {
	report := Report{ChatModel: "chat", Results: []QuestionResult{{ID: "q", Plain: &agent.Result{Mode: agent.Plain, Citations: []agent.Citation{}, InvalidCitations: []string{}}}}}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report, decoded) {
		t.Fatalf("round trip mismatch: %+v != %+v", report, decoded)
	}
}
