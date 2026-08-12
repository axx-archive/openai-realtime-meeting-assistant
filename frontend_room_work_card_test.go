package main

import (
	"os"
	"strings"
	"testing"
)

func TestIndexRoomWorkCardEvolvesByRunAndOpensArtifact(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		"item.dataset.workRunId = workRunId",
		"roomChatThread.querySelector(`.scout-chat-msg[data-work-run-id=\"${selectorEscape(workRunId)}\"]`)",
		"if (priorWork) priorWork.replaceWith(node)",
		"workStatus === 'complete' ? 'Delivered'",
		"workStatus === 'needs_attention' ? 'Needs attention'",
		"workStatus === 'approval_required' ? 'Needs approval'",
		"openArtifactStage(artifactId",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing evolving room-work behavior %q", want)
		}
	}
	if strings.Contains(html, "label.textContent = workRunId ? `${message?.workFamily}") {
		t.Fatal("room work card must use the human family fallback instead of dumping an unchecked wire value")
	}
}
