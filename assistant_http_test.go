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
	t.Setenv("OPENAI_API_KEY", "")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	req := httptest.NewRequest(http.MethodPost, "/assistant/realtime-offer", strings.NewReader(`{"sdp":"v=0"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	assistantRealtimeOfferHandler(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want %d for unauthenticated private realtime offer", recorder.Code, http.StatusUnauthorized)
	}

	req = httptest.NewRequest(http.MethodPost, "/assistant/realtime-offer", strings.NewReader(`{"sdp":"v=0"}`))
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

func TestAssistantRealtimeOfferForwardsTypedMultipartToOpenAI(t *testing.T) {
	setupAuthTestEnv(t)
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
		if !strings.Contains(parts["session"].body, `"model":"gpt-realtime-2"`) {
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

	emptyReq := httptest.NewRequest(http.MethodPost, "/assistant/realtime-offer", strings.NewReader(`{"sdp":"   "}`))
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

	req := httptest.NewRequest(http.MethodPost, "/assistant/realtime-offer", strings.NewReader(`{"sdp":"v=0\r\n"}`))
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
		SDP string `json:"sdp"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.SDP != "v=0\r\n" {
		t.Fatalf("response sdp=%q, want CRLF-normalized mock answer", payload.SDP)
	}
}

func TestAssistantRealtimeOfferReportsQuotaBlocker(t *testing.T) {
	setupAuthTestEnv(t)
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

	req := httptest.NewRequest(http.MethodPost, "/assistant/realtime-offer", strings.NewReader(`{"sdp":"v=0\r\n"}`))
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
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	for _, name := range []string{"set_voice_control", "set_recording"} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/assistant/realtime-tool", strings.NewReader(fmt.Sprintf(`{"name":%q,"arguments":{"enabled":true}}`, name)))
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
					OK    bool   `json:"ok"`
					Error string `json:"error"`
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
			want := fmt.Sprintf("private Realtime voice cannot use %q", name)
			if !strings.Contains(payload.Result.Error, want) {
				t.Fatalf("error=%q, want %q", payload.Result.Error, want)
			}
		})
	}
}

func TestAssistantQueryAnswersFromBoardWithoutRoom(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

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
	if payload.Source != "board" {
		t.Fatalf("source=%q, want board", payload.Source)
	}
	if payload.Mode != "chat" || payload.User != "AJ" {
		t.Fatalf("payload mode/user=%q/%q, want chat/AJ", payload.Mode, payload.User)
	}
	if !strings.Contains(payload.Answer, "Finish RTP HEVC Packetizer") {
		t.Fatalf("answer=%q, want board card answer", payload.Answer)
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

func TestAssistantQueryShapesOSModesWithoutModelKey(t *testing.T) {
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
	if payload.Mode != "grill" || payload.Source != "grill" {
		t.Fatalf("payload mode/source=%q/%q, want grill/grill", payload.Mode, payload.Source)
	}
	for _, want := range []string{"Grill mode scorecard", "Final score:", "Tough questions"} {
		if !strings.Contains(payload.Answer, want) {
			t.Fatalf("grill answer missing %q: %s", want, payload.Answer)
		}
	}

	artifacts := kanbanApp.osArtifactsSnapshot(10)
	if len(artifacts) != 1 {
		t.Fatalf("artifacts=%d, want 1 saved grill artifact", len(artifacts))
	}
	if artifacts[0].Kind != meetingMemoryKindOSArtifact || artifacts[0].Metadata["mode"] != "grill" {
		t.Fatalf("artifact kind/mode=%q/%q, want os_artifact/grill", artifacts[0].Kind, artifacts[0].Metadata["mode"])
	}
	if !strings.Contains(artifacts[0].Text, "Grill mode scorecard") {
		t.Fatalf("artifact text=%q, want scorecard", artifacts[0].Text)
	}
	if payload.Artifact.ID == "" {
		t.Fatalf("response missing saved artifact: %#v", payload)
	}
	if !hasAssistantAction(payload.Actions, "open_tool", "chat", payload.Artifact.ID) {
		t.Fatalf("actions=%#v, want chat open_tool for grill artifact %q", payload.Actions, payload.Artifact.ID)
	}
}

func TestAssistantQueryCreatesCodexWorkflowArtifact(t *testing.T) {
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
	if payload.Mode != "workflow" || payload.Source != "workflow" {
		t.Fatalf("payload mode/source=%q/%q, want workflow/workflow", payload.Mode, payload.Source)
	}
	for _, want := range []string{"Codex goal workflow", "Identify and set goal", "Gate before shipping", "Codex handoff"} {
		if !strings.Contains(payload.Answer, want) {
			t.Fatalf("workflow answer missing %q: %s", want, payload.Answer)
		}
	}
	if payload.Artifact.ID == "" || payload.Artifact.Metadata["mode"] != "workflow" {
		t.Fatalf("response artifact=%#v, want saved workflow artifact", payload.Artifact)
	}
	if payload.Artifact.Metadata["workflow"] != "codex_goal_loop" || payload.Artifact.Metadata["codexRunner"] != "not_connected" {
		t.Fatalf("workflow metadata=%v, want codex workflow scaffold", payload.Artifact.Metadata)
	}
	if !strings.Contains(payload.Artifact.Metadata["workflowStages"], "verify_goal_completed") {
		t.Fatalf("workflowStages=%q, want verify stage", payload.Artifact.Metadata["workflowStages"])
	}
	if !hasAssistantAction(payload.Actions, "open_tool", "chat", payload.Artifact.ID) {
		t.Fatalf("actions=%#v, want chat open_tool for workflow artifact %q", payload.Actions, payload.Artifact.ID)
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

func TestArtifactsHandlerRejectsNonAdminUser(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	artifact, _, err := kanbanApp.createOSArtifact("research", "restricted brief", "Research brief\n\n1. Interview operators.", "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}

	for _, tc := range []struct {
		name    string
		method  string
		path    string
		body    string
		handler http.HandlerFunc
	}{
		{
			name:    "list",
			method:  http.MethodGet,
			path:    "/artifacts",
			handler: artifactsHandler,
		},
		{
			name:    "update",
			method:  http.MethodPatch,
			path:    "/artifacts",
			body:    fmt.Sprintf(`{"id":%q,"title":"Nope","text":"Nope"}`, artifact.ID),
			handler: artifactsHandler,
		},
		{
			name:    "action",
			method:  http.MethodPost,
			path:    "/artifacts/action",
			body:    fmt.Sprintf(`{"id":%q,"action":"approve"}`, artifact.ID),
			handler: artifactRunnerActionHandler,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
				req.AddCookie(cookie)
			}
			recorder := httptest.NewRecorder()

			tc.handler(recorder, req)

			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusForbidden)
			}
		})
	}
}

func TestAssistantThreadsHandlerLaunchesRunningArtifact(t *testing.T) {
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

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusAccepted)
	}
	var payload struct {
		Thread   scoutAgentThread    `json:"thread"`
		Artifact meetingMemoryEntry  `json:"artifact"`
		Actions  []osAssistantAction `json:"actions"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Thread.ID == "" || payload.Thread.Status != "running" {
		t.Fatalf("thread=%#v, want running thread", payload.Thread)
	}
	if payload.Artifact.Kind != meetingMemoryKindOSArtifact || payload.Artifact.Metadata["source"] != "scout_thread" || payload.Artifact.Metadata["status"] != "running" {
		t.Fatalf("artifact=%#v, want running scout thread artifact", payload.Artifact)
	}
	if payload.Artifact.Metadata["agentLoop"] != "realtime_controlled_workforce" || payload.Artifact.Metadata["progressPercent"] != "35" {
		t.Fatalf("artifact metadata=%v, want realtime workforce progress metadata", payload.Artifact.Metadata)
	}
	if !strings.Contains(payload.Artifact.Text, "Scout work thread") || !strings.Contains(payload.Artifact.Text, "Goal workflow") {
		t.Fatalf("artifact text=%q, want thread scaffold", payload.Artifact.Text)
	}
	if !hasAssistantAction(payload.Actions, "open_tool", "chat", payload.Artifact.ID) {
		t.Fatalf("actions=%#v, want chat open action for research artifact", payload.Actions)
	}
}

func TestAssistantThreadsHandlerHidesArtifactBodyForNonAdminUser(t *testing.T) {
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

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusAccepted)
	}
	var payload struct {
		Thread   scoutAgentThread   `json:"thread"`
		Artifact meetingMemoryEntry `json:"artifact"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Artifact.ID == "" || payload.Thread.Artifact.ID == "" {
		t.Fatalf("artifact ids missing: %#v", payload)
	}
	if payload.Artifact.Text != "" || payload.Thread.Artifact.Text != "" {
		t.Fatalf("non-admin artifact text=%q thread text=%q, want hidden bodies", payload.Artifact.Text, payload.Thread.Artifact.Text)
	}
	if payload.Artifact.Metadata["restricted"] != "true" || payload.Thread.Artifact.Metadata["restricted"] != "true" {
		t.Fatalf("metadata=%v/%v, want restricted marker", payload.Artifact.Metadata, payload.Thread.Artifact.Metadata)
	}
}

func TestAssistantChatThreadsPersistMessagesAndAttachments(t *testing.T) {
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
	if got := messagePayload.Thread.Messages[0].Files[0].Name; got != "brief.txt" {
		t.Fatalf("attachment name=%q, want brief.txt", got)
	}
	if !strings.Contains(capturedInput, "Audience: rodeo creators") {
		t.Fatalf("model input missing attachment text: %s", capturedInput)
	}
	if messagePayload.Answer.Text != "Use the attached brief as launch context." {
		t.Fatalf("answer=%q, want responder output", messagePayload.Answer.Text)
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
	if len(listPayload.Threads) != 1 || listPayload.Threads[0].ID != createPayload.Thread.ID {
		t.Fatalf("threads=%#v, want persisted thread", listPayload.Threads)
	}
}

func TestAssistantChatThreadsArchiveHidesFromDefaultList(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Archive me")
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
	if len(listPayload.Threads) != 0 {
		t.Fatalf("threads=%#v, want archived thread hidden", listPayload.Threads)
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
	if len(listPayload.Threads) != 1 || listPayload.Threads[0].ArchivedAt == "" {
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
