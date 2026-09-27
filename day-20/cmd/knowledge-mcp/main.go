package main

import (
	"ai-challenge/day-20/internal/knowledge"
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
	run(os.Getenv("MCP_ADDR"), knowledge.Handler(knowledge.NewClient(&http.Client{Timeout: 15 * time.Second, Transport: tr})))
}
func run(addr string, h http.Handler) {
	if addr == "" {
		addr = ":8088"
	}
	s := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 25 * time.Second, WriteTimeout: 30 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		c, x := context.WithTimeout(context.Background(), 10*time.Second)
		defer x()
		_ = s.Shutdown(c)
	}()
	log.Printf("listening on %s", addr)
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
