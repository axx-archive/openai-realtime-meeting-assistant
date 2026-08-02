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
		"mount(roomChatForm, roomChatInput, 'chat')",
		"mount(osAssistantForm, osAssistantInput, 'chat')",
		"mount(assistantForm, assistantInput, 'chat')",
		"agentToolForms.forEach(form => mount",
		"body.append('audio', blob, 'dictation.webm')",
		"fetch('/assistant/transcribe'",
		"form.requestSubmit()",
		".stride-dictation-action[hidden]",
	} {
		if !strings.Contains(combined, want) {
			t.Errorf("dictation integration missing %q", want)
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
