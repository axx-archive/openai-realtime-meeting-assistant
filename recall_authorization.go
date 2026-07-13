package main

import (
	"context"
	"strings"
)

// RecallPrincipal is the audience carried through every retrieval lane. An
// Room and sitting bind explicit room-only grants. Organization-visible
// history remains available across rooms to authenticated organization members.
type RecallPrincipal struct {
	User      *userAccount
	GuestID   string
	ServiceID string
	TenantID  string
	RoomID    string
	SittingID string
	Audience  string
}

func recallPrincipalForUser(user *userAccount) RecallPrincipal {
	return RecallPrincipal{User: user, TenantID: canonicalArtifactTenantID(), Audience: "private"}
}

func recallPrincipalForEmail(email string) RecallPrincipal {
	email = normalizeAccountEmail(email)
	if email == "" {
		return RecallPrincipal{}
	}
	return recallPrincipalForUser(&userAccount{Email: email})
}

func sharedRoomRecallPrincipal(roomID string, sittingID string) RecallPrincipal {
	return RecallPrincipal{
		ServiceID: "scout-recall",
		TenantID:  canonicalArtifactTenantID(),
		RoomID:    normalizeRoomID(roomID),
		SittingID: strings.TrimSpace(sittingID),
		Audience:  "shared_room",
	}
}

func recallPrincipalForGuest(guestID string, roomID string, sittingID string) RecallPrincipal {
	return RecallPrincipal{
		GuestID:   strings.TrimSpace(guestID),
		TenantID:  canonicalArtifactTenantID(),
		RoomID:    normalizeRoomID(roomID),
		SittingID: strings.TrimSpace(sittingID),
		Audience:  "guest",
	}
}

// recallPrincipalForMemberRoom binds a signed-in caller to the room and
// sitting established by server-side admission state. Callers never provide
// these values in tool arguments.
func (app *kanbanBoardApp) recallPrincipalForMemberRoom(email string, roomID string) RecallPrincipal {
	user := accountStore().findUser(email)
	if user == nil {
		user = &userAccount{Email: normalizeAccountEmail(email)}
	}
	roomID = normalizeRoomID(roomID)
	principal := recallPrincipalForUser(user)
	principal.RoomID = roomID
	if app != nil && app.memory != nil {
		principal.SittingID = app.memory.currentMeetingID(roomID)
	}
	return principal
}

func recallEntryScopeAllowed(metadata map[string]string, principal RecallPrincipal) bool {
	if principal.Audience == "guest" || strings.TrimSpace(principal.GuestID) != "" {
		// Guests have a live media/chat grant, never durable company-brain recall.
		return false
	}
	member := principal.User != nil && accountStore().findUser(principal.User.Email) != nil
	sharedService := principal.Audience == "shared_room" && strings.TrimSpace(principal.ServiceID) != ""
	if !member && !sharedService {
		return false
	}
	visibility := strings.ToLower(strings.TrimSpace(metadata["visibility"]))
	switch visibility {
	case "", "organization", "org", "team", "public", "shared":
		// Known organization/shared vocabularies. Empty is the legacy office
		// migration value and remains organization-visible.
	case "private", "owner":
		viewer := ""
		if principal.User != nil {
			viewer = normalizeAccountEmail(principal.User.Email)
		}
		if viewer == "" || viewer != normalizeAccountEmail(metadata["ownerEmail"]) {
			return false
		}
	case "room", "room_only":
		roomID := normalizeRoomID(metadata["roomId"])
		if roomID == officeRoomID || normalizeRoomID(principal.RoomID) != roomID {
			return false
		}
		sittingID := firstNonEmptyString(strings.TrimSpace(metadata["sittingId"]), strings.TrimSpace(metadata["meetingId"]))
		if sittingID != "" && strings.TrimSpace(principal.SittingID) != sittingID {
			return false
		}
	default:
		// A new visibility value must acquire an explicit policy before recall.
		return false
	}
	entryTenant := strings.TrimSpace(metadata["tenantId"])
	if entryTenant != "" && entryTenant != strings.TrimSpace(principal.TenantID) {
		return false
	}
	return true
}

// recallStoreForPrincipal constructs a request-local store containing only
// authorized candidates. Artifact authorization is metadata-only and followed
// by the same locked header recompare used by object handlers. No denied body
// reaches lexical scoring, semantic fusion, digest/ledger folds, or prompts.
func (app *kanbanBoardApp) recallStoreForPrincipal(ctx context.Context, principal RecallPrincipal) *meetingMemoryStore {
	filtered := &meetingMemoryStore{seen: map[string]struct{}{}, meetingIDs: map[string]string{}, bootLatestIDs: map[string]string{}, bootLatestRoomIDs: map[string]map[string]string{}}
	if app == nil || app.memory == nil {
		return filtered
	}
	type artifactCandidate struct {
		index  int
		id     string
		header ArtifactAuthorizationHeader
	}
	var artifacts []artifactCandidate
	ordered := map[int]meetingMemoryEntry{}
	app.memory.mu.Lock()
	sourceLen := len(app.memory.entries)
	for index, stored := range app.memory.entries {
		if memoryEntryHiddenFromRecall(stored) || !recallEntryScopeAllowed(stored.Metadata, principal) {
			continue
		}
		if stored.Kind == meetingMemoryKindOSArtifact {
			header := app.memory.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(meetingMemoryEntry{ID: stored.ID, Kind: stored.Kind, Metadata: stored.Metadata}))
			artifacts = append(artifacts, artifactCandidate{index: index, id: stored.ID, header: header})
			continue
		}
		ordered[index] = cloneMemoryEntry(stored)
	}
	app.memory.mu.Unlock()
	for _, candidate := range artifacts {
		serviceAllowed := principal.ServiceID != "" && principal.Audience == "shared_room" &&
			candidate.header.TenantID == strings.TrimSpace(principal.TenantID) &&
			strings.EqualFold(strings.TrimSpace(candidate.header.Visibility), "organization")
		if !serviceAllowed && !artifactHeaderAuthorized(ctx, principal.User, ACLReadContent, candidate.header) {
			continue
		}
		entry, found := app.memory.artifactSnapshotIfHeaderMatches(candidate.id, candidate.header)
		if !found {
			continue
		}
		ordered[candidate.index] = entry
	}
	// Reassemble in source order. Appending authorized artifacts after every
	// non-artifact silently changed recency/tie-breaking and could evict old
	// authorized rows from bounded lanes.
	for index := 0; index < sourceLen; index++ {
		entry, ok := ordered[index]
		if !ok {
			continue
		}
		filtered.entries = append(filtered.entries, entry)
		filtered.seen[entry.ID] = struct{}{}
	}
	return filtered
}

func (app *kanbanBoardApp) scopedRecallApp(ctx context.Context, principal RecallPrincipal) *kanbanBoardApp {
	if app == nil {
		return nil
	}
	app.mu.Lock()
	apiKey, model := app.apiKey, app.model
	cards := append([]kanbanCard(nil), app.cards...)
	updatedAt := app.updatedAt
	app.mu.Unlock()
	return &kanbanBoardApp{memory: app.recallStoreForPrincipal(ctx, principal), meetings: app.meetings, apiKey: apiKey, model: model, cards: cards, updatedAt: updatedAt}
}

func (app *kanbanBoardApp) memorySnapshotForPrincipal(ctx context.Context, principal RecallPrincipal, limit int) []meetingMemoryEntry {
	scoped := app.scopedRecallApp(ctx, principal)
	if scoped == nil {
		return nil
	}
	return scoped.memorySnapshotForClients(limit)
}

func authenticatedRecallPrincipal(email string) (RecallPrincipal, bool) {
	user := accountStore().findUser(email)
	if user == nil {
		return RecallPrincipal{}, false
	}
	return recallPrincipalForUser(user), true
}

func authenticatedRequester(value string) (*userAccount, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false
	}
	if !strings.Contains(value, "@") {
		value = participantEmail(value)
	}
	user := accountStore().findUser(value)
	return user, user != nil
}

func (app *kanbanBoardApp) delegatedMemorySnapshot(ctx context.Context, requester string, roomID string, limit int) []meetingMemoryEntry {
	user, ok := authenticatedRequester(requester)
	if !ok {
		return nil
	}
	principal := recallPrincipalForUser(user)
	if strings.TrimSpace(roomID) != "" {
		principal = app.recallPrincipalForMemberRoom(user.Email, roomID)
	}
	return app.memorySnapshotForPrincipal(ctx, principal, limit)
}

func broadcastScopedMemoryEntry(event string, entry meetingMemoryEntry, payload any) {
	if kanbanApp == nil || strings.TrimSpace(entry.ID) == "" {
		return
	}
	type recipient struct {
		websocket *threadSafeWriter
		email     string
		roomID    string
	}
	listLock.RLock()
	seen := map[*threadSafeWriter]bool{}
	var recipients []recipient
	for _, state := range officeConnections {
		if state.websocket != nil && !seen[state.websocket] {
			seen[state.websocket] = true
			recipients = append(recipients, recipient{state.websocket, state.sessionEmail, officeRoomID})
		}
	}
	for _, state := range peerConnections {
		if state.websocket != nil && !state.websocket.guest && !seen[state.websocket] {
			seen[state.websocket] = true
			recipients = append(recipients, recipient{state.websocket, state.sessionEmail, state.roomID})
		}
	}
	listLock.RUnlock()
	for _, recipient := range recipients {
		principal := kanbanApp.recallPrincipalForMemberRoom(recipient.email, recipient.roomID)
		if !recallEntryScopeAllowed(entry.Metadata, principal) {
			continue
		}
		_ = sendKanbanEvent(recipient.websocket, event, payload)
	}
}
