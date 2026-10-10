package main

import (
	"ai-challenge/day-29/internal/agent"
	"ai-challenge/day-29/internal/experiment"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

var datasetPath, profilePaths string
var repeats int

func benchmark(r *experiment.Runtime, path string) error {
	b, e := os.ReadFile(datasetPath)
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
	ps := []experiment.Profile{}
	for _, f := range strings.Split(profilePaths, ",") {
		p, e := experiment.LoadProfile(f)
		if e != nil {
			return e
		}
		ps = append(ps, p)
	}
	protocol, e := os.ReadFile("eval/protocol.json")
	if e != nil {
		return e
	}
	ctx := context.Background()
	data := map[string]any{"metadata": r.Metadata(ctx), "profiles": ps, "dataset": datasetPath, "dataset_sha256": experiment.Hash(json.RawMessage(b)), "protocol_sha256": experiment.Hash(json.RawMessage(protocol)), "started_at": time.Now().UTC(), "repeats": repeats, "warmups": []any{}, "runs": []any{}}
	// Explicit model-only warmup, excluded from measured results. No evaluation labels.
	warmups := []any{}
	for _, p := range ps {
		rec := &experiment.Recorder{Runtime: r}
		s := p.Settings()
		s.JSON = true
		s.JSONSchema = map[string]any{"type": "object", "properties": map[string]any{"ready": map[string]any{"type": "boolean"}}, "required": []string{"ready"}}
		_, _ = rec.Generate(ctx, p.Model, "Верни JSON ready=true", s)
		warmups = append(warmups, rec.Calls)
	}
	data["warmups"] = warmups
	runs := []any{}
	hashes := map[string]string{}
	if e = save(path, data); e != nil {
		return e
	}
	for i, q := range qs {
		var fixed *agent.Prepared
		for n := 0; n < repeats; n++ {
			order := append([]experiment.Profile(nil), ps...)
			if (i*repeats+n)%2 == 1 {
				for l, h := 0, len(order)-1; l < h; l, h = l+1, h-1 {
					order[l], order[h] = order[h], order[l]
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			run, prepared := r.AskProfilesFixed(ctx, q.Question, order, fixed)
			if fixed == nil && run.Error == "" {
				fixed = &prepared
			}
			cancel()
			runs = append(runs, map[string]any{"question_id": q.ID, "repetition": n + 1, "run": run})
			data["runs"] = runs
			if e = save(path, data); e != nil {
				return e
			}
			if old := hashes[q.ID]; old != "" && old != run.ContextHash {
				return fmt.Errorf("retrieval drift for %s", q.ID)
			}
			hashes[q.ID] = run.ContextHash
			fmt.Printf("%s repeat %d %dms", q.ID, n+1, run.TotalMS)
			for _, a := range run.Arms {
				fmt.Printf(" %s=%s/%s", a.Provider, a.Result.Status, a.Result.AbstentionReason)
			}
			fmt.Println()
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
