package experiment

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ResourceSample struct {
	At        time.Time `json:"at"`
	PS        any       `json:"api_ps,omitempty"`
	PSerror   string    `json:"api_ps_error,omitempty"`
	Processes []Process `json:"processes"`
	RSSerror  string    `json:"rss_error,omitempty"`
}
type Process struct {
	PID       int    `json:"pid"`
	RSSBytes  int64  `json:"rss_bytes"`
	Command   string `json:"command"`
	ModelBlob string `json:"model_blob,omitempty"`
}
type Resources struct {
	IntervalMS        int              `json:"interval_ms"`
	Samples           []ResourceSample `json:"samples"`
	MaxRSSBytes       int64            `json:"max_observed_process_rss_bytes"`
	TargetModel       string           `json:"target_model"`
	TargetBlob        string           `json:"target_blob"`
	TargetMaxRSSBytes int64            `json:"target_runner_max_rss_bytes"`
	Utilization       string           `json:"utilization"`
}
type Sampler struct {
	cancel context.CancelFunc
	done   chan Resources
}

func StartSampler(parent context.Context, r *Runtime, model string) *Sampler {
	ctx, cancel := context.WithCancel(parent)
	s := &Sampler{cancel: cancel, done: make(chan Resources, 1)}
	go func() {
		payload, _ := json.Marshal(map[string]string{"model": model})
		req, _ := http.NewRequestWithContext(ctx, "POST", r.Base+"/api/show", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		blob := ""
		if resp, e := r.Client.Do(req); e == nil {
			var meta struct {
				Modelfile string `json:"modelfile"`
			}
			json.NewDecoder(resp.Body).Decode(&meta)
			resp.Body.Close()
			for _, line := range strings.Split(meta.Modelfile, "\n") {
				if strings.HasPrefix(line, "FROM ") {
					blob = filepath.Base(strings.TrimSpace(strings.TrimPrefix(line, "FROM ")))
				}
			}
		}
		res := Resources{TargetModel: model, TargetBlob: blob, IntervalMS: 250, Samples: []ResourceSample{}, Utilization: "CPU/GPU utilization unavailable; RSS and size_vram are not added (unified memory)"}
		sample := func() {
			c, stop := context.WithTimeout(parent, 2*time.Second)
			defer stop()
			x := ResourceSample{At: time.Now().UTC(), Processes: []Process{}}
			v, e := r.Get(c, "ps")
			x.PS = v
			if e != nil {
				x.PSerror = e.Error()
			}
			out, e := exec.CommandContext(c, "ps", "-A", "-o", "pid=,rss=,command=").Output()
			if e != nil {
				x.RSSerror = e.Error()
			} else {
				for _, line := range strings.Split(string(out), "\n") {

					f := strings.Fields(line)
					if len(f) < 3 || !strings.Contains(strings.ToLower(f[2]), "ollama") {
						continue
					}
					pid, _ := strconv.Atoi(f[0])
					rss, _ := strconv.ParseInt(f[1], 10, 64)
					rss *= 1024
					processBlob := ""
					for i := 3; i+1 < len(f); i++ {
						if f[i] == "--model" {
							processBlob = filepath.Base(f[i+1])
						}
					}
					x.Processes = append(x.Processes, Process{PID: pid, RSSBytes: rss, Command: strings.Join(f[2:], " "), ModelBlob: processBlob})
					if blob != "" && processBlob == blob && rss > res.TargetMaxRSSBytes {
						res.TargetMaxRSSBytes = rss
					}
					if rss > res.MaxRSSBytes {
						res.MaxRSSBytes = rss
					}
				}
			}
			res.Samples = append(res.Samples, x)
		}
		sample()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				sample()
				s.done <- res
				return
			case <-ticker.C:
				sample()
			}
		}
	}()
	return s
}
func (s *Sampler) Stop() Resources { s.cancel(); return <-s.done }
