package main

import (
	"fmt"
	"math"
	"unicode"
)

const (
	chatMessageOverheadTokens = 4
	chatReplyPrimerTokens     = 3
)

type TokenPricing struct {
	InputPer1M  float64 `json:"input_per_1m"`
	OutputPer1M float64 `json:"output_per_1m"`
}

type TokenCalibration struct {
	PromptMultiplier     float64 `json:"prompt_multiplier"`
	CompletionMultiplier float64 `json:"completion_multiplier"`
}

type TokenReport struct {
	RawCurrentRequestTokens int     `json:"raw_current_request_tokens"`
	RawHistoryTokens        int     `json:"raw_history_tokens"`
	RawSystemTokens         int     `json:"raw_system_tokens"`
	RawPromptTokens         int     `json:"raw_prompt_tokens"`
	RawAnswerTokens         int     `json:"raw_answer_tokens"`
	RawTotalTokens          int     `json:"raw_total_tokens"`
	EstimatedPromptTokens   int     `json:"estimated_prompt_tokens"`
	EstimatedAnswerTokens   int     `json:"estimated_answer_tokens"`
	EstimatedTotalTokens    int     `json:"estimated_total_tokens"`
	CurrentRequestTokens    int     `json:"current_request_tokens"`
	HistoryTokens           int     `json:"history_tokens"`
	SystemTokens            int     `json:"system_tokens"`
	PromptTokens            int     `json:"prompt_tokens"`
	AnswerTokens            int     `json:"answer_tokens"`
	TotalTokens             int     `json:"total_tokens"`
	ContextLimit            int     `json:"context_limit"`
	RemainingTokens         int     `json:"remaining_tokens"`
	OverflowTokens          int     `json:"overflow_tokens"`
	PromptMultiplier        float64 `json:"prompt_multiplier"`
	CompletionMultiplier    float64 `json:"completion_multiplier"`
	InputCostUSD            float64 `json:"input_cost_usd"`
	OutputCostUSD           float64 `json:"output_cost_usd"`
	TotalCostUSD            float64 `json:"total_cost_usd"`
}

type ContextLimitError struct {
	Report            TokenReport
	CompressionReport *CompressionReport
}

func (e *ContextLimitError) Error() string {
	return fmt.Sprintf(
		"лимит контекста превышен: оценка input=%d токенов, лимит=%d, превышение=%d; system=%d, history=%d, current=%d",
		e.Report.PromptTokens,
		e.Report.ContextLimit,
		e.Report.OverflowTokens,
		e.Report.SystemTokens,
		e.Report.HistoryTokens,
		e.Report.CurrentRequestTokens,
	)
}

func EstimateTextTokens(text string) int {
	runes := []rune(text)
	tokens := 0

	for i := 0; i < len(runes); {
		r := runes[i]
		if unicode.IsSpace(r) {
			i++
			continue
		}

		if isCJK(r) {
			tokens++
			i++
			continue
		}

		if unicode.IsLetter(r) {
			start := i
			for i < len(runes) && unicode.IsLetter(runes[i]) && !isCJK(runes[i]) {
				i++
			}
			tokens += fragmentTokens(i - start)
			continue
		}

		if unicode.IsDigit(r) {
			start := i
			for i < len(runes) && unicode.IsDigit(runes[i]) {
				i++
			}
			tokens += fragmentTokens(i - start)
			continue
		}

		tokens++
		i++
	}

	return tokens
}

func EstimateMessageTokens(message chatMessage) int {
	return chatMessageOverheadTokens + EstimateTextTokens(message.Role) + EstimateTextTokens(message.Content)
}

func EstimateMessagesTokens(messages []chatMessage) int {
	total := 0
	for _, message := range messages {
		total += EstimateMessageTokens(message)
	}
	if len(messages) > 0 {
		total += chatReplyPrimerTokens
	}
	return total
}

func EstimatePromptTokenReport(system string, history []chatMessage, userPrompt string, contextLimit int, pricing TokenPricing) TokenReport {
	return EstimatePromptTokenReportWithCalibration(system, history, userPrompt, contextLimit, pricing, DefaultTokenCalibration())
}

func EstimatePromptTokenReportWithCalibration(system string, history []chatMessage, userPrompt string, contextLimit int, pricing TokenPricing, calibration TokenCalibration) TokenReport {
	systemTokens := 0
	if system != "" {
		systemTokens = EstimateMessageTokens(chatMessage{Role: "system", Content: system})
	}
	historyTokens := 0
	for _, message := range history {
		historyTokens += EstimateMessageTokens(message)
	}
	currentTokens := EstimateMessageTokens(chatMessage{Role: "user", Content: userPrompt})
	promptTokens := systemTokens + historyTokens + currentTokens + chatReplyPrimerTokens

	return buildPromptTokenReport(systemTokens, historyTokens, currentTokens, promptTokens, contextLimit, pricing, calibration)
}

func EstimateChatTokenReport(messages []chatMessage, contextLimit int, pricing TokenPricing, calibration TokenCalibration) TokenReport {
	systemTokens := 0
	historyTokens := 0
	currentTokens := 0
	lastUserIndex := -1
	for i, message := range messages {
		if message.Role == "user" {
			lastUserIndex = i
		}
	}
	for i, message := range messages {
		tokens := EstimateMessageTokens(message)
		switch {
		case message.Role == "system":
			systemTokens += tokens
		case i == lastUserIndex:
			currentTokens += tokens
		default:
			historyTokens += tokens
		}
	}
	promptTokens := systemTokens + historyTokens + currentTokens
	if len(messages) > 0 {
		promptTokens += chatReplyPrimerTokens
	}

	return buildPromptTokenReport(systemTokens, historyTokens, currentTokens, promptTokens, contextLimit, pricing, calibration)
}

func buildPromptTokenReport(systemTokens, historyTokens, currentTokens, promptTokens, contextLimit int, pricing TokenPricing, calibration TokenCalibration) TokenReport {
	calibration = normalizeTokenCalibration(calibration)
	estimatedSystemTokens := applyTokenMultiplier(systemTokens, calibration.PromptMultiplier)
	estimatedHistoryTokens := applyTokenMultiplier(historyTokens, calibration.PromptMultiplier)
	estimatedCurrentTokens := applyTokenMultiplier(currentTokens, calibration.PromptMultiplier)
	estimatedPromptTokens := applyTokenMultiplier(promptTokens, calibration.PromptMultiplier)

	report := TokenReport{
		RawCurrentRequestTokens: currentTokens,
		RawHistoryTokens:        historyTokens,
		RawSystemTokens:         systemTokens,
		RawPromptTokens:         promptTokens,
		RawTotalTokens:          promptTokens,
		EstimatedPromptTokens:   estimatedPromptTokens,
		EstimatedTotalTokens:    estimatedPromptTokens,
		CurrentRequestTokens:    estimatedCurrentTokens,
		HistoryTokens:           estimatedHistoryTokens,
		SystemTokens:            estimatedSystemTokens,
		PromptTokens:            estimatedPromptTokens,
		TotalTokens:             estimatedPromptTokens,
		ContextLimit:            contextLimit,
		RemainingTokens:         contextLimit - estimatedPromptTokens,
		PromptMultiplier:        calibration.PromptMultiplier,
		CompletionMultiplier:    calibration.CompletionMultiplier,
	}
	if contextLimit <= 0 {
		report.RemainingTokens = 0
	} else if report.RemainingTokens < 0 {
		report.OverflowTokens = -report.RemainingTokens
	}
	return applyTokenPricing(report, pricing)
}

func AddAnswerTokens(report TokenReport, answer string, pricing TokenPricing) TokenReport {
	return AddAnswerTokensWithCalibration(report, answer, pricing, TokenCalibration{
		PromptMultiplier:     report.PromptMultiplier,
		CompletionMultiplier: report.CompletionMultiplier,
	})
}

func AddAnswerTokensWithCalibration(report TokenReport, answer string, pricing TokenPricing, calibration TokenCalibration) TokenReport {
	calibration = normalizeTokenCalibration(calibration)
	rawAnswerTokens := EstimateTextTokens(answer)
	estimatedAnswerTokens := applyTokenMultiplier(rawAnswerTokens, calibration.CompletionMultiplier)

	report.RawAnswerTokens = rawAnswerTokens
	report.RawTotalTokens = report.RawPromptTokens + rawAnswerTokens
	report.EstimatedAnswerTokens = estimatedAnswerTokens
	report.EstimatedTotalTokens = report.EstimatedPromptTokens + estimatedAnswerTokens
	report.AnswerTokens = estimatedAnswerTokens
	report.TotalTokens = report.PromptTokens + report.AnswerTokens
	report.PromptMultiplier = calibration.PromptMultiplier
	report.CompletionMultiplier = calibration.CompletionMultiplier
	return applyTokenPricing(report, pricing)
}

func applyTokenPricing(report TokenReport, pricing TokenPricing) TokenReport {
	report.InputCostUSD = float64(report.PromptTokens) * pricing.InputPer1M / 1_000_000
	report.OutputCostUSD = float64(report.AnswerTokens) * pricing.OutputPer1M / 1_000_000
	report.TotalCostUSD = report.InputCostUSD + report.OutputCostUSD
	return report
}

func DefaultTokenCalibration() TokenCalibration {
	return TokenCalibration{
		PromptMultiplier:     1,
		CompletionMultiplier: 1,
	}
}

func normalizeTokenCalibration(calibration TokenCalibration) TokenCalibration {
	if calibration.PromptMultiplier <= 0 || math.IsNaN(calibration.PromptMultiplier) || math.IsInf(calibration.PromptMultiplier, 0) {
		calibration.PromptMultiplier = 1
	}
	if calibration.CompletionMultiplier <= 0 || math.IsNaN(calibration.CompletionMultiplier) || math.IsInf(calibration.CompletionMultiplier, 0) {
		calibration.CompletionMultiplier = 1
	}
	return calibration
}

func applyTokenMultiplier(tokens int, multiplier float64) int {
	if tokens <= 0 {
		return 0
	}
	if multiplier <= 0 || math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
		multiplier = 1
	}
	return int(math.Ceil(float64(tokens) * multiplier))
}

func fragmentTokens(length int) int {
	if length <= 0 {
		return 0
	}
	return int(math.Ceil(float64(length) / 6))
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x3040 && r <= 0x30FF) ||
		(r >= 0xAC00 && r <= 0xD7AF)
}
