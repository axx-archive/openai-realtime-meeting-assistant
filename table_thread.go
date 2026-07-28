package main

import (
	"fmt"
	"strings"
	"sync"
)

// The Table — the deployment's single permanent team thread.
// Design: docs/plans/the-table-design.md §15.
//
// It is deliberately NOT a new kind of object. It is one public Scout thread
// with a flag, which means it inherits channel semantics for free: the
// #-prefix, broadcast notification, and @-mention parsing all follow from
// `visibility: "public"` with no new code (scout_chat_threads.go:1620).

// tableThreadTitle is stored WITHOUT the "#" — clients add the prefix when
// rendering a public thread (see mobile ChannelList.channelName), so storing
// "#team" would render as "##team".
const tableThreadTitle = "team"

// ensureTableMu serializes provisioning. Two devices hitting the thread list at
// the same moment on a fresh deployment is exactly when this races, and two
// Tables would split the team permanently with no natural repair.
var ensureTableMu sync.Mutex

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

	for _, entry := range app.memory.snapshot(0) {
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok || thread.ArchivedAt != "" {
			continue
		}
		if thread.Table {
			return thread, true
		}
		if !foundAdoptable &&
			scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic &&
			strings.EqualFold(strings.TrimSpace(thread.Title), tableThreadTitle) {
			adoptable = thread
			foundAdoptable = true
		}
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
	thread.Table = true
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
		if existing.Table {
			return existing, nil
		}
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
