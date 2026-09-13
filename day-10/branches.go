package main

import (
	"fmt"
	"strings"
)

const (
	defaultBranchID    = "main"
	defaultBranchTitle = "Main"
)

func normalizeBranchState(state ConversationState) ConversationState {
	branches := cloneBranchState(state.Branches)
	if len(branches.Items) == 0 {
		branches = BranchState{
			ActiveBranchID: defaultBranchID,
			Items: []ConversationBranch{{
				ID:       defaultBranchID,
				Title:    defaultBranchTitle,
				Messages: cloneMessages(state.Messages),
			}},
		}
		state.Branches = branches
		return state
	}

	for i := range branches.Items {
		branches.Items[i].ID = strings.TrimSpace(branches.Items[i].ID)
		branches.Items[i].Title = strings.TrimSpace(branches.Items[i].Title)
		if branches.Items[i].Title == "" {
			branches.Items[i].Title = branches.Items[i].ID
		}
	}
	if findBranchIndex(branches, branches.ActiveBranchID) == -1 {
		branches.ActiveBranchID = branches.Items[0].ID
	}
	state.Branches = branches
	return state
}

func activeBranch(branches BranchState) ConversationBranch {
	if len(branches.Items) == 0 {
		return ConversationBranch{ID: defaultBranchID, Title: defaultBranchTitle}
	}
	index := findBranchIndex(branches, branches.ActiveBranchID)
	if index == -1 {
		index = 0
	}
	return branches.Items[index]
}

func activeBranchIndex(branches BranchState) int {
	index := findBranchIndex(branches, branches.ActiveBranchID)
	if index == -1 && len(branches.Items) > 0 {
		return 0
	}
	return index
}

func findBranchIndex(branches BranchState, id string) int {
	for i, branch := range branches.Items {
		if branch.ID == id {
			return i
		}
	}
	return -1
}

func visibleBranchHistory(state ConversationState) []chatMessage {
	state = normalizeBranchState(state)
	branch := activeBranch(state.Branches)
	history := make([]chatMessage, 0, len(state.Branches.Checkpoint)+len(branch.Messages))
	history = append(history, state.Branches.Checkpoint...)
	history = append(history, branch.Messages...)
	return history
}

func nextBranchID(branches BranchState) string {
	seen := make(map[string]struct{}, len(branches.Items))
	for _, branch := range branches.Items {
		seen[branch.ID] = struct{}{}
	}
	for i := 1; ; i++ {
		id := fmt.Sprintf("branch-%d", i)
		if _, exists := seen[id]; !exists {
			return id
		}
	}
}

func defaultNewBranchTitle(branches BranchState) string {
	return fmt.Sprintf("Alternative %d", len(branches.Items))
}
