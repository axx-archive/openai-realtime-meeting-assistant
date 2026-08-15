package main

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"
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
	// ThreadID narrows a shared-channel worker to the exact destination
	// audience. It is server-stamped from durable origin metadata and is never
	// accepted as continuing authority from a model response.
	ThreadID string
	// MediaGeneration is populated only by server-owned, exact-scope readers.
	// It is never accepted from a browser or tool payload.
	MediaGeneration uint64
	Audience        string
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

// currentRoomMediaRecallPrincipal binds a shared-room reader to the exact
// server-owned media generation that is currently authoritative for the
// sitting. Captured transcript rows carry that generation as a revocation
// fence; a zero-generation service principal must not silently make those
// rows disappear from interval analysis or room Scout recall.
func (app *kanbanBoardApp) currentRoomMediaRecallPrincipal(roomID string, sittingID string) RecallPrincipal {
	principal := sharedRoomRecallPrincipal(roomID, sittingID)
	if app != nil {
		principal.MediaGeneration = app.roomMediaGeneration(roomID)
	}
	return principal
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
		principal.MediaGeneration = app.roomMediaGeneration(roomID)
	}
	return principal
}

func recallEntryScopeAllowed(metadata map[string]string, principal RecallPrincipal) bool {
	if principal.Audience == "guest" || strings.TrimSpace(principal.GuestID) != "" {
		// Guests have a live media/chat grant, never durable company-brain recall.
		return false
	}
	member := principal.User != nil && accountStore().findUser(principal.User.Email) != nil
	sharedService := (principal.Audience == "shared_room" || principal.Audience == "shared_channel") && strings.TrimSpace(principal.ServiceID) != ""
	if !member && !sharedService {
		return false
	}
	visibility := strings.ToLower(strings.TrimSpace(metadata["visibility"]))
	switch visibility {
	case "", "organization", "org", "team", "public", "shared":
		// Known organization/shared vocabularies. Empty is the legacy office
		// migration value and remains organization-visible.
	case "private", "owner":
		if principal.Audience == "shared_channel" || principal.Audience == "shared_room" {
			return false
		}
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
	if rawGeneration := strings.TrimSpace(metadata["mediaGeneration"]); rawGeneration != "" {
		generation, err := strconv.ParseUint(rawGeneration, 10, 64)
		if err != nil || generation == 0 || principal.MediaGeneration != generation {
			return false
		}
	}
	return true
}

// mediaSoakCanaryEntryForPrincipal is the sole authenticated read path allowed
// to see a server-owned release canary. It runs the same room/sitting/tenant
// policy and artifact object authorization as production recall, then performs
// a locked header recompare before returning the body. Normal readers remain
// governed by memoryEntryHiddenFromRecall and can never see these entries.
func (app *kanbanBoardApp) mediaSoakCanaryEntryForPrincipal(ctx context.Context, principal RecallPrincipal, id string) (meetingMemoryEntry, bool) {
	if app == nil || app.memory == nil || strings.TrimSpace(id) == "" || principal.MediaGeneration == 0 {
		return meetingMemoryEntry{}, false
	}
	var candidate meetingMemoryEntry
	var header ArtifactAuthorizationHeader
	app.memory.mu.Lock()
	for _, stored := range app.memory.entries {
		if stored.ID != strings.TrimSpace(id) || !strings.EqualFold(strings.TrimSpace(stored.Metadata["mediaSoakCanary"]), "true") || !recallEntryScopeAllowed(stored.Metadata, principal) {
			continue
		}
		candidate = cloneMemoryEntry(stored)
		if stored.Kind == meetingMemoryKindOSArtifact {
			header = app.memory.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(meetingMemoryEntry{ID: stored.ID, Kind: stored.Kind, Metadata: stored.Metadata}))
		}
		break
	}
	app.memory.mu.Unlock()
	if candidate.ID == "" {
		return meetingMemoryEntry{}, false
	}
	if candidate.Kind != meetingMemoryKindOSArtifact {
		return candidate, true
	}
	if !artifactHeaderAuthorized(ctx, principal.User, ACLReadContent, header) {
		return meetingMemoryEntry{}, false
	}
	app.memory.mu.Lock()
	defer app.memory.mu.Unlock()
	for _, stored := range app.memory.entries {
		if stored.ID != candidate.ID || !strings.EqualFold(strings.TrimSpace(stored.Metadata["mediaSoakCanary"]), "true") {
			continue
		}
		current := app.memory.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(meetingMemoryEntry{ID: stored.ID, Kind: stored.Kind, Metadata: stored.Metadata}))
		if !artifactAuthorizationHeaderEqual(header, current) || !recallEntryScopeAllowed(stored.Metadata, principal) {
			return meetingMemoryEntry{}, false
		}
		return cloneMemoryEntry(stored), true
	}
	return meetingMemoryEntry{}, false
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
		if app.memory.authorizationEntryVisitHook != nil {
			app.memory.authorizationEntryVisitHook()
		}
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
		publicationService := principal.ServiceID == "private-riff-publication" && principal.Audience == "shared_channel" && strings.TrimSpace(principal.ThreadID) != ""
		serviceAllowed := principal.ServiceID != "" && (principal.Audience == "shared_room" || publicationService) &&
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
	// Public/company chat remains a durable UI-state source. Only this
	// request-local, ledger-authorized join makes its current message bodies
	// searchable, keeping private Scout threads out of every recall lane.
	for _, entry := range app.authorizedSTRIDEConversationEntries(principal) {
		filtered.entries = append(filtered.entries, entry)
		filtered.seen[entry.ID] = struct{}{}
	}
	sort.SliceStable(filtered.entries, func(i, j int) bool {
		if !filtered.entries[i].CreatedAt.Equal(filtered.entries[j].CreatedAt) {
			return filtered.entries[i].CreatedAt.Before(filtered.entries[j].CreatedAt)
		}
		return filtered.entries[i].ID < filtered.entries[j].ID
	})
	filtered.rebuildMeetingEntryIndexesLocked()
	return filtered
}

// meetingRecordStoreForPrincipal is the bounded Meeting Record read lane. It
// preserves the exact principal and artifact-header authorization used by
// general recall, but it only copies rows belonging to the requested meetings.
// Callers choose which kinds require bodies; index callers copy metadata for
// transcript rows and only the small analysis digest bodies needed to label a
// row, while detail callers copy bodies for one exact meeting.
func (app *kanbanBoardApp) meetingRecordStoreForPrincipal(ctx context.Context, principal RecallPrincipal, meetingIDs map[string]struct{}, includeBody func(string) bool) *meetingMemoryStore {
	filtered := &meetingMemoryStore{seen: map[string]struct{}{}, meetingEntryIndexes: map[string][]int{}, meetingIDs: map[string]string{}, bootLatestIDs: map[string]string{}, bootLatestRoomIDs: map[string]map[string]string{}}
	if app == nil || app.memory == nil || len(meetingIDs) == 0 {
		return filtered
	}
	type artifactCandidate struct {
		index     int
		id        string
		meetingID string
		header    ArtifactAuthorizationHeader
	}
	artifacts := []artifactCandidate{}
	ordered := map[int]meetingMemoryEntry{}
	app.memory.mu.Lock()
	selectedIndexes := make(map[int]struct{})
	for meetingID := range meetingIDs {
		for _, index := range app.memory.meetingEntryIndexes[strings.TrimSpace(meetingID)] {
			selectedIndexes[index] = struct{}{}
		}
	}
	indexes := make([]int, 0, len(selectedIndexes))
	for index := range selectedIndexes {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		if index < 0 || index >= len(app.memory.entries) {
			continue
		}
		if app.memory.meetingEntryVisitHook != nil {
			app.memory.meetingEntryVisitHook()
		}
		stored := app.memory.entries[index]
		meetingID := strings.TrimSpace(stored.Metadata["meetingId"])
		if meetingID == "" || !meetingRecordMeetingWanted(meetingIDs, meetingID) || memoryEntryHiddenFromRecall(stored) || !recallEntryScopeAllowed(stored.Metadata, principal) {
			continue
		}
		if stored.Kind == meetingMemoryKindOSArtifact {
			header := app.memory.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(meetingMemoryEntry{ID: stored.ID, Kind: stored.Kind, Metadata: stored.Metadata}))
			artifacts = append(artifacts, artifactCandidate{index: index, id: stored.ID, meetingID: meetingID, header: header})
			continue
		}
		entry := meetingMemoryEntry{ID: stored.ID, Kind: stored.Kind, CreatedAt: stored.CreatedAt, Metadata: cloneMeetingRecordMetadata(stored.Metadata), BodyDigest: stored.BodyDigest}
		if includeBody != nil && includeBody(stored.Kind) {
			entry.Text = stored.Text
		}
		ordered[index] = entry
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
		if !found || strings.TrimSpace(entry.Metadata["meetingId"]) != candidate.meetingID || !recallEntryScopeAllowed(entry.Metadata, principal) {
			continue
		}
		if includeBody == nil || !includeBody(entry.Kind) {
			entry.Text = ""
		}
		ordered[candidate.index] = entry
	}
	for _, index := range indexes {
		entry, ok := ordered[index]
		if !ok {
			continue
		}
		filtered.entries = append(filtered.entries, entry)
		filtered.indexMeetingEntryLocked(len(filtered.entries)-1, entry)
		filtered.seen[entry.ID] = struct{}{}
	}
	return filtered
}

// meetingBriefingStoreForPrincipal is the bounded, current-source lane used by
// conversational meeting recap. It copies only authorized rows indexed to the
// requested calendar days: raw transcript/brain/decision sources plus current
// meeting/day digests. Unrelated ledger history is never visited or cloned.
func (app *kanbanBoardApp) meetingBriefingStoreForPrincipal(principal RecallPrincipal, start, end time.Time) *meetingMemoryStore {
	filtered := &meetingMemoryStore{
		seen: map[string]struct{}{}, meetingEntryIndexes: map[string][]int{},
		meetingIDs: map[string]string{}, bootLatestIDs: map[string]string{}, bootLatestRoomIDs: map[string]map[string]string{},
	}
	if app == nil || app.memory == nil || !end.After(start) {
		return filtered
	}
	days, indexed := digestQueryDays(start, end)
	if !indexed {
		return filtered
	}
	location := meetingTimeLocation()
	indexes := map[int]struct{}{}
	app.memory.mu.Lock()
	for _, day := range days {
		for _, index := range app.memory.briefingSourceDayIndexes[day] {
			indexes[index] = struct{}{}
		}
		for _, kind := range []string{meetingMemoryKindMeetingDigest, meetingMemoryKindDayDigest} {
			for _, index := range app.memory.currentDigestDayIndexes[kind][day] {
				indexes[index] = struct{}{}
			}
		}
	}
	ordered := make([]int, 0, len(indexes))
	for index := range indexes {
		ordered = append(ordered, index)
	}
	sort.Ints(ordered)
	for _, index := range ordered {
		if index < 0 || index >= len(app.memory.entries) {
			continue
		}
		if app.memory.authorizationEntryVisitHook != nil {
			app.memory.authorizationEntryVisitHook()
		}
		entry := app.memory.entries[index]
		if isMeetingDigestKind(entry.Kind) {
			if app.memory.digestEntryVisitHook != nil {
				app.memory.digestEntryVisitHook()
			}
			if !digestEntryCurrent(entry) || memoryEntryHiddenFromRecall(entry) {
				continue
			}
			spanStart, spanEnd := digestSpan(entry, location)
			if !spanStart.Before(end) || spanEnd.Before(start) {
				continue
			}
		} else {
			if app.memory.briefingEntryVisitHook != nil {
				app.memory.briefingEntryVisitHook()
			}
			if !briefingRangeSourceKind(entry.Kind) || entry.CreatedAt.Before(start) || !entry.CreatedAt.Before(end) {
				continue
			}
			if entry.Kind != meetingMemoryKindDeadLetter && memoryEntryHiddenFromRecall(entry) {
				continue
			}
		}
		if !recallEntryScopeAllowed(entry.Metadata, principal) {
			continue
		}
		cloned := cloneMemoryEntry(entry)
		filtered.entries = append(filtered.entries, cloned)
		filtered.seen[cloned.ID] = struct{}{}
	}
	app.memory.mu.Unlock()
	filtered.rebuildMeetingEntryIndexesLocked()
	return filtered
}

func meetingRecordMeetingWanted(wanted map[string]struct{}, meetingID string) bool {
	_, ok := wanted[meetingID]
	return ok
}

func cloneMeetingRecordMetadata(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
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
