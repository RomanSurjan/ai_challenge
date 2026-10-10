package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ai-challenge/day-29/internal/experiment"
)

//go:embed web/*
var assets embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:8089", "loopback address")
	base := flag.String("ollama", "http://127.0.0.1:11434", "literal loopback Ollama origin")
	index := flag.String("index", "../day-21/artifacts/index-structural.json", "read-only index")
	eval := flag.Bool("eval", false, "run fixed benchmark and exit")
	output := flag.String("output", "artifacts/evaluation.json", "benchmark output")
	flag.StringVar(&datasetPath, "dataset", "eval/control.json", "fixed dataset")
	flag.StringVar(&profilePaths, "profiles", "configs/baseline.json,configs/selected.json", "comma separated profiles")
	flag.IntVar(&repeats, "repeats", 3, "measured repetitions")
	flag.Parse()
	if repeats < 1 {
		log.Fatal("positive repeats required")
	}
	r, e := experiment.New(*index, *base)
	if e != nil {
		log.Fatal(e)
	}
	if *eval {
		if e = benchmark(r, *output); e != nil {
			log.Fatal(e)
		}
		return
	}
	host, _, e := net.SplitHostPort(*addr)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("web address must use literal loopback IP")
	}
	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" {
			http.Error(w, "GET required", 405)
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
		defer cancel()
		send(w, r.Metadata(ctx))
	})
	mux.HandleFunc("/api/ask", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "POST" {
			http.Error(w, "POST required", 405)
			return
		}
		if origin := req.Header.Get("Origin"); origin != "" && origin != "http://"+req.Host {
			w.WriteHeader(403)
			send(w, map[string]string{"error": "Origin rejected"})
			return
		}
		var p struct {
			Question string `json:"question"`
			Mode     string `json:"mode"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, req.Body, 64<<10))
		d.DisallowUnknownFields()
		err := d.Decode(&p)
		var tail any
		if err != nil || d.Decode(&tail) != io.EOF || len([]rune(p.Question)) > 12000 || strings.TrimSpace(p.Question) == "" || (p.Mode != "baseline" && p.Mode != "selected" && p.Mode != "compare") {
			w.WriteHeader(400)
			send(w, map[string]string{"error": "Нужен непустой вопрос до 12 000 символов и mode baseline/selected/compare."})
			return
		}
		// Serialize model experiments to keep timings interpretable on a shared GPU.
		mu.Lock()
		defer mu.Unlock()
		ctx, cancel := context.WithTimeout(req.Context(), 6*time.Minute)
		defer cancel()
		run := r.Ask(ctx, strings.TrimSpace(p.Question), p.Mode)
		if err := save(filepath.Join("artifacts", "traces", run.ID+".json"), run); err != nil {
			log.Print("trace save failed: ", err)
		}
		send(w, run)
	})
	mux.HandleFunc("/api/benchmark", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" {
			http.Error(w, "GET required", 405)
			return
		}
		b, e := os.ReadFile("artifacts/summary.json")
		if e != nil {
			send(w, map[string]any{"state": "not_run"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	})
	mux.HandleFunc("/api/quantization", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" {
			http.Error(w, "GET required", 405)
			return
		}
		b, err := os.ReadFile("artifacts/tune-quantization.json")
		if err != nil {
			http.Error(w, "Saved experiment unavailable", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(b)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		name := req.URL.Path
		if name == "/" {
			name = "/index.html"
		}
		if name != "/index.html" && name != "/app.js" && name != "/styles.css" {
			http.NotFound(w, req)
			return
		}
		b, err := assets.ReadFile("web" + name)
		if err != nil {
			http.NotFound(w, req)
			return
		}
		types := map[string]string{"/index.html": "text/html; charset=utf-8", "/app.js": "text/javascript; charset=utf-8", "/styles.css": "text/css; charset=utf-8"}
		w.Header().Set("Content-Type", types[name])
		w.Write(b)
	})
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, req)
	})
	log.Printf("Day 29 http://%s", *addr)
	log.Fatal((&http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}).ListenAndServe())
}
func send(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}
func save(path string, v any) error {
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}
