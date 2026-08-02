package e10probe

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRunRealtimeScoutBoundedTurn(t *testing.T) {
	fixture := rtNewFixture(t, nil)
	base := fixture.config.Config
	base.Model = ScoutRealtimeModel
	base.MaxUSD = 0.025
	config := RealtimeScoutConfig{Config: base, InteractionID: rtSegment}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/realtime" || r.URL.RawQuery != "model="+ScoutRealtimeModel {
			t.Errorf("target = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer "+rtAPIKey || r.Header.Get("OpenAI-Project") != rtProject {
			t.Error("unexpected provider headers")
		}
		conn, err := upgrader.Upgrade(w, r, http.Header{"OpenAI-Project": []string{rtProject}, "X-Request-ID": []string{"request-secret"}})
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		ssWrite(t, conn, map[string]any{"type": "session.created", "event_id": "created-secret", "session": map[string]any{"id": "session-secret", "type": "realtime", "model": ScoutRealtimeModel}})
		ssWrite(t, conn, map[string]any{"type": "conversation.created", "event_id": "conversation-secret", "conversation": map[string]any{"object": "realtime.conversation"}})
		ssReadExact(t, conn, scoutSessionUpdate(rtSegment))
		update := scoutSessionUpdate(rtSegment)
		update["session"].(map[string]any)["id"] = "session-secret"
		ssWrite(t, conn, map[string]any{"type": "session.updated", "event_id": "updated-secret", "session": update["session"]})
		ssReadExact(t, conn, scoutUserItem(rtSegment))
		ssReadExact(t, conn, scoutResponseCreate(rtSegment))
		ssWrite(t, conn, map[string]any{"type": "conversation.item.created", "event_id": "user-secret", "item": map[string]any{"id": "user-secret", "object": "realtime.item", "type": "message", "status": "completed", "role": "user"}})
		ssWrite(t, conn, map[string]any{"type": "response.created", "event_id": "response-created-secret", "response": map[string]any{"id": "response-secret", "status": "in_progress"}})
		ssWrite(t, conn, map[string]any{"type": "rate_limits.updated", "event_id": "rate-secret", "rate_limits": []any{}})
		ssWrite(t, conn, map[string]any{"type": "response.output_item.added", "event_id": "item-added-secret", "response_id": "response-secret", "output_index": 0, "item": map[string]any{"id": "assistant-secret", "type": "message", "role": "assistant"}})
		ssWrite(t, conn, map[string]any{"type": "conversation.item.created", "event_id": "assistant-created-secret", "item": map[string]any{"id": "assistant-secret", "type": "message", "role": "assistant"}})
		ssWrite(t, conn, map[string]any{"type": "response.content_part.added", "event_id": "content-added-secret", "response_id": "response-secret", "item_id": "assistant-secret", "output_index": 0, "content_index": 0, "part": map[string]any{"type": "audio"}})
		ssWrite(t, conn, map[string]any{"type": "response.output_audio.delta", "event_id": "audio-secret", "response_id": "response-secret", "item_id": "assistant-secret", "output_index": 0, "content_index": 0, "delta": base64.StdEncoding.EncodeToString([]byte{1, 2, 3})})
		ssWrite(t, conn, map[string]any{"type": "response.output_audio_transcript.delta", "event_id": "transcript-secret", "response_id": "response-secret", "item_id": "assistant-secret", "output_index": 0, "content_index": 0, "delta": "hello"})
		ssWrite(t, conn, map[string]any{"type": "response.output_audio.done", "event_id": "audio-done-secret", "response_id": "response-secret", "item_id": "assistant-secret", "output_index": 0, "content_index": 0})
		ssWrite(t, conn, map[string]any{"type": "response.output_audio_transcript.done", "event_id": "transcript-done-secret", "response_id": "response-secret", "item_id": "assistant-secret", "output_index": 0, "content_index": 0, "transcript": "hello"})
		ssWrite(t, conn, map[string]any{"type": "response.content_part.done", "event_id": "content-done-secret", "response_id": "response-secret", "item_id": "assistant-secret", "output_index": 0, "content_index": 0, "part": map[string]any{"type": "audio"}})
		ssWrite(t, conn, map[string]any{"type": "response.output_item.done", "event_id": "item-done-secret", "response_id": "response-secret", "output_index": 0, "item": map[string]any{"id": "assistant-secret", "object": "realtime.item", "type": "message", "status": "completed", "role": "assistant"}})
		ssWrite(t, conn, map[string]any{"type": "response.done", "event_id": "response-done-secret", "response": map[string]any{"id": "response-secret", "status": "completed", "usage": ssUsage()}})
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		)
	}))
	defer server.Close()
	config.WebSocketURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime?model=" + ScoutRealtimeModel
	receipt, err := RunRealtimeScout(context.Background(), config)
	if err != nil {
		t.Fatalf("RunRealtimeScout: %v", err)
	}
	if !receipt.Success || !receipt.CorrelationVerified || receipt.AssistantAudioBytes != 3 || receipt.OutputAudioTokens != 3 || receipt.ComputedCostUSD <= 0 ||
		!receipt.UsageObserved || !receipt.CostReconciled || receipt.CostState != "provider_response_done_usage_reconciled" || receipt.ResponseStatus != "completed" {
		t.Fatalf("unexpected safe receipt: %#v", receipt)
	}
	if receipt.AssistantTranscriptSHA256 != digest("hello") || receipt.ResponseIDSHA256 != digest("response-secret") {
		t.Fatal("receipt did not retain only the expected hashes")
	}
}

func TestRunRealtimeScoutAcceptsModernAddedDoneItems(t *testing.T) {
	config := ssConfig(t)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		ssBeginWithoutConversation(t, conn)
		ssCompleteModernWithUsage(t, conn, ssUsage())
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	}))
	defer server.Close()
	config.WebSocketURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime?model=" + ScoutRealtimeModel
	receipt, err := RunRealtimeScout(context.Background(), config)
	if err != nil || !receipt.Success || !receipt.CorrelationVerified || !receipt.PartialOutputObserved || receipt.MaxOutputTokens != 256 {
		t.Fatalf("modern Scout item lifecycle failed: receipt=%+v err=%v", receipt, err)
	}
	if receipt.ConversationCreatedObserved {
		t.Fatalf("receipt claimed an unobserved conversation.created event: %+v", receipt)
	}
	if receipt.UserItemLifecycle != "modern_added_done" || receipt.AssistantItemLifecycle != "modern_added_done" ||
		receipt.UserItemIDSHA256 != receipt.UserFinalizedIDSHA256 || receipt.AssistantItemIDSHA256 != receipt.AssistantFinalizedIDSHA256 {
		t.Fatalf("modern Scout receipt was not self-describing: %+v", receipt)
	}
}

func TestRunRealtimeScoutAcceptsDelayedOptionalConversationCreated(t *testing.T) {
	config := ssConfig(t)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		ssBeginWithoutConversation(t, conn)
		ssWrite(t, conn, map[string]any{"type": "conversation.created", "event_id": "conversation-secret", "conversation": map[string]any{"object": "realtime.conversation"}})
		ssCompleteModernWithUsage(t, conn, ssUsage())
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	}))
	defer server.Close()
	config.WebSocketURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime?model=" + ScoutRealtimeModel
	receipt, err := RunRealtimeScout(context.Background(), config)
	if err != nil || !receipt.Success || !receipt.CorrelationVerified || !receipt.ConversationCreatedObserved {
		t.Fatalf("delayed optional conversation.created failed: receipt=%+v err=%v", receipt, err)
	}
}

func TestRealtimeScoutClassifiesIncompleteResponseAndReconcilesUsage(t *testing.T) {
	config := ssConfig(t)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		ssBeginWithoutConversation(t, conn)
		ssWrite(t, conn, ssModernItem("conversation.item.added", "user-added-secret", "user-secret", "user", nil, []any{map[string]any{"type": "input_text", "text": "hello"}}))
		ssWrite(t, conn, ssModernItem("conversation.item.done", "user-done-secret", "user-secret", "user", nil, []any{map[string]any{"type": "input_text", "text": "hello"}}))
		ssWrite(t, conn, map[string]any{"type": "response.created", "event_id": "response-created-secret", "response": map[string]any{"id": "response-secret", "status": "in_progress"}})
		ssWrite(t, conn, map[string]any{"type": "response.output_item.added", "event_id": "item-added-secret", "response_id": "response-secret", "output_index": 0, "item": map[string]any{"id": "assistant-secret", "type": "message", "status": "in_progress", "role": "assistant"}})
		previous := "user-secret"
		assistantAdded := ssModernItem("conversation.item.added", "assistant-added-secret", "assistant-secret", "assistant", &previous, []any{})
		assistantAdded["item"].(map[string]any)["status"] = "in_progress"
		ssWrite(t, conn, assistantAdded)
		ssWrite(t, conn, map[string]any{"type": "response.content_part.added", "event_id": "content-added-secret", "response_id": "response-secret", "item_id": "assistant-secret", "output_index": 0, "content_index": 0, "part": map[string]any{"type": "audio"}})
		ssWrite(t, conn, map[string]any{"type": "response.output_audio_transcript.delta", "event_id": "transcript-secret", "response_id": "response-secret", "item_id": "assistant-secret", "output_index": 0, "content_index": 0, "delta": "hello"})
		ssWrite(t, conn, map[string]any{"type": "response.output_audio.delta", "event_id": "audio-secret", "response_id": "response-secret", "item_id": "assistant-secret", "output_index": 0, "content_index": 0, "delta": base64.StdEncoding.EncodeToString([]byte{1, 2, 3})})
		ssWrite(t, conn, map[string]any{"type": "response.output_audio.done", "event_id": "audio-done-secret", "response_id": "response-secret", "item_id": "assistant-secret", "output_index": 0, "content_index": 0})
		ssWrite(t, conn, map[string]any{"type": "response.output_audio_transcript.done", "event_id": "transcript-done-secret", "response_id": "response-secret", "item_id": "assistant-secret", "output_index": 0, "content_index": 0, "transcript": "hello"})
		ssWrite(t, conn, map[string]any{"type": "response.content_part.done", "event_id": "content-done-secret", "response_id": "response-secret", "item_id": "assistant-secret", "output_index": 0, "content_index": 0, "part": map[string]any{"type": "audio"}})
		assistantDone := ssModernItem("conversation.item.done", "assistant-done-secret", "assistant-secret", "assistant", &previous, []any{map[string]any{"type": "output_audio", "transcript": "hello"}})
		assistantDone["item"].(map[string]any)["status"] = "incomplete"
		ssWrite(t, conn, assistantDone)
		ssWrite(t, conn, map[string]any{"type": "response.output_item.done", "event_id": "item-done-secret", "response_id": "response-secret", "output_index": 0, "item": map[string]any{"id": "assistant-secret", "object": "realtime.item", "type": "message", "status": "incomplete", "role": "assistant"}})
		ssWrite(t, conn, map[string]any{"type": "response.done", "event_id": "response-done-secret", "response": map[string]any{"id": "response-secret", "status": "incomplete", "status_details": map[string]any{"type": "incomplete", "reason": "max_output_tokens"}, "usage": ssUsage()}})
	}))
	defer server.Close()
	config.WebSocketURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime?model=" + ScoutRealtimeModel
	receipt, err := RunRealtimeScout(context.Background(), config)
	if err == nil || receipt.Success || receipt.Outcome != "response_incomplete" || receipt.FailureClass != "provider_completion" {
		t.Fatalf("incomplete response was not classified safely: receipt=%+v err=%v", receipt, err)
	}
	if receipt.ResponseStatus != "incomplete" || receipt.ResponseStatusDetailType != "incomplete" || receipt.ResponseStatusReason != "max_output_tokens" || !receipt.PartialOutputObserved ||
		!receipt.UsageObserved || !receipt.CostReconciled || receipt.ComputedCostUSD <= 0 || receipt.AssistantAudioBytes != 3 ||
		receipt.AssistantFinalizedIDSHA256 != receipt.AssistantItemIDSHA256 {
		t.Fatalf("incomplete response receipt was not evidence-complete: %+v", receipt)
	}
}

func TestRealtimeScoutRejectsDuplicateOrLateConversationCreated(t *testing.T) {
	tests := []struct {
		name  string
		serve func(*testing.T, *websocket.Conn)
	}{
		{
			name: "duplicate during initialization",
			serve: func(t *testing.T, conn *websocket.Conn) {
				ssBegin(t, conn)
				ssWrite(t, conn, map[string]any{"type": "conversation.created", "event_id": "conversation-second-secret", "conversation": map[string]any{"object": "realtime.conversation"}})
			},
		},
		{
			name: "late after user admission",
			serve: func(t *testing.T, conn *websocket.Conn) {
				ssBeginWithoutConversation(t, conn)
				ssWrite(t, conn, map[string]any{"type": "conversation.item.created", "event_id": "user-secret", "item": map[string]any{"id": "user-secret", "type": "message", "role": "user"}})
				ssWrite(t, conn, map[string]any{"type": "conversation.created", "event_id": "conversation-late-secret", "conversation": map[string]any{"object": "realtime.conversation"}})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := ssConfig(t)
			upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				test.serve(t, conn)
			}))
			defer server.Close()
			config.WebSocketURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime?model=" + ScoutRealtimeModel
			receipt, err := RunRealtimeScout(context.Background(), config)
			if err == nil || receipt.Success || receipt.Outcome != "extra_conversation" || receipt.FailureClass != "event_correlation" {
				t.Fatalf("duplicate/late conversation.created was not rejected: receipt=%+v err=%v", receipt, err)
			}
		})
	}
}

func TestRealtimeScoutRejectsInvalidModernItemLifecycles(t *testing.T) {
	tests := []struct {
		name    string
		serve   func(*testing.T, *websocket.Conn)
		outcome string
	}{
		{
			name: "user added missing required content",
			serve: func(t *testing.T, conn *websocket.Conn) {
				event := ssModernItem("conversation.item.added", "user-added-secret", "user-secret", "user", nil, []any{})
				delete(event["item"].(map[string]any), "content")
				ssWrite(t, conn, event)
			},
			outcome: "schema_mismatch",
		},
		{
			name: "response before user done",
			serve: func(t *testing.T, conn *websocket.Conn) {
				ssWrite(t, conn, ssModernItem("conversation.item.added", "user-added-secret", "user-secret", "user", nil, []any{map[string]any{"type": "input_text", "text": "hello"}}))
				ssWrite(t, conn, map[string]any{"type": "response.created", "event_id": "response-created-secret", "response": map[string]any{"id": "response-secret", "status": "in_progress"}})
			},
			outcome: "out_of_order",
		},
		{
			name: "duplicate user added",
			serve: func(t *testing.T, conn *websocket.Conn) {
				ssWrite(t, conn, ssModernItem("conversation.item.added", "user-added-secret", "user-secret", "user", nil, []any{}))
				ssWrite(t, conn, ssModernItem("conversation.item.added", "user-added-second-secret", "user-secret", "user", nil, []any{}))
			},
			outcome: "extra_item",
		},
		{
			name: "duplicate user done",
			serve: func(t *testing.T, conn *websocket.Conn) {
				ssWrite(t, conn, ssModernItem("conversation.item.added", "user-added-secret", "user-secret", "user", nil, []any{}))
				ssWrite(t, conn, ssModernItem("conversation.item.done", "user-done-secret", "user-secret", "user", nil, []any{}))
				ssWrite(t, conn, ssModernItem("conversation.item.done", "user-done-second-secret", "user-secret", "user", nil, []any{}))
			},
			outcome: "extra_item",
		},
		{
			name: "user done identifier mismatch",
			serve: func(t *testing.T, conn *websocket.Conn) {
				ssWrite(t, conn, ssModernItem("conversation.item.added", "user-added-secret", "user-secret", "user", nil, []any{}))
				ssWrite(t, conn, ssModernItem("conversation.item.done", "user-done-secret", "different-user-secret", "user", nil, []any{}))
			},
			outcome: "correlation_mismatch",
		},
		{
			name: "modern user mixed with legacy created",
			serve: func(t *testing.T, conn *websocket.Conn) {
				ssWrite(t, conn, ssModernItem("conversation.item.added", "user-added-secret", "user-secret", "user", nil, []any{}))
				ssWrite(t, conn, map[string]any{"type": "conversation.item.created", "event_id": "user-created-secret", "item": map[string]any{"id": "user-secret", "type": "message", "status": "completed", "role": "user"}})
			},
			outcome: "extra_item",
		},
		{
			name: "legacy user mixed with modern done",
			serve: func(t *testing.T, conn *websocket.Conn) {
				ssWrite(t, conn, map[string]any{"type": "conversation.item.created", "event_id": "user-created-secret", "item": map[string]any{"id": "user-secret", "type": "message", "status": "completed", "role": "user"}})
				ssWrite(t, conn, ssModernItem("conversation.item.done", "user-done-secret", "user-secret", "user", nil, []any{}))
			},
			outcome: "out_of_order",
		},
		{
			name: "assistant done identifier mismatch",
			serve: func(t *testing.T, conn *websocket.Conn) {
				ssWrite(t, conn, ssModernItem("conversation.item.added", "user-added-secret", "user-secret", "user", nil, []any{}))
				ssWrite(t, conn, ssModernItem("conversation.item.done", "user-done-secret", "user-secret", "user", nil, []any{}))
				ssWrite(t, conn, map[string]any{"type": "response.created", "event_id": "response-created-secret", "response": map[string]any{"id": "response-secret", "status": "in_progress"}})
				previous := "user-secret"
				ssWrite(t, conn, ssModernItem("conversation.item.added", "assistant-added-secret", "assistant-secret", "assistant", &previous, []any{}))
				ssWrite(t, conn, ssModernItem("conversation.item.done", "assistant-done-secret", "different-assistant-secret", "assistant", &previous, []any{}))
			},
			outcome: "correlation_mismatch",
		},
		{
			name: "assistant previous item mismatch",
			serve: func(t *testing.T, conn *websocket.Conn) {
				ssWrite(t, conn, ssModernItem("conversation.item.added", "user-added-secret", "user-secret", "user", nil, []any{}))
				ssWrite(t, conn, ssModernItem("conversation.item.done", "user-done-secret", "user-secret", "user", nil, []any{}))
				ssWrite(t, conn, map[string]any{"type": "response.created", "event_id": "response-created-secret", "response": map[string]any{"id": "response-secret", "status": "in_progress"}})
				wrongPrevious := "different-user-secret"
				ssWrite(t, conn, ssModernItem("conversation.item.added", "assistant-added-secret", "assistant-secret", "assistant", &wrongPrevious, []any{}))
			},
			outcome: "correlation_mismatch",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := ssConfig(t)
			upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				ssBegin(t, conn)
				test.serve(t, conn)
			}))
			defer server.Close()
			config.WebSocketURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime?model=" + ScoutRealtimeModel
			receipt, err := RunRealtimeScout(context.Background(), config)
			if err == nil || receipt.Success || receipt.Outcome != test.outcome {
				t.Fatalf("invalid modern Scout lifecycle accepted: receipt=%+v err=%v", receipt, err)
			}
		})
	}
}

func TestRealtimeScoutRejectsResponseDoneBeforeModernAssistantItemDone(t *testing.T) {
	config := ssConfig(t)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		ssBegin(t, conn)
		ssCompleteLifecycleWithUsage(t, conn, ssUsage(), true, false)
	}))
	defer server.Close()
	config.WebSocketURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime?model=" + ScoutRealtimeModel
	receipt, err := RunRealtimeScout(context.Background(), config)
	if err == nil || receipt.Success || receipt.Outcome != "correlation_mismatch" || receipt.FailureClass != "event_correlation" {
		t.Fatalf("response.done accepted before assistant item.done: receipt=%+v err=%v", receipt, err)
	}
}

func TestRealtimeScoutRejectsNonLoopbackOverrideAndUnclassifiedUsage(t *testing.T) {
	fixture := rtNewFixture(t, nil)
	base := fixture.config.Config
	base.Model = ScoutRealtimeModel
	base.MaxUSD = 0.025
	config := RealtimeScoutConfig{Config: base, InteractionID: rtSegment, WebSocketURL: "wss://example.com/v1/realtime?model=" + ScoutRealtimeModel}
	if _, err := RunRealtimeScout(context.Background(), config); err == nil || !strings.Contains(err.Error(), "official") {
		t.Fatalf("foreign target accepted: %v", err)
	}
	usage := append(ssUsageBytes(), []byte(` `)...)
	usage = bytes.Replace(usage, []byte(`"output_token_details"`), []byte(`"reasoning_tokens":1,"output_token_details"`), 1)
	if _, err := parseScoutUsage(usage); err == nil {
		t.Fatal("unclassified reasoning usage was accepted")
	}
	unexpectedAudio := ssUsage()
	unexpectedAudio["input_tokens"] = 6
	unexpectedAudio["total_tokens"] = 12
	unexpectedAudio["input_token_details"].(map[string]any)["audio_tokens"] = 1
	rawUnexpectedAudio, _ := json.Marshal(unexpectedAudio)
	if _, err := parseScoutUsage(rawUnexpectedAudio); err == nil {
		t.Fatal("audio input usage was accepted for the fixed text-only probe")
	}
	if scoutWorstCaseCost() >= 0.02 || MaxScoutOutputTokens != 256 {
		t.Fatalf("bounded Scout token budget drifted: cost=%f output=%d", scoutWorstCaseCost(), MaxScoutOutputTokens)
	}
}

func TestParseScoutResponseStatus(t *testing.T) {
	tests := []struct {
		name, status, details, reason string
		wantErr                       bool
	}{
		{name: "completed", status: "completed", details: "null"},
		{name: "completed typed details", status: "completed", details: `{"type":"completed"}`},
		{name: "max token incomplete", status: "incomplete", details: `{"type":"incomplete","reason":"max_output_tokens"}`, reason: "max_output_tokens"},
		{name: "content filter incomplete", status: "incomplete", details: `{"type":"incomplete","reason":"content_filter"}`, reason: "content_filter"},
		{name: "cancelled", status: "cancelled", details: `{"type":"cancelled","reason":"client_cancelled"}`, reason: "client_cancelled"},
		{name: "failed", status: "failed", details: `{"type":"failed","error":{"type":"server_error","code":"provider_code"}}`, reason: "provider_failed"},
		{name: "failed optional error fields omitted", status: "failed", details: `{"type":"failed","error":{}}`, reason: "provider_failed"},
		{name: "failed optional type omitted", status: "failed", details: `{"type":"failed","error":{"code":"provider_code"}}`, reason: "provider_failed"},
		{name: "completed empty details forbidden", status: "completed", details: `{}`, wantErr: true},
		{name: "completed extra details forbidden", status: "completed", details: `{"type":"completed","reason":"other"}`, wantErr: true},
		{name: "in progress terminal forbidden", status: "in_progress", details: "null", wantErr: true},
		{name: "unknown incomplete reason", status: "incomplete", details: `{"type":"incomplete","reason":"other"}`, wantErr: true},
		{name: "mismatched detail type", status: "cancelled", details: `{"type":"incomplete","reason":"client_cancelled"}`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			details, err := parseScoutResponseStatus(test.status, json.RawMessage(test.details))
			if (err != nil) != test.wantErr || details.Reason != test.reason {
				t.Fatalf("parseScoutResponseStatus(%q) = details=%+v err=%v", test.status, details, err)
			}
		})
	}
}

func TestScoutFailedResponseReceiptRetainsHashedProviderClassification(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"type":     "response.done",
		"event_id": "event-failed",
		"response": map[string]any{
			"id":             "response-failed",
			"status":         "failed",
			"status_details": map[string]any{"type": "failed", "error": map[string]any{"type": "server_error", "code": "provider_code"}},
			"usage":          ssUsage(),
		},
	})
	done, err := parseScoutResponseDone(raw)
	if err != nil {
		t.Fatal(err)
	}
	var receipt RealtimeScoutReceipt
	recordScoutDoneStatus(&receipt, done)
	if receipt.ResponseStatus != "failed" || receipt.ResponseStatusDetailType != "failed" || receipt.ResponseStatusReason != "provider_failed" ||
		receipt.ResponseErrorTypeSHA256 != digest("server_error") || receipt.ResponseErrorCodeSHA256 != digest("provider_code") {
		t.Fatalf("failed provider classification was not safely retained: %+v", receipt)
	}
}

func TestRealtimeScoutRejectsMissingOrZeroUsageForCompletedAudio(t *testing.T) {
	tests := []struct {
		name  string
		usage map[string]any
	}{
		{name: "missing usage fields", usage: map[string]any{}},
		{name: "all-zero usage", usage: map[string]any{
			"total_tokens": 0, "input_tokens": 0, "output_tokens": 0,
			"input_token_details": map[string]any{
				"text_tokens": 0, "audio_tokens": 0, "image_tokens": 0, "cached_tokens": 0,
				"cached_tokens_details": map[string]any{"text_tokens": 0, "audio_tokens": 0, "image_tokens": 0},
			},
			"output_token_details": map[string]any{"text_tokens": 0, "audio_tokens": 0},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := ssConfig(t)
			upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				ssBegin(t, conn)
				ssCompleteWithUsage(t, conn, test.usage)
			}))
			defer server.Close()
			config.WebSocketURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime?model=" + ScoutRealtimeModel
			receipt, err := RunRealtimeScout(context.Background(), config)
			if err == nil || receipt.Outcome != "schema_mismatch" || receipt.FailureClass != "event_schema" {
				t.Fatalf("unaccounted completed audio was accepted: receipt=%+v err=%v", receipt, err)
			}
		})
	}
}

func TestParseScoutUsageAcceptsOmittedOptionalZeroDetailsButStillReconciles(t *testing.T) {
	usage := map[string]any{
		"total_tokens":  11,
		"input_tokens":  5,
		"output_tokens": 6,
		"input_token_details": map[string]any{
			"text_tokens": 5,
		},
		"output_token_details": map[string]any{
			"text_tokens":  3,
			"audio_tokens": 3,
		},
	}
	raw, _ := json.Marshal(usage)
	if got, err := parseScoutUsage(raw); err != nil || got.InputTotal != 5 || got.OutputAudio != 3 {
		t.Fatalf("optional zero usage details were not reconciled: usage=%+v err=%v", got, err)
	}

	delete(usage["input_token_details"].(map[string]any), "text_tokens")
	raw, _ = json.Marshal(usage)
	if _, err := parseScoutUsage(raw); err == nil {
		t.Fatal("omitted nonzero text category was accepted without reconciling the input total")
	}
}

func TestRealtimeScoutRejectsDuplicateServerEventID(t *testing.T) {
	config := ssConfig(t)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		ssBegin(t, conn)
		ssWrite(t, conn, map[string]any{"type": "conversation.item.created", "event_id": "duplicate-secret", "item": map[string]any{"id": "user-secret", "type": "message", "role": "user"}})
		ssWrite(t, conn, map[string]any{"type": "response.created", "event_id": "duplicate-secret", "response": map[string]any{"id": "response-secret", "status": "in_progress"}})
	}))
	defer server.Close()
	config.WebSocketURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime?model=" + ScoutRealtimeModel
	_, err := RunRealtimeScout(context.Background(), config)
	if !errors.Is(err, errRealtimeDuplicateEvent) {
		t.Fatalf("duplicate event id error = %v", err)
	}
}

func TestRealtimeScoutCancellationClosesEstablishedSocket(t *testing.T) {
	config := ssConfig(t)
	ready := make(chan struct{})
	closed := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		ssBegin(t, conn)
		close(ready)
		_, _, _ = conn.ReadMessage()
		close(closed)
	}))
	defer server.Close()
	config.WebSocketURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime?model=" + ScoutRealtimeModel
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := RunRealtimeScout(ctx, config); result <- err }()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("probe did not establish bounded session")
	}
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled probe returned success")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled probe did not return promptly")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("cancel did not close established websocket")
	}
}

func TestRealtimeScoutRejectsDuplicateTerminalDuringBoundedDrain(t *testing.T) {
	config := ssConfig(t)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		ssBegin(t, conn)
		ssComplete(t, conn)
		ssWrite(t, conn, map[string]any{"type": "response.done", "event_id": "duplicate-terminal-secret", "response": map[string]any{"id": "response-secret", "status": "completed", "usage": ssUsage()}})
	}))
	defer server.Close()
	config.WebSocketURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime?model=" + ScoutRealtimeModel
	_, err := RunRealtimeScout(context.Background(), config)
	if !errors.Is(err, errRealtimeDuplicateTerminal) {
		t.Fatalf("duplicate terminal error = %v", err)
	}
}

func TestRealtimeScoutRejectsMalformedEventAfterTerminal(t *testing.T) {
	config := ssConfig(t)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		ssBegin(t, conn)
		ssComplete(t, conn)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":`)); err != nil {
			t.Errorf("write malformed post-terminal event: %v", err)
		}
	}))
	defer server.Close()
	config.WebSocketURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime?model=" + ScoutRealtimeModel
	receipt, err := RunRealtimeScout(context.Background(), config)
	if err == nil || receipt.Outcome != "transport_error" || receipt.FailureClass != "read" {
		t.Fatalf("malformed post-terminal event was accepted: receipt=%+v err=%v", receipt, err)
	}
}

func TestVerifyNoPostTerminalScoutEventFailClosedMatrix(t *testing.T) {
	tests := []struct {
		name    string
		serve   func(*testing.T, *websocket.Conn)
		wantErr bool
	}{
		{
			name: "normal close",
			serve: func(t *testing.T, conn *websocket.Conn) {
				t.Helper()
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
			},
		},
		{
			name: "observation timeout",
			serve: func(t *testing.T, conn *websocket.Conn) {
				t.Helper()
				time.Sleep(2 * realtimeTerminalObserveWindow)
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
			},
		},
		{
			name: "binary frame",
			serve: func(t *testing.T, conn *websocket.Conn) {
				t.Helper()
				if err := conn.WriteMessage(websocket.BinaryMessage, []byte{1, 2, 3}); err != nil {
					t.Errorf("write binary event: %v", err)
				}
			},
			wantErr: true,
		},
		{
			name: "missing event id",
			serve: func(t *testing.T, conn *websocket.Conn) {
				t.Helper()
				ssWrite(t, conn, map[string]any{"type": "rate_limits.updated", "rate_limits": []any{}})
			},
			wantErr: true,
		},
		{
			name: "abnormal close",
			serve: func(t *testing.T, conn *websocket.Conn) {
				t.Helper()
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "synthetic failure"), time.Now().Add(time.Second))
			},
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("upgrade: %v", err)
					return
				}
				defer conn.Close()
				test.serve(t, conn)
			}))
			defer server.Close()
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer conn.Close()
			err = verifyNoPostTerminalScoutEvent(context.Background(), conn, &realtimeEventTracker{})
			if test.wantErr && err == nil {
				t.Fatal("post-terminal contract violation was accepted")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("safe post-terminal boundary failed: %v", err)
			}
		})
	}
}

func TestVerifyNoPostTerminalScoutEventAcceptsActualDrainCancellation(t *testing.T) {
	release := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		<-release
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	time.AfterFunc(10*time.Millisecond, cancel)
	err = verifyNoPostTerminalScoutEvent(ctx, conn, &realtimeEventTracker{})
	stopClose()
	close(release)
	if err != nil || ctx.Err() == nil {
		t.Fatalf("actual drain cancellation was not accepted safely: context=%v err=%v", ctx.Err(), err)
	}
}

func ssUsage() map[string]any {
	return map[string]any{"total_tokens": 11, "input_tokens": 5, "output_tokens": 6, "input_token_details": map[string]any{"text_tokens": 5, "audio_tokens": 0, "image_tokens": 0, "cached_tokens": 0, "cached_tokens_details": map[string]any{"text_tokens": 0, "audio_tokens": 0, "image_tokens": 0}}, "output_token_details": map[string]any{"text_tokens": 3, "audio_tokens": 3}}
}
func ssUsageBytes() []byte { b, _ := json.Marshal(ssUsage()); return b }
func ssWrite(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	if err := c.WriteJSON(v); err != nil {
		t.Error(err)
	}
}
func ssReadExact(t *testing.T, c *websocket.Conn, want any) {
	t.Helper()
	_, raw, err := c.ReadMessage()
	if err != nil {
		t.Error(err)
		return
	}
	var got any
	if json.Unmarshal(raw, &got) != nil {
		t.Error("invalid client json")
		return
	}
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if !bytes.Equal(g, w) {
		t.Errorf("client event mismatch\n got %s\nwant %s", g, w)
	}
}

func ssConfig(t *testing.T) RealtimeScoutConfig {
	t.Helper()
	fixture := rtNewFixture(t, nil)
	base := fixture.config.Config
	base.Model = ScoutRealtimeModel
	base.MaxUSD = 0.025
	return RealtimeScoutConfig{Config: base, InteractionID: rtSegment}
}

func ssBegin(t *testing.T, conn *websocket.Conn) {
	ssBeginWithConversation(t, conn, true)
}

func ssBeginWithoutConversation(t *testing.T, conn *websocket.Conn) {
	ssBeginWithConversation(t, conn, false)
}

func ssBeginWithConversation(t *testing.T, conn *websocket.Conn, includeConversation bool) {
	t.Helper()
	ssWrite(t, conn, map[string]any{"type": "session.created", "event_id": "created-secret", "session": map[string]any{"id": "session-secret", "type": "realtime", "model": ScoutRealtimeModel}})
	if includeConversation {
		ssWrite(t, conn, map[string]any{"type": "conversation.created", "event_id": "conversation-secret", "conversation": map[string]any{"object": "realtime.conversation"}})
	}
	ssReadExact(t, conn, scoutSessionUpdate(rtSegment))
	update := scoutSessionUpdate(rtSegment)
	update["session"].(map[string]any)["id"] = "session-secret"
	ssWrite(t, conn, map[string]any{"type": "session.updated", "event_id": "updated-secret", "session": update["session"]})
	ssReadExact(t, conn, scoutUserItem(rtSegment))
	ssReadExact(t, conn, scoutResponseCreate(rtSegment))
}

func ssComplete(t *testing.T, conn *websocket.Conn) {
	ssCompleteWithUsage(t, conn, ssUsage())
}

func ssCompleteWithUsage(t *testing.T, conn *websocket.Conn, usage map[string]any) {
	ssCompleteLifecycleWithUsage(t, conn, usage, false, true)
}

func ssCompleteModernWithUsage(t *testing.T, conn *websocket.Conn, usage map[string]any) {
	ssCompleteLifecycleWithUsage(t, conn, usage, true, true)
}

func ssCompleteLifecycleWithUsage(t *testing.T, conn *websocket.Conn, usage map[string]any, modern, finalizeAssistant bool) {
	t.Helper()
	if modern {
		ssWrite(t, conn, ssModernItem("conversation.item.added", "user-added-secret", "user-secret", "user", nil, []any{map[string]any{"type": "input_text", "text": "hello"}}))
		ssWrite(t, conn, ssModernItem("conversation.item.done", "user-done-secret", "user-secret", "user", nil, []any{map[string]any{"type": "input_text", "text": "hello"}}))
	} else {
		ssWrite(t, conn, map[string]any{"type": "conversation.item.created", "event_id": "user-secret", "item": map[string]any{"id": "user-secret", "type": "message", "role": "user"}})
	}
	ssWrite(t, conn, map[string]any{"type": "response.created", "event_id": "response-created-secret", "response": map[string]any{"id": "response-secret", "status": "in_progress"}})
	ssWrite(t, conn, map[string]any{"type": "response.output_item.added", "event_id": "item-added-secret", "response_id": "response-secret", "output_index": 0, "item": map[string]any{"id": "assistant-secret", "type": "message", "role": "assistant"}})
	if modern {
		previous := "user-secret"
		ssWrite(t, conn, ssModernItem("conversation.item.added", "assistant-added-secret", "assistant-secret", "assistant", &previous, []any{}))
	} else {
		ssWrite(t, conn, map[string]any{"type": "conversation.item.created", "event_id": "assistant-created-secret", "item": map[string]any{"id": "assistant-secret", "type": "message", "role": "assistant"}})
	}
	ssWrite(t, conn, map[string]any{"type": "response.content_part.added", "event_id": "content-added-secret", "response_id": "response-secret", "item_id": "assistant-secret", "output_index": 0, "content_index": 0, "part": map[string]any{"type": "audio"}})
	ssWrite(t, conn, map[string]any{"type": "response.output_audio.delta", "event_id": "audio-secret", "response_id": "response-secret", "item_id": "assistant-secret", "output_index": 0, "content_index": 0, "delta": base64.StdEncoding.EncodeToString([]byte{1, 2, 3})})
	ssWrite(t, conn, map[string]any{"type": "response.output_audio_transcript.delta", "event_id": "transcript-secret", "response_id": "response-secret", "item_id": "assistant-secret", "output_index": 0, "content_index": 0, "delta": "hello"})
	ssWrite(t, conn, map[string]any{"type": "response.output_audio.done", "event_id": "audio-done-secret", "response_id": "response-secret", "item_id": "assistant-secret", "output_index": 0, "content_index": 0})
	ssWrite(t, conn, map[string]any{"type": "response.output_audio_transcript.done", "event_id": "transcript-done-secret", "response_id": "response-secret", "item_id": "assistant-secret", "output_index": 0, "content_index": 0, "transcript": "hello"})
	ssWrite(t, conn, map[string]any{"type": "response.content_part.done", "event_id": "content-done-secret", "response_id": "response-secret", "item_id": "assistant-secret", "output_index": 0, "content_index": 0, "part": map[string]any{"type": "audio"}})
	if modern && finalizeAssistant {
		previous := "user-secret"
		ssWrite(t, conn, ssModernItem("conversation.item.done", "assistant-done-secret", "assistant-secret", "assistant", &previous, []any{map[string]any{"type": "output_audio", "transcript": "hello"}}))
	}
	ssWrite(t, conn, map[string]any{"type": "response.output_item.done", "event_id": "item-done-secret", "response_id": "response-secret", "output_index": 0, "item": map[string]any{"id": "assistant-secret", "object": "realtime.item", "type": "message", "status": "completed", "role": "assistant"}})
	ssWrite(t, conn, map[string]any{"type": "response.done", "event_id": "response-done-secret", "response": map[string]any{"id": "response-secret", "status": "completed", "usage": usage}})
}

func ssModernItem(eventType, eventID, itemID, role string, previous *string, content []any) map[string]any {
	var previousValue any
	if previous != nil {
		previousValue = *previous
	}
	return map[string]any{
		"type": eventType, "event_id": eventID, "previous_item_id": previousValue,
		"item": map[string]any{"id": itemID, "object": "realtime.item", "type": "message", "status": "completed", "role": role, "content": content},
	}
}
