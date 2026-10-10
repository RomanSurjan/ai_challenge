package experiment

import (
	"ai-challenge/day-29/internal/agent"
	"ai-challenge/day-29/internal/embedding"
	"ai-challenge/day-29/internal/evidence"
	"ai-challenge/day-29/internal/generation"
	"ai-challenge/day-29/internal/indexstore"
	"ai-challenge/day-29/internal/judging"
	"ai-challenge/day-29/internal/retrieval"
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
)

type Profile struct {
	ID            string  `json:"id"`
	Label         string  `json:"label"`
	Model         string  `json:"model"`
	Temperature   float64 `json:"temperature"`
	MaxTokens     int     `json:"max_tokens"`
	ContextWindow int     `json:"context_window"`
	Seed          int     `json:"seed"`
	PromptVersion string  `json:"prompt_version"`
}

func (p Profile) Settings() generation.Settings {
	s := generation.DefaultSettings()
	s.Temperature = p.Temperature
	s.MaxTokens = p.MaxTokens
	s.ContextWindow = p.ContextWindow
	s.Seed = p.Seed
	s.PromptVersion = p.PromptVersion
	return s
}
func LoadProfile(path string) (Profile, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return Profile{}, e
	}
	var p Profile
	e = json.Unmarshal(b, &p)
	if e == nil && (p.ID == "" || p.Model == "" || p.MaxTokens <= 0 || p.ContextWindow <= 0 || p.Temperature < 0 || (p.PromptVersion != "baseline-v1" && p.PromptVersion != "technical-v2" && p.PromptVersion != "technical-v3" && p.PromptVersion != "technical-v4" && p.PromptVersion != "technical-v5")) {
		e = fmt.Errorf("invalid profile %s", path)
	}
	return p, e
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type Call struct {
	Messages        []Message           `json:"messages"`
	MessagesHash    string              `json:"messages_sha256"`
	Payload         map[string]any      `json:"payload"`
	SchemaHash      string              `json:"schema_sha256"`
	Response        generation.Response `json:"response"`
	Raw             json.RawMessage     `json:"raw_response,omitempty"`
	RequestedModel  string              `json:"requested_model"`
	ActualModel     string              `json:"actual_model"`
	WallMS          int64               `json:"wall_ms"`
	InputTokens     int                 `json:"input_tokens_budget"`
	BudgetMethod    string              `json:"token_budget_method"`
	TokenizerError  string              `json:"tokenizer_error,omitempty"`
	InputUpperBound int                 `json:"input_token_upper_bound"`
	LoadNS          int64               `json:"load_duration"`
	PromptNS        int64               `json:"prompt_eval_duration"`
	PromptCount     int                 `json:"prompt_eval_count"`
	EvalTokens      int                 `json:"eval_count"`
	EvalNS          int64               `json:"eval_duration"`
	TokensPerSecond float64             `json:"tokens_per_second"`
	DoneReason      string              `json:"done_reason"`
	Error           string              `json:"error,omitempty"`
}
type Arm struct {
	Provider      string       `json:"provider"`
	Profile       Profile      `json:"profile"`
	Model         string       `json:"model"`
	ModelMetadata any          `json:"model_metadata"`
	Calls         []Call       `json:"calls"`
	Result        agent.Result `json:"result"`
	Resources     Resources    `json:"resources"`
}
type Run struct {
	ID                  string             `json:"id"`
	Question            string             `json:"question"`
	CreatedAt           time.Time          `json:"created_at"`
	ObservedContext     []retrieval.Result `json:"observed_retrieval_context"`
	ObservedContextHash string             `json:"observed_retrieval_sha256"`
	ContextFrozen       bool               `json:"context_frozen_from_first_repetition"`
	ContextHash         string             `json:"context_sha256"`
	IndexHash           string             `json:"index_sha256"`
	Arms                []Arm              `json:"arms"`
	TotalMS             int64              `json:"wall_total_ms"`
	Error               string             `json:"error,omitempty"`
}
type Runtime struct {
	Agent     agent.Agent
	Base      string
	IndexHash string
	Client    *http.Client
	Profiles  map[string]Profile
	Tokenizer generation.TokenCounter
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
func New(indexPath, base string) (*Runtime, error) {
	u, e := url.Parse(base)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("Ollama URL must be loopback HTTP origin")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("literal loopback required")
	}
	base = strings.TrimRight(base, "/")
	idx, e := indexstore.Load(indexPath)
	if e != nil {
		return nil, e
	}
	h, e := FileHash(indexPath)
	if e != nil {
		return nil, e
	}
	c := &http.Client{Timeout: 180 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	emb := embedding.NewOllama(base+"/api/embed", 180*time.Second)
	emb.Client = c
	gen := generation.NewOllama(base+"/api/chat", 180*time.Second)
	gen.Client = c
	gen.Tokenizer = generation.RunnerTokenizer(base, c)
	r := &Runtime{Base: base, IndexHash: h, Client: c, Profiles: map[string]Profile{}}
	for _, id := range []string{"baseline", "selected"} {
		p, e := LoadProfile("configs/" + id + ".json")
		if e != nil {
			return nil, e
		}
		r.Profiles[id] = p
	}
	r.Tokenizer = gen.Tokenizer
	r.Agent = agent.Agent{Index: &idx, IndexPath: indexPath, Embedder: emb, Generator: gen, Judge: judging.NewOllama(gen, "qwen2.5:3b", 128), Validator: evidence.Validator{MinQuoteRunes: 20, MaxQuoteRunes: 160}, EmbedModel: idx.Model, ChatModel: "qwen2.5:3b", Settings: generation.DefaultSettings(), Pipeline: agent.PipelineConfig{CandidateK: 20, FinalK: 5, MinSimilarity: 0.45, AnswerMinRelevance: 0.55, Alpha: 0.7, Beta: 0.2, Gamma: 0.1}}
	return r, nil
}
func (r *Runtime) Get(ctx context.Context, path string) (any, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", r.Base+"/api/"+path, nil)
	resp, e := r.Client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	var v any
	e = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&v)
	if e == nil && resp.StatusCode != 200 {
		e = fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return v, e
}
func (r *Runtime) ModelMetadata(ctx context.Context, model string) any {
	v, e := r.Get(ctx, "tags")
	if e != nil {
		return map[string]any{"error": e.Error()}
	}
	m, _ := v.(map[string]any)
	xs, _ := m["models"].([]any)
	for _, x := range xs {
		if d, ok := x.(map[string]any); ok && d["name"] == model {
			payload, _ := json.Marshal(map[string]string{"model": model})
			req, _ := http.NewRequestWithContext(ctx, "POST", r.Base+"/api/show", bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			if resp, e := r.Client.Do(req); e == nil {
				var show struct {
					Modelfile string `json:"modelfile"`
				}
				json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&show)
				resp.Body.Close()
				for _, line := range strings.Split(show.Modelfile, "\n") {
					if strings.HasPrefix(line, "FROM ") {
						path := strings.TrimSpace(strings.TrimPrefix(line, "FROM "))
						if strings.Contains(path, "/.ollama/models/blobs/sha256-") {
							if st, e := os.Stat(path); e == nil {
								d["weights_file_bytes"] = st.Size()
							}
						}
					}
				}
			}
			return d
		}
	}
	return map[string]any{"error": "model not installed", "name": model}
}
func (r *Runtime) Metadata(ctx context.Context) map[string]any {
	m := map[string]any{"profiles": r.Profiles, "index_sha256": r.IndexHash, "pipeline": r.Agent.Pipeline, "embedding_model": r.Agent.EmbedModel, "judge": map[string]any{"model": "qwen2.5:3b", "temperature": 0, "max_tokens": 128, "num_ctx": 32768, "seed": 0}, "token_budget_method": "exact matching loaded runner /tokenize + verified ChatML; conservative UTF8 bound fallback recorded per call"}
	for _, p := range []string{"tags", "version"} {
		v, e := r.Get(ctx, p)
		if e != nil {
			m["ollama_error"] = e.Error()
		} else {
			m[p] = v
		}
	}
	return m
}
func (r *Runtime) Ask(ctx context.Context, q, mode string) Run {
	ps := []Profile{r.Profiles[mode]}
	if mode == "compare" {
		ps = []Profile{r.Profiles["baseline"], r.Profiles["selected"]}
	}
	return r.AskProfiles(ctx, q, ps)
}
func (r *Runtime) AskProfiles(ctx context.Context, q string, ps []Profile) Run {
	run, _ := r.AskProfilesFixed(ctx, q, ps, nil)
	return run
}
func (r *Runtime) AskProfilesFixed(ctx context.Context, q string, ps []Profile, ref *agent.Prepared) (Run, agent.Prepared) {
	start := time.Now()
	run := Run{ID: fmt.Sprint(start.UnixNano()), Question: q, CreatedAt: start.UTC(), IndexHash: r.IndexHash, Arms: []Arm{}}
	p, e := r.Agent.Prepare(ctx, q)
	if e != nil {
		run.Error = e.Error()
		run.TotalMS = time.Since(start).Milliseconds()
		return run, agent.Prepared{}
	}
	snapshot := p
	run.ObservedContext = p.Chunks()
	run.ObservedContextHash = Hash(p.Chunks())
	if ref != nil {
		p = p.WithFrozenContext(*ref)
		run.ContextFrozen = true
	}
	run.ContextHash = Hash(p.Chunks())
	for _, profile := range ps {
		a := r.Agent
		a.ChatModel = profile.Model
		a.Settings = profile.Settings()
		if strings.HasPrefix(profile.PromptVersion, "technical-") {
			a.Validator.MaxClaims = 3
			a.Validator.AllowInsufficient = true
			a.Validator.RequireInline = true
		}
		rec := &Recorder{Runtime: r, Calls: []Call{}}
		a.Generator = rec
		meta := r.ModelMetadata(ctx, profile.Model)
		sampler := StartSampler(ctx, r, profile.Model)
		result := a.Answer(ctx, p)
		resources := sampler.Stop()
		run.Arms = append(run.Arms, Arm{Provider: profile.ID, Profile: profile, Model: profile.Model, ModelMetadata: meta, Calls: rec.Calls, Result: result, Resources: resources})
	}
	run.TotalMS = time.Since(start).Milliseconds()
	return run, snapshot
}
