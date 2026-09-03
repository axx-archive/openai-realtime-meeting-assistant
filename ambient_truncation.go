package main

// Truncation recovery for every window-driven ambient worker (Wave 8 D11
// follow-up, 2026-09-02 gen-248 post-deploy).
//
// The meeting digest was the only worker that knew how to recover from
// max_output_truncation (meeting_digest.go: retry once with 50% more output
// headroom, mark the record Stuck). Every other worker treated the truncation
// as an ordinary output rejection: the runner held the cursor, retried the
// identical request with the identical envelope, and after
// ambientProviderMaxWindowAttempts opened a restart-required provider circuit.
// Production wedged mission intelligence on ONE brain input exactly this way
// (`opened its provider circuit after 4 failures on input brain-…`), and the
// day-digest reflection failed twice the same minute.
//
// This file is the shared seam. ambientTruncationRetry runs a worker's model
// call over its input window and, on max_output_truncation only:
//
//  1. retries ONCE — with the window halved when it holds two or more inputs
//     (the chassis keeps the OLDEST half so the cursor stays contiguous and the
//     dropped newer half re-feeds next pass; a look-back caller keeps the
//     newest AND raises the budget, because its second truncation abandons the
//     day for good), or, for a single input, with the output budget raised once
//     (the digest's 1.5x precedent, capped) where the budget has headroom;
//  2. if the retry truncates too, the head input is STUCK. The chassis wrapper
//     skips it — durable baseline advanced past it, a dead-letter tombstone
//     stamped with the reason — and the pass returns cleanly, so nothing counts
//     toward the circuit and the remainder of the window runs next pass. The
//     capability snapshot surfaces the count as stuckInputs.
//
// Provider outages and every other rejection keep their existing
// classification: only the truncation reason ever enters this path.

import (
	"context"
	"fmt"
	"strings"
)

const (
	ambientTruncationReason = "max_output_truncation"
	// ambientTruncationRetryOutputTokenCap bounds the one-time output-budget
	// raise for a single-input window. The narrative maintainer already asks
	// for 12000; the cap keeps the raise inside the Responses envelope instead
	// of turning one truncation into an unbounded bill.
	ambientTruncationRetryOutputTokenCap = 16000
	// deadLetterReasonMetadataKey stamps WHY a window was abandoned. A stuck
	// input carries ambientTruncationReason so the capability snapshot can
	// count truncation skips separately from poison dead letters.
	deadLetterReasonMetadataKey = "deadLetterReason"
)

// ambientWindowOutcome is what one recovered call produced. Inputs is the
// window the accepted output actually covers — the caller's cursor metadata
// MUST follow it, never the original window, because a halved retry consumed
// only the prefix (or, for a look-back, the suffix).
type ambientWindowOutcome[T any] struct {
	Value           T
	Inputs          []meetingMemoryEntry
	MaxOutputTokens int
	// Recovered marks a first attempt that truncated and a retry that landed.
	Recovered bool
	// Skipped marks a head input abandoned after the retry truncated too. The
	// caller must return a clean pass (no artifact, nil error).
	Skipped bool
	// Attempts is the number of provider calls spent (1 or 2).
	Attempts int
}

func isAmbientTruncation(err error) bool {
	reason, rejected := openAIOutputRejectionReason(err)
	return rejected && reason == ambientTruncationReason
}

// ambientTruncationRaisedOutputBudget is the one-time budget raise: 1.5x the
// current budget (the digest precedent), capped. Zero (provider default)
// cannot be raised.
func ambientTruncationRaisedOutputBudget(current int) int {
	if current <= 0 {
		return 0
	}
	raised := current * 3 / 2
	if raised > ambientTruncationRetryOutputTokenCap {
		raised = ambientTruncationRetryOutputTokenCap
	}
	return raised
}

// ambientTruncationRetryPlan decides the single retry: keep half the inputs
// (rounded up) for a multi-input window, or raise the budget for one input.
// ok=false means no materially different attempt exists (one input already at
// the cap), so the caller goes straight to the stuck path without a second
// identical call.
func ambientTruncationRetryPlan(inputCount int, maxOutputTokens int) (keep int, retryBudget int, ok bool) {
	if inputCount >= 2 {
		return (inputCount + 1) / 2, maxOutputTokens, true
	}
	raised := ambientTruncationRaisedOutputBudget(maxOutputTokens)
	if raised <= maxOutputTokens {
		return inputCount, maxOutputTokens, false
	}
	return inputCount, raised, true
}

// ambientTruncationLookbackRetryPlan is the look-back variant of the plan
// (keepNewest callers: the daily reflection). A look-back's window is a
// digest SUMMARY set, not a stream of raw inputs, and its budget is the
// tightest in the fleet (reflectionMaxOutputTokens = 700): production's
// `day digest reflection failed: max_output_truncation` was the output /
// reasoning envelope running out, not input density, so halving the look-back
// at an unchanged budget re-runs the same envelope and truncates identically —
// and the second truncation abandons that day permanently (a tombstone
// maybeEmitDailyReflection treats as a hard skip, which no restart or raised
// constant can undo). The one retry therefore halves the window AND raises the
// budget once, so it is materially different before the day is given up. The
// chassis path (keepNewest=false) keeps its pinned unchanged budget.
func ambientTruncationLookbackRetryPlan(inputCount int, maxOutputTokens int) (keep int, retryBudget int, ok bool) {
	keep, retryBudget, ok = ambientTruncationRetryPlan(inputCount, maxOutputTokens)
	if !ok || inputCount < 2 {
		return keep, retryBudget, ok
	}
	if raised := ambientTruncationRaisedOutputBudget(maxOutputTokens); raised > retryBudget {
		retryBudget = raised
	}
	return keep, retryBudget, true
}

// ambientTruncationRetry is the provider-side core: one call, at most one
// retry, then onStuck. keepNewest selects which half survives the halving
// (false = oldest half, the chassis default; true = newest, for look-backs
// whose cursor has already moved). onStuck receives the attempts spent and
// must make the abandonment durable and visible; its error is returned as-is.
func ambientTruncationRetry[T any](agent ambientAgentConfig, inputs []meetingMemoryEntry, maxOutputTokens int, keepNewest bool, call func(window []meetingMemoryEntry, maxOutputTokens int) (T, error), onStuck func(attempts int) error) (ambientWindowOutcome[T], error) {
	outcome := ambientWindowOutcome[T]{Inputs: inputs, MaxOutputTokens: maxOutputTokens, Attempts: 1}
	value, err := call(inputs, maxOutputTokens)
	outcome.Value = value
	if err == nil || !isAmbientTruncation(err) || len(inputs) == 0 {
		return outcome, err
	}
	plan := ambientTruncationRetryPlan
	if keepNewest {
		plan = ambientTruncationLookbackRetryPlan
	}
	keep, retryBudget, ok := plan(len(inputs), maxOutputTokens)
	if ok {
		retryInputs := inputs[:keep]
		if keepNewest {
			retryInputs = inputs[len(inputs)-keep:]
		}
		outcome.Attempts++
		log.Warnf("%s output truncated over %d input(s); retrying once with %d input(s) and a %d-token output budget", agent.name, len(inputs), len(retryInputs), retryBudget)
		value, err = call(retryInputs, retryBudget)
		if err == nil || !isAmbientTruncation(err) {
			outcome.Value, outcome.Inputs, outcome.MaxOutputTokens, outcome.Recovered = value, retryInputs, retryBudget, err == nil
			return outcome, err
		}
	}
	if onStuck != nil {
		if stuckErr := onStuck(outcome.Attempts); stuckErr != nil {
			return outcome, stuckErr
		}
	}
	var zero T
	return ambientWindowOutcome[T]{Value: zero, MaxOutputTokens: maxOutputTokens, Skipped: true, Attempts: outcome.Attempts}, nil
}

// ambientWindowWithTruncationRecovery is the chassis wrapper every generic
// produce uses: oldest-half halving, and a stuck head skipped through
// skipAmbientStuckInput (baseline advanced, tombstone, clean return).
func ambientWindowWithTruncationRecovery[T any](app *kanbanBoardApp, agent ambientAgentConfig, inputs []meetingMemoryEntry, maxOutputTokens int, call func(window []meetingMemoryEntry, maxOutputTokens int) (T, error)) (ambientWindowOutcome[T], error) {
	return ambientTruncationRetry(agent, inputs, maxOutputTokens, false, call, func(attempts int) error {
		if len(inputs) == 0 {
			return nil
		}
		return app.skipAmbientStuckInput(agent, inputs[0], ambientWindowRoomID(inputs), ambientTruncationReason, attempts)
	})
}

// skipAmbientStuckInput abandons ONE head input the provider cannot fold even
// with the halved window: the durable checkpoint baseline moves past it first
// (a persistence fault holds instead — the same fail-closed order the poison
// dead-letter path uses), then the in-memory baseline follows and a
// dead-letter tombstone records the reason so coverage and the capability
// snapshot (stuckInputs) stay honest. The remainder of the window is untouched
// and runs on the next pass.
func (app *kanbanBoardApp) skipAmbientStuckInput(agent ambientAgentConfig, head meetingMemoryEntry, roomID string, reason string, attempts int) error {
	if app == nil || app.memory == nil || strings.TrimSpace(head.ID) == "" {
		return nil
	}
	roomID = normalizeRoomID(roomID)
	key := ambientAgentScopeKey(agent, roomID)
	if err := app.persistAmbientCheckpointBaseline(agent, head.ID, roomID); err != nil {
		app.recordAmbientAgentCheckpointFailure(agent, head.ID, roomID, err)
		return &ambientAgentHoldError{err: fmt.Errorf("%s could not durably skip stuck input %s: %w", agent.name, head.ID, err)}
	}
	app.setAmbientAgentBaselineID(key, head.ID)
	log.Errorf("%s skipped stuck input %s: %s persisted across %d attempt(s); baseline advanced past it and the skip is counted as stuckInputs", key, head.ID, reason, attempts)
	app.appendAmbientDeadLetterTombstone(agent, head.ID, roomID, attempts, reason)
	return nil
}

// countStuckInputs is the capability-snapshot count of inputs one worker
// skipped for truncation (a subset of its dead letters).
func (store *meetingMemoryStore) countStuckInputs(agentName string) int {
	if store == nil {
		return 0
	}
	agentName = strings.TrimSpace(agentName)
	store.mu.RLock()
	defer store.mu.RUnlock()
	count := 0
	for _, entry := range store.entries {
		if entry.Kind != meetingMemoryKindDeadLetter || memoryEntryIsMediaSoakCanary(entry) {
			continue
		}
		if strings.TrimSpace(entry.Metadata[deadLetterAgentMetadataKey]) == agentName && strings.TrimSpace(entry.Metadata[deadLetterReasonMetadataKey]) == ambientTruncationReason {
			count++
		}
	}
	return count
}

// ambientOutputBudget threads a retry's output budget to a model call nested
// below the produce seam (the entity ledger adjudicates inside its
// consolidation body) without widening every signature on the way down.
type ambientOutputBudgetContextKey struct{}

func withAmbientOutputBudget(ctx context.Context, maxOutputTokens int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ambientOutputBudgetContextKey{}, maxOutputTokens)
}

func ambientOutputBudget(ctx context.Context, fallback int) int {
	if ctx != nil {
		if budget, ok := ctx.Value(ambientOutputBudgetContextKey{}).(int); ok && budget > 0 {
			return budget
		}
	}
	return fallback
}
