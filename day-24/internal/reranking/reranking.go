package reranking

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"ai-challenge/day-24/internal/retrieval"
)

type Config struct {
	MinSimilarity float64 `json:"min_similarity"`
	Alpha         float64 `json:"alpha"`
	Beta          float64 `json:"beta"`
	Gamma         float64 `json:"gamma"`
	FinalK        int     `json:"final_k"`
}

type Candidate struct {
	RankBefore       int     `json:"rank_before"`
	RankAfter        int     `json:"rank_after,omitempty"`
	ChunkID          string  `json:"chunk_id"`
	Source           string  `json:"source"`
	Section          string  `json:"section"`
	Text             string  `json:"text"`
	CosineScore      float64 `json:"cosine_score"`
	NormalizedCosine float64 `json:"normalized_cosine"`
	LexicalScore     float64 `json:"lexical_score"`
	MetadataScore    float64 `json:"metadata_score"`
	RerankScore      float64 `json:"rerank_score"`
	PassedThreshold  bool    `json:"passed_threshold"`
	ExcludedReason   string  `json:"excluded_reason,omitempty"`
}

type Output struct {
	Candidates []Candidate        `json:"candidates"`
	Rejected   []Candidate        `json:"rejected_candidates"`
	Final      []retrieval.Result `json:"final_context"`
}

var stopTokens = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "with": {}, "from": {}, "this": {}, "that": {},
	"как": {}, "что": {}, "это": {}, "для": {}, "при": {}, "или": {}, "его": {}, "она": {}, "они": {},
}

func Validate(config Config) error {
	if config.FinalK <= 0 {
		return fmt.Errorf("final-k must be positive")
	}
	if config.MinSimilarity < -1 || config.MinSimilarity > 1 {
		return fmt.Errorf("min-similarity must be in [-1, 1]")
	}
	if config.Alpha < 0 || config.Beta < 0 || config.Gamma < 0 {
		return fmt.Errorf("reranker weights must be non-negative")
	}
	if config.Alpha+config.Beta+config.Gamma == 0 {
		return fmt.Errorf("at least one reranker weight must be positive")
	}
	return nil
}

// Tokens returns unique lowercase Unicode letter/digit tokens. Very short and
// common function words are ignored so they cannot dominate overlap scores.
func Tokens(value string) map[string]struct{} {
	tokens := make(map[string]struct{})
	var current []rune
	flush := func() {
		if len(current) == 0 {
			return
		}
		token := strings.ToLower(string(current))
		current = current[:0]
		if len([]rune(token)) < 3 {
			return
		}
		if _, stop := stopTokens[token]; !stop {
			tokens[token] = struct{}{}
		}
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current = append(current, r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

func overlap(query, value map[string]struct{}) float64 {
	if len(query) == 0 {
		return 0
	}
	matched := 0
	for token := range query {
		if _, ok := value[token]; ok {
			matched++
		}
	}
	return float64(matched) / float64(len(query))
}

func Scores(query, text, metadata string) (lexical, meta float64) {
	queryTokens := Tokens(query)
	return overlap(queryTokens, Tokens(text)), overlap(queryTokens, Tokens(metadata))
}

func Apply(query string, results []retrieval.Result, config Config) (Output, error) {
	if err := Validate(config); err != nil {
		return Output{}, err
	}
	output := Output{Candidates: make([]Candidate, len(results)), Rejected: []Candidate{}, Final: []retrieval.Result{}}
	passing := make([]Candidate, 0, len(results))
	weightTotal := config.Alpha + config.Beta + config.Gamma
	for i, result := range results {
		lexical, metadata := Scores(query, result.Text, result.Source+" "+result.Section)
		normalized := (result.Score + 1) / 2
		candidate := Candidate{
			RankBefore: i + 1, ChunkID: result.ChunkID, Source: result.Source, Section: result.Section, Text: result.Text,
			CosineScore: result.Score, NormalizedCosine: normalized, LexicalScore: lexical, MetadataScore: metadata,
			RerankScore:     (config.Alpha*normalized + config.Beta*lexical + config.Gamma*metadata) / weightTotal,
			PassedThreshold: result.Score >= config.MinSimilarity,
		}
		if !candidate.PassedThreshold {
			candidate.ExcludedReason = fmt.Sprintf("cosine %.6f below threshold %.6f", result.Score, config.MinSimilarity)
			output.Rejected = append(output.Rejected, candidate)
		} else {
			passing = append(passing, candidate)
		}
		output.Candidates[i] = candidate
	}
	sort.SliceStable(passing, func(i, j int) bool {
		if passing[i].RerankScore == passing[j].RerankScore {
			return passing[i].ChunkID < passing[j].ChunkID
		}
		return passing[i].RerankScore > passing[j].RerankScore
	})
	for i := range passing {
		if i < config.FinalK {
			passing[i].RankAfter = i + 1
			output.Final = append(output.Final, retrieval.Result{
				Rank: i + 1, Score: passing[i].CosineScore, ChunkID: passing[i].ChunkID,
				Source: passing[i].Source, Section: passing[i].Section, Text: passing[i].Text,
			})
		} else {
			passing[i].ExcludedReason = fmt.Sprintf("outside final-k=%d after reranking", config.FinalK)
			output.Rejected = append(output.Rejected, passing[i])
		}
	}
	byID := make(map[string]Candidate, len(output.Candidates))
	for _, candidate := range passing {
		byID[candidate.ChunkID] = candidate
	}
	for i, candidate := range output.Candidates {
		if updated, ok := byID[candidate.ChunkID]; ok {
			output.Candidates[i] = updated
		}
	}
	return output, nil
}

// Passthrough preserves cosine order for baseline and rewrite-only ablations.
func Passthrough(query string, results []retrieval.Result, finalK int) (Output, error) {
	if finalK <= 0 {
		return Output{}, fmt.Errorf("final-k must be positive")
	}
	output := Output{Candidates: make([]Candidate, len(results)), Rejected: []Candidate{}, Final: []retrieval.Result{}}
	for i, result := range results {
		lexical, metadata := Scores(query, result.Text, result.Source+" "+result.Section)
		candidate := Candidate{
			RankBefore: i + 1, ChunkID: result.ChunkID, Source: result.Source, Section: result.Section, Text: result.Text,
			CosineScore: result.Score, NormalizedCosine: (result.Score + 1) / 2,
			LexicalScore: lexical, MetadataScore: metadata, RerankScore: result.Score, PassedThreshold: true,
		}
		if i < finalK {
			candidate.RankAfter = i + 1
			copyResult := result
			copyResult.Rank = i + 1
			output.Final = append(output.Final, copyResult)
		} else {
			candidate.ExcludedReason = fmt.Sprintf("outside final-k=%d", finalK)
			output.Rejected = append(output.Rejected, candidate)
		}
		output.Candidates[i] = candidate
	}
	return output, nil
}
