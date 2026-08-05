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
	if !ok {
		return nil
	}
	var projections []STRIDEConversationMessageProjection
	err := app.strideRuntime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
		var projectErr error
		projections, projectErr = domains.ConversationLedger.ProjectForTenantPrincipal(canonicalTenantID(), projectedPrincipal)
		return projectErr
	})
	if err != nil || len(projections) == 0 {
		return nil
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
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if !entries[i].CreatedAt.Equal(entries[j].CreatedAt) {
			return entries[i].CreatedAt.Before(entries[j].CreatedAt)
		}
		return entries[i].ID < entries[j].ID
	})
	return entries
}

func strideConversationRecallEntry(thread scoutChatThreadRecord, message scoutChatMessageRecord, projection STRIDEConversationMessageProjection, createdAt time.Time) meetingMemoryEntry {
	author := firstNonEmptyString(strings.TrimSpace(message.AuthorName), projection.AuthorName, participantNameForEmail(message.AuthorEmail), scoutParticipantName)
	var body strings.Builder
	fmt.Fprintf(&body, "Channel #%s\nAuthor: %s\n", strings.TrimSpace(thread.Title), author)
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
	visibility := "project"
	if scoutChatThreadIsOrganizationPublic(thread) {
		visibility = "organization"
	}
	return meetingMemoryEntry{
		ID:        "stride_chat_recall_" + projection.LatestEvent.ID,
		Kind:      memoryContextKindCompanyConversation,
		Text:      strings.TrimSpace(body.String()),
		CreatedAt: createdAt,
		Metadata: map[string]string{
			"title":        "#" + strings.TrimSpace(thread.Title) + " — " + author,
			"visibility":   visibility,
			"tenantId":     projection.TenantID,
			"threadId":     thread.ID,
			"messageId":    message.ID,
			"sourceFamily": "company_conversation",
			"eventRef":     projection.LatestEvent.ID,
		},
	}
}
