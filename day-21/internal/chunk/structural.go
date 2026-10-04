package chunk

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"unicode/utf8"

	"ai-challenge/day-21/internal/document"
)

type sectionRange struct {
	section string
	start   int
	end     int
}

var markdownHeading = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)

func structuralDocument(doc document.Document, config Config) ([]Chunk, error) {
	var sections []sectionRange
	var err error
	switch doc.Type {
	case "markdown":
		sections = markdownSections(doc.Text)
	case "go":
		sections, err = goSections(doc.Text)
	case "text":
		sections = textSections(doc.Text)
	case "pdf":
		sections = pdfSections(doc.Text)
	default:
		return nil, fmt.Errorf("unsupported document type %q", doc.Type)
	}
	if err != nil {
		return nil, err
	}
	var result []Chunk
	for _, section := range sections {
		result = append(result, splitRange(doc, Structural, section.section, section.start, section.end, config)...)
	}
	return result, nil
}

func markdownSections(text string) []sectionRange {
	type headingAt struct {
		start int
		level int
		name  string
	}
	var headings []headingAt
	offset := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		withoutNewline := strings.TrimSuffix(line, "\n")
		if match := markdownHeading.FindStringSubmatch(strings.TrimSuffix(withoutNewline, "\r")); match != nil {
			headings = append(headings, headingAt{start: offset, level: len(match[1]), name: strings.TrimSpace(match[2])})
		}
		offset += utf8.RuneCountInString(line)
	}
	total := utf8.RuneCountInString(text)
	if len(headings) == 0 {
		return []sectionRange{{section: "Document", start: 0, end: total}}
	}
	var result []sectionRange
	if headings[0].start > 0 {
		result = append(result, sectionRange{section: "Preamble", start: 0, end: headings[0].start})
	}
	path := make([]string, 6)
	for i, heading := range headings {
		path[heading.level-1] = heading.name
		for level := heading.level; level < len(path); level++ {
			path[level] = ""
		}
		var names []string
		for _, name := range path[:heading.level] {
			if name != "" {
				names = append(names, name)
			}
		}
		end := total
		if i+1 < len(headings) {
			end = headings[i+1].start
		}
		result = append(result, sectionRange{section: strings.Join(names, " > "), start: heading.start, end: end})
	}
	return nonEmptySections(text, result)
}

func textSections(text string) []sectionRange {
	runes := []rune(text)
	var result []sectionRange
	start := -1
	paragraph := 0
	lineStart := 0
	for lineStart <= len(runes) {
		lineEnd := lineStart
		for lineEnd < len(runes) && runes[lineEnd] != '\n' {
			lineEnd++
		}
		blank := strings.TrimSpace(string(runes[lineStart:lineEnd])) == ""
		if !blank && start < 0 {
			start = lineStart
		}
		if blank && start >= 0 {
			paragraph++
			result = append(result, sectionRange{section: fmt.Sprintf("Paragraph %d", paragraph), start: start, end: lineStart})
			start = -1
		}
		if lineEnd == len(runes) {
			break
		}
		lineStart = lineEnd + 1
	}
	if start >= 0 {
		paragraph++
		result = append(result, sectionRange{section: fmt.Sprintf("Paragraph %d", paragraph), start: start, end: len(runes)})
	}
	return nonEmptySections(text, result)
}

func pdfSections(text string) []sectionRange {
	runes := []rune(text)
	var result []sectionRange
	start := 0
	page := 1
	for i, r := range runes {
		if r != '\f' {
			continue
		}
		result = append(result, sectionRange{section: fmt.Sprintf("Page %d", page), start: start, end: i})
		start = i + 1
		page++
	}
	if start < len(runes) {
		result = append(result, sectionRange{section: fmt.Sprintf("Page %d", page), start: start, end: len(runes)})
	}
	return nonEmptySections(text, result)
}

func goSections(text string) ([]sectionRange, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "document.go", text, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse Go source: %w", err)
	}
	type declaration struct {
		start   int
		section string
	}
	var declarations []declaration
	for _, decl := range file.Decls {
		section, ok := declarationName(decl)
		if !ok {
			continue
		}
		pos := decl.Pos()
		switch value := decl.(type) {
		case *ast.FuncDecl:
			if value.Doc != nil {
				pos = value.Doc.Pos()
			}
		case *ast.GenDecl:
			if value.Doc != nil {
				pos = value.Doc.Pos()
			}
		}
		byteOffset := fset.Position(pos).Offset
		declarations = append(declarations, declaration{start: utf8.RuneCountInString(text[:byteOffset]), section: section})
	}
	total := utf8.RuneCountInString(text)
	packageName := "package " + file.Name.Name
	if len(declarations) == 0 {
		return []sectionRange{{section: packageName, start: 0, end: total}}, nil
	}
	result := []sectionRange{{section: packageName, start: 0, end: declarations[0].start}}
	for i, decl := range declarations {
		end := total
		if i+1 < len(declarations) {
			end = declarations[i+1].start
		}
		result = append(result, sectionRange{section: decl.section, start: decl.start, end: end})
	}
	return nonEmptySections(text, result), nil
}

func declarationName(decl ast.Decl) (string, bool) {
	switch value := decl.(type) {
	case *ast.FuncDecl:
		if value.Recv == nil {
			return "func " + value.Name.Name, true
		}
		receiver := "receiver"
		if len(value.Recv.List) > 0 {
			receiver = exprName(value.Recv.List[0].Type)
		}
		return fmt.Sprintf("method (%s).%s", receiver, value.Name.Name), true
	case *ast.GenDecl:
		if value.Tok != token.TYPE && value.Tok != token.CONST && value.Tok != token.VAR {
			return "", false
		}
		kind := strings.ToLower(value.Tok.String())
		var names []string
		for _, spec := range value.Specs {
			switch item := spec.(type) {
			case *ast.TypeSpec:
				names = append(names, item.Name.Name)
			case *ast.ValueSpec:
				for _, name := range item.Names {
					names = append(names, name.Name)
				}
			}
		}
		if len(names) == 0 {
			names = []string{"group"}
		}
		return kind + " " + strings.Join(names, ", "), true
	default:
		return "", false
	}
}

func exprName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return "*" + exprName(value.X)
	case *ast.IndexExpr:
		return exprName(value.X)
	case *ast.IndexListExpr:
		return exprName(value.X)
	default:
		return "receiver"
	}
}

func nonEmptySections(text string, sections []sectionRange) []sectionRange {
	runes := []rune(text)
	result := sections[:0]
	for _, section := range sections {
		start, end := trimRuneRange(runes, section.start, section.end)
		if start < end {
			section.start, section.end = start, end
			result = append(result, section)
		}
	}
	return result
}
