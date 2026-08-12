package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMeetingIntelligenceDesktopUsesOneSafeThreeViewContract(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, marker := range []string{
		`id="roomMeetingRecapTab"`,
		`id="roomMeetingTranscriptTab"`,
		`id="roomMeetingChatTab"`,
		`function parseMeetingIntelligenceSnapshot(payload)`,
		`function handleMeetingIntelligenceSnapshot(payload)`,
		`case 'meeting_intelligence_snapshot':`,
		`appendRoomMeetingTranscriptEntry(message.data)`,
		`function renderMeetingRecap()`,
		`function renderMeetingTranscript()`,
		`roomMeetingTranscript.replaceChildren`,
	} {
		if !strings.Contains(html, marker) {
			t.Fatalf("desktop meeting intelligence missing %q", marker)
		}
	}
	recapBody := functionBody(html, "function renderMeetingRecap()")
	if recapBody == "" || strings.Contains(recapBody, "innerHTML") || !strings.Contains(recapBody, "replaceChildren") {
		t.Fatal("meeting recap must render fail-closed text nodes without HTML injection")
	}
	transcriptBody := functionBody(html, "function renderMeetingTranscript()")
	if !strings.Contains(transcriptBody, "speakerPrefix") || !strings.Contains(transcriptBody, "rawText.slice(speakerPrefix.length + 1).trim()") {
		t.Fatal("desktop transcript must remove only its authoritative speaker prefix, matching native")
	}
	parserBody := functionBody(html, "function parseMeetingIntelligenceSnapshot(payload)")
	for _, invariant := range []string{"meeting-intelligence-v1", "analysisCaptureHighWater", "Number.isSafeInteger", "transcriptStates.includes", "coverageStates.includes"} {
		if !strings.Contains(parserBody, invariant) {
			t.Fatalf("meeting intelligence parser is missing strict invariant %q", invariant)
		}
	}
	transcriptParser := functionBody(html, "function parseRoomMeetingTranscriptEntry(payload)")
	for _, invariant := range []string{"rawCaptureSequence", "Number.isSafeInteger(captureSequence)"} {
		if !strings.Contains(transcriptParser, invariant) {
			t.Fatalf("meeting transcript parser is missing capture-order invariant %q", invariant)
		}
	}
	transcriptOrder := functionBody(html, "function roomMeetingTranscriptOrder(left, right)")
	if !strings.Contains(transcriptOrder, "left.captureSequence - right.captureSequence") {
		t.Fatal("desktop transcript replay must prefer the server-owned capture sequence")
	}
	catchUpBody := functionBody(html, "function markMeetingIntelligenceBehindTranscript(entry)")
	for _, invariant := range []string{"entry.captureSequence <= current.transcript.captureHighWater", "state: 'catching_up'", "state: current.scout.state === 'unavailable' ? 'unavailable' : 'not_caught_up'"} {
		if !strings.Contains(catchUpBody, invariant) {
			t.Fatalf("desktop live transcript truth state missing %q", invariant)
		}
	}
	appendBody := functionBody(html, "function appendRoomMeetingTranscriptEntry(payload)")
	if !strings.Contains(appendBody, "renderMeetingTranscript()") {
		t.Fatal("desktop live transcript event must immediately update the visible transcript")
	}
}

func TestMeetingIntelligenceDesktopParserRejectsContradictoryTruthAndMissingSequence(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	parserStart := strings.Index(source, "function roomMeetingRecord(value)")
	parserEnd := strings.Index(source, "function meetingIntelligenceTimeLabel(value)")
	transcriptStart := strings.Index(source, "function parseRoomMeetingTranscriptEntry(payload)")
	transcriptEnd := strings.Index(source, "function roomMeetingTranscriptOrder(left, right)")
	if parserStart < 0 || parserEnd <= parserStart || transcriptStart < 0 || transcriptEnd <= transcriptStart {
		t.Fatal("desktop meeting parser source boundary unavailable")
	}
	script := source[parserStart:parserEnd] + "\n" + source[transcriptStart:transcriptEnd] + `
const valid = {
  contract: 'meeting-intelligence-v1', roomId: 'office', meetingId: 'meeting-1', revision: 'rev-1', generatedAt: '2026-08-11T20:00:00Z',
  transcript: { state: 'listening', captureHighWater: 44, sequenceComplete: true, segmentCount: 1, lastSegmentId: 'transcript-44', lastCapturedAt: '2026-08-11T19:59:30Z' },
  notes: { state: 'current', revision: 'digest-1', updatedAt: '2026-08-11T20:00:00Z', analysisCaptureHighWater: 44, coverage: 'full' },
  scout: { state: 'ready', sourceCount: 1 },
  recap: { topics: [], decisions: [], actions: [], openQuestions: [], risks: [], themes: [], sourceCount: 1 }
};
if (!parseMeetingIntelligenceSnapshot(valid)) throw new Error('valid snapshot rejected');
const contradictory = structuredClone(valid);
contradictory.transcript.sequenceComplete = false;
contradictory.notes.analysisCaptureHighWater = 43;
if (parseMeetingIntelligenceSnapshot(contradictory)) throw new Error('contradictory current snapshot accepted');
const missingSequence = { id: 'transcript-1', kind: 'transcript', text: 'AJ: private', createdAt: '2026-08-11T20:00:00Z', metadata: { roomId: 'office', meetingId: 'meeting-1' } };
if (parseRoomMeetingTranscriptEntry(missingSequence)) throw new Error('missing capture sequence accepted');
`
	if output, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("desktop meeting parser probe failed: %v\n%s", err, output)
	}
}

func meetingIntelligenceFixture(t *testing.T) (*kanbanBoardApp, string, meetingMemoryEntry) {
	t.Helper()
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	meetingID := "meeting-20260811-120000-000000001"
	store.mu.Lock()
	store.meetingIDs[officeRoomID] = meetingID
	store.mu.Unlock()
	transcript, appended, err := store.appendAttributedTranscriptEntry(
		officeRoomID,
		"transcript-1",
		"item-1",
		"AJ",
		"source_owned",
		"Ship the first-class meeting recap with exact source anchors.",
		map[string]string{"source": "openai_realtime"},
		true,
		meetingID,
	)
	if err != nil || !appended {
		t.Fatalf("append transcript: appended=%v err=%v", appended, err)
	}
	lane := &meetingTranscriptionLane{}
	lane.setConnected(true)
	scout, err := newRoomRealtimeBundle(RoomScoutScope{RoomID: officeRoomID, SittingID: meetingID, MediaGeneration: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	scout.mu.Lock()
	scout.status = RoomScoutReady
	scout.mu.Unlock()
	app := &kanbanBoardApp{
		memory: store,
		meetings: &meetingStore{records: []meetingRecord{{
			ID: meetingID, StartedAt: transcript.CreatedAt.Add(-time.Minute).UTC().Format(time.RFC3339Nano), Participants: []string{"AJ"},
		}}},
		roomLive: map[string]*roomLiveState{
			officeRoomID: {id: officeRoomID, recordingEnabled: true, lane: lane, realtime: scout, mediaGen: 1, mediaSittingID: meetingID},
		},
	}
	return app, meetingID, transcript
}

func TestMeetingIntelligenceSnapshotShowsTruthfulCatchUpBeforeDigest(t *testing.T) {
	app, meetingID, transcript := meetingIntelligenceFixture(t)
	snapshot := app.meetingIntelligenceSnapshot(officeRoomID, transcript.CreatedAt.Add(time.Second))
	if snapshot == nil || snapshot.Contract != meetingIntelligenceContractVersion || snapshot.MeetingID != meetingID {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if snapshot.Transcript.State != "listening" || snapshot.Transcript.CaptureHighWater == 0 || !snapshot.Transcript.SequenceComplete || snapshot.Transcript.SegmentCount != 1 {
		t.Fatalf("transcript=%+v", snapshot.Transcript)
	}
	if snapshot.Notes.State != "catching_up" || snapshot.Recap != nil || snapshot.Scout.State != "not_caught_up" {
		t.Fatalf("notes/scout/recap=%+v / %+v / %+v", snapshot.Notes, snapshot.Scout, snapshot.Recap)
	}
}

func TestMeetingIntelligenceNeverClaimsCurrentWithoutPositiveCompleteTranscript(t *testing.T) {
	app, meetingID, transcript := meetingIntelligenceFixture(t)
	body, err := json.Marshal(meetingDigestPayload{MeetingID: meetingID, Title: "Retained recap", Day: "2026-08-11"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, meetingID, string(body), map[string]string{
		"meetingId": meetingID, meetingDigestCaptureMetadataKey: transcript.Metadata["captureSequence"],
	}); err != nil {
		t.Fatal(err)
	}
	app.memory.mu.Lock()
	for index := range app.memory.entries {
		if app.memory.entries[index].Kind == meetingMemoryKindTranscript {
			app.memory.entries[index].Metadata["meetingId"] = "meeting-removed-from-current-window"
		}
	}
	app.memory.mu.Unlock()
	snapshot := app.meetingIntelligenceSnapshot(officeRoomID, time.Now().UTC())
	if snapshot == nil || snapshot.Transcript.SegmentCount != 0 || snapshot.Transcript.CaptureHighWater != 0 || snapshot.Notes.State != "catching_up" || snapshot.Scout.State != "not_caught_up" {
		t.Fatalf("retained digest without a positive complete transcript claimed current: %+v", snapshot)
	}
}

func TestMeetingIntelligenceLegacyDigestFallbackUsesExactThroughBrain(t *testing.T) {
	app, meetingID, transcript := meetingIntelligenceFixture(t)
	first, appended, err := app.memory.appendBrainWriteUp("brain-exact", "Exact source", map[string]string{
		"meetingId": meetingID, meetingBrainCaptureMetadataKey: transcript.Metadata["captureSequence"],
	})
	if err != nil || !appended {
		t.Fatalf("append exact brain: appended=%t err=%v", appended, err)
	}
	if _, appended, err := app.memory.appendBrainWriteUp("brain-newer-unrelated", "Newer but not the digest cursor", map[string]string{
		"meetingId": meetingID, meetingBrainCaptureMetadataKey: "999999",
	}); err != nil || !appended {
		t.Fatalf("append newer brain: appended=%t err=%v", appended, err)
	}
	body, err := json.Marshal(meetingDigestPayload{MeetingID: meetingID, Title: "Legacy exact cursor", Day: "2026-08-11"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, meetingID, string(body), map[string]string{
		"meetingId": meetingID, meetingDigestCursorMetadataKey: first.ID,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := app.meetingIntelligenceSnapshot(officeRoomID, time.Now().UTC())
	if snapshot == nil || snapshot.Notes.AnalysisCaptureHighWater != snapshot.Transcript.CaptureHighWater || snapshot.Notes.State != "current" {
		t.Fatalf("legacy exact brain cursor did not recover exact high-water: %+v", snapshot)
	}
}

func TestAmbientMeetingAnalysisSeesCurrentMediaGenerationTranscript(t *testing.T) {
	app, _, transcript := meetingIntelligenceFixture(t)
	app.memory.mu.Lock()
	app.memory.entries[0].Metadata["mediaGeneration"] = "1"
	app.memory.mu.Unlock()

	principal := app.currentRoomMediaRecallPrincipal(officeRoomID, app.memory.currentMeetingID(officeRoomID))
	if principal.MediaGeneration != 1 {
		t.Fatalf("media generation=%d, want 1", principal.MediaGeneration)
	}
	head, count, _, ok := app.peekUnconsumedWindow(meetingBrainAgent(), officeRoomID)
	if !ok || head != transcript.ID || count != 1 {
		t.Fatalf("ambient current-generation window head=%q count=%d ok=%v, want %q/1/true", head, count, ok, transcript.ID)
	}

	app.mu.Lock()
	app.roomLive[officeRoomID].mediaGen = 2
	app.mu.Unlock()
	if head, count, _, ok := app.peekUnconsumedWindow(meetingBrainAgent(), officeRoomID); ok || head != "" || count != 0 {
		t.Fatalf("stale-generation transcript leaked into successor window head=%q count=%d ok=%v", head, count, ok)
	}
}

func TestCurrentMeetingTranscriptSnapshotReplaysOnlyAuthorizedActiveSitting(t *testing.T) {
	app, meetingID, transcript := meetingIntelligenceFixture(t)
	app.memory.mu.Lock()
	app.memory.entries[0].Metadata["mediaGeneration"] = "1"
	app.memory.mu.Unlock()
	if _, appended, err := app.memory.appendEntry(
		meetingMemoryKindTranscript, "old-transcript", "Old meeting secret.",
		map[string]string{"meetingId": "meeting-old", "roomId": officeRoomID, "speaker": "AJ"},
	); err != nil || !appended {
		t.Fatalf("append old transcript: appended=%v err=%v", appended, err)
	}
	principal := app.recallPrincipalForMemberRoom("aj@shareability.com", officeRoomID)
	if principal.SittingID != meetingID || principal.MediaGeneration != 1 {
		t.Fatalf("member reconnect principal=%+v, want sitting=%q generation=1", principal, meetingID)
	}
	snapshot := app.currentMeetingTranscriptSnapshot(context.Background(), principal, officeRoomID)
	if snapshot == nil || snapshot.Contract != meetingTranscriptContractVersion || snapshot.MeetingID != meetingID || snapshot.RoomID != officeRoomID {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if len(snapshot.Entries) != 1 || snapshot.Entries[0].ID != transcript.ID {
		t.Fatalf("entries=%+v, want only current transcript %q", snapshot.Entries, transcript.ID)
	}
}

func TestMeetingIntelligenceSnapshotProjectsCurrentAnchoredRecap(t *testing.T) {
	app, meetingID, transcript := meetingIntelligenceFixture(t)
	payload := meetingDigestPayload{
		MeetingID: meetingID,
		Title:     "Launch readiness",
		Day:       "2026-08-11",
		Topics: []meetingDigestTopic{{
			T: "First-class meeting intelligence", Anchor: transcript.ID, At: transcript.CreatedAt.UTC().Format(time.RFC3339), Importance: 5,
		}},
		Decisions: []meetingDigestDecision{{
			D: "Keep one evolving recap", By: "AJ", Status: "ratified", Anchor: transcript.ID, At: transcript.CreatedAt.UTC().Format(time.RFC3339), Importance: 5,
		}},
		ActionItems: []meetingDigestAction{{
			A: "Wire the authenticated snapshot", Owner: "Scout", Status: "open", Anchor: transcript.ID, At: transcript.CreatedAt.UTC().Format(time.RFC3339), Importance: 4,
		}},
		OpenQuestions: []meetingDigestQuestion{{
			Q: "When is the reviewed release ready?", Anchor: transcript.ID, At: transcript.CreatedAt.UTC().Format(time.RFC3339), Importance: 3,
		}},
		Themes: []string{"Meeting memory"},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, meetingID, string(body), map[string]string{
		"meetingId":                     meetingID,
		"roomId":                        officeRoomID,
		digestSpanStartMetadataKey:      transcript.CreatedAt.UTC().Format(time.RFC3339),
		digestSpanEndMetadataKey:        transcript.CreatedAt.UTC().Format(time.RFC3339),
		digestCoverageMetadataKey:       coverageLabelFull,
		meetingDigestCaptureMetadataKey: transcript.Metadata["captureSequence"],
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := app.meetingIntelligenceSnapshot(officeRoomID, digest.CreatedAt.Add(time.Second))
	if snapshot == nil || snapshot.Notes.State != "current" || snapshot.Notes.Revision != digest.ID || snapshot.Notes.Coverage != coverageLabelFull {
		t.Fatalf("snapshot notes=%+v", snapshot)
	}
	if snapshot.Scout.State != "ready" || snapshot.Scout.SourceCount != 1 || snapshot.Recap == nil || snapshot.Recap.SourceCount != 1 {
		t.Fatalf("snapshot scout/recap=%+v / %+v", snapshot.Scout, snapshot.Recap)
	}
	if len(snapshot.Recap.Decisions) != 1 || snapshot.Recap.Decisions[0].SourceID != transcript.ID || len(snapshot.Recap.Actions) != 1 || len(snapshot.Recap.OpenQuestions) != 1 {
		t.Fatalf("recap=%+v", snapshot.Recap)
	}
	contextEntries := app.currentMeetingDigestContext(
		context.Background(),
		sharedRoomRecallPrincipal(officeRoomID, meetingID),
		map[string]string{"originMeetingId": meetingID, "originKind": agentThreadOriginRoom},
	)
	if len(contextEntries) != 1 || contextEntries[0].ID != digest.ID || contextEntries[0].Kind != meetingMemoryKindMeetingDigest {
		t.Fatalf("current meeting coworker context=%+v, want digest %q", contextEntries, digest.ID)
	}
	recall, _, recallErr := app.answerMemoryQuestionForPrincipal(
		map[string]any{"query": "What did we decide about the first-class meeting recap?"},
		sharedRoomRecallPrincipal(officeRoomID, meetingID),
		false,
	)
	recallText := strings.ToLower(asString(recall["answer"]))
	if recallErr != nil || !strings.Contains(recallText, "launch readiness") || !strings.Contains(recallText, strings.ToLower(meetingID)) {
		t.Fatalf("grounded current-meeting recall=%+v err=%v", recall, recallErr)
	}
}

func TestMeetingIntelligenceSnapshotRejectsMalformedCaptureOrdering(t *testing.T) {
	app, _, transcript := meetingIntelligenceFixture(t)
	app.memory.mu.Lock()
	app.memory.entries[0].Metadata["captureSequence"] = "not-a-sequence"
	app.memory.mu.Unlock()
	snapshot := app.meetingIntelligenceSnapshot(officeRoomID, transcript.CreatedAt.Add(time.Second))
	if snapshot == nil || snapshot.Transcript.SequenceComplete || snapshot.Transcript.CaptureHighWater != 0 {
		t.Fatalf("transcript=%+v", snapshot.Transcript)
	}
}

func TestMeetingIntelligenceBroadcastRejectsRolloverBetweenSnapshotAndFanout(t *testing.T) {
	app, meetingID, _ := meetingIntelligenceFixture(t)
	previousApp := kanbanApp
	kanbanApp = app
	previousProbe := meetingIntelligenceBeforeScopedFanoutProbe
	t.Cleanup(func() {
		meetingIntelligenceBeforeScopedFanoutProbe = previousProbe
		kanbanApp = previousApp
	})

	rolled := false
	meetingIntelligenceBeforeScopedFanoutProbe = func() {
		if rolled {
			return
		}
		rolled = true
		successorID := "meeting-20260811-130000-000000002"
		app.meetings.mu.Lock()
		for index := range app.meetings.records {
			if app.meetings.records[index].EndedAt == "" && meetingRoomID(app.meetings.records[index]) == officeRoomID {
				app.meetings.records[index].EndedAt = time.Now().UTC().Format(time.RFC3339Nano)
			}
		}
		app.meetings.records = append(app.meetings.records, meetingRecord{ID: successorID, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Participants: []string{"AJ"}})
		app.meetings.mu.Unlock()
		app.memory.mu.Lock()
		app.memory.meetingIDs[officeRoomID] = successorID
		app.memory.mu.Unlock()
		app.mu.Lock()
		state := app.roomLiveLocked(officeRoomID)
		state.mediaGen++
		state.mediaSittingID = successorID
		app.mu.Unlock()
	}

	if app.broadcastMeetingIntelligence(officeRoomID, meetingID) {
		t.Fatal("predecessor meeting intelligence crossed a rollover into fan-out")
	}
	if !rolled {
		t.Fatal("rollover probe did not run after the exact predecessor snapshot")
	}
}

func TestMeetingIntelligenceSnapshotRejectsCaptureGapAndDuplicate(t *testing.T) {
	for _, test := range []struct {
		name           string
		secondSequence string
	}{
		{name: "gap", secondSequence: "3"},
		{name: "duplicate", secondSequence: "1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, meetingID, _ := meetingIntelligenceFixture(t)
			if _, appended, err := app.memory.appendAttributedTranscriptEntry(
				officeRoomID,
				"transcript-2",
				"item-2",
				"Tyler",
				"source_owned",
				"The recap must never claim current across a broken capture sequence.",
				map[string]string{"source": "openai_realtime"},
				true,
				meetingID,
			); err != nil || !appended {
				t.Fatalf("append second transcript: appended=%v err=%v", appended, err)
			}
			app.memory.mu.Lock()
			app.memory.entries[1].Metadata["captureSequence"] = test.secondSequence
			app.memory.mu.Unlock()

			snapshot := app.meetingIntelligenceSnapshot(officeRoomID, time.Now().UTC())
			if snapshot == nil || snapshot.Transcript.SequenceComplete || snapshot.Notes.State != "catching_up" || snapshot.Scout.State != "not_caught_up" {
				t.Fatalf("snapshot=%+v", snapshot)
			}
		})
	}
}

func TestMeetingIntelligenceDigestBehindTranscriptStaysCatchingUp(t *testing.T) {
	app, meetingID, first := meetingIntelligenceFixture(t)
	payload := meetingDigestPayload{MeetingID: meetingID, Title: "Partial recap", Day: "2026-08-11"}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, meetingID, string(body), map[string]string{
		"meetingId":                     meetingID,
		"roomId":                        officeRoomID,
		meetingDigestCaptureMetadataKey: first.Metadata["captureSequence"],
	}); err != nil {
		t.Fatal(err)
	}
	second, appended, err := app.memory.appendAttributedTranscriptEntry(
		officeRoomID,
		"transcript-2",
		"item-2",
		"AJ",
		"source_owned",
		"This later transcript must keep the notes in a catching-up state.",
		map[string]string{"source": "openai_realtime"},
		true,
		meetingID,
	)
	if err != nil || !appended {
		t.Fatalf("append second transcript: appended=%v err=%v", appended, err)
	}

	snapshot := app.meetingIntelligenceSnapshot(officeRoomID, second.CreatedAt.Add(time.Second))
	if snapshot == nil || snapshot.Transcript.CaptureHighWater == 0 || snapshot.Notes.AnalysisCaptureHighWater == 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if snapshot.Notes.AnalysisCaptureHighWater >= snapshot.Transcript.CaptureHighWater || snapshot.Notes.State != "catching_up" || snapshot.Scout.State != "not_caught_up" {
		t.Fatalf("stale analysis was presented as current: %+v", snapshot)
	}
}

func TestMeetingIntelligenceRevisionChangesWithDigestContentAtSameHighWater(t *testing.T) {
	app, meetingID, transcript := meetingIntelligenceFixture(t)
	writeDigest := func(title string) {
		t.Helper()
		body, err := json.Marshal(meetingDigestPayload{MeetingID: meetingID, Title: title, Day: "2026-08-11"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, meetingID, string(body), map[string]string{
			"meetingId":                     meetingID,
			"roomId":                        officeRoomID,
			meetingDigestCaptureMetadataKey: transcript.Metadata["captureSequence"],
		}); err != nil {
			t.Fatal(err)
		}
	}
	writeDigest("First recap")
	first := app.meetingIntelligenceSnapshot(officeRoomID, time.Now().UTC())
	writeDigest("Revised recap")
	second := app.meetingIntelligenceSnapshot(officeRoomID, time.Now().UTC())
	if first == nil || second == nil || first.Recap == nil || second.Recap == nil {
		t.Fatalf("snapshots=%+v / %+v", first, second)
	}
	if first.Revision == second.Revision || first.Recap.Title == second.Recap.Title {
		t.Fatalf("digest content revision did not advance: first=%+v second=%+v", first, second)
	}
}

func TestCurrentMeetingTranscriptSnapshotOrdersByCaptureSequence(t *testing.T) {
	app, meetingID, first := meetingIntelligenceFixture(t)
	second, appended, err := app.memory.appendAttributedTranscriptEntry(
		officeRoomID,
		"transcript-2",
		"item-2",
		"Tyler",
		"source_owned",
		"Capture order, not wall-clock display time, owns replay order.",
		map[string]string{"source": "openai_realtime"},
		true,
		meetingID,
	)
	if err != nil || !appended {
		t.Fatalf("append second transcript: appended=%v err=%v", appended, err)
	}
	app.memory.mu.Lock()
	app.memory.entries[0].CreatedAt = second.CreatedAt.Add(time.Hour)
	app.memory.entries[1].CreatedAt = first.CreatedAt.Add(-time.Hour)
	app.memory.mu.Unlock()

	snapshot := app.currentMeetingTranscriptSnapshot(context.Background(), sharedRoomRecallPrincipal(officeRoomID, meetingID), officeRoomID)
	if snapshot == nil || len(snapshot.Entries) != 2 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if snapshot.Entries[0].ID != first.ID || snapshot.Entries[1].ID != second.ID {
		t.Fatalf("capture-order replay=%q,%q want=%q,%q", snapshot.Entries[0].ID, snapshot.Entries[1].ID, first.ID, second.ID)
	}
}

func TestCurrentMeetingTranscriptSnapshotRejectsMissingOrDuplicateCaptureSequence(t *testing.T) {
	for _, mutation := range []func(*meetingMemoryStore){
		func(store *meetingMemoryStore) { delete(store.entries[0].Metadata, "captureSequence") },
		func(store *meetingMemoryStore) {
			store.entries = append(store.entries, cloneMemoryEntry(store.entries[0]))
			store.entries[len(store.entries)-1].ID = "transcript-duplicate-sequence"
		},
	} {
		app, meetingID, _ := meetingIntelligenceFixture(t)
		app.memory.mu.Lock()
		mutation(app.memory)
		app.memory.mu.Unlock()
		if snapshot := app.currentMeetingTranscriptSnapshot(context.Background(), app.currentRoomMediaRecallPrincipal(officeRoomID, meetingID), officeRoomID); snapshot != nil {
			t.Fatalf("malformed capture replay was accepted: %+v", snapshot)
		}
	}
}

func TestMemberMeetingSnapshotsShareOneImmutableMemoryGeneration(t *testing.T) {
	app, meetingID, transcript := meetingIntelligenceFixture(t)
	body, err := json.Marshal(meetingDigestPayload{MeetingID: meetingID, Title: "Atomic pair", Day: "2026-08-11"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, meetingID, string(body), map[string]string{
		"meetingId": meetingID, meetingDigestCaptureMetadataKey: transcript.Metadata["captureSequence"],
	}); err != nil {
		t.Fatal(err)
	}
	principal := app.currentRoomMediaRecallPrincipal(officeRoomID, meetingID)
	replay, intelligence := app.memberMeetingIntelligenceSnapshots(context.Background(), principal, officeRoomID, time.Now().UTC())
	if replay == nil || intelligence == nil || len(replay.Entries) != 1 {
		t.Fatalf("paired snapshots unavailable: replay=%+v intelligence=%+v", replay, intelligence)
	}
	lastSequence, ok := entryCaptureSequence(replay.Entries[len(replay.Entries)-1])
	if !ok || lastSequence != intelligence.Transcript.CaptureHighWater || intelligence.Notes.AnalysisCaptureHighWater != lastSequence || intelligence.Notes.State != "current" {
		t.Fatalf("paired snapshots crossed memory generations: replay=%+v intelligence=%+v", replay, intelligence)
	}
}

func TestMemberMeetingSnapshotsStayConsistentDuringConcurrentTranscriptAppends(t *testing.T) {
	app, meetingID, _ := meetingIntelligenceFixture(t)
	principal := app.currentRoomMediaRecallPrincipal(officeRoomID, meetingID)
	var writers sync.WaitGroup
	writers.Add(1)
	go func() {
		defer writers.Done()
		for index := 2; index <= 40; index++ {
			_, _, _ = app.memory.appendAttributedTranscriptEntry(
				officeRoomID,
				"transcript-concurrent-"+strconv.Itoa(index),
				"item-concurrent-"+strconv.Itoa(index),
				"AJ",
				"source_owned",
				"Concurrent transcript "+strconv.Itoa(index),
				map[string]string{"source": "openai_realtime"},
				true,
				meetingID,
			)
		}
	}()
	for index := 0; index < 80; index++ {
		replay, intelligence := app.memberMeetingIntelligenceSnapshots(context.Background(), principal, officeRoomID, time.Now().UTC())
		if replay == nil || intelligence == nil || len(replay.Entries) == 0 {
			t.Fatalf("paired snapshot unavailable during append: replay=%+v intelligence=%+v", replay, intelligence)
		}
		lastSequence, ok := entryCaptureSequence(replay.Entries[len(replay.Entries)-1])
		if !ok || lastSequence != intelligence.Transcript.CaptureHighWater || len(replay.Entries) != intelligence.Transcript.SegmentCount {
			t.Fatalf("paired snapshot crossed a write generation: replay_last=%d replay_count=%d intelligence=%+v", lastSequence, len(replay.Entries), intelligence.Transcript)
		}
	}
	writers.Wait()
}

func TestMeetingReconnectReconcilesAfterScopedMediaRegistration(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	registration := strings.Index(source, "peerConnections = append(peerConnections, peerConnectionState{")
	if registration < 0 {
		t.Fatal("media registration seam is missing")
	}
	before := strings.Index(source[:registration], "sendMemberMeetingIntelligenceSnapshots(c, kanbanApp, sessionEmail, memberMeetingScope)")
	afterRelative := strings.Index(source[registration:], "sendMemberMeetingIntelligenceSnapshots(c, kanbanApp, sessionEmail, memberMeetingScope)")
	if before < 0 || afterRelative < 0 {
		t.Fatal("meeting reconnect must replay once before and reconcile once after scoped media registration")
	}
}

func TestMeetingDigestCurrentMeetingBootstrapAdmitsOnlyActiveSitting(t *testing.T) {
	t.Setenv(meetingDigestCurrentMeetingBootstrapEnv, "true")
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	oldBrain, appended, err := store.appendBrainWriteUp("brain-old", "## Overview\nOld meeting.", map[string]string{"roomId": officeRoomID, "meetingId": "meeting-old"})
	if err != nil || !appended {
		t.Fatalf("append old brain: appended=%v err=%v", appended, err)
	}
	currentBrain, appended, err := store.appendBrainWriteUp("brain-current", "## Overview\nCurrent meeting.", map[string]string{"roomId": officeRoomID, "meetingId": "meeting-current"})
	if err != nil || !appended {
		t.Fatalf("append current brain: appended=%v err=%v", appended, err)
	}
	app := &kanbanBoardApp{
		memory: store,
		meetings: &meetingStore{records: []meetingRecord{{
			ID: "meeting-current", StartedAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), Participants: []string{"AJ"},
		}}},
	}
	baseline, admitted := app.meetingDigestCurrentMeetingBootstrapBaseline(meetingDigestAgent(), officeRoomID)
	if !admitted || baseline != oldBrain.ID {
		t.Fatalf("baseline=%q admitted=%v", baseline, admitted)
	}
	inputs := store.unconsumedEntriesAfterForRoom(
		meetingMemoryKindBrain,
		meetingMemoryKindMeetingDigest,
		meetingDigestCursorMetadataKey,
		10,
		baseline,
		officeRoomID,
	)
	if len(inputs) != 1 || inputs[0].ID != currentBrain.ID {
		t.Fatalf("inputs=%+v", inputs)
	}
}
