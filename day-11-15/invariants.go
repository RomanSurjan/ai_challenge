package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	invariantsFileName         = "invariants.json"
	maxInvariantIDRunes        = 80
	maxInvariantTextRunes      = 500
	maxInvariantTermRunes      = 120
	maxInvariantTerms          = 40
	maxInvariantCount          = 100
	maxInvariantAnswerAttempts = 3
)

type InvariantCategory string

const (
	InvariantCategoryArchitecture      InvariantCategory = "architecture"
	InvariantCategoryTechnicalDecision InvariantCategory = "technical_decision"
	InvariantCategoryStack             InvariantCategory = "stack"
	InvariantCategoryBusinessRule      InvariantCategory = "business_rule"
)

type InvariantRuleType string

const (
	InvariantRuleForbiddenTerms InvariantRuleType = "forbidden_terms"
	InvariantRuleRequiredTerms  InvariantRuleType = "required_terms"
)

type InvariantTarget string

const (
	InvariantTargetRequest  InvariantTarget = "request"
	InvariantTargetResponse InvariantTarget = "response"
)

type InvariantRule struct {
	Type    InvariantRuleType `json:"type"`
	Terms   []string          `json:"terms"`
	Targets []InvariantTarget `json:"targets"`
}

type Invariant struct {
	ID          string            `json:"id"`
	Category    InvariantCategory `json:"category"`
	Description string            `json:"description"`
	Alternative string            `json:"alternative"`
	Rule        InvariantRule     `json:"rule"`
}

type InvariantSet struct {
	Invariants []Invariant `json:"invariants"`
}

type InvariantViolation struct {
	InvariantID string            `json:"invariant_id"`
	Category    InvariantCategory `json:"category"`
	Description string            `json:"description"`
	Alternative string            `json:"alternative"`
	RuleType    InvariantRuleType `json:"rule_type"`
	Target      InvariantTarget   `json:"target"`
	Terms       []string          `json:"terms"`
}

type InvariantDecision struct {
	Allowed    bool                 `json:"allowed"`
	Violations []InvariantViolation `json:"violations,omitempty"`
}

type InvariantViolationError struct {
	Target     InvariantTarget
	Violations []InvariantViolation
}

var ErrInvariantViolation = errors.New("нарушение программного инварианта")

func (e *InvariantViolationError) Error() string {
	if e == nil || len(e.Violations) == 0 {
		return ErrInvariantViolation.Error()
	}
	parts := make([]string, 0, len(e.Violations))
	for _, violation := range e.Violations {
		part := fmt.Sprintf("правило %q: %s", violation.Description, invariantConflictReason(violation))
		if violation.Alternative != "" {
			part += ". Допустимый вариант: " + violation.Alternative
		}
		parts = append(parts, part)
	}
	prefix := "Запрос отклонён: "
	if e.Target == InvariantTargetResponse {
		prefix = "Ответ заблокирован: "
	}
	return prefix + strings.Join(parts, "; ")
}

func (e *InvariantViolationError) Unwrap() error { return ErrInvariantViolation }

func invariantConflictReason(violation InvariantViolation) string {
	switch violation.RuleType {
	case InvariantRuleRequiredTerms:
		return "в тексте отсутствуют обязательные элементы: " + strings.Join(violation.Terms, ", ")
	default:
		return "обнаружены запрещённые элементы: " + strings.Join(violation.Terms, ", ")
	}
}

type InvariantStore interface {
	Load(context.Context) (InvariantSet, error)
	Save(context.Context, InvariantSet) error
}

type JSONInvariantStore struct {
	file *jsonFileStore[InvariantSet]
}

func NewJSONInvariantStore(path string) *JSONInvariantStore {
	return &JSONInvariantStore{file: newStrictJSONFileStore(path, "инварианты", validateInvariantSet, cloneInvariantSet)}
}

func (s *JSONInvariantStore) Load(ctx context.Context) (InvariantSet, error) {
	if s == nil {
		return InvariantSet{}, nil
	}
	return s.file.Load(ctx)
}

func (s *JSONInvariantStore) Save(ctx context.Context, invariants InvariantSet) error {
	if s == nil {
		return errors.New("хранилище инвариантов не настроено")
	}
	return s.file.Save(ctx, invariants)
}

type InvariantChecker interface {
	Check(InvariantTarget, string, InvariantSet) InvariantDecision
}

type TermInvariantChecker struct{}

func (TermInvariantChecker) Check(target InvariantTarget, text string, set InvariantSet) InvariantDecision {
	violations := make([]InvariantViolation, 0)
	for _, invariant := range set.Invariants {
		if !containsInvariantTarget(invariant.Rule.Targets, target) {
			continue
		}
		terms := matchingInvariantTerms(text, invariant.Rule.Terms)
		violated := invariant.Rule.Type == InvariantRuleForbiddenTerms && len(terms) > 0
		if invariant.Rule.Type == InvariantRuleRequiredTerms {
			terms = missingInvariantTerms(text, invariant.Rule.Terms)
			violated = len(terms) > 0
		}
		if violated {
			violations = append(violations, InvariantViolation{
				InvariantID: invariant.ID,
				Category:    invariant.Category,
				Description: invariant.Description,
				Alternative: invariant.Alternative,
				RuleType:    invariant.Rule.Type,
				Target:      target,
				Terms:       append([]string(nil), terms...),
			})
		}
	}
	return InvariantDecision{Allowed: len(violations) == 0, Violations: violations}
}

func matchingInvariantTerms(text string, terms []string) []string {
	matches := make([]string, 0)
	for _, term := range terms {
		if containsInvariantTerm(text, term) {
			matches = append(matches, term)
		}
	}
	return matches
}

func missingInvariantTerms(text string, terms []string) []string {
	missing := make([]string, 0)
	for _, term := range terms {
		if !containsInvariantTerm(text, term) {
			missing = append(missing, term)
		}
	}
	return missing
}

func containsInvariantTerm(text, term string) bool {
	text = normalizeInvariantText(text)
	term = normalizeInvariantText(term)
	if text == "" || term == "" {
		return false
	}
	for offset := 0; offset <= len(text)-len(term); {
		index := strings.Index(text[offset:], term)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(term)
		leftOK := !startsWithInvariantWord(term) || start == 0 || !isInvariantWordRune(runeBefore(text, start))
		rightOK := !endsWithInvariantWord(term) || end == len(text) || !isInvariantWordRune(runeAfter(text, end))
		if leftOK && rightOK {
			return true
		}
		_, size := utf8.DecodeRuneInString(text[start:])
		offset = start + size
	}
	return false
}

func normalizeInvariantText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func startsWithInvariantWord(value string) bool {
	r, _ := utf8.DecodeRuneInString(value)
	return isInvariantWordRune(r)
}

func endsWithInvariantWord(value string) bool {
	r, _ := utf8.DecodeLastRuneInString(value)
	return isInvariantWordRune(r)
}

func runeBefore(value string, offset int) rune {
	r, _ := utf8.DecodeLastRuneInString(value[:offset])
	return r
}

func runeAfter(value string, offset int) rune {
	r, _ := utf8.DecodeRuneInString(value[offset:])
	return r
}

func isInvariantWordRune(value rune) bool {
	return unicode.IsLetter(value) || unicode.IsDigit(value) || value == '_'
}

func validateInvariantSet(set InvariantSet) error {
	if len(set.Invariants) > maxInvariantCount {
		return fmt.Errorf("слишком много инвариантов: максимум %d", maxInvariantCount)
	}
	seenIDs := make(map[string]struct{}, len(set.Invariants))
	for index, invariant := range set.Invariants {
		if err := validateInvariant(invariant); err != nil {
			return fmt.Errorf("инвариант %d: %w", index, err)
		}
		if _, exists := seenIDs[invariant.ID]; exists {
			return fmt.Errorf("дублирующийся ID инварианта %q", invariant.ID)
		}
		seenIDs[invariant.ID] = struct{}{}
	}
	return nil
}

func validateInvariant(invariant Invariant) error {
	if !validInvariantID(invariant.ID) {
		return fmt.Errorf("некорректный ID %q", invariant.ID)
	}
	if !validInvariantCategory(invariant.Category) {
		return fmt.Errorf("неизвестная категория %q", invariant.Category)
	}
	if err := validateInvariantText("description", invariant.Description, maxInvariantTextRunes); err != nil {
		return err
	}
	if err := validateInvariantText("alternative", invariant.Alternative, maxInvariantTextRunes); err != nil {
		return err
	}
	if invariant.Rule.Type != InvariantRuleForbiddenTerms && invariant.Rule.Type != InvariantRuleRequiredTerms {
		return fmt.Errorf("неизвестный тип проверки %q", invariant.Rule.Type)
	}
	if len(invariant.Rule.Terms) == 0 || len(invariant.Rule.Terms) > maxInvariantTerms {
		return fmt.Errorf("terms должен содержать от 1 до %d элементов", maxInvariantTerms)
	}
	seenTerms := make(map[string]struct{}, len(invariant.Rule.Terms))
	for _, term := range invariant.Rule.Terms {
		if err := validateInvariantText("term", term, maxInvariantTermRunes); err != nil {
			return err
		}
		normalized := normalizeInvariantText(term)
		if _, exists := seenTerms[normalized]; exists {
			return fmt.Errorf("дублирующийся term %q", term)
		}
		seenTerms[normalized] = struct{}{}
	}
	if len(invariant.Rule.Targets) == 0 || len(invariant.Rule.Targets) > 2 {
		return errors.New("targets должен содержать request, response или оба значения")
	}
	seenTargets := make(map[InvariantTarget]struct{}, len(invariant.Rule.Targets))
	for _, target := range invariant.Rule.Targets {
		if target != InvariantTargetRequest && target != InvariantTargetResponse {
			return fmt.Errorf("неизвестная цель проверки %q", target)
		}
		if _, exists := seenTargets[target]; exists {
			return fmt.Errorf("дублирующаяся цель проверки %q", target)
		}
		seenTargets[target] = struct{}{}
	}
	return nil
}

func validateInvariantText(field, value string, maxRunes int) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("поле %s не может быть пустым", field)
	}
	if len([]rune(value)) > maxRunes {
		return fmt.Errorf("поле %s слишком длинное", field)
	}
	return nil
}

func validInvariantID(id string) bool {
	if id == "" || len([]rune(id)) > maxInvariantIDRunes {
		return false
	}
	for index, char := range id {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || (index > 0 && (char == '-' || char == '_' || char == '.')) {
			continue
		}
		return false
	}
	return true
}

func validInvariantCategory(category InvariantCategory) bool {
	switch category {
	case InvariantCategoryArchitecture, InvariantCategoryTechnicalDecision, InvariantCategoryStack, InvariantCategoryBusinessRule:
		return true
	default:
		return false
	}
}

func containsInvariantTarget(targets []InvariantTarget, target InvariantTarget) bool {
	for _, candidate := range targets {
		if candidate == target {
			return true
		}
	}
	return false
}

func cloneInvariantSet(set InvariantSet) InvariantSet {
	set.Invariants = append([]Invariant(nil), set.Invariants...)
	for index := range set.Invariants {
		set.Invariants[index].Rule.Terms = append([]string(nil), set.Invariants[index].Rule.Terms...)
		set.Invariants[index].Rule.Targets = append([]InvariantTarget(nil), set.Invariants[index].Rule.Targets...)
	}
	return set
}

func (m *MemoryLayers) LoadInvariants(ctx context.Context) (InvariantSet, error) {
	if m == nil || m.Invariants == nil {
		return InvariantSet{}, errors.New("хранилище инвариантов не настроено")
	}
	return m.Invariants.Load(ctx)
}

func (m *MemoryLayers) SetInvariants(ctx context.Context, set InvariantSet) (InvariantSet, error) {
	if m == nil || m.Invariants == nil {
		return InvariantSet{}, errors.New("хранилище инвариантов не настроено")
	}
	set = cloneInvariantSet(set)
	if err := validateInvariantSet(set); err != nil {
		return InvariantSet{}, err
	}
	if err := m.Invariants.Save(ctx, set); err != nil {
		return InvariantSet{}, err
	}
	return cloneInvariantSet(set), nil
}

func newInvariantStoreForDirectory(dir string) InvariantStore {
	return NewJSONInvariantStore(filepath.Join(dir, invariantsFileName))
}
