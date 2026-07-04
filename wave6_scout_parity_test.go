package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// --- Allowlist include / exclude --------------------------------------------

func TestPrivateVoiceAllowlistIncludesParityTools(t *testing.T) {
	included := []string{
		// grown parity set
		"update_artifact", "publish_artifact",
		"create_ticket", "move_ticket", "update_ticket", "add_tags",
		"add_key_date", "remove_key_dates", "delete_ticket", "undo_delete_ticket",
		// new Wave-6 tools
		"read_thread_aloud", "start_chat_as_user", "initiate_goal",
		// unchanged
		"control_app", "answer_memory_question", "post_to_channel",
	}
	for _, name := range included {
		if !privateRealtimeVoiceToolAllowed(name) {
			t.Errorf("private voice should allow %q", name)
		}
	}

	// Room-only tools stay excluded: they mutate the shared room session/recording
	// or swap the shared grill persona, and the private surface has no room.
	excluded := []string{"set_voice_control", "set_recording", "archive_meeting", "start_grill_session", "end_grill_session"}
	for _, name := range excluded {
		if privateRealtimeVoiceToolAllowed(name) {
			t.Errorf("private voice must NOT allow room-only tool %q", name)
		}
	}
}

func TestPrivateVoiceToolSchemasMatchAllowlist(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	names := map[string]bool{}
	for _, tool := range app.privateRealtimeVoiceTools() {
		names[asString(tool["name"])] = true
	}
	for _, want := range []string{"read_thread_aloud", "start_chat_as_user", "initiate_goal", "update_artifact", "delete_ticket"} {
		if !names[want] {
			t.Errorf("privateRealtimeVoiceTools() missing schema for %q", want)
		}
	}
	for _, forbidden := range []string{"set_recording", "archive_meeting", "start_grill_session"} {
		if names[forbidden] {
			t.Errorf("privateRealtimeVoiceTools() must not expose room-only %q", forbidden)
		}
	}
}

// --- Safety regression: disclosure is server-stamped, never spoofable --------

func TestStartChatAsUserStampsDisclosureRegardlessOfArgs(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	requester := "aj@shareability.com"

	// The model tries to spoof authorship: it sets disclose=false and smuggles a
	// postedOnBehalfOf pointing at someone else. The server must ignore both and
	// stamp the authenticated requester.
	args := map[string]any{
		"audience":         "channel",
		"name":             "dealflow",
		"text":             "kicking off the raise",
		"disclose":         false,
		"postedOnBehalfOf": "victim@shareability.com",
		"authorEmail":      "victim@shareability.com",
	}
	result, _, err := app.applyPrivateRealtimeVoiceTool(requester, "start_chat_as_user", args)
	if err != nil {
		t.Fatalf("start_chat_as_user: %v", err)
	}
	threadID := asString(result["threadId"])
	if threadID == "" {
		t.Fatalf("no threadId in result: %#v", result)
	}
	if stamp := asString(result["postedOnBehalfOf"]); stamp != requester {
		t.Fatalf("result postedOnBehalfOf=%q, want %q", stamp, requester)
	}

	thread, _, err := app.scoutChatThreadByID(requester, threadID)
	if err != nil {
		t.Fatalf("read back thread: %v", err)
	}
	if len(thread.Messages) == 0 {
		t.Fatalf("thread has no messages")
	}
	message := thread.Messages[len(thread.Messages)-1]
	if message.PostedOnBehalfOf != requester {
		t.Fatalf("message.PostedOnBehalfOf=%q, want the authenticated requester %q (spoof must not win)", message.PostedOnBehalfOf, requester)
	}
	if message.AuthorEmail != requester {
		t.Fatalf("message.AuthorEmail=%q, want %q", message.AuthorEmail, requester)
	}
}

func TestStartChatAsUserRequiresRequester(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	// No requester (e.g. the shared room path) cannot post as a user.
	if _, _, err := app.startChatAsUser(map[string]any{"audience": "channel", "name": "x", "text": "y"}, ""); err == nil {
		t.Fatal("expected start_chat_as_user to reject an empty requester")
	}
}

// --- initiate_goal can never yield external_write ---------------------------

func TestInitiateGoalCannotYieldExternalWrite(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	// Sets ANTHROPIC_API_KEY=test-key and stubs the async engine start so the
	// launch persists an artifact without running the model loop.
	installFakeResponder(t, goalResponderRoutes{})

	// The model smuggles authority_hint=external_write; the dispatch must clamp it.
	result, _, err := app.initiateGoalTool(map[string]any{
		"objective":      "commit and push the release then deploy to production",
		"authority_hint": "external_write",
	}, "aj@shareability.com")
	if err != nil {
		t.Fatalf("initiateGoalTool: %v", err)
	}
	if asString(result["authority"]) == codexJobAuthorityExternalWrite {
		t.Fatalf("initiate_goal yielded external_write authority: %#v", result)
	}
	artifact, ok := result["artifact"].(meetingMemoryEntry)
	if !ok {
		t.Fatalf("no artifact in result: %#v", result)
	}
	if artifact.Metadata["authority"] == codexJobAuthorityExternalWrite {
		t.Fatalf("goal artifact authority=external_write, must be gated")
	}
	if artifact.Metadata["authority"] != codexJobAuthorityWorkspaceWrite {
		t.Fatalf("goal artifact authority=%q, want clamped to workspace_write", artifact.Metadata["authority"])
	}
}

func TestInitiateGoalKeylessDegradesGracefully(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	// No ANTHROPIC key: launchGoalThread returns errAgentWorkerNotConfigured and
	// the tool must speak an honest fallback rather than erroring hard.
	t.Setenv("ANTHROPIC_API_KEY", "")
	result, _, err := app.initiateGoalTool(map[string]any{"objective": "package the IP"}, "aj@shareability.com")
	if err != nil {
		t.Fatalf("keyless initiate_goal should not hard-error: %v", err)
	}
	if launched, _ := result["launched"].(bool); launched {
		t.Fatalf("keyless initiate_goal must not report launched: %#v", result)
	}
}

// --- read_thread_aloud resolves and returns text ----------------------------

func TestReadThreadAloudReturnsChannelText(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	requester := "aj@shareability.com"
	if _, _, err := app.startChatAsUser(map[string]any{"audience": "channel", "name": "standup", "text": "shipping the parity wave today"}, requester); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	result, _, err := app.readThreadAloud(map[string]any{"target": "channel", "ref": "standup"}, requester)
	if err != nil {
		t.Fatalf("read_thread_aloud: %v", err)
	}
	if !strings.Contains(asString(result["text"]), "shipping the parity wave") {
		t.Fatalf("read_thread_aloud text missing the message: %#v", result)
	}
}

// --- control_app also_open loops extra surfaces -----------------------------

func TestControlAppAlsoOpen(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	result, _, err := app.controlApp(map[string]any{"tool": "artifacts", "also_open": []any{"board", "memory", "artifacts"}})
	if err != nil {
		t.Fatalf("control_app: %v", err)
	}
	opened, _ := result["opened"].([]string)
	joined := strings.Join(opened, ",")
	for _, want := range []string{"artifacts", "board", "memory"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("control_app opened=%v, want %q included", opened, want)
		}
	}
	// The duplicate (artifacts) must not open twice.
	if strings.Count(joined, "artifacts") != 1 {
		t.Fatalf("control_app opened duplicate artifacts: %v", opened)
	}
}

// --- /assistant/goal HTTP door ----------------------------------------------

func TestGoalHTTPEndpointLaunchesAsRequesterNoExternalWrite(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	installFakeResponder(t, goalResponderRoutes{})

	body, _ := json.Marshal(map[string]any{
		"objective":     "audit the deploy pipeline and push a fix to production",
		"authorityHint": "external_write",
		"originSurface": "chat:thread-123",
	})
	req := httptest.NewRequest(http.MethodPost, "/assistant/goal", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost")
	req.Host = "localhost"
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	assistantGoalHandler(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusAccepted)
	}
	var payload struct {
		Thread   scoutAgentThread   `json:"thread"`
		Artifact meetingMemoryEntry `json:"artifact"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Artifact.Metadata["requestedBy"] != "aj@shareability.com" {
		t.Fatalf("goal launched as %q, want aj@shareability.com", payload.Artifact.Metadata["requestedBy"])
	}
	if payload.Artifact.Metadata["authority"] == codexJobAuthorityExternalWrite {
		t.Fatalf("goal HTTP door yielded external_write authority")
	}
}

func TestGoalHTTPEndpointRejectsEmptyObjective(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	body, _ := json.Marshal(map[string]any{"objective": "   "})
	req := httptest.NewRequest(http.MethodPost, "/assistant/goal", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost")
	req.Host = "localhost"
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	assistantGoalHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 for empty objective", rec.Code)
	}
}

// --- Frontend markers -------------------------------------------------------

func TestIndexHasScoutParityMarkers(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)
	for _, want := range []string{
		// voice island new states
		`data-state="acting"`,
		`data-state="hand-raised"`,
		"'acting', 'hand-raised'",
		// session ledger
		"function recordScoutAction",
		"function renderScoutLedger",
		`id="voiceLedger"`,
		// via-Scout disclosure chip
		"scout-chat-msg__via",
		"via Scout",
		"postedOnBehalfOf",
		// /goal text door
		"function parseGoalCommand",
		"function launchGoalFromComposer",
		"'/assistant/goal'",
		// narration rhythm
		"scoutActionNarration",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing Scout-parity marker: %q", want)
		}
	}

	// Wiring, not just presence: the /goal parser must actually be CALLED from
	// the composer send path, or the door is dead code. Assert the call site
	// lives inside sendScoutChatFromForm's body (a substring-anywhere check would
	// pass even with the functions completely disconnected).
	sendBody := functionBody(html, "function sendScoutChatFromForm(event)")
	if sendBody == "" {
		t.Fatal("index.html missing sendScoutChatFromForm")
	}
	for _, want := range []string{"parseGoalCommand(scoutChatInput.value)", "launchGoalFromComposer("} {
		if !strings.Contains(sendBody, want) {
			t.Errorf("sendScoutChatFromForm does not call %q — the /goal door is not wired into the send path", want)
		}
	}
}
