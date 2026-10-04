package document

import "time"

const ManifestSchemaVersion = "1"

// Document is an extracted source document. Text is intentionally omitted from
// the corpus manifest; it is carried in memory into the chunking stage.
type Document struct {
	Source    string `json:"source"`
	Title     string `json:"title"`
	Type      string `json:"type"`
	CharCount int    `json:"character_count"`
	WordCount int    `json:"word_count"`
	LineCount int    `json:"line_count,omitempty"`
	PageCount int    `json:"page_count,omitempty"`
	SHA256    string `json:"sha256"`
	Text      string `json:"-"`
}

type ManifestStats struct {
	Documents      int     `json:"documents"`
	Characters     int     `json:"characters"`
	Words          int     `json:"words"`
	Lines          int     `json:"lines"`
	PDFPages       int     `json:"pdf_pages"`
	EstimatedPages float64 `json:"estimated_pages"`
}

type Manifest struct {
	SchemaVersion string        `json:"schema_version"`
	GeneratedAt   time.Time     `json:"generated_at"`
	CorpusID      string        `json:"corpus_id"`
	Inputs        []string      `json:"inputs"`
	Stats         ManifestStats `json:"stats"`
	Documents     []Document    `json:"documents"`
}
