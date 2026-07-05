package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tasteTestApp(t *testing.T) *kanbanBoardApp {
	t.Helper()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))
	return newKanbanBoardApp()
}

func recordTasteTestSignal(t *testing.T, app *kanbanBoardApp, actor string, event string, payload map[string]string) meetingMemoryEntry {
	t.Helper()
	entry, err := recordSignal(app.memory, actor, event, signalValenceNegative, "artifact-1", "", payload)
	if err != nil {
		t.Fatalf("record %s signal for %s: %v", event, actor, err)
	}
	return entry
}

// tasteTestResponse builds the strict-JSON analyst answer: a profile citing
// every id in citedIDs, plus optional proposals.
func tasteTestResponse(t *testing.T, citedIDs []string, proposals []tasteLedgerProposal) string {
	t.Helper()
	var bullets []string
	for _, id := range citedIDs {
		bullets = append(bullets, "- Trims intro throat-clearing ("+id+")")
	}
	body := "## Voice & style\n" + strings.Join(bullets, "\n") + "\n\n## Do\n- No clear pattern yet."
	payload := map[string]any{"profile": body}
	if proposals != nil {
		payload["ledgerProposals"] = proposals
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode taste response: %v", err)
	}
	return string(encoded)
}

// tasteWindowIDsFromInput pulls the signal ids the input's window section
// offered, so a fake responder can cite exactly what it was shown.
func tasteWindowIDsFromInput(input string) []string {
	_, window, found := strings.Cut(input, "# Unconsumed signal window")
	if !found {
		return nil
	}
	ids := []string{}
	for _, line := range strings.Split(window, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if strings.HasPrefix(line, "signal-") {
			ids = append(ids, strings.TrimSpace(strings.SplitN(line, " ", 2)[0]))
		}
	}
	return ids
}

// The gate: zero signals never runs; a full batch always runs; below the
// batch only a week-stale profile (or, profile-less, a week-old oldest
// signal) runs — the "min-batch OR weekly" contract.
func TestTasteAnalystShouldRunGating(t *testing.T) {
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour)
	stale := now.Add(-8 * 24 * time.Hour)

	cases := []struct {
		name           string
		unconsumed     int
		distilledAt    time.Time
		oldestSignalAt time.Time
		want           bool
	}{
		{"zero signals never runs even stale", 0, stale, time.Time{}, false},
		{"full batch runs", 15, fresh, fresh, true},
		{"below batch with fresh profile waits", 14, fresh, fresh, false},
		{"below batch with week-stale profile runs", 1, stale, fresh, true},
		{"no profile, fresh oldest signal waits", 3, time.Time{}, fresh, false},
		{"no profile, week-old oldest signal runs", 3, time.Time{}, stale, true},
	}
	for _, testCase := range cases {
		if got := tasteAnalystShouldRun(testCase.unconsumed, 15, testCase.distilledAt, testCase.oldestSignalAt, now); got != testCase.want {
			t.Fatalf("%s: shouldRun=%v, want %v", testCase.name, got, testCase.want)
		}
	}
}

func TestTasteAnalystSkipsBelowMinBatch(t *testing.T) {
	app := tasteTestApp(t)
	t.Setenv("TASTE_ANALYST_MIN_SIGNALS", "3")

	recordTasteTestSignal(t, app, "AJ", signalEventArtifactEdited, map[string]string{"removedSections": "Intro"})
	recordTasteTestSignal(t, app, "AJ", signalEventSurveyOff, map[string]string{"note": "too breathless"})

	if err := app.runTasteAnalystOnce(context.Background(), "test-key", func(context.Context, string, anthropicTextRequest) (string, error) {
		t.Fatal("responder should not run below the min signal batch")
		return "", nil
	}); err != nil {
		t.Fatalf("runTasteAnalystOnce: %v", err)
	}
	if _, found := app.tasteProfileForUser("AJ"); found {
		t.Fatal("profile written below the min batch, want none")
	}
}

func TestTasteAnalystWritesEvidenceCitedProfileAndStampsSignals(t *testing.T) {
	app := tasteTestApp(t)
	t.Setenv("TASTE_ANALYST_MIN_SIGNALS", "2")

	first := recordTasteTestSignal(t, app, "AJ", signalEventArtifactEdited, map[string]string{"removedSections": "Intro"})
	second := recordTasteTestSignal(t, app, "AJ", signalEventSurveyOff, map[string]string{"note": "too breathless"})

	err := app.runTasteAnalystOnce(context.Background(), "test-key", func(_ context.Context, _ string, request anthropicTextRequest) (string, error) {
		if request.Model != "claude-sonnet-5" {
			t.Fatalf("model=%q, want claude-sonnet-5", request.Model)
		}
		if request.Effort != "medium" {
			t.Fatalf("effort=%q, want medium", request.Effort)
		}
		if !strings.Contains(request.Input, first.ID) || !strings.Contains(request.Input, second.ID) {
			t.Fatalf("input missing the signal window: %s", request.Input)
		}
		return tasteTestResponse(t, []string{first.ID, second.ID}, []tasteLedgerProposal{{
			Kind:     "candidate",
			Text:     "Never open a deck with throat-clearing.",
			Evidence: []string{first.ID},
		}}), nil
	})
	if err != nil {
		t.Fatalf("runTasteAnalystOnce: %v", err)
	}

	profile, found := app.tasteProfileForUser("AJ")
	if !found {
		t.Fatal("no taste profile written for AJ")
	}
	if profile.Metadata["title"] != "Taste profile — AJ" {
		t.Fatalf("title=%q, want Taste profile — AJ", profile.Metadata["title"])
	}
	if profile.Metadata["mode"] != "workflow" {
		t.Fatalf("mode=%q, want workflow", profile.Metadata["mode"])
	}
	if profile.Metadata[tasteProfileArtifactTypeKey] != tasteProfileArtifactType {
		t.Fatalf("artifactType=%q, want %s", profile.Metadata[tasteProfileArtifactTypeKey], tasteProfileArtifactType)
	}
	if !strings.Contains(profile.Text, first.ID) {
		t.Fatalf("profile body is not evidence-cited: %q", profile.Text)
	}
	if profile.Metadata[tasteAnalystCursorKey] != second.ID {
		t.Fatalf("cursor=%q, want %s", profile.Metadata[tasteAnalystCursorKey], second.ID)
	}

	// Ledger candidates land as recorded proposals on the profile — never as
	// direct ledger writes.
	var proposals []tasteLedgerProposal
	if err := json.Unmarshal([]byte(profile.Metadata[tasteProfileProposalsKey]), &proposals); err != nil {
		t.Fatalf("decode ledgerProposals: %v", err)
	}
	if len(proposals) != 1 || proposals[0].Status != tasteProposalStatusProposed || proposals[0].Kind != "candidate" {
		t.Fatalf("proposals=%v, want one proposed candidate", proposals)
	}
	if decisions := app.memory.entriesOfKind(meetingMemoryKindDecision, 0); len(decisions) != 0 {
		t.Fatalf("decisions=%d, want 0 — the analyst must never write the ledger", len(decisions))
	}

	// Consumed signals are stamped distilledInto=<profile> for compaction.
	for _, id := range []string{first.ID, second.ID} {
		stamped, ok := app.memory.entryByID(id)
		if !ok {
			t.Fatalf("signal %s missing after distillation", id)
		}
		if stamped.Metadata[signalDistilledIntoKey] != profile.ID {
			t.Fatalf("signal %s distilledInto=%q, want %s", id, stamped.Metadata[signalDistilledIntoKey], profile.ID)
		}
	}
}

func TestTasteAnalystUpdatesLivingProfileInPlace(t *testing.T) {
	app := tasteTestApp(t)
	t.Setenv("TASTE_ANALYST_MIN_SIGNALS", "1")

	first := recordTasteTestSignal(t, app, "AJ", signalEventArtifactEdited, nil)
	responder := func(_ context.Context, _ string, request anthropicTextRequest) (string, error) {
		return tasteTestResponse(t, tasteWindowIDsFromInput(request.Input), nil), nil
	}
	if err := app.runTasteAnalystOnce(context.Background(), "test-key", responder); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	original, found := app.tasteProfileForUser("AJ")
	if !found {
		t.Fatal("no profile after first pass")
	}

	second := recordTasteTestSignal(t, app, "AJ", signalEventSurveyOff, map[string]string{"note": "kill the buzzwords"})
	if err := app.runTasteAnalystOnce(context.Background(), "test-key", func(_ context.Context, _ string, request anthropicTextRequest) (string, error) {
		if strings.Contains(request.Input, first.ID+" |") {
			t.Fatalf("second pass re-consumed the distilled signal %s: %s", first.ID, request.Input)
		}
		if !strings.Contains(request.Input, "living document") || !strings.Contains(request.Input, original.Text[:20]) {
			t.Fatalf("second pass input missing the current profile body: %s", request.Input)
		}
		return tasteTestResponse(t, []string{second.ID}, nil), nil
	}); err != nil {
		t.Fatalf("second pass: %v", err)
	}

	updated, found := app.tasteProfileForUser("AJ")
	if !found {
		t.Fatal("no profile after second pass")
	}
	if updated.ID != original.ID {
		t.Fatalf("profile re-minted: id %s -> %s, want update in place", original.ID, updated.ID)
	}
	if !strings.Contains(updated.Text, second.ID) {
		t.Fatalf("updated profile not citing the new signal: %q", updated.Text)
	}
	if updated.Metadata[tasteAnalystCursorKey] != second.ID {
		t.Fatalf("cursor=%q, want %s", updated.Metadata[tasteAnalystCursorKey], second.ID)
	}

	// Exactly ONE living profile artifact for the user — never duplicates.
	profileCount := 0
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0) {
		if entry.Metadata[tasteProfileArtifactTypeKey] == tasteProfileArtifactType && entry.Metadata[tasteProfileUserKey] == "AJ" {
			profileCount++
		}
	}
	if profileCount != 1 {
		t.Fatalf("profiles for AJ=%d, want exactly 1", profileCount)
	}
}

func TestTasteAnalystWeeklyPassRunsBelowMinBatch(t *testing.T) {
	app := tasteTestApp(t)
	t.Setenv("TASTE_ANALYST_MIN_SIGNALS", "1")

	seed := recordTasteTestSignal(t, app, "AJ", signalEventArtifactEdited, nil)
	if err := app.runTasteAnalystOnce(context.Background(), "test-key", func(context.Context, string, anthropicTextRequest) (string, error) {
		return tasteTestResponse(t, []string{seed.ID}, nil), nil
	}); err != nil {
		t.Fatalf("seed pass: %v", err)
	}
	profile, found := app.tasteProfileForUser("AJ")
	if !found {
		t.Fatal("no profile after seed pass")
	}

	// Back at the default min batch (15), one waiting signal is below the bar —
	// but a week-stale profile still triggers the weekly pass.
	t.Setenv("TASTE_ANALYST_MIN_SIGNALS", "15")
	late := recordTasteTestSignal(t, app, "AJ", signalEventSurveyOff, nil)

	if err := app.runTasteAnalystOnce(context.Background(), "test-key", func(context.Context, string, anthropicTextRequest) (string, error) {
		t.Fatal("responder should not run: below batch and the profile is fresh")
		return "", nil
	}); err != nil {
		t.Fatalf("fresh-profile pass: %v", err)
	}

	staleStamp := time.Now().UTC().Add(-8 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, _, err := app.memory.updateOSArtifactMetadata(profile.ID, map[string]string{tasteProfileDistilledAtKey: staleStamp}); err != nil {
		t.Fatalf("backdate distilledAt: %v", err)
	}
	if err := app.runTasteAnalystOnce(context.Background(), "test-key", func(context.Context, string, anthropicTextRequest) (string, error) {
		return tasteTestResponse(t, []string{late.ID}, nil), nil
	}); err != nil {
		t.Fatalf("weekly pass: %v", err)
	}
	updated, _ := app.tasteProfileForUser("AJ")
	if updated.Metadata[tasteAnalystCursorKey] != late.ID {
		t.Fatalf("weekly pass did not consume the waiting signal: cursor=%q, want %s", updated.Metadata[tasteAnalystCursorKey], late.ID)
	}
}

// Per-user isolation: user A's signals never reach user B's window or
// profile, and each user's consumed signals stamp into their own profile.
func TestTasteAnalystPerUserIsolation(t *testing.T) {
	app := tasteTestApp(t)
	t.Setenv("TASTE_ANALYST_MIN_SIGNALS", "1")

	ajSignal := recordTasteTestSignal(t, app, "AJ", signalEventArtifactEdited, nil)
	tylerSignal := recordTasteTestSignal(t, app, "Tyler", signalEventSurveyOff, nil)
	// an actorless signal reaches nobody.
	if _, err := recordSignal(app.memory, "", signalEventArtifactOpened, signalValenceNeutral, "artifact-2", "", nil); err != nil {
		t.Fatalf("record actorless signal: %v", err)
	}

	if err := app.runTasteAnalystOnce(context.Background(), "test-key", func(_ context.Context, _ string, request anthropicTextRequest) (string, error) {
		ids := tasteWindowIDsFromInput(request.Input)
		if strings.Contains(request.Input, "# Teammate\nAJ") {
			if len(ids) != 1 || ids[0] != ajSignal.ID {
				t.Fatalf("AJ window=%v, want only %s", ids, ajSignal.ID)
			}
		}
		if strings.Contains(request.Input, "# Teammate\nTyler") {
			if len(ids) != 1 || ids[0] != tylerSignal.ID {
				t.Fatalf("Tyler window=%v, want only %s", ids, tylerSignal.ID)
			}
		}
		return tasteTestResponse(t, ids, nil), nil
	}); err != nil {
		t.Fatalf("runTasteAnalystOnce: %v", err)
	}

	ajProfile, foundAJ := app.tasteProfileForUser("AJ")
	tylerProfile, foundTyler := app.tasteProfileForUser("Tyler")
	if !foundAJ || !foundTyler {
		t.Fatalf("profiles: AJ=%v Tyler=%v, want both", foundAJ, foundTyler)
	}
	if strings.Contains(ajProfile.Text, tylerSignal.ID) || strings.Contains(tylerProfile.Text, ajSignal.ID) {
		t.Fatal("cross-user signal ids leaked between profiles")
	}
	stampedAJ, _ := app.memory.entryByID(ajSignal.ID)
	stampedTyler, _ := app.memory.entryByID(tylerSignal.ID)
	if stampedAJ.Metadata[signalDistilledIntoKey] != ajProfile.ID {
		t.Fatalf("AJ signal distilledInto=%q, want %s", stampedAJ.Metadata[signalDistilledIntoKey], ajProfile.ID)
	}
	if stampedTyler.Metadata[signalDistilledIntoKey] != tylerProfile.ID {
		t.Fatalf("Tyler signal distilledInto=%q, want %s", stampedTyler.Metadata[signalDistilledIntoKey], tylerProfile.ID)
	}
}

// Keyless: no ANTHROPIC_API_KEY means the worker silently never starts (the
// goal-engine posture) — including through the registration seam.
func TestTasteAnalystKeylessNoOp(t *testing.T) {
	app := tasteTestApp(t)
	t.Setenv("ANTHROPIC_API_KEY", "")

	app.startTasteAnalystWorker("")
	app.ensureTasteAnalystStarted()
	app.startAmbientAgent(meetingBrainAgent(), "") // keyless generic path too

	app.mu.Lock()
	_, registered := app.agentCancels[tasteAnalystAgentName]
	app.mu.Unlock()
	if registered {
		t.Fatal("taste analyst registered keyless, want silent no-op")
	}
}

func TestAmbientAgentRegistrationStartsTasteAnalyst(t *testing.T) {
	app := tasteTestApp(t)
	t.Setenv("ANTHROPIC_API_KEY", "test-anthropic-key")

	// The brain worker registering (JoinConferenceRoom's path) brings the
	// analyst up alongside on its own key.
	app.startAmbientAgent(meetingBrainAgent(), "test-openai-key")
	defer app.Close()

	app.mu.Lock()
	_, registered := app.agentCancels[tasteAnalystAgentName]
	app.mu.Unlock()
	if !registered {
		t.Fatal("taste analyst not registered alongside the brain worker")
	}
}

// Unparseable or uncited output must never advance the cursor: the next pass
// retries the same window (decision-ledger precedent).
func TestTasteAnalystSkipsWithoutAdvancingOnBadOutput(t *testing.T) {
	app := tasteTestApp(t)
	t.Setenv("TASTE_ANALYST_MIN_SIGNALS", "1")

	signal := recordTasteTestSignal(t, app, "AJ", signalEventArtifactEdited, nil)

	if err := app.runTasteAnalystOnce(context.Background(), "test-key", func(context.Context, string, anthropicTextRequest) (string, error) {
		return "Here is my analysis of AJ's taste in prose.", nil
	}); err != nil {
		t.Fatalf("non-JSON pass: %v", err)
	}
	if _, found := app.tasteProfileForUser("AJ"); found {
		t.Fatal("profile written from non-JSON output")
	}

	if err := app.runTasteAnalystOnce(context.Background(), "test-key", func(context.Context, string, anthropicTextRequest) (string, error) {
		return `{"profile": "## Voice & style\n- Claims with no receipts."}`, nil
	}); err != nil {
		t.Fatalf("uncited pass: %v", err)
	}
	if _, found := app.tasteProfileForUser("AJ"); found {
		t.Fatal("profile written without evidence citations")
	}

	// The window is intact: a good pass still consumes the same signal.
	if err := app.runTasteAnalystOnce(context.Background(), "test-key", func(context.Context, string, anthropicTextRequest) (string, error) {
		return tasteTestResponse(t, []string{signal.ID}, nil), nil
	}); err != nil {
		t.Fatalf("good pass: %v", err)
	}
	profile, found := app.tasteProfileForUser("AJ")
	if !found || profile.Metadata[tasteAnalystCursorKey] != signal.ID {
		t.Fatalf("good pass did not consume the retried window (found=%v)", found)
	}
}

// A proposal citing evidence outside the window — or superseding a decision
// that does not exist — is invented and never recorded.
func TestTasteAnalystDropsInventedProposals(t *testing.T) {
	app := tasteTestApp(t)
	t.Setenv("TASTE_ANALYST_MIN_SIGNALS", "1")

	signal := recordTasteTestSignal(t, app, "AJ", signalEventArtifactEdited, nil)
	if err := app.runTasteAnalystOnce(context.Background(), "test-key", func(context.Context, string, anthropicTextRequest) (string, error) {
		return tasteTestResponse(t, []string{signal.ID}, []tasteLedgerProposal{
			{Kind: "candidate", Text: "Invented rule.", Evidence: []string{"signal-made-up-1"}},
			{Kind: "supersession", Text: "Supersede a ghost.", Supersedes: "decision-does-not-exist", Evidence: []string{signal.ID}},
			{Kind: "candidate", Text: "Real rule.", Evidence: []string{signal.ID}},
		}), nil
	}); err != nil {
		t.Fatalf("runTasteAnalystOnce: %v", err)
	}

	profile, found := app.tasteProfileForUser("AJ")
	if !found {
		t.Fatal("no profile written")
	}
	var proposals []tasteLedgerProposal
	if err := json.Unmarshal([]byte(profile.Metadata[tasteProfileProposalsKey]), &proposals); err != nil {
		t.Fatalf("decode ledgerProposals: %v", err)
	}
	if len(proposals) != 1 || proposals[0].Text != "Real rule." {
		t.Fatalf("proposals=%v, want only the evidence-backed candidate", proposals)
	}
}

// --- compaction (the slop_classifier.go seam) ---------------------------------

func TestSignalCompactionEligibility(t *testing.T) {
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour).Format(time.RFC3339Nano)
	aged := now.Add(-31 * 24 * time.Hour).Format(time.RFC3339Nano)

	undistilled := meetingMemoryEntry{Kind: meetingMemoryKindSignal, CreatedAt: now.Add(-90 * 24 * time.Hour), Metadata: map[string]string{"actor": "AJ"}}
	if slopCandidateEligible(undistilled, now) {
		t.Fatal("undistilled roster-actor signal must never be compaction-eligible — it is waiting for the analyst")
	}
	// System/external signals (actor resolves to no roster user) have no
	// analyst to consume them: they age out on the same 30-day reprieve.
	externalFresh := meetingMemoryEntry{Kind: meetingMemoryKindSignal, CreatedAt: now.Add(-time.Hour), Metadata: map[string]string{"actor": "external"}}
	if slopCandidateEligible(externalFresh, now) {
		t.Fatal("fresh external signal must wait out the 30-day reprieve")
	}
	externalAged := meetingMemoryEntry{Kind: meetingMemoryKindSignal, CreatedAt: now.Add(-31 * 24 * time.Hour), Metadata: map[string]string{"actor": "external"}}
	if !slopCandidateEligible(externalAged, now) {
		t.Fatal("aged external signal must be compaction-eligible — no analyst will ever stamp it")
	}
	runnerAged := meetingMemoryEntry{Kind: meetingMemoryKindSignal, CreatedAt: now.Add(-31 * 24 * time.Hour), Metadata: map[string]string{"actor": "render_runner"}}
	if !slopCandidateEligible(runnerAged, now) {
		t.Fatal("aged render_runner signal must be compaction-eligible")
	}
	freshlyDistilled := meetingMemoryEntry{Kind: meetingMemoryKindSignal, CreatedAt: now, Metadata: map[string]string{signalDistilledIntoKey: "profile-1", signalDistilledAtKey: fresh}}
	if slopCandidateEligible(freshlyDistilled, now) {
		t.Fatal("freshly distilled signal must wait out the 30-day reprieve")
	}
	agedDistilled := meetingMemoryEntry{Kind: meetingMemoryKindSignal, CreatedAt: now, Metadata: map[string]string{signalDistilledIntoKey: "profile-1", signalDistilledAtKey: aged}}
	if !slopCandidateEligible(agedDistilled, now) {
		t.Fatal("distilled signal past the reprieve must be compaction-eligible")
	}
}

func TestSweepDistilledSignalsCompactsAfterReprieve(t *testing.T) {
	app := tasteTestApp(t)

	aged := recordTasteTestSignal(t, app, "AJ", signalEventArtifactEdited, nil)
	fresh := recordTasteTestSignal(t, app, "AJ", signalEventSurveyOff, nil)
	undistilled := recordTasteTestSignal(t, app, "AJ", signalEventArtifactOpened, nil)

	now := time.Now().UTC()
	if err := app.memory.stampSignalsDistilled([]string{aged.ID, fresh.ID}, "os-artifact-workflow-profile", now); err != nil {
		t.Fatalf("stamp distilled: %v", err)
	}
	agedStamp := now.Add(-31 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindSignal, aged.ID, aged.Text, map[string]string{signalDistilledAtKey: agedStamp}); err != nil {
		t.Fatalf("backdate distilledAt: %v", err)
	}

	app.sweepDistilledSignals("")

	if _, found := app.memory.entryByID(aged.ID); found {
		t.Fatal("aged distilled signal survived compaction")
	}
	if _, found := app.memory.entryByID(fresh.ID); !found {
		t.Fatal("freshly distilled signal compacted before its 30-day reprieve")
	}
	if _, found := app.memory.entryByID(undistilled.ID); !found {
		t.Fatal("undistilled signal compacted — only distilled signals are eligible")
	}

	// ONE audit stub records the fact of compaction (never a silent delete).
	stub := meetingMemoryEntry{}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindSlopPass, 0) {
		if entry.Metadata["compactedCount"] != "" {
			stub = entry
		}
	}
	if stub.ID == "" {
		t.Fatal("no compaction audit stub written")
	}
	if stub.Metadata["compactedCount"] != "1" || stub.Metadata["deletedKind"] != meetingMemoryKindSignal {
		t.Fatalf("audit stub metadata=%v, want compactedCount=1 deletedKind=signal", stub.Metadata)
	}
	if !strings.Contains(stub.Metadata["distilledInto"], "os-artifact-workflow-profile") {
		t.Fatalf("audit stub distilledInto=%q, want the profile id", stub.Metadata["distilledInto"])
	}
}
