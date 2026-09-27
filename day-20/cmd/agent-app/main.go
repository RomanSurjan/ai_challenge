package main

import (
	"ai-challenge/day-20/internal/agent"
	"ai-challenge/day-20/internal/mcpclient"
	appweb "ai-challenge/day-20/internal/web"
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func env(k, d string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return d
}
func main() {
	k := env("KNOWLEDGE_MCP_ENDPOINT", "http://127.0.0.1:8088/mcp")
	g := env("GITHUB_MCP_ENDPOINT", "http://127.0.0.1:8089/mcp")
	a := env("ARTIFACT_MCP_ENDPOINT", "http://127.0.0.1:8090/mcp")
	connector := mcpclient.NewMulti(mcpclient.Target{Name: "Knowledge MCP", Endpoint: k, Connector: mcpclient.NewRemote(k, 25*time.Second)}, mcpclient.Target{Name: "GitHub MCP", Endpoint: g, Connector: mcpclient.NewRemote(g, 25*time.Second)}, mcpclient.Target{Name: "Artifact MCP", Endpoint: a, Connector: mcpclient.NewRemote(a, 25*time.Second)})
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSHandshakeTimeout = 30 * time.Second
	llm := agent.NewDeepSeekClient(os.Getenv("DEEPSEEK_API_KEY"), env("DEEPSEEK_BASE_URL", "https://api.deepseek.com"), &http.Client{Timeout: 110 * time.Second, Transport: tr})
	chat := agent.New(agent.Config{Model: env("DEEPSEEK_MODEL", "deepseek-chat"), Timeout: 110 * time.Second, MaxTokens: 16384, MaxToolRounds: 18, MaxToolCalls: 24}, llm, connector)
	addr := env("APP_ADDR", ":8091")
	s := &http.Server{Addr: addr, Handler: appweb.Handler(chat, env("OUTPUT_DIR", "/data/outputs")), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 125 * time.Second, WriteTimeout: 125 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		x, y := context.WithTimeout(context.Background(), 10*time.Second)
		defer y()
		_ = s.Shutdown(x)
	}()
	log.Printf("day20 agent app listening on %s", addr)
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
