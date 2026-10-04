package evidence

import (
	"testing"

	"ai-challenge/day-25/internal/retrieval"
)

func TestUnifiedCorpusAndUserMemoryEvidence(t *testing.T) {
	final := []retrieval.Result{{ChunkID: "chunk", Source: "day-20/store.go", Section: "Save", Text: "Запись использует временный файл, fsync и atomic rename для безопасного сохранения.", Score: .8}}
	users := []UserSource{{ID: "U1", Source: "conversation:s", Section: "user turn 1", ChunkID: "user-turn-U1", Quote: "Ограничение: учитывай только Go-реализацию.", MemoryID: "M1", SourceTurnID: "U1"}}
	response := Response{Status: StatusAnswered, Answer: "Проверяем Go-реализацию с atomic rename [S1] [U1].", Claims: []Claim{{ID: "C1", Text: "Проверяем Go-реализацию с atomic rename", SourceIDs: []string{"S1", "U1"}, Kind: "mixed"}}, Evidence: []Quote{{SourceID: "S1", ClaimIDs: []string{"C1"}, Quote: "временный файл, fsync и atomic rename"}, {SourceID: "U1", ClaimIDs: []string{"C1"}, Quote: "Ограничение: учитывай только Go-реализацию."}}}
	validation := Validator{MinQuoteRunes: 20, MaxQuoteRunes: 160}.ValidateWithUserSources(response, final, final, map[string]float64{"chunk": .9}, users)
	if !validation.Valid || len(validation.Sources) != 2 || validation.Sources[0].Kind != "corpus" || validation.Sources[1].Kind != "task_memory" {
		t.Fatalf("unexpected validation: %+v", validation)
	}
}

func TestClaimTypeRequiresCorrectEvidenceAndUnknownIDsFail(t *testing.T) {
	final := []retrieval.Result{{ChunkID: "c", Source: "s", Section: "x", Text: "Достаточно длинная точная цитата из corpus chunk.", Score: .8}}
	users := []UserSource{{ID: "U1", Source: "conversation:s", Section: "user turn 1", ChunkID: "user-turn-U1", Quote: "Достаточно длинная пользовательская цитата.", MemoryID: "M1", SourceTurnID: "U1"}}
	tests := []Response{
		{Status: StatusAnswered, Answer: "memory [S1]", Claims: []Claim{{ID: "C1", Text: "memory", Kind: "memory", SourceIDs: []string{"S1"}}}, Evidence: []Quote{{SourceID: "S1", ClaimIDs: []string{"C1"}, Quote: "Достаточно длинная точная цитата из corpus chunk."}}},
		{Status: StatusAnswered, Answer: "corpus [U1]", Claims: []Claim{{ID: "C1", Text: "corpus", Kind: "corpus", SourceIDs: []string{"U1"}}}, Evidence: []Quote{{SourceID: "U1", ClaimIDs: []string{"C1"}, Quote: "Достаточно длинная пользовательская цитата."}}},
		{Status: StatusAnswered, Answer: "mixed [S1]", Claims: []Claim{{ID: "C1", Text: "mixed", Kind: "mixed", SourceIDs: []string{"S1"}}}, Evidence: []Quote{{SourceID: "S1", ClaimIDs: []string{"C1"}, Quote: "Достаточно длинная точная цитата из corpus chunk."}}},
		{Status: StatusAnswered, Answer: "unknown [U9]", Claims: []Claim{{ID: "C1", Text: "unknown", Kind: "memory", SourceIDs: []string{"U9"}}}, Evidence: []Quote{{SourceID: "U9", ClaimIDs: []string{"C1"}, Quote: "Достаточно длинная пользовательская цитата."}}},
	}
	for i, response := range tests {
		if got := (Validator{MinQuoteRunes: 20, MaxQuoteRunes: 160}).ValidateWithUserSources(response, final, final, nil, users); got.Valid {
			t.Fatalf("case %d unexpectedly valid", i)
		}
	}
}

func TestUserQuoteMustMatchLinkedUserSource(t *testing.T) {
	users := []UserSource{{ID: "U1", Source: "conversation:s", Section: "user turn 1", ChunkID: "user-turn-U1", Quote: "Первая достаточно длинная пользовательская цитата.", MemoryID: "M1", SourceTurnID: "U1"}, {ID: "U2", Source: "conversation:s", Section: "user turn 2", ChunkID: "user-turn-U2", Quote: "Вторая достаточно длинная пользовательская цитата.", MemoryID: "M2", SourceTurnID: "U2"}}
	response := Response{Status: StatusAnswered, Answer: "constraint [U1]", Claims: []Claim{{ID: "C1", Text: "constraint", Kind: "memory", SourceIDs: []string{"U1"}}}, Evidence: []Quote{{SourceID: "U1", ClaimIDs: []string{"C1"}, Quote: users[1].Quote}}}
	got := (Validator{MinQuoteRunes: 20, MaxQuoteRunes: 160}).ValidateWithUserSources(response, nil, nil, nil, users)
	if got.Valid || got.WrongChunkQuotes != 1 {
		t.Fatalf("wrong user turn quote accepted: %+v", got)
	}
}
