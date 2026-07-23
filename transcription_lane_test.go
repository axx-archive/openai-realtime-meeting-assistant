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

	// whisper rejects prompt/near-field live ("prompt not supported for this
	// model"), so they MUST be omitted for it — otherwise the session errors.
	if _, present := transcription["prompt"]; present {
		t.Fatalf("whisper config must NOT carry a prompt (API rejects it)")
	}
	if _, present := input["noise_reduction"]; present {
		t.Fatalf("whisper config must NOT carry noise_reduction")
	}
}

func TestTranscriptionLaneSessionConfigBiasesDomainVocabulary(t *testing.T) {
	// A4/E2: the authoritative persisted lane must carry the domain-vocabulary
	// prompt + near-field noise reduction, matching the Scout realtime peer.
	session := transcriptionLaneSessionConfig("gpt-4o-transcribe")
	input := session["session"].(map[string]any)["audio"].(map[string]any)["input"].(map[string]any)

	noiseReduction, ok := input["noise_reduction"].(map[string]any)
	if !ok {
		t.Fatalf("audio.input.noise_reduction missing/typed %T, want map", input["noise_reduction"])
	}
	if nrType := noiseReduction["type"]; nrType != "near_field" {
		t.Fatalf("noise_reduction.type=%v, want near_field", nrType)
	}

	transcription := input["transcription"].(map[string]any)
	prompt, ok := transcription["prompt"].(string)
	if !ok || strings.TrimSpace(prompt) == "" {
		t.Fatalf("transcription.prompt=%q, want non-empty domain prompt", prompt)
	}
	if !strings.Contains(prompt, "Boot Barn") {
		t.Fatalf("transcription.prompt=%q, want domain vocabulary bias (Boot Barn)", prompt)
	}
}

func TestDefaultTranscriptionLaneModelAcceptsPrompt(t *testing.T) {
	// A4/E2: the default persisted model must be one that honours the prompt.
	if defaultTranscriptionLaneModel != "gpt-4o-transcribe" {
		t.Fatalf("defaultTranscriptionLaneModel=%q, want gpt-4o-transcribe (prompt-capable)", defaultTranscriptionLaneModel)
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

func TestTranscriptionLaneCommitPaddingSamples(t *testing.T) {
	oneMixerFrame := roomPCMForTranscription(make([]int16, roomAudioMixFrameSize))
	frameSamples := transcriptionLaneAudioSamples(oneMixerFrame)
	if frameSamples != transcriptionLaneInputSampleRate/50 {
		t.Fatalf("frame samples=%d, want %d", frameSamples, transcriptionLaneInputSampleRate/50)
	}

	paddingSamples := transcriptionLaneCommitPaddingSamples(frameSamples)
	if got, want := frameSamples+paddingSamples, transcriptionLaneMinCommitSamples; got != want {
		t.Fatalf("padded samples=%d, want %d", got, want)
	}

	duration := time.Duration(frameSamples+paddingSamples) * time.Second / transcriptionLaneInputSampleRate
	if duration != 100*time.Millisecond {
		t.Fatalf("padded duration=%s, want 100ms", duration)
	}

	if padding := transcriptionLaneCommitPaddingSamples(transcriptionLaneMinCommitSamples); padding != 0 {
		t.Fatalf("padding at minimum=%d, want 0", padding)
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

func TestPausedRecordingBypassesTranscriptLaneAndMemory(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	admitMemberWithTranscriptConsentForTest(t, app, officeRoomID, "aj@shareability.com")
	lane := &meetingTranscriptionLane{input: make(chan []int16, 1)}
	app.mu.Lock()
	app.transcriptLane = lane
	app.mu.Unlock()

	initialRecording, ok := app.roomSnapshot()["recording"].(roomRecordingState)
	if !ok {
		t.Fatalf("initial recording snapshot type=%T, want roomRecordingState", app.roomSnapshot()["recording"])
	}
	if !initialRecording.Enabled {
		t.Fatal("new rooms should start with transcript recording enabled")
	}
	app.mu.Lock()
	app.scoutVoiceArmedAt = time.Now().UTC()
	app.scoutVoiceArmedUntil = time.Now().UTC().Add(time.Minute)
	app.scoutSpokenResponse = true
	app.scoutSpokenResponseSent = true
	app.scoutLastToolResultAt = time.Now().UTC()
	app.scoutLastToolResultName = "answer_memory_question"
	app.mu.Unlock()

	snapshot := app.setTranscriptRecording(false, "AJ")
	recording, ok := snapshot["recording"].(roomRecordingState)
	if !ok {
		t.Fatalf("recording snapshot type=%T, want roomRecordingState", snapshot["recording"])
	}
	if recording.Enabled {
		t.Fatal("recording enabled=true, want false after pause")
	}
	if recording.UpdatedBy != "AJ" {
		t.Fatalf("recording updatedBy=%q, want AJ", recording.UpdatedBy)
	}
	app.mu.Lock()
	wakeArmed := !app.scoutVoiceArmedAt.IsZero() || !app.scoutVoiceArmedUntil.IsZero()
	spokenPending := app.scoutSpokenResponse || app.scoutSpokenResponseSent
	lastToolResult := app.scoutLastToolResultAt
	lastToolName := app.scoutLastToolResultName
	app.mu.Unlock()
	if wakeArmed || spokenPending || !lastToolResult.IsZero() || lastToolName != "" {
		t.Fatalf("recording pause left Scout listening state armed: wake=%v spoken=%v lastToolAt=%s lastTool=%q", wakeArmed, spokenPending, lastToolResult, lastToolName)
	}

	speechFrame := make([]int16, roomAudioMixFrameSize)
	speechFrame[0] = 1000
	if err := app.WriteMixedPCM(speechFrame); err != nil {
		t.Fatalf("WriteMixedPCM paused recording: %v", err)
	}
	select {
	case <-lane.input:
		t.Fatal("paused recording should not queue speech to the transcript lane")
	default:
	}

	app.rememberTranscript(officeRoomID, kanbanRealtimeEvent{
		EventID:    "event-paused-transcript",
		ItemID:     "item-paused-transcript",
		Transcript: "This paused transcript should not be persisted.",
	}, "test", "test-model")
	if entries := app.memorySnapshot(5); len(entries) != 0 {
		t.Fatalf("memory entries=%d, want 0 while recording is paused", len(entries))
	}

	app.setTranscriptRecording(true, "AJ")
	attributeNextTranscriptForTest(app, officeRoomID, "AJ")
	app.rememberTranscript(officeRoomID, kanbanRealtimeEvent{
		EventID:    "event-resumed-transcript",
		ItemID:     "item-resumed-transcript",
		Transcript: "This resumed transcript should be persisted.",
	}, "test", "test-model")
	entries := app.memorySnapshot(5)
	if len(entries) != 1 {
		t.Fatalf("memory entries=%d, want 1 after recording resumes", len(entries))
	}
	if !strings.Contains(entries[0].Text, "resumed transcript") {
		t.Fatalf("entry text=%q, want resumed transcript", entries[0].Text)
	}
}

func TestTranscriptionLaneCompletedTranscriptWritesSourceMetadata(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("OPENAI_TRANSCRIPT_MODEL", "gpt-realtime-whisper")

	app := newKanbanBoardApp()
	admitMemberWithTranscriptConsentForTest(t, app, officeRoomID, "tom@shareability.com")
	attributeNextTranscriptForTest(app, officeRoomID, "Tom")
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

// W0-5: the per-room committed-seconds FIFO pops in commit order, returns 0
// when drained (a terminal event for a pre-reconnect commit), and caps its
// depth instead of growing without bound.
func TestTranscriptionSegmentSecondsQueueOrderAndCap(t *testing.T) {
	room := "room-fifo-test"
	resetTranscriptionSegmentSecondsForRoom(room)
	t.Cleanup(func() { resetTranscriptionSegmentSecondsForRoom(room) })

	if got := popTranscriptionSegmentSeconds(room); got != 0 {
		t.Fatalf("empty pop = %v, want 0", got)
	}
	pushTranscriptionSegmentSeconds(room, 1.5)
	pushTranscriptionSegmentSeconds(room, 2.5)
	if got := popTranscriptionSegmentSeconds(room); got != 1.5 {
		t.Fatalf("first pop = %v, want commit order (1.5)", got)
	}
	if got := popTranscriptionSegmentSeconds(room); got != 2.5 {
		t.Fatalf("second pop = %v, want 2.5", got)
	}
	if got := popTranscriptionSegmentSeconds(room); got != 0 {
		t.Fatalf("drained pop = %v, want 0", got)
	}

	for i := 0; i < transcriptionSegmentSecondsCap+10; i++ {
		pushTranscriptionSegmentSeconds(room, float64(i))
	}
	// The oldest 10 fell off; the queue holds exactly the cap, oldest first.
	if got := popTranscriptionSegmentSeconds(room); got != 10 {
		t.Fatalf("post-cap first pop = %v, want 10 (oldest evicted)", got)
	}
	drained := 1
	for popTranscriptionSegmentSeconds(room) != 0 {
		drained++
	}
	if drained != transcriptionSegmentSecondsCap {
		t.Fatalf("queue depth = %d, want the cap %d", drained, transcriptionSegmentSecondsCap)
	}

	resetTranscriptionSegmentSecondsForRoom(room)
	pushTranscriptionSegmentSeconds(room, 9)
	resetTranscriptionSegmentSecondsForRoom(room)
	if got := popTranscriptionSegmentSeconds(room); got != 0 {
		t.Fatalf("reset must clear the room queue, popped %v", got)
	}
}

// W0-5 lane metering: a committed segment writes one duration-billed usage row
// (seat transcription_lane, AudioSeconds, per-minute est cost) and queues its
// duration so the terminal .completed/.failed event stamps audio_seconds onto
// its transcript_segment eval event.
func TestTranscriptionLaneCommitMetersSegmentAndTerminalEventsPop(t *testing.T) {
	dir := ledgerTestDir(t)
	fixed := time.Date(2026, time.July, 11, 16, 0, 0, 0, time.UTC)
	prevNow := usageLedgerNow
	usageLedgerNow = func() time.Time { return fixed }
	defer func() { usageLedgerNow = prevNow }()

	app := newIsolatedKanbanBoardApp(t)
	room := "room-meter-test"
	resetTranscriptionSegmentSecondsForRoom(room)
	t.Cleanup(func() { resetTranscriptionSegmentSecondsForRoom(room) })

	lane := newMeetingTranscriptionLaneForRoom(app, "test-key", "gpt-4o-transcribe", room)
	lane.noteCommittedSegment(2 * transcriptionLaneInputSampleRate) // 2.0s
	lane.noteCommittedSegment(transcriptionLaneInputSampleRate / 2) // 0.5s

	rows := readLedgerLines(t, filepath.Join(dir, "usage-2026-07-11.jsonl"))
	if len(rows) != 2 {
		t.Fatalf("usage rows = %d, want one per committed segment", len(rows))
	}
	first := rows[0]
	if first["provider"] != providerOpenAI || first["model"] != "gpt-4o-transcribe" ||
		first["seat"] != seatTranscriptionLane || first["room_id"] != room {
		t.Fatalf("segment row identity wrong: %v", first)
	}
	if got := first["audio_seconds"].(float64); !floatClose(got, 2.0) {
		t.Fatalf("audio_seconds = %v, want 2.0", got)
	}
	// Duration-billed: 2s of gpt-4o-transcribe at $0.006/min.
	if got := first["est_cost_usd"].(float64); !floatClose(got, 2.0/60*0.006) {
		t.Fatalf("est_cost_usd = %v, want %v", got, 2.0/60*0.006)
	}

	// .completed pops the first committed duration, .failed the next; neither
	// requests a reconnect.
	if app.handleTranscriptionLaneEventForRoom(room, []byte(`{
		"type":"conversation.item.input_audio_transcription.completed",
		"event_id":"event-meter-1",
		"item_id":"item-meter-1",
		"transcript":"Tom reviewed the launch checklist for the week."
	}`), "gpt-4o-transcribe") {
		t.Fatal("completed event must not request reconnect")
	}
	if app.handleTranscriptionLaneEventForRoom(room, []byte(`{
		"type":"conversation.item.input_audio_transcription.failed"
	}`), "gpt-4o-transcribe") {
		t.Fatal("failed event must not request reconnect")
	}

	evalRows := readLedgerLines(t, filepath.Join(dir, "eval-2026-07-11.jsonl"))
	segments := []map[string]any{}
	for _, row := range evalRows {
		if row["kind"] == evalKindTranscriptSegment {
			segments = append(segments, row)
		}
	}
	if len(segments) != 2 {
		t.Fatalf("transcript_segment rows = %d, want 2: %v", len(segments), evalRows)
	}
	completed := segments[0]["fields"].(map[string]any)
	if completed["status"] != "completed" || completed["room_id"] != room {
		t.Fatalf("completed fields = %v", completed)
	}
	if got := completed["audio_seconds"].(float64); !floatClose(got, 2.0) {
		t.Fatalf("completed audio_seconds = %v, want the first committed 2.0", got)
	}
	failed := segments[1]["fields"].(map[string]any)
	if failed["status"] != "failed" {
		t.Fatalf("failed fields = %v", failed)
	}
	if got := failed["audio_seconds"].(float64); !floatClose(got, 0.5) {
		t.Fatalf("failed audio_seconds = %v, want the second committed 0.5", got)
	}
	if segments[0]["lane"] != seatTranscriptionLane || segments[1]["lane"] != seatTranscriptionLane {
		t.Fatalf("transcript_segment lane wrong: %v / %v", segments[0]["lane"], segments[1]["lane"])
	}
}

func TestTranscriptionLaneSessionExpiredRequestsReconnect(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	reconnect := app.handleTranscriptionLaneEvent([]byte(`{
		"type":"error",
		"error":{
			"code":"session_expired",
			"message":"Your session hit the maximum duration of 60 minutes."
		}
	}`))
	if !reconnect {
		t.Fatal("session_expired should request transcript lane reconnect")
	}

	reconnect = app.handleTranscriptionLaneEvent([]byte(`{
		"type":"error",
		"error":{
			"code":"invalid_request_error",
			"message":"bad event"
		}
	}`))
	if reconnect {
		t.Fatal("non-expiry errors should not request transcript lane reconnect")
	}
}
