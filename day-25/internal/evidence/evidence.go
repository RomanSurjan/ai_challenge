package evidence

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"ai-challenge/day-25/internal/retrieval"
)

const (
	StatusAnswered            = "answered"
	StatusInsufficientContext = "insufficient_context"
)

type Claim struct {
	ID        string   `json:"id"`
	Text      string   `json:"text"`
	SourceIDs []string `json:"source_ids"`
	Kind      string   `json:"kind,omitempty"`
}

type Quote struct {
	SourceID string   `json:"source_id"`
	ClaimIDs []string `json:"claim_ids"`
	Quote    string   `json:"quote"`
}

type Response struct {
	Status                string  `json:"status"`
	Answer                string  `json:"answer"`
	Claims                []Claim `json:"claims"`
	Evidence              []Quote `json:"evidence"`
	ClarificationQuestion string  `json:"clarification_question"`
}

type Source struct {
	ID           string  `json:"id"`
	Kind         string  `json:"kind"`
	Source       string  `json:"source"`
	Section      string  `json:"section"`
	ChunkID      string  `json:"chunk_id"`
	Cosine       float64 `json:"cosine"`
	RerankScore  float64 `json:"rerank_score"`
	Quote        string  `json:"quote,omitempty"`
	MemoryID     string  `json:"memory_id,omitempty"`
	SourceTurnID string  `json:"source_turn_id,omitempty"`
}

type UserSource struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	Section      string `json:"section"`
	ChunkID      string `json:"chunk_id"`
	Quote        string `json:"quote"`
	MemoryID     string `json:"memory_id"`
	SourceTurnID string `json:"source_turn_id"`
}

type Citation struct {
	SourceID   string   `json:"source_id"`
	ClaimIDs   []string `json:"claim_ids"`
	Quote      string   `json:"quote"`
	ExactMatch bool     `json:"exact_match"`
}

type Error struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	ClaimID  string `json:"claim_id,omitempty"`
	SourceID string `json:"source_id,omitempty"`
}

type Validation struct {
	Valid            bool       `json:"valid"`
	Errors           []Error    `json:"errors"`
	Sources          []Source   `json:"sources"`
	Citations        []Citation `json:"citations"`
	InvalidSourceIDs []string   `json:"invalid_source_ids"`
	InvalidQuotes    []Quote    `json:"invalid_quotes"`
	WrongChunkQuotes int        `json:"wrong_chunk_quotes"`
	TooShortQuotes   int        `json:"too_short_quotes"`
	TooLongQuotes    int        `json:"too_long_quotes"`
}

type Validator struct {
	MinQuoteRunes int
	MaxQuoteRunes int
}

func JSONSchema(minQuoteRunes, maxQuoteRunes int, sourceIDs, quoteOptions []string) map[string]any {
	sourceSchema := map[string]any{"type": "string", "pattern": "^[SU][1-9][0-9]*$"}
	if len(sourceIDs) > 0 {
		sourceSchema["enum"] = sourceIDs
	}
	quoteSchema := map[string]any{"type": "string", "minLength": minQuoteRunes, "maxLength": maxQuoteRunes}
	if len(quoteOptions) > 0 {
		quoteSchema["enum"] = quoteOptions
	}
	claimIDSchema := map[string]any{"type": "string", "pattern": "^C[1-9][0-9]*$"}
	claimSchema := map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"id", "text", "kind", "evidence"},
		"properties": map[string]any{
			"id": claimIDSchema, "text": map[string]any{"type": "string", "minLength": 1},
			"kind": map[string]any{"type": "string", "enum": []string{"corpus", "memory", "mixed"}},
			"evidence": map[string]any{"type": "array", "minItems": 1, "maxItems": 2, "items": map[string]any{
				"type": "object", "additionalProperties": false,
				"required":   []string{"source_id", "quote"},
				"properties": map[string]any{"source_id": sourceSchema, "quote": quoteSchema},
			}},
		},
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"status", "answer", "claims", "clarification_question"},
		"properties": map[string]any{
			"status":                 map[string]any{"type": "string", "enum": []string{StatusAnswered}},
			"answer":                 map[string]any{"type": "string", "minLength": 1, "pattern": "\\[[SU][1-9][0-9]*\\]"},
			"claims":                 map[string]any{"type": "array", "minItems": 1, "maxItems": 1, "items": claimSchema},
			"clarification_question": map[string]any{"type": "string", "enum": []string{""}, "maxLength": 0},
		},
	}
}

func Parse(raw string) (Response, error) {
	if response, err := parseCanonical(raw); err == nil {
		return response, nil
	}
	type wireEvidence struct {
		SourceID string `json:"source_id"`
		Quote    string `json:"quote"`
	}
	type wireClaim struct {
		ID       string         `json:"id"`
		Text     string         `json:"text"`
		Kind     string         `json:"kind"`
		Evidence []wireEvidence `json:"evidence"`
	}
	var wire struct {
		Status                string      `json:"status"`
		Answer                string      `json:"answer"`
		Claims                []wireClaim `json:"claims"`
		ClarificationQuestion string      `json:"clarification_question"`
	}
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return Response{}, fmt.Errorf("decode structured response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Response{}, fmt.Errorf("decode structured response: unexpected trailing content")
	}
	response := Response{Status: wire.Status, Answer: wire.Answer, Claims: []Claim{}, Evidence: []Quote{}, ClarificationQuestion: wire.ClarificationQuestion}
	for _, claim := range wire.Claims {
		canonical := Claim{ID: claim.ID, Text: claim.Text, Kind: claim.Kind, SourceIDs: []string{}}
		seenSources := map[string]bool{}
		for _, item := range claim.Evidence {
			if !seenSources[item.SourceID] {
				canonical.SourceIDs = append(canonical.SourceIDs, item.SourceID)
				seenSources[item.SourceID] = true
			}
			response.Evidence = append(response.Evidence, Quote{SourceID: item.SourceID, ClaimIDs: []string{claim.ID}, Quote: item.Quote})
		}
		response.Claims = append(response.Claims, canonical)
	}
	return response, nil
}

func parseCanonical(raw string) (Response, error) {
	var response Response
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return Response{}, fmt.Errorf("decode structured response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Response{}, fmt.Errorf("decode structured response: unexpected trailing content")
	}
	return response, nil
}

var citationPattern = regexp.MustCompile(`\[([SU])([0-9]+)\]`)

func (v Validator) Validate(response Response, final []retrieval.Result, candidates []retrieval.Result, rerankScores map[string]float64) Validation {
	return v.ValidateWithUserSources(response, final, candidates, rerankScores, nil)
}

func (v Validator) ValidateWithUserSources(response Response, final []retrieval.Result, candidates []retrieval.Result, rerankScores map[string]float64, userSources []UserSource) Validation {
	result := Validation{Errors: []Error{}, Sources: []Source{}, Citations: []Citation{}, InvalidSourceIDs: []string{}, InvalidQuotes: []Quote{}}
	add := func(code, message, claimID, sourceID string) {
		result.Errors = append(result.Errors, Error{Code: code, Message: message, ClaimID: claimID, SourceID: sourceID})
	}
	if v.MinQuoteRunes <= 0 || v.MaxQuoteRunes < v.MinQuoteRunes {
		add("invalid_validator_config", "quote length bounds are invalid", "", "")
		return result
	}
	if response.Status != StatusAnswered {
		add("invalid_status", "structured answer must have status=answered", "", "")
	}
	if strings.TrimSpace(response.Answer) == "" {
		add("empty_answer", "answer is empty", "", "")
	}
	if len(response.Claims) == 0 {
		add("empty_claims", "answered response has no claims", "", "")
	}

	finalByID := make(map[string]retrieval.Result, len(final))
	for i, chunk := range final {
		finalByID[fmt.Sprintf("S%d", i+1)] = chunk
	}
	userByID := make(map[string]UserSource, len(userSources))
	for _, source := range userSources {
		userByID[source.ID] = source
	}
	candidateByText := make(map[string]retrieval.Result, len(candidates))
	for _, chunk := range candidates {
		candidateByText[chunk.Text] = chunk
	}

	claims := make(map[string]Claim, len(response.Claims))
	claimHasQuote := make(map[string]bool, len(response.Claims))
	usedSources := make(map[string]bool)
	for _, claim := range response.Claims {
		id := strings.TrimSpace(claim.ID)
		if id == "" {
			add("empty_claim_id", "claim ID is empty", "", "")
			continue
		}
		if _, exists := claims[id]; exists {
			add("duplicate_claim_id", "claim ID is duplicated", id, "")
			continue
		}
		claims[id] = claim
		if strings.TrimSpace(claim.Text) == "" {
			add("empty_claim_text", "claim text is empty", id, "")
		} else if !strings.Contains(normalize(response.Answer), normalize(claim.Text)) {
			add("claim_not_in_answer", "copy claim.text verbatim into answer before its [S<n>] citation; answer must not contain only citation IDs", id, "")
		}
		if len(claim.SourceIDs) == 0 {
			add("claim_without_source", "claim has no source IDs", id, "")
		}
		seen := map[string]bool{}
		hasCorpus, hasMemory := false, false
		for _, sourceID := range claim.SourceIDs {
			sourceID = normalizeSourceID(sourceID)
			if seen[sourceID] {
				add("duplicate_claim_source", "claim repeats a source ID", id, sourceID)
				continue
			}
			seen[sourceID] = true
			_, corpusOK := finalByID[sourceID]
			_, memoryOK := userByID[sourceID]
			if !corpusOK && !memoryOK {
				add("unknown_source_id", "claim refers to a source outside final context", id, sourceID)
				result.InvalidSourceIDs = appendUnique(result.InvalidSourceIDs, sourceID)
			} else {
				usedSources[sourceID] = true
				hasCorpus = hasCorpus || corpusOK
				hasMemory = hasMemory || memoryOK
			}
		}
		kind := claim.Kind
		if kind == "" {
			kind = "corpus"
		}
		if kind != "corpus" && kind != "memory" && kind != "mixed" {
			add("invalid_claim_kind", "claim kind must be corpus, memory, or mixed", id, "")
		}
		if (kind == "corpus" || kind == "mixed") && !hasCorpus {
			add("corpus_claim_without_s", "corpus claim requires at least one S source", id, "")
		}
		if (kind == "memory" || kind == "mixed") && !hasMemory {
			add("memory_claim_without_u", "memory claim requires at least one U source", id, "")
		}
	}

	sourceHasQuote := make(map[string]bool)
	for _, item := range response.Evidence {
		sourceID := normalizeSourceID(item.SourceID)
		chunk, corpusOK := finalByID[sourceID]
		userSource, memoryOK := userByID[sourceID]
		sourceOK := corpusOK || memoryOK
		validQuote := true
		if !sourceOK {
			add("unknown_evidence_source", "evidence refers to a source outside final context", "", sourceID)
			result.InvalidSourceIDs = appendUnique(result.InvalidSourceIDs, sourceID)
			validQuote = false
		}
		if len(item.ClaimIDs) == 0 {
			add("evidence_without_claim", "evidence has no claim IDs", "", sourceID)
			validQuote = false
		}
		quote := item.Quote
		length := utf8.RuneCountInString(quote)
		if strings.TrimSpace(quote) == "" {
			add("empty_quote", "evidence quote is empty", "", sourceID)
			validQuote = false
		} else if length < v.MinQuoteRunes {
			add("quote_too_short", fmt.Sprintf("quote has %d runes; minimum is %d", length, v.MinQuoteRunes), "", sourceID)
			result.TooShortQuotes++
			validQuote = false
		} else if length > v.MaxQuoteRunes {
			add("quote_too_long", fmt.Sprintf("quote has %d runes; maximum is %d", length, v.MaxQuoteRunes), "", sourceID)
			result.TooLongQuotes++
			validQuote = false
		}
		sourceText := chunk.Text
		if memoryOK {
			sourceText = userSource.Quote
		}
		if sourceOK && quote != "" && !strings.Contains(sourceText, quote) {
			wrong := false
			for id, other := range finalByID {
				if id != sourceID && strings.Contains(other.Text, quote) {
					wrong = true
					break
				}
			}
			for id, other := range userByID {
				if id != sourceID && strings.Contains(other.Quote, quote) {
					wrong = true
					break
				}
			}
			if !wrong {
				for text := range candidateByText {
					if text != chunk.Text && strings.Contains(text, quote) {
						wrong = true
						break
					}
				}
			}
			if wrong {
				add("quote_wrong_chunk", "quote exists in another chunk, not the linked final source", "", sourceID)
				result.WrongChunkQuotes++
			} else {
				add("quote_not_exact", "quote is not an exact substring of the linked chunk", "", sourceID)
			}
			validQuote = false
		}
		for _, claimID := range item.ClaimIDs {
			claim, ok := claims[claimID]
			if !ok {
				add("unknown_claim_id", "evidence refers to an unknown claim", claimID, sourceID)
				validQuote = false
				continue
			}
			if !containsSource(claim.SourceIDs, sourceID) {
				add("evidence_source_not_linked", "evidence source is not linked by the claim", claimID, sourceID)
				validQuote = false
			}
		}
		if validQuote {
			result.Citations = append(result.Citations, Citation{SourceID: sourceID, ClaimIDs: append([]string(nil), item.ClaimIDs...), Quote: quote, ExactMatch: true})
			sourceHasQuote[sourceID] = true
			for _, claimID := range item.ClaimIDs {
				claimHasQuote[claimID] = true
			}
		} else {
			copyItem := item
			copyItem.SourceID = sourceID
			result.InvalidQuotes = append(result.InvalidQuotes, copyItem)
		}
	}

	for id := range claims {
		if !claimHasQuote[id] {
			add("claim_without_quote", "claim has no validated evidence quote", id, "")
		}
	}
	for sourceID := range usedSources {
		if !sourceHasQuote[sourceID] {
			add("source_without_quote", "used source has no validated quote", "", sourceID)
		}
	}
	for _, match := range citationPattern.FindAllStringSubmatch(response.Answer, -1) {
		sourceID := match[1] + match[2]
		_, corpusOK := finalByID[sourceID]
		_, memoryOK := userByID[sourceID]
		if !corpusOK && !memoryOK {
			add("invalid_inline_citation", "inline citation is outside final context", "", sourceID)
			result.InvalidSourceIDs = appendUnique(result.InvalidSourceIDs, sourceID)
		}
	}

	ids := make([]string, 0, len(usedSources))
	for id := range usedSources {
		if sourceHasQuote[id] {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		if ids[i][0] != ids[j][0] {
			return ids[i][0] < ids[j][0]
		}
		return sourceNumber(ids[i]) < sourceNumber(ids[j])
	})
	for _, id := range ids {
		if chunk, ok := finalByID[id]; ok {
			result.Sources = append(result.Sources, Source{ID: id, Kind: "corpus", Source: chunk.Source, Section: chunk.Section, ChunkID: chunk.ChunkID, Cosine: chunk.Score, RerankScore: rerankScores[chunk.ChunkID]})
		} else {
			source := userByID[id]
			result.Sources = append(result.Sources, Source{ID: id, Kind: "task_memory", Source: source.Source, Section: source.Section, ChunkID: source.ChunkID, Quote: source.Quote, MemoryID: source.MemoryID, SourceTurnID: source.SourceTurnID})
		}
	}
	sort.Strings(result.InvalidSourceIDs)
	result.Valid = len(result.Errors) == 0
	if !result.Valid {
		result.Sources = []Source{}
	}
	return result
}

func normalize(value string) string         { return strings.Join(strings.Fields(strings.ToLower(value)), " ") }
func normalizeSourceID(value string) string { return strings.Trim(strings.TrimSpace(value), "[]") }
func sourceNumber(id string) int            { n, _ := strconv.Atoi(strings.TrimLeft(id, "SU")); return n }
func containsSource(values []string, want string) bool {
	for _, value := range values {
		if normalizeSourceID(value) == want {
			return true
		}
	}
	return false
}
func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
