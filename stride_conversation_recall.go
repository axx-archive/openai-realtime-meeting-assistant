package main

// This file is the request-local bridge between STRIDE's body-free
// conversation ledger and Scout's existing company-memory search. The durable
// scout_chat_thread record remains UI state and is never made searchable. An
// authorized request instead re-joins a current ledger projection to the
// current public-thread source, rechecks audience and content identity, and
// emits bounded prompt-only entries. That keeps private chat private while
// allowing a meeting Scout turn to find an exact #team link or file name.

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

const memoryContextKindCompanyConversation = "company_conversation"

func strideChatMessageContentDigest(deleted bool, message scoutChatMessageRecord) (string, error) {
	return STRIDEContractDigest(struct {
		Deleted   bool                       `json:"deleted"`
		Text      string                     `json:"text,omitempty"`
		Files     []scoutChatFileAttachment  `json:"files,omitempty"`
		Reactions []scoutChatMessageReaction `json:"reactions,omitempty"`
		ReplyTo   *scoutChatReplyRef         `json:"replyTo,omitempty"`
	}{Deleted: deleted, Text: message.Text, Files: message.Files, Reactions: message.Reactions, ReplyTo: message.ReplyTo})
}

func strideChatMessageRichRefs(message scoutChatMessageRecord) (attachments, links, structured []STRIDEReference, err error) {
	attachmentSeen := map[string]bool{}
	for _, file := range message.Files {
		digest, digestErr := STRIDEContractDigest(struct {
			Name, Kind, Ref, Mime, SourceID, SourceRevision string
			Size                                            int64
		}{file.Name, file.Kind, file.Ref, file.Mime, file.SourceID, file.SourceRevision, file.Size})
		if digestErr != nil {
			return nil, nil, nil, digestErr
		}
		ref := STRIDEReference{ContractType: STRIDEContractRichMessagePart, ID: "chat_file_" + digest[:24], Revision: 1, Digest: digest}
		key := strideConversationReferenceKey(ref)
		if !attachmentSeen[key] {
			attachmentSeen[key] = true
			attachments = append(attachments, ref)
		}
	}
	linkSeen := map[string]bool{}
	for _, exactURL := range exactScoutChatLinks(message.Text, 16) {
		digest := sha256Hex([]byte("stride-chat-link/v1\x00" + exactURL))
		ref := STRIDEReference{ContractType: STRIDEContractRichMessagePart, ID: "chat_link_" + digest[:24], Revision: 1, Digest: digest}
		key := strideConversationReferenceKey(ref)
		if !linkSeen[key] {
			linkSeen[key] = true
			links = append(links, ref)
		}
	}
	attachments = SortedSTRIDEReferences(attachments)
	links = SortedSTRIDEReferences(links)
	structured = SortedSTRIDEReferences(append(append([]STRIDEReference(nil), attachments...), links...))
	return attachments, links, structured, nil
}

func strideChatReactionActors(message scoutChatMessageRecord) []string {
	actors := make([]string, 0, len(message.Reactions))
	for _, reaction := range message.Reactions {
		if principal := strideRuntimePrincipalForEmail(reaction.ActorEmail); principal != "" {
			actors = append(actors, principal)
		}
	}
	return sortedUniqueSTRIDEIDs(actors)
}

// exactScoutChatLinks is used only to create body-free identity digests and an
// already-authorized prompt entry. Unlike safeScoutChatLinks, it preserves the
// authored query and fragment because those may be the exact link a coworker
// asks Scout to recover. Nothing here fetches the URL.
func exactScoutChatLinks(text string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	seen := map[string]bool{}
	links := make([]string, 0, min(limit, 4))
	for _, field := range strings.Fields(text) {
		candidate := strings.Trim(field, "<>[](){}\"',;!?\u201c\u201d\u2018\u2019")
		if len(candidate) > 2048 || (!strings.HasPrefix(candidate, "https://") && !strings.HasPrefix(candidate, "http://")) {
			continue
		}
		parsed, parseErr := url.Parse(candidate)
		if parseErr != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil {
			continue
		}
		canonical := parsed.String()
		if seen[canonical] {
			continue
		}
		seen[canonical] = true
		links = append(links, canonical)
		if len(links) == limit {
			break
		}
	}
	return links
}

func sameSTRIDEReferenceSlice(left, right []STRIDEReference) bool {
	left = SortedSTRIDEReferences(left)
	right = SortedSTRIDEReferences(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sameSTRIDEIDSet(left, right []string) bool {
	left = sortedUniqueSTRIDEIDs(left)
	right = sortedUniqueSTRIDEIDs(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (app *kanbanBoardApp) strideConversationProjectionPrincipal(principal RecallPrincipal) (string, bool, bool) {
	if app == nil || app.strideRuntime == nil || app.strideRuntime.Health().State != STRIDERuntimeStandby ||
		strings.TrimSpace(principal.TenantID) != canonicalTenantID() || principal.Audience == "guest" || strings.TrimSpace(principal.GuestID) != "" {
		return "", false, false
	}
	if principal.User != nil {
		user := accountStore().findUser(principal.User.Email)
		if user == nil {
			return "", false, false
		}
		projected := strideRuntimePrincipalForEmail(user.Email)
		return projected, principal.Audience == "shared_room", projected != ""
	}
	if principal.Audience == "shared_channel" && strings.TrimSpace(principal.ServiceID) == "private-riff-publication" && strings.TrimSpace(principal.ThreadID) != "" {
		// Publication reauthorization represents the destination audience, not
		// the sharer. Resolve one current runtime principal who is actually in
		// the exact public destination; the projection below remains hard-bound
		// to that thread and its current STRIDE audience/ACL revision.
		for _, stored := range app.memory.entriesOfKind(meetingMemoryKindScoutChat, 0) {
			thread, decoded := decodeScoutChatThreadEntry(stored)
			if !decoded || thread.ID != strings.TrimSpace(principal.ThreadID) || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic || thread.ArchivedAt != "" {
				continue
			}
			for _, member := range runtimeMemberPrincipals() {
				email := strings.TrimPrefix(strings.TrimSpace(member), "user:")
				if !scoutChatThreadAllowsViewer(thread, email) {
					continue
				}
				if projected := strideRuntimePrincipalForEmail(email); projected != "" {
					return projected, false, true
				}
			}
		}
		return "", false, false
	}
	if principal.Audience != "shared_room" || strings.TrimSpace(principal.ServiceID) != "scout-recall" {
		return "", false, false
	}
	roomID := normalizeRoomID(principal.RoomID)
	if roomID != officeRoomID {
		sittingID := strings.TrimSpace(principal.SittingID)
		if sittingID == "" || app.memory == nil || app.memory.currentMeetingID(roomID) != sittingID {
			return "", false, false
		}
		app.mu.Lock()
		state := app.roomLive[roomID]
		guestFree := state != nil && len(state.guestSeats) == 0 && (state.mediaSittingID == "" || state.mediaSittingID == sittingID)
		app.mu.Unlock()
		if !guestFree {
			return "", false, false
		}
	}
	for _, member := range runtimeMemberPrincipals() {
		email := strings.TrimPrefix(strings.TrimSpace(member), "user:")
		if projected := strideRuntimePrincipalForEmail(email); projected != "" {
			// A shared-room Scout may read only organization-public channels,
			// never a project channel that happens to include this member.
			return projected, true, true
		}
	}
	return "", false, false
}

func (app *kanbanBoardApp) authorizedSTRIDEConversationEntries(principal RecallPrincipal) []meetingMemoryEntry {
	projectedPrincipal, organizationOnly, ok := app.strideConversationProjectionPrincipal(principal)
	var projections []STRIDEConversationMessageProjection
	if ok {
		err := app.strideRuntime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
			var projectErr error
			projections, projectErr = domains.ConversationLedger.ProjectForTenantPrincipal(canonicalTenantID(), projectedPrincipal)
			return projectErr
		})
		if err != nil {
			projections = nil
		}
	}
	byThread := map[string]map[string]STRIDEConversationMessageProjection{}
	for _, projection := range projections {
		if projection.SourceType != "channel_message" || !projection.RecallEligible {
			continue
		}
		if byThread[projection.ThreadID] == nil {
			byThread[projection.ThreadID] = map[string]STRIDEConversationMessageProjection{}
		}
		byThread[projection.ThreadID][projection.SourceID] = projection
	}

	entries := make([]meetingMemoryEntry, 0, len(projections))
	seenSources := make(map[string]bool, len(projections))
	for _, stored := range app.memory.entriesOfKind(meetingMemoryKindScoutChat, 0) {
		thread, decoded := decodeScoutChatThreadEntry(stored)
		threadProjection := byThread[thread.ID]
		if !decoded || len(threadProjection) == 0 || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic || thread.ArchivedAt != "" ||
			organizationOnly && !scoutChatThreadIsOrganizationPublic(thread) {
			continue
		}
		if principal.Audience == "shared_channel" && strings.TrimSpace(principal.ThreadID) != thread.ID {
			continue
		}
		if principal.User != nil && !scoutChatThreadAllowsViewer(thread, principal.User.Email) {
			continue
		}
		currentAudience, currentACL, authorityErr := strideRuntimeChatAudienceAuthority(thread)
		if authorityErr != nil || !containsSTRIDEID(currentAudience.Principals, projectedPrincipal) {
			continue
		}
		for _, message := range thread.Messages {
			projection, found := threadProjection[message.ID]
			if !found || projection.ACLVersion != currentACL || projection.Audience.Visibility != currentAudience.Visibility ||
				!sameSTRIDEIDSet(projection.Audience.Principals, currentAudience.Principals) {
				continue
			}
			digest, digestErr := strideChatMessageContentDigest(false, message)
			attachments, links, _, refsErr := strideChatMessageRichRefs(message)
			reactions := strideChatReactionActors(message)
			if digestErr != nil || refsErr != nil || digest != projection.LatestEvent.Digest ||
				!sameSTRIDEReferenceSlice(attachments, projection.AttachmentRefs) || !sameSTRIDEReferenceSlice(links, projection.LinkRefs) ||
				!sameSTRIDEIDSet(reactions, projection.ReactionActors) {
				continue
			}
			createdAt, timeErr := parseSTRIDEChatTime(message.CreatedAt)
			if timeErr != nil {
				continue
			}
			entries = append(entries, strideConversationRecallEntry(thread, message, projection, createdAt))
			seenSources[thread.ID+"\x00"+message.ID] = true
		}
	}

	// RecallThreadIDs was intentionally an E1 rollout allowlist. Production
	// channels are dynamic, however, so treating that static list as the company
	// Brain corpus made older readable channels disappear from a private Scout
	// question even though their current source and ACL were still available.
	//
	// ConversationContinuity is the provider-independent, body-free authority
	// for exactly this bridge. For a fenced authenticated private Scout turn, join
	// its current checkpoint back to the current public thread only after the
	// viewer, audience digest, and whole-source digest all reauthorize. Shared
	// rooms/publication services deliberately do not receive this compatibility
	// lane: widening a private answer into another audience still requires the
	// ordinary STRIDE projection/destination checks above.
	entries = append(entries, app.authorizedConversationContinuityEntries(principal, seenSources)...)
	sort.SliceStable(entries, func(i, j int) bool {
		if !entries[i].CreatedAt.Equal(entries[j].CreatedAt) {
			return entries[i].CreatedAt.Before(entries[j].CreatedAt)
		}
		return entries[i].ID < entries[j].ID
	})
	return entries
}

func (app *kanbanBoardApp) authorizedConversationContinuityEntries(principal RecallPrincipal, seenSources map[string]bool) []meetingMemoryEntry {
	if app == nil || app.memory == nil || principal.User == nil || principal.Audience != "private" || !principal.ConversationContinuityRecall ||
		strings.TrimSpace(principal.ServiceID) != "" || strings.TrimSpace(principal.GuestID) != "" ||
		strings.TrimSpace(principal.TenantID) != canonicalArtifactTenantID() {
		return nil
	}
	viewer := accountStore().findUser(principal.User.Email)
	if viewer == nil {
		return nil
	}
	if seenSources == nil {
		seenSources = map[string]bool{}
	}
	latestContinuity := map[string]conversationContinuityCheckpoint{}
	for _, stored := range app.memory.entriesOfKind(meetingMemoryKindConversationContinuity, 0) {
		checkpoint, decoded := decodeConversationContinuity(stored)
		if !decoded {
			continue
		}
		prior := latestContinuity[checkpoint.ThreadID]
		if prior.ID == "" || checkpoint.Revision > prior.Revision || checkpoint.Revision == prior.Revision && checkpoint.UpdatedAt.After(prior.UpdatedAt) {
			latestContinuity[checkpoint.ThreadID] = checkpoint
		}
	}

	entries := []meetingMemoryEntry{}
	for _, stored := range app.memory.entriesOfKind(meetingMemoryKindScoutChat, 0) {
		candidate, decoded := decodeScoutChatThreadEntry(stored)
		if !decoded || candidate.ID == "" {
			continue
		}
		// Re-read under the same per-thread authority lock used by message,
		// archive, and audience mutations. A stale enumeration can only make this
		// source disappear; it can never carry a revoked body into the prompt.
		lock := app.scoutChatThreadLock(candidate.ID)
		lock.Lock()
		thread, _, readErr := app.scoutChatThreadByID(viewer.Email, candidate.ID)
		if readErr != nil || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic || thread.ArchivedAt != "" {
			lock.Unlock()
			continue
		}
		checkpoint := latestContinuity[thread.ID]
		if !conversationContinuityCheckpointCurrentForViewer(viewer.Email, thread, checkpoint) {
			lock.Unlock()
			continue
		}
		checkpointSources := make(map[string]bool, len(checkpoint.SourceMessageIDs))
		for _, messageID := range checkpoint.SourceMessageIDs {
			checkpointSources[strings.TrimSpace(messageID)] = true
		}
		for _, message := range thread.Messages {
			key := thread.ID + "\x00" + message.ID
			if seenSources[key] || !checkpointSources[message.ID] || strings.TrimSpace(message.Text) == "" {
				continue
			}
			createdAt, timeErr := parseSTRIDEChatTime(message.CreatedAt)
			contentDigest, digestErr := strideChatMessageContentDigest(false, message)
			if timeErr != nil || digestErr != nil {
				continue
			}
			entry := conversationContinuityRecallEntry(thread, message, checkpoint, contentDigest, createdAt)
			if !recallEntryScopeAllowed(entry.Metadata, principal) {
				continue
			}
			entries = append(entries, entry)
			seenSources[key] = true
		}
		lock.Unlock()
	}
	return entries
}

func strideConversationRecallEntry(thread scoutChatThreadRecord, message scoutChatMessageRecord, projection STRIDEConversationMessageProjection, createdAt time.Time) meetingMemoryEntry {
	author := firstNonEmptyString(strings.TrimSpace(message.AuthorName), projection.AuthorName, participantNameForEmail(message.AuthorEmail), scoutParticipantName)
	text := conversationRecallBody(thread, message, author, createdAt)
	metadata := conversationRecallMetadata(thread, message, author, projection.TenantID, projection.LatestEvent.ID, "stride_conversation_ledger", projection.LatestEvent.Digest, createdAt)
	return meetingMemoryEntry{
		ID:        "stride_chat_recall_" + projection.LatestEvent.ID,
		Kind:      memoryContextKindCompanyConversation,
		Text:      text,
		CreatedAt: createdAt,
		Metadata:  metadata,
	}
}

func conversationContinuityRecallEntry(thread scoutChatThreadRecord, message scoutChatMessageRecord, checkpoint conversationContinuityCheckpoint, contentDigest string, createdAt time.Time) meetingMemoryEntry {
	author := firstNonEmptyString(strings.TrimSpace(message.AuthorName), participantNameForEmail(message.AuthorEmail), scoutParticipantName)
	identity := sha256Hex([]byte(checkpoint.ID + "\x00" + message.ID + "\x00" + contentDigest))
	metadata := conversationRecallMetadata(thread, message, author, canonicalArtifactTenantID(), checkpoint.ID, "conversation_continuity", contentDigest, createdAt)
	metadata["sourceRevision"] = fmt.Sprint(checkpoint.Revision)
	metadata["sourceDigest"] = checkpoint.SourceDigest
	return meetingMemoryEntry{
		ID:        "continuity_chat_recall_" + identity[:24],
		Kind:      memoryContextKindCompanyConversation,
		Text:      conversationRecallBody(thread, message, author, createdAt),
		CreatedAt: createdAt,
		Metadata:  metadata,
	}
}

// currentCompanyConversationSource is deliberately not a meetingMemoryEntry.
// It can be minted only by re-reading an exact source message under its current
// per-thread authority lock after the provider returns. Keeping this capability
// distinct prevents a stored transcript or prompt wrapper from being mistaken
// for an openable, current citation.
type currentCompanyConversationSource struct {
	ThreadID    string
	ThreadTitle string
	MessageID   string
	Author      string
	AuthorEmail string
	Role        string
	OccurredAt  string
	Text        string
}

// lockCurrentCompanyConversationSources reauthorizes every company-channel
// context row used by a private Scout answer after the provider returns and
// holds the exact source thread locks through the caller's final persistence
// effect. A concurrent edit, delete, archive, or audience narrowing therefore
// either lands first and makes validation fail closed, or waits until the
// already-authorized private answer is durably committed.
func (app *kanbanBoardApp) lockCurrentCompanyConversationSources(principal RecallPrincipal, destinationThreadID string, contextEntries []meetingMemoryEntry) ([]currentCompanyConversationSource, func(), error) {
	sourceEntries := make([]meetingMemoryEntry, 0, len(contextEntries))
	threadSet := map[string]bool{}
	for _, entry := range contextEntries {
		conversationTranscript := entry.Kind == meetingMemoryKindTranscript && oneOf(strings.TrimSpace(entry.Metadata["source"]), transcriptSourceChannel, transcriptSourceRiff)
		if entry.Kind != memoryContextKindCompanyConversation && !conversationTranscript {
			continue
		}
		threadID := strings.TrimSpace(entry.Metadata["threadId"])
		messageID := strings.TrimSpace(entry.Metadata["messageId"])
		if threadID == "" || messageID == "" {
			return nil, func() {}, fmt.Errorf("company conversation source is unavailable")
		}
		sourceEntries = append(sourceEntries, entry)
		threadSet[threadID] = true
	}
	if len(sourceEntries) == 0 {
		return nil, func() {}, nil
	}
	if app == nil || app.memory == nil || principal.User == nil || principal.Audience != "private" ||
		strings.TrimSpace(principal.ServiceID) != "" || strings.TrimSpace(principal.GuestID) != "" ||
		strings.TrimSpace(principal.TenantID) != canonicalArtifactTenantID() {
		return nil, func() {}, fmt.Errorf("company conversation source is unavailable")
	}
	viewer := accountStore().findUser(principal.User.Email)
	if viewer == nil {
		return nil, func() {}, fmt.Errorf("company conversation source is unavailable")
	}
	if destinationThreadID = strings.TrimSpace(destinationThreadID); destinationThreadID != "" {
		threadSet[destinationThreadID] = true
	}

	threadIDs := make([]string, 0, len(threadSet))
	for threadID := range threadSet {
		threadIDs = append(threadIDs, threadID)
	}
	sort.Strings(threadIDs)
	release := app.lockScoutChatThreadSet(threadIDs...)
	fail := func() ([]currentCompanyConversationSource, func(), error) {
		release()
		return nil, func() {}, fmt.Errorf("company conversation source changed while Scout was answering")
	}

	threads := make(map[string]scoutChatThreadRecord, len(threadIDs))
	for _, threadID := range threadIDs {
		thread, _, err := app.scoutChatThreadByID(viewer.Email, threadID)
		if err != nil || thread.ArchivedAt != "" {
			return fail()
		}
		threads[threadID] = thread
	}

	current := make([]currentCompanyConversationSource, 0, len(sourceEntries))
	seen := map[string]bool{}
	for _, entry := range sourceEntries {
		threadID := strings.TrimSpace(entry.Metadata["threadId"])
		messageID := strings.TrimSpace(entry.Metadata["messageId"])
		key := threadID + "\x00" + messageID
		thread := threads[threadID]
		messageIndex := scoutChatMessageIndex(thread, messageID)
		if messageIndex < 0 {
			return fail()
		}
		message := thread.Messages[messageIndex]
		if entry.Kind == meetingMemoryKindTranscript {
			source := strings.TrimSpace(entry.Metadata["source"])
			var expectedMetadata map[string]string
			switch source {
			case transcriptSourceChannel:
				if scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
					return fail()
				}
				expectedMetadata = channelBrainMetadata(thread, message)
			case transcriptSourceRiff:
				if thread.Riff == nil {
					return fail()
				}
				expectedMetadata = riffBrainMetadata(thread, message)
			default:
				return fail()
			}
			expectedText := formatSpeakerTranscript(
				scoutChatAuthorName(&userAccount{Email: message.AuthorEmail, Name: message.AuthorName}),
				channelBrainTranscriptBody(thread, source, message.Text),
			)
			if !recallEntryScopeAllowed(expectedMetadata, principal) || entry.Text != expectedText || !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
				return fail()
			}
			for _, field := range []string{"source", "threadId", "messageId", "channelTitle", "sourceThreadId", "sourceTitle", "visibility", "ownerEmail", "memberEmails"} {
				if strings.TrimSpace(entry.Metadata[field]) != strings.TrimSpace(expectedMetadata[field]) {
					return fail()
				}
			}
			continue
		}
		if seen[key] || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
			if seen[key] {
				continue
			}
			return fail()
		}
		createdAt, timeErr := parseSTRIDEChatTime(message.CreatedAt)
		contentDigest, digestErr := strideChatMessageContentDigest(false, message)
		author := firstNonEmptyString(strings.TrimSpace(message.AuthorName), participantNameForEmail(message.AuthorEmail), scoutParticipantName)
		currentMetadata := conversationRecallMetadata(thread, message, author, canonicalArtifactTenantID(), "current", "current_chat_source", contentDigest, createdAt)
		if timeErr != nil || digestErr != nil || strings.TrimSpace(message.Text) == "" ||
			!recallEntryScopeAllowed(currentMetadata, principal) ||
			!oneOf(strings.TrimSpace(entry.Metadata["sourceAuthority"]), "stride_conversation_ledger", "conversation_continuity") ||
			entry.Metadata["tenantId"] != canonicalArtifactTenantID() ||
			entry.Metadata["contentDigest"] != contentDigest ||
			entry.Metadata["threadTitle"] != strings.TrimSpace(thread.Title) ||
			entry.Metadata["author"] != author ||
			normalizeAccountEmail(entry.Metadata["authorEmail"]) != normalizeAccountEmail(message.AuthorEmail) ||
			entry.Metadata["occurredAt"] != createdAt.UTC().Format(time.RFC3339Nano) ||
			entry.Text != conversationRecallBody(thread, message, author, createdAt) {
			return fail()
		}
		seen[key] = true
		current = append(current, currentCompanyConversationSource{
			ThreadID: thread.ID, ThreadTitle: strings.TrimSpace(thread.Title), MessageID: message.ID,
			Author: author, AuthorEmail: normalizeAccountEmail(message.AuthorEmail), Role: strings.ToLower(strings.TrimSpace(message.Role)),
			OccurredAt: createdAt.UTC().Format(time.RFC3339Nano), Text: message.Text,
		})
	}
	return current, release, nil
}

func conversationRecallBody(thread scoutChatThreadRecord, message scoutChatMessageRecord, author string, createdAt time.Time) string {
	var body strings.Builder
	fmt.Fprintf(&body, "Channel #%s\nAuthor: %s\nPosted: %s\n", strings.TrimSpace(thread.Title), author, createdAt.UTC().Format(time.RFC3339))
	if text := strings.TrimSpace(message.Text); text != "" {
		body.WriteString("Message: ")
		body.WriteString(text)
		body.WriteByte('\n')
	}
	if links := exactScoutChatLinks(message.Text, 16); len(links) > 0 {
		body.WriteString("Links:\n")
		for _, link := range links {
			body.WriteString("- ")
			body.WriteString(link)
			body.WriteByte('\n')
		}
	}
	if len(message.Files) > 0 {
		body.WriteString("Files:\n")
		for _, file := range message.Files {
			fmt.Fprintf(&body, "- %s", firstNonEmptyString(strings.TrimSpace(file.Name), "file"))
			if file.Mime != "" {
				fmt.Fprintf(&body, " (%s)", strings.TrimSpace(file.Mime))
			}
			if file.SourceID != "" {
				fmt.Fprintf(&body, " [source %s@%s]", strings.TrimSpace(file.SourceID), strings.TrimSpace(file.SourceRevision))
			}
			body.WriteByte('\n')
		}
	}
	if len(message.Reactions) > 0 {
		body.WriteString("Reactions:")
		for _, reaction := range message.Reactions {
			fmt.Fprintf(&body, " %s by %s;", strings.TrimSpace(reaction.Emoji), firstNonEmptyString(strings.TrimSpace(reaction.ActorName), participantNameForEmail(reaction.ActorEmail), "member"))
		}
		body.WriteByte('\n')
	}
	return strings.TrimSpace(body.String())
}

func conversationRecallMetadata(thread scoutChatThreadRecord, message scoutChatMessageRecord, author, tenantID, eventRef, authority, contentDigest string, createdAt time.Time) map[string]string {
	visibility := "project"
	if scoutChatThreadIsOrganizationPublic(thread) {
		visibility = "organization"
	}
	metadata := map[string]string{
		"title":           "#" + strings.TrimSpace(thread.Title) + " — " + author,
		"visibility":      visibility,
		"tenantId":        strings.TrimSpace(tenantID),
		"threadId":        thread.ID,
		"threadTitle":     strings.TrimSpace(thread.Title),
		"messageId":       message.ID,
		"author":          author,
		"authorEmail":     normalizeAccountEmail(message.AuthorEmail),
		"occurredAt":      createdAt.UTC().Format(time.RFC3339Nano),
		"sourceFamily":    "company_conversation",
		"sourceAuthority": strings.TrimSpace(authority),
		"eventRef":        strings.TrimSpace(eventRef),
		"contentDigest":   strings.TrimSpace(contentDigest),
	}
	if visibility == "project" {
		metadata["ownerEmail"] = normalizeAccountEmail(thread.OwnerEmail)
		metadata["memberEmails"] = strings.Join(scoutChatThreadMemberEmails(thread), ",")
	}
	return metadata
}
