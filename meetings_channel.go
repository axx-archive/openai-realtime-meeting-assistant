package main

// #meetings — the deployment's permanent recap channel.
// Founder decision (AJ, 2026-09-02): office-room recaps land in a dedicated
// #meetings channel rather than Bonfire Chat; both are pinned, ember, and can
// never be archived, deleted, renamed, or have members removed.
//
// Same shape as the Table (table_thread.go): ONE public Scout thread carrying
// a durable flag (System == "meetings") so it inherits every channel rule for
// free — the #-prefix, broadcast, @-mentions, read markers, mute. It is
// provisioned idempotently at boot, on the first thread-list load, and on the
// first office recap, and self-heals by adopting an EMPTY unflagged public
// channel that already carries the title (the failed-flag-write stub, never a
// channel people have posted in).

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	// meetingsChannelTitle is stored WITHOUT the "#" — clients add the prefix
	// when rendering a public thread.
	meetingsChannelTitle = "meetings"
	// scoutChatSystemMeetings is the System flag value on the record.
	scoutChatSystemMeetings = "meetings"
	// scoutChatPermanentChannelCopy is the honest refusal every mutation route
	// returns for the two pinned org channels.
	scoutChatPermanentChannelCopy = "Bonfire Chat and #meetings stay open for everyone"
)

// scoutChatThreadSystem returns the normalized System flag ("meetings") or "".
func scoutChatThreadSystem(thread scoutChatThreadRecord) string {
	return strings.ToLower(strings.TrimSpace(thread.System))
}

// scoutChatThreadIsPinnedSystem is true for the two permanent org channels:
// Bonfire Chat (the Table) and #meetings. They sort first, render in ember, and
// refuse archive / delete / rename / member removal.
func scoutChatThreadIsPinnedSystem(thread scoutChatThreadRecord) bool {
	return thread.Table || scoutChatThreadSystem(thread) != ""
}

func scoutChatThreadMetadataIsPinnedSystem(metadata map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(metadata["table"]), "true") || strings.TrimSpace(metadata["system"]) != ""
}

// ensureMeetingsMu serializes provisioning exactly like ensureTableMu — two
// concurrent first loads must converge on one #meetings, never two.
var ensureMeetingsMu sync.Mutex

// ensureMeetingsChannelForIndexEntries proves a healthy flagged #meetings from
// commit-time metadata alone (the body-free directory stays body-free); only
// missing or damaged states fall through to the decode/repair path.
func (app *kanbanBoardApp) ensureMeetingsChannelForIndexEntries(ownerEmail string, entries []meetingMemoryEntry) error {
	if app == nil || app.memory == nil {
		return fmt.Errorf("chat memory is unavailable")
	}
	for _, entry := range entries {
		if entry.Kind != meetingMemoryKindScoutChat {
			continue
		}
		metadata := entry.Metadata
		if strings.EqualFold(strings.TrimSpace(metadata["system"]), scoutChatSystemMeetings) &&
			strings.EqualFold(strings.TrimSpace(metadata["title"]), meetingsChannelTitle) &&
			strings.TrimSpace(metadata["archivedAt"]) == "" &&
			strings.TrimSpace(metadata["memberEmails"]) == "" &&
			normalizeScoutChatVisibility(metadata["visibility"]) == scoutChatVisibilityPublic {
			return nil
		}
	}
	_, err := app.ensureMeetingsChannel(ownerEmail)
	return err
}

// findMeetingsChannelThread returns the flagged #meetings, scanning every
// thread — it is shared, so "does it exist" is not a per-viewer question. An
// unflagged organization-public channel already titled "meetings" is adopted
// so a create whose flag write failed never mints a duplicate.
func (app *kanbanBoardApp) findMeetingsChannelThread() (scoutChatThreadRecord, bool) {
	if app == nil || app.memory == nil {
		return scoutChatThreadRecord{}, false
	}
	var adoptable scoutChatThreadRecord
	var foundAdoptable bool
	for _, entry := range app.memory.metadataSnapshot(0) {
		if entry.Kind != meetingMemoryKindScoutChat {
			continue
		}
		metadata := entry.Metadata
		flagged := strings.EqualFold(strings.TrimSpace(metadata["system"]), scoutChatSystemMeetings)
		adoptableTitle := strings.EqualFold(strings.TrimSpace(metadata["title"]), meetingsChannelTitle)
		adoptableAudience := strings.TrimSpace(metadata["archivedAt"]) == "" &&
			strings.TrimSpace(metadata["memberEmails"]) == "" &&
			strings.TrimSpace(metadata["table"]) != "true" &&
			normalizeScoutChatVisibility(metadata["visibility"]) == scoutChatVisibilityPublic
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
		if flagged || scoutChatThreadSystem(thread) == scoutChatSystemMeetings {
			return thread, true
		}
		// Adoption exists for exactly one bad state: a create that succeeded
		// but whose flag write did not — a stub with no messages. A channel
		// people have actually posted in is somebody else's channel, and
		// adopting it is a one-way door (flagAsMeetingsChannel rewrites the
		// title to lowercase "meetings", and rename/archive then refuse
		// forever). Leave it alone and mint a fresh #meetings alongside it.
		if thread.Table || len(thread.MemberEmails) > 0 || len(thread.Messages) > 0 {
			continue
		}
		adoptable = thread
		foundAdoptable = true
	}
	return adoptable, foundAdoptable
}

// flagAsMeetingsChannel persists System="meetings" (and the canonical title,
// unarchived) through updateScoutChatThread — never the append path, which
// silently no-ops on a known id (see flagAsTable).
func (app *kanbanBoardApp) flagAsMeetingsChannel(thread scoutChatThreadRecord) (scoutChatThreadRecord, error) {
	changed := scoutChatThreadSystem(thread) != scoutChatSystemMeetings || thread.Title != meetingsChannelTitle || thread.ArchivedAt != ""
	thread.System = scoutChatSystemMeetings
	thread.Title = meetingsChannelTitle
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
	_, ok, err := app.memory.updateScoutChatThread(thread.ID, entryText, scoutChatThreadMetadata(thread))
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	if !ok {
		return scoutChatThreadRecord{}, fmt.Errorf("could not flag thread %s as #meetings", thread.ID)
	}
	return thread, nil
}

// ensureMeetingsChannel returns #meetings, provisioning it on first use. The
// owner email is incidental (the thread is public); boot and the recap poster
// use the founder account so the channel exists before anyone opens Chat.
func (app *kanbanBoardApp) ensureMeetingsChannel(ownerEmail string) (scoutChatThreadRecord, error) {
	if app == nil || app.memory == nil {
		return scoutChatThreadRecord{}, fmt.Errorf("chat memory is unavailable")
	}
	ensureMeetingsMu.Lock()
	defer ensureMeetingsMu.Unlock()

	if existing, ok := app.findMeetingsChannelThread(); ok {
		return app.flagAsMeetingsChannel(existing)
	}
	ownerEmail = normalizeAccountEmail(ownerEmail)
	if ownerEmail == "" {
		ownerEmail = artifactLibraryAdminEmail
	}
	created, err := app.createScoutChatThread(ownerEmail, participantNameForEmail(ownerEmail), meetingsChannelTitle, scoutChatVisibilityPublic)
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	return app.flagAsMeetingsChannel(created)
}

// ensureMeetingsChannelAtBoot provisions #meetings before the first request so
// a fresh deployment's thread list already carries it. Fail-soft: a store that
// cannot write at boot logs and retries lazily on the next list or recap.
func (app *kanbanBoardApp) ensureMeetingsChannelAtBoot() {
	if app == nil || app.memory == nil {
		return
	}
	if _, err := app.ensureMeetingsChannel(artifactLibraryAdminEmail); err != nil {
		log.Errorf("#meetings channel was not provisioned at boot: %v", err)
	}
}
