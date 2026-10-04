package chunk

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"unicode"

	"ai-challenge/day-21/internal/document"
)

type Strategy string

const (
	Fixed      Strategy = "fixed"
	Structural Strategy = "structural"
)

type Config struct {
	TargetWords  int `json:"target_words"`
	OverlapWords int `json:"overlap_words"`
}

type Chunk struct {
	ChunkID   string    `json:"chunk_id"`
	Source    string    `json:"source"`
	Title     string    `json:"title"`
	Section   string    `json:"section"`
	Strategy  Strategy  `json:"strategy"`
	Ordinal   int       `json:"ordinal"`
	Start     int       `json:"start"`
	End       int       `json:"end"`
	WordCount int       `json:"word_count"`
	Text      string    `json:"text"`
	Embedding []float64 `json:"embedding"`
}

type wordSpan struct {
	start int
	end   int
}

func (c Config) Validate() error {
	if c.TargetWords <= 0 {
		return fmt.Errorf("target words must be positive")
	}
	if c.OverlapWords < 0 {
		return fmt.Errorf("overlap words cannot be negative")
	}
	if c.OverlapWords >= c.TargetWords {
		return fmt.Errorf("overlap words (%d) must be less than target words (%d)", c.OverlapWords, c.TargetWords)
	}
	return nil
}

func Build(docs []document.Document, strategy Strategy, config Config) ([]Chunk, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	var chunks []Chunk
	for _, doc := range docs {
		var docChunks []Chunk
		var err error
		switch strategy {
		case Fixed:
			docChunks = fixedDocument(doc, config)
		case Structural:
			docChunks, err = structuralDocument(doc, config)
		default:
			return nil, fmt.Errorf("unsupported chunking strategy %q", strategy)
		}
		if err != nil {
			return nil, fmt.Errorf("chunk %s: %w", doc.Source, err)
		}
		for i := range docChunks {
			docChunks[i].Ordinal = i
			docChunks[i].ChunkID = ID(docChunks[i])
		}
		chunks = append(chunks, docChunks...)
	}
	if err := Validate(chunks); err != nil {
		return nil, err
	}
	return chunks, nil
}

func fixedDocument(doc document.Document, config Config) []Chunk {
	return splitRange(doc, Fixed, "Document", 0, len([]rune(doc.Text)), config)
}

func splitRange(doc document.Document, strategy Strategy, section string, start, end int, config Config) []Chunk {
	runes := []rune(doc.Text)
	if start < 0 {
		start = 0
	}
	if end > len(runes) {
		end = len(runes)
	}
	if start >= end {
		return nil
	}
	spans := words(runes[start:end])
	if len(spans) == 0 {
		return nil
	}
	step := config.TargetWords - config.OverlapWords
	var result []Chunk
	for first := 0; first < len(spans); first += step {
		last := min(first+config.TargetWords, len(spans))
		chunkStart := start + spans[first].start
		chunkEnd := start + spans[last-1].end
		chunkStart, chunkEnd = trimRuneRange(runes, chunkStart, chunkEnd)
		if chunkStart < chunkEnd {
			result = append(result, Chunk{
				Source: doc.Source, Title: doc.Title, Section: section,
				Strategy: strategy, Start: chunkStart, End: chunkEnd,
				WordCount: last - first, Text: string(runes[chunkStart:chunkEnd]),
			})
		}
		if last == len(spans) {
			break
		}
	}
	return result
}

func words(runes []rune) []wordSpan {
	var spans []wordSpan
	for pos := 0; pos < len(runes); {
		for pos < len(runes) && unicode.IsSpace(runes[pos]) {
			pos++
		}
		if pos == len(runes) {
			break
		}
		start := pos
		for pos < len(runes) && !unicode.IsSpace(runes[pos]) {
			pos++
		}
		spans = append(spans, wordSpan{start: start, end: pos})
	}
	return spans
}

func trimRuneRange(runes []rune, start, end int) (int, int) {
	for start < end && unicode.IsSpace(runes[start]) {
		start++
	}
	for end > start && unicode.IsSpace(runes[end-1]) {
		end--
	}
	return start, end
}

func ID(c Chunk) string {
	textHash := sha256.Sum256([]byte(c.Text))
	payload := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s", c.Strategy, c.Source, c.Section, c.Ordinal, hex.EncodeToString(textHash[:]))
	sum := sha256.Sum256([]byte(payload))
	return string(c.Strategy) + "-" + hex.EncodeToString(sum[:12])
}

func Validate(chunks []Chunk) error {
	ids := make(map[string]bool, len(chunks))
	for i, c := range chunks {
		if c.ChunkID == "" || c.Source == "" || c.Title == "" || c.Section == "" || c.Text == "" {
			return fmt.Errorf("chunk %d has missing required metadata", i)
		}
		if strings.TrimSpace(c.Text) == "" || c.WordCount <= 0 || c.Start < 0 || c.End <= c.Start {
			return fmt.Errorf("chunk %s has invalid content or boundaries", c.ChunkID)
		}
		if c.Strategy != Fixed && c.Strategy != Structural {
			return fmt.Errorf("chunk %s has invalid strategy %q", c.ChunkID, c.Strategy)
		}
		if ids[c.ChunkID] {
			return fmt.Errorf("duplicate chunk ID %s", c.ChunkID)
		}
		ids[c.ChunkID] = true
		for _, value := range c.Embedding {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return fmt.Errorf("chunk %s has non-finite embedding", c.ChunkID)
			}
		}
	}
	return nil
}
