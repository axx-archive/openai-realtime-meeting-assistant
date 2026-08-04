package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

type capturedSourceFrame struct {
	track   string
	speaker string
	pcm     []int16
}

type sourceCaptureSink struct {
	mu     sync.Mutex
	frames []capturedSourceFrame
	wake   chan struct{}
}

func (sink *sourceCaptureSink) WriteMixedPCM([]int16) error { return nil }
func (sink *sourceCaptureSink) WriteMixedPCMWithConsent([]int16, []ConsentFence) error {
	return nil
}
func (sink *sourceCaptureSink) WriteSourcePCMWithConsent(track, speaker string, pcm []int16, _ ConsentFence) error {
	sink.mu.Lock()
	sink.frames = append(sink.frames, capturedSourceFrame{track: track, speaker: speaker, pcm: append([]int16(nil), pcm...)})
	sink.mu.Unlock()
	select {
	case sink.wake <- struct{}{}:
	default:
	}
	return nil
}
func (sink *sourceCaptureSink) RemoveSource(string) {}

func TestTranscriptionMixerPreservesOverlappingSourceIdentity(t *testing.T) {
	authority := NewConsentLaneAuthority(NewMemoryConsentStore(), "source-transcription-test")
	authority.CaptureCutoff = func() (uint64, error) { return 0, nil }
	bindingA := consentLaneTestBinding("source-a", "room-source", "sitting-source")
	bindingB := consentLaneTestBinding("source-b", "room-source", "sitting-source")
	for _, binding := range []ConsentAdmissionBinding{bindingA, bindingB} {
		grantConsentScope(t, authority, binding, ConsentAudioCapture)
		grantConsentScope(t, authority, binding, ConsentTranscription)
	}
	captureA, _ := authority.Authorize(context.Background(), bindingA, ConsentLaneAudioCapture)
	transcriptA, _ := authority.Authorize(context.Background(), bindingA, ConsentLaneTranscription)
	captureB, _ := authority.Authorize(context.Background(), bindingB, ConsentLaneAudioCapture)
	transcriptB, _ := authority.Authorize(context.Background(), bindingB, ConsentLaneTranscription)

	mixer := newAudioMixer()
	defer mixer.close()
	sink := &sourceCaptureSink{wake: make(chan struct{}, 8)}
	mixer.setConsentSink("source-transcript", ConsentLaneTranscription, authority, sink)
	pcmA := make([]int16, roomAudioMixFrameSize)
	pcmB := make([]int16, roomAudioMixFrameSize)
	for index := range pcmA {
		pcmA[index] = 1200
		pcmB[index] = -1400
	}
	mixer.submitWithConsent("track-a", "AJ", pcmA, map[ConsentLane]ConsentFence{ConsentLaneAudioCapture: captureA.Fence, ConsentLaneTranscription: transcriptA.Fence})
	mixer.submitWithConsent("track-b", "Tim", pcmB, map[ConsentLane]ConsentFence{ConsentLaneAudioCapture: captureB.Fence, ConsentLaneTranscription: transcriptB.Fence})
	deadline := time.After(time.Second)
	for {
		sink.mu.Lock()
		seen := map[string]string{}
		for _, frame := range sink.frames {
			seen[frame.track] = frame.speaker
		}
		sink.mu.Unlock()
		if seen["track-a"] == "AJ" && seen["track-b"] == "Tim" {
			break
		}
		select {
		case <-sink.wake:
		case <-deadline:
			t.Fatalf("source frames=%+v", seen)
		}
	}
}

func TestKnownSourceTranscriptUsesExactSpeakerAndRejectsPriorRecordingEpoch(t *testing.T) {
	app := newW2ATestApp(t)
	defer app.Close()
	roomID := "room-source2222"
	sittingID := admitMemberWithTranscriptConsentForTest(t, app, roomID, "aj@shareability.com")
	authority := currentConsentLaneAuthority()
	binding, err := app.consentBindingForPrincipal(context.Background(), memberAdmissionPrincipal("aj@shareability.com"), roomID, sittingID)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := authority.Authorize(context.Background(), binding, ConsentLaneTranscription)
	if err != nil || !decision.Allowed {
		t.Fatalf("transcription decision=%+v err=%v", decision, err)
	}
	capture, err := app.memory.reserveTranscriptCapture(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	capture.OccurredEnd = time.Now().UTC().Add(time.Millisecond)
	app.mu.Lock()
	epoch := app.roomLiveLocked(roomID).recordingEpoch
	app.mu.Unlock()
	record := sourceTranscriptRecord{sourceTranscriptIdentity: sourceTranscriptIdentity{
		TrackKey: "track-aj", Speaker: "AJ", Fence: decision.Fence, RecordingEpoch: epoch,
	}, Capture: capture}
	event := kanbanRealtimeEvent{EventID: "source-event-1", ItemID: "source-item-1", Transcript: "Exact source attribution survives overlap."}
	app.rememberTranscriptWithScopeAndSegmentAndSource(roomID, nil, nil, "segment-source-1", event, "transcript_lane", "gpt-transcribe", &record)
	entries := app.memorySnapshotForMeeting(sittingID, 100)
	if len(entries) != 1 || entries[0].Metadata["speaker"] != "AJ" || entries[0].Metadata["speakerConfidence"] != "source_bound" || entries[0].Metadata["sourceTrackId"] != "track-aj" {
		t.Fatalf("entries=%+v", entries)
	}
	app.setTranscriptRecordingInRoom(roomID, false, "AJ")
	app.setTranscriptRecordingInRoom(roomID, true, "AJ")
	stale := event
	stale.EventID = "source-event-stale"
	stale.ItemID = "source-item-stale"
	app.rememberTranscriptWithScopeAndSegmentAndSource(roomID, nil, nil, "segment-source-stale", stale, "transcript_lane", "gpt-transcribe", &record)
	if got := len(app.memorySnapshotForMeeting(sittingID, 100)); got != 1 {
		t.Fatalf("stale epoch persisted; entries=%d", got)
	}
}

func TestSourceManagerRecordingEpochNeverRollsBackward(t *testing.T) {
	manager := &meetingTranscriptionLane{sourceManager: true, sourceLanes: map[string]*meetingTranscriptionLane{}, recordingEpoch: 3}
	manager.resetSourcesForRecordingEpoch(2)
	if manager.recordingEpoch != 3 {
		t.Fatalf("recording epoch rolled backward to %d", manager.recordingEpoch)
	}
}

func TestSourceManagerConnectionReflectsChildProvider(t *testing.T) {
	manager := &meetingTranscriptionLane{sourceManager: true, sourceLanes: map[string]*meetingTranscriptionLane{}, sourceRetireTimers: map[string]*time.Timer{}, sourceRetireGeneration: map[string]uint64{}}
	if manager.isConnected() {
		t.Fatal("empty source manager reported connected")
	}
	child := &meetingTranscriptionLane{}
	manager.sourceLanes["track-aj"] = child
	child.mu.Lock()
	child.connected = true
	child.mu.Unlock()
	if !manager.isConnected() {
		t.Fatal("connected child was not reflected by source manager")
	}
	manager.removeSource("track-aj")
	if !manager.isConnected() {
		t.Fatal("removed source child was closed before its terminal transcript could settle")
	}
	manager.sourceMu.Lock()
	retirementTimer := manager.sourceRetireTimers["track-aj"]
	retirementScheduled := retirementTimer != nil
	if retirementTimer != nil {
		retirementTimer.Stop()
	}
	manager.sourceMu.Unlock()
	if !retirementScheduled {
		t.Fatal("ended source was not scheduled for bounded retirement")
	}
}

func TestSourceManagerClaimsEachAcceptedTurnExactlyOnce(t *testing.T) {
	manager := &meetingTranscriptionLane{sourceManager: true, sourceLanes: map[string]*meetingTranscriptionLane{}, sourceAcceptedForTurn: true}
	if !manager.claimSourceTurnOwnership() {
		t.Fatal("accepted source turn had no owner")
	}
	if manager.claimSourceTurnOwnership() {
		t.Fatal("source ownership leaked into the next mixed turn")
	}
}
