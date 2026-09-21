package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type turnStateSnapshot struct {
	Working  WorkingMemory
	LongTerm LongTermMemory
	Short    ShortTermMemory
	Dialogs  DialogList
}

func captureTurnState(t *testing.T, layers *MemoryLayers, dialogID string) turnStateSnapshot {
	t.Helper()
	ctx := context.Background()
	working, err := layers.LoadDialogWorking(ctx, dialogID)
	if err != nil {
		t.Fatal(err)
	}
	longTerm, err := layers.LoadLongTerm(ctx)
	if err != nil {
		t.Fatal(err)
	}
	short, err := layers.LoadDialogShortTerm(ctx, dialogID)
	if err != nil {
		t.Fatal(err)
	}
	dialogs, err := layers.ListDialogs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return turnStateSnapshot{Working: working, LongTerm: longTerm, Short: short, Dialogs: dialogs}
}

func assertTurnStateUnchanged(t *testing.T, dir, dialogID string, before turnStateSnapshot) {
	t.Helper()
	after := captureTurnState(t, NewJSONMemoryLayers(dir), dialogID)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("failed turn changed state:\nbefore=%+v\nafter=%+v", before, after)
	}
	if _, err := os.Stat(filepath.Join(dir, transactionJournalFileName)); !os.IsNotExist(err) {
		t.Fatalf("transaction journal was not finalized: %v", err)
	}
}

func failTransactionTarget(layers *MemoryLayers, target string) {
	layers.transactionHooks = &turnTransactionHooks{beforeTargetWrite: func(_ int, relative string) error {
		if relative == target || filepath.Base(relative) == target {
			return errors.New("injected write failure")
		}
		return nil
	}}
}

func mixedTurnAgent(t *testing.T, layers *MemoryLayers, calls ...toolCall) *Agent {
	t.Helper()
	agent := scriptedAgent(t, layers, nil, []scriptedReply{
		{body: toolReply(calls...)},
		{body: finalReply("accepted answer")},
	}, nil)
	agent.cfg.Timeout = 10 * time.Second
	return agent
}

func TestAcceptedTurnRollsBackWorkingWhenLongTermWriteFails(t *testing.T) {
	dir := t.TempDir()
	layers, dialog := initializedLayers(t, dir)
	before := captureTurnState(t, layers, dialog.ID)
	failTransactionTarget(layers, longTermFileName)
	agent := mixedTurnAgent(t, layers,
		toolCallFor("working", "memory_add_working_note", `{"content":"ghost working note"}`),
		toolCallFor("long", "memory_add_knowledge", `{"topic":"ghost","content":"ghost knowledge"}`),
	)
	if _, err := agent.AskInDialog(context.Background(), dialog.ID, "commit atomically"); err == nil {
		t.Fatal("AskInDialog unexpectedly succeeded")
	}
	assertTurnStateUnchanged(t, dir, dialog.ID, before)
}

func TestAcceptedTurnRollsBackTaskStateWhenLongTermWriteFails(t *testing.T) {
	dir := t.TempDir()
	layers, dialog := initializedLayers(t, dir)
	before := captureTurnState(t, layers, dialog.ID)
	failTransactionTarget(layers, longTermFileName)
	agent := mixedTurnAgent(t, layers,
		toolCallFor("task", "task_create", `{"goal":"ghost task","current_step":"plan","expected_action":"approve"}`),
		toolCallFor("long", "memory_add_knowledge", `{"topic":"ghost","content":"ghost knowledge"}`),
	)
	if _, err := agent.AskInDialog(context.Background(), dialog.ID, "commit atomically"); err == nil {
		t.Fatal("AskInDialog unexpectedly succeeded")
	}
	assertTurnStateUnchanged(t, dir, dialog.ID, before)
}

func TestAcceptedTurnRollsBackPendingWhenShortTermWriteFails(t *testing.T) {
	dir := t.TempDir()
	layers, dialog := initializedLayers(t, dir)
	before := captureTurnState(t, layers, dialog.ID)
	failTransactionTarget(layers, shortTermFileName)
	agent := mixedTurnAgent(t, layers,
		toolCallFor("working", "memory_add_working_note", `{"content":"ghost working note"}`),
		toolCallFor("task", "task_create", `{"goal":"ghost task","current_step":"plan","expected_action":"approve"}`),
		toolCallFor("long", "memory_add_knowledge", `{"topic":"ghost","content":"ghost knowledge"}`),
	)
	if _, err := agent.AskInDialog(context.Background(), dialog.ID, "commit atomically"); err == nil {
		t.Fatal("AskInDialog unexpectedly succeeded")
	}
	assertTurnStateUnchanged(t, dir, dialog.ID, before)
}

func TestAcceptedTurnRollsBackWhenDialogMetadataWriteFails(t *testing.T) {
	dir := t.TempDir()
	layers, dialog := initializedLayers(t, dir)
	before := captureTurnState(t, layers, dialog.ID)
	failTransactionTarget(layers, dialogIndexName)
	agent := mixedTurnAgent(t, layers,
		toolCallFor("working", "memory_add_working_note", `{"content":"ghost working note"}`),
		toolCallFor("long", "memory_add_knowledge", `{"topic":"ghost","content":"ghost knowledge"}`),
	)
	if _, err := agent.AskInDialog(context.Background(), dialog.ID, "commit atomically"); err == nil {
		t.Fatal("AskInDialog unexpectedly succeeded")
	}
	assertTurnStateUnchanged(t, dir, dialog.ID, before)
}

func TestAcceptedTurnRollsBackWhenFirstTargetWriteFails(t *testing.T) {
	dir := t.TempDir()
	layers, dialog := initializedLayers(t, dir)
	before := captureTurnState(t, layers, dialog.ID)
	layers.transactionHooks = &turnTransactionHooks{beforeTargetWrite: func(index int, _ string) error {
		if index == 0 {
			return errors.New("injected first-write failure")
		}
		return nil
	}}
	agent := mixedTurnAgent(t, layers, toolCallFor("working", "memory_add_working_note", `{"content":"ghost"}`))
	if _, err := agent.AskInDialog(context.Background(), dialog.ID, "commit atomically"); err == nil {
		t.Fatal("AskInDialog unexpectedly succeeded")
	}
	assertTurnStateUnchanged(t, dir, dialog.ID, before)
}

func TestInterruptedCommitRecoversAfterEveryTargetReplacement(t *testing.T) {
	for replaced := 1; replaced <= 4; replaced++ {
		t.Run(fmt.Sprintf("after_target_%d", replaced), func(t *testing.T) {
			dir := t.TempDir()
			layers, dialog := initializedLayers(t, dir)
			before := captureTurnState(t, layers, dialog.ID)
			journal := prepareMixedTurnJournal(t, layers, dialog.ID)
			writeJournalForTest(t, dir, journal)
			for index := 0; index < replaced; index++ {
				entry := journal.Entries[index]
				if err := writeBytesAtomically(filepath.Join(dir, filepath.FromSlash(entry.RelativePath)), entry.After); err != nil {
					t.Fatal(err)
				}
			}

			restarted := NewJSONMemoryLayers(dir)
			if _, err := restarted.Initialize(context.Background()); err != nil {
				t.Fatalf("recovery: %v", err)
			}
			if after := captureTurnState(t, restarted, dialog.ID); !reflect.DeepEqual(before, after) {
				t.Fatalf("recovery after %d replacements changed state:\nbefore=%+v\nafter=%+v", replaced, before, after)
			}
			secondRestart := NewJSONMemoryLayers(dir)
			if _, err := secondRestart.Initialize(context.Background()); err != nil {
				t.Fatalf("idempotent recovery: %v", err)
			}
			if after := captureTurnState(t, secondRestart, dialog.ID); !reflect.DeepEqual(before, after) {
				t.Fatalf("second recovery changed state: %+v", after)
			}
		})
	}
}

func TestCommittedJournalRecoveryRollsForwardIdempotently(t *testing.T) {
	dir := t.TempDir()
	layers, dialog := initializedLayers(t, dir)
	journal := prepareMixedTurnJournal(t, layers, dialog.ID)
	journal.State = turnTransactionCommitted
	for _, entry := range journal.Entries {
		if err := writeBytesAtomically(filepath.Join(dir, filepath.FromSlash(entry.RelativePath)), entry.After); err != nil {
			t.Fatal(err)
		}
	}
	writeJournalForTest(t, dir, journal)

	restarted := NewJSONMemoryLayers(dir)
	if _, err := restarted.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := captureTurnState(t, restarted, dialog.ID)
	if len(first.Working.Notes) != 1 || len(first.LongTerm.Knowledge) != 1 || len(first.Short.Turns) != 1 {
		t.Fatalf("committed transaction was not rolled forward: %+v", first)
	}
	secondRestart := NewJSONMemoryLayers(dir)
	if _, err := secondRestart.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := captureTurnState(t, secondRestart, dialog.ID)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("committed recovery was not idempotent:\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func TestCorruptedTransactionJournalFailsClosedBeforeTransport(t *testing.T) {
	dir := t.TempDir()
	layers, dialog := initializedLayers(t, dir)
	before := captureTurnState(t, layers, dialog.ID)
	if err := os.WriteFile(filepath.Join(dir, transactionJournalFileName), []byte(`{"version":1,"broken":`), 0o600); err != nil {
		t.Fatal(err)
	}
	transportCalls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		transportCalls++
		return response(http.StatusOK, finalReply("must not run")), nil
	})}
	agent := NewAgent(AgentConfig{APIKey: "key", BaseURL: "https://example.test", Timeout: time.Second, Memory: layers}, client)
	if _, err := agent.AskInDialog(context.Background(), dialog.ID, "do not call transport"); err == nil || !strings.Contains(err.Error(), "журнал") {
		t.Fatalf("corrupted journal error=%v", err)
	}
	if transportCalls != 0 {
		t.Fatalf("transport calls=%d", transportCalls)
	}
	if err := os.Remove(filepath.Join(dir, transactionJournalFileName)); err != nil {
		t.Fatal(err)
	}
	if after := captureTurnState(t, layers, dialog.ID); !reflect.DeepEqual(before, after) {
		t.Fatalf("corrupted journal changed state: %+v", after)
	}
}

func TestSuccessfulMixedTurnCommitsEveryLayerExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	layers, dialog := initializedLayers(t, dir)
	agent := mixedTurnAgent(t, layers,
		toolCallFor("task", "task_create", `{"goal":"goal","current_step":"plan","expected_action":"approve"}`),
		toolCallFor("note", "memory_add_working_note", `{"content":"one note"}`),
		toolCallFor("note-duplicate", "memory_add_working_note", `{"content":"one note"}`),
		toolCallFor("long", "memory_add_knowledge", `{"topic":"topic","content":"knowledge"}`),
	)
	result, err := agent.AskInDialog(context.Background(), dialog.ID, "mixed turn")
	if err != nil {
		t.Fatal(err)
	}
	state := captureTurnState(t, layers, dialog.ID)
	if state.Working.Task == nil || len(state.Working.Notes) != 1 || len(state.LongTerm.Knowledge) != 1 || len(state.Short.Turns) != 1 {
		t.Fatalf("mixed turn not committed exactly once: %+v", state)
	}
	if len(result.MemoryUpdates) != 3 {
		t.Fatalf("memory_updates=%+v", result.MemoryUpdates)
	}
	if _, err := os.Stat(filepath.Join(dir, transactionJournalFileName)); !os.IsNotExist(err) {
		t.Fatalf("journal remains after success: %v", err)
	}
}

func TestConcurrentTurnsAndDirectWorkingWritesDoNotLoseUpdates(t *testing.T) {
	layers, dialog := initializedLayers(t, t.TempDir())
	const count = 12
	var wg sync.WaitGroup
	errs := make(chan error, count*2)
	for index := 0; index < count; index++ {
		index := index
		wg.Add(2)
		go func() {
			defer wg.Done()
			note := fmt.Sprintf("turn-note-%d", index)
			agent := mixedTurnAgent(t, layers, toolCallFor(fmt.Sprintf("call-%d", index), "memory_add_working_note", fmt.Sprintf(`{"content":%q}`, note)))
			_, err := agent.AskInDialog(context.Background(), dialog.ID, fmt.Sprintf("turn-%d", index))
			errs <- err
		}()
		go func() {
			defer wg.Done()
			_, err := layers.AddDialogWorkingNote(context.Background(), dialog.ID, fmt.Sprintf("api-note-%d", index))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	state := captureTurnState(t, layers, dialog.ID)
	if len(state.Working.Notes) != count*2 || len(state.Short.Turns) != count {
		t.Fatalf("lost concurrent update: notes=%d turns=%d", len(state.Working.Notes), len(state.Short.Turns))
	}
}

func prepareMixedTurnJournal(t *testing.T, layers *MemoryLayers, dialogID string) turnTransactionJournal {
	t.Helper()
	ctx := context.Background()
	working, _ := layers.LoadDialogWorking(ctx, dialogID)
	working, _, err := applyWorkingMutationsToCopy(working, []workingMemoryMutation{{kind: workingMutationNote, value: "after"}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	longTerm, _ := layers.LoadLongTerm(ctx)
	longTerm, _, err = applyLongTermMutationsToCopy(longTerm, []longTermMemoryMutation{{kind: longTermMutationKnowledge, key: "after", value: "after"}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	short, _ := layers.LoadDialogShortTerm(ctx, dialogID)
	now := time.Now().UTC()
	short.Turns = append(short.Turns, ConversationTurn{User: "after", Assistant: "after", CreatedAt: now})
	index, err := layers.dialogs.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := touchDialogInMemory(&index, dialogID, now); err != nil {
		t.Fatal(err)
	}
	workingEntry, err := layers.transactionEntry(layers.workingStore(dialogID).file.path, working)
	if err != nil {
		t.Fatal(err)
	}
	longEntry, err := layers.transactionEntry(filepath.Join(layers.dir, longTermFileName), longTerm)
	if err != nil {
		t.Fatal(err)
	}
	shortEntry, err := layers.transactionEntry(layers.shortTermStore(dialogID).file.path, short)
	if err != nil {
		t.Fatal(err)
	}
	dialogEntry, err := layers.transactionEntry(layers.dialogs.path, index)
	if err != nil {
		t.Fatal(err)
	}
	return turnTransactionJournal{
		Version: turnTransactionVersion, ID: "tx-0123456789abcdef0123456789abcdef", State: turnTransactionPrepared,
		CreatedAt: now, Entries: []turnTransactionEntry{workingEntry, longEntry, shortEntry, dialogEntry},
	}
}

func writeJournalForTest(t *testing.T, dir string, journal turnTransactionJournal) {
	t.Helper()
	data, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeBytesAtomically(filepath.Join(dir, transactionJournalFileName), data); err != nil {
		t.Fatal(err)
	}
}
