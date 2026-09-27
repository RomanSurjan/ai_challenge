package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ai-challenge/day-18/internal/agent"
	"ai-challenge/day-18/internal/mcpgithub"
	"ai-challenge/day-18/internal/mcpmulti"
	appweb "ai-challenge/day-18/internal/web"
)

func main() {
	_ = loadEnv(".env")
	_ = loadEnv("../.env")
	serve := flag.Bool("serve", false, "start the web application")
	prompt := flag.String("prompt", "", "natural-language request")
	address := flag.String("addr", env("APP_ADDR", ":8080"), "web listen address")
	mcpServers := flag.String("mcp-servers", env("MCP_SERVERS", "github=http://127.0.0.1:8083/mcp,currency=http://127.0.0.1:8085/mcp"), "comma-separated name=MCP-endpoint list")
	model := flag.String("model", env("DEEPSEEK_MODEL", "deepseek-chat"), "DeepSeek model")
	baseURL := flag.String("base-url", env("DEEPSEEK_BASE_URL", "https://api.deepseek.com"), "DeepSeek API base URL")
	timeout := flag.Duration("timeout", 75*time.Second, "complete agent request timeout")
	traceJSON := flag.Bool("trace-json", false, "print the full response with MCP trace as JSON")
	flag.Parse()

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSHandshakeTimeout = 30 * time.Second
	httpClient := &http.Client{Timeout: *timeout, Transport: transport}
	connector, err := parseMCPServers(*mcpServers)
	if err != nil {
		log.Fatal(err)
	}
	chatAgent := agent.New(agent.Config{Model: *model, Timeout: *timeout, MaxTokens: 1600, Temperature: 0.2},
		agent.NewDeepSeekClient(os.Getenv("DEEPSEEK_API_KEY"), *baseURL, httpClient),
		connector,
	)
	if *serve {
		runServer(*address, chatAgent, connector)
		return
	}
	value := strings.TrimSpace(*prompt)
	if value == "" {
		piped, _ := io.ReadAll(os.Stdin)
		value = strings.TrimSpace(string(piped))
	}
	if value == "" {
		log.Fatal("provide -prompt or pipe a request to stdin")
	}
	result, err := chatAgent.Ask(context.Background(), value)
	if err != nil {
		log.Fatal(err)
	}
	if *traceJSON {
		_ = json.NewEncoder(os.Stdout).Encode(result)
		return
	}
	for _, event := range result.Trace {
		encoded, _ := json.Marshal(event)
		fmt.Fprintln(os.Stderr, "TRACE", string(encoded))
	}
	fmt.Println(result.Content)
}

func parseMCPServers(value string) (*mcpmulti.Connector, error) {
	parts := strings.Split(value, ",")
	targets := make([]mcpmulti.Target, 0, len(parts))
	names := make(map[string]bool)
	for _, part := range parts {
		name, endpoint, ok := strings.Cut(strings.TrimSpace(part), "=")
		name, endpoint = strings.TrimSpace(name), strings.TrimSpace(endpoint)
		if !ok || name == "" || endpoint == "" {
			return nil, fmt.Errorf("invalid MCP server %q: use name=http://host/mcp", part)
		}
		if names[name] {
			return nil, fmt.Errorf("duplicate MCP server name %q", name)
		}
		parsed, err := url.ParseRequestURI(endpoint)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, fmt.Errorf("invalid MCP endpoint for %s: %q", name, endpoint)
		}
		names[name] = true
		targets = append(targets, mcpmulti.Target{Name: name, Connector: mcpgithub.NewRemoteConnector(endpoint, 20*time.Second)})
	}
	if len(targets) == 0 {
		return nil, errors.New("at least one MCP server is required")
	}
	return mcpmulti.New(targets...), nil
}

func runServer(address string, chatAgent *agent.Agent, connector mcpgithub.Connector) {
	server := &http.Server{
		Addr: address, Handler: appweb.Handler(chatAgent, connector), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 90 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("day18 unified monitoring agent web app listening on %s", address)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func loadEnv(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if _, exists := os.LookupEnv(strings.TrimSpace(key)); !exists {
			_ = os.Setenv(strings.TrimSpace(key), strings.Trim(strings.TrimSpace(value), `"'`))
		}
	}
	return scanner.Err()
}
