package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Wave 8 D12: the channel ingestion row follows the message — an edit
// supersedes the first-post row (recall returns the new text only), a delete
// tombstones it (recall returns nothing), and a private thread still never
// ingests.
func TestChannelIngestionFollowsEditAndDelete(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seeded aj account missing")
	}
	thread, _, err := app.ensureScoutChatThread("channel-ingest-edit", user.Email, "AJ", "General", scoutChatVisibilityPublic, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	original := scoutChatMessageRecord{ID: "msg-edit-1", Kind: "message", Role: "user", Text: "ALPHACANARY the packaging vendor is Zebra", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: user.Email}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, original); err != nil {
		t.Fatalf("commit: %v", err)
	}
	firstID := "channel-" + thread.ID + "-" + original.ID
	if rows := app.channelIngestionRows(thread.ID, original.ID); len(rows) != 1 || rows[0].ID != firstID {
		t.Fatalf("first-post ingestion rows=%v, want [%s]", memoryEntryIDs(rows), firstID)
	}
	if matches := app.memory.search("ALPHACANARY", 8); len(matches) != 1 {
		t.Fatalf("first-post text must be recallable: %d", len(matches))
	}

	// EDIT → the new text is the only live row; the old row is superseded.
	edited := "BETACANARY the packaging vendor is Acme"
	editedThread, editedMessage, err := app.editScoutChatThreadMessage(context.Background(), user, thread.ID, original.ID, &edited, nil)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if matches := app.memory.search("ALPHACANARY", 8); len(matches) != 0 {
		t.Fatalf("stale first-post text still recallable after edit: %v", memoryEntryIDs(matchEntries(matches)))
	}
	newMatches := app.memory.search("BETACANARY", 8)
	if len(newMatches) != 1 {
		t.Fatalf("edited text matches=%d, want exactly 1", len(newMatches))
	}
	newRow := newMatches[0].Entry
	if !strings.HasPrefix(newRow.ID, firstID+"-r") || newRow.Metadata["supersedesId"] != firstID || newRow.Metadata["messageId"] != original.ID || newRow.Metadata["editedAt"] == "" {
		t.Fatalf("re-filed row=%s metadata=%v, want a revision id superseding %s", newRow.ID, newRow.Metadata, firstID)
	}
	oldRow, found := app.memory.entryByID(firstID)
	if !found {
		t.Fatal("superseded row must never be hard-deleted")
	}
	if !memoryEntryHiddenFromRecall(oldRow) || oldRow.Metadata["correctionState"] != "superseded" || oldRow.Metadata["supersededBy"] != newRow.ID {
		t.Fatalf("old row must be hidden + superseded: %v", oldRow.Metadata)
	}
	if embeddingEligible(oldRow) {
		t.Fatal("superseded row must leave the embedding corpus")
	}
	for _, entry := range app.memory.eligibleEmbeddingEntriesSnapshot() {
		if entry.ID == firstID {
			t.Fatal("superseded row still in the embedding corpus snapshot")
		}
	}
	for _, entry := range app.memory.unsummarizedTranscripts(500) {
		if entry.ID == firstID {
			t.Fatal("superseded row still feeds the brain window")
		}
	}
	for _, entry := range app.memory.contextEntriesForQuery("packaging vendor", 50, time.Now()) {
		if strings.Contains(entry.Text, "ALPHACANARY") {
			t.Fatal("stale text entered model context")
		}
	}
	// idempotent: replaying the same edit files nothing new.
	app.supersedeChannelMessageIngestion(editedThread, editedMessage)
	if rows := app.channelIngestionRows(thread.ID, original.ID); len(rows) != 1 || rows[0].ID != newRow.ID {
		t.Fatalf("replayed edit changed the live rows: %v", memoryEntryIDs(rows))
	}
	// a second edit supersedes the revision row too.
	again := "GAMMACANARY the packaging vendor is Bolt"
	if _, _, err := app.editScoutChatThreadMessage(context.Background(), user, thread.ID, original.ID, &again, nil); err != nil {
		t.Fatalf("second edit: %v", err)
	}
	if rows := app.channelIngestionRows(thread.ID, original.ID); len(rows) != 1 || rows[0].Metadata["supersedesId"] != newRow.ID {
		t.Fatalf("second edit rows=%v, want one row superseding %s", memoryEntryIDs(rows), newRow.ID)
	}
	if matches := app.memory.search("BETACANARY", 8); len(matches) != 0 {
		t.Fatal("first revision still recallable after the second edit")
	}

	// DELETE → tombstone: nothing recallable for the message, fact survives.
	if _, err := app.deleteScoutChatThreadMessage(user.Email, thread.ID, original.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if matches := app.memory.search("GAMMACANARY", 8); len(matches) != 0 {
		t.Fatalf("deleted message still recallable: %v", memoryEntryIDs(matchEntries(matches)))
	}
	if rows := app.channelIngestionRows(thread.ID, original.ID); len(rows) != 0 {
		t.Fatalf("deleted message still has live ingestion rows: %v", memoryEntryIDs(rows))
	}
	tombstoned := 0
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindTranscript, 0) {
		if entry.Metadata["messageId"] != original.ID {
			continue
		}
		if !memoryEntryHiddenFromRecall(entry) {
			t.Fatalf("row %s still visible after delete", entry.ID)
		}
		if entry.Metadata["correctionState"] == "deleted" {
			tombstoned++
			if strings.Contains(entry.Text, "GAMMACANARY") || !strings.HasPrefix(entry.Text, "Channel message deleted by") {
				t.Fatalf("tombstone kept the deleted text: %q", entry.Text)
			}
		}
	}
	if tombstoned != 1 {
		t.Fatalf("tombstoned rows=%d, want the one live row", tombstoned)
	}

	// PRIVATE thread: edits never ingest.
	private, err := app.createScoutChatThread(user.Email, "AJ", "Private notes", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("private thread: %v", err)
	}
	privateMessage := scoutChatMessageRecord{ID: "msg-private-1", Kind: "message", Role: "user", Text: "DELTACANARY keep this private", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorEmail: user.Email}
	if _, err := app.commitScoutChatThreadMessages(user.Email, private.ID, privateMessage); err != nil {
		t.Fatalf("private commit: %v", err)
	}
	privateEdit := "DELTACANARY still private after edit"
	if _, _, err := app.editScoutChatThreadMessage(context.Background(), user, private.ID, privateMessage.ID, &privateEdit, nil); err != nil {
		t.Fatalf("private edit: %v", err)
	}
	if matches := app.memory.search("DELTACANARY", 8); len(matches) != 0 {
		t.Fatalf("PRIVACY LEAK: private edit ingested: %v", memoryEntryIDs(matchEntries(matches)))
	}
}

func matchEntries(matches []meetingMemoryMatch) []meetingMemoryEntry {
	entries := make([]meetingMemoryEntry, 0, len(matches))
	for _, match := range matches {
		entries = append(entries, match.Entry)
	}
	return entries
}

// Wave 8 D12 follow-up: rows frozen at first-post text before the hooks
// existed converge at boot — an edited message's row is superseded with the
// current text, a deleted message's row is tombstoned, and a second run is a
// no-op.
func TestBackfillChannelIngestionDrift(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	thread, _, err := app.ensureScoutChatThread("channel-ingest-backfill", user.Email, "AJ", "Ops", scoutChatVisibilityPublic, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	now := time.Now().UTC()
	edited := scoutChatMessageRecord{ID: "msg-frozen-edit", Kind: "message", Role: "user", Text: "FROZENALPHA the audit starts Monday", CreatedAt: now.Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: user.Email}
	deleted := scoutChatMessageRecord{ID: "msg-frozen-delete", Kind: "message", Role: "user", Text: "FROZENBETA remove me later", CreatedAt: now.Add(time.Second).Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: user.Email}
	kept := scoutChatMessageRecord{ID: "msg-frozen-kept", Kind: "message", Role: "user", Text: "FROZENGAMMA untouched", CreatedAt: now.Add(2 * time.Second).Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: user.Email}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, edited, deleted, kept); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// Simulate pre-hook drift: mutate the thread record directly (no hooks).
	current, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		t.Fatalf("load thread: %v", err)
	}
	index := scoutChatMessageIndex(current, edited.ID)
	current.Messages[index].Text = "FROZENDELTA the audit starts Tuesday"
	current.Messages[index].EditedAt = now.Add(time.Minute).Format(time.RFC3339Nano)
	deleteIndex := scoutChatMessageIndex(current, deleted.ID)
	current.Messages = append(current.Messages[:deleteIndex], current.Messages[deleteIndex+1:]...)
	if err := app.saveScoutChatThread(current); err != nil {
		t.Fatalf("save drifted thread: %v", err)
	}
	if matches := app.memory.search("FROZENALPHA", 8); len(matches) != 1 {
		t.Fatalf("fixture must start frozen at first-post text: %d", len(matches))
	}

	superseded, tombstoned, skipped := app.backfillChannelIngestionDrift()
	if superseded != 1 || tombstoned != 1 || skipped != 0 {
		t.Fatalf("backfill counts superseded=%d tombstoned=%d skipped=%d, want 1/1/0", superseded, tombstoned, skipped)
	}
	if matches := app.memory.search("FROZENALPHA", 8); len(matches) != 0 {
		t.Fatalf("frozen first-post text still recallable: %v", memoryEntryIDs(matchEntries(matches)))
	}
	if matches := app.memory.search("FROZENDELTA", 8); len(matches) != 1 || matches[0].Entry.Metadata["messageId"] != edited.ID {
		t.Fatalf("current text matches=%v, want the re-filed row", memoryEntryIDs(matchEntries(matches)))
	}
	if matches := app.memory.search("FROZENBETA", 8); len(matches) != 0 {
		t.Fatalf("deleted message still recallable: %v", memoryEntryIDs(matchEntries(matches)))
	}
	if matches := app.memory.search("FROZENGAMMA", 8); len(matches) != 1 {
		t.Fatalf("untouched row must survive: %d", len(matches))
	}
	if rows := app.channelIngestionRows(thread.ID, edited.ID); len(rows) != 1 || rows[0].Metadata["supersedesId"] != "channel-"+thread.ID+"-"+edited.ID {
		t.Fatalf("re-filed row=%v, want one row superseding the frozen id", memoryEntryIDs(rows))
	}
	// second run: nothing left to do.
	if s2, t2, k2 := app.backfillChannelIngestionDrift(); s2 != 0 || t2 != 0 || k2 != 0 {
		t.Fatalf("second backfill run was not a no-op: %d/%d/%d", s2, t2, k2)
	}
	// private threads are never touched even when a row exists for them.
	private, err := app.createScoutChatThread(user.Email, "AJ", "Private", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("private: %v", err)
	}
	if _, _, err := app.memory.appendAttributedTranscriptEntry(officeRoomID, "channel-"+private.ID+"-ghost", "", "AJ", "", "FROZENEPSILON stray private row", map[string]string{"source": transcriptSourceChannel, "threadId": private.ID, "messageId": "ghost", "visibility": "private", "ownerEmail": user.Email}, true, ""); err != nil {
		t.Fatalf("stray row: %v", err)
	}
	if s3, t3, k3 := app.backfillChannelIngestionDrift(); s3 != 0 || t3 != 0 || k3 != 1 {
		t.Fatalf("private thread row must be skipped, got %d/%d/%d", s3, t3, k3)
	}
}
