package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL     = "https://api.deepseek.com"
	defaultModel       = "deepseek-v4-flash"
	defaultTemperature = 0.7
	defaultTimeout     = 60 * time.Second
	defaultMaxTokens   = 1024
	defaultAddr        = ":8080"
	defaultSystem      = "Ты полезный AI-ассистент. Отвечай ясно и по делу."
	defaultHistoryPath = "day-10/history.json"
)

type config struct {
	APIKey      string
	Addr        string
	BaseURL     string
	Model       string
	System      string
	Prompt      string
	Timeout     time.Duration
	MaxTokens   int
	Strategy    string
	Window      int
	Temperature float64
	Thinking    bool
	Serve       bool
	HistoryPath string
}

func main() {
	cfg, err := readConfig(os.Args[1:], os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}

	agent := NewAgent(AgentConfig{
		APIKey:      cfg.APIKey,
		BaseURL:     cfg.BaseURL,
		Model:       cfg.Model,
		System:      cfg.System,
		Timeout:     cfg.Timeout,
		MaxTokens:   cfg.MaxTokens,
		Strategy:    cfg.Strategy,
		Window:      cfg.Window,
		Temperature: cfg.Temperature,
		Thinking:    cfg.Thinking,
		Memory:      NewJSONMessageStore(cfg.HistoryPath),
	}, http.DefaultClient)

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
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}

	fmt.Println(response.Content)
	if response.Context.Strategy == StrategyFacts {
		fmt.Fprintf(os.Stderr, "\nContext: strategy=%s facts=%d request_messages=%d stored_history_messages=%d\n",
			response.Context.Strategy,
			response.Context.FactsCount,
			response.Context.ContextMessages,
			response.Context.HistoryMessages,
		)
	} else if response.Context.Strategy == StrategyBranching {
		branchLabel := response.Context.BranchTitle
		if branchLabel == "" {
			branchLabel = response.Context.BranchID
		}
		fmt.Fprintf(os.Stderr, "\nContext: strategy=%s branch=%s checkpoint=%d branch_messages=%d request_messages=%d stored_history_messages=%d\n",
			response.Context.Strategy,
			branchLabel,
			response.Context.CheckpointMessages,
			response.Context.BranchMessages,
			response.Context.ContextMessages,
			response.Context.HistoryMessages,
		)
	} else {
		fmt.Fprintf(os.Stderr, "\nContext: strategy=%s window=%d request_messages=%d stored_history_messages=%d\n",
			response.Context.Strategy,
			response.Context.WindowMessages,
			response.Context.ContextMessages,
			response.Context.HistoryMessages,
		)
	}
	if response.Usage != nil {
		fmt.Fprintf(os.Stderr, "\nTokens: prompt=%d completion=%d total=%d\n", response.Usage.PromptTokens, response.Usage.CompletionTokens, response.Usage.TotalTokens)
	}
}

func readConfig(args []string, stdin io.Reader) (config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return config{}, err
	}

	flags := flag.NewFlagSet("day-10-agent", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	var cfg config
	cfg.Strategy = envOrDefault("DAY10_CONTEXT_STRATEGY", StrategySliding)
	cfg.Window = defaultWindowMessages
	flags.StringVar(&cfg.Prompt, "prompt", "", "prompt to send to the agent")
	flags.BoolVar(&cfg.Serve, "serve", false, "start local web chat")
	flags.StringVar(&cfg.Addr, "addr", defaultAddr, "local web server address")
	flags.StringVar(&cfg.Model, "model", envOrDefault("DEEPSEEK_MODEL", defaultModel), "DeepSeek model")
	flags.StringVar(&cfg.BaseURL, "base-url", envOrDefault("DEEPSEEK_BASE_URL", defaultBaseURL), "DeepSeek API base URL")
	flags.StringVar(&cfg.System, "system", defaultSystem, "system message")
	flags.DurationVar(&cfg.Timeout, "timeout", defaultTimeout, "request timeout")
	flags.IntVar(&cfg.MaxTokens, "max-tokens", defaultMaxTokens, "maximum response tokens")
	flags.StringVar(&cfg.Strategy, "strategy", cfg.Strategy, "context strategy: sliding, facts, or branching")
	flags.IntVar(&cfg.Window, "window", envIntOrDefault("DAY10_CONTEXT_WINDOW", defaultWindowMessages), "number of recent history messages for sliding strategy")
	flags.Float64Var(&cfg.Temperature, "temperature", defaultTemperature, "sampling temperature")
	flags.BoolVar(&cfg.Thinking, "thinking", false, "enable DeepSeek thinking mode when supported")
	flags.StringVar(&cfg.HistoryPath, "history", envOrDefault("DAY10_HISTORY_PATH", defaultHistoryPath), "path to JSON conversation history")

	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if cfg.MaxTokens < 0 {
		return config{}, errors.New("значение -max-tokens не может быть отрицательным")
	}
	cfg.Strategy = normalizeStrategyName(cfg.Strategy)
	if _, err := NewContextStrategy(cfg.Strategy, cfg.Window); err != nil {
		return config{}, err
	}
	if strings.TrimSpace(cfg.Addr) == "" {
		return config{}, errors.New("значение -addr не может быть пустым")
	}
	cfg.HistoryPath = strings.TrimSpace(cfg.HistoryPath)
	if cfg.HistoryPath == "" {
		return config{}, errors.New("значение -history не может быть пустым")
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

	if !cfg.Serve && strings.TrimSpace(cfg.Prompt) == "" {
		return config{}, errors.New("передайте текст через -prompt или stdin")
	}

	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envIntOrDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
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
