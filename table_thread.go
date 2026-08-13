package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// The Table — the deployment's single permanent team thread.
// Design: docs/plans/the-table-design.md §15.
//
// It is deliberately NOT a new kind of object. It is one public Scout thread
// with a flag, which means it inherits channel semantics for free: the
// #-prefix, broadcast notification, and @-mention parsing all follow from
// `visibility: "public"` with no new code (scout_chat_threads.go:1620).

// tableThreadTitle is stored WITHOUT the "#" — clients add the prefix when
// rendering a public thread. legacyTableThreadTitle remains an adoption-only
// migration key so deployments with the former "team" record converge on one
// canonical permanent Bonfire Chat instead of minting a duplicate.
const (
	tableThreadTitle       = "Bonfire Chat"
	legacyTableThreadTitle = "team"
)

// ensureTableMu serializes provisioning. Two devices hitting the thread list at
// the same moment on a fresh deployment is exactly when this races, and two
// Tables would split the team permanently with no natural repair.
var ensureTableMu sync.Mutex

// ensureTableForIndex keeps the body-free Chat directory body-free. A healthy
// flagged Table can be proven entirely from commit-time metadata; only legacy,
// missing, or damaged states fall through to the exact decode/repair path.
func (app *kanbanBoardApp) ensureTableForIndex(ownerEmail string) error {
	return app.ensureTableForIndexEntries(ownerEmail, app.memory.metadataSnapshotOfKind(meetingMemoryKindScoutChat, 0))
}

func (app *kanbanBoardApp) ensureTableForIndexEntries(ownerEmail string, entries []meetingMemoryEntry) error {
	if app == nil || app.memory == nil {
		return fmt.Errorf("chat memory is unavailable")
	}
	for _, entry := range entries {
		if entry.Kind != meetingMemoryKindScoutChat {
			continue
		}
		metadata := entry.Metadata
		if strings.EqualFold(strings.TrimSpace(metadata["table"]), "true") &&
			strings.EqualFold(strings.TrimSpace(metadata["title"]), tableThreadTitle) &&
			strings.TrimSpace(metadata["archivedAt"]) == "" &&
			normalizeScoutChatVisibility(metadata["visibility"]) == scoutChatVisibilityPublic {
			return nil
		}
	}
	_, err := app.ensureTable(ownerEmail)
	return err
}

// findTableThread returns the flagged Table, scanning every thread rather than
// one viewer's visible set — the Table is shared, so "does it exist" is not a
// per-viewer question.
func (app *kanbanBoardApp) findTableThread() (scoutChatThreadRecord, bool) {
	if app == nil || app.memory == nil {
		return scoutChatThreadRecord{}, false
	}
	// Fall back to adopting an unflagged public thread that already carries the
	// Table's title. This self-heals the one bad state that could otherwise mint
	// a duplicate: a create that succeeded but whose flag write did not.
	var adoptable scoutChatThreadRecord
	var foundAdoptable bool

	for _, entry := range app.memory.metadataSnapshot(0) {
		if entry.Kind != meetingMemoryKindScoutChat {
			continue
		}
		metadata := entry.Metadata
		flagged := strings.EqualFold(strings.TrimSpace(metadata["table"]), "true")
		adoptableTitle := strings.EqualFold(strings.TrimSpace(metadata["title"]), tableThreadTitle) || strings.EqualFold(strings.TrimSpace(metadata["title"]), legacyTableThreadTitle)
		adoptableAudience := strings.TrimSpace(metadata["archivedAt"]) == "" && normalizeScoutChatVisibility(metadata["visibility"]) == scoutChatVisibilityPublic
		if !flagged && (!adoptableTitle || !adoptableAudience || foundAdoptable) {
			continue
		}
		fullEntry, exists := app.memory.entryByKindAndID(meetingMemoryKindScoutChat, entry.ID)
		if !exists {
			continue
		}
		thread, ok := decodeScoutChatThreadEntry(fullEntry)
		if !ok {
			continue
		}
		if flagged || thread.Table {
			return thread, true
		}
		adoptable = thread
		foundAdoptable = true
	}
	return adoptable, foundAdoptable
}

// flagAsTable persists Table=true on an existing thread.
//
// It must go through updateScoutChatThread, NOT appendScoutChatThread: the
// append path is append-only with dedup by id (memory.go:1399) and silently
// no-ops on an id it has already seen, returning appended=false rather than an
// error. Writing the flag with append looks like it worked and does nothing.
func (app *kanbanBoardApp) flagAsTable(thread scoutChatThreadRecord) (scoutChatThreadRecord, error) {
	changed := !thread.Table || thread.Title != tableThreadTitle || thread.ArchivedAt != ""
	thread.Table = true
	thread.Title = tableThreadTitle
	thread.ArchivedAt = ""
	if thread.Preview == "archived" {
		thread.Preview = scoutChatThreadPreview(thread)
	}
	if !changed {
		return thread, nil
	}
	thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	entryText, err := encodeScoutChatThread(thread)
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	updated, ok, err := app.memory.updateScoutChatThread(thread.ID, entryText, scoutChatThreadMetadata(thread))
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	if !ok {
		return scoutChatThreadRecord{}, fmt.Errorf("could not flag thread %s as the Table", thread.ID)
	}
	_ = updated
	return thread, nil
}

// ensureTable returns the Table, provisioning it on first use.
//
// Lazily provisioned rather than set up by an admin: a team chat that requires
// configuration before the first message does not get a first message. The
// caller's email becomes the record's owner, which is incidental — the thread
// is public, so ownership grants nothing here beyond authorship.
func (app *kanbanBoardApp) ensureTable(ownerEmail string) (scoutChatThreadRecord, error) {
	ensureTableMu.Lock()
	defer ensureTableMu.Unlock()

	if existing, ok := app.findTableThread(); ok {
		return app.flagAsTable(existing)
	}

	created, err := app.createScoutChatThread(
		ownerEmail,
		participantNameForEmail(ownerEmail),
		tableThreadTitle,
		scoutChatVisibilityPublic,
	)
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	return app.flagAsTable(created)
}
