#!/usr/bin/env bash
set -euo pipefail
cd /Users/romansurzhan/ai_challenge
GOCACHE=/tmp/day11-web-demo-smoke-cache go test -count=1 ./day-11-15 -run '^(TestSuccessfulMixedTurnCommitsEveryLayerExactlyOnce|TestDifferentProfilesProduceDifferentRequestsAndDeterministicAnswers|TestTaskTransitionGuardsAndHappyPath|TestInvariantPostCheckRetriesAndReturnsOnlyCorrectedAnswer|TestTaskHTTPConflictContainsMachineStateAndDoesNotWriteHistory)$'
