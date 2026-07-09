package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func runDecisionLedgerOnceForTest(t *testing.T, app *kanbanBoardApp, responder openAITextResponder) meetingMemoryEntry {
	t.Helper()
	entry, err := app.runAmbientAgentOnce(decisionLedgerAgent(), context.Background(), "test-key", responder, 1)
	if err != nil {
		t.Fatalf("runAmbientAgentOnce(decision ledger): %v", err)
	}
	return entry
}

func TestDecisionLedgerAgentContract(t *testing.T) {
	agent := decisionLedgerAgent()
	if agent.name != "decision ledger" {
		t.Fatalf("agent name=%q, want decision ledger", agent.name)
	}
	if agent.inputKind != meetingMemoryKindBrain || agent.artifactKind != meetingMemoryKindDecisionPass {
		t.Fatalf("agent kinds=%q->%q, want brain->decision_pass", agent.inputKind, agent.artifactKind)
	}
	if agent.cursorMetadataKey != "throughBrainId" {
		t.Fatalf("cursor key=%q, want throughBrainId", agent.cursorMetadataKey)
	}
	if agent.intervalEnv != "DECISION_LEDGER_INTERVAL" || agent.disabledEnv != "DECISION_LEDGER_DISABLED" || agent.backfillEnv != "DECISION_LEDGER_BACKFILL" {
		t.Fatalf("agent envs=%q/%q/%q, want DECISION_LEDGER_*", agent.intervalEnv, agent.disabledEnv, agent.backfillEnv)
	}
	if agent.defaultMinBatch != 1 || agent.defaultMaxBatch != 8 {
		t.Fatalf("batch defaults=%d/%d, want 1/8", agent.defaultMinBatch, agent.defaultMaxBatch)
	}
	if agent.produce == nil {
		t.Fatal("agent produce func must be set")
	}
}

// A pass with real decisions: statements persist as kind decision (fenced
// JSON tolerated), unknown madeBy is blanked, metadata carries the dedupe key
// and active status, and the pass entry advances the cursor.
func TestProduceDecisionLedgerAppendsDecisionsAndAdvancesCursor(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Decisions\nThe team set grill pricing at $500 per month.", nil); err != nil || !appended {
		t.Fatalf("append brain-1: appended=%v err=%v", appended, err)
	}

	fenced := "```json\n{\"decisions\":[{\"statement\":\"Grill tier is priced at $500 per month.\",\"madeBy\":\"AJ\",\"context\":\"pricing call\"},{\"statement\":\"Tyler owns the rodeo research brief.\",\"madeBy\":\"Somebody Unlisted\",\"context\":\"\"}]}\n```"
	entry := runDecisionLedgerOnceForTest(t, app, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if !strings.Contains(request.Instructions, "STRICT JSON") || !strings.Contains(request.Instructions, "FIRM decisions") {
			t.Fatalf("instructions=%q, want the strict extraction contract", request.Instructions)
		}
		if !strings.Contains(request.Input, "# Participants") || !strings.Contains(request.Input, "brain-1") {
			t.Fatalf("input missing participants or brain window: %s", request.Input)
		}
		return fenced, nil
	})

	if entry.Kind != meetingMemoryKindDecisionPass {
		t.Fatalf("pass entry kind=%q, want decision_pass", entry.Kind)
	}
	if entry.Metadata["throughBrainId"] != "brain-1" || entry.Metadata["decisionCount"] != "2" {
		t.Fatalf("pass metadata=%v, want cursor through brain-1 with 2 decisions", entry.Metadata)
	}

	decisions := app.memory.entriesOfKind(meetingMemoryKindDecision, 10)
	if len(decisions) != 2 {
		t.Fatalf("decisions=%d, want 2", len(decisions))
	}
	priced := decisions[0]
	if priced.Text != "Grill tier is priced at $500 per month." || priced.Metadata["madeBy"] != "AJ" {
		t.Fatalf("decision=%q madeBy=%q, want statement + canonical AJ", priced.Text, priced.Metadata["madeBy"])
	}
	if priced.Metadata["status"] != decisionStatusActive || priced.Metadata["dedupeKey"] == "" || priced.Metadata["sourceBrainId"] != "brain-1" {
		t.Fatalf("decision metadata=%v, want active status + dedupeKey + sourceBrainId", priced.Metadata)
	}
	if priced.Metadata["meetingId"] == "" {
		t.Fatal("decisions must inherit the automatic meetingId stamp")
	}
	if owns := decisions[1]; owns.Metadata["madeBy"] != "" {
		t.Fatalf("madeBy=%q for an unlisted name, want blanked", owns.Metadata["madeBy"])
	}

	// cursor advanced: nothing unconsumed for the ledger.
	if remaining := app.memory.unconsumedEntriesAfter(meetingMemoryKindBrain, meetingMemoryKindDecisionPass, "throughBrainId", 10, ""); len(remaining) != 0 {
		t.Fatalf("unconsumed brains=%d, want 0 after the ledger pass", len(remaining))
	}
}

// card 081: an appended decision inherits its meetingId from the source brain
// write-up it was extracted from, not from whatever meeting is current when the
// (up to 5-min-behind) ledger pass fires. This keeps the ledger row's jump
// anchor pointed at the meeting the decision was actually made in.
func TestProduceDecisionLedgerInheritsMeetingIdFromSourceBrain(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	// The brain carries an explicit meetingId (the meeting it covered).
	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Decisions\nThe team locked grill pricing at $500 per month.", map[string]string{"meetingId": "meeting-source"}); err != nil || !appended {
		t.Fatalf("append brain-1: appended=%v err=%v", appended, err)
	}
	// A distinct, later meeting is now the active one — this is what the
	// append-time stamp would use if the decision did not inherit.
	currentMeeting := app.memory.ensureMeetingID()
	if currentMeeting == "" || currentMeeting == "meeting-source" {
		t.Fatalf("current meeting id=%q, want a distinct non-empty id for the test", currentMeeting)
	}

	runDecisionLedgerOnceForTest(t, app, func(context.Context, string, openAITextRequest) (string, error) {
		return `{"decisions":[{"statement":"Grill tier is priced at $500 per month.","madeBy":"AJ","context":"pricing call"}]}`, nil
	})

	decisions := app.memory.entriesOfKind(meetingMemoryKindDecision, 10)
	if len(decisions) != 1 {
		t.Fatalf("decisions=%d, want 1", len(decisions))
	}
	if got := decisions[0].Metadata["meetingId"]; got != "meeting-source" {
		t.Fatalf("decision meetingId=%q, want inherited meeting-source (not the current meeting %q)", got, currentMeeting)
	}
}

// A zero-decision window still appends the decision_pass cursor artifact —
// otherwise unconsumedEntriesAfter re-feeds the same brains forever.
func TestProduceDecisionLedgerZeroDecisionsStillAdvancesCursor(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Overview\nPure status chatter, nothing settled.", nil); err != nil || !appended {
		t.Fatalf("append brain-1: appended=%v err=%v", appended, err)
	}

	entry := runDecisionLedgerOnceForTest(t, app, func(context.Context, string, openAITextRequest) (string, error) {
		return `{"decisions":[]}`, nil
	})
	if entry.Kind != meetingMemoryKindDecisionPass || !strings.Contains(entry.Text, "No decisions in this window") {
		t.Fatalf("pass entry=%v, want the explicit zero-decision cursor artifact", entry)
	}
	if decisions := app.memory.entriesOfKind(meetingMemoryKindDecision, 10); len(decisions) != 0 {
		t.Fatalf("decisions=%d, want 0", len(decisions))
	}
	if remaining := app.memory.unconsumedEntriesAfter(meetingMemoryKindBrain, meetingMemoryKindDecisionPass, "throughBrainId", 10, ""); len(remaining) != 0 {
		t.Fatalf("unconsumed brains=%d, want 0 — the zero pass must advance the cursor", len(remaining))
	}
}

// Unparseable output persists nothing, so the cursor stays put and the next
// pass retries the same brain window (mission-intel precedent).
func TestProduceDecisionLedgerSkipsUnparseableOutput(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Overview\nA thin window.", nil); err != nil || !appended {
		t.Fatalf("append brain-1: appended=%v err=%v", appended, err)
	}

	entry := runDecisionLedgerOnceForTest(t, app, func(context.Context, string, openAITextRequest) (string, error) {
		return "Here are the decisions in prose instead.", nil
	})
	if entry.ID != "" {
		t.Fatalf("entry=%v, want nothing persisted for non-JSON output", entry)
	}
	if passes := app.memory.entriesOfKind(meetingMemoryKindDecisionPass, 10); len(passes) != 0 {
		t.Fatalf("passes=%d, want 0", len(passes))
	}

	// the retry consumes the SAME window.
	entry = runDecisionLedgerOnceForTest(t, app, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if !strings.Contains(request.Input, "brain-1") {
			t.Fatalf("retry input missing unconsumed brain window: %s", request.Input)
		}
		return `{"decisions":[]}`, nil
	})
	if entry.Kind != meetingMemoryKindDecisionPass || entry.Metadata["throughBrainId"] != "brain-1" {
		t.Fatalf("retry entry=%v, want decision_pass through brain-1", entry)
	}
}

// Server-layer dedupe: exact-key restatements and near restatements
// (token-set Jaccard >= 0.8) are skipped; genuinely new decisions append. The
// prompt layer feeds the already-recorded exclusion list.
func TestDecisionLedgerDedupe(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	existing := "We will price the grill tier at $500 per month."
	if _, appended, err := app.memory.appendDecision("decision-existing", existing, map[string]string{
		"status":    decisionStatusActive,
		"dedupeKey": decisionDedupeKey(existing),
	}); err != nil || !appended {
		t.Fatalf("seed decision: appended=%v err=%v", appended, err)
	}
	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Decisions\nPricing restated; Tyler took the brief.", nil); err != nil || !appended {
		t.Fatalf("append brain-1: appended=%v err=%v", appended, err)
	}

	entry := runDecisionLedgerOnceForTest(t, app, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if !strings.Contains(request.Input, "Already recorded decisions (do not re-emit)") || !strings.Contains(request.Input, existing) {
			t.Fatalf("input missing the already-recorded exclusion list: %s", request.Input)
		}
		return `{"decisions":[
			{"statement":"We will price the grill tier at $500 per month.","madeBy":"AJ","context":"restated"},
			{"statement":"Price the grill tier at $500 per month.","madeBy":"AJ","context":"near restatement"},
			{"statement":"Tyler owns the rodeo research brief.","madeBy":"Tyler","context":"ownership"}
		]}`, nil
	})
	if entry.Metadata["decisionCount"] != "1" {
		t.Fatalf("decisionCount=%q, want 1 (both restatements deduped)", entry.Metadata["decisionCount"])
	}

	decisions := app.memory.entriesOfKind(meetingMemoryKindDecision, 10)
	if len(decisions) != 2 {
		t.Fatalf("decisions=%d, want the seed + one new", len(decisions))
	}
	if decisions[1].Text != "Tyler owns the rodeo research brief." {
		t.Fatalf("appended decision=%q, want the genuinely new one", decisions[1].Text)
	}
}

// Part B contract: package names ride into the extraction input, and a
// decision whose "package" field EXACTLY matches an existing venture package
// (case-insensitive, never fuzzy) is attached to the binder with the
// packageId stamped back onto the decision entry.
func TestDecisionLedgerAttachesExactPackageMatches(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	record, err := app.createVenturePackage("Nimbus creator platform", "", "AJ")
	if err != nil {
		t.Fatalf("createVenturePackage: %v", err)
	}
	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Decisions\nNimbus pricing settled; rodeo brief owned.", nil); err != nil || !appended {
		t.Fatalf("append brain-1: appended=%v err=%v", appended, err)
	}

	runDecisionLedgerOnceForTest(t, app, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if !strings.Contains(request.Input, "# Package names") || !strings.Contains(request.Input, "Nimbus creator platform") {
			t.Fatalf("input missing the package-names section: %s", request.Input)
		}
		return `{"decisions":[
			{"statement":"Nimbus launches at $99 per month.","madeBy":"AJ","context":"pricing","package":"nimbus CREATOR platform"},
			{"statement":"Tyler owns the rodeo research brief.","madeBy":"Tyler","context":"ownership","package":"Nimbus creator"}
		]}`, nil
	})

	attached, _ := app.venturePackageByID(record.ID)
	if len(attached.DecisionIDs) != 1 {
		t.Fatalf("decisionIds=%v, want exactly the exact-name match (no fuzzy attach)", attached.DecisionIDs)
	}
	decision, found := app.memory.entryByKindAndID(meetingMemoryKindDecision, attached.DecisionIDs[0])
	if !found || decision.Text != "Nimbus launches at $99 per month." {
		t.Fatalf("decision=%v, want the pricing decision attached", decision.Text)
	}
	if decision.Metadata["packageId"] != record.ID {
		t.Fatalf("decision packageId=%q, want %q stamped bidirectionally", decision.Metadata["packageId"], record.ID)
	}
}

// The load-bearing visibility asymmetry: decision statements ground Scout
// (search + query input) but never render as memory-timeline noise, and the
// decision_pass cursor is invisible everywhere.
func TestDecisionsGroundScoutButStayOffNoiseSurfaces(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, appended, err := app.memory.appendDecision("decision-1", "Zanzibar pricing is locked at $99.", map[string]string{
		"status": decisionStatusActive,
		"madeBy": "AJ",
	}); err != nil || !appended {
		t.Fatalf("append decision: appended=%v err=%v", appended, err)
	}
	if _, appended, err := app.memory.appendDecisionPass("decision-pass-1", "Extracted 1 decision(s)", map[string]string{
		"throughBrainId": "brain-1",
	}); err != nil || !appended {
		t.Fatalf("append decision pass: appended=%v err=%v", appended, err)
	}

	// search grounding: the statement is a first-class match.
	matches := app.memory.search("zanzibar pricing", 10)
	foundDecision := false
	for _, match := range matches {
		if match.Entry.Kind == meetingMemoryKindDecision {
			foundDecision = true
		}
		if match.Entry.Kind == meetingMemoryKindDecisionPass {
			t.Fatal("decision_pass leaked into Scout search")
		}
	}
	if !foundDecision {
		t.Fatal("decision statements must surface in Scout search results")
	}

	// query input grounding: the pinned Decisions-on-record section.
	input := buildAssistantQueryInput("what did we decide about zanzibar?", nil, nil, app.activeDecisionEntries(decisionContextLimit), nil, nil, time.Now(), false)
	if !strings.Contains(input, "# Decisions on record") || !strings.Contains(input, "Zanzibar pricing is locked at $99.") || !strings.Contains(input, "madeBy AJ") {
		t.Fatalf("query input missing the decisions section: %s", input)
	}

	// noise surfaces: neither kind reaches the client memory timeline.
	for _, entry := range app.memorySnapshotForClients(50) {
		if entry.Kind == meetingMemoryKindDecision || entry.Kind == meetingMemoryKindDecisionPass {
			t.Fatalf("kind %q leaked into the client memory timeline", entry.Kind)
		}
	}
	if !isUIStateMemoryKind(meetingMemoryKindDecisionPass) {
		t.Fatal("decision_pass must be a UI-state kind")
	}
	if isUIStateMemoryKind(meetingMemoryKindDecision) {
		t.Fatal("decision must NOT be a UI-state kind — it grounds Scout answers")
	}
}

// activeDecisionEntries returns newest-first active statements and skips
// non-active rows (the reserved superseded status).
func TestActiveDecisionEntriesNewestFirstActiveOnly(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, _, err := app.memory.appendDecision("decision-old", "Old active decision.", map[string]string{"status": decisionStatusActive}); err != nil {
		t.Fatalf("append old: %v", err)
	}
	if _, _, err := app.memory.appendDecision("decision-superseded", "Superseded decision.", map[string]string{"status": "superseded"}); err != nil {
		t.Fatalf("append superseded: %v", err)
	}
	if _, _, err := app.memory.appendDecision("decision-new", "New active decision.", map[string]string{"status": decisionStatusActive}); err != nil {
		t.Fatalf("append new: %v", err)
	}

	entries := app.activeDecisionEntries(5)
	if len(entries) != 2 || entries[0].ID != "decision-new" || entries[1].ID != "decision-old" {
		ids := make([]string, 0, len(entries))
		for _, entry := range entries {
			ids = append(ids, entry.ID)
		}
		t.Fatalf("active entries=%v, want newest-first active only", ids)
	}
}

// markDecisionSuperseded stamps the reserved status + supersededBy/At and the
// decision drops out of every active lane: the Scout query pinning, the
// snapshot's active ranking — while the row itself survives as history.
func TestMarkDecisionSupersededDropsFromActiveLanes(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, _, err := app.memory.appendDecision("decision-old", "Grill tier is priced at $500 per month.", map[string]string{"status": decisionStatusActive}); err != nil {
		t.Fatalf("append old: %v", err)
	}
	if _, _, err := app.memory.appendDecision("decision-new", "Grill tier is priced at $750 per month.", map[string]string{"status": decisionStatusActive}); err != nil {
		t.Fatalf("append new: %v", err)
	}

	updated, changed, err := app.markDecisionSuperseded("decision-old", "decision-new")
	if err != nil || !changed {
		t.Fatalf("markDecisionSuperseded changed=%v err=%v, want true/nil", changed, err)
	}
	if updated.Metadata["status"] != decisionStatusSuperseded || updated.Metadata["supersededBy"] != "decision-new" {
		t.Fatalf("metadata=%v, want superseded status pointing at decision-new", updated.Metadata)
	}
	if _, parseErr := time.Parse(time.RFC3339Nano, updated.Metadata["supersededAt"]); parseErr != nil {
		t.Fatalf("supersededAt=%q, want an RFC3339Nano stamp: %v", updated.Metadata["supersededAt"], parseErr)
	}

	// Active pinning lane: only the superseding decision remains.
	entries := app.activeDecisionEntries(decisionContextLimit)
	if len(entries) != 1 || entries[0].ID != "decision-new" {
		t.Fatalf("active entries=%v, want only decision-new", entries)
	}
	input := buildAssistantQueryInput("what did we decide on grill pricing?", nil, nil, app.activeDecisionEntries(decisionContextLimit), nil, nil, time.Now(), false)
	if !strings.Contains(input, "$750 per month") || strings.Contains(input, "$500 per month") {
		t.Fatalf("query input must pin the superseding decision and drop the superseded one: %s", input)
	}

	// The wire payload carries the supersession pointers; the snapshot ranks
	// the row after the active set instead of deleting it.
	payload := decisionPayload(updated)
	if payload["status"] != decisionStatusSuperseded || payload["supersededBy"] != "decision-new" || payload["supersededAt"] == "" {
		t.Fatalf("payload=%v, want status/supersededBy/supersededAt on the wire", payload)
	}
	snapshot := app.decisionLedgerSnapshot(10)
	if len(snapshot) != 2 || snapshot[0]["id"] != "decision-new" || snapshot[1]["id"] != "decision-old" || snapshot[1]["status"] != decisionStatusSuperseded {
		t.Fatalf("snapshot=%v, want the superseded row ranked after active, never dropped", snapshot)
	}
}

// Supersession is idempotent — first supersession wins, retries never rewrite
// the stamp — and the stamp survives a store reload (JSONL rewrite).
func TestMarkDecisionSupersededIdempotentAndSurvivesReload(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	for id, statement := range map[string]string{
		"decision-old":   "Old pricing decision.",
		"decision-new":   "New pricing decision.",
		"decision-other": "Unrelated ownership decision.",
	} {
		if _, _, err := app.memory.appendDecision(id, statement, map[string]string{"status": decisionStatusActive}); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}

	first, changed, err := app.markDecisionSuperseded("decision-old", "decision-new")
	if err != nil || !changed {
		t.Fatalf("first supersede changed=%v err=%v, want true/nil", changed, err)
	}
	stampedAt := first.Metadata["supersededAt"]

	// Retry with the same superseding id: no-op, stamp untouched.
	again, changed, err := app.markDecisionSuperseded("decision-old", "decision-new")
	if err != nil || changed {
		t.Fatalf("idempotent retry changed=%v err=%v, want false/nil", changed, err)
	}
	if again.Metadata["supersededAt"] != stampedAt {
		t.Fatalf("supersededAt=%q, want the original stamp %q preserved", again.Metadata["supersededAt"], stampedAt)
	}

	// A different superseding id cannot rewrite history either.
	rewrite, changed, err := app.markDecisionSuperseded("decision-old", "decision-other")
	if err != nil || changed {
		t.Fatalf("rewrite attempt changed=%v err=%v, want false/nil", changed, err)
	}
	if rewrite.Metadata["supersededBy"] != "decision-new" || rewrite.Metadata["supersededAt"] != stampedAt {
		t.Fatalf("metadata=%v, want the FIRST supersession to win", rewrite.Metadata)
	}

	// Reload from the same JSONL: the stamp and the active filter both hold.
	reloaded, err := newMeetingMemoryStore(os.Getenv("MEETING_MEMORY_PATH"))
	if err != nil {
		t.Fatalf("reload memory store: %v", err)
	}
	entry, found := reloaded.entryByKindAndID(meetingMemoryKindDecision, "decision-old")
	if !found || entry.Metadata["status"] != decisionStatusSuperseded || entry.Metadata["supersededBy"] != "decision-new" || entry.Metadata["supersededAt"] != stampedAt {
		t.Fatalf("reloaded entry=%v found=%v, want the supersession stamp to survive reload", entry.Metadata, found)
	}
}

// markDecisionSuperseded validation: both ids required and on the ledger, and
// a decision can never supersede itself.
func TestMarkDecisionSupersededValidation(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, _, err := app.memory.appendDecision("decision-1", "The only decision.", map[string]string{"status": decisionStatusActive}); err != nil {
		t.Fatalf("append decision: %v", err)
	}

	if _, _, err := app.markDecisionSuperseded("decision-1", "decision-1"); err == nil || !strings.Contains(err.Error(), "supersede itself") {
		t.Fatalf("self-supersede err=%v, want rejection", err)
	}
	if _, _, err := app.markDecisionSuperseded("", "decision-1"); err == nil {
		t.Fatal("blank decision id must error")
	}
	if _, _, err := app.markDecisionSuperseded("decision-missing", "decision-1"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing decision err=%v, want not-found", err)
	}
	if _, _, err := app.markDecisionSuperseded("decision-1", "decision-missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing superseding decision err=%v, want not-found — the chain must stay resolvable", err)
	}
	// Nothing above may have flipped the status.
	if entries := app.activeDecisionEntries(5); len(entries) != 1 || entries[0].ID != "decision-1" {
		t.Fatalf("active entries=%v, want decision-1 untouched by rejected calls", entries)
	}
}

// POST /assistant/decisions/supersede is the production invocation seam for
// markDecisionSuperseded (Wave 2 item 11) — session-gated like the other
// assistant routes, so the reserved superseded status is actually reachable:
// without this route nothing could retire a stale decision from the pinned
// "Decisions on record" lane.
func TestAssistantDecisionSupersedeHandler(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	for id, statement := range map[string]string{
		"decision-old": "Grill tier is priced at $500 per month.",
		"decision-new": "Grill tier is priced at $750 per month.",
	} {
		if _, _, err := kanbanApp.memory.appendDecision(id, statement, map[string]string{"status": decisionStatusActive}); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}
	body := `{"decisionId":"decision-old","supersededById":"decision-new"}`

	// Method gate.
	rec := httptest.NewRecorder()
	assistantDecisionSupersedeHandler(rec, httptest.NewRequest(http.MethodGet, "/assistant/decisions/supersede", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status=%d, want 405", rec.Code)
	}

	// Session gate: signed-out flips nothing.
	rec = httptest.NewRecorder()
	assistantDecisionSupersedeHandler(rec, httptest.NewRequest(http.MethodPost, "/assistant/decisions/supersede", strings.NewReader(body)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out status=%d, want 401", rec.Code)
	}
	if entries := kanbanApp.activeDecisionEntries(5); len(entries) != 2 {
		t.Fatalf("active entries=%d, want both untouched after the rejected call", len(entries))
	}

	// Signed-in: the stamp lands and the wire payload carries the chain.
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	post := func(payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/assistant/decisions/supersede", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		assistantDecisionSupersedeHandler(rec, req)
		return rec
	}
	rec = post(body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var payload struct {
		OK       bool           `json:"ok"`
		Changed  bool           `json:"changed"`
		Decision map[string]any `json:"decision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !payload.OK || !payload.Changed {
		t.Fatalf("payload=%+v, want ok+changed", payload)
	}
	if payload.Decision["status"] != decisionStatusSuperseded || payload.Decision["supersededBy"] != "decision-new" {
		t.Fatalf("decision payload=%v, want the supersession chain", payload.Decision)
	}
	if entries := kanbanApp.activeDecisionEntries(5); len(entries) != 1 || entries[0].ID != "decision-new" {
		t.Fatalf("active entries=%v, want only decision-new after the route ran", entries)
	}

	// Idempotent retry reports changed=false; unknown ids are 404; junk is 400.
	rec = post(body)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"changed":true`) {
		t.Fatalf("retry status=%d body=%s, want 200 with changed=false", rec.Code, rec.Body.String())
	}
	if rec := post(`{"decisionId":"decision-missing","supersededById":"decision-new"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("missing-id status=%d, want 404", rec.Code)
	}
	if rec := post(`{"decisionId":"decision-old","supersededById":""}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("blank superseding id status=%d, want 400", rec.Code)
	}
}

// The mission payload carries the browsable ledger: newest first, active
// before superseded, shaped as decision payloads.
func TestMissionSnapshotCarriesDecisions(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	if _, _, err := app.memory.appendDecision("decision-1", "Grill tier is priced at $500 per month.", map[string]string{
		"status":        decisionStatusActive,
		"madeBy":        "AJ",
		"sourceBrainId": "brain-9",
	}); err != nil {
		t.Fatalf("append decision: %v", err)
	}
	if _, _, err := app.memory.appendDecision("decision-2", "Superseded pick.", map[string]string{"status": "superseded"}); err != nil {
		t.Fatalf("append superseded: %v", err)
	}

	snapshot := app.missionIntelligenceSnapshot(time.Now())
	decisions, ok := snapshot["decisions"].([]map[string]any)
	if !ok {
		t.Fatalf("snapshot decisions=%T, want payload list", snapshot["decisions"])
	}
	if len(decisions) != 2 {
		t.Fatalf("decisions=%d, want 2", len(decisions))
	}
	if decisions[0]["id"] != "decision-1" || decisions[0]["statement"] != "Grill tier is priced at $500 per month." || decisions[0]["madeBy"] != "AJ" || decisions[0]["status"] != decisionStatusActive {
		t.Fatalf("payload=%v, want the active decision first", decisions[0])
	}
	if decisions[1]["status"] != "superseded" {
		t.Fatalf("payload=%v, want superseded ranked after active", decisions[1])
	}

	// the wire payload shape used by the "decision" office event.
	entry, _ := app.memory.entryByKindAndID(meetingMemoryKindDecision, "decision-1")
	payload := decisionPayload(entry)
	for _, key := range []string{"id", "statement", "madeBy", "context", "meetingId", "status", "createdAt", "sourceBrainId"} {
		if _, present := payload[key]; !present {
			t.Fatalf("decisionPayload missing %q: %v", key, payload)
		}
	}
	if payload["sourceBrainId"] != "brain-9" {
		t.Fatalf("decisionPayload sourceBrainId=%v, want brain-9 riding the wire", payload["sourceBrainId"])
	}
}

// The mission-intel input prefers ledger statements over the legacy keyword
// scan once decisions exist.
func TestMissionIntelInputPrefersLedgerDecisions(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	// keyword-scan bait: a transcript with a "decided" line, plus a brain
	// entry so the input window is non-empty.
	if _, appended, err := app.memory.appendTranscript("event-1", "item-1", "AJ: we decided to revisit the keyword scan later"); err != nil || !appended {
		t.Fatalf("append transcript: appended=%v err=%v", appended, err)
	}
	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Overview\nPlanning chatter.", nil); err != nil || !appended {
		t.Fatalf("append brain: appended=%v err=%v", appended, err)
	}

	// no ledger yet: the fallback keyword scan feeds the section.
	input := app.buildMissionIntelInput(app.memory.entriesOfKind(meetingMemoryKindBrain, 5), time.Now())
	if !strings.Contains(input, "# Recent decision signals") {
		t.Fatalf("input missing the fallback decision signals: %s", input)
	}

	if _, _, err := app.memory.appendDecision("decision-1", "Ledger decisions replace the keyword scan.", map[string]string{"status": decisionStatusActive}); err != nil {
		t.Fatalf("append decision: %v", err)
	}
	input = app.buildMissionIntelInput(app.memory.entriesOfKind(meetingMemoryKindBrain, 5), time.Now())
	if !strings.Contains(input, "Ledger decisions replace the keyword scan.") {
		t.Fatalf("input missing ledger statements: %s", input)
	}
}

// A5: the directional-alignment tier records a strategic lean as status
// proposed (visible, excluded from the active/firm lane) while a firm decision
// in the same window stays active — the strict firm discipline is not polluted.
func TestProduceDecisionLedgerDirectionalTier(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	if _, appended, err := app.memory.appendBrainWriteUp("brain-dir", "## Topics\nBall Dogs vs alternatives; grill pricing.", nil); err != nil || !appended {
		t.Fatalf("append brain: appended=%v err=%v", appended, err)
	}

	entry := runDecisionLedgerOnceForTest(t, app, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if !strings.Contains(request.Instructions, "DIRECTIONAL") || !strings.Contains(request.Instructions, "leaning toward Ball Dogs") {
			t.Fatalf("instructions missing the directional contract + example: %q", request.Instructions)
		}
		if request.ReasoningEffort != "medium" {
			t.Fatalf("reasoning effort=%q, want medium (doctrine floor)", request.ReasoningEffort)
		}
		return `{"decisions":[{"statement":"Grill tier is priced at $500 per month.","madeBy":"AJ","context":"pricing call","directional":false},{"statement":"The team is leaning toward Ball Dogs as the lead IP.","madeBy":"","context":"consensus forming","directional":true}]}`, nil
	})
	if entry.Metadata["decisionCount"] != "2" || entry.Metadata["directionalCount"] != "1" {
		t.Fatalf("pass metadata=%v, want 2 decisions incl. 1 directional", entry.Metadata)
	}

	active := app.activeDecisionEntries(10)
	if len(active) != 1 || !strings.Contains(active[0].Text, "Grill tier") {
		t.Fatalf("active decisions=%v, want only the firm grill-pricing decision", active)
	}
	proposed := app.proposedDecisionEntries(10)
	if len(proposed) != 1 || !strings.Contains(proposed[0].Text, "Ball Dogs") {
		t.Fatalf("proposed decisions=%v, want the directional Ball Dogs lean", proposed)
	}
	if proposed[0].Metadata["status"] != decisionStatusProposed {
		t.Fatalf("directional status=%q, want proposed", proposed[0].Metadata["status"])
	}
}

// A5: a directional lean does not re-fire every pass (deduped against the
// proposed lane), yet the same statement CAN later harden into a firm decision
// — a firm candidate dedupes only against the active lane, so the upgrade is not
// blocked by its own earlier proposed row.
func TestDirectionalDedupesButCanUpgradeToFirm(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	directional := `{"decisions":[{"statement":"The team is leaning toward Ball Dogs as the lead IP.","madeBy":"","context":"forming","directional":true}]}`

	if _, appended, err := app.memory.appendBrainWriteUp("brain-a", "## Topics\nBall Dogs lean.", nil); err != nil || !appended {
		t.Fatalf("append brain-a: %v", err)
	}
	runDecisionLedgerOnceForTest(t, app, func(context.Context, string, openAITextRequest) (string, error) {
		return directional, nil
	})
	if got := app.proposedDecisionEntries(10); len(got) != 1 {
		t.Fatalf("after first pass proposed=%d, want 1", len(got))
	}

	// Second pass re-emits the SAME directional: deduped, no new proposed row.
	if _, appended, err := app.memory.appendBrainWriteUp("brain-b", "## Topics\nStill leaning Ball Dogs.", nil); err != nil || !appended {
		t.Fatalf("append brain-b: %v", err)
	}
	runDecisionLedgerOnceForTest(t, app, func(context.Context, string, openAITextRequest) (string, error) {
		return directional, nil
	})
	if got := app.proposedDecisionEntries(10); len(got) != 1 {
		t.Fatalf("after re-emit proposed=%d, want the directional lean deduped to 1", len(got))
	}

	// Third pass: the lean hardens into a FIRM decision. It must land active
	// despite the existing proposed row of the same statement.
	if _, appended, err := app.memory.appendBrainWriteUp("brain-c", "## Decisions\nBall Dogs is the lead IP.", nil); err != nil || !appended {
		t.Fatalf("append brain-c: %v", err)
	}
	runDecisionLedgerOnceForTest(t, app, func(context.Context, string, openAITextRequest) (string, error) {
		return `{"decisions":[{"statement":"The team is leaning toward Ball Dogs as the lead IP.","madeBy":"AJ","context":"finalized","directional":false}]}`, nil
	})
	active := app.activeDecisionEntries(10)
	if len(active) != 1 || !strings.Contains(active[0].Text, "Ball Dogs") {
		t.Fatalf("active=%v, want the directional lean upgraded to a firm decision", active)
	}
}
