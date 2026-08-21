package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

// scoutChatSourceNeed is the deterministic admission result for a turn that
// asks Scout to read or work from a document. Missing means no readable source
// is currently available; ContextRefs names the exact authorized sources that
// must stay bound to any proposal and worker launch.
type scoutChatSourceNeed struct {
	Required       bool
	Work           bool
	Missing        bool
	FileName       string
	FileSize       int64
	StoredOnly     bool
	MissingMessage string
	ContextRefs    []string
}

func scoutTextHasWord(text string, candidates ...string) bool {
	wanted := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		wanted[candidate] = true
	}
	for _, token := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		if wanted[token] {
			return true
		}
	}
	return false
}

func scoutChatRequestNeedsReadableSource(text string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
	// Match single-word verbs on token boundaries. Substring matching makes
	// ordinary routing phrases such as "the outline we already have" look like
	// a request to READ a file because "already" contains "read".
	verb := scoutTextHasWord(normalized,
		"open", "read", "review", "summarize", "summarise", "analyze", "analyse",
		"audit", "critique", "assess", "extract",
	)
	if !verb {
		for _, phrase := range []string{"go through", "look at"} {
			if strings.Contains(normalized, phrase) {
				verb = true
				break
			}
		}
	}
	if !verb {
		return false
	}
	if scoutTextHasWord(normalized, "deck", "pdf", "document", "file", "files", "attachment", "transcript", "recording", "presentation", "slides") {
		return true
	}
	for _, phrase := range []string{"open it", "read it", "review it", "summarize it", "summarise it", "analyze it", "analyse it", "go through it", "look at it"} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

func scoutChatRequestIsFileWork(text string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
	// Reading, describing, analyzing, critiquing, and reconstructing a prompt
	// from an already-authorized source are ordinary multimodal conversation.
	// Durable work begins only when the user asks for a broader decision pass or
	// an output that should survive as its own artifact.
	for _, marker := range []string{"audit", "compare", "recommend", "strategy", "template", "work on", "create a report", "produce", "prepare", "deep research", "research pass"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

type scoutChatFileCandidate struct {
	file       scoutChatFileAttachment
	contextRef string
	score      int
}

func canonicalScoutExplicitFilename(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func scoutChatSupportedFilename(value string) bool {
	lower := canonicalScoutExplicitFilename(value)
	// Extensions are only syntax hints; the existing upload-safe MIME contract
	// remains the authority for which attachment classes can be named.
	for _, candidate := range []struct{ suffix, mime string }{
		{".png", "image/png"}, {".jpg", "image/jpeg"}, {".jpeg", "image/jpeg"},
		{".webp", "image/webp"}, {".gif", "image/gif"}, {".pdf", "application/pdf"},
		{".txt", "text/plain"}, {".md", "text/markdown"}, {".markdown", "text/markdown"},
	} {
		if strings.HasSuffix(lower, candidate.suffix) {
			return attachmentUploadSafeMimes[candidate.mime]
		}
	}
	return false
}

// scoutChatExplicitFilenameMentions parses the complete filename tokens the
// user supplied. Quoted names retain spaces; unquoted names are bounded by
// whitespace or list punctuation. Returning every mention makes the whole
// dependency set conjunctive: Existing.pdf + Missing.pdf cannot degrade to
// just Existing.pdf.
func scoutChatExplicitFilenameMentions(text string) []string {
	runes := []rune(text)
	unquoted := append([]rune(nil), runes...)
	var mentions []string
	seen := map[string]bool{}
	appendMention := func(value string) {
		value = strings.TrimSpace(value)
		key := canonicalScoutExplicitFilename(value)
		if value != "" && scoutChatSupportedFilename(value) && !seen[key] {
			seen[key] = true
			mentions = append(mentions, value)
		}
	}
	closerFor := map[rune]rune{'"': '"', '\'': '\'', '“': '”', '‘': '’'}
	for index := 0; index < len(runes); index++ {
		closer, quoted := closerFor[runes[index]]
		if !quoted {
			continue
		}
		end := index + 1
		for end < len(runes) && runes[end] != closer {
			end++
		}
		if end >= len(runes) {
			continue
		}
		candidate := string(runes[index+1 : end])
		if scoutChatSupportedFilename(candidate) {
			appendMention(candidate)
			for clear := index; clear <= end; clear++ {
				unquoted[clear] = ' '
			}
		}
		index = end
	}
	for _, field := range strings.FieldsFunc(string(unquoted), func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("+,&;", r)
	}) {
		candidate := strings.Trim(field, "\t\r\n\"'“”‘’()[]{}<>,;:")
		candidate = strings.TrimRight(candidate, ".!?")
		appendMention(candidate)
	}
	return mentions
}

// exactNamedScoutChatFileCandidates resolves only attachments whose complete
// filename appears in the request. This is the narrow cross-branch exception:
// an explicit unique name may select an older main-channel attachment, while
// unnamed/recent/similarly-tokened sibling files remain ineligible.
func (app *kanbanBoardApp) exactNamedScoutChatFileCandidates(user *userAccount, thread scoutChatThreadRecord, message scoutChatMessageRecord) ([]scoutChatFileCandidate, bool, string) {
	if app == nil || user == nil {
		return nil, false, ""
	}
	mentions := scoutChatExplicitFilenameMentions(message.Text)
	if len(mentions) == 0 {
		return nil, false, ""
	}
	type namedGroup struct {
		name       string
		candidates []scoutChatFileCandidate
		identities map[string]bool
	}
	groups := map[string]*namedGroup{}
	for _, mention := range mentions {
		key := canonicalScoutExplicitFilename(mention)
		groups[key] = &namedGroup{name: mention, identities: map[string]bool{}}
	}
	addNamed := func(file scoutChatFileAttachment, contextRef string, authorized bool) {
		key := canonicalScoutExplicitFilename(file.Name)
		group := groups[key]
		if group == nil {
			return
		}
		if !authorized {
			return
		}
		identity := strings.TrimSpace(file.Ref) + "\x00" + strings.TrimSpace(file.SourceRevision)
		if group.identities[identity] {
			return
		}
		group.identities[identity] = true
		group.candidates = append(group.candidates, scoutChatFileCandidate{file: file, contextRef: contextRef})
	}
	for index, file := range message.Files {
		// Current-turn attachments passed sanitizeScoutChatFiles and remain bound
		// to their reservation until commitUserMessage lands them.
		if groups[canonicalScoutExplicitFilename(file.Name)] != nil {
			addNamed(file, scoutChatFileContextRef(thread.ID, message.ID, index), true)
		}
	}
	branchEligible := map[string]bool{}
	if message.ReplyTo != nil {
		rootID := scoutChatReplyRootID(thread, message.ReplyTo.MessageID)
		for _, selected := range scoutChatReplyContextMessages(thread, rootID).Messages {
			branchEligible[selected.ID] = true
		}
	}
	for messageIndex := len(thread.Messages) - 1; messageIndex >= 0; messageIndex-- {
		prior := thread.Messages[messageIndex]
		// The exact-name cross-branch exception is intentionally limited to the
		// main channel. A file buried in another reply branch remains invisible
		// even when its name is guessed exactly; the current causal branch remains
		// eligible through the same bounded selection used by reply context.
		if prior.ReplyTo != nil && !branchEligible[prior.ID] {
			continue
		}
		for fileIndex, file := range prior.Files {
			if groups[canonicalScoutExplicitFilename(file.Name)] == nil {
				continue
			}
			addNamed(file, scoutChatFileContextRef(thread.ID, prior.ID, fileIndex), app.committedChatAttachmentAuthorized(user.Email, thread.ID, prior.ID, file))
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var resolved []scoutChatFileCandidate
	for _, key := range keys {
		group := groups[key]
		switch len(group.candidates) {
		case 0:
			return nil, true, "I couldn't resolve exactly one readable attachment named “" + group.name + "” in this channel. Attach that exact file, restore its access, or correct the filename; nothing was launched."
		case 1:
			resolved = append(resolved, group.candidates[0])
		default:
			return nil, true, "More than one readable attachment is named “" + group.name + "”. Reattach the exact revision you want in this reply, or rename it so the source is unambiguous; nothing was launched."
		}
	}
	if len(resolved) > scoutFileContextLimit {
		return nil, true, fmt.Sprintf("Scout can bind no more than %d exact files to one run. Name fewer files and ask again; nothing was launched.", scoutFileContextLimit)
	}
	return resolved, true, ""
}

func bestScoutChatFileCandidate(thread scoutChatThreadRecord, message scoutChatMessageRecord) (scoutChatFileCandidate, bool) {
	query := message.Text
	var candidates []scoutChatFileCandidate
	for index, file := range message.Files {
		candidates = append(candidates, scoutChatFileCandidate{
			file:       file,
			contextRef: scoutChatFileContextRef(thread.ID, message.ID, index),
			score:      1000 + scoutFileNameScore(query, file.Name),
		})
	}
	// A reply can only inherit attachments from its own causal branch. Scan the
	// bounded branch selection (which pins substantive/file sources) rather than
	// the channel tail, where a newer sibling PDF could otherwise become the
	// approved source. Root turns retain the historical recent-tail behavior.
	eligible := map[string]bool{}
	branchScoped := message.ReplyTo != nil
	start := len(thread.Messages) - 16
	if start < 0 {
		start = 0
	}
	if branchScoped {
		rootID := scoutChatReplyRootID(thread, message.ReplyTo.MessageID)
		selection := scoutChatReplyContextMessages(thread, rootID)
		for _, selected := range selection.Messages {
			eligible[selected.ID] = true
		}
		start = 0
	}
	for messageIndex := len(thread.Messages) - 1; messageIndex >= start; messageIndex-- {
		prior := thread.Messages[messageIndex]
		if branchScoped && !eligible[prior.ID] {
			continue
		}
		for fileIndex := len(prior.Files) - 1; fileIndex >= 0; fileIndex-- {
			file := prior.Files[fileIndex]
			recency := messageIndex - start
			candidates = append(candidates, scoutChatFileCandidate{
				file:       file,
				contextRef: scoutChatFileContextRef(thread.ID, prior.ID, fileIndex),
				score:      recency + scoutFileNameScore(query, file.Name),
			})
		}
	}
	if len(candidates) == 0 {
		return scoutChatFileCandidate{}, false
	}
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.score > best.score {
			best = candidate
		}
	}
	return best, true
}

func (app *kanbanBoardApp) scoutChatReadableSourceNeed(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, message scoutChatMessageRecord) scoutChatSourceNeed {
	need := scoutChatSourceNeed{
		Required: scoutChatRequestNeedsReadableSource(message.Text),
		Work:     scoutChatRequestIsFileWork(message.Text),
	}
	if app == nil || user == nil {
		return need
	}
	if exact, named, issue := app.exactNamedScoutChatFileCandidates(user, thread, message); named {
		need.Required = true
		if issue != "" {
			need.Missing = true
			need.MissingMessage = issue
			return need
		}
		for _, candidate := range exact {
			need.FileName = strings.TrimSpace(candidate.file.Name)
			need.FileSize += candidate.file.Size
			if strings.TrimSpace(candidate.file.Text) == "" {
				need.Missing = true
				need.MissingMessage = "I found “" + need.FileName + "”, but it has no readable contents. Attach a readable copy of that exact revision; nothing was launched."
				return need
			}
			need.ContextRefs = append(need.ContextRefs, candidate.contextRef)
		}
		need.ContextRefs = canonicalAssistantContextRefs(need.ContextRefs)
		return need
	}
	if !need.Required {
		return need
	}

	if candidate, ok := bestScoutChatFileCandidate(thread, message); ok {
		need.FileName = strings.TrimSpace(candidate.file.Name)
		need.FileSize = candidate.file.Size
		if strings.TrimSpace(candidate.file.Text) != "" {
			need.ContextRefs = []string{candidate.contextRef}
			return need
		}
		// A filename-only attachment may already have an ingested twin in Files.
		// Resolve it by exact authorized catalog identity before declaring the
		// dependency missing.
		if row, ref, found := app.readableAssistantFileByName(ctx, user, candidate.file.Name); found {
			need.FileName = row.Name
			need.FileSize = row.Size
			need.ContextRefs = []string{ref}
			return need
		}
		for _, row := range app.assistantFilesForPrincipal(ctx, user) {
			if scoutFileNameScore(candidate.file.Name, row.Name) >= 24 {
				need.StoredOnly = true
				break
			}
		}
		need.Missing = true
		return need
	}

	// A message can name an existing Drive file without re-attaching it. Exact
	// filename relevance is sufficient; generic “review this” with several
	// files remains ambiguous and asks for the source instead.
	if row, ref, found := app.readableAssistantFileByName(ctx, user, message.Text); found {
		need.FileName = row.Name
		need.FileSize = row.Size
		need.ContextRefs = []string{ref}
		return need
	}
	need.Missing = true
	return need
}

func formatScoutFileSize(size int64) string {
	if size <= 0 {
		return ""
	}
	const mb = int64(1 << 20)
	if size >= mb {
		return fmt.Sprintf("%.1f MB", float64(size)/float64(mb))
	}
	return fmt.Sprintf("%d KB", (size+1023)/1024)
}

func scoutChatMissingSourceMessage(need scoutChatSourceNeed) string {
	if message := strings.TrimSpace(need.MissingMessage); message != "" {
		return message
	}
	name := strings.TrimSpace(need.FileName)
	if name == "" {
		return "I understand the task, but I don't have a readable source yet. Attach the PDF/deck here, or name an ingested file in Files. Nothing is running yet; once I can read it, I'll prepare the work for approval and keep the status in this thread."
	}
	size := formatScoutFileSize(need.FileSize)
	label := "“" + name + "”"
	if size != "" {
		label += " (" + size + ")"
	}
	if need.FileSize > attachmentMaxPDFBytes {
		return fmt.Sprintf("I can see %s, but I don't have readable contents—the PDF is above Scout's 20 MB per-file reading limit. Please export/compress it or split it into parts under 20 MB, then attach those here or upload an ingested copy to Files. Nothing is running yet; once it's readable, I'll prepare the review for approval and keep the work status in this thread.", label)
	}
	if need.StoredOnly {
		return fmt.Sprintf("I can see %s in Files, but it is stored bytes only; no readable text was ingested. Re-upload a readable PDF under 20 MB or attach the document here. Nothing is running yet; once I can read it, I'll prepare the review for approval and keep the work status in this thread.", label)
	}
	return fmt.Sprintf("I can see %s, but it arrived as a filename only, so I can't read or review its contents yet. Attach a readable PDF under 20 MB or upload an ingested copy to Files. Nothing is running yet; once I can read it, I'll prepare the work for approval and keep the status in this thread.", label)
}

func scoutChatMissingSourceResponse(need scoutChatSourceNeed) scoutChatMessageRecord {
	return scoutChatMessageRecord{
		ID:         fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
		Kind:       "message",
		Role:       "scout",
		AuthorName: scoutParticipantName,
		Text:       scoutChatMissingSourceMessage(need),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
}
