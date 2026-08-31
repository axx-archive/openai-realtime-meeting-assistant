package main

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestWebComposerDictationInstallsEveryFirstPartyComposer(t *testing.T) {
	body, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	module, err := os.ReadFile("public/composer-dictation.js")
	if err != nil {
		t.Fatal(err)
	}
	combined := html + string(module)
	for _, want := range []string{
		`<script src="/public/composer-dictation.js"></script>`,
		"mount(scoutChatForm, scoutChatInput, 'chat')",
		"mount(chatContextReplyForm, chatContextReplyInput, 'chat')",
		"mount(homeScoutComposer, homeScoutInput, 'chat')",
		"mount(roomChatForm, roomChatInput, 'chat')",
		"mount(osAssistantForm, osAssistantInput, 'chat')",
		"mount(assistantForm, assistantInput, 'chat')",
		"agentToolForms.forEach(form => mount",
		"body.append('audio', blob, 'dictation.webm')",
		"fetch('/assistant/transcribe'",
		"form.requestSubmit()",
		".stride-dictation-action[hidden]",
		"min-width: 44px; height: 44px",
	} {
		if !strings.Contains(combined, want) {
			t.Errorf("dictation integration missing %q", want)
		}
	}
}

func TestWebHomeScoutComposerUsesAtomicPrivateOpening(t *testing.T) {
	body, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		`id="homeScoutComposer"`,
		`id="homeScoutInput"`,
		`id="homeScoutComposerStatus"`,
		"'Idempotency-Key': attempt.key",
		"JSON.stringify({ openingMessage: { text: attempt.text, ...(attempt.projectContextToken ? { projectContextToken: attempt.projectContextToken } : {}) } })",
		"homeScoutInput.value = attempt.text",
		"setScoutTab('private')",
		"selectScoutChatThread(thread.id)",
		"setMobileChatView('convo')",
		"Personal Realtime is app-level presence",
		`.home-scout-composer__send {`,
		"box-sizing: border-box;",
		"grid-template-columns: minmax(0, 1fr);",
		"width: 44px;",
		"height: 44px;",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("atomic web home opening missing %q", want)
		}
	}
	if strings.Contains(html, "terminalReason: 'superseded_by_text_composer'") {
		t.Fatal("ordinary Home typing or Send must not hang up persistent personal Realtime")
	}
}

func TestWebHomeScoutOpeningRendersReplyLifecycleWithoutFalseProviderClaims(t *testing.T) {
	body, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		"function scoutReplyLifecyclePresentation(message)",
		"Scout is queued",
		"Scout is responding",
		"Scout response canceled",
		"scout-chat-reply-state__retry",
		"/messages/${encodeURIComponent(messageId)}/retry",
		"decorateScoutReplyLifecycle(node, message, replyLifecycle)",
		"const messageActionsReady = !message.reply || message.reply.state === 'completed'",
		"message.id && kind !== 'error' && messageActionsReady",
		"? (String(authorLabel || '').trim() || 'scout')",
		"isPhone ? `ask ${directAgent || 'Scout'} anything…`",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("web Scout reply lifecycle missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"idname.textContent = 'claude'",
		"idmodel.textContent = `· fable 5",
	} {
		if strings.Contains(html, forbidden) {
			t.Errorf("ordinary Scout reply exposes an unattested provider/model claim %q", forbidden)
		}
	}
}

func TestWebComposerDictationAssetIsServedFromItsDeclaredURL(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/public/composer-dictation.js", nil)
	publicAssetHandler(recorder, request)

	if recorder.Code != 200 {
		t.Fatalf("GET /public/composer-dictation.js status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "javascript") {
		t.Fatalf("GET /public/composer-dictation.js content-type=%q", contentType)
	}
	if !strings.Contains(recorder.Body.String(), "StrideComposerDictation") {
		t.Fatal("served dictation asset is missing the controller export")
	}
}

func TestWebDictationFocusParksForRoomAndMutesPrivateCapture(t *testing.T) {
	body, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		"terminalReason: 'superseded_by_dictation'",
		"window.__bonfirePrivateRealtimeTerminalReason",
		"await window.__bonfireDictation?.parkForRoomJoin?.()",
		"await acquireRealtimeVoiceAudioFocus('private_realtime')",
		"await acquireRealtimeVoiceAudioFocus('room_realtime')",
		"acquireRealtime(mode, close) { return focus.acquire(mode, { close }) }",
		"controller.park('superseded_by_realtime')",
		"const priorMuted = isMicMuted",
		"window.__bonfirePrivateDictationCapture = true",
		"setLocalMute(true, { announce: false })",
		"setLocalMute(Boolean(restore.priorMuted), { announce: false })",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("audio focus boundary missing %q", want)
		}
	}
}
