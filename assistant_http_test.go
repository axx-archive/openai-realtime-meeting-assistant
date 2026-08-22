package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAssistantQueryRequiresAuth(t *testing.T) {
	setupAuthTestEnv(t)

	req := httptest.NewRequest(http.MethodPost, "/assistant/query", strings.NewReader(`{"query":"what is blocked?"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	assistantQueryHandler(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestAssistantRealtimeOfferRequiresAuthAndConfiguredRealtime(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("PRIVATE_REALTIME_VOICE_QUALIFIED", "true")
	t.Setenv("OPENAI_API_KEY", "")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	req := httptest.NewRequest(http.MethodPost, "/assistant/realtime-offer", strings.NewReader(`{"sdp":"v=0","voiceSessionId":"voice-auth-test"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	assistantRealtimeOfferHandler(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want %d for unauthenticated private realtime offer", recorder.Code, http.StatusUnauthorized)
	}

	req = httptest.NewRequest(http.MethodPost, "/assistant/realtime-offer", strings.NewReader(`{"sdp":"v=0","voiceSessionId":"voice-auth-test"}`))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()

	assistantRealtimeOfferHandler(recorder, req)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s, want %d when realtime key is missing", recorder.Code, recorder.Body.String(), http.StatusServiceUnavailable)
	}
}

func TestAssistantRealtimeOfferQualificationGatePreventsProviderAdmission(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("PRIVATE_REALTIME_VOICE_QUALIFIED", "false")
	t.Setenv("OPENAI_API_KEY", "must-not-be-used")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	providerCalls := 0
	previousURL := realtimeCallsURL
	previousClient := realtimeHTTPClient
	provider := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		providerCalls++
	}))
	t.Cleanup(func() {
		provider.Close()
		realtimeCallsURL = previousURL
		realtimeHTTPClient = previousClient
	})
	realtimeCallsURL = provider.URL
	realtimeHTTPClient = provider.Client()
	req := httptest.NewRequest(http.MethodPost, "/assistant/realtime-offer", strings.NewReader(`{"sdp":"v=0","voiceSessionId":"voice-unqualified"}`))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantRealtimeOfferHandler(recorder, req)
	if recorder.Code != http.StatusServiceUnavailable || providerCalls != 0 {
		t.Fatalf("status=%d body=%s providerCalls=%d", recorder.Code, recorder.Body.String(), providerCalls)
	}
	if !strings.Contains(recorder.Body.String(), "awaiting qualification") {
		t.Fatalf("qualification error is not actionable: %s", recorder.Body.String())
	}
}

func TestAssistantRealtimeOfferForwardsTypedMultipartToOpenAI(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("PRIVATE_REALTIME_VOICE_QUALIFIED", "true")
	t.Setenv("OPENAI_API_KEY", "test-realtime-key")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	previousURL := realtimeCallsURL
	previousClient := realtimeHTTPClient
	var sawRealtimeRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRealtimeRequest = true
		if r.Method != http.MethodPost {
			t.Errorf("method=%s, want POST", r.Method)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-realtime-key" {
			t.Errorf("authorization=%q, want bearer test key", auth)
		}
		if safety := r.Header.Get("OpenAI-Safety-Identifier"); safety != realtimeSafetyIdentifier("aj@shareability.com") {
			t.Errorf("safety identifier=%q, want privacy-preserving caller binding", safety)
		}
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Errorf("parse content type: %v", err)
		}
		if mediaType != "multipart/form-data" {
			t.Errorf("content type=%q, want multipart/form-data", mediaType)
		}
		reader := multipart.NewReader(r.Body, params["boundary"])
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
				t.Errorf("read part: %v", err)
				break
			}
			raw, err := io.ReadAll(part)
			if err != nil {
				t.Errorf("read part body: %v", err)
				break
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
			t.Errorf("sdp content type=%q, want application/sdp", parts["sdp"].contentType)
		}
		if parts["sdp"].body != "v=0\r\n" {
			t.Errorf("sdp body=%q, want raw offer", parts["sdp"].body)
		}
		if parts["session"].contentType != "application/json" {
			t.Errorf("session content type=%q, want application/json", parts["session"].contentType)
		}
		if !strings.Contains(parts["session"].body, `"model":"gpt-realtime-2.1"`) {
			t.Errorf("session body missing realtime model: %s", parts["session"].body)
		}
		w.Header().Set("Content-Type", "application/sdp")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("v=0\n"))
	}))
	t.Cleanup(func() {
		server.Close()
		realtimeCallsURL = previousURL
		realtimeHTTPClient = previousClient
	})
	realtimeCallsURL = server.URL
	realtimeHTTPClient = server.Client()

	emptyReq := httptest.NewRequest(http.MethodPost, "/assistant/realtime-offer", strings.NewReader(`{"sdp":"   ","voiceSessionId":"voice-forward-test"}`))
	emptyReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		emptyReq.AddCookie(cookie)
	}
	emptyRecorder := httptest.NewRecorder()

	assistantRealtimeOfferHandler(emptyRecorder, emptyReq)

	if emptyRecorder.Code != http.StatusBadRequest {
		t.Fatalf("empty sdp status=%d body=%s, want %d", emptyRecorder.Code, emptyRecorder.Body.String(), http.StatusBadRequest)
	}
	if sawRealtimeRequest {
		t.Fatal("empty sdp should not reach mock OpenAI server")
	}

	req := httptest.NewRequest(http.MethodPost, "/assistant/realtime-offer", strings.NewReader(`{"sdp":"v=0\r\n","voiceSessionId":"voice-forward-test"}`))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()

	assistantRealtimeOfferHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}
	if !sawRealtimeRequest {
		t.Fatal("mock OpenAI server did not receive realtime offer")
	}
	var payload struct {
		SDP            string `json:"sdp"`
		VoiceSessionID string `json:"voiceSessionId"`
		ThreadID       string `json:"threadId"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.SDP != "v=0\r\n" {
		t.Fatalf("response sdp=%q, want CRLF-normalized mock answer", payload.SDP)
	}
	if payload.VoiceSessionID != "voice-forward-test" || payload.ThreadID != privateRealtimeVoiceThreadID("aj@shareability.com", payload.VoiceSessionID) {
		t.Fatalf("response voice binding=%+v", payload)
	}
}

func TestAssistantRealtimeOfferReportsQuotaBlocker(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("PRIVATE_REALTIME_VOICE_QUALIFIED", "true")
	t.Setenv("OPENAI_API_KEY", "test-realtime-key")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	previousURL := realtimeCallsURL
	previousClient := realtimeHTTPClient
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"You exceeded your current quota, please check your plan and billing details.","type":"insufficient_quota","code":"insufficient_quota"}}`))
	}))
	t.Cleanup(func() {
		server.Close()
		realtimeCallsURL = previousURL
		realtimeHTTPClient = previousClient
	})
	realtimeCallsURL = server.URL
	realtimeHTTPClient = server.Client()

	req := httptest.NewRequest(http.MethodPost, "/assistant/realtime-offer", strings.NewReader(`{"sdp":"v=0\r\n","voiceSessionId":"voice-quota-test"}`))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()

	assistantRealtimeOfferHandler(recorder, req)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusTooManyRequests)
	}
	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if got := payload["error"]; got != "Scout voice is unavailable: OpenAI API quota is exhausted" {
		t.Fatalf("error=%q, want quota-specific Scout message", got)
	}
	if strings.Contains(recorder.Body.String(), "You exceeded your current quota") || strings.Contains(recorder.Body.String(), "insufficient_quota") {
		t.Fatalf("response leaked raw OpenAI quota body: %s", recorder.Body.String())
	}
}

func TestPrivateRealtimeToolRejectsRoomOnlyControls(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("PRIVATE_REALTIME_VOICE_QUALIFIED", "true")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	voiceSessionID := "voice-direct-rejection"
	thread, _, err := kanbanApp.ensurePrivateRealtimeVoiceConversation("aj@shareability.com", "AJ", voiceSessionID)
	if err != nil {
		t.Fatal(err)
	}
	lease := activatePrivateRealtimeLeaseForTest(t, kanbanApp, "aj@shareability.com", voiceSessionID, thread.ID, cookies)
	for _, name := range []string{"set_voice_control", "set_recording"} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/assistant/realtime-tool", strings.NewReader(fmt.Sprintf(`{"voiceSessionId":%q,"threadId":%q,"callId":"call-direct-rejected","name":%q,"arguments":{"enabled":true}%s}`, voiceSessionID, thread.ID, name, privateRealtimeLeaseTestJSON(lease))))
			req.Header.Set("Content-Type", "application/json")
			for _, cookie := range cookies {
				req.AddCookie(cookie)
			}
			recorder := httptest.NewRecorder()

			assistantRealtimeToolHandler(recorder, req)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
			}
			var payload struct {
				OK      bool  `json:"ok"`
				Changed bool  `json:"changed"`
				Actions []any `json:"actions"`
				Result  struct {
					OK      bool   `json:"ok"`
					Outcome string `json:"outcome"`
					Message string `json:"message"`
					Error   string `json:"error"`
				} `json:"result"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload.OK || payload.Result.OK {
				t.Fatalf("payload=%+v, want private realtime tool rejection", payload)
			}
			if payload.Changed {
				t.Fatalf("changed=true for rejected private realtime tool %q", name)
			}
			if len(payload.Actions) != 0 {
				t.Fatalf("actions=%#v, want none for rejected private realtime tool %q", payload.Actions, name)
			}
			want := fmt.Sprintf("private Realtime voice capability %q is unavailable", name)
			if !strings.Contains(payload.Result.Error, want) {
				t.Fatalf("error=%q, want %q", payload.Result.Error, want)
			}
			if payload.Result.Outcome != string(conversationIntentUnavailable) || payload.Result.Message != "I couldn't safely complete that voice turn. Nothing else was launched." {
				t.Fatalf("rejected voice route lacks one truthful durable receipt: %+v", payload.Result)
			}
		})
	}
}

func TestAssistantQueryAnswersWorkStatusFromCurrentBrainWithoutBoard(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	kanbanApp.mu.Lock()
	kanbanApp.apiKey = "current-work-test"
	kanbanApp.mu.Unlock()
	var capturedInput string
	previousResponder := createOpenAITextResponse
	createOpenAITextResponse = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		capturedInput = request.Input
		return "I could not find a source-current Work record for that exact name.", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = previousResponder })

	req := httptest.NewRequest(http.MethodPost, "/assistant/query", strings.NewReader(`{"query":"what is the status of Finish RTP HEVC Packetizer?","mode":"chat"}`))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()

	assistantQueryHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}
	var payload struct {
		Answer string `json:"answer"`
		Source string `json:"source"`
		Mode   string `json:"mode"`
		User   string `json:"user"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Source != "assistant" {
		t.Fatalf("source=%q, want current assistant synthesis", payload.Source)
	}
	if payload.Mode != "chat" || payload.User != "AJ" {
		t.Fatalf("payload mode/user=%q/%q, want chat/AJ", payload.Mode, payload.User)
	}
	if !strings.Contains(payload.Answer, "source-current Work") {
		t.Fatalf("answer=%q, want source-current abstention", payload.Answer)
	}
	if strings.Contains(capturedInput, `"owner"`) || strings.Contains(capturedInput, `"status"`) || !strings.Contains(capturedInput, "Archived filing surfaces are not answer sources") {
		t.Fatalf("model input reopened retired Board authority: %s", capturedInput)
	}
}

func TestAssistantQueryClarifiesAmbiguousFollowUpWithoutBoardLeak(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	originalResponder := createOpenAITextResponse
	defer func() { createOpenAITextResponse = originalResponder }()
	createOpenAITextResponse = func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		t.Fatal("ambiguous clarification should not call the model with board context")
		return "", nil
	}

	body := `{
		"query":"What?",
		"mode":"chat",
		"history":[
			{"role":"user","text":"If we built a YouTube-centric digital media platform for rodeo culture, is it viable?"},
			{"role":"scout","text":"Clarify the primary objective before choosing a media strategy."}
		]
	}`
	req := httptest.NewRequest(http.MethodPost, "/assistant/query", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()

	assistantQueryHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}
	var payload struct {
		Answer string `json:"answer"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Source != "clarification" {
		t.Fatalf("source=%q, want clarification", payload.Source)
	}
	if !strings.Contains(payload.Answer, "rodeo culture") {
		t.Fatalf("answer=%q, want prior user context", payload.Answer)
	}
	for _, leaked := range []string{"In Progress", "Backlog", "current board", "Investigate screen share"} {
		if strings.Contains(payload.Answer, leaked) {
			t.Fatalf("answer=%q leaked board/status language %q", payload.Answer, leaked)
		}
	}
}

func TestAssistantQueryIgnoresLegacyModeAndCreatesNoArtifact(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	req := httptest.NewRequest(http.MethodPost, "/assistant/query", strings.NewReader(`{
		"query":"Our BI OS helps operators spot revenue risk because it connects meetings, artifacts, and board work to decisions. We need a pilot with three customers and a clear weekly scorecard.",
		"mode":"grill"
	}`))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()

	assistantQueryHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}
	var payload struct {
		Answer   string              `json:"answer"`
		Source   string              `json:"source"`
		Mode     string              `json:"mode"`
		Actions  []osAssistantAction `json:"actions"`
		Artifact meetingMemoryEntry  `json:"artifact"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Mode != "chat" || payload.Source != "assistant" {
		t.Fatalf("payload mode/source=%q/%q, want server-owned chat/assistant", payload.Mode, payload.Source)
	}
	if payload.Artifact.ID != "" || len(kanbanApp.osArtifactsSnapshot(10)) != 0 {
		t.Fatalf("legacy mode created durable work: payload=%#v artifacts=%#v", payload, kanbanApp.osArtifactsSnapshot(10))
	}
}

func TestAssistantQueryCannotSelectWorkflowOutputContract(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	req := httptest.NewRequest(http.MethodPost, "/assistant/query", strings.NewReader(`{
		"query":"Turn this objective into a multi-agent Codex loop for research, design, implementation, review, gate, and completion.",
		"mode":"workflow"
	}`))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()

	assistantQueryHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}
	var payload struct {
		Answer   string              `json:"answer"`
		Source   string              `json:"source"`
		Mode     string              `json:"mode"`
		Actions  []osAssistantAction `json:"actions"`
		Artifact meetingMemoryEntry  `json:"artifact"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Mode != "chat" || payload.Source != "assistant" || payload.Artifact.ID != "" {
		t.Fatalf("legacy workflow selector crossed the chat boundary: %#v", payload)
	}
	if len(kanbanApp.osArtifactsSnapshot(10)) != 0 {
		t.Fatalf("legacy workflow selector created an artifact: %#v", kanbanApp.osArtifactsSnapshot(10))
	}
	if hasAssistantAction(payload.Actions, "open_tool", "chat", payload.Artifact.ID) {
		t.Fatalf("legacy workflow selector exposed an artifact action: %#v", payload.Actions)
	}
}

func TestAssistantQueryInfersOSNavigationActions(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	artifact, _, err := kanbanApp.createOSArtifact("research", "latest customer brief", "Research brief\n\nCustomer evidence.", "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}

	artifactPayload := postAssistantQueryForTest(t, `{"query":"open the latest artifact and show the artifacts app","mode":"chat"}`)
	if !hasAssistantAction(artifactPayload.Actions, "open_tool", "artifacts", artifact.ID) {
		t.Fatalf("actions=%#v, want artifacts open_tool for %q", artifactPayload.Actions, artifact.ID)
	}
	if !hasAssistantAction(artifactPayload.Actions, "select_artifact", "artifacts", artifact.ID) {
		t.Fatalf("actions=%#v, want select_artifact for %q", artifactPayload.Actions, artifact.ID)
	}

	designPayload := postAssistantQueryForTest(t, `{"query":"kick off design work for the investor workflow","mode":"chat"}`)
	if !hasAssistantAction(designPayload.Actions, "open_tool", "design", "") {
		t.Fatalf("actions=%#v, want design open_tool", designPayload.Actions)
	}

	chatPayload := postAssistantQueryForTest(t, `{"query":"open the chat app and start a thread with Scout","mode":"chat"}`)
	if !hasAssistantAction(chatPayload.Actions, "open_tool", "chat", "") {
		t.Fatalf("actions=%#v, want chat open_tool", chatPayload.Actions)
	}
}

func TestArtifactsHandlerListsSavedArtifactsForSignedInUser(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	if _, _, err := kanbanApp.createOSArtifact("research", "how should we validate demand?", "Research brief\n\n1. Interview operators.", "AJ"); err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/artifacts", nil)
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()

	artifactsHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}
	var payload struct {
		Artifacts []meetingMemoryEntry `json:"artifacts"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Artifacts) != 1 {
		t.Fatalf("artifacts=%d, want 1", len(payload.Artifacts))
	}
	if payload.Artifacts[0].Metadata["title"] == "" {
		t.Fatalf("artifact missing title metadata: %#v", payload.Artifacts[0])
	}
}

// The /artifacts LIST ships only an excerpt of each body so the payload stays
// small as artifacts accumulate; the full body is fetched per-artifact via
// ?id=. The stored entries must never be mutated by the list view.
func TestArtifactsHandlerListTrimsLargeBodiesButIDReturnsFull(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	bigBody := "# Big Report\n\n" + strings.Repeat("Samsung TV Plus reach and engagement. ", 400)
	if len([]rune(bigBody)) <= artifactListExcerptRunes {
		t.Fatalf("test body must exceed the excerpt (%d runes)", artifactListExcerptRunes)
	}
	big, _, err := kanbanApp.createOSArtifact("research", "big brief", bigBody, "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact big: %v", err)
	}
	// createOSArtifact may normalize the body (e.g. trailing whitespace), so the
	// stored text is the source of truth for "full body", not the raw input.
	storedBig, ok := kanbanApp.osArtifactByID(big.ID)
	if !ok {
		t.Fatalf("stored big artifact missing right after create")
	}
	fullBody := storedBig.Text
	smallBody := "# Small\n\nJust a line."
	if _, _, err := kanbanApp.createOSArtifact("research", "small brief", smallBody, "AJ"); err != nil {
		t.Fatalf("createOSArtifact small: %v", err)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")

	// LIST: big body trimmed to the excerpt + flagged; small body untouched.
	listReq := httptest.NewRequest(http.MethodGet, "/artifacts", nil)
	for _, cookie := range cookies {
		listReq.AddCookie(cookie)
	}
	listRec := httptest.NewRecorder()
	artifactsHandler(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s, want 200", listRec.Code, listRec.Body.String())
	}
	var listPayload struct {
		Artifacts []meetingMemoryEntry `json:"artifacts"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var sawBig, sawSmall bool
	for _, entry := range listPayload.Artifacts {
		switch entry.ID {
		case big.ID:
			sawBig = true
			if entry.Metadata["bodyTrimmed"] != "true" {
				t.Fatalf("big artifact not flagged bodyTrimmed: %#v", entry.Metadata)
			}
			if len([]rune(entry.Text)) != artifactListExcerptRunes {
				t.Fatalf("big excerpt = %d runes, want %d", len([]rune(entry.Text)), artifactListExcerptRunes)
			}
			if !strings.HasPrefix(entry.Text, "# Big Report") {
				t.Fatalf("excerpt lost its leading prefix: %q", entry.Text[:20])
			}
		case "":
			// skip
		default:
			if entry.Metadata["title"] == "small brief" {
				sawSmall = true
				if entry.Metadata["bodyTrimmed"] == "true" {
					t.Fatalf("small artifact should not be trimmed")
				}
				if entry.Text != smallBody {
					t.Fatalf("small body altered: %q", entry.Text)
				}
			}
		}
	}
	if !sawBig || !sawSmall {
		t.Fatalf("list missing artifacts (big=%v small=%v)", sawBig, sawSmall)
	}

	// ?id= returns the FULL untrimmed body.
	idReq := httptest.NewRequest(http.MethodGet, "/artifacts?id="+big.ID, nil)
	for _, cookie := range cookies {
		idReq.AddCookie(cookie)
	}
	idRec := httptest.NewRecorder()
	artifactsHandler(idRec, idReq)
	if idRec.Code != http.StatusOK {
		t.Fatalf("id status=%d body=%s, want 200", idRec.Code, idRec.Body.String())
	}
	var idPayload struct {
		Artifacts      []meetingMemoryEntry   `json:"artifacts"`
		DispositionRef ArtifactDispositionRef `json:"dispositionRef"`
	}
	if err := json.Unmarshal(idRec.Body.Bytes(), &idPayload); err != nil {
		t.Fatalf("decode id: %v", err)
	}
	if len(idPayload.Artifacts) != 1 {
		t.Fatalf("id artifacts=%d, want 1", len(idPayload.Artifacts))
	}
	if idPayload.Artifacts[0].Text != fullBody {
		t.Fatalf("?id= body length=%d, want full %d", len(idPayload.Artifacts[0].Text), len(bigBody))
	}
	if idPayload.Artifacts[0].Metadata["bodyTrimmed"] == "true" {
		t.Fatalf("?id= must not flag bodyTrimmed")
	}
	if idPayload.DispositionRef.Validate() != nil || idPayload.DispositionRef.ArtifactID != big.ID {
		t.Fatalf("?id= disposition ref=%+v", idPayload.DispositionRef)
	}

	// The stored entry is unmutated: still full body, no trim flag.
	stored, ok := kanbanApp.osArtifactByID(big.ID)
	if !ok {
		t.Fatalf("stored artifact missing")
	}
	if stored.Text != fullBody {
		t.Fatalf("stored body was mutated by the list view (len=%d, want %d)", len(stored.Text), len(bigBody))
	}
	if stored.Metadata["bodyTrimmed"] == "true" {
		t.Fatalf("list view leaked bodyTrimmed into stored metadata")
	}
}

// artifactListView caps the near-duplicate free-text metadata (query/objective/
// threadQuery) that dominate a list payload, preserves structured metadata that
// drives rendering (goalPlan), flags anything trimmed, and never mutates the
// stored entries.
func TestArtifactListViewCapsHeavyMetadataPreservesGoalPlan(t *testing.T) {
	bigObjective := strings.Repeat("o", artifactListMetaFieldCap+500)
	bigPlan := strings.Repeat("g", 5000)
	big := meetingMemoryEntry{
		ID:   "big",
		Kind: "os_artifact",
		Text: strings.Repeat("x", artifactListExcerptRunes+500),
		Metadata: map[string]string{
			"title":     "Big",
			"objective": bigObjective,
			"query":     bigObjective,
			"goalPlan":  bigPlan,
		},
	}
	small := meetingMemoryEntry{ID: "small", Text: "short", Metadata: map[string]string{"title": "Small"}}

	view := artifactListView([]meetingMemoryEntry{big, small})

	if len([]rune(view[0].Text)) != artifactListExcerptRunes {
		t.Fatalf("body excerpt = %d runes, want %d", len([]rune(view[0].Text)), artifactListExcerptRunes)
	}
	if len([]rune(view[0].Metadata["objective"])) != artifactListMetaFieldCap {
		t.Fatalf("objective cap = %d, want %d", len([]rune(view[0].Metadata["objective"])), artifactListMetaFieldCap)
	}
	if len([]rune(view[0].Metadata["query"])) != artifactListMetaFieldCap {
		t.Fatalf("query not capped")
	}
	if view[0].Metadata["goalPlan"] != bigPlan {
		t.Fatalf("goalPlan must NOT be capped (goalcards render from it)")
	}
	if view[0].Metadata["bodyTrimmed"] != "true" {
		t.Fatalf("trimmed entry not flagged bodyTrimmed")
	}
	if view[1].Metadata["bodyTrimmed"] == "true" {
		t.Fatalf("small entry should not be flagged")
	}

	// originals untouched
	if len(big.Text) != artifactListExcerptRunes+500 || big.Metadata["objective"] != bigObjective {
		t.Fatalf("artifactListView mutated the stored entry")
	}
	if _, leaked := big.Metadata["bodyTrimmed"]; leaked {
		t.Fatalf("bodyTrimmed leaked into the stored metadata map")
	}
}

// The trust boundary is the signed-in team: every account lists, reads, and
// edits artifacts. ONLY the external-write approval actions stay admin-only.
func TestArtifactsOpenToAllSignedInUsersExceptExternalWriteApproval(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	artifact, _, err := kanbanApp.createOSArtifact("research", "team brief", "Research brief\n\n1. Interview operators.", "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}

	timCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")

	// Non-admin list: 200 with full bodies.
	req := httptest.NewRequest(http.MethodGet, "/artifacts", nil)
	for _, cookie := range timCookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	artifactsHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("non-admin list status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	var listPayload struct {
		Artifacts []meetingMemoryEntry `json:"artifacts"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listPayload.Artifacts) != 1 || !strings.Contains(listPayload.Artifacts[0].Text, "Interview operators") {
		t.Fatalf("artifacts=%#v, want the full body for a non-admin reader", listPayload.Artifacts)
	}

	// Non-admin edit: 200, with updatedBy audit stamping.
	req = httptest.NewRequest(http.MethodPatch, "/artifacts", strings.NewReader(fmt.Sprintf(`{"id":%q,"title":"Operator interviews","text":"Research brief\n\n1. Interview operators.\n2. Validate pricing."}`, artifact.ID)))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range timCookies {
		req.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	artifactsHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("non-admin update status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	updated, found := kanbanApp.osArtifactByID(artifact.ID)
	if !found || updated.Metadata["title"] != "Operator interviews" {
		t.Fatalf("artifact after non-admin edit=%#v, want the edit applied", updated.Metadata)
	}
	if updated.Metadata["updatedBy"] == "" {
		t.Fatal("non-admin edit must stamp updatedBy for the audit trail")
	}

	// External-write approval stays admin-only: approve and reject 403.
	for _, action := range []string{"approve", "reject"} {
		req = httptest.NewRequest(http.MethodPost, "/artifacts/action", strings.NewReader(fmt.Sprintf(`{"id":%q,"action":%q}`, artifact.ID, action)))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range timCookies {
			req.AddCookie(cookie)
		}
		recorder = httptest.NewRecorder()
		artifactRunnerActionHandler(recorder, req)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("non-admin %s status=%d body=%s, want 403", action, recorder.Code, recorder.Body.String())
		}
	}

	// Rerun is the POST /assistant/threads capability — open to any account.
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	req = httptest.NewRequest(http.MethodPost, "/artifacts/action", strings.NewReader(fmt.Sprintf(`{"id":%q,"action":"rerun"}`, artifact.ID)))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range timCookies {
		req.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	artifactRunnerActionHandler(recorder, req)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("non-admin rerun status=%d body=%s, want 202", recorder.Code, recorder.Body.String())
	}
}

func TestAssistantMemoryAndCurrentWorkSurfacesReadableWithoutRoomJoin(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	for _, tc := range []struct {
		name    string
		path    string
		handler http.HandlerFunc
		key     string
	}{
		{name: "memory", path: "/assistant/memory", handler: assistantMemoryHandler, key: "memory"},
		{name: "agent mind", path: "/assistant/agent-mind", handler: assistantAgentMindHandler, key: "positions"},
		{name: "meetings", path: "/assistant/meetings", handler: assistantMeetingsHandler, key: "meetings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Signed-out reads stay rejected.
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			recorder := httptest.NewRecorder()
			tc.handler(recorder, req)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("signed-out status=%d, want %d", recorder.Code, http.StatusUnauthorized)
			}

			// Any authenticated session reads without joining the video call —
			// non-admin accounts included.
			req = httptest.NewRequest(http.MethodGet, tc.path, nil)
			for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
				req.AddCookie(cookie)
			}
			recorder = httptest.NewRecorder()
			tc.handler(recorder, req)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if _, ok := payload[tc.key]; !ok {
				t.Fatalf("response=%s, want %q payload", recorder.Body.String(), tc.key)
			}
		})
	}
}

func TestShouldServeIndexHTMLGatesAPIishPaths(t *testing.T) {
	for _, tc := range []struct {
		method string
		path   string
		accept string
		want   bool
	}{
		{method: http.MethodGet, path: "/", accept: "", want: true},
		{method: http.MethodGet, path: "/index.html", accept: "", want: true},
		{method: http.MethodGet, path: "/some-page", accept: "text/html,application/xhtml+xml", want: true},
		// Native Studio routes are browser-navigation entrypoints. The client
		// validates the artifact id and mode after the authenticated shell boots.
		{method: http.MethodGet, path: "/studio/deck/deck-proof", accept: "text/html,application/xhtml+xml", want: true},
		{method: http.MethodGet, path: "/studio/deck/deck-proof?mode=present", accept: "text/html", want: true},
		{method: http.MethodGet, path: "/studio/document/report-proof?mode=edit", accept: "text/html", want: true},
		// API-ish paths must 404 instead of serving the SPA, even to browsers.
		{method: http.MethodGet, path: "/assistant/unknown", accept: "text/html", want: false},
		{method: http.MethodGet, path: "/brain/state", accept: "*/*", want: false},
		{method: http.MethodGet, path: "/api/anything", accept: "text/html", want: false},
		// fetch() calls without a text/html accept get a real 404.
		{method: http.MethodGet, path: "/some.json", accept: "application/json", want: false},
		{method: http.MethodPost, path: "/", accept: "text/html", want: false},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.accept != "" {
			req.Header.Set("Accept", tc.accept)
		}
		if got := shouldServeIndexHTML(req); got != tc.want {
			t.Fatalf("shouldServeIndexHTML(%s %s accept=%q)=%v, want %v", tc.method, tc.path, tc.accept, got, tc.want)
		}
	}
}

func TestAssistantThreadsHandlerIsRetiredForAdmin(t *testing.T) {
	setupAuthTestEnv(t)
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	req := httptest.NewRequest(http.MethodPost, "/assistant/threads", strings.NewReader(`{
		"query":"Research the buyer proof for a Realtime 2 as UI workflow.",
		"mode":"research"
	}`))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()

	assistantThreadsHandler(recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf("status=%d body=%s, want retired direct-launch boundary", recorder.Code, recorder.Body.String())
	}
	if len(kanbanApp.osArtifactsSnapshot(10)) != 0 {
		t.Fatalf("retired /assistant/threads created work: %#v", kanbanApp.osArtifactsSnapshot(10))
	}
}

// Retirement is role-independent: a non-admin cannot revive the client-selected
// mode boundary either.
func TestAssistantThreadsHandlerIsRetiredForNonAdminUser(t *testing.T) {
	setupAuthTestEnv(t)
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	req := httptest.NewRequest(http.MethodPost, "/assistant/threads", strings.NewReader(`{
		"query":"Research the buyer proof for a Realtime 2 as UI workflow.",
		"mode":"research"
	}`))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()

	assistantThreadsHandler(recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf("status=%d body=%s, want retired direct-launch boundary", recorder.Code, recorder.Body.String())
	}
	if len(kanbanApp.osArtifactsSnapshot(10)) != 0 {
		t.Fatalf("retired /assistant/threads created non-admin work: %#v", kanbanApp.osArtifactsSnapshot(10))
	}
}

func TestAssistantChatThreadsKeepLegacyInlineAttachmentsEphemeral(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.mu.Lock()
	kanbanApp.apiKey = "test-key"
	kanbanApp.mu.Unlock()
	t.Cleanup(func() { kanbanApp = previousApp })

	var capturedInput string
	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		capturedInput = request.Input
		return "Use the attached brief as launch context.", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	createReq := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads", strings.NewReader(`{"title":"Scout"}`))
	createReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		createReq.AddCookie(cookie)
	}
	createRecorder := httptest.NewRecorder()
	assistantChatThreadsHandler(createRecorder, createReq)
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s, want %d", createRecorder.Code, createRecorder.Body.String(), http.StatusCreated)
	}
	var createPayload struct {
		Thread scoutChatThreadRecord `json:"thread"`
	}
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &createPayload); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if createPayload.Thread.ID == "" {
		t.Fatalf("created thread missing id: %#v", createPayload.Thread)
	}

	messageBody := fmt.Sprintf(`{
		"text":"Use this for the campaign plan",
		"operationId":"assistant-http-attachment-message-0001",
		"files":[{"name":"brief.txt","kind":"text/plain","size":42,"text":"Audience: rodeo creators\nBudget: 12k"}]
	}`)
	messageReq := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+createPayload.Thread.ID+"/messages", strings.NewReader(messageBody))
	messageReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		messageReq.AddCookie(cookie)
	}
	messageRecorder := httptest.NewRecorder()
	assistantChatThreadHandler(messageRecorder, messageReq)
	if messageRecorder.Code != http.StatusOK {
		t.Fatalf("message status=%d body=%s, want %d", messageRecorder.Code, messageRecorder.Body.String(), http.StatusOK)
	}
	var messagePayload struct {
		Thread scoutChatThreadRecord  `json:"thread"`
		Answer scoutChatMessageRecord `json:"answer"`
	}
	if err := json.Unmarshal(messageRecorder.Body.Bytes(), &messagePayload); err != nil {
		t.Fatalf("decode message response: %v", err)
	}
	if len(messagePayload.Thread.Messages) != 2 {
		t.Fatalf("messages=%d, want user+scout", len(messagePayload.Thread.Messages))
	}
	if len(messagePayload.Thread.Messages[0].Files) != 0 {
		t.Fatalf("legacy inline attachment leaked through viewer projection: %+v", messagePayload.Thread.Messages[0].Files)
	}
	if !strings.Contains(capturedInput, "Audience: rodeo creators") || !strings.Contains(capturedInput, "brief.txt") {
		t.Fatalf("current authenticated prompt omitted submitted inline context: %s", capturedInput)
	}
	if messagePayload.Answer.Text != "Use the attached brief as launch context." {
		t.Fatalf("answer=%q, want responder output", messagePayload.Answer.Text)
	}
	rawThread, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", createPayload.Thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, turn := range kanbanApp.scoutChatHistoryForViewer("aj@shareability.com", rawThread) {
		if strings.Contains(turn.text, "Audience: rodeo creators") || strings.Contains(turn.text, "brief.txt") {
			t.Fatalf("legacy inline attachment survived into later model history: %+v", turn)
		}
	}

	listReq := httptest.NewRequest(http.MethodGet, "/assistant/chat-threads", nil)
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		listReq.AddCookie(cookie)
	}
	listRecorder := httptest.NewRecorder()
	assistantChatThreadsHandler(listRecorder, listReq)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s, want %d", listRecorder.Code, listRecorder.Body.String(), http.StatusOK)
	}
	var listPayload struct {
		Threads []scoutChatThreadRecord `json:"threads"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	// Find by id rather than index: the auto-provisioned Table (#team) is
	// always present and sorts first.
	persisted := false
	for _, listed := range listPayload.Threads {
		if listed.ID == createPayload.Thread.ID {
			persisted = true
		}
	}
	if !persisted {
		t.Fatalf("threads=%#v, want persisted thread", listPayload.Threads)
	}
}

func TestAssistantChatThreadsArchiveHidesFromDefaultList(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Archive me", "")
	if err != nil {
		t.Fatalf("createScoutChatThread: %v", err)
	}
	if matches := kanbanApp.memory.search("Archive me", 10); len(matches) != 0 {
		t.Fatalf("chat thread leaked into memory search: %#v", matches)
	}
	for _, entry := range kanbanApp.memorySnapshotForClients(10) {
		if entry.Kind == meetingMemoryKindScoutChat {
			t.Fatalf("chat thread leaked into client memory snapshot: %#v", entry)
		}
	}

	req := httptest.NewRequest(http.MethodPatch, "/assistant/chat-threads/"+thread.ID, strings.NewReader(`{"archived":true}`))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantChatThreadHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("archive status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/assistant/chat-threads", nil)
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		listReq.AddCookie(cookie)
	}
	listRecorder := httptest.NewRecorder()
	assistantChatThreadsHandler(listRecorder, listReq)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s, want %d", listRecorder.Code, listRecorder.Body.String(), http.StatusOK)
	}
	var listPayload struct {
		Threads []scoutChatThreadRecord `json:"threads"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	// Assert on the archived thread specifically rather than on list length:
	// the list is now auto-provisioned with the Table (#team) on first load,
	// so it is never empty.
	for _, listed := range listPayload.Threads {
		if listed.ID == thread.ID {
			t.Fatalf("threads=%#v, want archived thread hidden", listPayload.Threads)
		}
	}

	archivedReq := httptest.NewRequest(http.MethodGet, "/assistant/chat-threads?archived=true", nil)
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		archivedReq.AddCookie(cookie)
	}
	archivedRecorder := httptest.NewRecorder()
	assistantChatThreadsHandler(archivedRecorder, archivedReq)
	if archivedRecorder.Code != http.StatusOK {
		t.Fatalf("archived list status=%d body=%s, want %d", archivedRecorder.Code, archivedRecorder.Body.String(), http.StatusOK)
	}
	if err := json.Unmarshal(archivedRecorder.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("decode archived list response: %v", err)
	}
	archivedListed := false
	for _, listed := range listPayload.Threads {
		if listed.ID == thread.ID && listed.ArchivedAt != "" {
			archivedListed = true
		}
	}
	if !archivedListed {
		t.Fatalf("threads=%#v, want archived thread when requested", listPayload.Threads)
	}
}

func TestArtifactsHandlerUpdatesSavedArtifactForSignedInUser(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	artifact, _, err := kanbanApp.createOSArtifact("design", "draft a workspace", "Design kickoff\n\nOriginal body.", "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}

	body := fmt.Sprintf(`{"id":%q,"title":"Edited artifact","text":"Edited body\n\nWith details."}`, artifact.ID)
	req := httptest.NewRequest(http.MethodPatch, "/artifacts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()

	artifactsHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}
	var payload struct {
		Updated  bool               `json:"updated"`
		Artifact meetingMemoryEntry `json:"artifact"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.Updated {
		t.Fatal("updated=false, want true")
	}
	if payload.Artifact.Text != "Edited body\n\nWith details." {
		t.Fatalf("artifact text=%q, want edited body", payload.Artifact.Text)
	}
	if payload.Artifact.Metadata["title"] != "Edited artifact" || payload.Artifact.Metadata["updatedBy"] != "AJ" {
		t.Fatalf("artifact metadata=%v, want title and updater", payload.Artifact.Metadata)
	}
}

func TestArtifactsHandlerPublishesSavedArtifactForSignedInUser(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	artifact, _, err := kanbanApp.createOSArtifact("workflow", "launch a multi-agent loop", "Codex goal workflow\n\n1. Identify and set goal.", "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}

	body := fmt.Sprintf(`{"id":%q,"title":"Published workflow","text":"Codex goal workflow\n\nReady.","published":true}`, artifact.ID)
	req := httptest.NewRequest(http.MethodPatch, "/artifacts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()

	artifactsHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}
	var payload struct {
		Updated  bool               `json:"updated"`
		Artifact meetingMemoryEntry `json:"artifact"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.Updated {
		t.Fatal("updated=false, want true")
	}
	if payload.Artifact.Metadata["published"] != "true" || payload.Artifact.Metadata["status"] != "published" || payload.Artifact.Metadata["publishedAt"] == "" {
		t.Fatalf("artifact metadata=%v, want published status", payload.Artifact.Metadata)
	}
	if artifactShareEligible(payload.Artifact) || payload.Artifact.Metadata[artifactHumanApprovedAtKey] != "" {
		t.Fatalf("body+publish retained stale approval eligibility: %v", payload.Artifact.Metadata)
	}
	published := kanbanApp.publishedOSArtifactsSnapshot(5)
	if len(published) != 1 || published[0].ID != artifact.ID {
		t.Fatalf("published=%#v, want published artifact %q", published, artifact.ID)
	}
}

func postAssistantQueryForTest(t *testing.T, body string) struct {
	Actions []osAssistantAction `json:"actions"`
} {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/assistant/query", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()

	assistantQueryHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}
	var payload struct {
		Actions []osAssistantAction `json:"actions"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return payload
}

func hasAssistantAction(actions []osAssistantAction, actionType string, tool string, artifactID string) bool {
	for _, action := range actions {
		if action.Type != actionType || action.Tool != tool {
			continue
		}
		if artifactID != "" && action.ArtifactID != artifactID {
			continue
		}
		return true
	}
	return false
}
