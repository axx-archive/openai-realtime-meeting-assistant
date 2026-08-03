package main

// Scout file context closes the gap between the Files surface and Scout's
// retrieval lane. Files is an ACL-filtered catalog; these helpers resolve an
// exact catalog row back to readable content without ever trusting a filename
// supplied by the client. Exact refs can then ride a chat proposal into the
// worker that was explicitly approved.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const scoutFileContextLimit = 4

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

func (app *kanbanBoardApp) agentThreadMemory(ctx context.Context, requester string, roomID string, refsValue string, limit int) []meetingMemoryEntry {
	base := app.delegatedMemorySnapshot(ctx, requester, roomID, limit)
	user, ok := authenticatedRequester(requester)
	if !ok {
		return base
	}
	principal := recallPrincipalForUser(user)
	if strings.TrimSpace(roomID) != "" {
		principal = app.recallPrincipalForMemberRoom(user.Email, roomID)
	}
	refCtx := withAssistantContextRefs(ctx, decodeAssistantContextRefs(refsValue))
	pinned := app.assistantFileContextEntries(refCtx, principal, "")
	return appendUniqueFileContextEntries(pinned, base)
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
