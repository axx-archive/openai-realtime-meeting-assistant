package main

import (
	"os"
	"strings"
	"testing"
)

func TestScoutWorkStatusIsVisibleOnDesktopAndMobile(t *testing.T) {
	desktop, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read desktop shell: %v", err)
	}
	for _, want := range []string{
		"scout-chat-research__progress",
		"queued ? 'queued' : running ? 'running'",
		"data-research-activity",
		"syncDesktopActiveWorkIndicator",
		"agentName",
		"progressNote",
		"updateScoutChatResearchNode",
		"scout-chat-msg--media",
	} {
		if !strings.Contains(string(desktop), want) {
			t.Fatalf("desktop work indicator missing %q", want)
		}
	}

	runner, err := os.ReadFile("agent_thread_runner.go")
	if err != nil {
		t.Fatalf("read agent thread runner: %v", err)
	}
	if !strings.Contains(string(runner), `app.updateScoutChatThreadRefs(thread.ID, "running", thread.Artifact.ID)`) {
		t.Fatal("mid-run progress must persist into chat refs so native work cards advance before terminal delivery")
	}

	mobile, err := os.ReadFile("mobile/src/messaging/MessageBubble.tsx")
	if err != nil {
		t.Fatalf("read mobile message bubble: %v", err)
	}
	for _, want := range []string{
		"workThreadPresentation",
		"Gathering evidence",
		"Deliverable ready",
		"View activity · ${workThread.phase}",
		"Open report",
		"workThread?.agentName",
		"workThread.delegatedBy",
		"workStatusNeedsInput",
		"questionmark.circle.fill",
	} {
		if !strings.Contains(string(mobile), want) {
			t.Fatalf("mobile work indicator missing %q", want)
		}
	}
}

func TestGuestCannotReceiveFilesCatalogContext(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	if entries := app.assistantFileContextEntries(t.Context(), recallPrincipalForGuest("guest-1", "room-a", "sitting-a"), "what is in Files?"); len(entries) != 0 {
		t.Fatalf("guest Files context=%#v, want none", entries)
	}
}

func TestDesktopAndNativeWorkPhasesSeparateResearchFromBuilding(t *testing.T) {
	desktop, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	desktopText := string(desktop)
	for _, want := range []string{
		"if (/research|source|evidence/.test(current)) return { label: 'Gathering evidence', index: 1 }",
		"if (/build|draft|synth|execute|codex/.test(current)) return { label: 'Building', index: 2 }",
	} {
		if !strings.Contains(desktopText, want) {
			t.Fatalf("desktop phase contract missing %q", want)
		}
	}
	native, err := os.ReadFile("mobile/src/screens/ThreadScreen.tsx")
	if err != nil {
		t.Fatal(err)
	}
	nativeText := string(native)
	for _, want := range []string{
		"if (/research|source|evidence/u.test(stage)) return 'Gathering evidence';",
		"if (/build|draft|synth|execute|codex/u.test(stage)) return 'Building';",
	} {
		if !strings.Contains(nativeText, want) {
			t.Fatalf("native phase contract missing %q", want)
		}
	}
}
