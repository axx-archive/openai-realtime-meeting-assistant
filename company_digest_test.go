package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

/* ---------- helpers ---------- */

// runCompanyPass drives one company-digest agent pass through the real runner
// (cursor + run-lock semantics included).
func runCompanyPass(t *testing.T, app *kanbanBoardApp, responder openAITextResponder) meetingMemoryEntry {
	t.Helper()
	entry, err := app.runAmbientAgentOnce(companyDigestAgent(), context.Background(), "test-key", responder, 1)
	if err != nil {
		t.Fatalf("company digest pass: %v", err)
	}

	return entry
}

func newestLedgerEventID(t *testing.T, app *kanbanBoardApp) string {
	t.Helper()
	events := app.memory.entriesOfKind(meetingMemoryKindLedgerEvent, 0)
	if len(events) == 0 {
		t.Fatal("no ledger events in the store")
	}

	return events[len(events)-1].ID
}

/* ---------- T4 = ledger state + thin narrative (amendment A2) ---------- */

// The company digest is the ledger's CURRENT state (deterministic Go
// projection: open records in, closed records out) plus the model's THIN
// delta narrative — and it is mint-free and cursor-stamped.
func TestCompanyDigestIsLedgerStateViewPlusThinNarrative(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)

	// meeting-a's digest lands four facts in the ledger, then a rebuild closes
	// the pricing-sheet action item.
	upsertLedgerTestDigest(t, app, "meeting-a", fullLedgerTestPayload())
	runLedgerPass(t, app, forbiddenLedgerResponder(t))
	closedPayload := fullLedgerTestPayload()
	closedPayload.ActionItems[0].Status = "done"
	upsertLedgerTestDigest(t, app, "meeting-a", closedPayload)
	runLedgerPass(t, app, forbiddenLedgerResponder(t))

	var got openAITextRequest
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		got = request
		return "  The Zebra pilot is decided (attributed to AJ); the pricing sheet closed out.  ", nil
	}
	entry := runCompanyPass(t, app, responder)

	if entry.Kind != meetingMemoryKindCompanyDigest || digestEntryKey(entry) != companyDigestKey {
		t.Fatalf("entry kind/key = %s/%s, want %s/%s", entry.Kind, digestEntryKey(entry), meetingMemoryKindCompanyDigest, companyDigestKey)
	}
	if !digestEntryCurrent(entry) {
		t.Fatalf("company digest not current: %+v", entry.Metadata)
	}
	if got := entry.Metadata[companyDigestCursorMetadataKey]; got != newestLedgerEventID(t, app) {
		t.Fatalf("cursor = %q, want the newest ledger event id", got)
	}
	// mint-free: a company digest describes no live meeting.
	if got := entry.Metadata["meetingId"]; got != "" {
		t.Fatalf("company digest stamped meetingId %q, want none", got)
	}
	if got := app.memory.currentMeetingID(); got != "" {
		t.Fatalf("company digest pass minted a meeting id %q at idle", got)
	}

	payload, ok := parseCompanyDigest(entry.Text)
	if !ok {
		t.Fatalf("stored company digest is not JSON: %s", entry.Text)
	}
	if payload.Narrative != "The Zebra pilot is decided (attributed to AJ); the pricing sheet closed out." {
		t.Fatalf("narrative = %q, want the trimmed model output", payload.Narrative)
	}
	if len(payload.State.Decisions) != 1 {
		t.Fatalf("state decisions = %+v, want exactly the open Zebra decision", payload.State.Decisions)
	}
	decision := payload.State.Decisions[0]
	if decision.Title != "Choose vendor Zebra for the packaging pilot" || decision.Status != ledgerStatusActive || decision.Importance != 5 {
		t.Fatalf("decision record = %+v", decision)
	}
	if decision.Anchor != "tx-1" {
		t.Fatalf("decision anchor = %q, want the ledger record's drill-down anchor", decision.Anchor)
	}
	found := false
	for _, meeting := range decision.Meetings {
		if meeting == "meeting-a" {
			found = true
		}
	}
	if !found {
		t.Fatalf("decision meetings = %v, want provenance meeting-a", decision.Meetings)
	}
	// the CLOSED action item must be OUT of the state view (state = current
	// records only, closure is the delta narrative's job).
	if len(payload.State.ActionItems) != 0 {
		t.Fatalf("state actionItems = %+v, want none (closed record excluded)", payload.State.ActionItems)
	}
	if len(payload.State.Topics) != 1 || len(payload.State.OpenQuestions) != 1 {
		t.Fatalf("state topics/questions = %+v/%+v, want the open ones", payload.State.Topics, payload.State.OpenQuestions)
	}

	// the model saw the DELTAS, and the contract is a thin refresh.
	if !strings.Contains(got.Input, "close action_item: Draft the pricing sheet") {
		t.Fatalf("narrative input missing the close delta:\n%s", got.Input)
	}
	if !strings.Contains(got.Instructions, "THIN running narrative") || !strings.Contains(got.Instructions, "never summarize other summaries") {
		t.Fatalf("instructions missing the A2 thin-narrative contract: %s", got.Instructions)
	}
	if !strings.Contains(got.Instructions, "hedge") {
		t.Fatalf("instructions missing hedged attribution: %s", got.Instructions)
	}

	if latest, ok := app.memory.latestCompanyDigest(); !ok || latest.ID != entry.ID {
		t.Fatal("latestCompanyDigest does not return the new digest")
	}
}

// Amendment A2's core prohibition: T4 never re-summarizes summaries. The
// narrative prompt is deltas + compact state records — day digest bodies,
// meeting digest JSON, brain prose, and artifact blobs are structurally
// absent, and the whole input stays small.
func TestCompanyDigestNeverResummarizesRollups(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)

	if _, appended, err := app.memory.appendBrainWriteUp("brain-marker", "## Overview\nBRAIN-MARKER prose that must never reach the company prompt.", nil); err != nil || !appended {
		t.Fatalf("append brain: appended=%v err=%v", appended, err)
	}
	if _, _, err := app.memory.appendOSArtifact("blob-marker", "data:image/png;base64,BLOBMARKER"+strings.Repeat("A", 9000), map[string]string{"title": "deck"}); err != nil {
		t.Fatalf("append artifact: %v", err)
	}
	if _, err := app.memory.upsertDigest(meetingMemoryKindDayDigest, "2026-07-06",
		`{"day":"2026-07-06","meetings":[{"meetingId":"meeting-a"}],"themes":["DAY-MARKER-THEME"]}`,
		map[string]string{digestDayMetadataKey: "2026-07-06"}); err != nil {
		t.Fatalf("upsert day digest: %v", err)
	}
	upsertLedgerTestDigest(t, app, "meeting-a", fullLedgerTestPayload())
	runLedgerPass(t, app, forbiddenLedgerResponder(t))

	var got openAITextRequest
	entry := runCompanyPass(t, app, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		got = request
		return "Momentum holds on the packaging pilot.", nil
	})

	for _, forbidden := range []string{"BRAIN-MARKER", "DAY-MARKER-THEME", ";base64,", "BLOBMARKER"} {
		if strings.Contains(got.Input, forbidden) {
			t.Fatalf("company narrative input leaked %q:\n%s", forbidden, got.Input)
		}
	}
	if len(got.Input) > 10000 {
		t.Fatalf("company narrative input is %d bytes, want a small delta+state prompt", len(got.Input))
	}
	if payload, ok := parseCompanyDigest(entry.Text); !ok || len(payload.State.Decisions) != 1 {
		t.Fatalf("state view missing the ledger decision: %s", entry.Text)
	}
}

// A model failure upserts nothing and leaves the cursor put: the prior digest
// stays current and the SAME delta window re-feeds the retry (the
// mission-intel precedent), then the retry supersedes in place.
func TestCompanyDigestModelFailureKeepsPriorAndRefeeds(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)

	upsertLedgerTestDigest(t, app, "meeting-a", fullLedgerTestPayload())
	runLedgerPass(t, app, forbiddenLedgerResponder(t))
	first := runCompanyPass(t, app, func(context.Context, string, openAITextRequest) (string, error) {
		return "narrative one", nil
	})
	firstCursor := first.Metadata[companyDigestCursorMetadataKey]

	// new ledger delta: a second decision lands.
	grown := fullLedgerTestPayload()
	grown.Decisions = append(grown.Decisions, meetingDigestDecision{
		D: "Kill the legacy pricing page", By: "attributed to Tyler", Status: "decided",
		Anchor: "tx-9", At: "2026-07-06T11:00:00Z", Importance: 4,
	})
	upsertLedgerTestDigest(t, app, "meeting-a", grown)
	runLedgerPass(t, app, forbiddenLedgerResponder(t))

	if _, err := app.runAmbientAgentOnce(companyDigestAgent(), context.Background(), "test-key",
		func(context.Context, string, openAITextRequest) (string, error) {
			return "", errors.New("model down")
		}, 1); err == nil {
		t.Fatal("company pass swallowed the model error")
	}
	latest, ok := app.memory.latestCompanyDigest()
	if !ok || latest.ID != first.ID {
		t.Fatalf("failed pass replaced the prior digest: %+v", latest.Metadata)
	}
	if got := latest.Metadata[companyDigestCursorMetadataKey]; got != firstCursor {
		t.Fatalf("failed pass advanced the cursor to %q", got)
	}

	var retryInput string
	retry := runCompanyPass(t, app, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		retryInput = request.Input
		return "narrative three", nil
	})
	if !strings.Contains(retryInput, "Kill the legacy pricing page") {
		t.Fatalf("retry did not re-feed the unconsumed delta window:\n%s", retryInput)
	}
	payload, ok := parseCompanyDigest(retry.Text)
	if !ok || payload.Narrative != "narrative three" || len(payload.State.Decisions) != 2 {
		t.Fatalf("retry digest = %s", retry.Text)
	}
	if got := retry.Metadata[companyDigestCursorMetadataKey]; got != newestLedgerEventID(t, app) {
		t.Fatalf("retry cursor = %q, want the newest ledger event id", got)
	}

	// exactly one current company digest; the superseded one is hidden.
	currents := 0
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindCompanyDigest, 0) {
		if digestEntryCurrent(entry) {
			currents++
			continue
		}
		if !memoryEntryHiddenFromRecall(entry) {
			t.Fatalf("superseded company digest %s is still recall-visible", entry.ID)
		}
	}
	if currents != 1 {
		t.Fatalf("current company digests = %d, want exactly 1", currents)
	}
}

// An empty narrative must not block the state refresh: the fresh state lands,
// the previous narrative is carried, and the cursor advances.
func TestCompanyDigestEmptyNarrativeCarriesPriorAndRefreshesState(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)

	upsertLedgerTestDigest(t, app, "meeting-a", fullLedgerTestPayload())
	runLedgerPass(t, app, forbiddenLedgerResponder(t))
	runCompanyPass(t, app, func(context.Context, string, openAITextRequest) (string, error) {
		return "first narrative", nil
	})

	closedPayload := fullLedgerTestPayload()
	closedPayload.ActionItems[0].Status = "done"
	upsertLedgerTestDigest(t, app, "meeting-a", closedPayload)
	runLedgerPass(t, app, forbiddenLedgerResponder(t))

	refreshed := runCompanyPass(t, app, func(context.Context, string, openAITextRequest) (string, error) {
		return "   ", nil
	})
	payload, ok := parseCompanyDigest(refreshed.Text)
	if !ok {
		t.Fatalf("refreshed digest is not JSON: %s", refreshed.Text)
	}
	if payload.Narrative != "first narrative" {
		t.Fatalf("narrative = %q, want the carried prior on empty model output", payload.Narrative)
	}
	if len(payload.State.ActionItems) != 0 {
		t.Fatalf("state actionItems = %+v, want the closed item gone (state refreshed)", payload.State.ActionItems)
	}
	if got := refreshed.Metadata[companyDigestCursorMetadataKey]; got != newestLedgerEventID(t, app) {
		t.Fatalf("cursor = %q, want advanced past the consumed window", got)
	}
	if latest, ok := app.memory.latestCompanyDigest(); !ok || latest.ID != refreshed.ID {
		t.Fatal("latestCompanyDigest does not return the refreshed digest")
	}
}

// The stored payload round-trips through the same fence tolerance as the other
// strict-JSON kinds (a defensive parse for Wave-5 readers).
func TestParseCompanyDigestToleratesFences(t *testing.T) {
	raw := companyDigestPayload{
		GeneratedAt: "2026-07-07T00:00:00Z",
		State:       companyDigestState{Decisions: []companyDigestRecord{{ID: "ldg-1", Title: "Ship it", Status: ledgerStatusActive}}},
		Narrative:   "Shipping.",
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	fenced := "```json\n" + string(encoded) + "\n```"
	payload, ok := parseCompanyDigest(fenced)
	if !ok || payload.Narrative != "Shipping." || len(payload.State.Decisions) != 1 {
		t.Fatalf("fenced parse = %+v ok=%v", payload, ok)
	}
	if _, ok := parseCompanyDigest("not json"); ok {
		t.Fatal("parseCompanyDigest accepted garbage")
	}
}
