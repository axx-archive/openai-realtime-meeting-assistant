package main

import (
	"errors"
	"testing"
)

func TestClassifyToolArgParse(t *testing.T) {
	incomplete := errors.New("parse do_nothing arguments: unexpected end of JSON input")
	malformed := errors.New("parse create_ticket arguments: invalid character 'x' looking for beginning of value")

	// probeParseErr produces the real error parseToolCallArguments returns for
	// the given raw arguments, so the table exercises Go's actual JSON syntax
	// errors (truncation mid-escape/mid-\u/mid-literal/mid-number yields
	// "invalid character ..." messages, not "unexpected end of JSON input").
	probeParseErr := func(arguments string) error {
		_, err := parseToolCallArguments(kanbanRealtimeOutputItem{Name: "probe", Arguments: arguments})
		if err == nil {
			t.Fatalf("expected parse error for %q", arguments)
		}
		return err
	}

	cases := []struct {
		name            string
		err             error
		allowIncomplete bool
		want            toolArgParseOutcome
	}{
		{"nil error proceeds", nil, false, toolArgsComplete},
		{"nil error proceeds while streaming", nil, true, toolArgsComplete},
		{"truncated while streaming waits", incomplete, true, toolArgsAwaitingMore},
		{"truncated on final event is interrupted", incomplete, false, toolArgsInterrupted},
		{"malformed complete JSON is a real error", malformed, false, toolArgsMalformed},
		{"malformed while streaming is still a real error", malformed, true, toolArgsMalformed},
		{"truncated mid-string waits while streaming", probeParseErr(`{"reason":"half spo`), true, toolArgsAwaitingMore},
		{"truncated mid-string on final event is interrupted", probeParseErr(`{"reason":"half spo`), false, toolArgsInterrupted},
		{"truncated mid-escape waits while streaming", probeParseErr(`{"reason":"\`), true, toolArgsAwaitingMore},
		{"truncated mid-escape on final event is interrupted", probeParseErr(`{"reason":"\`), false, toolArgsInterrupted},
		{"truncated mid-unicode-escape on final event is interrupted", probeParseErr(`{"reason":"\u0`), false, toolArgsInterrupted},
		{"truncated mid-literal on final event is interrupted", probeParseErr(`{"done":tru`), false, toolArgsInterrupted},
		{"truncated mid-number on final event is interrupted", probeParseErr(`{"n":12.`), false, toolArgsInterrupted},
		{"malformed mid-input number is a real error", probeParseErr(`{"n":12.x}`), false, toolArgsMalformed},
		{"malformed mid-input value is a real error", probeParseErr(`{"reason":x}`), false, toolArgsMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyToolArgParse(tc.err, tc.allowIncomplete); got != tc.want {
				t.Fatalf("classifyToolArgParse(%v, %v) = %d, want %d", tc.err, tc.allowIncomplete, got, tc.want)
			}
		})
	}
}

// TestInterruptedToolCallDoesNotMutateBoard guards against an interrupted, half-
// specified board mutation (not just do_nothing) being applied with partial args.
func TestInterruptedToolCallDoesNotMutateBoard(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	before := len(app.snapshotState().Cards)

	app.handleRealtimeEvent([]byte(`{
		"type": "response.done",
		"response": {
			"output": [{
				"type": "function_call",
				"name": "create_ticket",
				"call_id": "call-create-interrupted",
				"arguments": "{\"title\":\"Half spoken"
			}]
		}
	}`))

	if after := len(app.snapshotState().Cards); after != before {
		t.Fatalf("interrupted create_ticket mutated the board: %d -> %d cards", before, after)
	}
}

// TestInterruptedResponseDoneWithPartialArgsIsSkippedSilently reproduces the
// "parse do_nothing arguments: unexpected end of JSON input" chat-feed error.
//
// When a realtime response is interrupted (e.g. someone starts talking and VAD
// cancels the model mid-tool-call), response.done carries a function call whose
// arguments JSON was cut off. The streaming events wait for completion, but the
// final response.done must NOT surface that truncation as a user-visible error —
// the call was cancelled and should simply be skipped.
func TestInterruptedResponseDoneWithPartialArgsIsSkippedSilently(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	// broadcastAssistantEvent records "error"/"status" text on the global app;
	// point it at the isolated app so we can observe what reaches the chat feed.
	prev := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = prev })

	const callID = "call-do-nothing-interrupted"
	const truncatedArgs = `{\"reason\":\"`

	// Streaming event with partial args: should wait, not handle, not error.
	app.handleRealtimeEvent([]byte(`{
		"type": "response.output_item.done",
		"item": {
			"type": "function_call",
			"name": "do_nothing",
			"call_id": "` + callID + `",
			"arguments": "` + truncatedArgs + `"
		}
	}`))

	app.mu.Lock()
	_, handledEarly := app.handledCalls[callID]
	app.mu.Unlock()
	if handledEarly {
		t.Fatal("partial streaming arguments should not mark the call handled")
	}

	// The response is interrupted: response.done repeats the SAME truncated args.
	app.handleRealtimeEvent([]byte(`{
		"type": "response.done",
		"response": {
			"output": [{
				"type": "function_call",
				"name": "do_nothing",
				"call_id": "` + callID + `",
				"arguments": "` + truncatedArgs + `"
			}]
		}
	}`))

	app.mu.Lock()
	_, handled := app.handledCalls[callID]
	status := app.assistantStatus
	app.mu.Unlock()

	if !handled {
		t.Fatal("interrupted call should be marked handled so it is not reprocessed")
	}
	// An interrupted no-op tool call must surface NOTHING to the chat feed:
	// neither the "unexpected end of JSON input" parse error nor the follow-on
	// "could not send tool result" error from trying to answer a cancelled call.
	if status != "" {
		t.Fatalf("interrupted tool call leaked an error to the chat feed: %q", status)
	}
}
