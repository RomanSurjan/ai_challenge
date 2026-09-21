package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

type ProfileLanguage string

const (
	ProfileLanguageRussian ProfileLanguage = "ru"
	ProfileLanguageEnglish ProfileLanguage = "en"
)

type ProfileTone string

const (
	ProfileToneFormal   ProfileTone = "formal"
	ProfileToneFriendly ProfileTone = "friendly"
)

type ProfileDetail string

const (
	ProfileDetailConcise  ProfileDetail = "concise"
	ProfileDetailDetailed ProfileDetail = "detailed"
)

type ProfileFormat string

const (
	ProfileFormatPlainText ProfileFormat = "plain_text"
	ProfileFormatBullets   ProfileFormat = "bullets"
	ProfileFormatSteps     ProfileFormat = "steps"
)

const (
	maxProfileNameRunes       = 80
	maxProfileContextRunes    = 1000
	maxProfileConstraints     = 10
	maxProfileConstraintRunes = 240
)

// UserProfile is the typed, user-wide personalization contract. It is stored
// once in long-term memory and is shared by all dialogs in the memory folder.
type UserProfile struct {
	Name        string          `json:"name,omitempty"`
	Language    ProfileLanguage `json:"language"`
	Tone        ProfileTone     `json:"tone"`
	Detail      ProfileDetail   `json:"detail"`
	Format      ProfileFormat   `json:"format"`
	Context     string          `json:"context,omitempty"`
	Constraints []string        `json:"constraints,omitempty"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func (profile UserProfile) normalized() UserProfile {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Context = strings.TrimSpace(profile.Context)
	profile.Constraints = append([]string(nil), profile.Constraints...)
	for index := range profile.Constraints {
		profile.Constraints[index] = strings.TrimSpace(profile.Constraints[index])
	}
	return profile
}

func validateUserProfile(profile UserProfile) error {
	profile = profile.normalized()
	if !validProfileLanguage(profile.Language) {
		return fmt.Errorf("неподдерживаемый язык профиля: %q", profile.Language)
	}
	if !validProfileTone(profile.Tone) {
		return fmt.Errorf("неподдерживаемый тон профиля: %q", profile.Tone)
	}
	if !validProfileDetail(profile.Detail) {
		return fmt.Errorf("неподдерживаемый уровень подробности профиля: %q", profile.Detail)
	}
	if !validProfileFormat(profile.Format) {
		return fmt.Errorf("неподдерживаемый формат ответа профиля: %q", profile.Format)
	}
	if err := validateProfileText("name", profile.Name, maxProfileNameRunes, false); err != nil {
		return err
	}
	if err := validateProfileText("context", profile.Context, maxProfileContextRunes, false); err != nil {
		return err
	}
	if len(profile.Constraints) > maxProfileConstraints {
		return fmt.Errorf("constraints не может содержать больше %d ограничений", maxProfileConstraints)
	}
	seen := make(map[string]struct{}, len(profile.Constraints))
	for index, constraint := range profile.Constraints {
		if err := validateProfileText(fmt.Sprintf("constraints[%d]", index), constraint, maxProfileConstraintRunes, true); err != nil {
			return err
		}
		key := strings.ToLower(constraint)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("constraints содержит повторяющееся ограничение %q", constraint)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateStoredUserProfile(profile UserProfile) error {
	if err := validateUserProfile(profile); err != nil {
		return err
	}
	if profile.UpdatedAt.IsZero() {
		return errors.New("время обновления профиля не задано")
	}
	return nil
}

func validateProfileText(field, value string, maxRunes int, required bool) error {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return fmt.Errorf("поле %s не может быть пустым", field)
	}
	if len([]rune(value)) > maxRunes {
		return fmt.Errorf("поле %s не может быть длиннее %d символов", field, maxRunes)
	}
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("поле %s должно быть одной строкой", field)
	}
	if strings.Contains(strings.ToLower(value), "[end_user_profile]") {
		return fmt.Errorf("поле %s содержит зарезервированный маркер", field)
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return fmt.Errorf("поле %s содержит управляющий символ", field)
		}
	}
	return nil
}

func validProfileLanguage(value ProfileLanguage) bool {
	return value == ProfileLanguageRussian || value == ProfileLanguageEnglish
}

func validProfileTone(value ProfileTone) bool {
	return value == ProfileToneFormal || value == ProfileToneFriendly
}

func validProfileDetail(value ProfileDetail) bool {
	return value == ProfileDetailConcise || value == ProfileDetailDetailed
}

func validProfileFormat(value ProfileFormat) bool {
	return value == ProfileFormatPlainText || value == ProfileFormatBullets || value == ProfileFormatSteps
}

func cloneUserProfile(profile *UserProfile) *UserProfile {
	if profile == nil {
		return nil
	}
	cloned := *profile
	cloned.Constraints = append([]string(nil), profile.Constraints...)
	return &cloned
}

func (memory LongTermMemory) EffectiveProfile() *UserProfile {
	if memory.UserProfile != nil {
		return cloneUserProfile(memory.UserProfile)
	}
	return profileFromLegacyEntries(memory.Profile)
}

// profileFromLegacyEntries understands only the old keys that can be mapped
// unambiguously. Unknown entries remain stored and are never injected into the
// system prompt as arbitrary instructions.
func profileFromLegacyEntries(entries []ProfileEntry) *UserProfile {
	var profile UserProfile
	for _, entry := range entries {
		key := strings.ToLower(strings.TrimSpace(entry.Key))
		value := strings.TrimSpace(entry.Value)
		switch key {
		case "name", "display_name":
			profile.Name = value
		case "language":
			profile.Language = normalizeLegacyLanguage(value)
		case "tone", "style":
			profile.Tone = normalizeLegacyTone(value)
		case "detail", "verbosity":
			profile.Detail = normalizeLegacyDetail(value)
		case "format", "response_format":
			profile.Format = normalizeLegacyFormat(value)
		case "context", "user_context":
			profile.Context = value
		case "constraint":
			profile.Constraints = append(profile.Constraints, value)
		}
	}
	profile = profile.normalized()
	if validateUserProfile(profile) != nil {
		return nil
	}
	return &profile
}

func normalizeLegacyLanguage(value string) ProfileLanguage {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ru", "russian", "русский":
		return ProfileLanguageRussian
	case "en", "english", "английский":
		return ProfileLanguageEnglish
	default:
		return ProfileLanguage(value)
	}
}

func normalizeLegacyTone(value string) ProfileTone {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "formal", "формальный":
		return ProfileToneFormal
	case "friendly", "conversational", "дружелюбный", "разговорный":
		return ProfileToneFriendly
	default:
		return ProfileTone(value)
	}
}

func normalizeLegacyDetail(value string) ProfileDetail {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "concise", "brief", "краткий", "кратко":
		return ProfileDetailConcise
	case "detailed", "подробный", "подробно":
		return ProfileDetailDetailed
	default:
		return ProfileDetail(value)
	}
}

func normalizeLegacyFormat(value string) ProfileFormat {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "plain_text", "text", "обычный текст":
		return ProfileFormatPlainText
	case "bullets", "list", "список":
		return ProfileFormatBullets
	case "steps", "step_by_step", "пошаговый":
		return ProfileFormatSteps
	default:
		return ProfileFormat(value)
	}
}

func (m *MemoryLayers) LoadProfile(ctx context.Context) (*UserProfile, error) {
	memory, err := m.LoadLongTerm(ctx)
	if err != nil {
		return nil, err
	}
	return memory.EffectiveProfile(), nil
}

func (m *MemoryLayers) SetProfile(ctx context.Context, profile UserProfile) (*UserProfile, error) {
	profile = profile.normalized()
	if err := validateUserProfile(profile); err != nil {
		return nil, err
	}
	profile.UpdatedAt = time.Now().UTC()
	if m == nil || m.LongTerm == nil {
		return nil, errors.New("хранилище долговременной памяти не настроено")
	}
	m.dialogMu.Lock()
	defer m.dialogMu.Unlock()
	if err := m.recoverTurnTransactionLocked(); err != nil {
		return nil, err
	}
	m.longTermMu.Lock()
	defer m.longTermMu.Unlock()
	memory, err := m.LongTerm.Load(ctx)
	if err != nil {
		return nil, err
	}
	memory.UserProfile = cloneUserProfile(&profile)
	if err := m.LongTerm.Save(ctx, memory); err != nil {
		return nil, err
	}
	return memory.EffectiveProfile(), nil
}
