package pipeline

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var safeFilename = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,119}\.md$`)

type Saver struct {
	OutputDir string
	Now       func() time.Time
}

func NewSaver(outputDir string) *Saver { return &Saver{OutputDir: outputDir, Now: time.Now} }

func (s *Saver) Save(input SaveInput) (SaveOutput, error) {
	input.Filename = strings.TrimSpace(input.Filename)
	input.Query = cleanSpace(input.Query)
	input.Content = strings.TrimSpace(input.Content)
	if filepath.IsAbs(input.Filename) || strings.Contains(input.Filename, "..") || strings.ContainsAny(input.Filename, `/\\`) || !safeFilename.MatchString(input.Filename) {
		return SaveOutput{}, errors.New("filename must be a safe relative .md filename")
	}
	if input.Query == "" || len([]rune(input.Query)) > 300 {
		return SaveOutput{}, errors.New("query must contain 1 to 300 characters")
	}
	if input.Content == "" {
		return SaveOutput{}, errors.New("content cannot be empty")
	}
	if len([]byte(input.Content)) > MaxFileContent {
		return SaveOutput{}, fmt.Errorf("content exceeds %d bytes", MaxFileContent)
	}
	if len(input.Sources) > 5 {
		return SaveOutput{}, errors.New("sources must contain at most 5 items")
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	markdown := buildMarkdown(input, now)
	if err := os.MkdirAll(s.OutputDir, 0o750); err != nil {
		return SaveOutput{}, fmt.Errorf("create output directory: %w", err)
	}
	target := filepath.Join(s.OutputDir, input.Filename)
	cleanRoot, err := filepath.Abs(s.OutputDir)
	if err != nil {
		return SaveOutput{}, fmt.Errorf("resolve output directory: %w", err)
	}
	cleanTarget, err := filepath.Abs(target)
	if err != nil || filepath.Dir(cleanTarget) != cleanRoot {
		return SaveOutput{}, errors.New("resolved path is outside output directory")
	}
	temp, err := os.CreateTemp(cleanRoot, ".day19-*.tmp")
	if err != nil {
		return SaveOutput{}, fmt.Errorf("create temporary file: %w", err)
	}
	tempName := temp.Name()
	keep := false
	defer func() {
		_ = temp.Close()
		if !keep {
			_ = os.Remove(tempName)
		}
	}()
	if err := temp.Chmod(0o640); err != nil {
		return SaveOutput{}, fmt.Errorf("set temporary file mode: %w", err)
	}
	if _, err := temp.Write(markdown); err != nil {
		return SaveOutput{}, fmt.Errorf("write temporary file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return SaveOutput{}, fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return SaveOutput{}, fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(tempName, cleanTarget); err != nil {
		return SaveOutput{}, fmt.Errorf("commit output file: %w", err)
	}
	keep = true
	digest := fmt.Sprintf("%x", sha256.Sum256(markdown))
	sources := make([]Source, len(input.Sources))
	copy(sources, input.Sources)
	return SaveOutput{OK: true, Path: cleanTarget, Filename: input.Filename, Bytes: int64(len(markdown)), SHA256: digest, SavedAt: &now, Sources: sources}, nil
}

func buildMarkdown(input SaveInput, created time.Time) []byte {
	var text strings.Builder
	text.WriteString("# Сводка: ")
	text.WriteString(input.Query)
	text.WriteString("\n\n**Поисковый запрос:** ")
	text.WriteString(input.Query)
	text.WriteString("\n\n## Краткая сводка\n\n")
	text.WriteString(input.Content)
	text.WriteString("\n\n## Источники\n\n")
	if len(input.Sources) == 0 {
		text.WriteString("Источники не указаны.\n")
	} else {
		for _, source := range input.Sources {
			title := cleanSpace(source.Title)
			if title == "" {
				title = source.URL
			}
			text.WriteString("- [")
			text.WriteString(strings.ReplaceAll(title, "]", "\\]"))
			text.WriteString("](")
			text.WriteString(strings.TrimSpace(source.URL))
			text.WriteString(")\n")
		}
	}
	text.WriteString("\n---\nСоздано: ")
	text.WriteString(created.Format(time.RFC3339))
	text.WriteByte('\n')
	return []byte(text.String())
}
