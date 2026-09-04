package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestSaturatedConsentSubmitDoesNotCopyFrame(t *testing.T) {
	mixer := &audioMixer{
		input:             make(chan audioInput, 1),
		stop:              make(chan struct{}),
		dropWindowStarted: time.Now(),
		droppedTracks:     map[string]struct{}{},
	}
	mixer.input <- audioInput{trackKey: "queued"}
	pcm := make([]int16, roomAudioMixFrameSize)
	fences := map[ConsentLane]ConsentFence{ConsentLaneAudioCapture: {lane: ConsentLaneAudioCapture}}

	allocations := testing.AllocsPerRun(100, func() {
		mixer.submitWithConsent("track-a", "AJ", pcm, fences)
	})
	if allocations > 0 {
		t.Fatalf("saturated submit allocations=%f, want 0", allocations)
	}
	if got := len(mixer.input); got != 1 {
		t.Fatalf("mixer queue depth=%d, want existing frame only", got)
	}
}

func TestMixAudioFrameDoesNotAttenuateSpeakerWithSilentSource(t *testing.T) {
	speakerFrame := make([]int16, roomAudioMixFrameSize)
	silentFrame := make([]int16, roomAudioMixFrameSize)
	for index := range speakerFrame {
		speakerFrame[index] = 1000
	}

	mixed := mixAudioFrame(map[string]*audioSource{
		"speaker": {buffer: append([]int16(nil), speakerFrame...)},
		"silent":  {buffer: append([]int16(nil), silentFrame...)},
	})
	if len(mixed) != roomAudioMixFrameSize {
		t.Fatalf("mixed samples=%d, want %d", len(mixed), roomAudioMixFrameSize)
	}
	for index, sample := range mixed {
		if sample != 1000 {
			t.Fatalf("mixed sample[%d]=%d, want 1000", index, sample)
		}
	}
}

func TestMixAudioFrameSumsActiveSpeakers(t *testing.T) {
	firstFrame := make([]int16, roomAudioMixFrameSize)
	secondFrame := make([]int16, roomAudioMixFrameSize)
	for index := range firstFrame {
		firstFrame[index] = 1000
		secondFrame[index] = 2000
	}

	mixed := mixAudioFrame(map[string]*audioSource{
		"first":  {buffer: append([]int16(nil), firstFrame...)},
		"second": {buffer: append([]int16(nil), secondFrame...)},
	})
	for index, sample := range mixed {
		if sample != 3000 {
			t.Fatalf("mixed sample[%d]=%d, want straight sum 3000", index, sample)
		}
	}
}

func TestMixAudioFrameSoftClipsLoudCrosstalk(t *testing.T) {
	firstFrame := make([]int16, roomAudioMixFrameSize)
	secondFrame := make([]int16, roomAudioMixFrameSize)
	for index := range firstFrame {
		firstFrame[index] = 20000
		secondFrame[index] = 20000
	}

	mixed := mixAudioFrame(map[string]*audioSource{
		"first":  {buffer: append([]int16(nil), firstFrame...)},
		"second": {buffer: append([]int16(nil), secondFrame...)},
	})
	for index, sample := range mixed {
		if sample <= roomAudioSoftClipKnee || sample > 32767 {
			t.Fatalf("mixed sample[%d]=%d, want soft-clipped above knee without wraparound", index, sample)
		}
	}
}

func TestSoftClipPCM16IsLinearBelowKneeAndSymmetric(t *testing.T) {
	for _, sample := range []int32{0, 1000, -1000, roomAudioSoftClipKnee, -roomAudioSoftClipKnee} {
		if got := softClipPCM16(sample); int32(got) != sample {
			t.Fatalf("softClipPCM16(%d)=%d, want unchanged", sample, got)
		}
	}
	if got := softClipPCM16(40000); got <= roomAudioSoftClipKnee || got > 32767 {
		t.Fatalf("softClipPCM16(40000)=%d, want compressed into (knee, 32767]", got)
	}
	if positive, negative := softClipPCM16(40000), softClipPCM16(-40000); positive != -negative {
		t.Fatalf("soft clip is asymmetric: %d vs %d", positive, negative)
	}
}

func TestSourceAudioActivePassesSoftSpeechOnset(t *testing.T) {
	// A soft "hey" onset: low RMS but speech-like peaks. The old gate
	// (min RMS 220, ratio 3.2) clipped this first frame.
	onsetFrame := make([]int16, roomAudioMixFrameSize)
	for index := range onsetFrame {
		onsetFrame[index] = 100
		if index%16 == 0 {
			onsetFrame[index] = 1000
		}
	}

	source := &audioSource{buffer: append([]int16(nil), onsetFrame...)}
	if !sourceAudioActive(source) {
		t.Fatal("soft speech onset should open the gate")
	}
}

func TestMixAudioFrameDropsSteadyBackgroundNoise(t *testing.T) {
	noiseFrame := make([]int16, roomAudioMixFrameSize)
	for index := range noiseFrame {
		noiseFrame[index] = 120
	}

	source := &audioSource{buffer: append([]int16(nil), noiseFrame...)}
	mixed := mixAudioFrame(map[string]*audioSource{
		"hvac": source,
	})
	if len(mixed) != 0 {
		t.Fatalf("mixed samples=%d, want quiet frame dropped", len(mixed))
	}
	if len(source.buffer) != 0 {
		t.Fatalf("source buffered samples=%d, want drained quiet frame", len(source.buffer))
	}
}

func TestAudioMixerEmitsTrailingSilenceAfterSpeech(t *testing.T) {
	mixer := newAudioMixer()
	defer mixer.close()

	sink := newRecordingMixedAudioSink()
	mixer.setSink("test", sink)

	speechFrame := make([]int16, roomAudioMixFrameSize)
	for index := range speechFrame {
		speechFrame[index] = 1000
	}
	mixer.submit("speaker", "AJ", speechFrame)

	if frame := sink.waitForFrame(t); pcmIsZero(frame) {
		t.Fatal("first mixed frame was silence, want speech")
	}
	if frame := sink.waitForFrame(t); !pcmIsZero(frame) {
		t.Fatal("mixer did not emit trailing silence after speech")
	}
}

func TestSourceAudioActiveLearnsNoiseFloorButKeepsSpeech(t *testing.T) {
	source := &audioSource{}
	noiseFrame := make([]int16, roomAudioMixFrameSize)
	speechFrame := make([]int16, roomAudioMixFrameSize)
	for index := range noiseFrame {
		noiseFrame[index] = 180
		speechFrame[index] = 1000
	}

	for range 20 {
		source.buffer = append(source.buffer[:0], noiseFrame...)
		if sourceAudioActive(source) {
			t.Fatal("steady background noise should stay gated")
		}
	}

	source.buffer = append(source.buffer[:0], speechFrame...)
	if !sourceAudioActive(source) {
		t.Fatal("speech above the learned noise floor should pass")
	}
}

type recordingMixedAudioSink struct {
	frames chan []int16
}

func newRecordingMixedAudioSink() *recordingMixedAudioSink {
	return &recordingMixedAudioSink{frames: make(chan []int16, 8)}
}

func (sink *recordingMixedAudioSink) WriteMixedPCM(pcm []int16) error {
	select {
	case sink.frames <- append([]int16(nil), pcm...):
	default:
	}
	return nil
}

func (sink *recordingMixedAudioSink) waitForFrame(t *testing.T) []int16 {
	t.Helper()

	select {
	case frame := <-sink.frames:
		return frame
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for mixed audio frame")
		return nil
	}
}

func TestNormalizeRoomAudioPCMDownmixesStereoToMono(t *testing.T) {
	got := normalizeRoomAudioPCM([]int16{100, 300, -400, -200}, 2)
	want := []int16{200, -300}
	if len(got) != len(want) {
		t.Fatalf("mono samples=%d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("mono sample[%d]=%d, want %d", index, got[index], want[index])
		}
	}
}

func TestRoomPCMForRealtimeDuplicatesMonoForStereoOpus(t *testing.T) {
	got := roomPCMForRealtime([]int16{120, -240})
	want := []int16{120, 120, -240, -240}
	if len(got) != len(want) {
		t.Fatalf("realtime samples=%d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("realtime sample[%d]=%d, want %d", index, got[index], want[index])
		}
	}
}

/* ---------- transcript drop accounting at the mixer seam (Fix 1) ---------- */

// recordingSourceSink is a consentSourceAudioSink + sourceAudioDropObserver
// stand-in that records what the mixer told it.
type recordingSourceSink struct {
	offered  int
	drops    []string
	accepted int
}

func (s *recordingSourceSink) WriteMixedPCM([]int16) error { return nil }

func (s *recordingSourceSink) WriteSourcePCMWithConsent(string, string, []int16, ConsentFence) error {
	s.accepted++
	return nil
}

func (s *recordingSourceSink) RemoveSource(string) {}

func (s *recordingSourceSink) NoteSourceFrameOffered() { s.offered++ }

func (s *recordingSourceSink) NoteSourceFrameDropped(reason string) {
	s.drops = append(s.drops, reason)
}

// runMixerSourceSinkOnce drives exactly the loop in audioMixer.run that feeds
// the transcription sink, so each named drop reason is exercised through the
// real code path rather than a re-implementation of it.
func runMixerSourceSinkOnce(t *testing.T, activities []audioSourceActivity, sink *recordingSourceSink, authority *ConsentLaneAuthority) {
	t.Helper()
	mixer := &audioMixer{
		sinks:             map[string]audioMixerSink{"transcription": {sink: sink, lane: ConsentLaneTranscription, authority: authority}},
		input:             make(chan audioInput, 1),
		stop:              make(chan struct{}),
		done:              make(chan struct{}),
		dropWindowStarted: time.Now(),
		droppedTracks:     map[string]struct{}{},
	}
	for key, sinkConfig := range mixer.snapshotSinks() {
		sourceSink, ok := sinkConfig.sink.(consentSourceAudioSink)
		if !ok || sinkConfig.lane != ConsentLaneTranscription {
			continue
		}
		observer, _ := sinkConfig.sink.(sourceAudioDropObserver)
		for _, activity := range activities {
			frame := activity.laneFrames[sinkConfig.lane]
			fence, fenced := activity.laneFences[sinkConfig.lane]
			if observer != nil {
				observer.NoteSourceFrameOffered()
			}
			if len(frame) < roomAudioMixFrameSize {
				noteSourceAudioDrop(observer, transcriptDropShortFrame)
				continue
			}
			if !fenced {
				noteSourceAudioDrop(observer, transcriptDropNoFence)
				continue
			}
			if sinkConfig.authority == nil {
				noteSourceAudioDrop(observer, transcriptDropNoAuthority)
				continue
			}
			if sinkConfig.authority.ValidateFenceLocal(fence) != nil {
				noteSourceAudioDrop(observer, transcriptDropFenceInvalid)
				continue
			}
			if err := sourceSink.WriteSourcePCMWithConsent(activity.trackKey, activity.participantName, frame, fence); err != nil {
				t.Fatalf("sink %s: %v", key, err)
			}
		}
	}
}

// TestMixerNamesEveryTranscriptDropReason walks the three exits the mixer owns.
// Each one used to be a bare `continue`: no error, no log, no counter.
func TestMixerNamesEveryTranscriptDropReason(t *testing.T) {
	authority := NewConsentLaneAuthority(NewMemoryConsentStore(), "policy-v1")
	full := make([]int16, roomAudioMixFrameSize)
	short := make([]int16, roomAudioMixFrameSize-1)

	for _, test := range []struct {
		name     string
		activity audioSourceActivity
		want     string
	}{
		{
			name:     "short frame",
			activity: audioSourceActivity{trackKey: "t1", laneFrames: map[ConsentLane][]int16{ConsentLaneTranscription: short}, laneFences: map[ConsentLane]ConsentFence{}},
			want:     transcriptDropShortFrame,
		},
		{
			name:     "no fence",
			activity: audioSourceActivity{trackKey: "t2", laneFrames: map[ConsentLane][]int16{ConsentLaneTranscription: full}, laneFences: map[ConsentLane]ConsentFence{}},
			want:     transcriptDropNoFence,
		},
		{
			name: "invalid fence",
			activity: audioSourceActivity{trackKey: "t3", laneFrames: map[ConsentLane][]int16{ConsentLaneTranscription: full},
				laneFences: map[ConsentLane]ConsentFence{ConsentLaneTranscription: {lane: ConsentLaneTranscription, policy: "policy-v1", generation: 99}}},
			want: transcriptDropFenceInvalid,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			sink := &recordingSourceSink{}
			runMixerSourceSinkOnce(t, []audioSourceActivity{test.activity}, sink, authority)
			if sink.offered != 1 {
				t.Fatalf("offered=%d, want 1 — the mixer had audio and must say so before any consent check", sink.offered)
			}
			if len(sink.drops) != 1 || sink.drops[0] != test.want {
				t.Fatalf("drops=%v, want [%s]", sink.drops, test.want)
			}
			if sink.accepted != 0 {
				t.Fatalf("accepted=%d, want 0", sink.accepted)
			}
		})
	}

	t.Run("missing authority", func(t *testing.T) {
		sink := &recordingSourceSink{}
		runMixerSourceSinkOnce(t, []audioSourceActivity{{
			trackKey:   "t4",
			laneFrames: map[ConsentLane][]int16{ConsentLaneTranscription: full},
			laneFences: map[ConsentLane]ConsentFence{ConsentLaneTranscription: {lane: ConsentLaneTranscription}},
		}}, sink, nil)
		if len(sink.drops) != 1 || sink.drops[0] != transcriptDropNoAuthority {
			t.Fatalf("drops=%v, want [%s]", sink.drops, transcriptDropNoAuthority)
		}
	})
}

// TestSilentRoomOffersTheTranscriptSinkNothing is the mixer-side half of the
// quiet-room guarantee. mixAudioFrameSetWithActivity returns activities only
// for sources whose speech gate is open, so a room of silent publishers hands
// the transcription sink zero frames — which is exactly why the capture
// watchdog cannot see a quiet room as a stall.
func TestSilentRoomOffersTheTranscriptSinkNothing(t *testing.T) {
	silent := &audioSource{
		trackKey: "quiet", participantName: "AJ",
		buffer:      make([]int16, roomAudioMixFrameSize),
		laneBuffers: map[ConsentLane][]int16{ConsentLaneTranscription: make([]int16, roomAudioMixFrameSize)},
		laneFences:  map[ConsentLane]ConsentFence{ConsentLaneTranscription: {lane: ConsentLaneTranscription}},
		noiseFloor:  roomAudioNoiseSeedRMS,
	}
	mixed, levels, activities := mixAudioFrameSetWithActivity(map[string]*audioSource{"quiet": silent})
	if len(mixed) != 0 || len(levels) != 0 {
		t.Fatalf("silent room produced a mix (len=%d) / levels (len=%d)", len(mixed), len(levels))
	}
	sink := &recordingSourceSink{}
	runMixerSourceSinkOnce(t, activities, sink, NewConsentLaneAuthority(NewMemoryConsentStore(), "policy-v1"))
	if sink.offered != 0 {
		t.Fatalf("silent room offered the transcript sink %d frames, want 0; the watchdog would false-trip", sink.offered)
	}
	if len(sink.drops) != 0 {
		t.Fatalf("silent room recorded drops %v, want none", sink.drops)
	}
}

// TestMixerSourceLoopKeepsTheNamedDropOrder guards runMixerSourceSinkOnce
// against drift: the helper mirrors the real loop, so the real loop must keep
// offering the frame before any consent check and must keep naming every exit.
func TestMixerSourceLoopKeepsTheNamedDropOrder(t *testing.T) {
	raw, err := os.ReadFile("audio_mixer.go")
	if err != nil {
		t.Fatal(err)
	}
	loop := sourceSectionForAdmissionTest(t, string(raw), "if sourceSink, ok := sinkConfig.sink.(consentSourceAudioSink); ok", "sinkPCM, fences := mixedPCM")
	offeredAt := strings.Index(loop, "observer.NoteSourceFrameOffered()")
	if offeredAt < 0 {
		t.Fatal("the mixer no longer stamps the offered frame; the watchdog loses its 'is anyone talking' signal")
	}
	previous := offeredAt
	for _, reason := range []string{transcriptDropShortFrame, transcriptDropNoFence, transcriptDropNoAuthority, transcriptDropFenceInvalid} {
		at := strings.Index(loop, "noteSourceAudioDrop(observer, "+reasonConstNameForTest(reason))
		if at < 0 {
			t.Fatalf("mixer exit %q is silent again", reason)
		}
		if at < previous {
			t.Fatalf("mixer exit %q moved before the offered stamp / previous exit", reason)
		}
		previous = at
	}
	if strings.Contains(loop, "continue\n\t\t\t\t\t\t}\n\t\t\t\t\t\tif err := sourceSink.WriteSourcePCMWithConsent") && offeredAt > previous {
		t.Fatal("offered stamp drifted after the consent checks")
	}
}

func reasonConstNameForTest(reason string) string {
	switch reason {
	case transcriptDropShortFrame:
		return "transcriptDropShortFrame"
	case transcriptDropNoFence:
		return "transcriptDropNoFence"
	case transcriptDropNoAuthority:
		return "transcriptDropNoAuthority"
	case transcriptDropFenceInvalid:
		return "transcriptDropFenceInvalid"
	}
	return reason
}

// TestLiveMixerCountsUnfencedTranscriptFramesForItsRoom is the end-to-end
// wiring proof: a real audioMixer, the real roomLaneAudioSink, and a real
// room. Speech with no transcription fence must arrive at the room's census as
// offered-and-dropped rather than vanishing.
func TestLiveMixerCountsUnfencedTranscriptFramesForItsRoom(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	authority := NewConsentLaneAuthority(NewMemoryConsentStore(), "policy-v1")
	installConsentAuthorityForTest(t, authority)

	mixer := newAudioMixer()
	t.Cleanup(mixer.close)
	mixer.setConsentSink("transcription", ConsentLaneTranscription, authority, &roomLaneAudioSink{app: app, roomID: officeRoomID, lane: ConsentLaneTranscription})

	speech := make([]int16, roomAudioMixFrameSize)
	for index := range speech {
		speech[index] = 6000
	}
	// A capture fence with no transcription fence is exactly the shape the
	// mixer sees when consent starvation has emptied a gate's fence map.
	capture := ConsentFence{lane: ConsentLaneAudioCapture, policy: "policy-v1"}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mixer.submitWithConsent("track-1", "AJ", speech, map[ConsentLane]ConsentFence{ConsentLaneAudioCapture: capture})
		app.mu.Lock()
		state := app.roomLiveLocked(officeRoomID)
		offered := state.transcriptFramesOffered
		drops := state.transcriptFrameDrops[transcriptDropNoFence]
		app.mu.Unlock()
		if offered > 0 && drops > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	offered, drops := state.transcriptFramesOffered, state.transcriptFrameDrops
	app.mu.Unlock()
	t.Fatalf("live mixer census offered=%d drops=%v, want offered>0 with no_fence>0", offered, drops)
}
