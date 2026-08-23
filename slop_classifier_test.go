package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func slopKeepVerdictsJSON(t *testing.T, ids []string) string {
	t.Helper()
	verdicts := make([]slopVerdict, 0, len(ids))
	for _, id := range ids {
		verdicts = append(verdicts, slopVerdict{EntryID: id, Verdict: "keep", Confidence: 0.99, Reason: "retain", Evidence: "still potentially useful"})
	}
	raw, err := json.Marshal(verdicts)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestSlopClassifierAgentContract(t *testing.T) {
	agent := slopClassifierAgent()
	if agent.name != "slop_classifier" {
		t.Fatalf("agent name=%q, want slop_classifier", agent.name)
	}
	if agent.inputKind != meetingMemoryKindTranscript || agent.artifactKind != meetingMemoryKindSlopPass {
		t.Fatalf("agent kinds=%q->%q, want transcript->slop_pass", agent.inputKind, agent.artifactKind)
	}
	if agent.cursorMetadataKey != "slopConsumedThrough" {
		t.Fatalf("cursor key=%q, want slopConsumedThrough", agent.cursorMetadataKey)
	}
	if agent.intervalEnv != "SLOP_CLASSIFIER_INTERVAL" || agent.defaultInterval != 6*time.Hour {
		t.Fatalf("interval env/default=%q/%v, want SLOP_CLASSIFIER_INTERVAL/6h", agent.intervalEnv, agent.defaultInterval)
	}
	if agent.defaultMinBatch != 8 {
		t.Fatalf("min batch=%d, want 8", agent.defaultMinBatch)
	}
}

// TestSlopCandidateEligibleDenyList is the deny-list table test: every
// protected class is rejected in the candidate builder (code, not prompt).
func TestSlopCandidateEligibleDenyList(t *testing.T) {
	now := time.Now().UTC()
	old := now.Add(-30 * 24 * time.Hour)
	fresh := now.Add(-1 * time.Hour)

	entry := func(kind string, created time.Time, metadata map[string]string) meetingMemoryEntry {
		return meetingMemoryEntry{ID: "x", Kind: kind, Text: "body", CreatedAt: created, Metadata: metadata}
	}

	cases := []struct {
		name  string
		entry meetingMemoryEntry
		want  bool
	}{
		{"old active transcript", entry(meetingMemoryKindTranscript, old, nil), true},
		{"old unpublished artifact", entry(meetingMemoryKindOSArtifact, old, map[string]string{"published": "false"}), true},
		{"young transcript (7d gate)", entry(meetingMemoryKindTranscript, fresh, nil), false},
		{"young artifact (7d gate)", entry(meetingMemoryKindOSArtifact, fresh, nil), false},
		{"decision kind", entry(meetingMemoryKindDecision, old, nil), false},
		{"archive kind", entry(meetingMemoryKindArchive, old, nil), false},
		{"package kind", entry(meetingMemoryKindPackage, old, nil), false},
		{"mission_insight ui-state", entry(meetingMemoryKindMissionInsight, old, nil), false},
		{"scout_chat ui-state", entry(meetingMemoryKindScoutChat, old, nil), false},
		{"published artifact", entry(meetingMemoryKindOSArtifact, old, map[string]string{"published": "true"}), false},
		{"package-attached artifact", entry(meetingMemoryKindOSArtifact, old, map[string]string{"packageId": "pkg-1"}), false},
		{"human-pinned transcript", entry(meetingMemoryKindTranscript, old, map[string]string{"pinned": "true"}), false},
		{"already quarantined", entry(meetingMemoryKindTranscript, old, map[string]string{relevanceMetadataKey: relevanceQuarantined}), false},
		{"already archived", entry(meetingMemoryKindTranscript, old, map[string]string{relevanceMetadataKey: relevanceArchived}), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := slopCandidateEligible(tc.entry, now); got != tc.want {
				t.Fatalf("slopCandidateEligible=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestApplySlopVerdictThresholds(t *testing.T) {
	cases := []struct {
		name       string
		verdict    string
		confidence float64
		want       string
	}{
		{"quarantine at 0.85", "quarantine", 0.85, relevanceQuarantined},
		{"quarantine below 0.85 keeps", "quarantine", 0.84, relevanceActive},
		{"archive at 0.70", "archive", 0.70, relevanceArchived},
		{"archive below 0.70 keeps", "archive", 0.69, relevanceActive},
		{"explicit keep", "keep", 0.99, relevanceActive},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := newIsolatedKanbanBoardApp(t)
			if _, ok, err := app.memory.appendTranscript("t-verdict", "", "Some orphaned chatter about the weather outside."); err != nil || !ok {
				t.Fatalf("append: ok=%v err=%v", ok, err)
			}
			entry, _ := app.memory.entryByID("t-verdict")
			got, err := app.applySlopVerdict(entry, slopVerdict{EntryID: "t-verdict", Verdict: tc.verdict, Confidence: tc.confidence, Reason: "r", Evidence: "e"})
			if err != nil {
				t.Fatalf("applySlopVerdict: %v", err)
			}
			if got != tc.want {
				t.Fatalf("applySlopVerdict returned %q, want %q", got, tc.want)
			}
			stamped, _ := app.memory.entryByID("t-verdict")
			if memoryEntryRelevance(stamped) != tc.want {
				t.Fatalf("stamped relevance=%q, want %q", memoryEntryRelevance(stamped), tc.want)
			}
			if tc.want == relevanceQuarantined {
				if strings.TrimSpace(stamped.Metadata["expiresAt"]) == "" || strings.TrimSpace(stamped.Metadata["reviewedBy"]) != reviewedByClassifier {
					t.Fatalf("quarantine must stamp expiresAt + reviewedBy=classifier, got %v", stamped.Metadata)
				}
			}
		})
	}
}

func TestSweepExpiredQuarantineRetainsTranscriptSummaryFirstAndLeavesStub(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	const transcriptText = "Redundant chatter retained for exact meeting drilldown."
	if _, ok, err := app.memory.appendTranscript("t-expire", "", transcriptText); err != nil || !ok {
		t.Fatalf("append: ok=%v err=%v", ok, err)
	}
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindTranscript, "t-expire", transcriptText, map[string]string{
		relevanceMetadataKey: relevanceQuarantined,
		"expiresAt":          past,
		"classifierReason":   "orphaned chatter",
	}); err != nil {
		t.Fatalf("quarantine: %v", err)
	}

	app.sweepExpiredQuarantine("cursor-42")

	retained, found := app.memory.entryByID("t-expire")
	if !found || retained.Text != transcriptText {
		t.Fatalf("expired transcript source must remain byte-for-byte available: found=%v entry=%+v", found, retained)
	}
	if memoryEntryRelevance(retained) != relevanceArchived || retained.Metadata[retainedRawTranscriptMetadataKey] != "true" {
		t.Fatalf("expired transcript must become summary-first retained source, got %v", retained.Metadata)
	}
	for _, visible := range app.memory.snapshot(0) {
		if visible.ID == retained.ID {
			t.Fatal("retained raw transcript leaked into ordinary snapshot recall")
		}
	}
	meetingID := strings.TrimSpace(retained.Metadata["meetingId"])
	segments := meetingRecordSegments([]meetingMemoryEntry{retained}, meetingID)
	if len(segments) != 1 || segments[0].Text == "" {
		t.Fatalf("exact meeting drilldown lost retained transcript: %+v", segments)
	}
	stubs := app.memory.entriesOfKind(meetingMemoryKindSlopPass, 0)
	foundStub := false
	for _, stub := range stubs {
		if stub.Metadata["retainedId"] == "t-expire" {
			foundStub = true
			if stub.Metadata["reason"] == "" {
				t.Fatal("audit stub must record the retention reason")
			}
			if stub.Metadata[slopClassifierCursorKey] != "cursor-42" {
				t.Fatalf("audit stub must carry the forward cursor, got %q", stub.Metadata[slopClassifierCursorKey])
			}
		}
	}
	if !foundStub {
		t.Fatal("a slop_pass audit stub must record non-destructive retention")
	}
}

func TestSlopGenericArtifactUpdateCannotMutateRoomScope(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	entry, appended, err := app.memory.appendOSArtifact("slop-scope-immutable", "artifact body", map[string]string{
		"title": "Slop scope proof", "visibility": "room_only", "roomId": "room-a",
		"meetingId": "sitting-a", "sittingId": "sitting-a", "mediaGeneration": "7",
	})
	if err != nil || !appended {
		t.Fatalf("append artifact appended=%v err=%v", appended, err)
	}
	if _, changed, err := app.memory.updateEntryWithMetadata(meetingMemoryKindOSArtifact, entry.ID, entry.Text, map[string]string{
		relevanceMetadataKey: relevanceQuarantined,
		"roomId":             "room-b",
	}); err == nil || changed {
		t.Fatalf("generic slop update mutated room scope changed=%v err=%v", changed, err)
	}
	header, found := app.memory.artifactAuthorizationHeaderByID(entry.ID)
	if !found || header.RoomID != "room-a" || header.SittingID != "sitting-a" || header.MediaGeneration != 7 {
		t.Fatalf("generic slop update changed authorization scope: found=%v header=%+v", found, header)
	}
}

// TestSweepDoesNotDeleteBeforeExpiry guards the 30-day reprieve.
func TestSweepDoesNotDeleteBeforeExpiry(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	if _, ok, err := app.memory.appendTranscript("t-fresh-quar", "", "Freshly quarantined chatter, still inside the reprieve."); err != nil || !ok {
		t.Fatalf("append: ok=%v err=%v", ok, err)
	}
	future := time.Now().UTC().Add(30 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindTranscript, "t-fresh-quar", "Freshly quarantined chatter, still inside the reprieve.", map[string]string{
		relevanceMetadataKey: relevanceQuarantined,
		"expiresAt":          future,
	}); err != nil {
		t.Fatalf("quarantine: %v", err)
	}

	app.sweepExpiredQuarantine("cursor-1")

	if _, found := app.memory.entryByID("t-fresh-quar"); !found {
		t.Fatal("a quarantined entry inside its 30-day reprieve must NOT be deleted")
	}
}

// TestRunSlopClassifierIdempotent proves the cursor + run-lock stop a second
// pass from re-classifying an already-consumed window.
func TestRunSlopClassifierIdempotent(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	for i := 0; i < 8; i++ {
		id := "t-idem-" + string(rune('a'+i))
		if _, ok, err := app.memory.appendTranscript(id, "", "Distinct settled remark number "+string(rune('a'+i))+" about logistics and screens."); err != nil || !ok {
			t.Fatalf("append %s: ok=%v err=%v", id, ok, err)
		}
		backdateMemoryEntry(app.memory, id, 10*24*time.Hour)
	}

	calls := 0
	ids := []string{"t-idem-a", "t-idem-b", "t-idem-c", "t-idem-d", "t-idem-e", "t-idem-f", "t-idem-g", "t-idem-h"}
	responder := func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		calls++
		return slopKeepVerdictsJSON(t, ids), nil
	}

	if err := app.runSlopClassifierOnce(slopClassifierAgent(), context.Background(), "test-key", responder, 8); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if calls != 1 {
		t.Fatalf("first pass should call the model once, got %d", calls)
	}
	if err := app.runSlopClassifierOnce(slopClassifierAgent(), context.Background(), "test-key", responder, 8); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if calls != 1 {
		t.Fatalf("second pass must not re-classify the consumed window; model calls=%d, want 1", calls)
	}
}

func TestSlopIncompleteVerdictSetsHoldCursor(t *testing.T) {
	ids := []string{"slop-set-a", "slop-set-b", "slop-set-c", "slop-set-d", "slop-set-e", "slop-set-f", "slop-set-g", "slop-set-h"}
	valid := make([]slopVerdict, 0, len(ids))
	for _, id := range ids {
		valid = append(valid, slopVerdict{EntryID: id, Verdict: "keep", Confidence: 0.99, Reason: "retain", Evidence: "still potentially useful"})
	}
	encode := func(t *testing.T, verdicts []slopVerdict) string {
		t.Helper()
		raw, err := json.Marshal(verdicts)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	duplicate := append([]slopVerdict(nil), valid...)
	duplicate[len(duplicate)-1].EntryID = duplicate[0].EntryID
	invented := append([]slopVerdict(nil), valid...)
	invented[len(invented)-1].EntryID = "invented-slop-id"
	unknownVerdict := append([]slopVerdict(nil), valid...)
	unknownVerdict[0].Verdict = "defer"
	missingVerdict := append([]slopVerdict(nil), valid...)
	missingVerdict[0].Verdict = ""
	negativeConfidence := append([]slopVerdict(nil), valid...)
	negativeConfidence[0].Confidence = -0.1
	overConfidence := append([]slopVerdict(nil), valid...)
	overConfidence[0].Confidence = 1.1
	missingReason := append([]slopVerdict(nil), valid...)
	missingReason[0].Reason = ""
	missingEvidence := append([]slopVerdict(nil), valid...)
	missingEvidence[0].Evidence = ""
	for _, test := range []struct {
		name   string
		output string
	}{
		{name: "empty", output: `[]`},
		{name: "partial", output: encode(t, valid[:1])},
		{name: "duplicate", output: encode(t, duplicate)},
		{name: "invented", output: encode(t, invented)},
		{name: "unknown verdict", output: encode(t, unknownVerdict)},
		{name: "missing verdict", output: encode(t, missingVerdict)},
		{name: "negative confidence", output: encode(t, negativeConfidence)},
		{name: "over one confidence", output: encode(t, overConfidence)},
		{name: "missing reason", output: encode(t, missingReason)},
		{name: "missing evidence", output: encode(t, missingEvidence)},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := newIsolatedKanbanBoardApp(t)
			for _, id := range ids {
				if _, ok, err := app.memory.appendTranscript(id, "", "Settled candidate "+id+" about obsolete logistics."); err != nil || !ok {
					t.Fatalf("append %s: ok=%v err=%v", id, ok, err)
				}
				backdateMemoryEntry(app.memory, id, 10*24*time.Hour)
			}
			agent := slopClassifierAgent()
			err := app.runSlopClassifierOnce(agent, context.Background(), "test-key", func(context.Context, string, openAITextRequest) (string, error) {
				return test.output, nil
			}, len(ids))
			if err == nil {
				t.Fatal("incomplete verdict set unexpectedly succeeded")
			}
			if cursor := app.newestSlopCursor(); cursor != "" {
				t.Fatalf("incomplete verdict set advanced cursor=%q", cursor)
			}
			for _, id := range ids {
				entry, found := app.memory.entryByID(id)
				if !found || strings.TrimSpace(entry.Metadata["classifierVerdict"]) != "" {
					t.Fatalf("invalid output mutated candidate %s: found=%v metadata=%v", id, found, entry.Metadata)
				}
			}
			held, ok, heldErr := app.ambientHeldWindow(agent.name)
			if heldErr != nil || !ok || held.WindowID != agent.name+":provider-window" {
				t.Fatalf("held checkpoint=%+v ok=%v err=%v", held, ok, heldErr)
			}
		})
	}
}

func TestSlopVerdictPersistenceFailureKeepsHeldWindowAndCursor(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	ids := []string{"slop-write-a", "slop-write-b", "slop-write-c", "slop-write-d", "slop-write-e", "slop-write-f", "slop-write-g", "slop-write-h"}
	for _, id := range ids {
		if _, ok, err := app.memory.appendTranscript(id, "", "Settled candidate "+id+" about obsolete logistics."); err != nil || !ok {
			t.Fatalf("append %s: ok=%v err=%v", id, ok, err)
		}
		backdateMemoryEntry(app.memory, id, 10*24*time.Hour)
	}
	agent := slopClassifierAgent()
	originalPath := app.memory.path
	app.memory.path = t.TempDir() // rewrite target is a directory; verdict persistence must fail
	defer func() { app.memory.path = originalPath }()
	err := app.runSlopClassifierOnce(agent, context.Background(), "test-key", func(context.Context, string, openAITextRequest) (string, error) {
		return slopKeepVerdictsJSON(t, ids), nil
	}, len(ids))
	if err == nil {
		t.Fatal("verdict persistence failure unexpectedly succeeded")
	}
	if cursor := app.newestSlopCursor(); cursor != "" {
		t.Fatalf("verdict persistence failure advanced cursor=%q", cursor)
	}
	held, ok, heldErr := app.ambientHeldWindow(agent.name)
	if heldErr != nil || !ok || held.WindowID != agent.name+":provider-window" {
		t.Fatalf("held checkpoint=%+v ok=%v err=%v", held, ok, heldErr)
	}
}

func TestSlopClassifierProviderCircuitBoundsTickerRetries(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	for i := 0; i < 8; i++ {
		id := "t-provider-" + string(rune('a'+i))
		if _, ok, err := app.memory.appendTranscript(id, "", "Settled provider-outage candidate "+string(rune('a'+i))+" about a historical note."); err != nil || !ok {
			t.Fatalf("append %s: ok=%v err=%v", id, ok, err)
		}
		backdateMemoryEntry(app.memory, id, 10*24*time.Hour)
	}
	calls := 0
	responder := func(context.Context, string, openAITextRequest) (string, error) {
		calls++
		return "", &openAIProviderFailure{err: errors.New("provider unavailable")}
	}
	agent := slopClassifierAgent()
	for attempt := 0; attempt < ambientProviderMaxWindowAttempts+2; attempt++ {
		_ = app.runSlopClassifierOnce(agent, context.Background(), "test-key", responder, 8)
		app.mu.Lock()
		if failure := app.agentFailures[agent.name]; failure != nil && !failure.providerOpen {
			failure.backoffUntil = time.Now().Add(-time.Second)
		}
		app.mu.Unlock()
	}
	if calls != ambientProviderMaxWindowAttempts {
		t.Fatalf("provider calls=%d, want bounded at %d", calls, ambientProviderMaxWindowAttempts)
	}
	app.mu.Lock()
	failure := app.agentFailures[agent.name]
	app.mu.Unlock()
	if failure == nil || !failure.providerOpen {
		t.Fatalf("provider circuit=%+v, want open", failure)
	}
}

func TestSlopHeldWindowSurvivesSameProcessAndProcessRestart(t *testing.T) {
	t.Setenv("SLOP_CLASSIFIER_INTERVAL", "1h")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))

	agent := slopClassifierAgent()
	app := newKanbanBoardApp()
	if _, appended, err := app.memory.appendTranscript("slop-base", "", "Baseline transcript before the held classification window."); err != nil || !appended {
		t.Fatalf("append baseline: appended=%v err=%v", appended, err)
	}
	app.setAmbientAgentBaselineID(agent.name, "slop-base")
	if _, err := app.ensureAmbientScopeCheckpoint(agent, officeRoomID, "slop-base"); err != nil {
		t.Fatalf("seed pre-existing Slop scope checkpoint: %v", err)
	}
	app.startSlopClassifierWorker("test-key")
	heldIDs := make([]string, 0, 8)
	for index := 0; index < 8; index++ {
		id := fmt.Sprintf("slop-held-%d", index)
		heldIDs = append(heldIDs, id)
		if _, appended, err := app.memory.appendTranscript(id, "", fmt.Sprintf("Settled held transcript %d about obsolete office logistics.", index)); err != nil || !appended {
			t.Fatalf("append %s: appended=%v err=%v", id, appended, err)
		}
	}
	app.memory.mu.Lock()
	for index := range app.memory.entries {
		for _, id := range heldIDs {
			if app.memory.entries[index].ID == id {
				app.memory.entries[index].CreatedAt = time.Now().UTC().Add(-10 * 24 * time.Hour)
			}
		}
	}
	if err := app.memory.rewriteLocked(true); err != nil {
		app.memory.mu.Unlock()
		t.Fatalf("persist backdated held inputs: %v", err)
	}
	app.memory.mu.Unlock()

	providerWindow := agent.name + ":provider-window"
	app.recordAmbientAgentHoldFailure(agent, providerWindow, officeRoomID)
	held, ok, err := app.ambientHeldWindow(agent.name)
	if err != nil || !ok || held.BaselineID != "slop-base" || held.WindowID != providerWindow {
		t.Fatalf("slop held checkpoint=%+v ok=%v err=%v", held, ok, err)
	}
	app.startSlopClassifierWorker("test-key")
	if baseline := app.ambientAgentBaselineID(agent.name); baseline != "slop-base" {
		t.Fatalf("same-process Slop baseline=%q, want slop-base", baseline)
	}
	if candidates, _ := app.buildSlopCandidates(agent, time.Now().UTC()); len(candidates) != len(heldIDs) {
		t.Fatalf("same-process Slop candidates=%d, want %d", len(candidates), len(heldIDs))
	}
	if err := app.Close(); err != nil {
		t.Fatalf("close first process: %v", err)
	}

	restarted := newKanbanBoardApp()
	restarted.startSlopClassifierWorker("test-key")
	if baseline := restarted.ambientAgentBaselineID(agent.name); baseline != "slop-base" {
		t.Fatalf("process-restart Slop baseline=%q, want slop-base", baseline)
	}
	var seenInput string
	calls := 0
	if err := restarted.runSlopClassifierOnce(agent, context.Background(), "test-key", func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		seenInput = request.Input
		return slopKeepVerdictsJSON(t, heldIDs), nil
	}, len(heldIDs)); err != nil {
		t.Fatalf("process-restart Slop retry: %v", err)
	}
	if calls != 1 {
		t.Fatalf("Slop retry calls=%d, want 1", calls)
	}
	for _, id := range heldIDs {
		if !strings.Contains(seenInput, id) {
			t.Fatalf("Slop retry input omitted held id %s: %s", id, seenInput)
		}
	}
	if cursor := restarted.newestSlopCursor(); cursor != heldIDs[len(heldIDs)-1] {
		t.Fatalf("Slop cursor=%q, want %q", cursor, heldIDs[len(heldIDs)-1])
	}
	if _, ok, err := restarted.ambientHeldWindow(agent.name); err != nil || ok {
		t.Fatalf("Slop held checkpoint remained after success: ok=%v err=%v", ok, err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatalf("close restarted process: %v", err)
	}
}

// TestRunSlopClassifierQuarantinesFromVerdict drives the whole pass and asserts
// a high-confidence quarantine verdict moves the entry out of recall.
func TestRunSlopClassifierQuarantinesFromVerdict(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	for i := 0; i < 8; i++ {
		id := "t-q-" + string(rune('a'+i))
		if _, ok, err := app.memory.appendTranscript(id, "", "Settled remark "+string(rune('a'+i))+" about the offsite logistics and parking."); err != nil || !ok {
			t.Fatalf("append %s: ok=%v err=%v", id, ok, err)
		}
		backdateMemoryEntry(app.memory, id, 10*24*time.Hour)
	}

	responder := func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		verdicts := make([]slopVerdict, 0, 8)
		for i := 0; i < 8; i++ {
			id := "t-q-" + string(rune('a'+i))
			verdicts = append(verdicts, slopVerdict{
				EntryID:    id,
				Verdict:    "keep",
				Confidence: 0.99,
				Reason:     "retain",
				Evidence:   "still potentially useful",
			})
		}
		verdicts[0] = slopVerdict{
			EntryID:    "t-q-a",
			Verdict:    "quarantine",
			Confidence: 0.92,
			Reason:     "orphaned logistics chatter",
			Evidence:   "never attached to a package",
		}
		raw, err := json.Marshal(verdicts)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw), nil
	}
	if err := app.runSlopClassifierOnce(slopClassifierAgent(), context.Background(), "test-key", responder, 8); err != nil {
		t.Fatalf("pass: %v", err)
	}

	entry, found := app.memory.entryByID("t-q-a")
	if !found || memoryEntryRelevance(entry) != relevanceQuarantined {
		t.Fatalf("t-q-a should be quarantined, got found=%v relevance=%q", found, memoryEntryRelevance(entry))
	}
	if searchContainsID(app.memory.search("logistics parking", 10), "t-q-a") {
		t.Fatal("quarantined entry must leave recall")
	}
	// it appears in the quarantine tray list.
	found = false
	for _, payload := range app.quarantineListPayloads() {
		if payload["id"] == "t-q-a" {
			found = true
			if payload["reason"] == "" {
				t.Fatal("tray payload must carry the classifier reason")
			}
		}
	}
	if !found {
		t.Fatal("quarantined entry must appear in the tray list")
	}
}

// --- endpoints: permission matrix + auth guards ---

func setupQuarantineEndpointTest(t *testing.T) *kanbanBoardApp {
	t.Helper()
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previous := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previous })
	return app
}

func seedQuarantined(t *testing.T, app *kanbanBoardApp, id string) {
	t.Helper()
	if _, ok, err := app.memory.appendTranscript(id, "", "Quarantined chatter for endpoint tests, "+id+"."); err != nil || !ok {
		t.Fatalf("append %s: ok=%v err=%v", id, ok, err)
	}
	future := time.Now().UTC().Add(30 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindTranscript, id, "Quarantined chatter for endpoint tests, "+id+".", map[string]string{
		relevanceMetadataKey: relevanceQuarantined,
		"expiresAt":          future,
		"classifierReason":   "orphaned",
	}); err != nil {
		t.Fatalf("quarantine %s: %v", id, err)
	}
}

func TestQuarantineListRequiresAuth(t *testing.T) {
	setupQuarantineEndpointTest(t)
	req := httptest.NewRequest(http.MethodGet, "/assistant/quarantine", nil)
	rec := httptest.NewRecorder()
	assistantQuarantineHandler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", rec.Code)
	}
}

func TestQuarantineListReturnsEntries(t *testing.T) {
	app := setupQuarantineEndpointTest(t)
	seedQuarantined(t, app, "t-list-1")

	req := httptest.NewRequest(http.MethodGet, "/assistant/quarantine", nil)
	for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	assistantQuarantineHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var payload struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Entries) != 1 || payload.Entries[0]["id"] != "t-list-1" {
		t.Fatalf("entries=%v, want the seeded quarantined entry", payload.Entries)
	}
}

func TestQuarantineRestoreAllowsAnyUser(t *testing.T) {
	app := setupQuarantineEndpointTest(t)
	seedQuarantined(t, app, "t-restore")

	req := httptest.NewRequest(http.MethodPost, "/assistant/quarantine/t-restore/restore", nil)
	for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") { // non-admin
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	assistantQuarantineActionHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200 (any user may restore)", rec.Code, rec.Body.String())
	}
	entry, _ := app.memory.entryByID("t-restore")
	if memoryEntryRelevance(entry) != relevanceActive {
		t.Fatalf("restored entry relevance=%q, want active", memoryEntryRelevance(entry))
	}
	if strings.TrimSpace(entry.Metadata["reviewedBy"]) == "" {
		t.Fatal("restore must stamp the human reviewer")
	}
}

func TestQuarantineDeleteIsAdminOnly(t *testing.T) {
	app := setupQuarantineEndpointTest(t)
	seedQuarantined(t, app, "t-del-nonadmin")
	seedQuarantined(t, app, "t-del-admin")

	// non-admin is rejected.
	req := httptest.NewRequest(http.MethodPost, "/assistant/quarantine/t-del-nonadmin/delete", nil)
	for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	assistantQuarantineActionHandler(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin delete status=%d, want 403", rec.Code)
	}
	if _, found := app.memory.entryByID("t-del-nonadmin"); !found {
		t.Fatal("non-admin delete must not remove the entry")
	}

	// admin succeeds.
	req = httptest.NewRequest(http.MethodPost, "/assistant/quarantine/t-del-admin/delete", nil)
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	rec = httptest.NewRecorder()
	assistantQuarantineActionHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin delete status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if _, found := app.memory.entryByID("t-del-admin"); found {
		t.Fatal("admin delete must hard-remove the entry")
	}
}
