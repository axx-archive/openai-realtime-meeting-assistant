package main

import (
	"strings"
	"testing"
)

// observeChatFeed points the global broadcastAssistantEvent state at the
// isolated app so tests can see what reaches the chat feed via assistantStatus.
func observeChatFeed(t *testing.T, app *kanbanBoardApp) {
	t.Helper()
	prev := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = prev })
}

// TestResponseDoneInterruptedStatusSkipsToolCalls guards the primary
// interruption signal: response.done carries response.status, and tool calls
// from cancelled/incomplete/failed responses must be skipped silently even when
// their arguments parse cleanly.
func TestResponseDoneInterruptedStatusSkipsToolCalls(t *testing.T) {
	for _, status := range []string{"cancelled", "incomplete", "failed"} {
		t.Run(status, func(t *testing.T) {
			app := newIsolatedKanbanBoardApp(t)
			observeChatFeed(t, app)
			before := len(app.snapshotState().Cards)

			const callID = "call-interrupted-by-status"
			app.handleRealtimeEvent([]byte(`{
				"type": "response.done",
				"response": {
					"status": "` + status + `",
					"status_details": {"type": "` + status + `", "reason": "turn_detected"},
					"output": [{
						"type": "function_call",
						"name": "create_ticket",
						"call_id": "` + callID + `",
						"arguments": "{\"title\":\"Fully specified but cancelled\"}"
					}]
				}
			}`))

			if after := len(app.snapshotState().Cards); after != before {
				t.Fatalf("%s response mutated the board: %d -> %d cards", status, before, after)
			}
			app.mu.Lock()
			_, handled := app.handledCalls[callID]
			chat := app.assistantStatus
			app.mu.Unlock()
			if !handled {
				t.Fatalf("%s response should mark the call handled so later events skip it", status)
			}
			if chat != "" {
				t.Fatalf("%s response leaked to the chat feed: %q", status, chat)
			}
		})
	}
}

func TestResponseDoneCompletedStatusStillExecutesToolCalls(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	observeChatFeed(t, app)
	before := len(app.snapshotState().Cards)

	app.handleRealtimeEvent([]byte(`{
		"type": "response.done",
		"response": {
			"status": "completed",
			"output": [{
				"type": "function_call",
				"name": "create_ticket",
				"call_id": "call-completed-status",
				"arguments": "{\"title\":\"Completed response card\"}"
			}]
		}
	}`))

	if after := len(app.snapshotState().Cards); after != before+1 {
		t.Fatalf("completed response did not execute the tool call: %d -> %d cards", before, after)
	}
}

// TestEmptyToolArgumentsOnFinalEventSkippedSilently covers a barge-in before
// any argument bytes streamed: the tool must not execute with no args and no
// "title is required" style error may reach the chat feed.
func TestEmptyToolArgumentsOnFinalEventSkippedSilently(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
	}{
		{"empty", ""},
		{"whitespace", "  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newIsolatedKanbanBoardApp(t)
			observeChatFeed(t, app)
			before := len(app.snapshotState().Cards)

			const callID = "call-empty-args"
			// Streaming event first: must wait, not execute with no args.
			app.handleToolCall(kanbanRealtimeOutputItem{
				Type:      "function_call",
				Name:      "create_ticket",
				CallID:    callID,
				Arguments: tc.args,
			}, true)

			app.mu.Lock()
			_, handledEarly := app.handledCalls[callID]
			app.mu.Unlock()
			if handledEarly {
				t.Fatal("empty streaming arguments should not mark the call handled")
			}

			// Final event repeats the same empty args: skip silently.
			app.handleToolCall(kanbanRealtimeOutputItem{
				Type:      "function_call",
				Name:      "create_ticket",
				CallID:    callID,
				Arguments: tc.args,
			}, false)

			if after := len(app.snapshotState().Cards); after != before {
				t.Fatalf("empty-args tool call mutated the board: %d -> %d cards", before, after)
			}
			app.mu.Lock()
			_, handled := app.handledCalls[callID]
			chat := app.assistantStatus
			app.mu.Unlock()
			if !handled {
				t.Fatal("empty-args final event should mark the call handled")
			}
			if chat != "" {
				t.Fatalf("empty-args tool call leaked to the chat feed: %q", chat)
			}
		})
	}
}

func TestAPIRequestFailedErrorOmitsResponseBody(t *testing.T) {
	body := `{"error":{"message":"Incorrect API key provided: sk-proj-****","type":"invalid_request_error"}}`
	err := apiRequestFailedError("Realtime session failed", "401 Unauthorized", []byte(body))
	if err == nil {
		t.Fatal("expected an error")
	}
	if got, want := err.Error(), "api request failed (401 Unauthorized)"; got != want {
		t.Fatalf("error=%q, want %q", got, want)
	}
	if strings.Contains(err.Error(), "Incorrect API key") {
		t.Fatalf("error leaked the response body: %q", err.Error())
	}
}

// TestUnrecognizedRealtimeErrorStaysOffChatFeed verifies raw OpenAI error
// payloads are downgraded to a short status line instead of being broadcast
// verbatim as chat-feed errors.
func TestUnrecognizedRealtimeErrorStaysOffChatFeed(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	observeChatFeed(t, app)

	app.handleRealtimeEvent([]byte(`{
		"type": "error",
		"error": {
			"code": "server_error",
			"message": "The server had an error while processing your request: {\"raw\":\"json\"}"
		}
	}`))

	app.mu.Lock()
	chat := app.assistantStatus
	app.mu.Unlock()
	if chat != "assistant hit a server error" {
		t.Fatalf("chat feed text=%q, want short status text", chat)
	}
}

func TestTranscriptionLaneErrorStaysOffChatFeed(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	observeChatFeed(t, app)

	expired := app.handleTranscriptionLaneEvent([]byte(`{
		"type": "error",
		"error": {
			"code": "invalid_request_error",
			"message": "Invalid value: {\"raw\":\"json\"}"
		}
	}`))

	if expired {
		t.Fatal("non-expiry error should not signal session expiration")
	}
	app.mu.Lock()
	chat := app.assistantStatus
	app.mu.Unlock()
	if chat != "transcript lane hit a server error" {
		t.Fatalf("chat feed text=%q, want short status text", chat)
	}
}
