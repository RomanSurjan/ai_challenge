package main

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

type sensitiveRule string

const (
	sensitiveRuleCredentialAssignment sensitiveRule = "credential_assignment"
	sensitiveRuleBearerToken          sensitiveRule = "bearer_token"
	sensitiveRulePrivateKey           sensitiveRule = "private_key"
	sensitiveRuleKeyLikeToken         sensitiveRule = "key_like_token"
	sensitiveRulePaymentCard          sensitiveRule = "payment_card_number"
)

var (
	bearerTokenPattern = regexp.MustCompile(`(?i)(?:authorization\s*:\s*)?bearer\s+[a-z0-9._~+/=-]{8,}`)
	privateKeyPattern  = regexp.MustCompile(`(?i)-----begin (?:[a-z0-9]+ )*private key-----`)
	keyLikePattern     = regexp.MustCompile(`(?i)(?:^|[^a-z0-9_-])sk-[a-z0-9_-]{17,}(?:$|[^a-z0-9_-])`)
)

var credentialLabels = []string{
	"api key", "api_key", "api-key",
	"access token", "access_token", "access-token",
	"password", "passwd", "пароль",
	"secret key", "secret_key", "secret-key", "секретный ключ",
	"номер карты", "credit card", "credit_card", "credit-card",
	"private key", "private_key", "private-key",
	"cvv", "cvc", "pin-код", "pin code", "pin_code", "pin-code",
}

func looksSensitive(value string) bool {
	return sensitiveRuleFor(value) != ""
}

// sensitiveRuleFor identifies concrete secret-shaped values. Merely naming a
// credential category (for example, "password reset" or "API key rotation")
// is intentionally not enough to classify task text as secret.
func sensitiveRuleFor(value string) sensitiveRule {
	lower := strings.ToLower(value)
	if containsCredentialAssignment(lower) {
		return sensitiveRuleCredentialAssignment
	}
	if bearerTokenPattern.MatchString(lower) {
		return sensitiveRuleBearerToken
	}
	if privateKeyPattern.MatchString(lower) {
		return sensitiveRulePrivateKey
	}
	if keyLikePattern.MatchString(lower) {
		return sensitiveRuleKeyLikeToken
	}
	if containsSensitiveDigitSequence(value) {
		return sensitiveRulePaymentCard
	}
	return ""
}

func containsCredentialAssignment(lower string) bool {
	for _, label := range credentialLabels {
		searchFrom := 0
		for {
			index := strings.Index(lower[searchFrom:], label)
			if index < 0 {
				break
			}
			index += searchFrom
			remainder := strings.TrimLeft(lower[index+len(label):], " \t\r\n")
			if len(remainder) > 1 && (remainder[0] == '=' || remainder[0] == ':') && strings.TrimSpace(remainder[1:]) != "" {
				return true
			}
			searchFrom = index + len(label)
		}
	}
	return false
}

func containsSensitiveDigitSequence(value string) bool {
	digits := 0
	isSensitiveLength := func() bool {
		return digits >= 13 && digits <= 19
	}
	for _, char := range value {
		switch {
		case char >= '0' && char <= '9':
			digits++
		case (char == ' ' || char == '-') && digits > 0:
		default:
			if isSensitiveLength() {
				return true
			}
			digits = 0
		}
	}
	return isSensitiveLength()
}

func (m *MemoryLayers) applyLongTermMutations(ctx context.Context, mutations []longTermMemoryMutation) (LongTermMemory, []bool, error) {
	changed := make([]bool, len(mutations))
	if len(mutations) == 0 {
		return LongTermMemory{}, changed, nil
	}
	if m == nil || m.LongTerm == nil {
		return LongTermMemory{}, nil, errors.New("хранилище долговременной памяти не настроено")
	}
	m.dialogMu.Lock()
	defer m.dialogMu.Unlock()
	if err := m.recoverTurnTransactionLocked(); err != nil {
		return LongTermMemory{}, nil, err
	}
	m.longTermMu.Lock()
	defer m.longTermMu.Unlock()
	memory, err := m.LongTerm.Load(ctx)
	if err != nil {
		return LongTermMemory{}, nil, err
	}
	for index, mutation := range mutations {
		switch mutation.kind {
		case longTermMutationProfile:
			found := false
			for entryIndex := range memory.Profile {
				if memory.Profile[entryIndex].Key == mutation.key {
					found = true
					if memory.Profile[entryIndex].Value != mutation.value {
						memory.Profile[entryIndex].Value = mutation.value
						memory.Profile[entryIndex].UpdatedAt = time.Now().UTC()
						changed[index] = true
					}
					break
				}
			}
			if !found {
				memory.Profile = append(memory.Profile, ProfileEntry{Key: mutation.key, Value: mutation.value, UpdatedAt: time.Now().UTC()})
				changed[index] = true
			}
		case longTermMutationDecision:
			if !containsDecision(memory.Decisions, mutation.value, mutation.extra) {
				memory.Decisions = append(memory.Decisions, Decision{Statement: mutation.value, Rationale: mutation.extra, RecordedAt: time.Now().UTC()})
				changed[index] = true
			}
		case longTermMutationKnowledge:
			if !containsKnowledge(memory.Knowledge, mutation.key, mutation.value) {
				memory.Knowledge = append(memory.Knowledge, Knowledge{Topic: mutation.key, Content: mutation.value, RecordedAt: time.Now().UTC()})
				changed[index] = true
			}
		default:
			return LongTermMemory{}, nil, errors.New("неизвестная операция долговременной памяти")
		}
	}
	if !anyChanged(changed) {
		return cloneLongTermMemory(memory), changed, nil
	}
	if err := m.LongTerm.Save(ctx, memory); err != nil {
		return LongTermMemory{}, nil, err
	}
	return cloneLongTermMemory(memory), changed, nil
}

func anyChanged(values []bool) bool {
	for _, value := range values {
		if value {
			return true
		}
	}
	return false
}

func containsWorkingNote(notes []WorkingNote, content string) bool {
	for _, note := range notes {
		if note.Content == content {
			return true
		}
	}
	return false
}

func containsWorkingResult(results []WorkingResult, content string) bool {
	for _, result := range results {
		if result.Content == content {
			return true
		}
	}
	return false
}

func containsDecision(decisions []Decision, statement, rationale string) bool {
	for _, decision := range decisions {
		if decision.Statement == statement && decision.Rationale == rationale {
			return true
		}
	}
	return false
}

func containsKnowledge(knowledge []Knowledge, topic, content string) bool {
	for _, entry := range knowledge {
		if entry.Topic == topic && entry.Content == content {
			return true
		}
	}
	return false
}
