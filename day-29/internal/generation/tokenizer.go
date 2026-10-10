package generation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type TokenCounter func(context.Context, string, string, string) (int, error)

func ChatML(system, prompt string) string {
	return "<|im_start|>system\n" + system + "<|im_end|>\n<|im_start|>user\n" + prompt + "<|im_end|>\n<|im_start|>assistant\n"
}

// RunnerTokenizer uses the already loaded, exact model's local /tokenize API.
// It never starts a model or sends a generation. Unsupported/absent runner
// falls back to the conservative byte-BPE bound in Budget.
func RunnerTokenizer(base string, client *http.Client) TokenCounter {
	return func(ctx context.Context, model, system, prompt string) (int, error) {
		body, _ := json.Marshal(map[string]any{"model": model})
		req, _ := http.NewRequestWithContext(ctx, "POST", base+"/api/show", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, e := client.Do(req)
		if e != nil {
			return 0, e
		}
		var meta struct {
			Modelfile string `json:"modelfile"`
			Details   struct {
				Family string `json:"family"`
			} `json:"details"`
		}
		e = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&meta)
		resp.Body.Close()
		if e != nil || resp.StatusCode != 200 || meta.Details.Family != "qwen2" {
			return 0, fmt.Errorf("unverified model tokenizer")
		}
		blob := ""
		for _, line := range strings.Split(meta.Modelfile, "\n") {
			if strings.HasPrefix(line, "FROM ") {
				blob = filepath.Base(strings.TrimSpace(strings.TrimPrefix(line, "FROM ")))
			}
		}
		if !strings.HasPrefix(blob, "sha256-") {
			return 0, fmt.Errorf("model blob not identified")
		}
		out, e := exec.CommandContext(ctx, "ps", "-A", "-o", "command=").Output()
		if e != nil {
			return 0, e
		}
		port := 0
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) == 0 || !strings.HasSuffix(f[0], "/llama-server") || strings.Contains(line, " --embedding ") {
				continue
			}
			matched, chatml := false, false
			p := 0
			for i := 1; i+1 < len(f); i++ {
				if f[i] == "--model" && filepath.Base(f[i+1]) == blob {
					matched = true
				}
				if f[i] == "--chat-template" && f[i+1] == "chatml" {
					chatml = true
				}
				if f[i] == "--port" {
					p, _ = strconv.Atoi(f[i+1])
				}
			}
			if matched && chatml && p > 0 && p < 65536 {
				port = p
				break
			}
		}
		if port == 0 {
			return 0, fmt.Errorf("matching loaded ChatML runner unavailable")
		}
		b, _ := json.Marshal(map[string]any{"content": ChatML(system, prompt), "add_special": true})
		req, _ = http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("http://127.0.0.1:%d/tokenize", port), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		resp, e = client.Do(req)
		if e != nil {
			return 0, e
		}
		defer resp.Body.Close()
		var result struct {
			Tokens []int `json:"tokens"`
		}
		e = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&result)
		if e != nil {
			return 0, e
		}
		if resp.StatusCode != 200 || len(result.Tokens) == 0 {
			return 0, fmt.Errorf("local tokenize failed HTTP %d", resp.StatusCode)
		}
		return len(result.Tokens), nil
	}
}
func Budget(ctx context.Context, model, system, prompt string, s Settings, counter TokenCounter) (int, string, string, error) {
	count := InputBound(system, prompt)
	method := "utf8_byte_bpe_upper_bound"
	counterErr := ""
	if counter != nil {
		n, e := counter(ctx, model, system, prompt)
		if e == nil {
			count = n
			method = "exact_loaded_runner_chatml"
		} else {
			counterErr = e.Error()
		}
	}
	if !strings.HasPrefix(model, "qwen2.5:") {
		return count, method, counterErr, fmt.Errorf("context budget supports verified Qwen2.5 only")
	}
	if s.ContextWindow <= 0 || s.ContextWindow > 32768 || count+s.MaxTokens > s.ContextWindow {
		return count, method, counterErr, fmt.Errorf("context_window_unfit: %s input %d + output %d exceeds num_ctx %d; input not sent or truncated", method, count, s.MaxTokens, s.ContextWindow)
	}
	return count, method, counterErr, nil
}
