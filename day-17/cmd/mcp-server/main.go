package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ai-challenge/day-17/internal/githubapi"
	"ai-challenge/day-17/internal/mcpgithub"
)

func main() {
	address := os.Getenv("MCP_ADDR")
	if address == "" {
		address = ":8081"
	}
	server := &http.Server{
		Addr: address, Handler: mcpgithub.Handler(githubapi.NewClient(nil, os.Getenv("GITHUB_TOKEN"))),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("%s %s listening on %s (endpoint /mcp)", mcpgithub.ServerName, mcpgithub.ServerVersion, address)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
