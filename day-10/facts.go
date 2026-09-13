package main

import (
	"strings"
	"unicode"
)

func ExtractFacts(message string) map[string]string {
	extracted := make(map[string]string)
	lines := strings.Split(strings.ReplaceAll(message, "\r\n", "\n"), "\n")
	for _, line := range lines {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		key = normalizeFactKey(key)
		value = strings.TrimSpace(value)
		if !isExplicitFactKey(key) || !isExplicitFactValue(value) {
			continue
		}
		extracted[key] = value
	}
	if len(extracted) == 0 {
		return nil
	}
	return extracted
}

func MergeFacts(current, extracted map[string]string) map[string]string {
	merged := cloneFacts(current)
	if len(extracted) == 0 {
		return merged
	}
	if merged == nil {
		merged = make(map[string]string, len(extracted))
	}
	for key, value := range extracted {
		merged[normalizeFactKey(key)] = strings.TrimSpace(value)
	}
	return merged
}

func RecoverFactsFromMessages(current map[string]string, messages []chatMessage) map[string]string {
	recovered := FactsFromMessages(messages)
	if len(current) == 0 {
		return recovered
	}
	if len(recovered) == 0 {
		return cloneFacts(current)
	}
	return MergeFacts(recovered, current)
}

func FactsFromMessages(messages []chatMessage) map[string]string {
	var facts map[string]string
	for _, message := range messages {
		if message.Role != "user" {
			continue
		}
		facts = MergeFacts(facts, ExtractFacts(message.Content))
	}
	return facts
}

func normalizeFactKey(key string) string {
	return strings.ToLower(strings.Join(strings.Fields(key), " "))
}

func isExplicitFactKey(key string) bool {
	if key == "" || len([]rune(key)) > 80 {
		return false
	}

	hasLetter := false
	for _, r := range key {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r), unicode.IsSpace(r), r == '-', r == '_':
		default:
			return false
		}
	}
	return hasLetter
}

func isExplicitFactValue(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	return !strings.HasPrefix(strings.TrimSpace(value), "//")
}
