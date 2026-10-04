package evaluation

import (
	"fmt"
	"strings"
	"time"

	"ai-challenge/day-25/internal/agent"
	"ai-challenge/day-25/internal/judging"
)

func Markdown(r Report, abstention *AbstentionReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Day 24 — источники, точные цитаты и безопасный отказ\n\nRun: `%s`  \nModels: answer `%s`, judge `%s`, embedding `%s`  \nIndex: `%s` (corpus `%s`)  \nDataset: `%s`, SHA-256 `%s`  \nPipeline: candidate-K %d, final-K %d, chunk threshold %.2f, answer threshold %.2f, weights %.2f / %.2f / %.2f  \nQuotes: %d–%d Unicode characters\n\n", r.GeneratedAt.Format(time.RFC3339), r.ChatModel, r.JudgeModel, r.EmbeddingModel, r.Index, r.IndexCorpusID, r.Dataset, r.DatasetSHA256, r.Pipeline.CandidateK, r.Pipeline.FinalK, r.Pipeline.MinSimilarity, r.Pipeline.AnswerMinRelevance, r.Pipeline.Alpha, r.Pipeline.Beta, r.Pipeline.Gamma, r.QuoteMinRunes, r.QuoteMaxRunes)
	b.WriteString("## Answer quality and retrieval\n\n| Mode | Concepts | All concepts | Answered | Abstained | False refusal | Candidate recall | Final recall | Precision | Hit@1 | MRR | Top-1 cosine | Context |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, mode := range Modes {
		s := r.Summary[mode]
		fmt.Fprintf(&b, "| %s | %.3f | %.3f | %d | %d | %.3f | %.3f | %.3f | %.3f | %.3f | %.3f | %.3f | %.2f |\n", mode, s.ConceptCoverage, s.AllConceptsRate, s.Answered, s.Abstained, s.FalseRefusalRate, s.CandidateRecall, s.FinalRecall, s.FinalPrecision, s.HitAt1, s.MRR, s.AverageTop1Cosine, s.AverageFinalContextSize)
	}
	b.WriteString("\n## Source, quote, and claim validation\n\n| Mode | Answer+source | Metadata complete | Valid source ID | Cited recall | Invalid IDs | Answer+quote | Claims+quote | Exact quotes | Invalid / wrong | Claims+source | Claims+evidence | Fully supported |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, mode := range Modes {
		s := r.Summary[mode]
		fmt.Fprintf(&b, "| %s | %.3f | %.3f | %.3f | %.3f | %d | %.3f | %.3f | %.3f | %d / %d | %.3f | %.3f | %.3f |\n", mode, s.AnsweredWithSourceRate, s.SourceMetadataCompletenessRate, s.ValidFinalContextSourceIDRate, s.CitedSourceRecall, s.InvalidSourceIDs, s.AnsweredWithQuoteRate, s.ClaimsWithQuoteRate, s.ExactSubstringQuoteRate, s.InvalidQuotes, s.WrongChunkQuotes, s.ClaimsWithSourceIDsRate, s.ClaimsWithValidatedEvidenceRate, s.FullySupportedAnswerRate)
	}
	b.WriteString("\n## Entailment proxy\n\n| Mode | Supported | Partial | Unsupported | Unverifiable |\n|---|---:|---:|---:|---:|\n")
	for _, mode := range Modes {
		s := r.Summary[mode]
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d |\n", mode, s.SupportedClaims, s.PartiallySupportedClaims, s.UnsupportedClaims, s.UnverifiableClaims)
	}
	b.WriteString("\nThe local LLM judge is an automatic proxy: it is a small model evaluating answers from the same or a closely related model, so its verdict is not final proof and should be complemented with human review.\n")
	b.WriteString("\n## Latency and tokens\n\n| Mode | Retrieval | Rerank | Generation | Validation | Judge | Total ms | Generator prompt/completion | Judge prompt/completion |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, mode := range Modes {
		s := r.Summary[mode]
		fmt.Fprintf(&b, "| %s | %.1f | %.1f | %.1f | %.1f | %.1f | %.1f | %.1f / %.1f | %.1f / %.1f |\n", mode, s.AverageRetrievalMS, s.AverageFilterRerankMS, s.AverageGenerationMS, s.AverageValidationMS, s.AverageJudgeMS, s.AverageTotalMS, s.AverageGeneratorPromptTokens, s.AverageGeneratorCompletionTokens, s.AverageJudgePromptTokens, s.AverageJudgeCompletionTokens)
	}
	b.WriteString("\n## Results for all 10 questions\n\n| ID | day23 concepts/status | grounded concepts/status | strict concepts/status | strict reason | strict supported/claims |\n|---|---|---|---|---|---:|\n")
	for _, q := range r.Results {
		d := q.Modes[agent.Day23]
		g := q.Modes[agent.Grounded]
		s := q.Modes[agent.Strict]
		supported := 0
		if s.Result != nil {
			for _, v := range s.Result.Entailment {
				if v.Verdict == judging.Supported {
					supported++
				}
			}
		}
		fmt.Fprintf(&b, "| %s | %.3f / %s | %.3f / %s | %.3f / %s | %s | %d/%d |\n", q.ID, d.Concepts.Coverage, status(d), g.Concepts.Coverage, status(g), s.Concepts.Coverage, status(s), reason(s), supported, claimCount(s))
	}

	supported, invalid, refusal := pickExamples(r, abstention)
	b.WriteString("\n## Detailed examples\n")
	if supported != nil {
		writeExample(&b, "Correctly supported answer", supported.question, supported.result)
	} else {
		b.WriteString("\nNo structured answer received only `supported` verdicts in this run.\n")
	}
	if invalid != nil {
		writeExample(&b, "Invalid or semantically weak evidence", invalid.question, invalid.result)
	} else {
		b.WriteString("\nNo invalid/unsupported structured example was observed in this run.\n")
	}
	if refusal != nil {
		writeExample(&b, "Safe refusal", refusal.question, refusal.result)
	} else {
		b.WriteString("\nNo safe refusal was observed in the available main or abstention report.\n")
	}

	b.WriteString("\n## Honest conclusion\n\n")
	day23, grounded, strict := r.Summary[agent.Day23], r.Summary[agent.Grounded], r.Summary[agent.Strict]
	fmt.Fprintf(&b, "The run does not assume a winner. Concept coverage was %.3f / %.3f / %.3f for day23 / grounded / strict. Exact-quote enforcement made accepted structured answers auditable, but invalid output is rejected rather than silently repaired. Strict abstained on %d of 10 in-domain questions at the calibrated threshold; grounded abstained %d times (empty context or validation failure). Fully supported answer rates were %.3f and %.3f for grounded and strict. Retrieval remains the limiting factor when the right implementation chunk is absent from final top-5.\n", day23.ConceptCoverage, grounded.ConceptCoverage, strict.ConceptCoverage, strict.Abstained, grounded.Abstained, grounded.FullySupportedAnswerRate, strict.FullySupportedAnswerRate)
	return b.String()
}

func status(m ModeResult) string {
	if m.Error != "" {
		return "error"
	}
	if m.Result == nil {
		return "missing"
	}
	return m.Result.Status
}
func reason(m ModeResult) string {
	if m.Result == nil {
		return ""
	}
	return m.Result.AbstentionReason
}
func claimCount(m ModeResult) int {
	if m.Result == nil {
		return 0
	}
	return len(m.Result.Claims)
}

type example struct {
	question string
	result   *agent.Result
}

func pickExamples(r Report, abst *AbstentionReport) (*example, *example, *example) {
	var supported, invalid, refusal *example
	for i := range r.Results {
		q := &r.Results[i]
		for _, mode := range []agent.Mode{agent.Strict, agent.Grounded} {
			mr := q.Modes[mode]
			if mr.Result == nil {
				continue
			}
			res := mr.Result
			all := len(res.Entailment) > 0
			for _, v := range res.Entailment {
				if v.Verdict != judging.Supported {
					all = false
				}
			}
			if supported == nil && res.Status == agent.StatusAnswered && all {
				supported = &example{q.Question, res}
			}
			weak := res.AbstentionReason == agent.ReasonValidationFailed || len(res.InvalidQuotes) > 0
			for _, v := range res.Entailment {
				if v.Verdict != judging.Supported {
					weak = true
				}
			}
			if invalid == nil && weak {
				invalid = &example{q.Question, res}
			}
		}
	}
	if abst != nil {
		for i := range abst.Results {
			res := &abst.Results[i].Result
			if res.Status == agent.StatusInsufficientContext {
				refusal = &example{abst.Results[i].Question, res}
				break
			}
		}
	}
	if refusal == nil {
		for i := range r.Results {
			mr := r.Results[i].Modes[agent.Strict]
			if mr.Result != nil && mr.Result.Status == agent.StatusInsufficientContext {
				refusal = &example{r.Results[i].Question, mr.Result}
				break
			}
		}
	}
	return supported, invalid, refusal
}

func writeExample(b *strings.Builder, title, question string, r *agent.Result) {
	fmt.Fprintf(b, "\n### %s\n\nOriginal question: %s\n\nStatus: `%s`; relevance `%.6f` vs threshold `%.6f`; reason `%s`.\n\nFinal context:\n\n", title, question, r.Status, r.Gate.Score, r.Gate.Threshold, r.AbstentionReason)
	for i, c := range r.FinalContext {
		fmt.Fprintf(b, "- S%d: `%s` — %s — `%s`; cosine `%.6f`\n\n  > %s\n", i+1, c.Source, c.Section, c.ChunkID, c.Score, oneLine(c.Text, 420))
	}
	fmt.Fprintf(b, "\nAnswer:\n\n%s\n\nClaims:\n\n", r.Answer)
	if len(r.Claims) == 0 {
		b.WriteString("- None.\n")
	} else {
		for _, c := range r.Claims {
			fmt.Fprintf(b, "- %s: %s — sources %s\n", c.ID, c.Text, strings.Join(c.SourceIDs, ", "))
		}
	}
	b.WriteString("\nVerified source metadata:\n\n")
	if len(r.Sources) == 0 {
		b.WriteString("- None.\n")
	} else {
		for _, s := range r.Sources {
			fmt.Fprintf(b, "- %s: `%s` — %s — `%s`; cosine `%.6f`, rerank `%.6f`\n", s.ID, s.Source, s.Section, s.ChunkID, s.Cosine, s.RerankScore)
		}
	}
	b.WriteString("\nExact quotes:\n\n")
	if len(r.Citations) == 0 {
		b.WriteString("- None.\n")
	} else {
		for _, c := range r.Citations {
			fmt.Fprintf(b, "- %s → %s, exact `%t`: “%s”\n", c.SourceID, strings.Join(c.ClaimIDs, ", "), c.ExactMatch, c.Quote)
		}
	}
	fmt.Fprintf(b, "\nValidation: %d attempt(s), %d final error(s).\n\nJudge verdicts:\n\n", len(r.ValidationAttempts), len(r.ValidationErrors))
	if len(r.Entailment) == 0 {
		b.WriteString("- None.\n")
	} else {
		for _, v := range r.Entailment {
			fmt.Fprintf(b, "- %s: `%s` — %s", v.ClaimID, v.Verdict, v.Reason)
			if v.Error != "" {
				fmt.Fprintf(b, " (error: %s)", v.Error)
			}
			b.WriteString("\n")
		}
	}
	if r.ClarificationQuestion != "" {
		fmt.Fprintf(b, "\nClarification: %s\n", r.ClarificationQuestion)
	}
}
func oneLine(value string, max int) string {
	value = strings.Join(strings.Fields(value), " ")
	r := []rune(value)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return value
}

func CalibrationMarkdown(r GateCalibrationReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Day 24 — answer gate calibration\n\nRun: `%s`  \nPositive: `%s` (`%s`)  \nNegative: `%s` (`%s`)  \nScore: %s.\n\nSelection rule fixed before analysis: %s.\n\n| Threshold | Positive answer | False refusal | Negative abstention | Unsafe answer | Precision | Recall | F1 |\n|---:|---:|---:|---:|---:|---:|---:|---:|\n", r.GeneratedAt.Format(time.RFC3339), r.PositiveDataset, r.PositiveDatasetSHA256, r.NegativeDataset, r.NegativeDatasetSHA256, r.ScoreFormula, r.SelectionRule)
	for _, v := range r.Values {
		fmt.Fprintf(&b, "| %.3f | %.3f | %.3f | %.3f | %.3f | %.3f | %.3f | %.3f |\n", v.Threshold, v.PositiveAnswerRate, v.FalseRefusalRate, v.NegativeAbstentionRate, v.UnsafeAnswerRate, v.Precision, v.Recall, v.F1)
	}
	fmt.Fprintf(&b, "\nSelected answer threshold: **%.3f**. Safety compromise: `%t`. Answer model calls during calibration: **%d**. Chunk threshold remains a separate fixed value (0.45).\n\n## Retrieval scores\n\n| ID | Expected sufficient | Score | Final chunks |\n|---|---:|---:|---:|\n", r.SelectedThreshold, r.SafetyCompromise, r.AnswerModelCalls)
	for _, item := range r.Items {
		fmt.Fprintf(&b, "| %s | %t | %.6f | %d |\n", item.ID, item.ExpectedSufficient, item.Score, item.FinalContextSize)
	}
	return b.String()
}

func AbstentionMarkdown(r AbstentionReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Day 24 — abstention evaluation\n\nRun: `%s`  \nDataset: `%s` (`%s`)  \nAnswer threshold: `%.3f`\n\n| Questions | Abstention rate | Unsafe answered | Model not called | Low relevance | Empty context | Validation failure | Runtime errors | Avg latency ms |\n|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n| %d | %.3f | %.3f | %d | %d | %d | %d | %d | %.1f |\n\n", r.GeneratedAt.Format(time.RFC3339), r.Dataset, r.DatasetSHA256, r.AnswerThreshold, r.Summary.Questions, r.Summary.AbstentionRate, r.Summary.UnsafeAnsweredRate, r.Summary.AnswerModelNotCalled, r.Summary.LowRelevance, r.Summary.EmptyContext, r.Summary.OutputValidationFailed, r.Summary.RuntimeErrors, r.Summary.AverageLatencyMS)
	for _, item := range r.Results {
		res := item.Result
		fmt.Fprintf(&b, "## %s\n\nQuestion: %s\n\nStatus `%s`, score `%.6f`, reason `%s`, answer model called `%t`.\n\nAnswer: %s\n\nClarification: %s\n\n", item.ID, item.Question, res.Status, res.Gate.Score, res.AbstentionReason, res.AnswerModelCalled, res.Answer, res.ClarificationQuestion)
	}
	return b.String()
}
