package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	turnTransactionVersion   = 1
	turnTransactionPrepared  = "prepared"
	turnTransactionCommitted = "committed"
)

type turnTransactionHooks struct {
	beforeTargetWrite func(index int, relativePath string) error
}

type turnTransactionEntry struct {
	RelativePath string `json:"relative_path"`
	BeforeExists bool   `json:"before_exists"`
	Before       []byte `json:"before,omitempty"`
	After        []byte `json:"after"`
}

type turnTransactionJournal struct {
	Version   int                    `json:"version"`
	ID        string                 `json:"id"`
	State     string                 `json:"state"`
	CreatedAt time.Time              `json:"created_at"`
	Entries   []turnTransactionEntry `json:"entries"`
}

type acceptedTurnCommit struct {
	Updates []MemoryUpdate
	Working *WorkingMemory
}

// commitAcceptedTurn is the only commit boundary for a model answer. The
// caller must not separately persist pending memory or the short-term turn.
func (m *MemoryLayers) commitAcceptedTurn(
	ctx context.Context,
	dialogID, user, assistant string,
	workingMutations []workingMemoryMutation,
	longTermMutations []longTermMemoryMutation,
	includeWorking bool,
) (acceptedTurnCommit, error) {
	if m == nil {
		return acceptedTurnCommit{}, errors.New("хранилище памяти не настроено")
	}
	turn := ConversationTurn{User: strings.TrimSpace(user), Assistant: strings.TrimSpace(assistant), CreatedAt: time.Now().UTC()}
	if err := validateConversationTurn(turn); err != nil {
		return acceptedTurnCommit{}, err
	}

	// Global in-process order: dialogMu -> longTermMu -> individual file mutex.
	// All public working/short/dialog and long-term APIs follow this order.
	m.dialogMu.Lock()
	defer m.dialogMu.Unlock()
	if err := m.recoverTurnTransactionLocked(); err != nil {
		return acceptedTurnCommit{}, err
	}
	m.longTermMu.Lock()
	defer m.longTermMu.Unlock()

	index, err := m.ensureIndexWithoutRecoveryLocked(ctx)
	if err != nil {
		return acceptedTurnCommit{}, err
	}
	dialog, err := resolveDialog(index, dialogID)
	if err != nil {
		return acceptedTurnCommit{}, err
	}

	workingStore := m.workingStore(dialog.ID)
	var working WorkingMemory
	workingChanged := make([]bool, len(workingMutations))
	if len(workingMutations) > 0 || includeWorking {
		working, err = workingStore.Load(ctx)
		if err != nil {
			return acceptedTurnCommit{}, err
		}
		working, workingChanged, err = applyWorkingMutationsToCopy(working, workingMutations, turn.CreatedAt)
		if err != nil {
			return acceptedTurnCommit{}, err
		}
	}

	shortStore := m.shortTermStore(dialog.ID)
	short, err := shortStore.Load(ctx)
	if err != nil {
		return acceptedTurnCommit{}, err
	}
	short.Turns = append(short.Turns, turn)
	if err := validateShortTermMemory(short); err != nil {
		return acceptedTurnCommit{}, err
	}

	var longTerm LongTermMemory
	longTermChanged := make([]bool, len(longTermMutations))
	if len(longTermMutations) > 0 {
		jsonLongTerm, ok := m.LongTerm.(*JSONLongTermMemoryStore)
		if !ok || jsonLongTerm == nil || jsonLongTerm.file == nil {
			return acceptedTurnCommit{}, errors.New("транзакционная долговременная память недоступна")
		}
		longTerm, err = m.LongTerm.Load(ctx)
		if err != nil {
			return acceptedTurnCommit{}, err
		}
		longTerm, longTermChanged, err = applyLongTermMutationsToCopy(longTerm, longTermMutations, turn.CreatedAt)
		if err != nil {
			return acceptedTurnCommit{}, err
		}
	}

	if err := touchDialogInMemory(&index, dialog.ID, turn.CreatedAt); err != nil {
		return acceptedTurnCommit{}, err
	}

	entries := make([]turnTransactionEntry, 0, 4)
	if anyChanged(workingChanged) {
		entry, entryErr := m.transactionEntry(workingStore.file.path, working)
		if entryErr != nil {
			return acceptedTurnCommit{}, entryErr
		}
		entries = append(entries, entry)
	}
	if anyChanged(longTermChanged) {
		jsonLongTerm := m.LongTerm.(*JSONLongTermMemoryStore)
		entry, entryErr := m.transactionEntry(jsonLongTerm.file.path, longTerm)
		if entryErr != nil {
			return acceptedTurnCommit{}, entryErr
		}
		entries = append(entries, entry)
	}
	shortEntry, err := m.transactionEntry(shortStore.file.path, short)
	if err != nil {
		return acceptedTurnCommit{}, err
	}
	entries = append(entries, shortEntry)
	dialogEntry, err := m.transactionEntry(m.dialogs.path, index)
	if err != nil {
		return acceptedTurnCommit{}, err
	}
	entries = append(entries, dialogEntry)

	journalID, err := newTurnTransactionID()
	if err != nil {
		return acceptedTurnCommit{}, err
	}
	journal := turnTransactionJournal{
		Version: turnTransactionVersion, ID: journalID, State: turnTransactionPrepared,
		CreatedAt: turn.CreatedAt, Entries: entries,
	}
	if err := m.commitTurnJournalLocked(ctx, journal); err != nil {
		return acceptedTurnCommit{}, err
	}

	updates := make([]MemoryUpdate, 0, len(workingMutations)+len(longTermMutations))
	for i, changed := range workingChanged {
		if changed {
			updates = append(updates, workingMutations[i].memoryUpdate())
		}
	}
	for i, changed := range longTermChanged {
		if changed {
			updates = append(updates, longTermMutations[i].memoryUpdate())
		}
	}
	commit := acceptedTurnCommit{Updates: updates}
	if len(workingMutations) > 0 || includeWorking {
		copy := cloneWorkingMemory(working)
		commit.Working = &copy
	}
	return commit, nil
}

func (m *MemoryLayers) transactionEntry(path string, value any) (turnTransactionEntry, error) {
	relative, err := filepath.Rel(m.dir, path)
	if err != nil || !validTransactionTarget(relative) {
		return turnTransactionEntry{}, errors.New("некорректная цель транзакции")
	}
	before, beforeExists, err := readOptionalRegularFile(path)
	if err != nil {
		return turnTransactionEntry{}, err
	}
	after, err := marshalVersionedEnvelope(value)
	if err != nil {
		return turnTransactionEntry{}, err
	}
	return turnTransactionEntry{RelativePath: filepath.ToSlash(relative), BeforeExists: beforeExists, Before: before, After: after}, nil
}

func marshalVersionedEnvelope(value any) ([]byte, error) {
	payload := struct {
		Version   int       `json:"version"`
		Data      any       `json:"data"`
		UpdatedAt time.Time `json:"updated_at"`
	}{Version: 1, Data: value, UpdatedAt: time.Now().UTC()}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, errors.New("не удалось подготовить транзакционные данные")
	}
	return append(data, '\n'), nil
}

func (m *MemoryLayers) commitTurnJournalLocked(ctx context.Context, journal turnTransactionJournal) error {
	if err := validateTurnTransactionJournal(journal); err != nil {
		return err
	}
	journalPath := filepath.Join(m.dir, transactionJournalFileName)
	if err := writeTurnJournal(journalPath, journal); err != nil {
		return errors.New("не удалось подготовить транзакцию пользовательского хода")
	}

	for index, entry := range journal.Entries {
		if err := ctx.Err(); err != nil {
			return m.rollbackAfterCommitError(journal, err)
		}
		if hooks := m.transactionHooks; hooks != nil && hooks.beforeTargetWrite != nil {
			if err := hooks.beforeTargetWrite(index, entry.RelativePath); err != nil {
				return m.rollbackAfterCommitError(journal, err)
			}
		}
		if err := writeBytesAtomically(filepath.Join(m.dir, filepath.FromSlash(entry.RelativePath)), entry.After); err != nil {
			return m.rollbackAfterCommitError(journal, err)
		}
	}
	journal.State = turnTransactionCommitted
	if err := writeTurnJournal(journalPath, journal); err != nil {
		// A directory-sync error after rename is ambiguous. If the committed
		// decision is readable and a retry sync succeeds, the transaction is
		// durably committed; otherwise restore the prepared before-images.
		if !committedJournalIsDurable(journalPath, journal.ID, m.dir) {
			journal.State = turnTransactionPrepared
			return m.rollbackAfterCommitError(journal, err)
		}
	}
	// Cleanup is not part of the commit decision. A durable committed journal
	// is safe to leave behind: recovery rolls its after-images forward.
	_ = removeFileDurably(journalPath)
	return nil
}

func writeTurnJournal(path string, journal turnTransactionJournal) error {
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return errors.New("не удалось подготовить журнал транзакции")
	}
	return writeBytesAtomically(path, append(data, '\n'))
}

func committedJournalIsDurable(path, transactionID, dir string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	journal, err := decodeTurnTransactionJournal(data)
	if err != nil || journal.ID != transactionID || journal.State != turnTransactionCommitted {
		return false
	}
	return syncDirectory(dir) == nil
}

func (m *MemoryLayers) rollbackAfterCommitError(journal turnTransactionJournal, cause error) error {
	if err := m.rollbackTurnTransactionLocked(journal); err != nil {
		return errors.New("сбой транзакции; автоматическое восстановление будет повторено при следующем запуске")
	}
	if err := removeFileDurably(filepath.Join(m.dir, transactionJournalFileName)); err != nil {
		return errors.New("сбой транзакции; не удалось завершить автоматическое восстановление")
	}
	_ = cause
	return errors.New("не удалось атомарно сохранить пользовательский ход")
}

func (m *MemoryLayers) recoverTurnTransactionLocked() error {
	if m == nil || strings.TrimSpace(m.dir) == "" {
		return nil
	}
	path := filepath.Join(m.dir, transactionJournalFileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return errors.New("не удалось прочитать журнал незавершённой транзакции")
	}
	journal, err := decodeTurnTransactionJournal(data)
	if err != nil {
		return fmt.Errorf("повреждён журнал незавершённой транзакции: %w", err)
	}
	if journal.State == turnTransactionCommitted {
		if err := m.rollForwardTurnTransactionLocked(journal); err != nil {
			return errors.New("не удалось завершить зафиксированную транзакцию")
		}
	} else if err := m.rollbackTurnTransactionLocked(journal); err != nil {
		return errors.New("не удалось восстановить незавершённую транзакцию")
	}
	if err := removeFileDurably(path); err != nil {
		return errors.New("не удалось завершить восстановление транзакции")
	}
	return nil
}

func (m *MemoryLayers) rollForwardTurnTransactionLocked(journal turnTransactionJournal) error {
	for _, entry := range journal.Entries {
		path := filepath.Join(m.dir, filepath.FromSlash(entry.RelativePath))
		if err := writeBytesAtomically(path, entry.After); err != nil {
			return err
		}
	}
	return nil
}

func (m *MemoryLayers) rollbackTurnTransactionLocked(journal turnTransactionJournal) error {
	for index := len(journal.Entries) - 1; index >= 0; index-- {
		entry := journal.Entries[index]
		path := filepath.Join(m.dir, filepath.FromSlash(entry.RelativePath))
		if entry.BeforeExists {
			if err := writeBytesAtomically(path, entry.Before); err != nil {
				return err
			}
			continue
		}
		if err := removePathIfPresent(path); err != nil {
			return err
		}
	}
	return nil
}

func decodeTurnTransactionJournal(data []byte) (turnTransactionJournal, error) {
	var journal turnTransactionJournal
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil {
		return turnTransactionJournal{}, errors.New("некорректный JSON")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return turnTransactionJournal{}, errors.New("журнал должен содержать один JSON-объект")
	}
	if err := validateTurnTransactionJournal(journal); err != nil {
		return turnTransactionJournal{}, err
	}
	return journal, nil
}

func validateTurnTransactionJournal(journal turnTransactionJournal) error {
	if journal.Version != turnTransactionVersion ||
		(journal.State != turnTransactionPrepared && journal.State != turnTransactionCommitted) || journal.CreatedAt.IsZero() {
		return errors.New("неподдерживаемый формат журнала")
	}
	if !strings.HasPrefix(journal.ID, "tx-") || len(journal.ID) != 35 {
		return errors.New("некорректный ID транзакции")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(journal.ID, "tx-")); err != nil {
		return errors.New("некорректный ID транзакции")
	}
	if len(journal.Entries) == 0 || len(journal.Entries) > 4 {
		return errors.New("некорректное число целей транзакции")
	}
	seen := make(map[string]struct{}, len(journal.Entries))
	for _, entry := range journal.Entries {
		if !validTransactionTarget(entry.RelativePath) || len(entry.After) == 0 || (entry.BeforeExists && len(entry.Before) == 0) || (!entry.BeforeExists && len(entry.Before) != 0) {
			return errors.New("некорректная запись журнала")
		}
		if err := validateTransactionPayload(entry.RelativePath, entry.After); err != nil {
			return errors.New("некорректный новый снимок в журнале")
		}
		if entry.BeforeExists {
			if err := validateTransactionPayload(entry.RelativePath, entry.Before); err != nil {
				return errors.New("некорректный исходный снимок в журнале")
			}
		}
		if _, exists := seen[entry.RelativePath]; exists {
			return errors.New("повторяющаяся цель транзакции")
		}
		seen[entry.RelativePath] = struct{}{}
	}
	return nil
}

func validateTransactionPayload(relative string, data []byte) error {
	switch filepath.Base(relative) {
	case dialogIndexName:
		return validateEnvelopePayload(data, validateDialogIndex)
	case workingFileName:
		return validateEnvelopePayload(data, validateWorkingMemory)
	case shortTermFileName:
		return validateEnvelopePayload(data, validateShortTermMemory)
	case longTermFileName:
		return validateEnvelopePayload(data, validateLongTermMemory)
	default:
		return errors.New("неизвестный тип снимка")
	}
}

func validateEnvelopePayload[T any](data []byte, validate func(T) error) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var envelope jsonEnvelope[T]
	if err := decoder.Decode(&envelope); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("снимок должен содержать один JSON-объект")
	}
	if envelope.Version != 1 || envelope.UpdatedAt.IsZero() {
		return errors.New("неподдерживаемый формат снимка")
	}
	return validate(envelope.Data)
}

func validTransactionTarget(relative string) bool {
	relative = filepath.ToSlash(filepath.Clean(relative))
	if relative == dialogIndexName || relative == longTermFileName {
		return true
	}
	parts := strings.Split(relative, "/")
	if len(parts) != 3 || parts[0] != dialogsDirName || validateDialogID(parts[1]) != nil {
		return false
	}
	return parts[2] == workingFileName || parts[2] == shortTermFileName
}

func newTurnTransactionID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", errors.New("не удалось создать ID транзакции")
	}
	return "tx-" + hex.EncodeToString(random), nil
}

func readOptionalRegularFile(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, errors.New("не удалось прочитать исходный снимок транзакции")
	}
	return data, true, nil
}

func writeBytesAtomically(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".transaction-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func removePathIfPresent(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if err := os.Remove(path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func removeFileDurably(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func touchDialogInMemory(index *DialogIndex, id string, updatedAt time.Time) error {
	for i := range index.Dialogs {
		if index.Dialogs[i].ID == id {
			if updatedAt.Before(index.Dialogs[i].CreatedAt) {
				updatedAt = index.Dialogs[i].CreatedAt
			}
			index.Dialogs[i].UpdatedAt = updatedAt.UTC()
			return validateDialogIndex(*index)
		}
	}
	return fmt.Errorf("%w: %s", ErrDialogNotFound, id)
}

func (m *MemoryLayers) ensureIndexWithoutRecoveryLocked(ctx context.Context) (DialogIndex, error) {
	indexPath := filepath.Join(m.dir, dialogIndexName)
	if _, err := os.Stat(indexPath); err == nil {
		return m.dialogs.Load(ctx)
	}
	// Initialization/migration already uses ensureIndexLocked before a model
	// request. Refuse to invent an index inside a prepared turn transaction.
	return DialogIndex{}, errors.New("индекс диалогов не инициализирован")
}

func applyWorkingMutationsToCopy(memory WorkingMemory, mutations []workingMemoryMutation, now time.Time) (WorkingMemory, []bool, error) {
	memory = cloneWorkingMemory(memory)
	changed := make([]bool, len(mutations))
	for i, mutation := range mutations {
		switch mutation.kind {
		case workingMutationGoal:
			goal := strings.TrimSpace(mutation.value)
			if goal == "" {
				return WorkingMemory{}, nil, errors.New("цель рабочей памяти не может быть пустой")
			}
			if memory.Task != nil && memory.Goal != goal {
				return WorkingMemory{}, nil, memory.Task.conflict("set_goal", "", "цель активной задачи нельзя заменить; создайте новую задачу")
			}
			if memory.Task == nil {
				state, err := NewTaskState("", "", now)
				if err != nil {
					return WorkingMemory{}, nil, err
				}
				memory.Goal, memory.Task, changed[i] = goal, &state, true
			}
		case workingMutationStatus:
			if !validTaskStatus(mutation.status) {
				return WorkingMemory{}, nil, fmt.Errorf("неподдерживаемый статус рабочей памяти: %q", mutation.status)
			}
			if memory.Task != nil {
				expected := taskStatusForState(*memory.Task)
				if mutation.status != expected {
					return WorkingMemory{}, nil, memory.Task.conflict("set_legacy_status", "", fmt.Sprintf("status является производным от FSM и сейчас равен %q", expected))
				}
				break
			}
			if mutation.status != TaskStatusNotStarted {
				return WorkingMemory{}, nil, &TaskStateError{Operation: "set_legacy_status", AllowedTransitions: []TaskStage{TaskStagePlanning}, Reason: "сначала создайте задачу; старый status не управляет этапами"}
			}
			if memory.Status != mutation.status {
				memory.Status, changed[i] = mutation.status, true
			}
		case workingMutationNote:
			value := strings.TrimSpace(mutation.value)
			if value == "" {
				return WorkingMemory{}, nil, errors.New("заметка рабочей памяти не может быть пустой")
			}
			if !containsWorkingNote(memory.Notes, value) {
				memory.Notes = append(memory.Notes, WorkingNote{Content: value, RecordedAt: now})
				changed[i] = true
			}
		case workingMutationResult:
			value := strings.TrimSpace(mutation.value)
			if value == "" {
				return WorkingMemory{}, nil, errors.New("результат рабочей памяти не может быть пустым")
			}
			if !containsWorkingResult(memory.Results, value) {
				memory.Results = append(memory.Results, WorkingResult{Content: value, RecordedAt: now})
				changed[i] = true
			}
		case workingMutationTaskCreate:
			goal := strings.TrimSpace(mutation.value)
			if goal == "" {
				return WorkingMemory{}, nil, errors.New("цель задачи не может быть пустой")
			}
			if memory.Task != nil && memory.Task.Stage != TaskStageDone {
				return WorkingMemory{}, nil, memory.Task.conflict("create_task", "", "сначала завершите активную задачу")
			}
			state, err := NewTaskState(mutation.extra, mutation.expectedAction, now)
			if err != nil {
				return WorkingMemory{}, nil, err
			}
			memory, changed[i] = WorkingMemory{Goal: goal, Task: &state}, true
		default:
			state, err := requireTaskState(memory)
			if err != nil {
				return WorkingMemory{}, nil, err
			}
			var next TaskState
			switch mutation.kind {
			case workingMutationTaskProgress:
				next, err = state.UpdateProgress(mutation.value, mutation.expectedAction, now)
			case workingMutationPlanApprove:
				next, err = state.ApprovePlan(mutation.value, now)
			case workingMutationTaskTransition:
				next, err = state.Transition(mutation.stage, now)
			case workingMutationValidation:
				next, err = state.RecordValidation(mutation.validationPass, mutation.value, now)
			case workingMutationTaskPause:
				next, err = state.Pause(now)
			case workingMutationTaskResume:
				next, err = state.Resume(now)
			default:
				return WorkingMemory{}, nil, errors.New("неизвестная операция рабочей памяти")
			}
			if err != nil {
				return WorkingMemory{}, nil, err
			}
			changed[i] = next != state
			memory.Task = &next
		}
	}
	if memory.Task != nil {
		memory.Status = taskStatusForState(*memory.Task)
	} else if memory.Status == "" && (memory.Goal != "" || len(memory.Notes) > 0 || len(memory.Results) > 0) {
		memory.Status = TaskStatusNotStarted
	}
	if err := validateWorkingMemory(memory); err != nil {
		return WorkingMemory{}, nil, err
	}
	return memory, changed, nil
}

func applyLongTermMutationsToCopy(memory LongTermMemory, mutations []longTermMemoryMutation, now time.Time) (LongTermMemory, []bool, error) {
	memory = cloneLongTermMemory(memory)
	changed := make([]bool, len(mutations))
	for i, mutation := range mutations {
		switch mutation.kind {
		case longTermMutationProfile:
			found := false
			for entryIndex := range memory.Profile {
				if memory.Profile[entryIndex].Key == mutation.key {
					found = true
					if memory.Profile[entryIndex].Value != mutation.value {
						memory.Profile[entryIndex].Value = mutation.value
						memory.Profile[entryIndex].UpdatedAt = now
						changed[i] = true
					}
					break
				}
			}
			if !found {
				memory.Profile = append(memory.Profile, ProfileEntry{Key: mutation.key, Value: mutation.value, UpdatedAt: now})
				changed[i] = true
			}
		case longTermMutationDecision:
			if !containsDecision(memory.Decisions, mutation.value, mutation.extra) {
				memory.Decisions = append(memory.Decisions, Decision{Statement: mutation.value, Rationale: mutation.extra, RecordedAt: now})
				changed[i] = true
			}
		case longTermMutationKnowledge:
			if !containsKnowledge(memory.Knowledge, mutation.key, mutation.value) {
				memory.Knowledge = append(memory.Knowledge, Knowledge{Topic: mutation.key, Content: mutation.value, RecordedAt: now})
				changed[i] = true
			}
		default:
			return LongTermMemory{}, nil, errors.New("неизвестная операция долговременной памяти")
		}
	}
	if err := validateLongTermMemory(memory); err != nil {
		return LongTermMemory{}, nil, err
	}
	return memory, changed, nil
}
