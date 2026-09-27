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

	currencyscheduler "ai-challenge/day-18/internal/currencyscheduler"
	currencystore "ai-challenge/day-18/internal/currencystore"
	"ai-challenge/day-18/internal/frankfurter"
	"ai-challenge/day-18/internal/mcpcurrency"
)

func main() {
	address := env("MCP_ADDR", ":8085")
	databasePath := env("DATABASE_PATH", "/data/currency.db")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	storage, err := currencystore.Open(ctx, databasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer storage.Close()
	rates := frankfurter.NewClient(nil)
	if baseURL := strings.TrimSpace(os.Getenv("FRANKFURTER_BASE_URL")); baseURL != "" {
		rates.BaseURL = baseURL
	}
	planner := currencyscheduler.New(storage, rates)
	planner.OnError = func(err error) { log.Printf("scheduler: %v", err) }
	server := &http.Server{
		Addr: address, Handler: mcpcurrency.Handler(storage, rates),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
	}
	errCh := make(chan error, 2)
	go func() { errCh <- planner.Run(ctx) }()
	go func() {
		log.Printf("%s %s listening on %s (endpoint /mcp, database %s)", mcpcurrency.ServerName, mcpcurrency.ServerVersion, address, databasePath)
		errCh <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("service stopped: %v", err)
		}
		stop()
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		log.Printf("HTTP shutdown: %v", err)
	}
	select {
	case <-errCh:
	case <-shutdown.Done():
	}
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
