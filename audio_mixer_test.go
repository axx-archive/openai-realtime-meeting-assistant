package main

import "testing"

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

func TestMixAudioFrameAveragesActiveSpeakers(t *testing.T) {
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
		if sample != 1500 {
			t.Fatalf("mixed sample[%d]=%d, want 1500", index, sample)
		}
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
