package main

import (
	"testing"
	"time"
)

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
