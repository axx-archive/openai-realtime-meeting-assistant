package main

// Channel brain ingestion — automatic brain feeding from channels and Riffs.
//
// Per the product doctrine:
// - Private channels (public visibility + member restrictions) feed the brain.
// - Channel-tied Riffs feed the brain.
// - 1:1 Scout chats stay owner-only and OUT of the brain.
// - Non-members must not see channel clutter in the IA (visibility filtering).
//
// This replaces the legacy seven-step "Feed the brain" interview. Channels
// and Riffs are now the primary intake paths for company knowledge.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	transcriptSourceChannel = "channel"
	transcriptSourceRiff    = "riff"
)

// channelThreadShouldFeedBrain returns true if a channel's messages should
// automatically create transcript entries for brain synthesis.
//
// Rules:
// - Public visibility channels (including project channels) feed the brain
// - Private visibility (1:1 Scout) never feeds the brain
// - Archived threads never feed the brain
func channelThreadShouldFeedBrain(thread scoutChatThreadRecord) bool {
	if thread.ArchivedAt != "" {
		return false
	}
	if scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
		return false
	}
	return true
}

// riffThreadShouldFeedBrain returns true if a Riff's messages should
// automatically create transcript entries for brain synthesis.
//
// A Riff feeds the brain if it's bound to a channel that feeds the brain.
func riffThreadShouldFeedBrain(thread scoutChatThreadRecord) bool {
	if thread.Riff == nil {
		return false
	}
	if thread.ArchivedAt != "" {
		return false
	}
	return true
}

// channelBrainMetadata returns the metadata for a channel message transcript.
// The visibility is set to "project" for member-restricted channels, enabling
// non-member exclusion in recall while still feeding the brain.
func channelBrainMetadata(thread scoutChatThreadRecord, message scoutChatMessageRecord) map[string]string {
	meta := map[string]string{
		"source":   transcriptSourceChannel,
		"threadId": thread.ID,
	}

	if title := strings.TrimSpace(thread.Title); title != "" {
		meta["channelTitle"] = title
	}

	if messageID := strings.TrimSpace(message.ID); messageID != "" {
		meta["messageId"] = messageID
	}

	// Set visibility based on channel membership
	members := scoutChatThreadMemberEmails(thread)
	if len(members) > 0 {
		// Project channel with member restrictions
		meta["visibility"] = "project"
		meta["ownerEmail"] = thread.OwnerEmail
		meta["memberEmails"] = strings.Join(members, ",")
	} else {
		// Organization-public channel
		meta["visibility"] = "organization"
	}

	return meta
}

// channelThreadRecallFence is the recall fence a thread's CURRENT state
// implies for anything derived from it (a channel digest, a remembered note):
// the same vocabulary channelBrainMetadata / riffBrainMetadata stamp on
// ingestion rows, computed from the thread record at the time of use instead
// of copied from whichever old row happened to be first in a window.
func channelThreadRecallFence(thread scoutChatThreadRecord) map[string]string {
	owner := normalizeAccountEmail(thread.OwnerEmail)
	if thread.Riff != nil {
		return map[string]string{"visibility": "project", "ownerEmail": owner, "memberEmails": owner}
	}
	if scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
		return map[string]string{"visibility": "private", "ownerEmail": owner}
	}
	if members := scoutChatThreadMemberEmails(thread); len(members) > 0 {
		return map[string]string{"visibility": "project", "ownerEmail": owner, "memberEmails": strings.Join(members, ",")}
	}
	return map[string]string{"visibility": "organization"}
}

// riffBrainMetadata returns the metadata for a Riff message transcript.
// Riffs always have project visibility scoped to the owner only.
func riffBrainMetadata(thread scoutChatThreadRecord, message scoutChatMessageRecord) map[string]string {
	meta := map[string]string{
		"source":     transcriptSourceRiff,
		"threadId":   thread.ID,
		"visibility": "project",
		"ownerEmail": thread.OwnerEmail,
	}

	if thread.Riff != nil {
		meta["sourceThreadId"] = thread.Riff.SourceThreadID
		meta["sourceTitle"] = thread.Riff.SourceTitle
		// Only the Riff owner can see Riff content in the brain
		meta["memberEmails"] = normalizeAccountEmail(thread.OwnerEmail)
	}

	if messageID := strings.TrimSpace(message.ID); messageID != "" {
		meta["messageId"] = messageID
	}

	return meta
}

// channelBrainTranscriptBody is the one canonical source-body constructor for
// both durable ingestion and the post-provider current-source reauthorization
// boundary. Speaker attribution is applied by appendAttributedTranscriptEntry;
// callers reconstructing the stored bytes must pass this body through
// formatSpeakerTranscript with the same message author.
func channelBrainTranscriptBody(thread scoutChatThreadRecord, source, text string) string {
	text = strings.TrimSpace(text)
	var bodyBuilder strings.Builder
	if source == transcriptSourceChannel {
		if title := strings.TrimSpace(thread.Title); title != "" {
			fmt.Fprintf(&bodyBuilder, "[#%s] ", title)
		}
	} else if source == transcriptSourceRiff && thread.Riff != nil {
		if title := strings.TrimSpace(thread.Riff.SourceTitle); title != "" {
			fmt.Fprintf(&bodyBuilder, "[Riff on #%s] ", title)
		}
	}
	bodyBuilder.WriteString(text)
	return bodyBuilder.String()
}

// fileChannelMessageAsBrainTranscript creates a transcript entry from a
// channel or Riff message. This enables the brain worker to synthesize
// channel knowledge into brain entries.
func (app *kanbanBoardApp) fileChannelMessageAsBrainTranscript(thread scoutChatThreadRecord, message scoutChatMessageRecord) error {
	return app.fileChannelMessageAsBrainTranscriptSuperseding(thread, message, "")
}

// fileChannelMessageAsBrainTranscriptSuperseding is the filer with the
// supersession pointer (Wave 8 D12): a re-file after an edit stamps
// supersedesId = the prior live row, the same convention Meeting Record
// corrections use, so readers can walk the revision chain.
func (app *kanbanBoardApp) fileChannelMessageAsBrainTranscriptSuperseding(thread scoutChatThreadRecord, message scoutChatMessageRecord, supersedesID string) error {
	if app == nil || app.memory == nil {
		return nil
	}

	// Only file user messages (not Scout responses)
	if message.Role != "user" {
		return nil
	}

	text := strings.TrimSpace(message.Text)
	if text == "" {
		return nil
	}

	var meta map[string]string
	var source string

	if thread.Riff != nil && riffThreadShouldFeedBrain(thread) {
		meta = riffBrainMetadata(thread, message)
		source = transcriptSourceRiff
	} else if channelThreadShouldFeedBrain(thread) {
		meta = channelBrainMetadata(thread, message)
		source = transcriptSourceChannel
	} else {
		// This thread type should not feed the brain
		return nil
	}

	// Build the speaker from author info
	speaker := scoutChatAuthorName(&userAccount{Email: message.AuthorEmail, Name: message.AuthorName})

	// Create a unique event ID based on the message. An EDITED message gets a
	// revision-suffixed id (Wave 8 D12): the first-post row keeps its id, the
	// re-filed row is its own durable event, and the store seen-map still
	// dedupes a retried edit (same EditedAt → same id).
	eventID := channelIngestionEventID(source, thread, message)
	if edited := strings.TrimSpace(message.EditedAt); edited != "" {
		meta["editedAt"] = edited
	}
	if supersedesID = strings.TrimSpace(supersedesID); supersedesID != "" {
		meta["supersedesId"] = supersedesID
		meta["correctionState"] = "corrected"
	}

	// File the transcript entry (pinned to office room like brain intake)
	_, _, err := app.memory.appendAttributedTranscriptEntry(
		officeRoomID,
		eventID,
		"", // itemID
		speaker,
		"", // speakerType
		channelBrainTranscriptBody(thread, source, text),
		meta,
		true, // bypass usefulness filter
		"",   // expectedMeetingID
	)

	return err
}

// channelIngestionEventID mints the ingestion row id: the first-post id is
// `<source>-<thread>-<message>` (unchanged on disk for every existing row); a
// re-file after an edit appends `-r<8 hex of EditedAt>` so each edit revision
// is its own row and a retried edit still dedupes through the seen-map.
func channelIngestionEventID(source string, thread scoutChatThreadRecord, message scoutChatMessageRecord) string {
	base := fmt.Sprintf("%s-%s-%s", source, thread.ID, message.ID)
	edited := strings.TrimSpace(message.EditedAt)
	if edited == "" {
		return base
	}
	sum := sha256.Sum256([]byte(edited))
	return base + "-r" + hex.EncodeToString(sum[:])[:8]
}

// channelIngestionRows returns the LIVE (recall-visible) ingestion rows for one
// channel/riff message — normally exactly one; more only mid-supersession.
func (app *kanbanBoardApp) channelIngestionRows(threadID string, messageID string) []meetingMemoryEntry {
	if app == nil || app.memory == nil {
		return nil
	}
	return channelIngestionRowsFrom(app.memory.entriesOfKind(meetingMemoryKindTranscript, 0), threadID, messageID)
}

// channelIngestionRowsFrom is the pure row filter over an already-cloned
// transcript corpus, so a batch caller (the boot backfill) scans the corpus
// once instead of re-cloning it per drifted message.
func channelIngestionRowsFrom(corpus []meetingMemoryEntry, threadID string, messageID string) []meetingMemoryEntry {
	threadID, messageID = strings.TrimSpace(threadID), strings.TrimSpace(messageID)
	if threadID == "" || messageID == "" {
		return nil
	}
	rows := make([]meetingMemoryEntry, 0, 1)
	for _, entry := range corpus {
		if !channelSourcedTranscript(entry) || memoryEntryHiddenFromRecall(entry) {
			continue
		}
		if strings.TrimSpace(entry.Metadata["threadId"]) != threadID || strings.TrimSpace(entry.Metadata["messageId"]) != messageID {
			continue
		}
		rows = append(rows, entry)
	}
	return rows
}

// supersedeChannelMessageIngestion (Wave 8 D12) keeps the ingestion row
// current after a message EDIT: the new text is re-filed through the same
// fileChannelMessageAsBrainTranscript path under a revision id, then every
// prior live row for the message is expired with correctionState=superseded +
// supersededBy — hidden from search, context, the brain window, the embedding
// corpus, and Meeting Record segments, but never hard-deleted. Idempotent: a
// retried edit dedupes the re-file and re-expiring is a no-op. A thread that no
// longer feeds the brain (private, archived) has no rows and files none.
func (app *kanbanBoardApp) supersedeChannelMessageIngestion(thread scoutChatThreadRecord, message scoutChatMessageRecord) {
	if app == nil || app.memory == nil || message.Role != "user" {
		return
	}
	app.supersedeChannelMessageIngestionRows(thread, message, app.channelIngestionRows(thread.ID, message.ID))
}

// supersedeChannelMessageIngestionRows is supersedeChannelMessageIngestion
// over the message's live rows the caller already looked up (the boot
// backfill passes rows from its single corpus scan).
func (app *kanbanBoardApp) supersedeChannelMessageIngestionRows(thread scoutChatThreadRecord, message scoutChatMessageRecord, priors []meetingMemoryEntry) {
	if app == nil || app.memory == nil || message.Role != "user" {
		return
	}
	newText := normalizeMemoryText(canonicalizeDomainTerms(strings.TrimSpace(message.Text)))
	if newText == "" {
		// An edit that emptied the text reads as a delete for recall.
		app.tombstoneChannelMessageIngestionRows(thread, message.ID, message.AuthorEmail, priors)
		return
	}
	if len(priors) == 1 && strings.TrimSpace(message.EditedAt) != "" {
		// attachments-only edit: the body did not change, keep the row.
		source := strings.ToLower(strings.TrimSpace(priors[0].Metadata["source"]))
		if meetingRecordTranscriptText(priors[0]) == normalizeMemoryText(canonicalizeDomainTerms(channelBrainTranscriptBody(thread, source, message.Text))) {
			return
		}
	}
	supersedesID := ""
	if len(priors) > 0 {
		supersedesID = priors[len(priors)-1].ID
	}
	if err := app.fileChannelMessageAsBrainTranscriptSuperseding(thread, message, supersedesID); err != nil {
		log.Errorf("Failed to re-file edited channel message %s as brain transcript: %v", message.ID, err)
		return
	}
	source := transcriptSourceChannel
	if thread.Riff != nil {
		source = transcriptSourceRiff
	}
	newID := channelIngestionEventID(source, thread, message)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	superseded := 0
	for _, prior := range priors {
		if prior.ID == newID {
			continue
		}
		updates := map[string]string{
			relevanceMetadataKey: relevanceExpired,
			"correctionState":    "superseded",
			"supersededBy":       newID,
			"supersededAt":       now,
		}
		if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindTranscript, prior.ID, prior.Text, updates); err != nil {
			log.Errorf("Failed to supersede channel ingestion row %s: %v", prior.ID, err)
			continue
		}
		superseded++
	}
	if superseded > 0 {
		// The thread's cumulative digest folded the PRE-edit text and would
		// carry it forward on every rebuild; invalidate it so the next pass
		// rebuilds from live rows only.
		app.invalidateChannelDigest(thread.ID, "message_edited")
	}
}

// tombstoneChannelMessageIngestion (Wave 8 D12) hides every live ingestion row
// of a DELETED message: the body is replaced by a dated stub and the row is
// expired with correctionState=deleted, so no recall lane can ever return the
// deleted text while the FACT of the deletion survives on the row.
func (app *kanbanBoardApp) tombstoneChannelMessageIngestion(thread scoutChatThreadRecord, messageID string, deleterEmail string) {
	if app == nil || app.memory == nil {
		return
	}
	app.tombstoneChannelMessageIngestionRows(thread, messageID, deleterEmail, app.channelIngestionRows(thread.ID, messageID))
}

// tombstoneChannelMessageIngestionRows is tombstoneChannelMessageIngestion
// over rows the caller already looked up.
func (app *kanbanBoardApp) tombstoneChannelMessageIngestionRows(thread scoutChatThreadRecord, messageID string, deleterEmail string, rows []meetingMemoryEntry) {
	if app == nil || app.memory == nil {
		return
	}
	deleter := firstNonEmptyString(participantNameForEmail(deleterEmail), normalizeAccountEmail(deleterEmail), "a member")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tombstoned := 0
	for _, row := range rows {
		updates := map[string]string{
			relevanceMetadataKey: relevanceExpired,
			"correctionState":    "deleted",
			"deletedAt":          now,
			"deletedBy":          deleter,
			"tombstone":          "true",
		}
		if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindTranscript, row.ID, "Channel message deleted by "+deleter, updates); err != nil {
			log.Errorf("Failed to tombstone channel ingestion row %s: %v", row.ID, err)
			continue
		}
		tombstoned++
	}
	if tombstoned > 0 {
		// The cumulative digest still holds the deleted text (and would carry
		// it forward forever); invalidate it so the next pass rebuilds from
		// live rows only.
		app.invalidateChannelDigest(thread.ID, "message_deleted")
	}
}

// fileChannelMessagesAsBrainTranscripts files multiple messages as transcripts.
func (app *kanbanBoardApp) fileChannelMessagesAsBrainTranscripts(thread scoutChatThreadRecord, messages []scoutChatMessageRecord) {
	for _, message := range messages {
		if err := app.fileChannelMessageAsBrainTranscript(thread, message); err != nil {
			log.Errorf("Failed to file channel message as brain transcript: %v", err)
		}
	}
}

// channelIngestionBackfillRowCap bounds one boot's drift backfill so a large
// store never turns boot into a full rewrite storm; the remainder is picked up
// on the next boot (idempotent: converged rows are skipped in O(1)).
const channelIngestionBackfillRowCap = 2000

// backfillChannelIngestionDrift (Wave 8 D12 follow-up) reconciles ingestion
// rows frozen at first-post text before the edit/delete hooks existed. For
// every LIVE channel/riff transcript row it recomputes the text the current
// message would file and, when the stored text drifted, re-files it through
// supersedeChannelMessageIngestion; a row whose message no longer exists is
// tombstoned. Private threads are never touched (their rows should not exist;
// if one does, it is left alone and counted as skipped). Keyless-safe: pure
// store work, no model call. Runs once after the memory store loads.
func (app *kanbanBoardApp) backfillChannelIngestionDrift() (superseded int, tombstoned int, skipped int) {
	if app == nil || app.memory == nil {
		return 0, 0, 0
	}
	type messageKey struct{ threadID, messageID string }
	seen := map[messageKey]struct{}{}
	threads := map[string]scoutChatThreadRecord{}
	threadMissing := map[string]bool{}
	examined := 0
	// ONE corpus scan for the whole backfill: the per-message live rows are
	// indexed from this clone so supersede/tombstone never re-clone the
	// transcript corpus per drifted row (boot cost is one scan, not N).
	rows := app.memory.entriesOfKind(meetingMemoryKindTranscript, 0)
	liveRows := map[messageKey][]meetingMemoryEntry{}
	for _, row := range rows {
		if !channelSourcedTranscript(row) || memoryEntryHiddenFromRecall(row) {
			continue
		}
		key := messageKey{strings.TrimSpace(row.Metadata["threadId"]), strings.TrimSpace(row.Metadata["messageId"])}
		liveRows[key] = append(liveRows[key], row)
	}
	// newest first: the rows most likely to be answering today's questions
	// converge first when the cap bites.
	for index := len(rows) - 1; index >= 0 && examined < channelIngestionBackfillRowCap; index-- {
		row := rows[index]
		if !channelSourcedTranscript(row) || memoryEntryHiddenFromRecall(row) {
			continue
		}
		key := messageKey{strings.TrimSpace(row.Metadata["threadId"]), strings.TrimSpace(row.Metadata["messageId"])}
		if _, done := seen[key]; done {
			continue
		}
		seen[key] = struct{}{}
		examined++
		thread, ok := threads[key.threadID]
		if !ok {
			if threadMissing[key.threadID] {
				skipped++
				continue
			}
			entry, found := app.memory.entryByKindAndID(meetingMemoryKindScoutChat, key.threadID)
			decoded, decodedOK := scoutChatThreadRecord{}, false
			if found {
				decoded, decodedOK = decodeScoutChatThreadEntry(entry)
			}
			if !decodedOK {
				// A row whose thread record is gone cannot be reconciled either
				// way; leave it alone and say so.
				threadMissing[key.threadID] = true
				skipped++
				continue
			}
			threads[key.threadID] = decoded
			thread = decoded
		}
		feeds := (thread.Riff != nil && riffThreadShouldFeedBrain(thread)) || (thread.Riff == nil && channelThreadShouldFeedBrain(thread))
		if scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic || !feeds {
			// Never touch private (or no-longer-feeding) threads.
			skipped++
			continue
		}
		messageIndex := scoutChatMessageIndex(thread, key.messageID)
		if messageIndex < 0 {
			app.tombstoneChannelMessageIngestionRows(thread, key.messageID, thread.OwnerEmail, liveRows[key])
			tombstoned++
			continue
		}
		message := thread.Messages[messageIndex]
		if message.Role != "user" {
			skipped++
			continue
		}
		source := strings.ToLower(strings.TrimSpace(row.Metadata["source"]))
		speaker := scoutChatAuthorName(&userAccount{Email: message.AuthorEmail, Name: message.AuthorName})
		expected := formatSpeakerTranscript(speaker, normalizeMemoryText(canonicalizeDomainTerms(channelBrainTranscriptBody(thread, source, strings.TrimSpace(message.Text)))))
		if normalizeMemoryText(row.Text) == normalizeMemoryText(expected) {
			continue
		}
		if strings.TrimSpace(message.EditedAt) == "" {
			// Drift without an edit stamp (legacy): give the re-file a revision
			// identity so it never collides with the frozen first-post id.
			message.EditedAt = "backfill:" + time.Now().UTC().Format(time.RFC3339Nano)
		}
		app.supersedeChannelMessageIngestionRows(thread, message, liveRows[key])
		superseded++
	}
	if superseded > 0 || tombstoned > 0 || skipped > 0 {
		log.Infof("channel ingestion backfill: superseded %d, tombstoned %d, skipped %d (examined %d live rows)", superseded, tombstoned, skipped, examined)
	}
	return superseded, tombstoned, skipped
}
