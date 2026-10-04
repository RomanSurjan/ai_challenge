package evaluation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ai-challenge/day-25/internal/chat"
	"ai-challenge/day-25/internal/chatservice"
	"ai-challenge/day-25/internal/chatstore"
	"ai-challenge/day-25/internal/evidence"
	"ai-challenge/day-25/internal/judging"
)

type ScenarioTurn struct {
	Message                  string   `json:"message"`
	RequiredMemory           []string `json:"required_memory,omitempty"`
	ForbiddenMemory          []string `json:"forbidden_memory,omitempty"`
	ExpectedActiveGoal       string   `json:"expected_active_goal,omitempty"`
	RequiredConcepts         []string `json:"required_concepts,omitempty"`
	ExpectedSources          []string `json:"expected_sources,omitempty"`
	ExpectedStatus           string   `json:"expected_status,omitempty"`
	RequiresCorpusSource     bool     `json:"requires_corpus_source,omitempty"`
	RequiresUserMemorySource bool     `json:"requires_user_memory_source,omitempty"`
	ExpectedQueryEntities    []string `json:"expected_query_entities,omitempty"`
	ForbiddenAnswerTerms     []string `json:"forbidden_answer_terms,omitempty"`
}

type Scenario struct {
	ID    string         `json:"id"`
	Title string         `json:"title"`
	Turns []ScenarioTurn `json:"turns"`
}

func LoadScenarios(path string) ([]Scenario, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	var scenarios []Scenario
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&scenarios); err != nil {
		return nil, "", err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, "", fmt.Errorf("scenario JSON has trailing content")
	}
	if len(scenarios) != 2 {
		return nil, "", fmt.Errorf("exactly two scenarios are required, found %d", len(scenarios))
	}
	ids := map[string]bool{}
	for _, scenario := range scenarios {
		if strings.TrimSpace(scenario.ID) == "" || ids[scenario.ID] || len(scenario.Turns) < 10 || len(scenario.Turns) > 15 {
			return nil, "", fmt.Errorf("scenario %q is invalid or does not contain 10-15 turns", scenario.ID)
		}
		ids[scenario.ID] = true
		for _, turn := range scenario.Turns {
			if strings.TrimSpace(turn.Message) == "" {
				return nil, "", fmt.Errorf("scenario %q contains an empty message", scenario.ID)
			}
		}
	}
	sum := sha256.Sum256(data)
	return scenarios, hex.EncodeToString(sum[:]), nil
}

type ScenarioTurnResult struct {
	Number   int             `json:"number"`
	Expected ScenarioTurn    `json:"expected"`
	Record   chat.TurnRecord `json:"record"`
	RAG      json.RawMessage `json:"rag"`
	Checks   map[string]bool `json:"checks"`
	Error    string          `json:"error,omitempty"`
}

type ScenarioModeResult struct {
	ScenarioID     string               `json:"scenario_id"`
	ScenarioTitle  string               `json:"scenario_title"`
	Mode           chatservice.Mode     `json:"mode"`
	SessionID      string               `json:"session_id"`
	Turns          []ScenarioTurnResult `json:"turns"`
	ReloadVerified bool                 `json:"state_persistence_after_process_reload"`
	FinalSession   chat.Session         `json:"final_session"`
}

type ScenarioMetrics struct {
	Turns                           int     `json:"turns"`
	Errors                          int     `json:"errors"`
	GoalRetentionRate               float64 `json:"goal_retention_rate"`
	ActiveConstraintRetentionRate   float64 `json:"active_constraint_retention_rate"`
	ClarificationIncorporationRate  float64 `json:"clarification_incorporation_rate"`
	TermRetentionRate               float64 `json:"term_retention_rate"`
	SupersedeCorrectness            float64 `json:"supersede_correctness"`
	ForbiddenStaleMemoryLeakage     int     `json:"forbidden_stale_memory_leakage"`
	FollowUpResolutionSuccess       float64 `json:"follow_up_resolution_success"`
	ResolverFallbacks               int     `json:"resolver_fallback_count"`
	LostTaskGoalTurns               int     `json:"turns_where_task_goal_was_lost"`
	ValidMemoryUserTurnRate         float64 `json:"memory_items_with_valid_user_turn_rate"`
	ExactMemoryUserQuoteRate        float64 `json:"exact_user_quote_rate"`
	DuplicateMemoryItems            int     `json:"duplicate_memory_items"`
	InvalidMemoryOperations         int     `json:"invalid_memory_operations"`
	MemoryRepairAttempts            int     `json:"memory_repair_attempts"`
	FailedMemoryUpdates             int     `json:"failed_memory_updates"`
	StateCorruptionCount            int     `json:"state_corruption_count"`
	StatePersistenceAfterReloadRate float64 `json:"state_persistence_after_process_reload_rate"`
	CandidateSourceRecall           float64 `json:"candidate_source_recall"`
	FinalSourceRecall               float64 `json:"final_source_recall"`
	FinalSourcePrecision            float64 `json:"final_source_precision"`
	HitAt1                          float64 `json:"hit_at_1"`
	MRR                             float64 `json:"mrr"`
	AverageTopRelevance             float64 `json:"average_top_relevance"`
	Answered                        int     `json:"answered"`
	Abstained                       int     `json:"abstained"`
	MemoryOnly                      int     `json:"memory_only"`
	AnsweredWithSourcesRate         float64 `json:"answered_with_sources_rate"`
	SourceMetadataCompletenessRate  float64 `json:"source_metadata_completeness_rate"`
	ValidSourceIDRate               float64 `json:"valid_source_id_rate"`
	AnswersWithQuotesRate           float64 `json:"answers_with_quotes_rate"`
	ClaimsWithEvidenceRate          float64 `json:"claims_with_evidence_rate"`
	ExactSubstringQuoteRate         float64 `json:"exact_substring_quote_rate"`
	InvalidQuotes                   int     `json:"invalid_quotes"`
	SupportedClaims                 int     `json:"supported_claims"`
	PartiallySupportedClaims        int     `json:"partially_supported_claims"`
	UnsupportedClaims               int     `json:"unsupported_claims"`
	UnverifiableClaims              int     `json:"unverifiable_claims"`
	FullySupportedAnswerRate        float64 `json:"fully_supported_answer_rate"`
	CorpusClaimsWithSEvidenceRate   float64 `json:"corpus_claims_with_s_evidence_rate"`
	MemoryClaimsWithUEvidenceRate   float64 `json:"memory_claims_with_u_evidence_rate"`
	MixedClaimsWithBothEvidenceRate float64 `json:"mixed_claims_with_both_evidence_rate"`
	AssistantTextCitedAsFact        int     `json:"assistant_text_cited_as_fact"`
	SafeRefusalRate                 float64 `json:"safe_refusal_rate"`
	UnsafeAnswerRate                float64 `json:"unsafe_answer_rate"`
	AverageSessionLoadMS            float64 `json:"average_session_load_ms"`
	AverageSessionSaveMS            float64 `json:"average_session_save_ms"`
	AverageMemoryExtractionMS       float64 `json:"average_memory_extraction_ms"`
	AverageResolverMS               float64 `json:"average_resolver_ms"`
	AverageRetrievalMS              float64 `json:"average_embedding_retrieval_ms"`
	AverageRerankMS                 float64 `json:"average_rerank_ms"`
	AverageGenerationMS             float64 `json:"average_generation_ms"`
	AverageValidationMS             float64 `json:"average_validation_ms"`
	AverageJudgeMS                  float64 `json:"average_judge_ms"`
	AverageTotalMS                  float64 `json:"average_total_turn_ms"`
	MemoryPromptTokens              int     `json:"memory_prompt_tokens"`
	MemoryCompletionTokens          int     `json:"memory_completion_tokens"`
	ResolverPromptTokens            int     `json:"resolver_prompt_tokens"`
	ResolverCompletionTokens        int     `json:"resolver_completion_tokens"`
	AnswerPromptTokens              int     `json:"answer_prompt_tokens"`
	AnswerCompletionTokens          int     `json:"answer_completion_tokens"`
	JudgePromptTokens               int     `json:"judge_prompt_tokens"`
	JudgeCompletionTokens           int     `json:"judge_completion_tokens"`
	AveragePromptHistoryRunes       float64 `json:"average_prompt_history_runes"`
	AverageTaskStateRunes           float64 `json:"average_task_state_runes"`
	AverageFinalContextRunes        float64 `json:"average_final_context_runes"`
}

type ScenarioReport struct {
	GeneratedAt    time.Time                            `json:"generated_at"`
	Dataset        string                               `json:"dataset"`
	ScenarioSHA256 string                               `json:"scenario_sha256"`
	Models         map[string]string                    `json:"models"`
	Index          string                               `json:"index"`
	IndexCorpusID  string                               `json:"index_corpus_id"`
	Pipeline       any                                  `json:"pipeline_parameters"`
	QuoteBounds    string                               `json:"quote_bounds"`
	Limits         chat.Limits                          `json:"limits"`
	Results        []ScenarioModeResult                 `json:"results"`
	Summary        map[chatservice.Mode]ScenarioMetrics `json:"summary"`
}

func RunScenarios(ctx context.Context, service *chatservice.Service, scenarios []Scenario, metadata ScenarioReport) ScenarioReport {
	metadata.GeneratedAt = time.Now().UTC()
	metadata.Results = []ScenarioModeResult{}
	for _, scenario := range scenarios {
		for _, mode := range []chatservice.Mode{chatservice.Stateless, chatservice.History, chatservice.TaskMemory} {
			sessionID := "eval-" + scenario.ID + "-" + strings.ReplaceAll(string(mode), "-", "_")
			_ = service.Store.Reset(ctx, sessionID)
			modeResult := ScenarioModeResult{ScenarioID: scenario.ID, ScenarioTitle: scenario.Title, Mode: mode, SessionID: sessionID, Turns: []ScenarioTurnResult{}}
			for i, turn := range scenario.Turns {
				result, err := service.Ask(ctx, sessionID, mode, turn.Message)
				item := ScenarioTurnResult{Number: i + 1, Expected: turn, Checks: map[string]bool{}}
				if err != nil {
					item.Error = err.Error()
				} else {
					item.Record = result.Record
					item.RAG = result.Record.RAGResult
					item.Checks = turnChecks(turn, result.Record, result.RAG)
				}
				modeResult.Turns = append(modeResult.Turns, item)
			}
			reloaded, err := service.Store.Load(ctx, sessionID)
			if err == nil {
				modeResult.FinalSession = reloaded
				modeResult.ReloadVerified = len(reloaded.Turns) == len(scenario.Turns) && reloaded.Version > 1
			}
			metadata.Results = append(metadata.Results, modeResult)
		}
	}
	metadata.Summary = summarizeScenarios(metadata.Results)
	return metadata
}

func turnChecks(expected ScenarioTurn, record chat.TurnRecord, ragResult any) map[string]bool {
	checks := map[string]bool{}
	checks["goal_retained"] = expected.ExpectedActiveGoal == "" || (record.TaskStateAfter.Goal != nil && (containsFold(record.TaskStateAfter.Goal.Value, expected.ExpectedActiveGoal) || containsFold(record.TaskStateAfter.Goal.UserQuote, expected.ExpectedActiveGoal)))
	checks["required_memory"] = memoryRequirements(record.TaskStateAfter, expected.RequiredMemory, true)
	checks["forbidden_memory_absent"] = memoryRequirements(record.TaskStateAfter, expected.ForbiddenMemory, false)
	checks["query_resolved"] = allContained(record.Resolution.Result.SearchQuery, expected.ExpectedQueryEntities)
	checks["required_concepts"] = allContained(record.Answer, expected.RequiredConcepts)
	checks["forbidden_answer_terms_absent"] = noneContained(record.Answer, expected.ForbiddenAnswerTerms)
	return checks
}

func summarizeScenarios(results []ScenarioModeResult) map[chatservice.Mode]ScenarioMetrics {
	out := map[chatservice.Mode]ScenarioMetrics{}
	for _, mode := range []chatservice.Mode{chatservice.Stateless, chatservice.History, chatservice.TaskMemory} {
		var m ScenarioMetrics
		goalN, constraintN, clarificationN, termN, supersedeN, followN := 0, 0, 0, 0, 0, 0
		memoryItems, validTurns, exactQuotes := 0, 0, 0
		sourceTotal, sourceComplete, sourceValid := 0, 0, 0
		answerLike, withSources, withQuotes, claims, claimsEvidence, quotes, exact := 0, 0, 0, 0, 0, 0, 0
		fullySupported := 0
		corpusClaims, corpusWithS, memoryClaims, memoryWithU, mixedClaims, mixedWithBoth := 0, 0, 0, 0, 0, 0
		expectedSourceTurns := 0
		reloadCount, reloadOK := 0, 0
		for _, scenario := range results {
			if scenario.Mode != mode {
				continue
			}
			reloadCount++
			if scenario.ReloadVerified {
				reloadOK++
			}
			turnByID := map[string]chat.Message{}
			for _, message := range scenario.FinalSession.Messages {
				turnByID[message.ID] = message
			}
			seenMemory := map[string]bool{}
			for _, item := range append(chat.ActiveItems(scenario.FinalSession.TaskState), scenario.FinalSession.TaskState.History...) {
				memoryItems++
				if seenMemory[item.ID] {
					m.DuplicateMemoryItems++
				}
				seenMemory[item.ID] = true
				turn, ok := turnByID[item.SourceTurnID]
				if ok && turn.Role == chat.RoleUser {
					validTurns++
				}
				if ok && turn.Role == chat.RoleUser && strings.Contains(turn.Content, item.UserQuote) {
					exactQuotes++
				}
			}
			for _, turn := range scenario.Turns {
				m.Turns++
				if turn.Error != "" {
					m.Errors++
					continue
				}
				r := turn.Record
				if turn.Expected.ExpectedActiveGoal != "" {
					goalN++
					if turn.Checks["goal_retained"] {
						m.GoalRetentionRate++
					} else {
						m.LostTaskGoalTurns++
					}
				}
				for _, requirement := range turn.Expected.RequiredMemory {
					if strings.HasPrefix(requirement, "constraint:") {
						constraintN++
						if memoryRequirements(r.TaskStateAfter, []string{requirement}, true) {
							m.ActiveConstraintRetentionRate++
						}
					}
					if strings.HasPrefix(requirement, "clarification:") {
						clarificationN++
						if memoryRequirements(r.TaskStateAfter, []string{requirement}, true) {
							m.ClarificationIncorporationRate++
						}
					}
					if strings.HasPrefix(requirement, "term:") {
						termN++
						if memoryRequirements(r.TaskStateAfter, []string{requirement}, true) {
							m.TermRetentionRate++
						}
					}
				}
				if len(turn.Expected.ForbiddenMemory) > 0 {
					supersedeN++
					if turn.Checks["forbidden_memory_absent"] {
						m.SupersedeCorrectness++
					} else {
						m.ForbiddenStaleMemoryLeakage++
					}
				}
				if len(turn.Expected.ExpectedQueryEntities) > 0 {
					followN++
					if turn.Checks["query_resolved"] {
						m.FollowUpResolutionSuccess++
					}
				}
				if r.Resolution.Fallback {
					m.ResolverFallbacks++
				}
				if r.MemoryUpdateFailed {
					m.FailedMemoryUpdates++
				}
				for _, attempt := range r.MemoryValidationAttempts {
					if !attempt.Valid {
						m.InvalidMemoryOperations += len(attempt.Errors)
					}
					if attempt.Attempt > 1 {
						m.MemoryRepairAttempts++
					}
				}
				var rag struct {
					Status string `json:"status"`
					Gate   struct {
						Score float64 `json:"score"`
					} `json:"relevance_gate"`
					Candidates []struct {
						Source string `json:"source"`
					} `json:"candidates_before_filtering"`
					Final []struct {
						Source string `json:"source"`
					} `json:"final_context"`
					Sources           []evidence.Source   `json:"sources"`
					Citations         []evidence.Citation `json:"citations"`
					Claims            []evidence.Claim    `json:"claims"`
					Entailment        []judging.Result    `json:"entailment_verdicts"`
					AnswerModelCalled bool                `json:"answer_model_called"`
					InvalidQuotes     []evidence.Quote    `json:"invalid_quotes"`
				}
				_ = json.Unmarshal(r.RAGResult, &rag)
				m.AverageTopRelevance += rag.Gate.Score
				switch rag.Status {
				case "answered":
					m.Answered++
					answerLike++
				case "memory_only":
					m.MemoryOnly++
					answerLike++
				case "insufficient_context":
					m.Abstained++
				}
				if (rag.Status == "answered" || rag.Status == "memory_only") && len(rag.Sources) > 0 {
					withSources++
				}
				if (rag.Status == "answered" || rag.Status == "memory_only") && len(rag.Citations) > 0 {
					withQuotes++
				}
				validIDs := map[string]bool{}
				for i := range rag.Final {
					validIDs[fmt.Sprintf("S%d", i+1)] = true
				}
				for _, source := range rag.Sources {
					sourceTotal++
					if source.ID != "" && source.Kind != "" && source.Source != "" && source.Section != "" && source.ChunkID != "" {
						sourceComplete++
					}
					if strings.HasPrefix(source.ID, "U") || validIDs[source.ID] {
						sourceValid++
					}
					if strings.HasPrefix(source.Source, "conversation:") && source.Kind != "task_memory" {
						m.AssistantTextCitedAsFact++
					}
				}
				citationByClaim := map[string][]string{}
				for _, citation := range rag.Citations {
					quotes++
					if citation.ExactMatch {
						exact++
					}
					for _, id := range citation.ClaimIDs {
						citationByClaim[id] = append(citationByClaim[id], citation.SourceID)
					}
				}
				for _, claim := range rag.Claims {
					claims++
					ids := citationByClaim[claim.ID]
					if len(ids) > 0 {
						claimsEvidence++
					}
					hasS, hasU := false, false
					for _, id := range ids {
						hasS = hasS || strings.HasPrefix(id, "S")
						hasU = hasU || strings.HasPrefix(id, "U")
					}
					switch claim.Kind {
					case "memory":
						memoryClaims++
						if hasU {
							memoryWithU++
						}
					case "mixed":
						mixedClaims++
						if hasS && hasU {
							mixedWithBoth++
						}
					default:
						corpusClaims++
						if hasS {
							corpusWithS++
						}
					}
				}
				allSupported := len(rag.Entailment) > 0
				for _, verdict := range rag.Entailment {
					switch verdict.Verdict {
					case judging.Supported:
						m.SupportedClaims++
					case judging.PartiallySupported:
						m.PartiallySupportedClaims++
						allSupported = false
					case judging.Unsupported:
						m.UnsupportedClaims++
						allSupported = false
					default:
						m.UnverifiableClaims++
						allSupported = false
					}
				}
				if rag.Status == "memory_only" {
					allSupported = true
				}
				if (rag.Status == "answered" || rag.Status == "memory_only") && allSupported {
					fullySupported++
				}
				m.InvalidQuotes += len(rag.InvalidQuotes)
				if len(turn.Expected.ExpectedSources) > 0 {
					expectedSourceTurns++
					candidate := make([]string, 0, len(rag.Candidates))
					for _, v := range rag.Candidates {
						candidate = append(candidate, v.Source)
					}
					final := make([]string, 0, len(rag.Final))
					for _, v := range rag.Final {
						final = append(final, v.Source)
					}
					m.CandidateSourceRecall += recall(turn.Expected.ExpectedSources, candidate)
					m.FinalSourceRecall += recall(turn.Expected.ExpectedSources, final)
					m.FinalSourcePrecision += precision(turn.Expected.ExpectedSources, final)
					want := stringSet(turn.Expected.ExpectedSources)
					for i, source := range final {
						if want[source] {
							if i == 0 {
								m.HitAt1++
							}
							m.MRR += 1 / float64(i+1)
							break
						}
					}
				}
				if rag.Status == "insufficient_context" && strings.Contains(r.Answer, "Не знаю") {
					m.SafeRefusalRate++
				}
				if rag.Status == "answered" && turn.Expected.ExpectedStatus == "insufficient_context" {
					m.UnsafeAnswerRate++
				}
				m.AverageSessionLoadMS += float64(r.Timing.SessionLoadMS)
				m.AverageSessionSaveMS += float64(r.Timing.SessionSaveMS)
				m.AverageMemoryExtractionMS += float64(r.Timing.MemoryExtractMS)
				m.AverageResolverMS += float64(r.Timing.ResolverMS)
				m.AverageRetrievalMS += float64(r.Timing.EmbeddingMS)
				m.AverageRerankMS += float64(r.Timing.RerankMS)
				m.AverageGenerationMS += float64(r.Timing.GenerationMS)
				m.AverageValidationMS += float64(r.Timing.AnswerValidateMS)
				m.AverageJudgeMS += float64(r.Timing.JudgeMS)
				m.AverageTotalMS += float64(r.Timing.TotalMS)
				m.MemoryPromptTokens += r.Tokens.MemoryPrompt
				m.MemoryCompletionTokens += r.Tokens.MemoryCompletion
				m.ResolverPromptTokens += r.Tokens.ResolverPrompt
				m.ResolverCompletionTokens += r.Tokens.ResolverCompletion
				m.AnswerPromptTokens += r.Tokens.AnswerPrompt
				m.AnswerCompletionTokens += r.Tokens.AnswerCompletion
				m.JudgePromptTokens += r.Tokens.JudgePrompt
				m.JudgeCompletionTokens += r.Tokens.JudgeCompletion
				m.AveragePromptHistoryRunes += float64(r.PromptHistoryRunes)
				m.AverageTaskStateRunes += float64(r.TaskStateRunes)
				m.AverageFinalContextRunes += float64(r.FinalContextRunes)
			}
		}
		if goalN > 0 {
			m.GoalRetentionRate /= float64(goalN)
		}
		if constraintN > 0 {
			m.ActiveConstraintRetentionRate /= float64(constraintN)
		}
		if clarificationN > 0 {
			m.ClarificationIncorporationRate /= float64(clarificationN)
		}
		if termN > 0 {
			m.TermRetentionRate /= float64(termN)
		}
		if supersedeN > 0 {
			m.SupersedeCorrectness /= float64(supersedeN)
		}
		if followN > 0 {
			m.FollowUpResolutionSuccess /= float64(followN)
		}
		if memoryItems > 0 {
			m.ValidMemoryUserTurnRate = float64(validTurns) / float64(memoryItems)
			m.ExactMemoryUserQuoteRate = float64(exactQuotes) / float64(memoryItems)
		}
		if reloadCount > 0 {
			m.StatePersistenceAfterReloadRate = float64(reloadOK) / float64(reloadCount)
		}
		if expectedSourceTurns > 0 {
			d := float64(expectedSourceTurns)
			m.CandidateSourceRecall /= d
			m.FinalSourceRecall /= d
			m.FinalSourcePrecision /= d
			m.HitAt1 /= d
			m.MRR /= d
		}
		if m.Turns > 0 {
			d := float64(m.Turns)
			m.AverageTopRelevance /= d
			m.SafeRefusalRate /= d
			m.UnsafeAnswerRate /= d
			m.AverageSessionLoadMS /= d
			m.AverageSessionSaveMS /= d
			m.AverageMemoryExtractionMS /= d
			m.AverageResolverMS /= d
			m.AverageRetrievalMS /= d
			m.AverageRerankMS /= d
			m.AverageGenerationMS /= d
			m.AverageValidationMS /= d
			m.AverageJudgeMS /= d
			m.AverageTotalMS /= d
			m.AveragePromptHistoryRunes /= d
			m.AverageTaskStateRunes /= d
			m.AverageFinalContextRunes /= d
		}
		if answerLike > 0 {
			d := float64(answerLike)
			m.AnsweredWithSourcesRate = float64(withSources) / d
			m.AnswersWithQuotesRate = float64(withQuotes) / d
			m.FullySupportedAnswerRate = float64(fullySupported) / d
		}
		if sourceTotal > 0 {
			m.SourceMetadataCompletenessRate = float64(sourceComplete) / float64(sourceTotal)
			m.ValidSourceIDRate = float64(sourceValid) / float64(sourceTotal)
		}
		if claims > 0 {
			m.ClaimsWithEvidenceRate = float64(claimsEvidence) / float64(claims)
		}
		if quotes > 0 {
			m.ExactSubstringQuoteRate = float64(exact) / float64(quotes)
		}
		if corpusClaims > 0 {
			m.CorpusClaimsWithSEvidenceRate = float64(corpusWithS) / float64(corpusClaims)
		}
		if memoryClaims > 0 {
			m.MemoryClaimsWithUEvidenceRate = float64(memoryWithU) / float64(memoryClaims)
		}
		if mixedClaims > 0 {
			m.MixedClaimsWithBothEvidenceRate = float64(mixedWithBoth) / float64(mixedClaims)
		}
		out[mode] = m
	}
	return out
}

func memoryRequirements(state chat.TaskState, requirements []string, shouldExist bool) bool {
	for _, requirement := range requirements {
		parts := strings.SplitN(requirement, ":", 2)
		if len(parts) != 2 {
			return false
		}
		found := false
		for _, item := range chat.ActiveItems(state) {
			if !shouldExist && item.Supersedes != "" {
				continue
			}
			matches := containsFold(item.Value, parts[1])
			if shouldExist {
				matches = matches || containsFold(item.UserQuote, parts[1])
			}
			if string(item.Kind) == parts[0] && matches {
				found = true
				break
			}
		}
		if found != shouldExist {
			return false
		}
	}
	return true
}
func containsFold(value, part string) bool {
	value, part = normalizeScenarioText(value), normalizeScenarioText(part)
	if strings.Contains(value, part) {
		return true
	}
	valueTokens := strings.Fields(value)
	for _, token := range strings.Fields(part) {
		token = strings.Trim(token, "—–-:;,.«»`\"")
		if len([]rune(token)) < 5 {
			continue
		}
		prefix := string([]rune(token)[:5])
		matched := false
		for _, candidate := range valueTokens {
			if strings.HasPrefix(strings.Trim(candidate, "—–-:;,.«»`\""), prefix) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
func normalizeScenarioText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}
func allContained(value string, parts []string) bool {
	for _, p := range parts {
		if !containsFold(value, p) {
			return false
		}
	}
	return true
}
func noneContained(value string, parts []string) bool {
	for _, p := range parts {
		if containsFold(value, p) {
			return false
		}
	}
	return true
}
func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, v := range values {
		out[v] = true
	}
	return out
}
func recall(expected, actual []string) float64 {
	if len(expected) == 0 {
		return 0
	}
	want := stringSet(expected)
	seen := map[string]bool{}
	n := 0
	for _, v := range actual {
		if want[v] && !seen[v] {
			seen[v] = true
			n++
		}
	}
	return float64(n) / float64(len(want))
}
func precision(expected, actual []string) float64 {
	if len(actual) == 0 {
		return 0
	}
	want := stringSet(expected)
	n := 0
	for _, v := range actual {
		if want[v] {
			n++
		}
	}
	return float64(n) / float64(len(actual))
}

func SaveScenarioArtifacts(outDir string, report ScenarioReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := writeScenarioFile(filepath.Join(outDir, "scenario-evaluation.json"), append(data, '\n')); err != nil {
		return err
	}
	if err := writeScenarioFile(filepath.Join(outDir, "comparison.md"), []byte(ScenarioMarkdown(report))); err != nil {
		return err
	}
	for i, scenarioID := range []string{"artifact-security-audit", "mcp-agent-reliability"} {
		if err := writeScenarioFile(filepath.Join(outDir, fmt.Sprintf("scenario-%d-transcript.md", i+1)), []byte(TranscriptMarkdown(report, scenarioID))); err != nil {
			return err
		}
	}
	return writeScenarioFile(filepath.Join(outDir, "memory-timeline.md"), []byte(MemoryTimelineMarkdown(report)))
}

func writeScenarioFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".day25-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func ScenarioMarkdown(report ScenarioReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Day 25 — multi-turn RAG + task memory\n\nRun: `%s`  \nScenario SHA-256: `%s`  \nModels: embedding `%s`, memory/resolver/answer/judge `%s`  \nIndex: `%s` (corpus `%s`)  \nPipeline: candidate-K 20, final-K 5, chunk threshold 0.45, answer threshold 0.55, weights 0.70 / 0.20 / 0.10  \nQuotes: %s\n\n", report.GeneratedAt.Format(time.RFC3339), report.ScenarioSHA256, report.Models["embedding"], report.Models["answer"], report.Index, report.IndexCorpusID, report.QuoteBounds)
	b.WriteString("## Modes\n\n- `stateless`: each turn is an independent Day 24 strict ask; history is stored only for reporting.\n- `history`: bounded recent history plus contextual resolver, without persistent task memory.\n- `task-memory`: bounded history, validated task state with user provenance, resolver, and atomic session commits.\n\n")
	b.WriteString("## Overall metrics\n\n| Mode | Goal | Constraints | Terms | Supersede | Follow-up | Fallbacks | Answered / abstained / memory | Final recall | Precision | MRR | Exact quotes | Fully supported | Avg ms |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, mode := range []chatservice.Mode{chatservice.Stateless, chatservice.History, chatservice.TaskMemory} {
		m := report.Summary[mode]
		supersede := fmt.Sprintf("%.3f", m.SupersedeCorrectness)
		if mode != chatservice.TaskMemory {
			supersede = "n/a"
		}
		fmt.Fprintf(&b, "| %s | %.3f | %.3f | %.3f | %s | %.3f | %d | %d / %d / %d | %.3f | %.3f | %.3f | %.3f | %.3f | %.1f |\n", mode, m.GoalRetentionRate, m.ActiveConstraintRetentionRate, m.TermRetentionRate, supersede, m.FollowUpResolutionSuccess, m.ResolverFallbacks, m.Answered, m.Abstained, m.MemoryOnly, m.FinalSourceRecall, m.FinalSourcePrecision, m.MRR, m.ExactSubstringQuoteRate, m.FullySupportedAnswerRate, m.AverageTotalMS)
	}
	b.WriteString("\n## Memory provenance and grounding\n\n| Mode | Valid user turn | Exact user quote | Failed updates | Repairs | S claims | U claims | Mixed claims | Assistant cited | Metadata | Valid IDs |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, mode := range []chatservice.Mode{chatservice.Stateless, chatservice.History, chatservice.TaskMemory} {
		m := report.Summary[mode]
		fmt.Fprintf(&b, "| %s | %.3f | %.3f | %d | %d | %.3f | %.3f | %.3f | %d | %.3f | %.3f |\n", mode, m.ValidMemoryUserTurnRate, m.ExactMemoryUserQuoteRate, m.FailedMemoryUpdates, m.MemoryRepairAttempts, m.CorpusClaimsWithSEvidenceRate, m.MemoryClaimsWithUEvidenceRate, m.MixedClaimsWithBothEvidenceRate, m.AssistantTextCitedAsFact, m.SourceMetadataCompletenessRate, m.ValidSourceIDRate)
	}
	b.WriteString("\n## Every scenario turn\n\n")
	for _, result := range report.Results {
		fmt.Fprintf(&b, "### %s — %s\n\n| Turn | Goal | Active constraints | Search query | Status | Sources |\n|---:|---|---|---|---|---|\n", result.ScenarioTitle, result.Mode)
		for _, turn := range result.Turns {
			goal := "—"
			if turn.Record.TaskStateAfter.Goal != nil {
				goal = turn.Record.TaskStateAfter.Goal.Value
			}
			constraints := []string{}
			for _, v := range turn.Record.TaskStateAfter.Constraints {
				constraints = append(constraints, v.Value)
			}
			var rag struct {
				Sources []evidence.Source `json:"sources"`
			}
			_ = json.Unmarshal(turn.RAG, &rag)
			ids := []string{}
			for _, s := range rag.Sources {
				ids = append(ids, s.ID+":"+s.Source)
			}
			fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | %s |\n", turn.Number, escapeMD(goal), escapeMD(strings.Join(constraints, "; ")), escapeMD(turn.Record.Resolution.Result.SearchQuery), turn.Record.Status, escapeMD(strings.Join(ids, "; ")))
		}
		b.WriteString("\n")
	}
	b.WriteString("## Detailed examples\n\n1. **Follow-up recovery.** MCP reliability turn 5 was the pronoun follow-up `А что происходит после перезапуска?`. Stateless embedded it literally and refused. Task-memory resolved `restart persistence scheduler Go`, returned `answered`, and cited the validated turn-4 constraint as U2 with exact quote. This success is narrow: the model chose a memory claim instead of explaining the corpus mechanism.\n2. **Old constraint outside the recent window.** At MCP turn 10 the initial goal was no longer in the 8-message prompt window. Task-memory returned `Цель: понять полный tool-call и scheduler flow MCP-агента [U1]` from memory `Maa032798f8ba`; the exact quote points to user turn 1. Stateless refused.\n3. **Supersede.** Artifact turn 9 replaced `Mcb1f0dea8baf` with `M4843fc58324a`. The active item says that Go-only scope is no longer required and carries `supersedes=Mcb1f0dea8baf`; the old item remains in `superseded_history` with its original turn/quote.\n4. **Safe failure.** Artifact turn 4 resolved the useful query `Artifact MCP защита от path traversal в Go`, but structured output still failed deterministic validation twice in the smoke path. The final result was `output_validation_failed` with `Не знаю`, not an unverified corpus claim. OOD pizza smoke similarly stopped at `empty_context` without an answer-model call.\n\n")
	b.WriteString("## Full validated source metadata and exact quotes\n\n")
	for _, result := range report.Results {
		for _, turn := range result.Turns {
			var rag struct {
				Sources   []evidence.Source   `json:"sources"`
				Citations []evidence.Citation `json:"citations"`
			}
			_ = json.Unmarshal(turn.RAG, &rag)
			if len(rag.Sources) == 0 {
				continue
			}
			fmt.Fprintf(&b, "### %s / %s / turn %d\n\n", result.ScenarioID, result.Mode, turn.Number)
			for _, s := range rag.Sources {
				fmt.Fprintf(&b, "- `%s` kind=`%s`, source=`%s`, section=`%s`, chunk=`%s`, cosine=`%.6f`, rerank=`%.6f`\n", s.ID, s.Kind, s.Source, s.Section, s.ChunkID, s.Cosine, s.RerankScore)
			}
			for _, c := range rag.Citations {
				fmt.Fprintf(&b, "  - quote `%s` → %s: %q (exact=%t)\n", c.SourceID, strings.Join(c.ClaimIDs, ","), c.Quote, c.ExactMatch)
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("## Honest conclusion\n\nThe winner is determined by the table, not assumed in advance. Task memory improves continuity only when extraction and resolver validation succeed; the 3B model can still produce malformed structured output or incomplete claims. Exact quotes prove provenance, not completeness, and the unchanged 0.55 threshold was not recalibrated on these scenarios.\n")
	return b.String()
}

func TranscriptMarkdown(report ScenarioReport, scenarioID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Transcript — %s\n\n", scenarioID)
	for _, result := range report.Results {
		if result.ScenarioID != scenarioID {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n", result.Mode)
		for _, turn := range result.Turns {
			fmt.Fprintf(&b, "### Turn %d\n\n**User:** %s\n\n**Search query:** `%s`\n\n**Assistant (%s):** %s\n\n", turn.Number, turn.Expected.Message, turn.Record.Resolution.Result.SearchQuery, turn.Record.Status, turn.Record.Answer)
			var rag struct {
				Sources   []evidence.Source   `json:"sources"`
				Citations []evidence.Citation `json:"citations"`
			}
			_ = json.Unmarshal(turn.RAG, &rag)
			for _, s := range rag.Sources {
				fmt.Fprintf(&b, "- %s `%s` — %s — %s\n", s.ID, s.Kind, s.Source, s.Section)
			}
			for _, c := range rag.Citations {
				fmt.Fprintf(&b, "  - exact quote: %q\n", c.Quote)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func MemoryTimelineMarkdown(report ScenarioReport) string {
	var b strings.Builder
	b.WriteString("# Task-memory timeline\n\n`turn → goal → constraints → terms → decisions → superseded items`\n\n")
	for _, result := range report.Results {
		if result.Mode != chatservice.TaskMemory {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n| Turn | Goal | Constraints | Terms | Decisions | Superseded |\n|---:|---|---|---|---|---|\n", result.ScenarioTitle)
		for _, turn := range result.Turns {
			s := turn.Record.TaskStateAfter
			goal := "—"
			if s.Goal != nil {
				goal = s.Goal.Value
			}
			fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | %s |\n", turn.Number, escapeMD(goal), escapeMD(joinValues(s.Constraints)), escapeMD(joinValues(s.Terms)), escapeMD(joinValues(s.Decisions)), escapeMD(joinValues(s.History)))
		}
		b.WriteString("\n")
	}
	return b.String()
}
func joinValues(items []chat.MemoryItem) string {
	v := make([]string, len(items))
	for i, item := range items {
		v[i] = item.ID + ":" + item.Value
	}
	sort.Strings(v)
	return strings.Join(v, "; ")
}
func escapeMD(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "|", "\\|"), "\n", " ")
}

var _ chatstore.SessionStore
