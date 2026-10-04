package document

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var supportedExtensions = map[string]string{
	".md":  "markdown",
	".txt": "text",
	".go":  "go",
	".pdf": "pdf",
}

var excludedDirectories = map[string]bool{
	".git": true, "vendor": true, "node_modules": true,
	"artifacts": true, "build": true, "dist": true, "target": true,
	"generated": true, ".dart_tool": true, ".idea": true, ".vscode": true,
}

var errSkipFile = errors.New("skip non-source file")

// ExtractPDF is replaceable in tests. The default implementation invokes the
// documented local pdftotext dependency.
var ExtractPDF = extractPDFText

func Load(inputs []string) ([]Document, error) {
	if len(inputs) == 0 {
		return nil, errors.New("at least one input path is required")
	}
	seen := make(map[string]bool)
	var docs []Document
	for _, rawInput := range inputs {
		input := filepath.Clean(strings.TrimSpace(rawInput))
		if input == "." && strings.TrimSpace(rawInput) == "" {
			continue
		}
		info, err := os.Stat(input)
		if err != nil {
			return nil, fmt.Errorf("inspect input %q: %w", input, err)
		}
		root := input
		if !info.IsDir() {
			root = filepath.Dir(input)
		}
		prefix := filepath.Base(root)
		err = filepath.WalkDir(input, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if path != input && excludedDirectories[entry.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if excludedFile(entry.Name()) {
				return nil
			}
			docType, ok := supportedExtensions[strings.ToLower(filepath.Ext(entry.Name()))]
			if !ok {
				return nil
			}
			absolute, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			if seen[absolute] {
				return nil
			}
			seen[absolute] = true

			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			source := filepath.ToSlash(filepath.Join(prefix, rel))
			doc, err := loadFile(path, source, docType)
			if err != nil {
				if errors.Is(err, errSkipFile) {
					return nil
				}
				return err
			}
			docs = append(docs, doc)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("load %q: %w", input, err)
		}
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("no supported documents found (extensions: .md, .txt, .go, .pdf)")
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Source < docs[j].Source })
	return docs, nil
}

func excludedFile(name string) bool {
	lower := strings.ToLower(name)
	return lower == ".env" || strings.HasPrefix(lower, ".env.") ||
		lower == ".ds_store" || strings.HasSuffix(lower, ".generated.go") ||
		strings.HasSuffix(lower, "_generated.go")
}

func loadFile(path, source, docType string) (Document, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Document{}, fmt.Errorf("read %s: %w", source, err)
	}
	sum := sha256.Sum256(raw)
	text := string(raw)
	pages := 0
	if docType == "pdf" {
		text, pages, err = ExtractPDF(path)
		if err != nil {
			return Document{}, fmt.Errorf("extract PDF %s: %w", source, err)
		}
	} else if !utf8.Valid(raw) || strings.IndexByte(text, 0) >= 0 {
		return Document{}, fmt.Errorf("%w: %s is binary or not valid UTF-8", errSkipFile, source)
	} else if docType == "go" {
		prefix := text
		if len(prefix) > 2048 {
			prefix = prefix[:2048]
		}
		if strings.Contains(prefix, "Code generated") && strings.Contains(prefix, "DO NOT EDIT") {
			return Document{}, fmt.Errorf("%w: %s is generated Go source", errSkipFile, source)
		}
	}
	if strings.TrimSpace(text) == "" {
		return Document{}, fmt.Errorf("%s contains no extractable text", source)
	}
	lines := 0
	if docType != "pdf" {
		lines = countLines(text)
	}
	return Document{
		Source: source, Title: filepath.Base(path), Type: docType,
		CharCount: utf8.RuneCountInString(text), WordCount: len(strings.Fields(text)),
		LineCount: lines, PageCount: pages, SHA256: hex.EncodeToString(sum[:]), Text: text,
	}, nil
}

func countLines(text string) int {
	if text == "" {
		return 0
	}
	count := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		count++
	}
	return count
}

func NewManifest(inputs []string, docs []Document, generatedAt time.Time) Manifest {
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		GeneratedAt:   generatedAt.UTC(),
		CorpusID:      CorpusID(docs),
		Inputs:        append([]string(nil), inputs...),
		Documents:     append([]Document(nil), docs...),
	}
	manifest.Stats.Documents = len(docs)
	for _, doc := range docs {
		manifest.Stats.Characters += doc.CharCount
		manifest.Stats.Words += doc.WordCount
		manifest.Stats.Lines += doc.LineCount
		manifest.Stats.PDFPages += doc.PageCount
	}
	manifest.Stats.EstimatedPages = float64(manifest.Stats.Words) / 400
	return manifest
}

func CorpusID(docs []Document) string {
	h := sha256.New()
	for _, doc := range docs {
		_, _ = io.WriteString(h, doc.Source)
		_, _ = io.WriteString(h, "\x00")
		_, _ = io.WriteString(h, doc.SHA256)
		_, _ = io.WriteString(h, "\n")
	}
	return hex.EncodeToString(h.Sum(nil))
}

func WriteManifest(path string, manifest Manifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".corpus-manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary manifest: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("set manifest permissions: %w", err)
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest); err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close manifest: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace manifest: %w", err)
	}
	ok = true
	return nil
}
