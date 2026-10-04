package document

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

func extractPDFText(path string) (string, int, error) {
	binary, err := exec.LookPath("pdftotext")
	if err != nil {
		return "", 0, fmt.Errorf("pdftotext is required for PDF input; install Poppler (macOS: brew install poppler): %w", err)
	}
	cmd := exec.Command(binary, "-layout", "-enc", "UTF-8", path, "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", 0, fmt.Errorf("pdftotext failed: %s", message)
	}
	text := string(out)
	pages := 0
	for _, page := range strings.Split(text, "\f") {
		if strings.TrimSpace(page) != "" {
			pages++
		}
	}
	if pages == 0 && strings.TrimSpace(text) != "" {
		pages = 1
	}
	return text, pages, nil
}
