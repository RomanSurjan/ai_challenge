package main

import (
	"ai-challenge/day-20/internal/githubapi"
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSHandshakeTimeout = 10 * time.Second
	c := githubapi.NewClient(&http.Client{Timeout: 15 * time.Second, Transport: tr}, os.Getenv("GITHUB_TOKEN"))
	addr := os.Getenv("MCP_ADDR")
	if addr == "" {
		addr = ":8089"
	}
	s := &http.Server{Addr: addr, Handler: githubapi.Handler(c), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 25 * time.Second, WriteTimeout: 30 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		x, y := context.WithTimeout(context.Background(), 10*time.Second)
		defer y()
		_ = s.Shutdown(x)
	}()
	log.Printf("listening on %s", addr)
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
