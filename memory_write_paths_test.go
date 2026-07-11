package main

import (
	"strings"
	"testing"
	"time"
)

// Wave F — the deliberate/audited write paths (memory study §6 items 2.1, 1.4,
// 1.5b). Three seams: note_for_the_record (author-certain deliberate write),
// dead-letter tombstones wired into coverage honesty, and the own-chat delete
// tombstone.

// --- 2.1 note_for_the_record ------------------------------------------------

// A note filed for the record is author-certain, recall-eligible knowledge: it
// is searchable, enters model context, and renders in the client timeline — the
// escape hatch that lets a stance argued to Scout reach company memory.
func TestNoteForTheRecordFilesRecallEligibleNote(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	const author = "aj@shareability.com"

	result, changed, err := app.noteForTheRecordTool(map[string]any{
		"note":  "We never ship Q4 launches; the freeze is absolute.",
		"topic": "Q4 launches",
	}, author, "agent-thread:thread-1:call-1")
	if err != nil {
		t.Fatalf("noteForTheRecordTool: %v", err)
	}
	if changed {
		t.Fatal("filing a note must not report a board change")
	}
	if result["recorded"] != true {
		t.Fatalf("result=%v, want recorded=true", result)
	}

	// The entry lands under kind=note, author stamped from the session (not a
	// heuristic), authorCertain=true.
	var noteEntry meetingMemoryEntry
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindNote, 0) {
		noteEntry = entry
	}
	if noteEntry.ID == "" {
		t.Fatal("no note entry was filed")
	}
	if wantAuthor := participantNameForEmail(author); noteEntry.Metadata["author"] != wantAuthor {
		t.Fatalf("author=%q, want the session identity %q", noteEntry.Metadata["author"], wantAuthor)
	}
	if noteEntry.Metadata["authorCertain"] != "true" {
		t.Fatalf("authorCertain=%q, want true", noteEntry.Metadata["authorCertain"])
	}

	// (a) searchable — entry.Text is the statement, so store.search matches for free.
	if matches := app.memory.search("Q4 launches freeze", 10); len(matches) == 0 {
		t.Fatal("store.search did not surface the filed note")
	} else {
		found := false
		for _, m := range matches {
			if m.Entry.Kind == meetingMemoryKindNote {
				found = true
			}
		}
		if !found {
			t.Fatal("store.search matched, but not the note entry")
		}
	}

	// (b) model context — the note enters contextEntriesForQuery.
	assertNoteInContext := false
	for _, entry := range app.memory.contextEntriesForQuery("Q4 launches freeze", 30, time.Now()) {
		if entry.Kind == meetingMemoryKindNote {
			assertNoteInContext = true
		}
	}
	if !assertNoteInContext {
		t.Fatal("the note never reached contextEntriesForQuery")
	}

	// (c) client timeline — unlike decisions/narratives, a deliberate note is
	// timeline-worthy and survives visibleMeetingMemoryEntries.
	timelineHasNote := false
	for _, entry := range visibleMeetingMemoryEntries(app.memory.snapshot(0), 500) {
		if entry.Kind == meetingMemoryKindNote {
			timelineHasNote = true
		}
	}
	if !timelineHasNote {
		t.Fatal("the note is missing from the client memory timeline")
	}
}

// A double-call with the same content and scope files exactly once (the seen-map
// idempotency, run-log discipline) — a retried turn never duplicates a note.
func TestNoteForTheRecordIsIdempotent(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	args := map[string]any{"note": "Ball Dogs is the lead IP."}
	scope := "agent-thread:thread-9:call-9"

	first, _, err := app.noteForTheRecordTool(args, "aj@shareability.com", scope)
	if err != nil {
		t.Fatalf("first file: %v", err)
	}
	second, _, err := app.noteForTheRecordTool(args, "aj@shareability.com", scope)
	if err != nil {
		t.Fatalf("second file: %v", err)
	}
	if first["id"] != second["id"] {
		t.Fatalf("idempotent id drifted: %v vs %v", first["id"], second["id"])
	}
	if second["recorded"] != false {
		t.Fatalf("second identical call recorded=%v, want false (deduped)", second["recorded"])
	}
	if got := len(app.memory.entriesOfKind(meetingMemoryKindNote, 0)); got != 1 {
		t.Fatalf("filed %d note entries, want exactly 1", got)
	}
}

// kind=decision routes the statement onto the decision ledger as a PROPOSED
// decision the team can ratify, attributed to the named owner (owner wins over
// the filer so "Tim is against X" credits Tim).
func TestNoteForTheRecordDecisionLandsProposed(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, _, err := app.noteForTheRecordTool(map[string]any{
		"note":  "Tim is against the Samsung deal.",
		"kind":  "decision",
		"owner": "Tim",
	}, "aj@shareability.com", "agent-thread:thread-2:call-2"); err != nil {
		t.Fatalf("noteForTheRecordTool decision: %v", err)
	}

	decisions := app.memory.entriesOfKind(meetingMemoryKindDecision, 0)
	if len(decisions) != 1 {
		t.Fatalf("filed %d decisions, want exactly 1", len(decisions))
	}
	decision := decisions[0]
	if decision.Metadata["status"] != decisionStatusProposed {
		t.Fatalf("decision status=%q, want %q", decision.Metadata["status"], decisionStatusProposed)
	}
	if decision.Metadata["madeBy"] != "Tim" {
		t.Fatalf("decision madeBy=%q, want the named owner Tim", decision.Metadata["madeBy"])
	}
	if !strings.Contains(decision.Text, "Samsung") {
		t.Fatalf("decision text=%q, want the filed statement", decision.Text)
	}
	// The certain filer is recorded distinctly from the attributed owner.
	if decision.Metadata["filedBy"] != participantNameForEmail("aj@shareability.com") {
		t.Fatalf("filedBy=%q, want the session filer", decision.Metadata["filedBy"])
	}
}

// The shared room-voice path has no session author, so a note filed there is
// author-uncertain — honest, never a fabricated attribution.
func TestNoteForTheRecordRoomVoiceIsAuthorUncertain(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	// The shared dispatch path (applyToolCallArgs) passes no author.
	if _, _, err := app.applyToolCallArgs("note_for_the_record", map[string]any{
		"note": "The room agreed to revisit pricing next week.",
	}); err != nil {
		t.Fatalf("applyToolCallArgs note_for_the_record: %v", err)
	}
	notes := app.memory.entriesOfKind(meetingMemoryKindNote, 0)
	if len(notes) != 1 {
		t.Fatalf("filed %d notes, want 1", len(notes))
	}
	if notes[0].Metadata["authorCertain"] != "false" {
		t.Fatalf("room-voice note authorCertain=%q, want false", notes[0].Metadata["authorCertain"])
	}
}

// The tool is registered in every dispatch table it must reach: the master tool
// list (so room voice sees it), the private-voice allowlist, and the
// orchestrator allowlist.
func TestNoteForTheRecordRegisteredInDispatchTables(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	inMasterList := false
	for _, tool := range app.kanbanTools() {
		if name, _ := tool["name"].(string); name == "note_for_the_record" {
			inMasterList = true
		}
	}
	if !inMasterList {
		t.Fatal("note_for_the_record missing from kanbanTools()")
	}
	if !privateRealtimeVoiceToolAllowed("note_for_the_record") {
		t.Fatal("note_for_the_record not allowed on the private voice surface")
	}
	if !orchestratorToolAllowlist["note_for_the_record"] {
		t.Fatal("note_for_the_record missing from the orchestrator allowlist")
	}
}

// The private-thread privacy contract still holds: filing a note is a deliberate
// tool call (the consent surface), so a note enters recall — but ambient private
// thread text stays out. This test proves the note seam does not open a leak in
// the ambient path (the canary contract runs separately in
// TestPrivateChatBrainContract).
func TestNoteForTheRecordDoesNotBreachAmbientPrivacy(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	// A note filed FROM a private thread is company-recallable by design.
	if _, _, err := app.noteForTheRecordTool(map[string]any{
		"note": "For the record: I want us to pass on the acquisition.",
	}, "aj@shareability.com", "agent-thread:private-thread:call-x"); err != nil {
		t.Fatalf("noteForTheRecordTool: %v", err)
	}
	// The note IS searchable (deliberate write), proving the seam works...
	if len(app.memory.search("pass on the acquisition", 10)) == 0 {
		t.Fatal("the deliberately-filed note should be recall-eligible")
	}
	// ...but the note kind is NOT a UI-state kind, and scout_chat_thread ambient
	// content remains UI-state — the two seams are independent.
	if isUIStateMemoryKind(meetingMemoryKindNote) {
		t.Fatal("note must be knowledge (not UI-state), or recall would drop it")
	}
	if !isUIStateMemoryKind(meetingMemoryKindScoutChat) {
		t.Fatal("scout_chat_thread must stay UI-state so ambient private text never enters recall")
	}
}

// --- 1.4 dead-letter tombstones + coverage honesty --------------------------

// When the runner dead-letters a head input after repeated failures, a
// tombstone survives recording the agent, meeting, and abandoned window — so the
// FACT that a synthesis window was skipped is never trace-free.
func TestDeadLetterWritesTombstone(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	var produced [][]string
	agent := newTestAmbientAgent(&produced)

	appendTestTranscript(t, app, "dl-head", "Boot Barn synthesis window that keeps failing.")
	meetingID := app.memory.currentMeetingID(officeRoomID)
	if meetingID == "" {
		t.Fatal("appending a transcript should have opened the office sitting")
	}

	// Drive the head to the dead-letter cap.
	for i := 0; i < ambientAgentMaxWindowAttempts; i++ {
		app.recordAmbientAgentFailure(agent, "dl-head", officeRoomID)
	}

	stubs := app.memory.entriesOfKind(meetingMemoryKindDeadLetter, 0)
	if len(stubs) != 1 {
		t.Fatalf("wrote %d dead-letter tombstones, want exactly 1", len(stubs))
	}
	stub := stubs[0]
	if stub.Metadata[deadLetterAgentMetadataKey] != agent.name {
		t.Fatalf("tombstone agent=%q, want %q", stub.Metadata[deadLetterAgentMetadataKey], agent.name)
	}
	if stub.Metadata["meetingId"] != meetingID {
		t.Fatalf("tombstone meetingId=%q, want the abandoned input's meeting %q", stub.Metadata["meetingId"], meetingID)
	}
	if stub.Metadata[deadLetterSpanStartMetadataKey] == "" {
		t.Fatal("tombstone should carry the abandoned window span")
	}
	// The stub is hidden from recall (expired) and never a search candidate.
	if !memoryEntryHiddenFromRecall(stub) {
		t.Fatal("dead-letter tombstone must be hidden from recall")
	}
	if len(app.memory.search("Boot Barn synthesis window", 20)) != 0 {
		// only the tombstone text matches; the raw transcript uses different words
		for _, m := range app.memory.search("Boot Barn synthesis window", 20) {
			if m.Entry.Kind == meetingMemoryKindDeadLetter {
				t.Fatal("dead-letter tombstone leaked into search")
			}
		}
	}
}

// A dead-letter overlapping a meeting flips its coverage to partial_synthesis —
// overriding even a "full" capture stamp — and the detail note explains it.
func TestDeadLetterFlipsCoverageToPartialSynthesis(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	meetingID := "meeting-20260710-101500-000000009"
	digest := `{"meetingId":"` + meetingID + `","title":"Pilot"}`
	if _, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, meetingID, digest, map[string]string{
		"meetingId":                meetingID,
		digestSpanStartMetadataKey: "2026-07-10T16:55:00Z",
		digestSpanEndMetadataKey:   "2026-07-10T17:30:00Z",
		digestCoverageMetadataKey:  coverageLabelFull,
	}); err != nil {
		t.Fatalf("upsertDigest: %v", err)
	}

	// Baseline: full capture, no dead-letter.
	if got := app.meetingCoverageDetail(meetingID).Label; got != coverageLabelFull {
		t.Fatalf("baseline coverage=%q, want %q", got, coverageLabelFull)
	}

	// A synthesis pass later abandoned a window of this meeting.
	// The synthesis lane (transcript→brain→digest) is the only one that flips
	// coverage (finding F13), so the stub must name a real synthesis agent.
	if _, _, err := app.memory.appendDeadLetter("dead-letter-brain-office-brain-x", "brain abandoned a window", map[string]string{
		relevanceMetadataKey:           relevanceExpired,
		deadLetterAgentMetadataKey:     meetingBrainAgentName,
		"meetingId":                    meetingID,
		deadLetterSpanStartMetadataKey: "2026-07-10T17:05:00Z",
		deadLetterSpanEndMetadataKey:   "2026-07-10T17:05:00Z",
	}); err != nil {
		t.Fatalf("appendDeadLetter: %v", err)
	}

	summary := app.meetingCoverageDetail(meetingID)
	if summary.Label != coverageLabelPartialSynthesis {
		t.Fatalf("coverage=%q, want %q after a dead-letter overlap", summary.Label, coverageLabelPartialSynthesis)
	}
	note := coverageDetailNote(summary)
	if !strings.Contains(strings.ToLower(note), "synthes") {
		t.Fatalf("coverage note=%q, want a synthesis caveat", note)
	}
	if !summary.partial() {
		t.Fatal("partial_synthesis must read as partial coverage")
	}
}

// Without a dead-letter stub, the coverage read is unchanged — the existing
// stamp/legacy behavior is untouched (the guard is additive).
func TestDeadLetterCoverageUnchangedWithoutStub(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	meetingID := "meeting-20260710-090000-000000010"
	digest := `{"meetingId":"` + meetingID + `","title":"Standup"}`
	if _, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, meetingID, digest, map[string]string{
		"meetingId":                meetingID,
		digestSpanStartMetadataKey: "2026-07-10T14:00:00Z",
		digestSpanEndMetadataKey:   "2026-07-10T14:20:00Z",
		digestCoverageMetadataKey:  coverageLabelPartialGaps,
	}); err != nil {
		t.Fatalf("upsertDigest: %v", err)
	}
	if got := app.meetingCoverageDetail(meetingID).Label; got != coverageLabelPartialGaps {
		t.Fatalf("coverage=%q, want the untouched stamp %q", got, coverageLabelPartialGaps)
	}
}

// A dead-letter for a DIFFERENT meeting must not steal another meeting's blame.
func TestDeadLetterDoesNotBleedAcrossMeetings(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	meetingID := "meeting-20260710-120000-000000011"
	digest := `{"meetingId":"` + meetingID + `","title":"Sync"}`
	if _, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, meetingID, digest, map[string]string{
		"meetingId":                meetingID,
		digestSpanStartMetadataKey: "2026-07-10T19:00:00Z",
		digestSpanEndMetadataKey:   "2026-07-10T19:30:00Z",
		digestCoverageMetadataKey:  coverageLabelFull,
	}); err != nil {
		t.Fatalf("upsertDigest: %v", err)
	}
	// A real synthesis-lane dead-letter (so the meetingId-bleed guard, not the
	// F13 lane scope, is what keeps this meeting "full") that belongs to another
	// meeting entirely.
	if _, _, err := app.memory.appendDeadLetter("dead-letter-brain-office-other", "abandoned other meeting", map[string]string{
		relevanceMetadataKey:       relevanceExpired,
		deadLetterAgentMetadataKey: meetingBrainAgentName,
		"meetingId":                "meeting-somewhere-else",
	}); err != nil {
		t.Fatalf("appendDeadLetter: %v", err)
	}
	if got := app.meetingCoverageDetail(meetingID).Label; got != coverageLabelFull {
		t.Fatalf("coverage=%q, want %q (another meeting's dead-letter must not bleed in)", got, coverageLabelFull)
	}
}

// --- 1.5b own-chat delete tombstone -----------------------------------------

// Deleting an own room-chat message removes the CONTENT but leaves a dated,
// recall-hidden tombstone recording the FACT of the deletion.
func TestChatDeleteLeavesTombstone(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	const (
		speaker = "Tom"
		email   = "tom@shareability.com"
		secret  = "chatdeletecanary5521"
	)
	if _, appended, err := app.memory.appendRoomChatTranscript("chat-1", speaker, secret+" this was a mistake"); err != nil || !appended {
		t.Fatalf("append room chat: appended=%v err=%v", appended, err)
	}

	payload, ok := app.deleteRoomChatMessage("chat-1", email, speaker)
	if !ok {
		t.Fatal("deleteRoomChatMessage returned !ok — the author delete should succeed")
	}
	if payload["id"] != "chat-1" {
		t.Fatalf("delete payload=%v, want id chat-1", payload)
	}

	// The CONTENT is gone: the original transcript no longer exists, and the
	// canary is unsearchable.
	if _, found := app.memory.entryByID("chat-1"); found {
		t.Fatal("deleted message content still present")
	}
	for _, m := range app.memory.search(secret, 20) {
		if strings.Contains(m.Entry.Text, secret) {
			t.Fatalf("deleted content still recall-findable via %s/%s", m.Entry.ID, m.Entry.Kind)
		}
	}

	// The FACT of the deletion survives as a hidden tombstone.
	tombstones := app.memory.entriesOfKind(meetingMemoryKindChatDelete, 0)
	if len(tombstones) != 1 {
		t.Fatalf("wrote %d chat-delete tombstones, want exactly 1", len(tombstones))
	}
	tomb := tombstones[0]
	if tomb.Metadata["deletedId"] != "chat-1" {
		t.Fatalf("tombstone deletedId=%q, want chat-1", tomb.Metadata["deletedId"])
	}
	if tomb.Metadata["deletedBy"] != participantNameForEmail(email) {
		t.Fatalf("tombstone deletedBy=%q, want %q", tomb.Metadata["deletedBy"], participantNameForEmail(email))
	}
	if !memoryEntryHiddenFromRecall(tomb) {
		t.Fatal("chat-delete tombstone must be hidden from recall")
	}
	// It never surfaces in the client timeline.
	for _, entry := range visibleMeetingMemoryEntries(app.memory.snapshot(0), 500) {
		if entry.Kind == meetingMemoryKindChatDelete {
			t.Fatal("chat-delete tombstone leaked into the client timeline")
		}
	}
}

// --- F12/F27: decision attribution never fabricates a non-roster owner --------

// kind=decision attribution has three honest cases: no owner → the filer records
// their own stance; a roster owner → attributed to that member; a NAMED but
// non-roster owner → NEVER silently replaced by the filer (that would fabricate a
// stance in the who-thinks-what index) — madeBy blanks and the tool says so.
func TestNoteForTheRecordDecisionAttributionCases(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	const filer = "aj@shareability.com"
	filerName := participantNameForEmail(filer)

	// (1) no owner: the filer is recording their own stance.
	own, _, err := app.noteForTheRecordTool(map[string]any{
		"note": "For the record, I want us to pass on the acquisition.",
		"kind": "decision",
	}, filer, "agent-thread:t1:c1")
	if err != nil {
		t.Fatalf("own stance: %v", err)
	}
	if own["madeBy"] != filerName {
		t.Fatalf("no-owner madeBy = %v, want the filer %q (own stance)", own["madeBy"], filerName)
	}

	// (2) roster owner: attributed to that member, never the filer.
	roster, _, err := app.noteForTheRecordTool(map[string]any{
		"note": "Tim is against the Samsung deal.", "kind": "decision", "owner": "Tim",
	}, filer, "agent-thread:t2:c2")
	if err != nil {
		t.Fatalf("roster owner: %v", err)
	}
	if roster["madeBy"] != "Tim" {
		t.Fatalf("roster madeBy = %v, want Tim", roster["madeBy"])
	}

	// (3) non-roster owner: NEVER the filer — madeBy blanks and an honest message
	// explains the owner could not be grounded.
	nonRoster, _, err := app.noteForTheRecordTool(map[string]any{
		"note": "Jordan from Acme wants us to hold pricing.", "kind": "decision", "owner": "Jordan",
	}, filer, "agent-thread:t3:c3")
	if err != nil {
		t.Fatalf("non-roster owner: %v", err)
	}
	if madeBy := asString(nonRoster["madeBy"]); madeBy != "" {
		t.Fatalf("non-roster madeBy = %q, want blank (never the filer — no fabricated attribution)", madeBy)
	}
	if _, ok := nonRoster["message"]; !ok {
		t.Fatal("a non-roster owner should return an honest tool message")
	}
	// the persisted decision entry itself carries a blank madeBy.
	entry, ok := app.memory.entryByKindAndID(meetingMemoryKindDecision, asString(nonRoster["id"]))
	if !ok {
		t.Fatal("non-roster decision entry not found")
	}
	if entry.Metadata["madeBy"] != "" {
		t.Fatalf("persisted madeBy = %q, want blank — the filer must never be substituted", entry.Metadata["madeBy"])
	}
	// but the filer is still recorded distinctly as who FILED it (audit trail).
	if entry.Metadata["filedBy"] != filerName {
		t.Fatalf("filedBy = %q, want the filer %q", entry.Metadata["filedBy"], filerName)
	}
}

// --- F14: note idempotency scope — dedupe double-fires, allow later re-filing ---

func TestHourBucketedNoteScope(t *testing.T) {
	base := time.Date(2026, 7, 11, 14, 30, 0, 0, time.UTC)
	sameHour := time.Date(2026, 7, 11, 14, 59, 30, 0, time.UTC)
	nextHour := time.Date(2026, 7, 11, 15, 0, 5, 0, time.UTC)

	if hourBucketedNoteScope("room-voice", base) != hourBucketedNoteScope("room-voice", sameHour) {
		t.Fatal("same-hour scopes must match so an accidental double-fire dedupes")
	}
	if hourBucketedNoteScope("room-voice", base) == hourBucketedNoteScope("room-voice", nextHour) {
		t.Fatal("a later hour must produce a distinct scope so a deliberate re-file lands")
	}
	if hourBucketedNoteScope("private-voice:a@x.com", base) == hourBucketedNoteScope("private-voice:b@x.com", base) {
		t.Fatal("distinct namespaces must never collide in the same hour")
	}
}

// The room-voice dispatch now buckets its scope, so an accidental double-fire in
// the same hour files exactly once (previously these deduped FOREVER; the point
// of the bucket is that a LATER hour re-files — proven by TestHourBucketedNoteScope).
func TestRoomVoiceNoteDedupesDoubleFireWithinHour(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	args := map[string]any{"note": "The room agreed to revisit pricing next week."}
	if _, _, err := app.applyToolCallArgs("note_for_the_record", args); err != nil {
		t.Fatalf("first room-voice note: %v", err)
	}
	if _, _, err := app.applyToolCallArgs("note_for_the_record", args); err != nil {
		t.Fatalf("second room-voice note: %v", err)
	}
	if got := len(app.memory.entriesOfKind(meetingMemoryKindNote, 0)); got != 1 {
		t.Fatalf("room-voice double-fire filed %d notes, want 1 (same-hour dedupe)", got)
	}
}
