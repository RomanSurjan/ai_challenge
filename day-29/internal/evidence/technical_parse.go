package evidence

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ParseTechnical enforces the required wire fields independently of decoder schema.
func ParseTechnical(raw string) (Response, error) {
	r, e := Parse(raw)
	if e != nil {
		return r, e
	}
	var wire map[string]json.RawMessage
	if e = json.Unmarshal([]byte(raw), &wire); e != nil {
		return r, e
	}
	for _, k := range []string{"status", "answer", "claims", "clarification_question"} {
		if _, ok := wire[k]; !ok {
			return r, fmt.Errorf("missing required field %s", k)
		}
	}
	var claims []struct {
		ID       string `json:"id"`
		Text     string `json:"text"`
		Evidence []struct {
			Source string `json:"source_id"`
			Quote  string `json:"quote"`
		} `json:"evidence"`
	}
	if e = json.Unmarshal(wire["claims"], &claims); e != nil {
		return r, e
	}
	if string(wire["claims"]) == "null" {
		return r, fmt.Errorf("claims must be an array")
	}
	if utf8.RuneCountInString(r.Answer) > 1400 || utf8.RuneCountInString(r.ClarificationQuestion) > 200 {
		return r, fmt.Errorf("technical text length exceeds contract")
	}
	for _, c := range claims {
		if !regexp.MustCompile(`^C[123]$`).MatchString(c.ID) || utf8.RuneCountInString(c.Text) > 350 || len(c.Evidence) < 1 || len(c.Evidence) > 2 {
			return r, fmt.Errorf("invalid claim shape")
		}
	}
	return r, nil
}

// ParseDraft removes duplicated output text, not evidence checks: the complete
// inherited Validator still verifies every claim, source ID and exact quote.
func ParseDraft(raw string) (Response, error) {
	r, e := Parse(raw)
	if e != nil {
		return r, e
	}
	var wire map[string]json.RawMessage
	if e = json.Unmarshal([]byte(raw), &wire); e != nil {
		return r, e
	}
	if len(wire) != 3 {
		return r, fmt.Errorf("draft requires exactly status/claims/clarification_question")
	}
	for _, k := range []string{"status", "claims", "clarification_question"} {
		if _, ok := wire[k]; !ok {
			return r, fmt.Errorf("missing field %s", k)
		}
	}
	if string(wire["claims"]) == "null" || len(r.Claims) > 3 || utf8.RuneCountInString(r.ClarificationQuestion) > 200 {
		return r, fmt.Errorf("invalid draft shape")
	}
	var cs []struct {
		ID       string `json:"id"`
		Text     string `json:"text"`
		Evidence []struct {
			SourceID string `json:"source_id"`
			Quote    string `json:"quote"`
		} `json:"evidence"`
	}
	if e = json.Unmarshal(wire["claims"], &cs); e != nil {
		return r, e
	}
	var b strings.Builder
	for _, c := range cs {
		if !regexp.MustCompile(`^C[123]$`).MatchString(c.ID) || utf8.RuneCountInString(c.Text) > 280 || len(c.Evidence) < 1 || len(c.Evidence) > 2 {
			return r, fmt.Errorf("invalid draft claim")
		}
		b.WriteString(c.Text)
		seen := map[string]bool{}
		for _, q := range c.Evidence {
			if !seen[q.SourceID] {
				b.WriteString(" [" + q.SourceID + "]")
				seen[q.SourceID] = true
			}
		}
		b.WriteString("\n")
	}
	if r.ClarificationQuestion != "" {
		b.WriteString(r.ClarificationQuestion)
	}
	r.Answer = strings.TrimSpace(b.String())
	return r, nil
}
