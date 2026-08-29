package main

import (
	"os"
	"strings"
	"testing"
)

func TestDesktopWorkActivityNeverInventsProgress(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)
	for _, forbidden := range []string{
		"status === 'queued' ? 12 : 35",
		"approvalRequired ? 82",
		"rejected || error ? 72",
		`class="scout-chat-research__flow"`,
		"const message = activeMessage || terminalMessage",
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("desktop work activity still invents progress with %q", forbidden)
		}
	}
	for _, required := range []string{
		"const workRunUnavailable = ref?.workRunRequired === true && !workRun",
		"const replayedActivity = workRun ? workRun.activity : []",
		"Activity is repairing from durable history. Provider-local status is hidden.",
		"data-research-preview",
		"data-research-provenance",
		"compactArtifactPreview(String(artifact?.text || ''))",
		"function openChatAttachmentSourceMenu(anchor, target = 'main')",
		"function chooseDriveFile()",
		"function attachDriveFileToComposer(file, target = 'main')",
		"'/assistant/attachments/from-file'",
		"attachment?.sourceId",
		"attachment?.sourceRevision",
		"function documentTokenAtCaret(input)",
		"function applyDocumentCompletion(file)",
		"Attach from Drive",
		"Scout interpretation",
		"Scout’s execution prompt",
		"Regenerate",
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("desktop work activity missing evidence-only progress guard %q", required)
		}
	}
}
