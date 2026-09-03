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
	defaultBaseURL   = "https://api.deepseek.com"
	defaultModel     = "deepseek-v4-flash"
	defaultTimeout   = 90 * time.Second
	defaultMaxTokens = 2048
	defaultTaskName  = "temperature_experiment"
)

var experimentTemperatures = []float64{0, 0.7, 1.2}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type thinkingConfig struct {
	Type string `json:"type"`
}

type chatRequest struct {
	Model       string          `json:"model"`
	Messages    []chatMessage   `json:"messages"`
	Temperature float64         `json:"temperature"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Thinking    *thinkingConfig `json:"thinking,omitempty"`
}

type tokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
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

type config struct {
	APIKey     string
	BaseURL    string
	Model      string
	System     string
	Prompt     string
	Timeout    time.Duration
	MaxTokens  int
	Thinking   bool
	TaskName   string
	OutputPath string
	OpenReport bool
}

type experimentResult struct {
	Temperature float64
	Prompt      string
	Answer      string
	Usage       *tokenUsage
}

func main() {
	cfg, err := readConfig(os.Args[1:], os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}

	results, err := runExperiment(context.Background(), http.DefaultClient, cfg)
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

	flags := flag.NewFlagSet("day-4-temperature-experiment", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	var cfg config
	flags.StringVar(&cfg.Prompt, "prompt", "", "same request to run with different temperatures")
	flags.StringVar(&cfg.Model, "model", envOrDefault("DEEPSEEK_MODEL", defaultModel), "DeepSeek model")
	flags.StringVar(&cfg.BaseURL, "base-url", envOrDefault("DEEPSEEK_BASE_URL", defaultBaseURL), "DeepSeek API base URL")
	flags.StringVar(&cfg.System, "system", "", "optional system message")
	flags.DurationVar(&cfg.Timeout, "timeout", defaultTimeout, "request timeout")
	flags.IntVar(&cfg.MaxTokens, "max-tokens", defaultMaxTokens, "maximum response tokens per API call")
	flags.BoolVar(&cfg.Thinking, "thinking", false, "enable DeepSeek thinking mode when supported")
	flags.StringVar(&cfg.TaskName, "task-name", defaultTaskName, "task name for the default answer_<task_name>.md report")
	flags.StringVar(&cfg.OutputPath, "out", "", "path to save Markdown report")
	flags.BoolVar(&cfg.OpenReport, "open", false, "open Markdown report after saving")

	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if cfg.MaxTokens < 0 {
		return config{}, errors.New("значение -max-tokens не может быть отрицательным")
	}

	cfg.APIKey = strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	if cfg.APIKey == "" {
		return config{}, errors.New("переменная окружения DEEPSEEK_API_KEY не задана")
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

	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return cfg, nil
}

func runExperiment(ctx context.Context, client *http.Client, cfg config) ([]experimentResult, error) {
	results := make([]experimentResult, 0, len(experimentTemperatures))
	for _, temperature := range experimentTemperatures {
		answer, usage, err := requestCompletion(ctx, client, cfg, temperature)
		if err != nil {
			return nil, fmt.Errorf("temperature %.1f: %w", temperature, err)
		}
		results = append(results, experimentResult{
			Temperature: temperature,
			Prompt:      cfg.Prompt,
			Answer:      answer,
			Usage:       usage,
		})
	}
	return results, nil
}

func requestCompletion(ctx context.Context, client *http.Client, cfg config, temperature float64) (string, *tokenUsage, error) {
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	reqBody := chatRequest{
		Model:       cfg.Model,
		Messages:    buildMessages(cfg),
		Temperature: temperature,
		MaxTokens:   cfg.MaxTokens,
	}
	if !cfg.Thinking {
		reqBody.Thinking = &thinkingConfig{Type: "disabled"}
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", nil, fmt.Errorf("не удалось собрать JSON-запрос: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", nil, fmt.Errorf("не удалось создать HTTP-запрос: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("DeepSeek API недоступен: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("не удалось прочитать ответ DeepSeek: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", nil, fmt.Errorf("DeepSeek API вернул HTTP %d: %s", resp.StatusCode, formatAPIError(respBody))
	}

	var decoded chatResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return "", nil, fmt.Errorf("не удалось разобрать JSON-ответ DeepSeek: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return "", nil, errors.New("DeepSeek API вернул ответ без choices")
	}

	answer := strings.TrimSpace(decoded.Choices[0].Message.Content)
	if answer == "" {
		return "", nil, errors.New("DeepSeek API вернул пустой ответ")
	}

	return answer, decoded.Usage, nil
}

func buildMessages(cfg config) []chatMessage {
	if strings.TrimSpace(cfg.System) == "" {
		return []chatMessage{{Role: "user", Content: cfg.Prompt}}
	}
	return []chatMessage{
		{Role: "system", Content: cfg.System},
		{Role: "user", Content: cfg.Prompt},
	}
}

func printReport(w io.Writer, cfg config, results []experimentResult) {
	fmt.Fprintln(w, "# Day 4: влияние temperature на ответ модели")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Задача")
	fmt.Fprintln(w)
	fmt.Fprintln(w, cfg.Prompt)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Настройки")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "- model: `%s`\n", cfg.Model)
	fmt.Fprintf(w, "- temperatures: `%s`\n", formatTemperatures(experimentTemperatures))
	fmt.Fprintf(w, "- max_tokens: `%d`\n", cfg.MaxTokens)
	fmt.Fprintf(w, "- task_name: `%s`\n", cfg.TaskName)
	fmt.Fprintln(w)

	for _, result := range results {
		fmt.Fprintf(w, "## temperature = %s\n\n", formatTemperature(result.Temperature))
		fmt.Fprintln(w, "### Пример ответа")
		fmt.Fprintln(w)
		fmt.Fprintln(w, result.Answer)
		printUsage(w, result.Usage)
		fmt.Fprintln(w)
	}

	printConclusions(w)
}

func printUsage(w io.Writer, usage *tokenUsage) {
	if usage == nil {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "_Tokens: prompt=%d completion=%d total=%d_\n", usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
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
	return filepath.Join("day-4", "answer_"+taskName+".md")
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

func formatTemperatures(temperatures []float64) string {
	parts := make([]string, 0, len(temperatures))
	for _, temperature := range temperatures {
		parts = append(parts, formatTemperature(temperature))
	}
	return strings.Join(parts, ", ")
}

func formatTemperature(temperature float64) string {
	if temperature == float64(int(temperature)) {
		return fmt.Sprintf("%.0f", temperature)
	}
	return fmt.Sprintf("%.1f", temperature)
}

func printConclusions(w io.Writer) {
	fmt.Fprintln(w, "## Выводы")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "- `temperature = 0` лучше использовать для задач, где важны стабильность, точность и повторяемость: алгоритмы, факты, инструкции, проверяемый код.")
	fmt.Fprintln(w, "- `temperature = 0.7` подходит для большинства рабочих задач: ответ остается достаточно управляемым, но становится живее и может предлагать разные формулировки.")
	fmt.Fprintln(w, "- `temperature = 1.2` полезна для креативных идей, вариантов текста и мозгового штурма, но ее ответы нужно внимательнее проверять: выше шанс лишних допущений и отклонений от задачи.")
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
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
