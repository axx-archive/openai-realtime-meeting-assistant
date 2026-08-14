package main

import (
	"os"
	"strings"
	"testing"
)

// The server retains the signed correction protocol so historical Project
// associations can be reconciled safely, but ordinary chat no longer exposes
// manual Project selection or correction controls. Project/workstream
// placement is server-owned inference.
func TestManualProjectAttachmentAndCorrectionAreRetiredFromDesktopChat(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, forbidden := range []string{
		`id="homeProjectChip"`,
		`id="scoutChatProjectChip"`,
		`id="chatContextProjectChip"`,
		`project.dataset.projectCorrection = 'true'`,
		`Project: ${projectTitle}. Change project for this message`,
		`onProject: !generatedImage`,
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("retired manual Project affordance remains reachable: %q", forbidden)
		}
	}
	for _, want := range []string{
		`const explicitProjectAttachmentEnabled = false`,
		`const project = bfEl('span', 'scout-chat-msg__project'`,
		`onProject: null`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("server-owned Project inference contract missing %q", want)
		}
	}
}

func TestDesktopProjectCorrectionLivesOnResultingWork(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		`function workResultProjectPath(artifactId)`,
		`/artifacts/workstream?id=${encodeURIComponent(artifactId)}`,
		`function openWorkResultProjectCorrection(artifact, trigger)`,
		`correctProject.dataset.workProjectCorrection = 'true'`,
		`Correct this Work and future continuity. The source conversation stays unchanged.`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("result-side Work correction contract missing %q", want)
		}
	}
	if strings.Contains(html, `onProject: !generatedImage`) {
		t.Fatal("message composer/manual Project correction became reachable again")
	}
}
