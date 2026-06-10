package main

import (
	"sync"
	"testing"
	"time"
)

// TestScoutSpeaksWhenWakeTranscriptArrivesAfterToolResult replays the realistic
// event order: the function call's response.done completes before the async ASR
// transcript arms the wake window. The spoken response must still be queued.
func TestScoutSpeaksWhenWakeTranscriptArrivesAfterToolResult(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	app.handleRealtimeEvent([]byte(`{
		"type": "response.done",
		"response": {
			"status": "completed",
			"output": [{
				"type": "function_call",
				"name": "move_ticket",
				"call_id": "call-late-arm",
				"arguments": "{\"card_id\":\"card-002\",\"status\":\"In Progress\"}"
			}]
		}
	}`))

	app.mu.Lock()
	pendingBefore := app.scoutSpokenResponse
	recordedAt := app.scoutLastToolResultAt
	app.mu.Unlock()
	if pendingBefore {
		t.Fatal("spoken response should not be pending before the wake transcript arms")
	}
	if recordedAt.IsZero() {
		t.Fatal("tool result completed without an armed window should be recorded for a late arm")
	}

	app.handleRealtimeEvent([]byte(`{
		"type": "conversation.item.input_audio_transcription.completed",
		"transcript": "Hey Scout, move card two to in progress."
	}`))

	app.mu.Lock()
	defer app.mu.Unlock()
	if !app.scoutSpokenResponse {
		t.Fatal("late wake transcript should queue the spoken response for the recent tool result")
	}
	if !app.scoutVoiceArmedUntil.IsZero() {
		t.Fatal("consumed arm window should be cleared")
	}
	if !app.scoutLastToolResultAt.IsZero() {
		t.Fatal("recorded tool result should be consumed by the late arm")
	}
}

// TestSpeechStartedDoesNotDisarmScoutWake locks in that crosstalk
// (speech_started from anyone on the mixed stream) no longer clears the wake
// window, while a completed non-wake transcript still does.
func TestSpeechStartedDoesNotDisarmScoutWake(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	app.handleRealtimeEvent([]byte(`{
		"type": "conversation.item.input_audio_transcription.completed",
		"transcript": "Hey Scout, what is blocked?"
	}`))
	if !app.scoutVoiceArmed() {
		t.Fatal("wake transcript should arm the spoken response window")
	}

	app.handleRealtimeEvent([]byte(`{"type": "input_audio_buffer.speech_started"}`))
	if !app.scoutVoiceArmed() {
		t.Fatal("speech_started must not disarm the wake window")
	}

	app.handleTranscriptionLaneEvent([]byte(`{"type": "input_audio_buffer.speech_started"}`))
	if !app.scoutVoiceArmed() {
		t.Fatal("transcript lane speech_started must not disarm the wake window")
	}

	app.handleRealtimeEvent([]byte(`{
		"type": "conversation.item.input_audio_transcription.completed",
		"transcript": "I think the deploy is fine, moving on."
	}`))
	if app.scoutVoiceArmed() {
		t.Fatal("a completed non-wake transcript should clear the wake window")
	}
}

// TestConcurrentToolCallHandlingExecutesOnce guards the per-call dedupe now
// that tool calls can run concurrently with event processing.
func TestConcurrentToolCallHandlingExecutesOnce(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	item := kanbanRealtimeOutputItem{
		Type:      "function_call",
		Name:      "create_ticket",
		CallID:    "call-concurrent",
		Arguments: `{"title":"Concurrency card","notes":"Created exactly once.","owner":"AJ","tags":["test"],"status":"Backlog"}`,
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			app.handleToolCall(item, false)
		}()
	}
	wg.Wait()

	created := 0
	for _, card := range app.snapshotState().Cards {
		if card.Title == "Concurrency card" {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("concurrent handling created %d cards, want exactly 1", created)
	}
}

// TestArmedNoOpToolResultStillSpeaks covers "hey scout, move X to done" when X
// is already Done: an armed turn gets a confirmation even when nothing changed.
func TestArmedNoOpToolResultStillSpeaks(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	app.handleRealtimeEvent([]byte(`{
		"type": "conversation.item.input_audio_transcription.completed",
		"transcript": "Hey Scout, move card two to backlog."
	}`))

	// card-002 already sits in Backlog, so this is an ok no-op.
	app.handleToolCall(kanbanRealtimeOutputItem{
		Type:      "function_call",
		Name:      "move_ticket",
		CallID:    "call-armed-noop",
		Arguments: `{"card_id":"card-002","status":"Backlog"}`,
	}, false)

	app.mu.Lock()
	defer app.mu.Unlock()
	if !app.scoutSpokenResponse {
		t.Fatal("armed no-op tool result should still queue a spoken confirmation")
	}
}

// TestArmStateSnapshotAtToolStartSurvivesExpiry locks in fix 3: a slow tool
// whose turn was armed when it started still speaks after the window expired.
func TestArmStateSnapshotAtToolStartSurvivesExpiry(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.scoutVoiceArmedAt = time.Now().Add(-time.Minute)
	app.scoutVoiceArmedUntil = time.Now().Add(-time.Minute)
	app.mu.Unlock()

	app.markScoutSpokenResponsePending("answer_memory_question", map[string]any{"ok": true}, false, true)

	app.mu.Lock()
	pending := app.scoutSpokenResponse
	app.mu.Unlock()
	if !pending {
		t.Fatal("armed-at-start tool result should speak even after the window expired")
	}
}

func TestUnarmedToolResultIsRecordedNotSpoken(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	app.markScoutSpokenResponsePending("do_nothing", map[string]any{"ok": true, "reason": "nothing actionable"}, false, false)

	app.mu.Lock()
	defer app.mu.Unlock()
	if app.scoutSpokenResponse {
		t.Fatal("unarmed tool result should not queue a spoken response")
	}
	if app.scoutLastToolResultAt.IsZero() {
		t.Fatal("unarmed tool result should be recorded for a late-arriving wake transcript")
	}
	if app.scoutLastToolResultName != "do_nothing" {
		t.Fatalf("recorded tool name=%q, want do_nothing", app.scoutLastToolResultName)
	}
}

// TestInterruptedArmedToolCallBroadcastsRetryStatus covers fix 7: an armed
// command whose tool call was interrupted must not be dropped silently.
func TestInterruptedArmedToolCallBroadcastsRetryStatus(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	observeChatFeed(t, app)

	app.handleRealtimeEvent([]byte(`{
		"type": "conversation.item.input_audio_transcription.completed",
		"transcript": "Hey Scout, add a ticket for the launch."
	}`))

	app.handleToolCall(kanbanRealtimeOutputItem{
		Type:      "function_call",
		Name:      "create_ticket",
		CallID:    "call-armed-interrupted",
		Arguments: `{"title":"Launch tick`,
	}, false)

	app.mu.Lock()
	chat := app.assistantStatus
	armedUntil := app.scoutVoiceArmedUntil
	app.mu.Unlock()
	if chat != "scout missed that — say it again" {
		t.Fatalf("chat feed status=%q, want interrupted-command retry hint", chat)
	}
	if !armedUntil.IsZero() {
		t.Fatal("dropped armed command should clear the wake window")
	}
}

// TestAnswerMemoryQuestionRunsOffEventLoop verifies the slow memory tool is
// dispatched off the datachannel event loop and still queues the spoken reply.
func TestAnswerMemoryQuestionRunsOffEventLoop(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	app.handleRealtimeEvent([]byte(`{
		"type": "conversation.item.input_audio_transcription.completed",
		"transcript": "Hey Scout, what did we decide about the deploy?"
	}`))

	app.handleRealtimeEvent([]byte(`{
		"type": "response.done",
		"response": {
			"status": "completed",
			"output": [{
				"type": "function_call",
				"name": "answer_memory_question",
				"call_id": "call-async-memory",
				"arguments": "{\"query\":\"what did we decide about the deploy\"}"
			}]
		}
	}`))

	// dedupe is synchronous even though execution is not
	app.mu.Lock()
	_, handled := app.handledCalls["call-async-memory"]
	app.mu.Unlock()
	if !handled {
		t.Fatal("memory tool call should be marked handled before async execution")
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		app.mu.Lock()
		pending := app.scoutSpokenResponse
		app.mu.Unlock()
		if pending {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("async memory answer never queued the spoken response")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
