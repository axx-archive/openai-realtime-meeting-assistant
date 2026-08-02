package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recordedMeetingSpecialistRealtimeConn struct {
	mu            sync.Mutex
	reads         chan []byte
	writes        []map[string]any
	writeAttempts int
	failWriteAt   int
	closed        chan struct{}
	closeOnce     sync.Once
	limit         int64
}

func newRecordedMeetingSpecialistRealtimeConn() *recordedMeetingSpecialistRealtimeConn {
	return &recordedMeetingSpecialistRealtimeConn{reads: make(chan []byte, 64), closed: make(chan struct{})}
}

func (conn *recordedMeetingSpecialistRealtimeConn) WriteJSON(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var event map[string]any
	if err := json.Unmarshal(raw, &event); err != nil {
		return err
	}
	conn.mu.Lock()
	conn.writeAttempts++
	if conn.failWriteAt > 0 && conn.writeAttempts == conn.failWriteAt {
		conn.mu.Unlock()
		return errors.New("forced recorded websocket write failure")
	}
	conn.writes = append(conn.writes, event)
	conn.mu.Unlock()
	return nil
}

func (conn *recordedMeetingSpecialistRealtimeConn) ReadMessage() (int, []byte, error) {
	select {
	case raw := <-conn.reads:
		return 1, raw, nil
	case <-conn.closed:
		return 0, nil, errors.New("recorded connection closed")
	}
}

func (conn *recordedMeetingSpecialistRealtimeConn) SetReadLimit(limit int64) {
	conn.mu.Lock()
	conn.limit = limit
	conn.mu.Unlock()
}

func (conn *recordedMeetingSpecialistRealtimeConn) SetWriteDeadline(time.Time) error { return nil }

func (conn *recordedMeetingSpecialistRealtimeConn) Close() error {
	conn.closeOnce.Do(func() { close(conn.closed) })
	return nil
}

func (conn *recordedMeetingSpecialistRealtimeConn) push(value any) {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	conn.reads <- raw
}

func (conn *recordedMeetingSpecialistRealtimeConn) writeTypes() []string {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	result := make([]string, 0, len(conn.writes))
	for _, event := range conn.writes {
		result = append(result, asString(event["type"]))
	}
	return result
}

func specialistRealtimeConfigFixture(now time.Time, conn *recordedMeetingSpecialistRealtimeConn) MeetingSpecialistRealtimeConfig {
	config := defaultOffMeetingSpecialistRealtimeConfig()
	config.Enabled = true
	config.APIKey = "test-specialist-backend-key"
	config.SafetyIdentifier = sha256Hex([]byte("specialist-safety-identifier"))
	config.Now = func() time.Time { return now }
	config.ResolveBrief = func(_ context.Context, launch MeetingSpecialistLaunch) (MeetingSpecialistRealtimeBrief, error) {
		brief := MeetingSpecialistRealtimeBrief{Purpose: "Review the authorized meeting evidence"}
		for _, values := range [][]STRIDEReference{launch.Context.TranscriptRefs, launch.Context.AnalysisRefs, launch.Context.BrainRefs, launch.Context.WorkRefs} {
			for _, reference := range values {
				brief.Evidence = append(brief.Evidence, MeetingSpecialistRealtimeBriefEvidence{Reference: reference, Text: "Authorized evidence for " + reference.ID})
			}
		}
		return brief, nil
	}
	config.dial = func(_ context.Context, endpoint string, headers http.Header) (meetingSpecialistRealtimeConn, error) {
		if endpoint != meetingSpecialistRealtimeEndpoint {
			return nil, errors.New("unexpected endpoint")
		}
		if headers.Get("Authorization") != "Bearer test-specialist-backend-key" {
			return nil, errors.New("credential not held in backend header")
		}
		if headers.Get("OpenAI-Safety-Identifier") != config.SafetyIdentifier {
			return nil, errors.New("privacy-preserving safety identifier missing")
		}
		return conn, nil
	}
	return config
}

func bindSpecialistRealtimePurpose(launch *MeetingSpecialistLaunch) {
	launch.Invitation.PurposeDigest = sha256Hex([]byte("Review the authorized meeting evidence"))
}

func qualifySpecialistRealtimeDirectPCMLaunch(launch *MeetingSpecialistLaunch) {
	launch.Invitation.ExpectedCostCents = 2000
	launch.Context.TokenBudget = 130000
	launch.ApprovalLimits.TokenBudget = 130000
	launch.Context.CostBudgetCents = 1500
	launch.ApprovalLimits.CostBudgetCents = 1500
	launch.Policy.CostBudgetCents = 1500
}

func pushSpecialistSessionHandshake(conn *recordedMeetingSpecialistRealtimeConn, config MeetingSpecialistRealtimeConfig, sessionID string) {
	conn.push(map[string]any{
		"type": "session.created", "event_id": "provider-session-created-1",
		"session": map[string]any{"id": sessionID, "type": "realtime", "model": meetingSpecialistRealtimeModel},
	})
	conn.push(map[string]any{
		"type": "conversation.created", "event_id": "provider-conversation-created-1",
		"conversation": map[string]any{"id": "provider-conversation-1", "object": "realtime.conversation"},
	})
	conn.push(map[string]any{
		"type": "session.updated", "event_id": "provider-session-updated-1",
		"session": map[string]any{
			"id": sessionID, "type": "realtime", "model": meetingSpecialistRealtimeModel,
			"max_output_tokens": config.MaxOutputTokens, "output_modalities": []string{"audio"},
			"tools": []any{}, "tool_choice": "none", "parallel_tool_calls": false,
			"reasoning": map[string]any{"effort": config.ReasoningEffort}, "tracing": nil, "truncation": "disabled",
			"audio": map[string]any{
				"input":  map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": meetingSpecialistRealtimeSampleRate}, "turn_detection": nil},
				"output": map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": meetingSpecialistRealtimeSampleRate}, "voice": config.Voice},
			},
		},
	})
}

func newRecordedSpecialistRuntime(t *testing.T, now time.Time, conn *recordedMeetingSpecialistRealtimeConn, publish MeetingSpecialistAudioPublisher) (*MeetingSpecialistRuntime, MeetingAgentSessionLease, MeetingSpecialistLaunch) {
	t.Helper()
	config := specialistRealtimeConfigFixture(now, conn)
	pushSpecialistSessionHandshake(conn, config, "provider-session-secret-id")
	authority := newFakeMeetingSpecialistAuthority()
	runtime := NewMeetingSpecialistRuntime(func() time.Time { return now }, enabledSpecialistHarnessGates(), authority, NewMeetingSpecialistRealtimeProviderFactory(config), publish)
	launch := specialistRuntimeLaunchFixture(now)
	bindSpecialistRealtimePurpose(&launch)
	qualifySpecialistRealtimeDirectPCMLaunch(&launch)
	launch.CapabilityReceipt = authority.issue(launch)
	session, err := runtime.Start(context.Background(), launch)
	if err != nil {
		t.Fatalf("start recorded specialist runtime: %v", err)
	}
	t.Cleanup(func() { runtime.RevokeGates("test_cleanup") })
	return runtime, session, launch
}

func TestMeetingSpecialistRealtimeFactoryIsDefaultOffAndModelPinned(t *testing.T) {
	now := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)
	launch := specialistRuntimeLaunchFixture(now)
	called := false
	config := defaultOffMeetingSpecialistRealtimeConfig()
	config.APIKey = "present-but-disabled"
	config.ResolveBrief = func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistRealtimeBrief, error) {
		return MeetingSpecialistRealtimeBrief{}, nil
	}
	config.dial = func(context.Context, string, http.Header) (meetingSpecialistRealtimeConn, error) {
		called = true
		return newRecordedMeetingSpecialistRealtimeConn(), nil
	}
	if _, err := NewMeetingSpecialistRealtimeProviderFactory(config)(context.Background(), launch); !errors.Is(err, ErrMeetingSpecialistProviderDisabled) || called {
		t.Fatalf("default-off factory err=%v called=%v", err, called)
	}

	for _, badModel := range []string{"", "gpt-realtime-2", "gpt-realtime-2.1-mini"} {
		config.Enabled = true
		config.Model = badModel
		if _, err := NewMeetingSpecialistRealtimeProviderFactory(config)(context.Background(), launch); !errors.Is(err, ErrMeetingSpecialistProviderConfig) || called {
			t.Fatalf("model %q err=%v called=%v", badModel, err, called)
		}
	}
	config.Model = meetingSpecialistRealtimeModel
	unapproved := launch
	unapproved.Invitation.Decision = "requested"
	unapproved.Invitation.DecisionAt = nil
	unapproved.Invitation.DecisionPrincipal = ""
	if _, err := NewMeetingSpecialistRealtimeProviderFactory(config)(context.Background(), unapproved); !errors.Is(err, ErrMeetingSpecialistUnauthorized) || called {
		t.Fatalf("unapproved launch err=%v called=%v", err, called)
	}
}

func TestMeetingSpecialistRealtimeBriefUsesBoundedNoToolServerConfig(t *testing.T) {
	now := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)
	conn := newRecordedMeetingSpecialistRealtimeConn()
	config := specialistRealtimeConfigFixture(now, conn)
	pushSpecialistSessionHandshake(conn, config, "provider-session-secret-id")
	launch := specialistRuntimeLaunchFixture(now)
	bindSpecialistRealtimePurpose(&launch)
	qualifySpecialistRealtimeDirectPCMLaunch(&launch)
	providerValue, err := NewMeetingSpecialistRealtimeProviderFactory(config)(context.Background(), launch)
	if err != nil {
		t.Fatal(err)
	}
	provider := providerValue.(*openAIMeetingSpecialistProvider)
	if err := provider.BindMeetingSpecialistProviderHooks(MeetingSpecialistProviderHooks{
		PublishAudio: func(MeetingAgentFloorLease, []int16, int64, int64) error { return nil },
		CompleteTurn: func(MeetingAgentFloorLease) error { return nil },
		FailSession:  func(string) {},
	}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Brief(context.Background(), launch.Context); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(context.Background(), "test")

	conn.mu.Lock()
	if len(conn.writes) == 0 {
		conn.mu.Unlock()
		t.Fatal("brief wrote no session.update")
	}
	update := conn.writes[0]
	encoded, _ := json.Marshal(update)
	conn.mu.Unlock()
	if strings.Contains(string(encoded), config.APIKey) || strings.Contains(string(encoded), "provider-session-secret-id") {
		t.Fatalf("brief leaked credential or raw provider id: %s", encoded)
	}
	if !strings.Contains(string(encoded), "Review the authorized meeting evidence") || !strings.Contains(string(encoded), "Authorized evidence for transcript-1") {
		t.Fatalf("brief omitted bound purpose/evidence: %s", encoded)
	}
	session := update["session"].(map[string]any)
	if _, hasModelOverride := session["model"]; hasModelOverride || asString(session["tool_choice"]) != "none" || len(session["tools"].([]any)) != 0 || session["parallel_tool_calls"] != false || session["truncation"] != "disabled" {
		t.Fatalf("unbounded session config=%v", session)
	}
	reasoning := session["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" || int64(session["max_output_tokens"].(float64)) != 256 {
		t.Fatalf("reasoning/budget config=%v", session)
	}
	input := session["audio"].(map[string]any)["input"].(map[string]any)
	if input["turn_detection"] != nil {
		t.Fatalf("automatic provider turn creation was enabled: %v", input)
	}
	receipt := provider.MeetingSpecialistProviderReceipt()
	if receipt.BindingDigest == "" || receipt.RequestDigest == "" || receipt.SessionIDHash != sha256Hex([]byte("provider-session-secret-id")) || receipt.EventCount != 3 ||
		receipt.ProtocolSource != meetingSpecialistRealtimeProtocolSource || receipt.ModelSource != meetingSpecialistRealtimeModelSource || receipt.ContractDigest == "" {
		t.Fatalf("immutable receipt=%+v", receipt)
	}
}

func TestMeetingSpecialistRealtimeBriefRejectsUnboundEvidenceBeforeSessionUpdate(t *testing.T) {
	now := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)
	conn := newRecordedMeetingSpecialistRealtimeConn()
	config := specialistRealtimeConfigFixture(now, conn)
	config.ResolveBrief = func(_ context.Context, launch MeetingSpecialistLaunch) (MeetingSpecialistRealtimeBrief, error) {
		brief := MeetingSpecialistRealtimeBrief{Purpose: "Review the authorized meeting evidence"}
		for _, values := range [][]STRIDEReference{launch.Context.TranscriptRefs, launch.Context.AnalysisRefs, launch.Context.BrainRefs, launch.Context.WorkRefs} {
			for _, reference := range values {
				brief.Evidence = append(brief.Evidence, MeetingSpecialistRealtimeBriefEvidence{Reference: reference, Text: "authorized"})
			}
		}
		brief.Evidence = append(brief.Evidence, MeetingSpecialistRealtimeBriefEvidence{
			Reference: STRIDEReference{ContractType: STRIDEContractAnalysisProjection, ID: "unbound-analysis", Revision: 1, Digest: sha256Hex([]byte("unbound"))},
			Text:      "must not enter the session",
		})
		return brief, nil
	}
	launch := specialistRuntimeLaunchFixture(now)
	bindSpecialistRealtimePurpose(&launch)
	qualifySpecialistRealtimeDirectPCMLaunch(&launch)
	providerValue, err := NewMeetingSpecialistRealtimeProviderFactory(config)(context.Background(), launch)
	if err != nil {
		t.Fatal(err)
	}
	provider := providerValue.(*openAIMeetingSpecialistProvider)
	defer provider.Close(context.Background(), "test")
	if err := provider.BindMeetingSpecialistProviderHooks(MeetingSpecialistProviderHooks{
		PublishAudio: func(MeetingAgentFloorLease, []int16, int64, int64) error { return nil },
		CompleteTurn: func(MeetingAgentFloorLease) error { return nil },
		FailSession:  func(string) {},
	}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Brief(context.Background(), launch.Context); !errors.Is(err, ErrMeetingSpecialistProviderProtocol) {
		t.Fatalf("unbound brief err=%v", err)
	}
	if writes := conn.writeTypes(); len(writes) != 0 {
		t.Fatalf("unbound evidence reached provider session: %v", writes)
	}
}

func TestMeetingSpecialistRealtimeRejectsReturnedModelMismatchWithoutSendingModelOverride(t *testing.T) {
	now := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)
	conn := newRecordedMeetingSpecialistRealtimeConn()
	config := specialistRealtimeConfigFixture(now, conn)
	conn.push(map[string]any{
		"type": "session.created", "event_id": "provider-wrong-model",
		"session": map[string]any{"id": "provider-session", "type": "realtime", "model": "gpt-realtime-2"},
	})
	launch := specialistRuntimeLaunchFixture(now)
	bindSpecialistRealtimePurpose(&launch)
	qualifySpecialistRealtimeDirectPCMLaunch(&launch)
	providerValue, err := NewMeetingSpecialistRealtimeProviderFactory(config)(context.Background(), launch)
	if err != nil {
		t.Fatal(err)
	}
	provider := providerValue.(*openAIMeetingSpecialistProvider)
	defer provider.Close(context.Background(), "test")
	if err := provider.BindMeetingSpecialistProviderHooks(MeetingSpecialistProviderHooks{
		PublishAudio: func(MeetingAgentFloorLease, []int16, int64, int64) error { return nil },
		CompleteTurn: func(MeetingAgentFloorLease) error { return nil },
		FailSession:  func(string) {},
	}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Brief(context.Background(), launch.Context); !errors.Is(err, ErrMeetingSpecialistProviderProtocol) {
		t.Fatalf("returned model mismatch err=%v", err)
	}
	if writes := conn.writeTypes(); len(writes) != 0 {
		t.Fatalf("model mismatch wrote session override: %v", writes)
	}
}

func TestMeetingSpecialistRealtimeRecordedEventsPublishMeterAndComplete(t *testing.T) {
	now := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)
	conn := newRecordedMeetingSpecialistRealtimeConn()
	var mu sync.Mutex
	var published []int16
	var wantScope MeetingAgentFloorScope
	runtime, session, launch := newRecordedSpecialistRuntime(t, now, conn, func(scope MeetingAgentFloorScope, _ uint64, pcm []int16) error {
		if scope != wantScope {
			return errors.New("published audio used the wrong scope")
		}
		mu.Lock()
		published = append(published, pcm...)
		mu.Unlock()
		return nil
	})
	wantScope = launch.Scope
	if err := runtime.SendHumanAudio(session, "human-aj-track", []int16{1, -2, 3}); err != nil {
		t.Fatal(err)
	}
	floor, err := runtime.RequestTurn(session, "approved_scout_handoff", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	audio := []byte{1, 0, 2, 0, 3, 0, 4, 0}
	conn.push(map[string]any{"type": "response.created", "event_id": "provider-response-created", "response": map[string]any{"id": "response-1", "status": "in_progress"}})
	conn.push(map[string]any{
		"type": "response.output_audio.delta", "event_id": "provider-audio-delta", "response_id": "response-1", "item_id": "assistant-item-1",
		"output_index": 0, "content_index": 0, "delta": base64.StdEncoding.EncodeToString(audio),
	})
	conn.push(map[string]any{
		"type": "response.output_audio.done", "event_id": "provider-audio-done", "response_id": "response-1", "item_id": "assistant-item-1",
		"output_index": 0, "content_index": 0,
	})
	conn.push(map[string]any{
		"type": "response.done", "event_id": "provider-response-done",
		"response": map[string]any{
			"id": "response-1", "status": "completed", "status_details": nil,
			"usage": map[string]any{
				"total_tokens": 16, "input_tokens": 10, "output_tokens": 6,
				"input_token_details":  map[string]any{"text_tokens": 4, "audio_tokens": 6, "image_tokens": 0, "cached_tokens": 2, "cached_tokens_details": map[string]any{"text_tokens": 2, "audio_tokens": 0, "image_tokens": 0}},
				"output_token_details": map[string]any{"text_tokens": 1, "audio_tokens": 5},
			},
		},
	})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && runtime.Snapshot().Floor != nil {
		time.Sleep(time.Millisecond)
	}
	if runtime.Snapshot().Floor != nil {
		t.Fatal("recorded response did not release floor")
	}
	mu.Lock()
	got := append([]int16(nil), published...)
	mu.Unlock()
	if !reflect.DeepEqual(got, []int16{1, 2, 3, 4}) {
		t.Fatalf("published PCM=%v", got)
	}
	receipt := runtime.ProviderReceipt()
	if receipt.TerminalStatus != "completed" || receipt.UsageDigest == "" || receipt.TerminalEventHash == "" || receipt.OutputAudioTokens != 5 || receipt.ReconciledCostCent != 1 {
		t.Fatalf("terminal receipt=%+v", receipt)
	}
	firstUsageDigest := receipt.UsageDigest
	secondFloor, err := runtime.RequestTurn(session, "human_followup", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.push(map[string]any{"type": "response.created", "event_id": "provider-response-created-2", "response": map[string]any{"id": "response-2", "status": "in_progress"}})
	provider := runtime.provider.(*openAIMeetingSpecialistProvider)
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		provider.mu.Lock()
		created := provider.activeResponse == "response-2"
		provider.mu.Unlock()
		if created {
			break
		}
		time.Sleep(time.Millisecond)
	}
	provider.mu.Lock()
	created := provider.activeResponse == "response-2"
	provider.mu.Unlock()
	if !created {
		t.Fatal("second provider response did not start")
	}
	if interruption, ok := runtime.HumanBargeIn(launch.Scope.RoomID, launch.Scope.SittingID, launch.Scope.MediaGeneration); !ok || interruption.FloorGeneration != secondFloor.Generation {
		t.Fatalf("second turn barge-in=%+v ok=%v", interruption, ok)
	}
	conn.push(map[string]any{
		"type": "response.done", "event_id": "provider-response-done-2",
		"response": map[string]any{
			"id": "response-2", "status": "cancelled", "status_details": map[string]any{"type": "cancelled", "reason": "client_cancelled"},
			"usage": map[string]any{
				"total_tokens": 4, "input_tokens": 2, "output_tokens": 2,
				"input_token_details":  map[string]any{"text_tokens": 1, "audio_tokens": 1, "cached_tokens": 0},
				"output_token_details": map[string]any{"text_tokens": 0, "audio_tokens": 2},
			},
		},
	})
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) && runtime.ProviderReceipt().TerminalStatus != "cancelled" {
		time.Sleep(time.Millisecond)
	}
	receipt = runtime.ProviderReceipt()
	if receipt.TerminalStatus != "cancelled" || receipt.InputTokens != 12 || receipt.OutputTokens != 8 || receipt.OutputAudioTokens != 7 || receipt.ReconciledCostCent != 2 || receipt.UsageDigest == firstUsageDigest {
		t.Fatalf("aggregate multi-turn receipt=%+v", receipt)
	}
	writes := conn.writeTypes()
	for _, want := range []string{"session.update", "input_audio_buffer.append", "input_audio_buffer.commit", "response.create"} {
		if !containsMeetingSpecialistTool(writes, want) {
			t.Fatalf("writes=%v missing %s", writes, want)
		}
	}
	conn.mu.Lock()
	for _, event := range conn.writes {
		if asString(event["type"]) != "response.create" {
			continue
		}
		response := event["response"].(map[string]any)
		if _, overridesSessionBrief := response["instructions"]; overridesSessionBrief {
			conn.mu.Unlock()
			t.Fatalf("response.create overrode the authority-bound session brief: %v", response)
		}
	}
	conn.mu.Unlock()
	if floor.Generation == 0 {
		t.Fatal("floor generation was not bound")
	}
}

func TestMeetingSpecialistRealtimeBargeInCancelsAndTruncatesPlayedAudio(t *testing.T) {
	now := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)
	conn := newRecordedMeetingSpecialistRealtimeConn()
	var published atomic.Int64
	runtime, session, launch := newRecordedSpecialistRuntime(t, now, conn, func(MeetingAgentFloorScope, uint64, []int16) error {
		published.Add(1)
		return nil
	})
	floor, err := runtime.RequestTurn(session, "approved_scout_handoff", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.push(map[string]any{"type": "response.created", "event_id": "barge-response-created", "response": map[string]any{"id": "barge-response", "status": "in_progress"}})
	conn.push(map[string]any{
		"type": "response.output_audio.delta", "event_id": "barge-audio-delta", "response_id": "barge-response", "item_id": "barge-item",
		"output_index": 0, "content_index": 0, "delta": base64.StdEncoding.EncodeToString(make([]byte, meetingSpecialistRealtimeSampleRate*4)),
	})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		provider := runtime.provider.(*openAIMeetingSpecialistProvider)
		provider.mu.Lock()
		ready := provider.activeItem == "barge-item" && provider.audioBytes > 0
		if ready {
			// Simulate a downstream playback acknowledgement. Generated audio is
			// staged by this adapter, but any future/alternate publisher that has
			// actually played audio must still emit the documented truncation.
			provider.publishedAudioMS = 1000
		}
		provider.mu.Unlock()
		if ready {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if interruption, ok := runtime.HumanBargeIn(launch.Scope.RoomID, launch.Scope.SittingID, launch.Scope.MediaGeneration); !ok || interruption.FloorGeneration != floor.Generation {
		t.Fatalf("barge-in interruption=%+v ok=%v", interruption, ok)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !containsMeetingSpecialistTool(conn.writeTypes(), "conversation.item.truncate") {
		time.Sleep(time.Millisecond)
	}
	conn.push(map[string]any{
		"type": "response.done", "event_id": "barge-response-done",
		"response": map[string]any{
			"id": "barge-response", "status": "cancelled", "status_details": nil,
			"usage": map[string]any{
				"total_tokens": 4, "input_tokens": 2, "output_tokens": 2,
				"input_token_details":  map[string]any{"text_tokens": 0, "audio_tokens": 2, "cached_tokens": 0},
				"output_token_details": map[string]any{"text_tokens": 0, "audio_tokens": 2},
			},
		},
	})
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) && runtime.ProviderReceipt().TerminalStatus != "cancelled" {
		time.Sleep(time.Millisecond)
	}
	if runtime.ProviderReceipt().TerminalStatus != "cancelled" {
		t.Fatal("cancelled response.done did not reconcile")
	}
	if published.Load() != 0 {
		t.Fatal("barge-in published staged partial audio before terminal authority")
	}
	if next, err := runtime.RequestTurn(session, "human_followup", time.Second); err != nil || next.Generation == floor.Generation {
		t.Fatalf("session did not remain available after reconciled barge-in: next=%+v err=%v", next, err)
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	var cancel, truncate map[string]any
	for _, event := range conn.writes {
		switch asString(event["type"]) {
		case "response.cancel":
			cancel = event
		case "conversation.item.truncate":
			truncate = event
		}
	}
	if cancel == nil || truncate == nil || asString(truncate["item_id"]) != "barge-item" || int64(truncate["audio_end_ms"].(float64)) != 1000 {
		t.Fatalf("barge-in provider hooks cancel=%v truncate=%v writes=%v", cancel, truncate, conn.writes)
	}
}

func TestMeetingSpecialistRealtimeCancelledTerminalAllowsOptionalUsage(t *testing.T) {
	now := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)
	for _, fixture := range []struct {
		name  string
		usage json.RawMessage
	}{
		{name: "omitted"},
		{name: "null", usage: json.RawMessage("null")},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			launch := specialistRuntimeLaunchFixture(now)
			config := defaultOffMeetingSpecialistRealtimeConfig()
			config.Now = func() time.Time { return now }
			provider := &openAIMeetingSpecialistProvider{
				config: config.normalized(), launch: launch,
				cancelledResponse: "cancelled-response", cancelledItem: "cancelled-item", cancelPending: true,
				activeInputLimit: meetingSpecialistRealtimeContextWindowTokens, activeOutputLimit: config.MaxOutputTokens, activeCostLimit: 1000,
			}
			raw := []byte(`{"type":"response.done","response":{"id":"cancelled-response","status":"cancelled"}}`)
			if err := provider.handleCancelledResponseDone(raw, "cancelled-response", nil, fixture.usage); err != nil {
				t.Fatalf("optional usage terminal: %v", err)
			}
			if provider.cancelPending || provider.cancelledResponse != "" || provider.cancelledItem != "" {
				t.Fatalf("cancel fence was not cleared: pending=%v response=%q item=%q", provider.cancelPending, provider.cancelledResponse, provider.cancelledItem)
			}
			if receipt := provider.MeetingSpecialistProviderReceipt(); receipt.TerminalStatus != "cancelled" || receipt.UsageStatus != "usage_unreconciled" || receipt.TerminalEventHash != sha256Hex(raw) || receipt.UsageDigest != "" || receipt.InputTokens != 0 || receipt.OutputTokens != 0 || receipt.ReconciledCostCent != 0 {
				t.Fatalf("optional usage receipt=%+v", receipt)
			}
			if err := provider.write(map[string]any{"type": "response.create"}); !errors.Is(err, ErrMeetingSpecialistClosed) {
				t.Fatalf("usage-unreconciled provider admitted another write: %v", err)
			}
		})
	}
}

func TestMeetingSpecialistRealtimeMissingUsageRevokesRuntimeWithoutProtocolFailure(t *testing.T) {
	now := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)
	usageFixtures := []struct {
		name    string
		include bool
		value   any
	}{
		{name: "omitted"},
		{name: "null", include: true, value: nil},
		{name: "empty", include: true, value: map[string]any{}},
		{name: "partial", include: true, value: map[string]any{"total_tokens": 4}},
	}
	for _, status := range []string{"completed", "incomplete", "cancelled"} {
		for _, usageFixture := range usageFixtures {
			t.Run(status+"_"+usageFixture.name, func(t *testing.T) {
				conn := newRecordedMeetingSpecialistRealtimeConn()
				var published atomic.Int64
				runtime, session, launch := newRecordedSpecialistRuntime(t, now, conn, func(MeetingAgentFloorScope, uint64, []int16) error {
					published.Add(1)
					return nil
				})
				floor, err := runtime.RequestTurn(session, "approved_scout_handoff", time.Second)
				if err != nil {
					t.Fatal(err)
				}
				responseID := "usage-missing-" + status + "-" + usageFixture.name
				conn.push(map[string]any{"type": "response.created", "event_id": "created-" + status + "-" + usageFixture.name, "response": map[string]any{"id": responseID, "status": "in_progress"}})
				conn.push(map[string]any{
					"type": "response.output_audio.delta", "event_id": "audio-" + status + "-" + usageFixture.name, "response_id": responseID, "item_id": "item-" + status + "-" + usageFixture.name,
					"output_index": 0, "content_index": 0, "delta": base64.StdEncoding.EncodeToString([]byte{1, 0, 2, 0}),
				})
				provider := runtime.provider.(*openAIMeetingSpecialistProvider)
				deadline := time.Now().Add(time.Second)
				for time.Now().Before(deadline) {
					provider.mu.Lock()
					created := provider.activeResponse == responseID && provider.audioBytes > 0
					provider.mu.Unlock()
					if created {
						break
					}
					time.Sleep(time.Millisecond)
				}
				if status == "cancelled" {
					if interruption, ok := runtime.HumanBargeIn(launch.Scope.RoomID, launch.Scope.SittingID, launch.Scope.MediaGeneration); !ok || interruption.FloorGeneration != floor.Generation {
						t.Fatalf("barge-in=%+v ok=%v", interruption, ok)
					}
				}
				details := map[string]any{"type": status}
				if status == "incomplete" {
					details["reason"] = "max_output_tokens"
				} else if status == "cancelled" {
					details["reason"] = "client_cancelled"
				}
				response := map[string]any{"id": responseID, "status": status, "status_details": details}
				if usageFixture.include {
					response["usage"] = usageFixture.value
				}
				conn.push(map[string]any{
					"type": "response.done", "event_id": "done-" + status + "-" + usageFixture.name,
					"response": response,
				})
				deadline = time.Now().Add(2 * time.Second)
				var receipt MeetingSpecialistProviderReceipt
				for time.Now().Before(deadline) {
					receipt = runtime.ProviderReceipt()
					if runtime.Snapshot().Session == nil && receipt.TerminalStatus == status && receipt.UsageStatus == "usage_unreconciled" {
						break
					}
					time.Sleep(time.Millisecond)
				}
				if runtime.Snapshot().Session != nil || receipt.TerminalStatus != status || receipt.UsageStatus != "usage_unreconciled" {
					t.Fatalf("missing usage did not revoke cleanly: snapshot=%+v receipt=%+v", runtime.Snapshot(), receipt)
				}
				if published.Load() != 0 {
					t.Fatalf("usage-unreconciled %s response published staged audio", status)
				}
				if _, err := runtime.RequestTurn(session, "must_not_restart", time.Second); !errors.Is(err, ErrMeetingSpecialistClosed) {
					t.Fatalf("missing usage admitted next turn: %v", err)
				}
			})
		}
	}
}

func TestMeetingSpecialistRealtimeCloseLinearizesAheadOfQueuedWrite(t *testing.T) {
	conn := newRecordedMeetingSpecialistRealtimeConn()
	ctx, cancel := context.WithCancel(context.Background())
	launch := specialistRuntimeLaunchFixture(time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC))
	provider := &openAIMeetingSpecialistProvider{conn: conn, ctx: ctx, cancel: cancel, launch: launch, done: make(chan struct{})}

	provider.writeMu.Lock()
	writeResult := make(chan error, 1)
	go func() {
		writeResult <- provider.write(map[string]any{"type": "input_audio_buffer.append"})
	}()
	if err := provider.Close(context.Background(), "authority_revoked"); err != nil {
		provider.writeMu.Unlock()
		t.Fatal(err)
	}
	provider.writeMu.Unlock()
	select {
	case err := <-writeResult:
		if !errors.Is(err, ErrMeetingSpecialistClosed) {
			t.Fatalf("queued writer after close err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued writer did not observe close fence")
	}
	if writes := conn.writeTypes(); len(writes) != 0 {
		t.Fatalf("queued writer emitted after close linearized: %v", writes)
	}
}

func TestMeetingSpecialistRealtimeBeginResponseWriteFailureClosesRuntime(t *testing.T) {
	now := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)
	for _, failureOffset := range []int{1, 2} {
		t.Run(fmt.Sprintf("response_write_%d", failureOffset), func(t *testing.T) {
			conn := newRecordedMeetingSpecialistRealtimeConn()
			runtime, session, _ := newRecordedSpecialistRuntime(t, now, conn, func(MeetingAgentFloorScope, uint64, []int16) error { return nil })
			conn.mu.Lock()
			conn.failWriteAt = conn.writeAttempts + failureOffset
			conn.mu.Unlock()
			if _, err := runtime.RequestTurn(session, "approved_scout_handoff", time.Second); !errors.Is(err, ErrMeetingSpecialistProviderProtocol) {
				t.Fatalf("forced write failure err=%v", err)
			}
			if snapshot := runtime.Snapshot(); snapshot.Session != nil || snapshot.TerminalReason != session.Scope.SessionID+"\x00failed" {
				t.Fatalf("provider write failure did not close runtime: %+v", snapshot)
			}
			if _, err := runtime.RequestTurn(session, "must_not_restart", time.Second); !errors.Is(err, ErrMeetingSpecialistClosed) {
				t.Fatalf("provider write failure admitted later turn: %v", err)
			}
		})
	}
}

func TestMeetingSpecialistRealtimeDirectPCMBudgetsFenceBeforeProviderCalls(t *testing.T) {
	now := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)

	t.Run("ordinary specialist profile cannot fit documented input worst case", func(t *testing.T) {
		conn := newRecordedMeetingSpecialistRealtimeConn()
		config := specialistRealtimeConfigFixture(now, conn)
		baseDial := config.dial
		var dialed atomic.Bool
		config.dial = func(ctx context.Context, endpoint string, headers http.Header) (meetingSpecialistRealtimeConn, error) {
			dialed.Store(true)
			return baseDial(ctx, endpoint, headers)
		}
		authority := newFakeMeetingSpecialistAuthority()
		launch := specialistRuntimeLaunchFixture(now)
		bindSpecialistRealtimePurpose(&launch)
		launch.CapabilityReceipt = authority.issue(launch)
		runtime := NewMeetingSpecialistRuntime(func() time.Time { return now }, enabledSpecialistHarnessGates(), authority, NewMeetingSpecialistRealtimeProviderFactory(config), func(MeetingAgentFloorScope, uint64, []int16) error { return nil })
		if _, err := runtime.Start(context.Background(), launch); !errors.Is(err, ErrMeetingSpecialistProviderConfig) {
			t.Fatalf("direct PCM factory preflight err=%v", err)
		}
		if dialed.Load() || len(conn.writeTypes()) != 0 {
			t.Fatalf("unfunded direct PCM profile dialed=%v writes=%v", dialed.Load(), conn.writeTypes())
		}
		if snapshot := runtime.Snapshot(); snapshot.Session != nil || snapshot.TeardownReceiptDigest == "" {
			t.Fatalf("unfunded direct PCM launch was not fenced: %+v", snapshot)
		}
	})

	t.Run("cumulative PCM stops before append", func(t *testing.T) {
		conn := newRecordedMeetingSpecialistRealtimeConn()
		config := specialistRealtimeConfigFixture(now, conn)
		pushSpecialistSessionHandshake(conn, config, "provider-pcm-session")
		launch := specialistRuntimeLaunchFixture(now)
		bindSpecialistRealtimePurpose(&launch)
		qualifySpecialistRealtimeDirectPCMLaunch(&launch)
		launch.Context.AudioBudgetSeconds = 1
		launch.ApprovalLimits.AudioBudgetSeconds = 1
		launch.Policy.AudioBudgetSecond = 1
		providerValue, err := NewMeetingSpecialistRealtimeProviderFactory(config)(context.Background(), launch)
		if err != nil {
			t.Fatal(err)
		}
		provider := providerValue.(*openAIMeetingSpecialistProvider)
		defer provider.Close(context.Background(), "test")
		if err := provider.BindMeetingSpecialistProviderHooks(MeetingSpecialistProviderHooks{
			PublishAudio: func(MeetingAgentFloorLease, []int16, int64, int64) error { return nil },
			CompleteTurn: func(MeetingAgentFloorLease) error { return nil },
			FailSession:  func(string) {},
		}); err != nil {
			t.Fatal(err)
		}
		if err := provider.Brief(context.Background(), launch.Context); err != nil {
			t.Fatal(err)
		}
		if err := provider.WriteHumanPCM(context.Background(), 1, make([]int16, meetingSpecialistRealtimeSampleRate)); err != nil {
			t.Fatal(err)
		}
		if err := provider.WriteHumanPCM(context.Background(), 2, []int16{1}); !errors.Is(err, ErrMeetingSpecialistProviderBudget) {
			t.Fatalf("cumulative PCM err=%v", err)
		}
		writes := conn.writeTypes()
		appends := 0
		for _, typ := range writes {
			if typ == "input_audio_buffer.append" {
				appends++
			}
		}
		if appends != 1 || containsMeetingSpecialistTool(writes, "input_audio_buffer.commit") || provider.MeetingSpecialistProviderReceipt().InputAudioSamples != meetingSpecialistRealtimeSampleRate {
			t.Fatalf("PCM ceiling writes=%v receipt=%+v", writes, provider.MeetingSpecialistProviderReceipt())
		}
	})
}

func TestMeetingSpecialistRealtimeAdmissionCapsCumulativeTokenAndCostEnvelopes(t *testing.T) {
	now := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)
	config := defaultOffMeetingSpecialistRealtimeConfig().normalized()
	config.Now = func() time.Time { return now }

	t.Run("token remainder", func(t *testing.T) {
		launch := specialistRuntimeLaunchFixture(now)
		qualifySpecialistRealtimeDirectPCMLaunch(&launch)
		launch.Context.TokenBudget = meetingSpecialistRealtimeContextWindowTokens + 7
		launch.ApprovalLimits.TokenBudget = launch.Context.TokenBudget
		output, cost, ok := meetingSpecialistRealtimeResponseAdmission(config, launch, 0, 0)
		if !ok || output != 7 || cost <= 0 {
			t.Fatalf("token-capped admission output=%d cost=%d ok=%v", output, cost, ok)
		}
		if _, _, ok := meetingSpecialistRealtimeResponseAdmission(config, launch, 7, 0); ok {
			t.Fatal("cumulative token exhaustion admitted another response")
		}
	})

	t.Run("cost remainder", func(t *testing.T) {
		launch := specialistRuntimeLaunchFixture(now)
		qualifySpecialistRealtimeDirectPCMLaunch(&launch)
		output, cost, ok := meetingSpecialistRealtimeResponseAdmission(config, launch, 0, 1090)
		if !ok || output != 62 || cost != 410 {
			t.Fatalf("cost-capped admission output=%d cost=%d ok=%v", output, cost, ok)
		}
		if _, _, ok := meetingSpecialistRealtimeResponseAdmission(config, launch, 0, 1091); ok {
			t.Fatal("cumulative cost exhaustion admitted another response")
		}
	})
}

func TestMeetingSpecialistRealtimeFailedResponseFencesSynchronously(t *testing.T) {
	now := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)
	dir := ledgerTestDir(t)
	_ = dir
	swapUsageLedgerNow(t, now)
	conn := newRecordedMeetingSpecialistRealtimeConn()
	config := defaultOffMeetingSpecialistRealtimeConfig().normalized()
	config.Now = func() time.Time { return now }
	launch := specialistRuntimeLaunchFixture(now)
	qualifySpecialistRealtimeDirectPCMLaunch(&launch)
	session := MeetingAgentSessionLease{Scope: launch.Scope, Generation: 1, GrantedAt: now, ExpiresAt: now.Add(time.Minute)}
	floor := MeetingAgentFloorLease{Session: session, Generation: 1, GrantedAt: now, ExpiresAt: now.Add(time.Second), Trigger: "approved_scout_handoff"}
	entered, release := make(chan struct{}), make(chan struct{})
	provider := &openAIMeetingSpecialistProvider{
		config: config, launch: launch, conn: conn, briefed: true, activeFloor: &floor, activeResponse: "failed-response",
		activeInputLimit: meetingSpecialistRealtimeContextWindowTokens, activeOutputLimit: config.MaxOutputTokens, activeCostLimit: 1000,
		hooks: MeetingSpecialistProviderHooks{
			CompleteTurn: func(MeetingAgentFloorLease) error { close(entered); <-release; return nil },
			FailSession:  func(string) {},
		},
	}
	usage := json.RawMessage(`{"total_tokens":4,"input_tokens":2,"output_tokens":2,"input_token_details":{"text_tokens":1,"audio_tokens":1,"cached_tokens":0},"output_token_details":{"text_tokens":0,"audio_tokens":2}}`)
	result := make(chan error, 1)
	go func() {
		result <- provider.handleUnacceptedResponseDone([]byte(`{"type":"response.done"}`), "failed-response", "failed", nil, usage)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("failed response did not reach completion hook")
	}
	nextFloor := floor
	nextFloor.Generation++
	if err := provider.BeginResponse(context.Background(), nextFloor); !errors.Is(err, ErrMeetingSpecialistProviderProtocol) {
		close(release)
		t.Fatalf("failed response admitted a concurrent successor: %v", err)
	}
	if err := provider.write(map[string]any{"type": "response.create"}); !errors.Is(err, ErrMeetingSpecialistClosed) {
		close(release)
		t.Fatalf("failed response write fence err=%v", err)
	}
	if writes := conn.writeTypes(); len(writes) != 0 {
		close(release)
		t.Fatalf("failed response emitted a successor event: %v", writes)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestMeetingSpecialistRealtimeCompletedUsageIsLedgeredWhenPublicationFails(t *testing.T) {
	now := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)
	dir := ledgerTestDir(t)
	swapUsageLedgerNow(t, now)
	conn := newRecordedMeetingSpecialistRealtimeConn()
	runtime, session, _ := newRecordedSpecialistRuntime(t, now, conn, func(MeetingAgentFloorScope, uint64, []int16) error {
		return errors.New("forced publication failure")
	})
	if _, err := runtime.RequestTurn(session, "approved_scout_handoff", time.Second); err != nil {
		t.Fatal(err)
	}
	conn.push(map[string]any{"type": "response.created", "event_id": "publish-fail-created", "response": map[string]any{"id": "publish-fail-response", "status": "in_progress"}})
	conn.push(map[string]any{
		"type": "response.output_audio.delta", "event_id": "publish-fail-audio", "response_id": "publish-fail-response", "item_id": "publish-fail-item",
		"output_index": 0, "content_index": 0, "delta": base64.StdEncoding.EncodeToString([]byte{1, 0, 2, 0}),
	})
	conn.push(map[string]any{
		"type": "response.done", "event_id": "publish-fail-done",
		"response": map[string]any{
			"id": "publish-fail-response", "status": "completed", "status_details": nil,
			"usage": map[string]any{
				"total_tokens": 4, "input_tokens": 2, "output_tokens": 2,
				"input_token_details":  map[string]any{"text_tokens": 1, "audio_tokens": 1, "cached_tokens": 0},
				"output_token_details": map[string]any{"text_tokens": 0, "audio_tokens": 2},
			},
		},
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && runtime.Snapshot().Session != nil {
		time.Sleep(time.Millisecond)
	}
	entries := readUsageLedgerEntries(t, dir, now)
	if len(entries) != 1 {
		t.Fatalf("publication failure usage entries=%d: %+v", len(entries), entries)
	}
	entry := entries[0]
	if !entry.WireSuccess || entry.AcceptedOutput || entry.OutputFailureReason != "publication_failed" || entry.InputTokens+entry.CachedInputTokens+entry.AudioInputTokens+entry.CachedAudioInputTokens != 2 || entry.OutputTokens+entry.AudioOutputTokens != 2 || entry.EstCostUSD <= 0 {
		t.Fatalf("publication failure usage=%+v", entry)
	}
	receipt := runtime.ProviderReceipt()
	if receipt.TerminalStatus != "completed" || receipt.UsageStatus != "reconciled" || receipt.SessionFailureHash != sha256Hex([]byte("provider_event_invalid")) {
		t.Fatalf("publication failure rewrote provider terminal provenance: %+v", receipt)
	}
}

func TestMeetingSpecialistRealtimeUnknownAndUnreconciledEventsFailClosed(t *testing.T) {
	now := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)
	for _, fixture := range []struct {
		name  string
		event map[string]any
	}{
		{name: "unknown", event: map[string]any{"type": "future.unreviewed.event", "event_id": "unknown-event-1"}},
		{name: "tool widening", event: map[string]any{"type": "response.function_call_arguments.done", "event_id": "tool-event-1", "call_id": "call-1", "name": "search_everything", "arguments": `{}`}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			conn := newRecordedMeetingSpecialistRealtimeConn()
			runtime, session, _ := newRecordedSpecialistRuntime(t, now, conn, func(MeetingAgentFloorScope, uint64, []int16) error {
				t.Fatal("invalid event published audio")
				return nil
			})
			if _, err := runtime.RequestTurn(session, "approved_scout_handoff", time.Second); err != nil {
				t.Fatal(err)
			}
			conn.push(fixture.event)
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) && runtime.Snapshot().Session != nil {
				time.Sleep(time.Millisecond)
			}
			if snapshot := runtime.Snapshot(); snapshot.Session != nil || snapshot.TerminalReason != session.Scope.SessionID+"\x00failed" {
				runtime.mu.Lock()
				state := struct {
					Closed, Stopping bool
					Inflight         int
				}{runtime.closed, runtime.stopping, runtime.inflight}
				runtime.mu.Unlock()
				t.Fatalf("invalid event did not tear down runtime: snapshot=%+v state=%+v writes=%v", snapshot, state, conn.writeTypes())
			}
		})
	}
}

func TestMeetingSpecialistRealtimeDocumentedIncompleteAndFailedTerminalsReconcileWithoutPublishing(t *testing.T) {
	now := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)
	for _, fixture := range []struct {
		name          string
		status        string
		statusDetails map[string]any
		wantSession   bool
	}{
		// Official response.done schema: incomplete carries one of the
		// documented max_output_tokens/content_filter reasons.
		{name: "incomplete", status: "incomplete", statusDetails: map[string]any{"type": "incomplete", "reason": "max_output_tokens"}, wantSession: true},
		{name: "incomplete_optional_details_omitted", status: "incomplete", statusDetails: nil, wantSession: true},
		{name: "incomplete_optional_type_and_reason_omitted", status: "incomplete", statusDetails: map[string]any{}, wantSession: true},
		// Official response.done schema permits the nested error and its fields
		// to be omitted on a failed terminal.
		{name: "failed_with_error", status: "failed", statusDetails: map[string]any{"type": "failed", "error": map[string]any{"type": "server_error", "code": "realtime_internal"}}},
		{name: "failed_without_error", status: "failed", statusDetails: map[string]any{"type": "failed"}},
		{name: "failed_without_error_fields", status: "failed", statusDetails: map[string]any{"type": "failed", "error": map[string]any{}}},
		{name: "failed_optional_type_omitted", status: "failed", statusDetails: map[string]any{"error": map[string]any{"code": "realtime_internal"}}},
		{name: "failed_optional_details_omitted", status: "failed", statusDetails: nil},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			conn := newRecordedMeetingSpecialistRealtimeConn()
			var published atomic.Int64
			runtime, session, _ := newRecordedSpecialistRuntime(t, now, conn, func(MeetingAgentFloorScope, uint64, []int16) error {
				published.Add(1)
				return nil
			})
			if _, err := runtime.RequestTurn(session, "approved_scout_handoff", time.Second); err != nil {
				t.Fatal(err)
			}
			responseID := "response-" + fixture.status
			itemID := "item-" + fixture.status
			conn.push(map[string]any{"type": "response.created", "event_id": "created-" + fixture.status, "response": map[string]any{"id": responseID, "status": "in_progress"}})
			conn.push(map[string]any{
				"type": "response.output_audio.delta", "event_id": "audio-" + fixture.status, "response_id": responseID, "item_id": itemID,
				"output_index": 0, "content_index": 0, "delta": base64.StdEncoding.EncodeToString([]byte{1, 0, 2, 0}),
			})
			conn.push(map[string]any{
				"type": "response.output_item.done", "event_id": "item-done-" + fixture.status, "response_id": responseID, "output_index": 0,
				"item": map[string]any{"id": itemID, "type": "message", "role": "assistant", "status": "incomplete"},
			})
			conn.push(map[string]any{
				"type": "response.done", "event_id": "done-" + fixture.status,
				"response": map[string]any{
					"id": responseID, "status": fixture.status, "status_details": fixture.statusDetails,
					"usage": map[string]any{
						"total_tokens": 4, "input_tokens": 2, "output_tokens": 2,
						"input_token_details":  map[string]any{"text_tokens": 1, "audio_tokens": 1, "cached_tokens": 0},
						"output_token_details": map[string]any{"text_tokens": 0, "audio_tokens": 2},
					},
				},
			})
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) && runtime.ProviderReceipt().TerminalStatus != fixture.status {
				time.Sleep(time.Millisecond)
			}
			if receipt := runtime.ProviderReceipt(); receipt.TerminalStatus != fixture.status || receipt.InputTokens != 2 || receipt.OutputTokens != 2 || receipt.ReconciledCostCent != 1 {
				t.Fatalf("terminal receipt=%+v", receipt)
			}
			if published.Load() != 0 {
				t.Fatalf("%s terminal published staged partial audio", fixture.status)
			}
			deadline = time.Now().Add(time.Second)
			for time.Now().Before(deadline) && (runtime.Snapshot().Session != nil) != fixture.wantSession {
				time.Sleep(time.Millisecond)
			}
			if got := runtime.Snapshot().Session != nil; got != fixture.wantSession {
				t.Fatalf("session present=%v want=%v snapshot=%+v", got, fixture.wantSession, runtime.Snapshot())
			}
		})
	}
}

func TestParseMeetingSpecialistRealtimeUsageClassifiesOptionalPartialFields(t *testing.T) {
	valid := []byte(`{"total_tokens":4,"input_tokens":2,"output_tokens":2,"input_token_details":{"text_tokens":1,"audio_tokens":1,"cached_tokens":0},"output_token_details":{"text_tokens":0,"audio_tokens":2}}`)
	if _, err := parseMeetingSpecialistRealtimeUsage(valid); err != nil {
		t.Fatalf("valid usage: %v", err)
	}
	for _, raw := range [][]byte{nil, []byte("null"), []byte(`{}`), []byte(`{"total_tokens":4,"input_tokens":2,"output_tokens":2}`)} {
		if usage, reconciled, err := classifyMeetingSpecialistRealtimeUsage(raw); err != nil || reconciled || usage != (meetingSpecialistRealtimeUsage{}) {
			t.Fatalf("optional partial usage %s classified as usage=%+v reconciled=%v err=%v", raw, usage, reconciled, err)
		}
	}
	for _, raw := range [][]byte{
		[]byte(`{"total_tokens":4,"input_tokens":2,"output_tokens":2,"input_token_details":{"text_tokens":1,"audio_tokens":1,"cached_tokens":0,"cached_tokens_details":{"text_tokens":0}},"output_token_details":{"text_tokens":0,"audio_tokens":2}}`),
		[]byte(`{"total_tokens":6,"input_tokens":4,"output_tokens":2,"input_token_details":{"text_tokens":3,"audio_tokens":1,"cached_tokens":2,"cached_tokens_details":{"text_tokens":2}},"output_token_details":{"text_tokens":0,"audio_tokens":2}}`),
	} {
		if usage, reconciled, err := classifyMeetingSpecialistRealtimeUsage(raw); err != nil || reconciled || usage != (meetingSpecialistRealtimeUsage{}) {
			t.Fatalf("partial cached breakdown %s classified as usage=%+v reconciled=%v err=%v", raw, usage, reconciled, err)
		}
	}
	for _, raw := range [][]byte{
		[]byte(`{"total_tokens":4,"input_tokens":2,"output_tokens":2,"mystery_tokens":1,"input_token_details":{"text_tokens":1,"audio_tokens":1,"cached_tokens":0},"output_token_details":{"text_tokens":0,"audio_tokens":2}}`),
		[]byte(`{"total_tokens":5,"input_tokens":2,"output_tokens":2,"input_token_details":{"text_tokens":1,"audio_tokens":1,"cached_tokens":0},"output_token_details":{"text_tokens":0,"audio_tokens":2}}`),
		[]byte(`{"total_tokens":4,"input_tokens":2,"output_tokens":2,"input_token_details":{"text_tokens":1,"audio_tokens":1,"cached_tokens":0,"cached_tokens_details":{"text_tokens":1}},"output_token_details":{"text_tokens":0,"audio_tokens":2}}`),
	} {
		if _, err := parseMeetingSpecialistRealtimeUsage(raw); !errors.Is(err, ErrMeetingSpecialistProviderProtocol) {
			t.Fatalf("usage %s err=%v", raw, err)
		}
	}
}

func TestMeetingSpecialistProductDefaultFactoryCannotActivateOnApproval(t *testing.T) {
	product, _, user := specialistProductFixture(t)
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review the launch", "default-factory-request", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved")
	if err != nil {
		t.Fatal(err)
	}
	product.mu.Lock()
	runtime := product.invitations[requested.ID].Runtime
	product.mu.Unlock()
	if approved.ProviderSessionStarted || runtime == nil || runtime.factory == nil || runtime.Snapshot().Session != nil {
		t.Fatalf("default product factory activated: approved=%+v runtime=%+v", approved, runtime)
	}
	launch := specialistRuntimeLaunchFixture(product.now().UTC())
	if _, err := runtime.factory(context.Background(), launch); !errors.Is(err, ErrMeetingSpecialistProviderDisabled) {
		t.Fatalf("default product factory err=%v", err)
	}
}
