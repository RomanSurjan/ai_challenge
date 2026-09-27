package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ai-challenge/day-19/internal/agent"
	"ai-challenge/day-19/internal/mcpclient"
	appweb "ai-challenge/day-19/internal/web"
)

func main() {
	address := flag.String("addr", env("APP_ADDR", ":8087"), "web listen address")
	mcpEndpoint := flag.String("mcp", env("MCP_ENDPOINT", "http://127.0.0.1:8086/mcp"), "MCP endpoint")
	model := flag.String("model", env("DEEPSEEK_MODEL", "deepseek-chat"), "DeepSeek model")
	baseURL := flag.String("base-url", env("DEEPSEEK_BASE_URL", "https://api.deepseek.com"), "DeepSeek API base URL")
	outputDir := flag.String("outputs", env("OUTPUT_DIR", "/data/outputs"), "read-only output directory")
	flag.Parse()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSHandshakeTimeout = 30 * time.Second
	deepSeekHTTP := &http.Client{Timeout: 90 * time.Second, Transport: transport}
	chatAgent := agent.New(agent.Config{Model: *model, Timeout: 90 * time.Second, MaxTokens: 8192, MaxToolRounds: 6, MaxToolCalls: 6}, agent.NewDeepSeekClient(os.Getenv("DEEPSEEK_API_KEY"), *baseURL, deepSeekHTTP), mcpclient.NewRemoteConnector(*mcpEndpoint, 25*time.Second))
	server := &http.Server{Addr: *address, Handler: appweb.Handler(chatAgent, *outputDir), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 105 * time.Second, WriteTimeout: 105 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("day19 agent web app listening on %s", *address)
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
