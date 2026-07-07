package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// countGmailConsentBroadcasts returns how many broadcast (UserEmail=="")
// notifications point at the fixed Gmail-consent artifact, and asserts each is
// kind=task. It reads under the app lock like every other notification path.
func countGmailConsentBroadcasts(t *testing.T, app *kanbanBoardApp) int {
	t.Helper()
	app.mu.Lock()
	defer app.mu.Unlock()
	count := 0
	for _, record := range app.notifications {
		if record.ArtifactID != gmailConsentProposalArtifactID {
			continue
		}
		if record.UserEmail != "" {
			t.Fatalf("Gmail consent notification targeted %q, want a broadcast (empty UserEmail)", record.UserEmail)
		}
		if record.Kind != notificationKindTask {
			t.Fatalf("Gmail consent notification kind=%q, want %q", record.Kind, notificationKindTask)
		}
		count++
	}
	return count
}

func TestSeedGmailConsentProposalCreatesArtifactAndNotification(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("NOTIFICATIONS_PATH", filepath.Join(t.TempDir(), "notifications.json"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	seedGmailConsentProposal(app)

	artifact, ok := app.osArtifactByID(gmailConsentProposalArtifactID)
	if !ok {
		t.Fatalf("artifact %q not found after seed", gmailConsentProposalArtifactID)
	}
	if got := artifact.Metadata["type"]; got != artifactTypeMarkdown {
		t.Fatalf("artifact type=%q, want %q", got, artifactTypeMarkdown)
	}
	if got := artifact.Metadata["mode"]; got != "research" {
		t.Fatalf("artifact mode=%q, want research", got)
	}
	if !artifactIsPublished(artifact) {
		t.Fatalf("artifact metadata=%v, want published", artifact.Metadata)
	}
	if !strings.Contains(artifact.Text, "Decision:") {
		t.Fatalf("artifact body missing ratification Decision sentence")
	}

	// The published artifact must reach the data-room surface.
	published := app.publishedOSArtifactsSnapshot(0)
	foundPublished := false
	for _, entry := range published {
		if entry.ID == gmailConsentProposalArtifactID {
			foundPublished = true
			break
		}
	}
	if !foundPublished {
		t.Fatalf("artifact %q not in publishedOSArtifactsSnapshot", gmailConsentProposalArtifactID)
	}

	if count := countGmailConsentBroadcasts(t, app); count != 1 {
		t.Fatalf("broadcast notifications for the proposal=%d, want exactly 1", count)
	}

	// The broadcast must be visible (and deep-link) to any signed-in member.
	viewer := app.notificationsForUser("aj@shareability.com", 0)
	seen := false
	for _, note := range viewer {
		if note["artifactId"] == gmailConsentProposalArtifactID {
			seen = true
			if note["kind"] != notificationKindTask {
				t.Fatalf("viewer notification kind=%v, want task", note["kind"])
			}
		}
	}
	if !seen {
		t.Fatalf("proposal broadcast not visible to a signed-in member")
	}
}

func TestSeedGmailConsentProposalIdempotentAcrossRestarts(t *testing.T) {
	memoryPath := filepath.Join(t.TempDir(), "memory.jsonl")
	notificationsPath := filepath.Join(t.TempDir(), "notifications.json")
	boardPath := filepath.Join(t.TempDir(), "board.json")
	t.Setenv("MEETING_MEMORY_PATH", memoryPath)
	t.Setenv("NOTIFICATIONS_PATH", notificationsPath)
	t.Setenv("KANBAN_BOARD_PATH", boardPath)

	// First boot mints the artifact + broadcast.
	first := newKanbanBoardApp()
	seedGmailConsentProposal(first)

	// Reboot: a fresh app over the SAME files reloads the seen-set and the
	// notification store, so the seed appends nothing and broadcasts nothing.
	second := newKanbanBoardApp()
	seedGmailConsentProposal(second)

	artifacts := second.osArtifactsSnapshot(0)
	artifactCount := 0
	for _, entry := range artifacts {
		if entry.ID == gmailConsentProposalArtifactID {
			artifactCount++
		}
	}
	if artifactCount != 1 {
		t.Fatalf("os_artifact entries for the proposal=%d after restart, want exactly 1", artifactCount)
	}

	if count := countGmailConsentBroadcasts(t, second); count != 1 {
		t.Fatalf("broadcast notifications for the proposal=%d after restart, want exactly 1", count)
	}
}

func TestGmailConsentProposalBodyCarriesTheContract(t *testing.T) {
	body := gmailConsentProposalBody
	for _, needle := range []string{
		"gmail.metadata",
		"contacts.readonly",
		"gmail.readonly",
		"Retention",
		"Disconnect",
		"Decision:",
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("gmailConsentProposalBody missing required content %q", needle)
		}
	}
}
