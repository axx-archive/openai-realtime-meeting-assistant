package main

import (
	"strings"
	"testing"
)

// TestBrainIntakeDeprecated verifies that the seven-step guided intake flow
// is deprecated. Per the product doctrine: "The seven-step agency interview
// must not remain the intake path. Channels and Riffs are the intake."
func TestBrainIntakeDeprecated(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}

	// startBrainIntakeThread should return an error directing users to channels
	_, err := app.startBrainIntakeThread(user)
	if err == nil {
		t.Fatal("startBrainIntakeThread should return error (intake is deprecated)")
	}
	if !strings.Contains(err.Error(), "channel") {
		t.Fatalf("deprecation error should mention channels: %v", err)
	}
}

// TestChannelBrainIngestionReplacesIntake verifies that channel messages
// are now the brain intake path. This is the replacement for the deprecated
// seven-step interview.
func TestChannelBrainIngestionReplacesIntake(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)

	const channelCanary = "CHANNEL-INTAKE-REPLACEMENT-7788"

	// Create a channel
	channel, created, err := app.ensureScoutChatThread(
		"channel-intake-test",
		"aj@shareability.com",
		"AJ",
		"Company Knowledge",
		scoutChatVisibilityPublic,
		nil,
	)
	if err != nil || !created {
		t.Fatalf("create channel: created=%v err=%v", created, err)
	}

	// Post knowledge to the channel - this is the new intake path
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", channel.ID, scoutChatMessageRecord{
		ID: "company-knowledge", Kind: "message", Role: "user",
		Text:        channelCanary + " — company knowledge for the brain",
		AuthorName:  "AJ",
		AuthorEmail: "aj@shareability.com",
	}); err != nil {
		t.Fatal(err)
	}

	// Verify the channel message created a transcript entry (brain intake)
	transcripts := app.memory.unsummarizedTranscripts(500)
	found := false
	for _, entry := range transcripts {
		if entry.Kind == meetingMemoryKindTranscript && strings.Contains(entry.Text, channelCanary) {
			found = true
			if entry.Metadata["source"] != transcriptSourceChannel {
				t.Fatalf("channel intake should have source=channel: %+v", entry.Metadata)
			}
		}
	}
	if !found {
		t.Fatal("channel message did not create transcript entry (brain not fed)")
	}
}

// TestChannelBrainIngestionPinnedToOfficeRoom verifies that channel brain
// entries are pinned to the office room (same as the old brain intake was).
func TestChannelBrainIngestionPinnedToOfficeRoom(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)

	const canary = "OFFICE-PINNED-CANARY-9911"

	channel, _, err := app.ensureScoutChatThread(
		"office-pin-test",
		"aj@shareability.com",
		"AJ",
		"Test Channel",
		scoutChatVisibilityPublic,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", channel.ID, scoutChatMessageRecord{
		ID: "office-test", Kind: "message", Role: "user",
		Text:        canary,
		AuthorName:  "AJ",
		AuthorEmail: "aj@shareability.com",
	}); err != nil {
		t.Fatal(err)
	}

	// Verify the transcript is pinned to office room
	for _, entry := range app.memory.unsummarizedTranscripts(500) {
		if strings.Contains(entry.Text, canary) {
			roomID := entry.Metadata["roomId"]
			if roomID != officeRoomID && roomID != "" {
				t.Fatalf("channel brain entry pinned to wrong room: %s", roomID)
			}
			return
		}
	}
	t.Fatal("channel brain entry not found")
}
