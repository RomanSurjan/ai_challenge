package main

import (
	"fmt"
	"io"
	"strings"
)

type TokenDemoConfig struct {
	ContextLimit int
	Pricing      TokenPricing
	Calibration  TokenCalibration
}

type demoTurn struct {
	User   string
	Answer string
}

func runTokenDemo(w io.Writer, cfg TokenDemoConfig) error {
	limit := cfg.ContextLimit
	if limit <= 0 {
		limit = defaultContextLimit
	}

	scenarios := []struct {
		Name    string
		Limit   int
		History []chatMessage
		Turns   []demoTurn
	}{
		{
			Name:  "short",
			Limit: limit,
			Turns: []demoTurn{
				{User: "Привет. Что такое токены?", Answer: "Токены - это маленькие части текста, которые считает модель."},
				{User: "Объясни еще короче.", Answer: "Токены - расчетные кусочки текста."},
			},
		},
		{
			Name:  "long",
			Limit: limit,
			Turns: []demoTurn{
				{User: "Запомни: проект учебный, день восьмой.", Answer: "Запомнил учебный контекст про день восьмой."},
				{User: "Добавь деталь: мы считаем историю.", Answer: "История будет добавляться к каждому следующему запросу."},
				{User: "Теперь оцени следующий ход с учетом памяти.", Answer: "Следующий prompt станет больше из-за прошлых сообщений."},
				{User: "Почему стоимость растет?", Answer: "Каждый новый запрос повторно отправляет накопленную историю."},
				{User: "Сделай краткий вывод.", Answer: "Чем длиннее диалог, тем дороже следующий ход."},
			},
		},
		{
			Name:  "overflow",
			Limit: minPositive(limit, 120),
			History: []chatMessage{
				{Role: "user", Content: strings.Repeat("очень длинная история ", 90)},
				{Role: "assistant", Content: strings.Repeat("подробный ответ агента ", 90)},
			},
			Turns: []demoTurn{
				{User: "Продолжи диалог.", Answer: "Этот ответ не будет создан, потому что input уже больше лимита."},
			},
		},
	}

	for i, scenario := range scenarios {
		if i > 0 {
			fmt.Fprintln(w)
		}
		runTokenDemoScenario(w, scenario.Name, scenario.Limit, scenario.History, scenario.Turns, cfg.Pricing, cfg.Calibration)
	}
	return nil
}

func runTokenDemoScenario(w io.Writer, name string, limit int, seedHistory []chatMessage, turns []demoTurn, pricing TokenPricing, calibration TokenCalibration) {
	fmt.Fprintf(w, "Scenario: %s\n", name)
	fmt.Fprintln(w, "turn input output overall status")

	history := cloneMessages(seedHistory)
	for i, turn := range turns {
		report := EstimatePromptTokenReportWithCalibration(defaultSystem, history, turn.User, limit, pricing, calibration)
		status := "ok"
		if report.OverflowTokens > 0 {
			status = "context limit exceeded"
		} else {
			report = AddAnswerTokensWithCalibration(report, turn.Answer, pricing, calibration)
			history = append(history,
				chatMessage{Role: "user", Content: turn.User},
				chatMessage{Role: "assistant", Content: turn.Answer},
			)
		}
		fmt.Fprintf(
			w,
			"%d    %d     %d      %d       %s\n",
			i+1,
			report.PromptTokens,
			report.AnswerTokens,
			report.TotalTokens,
			status,
		)
		if report.OverflowTokens > 0 {
			fmt.Fprintf(w, "status: context limit exceeded by %d tokens\n", report.OverflowTokens)
			break
		}
	}
}

func minPositive(a, b int) int {
	if a <= 0 {
		return b
	}
	if b <= 0 || a < b {
		return a
	}
	return b
}
