package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ledgerSliceContains reports slice membership for the provenance-spill tests.
func ledgerSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// appendProposedLean records a directional lean (status=proposed) with a holder
// the way the decision extraction does — the fold source for a position record.
func appendProposedLean(t *testing.T, app *kanbanBoardApp, id string, statement string, holder string, meetingID string) {
	t.Helper()
	if _, appended, err := app.memory.appendDecision(id, statement, map[string]string{
		"status": decisionStatusProposed, "madeBy": holder, "meetingId": meetingID,
	}); err != nil || !appended {
		t.Fatalf("append proposed lean %s: appended=%v err=%v", id, appended, err)
	}
}

/* ---------- helpers ---------- */

// upsertLedgerTestDigest lands a current meeting_digest for meetingID the way
// the Wave-2 producer does (digestKey == meetingId, span stamps present).
func upsertLedgerTestDigest(t *testing.T, app *kanbanBoardApp, meetingID string, payload meetingDigestPayload) meetingMemoryEntry {
	t.Helper()
	payload.MeetingID = meetingID
	if payload.Day == "" {
		payload.Day = "2026-07-06"
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal digest payload: %v", err)
	}
	entry, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, meetingID, string(raw), map[string]string{
		"meetingId":                meetingID,
		digestDayMetadataKey:       payload.Day,
		digestSpanStartMetadataKey: "2026-07-06T10:00:00Z",
		digestSpanEndMetadataKey:   "2026-07-06T18:00:00Z",
	})
	if err != nil {
		t.Fatalf("upsert digest for %s: %v", meetingID, err)
	}

	return entry
}

// forbiddenLedgerResponder fails the test on ANY model call — the proof that
// a deterministic pass never spends adjudication budget (amendment A8).
func forbiddenLedgerResponder(t *testing.T) openAITextResponder {
	return func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("unexpected model call: deterministic consolidation must not adjudicate")
		return "", nil
	}
}

func runLedgerPass(t *testing.T, app *kanbanBoardApp, responder openAITextResponder) meetingMemoryEntry {
	t.Helper()
	entry, err := app.runAmbientAgentOnce(entityLedgerAgent(), context.Background(), "test-key", responder, 1)
	if err != nil {
		t.Fatalf("entity ledger pass: %v", err)
	}

	return entry
}

func ledgerRecordsOfEntity(state map[string]ledgerRecord, entity string) []ledgerRecord {
	records := make([]ledgerRecord, 0, len(state))
	for _, record := range state {
		if record.Entity == entity {
			records = append(records, record)
		}
	}

	return records
}

func fullLedgerTestPayload() meetingDigestPayload {
	return meetingDigestPayload{
		Decisions: []meetingDigestDecision{{
			D:          "Choose vendor Zebra for the packaging pilot",
			By:         "attributed to AJ",
			Status:     "decided",
			Anchor:     "tx-1",
			At:         "2026-07-06T10:06:00Z",
			Importance: 5,
		}},
		ActionItems: []meetingDigestAction{{
			A:          "Draft the pricing sheet",
			Owner:      "Tyler",
			Status:     "open",
			Anchor:     "tx-2",
			At:         "2026-07-06T10:07:00Z",
			Importance: 4,
		}},
		Topics: []meetingDigestTopic{{
			T:          "Packaging pilot logistics",
			Anchor:     "tx-1",
			At:         "2026-07-06T10:05:00Z",
			Importance: 3,
		}},
		OpenQuestions: []meetingDigestQuestion{{
			Q:          "Which SKU ships first?",
			Anchor:     "tx-3",
			At:         "2026-07-06T10:08:00Z",
			Importance: 2,
		}},
	}
}

/* ---------- consolidation: add / idempotence / update / close ---------- */

func TestEntityLedgerConsolidatesDigestFactsIntoRecords(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	digest := upsertLedgerTestDigest(t, app, "meeting-a", fullLedgerTestPayload())

	pass := runLedgerPass(t, app, forbiddenLedgerResponder(t))
	if pass.Kind != meetingMemoryKindLedgerPass {
		t.Fatalf("pass kind = %s, want %s", pass.Kind, meetingMemoryKindLedgerPass)
	}
	if got := pass.Metadata[entityLedgerCursorMetadataKey]; got != digest.ID {
		t.Fatalf("cursor = %q, want %q", got, digest.ID)
	}
	if got := pass.Metadata["eventCount"]; got != "4" {
		t.Fatalf("eventCount = %q, want 4", got)
	}
	if meetingID := strings.TrimSpace(pass.Metadata["meetingId"]); meetingID != "" {
		t.Fatalf("ledger_pass carries meetingId %q, want mint-free append", meetingID)
	}

	state := app.memory.ledgerState()
	if len(state) != 4 {
		t.Fatalf("ledger state size = %d, want 4: %+v", len(state), state)
	}

	decisions := ledgerRecordsOfEntity(state, ledgerEntityDecision)
	if len(decisions) != 1 {
		t.Fatalf("decisions = %+v, want exactly one", decisions)
	}
	decision := decisions[0]
	if decision.Title != "Choose vendor Zebra for the packaging pilot" {
		t.Fatalf("decision title = %q", decision.Title)
	}
	if decision.Status != ledgerStatusActive {
		t.Fatalf("decision status = %q, want %q (free-text 'decided' normalized)", decision.Status, ledgerStatusActive)
	}
	if decision.Owner != "AJ" {
		t.Fatalf("decision owner = %q, want the hedge prefix unwrapped to AJ", decision.Owner)
	}
	if decision.ValidFrom != "2026-07-06T10:06:00Z" {
		t.Fatalf("decision validFrom = %q, want the fact's own at-stamp", decision.ValidFrom)
	}
	if !decision.current() || decision.SupersededBy != "" {
		t.Fatalf("decision must open a current validity window: %+v", decision)
	}
	if !reflect.DeepEqual(decision.Anchors, []string{"tx-1"}) {
		t.Fatalf("decision anchors = %+v, want [tx-1]", decision.Anchors)
	}
	if !reflect.DeepEqual(decision.MeetingIDs, []string{"meeting-a"}) {
		t.Fatalf("decision meetingIds = %+v, want [meeting-a]", decision.MeetingIDs)
	}
	if decision.Importance != 5 {
		t.Fatalf("decision importance = %d, want 5", decision.Importance)
	}
	if decision.ID == "" || !strings.HasPrefix(decision.ID, "ldg-decision-") {
		t.Fatalf("decision id = %q, want a stable ldg-decision-* id", decision.ID)
	}

	actions := ledgerRecordsOfEntity(state, ledgerEntityActionItem)
	if len(actions) != 1 || actions[0].Status != ledgerStatusOpen || actions[0].Owner != "Tyler" {
		t.Fatalf("action items = %+v, want one open item owned by Tyler", actions)
	}
	if topics := ledgerRecordsOfEntity(state, ledgerEntityTopic); len(topics) != 1 || topics[0].Status != ledgerStatusActive {
		t.Fatalf("topics = %+v, want one active topic", topics)
	}
	if questions := ledgerRecordsOfEntity(state, ledgerEntityOpenQuestion); len(questions) != 1 || questions[0].Status != ledgerStatusOpen {
		t.Fatalf("open questions = %+v, want one open question", questions)
	}

	for _, event := range app.memory.entriesOfKind(meetingMemoryKindLedgerEvent, 0) {
		if event.Metadata["op"] != ledgerOpAdd {
			t.Fatalf("first pass op = %q, want %q", event.Metadata["op"], ledgerOpAdd)
		}
		if meetingID := strings.TrimSpace(event.Metadata["meetingId"]); meetingID != "" {
			t.Fatalf("ledger_event carries meetingId %q, want mint-free append", meetingID)
		}
	}
}

func TestEntityLedgerPassIsIdempotentAcrossDigestRebuilds(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	upsertLedgerTestDigest(t, app, "meeting-a", fullLedgerTestPayload())
	runLedgerPass(t, app, forbiddenLedgerResponder(t))
	before := app.memory.ledgerState()

	// the cumulative digest is rebuilt with identical facts (a new entry id
	// supersedes the old one) — re-consolidation must be a no-op.
	rebuilt := upsertLedgerTestDigest(t, app, "meeting-a", fullLedgerTestPayload())
	pass := runLedgerPass(t, app, forbiddenLedgerResponder(t))
	if got := pass.Metadata["eventCount"]; got != "0" {
		t.Fatalf("second pass eventCount = %q, want 0", got)
	}
	if got := pass.Metadata[entityLedgerCursorMetadataKey]; got != rebuilt.ID {
		t.Fatalf("second pass cursor = %q, want %q (zero-event pass still advances)", got, rebuilt.ID)
	}
	if events := app.memory.entriesOfKind(meetingMemoryKindLedgerEvent, 0); len(events) != 4 {
		t.Fatalf("ledger events = %d, want the original 4 only", len(events))
	}
	if after := app.memory.ledgerState(); !reflect.DeepEqual(before, after) {
		t.Fatalf("state changed on an idempotent rerun:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestEntityLedgerUpdatesAndClosesOnStatusChange(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	upsertLedgerTestDigest(t, app, "meeting-a", fullLedgerTestPayload())
	runLedgerPass(t, app, forbiddenLedgerResponder(t))

	evolved := fullLedgerTestPayload()
	evolved.ActionItems[0].Status = "done" // completion → CLOSE
	evolved.Decisions[0].By = "attributed to Tyler"
	evolved.Decisions[0].Anchor = "tx-9" // new provenance → UPDATE
	upsertLedgerTestDigest(t, app, "meeting-a", evolved)
	pass := runLedgerPass(t, app, forbiddenLedgerResponder(t))
	if got := pass.Metadata["eventCount"]; got != "2" {
		t.Fatalf("eventCount = %q, want 2 (one update + one close)", got)
	}

	state := app.memory.ledgerState()
	if len(state) != 4 {
		t.Fatalf("state size = %d, want 4 — close must NEVER delete a record", len(state))
	}
	actions := ledgerRecordsOfEntity(state, ledgerEntityActionItem)
	if len(actions) != 1 {
		t.Fatalf("actions = %+v, want the one closed record", actions)
	}
	if actions[0].Status != ledgerStatusDone || actions[0].current() {
		t.Fatalf("action = %+v, want status done with a closed validity window", actions[0])
	}
	decisions := ledgerRecordsOfEntity(state, ledgerEntityDecision)
	if len(decisions) != 1 {
		t.Fatalf("decisions = %+v, want the single merged record", decisions)
	}
	if decisions[0].Owner != "Tyler" {
		t.Fatalf("decision owner = %q, want newest-wins Tyler", decisions[0].Owner)
	}
	if !reflect.DeepEqual(decisions[0].Anchors, []string{"tx-1", "tx-9"}) {
		t.Fatalf("decision anchors = %+v, want the union [tx-1 tx-9]", decisions[0].Anchors)
	}

	ops := map[string]int{}
	for _, event := range app.memory.entriesOfKind(meetingMemoryKindLedgerEvent, 0) {
		ops[event.Metadata["op"]]++
	}
	if ops[ledgerOpUpdate] != 1 || ops[ledgerOpClose] != 1 || ops[ledgerOpAdd] != 4 {
		t.Fatalf("ops = %+v, want 4 add / 1 update / 1 close", ops)
	}
}

/* ---------- adjudication: the ambiguous band only ---------- */

func TestEntityLedgerAdjudicationSupersedesInOneBatchedCall(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	seed := meetingDigestPayload{Decisions: []meetingDigestDecision{{
		D: "Ship the pilot with vendor Zebra packaging", By: "AJ", Anchor: "tx-1", Importance: 4,
	}}}
	upsertLedgerTestDigest(t, app, "meeting-a", seed)
	runLedgerPass(t, app, forbiddenLedgerResponder(t))

	calls := 0
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		if !strings.Contains(request.Input, "Ship the pilot with vendor Zebra packaging") ||
			!strings.Contains(request.Input, "Use vendor Kappa for the packaging pilot instead of Zebra") {
			t.Errorf("adjudication input missing the pair: %s", request.Input)
		}
		return `{"verdicts":[{"i":0,"verdict":"supersedes"}]}`, nil
	}
	contradiction := meetingDigestPayload{Decisions: []meetingDigestDecision{{
		D: "Use vendor Kappa for the packaging pilot instead of Zebra", By: "AJ", Anchor: "tx-7", Importance: 5,
	}}}
	upsertLedgerTestDigest(t, app, "meeting-b", contradiction)
	runLedgerPass(t, app, responder)
	if calls != 1 {
		t.Fatalf("adjudication calls = %d, want exactly one batched call", calls)
	}

	state := app.memory.ledgerState()
	decisions := ledgerRecordsOfEntity(state, ledgerEntityDecision)
	if len(decisions) != 2 {
		t.Fatalf("decisions = %+v, want the closed old window plus the new record", decisions)
	}
	var old, fresh ledgerRecord
	for _, record := range decisions {
		if record.current() {
			fresh = record
		} else {
			old = record
		}
	}
	if fresh.Title != "Use vendor Kappa for the packaging pilot instead of Zebra" {
		t.Fatalf("current record = %+v, want the superseding fact", fresh)
	}
	if old.Status != ledgerStatusSuperseded || old.ValidTo == "" {
		t.Fatalf("old record = %+v, want status superseded with a closed validity window", old)
	}
	if old.SupersededBy != fresh.ID {
		t.Fatalf("old.supersededBy = %q, want %q", old.SupersededBy, fresh.ID)
	}
	if !reflect.DeepEqual(fresh.MeetingIDs, []string{"meeting-b"}) {
		t.Fatalf("new record meetingIds = %+v, want [meeting-b]", fresh.MeetingIDs)
	}
}

func TestEntityLedgerAdjudicationFailureFallsBackToAdd(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	upsertLedgerTestDigest(t, app, "meeting-a", meetingDigestPayload{Decisions: []meetingDigestDecision{{
		D: "Ship the pilot with vendor Zebra packaging", Importance: 4,
	}}})
	runLedgerPass(t, app, forbiddenLedgerResponder(t))

	responder := func(context.Context, string, openAITextRequest) (string, error) {
		return "", errors.New("model unavailable")
	}
	upsertLedgerTestDigest(t, app, "meeting-b", meetingDigestPayload{Decisions: []meetingDigestDecision{{
		D: "Use vendor Kappa for the packaging pilot instead of Zebra", Importance: 4,
	}}})
	pass := runLedgerPass(t, app, responder)
	if got := pass.Metadata["eventCount"]; got != "1" {
		t.Fatalf("eventCount = %q, want 1 (ambiguous fact degraded to add)", got)
	}

	decisions := ledgerRecordsOfEntity(app.memory.ledgerState(), ledgerEntityDecision)
	if len(decisions) != 2 {
		t.Fatalf("decisions = %+v, want two records (no merge, no loss)", decisions)
	}
	for _, record := range decisions {
		if !record.current() {
			t.Fatalf("record %+v closed by a failed adjudication — must stay current", record)
		}
	}
}

/* ---------- amendment A9: the decision log is a fold source ---------- */

func TestEntityLedgerConsumesDecisionEntries(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	if _, appended, err := app.memory.appendDecision("dec-1", "We will sunset the legacy billing exporter next sprint", map[string]string{
		"status": decisionStatusActive, "madeBy": "AJ", "meetingId": "meeting-a",
	}); err != nil || !appended {
		t.Fatalf("append decision: appended=%v err=%v", appended, err)
	}
	// a proposed default (card 069) is not a team decision yet — never folded.
	if _, appended, err := app.memory.appendDecision("dec-2", "Placeholder governance default awaiting ratification", map[string]string{
		"status": decisionStatusProposed, "madeBy": "Scout", "meetingId": "meeting-a",
	}); err != nil || !appended {
		t.Fatalf("append proposed decision: appended=%v err=%v", appended, err)
	}
	// the digest tick is the trigger; this one carries no facts of its own.
	upsertLedgerTestDigest(t, app, "meeting-a", meetingDigestPayload{})

	pass := runLedgerPass(t, app, forbiddenLedgerResponder(t))
	if got := pass.Metadata[entityLedgerDecisionCursorMetadataKey]; got != "dec-2" {
		t.Fatalf("throughDecisionId = %q, want dec-2", got)
	}
	state := app.memory.ledgerState()
	decisions := ledgerRecordsOfEntity(state, ledgerEntityDecision)
	if len(decisions) != 1 {
		t.Fatalf("decisions = %+v, want exactly the active row (proposed skipped)", decisions)
	}
	record := decisions[0]
	if record.Title != "We will sunset the legacy billing exporter next sprint" {
		t.Fatalf("title = %q", record.Title)
	}
	if !reflect.DeepEqual(record.Anchors, []string{"dec-1"}) {
		t.Fatalf("anchors = %+v, want the decision entry id [dec-1] (A9 provenance)", record.Anchors)
	}
	if record.Owner != "AJ" || record.Importance != 4 || record.Status != ledgerStatusActive {
		t.Fatalf("record = %+v, want owner AJ / importance 4 / active", record)
	}
	if !reflect.DeepEqual(record.MeetingIDs, []string{"meeting-a"}) {
		t.Fatalf("meetingIds = %+v, want [meeting-a]", record.MeetingIDs)
	}

	// next tick: no new decisions — the cursor is carried forward and the row
	// is not re-consumed.
	upsertLedgerTestDigest(t, app, "meeting-a", meetingDigestPayload{})
	second := runLedgerPass(t, app, forbiddenLedgerResponder(t))
	if got := second.Metadata[entityLedgerDecisionCursorMetadataKey]; got != "dec-2" {
		t.Fatalf("carried-forward throughDecisionId = %q, want dec-2", got)
	}
	if got := second.Metadata["eventCount"]; got != "0" {
		t.Fatalf("second pass eventCount = %q, want 0", got)
	}
}

/* ---------- event sourcing: rebuildable, bookkeeping-only ---------- */

func TestLedgerStateRebuildsFromLogByFolding(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	upsertLedgerTestDigest(t, app, "meeting-a", fullLedgerTestPayload())
	runLedgerPass(t, app, forbiddenLedgerResponder(t))
	evolved := fullLedgerTestPayload()
	evolved.ActionItems[0].Status = "done"
	upsertLedgerTestDigest(t, app, "meeting-a", evolved)
	runLedgerPass(t, app, forbiddenLedgerResponder(t))
	before := app.memory.ledgerState()
	if len(before) != 4 {
		t.Fatalf("state size = %d, want 4", len(before))
	}

	reloaded, err := newMeetingMemoryStore(meetingMemoryPath())
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	if after := reloaded.ledgerState(); !reflect.DeepEqual(before, after) {
		t.Fatalf("fold-from-scratch diverged:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestLedgerEventsAreBookkeepingNotRecall(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	appendTestTranscript(t, app, "tx-1", "Quokka rollout budget planning session.")
	meetingID := app.memory.currentMeetingID(officeRoomID)
	if meetingID == "" {
		t.Fatal("expected a minted meeting id")
	}
	upsertLedgerTestDigest(t, app, "meeting-a", meetingDigestPayload{Decisions: []meetingDigestDecision{{
		D: "Quokka rollout budget approved", Anchor: "tx-1", Importance: 5,
	}}})
	runLedgerPass(t, app, forbiddenLedgerResponder(t))

	// search: ledger events/passes never surface as recall material.
	for _, match := range app.memory.search("quokka rollout budget", 10) {
		if match.Entry.Kind == meetingMemoryKindLedgerEvent || match.Entry.Kind == meetingMemoryKindLedgerPass {
			t.Fatalf("ledger bookkeeping leaked into search: %+v", match.Entry)
		}
	}
	// client timeline: excluded by kind.
	events := app.memory.entriesOfKind(meetingMemoryKindLedgerEvent, 0)
	passes := app.memory.entriesOfKind(meetingMemoryKindLedgerPass, 0)
	if len(events) == 0 || len(passes) == 0 {
		t.Fatalf("expected ledger events and a pass artifact (events=%d passes=%d)", len(events), len(passes))
	}
	for _, entry := range visibleMeetingMemoryEntries(append(append([]meetingMemoryEntry{}, events...), passes...), 0) {
		t.Fatalf("ledger bookkeeping leaked into the client timeline: %+v", entry)
	}

	// boot resume: mint-free ledger lines as the newest entries must not
	// clear the in-flight meeting id across a restart.
	reloaded, err := newMeetingMemoryStore(meetingMemoryPath())
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	if got := reloaded.currentMeetingID(officeRoomID); got != meetingID {
		t.Fatalf("resumed meeting id = %q, want %q (ledger lines must be skipped)", got, meetingID)
	}
}

/* ---------- read surfaces: state view + ledger-first lookup ---------- */

func TestLedgerCurrentStateViewAndSearch(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	upsertLedgerTestDigest(t, app, "meeting-a", fullLedgerTestPayload())
	runLedgerPass(t, app, forbiddenLedgerResponder(t))
	evolved := fullLedgerTestPayload()
	evolved.ActionItems[0].Status = "done"
	upsertLedgerTestDigest(t, app, "meeting-a", evolved)
	runLedgerPass(t, app, forbiddenLedgerResponder(t))

	view := app.ledgerCurrentStateView(10)
	if len(view.Decisions) != 1 || len(view.Topics) != 1 || len(view.OpenQuestions) != 1 {
		t.Fatalf("state view = %+v, want one current record per open section", view)
	}
	if len(view.ActionItems) != 0 {
		t.Fatalf("state view actions = %+v, want the closed item excluded from the CURRENT view", view.ActionItems)
	}

	// A5 ledger-first lookup: "status of X" resolves by title overlap, and the
	// closed record is still findable (history, never deleted).
	found := app.searchLedgerRecords("status of the pricing sheet", 5)
	if len(found) != 1 || found[0].Entity != ledgerEntityActionItem || found[0].Status != ledgerStatusDone {
		t.Fatalf("searchLedgerRecords = %+v, want the closed pricing-sheet action", found)
	}
	if len(app.searchLedgerRecords("completely unrelated moonbase query", 5)) != 0 {
		t.Fatalf("unrelated query must match nothing")
	}
}

/* ---------- unit coverage for the merge primitives ---------- */

func TestLedgerStatusAndOwnerNormalization(t *testing.T) {
	if got := normalizeLedgerStatus(ledgerEntityActionItem, "Completed"); got != ledgerStatusDone {
		t.Fatalf("completed → %q, want %q", got, ledgerStatusDone)
	}
	if got := normalizeLedgerStatus(ledgerEntityDecision, "reversed"); got != ledgerStatusSuperseded {
		t.Fatalf("reversed → %q, want %q", got, ledgerStatusSuperseded)
	}
	if got := normalizeLedgerStatus(ledgerEntityDecision, "some novel phrasing"); got != ledgerStatusActive {
		t.Fatalf("unrecognized decision status → %q, want default %q", got, ledgerStatusActive)
	}
	if got := normalizeLedgerStatus(ledgerEntityOpenQuestion, ""); got != ledgerStatusOpen {
		t.Fatalf("empty question status → %q, want default %q", got, ledgerStatusOpen)
	}
	if got := normalizeLedgerOwner("Attributed to  Caitlyn"); got != "Caitlyn" {
		t.Fatalf("owner hedge unwrap = %q, want Caitlyn", got)
	}
	if !isTerminalLedgerStatus(ledgerStatusAnswered) || isTerminalLedgerStatus(ledgerStatusInProgress) {
		t.Fatal("terminal classification wrong for answered/in_progress")
	}
}

/* ---------- item 2.2: keyed position records ---------- */

func TestEntityLedgerMintsPositionFromDirectionalLean(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	// a directional lean WITH a holder → a keyed position record.
	appendProposedLean(t, app, "dec-lean-tim", "Tim favors holding the current pricing rather than discounting.", "Tim", "meeting-a")
	// a floating lean with NO holder is not a personal position → skipped.
	appendProposedLean(t, app, "dec-lean-team", "The team is leaning toward Ball Dogs as the lead IP.", "", "meeting-a")
	upsertLedgerTestDigest(t, app, "meeting-a", meetingDigestPayload{})

	runLedgerPass(t, app, forbiddenLedgerResponder(t))

	positions := ledgerRecordsOfEntity(app.memory.ledgerState(), ledgerEntityPosition)
	if len(positions) != 1 {
		t.Fatalf("positions = %+v, want exactly one (the held lean; the ownerless lean is skipped)", positions)
	}
	position := positions[0]
	if position.Owner != "Tim" {
		t.Fatalf("position owner = %q, want Tim", position.Owner)
	}
	if position.Status != ledgerStatusActive || !position.current() {
		t.Fatalf("position = %+v, want an active current stance", position)
	}
	if !reflect.DeepEqual(position.Anchors, []string{"dec-lean-tim"}) {
		t.Fatalf("position anchors = %+v, want [dec-lean-tim]", position.Anchors)
	}
	// a lean is a position, never ALSO a firm decision record.
	if decisions := ledgerRecordsOfEntity(app.memory.ledgerState(), ledgerEntityDecision); len(decisions) != 0 {
		t.Fatalf("decisions = %+v, want none (a lean routes to a position)", decisions)
	}
}

func TestEntityLedgerPositionsAreOwnerScopedAndSupersede(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	appendProposedLean(t, app, "dec-tim-1", "Tim favors holding the current pricing.", "Tim", "meeting-a")
	appendProposedLean(t, app, "dec-aj-1", "AJ favors cutting the current pricing to win share.", "AJ", "meeting-a")
	upsertLedgerTestDigest(t, app, "meeting-a", meetingDigestPayload{})
	runLedgerPass(t, app, forbiddenLedgerResponder(t))
	if positions := ledgerRecordsOfEntity(app.memory.ledgerState(), ledgerEntityPosition); len(positions) != 2 {
		t.Fatalf("positions = %+v, want two owner-scoped stances (never merged across owners)", positions)
	}

	// Tim changes his mind on the SAME topic: owner-scoped ambiguous match → one
	// batched adjudication → supersedes. AJ's stance is never a candidate.
	appendProposedLean(t, app, "dec-tim-2", "Tim favors cutting the current pricing.", "Tim", "meeting-b")
	upsertLedgerTestDigest(t, app, "meeting-b", meetingDigestPayload{})
	calls := 0
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		if strings.Contains(request.Input, "AJ favors cutting") {
			t.Errorf("adjudication compared across owners; AJ's stance must never be a candidate:\n%s", request.Input)
		}
		return `{"verdicts":[{"i":0,"verdict":"supersedes"}]}`, nil
	}
	runLedgerPass(t, app, responder)
	if calls != 1 {
		t.Fatalf("adjudication calls = %d, want exactly one batched call", calls)
	}

	positions := ledgerRecordsOfEntity(app.memory.ledgerState(), ledgerEntityPosition)
	timCurrent, timClosed, ajCount := 0, 0, 0
	for _, position := range positions {
		switch position.Owner {
		case "Tim":
			if position.current() {
				timCurrent++
			} else {
				timClosed++
			}
		case "AJ":
			ajCount++
		}
	}
	if timCurrent != 1 || timClosed != 1 {
		t.Fatalf("Tim positions current=%d closed=%d, want one current + one superseded", timCurrent, timClosed)
	}
	if ajCount != 1 {
		t.Fatalf("AJ positions = %d, want the single untouched stance", ajCount)
	}
}

/* ---------- item 1.3a: alias union prevents a rename fork ---------- */

func TestEntityLedgerAliasUnionPreventsRenameFork(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	seed := meetingDigestPayload{
		Topics:  []meetingDigestTopic{{T: "Samsung TV Plus partnership", Anchor: "tx-1", Importance: 4}},
		Aliases: []string{"the Korean TV deal", "CTV partnership"},
	}
	upsertLedgerTestDigest(t, app, "meeting-a", seed)
	runLedgerPass(t, app, forbiddenLedgerResponder(t))
	topics := ledgerRecordsOfEntity(app.memory.ledgerState(), ledgerEntityTopic)
	if len(topics) != 1 || len(topics[0].Aliases) == 0 {
		t.Fatalf("seed topic = %+v, want one record carrying its folded aliases", topics)
	}

	// a later meeting renames the topic to one of its aliases. The raw titles
	// share nothing, so ONLY the folded alias set can bridge the match into the
	// ambiguous band → one adjudication → "same" → merge (no second record).
	renamed := meetingDigestPayload{
		Topics: []meetingDigestTopic{{T: "the Korean TV deal", Anchor: "tx-9", Importance: 4}},
	}
	upsertLedgerTestDigest(t, app, "meeting-b", renamed)
	calls := 0
	responder := func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		calls++
		return `{"verdicts":[{"i":0,"verdict":"same"}]}`, nil
	}
	runLedgerPass(t, app, responder)
	if calls != 1 {
		t.Fatalf("adjudication calls = %d, want one alias-bridged pair", calls)
	}
	topics = ledgerRecordsOfEntity(app.memory.ledgerState(), ledgerEntityTopic)
	if len(topics) != 1 {
		t.Fatalf("topics = %+v, want the rename merged into ONE record (no fork)", topics)
	}
	if !ledgerSliceContains(topics[0].MeetingIDs, "meeting-a") || !ledgerSliceContains(topics[0].MeetingIDs, "meeting-b") {
		t.Fatalf("merged meetingIds = %+v, want both source meetings", topics[0].MeetingIDs)
	}
}

/* ---------- item Q6: provenance-cap overflow spill ---------- */

func TestLedgerProvenanceOverflowSpill(t *testing.T) {
	ids := make([]string, 0, ledgerMeetingIDCap)
	for index := 0; index < ledgerMeetingIDCap; index++ {
		ids = append(ids, fmt.Sprintf("m-%02d", index))
	}
	anchors := make([]string, 0, ledgerAnchorCap)
	for index := 0; index < ledgerAnchorCap; index++ {
		anchors = append(anchors, fmt.Sprintf("a-%02d", index))
	}
	record := ledgerRecord{Entity: ledgerEntityTopic, Title: "Recurring topic", Status: ledgerStatusActive, MeetingIDs: ids, Anchors: anchors}
	fact := ledgerFact{Entity: ledgerEntityTopic, Title: "Recurring topic", MeetingID: "m-new", Anchor: "a-new"}

	merged, changed := mergeLedgerFact(record, fact, "2026-07-06T10:00:00Z")
	if !changed {
		t.Fatal("merge should register the new provenance")
	}
	if len(merged.MeetingIDs) != ledgerMeetingIDCap {
		t.Fatalf("meetingIds len = %d, want the cap %d", len(merged.MeetingIDs), ledgerMeetingIDCap)
	}
	if merged.MeetingIDs[len(merged.MeetingIDs)-1] != "m-new" {
		t.Fatalf("newest meetingId = %q, want m-new at the tail", merged.MeetingIDs[len(merged.MeetingIDs)-1])
	}
	if !ledgerSliceContains(merged.MeetingIDsOverflow, "m-00") {
		t.Fatalf("meetingIds overflow = %+v, want the evicted m-00 preserved (spill-never-shed)", merged.MeetingIDsOverflow)
	}
	if !ledgerSliceContains(merged.AnchorsOverflow, "a-00") {
		t.Fatalf("anchor overflow = %+v, want the evicted a-00 preserved", merged.AnchorsOverflow)
	}
}

/* ---------- item 2.3b: evolution replay from the event log ---------- */

func TestLedgerRecordEvolutionReplaysTransitions(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	upsertLedgerTestDigest(t, app, "meeting-a", meetingDigestPayload{Decisions: []meetingDigestDecision{{
		D: "Ship the pilot with vendor Zebra packaging", By: "AJ", Anchor: "tx-1", Importance: 4,
	}}})
	runLedgerPass(t, app, forbiddenLedgerResponder(t))
	upsertLedgerTestDigest(t, app, "meeting-b", meetingDigestPayload{Decisions: []meetingDigestDecision{{
		D: "Use vendor Kappa for the packaging pilot instead of Zebra", By: "AJ", Anchor: "tx-7", Importance: 5,
	}}})
	runLedgerPass(t, app, func(context.Context, string, openAITextRequest) (string, error) {
		return `{"verdicts":[{"i":0,"verdict":"supersedes"}]}`, nil
	})

	var fresh ledgerRecord
	for _, record := range ledgerRecordsOfEntity(app.memory.ledgerState(), ledgerEntityDecision) {
		if record.current() {
			fresh = record
		}
	}
	if fresh.ID == "" {
		t.Fatal("expected a current superseding record")
	}

	// lineage spans the two-record chain, oldest→newest.
	if lineage := app.memory.ledgerLineage(fresh.ID); len(lineage) != 2 {
		t.Fatalf("lineage = %+v, want the two-record supersession chain", lineage)
	}
	// evolution replays the whole arc (old add → supersede → new add), oldest first.
	transitions := app.memory.ledgerRecordEvolution(fresh.ID)
	if len(transitions) < 3 {
		t.Fatalf("transitions = %+v, want at least add/supersede/add across the lineage", transitions)
	}
	if !strings.Contains(transitions[0].Title, "Zebra") {
		t.Fatalf("first transition = %+v, want the original Zebra decision", transitions[0])
	}
	if last := transitions[len(transitions)-1]; !strings.Contains(last.Title, "Kappa") {
		t.Fatalf("final transition = %+v, want the superseding Kappa decision", last)
	}
	// deterministic: a second replay is byte-identical — no model, pure fold.
	if again := app.memory.ledgerRecordEvolution(fresh.ID); !reflect.DeepEqual(transitions, again) {
		t.Fatal("evolution replay is not deterministic across calls")
	}
}

func TestAppendLedgerEventsIsAtomicAndTyped(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	if _, err := app.memory.appendLedgerEvents([]meetingMemoryEntry{{
		ID: "bad-1", Kind: meetingMemoryKindTranscript, Text: "nope",
	}}); err == nil {
		t.Fatal("appendLedgerEvents accepted a non-ledger kind")
	}
	record := ledgerRecord{ID: "ldg-topic-1", Entity: ledgerEntityTopic, Title: "Test", Status: ledgerStatusActive, ValidFrom: "2026-07-06T10:00:00Z"}
	raw, err := json.Marshal(ledgerEventPayload{Op: ledgerOpAdd, Record: record, At: "2026-07-06T10:00:00Z"})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	entry := meetingMemoryEntry{ID: "ledger-event-test-1", Kind: meetingMemoryKindLedgerEvent, Text: string(raw), CreatedAt: time.Now().UTC()}
	if appended, err := app.memory.appendLedgerEvents([]meetingMemoryEntry{entry}); err != nil || appended != 1 {
		t.Fatalf("append: appended=%d err=%v", appended, err)
	}
	// idempotent replay: an already-seen id is skipped, never duplicated.
	if appended, err := app.memory.appendLedgerEvents([]meetingMemoryEntry{entry}); err != nil || appended != 0 {
		t.Fatalf("replay: appended=%d err=%v, want 0/nil", appended, err)
	}
	if state := app.memory.ledgerState(); len(state) != 1 || state["ldg-topic-1"].Title != "Test" {
		t.Fatalf("state = %+v, want the single folded record", state)
	}
}

/* ---------- F23: non-roster owner never mints a bogus position ---------- */

// A proposed decision only mints a position when its holder grounds to a real
// participant. The card-069 governance default (madeBy="Scout") must NOT mint a
// position under the ungroundable system name.
func TestLedgerPositionSkipsNonRosterOwner(t *testing.T) {
	if fact, ok := ledgerFactFromDecisionEntry(meetingMemoryEntry{
		ID:       "dec-gov",
		Text:     governanceLanesDecisionStatement,
		Metadata: map[string]string{"status": decisionStatusProposed, "madeBy": scoutParticipantName},
	}); ok {
		t.Fatalf("non-roster owner %q minted a position: %+v", scoutParticipantName, fact)
	}
	// a roster holder still mints their keyed position.
	fact, ok := ledgerFactFromDecisionEntry(meetingMemoryEntry{
		ID:       "dec-tim",
		Text:     "Tim favors holding the current pricing.",
		Metadata: map[string]string{"status": decisionStatusProposed, "madeBy": "Tim"},
	})
	if !ok || fact.Entity != ledgerEntityPosition || fact.Owner != "Tim" {
		t.Fatalf("roster holder should mint a Tim position; got %+v ok=%v", fact, ok)
	}
}

// End-to-end: a Scout-owned proposed default swept by the ledger produces ZERO
// position records (only the active roster decision folds).
func TestLedgerConsumesScoutDefaultWithoutPosition(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	if _, appended, err := app.memory.appendDecision("dec-active", "Ship the pilot with vendor Zebra", map[string]string{
		"status": decisionStatusActive, "madeBy": "AJ", "meetingId": "meeting-a",
	}); err != nil || !appended {
		t.Fatalf("append active: appended=%v err=%v", appended, err)
	}
	if _, appended, err := app.memory.appendDecision("dec-gov", governanceLanesDecisionStatement, map[string]string{
		"status": decisionStatusProposed, "madeBy": scoutParticipantName, "meetingId": "meeting-a",
	}); err != nil || !appended {
		t.Fatalf("append gov default: appended=%v err=%v", appended, err)
	}
	upsertLedgerTestDigest(t, app, "meeting-a", meetingDigestPayload{})
	runLedgerPass(t, app, forbiddenLedgerResponder(t))

	if positions := ledgerRecordsOfEntity(app.memory.ledgerState(), ledgerEntityPosition); len(positions) != 0 {
		t.Fatalf("positions = %+v, want none (Scout is not roster-groundable)", positions)
	}
}

/* ---------- F24: at-cap alias rotation lands (content compare, not length) ---------- */

func TestMergeLedgerFactAliasRotationAtCap(t *testing.T) {
	aliases := make([]string, 0, ledgerAliasCap)
	for index := 0; index < ledgerAliasCap; index++ {
		aliases = append(aliases, fmt.Sprintf("alias-%02d", index))
	}
	record := ledgerRecord{Entity: ledgerEntityTopic, Title: "Recurring topic", Status: ledgerStatusActive, Aliases: aliases}
	fact := ledgerFact{Entity: ledgerEntityTopic, Title: "Recurring topic", Aliases: []string{"brand new phrasing"}}

	merged, changed := mergeLedgerFact(record, fact, "2026-07-06T10:00:00Z")
	if !changed {
		t.Fatal("at-cap alias rotation must register as a change — length was equal, only content differs (F24)")
	}
	if len(merged.Aliases) != ledgerAliasCap {
		t.Fatalf("alias count = %d, want the cap %d", len(merged.Aliases), ledgerAliasCap)
	}
	if !ledgerSliceContains(merged.Aliases, "brand new phrasing") {
		t.Fatalf("new alias missing after rotation: %+v", merged.Aliases)
	}
	if ledgerSliceContains(merged.Aliases, "alias-00") {
		t.Fatalf("oldest alias should have been evicted: %+v", merged.Aliases)
	}
}

/* ---------- F17: digest aliases gate per-fact, never onto siblings ---------- */

func ledgerFactByTitle(facts []ledgerFact, title string) (ledgerFact, bool) {
	for _, fact := range facts {
		if fact.Title == title {
			return fact, true
		}
	}
	return ledgerFact{}, false
}

func TestDigestAliasesGatePerFact(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	// two topics + one unrelated decision; the storyline alias plainly renames the
	// Samsung topic but shares nothing with the lease topic or the hiring decision.
	entry := upsertLedgerTestDigest(t, app, "meeting-a", meetingDigestPayload{
		Decisions: []meetingDigestDecision{{D: "Approve the Q3 hiring budget", Anchor: "tx-d", Importance: 4}},
		Topics: []meetingDigestTopic{
			{T: "Samsung TV Plus partnership", Anchor: "tx-t1", Importance: 4},
			{T: "Office lease renewal", Anchor: "tx-t2", Importance: 2},
		},
		Aliases: []string{"the Samsung deal", "Samsung CTV"},
	})
	facts := ledgerFactsFromDigest(entry)

	samsung, ok := ledgerFactByTitle(facts, "Samsung TV Plus partnership")
	if !ok || len(samsung.Aliases) == 0 {
		t.Fatalf("the storyline topic should carry the aliases: %+v", samsung)
	}
	lease, ok := ledgerFactByTitle(facts, "Office lease renewal")
	if !ok || len(lease.Aliases) != 0 {
		t.Fatalf("an unrelated sibling topic must NOT inherit the storyline aliases: %+v", lease)
	}
	decision, ok := ledgerFactByTitle(facts, "Approve the Q3 hiring budget")
	if !ok || len(decision.Aliases) != 0 {
		t.Fatalf("an unrelated sibling decision must NOT inherit the storyline aliases: %+v", decision)
	}
}

// A digest with exactly ONE topic force-attaches its aliases even without a token
// overlap: the lone topic unambiguously IS the storyline (the rename-bridge seed).
func TestDigestAliasesForceAttachToSoleTopic(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	entry := upsertLedgerTestDigest(t, app, "meeting-a", meetingDigestPayload{
		Topics:  []meetingDigestTopic{{T: "Weekly logistics sync", Anchor: "tx-t", Importance: 3}},
		Aliases: []string{"the Korean TV deal"},
	})
	facts := ledgerFactsFromDigest(entry)
	topic, ok := ledgerFactByTitle(facts, "Weekly logistics sync")
	if !ok || len(topic.Aliases) == 0 {
		t.Fatalf("the sole topic should force-attach the storyline aliases: %+v", topic)
	}
}

/* ---------- F20: a ratified reversal re-enters the ledger via the decision lane ---------- */

func TestMarkDecisionRatifiedRefeedsReversalIntoLedger(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	// an active decision that folds into a canonical ledger record.
	if _, appended, err := app.memory.appendDecision("decision-old", "Vendor Zebra wins the packaging pilot", map[string]string{
		"status": decisionStatusActive, "madeBy": "AJ", "meetingId": "meeting-a",
		"dedupeKey": decisionDedupeKey("Vendor Zebra wins the packaging pilot"),
	}); err != nil || !appended {
		t.Fatalf("seed old: appended=%v err=%v", appended, err)
	}
	// a pending reversal — held out of the decision lane, so the ledger sweep
	// advances past it with no record of its own (token-distinct from the old
	// statement so the fold is deterministic; the supersedes LINK is metadata).
	if _, appended, err := app.memory.appendDecision("decision-new", "Weekly all-hands moves to Thursday mornings", map[string]string{
		"status": decisionStatusProposedSupersession, "supersedes": "decision-old",
		"madeBy": "AJ", "meetingId": "meeting-a",
		"dedupeKey": decisionDedupeKey("Weekly all-hands moves to Thursday mornings"),
	}); err != nil || !appended {
		t.Fatalf("seed new: appended=%v err=%v", appended, err)
	}

	// first pass: the old decision folds to a current record; the reversal is skipped.
	upsertLedgerTestDigest(t, app, "meeting-a", meetingDigestPayload{})
	runLedgerPass(t, app, forbiddenLedgerResponder(t))
	if got := len(ledgerCurrentDecisionTitles(app)); got != 1 {
		t.Fatalf("current decisions after first pass = %d, want just the old one", got)
	}

	// ratify the reversal: the row flips active and flags itself (and the retired
	// decision) for ledger re-feed.
	if _, changed, err := app.markDecisionRatified("decision-new", "AJ"); err != nil || !changed {
		t.Fatalf("ratify: changed=%v err=%v", changed, err)
	}

	// next pass re-feeds both: the new canon becomes a current decision record and
	// the retired one closes.
	upsertLedgerTestDigest(t, app, "meeting-a", meetingDigestPayload{})
	runLedgerPass(t, app, forbiddenLedgerResponder(t))

	titles := ledgerCurrentDecisionTitles(app)
	if len(titles) != 1 || titles[0] != "Weekly all-hands moves to Thursday mornings" {
		t.Fatalf("current decisions after ratify+pass = %+v, want just the ratified reversal", titles)
	}
	// the old statement's record is now closed (present in history, not current).
	oldClosed := false
	for _, record := range app.memory.ledgerState() {
		if record.Entity == ledgerEntityDecision && record.Title == "Vendor Zebra wins the packaging pilot" {
			if record.current() {
				t.Fatalf("the retired decision record is still current: %+v", record)
			}
			oldClosed = true
		}
	}
	if !oldClosed {
		t.Fatal("the retired decision never reached the ledger to be closed")
	}

	// the re-feed markers are cleared, so a subsequent pass folds nothing new.
	for _, id := range []string{"decision-old", "decision-new"} {
		entry, _ := app.memory.entryByKindAndID(meetingMemoryKindDecision, id)
		if entry.Metadata[entityLedgerRefeedMetadataKey] == "1" {
			t.Fatalf("re-feed marker on %s was never cleared", id)
		}
	}
}

// ledgerCurrentDecisionTitles is the current (validity window open) decision
// record titles — the read the F20 test asserts against.
func ledgerCurrentDecisionTitles(app *kanbanBoardApp) []string {
	titles := []string{}
	for _, record := range app.memory.ledgerState() {
		if record.Entity == ledgerEntityDecision && record.current() {
			titles = append(titles, record.Title)
		}
	}
	return titles
}
