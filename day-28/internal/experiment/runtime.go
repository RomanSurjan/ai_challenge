package experiment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"ai-challenge/day-28/internal/agent"
	"ai-challenge/day-28/internal/embedding"
	"ai-challenge/day-28/internal/evidence"
	"ai-challenge/day-28/internal/generation"
	"ai-challenge/day-28/internal/indexstore"
	"ai-challenge/day-28/internal/judging"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type Call struct {
	Messages        []Message           `json:"messages"`
	MessagesHash    string              `json:"messages_sha256"`
	Response        generation.Response `json:"response"`
	RequestedModel  string              `json:"requested_model"`
	ActualModel     string              `json:"actual_model"`
	SchemaHash      string              `json:"schema_sha256"`
	WallMS          int64               `json:"wall_ms"`
	EvalTokens      int                 `json:"eval_tokens,omitempty"`
	EvalNS          int64               `json:"eval_duration_ns,omitempty"`
	TokensPerSecond float64             `json:"tokens_per_second,omitempty"`
	Error           string              `json:"error,omitempty"`
}
type Arm struct {
	Provider string       `json:"provider"`
	Model    string       `json:"model"`
	Calls    []Call       `json:"calls"`
	Result   agent.Result `json:"result"`
}
type Run struct {
	ID           string    `json:"id"`
	Question     string    `json:"question"`
	CreatedAt    time.Time `json:"created_at"`
	ContextHash  string    `json:"context_sha256"`
	MessagesHash string    `json:"messages_sha256"`
	IndexHash    string    `json:"index_sha256"`
	CloudState   string    `json:"cloud_state"`
	Arms         []Arm     `json:"arms"`
	TotalMS      int64     `json:"wall_total_ms"`
	Error        string    `json:"error,omitempty"`
}
type Runtime struct {
	CloudEnabled bool

	Agent      agent.Agent
	Base       string
	IndexHash  string
	Key        string
	CloudModel string
	CloudURL   string
	Client     *http.Client
}

func Hash(v any) string {
	b, _ := json.Marshal(v)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
func FileHash(path string) (string, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return "", e
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:]), nil
}
func New(indexPath, base, model string, cloud bool) (*Runtime, error) {
	u, e := url.Parse(base)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("Ollama URL must be loopback HTTP origin")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("Ollama must use literal loopback IP")
	}
	idx, e := indexstore.Load(indexPath)
	if e != nil {
		return nil, e
	}
	hash, e := FileHash(indexPath)
	if e != nil {
		return nil, e
	}
	base = strings.TrimRight(base, "/")
	// Disable proxies and redirects for every local model call.
	client := &http.Client{Timeout: 180 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	emb := embedding.NewOllama(base+"/api/embed", 180*time.Second)
	emb.Client = client
	gen := generation.NewOllama(base+"/api/chat", 180*time.Second)
	gen.Client = client
	r := &Runtime{CloudEnabled: cloud, Base: base, IndexHash: hash, CloudModel: env("DEEPSEEK_MODEL", "deepseek-chat"), CloudURL: strings.TrimRight(env("DEEPSEEK_BASE_URL", "https://api.deepseek.com"), "/"), Client: client}
	if cloud {
		r.Key = strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	}
	r.Agent = agent.Agent{Index: &idx, IndexPath: indexPath, Embedder: emb, Generator: gen, Judge: judging.NewOllama(gen, model, 128), Validator: evidence.Validator{MinQuoteRunes: 20, MaxQuoteRunes: 160}, EmbedModel: idx.Model, ChatModel: model, Settings: generation.DefaultSettings(), Pipeline: agent.PipelineConfig{CandidateK: 20, FinalK: 5, MinSimilarity: 0.45, AnswerMinRelevance: 0.55, Alpha: 0.7, Beta: 0.2, Gamma: 0.1}}
	return r, nil
}
func env(k, d string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return d
}
func (r *Runtime) CloudState() string {
	if !r.CloudEnabled {
		return "disabled"
	}
	if r.Key == "" {
		return "not_configured"
	}
	return "configured_unverified"
}
func (r *Runtime) Metadata(ctx context.Context) map[string]any {
	m := map[string]any{"index_sha256": r.IndexHash, "index": map[string]any{"corpus_id": r.Agent.Index.CorpusID, "model": r.Agent.Index.Model, "dimension": r.Agent.Index.EmbeddingDimension, "stats": r.Agent.Index.Stats}, "local_model": r.Agent.ChatModel, "ollama": r.Base, "cloud_state": r.CloudState(), "cloud_model": r.CloudModel, "pipeline": r.Agent.Pipeline, "mode": "stateless", "format": map[string]string{"local": "json_schema", "cloud": "json_object"}, "temperature": 0, "max_tokens": 512, "num_ctx": 32768}
	for _, p := range []string{"tags", "version"} {
		req, _ := http.NewRequestWithContext(ctx, "GET", r.Base+"/api/"+p, nil)
		resp, e := r.Client.Do(req)
		if e != nil {
			m["ollama_error"] = "Ollama недоступен. Запустите ollama serve; облачный fallback отсутствует."
			continue
		}
		var v any
		e = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&v)
		resp.Body.Close()
		if e == nil && resp.StatusCode == 200 {
			m[p] = v
		}
	}
	return m
}
func (r *Runtime) Ask(ctx context.Context, question, mode string) Run {
	start := time.Now()
	run := Run{ID: fmt.Sprintf("%d", start.UnixNano()), Question: question, CreatedAt: start.UTC(), IndexHash: r.IndexHash, CloudState: r.CloudState(), Arms: []Arm{}}
	p, e := r.Agent.Prepare(ctx, question)
	if e != nil {
		run.Error = "Локальный embedding/retrieval не выполнен: " + e.Error()
		run.TotalMS = time.Since(start).Milliseconds()
		return run
	}
	run.ContextHash = Hash(p.Chunks())
	// Hash the exact initial shared messages even when the gate skips generation.
	prompt := agent.StructuredPrompt(question, p.Chunks(), 20, 160)
	run.MessagesHash = Hash([]Message{{"system", r.Agent.Settings.System}, {"user", prompt}})
	providers := []string{"local"}
	if mode == "compare" && r.Key != "" {
		providers = append(providers, "cloud")
	}
	for _, provider := range providers {
		a := r.Agent
		rec := &Recorder{Runtime: r, Provider: provider, Calls: []Call{}}
		a.Generator = rec
		if provider == "cloud" {
			a.ChatModel = r.CloudModel
		}
		result := a.Answer(ctx, p)
		run.Arms = append(run.Arms, Arm{Provider: provider, Model: a.ChatModel, Calls: rec.Calls, Result: result})
	}
	run.TotalMS = time.Since(start).Milliseconds()
	return run
}

type Recorder struct {
	Runtime  *Runtime
	Provider string
	Calls    []Call
}

func (g *Recorder) Generate(ctx context.Context, model, prompt string, s generation.Settings) (generation.Response, error) {
	c := Call{RequestedModel: model, Messages: []Message{{"system", s.System}, {"user", prompt}}}
	c.MessagesHash = Hash(c.Messages)
	c.SchemaHash = Hash(s.JSONSchema)
	start := time.Now()
	payload := map[string]any{"model": model, "messages": c.Messages, "stream": false}
	endpoint := g.Runtime.Base + "/api/chat"
	client := g.Runtime.Client
	if g.Provider == "local" {
		// Preserve Day 24 constrained decoding locally; cloud supports JSON object.
		payload["format"] = s.JSONSchema
		payload["options"] = map[string]any{"temperature": s.Temperature, "num_predict": s.MaxTokens, "num_ctx": 32768, "seed": 0}
	} else {
		endpoint = g.Runtime.CloudURL + "/chat/completions"
		payload["response_format"] = map[string]string{"type": "json_object"}
		payload["temperature"] = s.Temperature
		payload["max_tokens"] = s.MaxTokens
		client = &http.Client{Timeout: 180 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	}
	b, _ := json.Marshal(payload)
	req, e := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(b))
	if e == nil {
		req.Header.Set("Content-Type", "application/json")
		if g.Provider == "cloud" {
			req.Header.Set("Authorization", "Bearer "+g.Runtime.Key)
		}
		var resp *http.Response
		resp, e = client.Do(req)
		if e == nil {
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
			resp.Body.Close()
			e = readErr
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				e = fmt.Errorf("%s API HTTP %d (response body withheld)", g.Provider, resp.StatusCode)
			}
			if e == nil {
				var v struct {
					Model   string  `json:"model"`
					Message Message `json:"message"`
					Prompt  int     `json:"prompt_eval_count"`
					Count   int     `json:"eval_count"`
					EvalNS  int64   `json:"eval_duration"`
					Error   string  `json:"error"`
					Choices []struct {
						Message Message `json:"message"`
					} `json:"choices"`
					Usage generation.Usage `json:"usage"`
				}
				e = json.Unmarshal(body, &v)
				if e == nil {
					c.ActualModel = v.Model
					if v.Error != "" {
						e = fmt.Errorf("local model API returned an error")
					}
					if g.Provider == "local" {
						c.Response = generation.Response{Text: strings.TrimSpace(v.Message.Content), Usage: generation.Usage{PromptTokens: v.Prompt, CompletionTokens: v.Count}}
						c.EvalTokens = v.Count
						c.EvalNS = v.EvalNS
						if v.EvalNS > 0 {
							c.TokensPerSecond = float64(v.Count) * 1e9 / float64(v.EvalNS)
						}
					} else if len(v.Choices) > 0 {
						c.Response = generation.Response{Text: strings.TrimSpace(v.Choices[0].Message.Content), Usage: v.Usage}
					}
				}
			}
		}
	}
	if e == nil && c.Response.Text == "" {
		e = fmt.Errorf("%s API returned empty answer", g.Provider)
	}
	c.WallMS = time.Since(start).Milliseconds()
	if e != nil {
		c.Error = e.Error()
	}
	g.Calls = append(g.Calls, c)
	return c.Response, e
}
