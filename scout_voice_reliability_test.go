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

// TestCrosstalkTranscriptDoesNotDisarmDuringActiveResponse covers the
// crosstalk ordering on the single mixed room stream: arm -> another
// speaker's non-wake transcript completes while the wake turn's response is
// in flight -> tool result arrives -> Scout still speaks.
func TestCrosstalkTranscriptDoesNotDisarmDuringActiveResponse(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	app.handleRealtimeEvent([]byte(`{
		"type": "conversation.item.input_audio_transcription.completed",
		"transcript": "Hey Scout, move card two to in progress."
	}`))
	if !app.scoutVoiceArmed() {
		t.Fatal("wake transcript should arm the spoken response window")
	}

	// the model starts generating the wake turn's tool call
	app.handleRealtimeEvent([]byte(`{"type": "response.created"}`))

	// another participant's segment completes mid-turn
	app.handleRealtimeEvent([]byte(`{
		"type": "conversation.item.input_audio_transcription.completed",
		"transcript": "I think the deploy is fine, moving on."
	}`))
	if !app.scoutVoiceArmed() {
		t.Fatal("crosstalk must not disarm while the wake turn's response is in flight")
	}

	app.handleRealtimeEvent([]byte(`{
		"type": "response.done",
		"response": {
			"status": "completed",
			"output": [{
				"type": "function_call",
				"name": "move_ticket",
				"call_id": "call-crosstalk",
				"arguments": "{\"card_id\":\"card-002\",\"status\":\"In Progress\"}"
			}]
		}
	}`))

	app.mu.Lock()
	defer app.mu.Unlock()
	if !app.scoutSpokenResponse {
		t.Fatal("armed wake turn interrupted by crosstalk should still speak its result")
	}
}

// TestSplitUtteranceContinuationDoesNotDisarmDuringToolCall covers
// "Hey Scout" [pause] "command": the continuation's completed transcript must
// not disarm while the turn's tool call is still executing.
func TestSplitUtteranceContinuationDoesNotDisarmDuringToolCall(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	app.handleRealtimeEvent([]byte(`{
		"type": "conversation.item.input_audio_transcription.completed",
		"transcript": "Hey Scout."
	}`))
	if !app.scoutVoiceArmed() {
		t.Fatal("wake transcript should arm the spoken response window")
	}

	// a slow tool call (e.g. answer_memory_question) is mid-execution
	app.beginScoutToolCall()
	app.handleRealtimeEvent([]byte(`{
		"type": "conversation.item.input_audio_transcription.completed",
		"transcript": "Move the login card to done."
	}`))
	if !app.scoutVoiceArmed() {
		t.Fatal("the command continuation must not disarm while a tool call is executing")
	}
	app.endScoutToolCall()

	// once nothing is in flight, a completed non-wake transcript still ends
	// the armed turn (the pre-existing behavior).
	app.handleRealtimeEvent([]byte(`{
		"type": "conversation.item.input_audio_transcription.completed",
		"transcript": "Anyway, back to the standup."
	}`))
	if app.scoutVoiceArmed() {
		t.Fatal("a completed non-wake transcript should clear the wake window when idle")
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

	app.markScoutSpokenResponsePending("answer_memory_question", map[string]any{"ok": true, "answer": "the shoot is friday"}, false, false)

	app.mu.Lock()
	defer app.mu.Unlock()
	if app.scoutSpokenResponse {
		t.Fatal("unarmed tool result should not queue a spoken response")
	}
	if app.scoutLastToolResultAt.IsZero() {
		t.Fatal("unarmed tool result should be recorded for a late-arriving wake transcript")
	}
	if app.scoutLastToolResultName != "answer_memory_question" {
		t.Fatalf("recorded tool name=%q, want answer_memory_question", app.scoutLastToolResultName)
	}
}

// TestDoNothingNeverEntersWakeGraceBuffer locks in that ambient do_nothing
// churn (tool_choice "required" emits it for nearly every utterance) cannot
// contaminate the wake-grace buffer: a later "Hey Scout" must arm for the
// real answer instead of speaking immediately about nothing.
func TestDoNothingNeverEntersWakeGraceBuffer(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	app.markScoutSpokenResponsePending("do_nothing", map[string]any{"ok": true, "reason": "side conversation"}, false, false)

	app.mu.Lock()
	recordedAt := app.scoutLastToolResultAt
	app.mu.Unlock()
	if !recordedAt.IsZero() {
		t.Fatal("do_nothing must not be recorded into the wake-grace buffer")
	}

	app.handleRealtimeEvent([]byte(`{
		"type": "conversation.item.input_audio_transcription.completed",
		"transcript": "Hey Scout, what did we decide about pricing?"
	}`))

	app.mu.Lock()
	defer app.mu.Unlock()
	if app.scoutSpokenResponse {
		t.Fatal("wake after ambient do_nothing must not speak immediately")
	}
	if app.scoutVoiceArmedUntil.IsZero() {
		t.Fatal("wake after ambient do_nothing should arm normally for the real answer")
	}
}

// TestWakeArmsNormallyWhenBufferedResultIsDoNothing is the defensive half of
// the grace-buffer fix: even if a do_nothing somehow lands in the buffer, the
// wake transcript must arm instead of consuming it as speak-now.
func TestWakeArmsNormallyWhenBufferedResultIsDoNothing(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.scoutLastToolResultAt = time.Now()
	app.scoutLastToolResultName = "do_nothing"
	app.mu.Unlock()

	app.handleRealtimeEvent([]byte(`{
		"type": "conversation.item.input_audio_transcription.completed",
		"transcript": "Hey Scout, what did we decide about pricing?"
	}`))

	app.mu.Lock()
	defer app.mu.Unlock()
	if app.scoutSpokenResponse {
		t.Fatal("buffered do_nothing must not be consumed as a speak-now result")
	}
	if app.scoutVoiceArmedUntil.IsZero() {
		t.Fatal("wake should arm normally when the buffered result is do_nothing")
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
	if chat != "Scout missed that — say it again" {
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
