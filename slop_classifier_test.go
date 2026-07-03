package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
			got := app.applySlopVerdict(entry, slopVerdict{EntryID: "t-verdict", Verdict: tc.verdict, Confidence: tc.confidence, Reason: "r", Evidence: "e"})
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

func TestSweepExpiredQuarantineDeletesAndLeavesStub(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	if _, ok, err := app.memory.appendTranscript("t-expire", "", "Redundant chatter destined to expire from memory."); err != nil || !ok {
		t.Fatalf("append: ok=%v err=%v", ok, err)
	}
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindTranscript, "t-expire", "Redundant chatter destined to expire from memory.", map[string]string{
		relevanceMetadataKey: relevanceQuarantined,
		"expiresAt":          past,
		"classifierReason":   "orphaned chatter",
	}); err != nil {
		t.Fatalf("quarantine: %v", err)
	}

	app.sweepExpiredQuarantine("cursor-42")

	if _, found := app.memory.entryByID("t-expire"); found {
		t.Fatal("expired entry must be hard-deleted")
	}
	stubs := app.memory.entriesOfKind(meetingMemoryKindSlopPass, 0)
	foundStub := false
	for _, stub := range stubs {
		if stub.Metadata["deletedId"] == "t-expire" {
			foundStub = true
			if stub.Metadata["reason"] == "" {
				t.Fatal("audit stub must record the deletion reason")
			}
			if stub.Metadata[slopClassifierCursorKey] != "cursor-42" {
				t.Fatalf("audit stub must carry the forward cursor, got %q", stub.Metadata[slopClassifierCursorKey])
			}
		}
	}
	if !foundStub {
		t.Fatal("a slop_pass audit stub must survive the hard delete")
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
	responder := func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		calls++
		return "[]", nil // valid empty pass: keeps everything, advances the cursor.
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
		return `[{"entry_id":"t-q-a","verdict":"quarantine","confidence":0.92,"reason":"orphaned logistics chatter","evidence":"never attached to a package"}]`, nil
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
