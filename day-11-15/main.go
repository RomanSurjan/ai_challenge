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
	defaultMemoryDir   = "day-11-15/memory"
)

type config struct {
	APIKey       string
	Addr         string
	BaseURL      string
	Model        string
	System       string
	Prompt       string
	Timeout      time.Duration
	MaxTokens    int
	Temperature  float64
	Thinking     bool
	Serve        bool
	MemoryDir    string
	UseShortTerm bool
	UseWorking   bool
	UseLongTerm  bool
	AutoMemory   bool
	DialogID     string
	DialogNew    bool
	DialogTitle  string
	DialogList   bool
	DialogSelect string
}

func main() {
	cfg, err := readConfig(os.Args[1:], os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}

	selection := MemorySelection{
		ShortTerm: cfg.UseShortTerm,
		Working:   cfg.UseWorking,
		LongTerm:  cfg.UseLongTerm,
	}
	memory := NewJSONMemoryLayers(cfg.MemoryDir)
	agent := NewAgent(AgentConfig{
		APIKey:      cfg.APIKey,
		BaseURL:     cfg.BaseURL,
		Model:       cfg.Model,
		System:      cfg.System,
		Timeout:     cfg.Timeout,
		MaxTokens:   cfg.MaxTokens,
		Temperature: cfg.Temperature,
		Thinking:    cfg.Thinking,
		Memory:      memory,
		Selection:   &selection,
		AutoMemory:  &cfg.AutoMemory,
	}, http.DefaultClient)
	ctx := context.Background()
	if _, err := agent.Initialize(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}

	if cfg.DialogList {
		list, err := agent.Dialogs(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Ошибка:", err)
			os.Exit(1)
		}
		for _, dialog := range list.Dialogs {
			marker := " "
			if dialog.ID == list.ActiveDialogID {
				marker = "*"
			}
			fmt.Printf("%s %s\t%s\t%s\n", marker, dialog.ID, dialog.Title, dialog.UpdatedAt.Format(time.RFC3339))
		}
		return
	}
	if cfg.DialogNew {
		dialog, err := agent.CreateDialog(ctx, cfg.DialogTitle)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Ошибка:", err)
			os.Exit(1)
		}
		fmt.Printf("Создан и выбран диалог: %s (%s)\n", dialog.ID, dialog.Title)
		return
	}
	if cfg.DialogSelect != "" {
		dialog, err := agent.SelectDialog(ctx, cfg.DialogSelect)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Ошибка:", err)
			os.Exit(1)
		}
		fmt.Printf("Выбран диалог: %s (%s)\n", dialog.ID, dialog.Title)
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

	if cfg.DialogID != "" {
		if _, err := agent.SelectDialog(ctx, cfg.DialogID); err != nil {
			fmt.Fprintln(os.Stderr, "Ошибка:", err)
			os.Exit(1)
		}
	}
	response, err := agent.AskInDialog(ctx, cfg.DialogID, cfg.Prompt)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Диалог: %s\n", response.DialogID)
	fmt.Println(response.Content)
	if response.Usage != nil {
		fmt.Fprintf(os.Stderr, "\nTokens: prompt=%d completion=%d total=%d\n", response.Usage.PromptTokens, response.Usage.CompletionTokens, response.Usage.TotalTokens)
	}
}

func readConfig(args []string, stdin io.Reader) (config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return config{}, err
	}

	flags := flag.NewFlagSet("day-11-15-agent", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	var cfg config
	flags.StringVar(&cfg.Prompt, "prompt", "", "prompt to send to the agent")
	flags.BoolVar(&cfg.Serve, "serve", false, "start local web chat")
	flags.StringVar(&cfg.Addr, "addr", defaultAddr, "local web server address")
	flags.StringVar(&cfg.Model, "model", envOrDefault("DEEPSEEK_MODEL", defaultModel), "DeepSeek model")
	flags.StringVar(&cfg.BaseURL, "base-url", envOrDefault("DEEPSEEK_BASE_URL", defaultBaseURL), "DeepSeek API base URL")
	flags.StringVar(&cfg.System, "system", defaultSystem, "system message")
	flags.DurationVar(&cfg.Timeout, "timeout", defaultTimeout, "request timeout")
	flags.IntVar(&cfg.MaxTokens, "max-tokens", defaultMaxTokens, "maximum response tokens")
	flags.Float64Var(&cfg.Temperature, "temperature", defaultTemperature, "sampling temperature")
	flags.BoolVar(&cfg.Thinking, "thinking", false, "enable DeepSeek thinking mode when supported")
	flags.StringVar(&cfg.MemoryDir, "memory-dir", envOrDefault("DAY11_MEMORY_DIR", defaultMemoryDir), "directory with separate memory layer files")
	flags.BoolVar(&cfg.UseShortTerm, "use-short-term", true, "include short-term memory in model context")
	flags.BoolVar(&cfg.UseWorking, "use-working", true, "include working memory in model context")
	flags.BoolVar(&cfg.UseLongTerm, "use-long-term", true, "include long-term memory in model context")
	flags.BoolVar(&cfg.AutoMemory, "auto-memory", true, "allow the model to update enabled working and long-term memory")
	flags.StringVar(&cfg.DialogID, "dialog", "", "dialog ID for the prompt and active selection")
	flags.BoolVar(&cfg.DialogNew, "dialog-new", false, "create and select a new dialog")
	flags.StringVar(&cfg.DialogTitle, "dialog-title", "", "title used with -dialog-new")
	flags.BoolVar(&cfg.DialogList, "dialog-list", false, "list dialogs and mark the active one")
	flags.StringVar(&cfg.DialogSelect, "dialog-select", "", "select an existing dialog and exit")

	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if cfg.MaxTokens < 0 {
		return config{}, errors.New("значение -max-tokens не может быть отрицательным")
	}
	if strings.TrimSpace(cfg.Addr) == "" {
		return config{}, errors.New("значение -addr не может быть пустым")
	}
	cfg.MemoryDir = strings.TrimSpace(cfg.MemoryDir)
	if cfg.MemoryDir == "" {
		return config{}, errors.New("значение -memory-dir не может быть пустым")
	}
	cfg.DialogID = strings.TrimSpace(cfg.DialogID)
	cfg.DialogTitle = strings.TrimSpace(cfg.DialogTitle)
	cfg.DialogSelect = strings.TrimSpace(cfg.DialogSelect)
	if cfg.DialogID != "" {
		if err := validateDialogID(cfg.DialogID); err != nil {
			return config{}, err
		}
	}
	if cfg.DialogSelect != "" {
		if err := validateDialogID(cfg.DialogSelect); err != nil {
			return config{}, err
		}
	}
	actionCount := 0
	for _, enabled := range []bool{cfg.Serve, cfg.DialogNew, cfg.DialogList, cfg.DialogSelect != ""} {
		if enabled {
			actionCount++
		}
	}
	if actionCount > 1 {
		return config{}, errors.New("-serve, -dialog-new, -dialog-list и -dialog-select являются взаимоисключающими режимами")
	}
	if cfg.DialogTitle != "" && !cfg.DialogNew {
		return config{}, errors.New("-dialog-title можно использовать только вместе с -dialog-new")
	}
	if cfg.DialogID != "" && actionCount > 0 {
		return config{}, errors.New("-dialog используется только в режиме отправки prompt")
	}

	isDialogAction := cfg.DialogNew || cfg.DialogList || cfg.DialogSelect != ""
	if !cfg.Serve && !isDialogAction && cfg.Prompt == "" && shouldReadStdin(stdin) {
		piped, err := io.ReadAll(stdin)
		if err != nil {
			return config{}, fmt.Errorf("не удалось прочитать stdin: %w", err)
		}
		cfg.Prompt = strings.TrimSpace(string(piped))
	}

	if !cfg.Serve && !isDialogAction && strings.TrimSpace(cfg.Prompt) == "" {
		return config{}, errors.New("передайте текст через -prompt или stdin")
	}
	if (cfg.Serve || isDialogAction) && strings.TrimSpace(cfg.Prompt) != "" {
		return config{}, errors.New("prompt нельзя сочетать с выбранным режимом управления диалогами")
	}
	if cfg.Serve || !isDialogAction {
		cfg.APIKey = strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
		if cfg.APIKey == "" {
			return config{}, errors.New("переменная окружения DEEPSEEK_API_KEY не задана")
		}
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
