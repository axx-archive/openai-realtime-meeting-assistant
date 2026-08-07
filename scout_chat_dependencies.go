package main

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// scoutChatSourceNeed is the deterministic admission result for a turn that
// asks Scout to read or work from a document. Missing means no readable source
// is currently available; ContextRefs names the exact authorized sources that
// must stay bound to any proposal and worker launch.
type scoutChatSourceNeed struct {
	Required    bool
	Work        bool
	Missing     bool
	FileName    string
	FileSize    int64
	StoredOnly  bool
	ContextRefs []string
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
	// The newest relevant attachment wins for pronoun follow-ups such as
	// “open it and review it.” Bound the scan so an unrelated old deck does not
	// silently become the source of a later generic request.
	start := len(thread.Messages) - 16
	if start < 0 {
		start = 0
	}
	for messageIndex := len(thread.Messages) - 1; messageIndex >= start; messageIndex-- {
		prior := thread.Messages[messageIndex]
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
	if !need.Required || app == nil || user == nil {
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
