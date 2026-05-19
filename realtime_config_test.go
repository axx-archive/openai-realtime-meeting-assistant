package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRealtimeSessionConfigUsesGptRealtime2Optimizations(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("OPENAI_REALTIME_REASONING_EFFORT", "")
	t.Setenv("OPENAI_REALTIME_VAD_TYPE", "")
	t.Setenv("OPENAI_REALTIME_VAD_EAGERNESS", "")

	app := newKanbanBoardApp()
	session := app.sessionConfig("gpt-realtime-2")

	reasoning, ok := session["reasoning"].(map[string]any)
	if !ok {
		t.Fatal("session reasoning config missing")
	}
	if effort := reasoning["effort"]; effort != defaultReasoningEffort {
		t.Fatalf("reasoning effort=%v, want %s", effort, defaultReasoningEffort)
	}

	audio := session["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	output := audio["output"].(map[string]any)
	noiseReduction := input["noise_reduction"].(map[string]any)
	if noiseType := noiseReduction["type"]; noiseType != "near_field" {
		t.Fatalf("audio.input.noise_reduction.type=%v, want near_field", noiseType)
	}
	if voice := output["voice"]; voice != defaultRealtimeVoice {
		t.Fatalf("audio.output.voice=%v, want %s", voice, defaultRealtimeVoice)
	}
	if got, want := session["output_modalities"], []string{"audio"}; !sameStringSlice(got.([]string), want) {
		t.Fatalf("output_modalities=%v, want %v", got, want)
	}
	transcription := input["transcription"].(map[string]any)
	if model := transcription["model"]; model != defaultRealtimeTranscriptionModel {
		t.Fatalf("transcription.model=%v, want %s", model, defaultRealtimeTranscriptionModel)
	}
	prompt, ok := transcription["prompt"].(string)
	if !ok || !strings.Contains(prompt, "Boot Barn") || !strings.Contains(prompt, "WebRTC") {
		t.Fatalf("transcription prompt missing domain vocabulary: %v", transcription["prompt"])
	}
	turnDetection := input["turn_detection"].(map[string]any)
	if vadType := turnDetection["type"]; vadType != "semantic_vad" {
		t.Fatalf("turn_detection.type=%v, want semantic_vad", vadType)
	}
	if eagerness := turnDetection["eagerness"]; eagerness != defaultVADEagerness {
		t.Fatalf("turn_detection.eagerness=%v, want %s", eagerness, defaultVADEagerness)
	}
	if interrupt := turnDetection["interrupt_response"]; interrupt != true {
		t.Fatalf("turn_detection.interrupt_response=%v, want true", interrupt)
	}
}

func TestRealtimeConfigEnvironmentOverrides(t *testing.T) {
	t.Setenv("OPENAI_REALTIME_REASONING_EFFORT", "high")
	t.Setenv("OPENAI_REALTIME_VAD_TYPE", "server_vad")
	t.Setenv("OPENAI_REALTIME_VAD_EAGERNESS", "low")

	if effort := realtimeReasoningEffort(); effort != "high" {
		t.Fatalf("reasoning effort=%q, want high", effort)
	}
	turnDetection := realtimeTurnDetectionConfig()
	if vadType := turnDetection["type"]; vadType != "server_vad" {
		t.Fatalf("turn_detection.type=%v, want server_vad", vadType)
	}
	if _, ok := turnDetection["eagerness"]; ok {
		t.Fatal("server_vad config should not include semantic eagerness")
	}
	if interrupt := turnDetection["interrupt_response"]; interrupt != true {
		t.Fatalf("server_vad interrupt_response=%v, want true", interrupt)
	}
}

func TestRealtimeVoiceEnvironmentOverride(t *testing.T) {
	t.Setenv("OPENAI_REALTIME_VOICE", "cedar")

	if voice := realtimeVoice(); voice != "cedar" {
		t.Fatalf("realtimeVoice=%q, want cedar", voice)
	}
}

func TestScoutWakePhraseRequiresLeadingHeyScout(t *testing.T) {
	for _, transcript := range []string{
		"Hey Scout, what is blocked?",
		"hey scout what did Tim commit to last week",
		"Hey, Scout: summarize this meeting.",
	} {
		if !transcriptStartsWithScoutWakePhrase(transcript) {
			t.Fatalf("wake phrase was not detected in %q", transcript)
		}
	}

	for _, transcript := range []string{
		"Can you ask Scout what is blocked?",
		"Scout, what is blocked?",
		"They said hey Scout in the last meeting.",
		"Hey team, Scout should ignore this.",
		"",
	} {
		if transcriptStartsWithScoutWakePhrase(transcript) {
			t.Fatalf("wake phrase should not be detected in %q", transcript)
		}
	}
}

func TestDetectsRealtimeActiveResponseErrors(t *testing.T) {
	event := kanbanRealtimeEvent{
		Error: &struct {
			Code    string `json:"code,omitempty"`
			Message string `json:"message,omitempty"`
		}{
			Code:    "invalid_request_error",
			Message: "Conversation already has an active response in progress: resp_123. Wait until the response is finished before creating a new one.",
		},
	}
	if !isRealtimeActiveResponseError(event) {
		t.Fatal("active response error was not detected")
	}
}

func TestUpdateTicketAppliesRichRealtimeChangesAtomically(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))

	app := newKanbanBoardApp()
	createResult, changed, err := app.createTicket(map[string]any{
		"title":  "Billing launch",
		"notes":  "Initial rollout task.",
		"owner":  "AJ",
		"tags":   []any{"billing"},
		"status": "Backlog",
	})
	if err != nil {
		t.Fatalf("createTicket: %v", err)
	}
	if !changed {
		t.Fatal("createTicket changed=false, want true")
	}
	card := createResult["card"].(kanbanCard)

	if _, changed, err := app.updateTicket(map[string]any{
		"card_id": card.ID,
		"notes":   "Blocked by finance approval.",
		"owner":   "Tim",
		"tags":    []any{"blocked", "risk"},
		"status":  "Blocked",
	}); err != nil {
		t.Fatalf("updateTicket: %v", err)
	} else if !changed {
		t.Fatal("updateTicket changed=false, want true")
	}

	updated, ok := findSnapshotCard(app.snapshotState().Cards, card.ID)
	if !ok {
		t.Fatalf("updated card %q not found", card.ID)
	}
	if updated.Status != kanbanStatusBlocked {
		t.Fatalf("status=%q, want %q", updated.Status, kanbanStatusBlocked)
	}
	if updated.Owner != "Tim" {
		t.Fatalf("owner=%q, want Tim", updated.Owner)
	}
	if updated.Notes != "Blocked by finance approval." {
		t.Fatalf("notes=%q, want blocker note", updated.Notes)
	}
	if got, want := updated.Tags, []string{"billing", "blocked", "risk"}; !sameStringSlice(got, want) {
		t.Fatalf("tags=%v, want %v", got, want)
	}
}

func sameStringSlice(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

func findSnapshotCard(cards []kanbanCard, cardID string) (kanbanCard, bool) {
	for _, card := range cards {
		if card.ID == cardID {
			return card, true
		}
	}

	return kanbanCard{}, false
}
