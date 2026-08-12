package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrivateRealtimeVoiceTransportReconnectAndMilestonesSurviveRestart(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	const voiceSessionID = "voice-reconnect-contract"
	thread, _, err := app.ensurePrivateRealtimeVoiceConversation("aj@shareability.com", "AJ", voiceSessionID)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)
	revision, err := app.beginPrivateRealtimeVoiceTransport("aj@shareability.com", voiceSessionID, thread.ID, startedAt)
	if err != nil || revision != 1 {
		t.Fatalf("first transport revision=%d err=%v", revision, err)
	}
	if err := app.finishPrivateRealtimeVoiceTransport("aj@shareability.com", voiceSessionID, thread.ID, revision, true, startedAt.Add(125*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.appendPrivateRealtimeVoiceTransportMilestone("aj@shareability.com", voiceSessionID, thread.ID, revision, "milestone-too-early", "first_audio", startedAt.Add(200*time.Millisecond)); err == nil {
		t.Fatal("first audio was accepted before the data channel and remote track")
	}
	for index, milestone := range []string{"peer_connected", "data_channel_open", "remote_track", "first_audio", "response_done"} {
		latency, replayed, err := app.appendPrivateRealtimeVoiceTransportMilestone(
			"aj@shareability.com", voiceSessionID, thread.ID, revision,
			"milestone-operation-"+milestone, milestone,
			startedAt.Add(time.Duration(index+2)*100*time.Millisecond),
		)
		if err != nil || replayed {
			t.Fatalf("milestone %s latency=%+v replayed=%v err=%v", milestone, latency, replayed, err)
		}
		if milestone == "first_audio" && latency.FirstAudioMS != 500 {
			t.Fatalf("first audio latency=%+v, want 500ms from offer start", latency)
		}
	}
	latency, replayed, err := app.appendPrivateRealtimeVoiceTransportMilestone(
		"aj@shareability.com", voiceSessionID, thread.ID, revision,
		"milestone-operation-first_audio", "first_audio", startedAt.Add(time.Second),
	)
	if err != nil || !replayed || latency.FirstAudioMS != 500 {
		t.Fatalf("replay latency=%+v replayed=%v err=%v", latency, replayed, err)
	}
	if _, _, err := app.appendPrivateRealtimeVoiceTransportMilestone(
		"aj@shareability.com", voiceSessionID, thread.ID, revision,
		"milestone-operation-first_audio", "transport_error", startedAt.Add(time.Second),
	); err == nil {
		t.Fatal("milestone operation id reuse with changed content was accepted")
	}

	restarted := newKanbanBoardApp()
	reloaded, err := restarted.privateRealtimeVoiceConversation("aj@shareability.com", voiceSessionID, thread.ID)
	if err != nil || reloaded.VoiceSession == nil || reloaded.VoiceSession.TransportRevision != 1 || len(reloaded.VoiceSession.TransportAttempts) != 1 {
		t.Fatalf("reloaded=%+v err=%v", reloaded.VoiceSession, err)
	}
	second, err := restarted.beginPrivateRealtimeVoiceTransport("aj@shareability.com", voiceSessionID, thread.ID, startedAt.Add(2*time.Second))
	if err != nil || second != 2 {
		t.Fatalf("reconnect revision=%d err=%v", second, err)
	}
	if err := restarted.finishPrivateRealtimeVoiceTransport("aj@shareability.com", voiceSessionID, thread.ID, second, true, startedAt.Add(2100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := restarted.appendPrivateRealtimeVoiceTransportMilestone(
		"aj@shareability.com", voiceSessionID, thread.ID, revision,
		"late-old-transport", "transport_error", startedAt.Add(3*time.Second),
	); err == nil || !strings.Contains(err.Error(), "superseded") {
		t.Fatalf("late old transport err=%v, want superseded", err)
	}
	if projected := restarted.projectScoutChatThreadForViewer("aj@shareability.com", reloaded); projected.VoiceSession != nil {
		t.Fatalf("transport receipts leaked to viewer: %+v", projected.VoiceSession)
	}
}

func TestAssistantRealtimeOfferReconnectKeepsThreadAndAdvancesTransport(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-realtime-key")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	previousURL := realtimeCallsURL
	previousClient := realtimeHTTPClient
	providerCalls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls++
		w.Header().Set("Content-Type", "application/sdp")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("v=0\n"))
	}))
	t.Cleanup(func() {
		provider.Close()
		realtimeCallsURL = previousURL
		realtimeHTTPClient = previousClient
	})
	realtimeCallsURL = provider.URL
	realtimeHTTPClient = provider.Client()
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	post := func() struct {
		ThreadID          string `json:"threadId"`
		VoiceSessionID    string `json:"voiceSessionId"`
		TransportRevision int    `json:"transportRevision"`
	} {
		req := httptest.NewRequest(http.MethodPost, "/assistant/realtime-offer", strings.NewReader(`{"sdp":"v=0\r\n","voiceSessionId":"voice-http-reconnect"}`))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantRealtimeOfferHandler(recorder, req)
		if recorder.Code != http.StatusOK {
			t.Fatalf("offer status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var payload struct {
			ThreadID          string `json:"threadId"`
			VoiceSessionID    string `json:"voiceSessionId"`
			TransportRevision int    `json:"transportRevision"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	first := post()
	second := post()
	if first.ThreadID == "" || first.ThreadID != second.ThreadID || first.VoiceSessionID != "voice-http-reconnect" || second.VoiceSessionID != first.VoiceSessionID {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	if first.TransportRevision != 1 || second.TransportRevision != 2 || providerCalls != 2 {
		t.Fatalf("first=%+v second=%+v providerCalls=%d", first, second, providerCalls)
	}
	reloaded, err := newKanbanBoardApp().privateRealtimeVoiceConversation("aj@shareability.com", first.VoiceSessionID, first.ThreadID)
	if err != nil || reloaded.VoiceSession == nil || len(reloaded.VoiceSession.TransportAttempts) != 2 {
		t.Fatalf("reloaded binding=%+v err=%v", reloaded.VoiceSession, err)
	}
}

func TestAssistantRealtimeMilestoneExactBindingAndReplay(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	const voiceSessionID = "voice-bound-milestone"
	thread, _, err := kanbanApp.ensurePrivateRealtimeVoiceConversation("aj@shareability.com", "AJ", voiceSessionID)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := kanbanApp.beginPrivateRealtimeVoiceTransport("aj@shareability.com", voiceSessionID, thread.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := kanbanApp.finishPrivateRealtimeVoiceTransport("aj@shareability.com", voiceSessionID, thread.ID, revision, true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	ajCookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	post := func(cookies []*http.Cookie, operationID, milestone string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{
			"voiceSessionId": voiceSessionID, "threadId": thread.ID,
			"transportRevision": revision, "operationId": operationID, "milestone": milestone,
		})
		req := httptest.NewRequest(http.MethodPost, "/assistant/realtime/milestone", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantRealtimeMilestoneHandler(recorder, req)
		return recorder
	}
	first := post(ajCookies, "bound-milestone-operation", "data_channel_open")
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"replayed":false`) {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	replay := post(ajCookies, "bound-milestone-operation", "data_channel_open")
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), `"replayed":true`) {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	conflict := post(ajCookies, "bound-milestone-operation", "transport_error")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}
	foreign := post(loginAs(t, "tim@shareability.com", "B0NFIRE!"), "foreign-milestone-operation", "peer_connected")
	if foreign.Code != http.StatusConflict {
		t.Fatalf("foreign status=%d body=%s", foreign.Code, foreign.Body.String())
	}
}
