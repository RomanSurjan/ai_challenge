package pipeline

import "time"

const (
	SearchToolName    = "search"
	SummarizeToolName = "summarize"
	SaveToolName      = "save_to_file"
	WikipediaSource   = "Wikipedia"
	MaxFileContent    = 256 << 10
)

type ToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Document struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Text  string `json:"text"`
}

type SearchInput struct {
	Query    string `json:"query"`
	Language string `json:"language,omitempty"`
	Limit    int    `json:"limit"`
}

type SearchOutput struct {
	OK        bool       `json:"ok"`
	Query     string     `json:"query,omitempty"`
	Source    string     `json:"source,omitempty"`
	FetchedAt *time.Time `json:"fetched_at,omitempty"`
	Documents []Document `json:"documents"`
	Count     int        `json:"count"`
	Error     *ToolError `json:"error,omitempty"`
}

type SummarizeInput struct {
	Query        string     `json:"query"`
	Documents    []Document `json:"documents"`
	MaxSentences int        `json:"max_sentences"`
}

type Source struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type SummarizeOutput struct {
	OK             bool       `json:"ok"`
	Query          string     `json:"query,omitempty"`
	Summary        string     `json:"summary,omitempty"`
	Sources        []Source   `json:"sources"`
	InputDocuments int        `json:"input_documents"`
	SentenceCount  int        `json:"sentence_count"`
	Warning        string     `json:"warning,omitempty"`
	Error          *ToolError `json:"error,omitempty"`
}

type SaveInput struct {
	Filename string   `json:"filename"`
	Content  string   `json:"content"`
	Sources  []Source `json:"sources"`
	Query    string   `json:"query"`
}

type SaveOutput struct {
	OK       bool       `json:"ok"`
	Path     string     `json:"path,omitempty"`
	Filename string     `json:"filename,omitempty"`
	Bytes    int64      `json:"bytes"`
	SHA256   string     `json:"sha256,omitempty"`
	SavedAt  *time.Time `json:"saved_at,omitempty"`
	Sources  []Source   `json:"sources"`
	Error    *ToolError `json:"error,omitempty"`
}

func failure(code string, err error) *ToolError {
	return &ToolError{Code: code, Message: err.Error()}
}
