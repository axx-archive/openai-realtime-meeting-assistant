package main

import (
	"encoding/json"
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
	if vadType := turnDetection["type"]; vadType != "server_vad" {
		t.Fatalf("turn_detection.type=%v, want server_vad", vadType)
	}
	if silence := turnDetection["silence_duration_ms"]; silence != 300 {
		t.Fatalf("turn_detection.silence_duration_ms=%v, want 300", silence)
	}
	if interrupt := turnDetection["interrupt_response"]; interrupt != true {
		t.Fatalf("turn_detection.interrupt_response=%v, want true", interrupt)
	}
}

func TestRealtimeConfigEnvironmentOverrides(t *testing.T) {
	t.Setenv("OPENAI_REALTIME_REASONING_EFFORT", "xhigh")
	t.Setenv("OPENAI_REALTIME_VAD_TYPE", "semantic_vad")
	t.Setenv("OPENAI_REALTIME_VAD_EAGERNESS", "low")

	if effort := realtimeReasoningEffort(); effort != "xhigh" {
		t.Fatalf("reasoning effort=%q, want xhigh", effort)
	}
	turnDetection := realtimeTurnDetectionConfig()
	if vadType := turnDetection["type"]; vadType != "semantic_vad" {
		t.Fatalf("turn_detection.type=%v, want semantic_vad", vadType)
	}
	if eagerness := turnDetection["eagerness"]; eagerness != "low" {
		t.Fatalf("turn_detection.eagerness=%v, want low", eagerness)
	}
	if interrupt := turnDetection["interrupt_response"]; interrupt != true {
		t.Fatalf("turn_detection.interrupt_response=%v, want true", interrupt)
	}
}

func TestBrowserRTCConfigurationSupportsTurnFallback(t *testing.T) {
	t.Setenv("MEETING_STUN_URLS", "stun:stun.example.com:3478")
	t.Setenv("MEETING_TURN_URLS", "turn:turn.example.com:3478,turns:turn.example.com:5349")
	t.Setenv("MEETING_TURN_USERNAME", "meeting")
	t.Setenv("MEETING_TURN_CREDENTIAL", "secret")

	config := browserRTCConfigurationFromEnv()
	servers, ok := config["iceServers"].([]map[string]any)
	if !ok {
		t.Fatalf("iceServers missing from config: %#v", config)
	}
	if len(servers) != 2 {
		t.Fatalf("iceServers len=%d, want 2", len(servers))
	}
	if got := servers[0]["urls"].([]string); !sameStringSlice(got, []string{"stun:stun.example.com:3478"}) {
		t.Fatalf("stun urls=%v", got)
	}
	if got := servers[1]["urls"].([]string); !sameStringSlice(got, []string{"turn:turn.example.com:3478", "turns:turn.example.com:5349"}) {
		t.Fatalf("turn urls=%v", got)
	}
	if servers[1]["username"] != "meeting" || servers[1]["credential"] != "secret" {
		t.Fatalf("turn credentials missing: %#v", servers[1])
	}
}

func TestRealtimeToolsExposeKeyDateMutations(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	rawTools, err := json.Marshal(app.kanbanTools())
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	toolsJSON := string(rawTools)
	for _, want := range []string{`"name":"add_key_date"`, `"name":"remove_key_dates"`, `"replace_key_dates"`, `"due_date"`, `"key_dates"`} {
		if !strings.Contains(toolsJSON, want) {
			t.Fatalf("tools JSON missing %s: %s", want, toolsJSON)
		}
	}
	if instructions := app.sessionInstructions(); !strings.Contains(instructions, "add_key_date") || !strings.Contains(instructions, "remove_key_dates") || !strings.Contains(instructions, "key dates") {
		t.Fatalf("session instructions missing key-date guidance: %s", instructions)
	}
}

func TestRealtimeReasoningEffortAcceptsMinimal(t *testing.T) {
	t.Setenv("OPENAI_REALTIME_REASONING_EFFORT", "minimal")

	if effort := realtimeReasoningEffort(); effort != "minimal" {
		t.Fatalf("reasoning effort=%q, want minimal", effort)
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

func TestScoutSpokenResponseWaitsForActiveRealtimeResponseToFinish(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.scoutSpokenResponse = true
	app.scoutSpokenResponseSent = false
	app.realtimeResponseActive = true
	app.mu.Unlock()

	app.flushScoutSpokenResponseIfPending()

	app.mu.Lock()
	defer app.mu.Unlock()
	if !app.scoutSpokenResponse {
		t.Fatal("pending spoken response should remain queued while a realtime response is active")
	}
	if app.scoutSpokenResponseSent {
		t.Fatal("spoken response should not be marked sent while a realtime response is active")
	}
}

func TestRealtimeFunctionCallArgumentsDoneUsesNestedItem(t *testing.T) {
	item := realtimeFunctionCallFromArgumentsDone(kanbanRealtimeEvent{
		Type:      "response.function_call_arguments.done",
		Name:      "answer_memory_question",
		Arguments: `{"query":"truncated`,
		CallID:    "call-top-level",
		Item: &kanbanRealtimeOutputItem{
			Type:      "function_call",
			Name:      "answer_memory_question",
			Arguments: `{"query":"Dog Perfect status"}`,
			CallID:    "call-nested",
		},
	})

	if item.CallID != "call-nested" {
		t.Fatalf("call_id=%q, want nested call id", item.CallID)
	}
	if item.Arguments != `{"query":"Dog Perfect status"}` {
		t.Fatalf("arguments=%q, want nested item arguments", item.Arguments)
	}
}

func TestHandleToolCallWaitsForCompleteArgumentsBeforeDedupe(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	callID := "call-dog-perfect"

	app.handleToolCall(kanbanRealtimeOutputItem{
		Type:      "function_call",
		Name:      "create_ticket",
		CallID:    callID,
		Arguments: `{"title":"Dog Perfect"`,
	}, true)

	app.mu.Lock()
	_, handled := app.handledCalls[callID]
	app.mu.Unlock()
	if handled {
		t.Fatal("incomplete arguments should not mark the call as handled")
	}

	app.handleToolCall(kanbanRealtimeOutputItem{
		Type:   "function_call",
		Name:   "create_ticket",
		CallID: callID,
		Arguments: `{
			"title":"Dog Perfect",
			"notes":"Waiting on Erick for launch approval.",
			"owner":"Erick",
			"tags":["client"],
			"status":"Blocked"
		}`,
	}, false)

	found := false
	for _, card := range app.snapshotState().Cards {
		if card.Title == "Dog Perfect" && card.Status == kanbanStatusBlocked {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("complete retry did not create the Dog Perfect card")
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
