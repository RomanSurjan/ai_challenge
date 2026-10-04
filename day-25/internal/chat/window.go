package chat

import "unicode/utf8"

// BoundedHistory returns complete messages only, newest first selected and then
// restored to chronological order. It never splits a UTF-8 sequence.
func BoundedHistory(messages []Message, limits Limits) []Message {
	maxMessages := limits.MaxRecentTurns * 2
	if maxMessages <= 0 || limits.MaxHistoryRunes <= 0 {
		return []Message{}
	}
	start := len(messages)
	runes := 0
	for start > 0 && len(messages)-start < maxMessages {
		n := utf8.RuneCountInString(messages[start-1].Content)
		if runes+n > limits.MaxHistoryRunes {
			break
		}
		runes += n
		start--
	}
	return append([]Message{}, messages[start:]...)
}

func ActiveItems(state TaskState) []MemoryItem {
	items := make([]MemoryItem, 0)
	if state.Goal != nil {
		items = append(items, *state.Goal)
	}
	items = append(items, state.Constraints...)
	items = append(items, state.Terms...)
	items = append(items, state.Decisions...)
	items = append(items, state.Clarifications...)
	items = append(items, state.OpenQuestions...)
	return items
}
