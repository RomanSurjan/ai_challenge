package domain

import "time"

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Document struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Text  string `json:"text"`
}
type Source struct {
	Title string `json:"title"`
	URL   string `json:"url"`
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
	Error     *Error     `json:"error,omitempty"`
}
type SummarizeInput struct {
	Query        string     `json:"query"`
	Documents    []Document `json:"documents"`
	MaxSentences int        `json:"max_sentences"`
}
type SummarizeOutput struct {
	OK             bool     `json:"ok"`
	Query          string   `json:"query,omitempty"`
	Summary        string   `json:"summary,omitempty"`
	Sources        []Source `json:"sources"`
	InputDocuments int      `json:"input_documents"`
	SentenceCount  int      `json:"sentence_count"`
	Warning        string   `json:"warning,omitempty"`
	Error          *Error   `json:"error,omitempty"`
}
type RepositoryInput struct {
	Owner      string `json:"owner"`
	Repository string `json:"repository"`
}
type RepositoryOutput struct {
	OK            bool       `json:"ok"`
	Owner         string     `json:"owner,omitempty"`
	Repository    string     `json:"repository,omitempty"`
	FullName      string     `json:"full_name,omitempty"`
	URL           string     `json:"url,omitempty"`
	Description   string     `json:"description,omitempty"`
	Language      string     `json:"language,omitempty"`
	DefaultBranch string     `json:"default_branch,omitempty"`
	Stars         int        `json:"stars"`
	Forks         int        `json:"forks"`
	OpenIssues    int        `json:"open_issues"`
	UpdatedAt     string     `json:"updated_at,omitempty"`
	Source        string     `json:"source,omitempty"`
	FetchedAt     *time.Time `json:"fetched_at,omitempty"`
	Error         *Error     `json:"error,omitempty"`
}
type ReleaseOutput struct {
	OK          bool       `json:"ok"`
	Owner       string     `json:"owner,omitempty"`
	Repository  string     `json:"repository,omitempty"`
	Found       bool       `json:"found"`
	Tag         string     `json:"tag,omitempty"`
	Name        string     `json:"name,omitempty"`
	URL         string     `json:"url,omitempty"`
	PublishedAt string     `json:"published_at,omitempty"`
	Draft       bool       `json:"draft"`
	Prerelease  bool       `json:"prerelease"`
	Source      string     `json:"source,omitempty"`
	FetchedAt   *time.Time `json:"fetched_at,omitempty"`
	Error       *Error     `json:"error,omitempty"`
}
type ReportInput struct {
	Topic            string           `json:"topic"`
	Title            string           `json:"title"`
	Knowledge        SummarizeOutput  `json:"knowledge"`
	KnowledgeSources []Source         `json:"knowledge_sources"`
	Repository       RepositoryOutput `json:"repository"`
	LatestRelease    ReleaseOutput    `json:"latest_release"`
}
type ReportOutput struct {
	OK       bool     `json:"ok"`
	Markdown string   `json:"markdown,omitempty"`
	Sources  []Source `json:"sources"`
	SHA256   string   `json:"sha256,omitempty"`
	Sections []string `json:"sections"`
	Error    *Error   `json:"error,omitempty"`
}
type SaveInput struct {
	Filename string   `json:"filename"`
	Markdown string   `json:"markdown"`
	Sources  []Source `json:"sources"`
}
type SaveOutput struct {
	OK       bool       `json:"ok"`
	Path     string     `json:"path,omitempty"`
	Filename string     `json:"filename,omitempty"`
	Bytes    int64      `json:"bytes"`
	SHA256   string     `json:"sha256,omitempty"`
	SavedAt  *time.Time `json:"saved_at,omitempty"`
	Sources  []Source   `json:"sources"`
	Error    *Error     `json:"error,omitempty"`
}
type ListInput struct{}
type FileInfo struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Bytes      int64     `json:"bytes"`
	ModifiedAt time.Time `json:"modified_at"`
	SHA256     string    `json:"sha256"`
}
type ListOutput struct {
	OK    bool       `json:"ok"`
	Files []FileInfo `json:"files"`
	Count int        `json:"count"`
	Error *Error     `json:"error,omitempty"`
}
type ReadInput struct {
	Filename string `json:"filename"`
}
type ReadOutput struct {
	OK       bool   `json:"ok"`
	Filename string `json:"filename,omitempty"`
	Path     string `json:"path,omitempty"`
	Markdown string `json:"markdown,omitempty"`
	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256,omitempty"`
	Error    *Error `json:"error,omitempty"`
}

func Failure(code string, err error) *Error { return &Error{Code: code, Message: err.Error()} }
