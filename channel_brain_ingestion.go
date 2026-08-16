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
	"fmt"
	"strings"
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

// fileChannelMessageAsBrainTranscript creates a transcript entry from a
// channel or Riff message. This enables the brain worker to synthesize
// channel knowledge into brain entries.
func (app *kanbanBoardApp) fileChannelMessageAsBrainTranscript(thread scoutChatThreadRecord, message scoutChatMessageRecord) error {
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

	// Add channel context to the text for better brain synthesis
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

	// Create a unique event ID based on the message
	eventID := fmt.Sprintf("%s-%s-%s", source, thread.ID, message.ID)

	// File the transcript entry (pinned to office room like brain intake)
	_, _, err := app.memory.appendAttributedTranscriptEntry(
		officeRoomID,
		eventID,
		"",       // itemID
		speaker,
		"",       // speakerType
		bodyBuilder.String(),
		meta,
		true, // bypass usefulness filter
		"",   // expectedMeetingID
	)

	return err
}

// fileChannelMessagesAsBrainTranscripts files multiple messages as transcripts.
func (app *kanbanBoardApp) fileChannelMessagesAsBrainTranscripts(thread scoutChatThreadRecord, messages []scoutChatMessageRecord) {
	for _, message := range messages {
		if err := app.fileChannelMessageAsBrainTranscript(thread, message); err != nil {
			log.Errorf("Failed to file channel message as brain transcript: %v", err)
		}
	}
}
