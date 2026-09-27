package pipeline

import (
	"errors"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var sentenceBoundary = regexp.MustCompile(`[^.!?…]+(?:[.!?…]+|$)`)

type rankedSentence struct {
	text  string
	order int
	score int
}

func Summarize(input SummarizeInput) (SummarizeOutput, error) {
	input.Query = cleanSpace(input.Query)
	if input.Query == "" || len([]rune(input.Query)) > 300 {
		return SummarizeOutput{}, errors.New("query must contain 1 to 300 characters")
	}
	if input.MaxSentences < 1 || input.MaxSentences > 10 {
		return SummarizeOutput{}, errors.New("max_sentences must be between 1 and 10")
	}
	if len(input.Documents) > 5 {
		return SummarizeOutput{}, errors.New("documents must contain at most 5 items")
	}
	terms := meaningfulWords(input.Query)
	seen := map[string]bool{}
	all := make([]rankedSentence, 0)
	sources := make([]Source, 0, len(input.Documents))
	seenSources := map[string]bool{}
	order := 0
	for _, document := range input.Documents {
		document.Title, document.URL, document.Text = cleanSpace(document.Title), strings.TrimSpace(document.URL), cleanSpace(document.Text)
		if document.URL != "" && !seenSources[document.URL] {
			sources = append(sources, Source{Title: document.Title, URL: document.URL})
			seenSources[document.URL] = true
		}
		for _, match := range sentenceBoundary.FindAllString(document.Text, -1) {
			sentence := cleanSpace(match)
			key := strings.ToLower(strings.Trim(sentence, " .!?…"))
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			words := meaningfulWords(sentence)
			score := len(words)
			for term := range terms {
				if words[term] {
					score += 12
				}
			}
			if len([]rune(sentence)) >= 45 && len([]rune(sentence)) <= 260 {
				score += 4
			}
			all = append(all, rankedSentence{text: sentence, order: order, score: score})
			order++
		}
	}
	warning := ""
	if len(all) == 0 {
		warning = "Недостаточно данных для составления сводки."
		return SummarizeOutput{OK: true, Query: input.Query, Sources: sources, InputDocuments: len(input.Documents), Warning: warning}, nil
	}
	ranked := append([]rankedSentence(nil), all...)
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].order < ranked[j].order
		}
		return ranked[i].score > ranked[j].score
	})
	count := input.MaxSentences
	if len(ranked) < count {
		count = len(ranked)
		warning = "Доступно меньше информативных предложений, чем запрошено."
	}
	chosen := append([]rankedSentence(nil), ranked[:count]...)
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].order < chosen[j].order })
	parts := make([]string, len(chosen))
	for i := range chosen {
		parts[i] = chosen[i].text
	}
	return SummarizeOutput{OK: true, Query: input.Query, Summary: strings.Join(parts, " "), Sources: sources, InputDocuments: len(input.Documents), SentenceCount: len(chosen), Warning: warning}, nil
}

func meaningfulWords(value string) map[string]bool {
	words := map[string]bool{}
	var current []rune
	flush := func() {
		if len(current) >= 3 {
			words[string(current)] = true
		}
		current = current[:0]
	}
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current = append(current, r)
		} else {
			flush()
		}
	}
	flush()
	return words
}
