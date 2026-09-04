package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

const sparseTestRoom = "room-sparse-coverage"

func sparseRecoveryTestApp(t *testing.T) *kanbanBoardApp {
	t.Helper()
	app := newIsolatedKanbanBoardApp(t)
	appendDigestTestBrain(t, app, "brain-private-history", "meeting-old", "HISTORICAL PRIVATE SECRET", map[string]string{"roomId": sparseTestRoom, "visibility": "room_only", "sittingId": "meeting-old"})
	appendDigestTestBrain(t, app, "brain-org-history", "meeting-old", "Organization packaging overview", map[string]string{"roomId": sparseTestRoom, "visibility": "organization", "processedThroughCaptureSequence": "1913"})
	store, err := newMeetingMemoryStore(app.memory.path)
	if err != nil {
		t.Fatal(err)
	}
	app.memory = store
	app.memory.mu.Lock()
	app.memory.meetingIDs[sparseTestRoom] = "meeting-current"
	app.memory.mu.Unlock()
	if _, err := app.ensureAmbientScopeCheckpoint(meetingDigestAgent(), sparseTestRoom, "", ambientContinuityAmbiguous); err != nil {
		t.Fatal(err)
	}
	return app
}
func sparseStateForTest(t *testing.T, app *kanbanBoardApp) meetingDigestSparseState {
	t.Helper()
	s, active, err := loadMeetingDigestSparseState(app.meetingDigestSparsePath(sparseTestRoom), sparseTestRoom)
	if err != nil || !active {
		t.Fatalf("state: active=%v err=%v", active, err)
	}
	return s
}
func sparseSourceStatus(s meetingDigestSparseState, id string) string {
	for i := len(s.Sources) - 1; i >= 0; i-- {
		if s.Sources[i].ID == id {
			return s.Sources[i].Status
		}
	}
	return ""
}
func reviseSparseTestBrain(t *testing.T, app *kanbanBoardApp, id string, change func(*meetingMemoryEntry)) {
	t.Helper()
	app.memory.mu.Lock()
	defer app.memory.mu.Unlock()
	for i := range app.memory.entries {
		if app.memory.entries[i].ID == id {
			change(&app.memory.entries[i])
			if err := app.memory.rewriteLocked(true); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("source not found")
}
func TestMeetingDigestSparseMixedSourcesAndRestart(t *testing.T) {
	app := sparseRecoveryTestApp(t)
	app.meetings.mu.Lock()
	app.meetings.records = append(app.meetings.records, meetingRecord{ID: "meeting-old", RoomID: sparseTestRoom, Title: "PRIVATE DIRECTORY TITLE", StartedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), EndedAt: time.Now().UTC().Format(time.RFC3339)})
	app.meetings.mu.Unlock()
	calls := 0
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		if strings.Contains(request.Input, "HISTORICAL PRIVATE SECRET") || strings.Contains(request.Input, "PRIVATE DIRECTORY TITLE") {
			t.Fatal("private source reached provider")
		}
		return cannedMeetingDigestJSON(), nil
	}
	entry, err := app.invokeAmbientAgentGuarded(meetingDigestAgent(), context.Background(), "fake", responder, 1, sparseTestRoom)
	if err != nil || entry.ID == "" {
		t.Fatalf("run: %+v %v", entry, err)
	}
	if entry.Metadata["meetingId"] != "meeting-old" || entry.Metadata[digestCoverageMetadataKey] != "partial" || entry.Metadata[meetingDigestCaptureMetadataKey] != "" || entry.Metadata[meetingDigestCursorMetadataKey] != "" {
		t.Fatalf("misleading metadata: %+v", entry.Metadata)
	}
	state := sparseStateForTest(t, app)
	if sparseSourceStatus(state, "brain-private-history") != "pending_authority" || sparseSourceStatus(state, "brain-org-history") != "consumed" {
		t.Fatalf("state: %+v", state)
	}
	if len(app.memory.latestDigestPerMeeting()) != 0 {
		t.Fatal("sparse child promoted into cumulative rollup")
	}
	if got := app.memory.unconsumedEntriesAfterFiltered(meetingMemoryKindMeetingDigest, meetingMemoryKindDayDigestPass, dayDigestCursorMetadataKey, 10, "", "", sharedRoomRecallPrincipal(sparseTestRoom, "meeting-current"), nil); len(got) != 0 {
		t.Fatal("sparse child admitted into day fold")
	}
	principal := sharedRoomRecallPrincipal(sparseTestRoom, "meeting-current")
	if got := app.recallStoreForPrincipal(context.Background(), principal).snapshot(0); !sparseContains(got, entry.ID) {
		t.Fatal("authorized recovered digest unavailable to recall")
	}
	raw, _ := json.Marshal(ambientWorkerCheckpointDiagnostics(app, meetingDigestAgent()))
	if strings.Contains(string(raw), "brain-private-history") || !strings.Contains(string(raw), "partial_source_coverage") {
		t.Fatalf("unsafe/missing diagnostics %s", raw)
	}
	// App restart resolves from the exact consumed receipt, never a scalar ID.
	reloaded, err := newMeetingMemoryStore(app.memory.path)
	if err != nil {
		t.Fatal(err)
	}
	app.memory = reloaded
	app.memory.mu.Lock()
	app.memory.meetingIDs[sparseTestRoom] = "meeting-current"
	app.memory.mu.Unlock()
	if base, reason, err := app.bootstrapAmbientContinuity(meetingDigestAgent(), sparseTestRoom); base != "" || reason != "" || err != nil {
		t.Fatalf("restart baseline=%q reason=%q err=%v", base, reason, err)
	}
	if _, err := app.invokeAmbientAgentGuarded(meetingDigestAgent(), context.Background(), "fake", responder, 1, sparseTestRoom); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("replayed consumed source: %d calls", calls)
	}
	// Current room-only material is its own audience lane and remains room-only.
	if _, started := app.meetings.startMeeting(sparseTestRoom, "meeting-current", time.Now().UTC(), []string{"member"}); !started {
		t.Fatal("start current sitting")
	}
	appendDigestTestBrain(t, app, "brain-current-private", "meeting-current", "Current private room overview", map[string]string{"roomId": sparseTestRoom, "visibility": "room_only", "sittingId": "meeting-current"})
	newer, err := app.invokeAmbientAgentGuarded(meetingDigestAgent(), context.Background(), "fake", responder, 1, sparseTestRoom)
	if err != nil || newer.ID == "" || newer.Metadata["visibility"] != "room_only" || digestEntryKey(newer) == digestEntryKey(entry) {
		t.Fatalf("current lane: %+v %v", newer, err)
	}
	if sparseSourceStatus(sparseStateForTest(t, app), "brain-private-history") != "pending_authority" {
		t.Fatal("historical private source silently consumed")
	}
}
func sparseContains(entries []meetingMemoryEntry, id string) bool {
	for _, e := range entries {
		if e.ID == id {
			return true
		}
	}
	return false
}

func TestMeetingDigestSparseCorrectionAndACLRechecked(t *testing.T) {
	for _, kind := range []string{"body", "acl"} {
		t.Run(kind, func(t *testing.T) {
			app := sparseRecoveryTestApp(t)
			responder := func(context.Context, string, openAITextRequest) (string, error) {
				reviseSparseTestBrain(t, app, "brain-org-history", func(e *meetingMemoryEntry) {
					if kind == "body" {
						e.Text = "Corrected organization overview"
					} else {
						e.Metadata["visibility"] = "private"
					}
				})
				return cannedMeetingDigestJSON(), nil
			}
			entry, err := app.invokeAmbientAgentGuarded(meetingDigestAgent(), context.Background(), "fake", responder, 1, sparseTestRoom)
			if err == nil || entry.ID != "" {
				t.Fatalf("stale source committed: %+v %v", entry, err)
			}
			if len(app.memory.entriesOfKind(meetingMemoryKindMeetingDigest, 0)) != 0 {
				t.Fatal("stale digest persisted")
			}
			good := func(context.Context, string, openAITextRequest) (string, error) {
				return cannedMeetingDigestJSON(), nil
			}
			entry, err = app.invokeAmbientAgentGuarded(meetingDigestAgent(), context.Background(), "fake", good, 1, sparseTestRoom)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "acl" {
				if entry.ID != "" || sparseSourceStatus(sparseStateForTest(t, app), "brain-org-history") != "pending_ambiguous_audience" {
					t.Fatal("private source not withheld")
				}
				return
			}
			if entry.ID == "" {
				t.Fatal("corrected source did not progress")
			}
			reviseSparseTestBrain(t, app, "brain-org-history", func(e *meetingMemoryEntry) { e.Metadata["visibility"] = "private" })
			if sparseContains(app.recallStoreForPrincipal(context.Background(), sharedRoomRecallPrincipal(sparseTestRoom, "meeting-current")).snapshot(0), entry.ID) {
				t.Fatal("revoked child remained in recall")
			}
			if sparseContains(app.memory.snapshotForMeeting("meeting-old", 0), entry.ID) {
				t.Fatal("revoked child remained in meeting projection")
			}
		})
	}
}
func TestMeetingDigestSparseOutputReceiptRecoversTerminalCheckpointFailure(t *testing.T) {
	app := sparseRecoveryTestApp(t)
	original := persistMeetingDigestSparseState
	defer func() { persistMeetingDigestSparseState = original }()
	calls := 0
	persistMeetingDigestSparseState = func(path string, state meetingDigestSparseState) error {
		if sparseSourceStatus(state, "brain-org-history") == "consumed" {
			return errors.New("injected terminal checkpoint failure")
		}
		return original(path, state)
	}
	responder := func(context.Context, string, openAITextRequest) (string, error) {
		calls++
		return cannedMeetingDigestJSON(), nil
	}
	entry, err := app.invokeAmbientAgentGuarded(meetingDigestAgent(), context.Background(), "fake", responder, 1, sparseTestRoom)
	if err == nil || entry.ID == "" {
		t.Fatalf("expected persisted output and terminal error %+v %v", entry, err)
	}
	persistMeetingDigestSparseState = original
	reloaded, err := newMeetingMemoryStore(app.memory.path)
	if err != nil {
		t.Fatal(err)
	}
	app.memory = reloaded
	app.memory.mu.Lock()
	app.memory.meetingIDs[sparseTestRoom] = "meeting-current"
	app.memory.mu.Unlock()
	if _, err := app.invokeAmbientAgentGuarded(meetingDigestAgent(), context.Background(), "fake", responder, 1, sparseTestRoom); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || sparseSourceStatus(sparseStateForTest(t, app), "brain-org-history") != "consumed" {
		t.Fatal("crash recovery repeated provider")
	}
}
func TestMeetingDigestSparsePersistenceFailureSpendsNothing(t *testing.T) {
	app := sparseRecoveryTestApp(t)
	original := persistMeetingDigestSparseState
	defer func() { persistMeetingDigestSparseState = original }()
	persistMeetingDigestSparseState = func(string, meetingDigestSparseState) error { return errors.New("injected persistence failure") }
	calls := 0
	_, err := app.invokeAmbientAgentGuarded(meetingDigestAgent(), context.Background(), "fake", func(context.Context, string, openAITextRequest) (string, error) {
		calls++
		return cannedMeetingDigestJSON(), nil
	}, 1, sparseTestRoom)
	if err == nil || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	if _, err := os.Stat(app.meetingDigestSparsePath(sparseTestRoom)); !os.IsNotExist(err) {
		t.Fatalf("ledger unexpectedly created %v", err)
	}
}
func TestMeetingDigestSparseNeverSupersedesHeldOrExistingHiddenOutput(t *testing.T) {
	for _, mode := range []string{"held", "baseline", "hidden_output"} {
		t.Run(mode, func(t *testing.T) {
			app := sparseRecoveryTestApp(t)
			if mode == "held" {
				if err := app.persistAmbientHeldWindow(meetingDigestAgent(), "brain-private-history", sparseTestRoom); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "baseline" {
				if err := app.persistAmbientCheckpointBaseline(meetingDigestAgent(), "brain-private-history", sparseTestRoom); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "hidden_output" {
				if _, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, "meeting-old", cannedMeetingDigestJSON(), map[string]string{"roomId": sparseTestRoom, "meetingId": "meeting-old", relevanceMetadataKey: relevanceQuarantined}); err != nil {
					t.Fatal(err)
				}
			}
			before, _, _ := app.ambientScopeCheckpoint(ambientAgentScopeKey(meetingDigestAgent(), sparseTestRoom))
			_, handled, err := app.runMeetingDigestSparseRecovery(meetingDigestAgent(), context.Background(), "fake", func(context.Context, string, openAITextRequest) (string, error) {
				t.Fatal("provider called")
				return "", nil
			}, sparseTestRoom)
			if err != nil || handled {
				t.Fatalf("ineligible recovery handled=%v err=%v", handled, err)
			}
			after, _, _ := app.ambientScopeCheckpoint(ambientAgentScopeKey(meetingDigestAgent(), sparseTestRoom))
			if before != after {
				t.Fatal("held/baseline checkpoint changed")
			}
			if _, err := os.Stat(app.meetingDigestSparsePath(sparseTestRoom)); !os.IsNotExist(err) {
				t.Fatal("ineligible ledger created")
			}
		})
	}
}
func TestMeetingDigestSparseRetryBudgetSurvivesRestart(t *testing.T) {
	app := sparseRecoveryTestApp(t)
	calls := 0
	responder := func(context.Context, string, openAITextRequest) (string, error) {
		calls++
		return "", errors.New("injected provider failure")
	}
	for i := 0; i < 5; i++ {
		_, err := app.invokeAmbientAgentGuarded(meetingDigestAgent(), context.Background(), "fake", responder, 1, sparseTestRoom)
		if err == nil {
			t.Fatal("expected hold")
		}
		state := sparseStateForTest(t, app)
		for j := range state.Sources {
			state.Sources[j].RetryAt = time.Time{}
		}
		if err := persistMeetingDigestSparseState(app.meetingDigestSparsePath(sparseTestRoom), state); err != nil {
			t.Fatal(err)
		}
		reloaded, err := newMeetingMemoryStore(app.memory.path)
		if err != nil {
			t.Fatal(err)
		}
		app.memory = reloaded
		app.memory.mu.Lock()
		app.memory.meetingIDs[sparseTestRoom] = "meeting-current"
		app.memory.mu.Unlock()
	}
	if calls != ambientProviderMaxWindowAttempts {
		t.Fatalf("retry calls=%d", calls)
	}
	if sparseSourceStatus(sparseStateForTest(t, app), "brain-org-history") != "needs_attention" {
		t.Fatal("exhausted source not explicitly held")
	}
}

func TestMeetingDigestSparseWithheldAndNoEndedSittingAuthority(t *testing.T) {
	app := sparseRecoveryTestApp(t)
	// A stale memory meeting ID must not reopen private historical material.
	app.memory.mu.Lock()
	app.memory.meetingIDs[sparseTestRoom] = "meeting-old"
	app.memory.mu.Unlock()
	appendDigestTestBrain(t, app, "brain-withdrawn", "meeting-old", "WITHDRAWN SECRET", map[string]string{"roomId": sparseTestRoom, "visibility": "organization", relevanceMetadataKey: relevanceQuarantined})
	entry, err := app.invokeAmbientAgentGuarded(meetingDigestAgent(), context.Background(), "fake", func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if strings.Contains(request.Input, "PRIVATE SECRET") || strings.Contains(request.Input, "WITHDRAWN SECRET") {
			t.Fatal("withheld material sent to provider")
		}
		return cannedMeetingDigestJSON(), nil
	}, 1, sparseTestRoom)
	if err != nil || entry.ID == "" {
		t.Fatalf("org source failed %v", err)
	}
	state := sparseStateForTest(t, app)
	if sparseSourceStatus(state, "brain-private-history") != "pending_authority" || sparseSourceStatus(state, "brain-withdrawn") != "withheld" {
		t.Fatalf("coverage statuses %+v", state)
	}
}
func TestMeetingDigestSparseTruncationCannotSkipSource(t *testing.T) {
	app := sparseRecoveryTestApp(t)
	responder, calls := truncatingResponder(10, func(openAITextRequest) string { return cannedMeetingDigestJSON() })
	_, err := app.invokeAmbientAgentGuarded(meetingDigestAgent(), context.Background(), "fake", responder, 1, sparseTestRoom)
	if err == nil || len(*calls) != 2 {
		t.Fatalf("truncation error=%v calls=%d", err, len(*calls))
	}
	checkpoint, _, _ := app.ambientScopeCheckpoint(ambientAgentScopeKey(meetingDigestAgent(), sparseTestRoom))
	if checkpoint.BaselineID != "" || checkpoint.WindowID != "" {
		t.Fatalf("sparse truncation advanced cursor %+v", checkpoint)
	}
	state := sparseStateForTest(t, app)
	if sparseSourceStatus(state, "brain-org-history") != "in_flight" {
		t.Fatal("truncated source not held")
	}
	for _, ref := range state.Sources {
		if ref.ID == "brain-org-history" && ref.Attempts != 2 {
			t.Fatalf("provider calls not durably counted %+v", ref)
		}
	}
}

func TestMeetingDigestSparseBoundedRecallKeepsExactReadEvidence(t *testing.T) {
	app := sparseRecoveryTestApp(t)
	start := time.Date(2026, 8, 23, 8, 0, 0, 0, time.UTC)
	reviseSparseTestBrain(t, app, "brain-org-history", func(e *meetingMemoryEntry) {
		e.Metadata["fromTranscriptCreatedAt"] = start.Format(time.RFC3339)
		e.Metadata["throughTranscriptCreatedAt"] = start.Add(time.Hour).Format(time.RFC3339)
	})
	entry, err := app.invokeAmbientAgentGuarded(meetingDigestAgent(), context.Background(), "fake", func(context.Context, string, openAITextRequest) (string, error) {
		return cannedMeetingDigestJSON(), nil
	}, 1, sparseTestRoom)
	if err != nil || entry.ID == "" {
		t.Fatalf("produce: %v", err)
	}
	principal := sharedRoomRecallPrincipal(sparseTestRoom, "")
	record := app.meetingRecordStoreForPrincipal(context.Background(), principal, map[string]struct{}{"meeting-old": {}}, func(kind string) bool { return isMeetingDigestKind(kind) })
	if !sparseContains(record.snapshotForMeeting("meeting-old", 0), entry.ID) {
		t.Fatal("body-free navigation lost authorized source evidence")
	}
	briefing := app.meetingBriefingStoreForPrincipal(principal, start.Add(-time.Hour), start.Add(3*time.Hour))
	if sparseContains(briefing.snapshot(0), "brain-org-history") {
		t.Fatal("fixture should keep out-of-day raw brain outside bounded projection")
	}
	if !sparseContains(briefing.digestsInRange(start.Add(-time.Hour), start.Add(3*time.Hour)), entry.ID) {
		t.Fatal("bounded authorized briefing lost source-bound digest")
	}
	if sparseContains(app.memory.digestsInRange(start.Add(-time.Hour), start.Add(3*time.Hour)), entry.ID) {
		t.Fatal("unscoped reflection can promote sparse digest")
	}
	reviseSparseTestBrain(t, app, "brain-org-history", func(e *meetingMemoryEntry) { e.Text = "Corrected source" })
	if sparseContains(app.meetingBriefingStoreForPrincipal(principal, start.Add(-time.Hour), start.Add(3*time.Hour)).snapshot(0), entry.ID) {
		t.Fatal("new bounded read reused stale authorization evidence")
	}
}

func TestMeetingDigestSparseDoesNotUndoOutputWithholding(t *testing.T) {
	app := sparseRecoveryTestApp(t)
	calls := 0
	responder := func(context.Context, string, openAITextRequest) (string, error) {
		calls++
		return cannedMeetingDigestJSON(), nil
	}
	entry, err := app.invokeAmbientAgentGuarded(meetingDigestAgent(), context.Background(), "fake", responder, 1, sparseTestRoom)
	if err != nil {
		t.Fatal(err)
	}
	reviseSparseTestBrain(t, app, entry.ID, func(e *meetingMemoryEntry) { e.Metadata[relevanceMetadataKey] = relevanceQuarantined })
	if _, err := app.invokeAmbientAgentGuarded(meetingDigestAgent(), context.Background(), "fake", responder, 1, sparseTestRoom); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || sparseSourceStatus(sparseStateForTest(t, app), "brain-org-history") != "output_withheld" {
		t.Fatal("withdrawn output regenerated")
	}
}

func TestMeetingDigestSparseCorruptCheckpointFailsClosed(t *testing.T) {
	app := sparseRecoveryTestApp(t)
	if err := os.WriteFile(app.meetingDigestSparsePath(sparseTestRoom), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := app.invokeAmbientAgentGuarded(meetingDigestAgent(), context.Background(), "fake", func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("provider called with corrupt sparse checkpoint")
		return "", nil
	}, 1, sparseTestRoom)
	if err == nil {
		t.Fatal("corrupt checkpoint did not hold")
	}
	health := ambientWorkerCheckpointDiagnostics(app, meetingDigestAgent())
	if health["checkpointStatus"] != "invalid" || health["ambientContinuityHealthy"] != false {
		t.Fatalf("corrupt recovery reported healthy: %+v", health)
	}
}

func TestMeetingDigestSparseExhaustedSourceDoesNotStopNewSource(t *testing.T) {
	app := sparseRecoveryTestApp(t)
	_, _, _ = app.runMeetingDigestSparseRecovery(meetingDigestAgent(), context.Background(), "fake", func(context.Context, string, openAITextRequest) (string, error) {
		return "", errors.New("source-specific failure")
	}, sparseTestRoom)
	state := sparseStateForTest(t, app)
	for i := range state.Sources {
		if state.Sources[i].ID == "brain-org-history" {
			state.Sources[i].Status = "needs_attention"
			state.Sources[i].Attempts = ambientProviderMaxWindowAttempts
			state.Sources[i].RetryAt = time.Time{}
		}
	}
	if err := persistMeetingDigestSparseState(app.meetingDigestSparsePath(sparseTestRoom), state); err != nil {
		t.Fatal(err)
	}
	appendDigestTestBrain(t, app, "brain-new-eligible", "meeting-new", "Independent new organization update", map[string]string{"roomId": sparseTestRoom, "visibility": "organization"})
	calls := 0
	entry, _, err := app.runMeetingDigestSparseRecovery(meetingDigestAgent(), context.Background(), "fake", func(context.Context, string, openAITextRequest) (string, error) {
		calls++
		return cannedMeetingDigestJSON(), nil
	}, sparseTestRoom)
	if calls != 1 || entry.ID == "" {
		t.Fatalf("new eligible source starved: calls=%d entry=%q err=%v statuses=%+v", calls, entry.ID, err, sparseStateForTest(t, app).Sources)
	}
}

func TestMeetingDigestSparseIdleDoesNotRewriteCheckpoint(t *testing.T) {
	app := sparseRecoveryTestApp(t)
	responder := func(context.Context, string, openAITextRequest) (string, error) {
		return cannedMeetingDigestJSON(), nil
	}
	if _, _, err := app.runMeetingDigestSparseRecovery(meetingDigestAgent(), context.Background(), "fake", responder, sparseTestRoom); err != nil {
		t.Fatal(err)
	}
	original := persistMeetingDigestSparseState
	writes := 0
	persistMeetingDigestSparseState = func(path string, state meetingDigestSparseState) error { writes++; return original(path, state) }
	defer func() { persistMeetingDigestSparseState = original }()
	for i := 0; i < 3; i++ {
		if entry, _, err := app.runMeetingDigestSparseRecovery(meetingDigestAgent(), context.Background(), "fake", func(context.Context, string, openAITextRequest) (string, error) {
			t.Fatal("idle provider call")
			return "", nil
		}, sparseTestRoom); err != nil || entry.ID != "" {
			t.Fatalf("idle: %s %v", entry.ID, err)
		}
	}
	if writes != 0 {
		t.Fatalf("idle checkpoint writes=%d", writes)
	}
}
func TestMeetingDigestSparseReconciliationBoundAndRestartProgress(t *testing.T) {
	app := sparseRecoveryTestApp(t)
	// Four pages of sources plus unrelated durable rows must not turn one tick
	// into a lifetime scan. The normal append/rebuild directory supplies lookup.
	app.memory.mu.Lock()
	for i := 0; i < meetingDigestSparseReconcileLimit*4; i++ {
		entry := meetingMemoryEntry{ID: fmt.Sprintf("bounded-brain-%04d", i), Kind: meetingMemoryKindBrain, Text: "New organization update", Metadata: map[string]string{"roomId": sparseTestRoom, "visibility": "organization"}}
		app.memory.entries = append(app.memory.entries, entry)
		app.memory.indexMeetingEntryLocked(len(app.memory.entries)-1, entry)
	}
	for i := 0; i < 5000; i++ {
		entry := meetingMemoryEntry{ID: fmt.Sprintf("unrelated-%04d", i), Kind: meetingMemoryKindTranscript, Text: "Other history", Metadata: map[string]string{"roomId": "unrelated-room"}}
		app.memory.entries = append(app.memory.entries, entry)
		app.memory.indexMeetingEntryLocked(len(app.memory.entries)-1, entry)
	}
	if err := app.memory.rewriteLocked(true); err != nil {
		app.memory.mu.Unlock()
		t.Fatal(err)
	}
	app.memory.mu.Unlock()
	produced := map[string]bool{}
	for tick := 0; tick < 6; tick++ {
		visits := 0
		ctx := context.WithValue(context.Background(), meetingDigestSparseVisitKey{}, func() { visits++ })
		entry, _, err := app.runMeetingDigestSparseRecovery(meetingDigestAgent(), ctx, "fake", func(context.Context, string, openAITextRequest) (string, error) {
			return cannedMeetingDigestJSON(), nil
		}, sparseTestRoom)
		if err != nil {
			t.Fatal(err)
		}
		// Discovery128 + at most256 exact sources +256 exact outputs and
		// their256 source fences, plus pre-dispatch and final-write fences. This bound does not depend on lifetime store size.
		if visits > meetingDigestSparseReconcileLimit*7+2 {
			t.Fatalf("unbounded row visits=%d", visits)
		}
		if entry.ID != "" {
			var ref meetingDigestSparseRef
			_ = json.Unmarshal([]byte(entry.Metadata[meetingDigestSparseMetadataKey]), &ref)
			if produced[ref.ID] {
				t.Fatalf("replayed source %s", ref.ID)
			}
			produced[ref.ID] = true
		}
		if tick == 0 {
			state := sparseStateForTest(t, app)
			if len(state.Sources) >= meetingDigestSparseReconcileLimit*4 {
				t.Fatal("entire history enrolled in one tick")
			}
		}
		reloaded, err := newMeetingMemoryStore(app.memory.path)
		if err != nil {
			t.Fatal(err)
		}
		app.memory = reloaded
	}
	state := sparseStateForTest(t, app)
	if sparseSourceStatus(state, fmt.Sprintf("bounded-brain-%04d", meetingDigestSparseReconcileLimit*4-1)) == "" {
		t.Fatal("durable reconciliation cursor did not reach tail after restart")
	}
	if len(produced) != 6 {
		t.Fatalf("new work progress=%d", len(produced))
	}
}
