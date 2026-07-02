package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
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
	if effort := reasoning["effort"]; effort != "high" {
		t.Fatalf("reasoning effort=%v, want high", effort)
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
	if toolChoice := session["tool_choice"]; toolChoice != "required" {
		t.Fatalf("tool_choice=%v, want required while voice control is inactive", toolChoice)
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

func TestRealtimeVoiceControlSessionAllowsDirectAnswers(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.setVoiceControlActive(true, "AJ")

	session := app.sessionConfig("gpt-realtime-2")
	if toolChoice := session["tool_choice"]; toolChoice != "auto" {
		t.Fatalf("tool_choice=%v, want auto while voice control is active", toolChoice)
	}
	instructions := session["instructions"].(string)
	for _, want := range []string{"shared room Realtime 2 Scout", "private Scout chat outside the room", "answer simple capability", "directly unless a listed tool is needed"} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("voice-control instructions missing %q: %s", want, instructions)
		}
	}
}

func TestPrivateRealtimeVoiceSessionStaysOutsideRoom(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	session := app.privateRealtimeVoiceSessionConfig("gpt-realtime-2")

	tools, ok := session["tools"].([]map[string]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("private dashboard Realtime voice tools=%T, want constrained OS tool list", session["tools"])
	}
	allowed := map[string]bool{
		"control_app":            true,
		"create_artifact":        true,
		"launch_agent_thread":    true,
		"answer_memory_question": true,
		"send_notification":      true,
		"do_nothing":             true,
	}
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		if !allowed[name] {
			t.Fatalf("private dashboard Realtime voice inherited disallowed tool %q", name)
		}
		delete(allowed, name)
	}
	for missing := range allowed {
		t.Fatalf("private dashboard Realtime voice missing OS tool %q", missing)
	}
	if toolChoice := session["tool_choice"]; toolChoice != "auto" {
		t.Fatalf("tool_choice=%v, want auto for private dashboard voice", toolChoice)
	}
	instructions := session["instructions"].(string)
	for _, want := range []string{
		"private Bonfire OS voice assistant",
		"outside the video room",
		"Do not describe yourself as the shared room Scout",
		"do not mutate the shared Kanban board",
		"Use launch_agent_thread",
		"Use board context only when the user explicitly asks about board, card, task, status, owner, or due-date information",
		"do not volunteer board status for unclear follow-ups like \"what?\"",
	} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("private voice instructions missing %q: %s", want, instructions)
		}
	}
}

func TestRealtimeCallRequestUsesTypedMultipartParts(t *testing.T) {
	contentType, body, err := buildRealtimeCallRequest("v=0\n", map[string]any{
		"type":  "realtime",
		"model": "gpt-realtime-2",
	})
	if err != nil {
		t.Fatalf("build realtime request: %v", err)
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	if mediaType != "multipart/form-data" {
		t.Fatalf("content type=%q, want multipart/form-data", mediaType)
	}

	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	parts := map[string]struct {
		contentType string
		body        string
	}{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		raw, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read part body: %v", err)
		}
		parts[part.FormName()] = struct {
			contentType string
			body        string
		}{
			contentType: part.Header.Get("Content-Type"),
			body:        string(raw),
		}
	}

	if parts["sdp"].contentType != "application/sdp" {
		t.Fatalf("sdp content type=%q, want application/sdp", parts["sdp"].contentType)
	}
	if parts["sdp"].body != "v=0\r\n" {
		t.Fatalf("sdp body=%q, want CRLF-normalized offer", parts["sdp"].body)
	}
	if parts["session"].contentType != "application/json" {
		t.Fatalf("session content type=%q, want application/json", parts["session"].contentType)
	}
	if !strings.Contains(parts["session"].body, `"model":"gpt-realtime-2"`) {
		t.Fatalf("session body missing realtime model: %s", parts["session"].body)
	}
}

func TestNormalizeRealtimeSDPRequiresValidSessionDescription(t *testing.T) {
	normalized, err := normalizeRealtimeSDP("v=0\no=- 0 0 IN IP4 127.0.0.1\na=ice-pwd:abc1234567890123456789")
	if err != nil {
		t.Fatalf("normalize SDP: %v", err)
	}
	if normalized != "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\na=ice-pwd:abc1234567890123456789\r\n" {
		t.Fatalf("normalized SDP=%q", normalized)
	}

	if _, err := normalizeRealtimeSDP(`{"error":"not sdp"}`); err == nil {
		t.Fatal("expected non-SDP payload to fail validation")
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

func TestBrowserRTCConfigurationSupportsEphemeralTurnCredentials(t *testing.T) {
	t.Setenv("MEETING_STUN_URLS", "")
	t.Setenv("MEETING_DISABLE_DEFAULT_STUN", "true")
	t.Setenv("MEETING_TURN_URLS", "turn:thebonfire.xyz:3478?transport=udp,turn:thebonfire.xyz:3478?transport=tcp")
	t.Setenv("MEETING_TURN_USERNAME", "")
	t.Setenv("MEETING_TURN_CREDENTIAL", "")
	t.Setenv("MEETING_TURN_SECRET", "shared-secret-for-tests")
	t.Setenv("MEETING_TURN_TTL_SECONDS", "3600")

	config := browserRTCConfigurationFromEnv()
	servers, ok := config["iceServers"].([]map[string]any)
	if !ok {
		t.Fatalf("iceServers missing from config: %#v", config)
	}
	if len(servers) != 1 {
		t.Fatalf("iceServers len=%d, want 1", len(servers))
	}
	username, _ := servers[0]["username"].(string)
	credential, _ := servers[0]["credential"].(string)
	if !strings.Contains(username, ":bonfire") || credential == "" {
		t.Fatalf("ephemeral turn credentials missing: %#v", servers[0])
	}
	if servers[0]["credentialType"] != "password" {
		t.Fatalf("turn credentialType=%v, want password", servers[0]["credentialType"])
	}
}

func TestBrowserRTCConfigurationDefaultsToPublicStun(t *testing.T) {
	t.Setenv("MEETING_STUN_URLS", "")
	t.Setenv("MEETING_TURN_URLS", "")
	t.Setenv("MEETING_ICE_SERVERS_JSON", "")
	t.Setenv("MEETING_DISABLE_DEFAULT_STUN", "")

	config := browserRTCConfigurationFromEnv()
	servers, ok := config["iceServers"].([]map[string]any)
	if !ok {
		t.Fatalf("iceServers missing from config: %#v", config)
	}
	if len(servers) != 1 {
		t.Fatalf("iceServers len=%d, want default stun only", len(servers))
	}
	if got := servers[0]["urls"].([]string); !sameStringSlice(got, []string{"stun:stun.l.google.com:19302"}) {
		t.Fatalf("default stun urls=%v", got)
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

func TestRealtimeToolsExposeOSControlAndArtifacts(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	rawTools, err := json.Marshal(app.kanbanTools())
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	toolsJSON := string(rawTools)
	for _, want := range []string{`"name":"control_app"`, `"name":"set_voice_control"`, `"name":"set_recording"`, `"name":"archive_meeting"`, `"name":"undo_delete_ticket"`, `"name":"create_artifact"`, `"name":"launch_agent_thread"`, `"name":"update_artifact"`, `"name":"publish_artifact"`, `"artifacts"`, `"research"`, `"workflow"`, "conversational thread", "agent-workforce thread", "latest published", `"memory"`, "local mic"} {
		if !strings.Contains(toolsJSON, want) {
			t.Fatalf("tools JSON missing %s: %s", want, toolsJSON)
		}
	}
	instructions := app.sessionInstructions()
	for _, want := range []string{"Bonfire OS voice operator", "control_app", "set_voice_control", "set_recording", "archive_meeting", "undo_delete_ticket", "update_artifact", "publish_artifact", "browser and device permissions", "pinning a speaker", "create_artifact", "launch_agent_thread", "goal workflow", "conversational thread", "agent workforce", "vision", "Latest published artifacts", "Voice control mode"} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("session instructions missing %q: %s", want, instructions)
		}
	}
	if !realtimeToolRunsAsync("archive_meeting") {
		t.Fatal("archive_meeting should run async because it writes archives and artifacts")
	}
	if !realtimeToolRunsAsync("launch_agent_thread") {
		t.Fatal("launch_agent_thread should run async because it creates worker artifacts")
	}
}

func TestRealtimeControlAppReturnsOSActions(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	result, changed, err := app.applyToolCallArgs("control_app", map[string]any{
		"tool":        "artifacts",
		"artifact_id": "os-artifact-research-1",
	})
	if err != nil {
		t.Fatalf("control_app: %v", err)
	}
	if changed {
		t.Fatal("control_app changed board state")
	}
	actions, ok := result["actions"].([]osAssistantAction)
	if !ok {
		t.Fatalf("actions type=%T, want []osAssistantAction", result["actions"])
	}
	if !hasAssistantAction(actions, "open_tool", "artifacts", "os-artifact-research-1") ||
		!hasAssistantAction(actions, "select_artifact", "artifacts", "os-artifact-research-1") {
		t.Fatalf("actions=%#v, want artifact navigation", actions)
	}

	result, changed, err = app.applyToolCallArgs("control_app", map[string]any{
		"tool": "chat",
	})
	if err != nil {
		t.Fatalf("control_app chat: %v", err)
	}
	if changed {
		t.Fatal("control_app chat changed board state")
	}
	actions, ok = result["actions"].([]osAssistantAction)
	if !ok {
		t.Fatalf("chat actions type=%T, want []osAssistantAction", result["actions"])
	}
	if !hasAssistantAction(actions, "open_tool", "chat", "") {
		t.Fatalf("actions=%#v, want chat open_tool", actions)
	}
}

func TestRealtimeVoiceControlCanStopListening(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.setVoiceControlActive(true, "AJ")

	result, changed, err := app.applyToolCallArgs("set_voice_control", map[string]any{
		"enabled": false,
	})
	if err != nil {
		t.Fatalf("set_voice_control false: %v", err)
	}
	if changed {
		t.Fatal("set_voice_control changed board state")
	}
	if app.voiceControlEnabled() {
		t.Fatal("voice control still active after realtime stop")
	}
	actions, ok := result["actions"].([]osAssistantAction)
	if !ok || len(actions) != 1 {
		t.Fatalf("actions=%#v, want one set_voice_control action", result["actions"])
	}
	if actions[0].Type != "set_voice_control" || actions[0].Enabled == nil || *actions[0].Enabled {
		t.Fatalf("action=%#v, want set_voice_control enabled=false", actions[0])
	}
}

func TestRealtimeSetRecordingControlsTranscriptCapture(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	result, changed, err := app.applyToolCallArgs("set_recording", map[string]any{
		"enabled": false,
	})
	if err != nil {
		t.Fatalf("set_recording false: %v", err)
	}
	if changed {
		t.Fatal("set_recording changed board state")
	}
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("ok=%v, want true", result["ok"])
	}
	recording, ok := result["recording"].(roomRecordingState)
	if !ok {
		t.Fatalf("recording type=%T, want roomRecordingState", result["recording"])
	}
	if recording.Enabled {
		t.Fatal("recording enabled=true, want false")
	}
	if recording.UpdatedBy != scoutParticipantName {
		t.Fatalf("recording updatedBy=%q, want %q", recording.UpdatedBy, scoutParticipantName)
	}
	if message, _ := result["message"].(string); !strings.Contains(message, "off for the room") {
		t.Fatalf("message=%q, want room-wide off announcement", message)
	}
	if app.transcriptRecordingActive() {
		t.Fatal("transcript recording still active after realtime pause")
	}

	result, _, err = app.applyToolCallArgs("set_recording", map[string]any{
		"enabled": true,
	})
	if err != nil {
		t.Fatalf("set_recording true: %v", err)
	}
	recording, ok = result["recording"].(roomRecordingState)
	if !ok || !recording.Enabled {
		t.Fatalf("recording=%#v, want enabled roomRecordingState", result["recording"])
	}
	if !app.transcriptRecordingActive() {
		t.Fatal("transcript recording inactive after realtime resume")
	}
	if message, _ := result["message"].(string); !strings.Contains(message, "on for the room") {
		t.Fatalf("message=%q, want room-wide on announcement", message)
	}
}

func TestRoomRecordingAnnouncementNamesActor(t *testing.T) {
	manual := roomRecordingAnnouncementText(roomRecordingState{
		Enabled:   false,
		UpdatedBy: "AJ",
	})
	if manual != "Scout: AJ turned meeting recording off for the room." {
		t.Fatalf("manual announcement=%q", manual)
	}

	scout := roomRecordingAnnouncementText(roomRecordingState{
		Enabled:   true,
		UpdatedBy: scoutParticipantName,
	})
	if scout != "Scout: meeting recording is on for the room." {
		t.Fatalf("scout announcement=%q", scout)
	}
}

func TestRealtimeUndoDeleteRestoresLastDeletedCard(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	created, changed, err := app.applyToolCallArgs("create_ticket", map[string]any{
		"title":  "Restore me",
		"notes":  "Card to delete and restore.",
		"owner":  "AJ",
		"tags":   []any{"qa"},
		"status": "Backlog",
	})
	if err != nil {
		t.Fatalf("create_ticket: %v", err)
	}
	if !changed {
		t.Fatal("create_ticket changed=false, want true")
	}
	card, ok := created["card"].(kanbanCard)
	if !ok || card.ID == "" {
		t.Fatalf("created card missing: %#v", created)
	}
	if _, changed, err = app.applyToolCallArgs("delete_ticket", map[string]any{"card_id": card.ID}); err != nil || !changed {
		t.Fatalf("delete_ticket changed=%v err=%v, want changed nil err", changed, err)
	}

	result, changed, err := app.applyToolCallArgs("undo_delete_ticket", map[string]any{})
	if err != nil {
		t.Fatalf("undo_delete_ticket: %v", err)
	}
	if !changed {
		t.Fatal("undo_delete_ticket changed=false, want true")
	}
	if restored, _ := result["restored"].(bool); !restored {
		t.Fatalf("restored=%v, want true", result["restored"])
	}
	foundRestored := false
	for _, candidate := range app.snapshotState().Cards {
		if candidate.ID == card.ID && candidate.Title == card.Title {
			foundRestored = true
			break
		}
	}
	if !foundRestored {
		t.Fatalf("restored card %q not found in board", card.ID)
	}
}

func TestRealtimeArchiveMeetingCreatesMeetingArtifact(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	appendTestTranscript(t, app, "event-1", "Decision: pilot the executive weekly review workflow first.")

	result, changed, err := app.applyToolCallArgs("archive_meeting", map[string]any{})
	if err != nil {
		t.Fatalf("archive_meeting: %v", err)
	}
	if changed {
		t.Fatal("archive_meeting changed board state")
	}
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("ok=%v, want true", result["ok"])
	}
	archive, ok := result["archive"].(meetingArchiveResult)
	if !ok {
		t.Fatalf("archive type=%T, want meetingArchiveResult", result["archive"])
	}
	if archive.ID == "" || archive.DownloadURL == "" {
		t.Fatalf("archive missing id/download URL: %#v", archive)
	}
	if archive.Artifact == nil {
		t.Fatalf("archive artifact=nil, want saved meeting artifact")
	}
	if archive.Artifact.Kind != meetingMemoryKindOSArtifact || archive.Artifact.Metadata["mode"] != "meeting" {
		t.Fatalf("artifact kind/mode=%q/%q, want os_artifact/meeting", archive.Artifact.Kind, archive.Artifact.Metadata["mode"])
	}
	actions, ok := result["actions"].([]osAssistantAction)
	if !ok {
		t.Fatalf("actions type=%T, want []osAssistantAction", result["actions"])
	}
	if !hasAssistantAction(actions, "open_tool", "artifacts", archive.Artifact.ID) ||
		!hasAssistantAction(actions, "select_artifact", "artifacts", archive.Artifact.ID) {
		t.Fatalf("actions=%#v, want artifact selection for %q", actions, archive.Artifact.ID)
	}
}

func TestRealtimeUpdateArtifactRenamesKnownArtifact(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	created, _, err := app.applyToolCallArgs("create_artifact", map[string]any{
		"mode":    "artifacts",
		"query":   "save pilot notes",
		"content": "Pilot notes body.",
	})
	if err != nil {
		t.Fatalf("create_artifact: %v", err)
	}
	artifact := created["artifact"].(meetingMemoryEntry)

	result, changed, err := app.applyToolCallArgs("update_artifact", map[string]any{
		"artifact_id": artifact.ID,
		"title":       "Renamed pilot notes",
	})
	if err != nil {
		t.Fatalf("update_artifact: %v", err)
	}
	if changed {
		t.Fatal("update_artifact changed board state")
	}
	updated, ok := result["artifact"].(meetingMemoryEntry)
	if !ok {
		t.Fatalf("artifact type=%T, want meetingMemoryEntry", result["artifact"])
	}
	if updated.Metadata["title"] != "Renamed pilot notes" {
		t.Fatalf("title=%q, want renamed title", updated.Metadata["title"])
	}
	if updated.Text != artifact.Text {
		t.Fatalf("text=%q, want preserved %q", updated.Text, artifact.Text)
	}
	if updated.Metadata["updatedBy"] != scoutParticipantName {
		t.Fatalf("updatedBy=%q, want %q", updated.Metadata["updatedBy"], scoutParticipantName)
	}
	actions, ok := result["actions"].([]osAssistantAction)
	if !ok {
		t.Fatalf("actions type=%T, want []osAssistantAction", result["actions"])
	}
	if !hasAssistantAction(actions, "open_tool", "artifacts", updated.ID) ||
		!hasAssistantAction(actions, "select_artifact", "artifacts", updated.ID) {
		t.Fatalf("actions=%#v, want artifact selection for %q", actions, updated.ID)
	}
}

func TestRealtimeCreateArtifactSavesOSArtifact(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	result, changed, err := app.applyToolCallArgs("create_artifact", map[string]any{
		"mode":    "research",
		"query":   "summarize the pilot evidence",
		"content": "Research brief\n\nPilot evidence goes here.",
	})
	if err != nil {
		t.Fatalf("create_artifact: %v", err)
	}
	if changed {
		t.Fatal("create_artifact changed board state")
	}
	artifact, ok := result["artifact"].(meetingMemoryEntry)
	if !ok {
		t.Fatalf("artifact type=%T, want meetingMemoryEntry", result["artifact"])
	}
	if artifact.Kind != meetingMemoryKindOSArtifact || artifact.Metadata["mode"] != "research" {
		t.Fatalf("artifact kind/mode=%q/%q, want os_artifact/research", artifact.Kind, artifact.Metadata["mode"])
	}
	if artifact.Metadata["createdBy"] != scoutParticipantName {
		t.Fatalf("artifact createdBy=%q, want %q", artifact.Metadata["createdBy"], scoutParticipantName)
	}
	if !strings.Contains(artifact.Text, "Pilot evidence") {
		t.Fatalf("artifact text=%q, want saved content", artifact.Text)
	}
}

func TestRealtimeCreateArtifactScaffoldsWorkflow(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	result, changed, err := app.applyToolCallArgs("create_artifact", map[string]any{
		"mode":  "workflow",
		"query": "turn this into a Codex goal loop with review and shipping gates",
	})
	if err != nil {
		t.Fatalf("create_artifact workflow: %v", err)
	}
	if changed {
		t.Fatal("create_artifact workflow changed board state")
	}
	artifact, ok := result["artifact"].(meetingMemoryEntry)
	if !ok {
		t.Fatalf("artifact type=%T, want meetingMemoryEntry", result["artifact"])
	}
	if artifact.Kind != meetingMemoryKindOSArtifact || artifact.Metadata["mode"] != "workflow" {
		t.Fatalf("artifact kind/mode=%q/%q, want os_artifact/workflow", artifact.Kind, artifact.Metadata["mode"])
	}
	if artifact.Metadata["workflow"] != "codex_goal_loop" || artifact.Metadata["codexRunner"] != "not_connected" {
		t.Fatalf("workflow metadata=%v, want codex workflow scaffold", artifact.Metadata)
	}
	for _, want := range []string{"Codex goal workflow", "Review against the original goal", "Verify goal as completed"} {
		if !strings.Contains(artifact.Text, want) {
			t.Fatalf("artifact text missing %q: %s", want, artifact.Text)
		}
	}
}

func TestRealtimeLaunchAgentThreadCreatesRunningArtifact(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	result, changed, err := app.applyToolCallArgs("launch_agent_thread", map[string]any{
		"mode":  "design",
		"query": "turn Realtime 2 into the UI for Scout threads and artifacts",
	})
	if err != nil {
		t.Fatalf("launch_agent_thread: %v", err)
	}
	if changed {
		t.Fatal("launch_agent_thread changed board state")
	}
	thread, ok := result["thread"].(scoutAgentThread)
	if !ok {
		t.Fatalf("thread type=%T, want scoutAgentThread", result["thread"])
	}
	if thread.ID == "" || thread.Status != "running" {
		t.Fatalf("thread=%#v, want running thread", thread)
	}
	artifact, ok := result["artifact"].(meetingMemoryEntry)
	if !ok {
		t.Fatalf("artifact type=%T, want meetingMemoryEntry", result["artifact"])
	}
	if artifact.Kind != meetingMemoryKindOSArtifact || artifact.Metadata["source"] != "scout_thread" || artifact.Metadata["status"] != "running" {
		t.Fatalf("artifact=%#v, want running scout thread artifact", artifact)
	}
	if artifact.Metadata["agentLoop"] != "realtime_controlled_workforce" || artifact.Metadata["goalStatus"] != "running" || artifact.Metadata["reviewGate"] != "pending" {
		t.Fatalf("artifact metadata=%v, want realtime workforce loop metadata", artifact.Metadata)
	}
	if !strings.Contains(artifact.Text, "Scout work thread") || !strings.Contains(artifact.Text, "Goal workflow") {
		t.Fatalf("artifact text=%q, want thread scaffold", artifact.Text)
	}
	actions, ok := result["actions"].([]osAssistantAction)
	if !ok || len(actions) == 0 {
		t.Fatalf("actions=%T %#v, want chat action", result["actions"], result["actions"])
	}
	if actions[0].Tool != "chat" || actions[0].ArtifactID != artifact.ID {
		t.Fatalf("actions=%#v, want launch_agent_thread to route visible work to Chat with artifact id", actions)
	}
}

func TestRealtimePublishArtifactMarksDashboardMetadata(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	artifact, _, err := app.createOSArtifact("workflow", "ship a gated loop", "Codex goal workflow\n\nReady.", "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}

	result, changed, err := app.applyToolCallArgs("publish_artifact", map[string]any{
		"artifact_id": artifact.ID,
		"published":   true,
	})
	if err != nil {
		t.Fatalf("publish_artifact: %v", err)
	}
	if changed {
		t.Fatal("publish_artifact changed board state")
	}
	published, ok := result["artifact"].(meetingMemoryEntry)
	if !ok {
		t.Fatalf("artifact type=%T, want meetingMemoryEntry", result["artifact"])
	}
	if published.Metadata["published"] != "true" || published.Metadata["status"] != "published" || published.Metadata["publishedAt"] == "" {
		t.Fatalf("published metadata=%v, want dashboard publish status", published.Metadata)
	}
	if len(app.publishedOSArtifactsSnapshot(10)) != 1 {
		t.Fatalf("published snapshot=%#v, want one artifact", app.publishedOSArtifactsSnapshot(10))
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

func TestScoutWakePhraseAcceptsAddressedSpeech(t *testing.T) {
	for _, transcript := range []string{
		"Hey Scout, what is blocked?",
		"hey scout what did Tim commit to last week",
		"Hey, Scout: summarize this meeting.",
		"Scout, what is blocked?",
		"scout move card two to done",
		"Okay scout, what's next?",
		"Um, hey scout what's blocked?",
		"Hey Scott, what's in progress?",
		"Scouts, give me a status update.",
		"Scout's the one I'm asking: what's left?",
		"Hey there scout, what's left?",
	} {
		if !transcriptStartsWithScoutWakePhrase(transcript) {
			t.Fatalf("wake phrase was not detected in %q", transcript)
		}
	}

	for _, transcript := range []string{
		"Can you ask Scout what is blocked?",
		"They said hey Scout in the last meeting.",
		"Hey team, Scout should ignore this.",
		"Let's wrap up the meeting.",
		"Hey everyone, let's get started.",
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

func TestRealtimeOutputItemDoneWithPartialArgumentsWaitsForResponseDone(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	callID := "call-do-nothing"

	app.handleRealtimeEvent([]byte(`{
		"type": "response.output_item.done",
		"item": {
			"type": "function_call",
			"name": "do_nothing",
			"call_id": "call-do-nothing",
			"arguments": "{\"reason\":\""
		}
	}`))

	app.mu.Lock()
	_, handled := app.handledCalls[callID]
	app.mu.Unlock()
	if handled {
		t.Fatal("partial output_item.done arguments should not mark the call as handled")
	}

	app.handleRealtimeEvent([]byte(`{
		"type": "response.done",
		"response": {
			"output": [{
				"type": "function_call",
				"name": "do_nothing",
				"call_id": "call-do-nothing",
				"arguments": "{\"reason\":\"nothing actionable\"}"
			}]
		}
	}`))

	app.mu.Lock()
	_, handled = app.handledCalls[callID]
	app.mu.Unlock()
	if !handled {
		t.Fatal("complete response.done arguments should handle the call")
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
