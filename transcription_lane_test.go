package main

import (
	"encoding/binary"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTranscriptionLaneSessionConfigUsesStreamingTranscription(t *testing.T) {
	session := transcriptionLaneSessionConfig("gpt-realtime-whisper")

	if sessionType := session["type"]; sessionType != "session.update" {
		t.Fatalf("type=%v, want session.update", sessionType)
	}
	transcriptionSession := session["session"].(map[string]any)
	if sessionType := transcriptionSession["type"]; sessionType != "transcription" {
		t.Fatalf("session.type=%v, want transcription", sessionType)
	}

	audio := transcriptionSession["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	format := input["format"].(map[string]any)
	if formatType := format["type"]; formatType != "audio/pcm" {
		t.Fatalf("audio.input.format.type=%v, want audio/pcm", formatType)
	}
	if rate := format["rate"]; rate != transcriptionLaneInputSampleRate {
		t.Fatalf("audio.input.format.rate=%v, want %d", rate, transcriptionLaneInputSampleRate)
	}

	transcription := input["transcription"].(map[string]any)
	if model := transcription["model"]; model != "gpt-realtime-whisper" {
		t.Fatalf("audio.input.transcription.model=%v, want gpt-realtime-whisper", model)
	}
	if language := transcription["language"]; language != "en" {
		t.Fatalf("audio.input.transcription.language=%v, want en", language)
	}

	if turnDetection := input["turn_detection"]; turnDetection != nil {
		t.Fatalf("turn_detection=%v, want nil for manual transcript commits", turnDetection)
	}
}

func TestTranscriptionLaneWebSocketURLUsesTranscriptionIntent(t *testing.T) {
	got := transcriptionLaneWebSocketURL()
	if !strings.Contains(got, "intent=transcription") {
		t.Fatalf("websocket url=%q, want transcription intent", got)
	}
}

func TestRoomPCMForTranscriptionDownsamplesToPCM24k(t *testing.T) {
	got := roomPCMForTranscription([]int16{100, 300, -100, -300, 12, 14})
	if len(got) != 6 {
		t.Fatalf("encoded bytes len=%d, want 6", len(got))
	}

	samples := []int16{
		int16(binary.LittleEndian.Uint16(got[0:2])),
		int16(binary.LittleEndian.Uint16(got[2:4])),
		int16(binary.LittleEndian.Uint16(got[4:6])),
	}
	want := []int16{200, -200, 13}
	for index := range want {
		if samples[index] != want[index] {
			t.Fatalf("sample[%d]=%d, want %d", index, samples[index], want[index])
		}
	}
}

func TestSyntheticSilenceBypassesTranscriptLane(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	lane := &meetingTranscriptionLane{input: make(chan []int16, 1)}
	app.mu.Lock()
	app.transcriptLane = lane
	app.mu.Unlock()

	if err := app.WriteMixedPCM(make([]int16, roomAudioMixFrameSize)); err != nil {
		t.Fatalf("WriteMixedPCM silence: %v", err)
	}
	select {
	case <-lane.input:
		t.Fatal("synthetic silence should not be queued to the transcript lane")
	default:
	}

	speechFrame := make([]int16, roomAudioMixFrameSize)
	speechFrame[0] = 1000
	if err := app.WriteMixedPCM(speechFrame); err != nil {
		t.Fatalf("WriteMixedPCM speech: %v", err)
	}
	select {
	case <-lane.input:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("speech was not queued to the transcript lane")
	}
}

func TestTranscriptionLaneCompletedTranscriptWritesSourceMetadata(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("OPENAI_TRANSCRIPT_MODEL", "gpt-realtime-whisper")

	app := newKanbanBoardApp()
	app.handleTranscriptionLaneEvent([]byte(`{
		"type":"conversation.item.input_audio_transcription.completed",
		"event_id":"event-transcript-1",
		"item_id":"item-transcript-1",
		"transcript":"Tom and Tyler talked about Boot Barn."
	}`))

	entries := app.memorySnapshot(5)
	if len(entries) != 1 {
		t.Fatalf("memory entries=%d, want 1", len(entries))
	}
	entry := entries[0]
	if !strings.Contains(entry.Text, "Tom and Tyler talked about Boot Barn.") {
		t.Fatalf("entry text=%q, want transcript", entry.Text)
	}
	if source := entry.Metadata["source"]; source != "transcript_lane" {
		t.Fatalf("source=%q, want transcript_lane", source)
	}
	if model := entry.Metadata["model"]; model != "gpt-realtime-whisper" {
		t.Fatalf("model=%q, want gpt-realtime-whisper", model)
	}
}
