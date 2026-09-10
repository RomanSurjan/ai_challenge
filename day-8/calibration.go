package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
)

type tokenCalibrationScenario struct {
	Name     string
	Messages []chatMessage
}

type tokenCalibrationResult struct {
	Scenario        string
	LocalPrompt     int
	APIPrompt       int
	PromptDiff      int
	PromptDiffPct   float64
	LocalAnswer     int
	APIAnswer       int
	TotalAPI        int
	FinishReason    string
	ResponsePreview string
}

type tokenCalibrationSummary struct {
	PromptMultiplier           float64
	CompletionMultiplier       float64
	MeanPromptDeviationPercent float64
	MaxPromptDeviationPercent  float64
	MeanAnswerDeviationPercent float64
	MaxAnswerDeviationPercent  float64
}

func runTokenCalibration(ctx context.Context, w io.Writer, cfg AgentConfig, client *http.Client) error {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return errors.New("переменная окружения DEEPSEEK_API_KEY не задана")
	}

	agent := NewAgent(cfg, client)
	scenarios := buildTokenCalibrationScenarios(strings.TrimSpace(cfg.System))
	results := make([]tokenCalibrationResult, 0, len(scenarios))

	for _, scenario := range scenarios {
		result, err := runTokenCalibrationScenario(ctx, agent, scenario)
		if err != nil {
			return fmt.Errorf("калибровка %s: %w", scenario.Name, err)
		}
		results = append(results, result)
	}

	printTokenCalibrationResults(w, cfg.Model, results)
	return nil
}

func buildTokenCalibrationScenarios(system string) []tokenCalibrationScenario {
	withSystem := func(messages ...chatMessage) []chatMessage {
		if system == "" {
			return messages
		}
		out := make([]chatMessage, 0, len(messages)+1)
		out = append(out, chatMessage{Role: "system", Content: system})
		out = append(out, messages...)
		return out
	}

	longRussian := strings.Repeat("Калибровочный русский текст описывает историю диалога, локальную оценку токенов и осторожную проверку лимита контекста. ", 8)

	return []tokenCalibrationScenario{
		{
			Name: "russian_short",
			Messages: withSystem(chatMessage{
				Role:    "user",
				Content: "Ответь ровно одним словом: готово",
			}),
		},
		{
			Name: "english_short",
			Messages: withSystem(chatMessage{
				Role:    "user",
				Content: "Reply with exactly one word: ready",
			}),
		},
		{
			Name: "punctuation",
			Messages: withSystem(chatMessage{
				Role:    "user",
				Content: "Ответь ровно: да. Проверка: запятые, точки, тире - и скобки (ок)!",
			}),
		},
		{
			Name: "json_code",
			Messages: withSystem(chatMessage{
				Role:    "user",
				Content: "Верни ровно OK для JSON: {\"task\":\"count_tokens\",\"n\":3,\"items\":[\"go\",\"api\"]}",
			}),
		},
		{
			Name: "russian_long",
			Messages: withSystem(chatMessage{
				Role:    "user",
				Content: longRussian + "Ответь ровно: принято.",
			}),
		},
		{
			Name: "multi_message_history",
			Messages: withSystem(
				chatMessage{Role: "user", Content: "Меня зовут Роман."},
				chatMessage{Role: "assistant", Content: "Запомнил: вас зовут Роман."},
				chatMessage{Role: "user", Content: "Мы изучаем токены DeepSeek."},
				chatMessage{Role: "assistant", Content: "Понял, фокус на токенах DeepSeek."},
				chatMessage{Role: "user", Content: "Ответь ровно одним словом: калибровка"},
			),
		},
	}
}

func runTokenCalibrationScenario(ctx context.Context, agent *Agent, scenario tokenCalibrationScenario) (tokenCalibrationResult, error) {
	report := EstimateChatTokenReport(scenario.Messages, 0, agent.cfg.Pricing, DefaultTokenCalibration())
	request := chatRequest{
		Model:       agent.cfg.Model,
		Messages:    scenario.Messages,
		Temperature: agent.cfg.Temperature,
		MaxTokens:   agent.cfg.MaxTokens,
		Stream:      false,
	}
	if !agent.cfg.Thinking {
		request.Thinking = &thinkingConfig{Type: "disabled"}
	}

	decoded, err := agent.complete(ctx, request)
	if err != nil {
		return tokenCalibrationResult{}, err
	}
	if len(decoded.Choices) == 0 {
		return tokenCalibrationResult{}, errors.New("DeepSeek API вернул ответ без choices")
	}
	if decoded.Usage == nil {
		return tokenCalibrationResult{}, errors.New("DeepSeek API не вернул usage")
	}

	answer := strings.TrimSpace(decoded.Choices[0].Message.Content)
	report = AddAnswerTokensWithCalibration(report, answer, agent.cfg.Pricing, DefaultTokenCalibration())
	promptDiff := decoded.Usage.PromptTokens - report.RawPromptTokens
	return tokenCalibrationResult{
		Scenario:        scenario.Name,
		LocalPrompt:     report.RawPromptTokens,
		APIPrompt:       decoded.Usage.PromptTokens,
		PromptDiff:      promptDiff,
		PromptDiffPct:   percentDiff(promptDiff, report.RawPromptTokens),
		LocalAnswer:     report.RawAnswerTokens,
		APIAnswer:       decoded.Usage.CompletionTokens,
		TotalAPI:        decoded.Usage.TotalTokens,
		FinishReason:    decoded.Choices[0].FinishReason,
		ResponsePreview: answer,
	}, nil
}

func printTokenCalibrationResults(w io.Writer, model string, results []tokenCalibrationResult) {
	fmt.Fprintf(w, "Token calibration for %s\n", model)
	fmt.Fprintln(w, "empirical calibration for current model/config; API usage is available only after responses")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "scenario local_input api_input diff diff_% local_output api_output api_overall")
	for _, result := range results {
		fmt.Fprintf(
			w,
			"%-22s %12d %10d %+5d %+6.1f%% %12d %10d %9d\n",
			result.Scenario,
			result.LocalPrompt,
			result.APIPrompt,
			result.PromptDiff,
			result.PromptDiffPct,
			result.LocalAnswer,
			result.APIAnswer,
			result.TotalAPI,
		)
	}

	summary := summarizeTokenCalibration(results)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Suggested empirical multipliers:")
	fmt.Fprintf(w, "  prompt_multiplier:     %.4f\n", summary.PromptMultiplier)
	fmt.Fprintf(w, "  completion_multiplier: %.4f\n", summary.CompletionMultiplier)
	fmt.Fprintf(w, "  mean prompt deviation: %.1f%%\n", summary.MeanPromptDeviationPercent)
	fmt.Fprintf(w, "  max prompt deviation:  %.1f%%\n", summary.MaxPromptDeviationPercent)
	fmt.Fprintf(w, "  mean answer deviation: %.1f%%\n", summary.MeanAnswerDeviationPercent)
	fmt.Fprintf(w, "  max answer deviation:  %.1f%%\n", summary.MaxAnswerDeviationPercent)
}

func summarizeTokenCalibration(results []tokenCalibrationResult) tokenCalibrationSummary {
	var localPromptTotal, apiPromptTotal int
	var localAnswerTotal, apiAnswerTotal int
	var promptDeviationTotal, answerDeviationTotal float64
	var maxPromptDeviation, maxAnswerDeviation float64
	var promptDeviationCount, answerDeviationCount int

	for _, result := range results {
		localPromptTotal += result.LocalPrompt
		apiPromptTotal += result.APIPrompt
		localAnswerTotal += result.LocalAnswer
		apiAnswerTotal += result.APIAnswer

		if result.LocalPrompt > 0 {
			deviation := math.Abs(percentDiff(result.PromptDiff, result.LocalPrompt))
			promptDeviationTotal += deviation
			promptDeviationCount++
			if deviation > maxPromptDeviation {
				maxPromptDeviation = deviation
			}
		}
		if result.LocalAnswer > 0 {
			deviation := math.Abs(percentDiff(result.APIAnswer-result.LocalAnswer, result.LocalAnswer))
			answerDeviationTotal += deviation
			answerDeviationCount++
			if deviation > maxAnswerDeviation {
				maxAnswerDeviation = deviation
			}
		}
	}

	summary := tokenCalibrationSummary{
		PromptMultiplier:          ratioOrOne(apiPromptTotal, localPromptTotal),
		CompletionMultiplier:      ratioOrOne(apiAnswerTotal, localAnswerTotal),
		MaxPromptDeviationPercent: maxPromptDeviation,
		MaxAnswerDeviationPercent: maxAnswerDeviation,
	}
	if promptDeviationCount > 0 {
		summary.MeanPromptDeviationPercent = promptDeviationTotal / float64(promptDeviationCount)
	}
	if answerDeviationCount > 0 {
		summary.MeanAnswerDeviationPercent = answerDeviationTotal / float64(answerDeviationCount)
	}
	return summary
}

func percentDiff(diff, base int) float64 {
	if base == 0 {
		return 0
	}
	return float64(diff) * 100 / float64(base)
}

func ratioOrOne(numerator, denominator int) float64 {
	if denominator == 0 {
		return 1
	}
	return float64(numerator) / float64(denominator)
}
