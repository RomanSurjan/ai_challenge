package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	defaultTimeout     = 180 * time.Second
	defaultMaxTokens   = 4096
	defaultTemperature = 0.2
	defaultTaskName    = "model_comparison"
)

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type thinkingConfig struct {
	Type string `json:"type"`
}

type chatRequest struct {
	Model               string          `json:"model"`
	Messages            []chatMessage   `json:"messages"`
	Temperature         float64         `json:"temperature,omitempty"`
	MaxTokens           int             `json:"max_tokens,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Thinking            *thinkingConfig `json:"thinking,omitempty"`
	IncludeReasoning    *bool           `json:"include_reasoning,omitempty"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	Stream              bool            `json:"stream"`
}

type tokenUsage struct {
	PromptTokens          int `json:"prompt_tokens"`
	CompletionTokens      int `json:"completion_tokens"`
	TotalTokens           int `json:"total_tokens"`
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *tokenUsage `json:"usage,omitempty"`
}

type apiError struct {
	Error any `json:"error"`
}

type modelSpec struct {
	Strength        string
	Provider        string
	DisplayName     string
	Model           string
	BaseURL         string
	APIKeyEnv       string
	SelectionReason string
	DeepSeekPricing bool
	PriceKnown      bool
	InputPrice      float64
	OutputPrice     float64
}

type priceBreakdown struct {
	InputCacheHitPrice  float64
	InputCacheMissPrice float64
	OutputPrice         float64
	Window              string
	Assumption          string
}

type config struct {
	Prompt      string
	System      string
	Timeout     time.Duration
	MaxTokens   int
	Temperature float64
	TaskName    string
	OutputPath  string
	OpenReport  bool
}

type modelResult struct {
	Spec         modelSpec
	Answer       string
	Usage        *tokenUsage
	FinishReason string
	Duration     time.Duration
	CostUSD      float64
	CostKnown    bool
	Pricing      priceBreakdown
}

func main() {
	cfg, err := readConfig(os.Args[1:], os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}

	results, err := runExperiment(context.Background(), http.DefaultClient, cfg, defaultModels())
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}

	var report bytes.Buffer
	printReport(&report, cfg, results)

	if cfg.OutputPath == "" {
		fmt.Print(report.String())
		return
	}

	if err := writeReport(cfg.OutputPath, report.Bytes()); err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Отчет сохранен: %s\n", cfg.OutputPath)

	if cfg.OpenReport {
		if err := openFile(cfg.OutputPath); err != nil {
			fmt.Fprintln(os.Stderr, "Ошибка: не удалось открыть отчет:", err)
			os.Exit(1)
		}
	}
}

func readConfig(args []string, stdin io.Reader) (config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return config{}, err
	}

	flags := flag.NewFlagSet("day-5-model-comparison", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	var cfg config
	flags.StringVar(&cfg.Prompt, "prompt", "", "same prompt to send to all models")
	flags.StringVar(&cfg.System, "system", "Отвечай на русском языке. Будь точным, проверяй крайние случаи и не выдумывай ограничения.", "optional system message")
	flags.DurationVar(&cfg.Timeout, "timeout", defaultTimeout, "request timeout")
	flags.IntVar(&cfg.MaxTokens, "max-tokens", defaultMaxTokens, "maximum response tokens per API call")
	flags.Float64Var(&cfg.Temperature, "temperature", defaultTemperature, "sampling temperature")
	flags.StringVar(&cfg.TaskName, "task-name", defaultTaskName, "task name for the default answer_<task_name>.md report")
	flags.StringVar(&cfg.OutputPath, "out", "", "path to save Markdown report")
	flags.BoolVar(&cfg.OpenReport, "open", false, "open Markdown report after saving")

	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if cfg.MaxTokens < 0 {
		return config{}, errors.New("значение -max-tokens не может быть отрицательным")
	}

	if cfg.Prompt == "" && shouldReadStdin(stdin) {
		piped, err := io.ReadAll(stdin)
		if err != nil {
			return config{}, fmt.Errorf("не удалось прочитать stdin: %w", err)
		}
		cfg.Prompt = strings.TrimSpace(string(piped))
	}

	if cfg.Prompt == "" {
		return config{}, errors.New("передайте задачу через -prompt или stdin")
	}

	cfg.TaskName = sanitizeTaskName(cfg.TaskName)
	if cfg.TaskName == "" {
		return config{}, errors.New("значение -task-name должно содержать хотя бы одну букву или цифру")
	}
	if cfg.OpenReport && cfg.OutputPath == "" {
		cfg.OutputPath = defaultReportPath(cfg.TaskName)
	}

	return cfg, nil
}

func defaultModels() []modelSpec {
	return []modelSpec{
		{
			Strength:        "слабая",
			Provider:        "Groq",
			DisplayName:     "GPT OSS 20B",
			Model:           "openai/gpt-oss-20b",
			BaseURL:         "https://api.groq.com/openai/v1",
			APIKeyEnv:       "GROQ_API_KEY",
			PriceKnown:      true,
			InputPrice:      0.075,
			OutputPrice:     0.30,
			SelectionReason: "open-weight модель на 20B параметров, доступная через Groq API",
		},
		{
			Strength:        "средняя",
			Provider:        "DeepSeek",
			DisplayName:     "DeepSeek V4 Flash",
			Model:           "deepseek-v4-flash",
			BaseURL:         "https://api.deepseek.com",
			APIKeyEnv:       "DEEPSEEK_API_KEY",
			DeepSeekPricing: true,
			PriceKnown:      true,
			SelectionReason: "быстрый и дешевый вариант DeepSeek, компромисс между ценой, скоростью и качеством",
		},
		{
			Strength:        "сильная",
			Provider:        "DeepSeek",
			DisplayName:     "DeepSeek V4 Pro",
			Model:           "deepseek-v4-pro",
			BaseURL:         "https://api.deepseek.com",
			APIKeyEnv:       "DEEPSEEK_API_KEY",
			DeepSeekPricing: true,
			PriceKnown:      true,
			SelectionReason: "флагманская Pro-модель DeepSeek, ожидаемо сильнее Flash по сложным задачам",
		},
	}
}

func runExperiment(ctx context.Context, client *http.Client, cfg config, models []modelSpec) ([]modelResult, error) {
	results := make([]modelResult, 0, len(models))
	for _, spec := range models {
		apiKey := strings.TrimSpace(os.Getenv(spec.APIKeyEnv))
		if apiKey == "" {
			return nil, fmt.Errorf("переменная окружения %s не задана", spec.APIKeyEnv)
		}

		answer, usage, finishReason, duration, err := requestCompletion(ctx, client, cfg, spec, apiKey, cfg.Prompt)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", spec.Provider, spec.Model, err)
		}

		pricing := resolvePricing(spec, time.Now().UTC())
		results = append(results, modelResult{
			Spec:         spec,
			Answer:       answer,
			Usage:        usage,
			FinishReason: finishReason,
			Duration:     duration,
			CostUSD:      estimateCost(usage, pricing),
			CostKnown:    spec.DeepSeekPricing || spec.PriceKnown,
			Pricing:      pricing,
		})
	}

	return results, nil
}

func requestCompletion(ctx context.Context, client *http.Client, cfg config, spec modelSpec, apiKey, prompt string) (string, *tokenUsage, string, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	reqBody := chatRequest{
		Model:       spec.Model,
		Messages:    buildMessages(spec, cfg.System, prompt),
		Temperature: cfg.Temperature,
		MaxTokens:   cfg.MaxTokens,
		Stream:      false,
	}
	if spec.Provider == "DeepSeek" {
		reqBody.Thinking = &thinkingConfig{Type: "disabled"}
	}
	if isGroqGPTOSS(spec) {
		includeReasoning := false
		reqBody.IncludeReasoning = &includeReasoning
		reqBody.ReasoningEffort = "low"
		reqBody.MaxCompletionTokens = cfg.MaxTokens
		reqBody.MaxTokens = 0
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", nil, "", 0, fmt.Errorf("не удалось собрать JSON-запрос: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(spec.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", nil, "", 0, fmt.Errorf("не удалось создать HTTP-запрос: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	started := time.Now()
	resp, err := client.Do(req)
	duration := time.Since(started)
	if err != nil {
		return "", nil, "", duration, fmt.Errorf("API недоступен: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, "", duration, fmt.Errorf("не удалось прочитать ответ API: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", nil, "", duration, fmt.Errorf("API вернул HTTP %d: %s", resp.StatusCode, formatAPIError(respBody))
	}

	var decoded chatResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return "", nil, "", duration, fmt.Errorf("не удалось разобрать JSON-ответ: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return "", nil, "", duration, errors.New("API вернул ответ без choices")
	}

	answer := strings.TrimSpace(decoded.Choices[0].Message.Content)
	if answer == "" {
		answer = "_Модель вернула пустой `message.content`._"
	}

	return answer, decoded.Usage, decoded.Choices[0].FinishReason, duration, nil
}

func buildMessages(spec modelSpec, system, prompt string) []chatMessage {
	if strings.TrimSpace(system) == "" {
		return []chatMessage{{Role: "user", Content: prompt}}
	}
	if isGroqGPTOSS(spec) {
		return []chatMessage{{Role: "user", Content: strings.TrimSpace(system) + "\n\n" + prompt}}
	}
	return []chatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: prompt},
	}
}

func isGroqGPTOSS(spec modelSpec) bool {
	return spec.Provider == "Groq" && strings.HasPrefix(spec.Model, "openai/gpt-oss")
}

func resolvePricing(spec modelSpec, nowUTC time.Time) priceBreakdown {
	if !spec.DeepSeekPricing {
		return priceBreakdown{
			InputCacheHitPrice:  spec.InputPrice,
			InputCacheMissPrice: spec.InputPrice,
			OutputPrice:         spec.OutputPrice,
			Window:              "standard",
			Assumption:          "Groq: цена за 1M input/output tokens, если публичный тариф известен",
		}
	}

	peak := isDeepSeekPeak(nowUTC)
	if spec.Model == "deepseek-v4-pro" {
		if peak {
			return priceBreakdown{InputCacheHitPrice: 0.044, InputCacheMissPrice: 1.32, OutputPrice: 3.96, Window: "peak", Assumption: "DeepSeek: cache hit/miss по фактическим usage-полям, иначе input как cache miss"}
		}
		return priceBreakdown{InputCacheHitPrice: 0.022, InputCacheMissPrice: 0.66, OutputPrice: 1.98, Window: "off-peak", Assumption: "DeepSeek: cache hit/miss по фактическим usage-полям, иначе input как cache miss"}
	}

	if peak {
		return priceBreakdown{InputCacheHitPrice: 0.014, InputCacheMissPrice: 0.44, OutputPrice: 1.32, Window: "peak", Assumption: "DeepSeek: cache hit/miss по фактическим usage-полям, иначе input как cache miss"}
	}
	return priceBreakdown{InputCacheHitPrice: 0.007, InputCacheMissPrice: 0.22, OutputPrice: 0.66, Window: "off-peak", Assumption: "DeepSeek: cache hit/miss по фактическим usage-полям, иначе input как cache miss"}
}

func isDeepSeekPeak(nowUTC time.Time) bool {
	weekday := nowUTC.Weekday()
	if weekday == time.Saturday || weekday == time.Sunday {
		return false
	}
	hour := nowUTC.Hour()
	return (hour >= 1 && hour < 4) || (hour >= 6 && hour < 10)
}

func estimateCost(usage *tokenUsage, pricing priceBreakdown) float64 {
	if usage == nil {
		return 0
	}

	cacheHit := usage.PromptCacheHitTokens
	cacheMiss := usage.PromptCacheMissTokens
	if cacheHit == 0 && cacheMiss == 0 {
		cacheMiss = usage.PromptTokens
	}

	return float64(cacheHit)/1_000_000*pricing.InputCacheHitPrice +
		float64(cacheMiss)/1_000_000*pricing.InputCacheMissPrice +
		float64(usage.CompletionTokens)/1_000_000*pricing.OutputPrice
}

func printReport(w io.Writer, cfg config, results []modelResult) {
	fmt.Fprintln(w, "# Day 5: сравнение моделей через API")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Задача")
	fmt.Fprintln(w)
	fmt.Fprintln(w, cfg.Prompt)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Источники и модели")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "- Hugging Face Open LLM Leaderboard использовался как ориентир для подбора моделей.")
	fmt.Fprintln(w, "- DeepSeek API: `https://api-docs.deepseek.com/` и `https://api-docs.deepseek.com/quick_start/pricing`.")
	fmt.Fprintln(w, "- Groq models/pricing: `https://console.groq.com/docs/models`.")
	fmt.Fprintln(w)
	for _, result := range results {
		fmt.Fprintf(w, "- `%s` (%s, `%s`).\n", result.Spec.DisplayName, result.Spec.Provider, result.Spec.Model)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Настройки")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "- temperature: `%.2f`\n", cfg.Temperature)
	fmt.Fprintf(w, "- max_tokens: `%d`\n", cfg.MaxTokens)
	fmt.Fprintf(w, "- task_name: `%s`\n", cfg.TaskName)
	fmt.Fprintln(w, "- DeepSeek thinking: `disabled`, чтобы сравнение было ближе к обычному chat-completion режиму.")
	fmt.Fprintln(w)
	printMetricsTable(w, results)
	fmt.Fprintln(w)

	for _, result := range results {
		fmt.Fprintf(w, "## Ответ: %s\n\n", result.Spec.DisplayName)
		fmt.Fprintf(w, "_Provider: %s; model: `%s`; time=%s; %s; estimated_cost=%s; finish_reason=%s_\n\n", result.Spec.Provider, result.Spec.Model, formatDuration(result.Duration), formatUsageInline(result.Usage), formatCost(result.CostUSD, result.CostKnown), result.FinishReason)
		fmt.Fprintln(w, result.Answer)
		fmt.Fprintln(w)
	}

	printConclusions(w, results)
}

func printMetricsTable(w io.Writer, results []modelResult) {
	fmt.Fprintln(w, "## Метрики")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Провайдер | Модель | Время | Input | Output | Total | Tokens/sec | Цена input/output за 1M | Стоимость |")
	fmt.Fprintln(w, "|---|---|---:|---:|---:|---:|---:|---|---:|")
	for _, result := range results {
		usage := safeUsage(result.Usage)
		tokensPerSecond := 0.0
		if result.Duration > 0 {
			tokensPerSecond = float64(usage.CompletionTokens) / result.Duration.Seconds()
		}
		price := fmt.Sprintf("$%.3f miss / $%.3f out (%s)", result.Pricing.InputCacheMissPrice, result.Pricing.OutputPrice, result.Pricing.Window)
		if result.Pricing.InputCacheHitPrice != result.Pricing.InputCacheMissPrice {
			price = fmt.Sprintf("$%.3f hit, $%.3f miss / $%.3f out (%s)", result.Pricing.InputCacheHitPrice, result.Pricing.InputCacheMissPrice, result.Pricing.OutputPrice, result.Pricing.Window)
		}
		fmt.Fprintf(w, "| %s | `%s` | %s | %d | %d | %d | %.1f | %s | %s |\n",
			result.Spec.Provider,
			result.Spec.Model,
			formatDuration(result.Duration),
			usage.PromptTokens,
			usage.CompletionTokens,
			usage.TotalTokens,
			tokensPerSecond,
			price,
			formatCost(result.CostUSD, result.CostKnown),
		)
	}
}

func printConclusions(w io.Writer, results []modelResult) {
	if len(results) == 0 {
		return
	}

	fastest := results[0]
	cheapest := results[0]
	leastTokens := results[0]
	for _, result := range results[1:] {
		if result.Duration < fastest.Duration {
			fastest = result
		}
		if result.CostUSD < cheapest.CostUSD {
			cheapest = result
		}
		if safeUsage(result.Usage).TotalTokens < safeUsage(leastTokens.Usage).TotalTokens {
			leastTokens = result
		}
	}

	fmt.Fprintln(w, "## Краткий вывод")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "- Самая быстрая по wall-clock времени: `%s` (%s).\n", fastest.Spec.Model, formatDuration(fastest.Duration))
	fmt.Fprintf(w, "- Самая дешевая в этом запуске: `%s` (%s).\n", cheapest.Spec.Model, formatCost(cheapest.CostUSD, cheapest.CostKnown))
	fmt.Fprintf(w, "- Меньше всего токенов потратила: `%s` (%d total tokens).\n", leastTokens.Spec.Model, safeUsage(leastTokens.Usage).TotalTokens)
	fmt.Fprintln(w, "- Корректность лучше оценивать по самим ответам: метрики скорости и цены не гарантируют правильность решения.")
}

func formatCost(cost float64, known bool) string {
	if !known {
		return "N/A"
	}
	return fmt.Sprintf("$%.8f", cost)
}

func safeUsage(usage *tokenUsage) tokenUsage {
	if usage == nil {
		return tokenUsage{}
	}
	return *usage
}

func formatUsageInline(usage *tokenUsage) string {
	if usage == nil {
		return "tokens: unavailable"
	}

	parts := []string{
		fmt.Sprintf("prompt=%d", usage.PromptTokens),
		fmt.Sprintf("completion=%d", usage.CompletionTokens),
		fmt.Sprintf("total=%d", usage.TotalTokens),
	}
	if usage.PromptCacheHitTokens > 0 || usage.PromptCacheMissTokens > 0 {
		parts = append(parts,
			fmt.Sprintf("cache_hit=%d", usage.PromptCacheHitTokens),
			fmt.Sprintf("cache_miss=%d", usage.PromptCacheMissTokens),
		)
	}
	return "tokens: " + strings.Join(parts, ", ")
}

func formatDuration(duration time.Duration) string {
	if duration < time.Second {
		return fmt.Sprintf("%d ms", duration.Milliseconds())
	}
	return fmt.Sprintf("%.2f s", duration.Seconds())
}

func writeReport(path string, content []byte) error {
	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("не удалось создать папку для отчета: %w", err)
		}
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		return fmt.Errorf("не удалось сохранить отчет: %w", err)
	}
	return nil
}

func defaultReportPath(taskName string) string {
	return filepath.Join("day-5", "answer_"+taskName+".md")
}

func sanitizeTaskName(name string) string {
	var builder strings.Builder
	previousUnderscore := false

	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			builder.WriteRune(r)
			previousUnderscore = false
			continue
		}
		if !previousUnderscore {
			builder.WriteByte('_')
			previousUnderscore = true
		}
	}

	return strings.Trim(builder.String(), "_")
}

func openFile(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", absPath)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", absPath)
	default:
		cmd = exec.Command("xdg-open", absPath)
	}
	return cmd.Start()
}

func formatAPIError(body []byte) string {
	var decoded apiError
	if err := json.Unmarshal(body, &decoded); err == nil && decoded.Error != nil {
		formatted, err := json.Marshal(decoded.Error)
		if err == nil {
			return string(formatted)
		}
	}

	text := strings.TrimSpace(string(body))
	if text == "" {
		return "empty response body"
	}
	return text
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("не удалось открыть %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("некорректная строка %d в %s", lineNumber, path)
		}

		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key == "" {
			return fmt.Errorf("пустой ключ в строке %d файла %s", lineNumber, path)
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf("не удалось установить %s из %s: %w", key, path, err)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("не удалось прочитать %s: %w", path, err)
	}
	return nil
}

func shouldReadStdin(stdin io.Reader) bool {
	file, ok := stdin.(*os.File)
	if !ok {
		return true
	}

	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice == 0
}
