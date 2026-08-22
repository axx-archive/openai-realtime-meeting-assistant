package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPrivateRealtimeWebRoutesOnceBeforeSpeaking(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	events := functionBody(html, "function handlePrivateRealtimeVoiceEvent(raw, sessionToken, peer)")
	continueCalls := functionBody(html, "async function continuePrivateRealtimeToolCalls(items, sessionToken, peer)")
	handleCall := functionBody(html, "async function handlePrivateRealtimeToolCall(item, sessionToken, peer)")

	for _, retired := range []string{
		"checkPrivateRealtimeShouldRoute",
		"enablePrivateRealtimeToolsForAction",
		"privateRealtimeVoicePendingRouteCheck",
		"privateRealtimeVoiceResponseDoneWithoutTool",
	} {
		if strings.Contains(html, retired) {
			t.Fatalf("private voice still contains retired first-answer/late-route state %q", retired)
		}
	}
	if strings.Contains(events, "/assistant/realtime/should-route") || strings.Contains(events, "input_audio_transcription") {
		t.Fatalf("private voice events still run the delayed transcript classifier:\n%s", events)
	}
	if got := strings.Count(events, "continuePrivateRealtimeToolCalls(toolCalls, sessionToken, peer)"); got != 1 {
		t.Fatalf("terminal provider response starts %d tool continuations, want exactly one", got)
	}
	if !strings.Contains(events, "setVoiceIslandState('thinking', 'checking context…')") {
		t.Fatal("completed private voice turn does not truthfully expose the grounded routing beat")
	}
	if strings.Contains(continueCalls, "session.update") || strings.Contains(continueCalls, "tool_choice: 'auto'") {
		t.Fatalf("post-tool continuation mutates the required session contract:\n%s", continueCalls)
	}
	if got := strings.Count(continueCalls, "type: 'response.create'"); got != 2 || strings.Count(continueCalls, "tool_choice: 'none'") != 2 || !strings.Contains(continueCalls, "if (!batchValid)") {
		t.Fatalf("post-tool continuation must have one normal and one fail-closed speech site:\n%s", continueCalls)
	}
	for _, want := range []string{
		"const batchValid = calls.length === 1",
		"execute none",
		"privateRealtimeVoiceHandledCalls.add(callId)",
		"I couldn't safely route that voice turn. Please try again.",
		"item?.name || '').trim() === 'route_conversation_turn'",
		"if (!hasRoutedTurn)",
		"setVoiceIslandState('listening', 'listening…')",
		"Speak only the message string from the most recent route_conversation_turn function result, exactly as written.",
	} {
		if !strings.Contains(continueCalls, want) {
			t.Fatalf("web silence/verbatim continuation missing %q:\n%s", want, continueCalls)
		}
	}
	if !strings.Contains(handleCall, "privateRealtimeVoiceHandledCalls.has(callId)") || !strings.Contains(handleCall, "privateRealtimeVoiceHandledCalls.add(callId)") {
		t.Fatal("provider call-id replay fence is missing from private voice")
	}
}

func TestPrivateRealtimeWebRejectsParallelDistinctRoutesWithoutExecutingEither(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	body := functionBody(string(raw), "async function continuePrivateRealtimeToolCalls(items, sessionToken, peer)")
	script := fmt.Sprintf(`
const assert = require('node:assert/strict');
let privateRealtimeVoiceSessionToken = 7;
let privateRealtimeVoicePeer = { id: 'peer' };
let privateRealtimeVoiceHandledCalls = new Set();
let executed = 0;
let outputs = [];
let events = [];
let states = [];
const privateRealtimeVoiceActive = () => true;
const handlePrivateRealtimeToolCall = async () => { executed += 1; return true; };
const sendPrivateRealtimeToolOutput = (callId, output) => { outputs.push({ callId, output }); return true; };
const sendPrivateRealtimeEvent = event => { events.push(event); return true; };
const setVoiceIslandState = (...args) => states.push(args);
async function continuePrivateRealtimeToolCalls(items, sessionToken, peer) {%s}
(async () => {
  await continuePrivateRealtimeToolCalls([
    { type: 'function_call', call_id: 'route-a', name: 'route_conversation_turn', arguments: '{"utterance":"make a deck"}' },
    { type: 'function_call', call_id: 'route-b', name: 'route_conversation_turn', arguments: '{"utterance":"make a deck"}' }
  ], 7, privateRealtimeVoicePeer);
  assert.equal(executed, 0, 'invalid parallel routes executed a durable turn');
  assert.deepEqual(outputs.map(item => item.callId), ['route-a', 'route-b']);
  assert.equal(events.length, 1, 'invalid batch must produce one fixed speech continuation');
  assert.equal(events[0].type, 'response.create');
  assert.equal(events[0].response.tool_choice, 'none');
  assert.match(events[0].response.instructions, /Please try again/);
  assert.equal(privateRealtimeVoiceHandledCalls.size, 2);
})().catch(error => { console.error(error); process.exit(1); });
`, body)
	cmd := exec.Command("node", "-e", script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("web parallel-route runtime gate: %v\n%s", err, output)
	}
}

func TestPrivateRealtimeNativeUsesRequiredSessionWithoutRecursiveRouting(t *testing.T) {
	realtime := string(mustReadTestFile(t, "mobile/src/realtime/usePersonalRealtime.ts"))
	protocol := string(mustReadTestFile(t, "mobile/src/realtime/personalRealtimeProtocol.ts"))
	continueCalls := functionBodyAfterSignature(realtime, "const continueToolCalls = useCallback(async (")

	if !strings.Contains(realtime, "call.name === 'do_nothing' || call.name === 'route_conversation_turn'") {
		t.Fatal("native voice does not render the server-owned route as a grounded thinking beat")
	}
	if strings.Contains(continueCalls, "session.update") || strings.Contains(continueCalls, "tool_choice: 'auto'") {
		t.Fatalf("native post-tool continuation mutates the required session contract:\n%s", continueCalls)
	}
	for _, want := range []string{
		"realtimeToolContinuationPolicy(calls)",
		"if (!continuation.valid)",
		"none of them when that invariant is violated",
		"handledCallsRef.current.add(call.callId)",
		"if (!continuation.shouldRespond)",
		"setLiveStatus('listening')",
		"sendSpokenToolContinuation(continuation.instructions)",
	} {
		if !strings.Contains(continueCalls, want) {
			t.Fatalf("native silence/verbatim continuation missing %q:\n%s", want, continueCalls)
		}
	}
	if !strings.Contains(protocol, "if (type.includes('speech_stopped')) return 'thinking'") {
		t.Fatal("native voice does not show the grounded route after a completed utterance")
	}
	speech := functionBodyAfterSignature(realtime, "const sendSpokenToolContinuation = useCallback((instructions: string): boolean =>")
	for _, want := range []string{"type: 'response.create'", "tool_choice: 'none'", "instructions"} {
		if !strings.Contains(speech, want) {
			t.Fatalf("native speech-only continuation helper missing %q:\n%s", want, speech)
		}
	}
	if got := strings.Count(realtime, "type: 'response.create'"); got != 1 {
		t.Fatalf("native voice has %d speech continuation sites, want one helper-owned site", got)
	}
}

func TestOpenChatThreadActionUsesExactServerResolvedIDOnWebAndNative(t *testing.T) {
	html := string(mustReadTestFile(t, "index.html"))
	webOpen := functionBody(html, "async function openAuthorizedChatThreadAction(action)")
	webDispatch := functionBody(html, "function handleOSAssistantActions(actions)")
	root := string(mustReadTestFile(t, "mobile/src/navigation/RootNavigator.tsx"))
	thread := string(mustReadTestFile(t, "mobile/src/screens/ThreadScreen.tsx"))

	for label, source := range map[string]string{"web": webDispatch, "native voice": root, "native typed": thread} {
		if !strings.Contains(source, "open_chat_thread") {
			t.Fatalf("%s client does not handle the admitted chat navigation action", label)
		}
	}
	for _, want := range []string{
		"const threadId = String(action?.threadId || '').trim()",
		"selectScoutChatThread(threadId)",
		"setMobileChatView('convo')",
	} {
		if !strings.Contains(webOpen, want) {
			t.Fatalf("web exact-thread navigation missing %q:\n%s", want, webOpen)
		}
	}
	if strings.Contains(webOpen, "action?.title") {
		t.Fatal("web navigation fell back to the model/display title instead of the server-resolved thread id")
	}
	for label, source := range map[string]string{"native voice": root, "native typed": thread} {
		if !strings.Contains(source, "navigationRef.navigate('Thread'") && !strings.Contains(source, "navigation.navigate(\"Thread\"") {
			t.Fatalf("%s does not deep-link to the exact Thread route", label)
		}
	}
	if strings.Contains(root, "voiceThreadIdRef") || strings.Contains(thread, "voiceThreadIdRef") {
		t.Fatal("chat navigation must not rebind the owner-private Realtime transcript")
	}
}
