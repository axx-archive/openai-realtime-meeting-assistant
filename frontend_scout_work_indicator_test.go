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
		"artifact ready",
		"updateScoutChatResearchNode",
	} {
		if !strings.Contains(string(desktop), want) {
			t.Fatalf("desktop work indicator missing %q", want)
		}
	}

	mobile, err := os.ReadFile("mobile/src/messaging/MessageBubble.tsx")
	if err != nil {
		t.Fatalf("read mobile message bubble: %v", err)
	}
	for _, want := range []string{
		"workThreadPresentation",
		"Scout is working",
		"Deliverable ready",
		"Updates and the finished work will land here",
		"Delivered here · Tap to open the report",
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
