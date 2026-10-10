package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ai-challenge/day-28/internal/experiment"
)

//go:embed web/*
var assets embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:8088", "loopback address")
	base := flag.String("ollama", "http://127.0.0.1:11434", "literal loopback Ollama origin")
	model := flag.String("model", "qwen2.5:3b", "local model")
	index := flag.String("index", "../day-21/artifacts/index-structural.json", "read-only index")
	cloud := flag.Bool("cloud", false, "enable existing DeepSeek credentials from environment or repository .env")
	cloudEnv := flag.String("cloud-env-file", "../.env", "optional server-side dotenv; read only with --cloud")
	eval := flag.Bool("eval", false, "run fixed benchmark and exit")
	output := flag.String("output", "artifacts/evaluation.json", "benchmark output")
	flag.Parse()
	if *cloud {
		loadEnv(*cloudEnv)
	}
	r, e := experiment.New(*index, *base, *model, *cloud)
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
		if err != nil || d.Decode(&tail) != io.EOF || len([]rune(p.Question)) > 12000 || strings.TrimSpace(p.Question) == "" || (p.Mode != "local" && p.Mode != "compare") {
			w.WriteHeader(400)
			send(w, map[string]string{"error": "Нужен непустой вопрос до 12 000 символов и mode local/compare."})
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
	log.Printf("Day 28 http://%s • local %s • cloud %s", *addr, *model, r.CloudState())
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
func loadEnv(path string) {
	b, e := os.ReadFile(path)
	if e != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if ok && (k == "DEEPSEEK_API_KEY" || k == "DEEPSEEK_MODEL" || k == "DEEPSEEK_BASE_URL") && os.Getenv(k) == "" {
			os.Setenv(k, strings.Trim(strings.TrimSpace(v), "\"'"))
		}
	}
}
func benchmark(r *experiment.Runtime, path string) error {
	b, e := os.ReadFile("eval/questions.json")
	if e != nil {
		return e
	}
	var qs []struct {
		ID       string `json:"id"`
		Question string `json:"question"`
	}
	if e = json.Unmarshal(b, &qs); e != nil {
		return e
	}
	protocol, e := os.ReadFile("eval/protocol.json")
	if e != nil {
		return e
	}
	var plan struct {
		IDs []string `json:"repeated_ids"`
		N   int      `json:"repetitions"`
	}
	if e = json.Unmarshal(protocol, &plan); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	meta := r.Metadata(ctx)
	cancel()
	data := map[string]any{"metadata": meta, "dataset_sha256": experiment.Hash(json.RawMessage(b)), "protocol_sha256": experiment.Hash(json.RawMessage(protocol)), "started_at": time.Now().UTC(), "runs": []any{}}
	runs := []any{}
	execute := func(q struct {
		ID       string `json:"id"`
		Question string `json:"question"`
	}, phase string, n int) error {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		run := r.Ask(ctx, q.Question, "compare")
		runs = append(runs, map[string]any{"question_id": q.ID, "phase": phase, "repetition": n, "run": run})
		data["runs"] = runs
		if e := save(path, data); e != nil {
			return e
		}
		fmt.Printf("%s %s %d: %dms", q.ID, phase, n, run.TotalMS)
		for _, a := range run.Arms {
			fmt.Printf(" %s=%s/%s", a.Provider, a.Result.Status, a.Result.AbstentionReason)
		}
		fmt.Println()
		return nil
	}
	for i, q := range qs {
		phase := "initial_pass"
		if i == 0 {
			phase = "first_application_request"
		}
		if e := execute(q, phase, 1); e != nil {
			return e
		}
	}
	for _, id := range plan.IDs {
		for _, q := range qs {
			if q.ID == id {
				for n := 1; n <= plan.N; n++ {
					if e := execute(q, "repeat", n); e != nil {
						return e
					}
				}
			}
		}
	}
	data["completed_at"] = time.Now().UTC()
	after, e := experiment.FileHash(r.Agent.IndexPath)
	if e != nil {
		return e
	}
	data["index_sha256_after"] = after
	return save(path, data)
}
