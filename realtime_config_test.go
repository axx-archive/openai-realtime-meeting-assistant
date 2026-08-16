package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func waitForRealtimeArtifactCount(t *testing.T, app *kanbanBoardApp, want int) []meetingMemoryEntry {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		artifacts := app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0)
		if len(artifacts) == want {
			return artifacts
		}
		if time.Now().After(deadline) {
			t.Fatalf("artifact count=%d, want %d", len(artifacts), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRealtimeSessionConfigUsesGptRealtime2Optimizations(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("OPENAI_REALTIME_REASONING_EFFORT", "")
	t.Setenv("OPENAI_REALTIME_VAD_TYPE", "")
	t.Setenv("OPENAI_REALTIME_VAD_EAGERNESS", "")
	t.Setenv("OPENAI_REALTIME_TRANSCRIPTION_MODEL", "")

	app := newKanbanBoardApp()
	session := app.sessionConfig("gpt-realtime-2")

	reasoning, ok := session["reasoning"].(map[string]any)
	if !ok {
		t.Fatal("session reasoning config missing")
	}
	if effort := reasoning["effort"]; effort != "medium" {
		t.Fatalf("room reasoning effort=%v, want medium", effort)
	}

	audio := session["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	output := audio["output"].(map[string]any)
	if _, found := input["noise_reduction"]; found {
		t.Fatalf("gpt-live-transcribe must not inherit unqualified near-field controls: %v", input["noise_reduction"])
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
	if got, ok := transcription["languages"].([]string); !ok || !reflect.DeepEqual(got, []string{"en"}) {
		t.Fatalf("transcription.languages=%#v, want modern plural hint", transcription["languages"])
	}
	if keywords, ok := transcription["keywords"].([]string); !ok || len(keywords) == 0 {
		t.Fatalf("transcription keywords missing for gpt-live-transcribe: %#v", transcription["keywords"])
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
	if create := turnDetection["create_response"]; create != false {
		t.Fatalf("room turn_detection.create_response=%v, want false until Scout invocation is admitted", create)
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
	input := session["audio"].(map[string]any)["input"].(map[string]any)
	turnDetection := input["turn_detection"].(map[string]any)
	if create := turnDetection["create_response"]; create != true {
		t.Fatalf("private turn_detection.create_response=%v, want true", create)
	}
	// Conversational voice uses medium effort for tight latency. High effort
	// causes perceivable "thinking" delay that breaks conversational flow.
	if effort := session["reasoning"].(map[string]any)["effort"]; effort != "medium" {
		t.Fatalf("private reasoning effort=%v, want medium for conversational latency", effort)
	}

	tools, ok := session["tools"].([]map[string]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("private dashboard Realtime voice tools=%T, want constrained OS tool list", session["tools"])
	}
	// E10 conversation-first admission: the model may submit natural language
	// or explicitly discard noise. It cannot select a product tool, template,
	// provider, authority, audience, or mutation.
	allowed := map[string]bool{
		"route_conversation_turn": true,
		"do_nothing":              true,
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
	for _, directTool := range []string{"launch_agent_thread", "initiate_goal", "delete_ticket", "post_to_channel", "create_artifact", "answer_memory_question", "start_private_grill", "set_voice_control"} {
		if privateRealtimeVoiceToolAllowed(directTool) {
			t.Fatalf("private realtime voice must not expose direct tool %q", directTool)
		}
	}
	// Conversational voice defaults to tool_choice=none so ordinary talk speaks
	// on the first response with no route HTTP. The client flips to auto via
	// session.update only when the user's transcription indicates an action.
	if toolChoice := session["tool_choice"]; toolChoice != "none" {
		t.Fatalf("tool_choice=%v, want none so ordinary talk speaks first without route HTTP", toolChoice)
	}
	instructions := session["instructions"].(string)
	for _, want := range []string{
		"private Stride voice assistant",
		"outside the video room",
		"You are NOT the room's shared voice",
		"speak your answer directly",
		"For explicit action requests",
		"call route_conversation_turn with the user's exact words",
		"never choose a tool, deliverable template, model, provider",
		"The Kanban Board is retired",
		"Current Work and Project context is server-resolved",
		"If that context is unavailable or ambiguous, say so instead of guessing",
	} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("private voice instructions missing %q: %s", want, instructions)
		}
	}
}

func TestPrivateRealtimeVoiceShouldRouteOrdinaryTalkStaysFalse(t *testing.T) {
	// Ordinary conversation should NOT trigger shouldRoute
	ordinaryTalk := []string{
		"does that make sense",
		"Does that make sense?",
		"let me write that down",
		"I'll send it later",
		"share your thoughts",
		"I like that picture",
		"going away for lunch",
		"I have to leave for the meeting",
		"stepping away from my desk",
	}
	for _, text := range ordinaryTalk {
		if privateRealtimeVoiceTranscriptIndicatesAction(text) {
			t.Errorf("privateRealtimeVoiceTranscriptIndicatesAction(%q) = true, want false", text)
		}
	}
	// Bare confirmations should NOT be detected as actions
	bareConfirmations := []string{"ok", "great", "yes", "Yes.", "Yes!"}
	for _, text := range bareConfirmations {
		if privateRealtimeVoiceTranscriptIndicatesAction(text) {
			t.Errorf("privateRealtimeVoiceTranscriptIndicatesAction(%q) = true, want false (confirmations need direction pass)", text)
		}
	}
}

func TestPrivateRealtimeVoiceShouldRouteStudioAsksWork(t *testing.T) {
	// Named studio asks should trigger shouldRoute
	studioAsks := []string{
		"make a 5-slide deck",
		"create a deck about our product",
		"build a pitch deck",
		"make me a presentation",
		"generate an image of a sunset",
		"create a picture of a logo",
	}
	for _, text := range studioAsks {
		if !privateRealtimeVoiceTranscriptIndicatesAction(text) {
			t.Errorf("privateRealtimeVoiceTranscriptIndicatesAction(%q) = false, want true", text)
		}
	}
}

func TestPrivateRealtimeVoiceConfirmationStripsPunctuation(t *testing.T) {
	// ASR punctuation should be stripped
	confirmationsWithPunctuation := []string{
		"Yes.",
		"Yes!",
		"yes,",
		"Ok.",
		"OK!",
		"Sure!",
		"Go ahead.",
		"Perfect!",
	}
	for _, text := range confirmationsWithPunctuation {
		if !privateRealtimeVoiceIsConfirmation(text) {
			t.Errorf("privateRealtimeVoiceIsConfirmation(%q) = false, want true (should strip punctuation)", text)
		}
	}
}

func TestPrivateRealtimeVoiceOrdinaryQuestionPlusYesStaysFalse(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "test@example.com", Name: "Test"}

	// Create a thread where Scout asked an ordinary conversational question
	// (NOT a clarify_once choices card)
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Scout", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}

	// Add Scout message with an ordinary question — this is NOT a direction pass
	// because it's Kind="message", not Kind="choices" with IntentOutcome=clarify_once
	thread.Messages = append(thread.Messages, scoutChatMessageRecord{
		ID:            "scout-msg-1",
		Kind:          "message",
		Role:          "assistant",
		AuthorName:    "Scout",
		IntentOutcome: string(conversationIntentConversationalReply),
		Text:          "How's the week going?",
		CreatedAt:     "2024-01-01T00:00:00Z",
	})
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatalf("save thread: %v", err)
	}

	// "yes" should NOT route — ordinary question is not a direction pass
	if app.privateRealtimeVoiceShouldRoute(thread.ID, "yes") {
		t.Error("shouldRoute('yes') = true after ordinary question, want false")
	}
	if app.privateRealtimeVoiceShouldRoute(thread.ID, "yeah") {
		t.Error("shouldRoute('yeah') = true after ordinary question, want false")
	}
}

func TestPrivateRealtimeVoiceRealDirectionPassPlusYesRoutes(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "test@example.com", Name: "Test"}

	// Create a thread where Scout asked a REAL direction pass (clarify_once choices card)
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Scout", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}

	// Add a real direction pass — Kind=choices with IntentOutcome=clarify_once
	// This is what the router produces for "What's the deck about... typographic deck?"
	thread.Messages = append(thread.Messages, scoutChatMessageRecord{
		ID:            "scout-choices-1",
		Kind:          scoutChatMessageKindChoices,
		Role:          "assistant",
		AuthorName:    "Scout",
		IntentOutcome: string(conversationIntentClarifyOnce),
		Text:          "What's the deck about — typographic deck?",
		CreatedAt:     "2024-01-01T00:00:00Z",
		Choices: &scoutChatChoices{
			Question: "What's the deck about — typographic deck?",
			Options: []scoutChatChoiceOption{
				{ID: "1", Label: "Company overview"},
				{ID: "2", Label: "Product launch"},
			},
		},
	})
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatalf("save thread: %v", err)
	}

	// "yes" should route — real direction pass is pending
	if !app.privateRealtimeVoiceShouldRoute(thread.ID, "yes") {
		t.Error("shouldRoute('yes') = false after real direction pass (choices card), want true")
	}
	if !app.privateRealtimeVoiceShouldRoute(thread.ID, "Yes.") {
		t.Error("shouldRoute('Yes.') = false after real direction pass, want true")
	}
	if !app.privateRealtimeVoiceShouldRoute(thread.ID, "Yeah!") {
		t.Error("shouldRoute('Yeah!') = false after real direction pass, want true")
	}

	// Add user response - direction pass should clear
	thread.Messages = append(thread.Messages, scoutChatMessageRecord{
		ID:        "user-msg-1",
		Kind:      "message",
		Role:      "user",
		Text:      "Company overview",
		CreatedAt: "2024-01-01T00:00:01Z",
	})
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatalf("save thread: %v", err)
	}

	// "yes" should NOT route without pending direction pass
	if app.privateRealtimeVoiceShouldRoute(thread.ID, "yes") {
		t.Error("shouldRoute('yes') = true without direction pass, want false")
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

func TestRealtimeReasoningIsServerOwnedWhileVADRemainsConfigurable(t *testing.T) {
	t.Setenv("OPENAI_REALTIME_REASONING_EFFORT", "xhigh")
	t.Setenv("OPENAI_REALTIME_VAD_TYPE", "semantic_vad")
	t.Setenv("OPENAI_REALTIME_VAD_EAGERNESS", "low")

	if effort := realtimeReasoningEffort(); effort != "high" {
		t.Fatalf("reasoning effort=%q, want fixed high", effort)
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

func TestRealtimeToolsRetireKanbanMutations(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	rawTools, err := json.Marshal(app.kanbanTools())
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	toolsJSON := string(rawTools)
	for _, name := range []string{"create_ticket", "move_ticket", "add_tags", "add_key_date", "remove_key_dates", "update_ticket", "delete_ticket", "undo_delete_ticket"} {
		if strings.Contains(toolsJSON, `"name":"`+name+`"`) {
			t.Fatalf("retired Board tool %q remains advertised: %s", name, toolsJSON)
		}
	}
	if strings.Contains(toolsJSON, `"board"`) {
		t.Fatalf("retired Board destination remains advertised: %s", toolsJSON)
	}
	if strings.Contains(toolsJSON, `"card_id"`) || strings.Contains(toolsJSON, `"card"`) {
		t.Fatalf("retired Board linkage remains advertised: %s", toolsJSON)
	}
	if instructions := app.sessionInstructions(); strings.Contains(instructions, "Kanban board") || strings.Contains(instructions, "# Board") || strings.Contains(instructions, "board operation") {
		t.Fatalf("Realtime instructions still teach retired Board behavior: %s", instructions)
	}
}

func TestRealtimeToolsExposeOSControlAndArtifacts(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	rawTools, err := json.Marshal(app.kanbanTools())
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	toolsJSON := string(rawTools)
	for _, want := range []string{`"name":"control_app"`, `"name":"set_voice_control"`, `"name":"set_recording"`, `"name":"archive_meeting"`, `"name":"create_artifact"`, `"name":"launch_agent_thread"`, `"name":"update_artifact"`, `"name":"publish_artifact"`, `"artifacts"`, `"research"`, `"workflow"`, "conversational thread", "agent-workforce thread", "latest published", `"memory"`, "local mic"} {
		if !strings.Contains(toolsJSON, want) {
			t.Fatalf("tools JSON missing %s: %s", want, toolsJSON)
		}
	}
	instructions := app.sessionInstructions()
	for _, want := range []string{"Stride voice operator", "control_app", "set_voice_control", "set_recording", "archive_meeting", "update_artifact", "publish_artifact", "browser and device permissions", "pinning a speaker", "create_artifact", "launch_agent_thread", "goal workflow", "conversational thread", "agent workforce", "vision", "Latest published artifacts", "Voice control mode"} {
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

func TestRealtimeBoardMutationsAreRejectedAfterRetirement(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	before := len(app.snapshotState().Cards)
	for _, name := range []string{"create_ticket", "delete_ticket", "undo_delete_ticket"} {
		if result, changed, err := app.applyToolCallArgs(name, map[string]any{"title": "Must not land", "card_id": "legacy-card"}); !errors.Is(err, ErrBoardRetired) || changed || result != nil {
			t.Fatalf("%s result=%v changed=%v err=%v", name, result, changed, err)
		}
	}
	if after := len(app.snapshotState().Cards); after != before {
		t.Fatalf("retired calls changed Board size %d -> %d", before, after)
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
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	meetingID := app.memory.ensureMeetingID(officeRoomID)
	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	state.mediaGen = 1
	state.mediaSittingID = meetingID
	state.activeSpeakerName = "AJ"
	state.participantCounts["AJ"] = 1
	app.mu.Unlock()
	app.captureOfficeScoutRequesterCandidate()
	app.armOfficeScoutRequesterCandidate()
	app.bindOfficeScoutRequesterToResponse("response-design")
	app.bindOfficeScoutRequesterToCall("response-design", "call-design")
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	result, changed, err := app.applyOfficeRealtimeToolCallArgs(kanbanRealtimeOutputItem{
		Type: "function_call", Name: "launch_agent_thread", ResponseID: "response-design", CallID: "call-design",
	}, map[string]any{
		"mode":  "design",
		"query": "turn Realtime 2 into the UI for Scout threads and artifacts",
	}, meetingID)
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

func TestRealtimeLaunchAgentThreadProviderResultIsSmallAndHuman(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	artifact := meetingMemoryEntry{
		ID:   "os-artifact-research-1",
		Text: strings.Repeat("internal scaffold and provider context ", 200),
		Metadata: map[string]string{
			"title": "Hey! What's Up? market research",
		},
	}
	thread := scoutAgentThread{ID: "agent-thread-research-1", Status: "running", Query: "research the meeting", Artifact: artifact}
	minimized := realtimeProviderToolResult("launch_agent_thread", map[string]any{
		"ok": true, "thread": thread, "artifact": artifact, "actions": []string{"internal action"},
	})
	raw, err := json.Marshal(minimized)
	if err != nil {
		t.Fatalf("marshal minimized result: %v", err)
	}
	if len(raw) > 400 || bytes.Contains(raw, []byte("internal scaffold")) || bytes.Contains(raw, []byte("actions")) {
		t.Fatalf("provider tool result leaked internal work payload (%d bytes): %s", len(raw), raw)
	}
	if minimized["work_run_id"] != thread.ID || minimized["artifact_id"] != artifact.ID || minimized["status"] != "running" {
		t.Fatalf("minimized result=%v, want truthful visible work identity", minimized)
	}
	instructions := scoutSpokenResponseInstructions()
	if !strings.Contains(instructions, "only one short sentence") || !strings.Contains(instructions, "room work card") {
		t.Fatalf("spoken continuation instructions are not bounded: %q", instructions)
	}
	roomInstructions := app.sessionInstructions()
	for _, want := range []string{"For launch_agent_thread, call the tool with no spoken preamble", "one sentence of at most twelve words", "Never narrate reasoning"} {
		if !strings.Contains(roomInstructions, want) {
			t.Fatalf("room voice instructions missing latency fence %q: %s", want, roomInstructions)
		}
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

func TestRealtimeReasoningEffortRejectsLegacyMinimal(t *testing.T) {
	t.Setenv("OPENAI_REALTIME_REASONING_EFFORT", "minimal")

	if effort := realtimeReasoningEffort(); effort != "high" {
		t.Fatalf("reasoning effort=%q, want fixed high", effort)
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
	callID := "call-durable-artifact"

	app.handleToolCall(kanbanRealtimeOutputItem{
		Type:      "function_call",
		Name:      "create_artifact",
		CallID:    callID,
		Arguments: `{"mode":"artifacts","query":"Dog Perfect status"`,
	}, true)

	app.mu.Lock()
	_, handled := app.handledCalls[callID]
	app.mu.Unlock()
	if handled {
		t.Fatal("incomplete arguments should not mark the call as handled")
	}

	app.handleToolCall(kanbanRealtimeOutputItem{
		Type:      "function_call",
		Name:      "create_artifact",
		CallID:    callID,
		Arguments: `{"mode":"artifacts","query":"Dog Perfect status","content":"Waiting on Erick for launch approval."}`,
	}, false)

	found := false
	for _, artifact := range waitForRealtimeArtifactCount(t, app, 1) {
		if artifact.Metadata["title"] == "Dog Perfect status" || strings.Contains(artifact.Text, "Waiting on Erick") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("complete retry did not create the durable artifact")
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

// realtimeLedgerSetup points the usage ledger at a temp dir and pins its
// clock so file rotation lands on a known date. Reuses the usage_ledger_test
// helpers (same package).
func realtimeLedgerSetup(t *testing.T) string {
	t.Helper()
	dir := ledgerTestDir(t)
	fixed := time.Date(2026, time.July, 11, 12, 0, 0, 0, time.UTC)
	prevNow := usageLedgerNow
	usageLedgerNow = func() time.Time { return fixed }
	t.Cleanup(func() { usageLedgerNow = prevNow })
	return dir
}

func TestRealtimeSessionConfigPinsLiveTranscriptionModel(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	// A stale environment value cannot move the live-session transcription
	// lane away from gpt-live-transcribe or suppress its vocabulary hints.
	t.Setenv("OPENAI_REALTIME_TRANSCRIPTION_MODEL", "gpt-realtime-whisper")
	session := app.sessionConfig("gpt-realtime-2")
	input := session["audio"].(map[string]any)["input"].(map[string]any)
	if _, present := input["noise_reduction"]; present {
		t.Fatalf("unqualified noise_reduction sent to live transcription model: %v", input)
	}
	transcription := input["transcription"].(map[string]any)
	if _, present := transcription["prompt"]; !present {
		t.Fatalf("vocabulary prompt missing from fixed live transcription model: %v", transcription)
	}
	if model := transcription["model"]; model != defaultRealtimeTranscriptionModel {
		t.Fatalf("transcription.model=%v, want %s", model, defaultRealtimeTranscriptionModel)
	}

	// A second legacy override is ignored identically.
	t.Setenv("OPENAI_REALTIME_TRANSCRIPTION_MODEL", "gpt-4o-transcribe")
	session = app.sessionConfig("gpt-realtime-2")
	input = session["audio"].(map[string]any)["input"].(map[string]any)
	transcription = input["transcription"].(map[string]any)
	if transcription["model"] != defaultRealtimeTranscriptionModel {
		t.Fatalf("legacy override changed transcription=%v", transcription)
	}
	transcription = input["transcription"].(map[string]any)
	prompt, ok := transcription["prompt"].(string)
	if !ok || !strings.Contains(prompt, "Boot Barn") {
		t.Fatalf("vocabulary prompt missing for gpt-4o-transcribe: %v", transcription["prompt"])
	}

	// GPT Transcribe replaces singular `language` with plural `languages` and
	// adds literal keyword hints. Its prompt support does not imply the older
	// near-field input control is compatible.
	t.Setenv("OPENAI_REALTIME_TRANSCRIPTION_MODEL", "gpt-transcribe")
	session = app.sessionConfig("gpt-realtime-2.1")
	input = session["audio"].(map[string]any)["input"].(map[string]any)
	if _, present := input["noise_reduction"]; present {
		t.Fatalf("unqualified near-field control sent to gpt-transcribe: %v", input)
	}
	transcription = input["transcription"].(map[string]any)
	if _, present := transcription["language"]; present {
		t.Fatalf("legacy singular language sent to gpt-transcribe: %v", transcription)
	}
	if languages, ok := transcription["languages"].([]string); !ok || !reflect.DeepEqual(languages, []string{"en"}) {
		t.Fatalf("modern languages missing for gpt-transcribe: %v", transcription["languages"])
	}
	if keywords, ok := transcription["keywords"].([]string); !ok || len(keywords) == 0 {
		t.Fatalf("modern keywords missing for gpt-transcribe: %v", transcription["keywords"])
	}
	if prompt, ok := transcription["prompt"].(string); !ok || !strings.Contains(prompt, "STRIDE") {
		t.Fatalf("modern prompt missing for gpt-transcribe: %v", transcription["prompt"])
	}
}

func TestUsesAdvancedCommandProfileMatchesRealtime2Family(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"gpt-realtime-2", true},
		{" GPT-Realtime-2 ", true},
		{"gpt-realtime-2.1", true},
		{"gpt-realtime-2.2", true},
		{"gpt-realtime-2.1-mini", false}, // session.reasoning support unverified on mini
		{"gpt-realtime-2-mini", false},
		{"gpt-realtime-20", false}, // different family, not a point release
		{"gpt-realtime-1", false},
		{"gpt-4o-realtime-preview", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := usesAdvancedCommandProfile(tc.model); got != tc.want {
			t.Errorf("usesAdvancedCommandProfile(%q)=%v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestHostileRealtimeTranscriptionEnvCannotTriggerNoVocabRoute(t *testing.T) {
	dir := realtimeLedgerSetup(t)

	// A prompt-accepting model stays silent.
	t.Setenv("OPENAI_REALTIME_TRANSCRIPTION_MODEL", "gpt-4o-transcribe")
	warnRealtimeVoiceSessionNoVocab("room")
	if _, err := os.Stat(filepath.Join(dir, "eval-2026-07-11.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("no event expected for a prompt-accepting model (stat err=%v)", err)
	}

	// A stale whisper-family environment pin cannot move the server-owned live
	// transcription lane, so it cannot create a misleading degradation event.
	t.Setenv("OPENAI_REALTIME_TRANSCRIPTION_MODEL", "gpt-realtime-whisper")
	warnRealtimeVoiceSessionNoVocab("room")
	if _, err := os.Stat(filepath.Join(dir, "eval-2026-07-11.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("hostile env triggered a no-vocab event (stat err=%v)", err)
	}
}

func TestValidateRealtimeConfigWarnings(t *testing.T) {
	t.Setenv("OPENAI_REALTIME_MODEL", "")
	t.Setenv("OPENAI_TRANSCRIPT_MODEL", "")
	t.Setenv("OPENAI_REALTIME_TRANSCRIPTION_MODEL", "")
	t.Setenv("OPENAI_REALTIME_REASONING_EFFORT", "")
	if warnings := validateRealtimeConfig(); len(warnings) != 0 {
		t.Fatalf("defaults should validate clean, got %v", warnings)
	}

	// Stale model and effort dials are ignored; effective server-owned routes
	// remain valid and emit no misleading warnings.
	t.Setenv("OPENAI_TRANSCRIPT_MODEL", "gpt-realtime-whisper")
	t.Setenv("OPENAI_REALTIME_MODEL", "gpt-realtime-2migthy")
	t.Setenv("OPENAI_REALTIME_REASONING_EFFORT", "turbo")
	if warnings := validateRealtimeConfig(); len(warnings) != 0 {
		t.Fatalf("ignored legacy dials changed effective validation: %v", warnings)
	}
}

func TestTelemetryLaneSnapshotReportsLaneModels(t *testing.T) {
	t.Setenv("OPENAI_REALTIME_MODEL", "")
	t.Setenv("OPENAI_REALTIME_REASONING_EFFORT", "")
	t.Setenv("OPENAI_TRANSCRIPT_MODEL", "gpt-realtime-whisper")
	t.Setenv("OPENAI_REALTIME_TRANSCRIPTION_MODEL", "")

	snapshot := telemetryLaneSnapshot()
	if snapshot["realtime_model"] != "gpt-realtime-2.1" || snapshot["realtime_reasoning_effort"] != "high" {
		t.Fatalf("realtime config wrong: %v", snapshot)
	}
	if snapshot["transcription_lane_model"] != defaultTranscriptionLaneModel || snapshot["transcription_lane_vocab"] != true {
		t.Fatalf("committed transcription lane wrong: %v", snapshot)
	}
	if snapshot["voice_transcription_model"] != "gpt-live-transcribe" || snapshot["voice_transcription_vocab"] != true {
		t.Fatalf("voice transcription lane wrong: %v", snapshot)
	}
	if snapshot["private_voice_model"] != "gpt-realtime-2.1" {
		t.Fatalf("private voice model should mirror the shared dial today: %v", snapshot)
	}
}

func TestRealtimeResponseDoneRecordsVoiceRoomUsage(t *testing.T) {
	dir := realtimeLedgerSetup(t)
	t.Setenv("OPENAI_REALTIME_MODEL", "")
	app := newIsolatedKanbanBoardApp(t)

	app.handleRealtimeEvent([]byte(`{
		"type": "response.done",
		"response": {
			"status": "completed",
			"output": [],
			"usage": {
				"total_tokens": 1300,
				"input_tokens": 1100,
				"output_tokens": 200,
				"input_token_details": {
					"text_tokens": 600,
					"audio_tokens": 500,
					"cached_tokens": 100,
					"cached_tokens_details": {"text_tokens": 80, "audio_tokens": 20}
				},
				"output_token_details": {"text_tokens": 50, "audio_tokens": 150}
			}
		}
	}`))

	rows := readLedgerLines(t, filepath.Join(dir, "usage-2026-07-11.jsonl"))
	if len(rows) != 1 {
		t.Fatalf("expected 1 usage row, got %d", len(rows))
	}
	row := rows[0]
	if row["seat"] != seatVoiceRoom || row["provider"] != providerOpenAI || row["model"] != "gpt-realtime-2.1" {
		t.Fatalf("wrong identity fields: %v", row)
	}
	if row["room_id"] != officeRoomID {
		t.Fatalf("room_id=%v, want %q", row["room_id"], officeRoomID)
	}
	// Cached shares subtract out of the full-price splits.
	if row["input_tokens"].(float64) != 520 || row["cached_input_tokens"].(float64) != 80 {
		t.Fatalf("text split wrong: %v", row)
	}
	if row["audio_input_tokens"].(float64) != 480 || row["cached_audio_input_tokens"].(float64) != 20 {
		t.Fatalf("audio split wrong: %v", row)
	}
	if row["output_tokens"].(float64) != 50 || row["audio_output_tokens"].(float64) != 150 {
		t.Fatalf("output split wrong: %v", row)
	}
	if _, present := row["price_missing"]; present {
		t.Fatalf("gpt-realtime-2.1 should be priced: %v", row)
	}

	// A turn without usage records nothing — no invented numbers.
	app.handleRealtimeEvent([]byte(`{"type":"response.done","response":{"status":"completed","output":[]}}`))
	if rows := readLedgerLines(t, filepath.Join(dir, "usage-2026-07-11.jsonl")); len(rows) != 1 {
		t.Fatalf("usage-free turn must not append: got %d rows", len(rows))
	}
}

func TestRealtimeResponseAndToolLatencyAreRecordedWithoutTranscriptBody(t *testing.T) {
	dir := realtimeLedgerSetup(t)
	app := newIsolatedKanbanBoardApp(t)

	app.markRealtimeResponseStarted()
	app.markRealtimeFirstAudio()
	app.finishRealtimeResponseTelemetry("completed", true)
	app.recordRealtimeToolLatency("launch_agent_thread", time.Now().Add(-25*time.Millisecond))

	allRows := readLedgerLines(t, filepath.Join(dir, "eval-2026-07-11.jsonl"))
	rows := make([]map[string]any, 0, 2)
	for _, row := range allRows {
		if row["kind"] == evalKindRealtimeLatency {
			rows = append(rows, row)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("latency rows=%d, want response+tool: %v", len(rows), allRows)
	}
	responseFields := rows[0]["fields"].(map[string]any)
	if rows[0]["kind"] != evalKindRealtimeLatency || responseFields["phase"] != "response_done" || responseFields["status"] != "completed" || responseFields["tool_response"] != true {
		t.Fatalf("response latency row=%v", rows[0])
	}
	if _, ok := responseFields["first_audio_ms"].(float64); !ok {
		t.Fatalf("first-audio latency missing: %v", responseFields)
	}
	toolFields := rows[1]["fields"].(map[string]any)
	if toolFields["phase"] != "tool_done" || toolFields["tool"] != "launch_agent_thread" || toolFields["duration_ms"].(float64) < 0 {
		t.Fatalf("tool latency row=%v", rows[1])
	}
	for _, row := range rows {
		raw, _ := json.Marshal(row)
		if bytes.Contains(bytes.ToLower(raw), []byte("transcript")) || bytes.Contains(bytes.ToLower(raw), []byte("utterance")) {
			t.Fatalf("latency telemetry leaked conversation body: %s", raw)
		}
	}
}

func TestVoicePeerTranscriptionSegmentsFeedLedgerAndFunnel(t *testing.T) {
	dir := realtimeLedgerSetup(t)
	t.Setenv("OPENAI_REALTIME_TRANSCRIPTION_MODEL", "")
	app := newIsolatedKanbanBoardApp(t)

	app.handleRealtimeEvent([]byte(`{
		"type": "conversation.item.input_audio_transcription.completed",
		"event_id": "evt-1",
		"item_id": "item-1",
		"transcript": "Boot Barn pipeline update",
		"usage": {
			"type": "tokens",
			"total_tokens": 442,
			"input_tokens": 412,
			"output_tokens": 30,
			"input_token_details": {"text_tokens": 12, "audio_tokens": 400}
		}
	}`))

	usageRows := readLedgerLines(t, filepath.Join(dir, "usage-2026-07-11.jsonl"))
	if len(usageRows) != 1 {
		t.Fatalf("expected 1 usage row, got %d", len(usageRows))
	}
	row := usageRows[0]
	if row["seat"] != seatTranscriptionSession || row["model"] != "gpt-live-transcribe" {
		t.Fatalf("wrong transcription usage identity: %v", row)
	}
	if row["input_tokens"].(float64) != 12 || row["audio_input_tokens"].(float64) != 400 || row["output_tokens"].(float64) != 30 {
		t.Fatalf("wrong transcription splits: %v", row)
	}

	// Lane down => the voice peer is the persisting session, so the funnel
	// records the segment; a .failed segment lands too — that is speech the
	// brain never heard.
	app.handleRealtimeEvent([]byte(`{"type": "conversation.item.input_audio_transcription.failed", "item_id": "item-2"}`))

	evalRows := readLedgerLines(t, filepath.Join(dir, "eval-2026-07-11.jsonl"))
	var segments []map[string]any
	for _, evalRow := range evalRows {
		if evalRow["kind"] == evalKindTranscriptSegment {
			segments = append(segments, evalRow)
		}
	}
	if len(segments) != 2 {
		t.Fatalf("expected completed+failed segments, got %v", evalRows)
	}
	first := segments[0]["fields"].(map[string]any)
	second := segments[1]["fields"].(map[string]any)
	if first["status"] != "completed" || second["status"] != "failed" {
		t.Fatalf("segment statuses wrong: %v %v", first, second)
	}
	if first["room_id"] != officeRoomID || segments[0]["lane"] != seatTranscriptionSession {
		t.Fatalf("segment lane/room wrong: %v", segments[0])
	}
}

func TestRoomVoiceProposalMintStampsProvenance(t *testing.T) {
	dir := realtimeLedgerSetup(t)
	app := newIsolatedKanbanBoardApp(t)

	app.finishToolCall(kanbanRealtimeOutputItem{
		Type:   "function_call",
		Name:   "propose_codex_task",
		CallID: "call-1",
	}, map[string]any{
		"title": "Research comparable exits",
		"mode":  "research",
		"query": "comparable exits for StationTenn",
	}, nil, false)

	rows := readLedgerLines(t, filepath.Join(dir, "eval-2026-07-11.jsonl"))
	var minted map[string]any
	for _, row := range rows {
		if row["type"] != telemetryTypeProposal || row["kind"] != proposalEventMinted {
			continue
		}
		if fields := row["fields"].(map[string]any); fields["source"] == proposalSourceRoomVoice {
			minted = row
		}
	}
	if minted == nil {
		t.Fatalf("no room_voice minted event in %v", rows)
	}
	fields := minted["fields"].(map[string]any)
	if asString(fields["proposal_id"]) == "" {
		t.Fatalf("minted event missing proposal_id: %v", fields)
	}
	if fields["room_id"] != officeRoomID {
		t.Fatalf("minted event missing room lineage: %v", fields)
	}
}

func TestPrivateVoiceProposalMintStampsProvenance(t *testing.T) {
	dir := realtimeLedgerSetup(t)
	app := newIsolatedKanbanBoardApp(t)

	if _, _, err := app.applyPrivateRealtimeVoiceTool("aj@bonfire.os", "propose_codex_task", map[string]any{
		"title": "Draft the weekly memo",
		"mode":  "artifacts",
		"query": "draft this week's investor memo",
	}); err != nil {
		t.Fatalf("propose_codex_task: %v", err)
	}

	rows := readLedgerLines(t, filepath.Join(dir, "eval-2026-07-11.jsonl"))
	var minted map[string]any
	for _, row := range rows {
		if row["type"] != telemetryTypeProposal || row["kind"] != proposalEventMinted {
			continue
		}
		if fields := row["fields"].(map[string]any); fields["source"] == proposalSourcePrivateVoice {
			minted = row
		}
	}
	if minted == nil {
		t.Fatalf("no private_voice minted event in %v", rows)
	}
	fields := minted["fields"].(map[string]any)
	if fields["proposer"] != "aj@bonfire.os" {
		t.Fatalf("minted event missing proposer: %v", fields)
	}
	if asString(fields["proposal_id"]) == "" {
		t.Fatalf("minted event missing proposal_id: %v", fields)
	}
	if _, present := fields["room_id"]; present {
		t.Fatalf("private-voice mint must not claim a room: %v", fields)
	}
}

func TestRoomVoiceDirectLaunchRecordsLaunchProvenance(t *testing.T) {
	setupAuthTestEnv(t)
	dir := realtimeLedgerSetup(t)
	app := newIsolatedKanbanBoardApp(t)
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	// A room-voice launch is authorized by the server-observed human speaker,
	// never provider tool arguments. Establish the exact current sitting and
	// AJ's authenticated presence before the tool callback arrives.
	meetingID := app.memory.ensureMeetingID(officeRoomID)
	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	state.mediaGen = 1
	state.mediaSittingID = meetingID
	state.activeSpeakerName = "AJ"
	state.participantCounts["AJ"] = 1
	app.mu.Unlock()
	app.captureOfficeScoutRequesterCandidate()
	app.armOfficeScoutRequesterCandidate()
	app.bindOfficeScoutRequesterToResponse("response-2")
	app.bindOfficeScoutRequesterToCall("response-2", "call-2")

	app.finishToolCall(kanbanRealtimeOutputItem{
		Type: "function_call", Name: "launch_agent_thread",
		ResponseID: "response-2", CallID: "call-2",
	}, map[string]any{
		"mode":  "research",
		"query": "map the exit landscape",
	}, nil, false)

	rows := readLedgerLines(t, filepath.Join(dir, "eval-2026-07-11.jsonl"))
	var launched map[string]any
	for _, row := range rows {
		if row["type"] == telemetryTypeProposal && row["kind"] == proposalEventLaunched {
			launched = row
		}
	}
	if launched == nil {
		t.Fatalf("no launched event in %v", rows)
	}
	fields := launched["fields"].(map[string]any)
	if fields["source"] != proposalSourceRoomVoice || fields["path"] != "launch_agent_thread" {
		t.Fatalf("launch provenance wrong: %v", fields)
	}
	if asString(fields["thread_id"]) == "" {
		t.Fatalf("launched event missing thread_id: %v", fields)
	}
	if _, present := fields["proposal_id"]; present {
		t.Fatalf("direct launch must not carry a proposal_id: %v", fields)
	}
}
