package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ai-challenge/day-19/internal/pipeline"
)

func main() {
	address := env("MCP_ADDR", ":8086")
	outputDir := env("OUTPUT_DIR", "/data/outputs")
	wikiTransport := http.DefaultTransport.(*http.Transport).Clone()
	wikiTransport.TLSHandshakeTimeout = 10 * time.Second
	wikipedia := pipeline.NewWikipediaClient(&http.Client{Timeout: 12 * time.Second, Transport: wikiTransport})
	server := &http.Server{Addr: address, Handler: pipeline.Handler(wikipedia, pipeline.NewSaver(outputDir)), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("day19 MCP server listening on %s", address)
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
