package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// --- POST /signals/survey ------------------------------------------------------

// postSurveySignal drives the survey route the way the browser chips do.
func postSurveySignal(t *testing.T, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/signals/survey", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	signalSurveyHandler(recorder, req)
	return recorder
}

// storedSurveyRecords filters the survey chips out of the signal stream (the
// implicit seams — attach, open, edit — share the kind).
func storedSurveyRecords(t *testing.T, store *meetingMemoryStore) []signalRecord {
	t.Helper()
	surveys := []signalRecord{}
	for _, entry := range store.entriesOfKind(meetingMemoryKindSignal, 0) {
		record, ok := decodeSignalEntry(entry)
		if !ok {
			t.Fatalf("decodeSignalEntry failed for %q", entry.Text)
		}
		if isSurveySignalEvent(record.Event) {
			surveys = append(surveys, record)
		}
	}
	return surveys
}

// The survey taste rules (§5 "Surveys: garnish, not a surface") are pure
// store reads — zero model calls, so the whole survey system runs KEYLESS —
// and they enforce: max 1 stored survey per user per UTC day; never two
// surveys for the same package+stage combination (any user, dedupe winning
// over the rate limit so a re-tap stays an idempotent 200); suppression once
// implicit signal volume already answers the question (rule zero).
func TestEvaluateSurveyTasteRules(t *testing.T) {
	// Surveys are capture, never generation: no model keys, ever.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")

	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	now := time.Now().UTC()

	if outcome := evaluateSurveyTasteRules(store, "AJ", "artifact-1", "package-1", "research", now); outcome != surveyOutcomeStore {
		t.Fatalf("empty-store outcome=%v, want surveyOutcomeStore", outcome)
	}

	// AJ spends today's budget on package-1 at stage research.
	if _, err := recordSignal(store, "AJ", signalEventSurveyLanded, signalValencePositive, "artifact-1", "package-1", map[string]string{"stage": "research"}); err != nil {
		t.Fatalf("seed survey: %v", err)
	}

	// Rate limit: AJ is done for the day, even on a different package.
	if outcome := evaluateSurveyTasteRules(store, "AJ", "artifact-2", "package-2", "thesis", now); outcome != surveyOutcomeRateLimited {
		t.Fatalf("same-day outcome=%v, want surveyOutcomeRateLimited", outcome)
	}
	// The cap is per user: Tim still has budget.
	if outcome := evaluateSurveyTasteRules(store, "Tim", "artifact-2", "package-2", "thesis", now); outcome != surveyOutcomeStore {
		t.Fatalf("other-user outcome=%v, want surveyOutcomeStore", outcome)
	}
	// Dedupe: package-1+research was answered — for ANYONE, and it beats the
	// rate limit so AJ's re-tap reads as idempotent, not as an error.
	if outcome := evaluateSurveyTasteRules(store, "AJ", "artifact-1", "package-1", "research", now); outcome != surveyOutcomeDuplicate {
		t.Fatalf("re-tap outcome=%v, want surveyOutcomeDuplicate", outcome)
	}
	if outcome := evaluateSurveyTasteRules(store, "Tim", "artifact-1", "package-1", "research", now); outcome != surveyOutcomeDuplicate {
		t.Fatalf("cross-user duplicate outcome=%v, want surveyOutcomeDuplicate", outcome)
	}

	// The daily budget resets at UTC midnight...
	tomorrow := now.Add(24 * time.Hour)
	if outcome := evaluateSurveyTasteRules(store, "AJ", "artifact-2", "package-2", "thesis", tomorrow); outcome != surveyOutcomeStore {
		t.Fatalf("next-day outcome=%v, want surveyOutcomeStore", outcome)
	}
	// ...but package+stage dedupe never expires; only a stage ADVANCE reopens
	// the question (genuinely new work to react to).
	if outcome := evaluateSurveyTasteRules(store, "AJ", "artifact-1", "package-1", "research", tomorrow); outcome != surveyOutcomeDuplicate {
		t.Fatalf("next-day same-stage outcome=%v, want surveyOutcomeDuplicate", outcome)
	}
	if outcome := evaluateSurveyTasteRules(store, "AJ", "artifact-1", "package-1", "design", tomorrow); outcome != surveyOutcomeStore {
		t.Fatalf("advanced-stage outcome=%v, want surveyOutcomeStore", outcome)
	}

	// Suppression (rule zero): below the implicit-volume threshold the ask is
	// still worth it; at the threshold it is redundant.
	implicitEvents := []string{signalEventArtifactOpened, signalEventArtifactEdited, signalEventArtifactRerun}
	for i := 0; i < signalSurveyImplicitVolumeThreshold-1; i++ {
		if _, err := recordSignal(store, "AJ", implicitEvents[i], signalValenceNeutral, "artifact-3", "", nil); err != nil {
			t.Fatalf("seed implicit signal %d: %v", i, err)
		}
	}
	if outcome := evaluateSurveyTasteRules(store, "Tim", "artifact-3", "package-3", "thesis", now); outcome != surveyOutcomeStore {
		t.Fatalf("below-threshold outcome=%v, want surveyOutcomeStore", outcome)
	}
	if _, err := recordSignal(store, "AJ", implicitEvents[signalSurveyImplicitVolumeThreshold-1], signalValenceNeutral, "artifact-3", "", nil); err != nil {
		t.Fatalf("seed final implicit signal: %v", err)
	}
	if outcome := evaluateSurveyTasteRules(store, "Tim", "artifact-3", "package-3", "thesis", now); outcome != surveyOutcomeSuppressed {
		t.Fatalf("at-threshold outcome=%v, want surveyOutcomeSuppressed", outcome)
	}
}

// POST /signals/survey: session-gated like its /artifacts neighbors; verdict
// chips map to survey_landed(+)/survey_off(-); the note is truncated to one
// line; and the artifact's toolTemplate/packageId/stage ride along as free
// context. Then the taste rules bite: the same package+stage is idempotent
// for everyone, and a user's second survey of the day is a 429.
func TestSignalSurveyRouteRecordsSurveyWithContext(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	// Capture stays free: the route must work with no model keys at all.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")

	pkg, err := kanbanApp.createVenturePackage("Aurora", "an IP thesis", "AJ")
	if err != nil {
		t.Fatalf("createVenturePackage: %v", err)
	}
	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("research", "deck draft", "Deck body.", "AJ", map[string]string{"toolTemplate": "one_pager"})
	if err != nil {
		t.Fatalf("createOSArtifactWithMetadata: %v", err)
	}
	// Stamps packageId onto the artifact (and emits one implicit
	// artifact_attached signal — below the suppression threshold).
	if _, err := kanbanApp.attachToPackage(pkg.ID, "artifact", artifact.ID, "AJ"); err != nil {
		t.Fatalf("attachToPackage: %v", err)
	}

	// Session gate: no cookie -> 401, same contract as artifactOpenHandler.
	if recorder := postSurveySignal(t, fmt.Sprintf(`{"artifactId":%q,"verdict":"landed"}`, artifact.ID), nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous survey status=%d, want 401", recorder.Code)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")

	// Verdict is a two-chip enum, nothing else.
	if recorder := postSurveySignal(t, fmt.Sprintf(`{"artifactId":%q,"verdict":"meh"}`, artifact.ID), cookies); recorder.Code != http.StatusBadRequest {
		t.Fatalf("bad verdict status=%d, want 400", recorder.Code)
	}
	// Unknown artifact -> 404.
	if recorder := postSurveySignal(t, `{"artifactId":"missing-artifact","verdict":"landed"}`, cookies); recorder.Code != http.StatusNotFound {
		t.Fatalf("missing artifact status=%d, want 404", recorder.Code)
	}
	if surveys := storedSurveyRecords(t, kanbanApp.memory); len(surveys) != 0 {
		t.Fatalf("surveys after rejected requests=%d, want 0", len(surveys))
	}

	// "off" + an over-long note: stored with the note cut to one line.
	longNote := strings.Repeat("n", signalSurveyNoteLimit+80)
	recorder := postSurveySignal(t, fmt.Sprintf(`{"artifactId":%q,"verdict":"off","note":%q}`, artifact.ID, longNote), cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("survey status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	response := decodeJSON(t, recorder)
	if response["stored"] != true || response["suppressed"] != false {
		t.Fatalf("response=%v, want stored=true suppressed=false", response)
	}

	surveys := storedSurveyRecords(t, kanbanApp.memory)
	if len(surveys) != 1 {
		t.Fatalf("surveys=%d, want 1", len(surveys))
	}
	record := surveys[0]
	if record.Event != signalEventSurveyOff || record.Valence != signalValenceNegative || record.Actor != "AJ" {
		t.Fatalf("record=%#v, want a negative survey_off by AJ", record)
	}
	if record.ArtifactID != artifact.ID || record.PackageID != pkg.ID {
		t.Fatalf("record=%#v, want artifact/package ids carried", record)
	}
	if len(record.Payload["note"]) == 0 || len(record.Payload["note"]) > signalSurveyNoteLimit {
		t.Fatalf("note length=%d, want 1..%d — the note must be truncated", len(record.Payload["note"]), signalSurveyNoteLimit)
	}
	if record.Payload["toolTemplate"] != "one_pager" || record.Payload["stage"] != "thesis" {
		t.Fatalf("payload=%v, want toolTemplate/stage context pulled from the artifact's package", record.Payload)
	}
	if _, flagged := record.Payload["suppressed"]; flagged {
		t.Fatalf("payload=%v, want no suppressed flag on a normal store", record.Payload)
	}

	// Same package+stage again — by ANOTHER user — is idempotent: 200, not
	// stored, no second vote.
	timCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	recorder = postSurveySignal(t, fmt.Sprintf(`{"artifactId":%q,"verdict":"landed"}`, artifact.ID), timCookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("duplicate survey status=%d body=%s, want idempotent 200", recorder.Code, recorder.Body.String())
	}
	if response := decodeJSON(t, recorder); response["stored"] != false {
		t.Fatalf("duplicate response=%v, want stored=false", response)
	}
	if surveys := storedSurveyRecords(t, kanbanApp.memory); len(surveys) != 1 {
		t.Fatalf("surveys after duplicate=%d, want still 1", len(surveys))
	}

	// Rate limit: AJ already stored a survey today — a fresh artifact (no
	// package, so no dedupe) is a 429.
	second, _, err := kanbanApp.createOSArtifact("research", "second draft", "Another body.", "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}
	if recorder := postSurveySignal(t, fmt.Sprintf(`{"artifactId":%q,"verdict":"landed"}`, second.ID), cookies); recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("second survey of the day status=%d body=%s, want 429", recorder.Code, recorder.Body.String())
	}
	if surveys := storedSurveyRecords(t, kanbanApp.memory); len(surveys) != 1 {
		t.Fatalf("surveys after rate-limited request=%d, want still 1", len(surveys))
	}
}

// Rule zero over the wire: once implicit volume on an artifact is high, the
// survey answer comes back 200 but is stored flagged suppressed=true — the
// analyst can calibrate the chips, but the answer never counts as fresh taste.
func TestSignalSurveyRouteSuppressesWhenImplicitVolumeIsHigh(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	artifact, _, err := kanbanApp.createOSArtifact("research", "busy artifact", "Body.", "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}
	implicitEvents := []string{signalEventArtifactOpened, signalEventArtifactEdited, signalEventArtifactPublished}
	for i := 0; i < signalSurveyImplicitVolumeThreshold; i++ {
		if _, err := recordSignal(kanbanApp.memory, "AJ", implicitEvents[i%len(implicitEvents)], signalValenceNeutral, artifact.ID, "", nil); err != nil {
			t.Fatalf("seed implicit signal %d: %v", i, err)
		}
	}

	recorder := postSurveySignal(t, fmt.Sprintf(`{"artifactId":%q,"verdict":"landed"}`, artifact.ID), loginAs(t, "tim@shareability.com", "B0NFIRE!"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("suppressed survey status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	if response := decodeJSON(t, recorder); response["stored"] != true || response["suppressed"] != true {
		t.Fatalf("response=%v, want stored=true suppressed=true", response)
	}

	surveys := storedSurveyRecords(t, kanbanApp.memory)
	if len(surveys) != 1 {
		t.Fatalf("surveys=%d, want 1 suppressed survey stored for calibration", len(surveys))
	}
	if surveys[0].Payload["suppressed"] != "true" {
		t.Fatalf("payload=%v, want suppressed=true so the analyst can discount it", surveys[0].Payload)
	}
	if surveys[0].Event != signalEventSurveyLanded || surveys[0].Valence != signalValencePositive {
		t.Fatalf("record=%#v, want a positive survey_landed", surveys[0])
	}
}
