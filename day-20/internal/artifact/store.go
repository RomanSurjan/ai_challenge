package artifact

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"ai-challenge/day-20/internal/domain"
)

const MaxMarkdown = 512 << 10

var safeFilename = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,119}\.md$`)

type Store struct {
	OutputDir string
	Now       func() time.Time
}

func NewStore(dir string) *Store { return &Store{OutputDir: dir, Now: time.Now} }

func Build(input domain.ReportInput) (domain.ReportOutput, error) {
	input.Topic = strings.TrimSpace(input.Topic)
	input.Title = strings.TrimSpace(input.Title)
	if input.Topic == "" || input.Title == "" {
		return domain.ReportOutput{}, errors.New("topic and title are required")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n## Тема\n\n%s\n\n## Сводка Wikipedia\n\n%s\n\n## Репозиторий GitHub\n\n", input.Title, input.Topic, input.Knowledge.Summary)
	fmt.Fprintf(&b, "- **Репозиторий:** [%s](%s)\n- **Описание:** %s\n- **Язык:** %s\n- **Ветка:** `%s`\n- **Stars / forks / issues:** %d / %d / %d\n- **Обновлён:** %s\n\n", input.Repository.FullName, input.Repository.URL, input.Repository.Description, input.Repository.Language, input.Repository.DefaultBranch, input.Repository.Stars, input.Repository.Forks, input.Repository.OpenIssues, input.Repository.UpdatedAt)
	b.WriteString("## Последний релиз\n\n")
	if input.LatestRelease.Found {
		fmt.Fprintf(&b, "[%s — %s](%s), опубликован %s. Draft: %t. Prerelease: %t.\n\n", input.LatestRelease.Tag, input.LatestRelease.Name, input.LatestRelease.URL, input.LatestRelease.PublishedAt, input.LatestRelease.Draft, input.LatestRelease.Prerelease)
	} else {
		b.WriteString("Опубликованные релизы не найдены.\n\n")
	}
	b.WriteString("## Источники\n\n")
	sources := append([]domain.Source(nil), input.KnowledgeSources...)
	if input.Repository.URL != "" {
		sources = append(sources, domain.Source{Title: input.Repository.FullName, URL: input.Repository.URL})
	}
	if input.LatestRelease.Found && input.LatestRelease.URL != "" {
		sources = append(sources, domain.Source{Title: input.LatestRelease.Tag, URL: input.LatestRelease.URL})
	}
	for _, s := range sources {
		fmt.Fprintf(&b, "- [%s](%s)\n", strings.ReplaceAll(strings.TrimSpace(s.Title), "]", "\\]"), strings.TrimSpace(s.URL))
	}
	markdown := b.String()
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(markdown)))
	return domain.ReportOutput{OK: true, Markdown: markdown, Sources: sources, SHA256: sum, Sections: []string{"Тема", "Сводка Wikipedia", "Репозиторий GitHub", "Последний релиз", "Источники"}}, nil
}
func validName(name string) bool {
	return !filepath.IsAbs(name) && !strings.Contains(name, "..") && !strings.ContainsAny(name, `/\`) && safeFilename.MatchString(name)
}
func (s *Store) resolve(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !validName(name) {
		return "", errors.New("filename must be a safe relative .md filename")
	}
	root, err := filepath.Abs(s.OutputDir)
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, name)
	if filepath.Dir(target) != root {
		return "", errors.New("resolved path is outside output directory")
	}
	return target, nil
}
func (s *Store) Save(input domain.SaveInput) (domain.SaveOutput, error) {
	target, err := s.resolve(input.Filename)
	if err != nil {
		return domain.SaveOutput{}, err
	}
	if input.Markdown == "" {
		return domain.SaveOutput{}, errors.New("markdown cannot be empty")
	}
	data := []byte(input.Markdown)
	if len(data) > MaxMarkdown {
		return domain.SaveOutput{}, fmt.Errorf("markdown exceeds %d bytes", MaxMarkdown)
	}
	if err = os.MkdirAll(filepath.Dir(target), 0750); err != nil {
		return domain.SaveOutput{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".day20-*.tmp")
	if err != nil {
		return domain.SaveOutput{}, err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()
	if err = tmp.Chmod(0640); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if err == nil {
		err = tmp.Close()
	}
	if err == nil {
		err = os.Rename(tmpName, target)
	}
	if err != nil {
		return domain.SaveOutput{}, fmt.Errorf("atomic write: %w", err)
	}
	committed = true
	now := s.Now().UTC()
	sum := fmt.Sprintf("%x", sha256.Sum256(data))
	return domain.SaveOutput{OK: true, Path: target, Filename: input.Filename, Bytes: int64(len(data)), SHA256: sum, SavedAt: &now, Sources: append([]domain.Source(nil), input.Sources...)}, nil
}
func (s *Store) List() (domain.ListOutput, error) {
	entries, err := os.ReadDir(s.OutputDir)
	if errors.Is(err, os.ErrNotExist) {
		return domain.ListOutput{OK: true, Files: []domain.FileInfo{}}, nil
	}
	if err != nil {
		return domain.ListOutput{}, err
	}
	files := []domain.FileInfo{}
	for _, e := range entries {
		if e.IsDir() || !validName(e.Name()) {
			continue
		}
		out, err := s.Read(domain.ReadInput{Filename: e.Name()})
		if err != nil {
			continue
		}
		info, _ := e.Info()
		files = append(files, domain.FileInfo{Name: e.Name(), Path: out.Path, Bytes: out.Bytes, ModifiedAt: info.ModTime().UTC(), SHA256: out.SHA256})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return domain.ListOutput{OK: true, Files: files, Count: len(files)}, nil
}
func (s *Store) Read(input domain.ReadInput) (domain.ReadOutput, error) {
	target, err := s.resolve(input.Filename)
	if err != nil {
		return domain.ReadOutput{}, err
	}
	f, err := os.Open(target)
	if err != nil {
		return domain.ReadOutput{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxMarkdown+1))
	if err != nil {
		return domain.ReadOutput{}, err
	}
	if len(data) > MaxMarkdown {
		return domain.ReadOutput{}, errors.New("file exceeds size limit")
	}
	sum := fmt.Sprintf("%x", sha256.Sum256(data))
	return domain.ReadOutput{OK: true, Filename: input.Filename, Path: target, Markdown: string(data), Bytes: int64(len(data)), SHA256: sum}, nil
}
