package main

import (
	"context"
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

type CompressionDemoConfig struct {
	Compression  CompressionConfig
	ContextLimit int
	Pricing      TokenPricing
	Calibration  TokenCalibration
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
				{User: "Запомни: проект учебный, день девятый.", Answer: "Запомнил учебный контекст про день девятый."},
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

func runCompressionDemo(w io.Writer, cfg CompressionDemoConfig) error {
	compression := normalizeCompressionConfig(cfg.Compression)
	compression.Enabled = true
	question := "Что важно помнить перед следующим шагом проекта?"
	store := &demoSummaryStore{}

	fmt.Fprintln(w, "Scenario: compression")
	fmt.Fprintln(w, "Formula:")
	fmt.Fprintln(w, "  targetCovered = len(history) - KeepLastMessages")
	fmt.Fprintln(w, "  newBlock = history[summary.CoveredMessages : targetCovered]")
	fmt.Fprintln(w, "  summary updates only when len(newBlock) >= ChunkSize")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Context-limit note:")
	fmt.Fprintln(w, "  context-limit does not trigger compression; it is checked after the actual prompt is chosen.")
	fmt.Fprintln(w)

	stages := []struct {
		Name string
		Size int
	}{
		{Name: "small history, no summary yet", Size: compression.KeepLastMessages + compression.ChunkSize/2},
		{Name: "chunk accumulated, summary is created", Size: compression.KeepLastMessages + compression.ChunkSize},
		{Name: "history grows, summary saves input", Size: compression.KeepLastMessages + compression.ChunkSize + 4},
		{Name: "next chunk is still waiting", Size: compression.KeepLastMessages + compression.ChunkSize + compression.ChunkSize - 2},
	}

	for i, stage := range stages {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "Stage: %s\n", stage.Name)
		history := buildCompressionDemoHistory(stage.Size)
		summary, err := store.Load(context.Background())
		if err != nil {
			return err
		}
		summary, updateReport, err := maybeUpdateSummary(context.Background(), history, summary, compression, store, LocalSummarySummarizer{MaxItems: 8, MaxChars: 80})
		if err != nil {
			return err
		}
		report := compressionDemoReport(history, question, summary, updateReport, compression, cfg)
		printCompressionReport(w, &report)
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Compact table:")
	fmt.Fprintln(w, "messages full_prompt actual_prompt saved saved_% covers pending update_status wait updated reason")
	store = &demoSummaryStore{}
	for _, size := range []int{
		compression.KeepLastMessages + compression.ChunkSize/2,
		compression.KeepLastMessages + compression.ChunkSize,
		compression.KeepLastMessages + compression.ChunkSize + 4,
		compression.KeepLastMessages + compression.ChunkSize + compression.ChunkSize - 2,
	} {
		history := buildCompressionDemoHistory(size)
		summary, err := store.Load(context.Background())
		if err != nil {
			return err
		}
		summary, updateReport, err := maybeUpdateSummary(context.Background(), history, summary, compression, store, LocalSummarySummarizer{MaxItems: 8, MaxChars: 80})
		if err != nil {
			return err
		}
		report := compressionDemoReport(history, question, summary, updateReport, compression, cfg)

		fmt.Fprintf(
			w,
			"%d       %d           %d            %d    %.1f%%   %d      %d       %s       %d    %s     %s\n",
			size,
			report.FullPromptInputTokens,
			report.ActualPromptInputTokens,
			report.EstimatedSavedInputTokens,
			report.EstimatedSavedPercent,
			report.SummaryCoversMessages,
			report.PendingOldMessages,
			report.SummaryUpdateStatus,
			report.MessagesUntilSummaryUpdate,
			yesNo(report.SummaryUpdatedNow),
			report.Reason,
		)
	}

	history := buildCompressionDemoHistory(compression.KeepLastMessages + compression.ChunkSize + 4)
	summary, err := store.Load(context.Background())
	if err != nil {
		return err
	}
	fullContext := buildChatMessages(defaultSystem, history, question)
	compressedContext := buildCompressedContext(defaultSystem, history, question, summary, compression, cfg.Pricing, cfg.Calibration)

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Quality comparison: what the prompt keeps")
	fmt.Fprintln(w, "Without compression:")
	printDemoContext(w, fullContext, 8)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "With compression:")
	printDemoContext(w, compressedContext.Messages, 8)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Summary should preserve: имя пользователя, цель проекта, ключевые решения, важные ограничения.")

	return nil
}

func compressionDemoReport(history []chatMessage, question string, summary ConversationSummary, updateReport SummaryUpdateReport, compression CompressionConfig, cfg CompressionDemoConfig) CompressionReport {
	fullReport := EstimateChatTokenReport(buildChatMessages(defaultSystem, history, question), cfg.ContextLimit, cfg.Pricing, cfg.Calibration)
	compressedContext := buildCompressedContext(defaultSystem, history, question, summary, compression, cfg.Pricing, cfg.Calibration)
	actualReport := EstimateChatTokenReport(compressedContext.Messages, cfg.ContextLimit, cfg.Pricing, cfg.Calibration)
	return buildCompressionReport(true, fullReport, actualReport, summary, compression, len(history), updateReport)
}

type demoSummaryStore struct {
	summary ConversationSummary
}

func (s *demoSummaryStore) Load(ctx context.Context) (ConversationSummary, error) {
	if err := ctx.Err(); err != nil {
		return ConversationSummary{}, err
	}
	return s.summary, nil
}

func (s *demoSummaryStore) Save(ctx context.Context, summary ConversationSummary) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.summary = summary
	return nil
}

func buildCompressionDemoHistory(messages int) []chatMessage {
	facts := []string{
		"Меня зовут Роман, я прохожу AI challenge.",
		"Цель дня 9 - научиться управлять длинной историей диалога.",
		"Решение: полная история хранится отдельно и не удаляется.",
		"Ограничение: тесты не должны зависеть от реального API.",
		"Важно сохранить последние сообщения дословно.",
		"Summary должно заменить старую часть prompt.",
		"Нужно сравнить токены до и после компрессии.",
		"Web API должен вернуть отчет по компрессии.",
		"CLI должен уметь запускать demo без ключа API.",
		"Summary обновляется блоками после успешного ответа.",
		"История нужна для оценки качества без сжатия.",
		"Сжатый prompt должен сохранить имя, цель, решения и ограничения.",
		"Пользователь хочет учебное и проверяемое решение.",
		"Контекстный лимит проверяется перед запросом к модели.",
		"Последние ходы показывают текущий вопрос и свежие уточнения.",
	}

	history := make([]chatMessage, 0, messages)
	for i := 0; i < messages; i++ {
		role := "user"
		prefix := "Факт"
		if i%2 == 1 {
			role = "assistant"
			prefix = "Подтверждение"
		}
		history = append(history, chatMessage{
			Role:    role,
			Content: fmt.Sprintf("%s %02d: %s", prefix, i+1, facts[i%len(facts)]),
		})
	}
	return history
}

func printDemoContext(w io.Writer, messages []chatMessage, maxMessages int) {
	if len(messages) <= maxMessages {
		for _, message := range messages {
			fmt.Fprintf(w, "- %s: %s\n", message.Role, oneLine(message.Content, 120))
		}
		return
	}
	head := maxMessages / 2
	tail := maxMessages - head
	for _, message := range messages[:head] {
		fmt.Fprintf(w, "- %s: %s\n", message.Role, oneLine(message.Content, 120))
	}
	fmt.Fprintf(w, "- ... omitted messages: %d\n", len(messages)-maxMessages)
	for _, message := range messages[len(messages)-tail:] {
		fmt.Fprintf(w, "- %s: %s\n", message.Role, oneLine(message.Content, 120))
	}
}

func oneLine(text string, maxRunes int) string {
	value := strings.Join(strings.Fields(text), " ")
	if maxRunes <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "..."
}
