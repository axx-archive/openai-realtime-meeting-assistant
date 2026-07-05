package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// Signals are the compounding brain's raw input: append-only, zero model
// calls, and NEVER recall material — kind "signal" must ride isUIStateMemoryKind
// so a stored reject reason can't surface as Scout "knowledge".
func TestRecordSignalPersistsAndStaysOutOfRecall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	entry, err := recordSignal(store, "AJ", signalEventProposalRejected, signalValenceNegative, "artifact-1", "package-1", map[string]string{
		"reason": "comps table cites invented precedent titles",
		"empty":  "   ",
	})
	if err != nil {
		t.Fatalf("recordSignal: %v", err)
	}
	if entry.Kind != meetingMemoryKindSignal {
		t.Fatalf("kind=%q, want %q", entry.Kind, meetingMemoryKindSignal)
	}
	if entry.Metadata["event"] != signalEventProposalRejected || entry.Metadata["valence"] != signalValenceNegative {
		t.Fatalf("metadata=%v, want event/valence mirrored for cheap filtering", entry.Metadata)
	}
	if entry.Metadata["artifactId"] != "artifact-1" || entry.Metadata["packageId"] != "package-1" {
		t.Fatalf("metadata=%v, want artifactId/packageId mirrored", entry.Metadata)
	}

	record, ok := decodeSignalEntry(entry)
	if !ok {
		t.Fatalf("decodeSignalEntry failed for text %q", entry.Text)
	}
	if record.Actor != "AJ" || record.Event != signalEventProposalRejected || record.Valence != signalValenceNegative {
		t.Fatalf("record=%#v, want actor/event/valence round-tripped", record)
	}
	if record.Payload["reason"] != "comps table cites invented precedent titles" {
		t.Fatalf("payload=%v, want the human reason verbatim", record.Payload)
	}
	if _, exists := record.Payload["empty"]; exists {
		t.Fatalf("payload=%v, want blank values dropped", record.Payload)
	}

	// The UI-state registration is the recall firewall.
	if !isUIStateMemoryKind(meetingMemoryKindSignal) {
		t.Fatal("kind signal must be UI state so raw signals never pollute recall")
	}
	if matches := store.search("invented precedent titles", 5); len(matches) != 0 {
		t.Fatalf("search matches=%d, want 0 — signals must never be recall candidates", len(matches))
	}
	if entries := store.contextEntriesForQuery("invented precedent titles", 5, entry.CreatedAt); len(entries) != 0 {
		t.Fatalf("context entries=%d, want 0 — signals must never enter model context", len(entries))
	}
	// The snapshot lane is the OTHER leak path: store.snapshot feeds
	// memorySnapshotForClients (broadcast to every signed-in browser) and the
	// snapshot-fed worker prompts (buildAgentThreadFollowUpInput). A signal
	// quoting a private thread must never ride it.
	for _, visible := range store.snapshot(0) {
		if visible.Kind == meetingMemoryKindSignal {
			t.Fatalf("signal entry %q leaked into store.snapshot — it would broadcast to all clients and enter worker prompts", visible.ID)
		}
	}
	for _, visible := range visibleMeetingMemoryEntries(store.snapshot(0), 0) {
		if visible.Kind == meetingMemoryKindSignal {
			t.Fatalf("signal entry %q leaked into visibleMeetingMemoryEntries", visible.ID)
		}
	}

	// Append-only durability: a reload sees the signal.
	reloaded, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("reload memory store: %v", err)
	}
	if persisted := reloaded.entriesOfKind(meetingMemoryKindSignal, 0); len(persisted) != 1 {
		t.Fatalf("persisted signals=%d, want 1", len(persisted))
	}
}

func TestRecordSignalRequiresEvent(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	if _, err := recordSignal(store, "AJ", "   ", "", "", "", nil); err == nil {
		t.Fatal("recordSignal with a blank event must error")
	}
	if _, err := recordSignal(nil, "AJ", signalEventArtifactOpened, "", "", "", nil); err == nil {
		t.Fatal("recordSignal with a nil store must error")
	}
}

func TestSummarizeArtifactDiffReportsSectionsAndDelta(t *testing.T) {
	prior := "Intro line.\n\n## Thesis\nOriginal thesis body.\n\n## Comps\nRow one.\nRow two.\n\n## Risks\nRisk body."
	next := "Intro line.\n\n## Thesis\nSharper thesis body.\n\n## Comps\nRow one.\nRow two.\n\n## Distribution\nNew channel plan."

	summary := summarizeArtifactDiff(prior, next)
	if summary["changedSections"] != "Thesis" {
		t.Fatalf("changedSections=%q, want Thesis", summary["changedSections"])
	}
	if summary["addedSections"] != "Distribution" {
		t.Fatalf("addedSections=%q, want Distribution", summary["addedSections"])
	}
	if summary["removedSections"] != "Risks" {
		t.Fatalf("removedSections=%q, want Risks", summary["removedSections"])
	}
	wantDelta := fmt.Sprintf("%+d", len(next)-len(prior))
	if summary["charsDelta"] != wantDelta {
		t.Fatalf("charsDelta=%q, want %q", summary["charsDelta"], wantDelta)
	}
	// The summary must never smuggle either full body into the store.
	for key, value := range summary {
		if strings.Contains(value, "Original thesis body") || strings.Contains(value, "Sharper thesis body") {
			t.Fatalf("summary[%q]=%q leaks a full section body", key, value)
		}
	}
}

func TestSummarizeArtifactDiffHandlesHeadingless(t *testing.T) {
	summary := summarizeArtifactDiff("plain prose body", "plain prose body plus a new closing thought")
	if summary["addedSections"] != "" || summary["removedSections"] != "" {
		t.Fatalf("summary=%v, want no added/removed sections for headingless bodies", summary)
	}
	if summary["changedSections"] != signalDiffIntroHeading {
		t.Fatalf("changedSections=%q, want %q (the preamble pseudo-section)", summary["changedSections"], signalDiffIntroHeading)
	}
	if !strings.HasPrefix(summary["charsDelta"], "+") {
		t.Fatalf("charsDelta=%q, want a signed positive delta", summary["charsDelta"])
	}

	if identical := summarizeArtifactDiff("same", "same"); identical["charsDelta"] != "+0" || len(identical) != 1 {
		t.Fatalf("identical bodies summary=%v, want only charsDelta=+0", identical)
	}
}

// The PATCH /artifacts seam: a human edit emits exactly one artifact_edited
// signal carrying the section diff — mirrors
// TestArtifactsHandlerUpdatesSavedArtifactForSignedInUser.
func TestArtifactsPatchEmitsEditSignal(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	artifact, _, err := kanbanApp.createOSArtifact("research", "demand validation", "Research brief\n\n## Plan\n1. Interview operators.\n\n## Risks\nThin panel.", "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/artifacts", strings.NewReader(fmt.Sprintf(`{"id":%q,"text":"Research brief\n\n## Plan\n1. Interview operators.\n2. Validate pricing."}`, artifact.ID)))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	artifactsHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}

	signals := kanbanApp.memory.entriesOfKind(meetingMemoryKindSignal, 0)
	if len(signals) != 1 {
		t.Fatalf("signals=%d, want exactly 1 artifact_edited", len(signals))
	}
	record, ok := decodeSignalEntry(signals[0])
	if !ok {
		t.Fatalf("decodeSignalEntry failed for %q", signals[0].Text)
	}
	if record.Event != signalEventArtifactEdited || record.ArtifactID != artifact.ID {
		t.Fatalf("record=%#v, want artifact_edited for %q", record, artifact.ID)
	}
	if record.Actor != "AJ" {
		t.Fatalf("actor=%q, want AJ", record.Actor)
	}
	if record.Payload["changedSections"] != "Plan" || record.Payload["removedSections"] != "Risks" {
		t.Fatalf("payload=%v, want changed=Plan removed=Risks", record.Payload)
	}
	if strings.Contains(signals[0].Text, "Interview operators") {
		t.Fatalf("signal text %q stores body content — the diff summary must not carry full bodies", signals[0].Text)
	}
	for _, entry := range kanbanApp.memorySnapshotForClients(0) {
		if entry.Kind == meetingMemoryKindSignal {
			t.Fatalf("signal entry %q leaked into memorySnapshotForClients", entry.ID)
		}
	}

	// A pure re-save with identical text must NOT emit a second signal.
	req = httptest.NewRequest(http.MethodPatch, "/artifacts", strings.NewReader(fmt.Sprintf(`{"id":%q,"text":"Research brief\n\n## Plan\n1. Interview operators.\n2. Validate pricing."}`, artifact.ID)))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	artifactsHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("no-op status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	if signals := kanbanApp.memory.entriesOfKind(meetingMemoryKindSignal, 0); len(signals) != 1 {
		t.Fatalf("signals after no-op re-save=%d, want still 1", len(signals))
	}
}

// publishOSArtifact is the programmatic publish seam (Scout tools): first
// publish emits artifact_published/positive; re-publish stays silent.
func TestPublishOSArtifactEmitsPublishedSignalOnce(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	artifact, _, err := app.createOSArtifact("research", "publish me", "Research brief\n\nEvidence.", "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}

	if _, changed, err := app.publishOSArtifact(artifact.ID, true, "AJ"); err != nil || !changed {
		t.Fatalf("publishOSArtifact changed=%v err=%v, want true/nil", changed, err)
	}
	signals := app.memory.entriesOfKind(meetingMemoryKindSignal, 0)
	if len(signals) != 1 {
		t.Fatalf("signals=%d, want 1 artifact_published", len(signals))
	}
	record, ok := decodeSignalEntry(signals[0])
	if !ok || record.Event != signalEventArtifactPublished || record.Valence != signalValencePositive {
		t.Fatalf("record=%#v ok=%v, want positive artifact_published", record, ok)
	}

	// Publishing an already-published artifact must not double-count the vote.
	if _, _, err := app.publishOSArtifact(artifact.ID, true, "AJ"); err != nil {
		t.Fatalf("re-publish: %v", err)
	}
	if signals := app.memory.entriesOfKind(meetingMemoryKindSignal, 0); len(signals) != 1 {
		t.Fatalf("signals after re-publish=%d, want still 1", len(signals))
	}
}

// The openedAt stamp rides the metadata-only path: a body written between the
// open handler's artifact read and its stamp (a completing thread run, a user
// PATCH) must survive — updateOSArtifactWithMetadata with the stale text
// snapshot would silently revert it.
func TestUpdateOSArtifactMetadataPreservesConcurrentBodyUpdate(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	artifact, _, err := app.createOSArtifact("research", "stamp me", "Original body.", "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}
	// A concurrent writer lands a newer body AFTER a caller snapshotted the
	// artifact (the open handler's read).
	if _, _, err := app.memory.updateOSArtifact(artifact.ID, "", "Newer body from the completing run.", "Scout"); err != nil {
		t.Fatalf("concurrent body update: %v", err)
	}

	stamped, changed, err := app.memory.updateOSArtifactMetadata(artifact.ID, map[string]string{"openedAt": "2026-07-05T00:00:00Z"})
	if err != nil || !changed {
		t.Fatalf("updateOSArtifactMetadata changed=%v err=%v, want true/nil", changed, err)
	}
	if stamped.Text != "Newer body from the completing run." {
		t.Fatalf("text=%q — the metadata-only stamp reverted the concurrent body update", stamped.Text)
	}
	if stamped.Metadata["openedAt"] != "2026-07-05T00:00:00Z" {
		t.Fatalf("metadata=%v, want openedAt stamped", stamped.Metadata)
	}

	// Re-stamping the same value is a no-op (no rewrite, no changed).
	if _, changed, err := app.memory.updateOSArtifactMetadata(artifact.ID, map[string]string{"openedAt": "2026-07-05T00:00:00Z"}); err != nil || changed {
		t.Fatalf("idempotent re-stamp changed=%v err=%v, want false/nil", changed, err)
	}
}

// POST /artifacts/open: session-gated, records artifact_opened and stamps
// openedAt exactly once — the datum is open vs never-opened (first open wins);
// repeat clicks are zero-information volume and must not append signals.
func TestArtifactOpenRouteRecordsSignalAndStampsOpenedAt(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	artifact, _, err := kanbanApp.createOSArtifact("research", "open me", "Research brief\n\nEvidence.", "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}

	// Session gate: no cookie -> 401, same contract as artifactsHandler.
	req := httptest.NewRequest(http.MethodPost, "/artifacts/open", strings.NewReader(fmt.Sprintf(`{"id":%q}`, artifact.ID)))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	artifactOpenHandler(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous open status=%d, want 401", recorder.Code)
	}

	cookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")

	// Unknown artifact -> 404.
	req = httptest.NewRequest(http.MethodPost, "/artifacts/open", strings.NewReader(`{"id":"missing-artifact"}`))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	artifactOpenHandler(recorder, req)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing artifact status=%d body=%s, want 404", recorder.Code, recorder.Body.String())
	}

	// First open: signal + openedAt stamp.
	req = httptest.NewRequest(http.MethodPost, "/artifacts/open", strings.NewReader(fmt.Sprintf(`{"id":%q}`, artifact.ID)))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	artifactOpenHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("open status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	opened, found := kanbanApp.osArtifactByID(artifact.ID)
	if !found || strings.TrimSpace(opened.Metadata["openedAt"]) == "" {
		t.Fatalf("metadata=%v, want openedAt stamped on first open", opened.Metadata)
	}
	firstOpenedAt := opened.Metadata["openedAt"]

	signals := kanbanApp.memory.entriesOfKind(meetingMemoryKindSignal, 0)
	if len(signals) != 1 {
		t.Fatalf("signals=%d, want 1 artifact_opened", len(signals))
	}
	record, ok := decodeSignalEntry(signals[0])
	if !ok || record.Event != signalEventArtifactOpened || record.ArtifactID != artifact.ID {
		t.Fatalf("record=%#v ok=%v, want artifact_opened for %q", record, ok, artifact.ID)
	}

	// Second open: no new signal (first open only), openedAt unchanged.
	req = httptest.NewRequest(http.MethodPost, "/artifacts/open", strings.NewReader(fmt.Sprintf(`{"id":%q}`, artifact.ID)))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	artifactOpenHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("second open status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	if reopened, _ := kanbanApp.osArtifactByID(artifact.ID); reopened.Metadata["openedAt"] != firstOpenedAt {
		t.Fatalf("openedAt=%q, want the first-open stamp %q preserved", reopened.Metadata["openedAt"], firstOpenedAt)
	}
	if signals := kanbanApp.memory.entriesOfKind(meetingMemoryKindSignal, 0); len(signals) != 1 {
		t.Fatalf("signals after second open=%d, want still 1 — repeat opens must not flood the store", len(signals))
	}
}
