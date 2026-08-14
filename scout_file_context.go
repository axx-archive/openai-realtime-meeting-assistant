package main

// Scout file context closes the gap between the Files surface and Scout's
// retrieval lane. Files is an ACL-filtered catalog; these helpers resolve an
// exact catalog row back to readable content without ever trusting a filename
// supplied by the client. Exact refs can then ride a chat proposal into the
// worker that was explicitly approved.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const scoutFileContextLimit = 4

// ErrAgentThreadSourceChanged is the provider-admission fence for durable
// agent work. A context ref is only an audit binding to a server-resolved File;
// it is never continuing authority. Every runner must resolve the ref again as
// the original requester immediately before provider admission.
var ErrAgentThreadSourceChanged = errors.New("agent work source changed or is no longer authorized")

type assistantContextRefsContextKey struct{}

func assistantFilePrincipalCanRead(principal RecallPrincipal) bool {
	member := principal.User != nil && accountStore().findUser(principal.User.Email) != nil
	sharedService := principal.Audience == "shared_room" && strings.TrimSpace(principal.ServiceID) != ""
	return member || sharedService
}

func withAssistantContextRefs(ctx context.Context, refs []string) context.Context {
	refs = canonicalAssistantContextRefs(refs)
	if len(refs) == 0 {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, assistantContextRefsContextKey{}, refs)
}

func assistantContextRefs(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	refs, _ := ctx.Value(assistantContextRefsContextKey{}).([]string)
	return canonicalAssistantContextRefs(refs)
}

func canonicalAssistantContextRefs(refs []string) []string {
	seen := make(map[string]struct{}, len(refs))
	cleaned := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if _, duplicate := seen[ref]; duplicate {
			continue
		}
		seen[ref] = struct{}{}
		cleaned = append(cleaned, ref)
	}
	return cleaned
}

func encodeAssistantContextRefs(refs []string) string {
	refs = canonicalAssistantContextRefs(refs)
	if len(refs) == 0 {
		return ""
	}
	encoded, err := json.Marshal(refs)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func decodeAssistantContextRefs(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var refs []string
	if json.Unmarshal([]byte(value), &refs) == nil {
		return canonicalAssistantContextRefs(refs)
	}
	return canonicalAssistantContextRefs(strings.Split(value, ","))
}

func assistantFileContextRef(fileID string) string {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return ""
	}
	return "file|" + fileID
}

func scoutChatFileContextRef(threadID string, messageID string, index int) string {
	if strings.TrimSpace(threadID) == "" || strings.TrimSpace(messageID) == "" || index < 0 {
		return ""
	}
	return fmt.Sprintf("chatfile|%s|%s|%d", strings.TrimSpace(threadID), strings.TrimSpace(messageID), index)
}

func normalizeScoutFileSearch(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, ".pdf")
	value = strings.TrimSuffix(value, ".pptx")
	value = strings.TrimSuffix(value, ".key")
	value = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return r
		}
		return ' '
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

var scoutFileSearchStopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "at": true,
	"deck": true, "document": true, "file": true, "files": true, "in": true,
	"it": true, "of": true, "on": true, "open": true, "our": true, "pdf": true,
	"please": true, "read": true, "review": true, "scout": true, "the": true,
	"this": true, "to": true, "what": true, "you": true,
}

func scoutFileSearchTokens(value string) []string {
	var tokens []string
	seen := map[string]bool{}
	for _, token := range strings.Fields(normalizeScoutFileSearch(value)) {
		if len([]rune(token)) < 2 || scoutFileSearchStopWords[token] || seen[token] {
			continue
		}
		seen[token] = true
		tokens = append(tokens, token)
	}
	return tokens
}

func scoutFileNameScore(query string, name string) int {
	queryNorm := normalizeScoutFileSearch(query)
	nameNorm := normalizeScoutFileSearch(name)
	if queryNorm == "" || nameNorm == "" {
		return 0
	}
	if queryNorm == nameNorm {
		return 100
	}
	if strings.Contains(queryNorm, nameNorm) {
		return 80
	}
	score := 0
	for _, token := range scoutFileSearchTokens(nameNorm) {
		if strings.Contains(" "+queryNorm+" ", " "+token+" ") {
			score += 12
		}
	}
	return score
}

func scoutQueryAsksForFilesCatalog(query string) bool {
	normalized := normalizeScoutFileSearch(query)
	for _, phrase := range []string{"drive", "files tab", "our files", "uploaded files", "what files", "which files", "find a file", "find the file", "look in files", "search files"} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

func (app *kanbanBoardApp) assistantFileRecordContextEntry(ctx context.Context, principal RecallPrincipal, row assistantFileRecord) (meetingMemoryEntry, bool) {
	if app == nil || app.memory == nil || strings.TrimSpace(row.ID) == "" || !assistantFilePrincipalCanRead(principal) {
		return meetingMemoryEntry{}, false
	}
	// Direct Drive uploads and saved deliverables already live in the scoped
	// recall store. Re-read from that ACL-filtered snapshot instead of copying
	// the Files row's body-free metadata.
	scoped := app.recallStoreForPrincipal(ctx, principal)
	entryID := row.ID
	if strings.TrimSpace(row.ArtifactID) != "" {
		entryID = row.ArtifactID
	}
	for _, entry := range scoped.entries {
		if entry.ID != entryID {
			continue
		}
		if row.Origin == "files" && row.BrainStatus != fileBrainStatusIngested {
			return meetingMemoryEntry{}, false
		}
		if strings.TrimSpace(entry.Text) == "" {
			return meetingMemoryEntry{}, false
		}
		copy := cloneMemoryEntry(entry)
		if copy.Metadata == nil {
			copy.Metadata = map[string]string{}
		}
		copy.Metadata["title"] = firstNonEmptyString(strings.TrimSpace(row.Name), copy.Metadata["title"])
		copy.Metadata["filesSurface"] = "true"
		return copy, true
	}

	// Chat attachments are adapted rows rather than first-class memory entries.
	// Resolve them through the same viewer-aware thread projection the Files
	// endpoint uses, then synthesize an in-request context entry only when the
	// server-derived text exists.
	if row.Origin != "chat" || strings.TrimSpace(row.OriginThreadID) == "" {
		return meetingMemoryEntry{}, false
	}
	viewerEmail := ""
	if principal.User != nil {
		viewerEmail = principal.User.Email
	}
	thread, _, err := app.scoutChatThreadByID(viewerEmail, row.OriginThreadID)
	if err != nil {
		return meetingMemoryEntry{}, false
	}
	thread = app.projectScoutChatThreadForViewer(viewerEmail, thread)
	for _, message := range thread.Messages {
		for index, file := range message.Files {
			rowID := fmt.Sprintf("%s:%s:%d", thread.ID, message.ID, index)
			if rowID != row.ID || strings.TrimSpace(file.Text) == "" {
				continue
			}
			return meetingMemoryEntry{
				ID:        "files-context-" + temporalDigest(row.ID)[:20],
				Kind:      meetingMemoryKindFile,
				Text:      fmt.Sprintf("File %s from %s: %s", file.Name, firstNonEmptyString(thread.Title, "chat"), file.Text),
				CreatedAt: parseRFC3339OrZero(message.CreatedAt),
				Metadata: map[string]string{
					"title":          firstNonEmptyString(strings.TrimSpace(file.Name), "file"),
					"filesSurface":   "true",
					"originThreadId": thread.ID,
				},
			}, true
		}
	}
	return meetingMemoryEntry{}, false
}

func parseRFC3339OrZero(value string) (parsed time.Time) {
	parsed, _ = time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if parsed.IsZero() {
		parsed, _ = time.Parse(time.RFC3339, strings.TrimSpace(value))
	}
	return parsed
}

func (app *kanbanBoardApp) assistantContextEntryForRef(ctx context.Context, principal RecallPrincipal, ref string) (meetingMemoryEntry, bool) {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, meetingRangeContextRefPrefix+"|") {
		return app.meetingRangeContextEntry(ctx, principal, ref)
	}
	if parts := strings.Split(ref, "|"); len(parts) == 3 && parts[0] == meetingRecordContextRefVersion {
		if principal.User == nil {
			return meetingMemoryEntry{}, false
		}
		projection, found := app.meetingRecordProjectionForPrincipal(ctx, principal, parts[1])
		if !found || projection.index.RecordRevision != parts[2] || len(projection.segments) == 0 {
			return meetingMemoryEntry{}, false
		}
		lines := []string{
			"Exact Meeting Record transcript truth.",
			"meeting_id=" + projection.index.ID,
			"record_revision=" + projection.index.RecordRevision,
			"coverage=" + projection.index.CoverageState,
		}
		for _, gap := range meetingRecordCoverageForProjection(projection).Gaps {
			lines = append(lines, "coverage_gap="+gap)
		}
		for _, segment := range projection.segments {
			line := fmt.Sprintf("[segment:%s] %s · %s: %s", segment.ID, segment.At, firstNonEmptyString(segment.Speaker, "Unknown speaker"), trimForStorage(segment.Text, 1200))
			candidate := strings.Join(append(lines, line), "\n")
			if len(candidate) > maxPromptBodyBytes {
				lines = append(lines, "[remaining authorized transcript omitted from this bounded worker context]")
				break
			}
			lines = append(lines, line)
		}
		return meetingMemoryEntry{
			ID: "meeting-record-context-" + temporalDigest(ref)[:20], Kind: meetingMemoryKindMeetingDigest,
			Text: strings.Join(lines, "\n"), CreatedAt: parseRFC3339OrZero(projection.index.StartedAt),
			Metadata: map[string]string{
				"title": projection.index.Title, "meetingId": projection.index.ID, "recordRevision": projection.index.RecordRevision,
				"visibility": "private", "ownerEmail": normalizeAccountEmail(principal.User.Email), "meetingRecordContext": "true",
			},
		}, true
	}
	if strings.HasPrefix(ref, "file|") {
		fileID := strings.TrimSpace(strings.TrimPrefix(ref, "file|"))
		viewer := principal.User
		for _, row := range app.assistantFilesForPrincipal(ctx, viewer) {
			if row.ID == fileID {
				return app.assistantFileRecordContextEntry(ctx, principal, row)
			}
		}
		return meetingMemoryEntry{}, false
	}
	parts := strings.Split(ref, "|")
	if len(parts) != 4 || parts[0] != "chatfile" {
		return meetingMemoryEntry{}, false
	}
	index, err := strconv.Atoi(parts[3])
	if err != nil || index < 0 {
		return meetingMemoryEntry{}, false
	}
	viewerEmail := ""
	if principal.User != nil {
		viewerEmail = principal.User.Email
	}
	thread, _, err := app.scoutChatThreadByID(viewerEmail, parts[1])
	if err != nil {
		return meetingMemoryEntry{}, false
	}
	thread = app.projectScoutChatThreadForViewer(viewerEmail, thread)
	messageIndex := scoutChatMessageIndex(thread, parts[2])
	if messageIndex < 0 || index >= len(thread.Messages[messageIndex].Files) {
		return meetingMemoryEntry{}, false
	}
	message := thread.Messages[messageIndex]
	file := message.Files[index]
	if strings.TrimSpace(file.Text) == "" {
		return meetingMemoryEntry{}, false
	}
	return meetingMemoryEntry{
		ID:        "files-context-" + temporalDigest(ref)[:20],
		Kind:      meetingMemoryKindFile,
		Text:      fmt.Sprintf("File %s from %s: %s", file.Name, firstNonEmptyString(thread.Title, "chat"), file.Text),
		CreatedAt: parseRFC3339OrZero(message.CreatedAt),
		Metadata: map[string]string{
			"title":          firstNonEmptyString(strings.TrimSpace(file.Name), "file"),
			"filesSurface":   "true",
			"originThreadId": thread.ID,
		},
	}, true
}

func appendUniqueFileContextEntries(primary []meetingMemoryEntry, existing []meetingMemoryEntry) []meetingMemoryEntry {
	seen := make(map[string]bool, len(primary)+len(existing))
	merged := make([]meetingMemoryEntry, 0, len(primary)+len(existing))
	for _, group := range [][]meetingMemoryEntry{primary, existing} {
		for _, entry := range group {
			if strings.TrimSpace(entry.ID) == "" || seen[entry.ID] {
				continue
			}
			seen[entry.ID] = true
			merged = append(merged, entry)
		}
	}
	return merged
}

func (app *kanbanBoardApp) assistantFileContextEntries(ctx context.Context, principal RecallPrincipal, query string) []meetingMemoryEntry {
	if app == nil || app.memory == nil || !assistantFilePrincipalCanRead(principal) {
		return nil
	}
	entries := make([]meetingMemoryEntry, 0, scoutFileContextLimit+1)
	for _, ref := range assistantContextRefs(ctx) {
		if entry, ok := app.assistantContextEntryForRef(ctx, principal, ref); ok {
			entries = append(entries, entry)
		}
	}
	if len(entries) >= scoutFileContextLimit {
		return entries[:scoutFileContextLimit]
	}

	viewer := principal.User
	rows := app.assistantFilesForPrincipal(ctx, viewer)
	if scoutQueryAsksForFilesCatalog(query) {
		lines := []string{"Authorized Files catalog:"}
		for index, row := range rows {
			if index >= 40 {
				break
			}
			lines = append(lines, fmt.Sprintf("- %s (%s, %s)", row.Name, firstNonEmptyString(row.BrainStatus, "stored"), row.Origin))
		}
		if len(lines) == 1 {
			lines = append(lines, "- No files are currently visible to this account.")
		}
		entries = append(entries, meetingMemoryEntry{ID: "files-catalog-context", Kind: meetingMemoryKindFile, Text: strings.Join(lines, "\n"), Metadata: map[string]string{"title": "Files catalog", "filesSurface": "true"}})
	}

	type scoredRow struct {
		row   assistantFileRecord
		score int
	}
	var scored []scoredRow
	for _, row := range rows {
		if score := scoutFileNameScore(query, row.Name); score > 0 {
			scored = append(scored, scoredRow{row: row, score: score})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
	for _, candidate := range scored {
		if len(entries) >= scoutFileContextLimit {
			break
		}
		if entry, ok := app.assistantFileRecordContextEntry(ctx, principal, candidate.row); ok {
			entries = append(entries, entry)
		}
	}
	return appendUniqueFileContextEntries(entries, nil)
}

func (app *kanbanBoardApp) readableAssistantFileByName(ctx context.Context, user *userAccount, name string) (assistantFileRecord, string, bool) {
	if app == nil || user == nil {
		return assistantFileRecord{}, "", false
	}
	principal := recallPrincipalForUser(user)
	type candidate struct {
		row   assistantFileRecord
		score int
	}
	var candidates []candidate
	for _, row := range app.assistantFilesForPrincipal(ctx, user) {
		if score := scoutFileNameScore(name, row.Name); score > 0 {
			candidates = append(candidates, candidate{row: row, score: score})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	for _, candidate := range candidates {
		if candidate.score < 24 {
			continue
		}
		if _, ok := app.assistantFileRecordContextEntry(ctx, principal, candidate.row); ok {
			return candidate.row, assistantFileContextRef(candidate.row.ID), true
		}
	}
	return assistantFileRecord{}, "", false
}

func (app *kanbanBoardApp) agentThreadRecallPrincipal(requester string, metadata map[string]string) (RecallPrincipal, bool) {
	user, ok := authenticatedRequester(requester)
	if !ok {
		return RecallPrincipal{}, false
	}
	principal := recallPrincipalForUser(user)
	switch strings.TrimSpace(metadata["originKind"]) {
	case agentThreadOriginChannel:
		threadID := strings.TrimSpace(metadata["originId"])
		thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
		if err != nil || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic || thread.ArchivedAt != "" {
			return RecallPrincipal{}, false
		}
		principal.Audience = "shared_channel"
		principal.ThreadID = thread.ID
	case agentThreadOriginPrivateThread:
		threadID := strings.TrimSpace(metadata["originId"])
		thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
		if err != nil || scoutChatThreadVisibility(thread) != scoutChatVisibilityPrivate || thread.ArchivedAt != "" {
			return RecallPrincipal{}, false
		}
		principal.ThreadID = thread.ID
	case agentThreadOriginRoom:
		roomID := normalizeRoomID(firstNonEmptyString(metadata["originRoomId"], officeRoomID))
		principal.Audience = "shared_room"
		principal.RoomID = roomID
		principal.SittingID = firstNonEmptyString(strings.TrimSpace(metadata["originMeetingId"]), app.memory.currentMeetingID(roomID))
	}
	return principal, true
}

func (app *kanbanBoardApp) agentThreadMemory(ctx context.Context, requester string, metadata map[string]string, refsValue string, limit int) []meetingMemoryEntry {
	principal, ok := app.agentThreadRecallPrincipal(requester, metadata)
	if !ok {
		return nil
	}
	base := activeAgentMemory(app.memorySnapshotForPrincipal(ctx, principal, limit))
	refCtx := withAssistantContextRefs(ctx, decodeAssistantContextRefs(refsValue))
	pinned := app.assistantFileContextEntries(refCtx, principal, "")
	if strings.TrimSpace(metadata["originKind"]) == agentThreadOriginChannel || strings.TrimSpace(metadata["originKind"]) == agentThreadOriginRoom {
		shared := pinned[:0]
		for _, entry := range pinned {
			if app.agentThreadEntryAuthorizedForDestination(ctx, metadata, entry) {
				shared = append(shared, entry)
			}
		}
		pinned = append([]meetingMemoryEntry(nil), shared...)
	}
	return activeAgentMemory(appendUniqueFileContextEntries(pinned, base))
}

// currentMeetingDigestContext adds the latest authorized cumulative recap to
// a provider-admission snapshot. Digest records are intentionally omitted from
// the generic client memory rail, but an eligible Scout/coworker should still
// know the current meeting state when answering or starting work. The store is
// principal-scoped first, so private or other-room material cannot widen the
// job. A room-launched job is pinned to its exact sitting; other eligible jobs
// receive at most the three newest active meetings they are authorized to read.
func (app *kanbanBoardApp) currentMeetingDigestContext(ctx context.Context, principal RecallPrincipal, metadata map[string]string) []meetingMemoryEntry {
	if app == nil || app.meetings == nil || app.memory == nil {
		return nil
	}
	scoped := app.scopedRecallApp(ctx, principal)
	if scoped == nil || scoped.memory == nil {
		return nil
	}
	digests := scoped.memory.latestDigestPerMeeting()
	if meetingID := strings.TrimSpace(metadata["originMeetingId"]); meetingID != "" {
		if digest, ok := digests[meetingID]; ok {
			return []meetingMemoryEntry{digest}
		}
		return nil
	}

	app.meetings.mu.Lock()
	records := append([]meetingRecord(nil), app.meetings.records...)
	app.meetings.mu.Unlock()
	current := make([]meetingMemoryEntry, 0, 3)
	for index := len(records) - 1; index >= 0 && len(current) < 3; index-- {
		record := records[index]
		if strings.TrimSpace(record.EndedAt) != "" {
			continue
		}
		if digest, ok := digests[strings.TrimSpace(record.ID)]; ok {
			current = append(current, digest)
		}
	}
	return current
}

// agentThreadSourceConversationEntries resolves the exact, currently
// authorized public-channel or private-thread transcript ending at the human
// message that minted the work. It deliberately excludes later conversation:
// a run is bound to what the requester could see and approved, not whatever
// happened to land while the provider was working.
func (app *kanbanBoardApp) agentThreadSourceConversationEntries(principal RecallPrincipal, metadata map[string]string) ([]meetingMemoryEntry, error) {
	sourceMessageID := strings.TrimSpace(metadata["sourceMessageId"])
	if sourceMessageID == "" {
		return nil, nil
	}
	threadID := strings.TrimSpace(metadata["originId"])
	originKind := strings.TrimSpace(metadata["originKind"])
	if (originKind != agentThreadOriginChannel && originKind != agentThreadOriginPrivateThread) || threadID == "" || principal.ThreadID != threadID {
		return nil, fmt.Errorf("%w: the originating conversation is unavailable", ErrAgentThreadSourceChanged)
	}
	if principal.User == nil || normalizeAccountEmail(principal.User.Email) == "" {
		return nil, fmt.Errorf("%w: the originating requester is unavailable", ErrAgentThreadSourceChanged)
	}
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	thread, _, threadErr := app.scoutChatThreadByID(principal.User.Email, threadID)
	wantVisibility := scoutChatVisibilityPublic
	if originKind == agentThreadOriginPrivateThread {
		wantVisibility = scoutChatVisibilityPrivate
	}
	if threadErr != nil || thread.ArchivedAt != "" || scoutChatThreadVisibility(thread) != wantVisibility {
		lock.Unlock()
		return nil, fmt.Errorf("%w: the originating conversation is unavailable", ErrAgentThreadSourceChanged)
	}
	messages, binding, bindingErr := scoutChatSourceWindow(thread, sourceMessageID)
	lock.Unlock()
	if bindingErr != nil || binding.MessageDigest != strings.TrimSpace(metadata["sourceMessageDigest"]) || binding.WindowDigest != strings.TrimSpace(metadata["sourceWindowDigest"]) {
		return nil, fmt.Errorf("%w: the approved source conversation changed", ErrAgentThreadSourceChanged)
	}

	window := make([]meetingMemoryEntry, 0, agentThreadSourceConversationWindow)
	for _, message := range messages {
		if strings.TrimSpace(message.ID) == "" {
			continue
		}
		digest, digestErr := scoutChatSourceMessageDigest(thread, message)
		if digestErr != nil {
			return nil, fmt.Errorf("%w: a source message is invalid", ErrAgentThreadSourceChanged)
		}
		createdAt, timeErr := parseSTRIDEChatTime(message.CreatedAt)
		if timeErr != nil {
			return nil, fmt.Errorf("%w: a source message timestamp is invalid", ErrAgentThreadSourceChanged)
		}
		entry := strideConversationRecallEntry(thread, message, STRIDEConversationMessageProjection{
			TenantID:   canonicalTenantID(),
			ThreadID:   thread.ID,
			SourceID:   message.ID,
			AuthorName: message.AuthorName,
			LatestEvent: STRIDEReference{
				ID:     "chat-source-" + digest,
				Digest: digest,
			},
		}, createdAt)
		window = append(window, entry)
	}
	return append([]meetingMemoryEntry(nil), window...), nil
}

// withCurrentAgentThreadSource holds the originating chat's mutation lock from
// the exact approval-digest recheck through the authoritative artifact write.
// Provider admission alone is not enough: a rename, edit, delete, archive, or
// audience change must not race between the final check and publication.
func (app *kanbanBoardApp) withCurrentAgentThreadSource(thread scoutAgentThread, effect func() error) error {
	metadata := thread.Artifact.Metadata
	sourceMessageID := strings.TrimSpace(metadata["sourceMessageId"])
	if sourceMessageID == "" {
		return effect()
	}
	threadID := strings.TrimSpace(metadata["originId"])
	requester := normalizeAccountEmail(metadata["requestedBy"])
	originKind := strings.TrimSpace(metadata["originKind"])
	if (originKind != agentThreadOriginChannel && originKind != agentThreadOriginPrivateThread) || threadID == "" || requester == "" {
		return fmt.Errorf("%w: the originating conversation is unavailable", ErrAgentThreadSourceChanged)
	}
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()
	current, _, err := app.scoutChatThreadByID(requester, threadID)
	wantVisibility := scoutChatVisibilityPublic
	if originKind == agentThreadOriginPrivateThread {
		wantVisibility = scoutChatVisibilityPrivate
	}
	if err != nil || current.ArchivedAt != "" || scoutChatThreadVisibility(current) != wantVisibility {
		return fmt.Errorf("%w: the originating conversation is unavailable", ErrAgentThreadSourceChanged)
	}
	if current.MeetingRecord != nil && !app.meetingRecordConversationBindingCurrent(requester, current) {
		return fmt.Errorf("%w: the originating Meeting Record changed", ErrAgentThreadSourceChanged)
	}
	_, binding, err := scoutChatSourceWindow(current, sourceMessageID)
	if err != nil || binding.MessageDigest != strings.TrimSpace(metadata["sourceMessageDigest"]) || binding.WindowDigest != strings.TrimSpace(metadata["sourceWindowDigest"]) {
		return fmt.Errorf("%w: the approved source conversation changed", ErrAgentThreadSourceChanged)
	}
	if raw := strings.TrimSpace(metadata[projectWorkBindingMetadataKey]); raw != "" {
		projectBinding, ok := decodeProjectWorkBinding(metadata)
		messageIndex := scoutChatMessageIndex(current, projectBinding.MessageID)
		if !ok || !projectWorkBindingMatchesThread(projectBinding, current) || messageIndex < 0 ||
			!projectWorkBindingCanonicalCurrent(context.Background(), currentHomeProjectStore(), projectBinding, current.Messages[messageIndex].Project) {
			return fmt.Errorf("%w: the linked Project or its source turn changed", ErrAgentThreadSourceChanged)
		}
	}
	if strings.TrimSpace(metadata[workstreamAffinityMetadataKey]) != "" && !app.workstreamAffinityCurrent(context.Background(), thread.Artifact) {
		return fmt.Errorf("%w: the inferred workstream changed", ErrAgentThreadSourceChanged)
	}
	return effect()
}

// agentThreadEntryAuthorizedForDestination proves that a selected source can
// be read by the audience that will receive the completed work. Requester read
// access is necessary but not sufficient for a shared delivery: a private
// artifact or project-channel attachment must not be laundered into a broader
// channel/meeting through an agent prompt.
func (app *kanbanBoardApp) agentThreadEntryAuthorizedForDestination(ctx context.Context, metadata map[string]string, entry meetingMemoryEntry) bool {
	originKind := strings.TrimSpace(metadata["originKind"])
	if originKind != agentThreadOriginChannel && originKind != agentThreadOriginRoom {
		return true
	}
	visibility := strings.ToLower(strings.TrimSpace(entry.Metadata["visibility"]))
	if visibility == "private" || visibility == "owner" {
		return false
	}

	var destinationEmails []string
	if originKind == agentThreadOriginChannel {
		threadID := strings.TrimSpace(metadata["originId"])
		requester := normalizeAccountEmail(metadata["requestedBy"])
		destination, _, err := app.scoutChatThreadByID(requester, threadID)
		if err != nil || scoutChatThreadVisibility(destination) != scoutChatVisibilityPublic || destination.ArchivedAt != "" {
			return false
		}
		if scoutChatThreadIsOrganizationPublic(destination) {
			for _, seed := range seededAccounts {
				destinationEmails = append(destinationEmails, normalizeAccountEmail(seed.Email))
			}
		} else {
			destinationEmails = scoutChatThreadMemberEmails(destination)
		}
		if sourceThreadID := strings.TrimSpace(entry.Metadata["originThreadId"]); sourceThreadID != "" {
			source, _, sourceErr := app.scoutChatThreadByID(requester, sourceThreadID)
			if sourceErr != nil || scoutChatThreadVisibility(source) != scoutChatVisibilityPublic || source.ArchivedAt != "" {
				return false
			}
			for _, email := range destinationEmails {
				if !scoutChatThreadAllowsViewer(source, email) {
					return false
				}
			}
		}
	} else {
		roomID := normalizeRoomID(firstNonEmptyString(metadata["originRoomId"], officeRoomID))
		for _, name := range app.participantSnapshotForRoom(roomID) {
			if email := participantEmail(name); email != "" {
				destinationEmails = append(destinationEmails, email)
			}
		}
		if sourceThreadID := strings.TrimSpace(entry.Metadata["originThreadId"]); sourceThreadID != "" {
			requester := normalizeAccountEmail(metadata["requestedBy"])
			source, _, sourceErr := app.scoutChatThreadByID(requester, sourceThreadID)
			if sourceErr != nil || !scoutChatThreadIsOrganizationPublic(source) || source.ArchivedAt != "" {
				return false
			}
		}
	}

	if entry.Kind != meetingMemoryKindOSArtifact {
		return true
	}
	header := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(entry))
	if !strings.EqualFold(strings.TrimSpace(header.Visibility), "organization") && len(destinationEmails) == 0 {
		return false
	}
	for _, email := range destinationEmails {
		user := accountStore().findUser(email)
		if user == nil || !artifactHeaderAuthorized(ctx, user, ACLReadContent, header) {
			return false
		}
	}
	return true
}

// agentThreadProviderContext resolves the exact source bindings carried by a
// launched work thread under the requester's current tenant/File ACL. It is the
// single admission seam shared by OpenAI, Anthropic, and selectable Codex
// runners, so no provider can accidentally receive a stale launch-time source
// or a different memory snapshot.
//
// Missing, deleted, unreadable, tenant-mismatched, or newly private refs all
// fail closed. The caller turns the returned error into a durable
// needs-attention work card; it must never silently continue with ambient
// memory only.
func (app *kanbanBoardApp) agentThreadProviderContext(ctx context.Context, thread scoutAgentThread) (AgentJobContext, error) {
	if app == nil {
		return AgentJobContext{}, fmt.Errorf("%w: assistant is unavailable", ErrAgentThreadSourceChanged)
	}
	metadata := thread.Artifact.Metadata
	if !app.projectBoundArtifactCurrent(ctx, thread.Artifact) {
		return AgentJobContext{}, fmt.Errorf("%w: the linked Project or its source turn changed; review the conversation and retry", ErrAgentThreadSourceChanged)
	}
	if strings.TrimSpace(metadata["goalParentId"]) != "" {
		if err := app.verifyGoalChildRoute(thread.Artifact); err != nil {
			return AgentJobContext{}, err
		}
	}
	requester := firstNonEmptyString(strings.TrimSpace(metadata["requestedBy"]), strings.TrimSpace(metadata["createdBy"]))
	principal, ok := app.agentThreadRecallPrincipal(requester, metadata)
	var base []meetingMemoryEntry
	if ok {
		base = activeAgentMemory(app.memorySnapshotForPrincipal(ctx, principal, 20))
		base = appendUniqueFileContextEntries(app.currentMeetingDigestContext(ctx, principal, metadata), base)
		sourceEntries, sourceErr := app.agentThreadSourceConversationEntries(principal, metadata)
		if sourceErr != nil {
			return AgentJobContext{}, sourceErr
		}
		base = appendUniqueFileContextEntries(sourceEntries, base)
	}
	context := AgentJobContext{Memory: base}
	refs := decodeAssistantContextRefs(metadata["contextRefs"])
	sharedOrigin := strings.TrimSpace(metadata["originKind"]) == agentThreadOriginChannel || strings.TrimSpace(metadata["originKind"]) == agentThreadOriginRoom
	if !ok && (len(refs) > 0 || sharedOrigin) {
		return AgentJobContext{}, fmt.Errorf("%w: the original requester or destination is no longer authorized; ask them to retry", ErrAgentThreadSourceChanged)
	}
	if len(refs) == 0 {
		return context, nil
	}
	if len(refs) > scoutFileContextLimit {
		return AgentJobContext{}, fmt.Errorf("%w: select no more than %d Files and retry", ErrAgentThreadSourceChanged, scoutFileContextLimit)
	}
	pinned := make([]meetingMemoryEntry, 0, len(refs))
	for _, ref := range refs {
		entry, readable := app.assistantContextEntryForRef(ctx, principal, ref)
		if !readable {
			return AgentJobContext{}, fmt.Errorf("%w: a referenced File is missing, unreadable, or its access was revoked; reselect or reattach the source and retry", ErrAgentThreadSourceChanged)
		}
		if !app.agentThreadEntryAuthorizedForDestination(ctx, metadata, entry) {
			return AgentJobContext{}, fmt.Errorf("%w: a referenced File is not readable by the work's current destination audience; share it there or choose a different source", ErrAgentThreadSourceChanged)
		}
		pinned = append(pinned, entry)
	}
	context.Memory = activeAgentMemory(appendUniqueFileContextEntries(pinned, base))
	return context, nil
}

func (app *kanbanBoardApp) assistantContextRefsReadable(ctx context.Context, user *userAccount, refsValue string) bool {
	refs := decodeAssistantContextRefs(refsValue)
	if len(refs) == 0 {
		return true
	}
	if app == nil || user == nil {
		return false
	}
	principal := recallPrincipalForUser(user)
	resolved := app.assistantFileContextEntries(withAssistantContextRefs(ctx, refs), principal, "")
	return len(resolved) == len(refs)
}
