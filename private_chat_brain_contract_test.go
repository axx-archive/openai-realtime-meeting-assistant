package main

import (
	"strings"
	"testing"
	"time"
)

// The private-chat privacy contract, proven server-side (card kanban-card-103).
//
// A private Scout thread's messages are persisted as a single
// meetingMemoryKindScoutChat entry (the whole thread JSON, messages included).
// isUIStateMemoryKind classes that kind as UI/workspace state, so it is excluded
// from every recall, model-context, brain-summarizer, and client-timeline seam.
// In other words: private chats are NOT fed to the company brain. The only
// opt-in door is the card-082 "Feed the brain" guided intake, which is separate
// and disclosed.
//
// This test plants a canary token in a private thread through the exact seams
// the product uses (createScoutChatThread + commitScoutChatThreadMessages) and
// asserts the canary never surfaces at any of those seams. A POSITIVE CONTROL
// then proves the identical token IS findable once it rides a real room-chat
// transcript — so the negatives are the scout_chat_thread exclusion at work,
// not an unsearchable canary or a message that silently failed to save.
func TestPrivateChatBrainContract(t *testing.T) {
	const (
		owner  = "aj@shareability.com"
		canary = "zanzibarprivacycanary8842"
	)
	app := newIsolatedKanbanBoardApp(t)

	thread, err := app.createScoutChatThread(owner, "AJ", "Private notes", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic {
		t.Fatalf("thread visibility=%q, want private", thread.Visibility)
	}
	now := time.Now().UTC()
	msg := scoutChatMessageRecord{
		ID:          "scout-chat-message-canary",
		Kind:        "message",
		Role:        "user",
		Text:        canary + " keep this between me and Scout",
		CreatedAt:   now.Format(time.RFC3339Nano),
		AuthorEmail: owner,
	}
	if _, err := app.commitScoutChatThreadMessages(owner, thread.ID, msg); err != nil {
		t.Fatalf("commit private message: %v", err)
	}

	// INTEGRITY: the canary really is persisted (as a scout_chat_thread entry),
	// so the "returns nothing" assertions below prove the exclusion — not a
	// message that silently failed to save.
	storedCanary := false
	for _, entry := range app.memory.snapshot(0) {
		if strings.Contains(entry.Text, canary) {
			storedCanary = true
			if entry.Kind != meetingMemoryKindScoutChat {
				t.Fatalf("canary stored under kind %q, want %q", entry.Kind, meetingMemoryKindScoutChat)
			}
		}
	}
	if !storedCanary {
		t.Fatal("canary never reached the store — the seed failed; negatives below would be vacuous")
	}

	assertNoCanary := func(seam string, entries []meetingMemoryEntry) {
		t.Helper()
		for _, entry := range entries {
			if strings.Contains(entry.Text, canary) {
				t.Fatalf("PRIVACY LEAK: %s surfaced the private-chat canary (entry %s, kind %s)", seam, entry.ID, entry.Kind)
			}
		}
	}

	// (a) keyword recall never matches a private thread.
	if matches := app.memory.search(canary, 50); len(matches) != 0 {
		ids := make([]string, 0, len(matches))
		for _, m := range matches {
			ids = append(ids, m.Entry.ID+"/"+m.Entry.Kind)
		}
		t.Fatalf("PRIVACY LEAK: store.search matched the private-chat canary: %v", ids)
	}

	// (b) model-context assembly never carries the private thread.
	assertNoCanary("contextEntriesForQuery", app.memory.contextEntriesForQuery(canary, 50, now))

	// (c) the brain-summarizer window reads transcripts only — a private chat is
	// kind scout_chat_thread, so it is never queued for a brain write-up.
	assertNoCanary("unsummarizedTranscripts", app.memory.unsummarizedTranscripts(500))
	assertNoCanary("unconsumedEntriesAfter(transcript->brain)", app.memory.unconsumedEntriesAfter(meetingMemoryKindTranscript, meetingMemoryKindBrain, "throughTranscriptId", 500, ""))

	// (d) the client-visible meeting-memory timeline excludes it.
	assertNoCanary("visibleMeetingMemoryEntries", visibleMeetingMemoryEntries(app.memory.snapshot(0), 500))

	// (e) POSITIVE CONTROL: the identical token, pushed through a real room-chat
	// transcript, IS searchable — so every negative above is the exclusion, not
	// an unsearchable canary.
	if _, _, err := app.memory.appendRoomChatTranscript("canary-transcript-1", "AJ", canary+" said out loud in the room"); err != nil {
		t.Fatalf("positive-control transcript append: %v", err)
	}
	controlMatches := app.memory.search(canary, 50)
	foundTranscript := false
	for _, m := range controlMatches {
		if m.Entry.Kind == meetingMemoryKindScoutChat {
			t.Fatalf("PRIVACY LEAK: the private chat became searchable after the control append: %s", m.Entry.ID)
		}
		if m.Entry.Kind == meetingMemoryKindTranscript && strings.Contains(m.Entry.Text, canary) {
			foundTranscript = true
		}
	}
	if !foundTranscript {
		t.Fatalf("positive control failed: search did not find the canary transcript (matches=%d) — the negatives are not trustworthy", len(controlMatches))
	}
}
