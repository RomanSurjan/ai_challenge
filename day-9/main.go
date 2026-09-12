package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL      = "https://api.deepseek.com"
	defaultModel        = "deepseek-v4-flash"
	defaultTemperature  = 0.7
	defaultTimeout      = 60 * time.Second
	defaultMaxTokens    = 1024
	defaultContextLimit = 8192
	defaultAddr         = ":8080"
	defaultSystem       = "Ты полезный AI-ассистент. Отвечай ясно и по делу."
	defaultHistoryPath  = "day-9/history.json"
	defaultSummaryPath  = "day-9/summary.json"
)

type config struct {
	APIKey          string
	Addr            string
	BaseURL         string
	Model           string
	System          string
	Prompt          string
	Timeout         time.Duration
	MaxTokens       int
	ContextLimit    int
	Temperature     float64
	Thinking        bool
	Serve           bool
	DemoTokens      bool
	DemoCompression bool
	CalibrateTokens bool
	HistoryPath     string
	Compression     CompressionConfig
	Pricing         TokenPricing
	Calibration     TokenCalibration
}

func main() {
	cfg, err := readConfig(os.Args[1:], os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}

	agentCfg := AgentConfig{
		APIKey:       cfg.APIKey,
		BaseURL:      cfg.BaseURL,
		Model:        cfg.Model,
		System:       cfg.System,
		Timeout:      cfg.Timeout,
		MaxTokens:    cfg.MaxTokens,
		ContextLimit: cfg.ContextLimit,
		Temperature:  cfg.Temperature,
		Thinking:     cfg.Thinking,
		Memory:       NewJSONMessageStore(cfg.HistoryPath),
		Compression:  cfg.Compression,
		Pricing:      cfg.Pricing,
		Calibration:  cfg.Calibration,
	}
	agent := NewAgent(agentCfg, http.DefaultClient)

	if cfg.DemoTokens {
		if err := runTokenDemo(os.Stdout, TokenDemoConfig{
			ContextLimit: cfg.ContextLimit,
			Pricing:      cfg.Pricing,
			Calibration:  cfg.Calibration,
		}); err != nil {
			fmt.Fprintln(os.Stderr, "Ошибка:", err)
			os.Exit(1)
		}
		return
	}

	if cfg.DemoCompression {
		if err := runCompressionDemo(os.Stdout, CompressionDemoConfig{
			Compression:  cfg.Compression,
			ContextLimit: cfg.ContextLimit,
			Pricing:      cfg.Pricing,
			Calibration:  cfg.Calibration,
		}); err != nil {
			fmt.Fprintln(os.Stderr, "Ошибка:", err)
			os.Exit(1)
		}
		return
	}

	if cfg.CalibrateTokens {
		agentCfg.Memory = nil
		if err := runTokenCalibration(context.Background(), os.Stdout, agentCfg, http.DefaultClient); err != nil {
			fmt.Fprintln(os.Stderr, "Ошибка:", err)
			os.Exit(1)
		}
		return
	}

	if cfg.Serve {
		fmt.Fprintf(os.Stderr, "Web-чат запущен: %s\n", localWebURL(cfg.Addr))
		if err := serveChat(cfg.Addr, agent); err != nil {
			fmt.Fprintln(os.Stderr, "Ошибка:", err)
			os.Exit(1)
		}
		return
	}

	response, err := agent.Ask(context.Background(), cfg.Prompt)
	if err != nil {
		var limitErr *ContextLimitError
		if errors.As(err, &limitErr) {
			fmt.Fprintln(os.Stderr, "Ошибка:", err)
			printTokenReport(os.Stderr, limitErr.Report, nil, "")
			printCompressionReport(os.Stderr, limitErr.CompressionReport)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}

	fmt.Println(response.Content)
	printTokenReport(os.Stderr, response.TokenReport, response.Usage, response.FinishReason)
	printCompressionReport(os.Stderr, response.CompressionReport)
}

func readConfig(args []string, stdin io.Reader) (config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return config{}, err
	}

	contextLimit, err := envIntOrDefault("DAY9_CONTEXT_LIMIT", defaultContextLimit)
	if err != nil {
		return config{}, err
	}
	inputPrice, err := envFloatOrDefault("DAY9_INPUT_PRICE_PER_1M", 0)
	if err != nil {
		return config{}, err
	}
	outputPrice, err := envFloatOrDefault("DAY9_OUTPUT_PRICE_PER_1M", 0)
	if err != nil {
		return config{}, err
	}
	promptMultiplier, err := envFloatOrDefault("DAY9_PROMPT_TOKEN_MULTIPLIER", 1)
	if err != nil {
		return config{}, err
	}
	completionMultiplier, err := envFloatOrDefault("DAY9_COMPLETION_TOKEN_MULTIPLIER", 1)
	if err != nil {
		return config{}, err
	}
	compressionEnabled, err := envBoolOrDefault("DAY9_COMPRESSION_ENABLED", true)
	if err != nil {
		return config{}, err
	}
	keepLastMessages, err := envIntOrDefault("DAY9_KEEP_LAST_MESSAGES", DefaultCompressionConfig().KeepLastMessages)
	if err != nil {
		return config{}, err
	}
	summaryChunkSize, err := envIntOrDefault("DAY9_SUMMARY_CHUNK_SIZE", DefaultCompressionConfig().ChunkSize)
	if err != nil {
		return config{}, err
	}

	flags := flag.NewFlagSet("day-9-agent", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	var cfg config
	cfg.Compression.Enabled = compressionEnabled
	flags.StringVar(&cfg.Prompt, "prompt", "", "prompt to send to the agent")
	flags.BoolVar(&cfg.Serve, "serve", false, "start local web chat")
	flags.StringVar(&cfg.Addr, "addr", defaultAddr, "local web server address")
	flags.StringVar(&cfg.Model, "model", envOrDefault("DEEPSEEK_MODEL", defaultModel), "DeepSeek model")
	flags.StringVar(&cfg.BaseURL, "base-url", envOrDefault("DEEPSEEK_BASE_URL", defaultBaseURL), "DeepSeek API base URL")
	flags.StringVar(&cfg.System, "system", defaultSystem, "system message")
	flags.DurationVar(&cfg.Timeout, "timeout", defaultTimeout, "request timeout")
	flags.IntVar(&cfg.MaxTokens, "max-tokens", defaultMaxTokens, "maximum response tokens")
	flags.IntVar(&cfg.ContextLimit, "context-limit", contextLimit, "local educational input-token limit checked after compression; 0 disables it")
	flags.Float64Var(&cfg.Temperature, "temperature", defaultTemperature, "sampling temperature")
	flags.BoolVar(&cfg.Thinking, "thinking", false, "enable DeepSeek thinking mode when supported")
	flags.BoolVar(&cfg.DemoTokens, "demo-tokens", false, "show token growth demo without calling the API")
	flags.BoolVar(&cfg.DemoCompression, "demo-compression", false, "compare full and compressed history without calling the API")
	flags.BoolVar(&cfg.CalibrateTokens, "calibrate-tokens", false, "compare local token estimates with real DeepSeek API usage")
	flags.StringVar(&cfg.HistoryPath, "history", envOrDefault("DAY9_HISTORY_PATH", defaultHistoryPath), "path to JSON conversation history")
	flags.BoolVar(&cfg.Compression.Enabled, "compress-history", cfg.Compression.Enabled, "send summary plus recent messages instead of full history")
	flags.IntVar(&cfg.Compression.KeepLastMessages, "keep-last-messages", keepLastMessages, "number of recent history messages to keep verbatim")
	flags.IntVar(&cfg.Compression.ChunkSize, "summary-chunk-size", summaryChunkSize, "minimum old message block size before refreshing summary")
	flags.StringVar(&cfg.Compression.SummaryPath, "summary", envOrDefault("DAY9_SUMMARY_PATH", defaultSummaryPath), "path to JSON conversation summary")
	flags.Float64Var(&cfg.Pricing.InputPer1M, "input-price-per-1m", inputPrice, "example input token price per 1M tokens")
	flags.Float64Var(&cfg.Pricing.OutputPer1M, "output-price-per-1m", outputPrice, "example output token price per 1M tokens")
	flags.Float64Var(&cfg.Calibration.PromptMultiplier, "prompt-token-multiplier", promptMultiplier, "empirical multiplier applied to local input token estimates")
	flags.Float64Var(&cfg.Calibration.CompletionMultiplier, "completion-token-multiplier", completionMultiplier, "empirical multiplier applied to local output token estimates")

	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if cfg.MaxTokens < 0 {
		return config{}, errors.New("значение -max-tokens не может быть отрицательным")
	}
	if cfg.ContextLimit < 0 {
		return config{}, errors.New("значение -context-limit не может быть отрицательным")
	}
	if cfg.Compression.KeepLastMessages < 0 {
		return config{}, errors.New("значение -keep-last-messages не может быть отрицательным")
	}
	if cfg.Compression.ChunkSize <= 0 {
		return config{}, errors.New("значение -summary-chunk-size должно быть положительным")
	}
	if cfg.Pricing.InputPer1M < 0 || cfg.Pricing.OutputPer1M < 0 {
		return config{}, errors.New("стоимость токенов не может быть отрицательной")
	}
	if !validPositiveFinite(cfg.Calibration.PromptMultiplier) {
		return config{}, errors.New("значение -prompt-token-multiplier должно быть положительным числом")
	}
	if !validPositiveFinite(cfg.Calibration.CompletionMultiplier) {
		return config{}, errors.New("значение -completion-token-multiplier должно быть положительным числом")
	}
	if strings.TrimSpace(cfg.Addr) == "" {
		return config{}, errors.New("значение -addr не может быть пустым")
	}
	cfg.HistoryPath = strings.TrimSpace(cfg.HistoryPath)
	if cfg.HistoryPath == "" {
		return config{}, errors.New("значение -history не может быть пустым")
	}
	cfg.Compression.SummaryPath = strings.TrimSpace(cfg.Compression.SummaryPath)
	if cfg.Compression.SummaryPath == "" {
		return config{}, errors.New("значение -summary не может быть пустым")
	}
	cfg.Compression = normalizeCompressionConfig(cfg.Compression)

	cfg.APIKey = strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	if !cfg.DemoTokens && !cfg.DemoCompression && cfg.APIKey == "" {
		return config{}, errors.New("переменная окружения DEEPSEEK_API_KEY не задана")
	}

	if !cfg.Serve && !cfg.DemoTokens && !cfg.DemoCompression && !cfg.CalibrateTokens && cfg.Prompt == "" && shouldReadStdin(stdin) {
		piped, err := io.ReadAll(stdin)
		if err != nil {
			return config{}, fmt.Errorf("не удалось прочитать stdin: %w", err)
		}
		cfg.Prompt = strings.TrimSpace(string(piped))
	}

	if !cfg.Serve && !cfg.DemoTokens && !cfg.DemoCompression && !cfg.CalibrateTokens && strings.TrimSpace(cfg.Prompt) == "" {
		return config{}, errors.New("передайте текст через -prompt или stdin")
	}

	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return cfg, nil
}

func validPositiveFinite(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envIntOrDefault(key string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("переменная окружения %s должна быть целым числом: %w", key, err)
	}
	return parsed, nil
}

func envFloatOrDefault(key string, fallback float64) (float64, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("переменная окружения %s должна быть числом: %w", key, err)
	}
	return parsed, nil
}

func envBoolOrDefault(key string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("переменная окружения %s должна быть bool: %w", key, err)
	}
	return parsed, nil
}

func printTokenReport(w io.Writer, report TokenReport, usage *tokenUsage, finishReason string) {
	fmt.Fprintln(w, "\nToken report:")
	fmt.Fprintf(w, "  local estimate:      input=%d output=%d overall=%d\n",
		report.RawPromptTokens,
		report.RawAnswerTokens,
		report.RawTotalTokens,
	)
	if hasNonDefaultCalibration(report) {
		fmt.Fprintf(w, "  calibrated estimate: input=%d output=%d overall=%d (input x%.4g, output x%.4g)\n",
			report.PromptTokens,
			report.AnswerTokens,
			report.TotalTokens,
			report.PromptMultiplier,
			report.CompletionMultiplier,
		)
	}
	if usage != nil {
		fmt.Fprintf(w, "  API usage:           input=%d output=%d overall=%d\n", usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
		fmt.Fprintf(w, "  estimate vs API:     input=%+d (%.1f%%) output=%+d (%.1f%%) overall=%+d\n",
			report.PromptTokens-usage.PromptTokens,
			percentDiff(report.PromptTokens-usage.PromptTokens, usage.PromptTokens),
			report.AnswerTokens-usage.CompletionTokens,
			percentDiff(report.AnswerTokens-usage.CompletionTokens, usage.CompletionTokens),
			report.TotalTokens-usage.TotalTokens,
		)
	}
}

func printCompressionReport(w io.Writer, report *CompressionReport) {
	if report == nil {
		return
	}
	fmt.Fprintln(w, "\nCompression report:")
	fmt.Fprintf(w, "  mode:                       %s\n", report.Mode)
	fmt.Fprintf(w, "  total history messages:     %d\n", report.TotalHistoryMessages)
	fmt.Fprintf(w, "  summary covers:             %d messages\n", report.SummaryCoversMessages)
	fmt.Fprintf(w, "  recent messages kept:       %d\n", report.RecentMessagesKept)
	fmt.Fprintf(w, "  old messages waiting for summary: %d\n", report.PendingOldMessages)
	fmt.Fprintf(w, "  summary update:             %s\n", report.SummaryUpdateLabel)
	if report.SummaryUpdateStatus == "waiting" {
		fmt.Fprintf(w, "  messages until summary:     %d\n", report.MessagesUntilSummaryUpdate)
	}
	fmt.Fprintf(w, "  summary updated now:        %s\n", yesNo(report.SummaryUpdatedNow))
	if report.SummaryUpdatedNow {
		fmt.Fprintf(w, "  newly compressed:           %d messages\n", report.NewlyCompressedMessages)
	}
	fmt.Fprintf(w, "  reason:                     %s\n", report.Reason)
	fmt.Fprintf(w, "  full prompt input:          %d tokens\n", report.FullPromptInputTokens)
	fmt.Fprintf(w, "  actual prompt input:        %d tokens\n", report.ActualPromptInputTokens)
	fmt.Fprintf(w, "  estimated saved input:      %d tokens (%.1f%%)\n", report.EstimatedSavedInputTokens, report.EstimatedSavedPercent)
	fmt.Fprintf(w, "  saving status:              %s\n", report.SavingStatus)
	fmt.Fprintf(w, "  context-limit check:        %s\n", report.ContextLimitStatus)
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func hasNonDefaultCalibration(report TokenReport) bool {
	calibration := normalizeTokenCalibration(TokenCalibration{
		PromptMultiplier:     report.PromptMultiplier,
		CompletionMultiplier: report.CompletionMultiplier,
	})
	return math.Abs(calibration.PromptMultiplier-1) > 0.000001 || math.Abs(calibration.CompletionMultiplier-1) > 0.000001
}

func localWebURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	return "http://" + addr
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
