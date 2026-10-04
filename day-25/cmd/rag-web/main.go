package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"ai-challenge/day-25/internal/app"
	"ai-challenge/day-25/internal/chatservice"
	appweb "ai-challenge/day-25/internal/web"
)

const (
	defaultAddr            = "127.0.0.1:8080"
	defaultShutdownTimeout = 10 * time.Second
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		log.Printf("day25 rag web: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("rag-web", flag.ContinueOnError)
	config := app.ConfigFromEnv()
	config.BindFlags(f)
	addr := f.String("addr", envString("RAG_WEB_ADDR", defaultAddr), "HTTP listen address")
	modeValue := f.String("mode", envString("RAG_CHAT_MODE", string(chatservice.TaskMemory)), "stateless, history, or task-memory")
	secureCookie := f.Bool("secure-cookie", envBool("RAG_WEB_SECURE_COOKIE", false), "set Secure on the session cookie")
	if err := f.Parse(args); err != nil {
		return err
	}
	mode, err := app.ParseMode(*modeValue)
	if err != nil {
		return err
	}
	runtime, err := config.Build()
	if err != nil {
		return err
	}

	handler := appweb.Handler(runtime.Service, runtime.Store, appweb.Config{
		DefaultMode:    mode,
		RequestTimeout: config.Timeout,
		CookieSecure:   *secureCookie,
		Logger:         log.Default(),
	})
	server := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      config.Timeout + 10*time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()
	log.Printf("day25 rag web listening on http://%s", *addr)

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}
		err := <-serverErrors
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
