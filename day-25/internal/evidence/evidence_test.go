package evidence

import (
	"encoding/json"
	"strings"
	"testing"

	"ai-challenge/day-25/internal/retrieval"
)

func contexts() ([]retrieval.Result, []retrieval.Result) {
	final := []retrieval.Result{
		{Rank: 1, Score: .8, ChunkID: "a", Source: "a.go", Section: "A", Text: "Unicode цитата точно находится в первом чанке и подтверждает важный технический факт."},
		{Rank: 2, Score: .7, ChunkID: "b", Source: "b.go", Section: "B", Text: "Другая достаточно длинная цитата находится только во втором чанке для проверки связи."},
	}
	candidates := append([]retrieval.Result(nil), final...)
	candidates = append(candidates, retrieval.Result{Rank: 3, Score: .6, ChunkID: "c", Source: "c.go", Section: "C", Text: "Эта цитата присутствует только в кандидате, который не вошёл в финальный контекст."})
	return final, candidates
}

func validResponse() Response {
	claim := "Первый технический факт подтверждён"
	return Response{Status: StatusAnswered, Answer: claim + " [S1].", Claims: []Claim{{ID: "C1", Text: claim, SourceIDs: []string{"S1"}}}, Evidence: []Quote{{SourceID: "S1", ClaimIDs: []string{"C1"}, Quote: "Unicode цитата точно находится в первом чанке"}}}
}

func TestSuccessfulStructuredResponseAndEnrichment(t *testing.T) {
	final, candidates := contexts()
	got := (Validator{MinQuoteRunes: 20, MaxQuoteRunes: 500}).Validate(validResponse(), final, candidates, map[string]float64{"a": .9})
	if !got.Valid || len(got.Sources) != 1 || got.Sources[0].Source != "a.go" || got.Sources[0].Section != "A" || got.Sources[0].ChunkID != "a" || got.Sources[0].RerankScore != .9 {
		t.Fatalf("validation=%+v", got)
	}
	if len(got.Citations) != 1 || !got.Citations[0].ExactMatch {
		t.Fatalf("citations=%+v", got.Citations)
	}
}

func TestValidationFailures(t *testing.T) {
	final, candidates := contexts()
	tests := []struct {
		name, code string
		mutate     func(*Response)
	}{
		{"wrong chunk", "quote_wrong_chunk", func(r *Response) {
			r.Evidence[0].Quote = "Другая достаточно длинная цитата находится только во втором чанке"
		}},
		{"candidate outside final", "quote_wrong_chunk", func(r *Response) {
			r.Evidence[0].Quote = "Эта цитата присутствует только в кандидате, который не вошёл в финальный контекст."
		}},
		{"unknown source", "unknown_source_id", func(r *Response) { r.Claims[0].SourceIDs = []string{"S9"}; r.Evidence[0].SourceID = "S9" }},
		{"empty quote", "empty_quote", func(r *Response) { r.Evidence[0].Quote = "" }},
		{"short quote", "quote_too_short", func(r *Response) { r.Evidence[0].Quote = "Unicode" }},
		{"long quote", "quote_too_long", func(r *Response) { r.Evidence[0].Quote = strings.Repeat("я", 501) }},
		{"claim without source", "claim_without_source", func(r *Response) { r.Claims[0].SourceIDs = nil }},
		{"claim without quote", "claim_without_quote", func(r *Response) { r.Evidence = nil }},
		{"unknown claim", "unknown_claim_id", func(r *Response) { r.Evidence[0].ClaimIDs = []string{"C9"} }},
		{"duplicate claim", "duplicate_claim_id", func(r *Response) { r.Claims = append(r.Claims, r.Claims[0]) }},
		{"duplicate source", "duplicate_claim_source", func(r *Response) { r.Claims[0].SourceIDs = []string{"S1", "S1"} }},
		{"inline outside", "invalid_inline_citation", func(r *Response) { r.Answer += " [S9]" }},
		{"empty answer", "empty_answer", func(r *Response) { r.Answer = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validResponse()
			tt.mutate(&r)
			got := (Validator{20, 500}).Validate(r, final, candidates, nil)
			if got.Valid || !hasCode(got.Errors, tt.code) {
				t.Fatalf("want %s, got %+v", tt.code, got.Errors)
			}
		})
	}
}

func TestParseStrictJSONAndRoundTrip(t *testing.T) {
	r := validResponse()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Parse(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Answer != r.Answer {
		t.Fatalf("decoded=%+v", decoded)
	}
	if _, err := Parse("```json\n{}\n```"); err == nil {
		t.Fatal("accepted fenced JSON")
	}
	if _, err := Parse(`{"status":"answered","unknown":true}`); err == nil {
		t.Fatal("accepted unknown field")
	}
}

func TestParseNestedWireContractDerivesAndDeduplicatesSources(t *testing.T) {
	raw := `{"status":"answered","answer":"Факт [S1].","claims":[{"id":"C1","text":"Факт","evidence":[{"source_id":"S1","quote":"первая достаточно длинная точная цитата"},{"source_id":"S1","quote":"вторая достаточно длинная точная цитата"}]}],"clarification_question":""}`
	got, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Claims) != 1 || len(got.Claims[0].SourceIDs) != 1 || got.Claims[0].SourceIDs[0] != "S1" || len(got.Evidence) != 2 {
		t.Fatalf("canonical=%+v", got)
	}
}

func hasCode(errors []Error, want string) bool {
	for _, item := range errors {
		if item.Code == want {
			return true
		}
	}
	return false
}
