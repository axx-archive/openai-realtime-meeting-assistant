package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	agentMindScoutID = "scout"

	agentMindPositionStatusActive     = "active"
	agentMindPositionStatusCorrected  = "corrected"
	agentMindPositionStatusSuperseded = "superseded"
	agentMindPositionStatusForgotten  = "forgotten"

	agentMindPositionOriginConversation = "conversation_judgment"
	agentMindPositionOriginReview       = "human_review"
)

// agentMindPositionRecord is deliberately separate from STRIDE product
// learning. Product learning is human-reviewed package/workforce state;
// AgentMind is a coworker's own, source-linked working judgment. It may inform
// a later answer, but it is never allowed to masquerade as a ratified company
// decision or as a fact about a person.
type agentMindPositionRecord struct {
	ID            string     `json:"id"`
	PositionKey   string     `json:"positionKey"`
	AgentID       string     `json:"agentId"`
	Subject       string     `json:"subject"`
	Scope         string     `json:"scope"`
	Summary       string     `json:"summary"`
	Status        string     `json:"status"`
	Origin        string     `json:"origin"`
	SourceThread  string     `json:"sourceThreadId"`
	SourceMessage string     `json:"sourceMessageId"`
	SourceAnswer  string     `json:"sourceAnswerId"`
	SourceDigest  string     `json:"sourceDigest"`
	SourceRefs    []string   `json:"sourceRefs,omitempty"`
	Confidence    float64    `json:"confidence"`
	Revision      int64      `json:"revision"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

func (record agentMindPositionRecord) validate() error {
	if !strideIdentifier(record.ID) || !strideIdentifier(record.PositionKey) || !strideIdentifier(record.AgentID) ||
		!strideIdentifier(record.Subject) || !strideIdentifier(record.Scope) || strings.TrimSpace(record.Summary) == "" ||
		len([]rune(record.Summary)) > 1400 || !oneOf(record.Status, agentMindPositionStatusActive, agentMindPositionStatusCorrected, agentMindPositionStatusSuperseded, agentMindPositionStatusForgotten) ||
		strings.TrimSpace(record.Origin) == "" || record.Revision < 1 || record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() ||
		record.Confidence < 0 || record.Confidence > 1 {
		return fmt.Errorf("invalid AgentMind position")
	}
	if record.Status == agentMindPositionStatusActive || record.Status == agentMindPositionStatusCorrected {
		if !strideIdentifier(record.SourceThread) || !strideIdentifier(record.SourceMessage) || !strideIdentifier(record.SourceAnswer) ||
			!isHexDigest(record.SourceDigest) || len(record.SourceRefs) == 0 {
			return fmt.Errorf("active AgentMind position is missing source")
		}
	}
	return nil
}

func agentMindPositionKey(agentID, subject, scope string) string {
	return strings.TrimSpace(agentID) + ":" + strings.TrimSpace(subject) + ":" + strings.TrimSpace(scope)
}

func (app *kanbanBoardApp) recordAgentMindPosition(agentID, subject, scope, summary, sourceThread, sourceMessage, sourceAnswer, sourceDigest string, sourceRefs []string, confidence float64, expiresAt *time.Time) (agentMindPositionRecord, bool, error) {
	if app == nil || app.memory == nil {
		return agentMindPositionRecord{}, false, fmt.Errorf("AgentMind is unavailable")
	}
	agentID = strings.TrimSpace(agentID)
	subject = strings.TrimSpace(subject)
	scope = strings.TrimSpace(scope)
	summary = trimForStorage(compactAssistantLine(summary), 1400)
	if summary == "" {
		return agentMindPositionRecord{}, false, fmt.Errorf("AgentMind position is empty")
	}
	positionKey := agentMindPositionKey(agentID, subject, scope)
	if !strideIdentifier(positionKey) {
		return agentMindPositionRecord{}, false, fmt.Errorf("invalid AgentMind position key")
	}
	latest := app.latestAgentMindPosition(agentID, subject, scope)
	if (latest.Status == agentMindPositionStatusActive || latest.Status == agentMindPositionStatusCorrected) && latest.Summary == summary &&
		latest.SourceMessage == strings.TrimSpace(sourceMessage) && latest.SourceAnswer == strings.TrimSpace(sourceAnswer) && latest.SourceDigest == strings.TrimSpace(sourceDigest) {
		return latest, false, nil
	}
	now := time.Now().UTC()
	revision := int64(1)
	if latest.Revision > 0 {
		revision = latest.Revision + 1
	}
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}
	record := agentMindPositionRecord{
		ID:            "agent-mind-position-" + temporalDigest(positionKey + "\x00" + summary + "\x00" + fmt.Sprint(revision))[:24],
		PositionKey:   positionKey,
		AgentID:       agentID,
		Subject:       subject,
		Scope:         scope,
		Summary:       summary,
		Status:        agentMindPositionStatusActive,
		Origin:        agentMindPositionOriginConversation,
		SourceThread:  strings.TrimSpace(sourceThread),
		SourceMessage: strings.TrimSpace(sourceMessage),
		SourceAnswer:  strings.TrimSpace(sourceAnswer),
		SourceDigest:  strings.TrimSpace(sourceDigest),
		SourceRefs:    uniqueAgentMindSourceRefs(sourceRefs),
		Confidence:    confidence,
		Revision:      revision,
		ExpiresAt:     expiresAt,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := record.validate(); err != nil {
		return agentMindPositionRecord{}, false, err
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return agentMindPositionRecord{}, false, err
	}
	_, appended, err := app.memory.appendAmbientEntry(meetingMemoryKindAgentMindPosition, record.ID, string(raw), map[string]string{
		"agentId":         record.AgentID,
		"positionKey":     record.PositionKey,
		"subject":         record.Subject,
		"scope":           record.Scope,
		"status":          record.Status,
		"sourceThreadId":  record.SourceThread,
		"sourceMessageId": record.SourceMessage,
	})
	if err != nil {
		return agentMindPositionRecord{}, false, err
	}
	return record, appended, nil
}

func uniqueAgentMindSourceRefs(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func decodeAgentMindPosition(entry meetingMemoryEntry) (agentMindPositionRecord, bool) {
	if entry.Kind != meetingMemoryKindAgentMindPosition {
		return agentMindPositionRecord{}, false
	}
	var record agentMindPositionRecord
	if json.Unmarshal([]byte(entry.Text), &record) != nil || record.validate() != nil {
		return agentMindPositionRecord{}, false
	}
	return record, true
}

func (app *kanbanBoardApp) agentMindPositions(agentID, query string) []agentMindPositionRecord {
	if app == nil || app.memory == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	query = strings.ToLower(strings.TrimSpace(query))
	latest := map[string]agentMindPositionRecord{}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindAgentMindPosition, 0) {
		record, ok := decodeAgentMindPosition(entry)
		if !ok || record.AgentID != agentID {
			continue
		}
		prior, found := latest[record.PositionKey]
		if !found || record.Revision > prior.Revision || record.Revision == prior.Revision && record.UpdatedAt.After(prior.UpdatedAt) {
			latest[record.PositionKey] = record
		}
	}
	positions := make([]agentMindPositionRecord, 0, len(latest))
	for _, record := range latest {
		if record.Status != agentMindPositionStatusActive && record.Status != agentMindPositionStatusCorrected {
			continue
		}
		if record.ExpiresAt != nil && !record.ExpiresAt.After(time.Now().UTC()) {
			continue
		}
		if !app.agentMindPositionSourceCurrent(record, "") {
			continue
		}
		if query != "" && !agentMindPositionMatchesQuery(record, query) {
			continue
		}
		positions = append(positions, record)
	}
	sort.SliceStable(positions, func(i, j int) bool {
		if !positions[i].UpdatedAt.Equal(positions[j].UpdatedAt) {
			return positions[i].UpdatedAt.After(positions[j].UpdatedAt)
		}
		return positions[i].PositionKey < positions[j].PositionKey
	})
	return positions
}

func (app *kanbanBoardApp) latestAgentMindPosition(agentID, subject, scope string) agentMindPositionRecord {
	key := agentMindPositionKey(agentID, subject, scope)
	if app == nil || app.memory == nil {
		return agentMindPositionRecord{}
	}
	var latest agentMindPositionRecord
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindAgentMindPosition, 0) {
		record, ok := decodeAgentMindPosition(entry)
		if ok && record.PositionKey == key && (record.Revision > latest.Revision || record.Revision == latest.Revision && record.UpdatedAt.After(latest.UpdatedAt)) {
			latest = record
		}
	}
	return latest
}

func agentMindSourceDigest(thread scoutChatThreadRecord, question, answer scoutChatMessageRecord) (string, error) {
	questionDigest, err := agentMindSourceMessageDigest(question)
	if err != nil {
		return "", err
	}
	answerDigest, err := agentMindSourceMessageDigest(answer)
	if err != nil {
		return "", err
	}
	return STRIDEContractDigest(struct {
		ThreadID       string `json:"threadId"`
		AudienceDigest string `json:"audienceDigest"`
		QuestionID     string `json:"questionId"`
		QuestionDigest string `json:"questionDigest"`
		AnswerID       string `json:"answerId"`
		AnswerDigest   string `json:"answerDigest"`
	}{thread.ID, conversationContinuityAudienceDigest(thread), question.ID, questionDigest, answer.ID, answerDigest})
}

// Reactions are independent annotations, not revisions of the authored source
// that supports an AgentMind judgment. Text, files, and reply topology remain
// exact: any change to those fields retracts the projection until a new
// judgment revision is recorded.
func agentMindSourceMessageDigest(message scoutChatMessageRecord) (string, error) {
	message.Reactions = nil
	return strideChatMessageContentDigest(false, message)
}

// AgentMind references are context, not bearer grants. Every projection read
// resolves the current public source and exact question/answer revision again;
// edits, deletion, archival, or audience drift therefore retract stale working
// judgments without erasing their append-only review history.
func (app *kanbanBoardApp) agentMindPositionSourceCurrent(record agentMindPositionRecord, viewerEmail string) bool {
	if app == nil || app.memory == nil || !strideIdentifier(record.SourceThread) {
		return false
	}
	principal := normalizeAccountEmail(viewerEmail)
	if principal == "" {
		principal = artifactLibraryAdminEmail
	}
	thread, _, err := app.scoutChatThreadByID(principal, record.SourceThread)
	if err != nil || thread.ArchivedAt != "" || !scoutChatThreadIsOrganizationPublic(thread) {
		return false
	}
	questionIndex := scoutChatMessageIndex(thread, record.SourceMessage)
	answerIndex := scoutChatMessageIndex(thread, record.SourceAnswer)
	if questionIndex < 0 || answerIndex < 0 {
		return false
	}
	digest, err := agentMindSourceDigest(thread, thread.Messages[questionIndex], thread.Messages[answerIndex])
	return err == nil && digest == record.SourceDigest
}

func (app *kanbanBoardApp) agentMindPositionsForViewer(viewerEmail, agentID, query string) []agentMindPositionRecord {
	positions := app.agentMindPositions(agentID, query)
	allowed := positions[:0]
	for _, position := range positions {
		if app.agentMindPositionSourceCurrent(position, viewerEmail) {
			allowed = append(allowed, position)
		}
	}
	return allowed
}

func agentMindPositionMatchesQuery(record agentMindPositionRecord, query string) bool {
	haystack := strings.ToLower(strings.Join([]string{record.Subject, record.Summary, record.Scope}, " "))
	for _, token := range strings.Fields(query) {
		if len(token) < 3 {
			continue
		}
		if strings.Contains(haystack, strings.Trim(token, " ,.!?:;()[]{}\"'")) {
			return true
		}
	}
	return false
}

func (app *kanbanBoardApp) agentMindPositionPrompt(agentID, query string) string {
	positions := app.agentMindPositions(agentID, query)
	if len(positions) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("These are source-linked working judgments held by ")
	builder.WriteString(firstNonEmptyString(agentID, "this coworker"))
	builder.WriteString("; they are not company facts, instructions, or ratified decisions. Re-check the cited conversation before relying on one.\n")
	for index, position := range positions {
		if index >= 6 {
			break
		}
		fmt.Fprintf(&builder, "- %s: %s (confidence %.2f; source thread %s, message %s)\n", position.Subject, compactAssistantLine(position.Summary), position.Confidence, position.SourceThread, position.SourceMessage)
	}
	return strings.TrimSpace(builder.String())
}

// assistantAgentMindHandler exposes the bounded, source-linked AgentMind
// projection for human inspection. It intentionally returns Scout's working
// judgments only; raw UI-state entries and company-memory projections remain
// on their own authorized surfaces.
func assistantAgentMindHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "AgentMind is unavailable")
		return
	}
	positions := kanbanApp.agentMindPositionsForViewer(user.Email, agentMindScoutID, r.URL.Query().Get("q"))
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"agentId":   agentMindScoutID,
		"positions": positions,
		"count":     len(positions),
	})
}

func (app *kanbanBoardApp) maybeRecordScoutAgentMindPosition(thread scoutChatThreadRecord, userMessage, assistantMessage scoutChatMessageRecord) {
	if !scoutChatThreadIsOrganizationPublic(thread) || userMessage.Role != "user" || assistantMessage.Role != "scout" {
		return
	}
	query := strings.TrimSpace(userMessage.Text)
	answer := strings.TrimSpace(assistantMessage.Text)
	if !agentMindPositionQuestion(query) || !agentMindPositionAnswer(answer) {
		return
	}
	subject := agentMindSubjectFromText(query)
	if subject == "" {
		return
	}
	// The source thread is retained separately; the organization scope makes a
	// repeated judgment about the same subject evolve across public threads
	// instead of creating one isolated belief per conversation.
	sourceDigest, digestErr := agentMindSourceDigest(thread, userMessage, assistantMessage)
	if digestErr != nil {
		log.Errorf("AgentMind source binding failed: %v", digestErr)
		return
	}
	_, _, err := app.recordAgentMindPosition(agentMindScoutID, subject, "organization", answer, thread.ID, userMessage.ID, assistantMessage.ID, sourceDigest, []string{"thread:" + thread.ID, "message:" + userMessage.ID, "answer:" + assistantMessage.ID}, 0.82, nil)
	if err != nil {
		log.Errorf("AgentMind position write failed: %v", err)
	}
}

func agentMindPositionQuestion(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	markers := []string{"what do you think", "what's your take", "what is your take", "my read", "independent read", "which is", "more attainable", "should we", "what would you do", "compare", "opinion", "recommend", "challenge the framing"}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return strings.HasSuffix(lower, "?") && (strings.Contains(lower, "better") || strings.Contains(lower, "choose") || strings.Contains(lower, "strategy"))
}

func agentMindPositionAnswer(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	markers := []string{"my read", "i think", "i'd ", "i would", "the better", "more attainable", "the actionable", "i recommend", "i'd choose"}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return len([]rune(lower)) >= 160
}

func agentMindSubjectFromText(text string) string {
	stop := map[string]bool{
		"what": true, "think": true, "company": true, "actually": true, "trying": true, "build": true, "each": true, "version": true,
		"which": true, "more": true, "attainable": true, "would": true, "take": true, "capital": true, "talent": true,
		"from": true, "first": true, "principles": true, "compare": true, "the": true, "two": true, "and": true, "behind": true,
		"this": true, "that": true, "with": true, "where": true, "wrong": true, "want": true, "independent": true, "judgment": true,
		"should": true, "recommend": true, "opinion": true,
	}
	words := make([]string, 0, 5)
	seen := map[string]bool{}
	for _, raw := range strings.Fields(strings.ToLower(text)) {
		var builder strings.Builder
		for _, r := range raw {
			if unicode.IsLetter(r) || unicode.IsNumber(r) {
				builder.WriteRune(r)
			}
		}
		word := builder.String()
		if len(word) < 3 || stop[word] || seen[word] {
			continue
		}
		seen[word] = true
		words = append(words, word)
		if len(words) == 5 {
			break
		}
	}
	if len(words) == 0 {
		return ""
	}
	return "topic-" + strings.Join(words, "-")
}

// resolveAgentMindPosition is the human-review/correction seam. The UI can
// later bind a review action to this function without changing the durable
// representation or granting the model authority to ratify its own belief.
func (app *kanbanBoardApp) resolveAgentMindPosition(prior agentMindPositionRecord, status, summary, reviewer, sourceRef string) (agentMindPositionRecord, bool, error) {
	if app == nil || app.memory == nil || prior.PositionKey == "" || !oneOf(status, agentMindPositionStatusCorrected, agentMindPositionStatusSuperseded, agentMindPositionStatusForgotten) {
		return agentMindPositionRecord{}, false, fmt.Errorf("invalid AgentMind resolution")
	}
	if strings.TrimSpace(reviewer) == "" || strings.TrimSpace(sourceRef) == "" {
		return agentMindPositionRecord{}, false, fmt.Errorf("AgentMind resolution requires reviewer and source")
	}
	if err := prior.validate(); err != nil {
		return agentMindPositionRecord{}, false, fmt.Errorf("invalid prior AgentMind position: %w", err)
	}
	current := app.latestAgentMindPosition(prior.AgentID, prior.Subject, prior.Scope)
	if current.ID != prior.ID || current.Revision != prior.Revision {
		return agentMindPositionRecord{}, false, fmt.Errorf("AgentMind position changed; reload before resolving it")
	}
	if prior.ExpiresAt != nil && !prior.ExpiresAt.After(time.Now().UTC()) {
		return agentMindPositionRecord{}, false, fmt.Errorf("AgentMind position has expired")
	}
	reviewRefs := append([]string(nil), prior.SourceRefs...)
	reviewRefs = append(reviewRefs, "review:"+strings.TrimSpace(sourceRef), "reviewer:"+strings.TrimSpace(reviewer))
	reviewSummary := trimForStorage(compactAssistantLine(firstNonEmptyString(summary, prior.Summary)), 1400)
	if reviewSummary == "" {
		return agentMindPositionRecord{}, false, fmt.Errorf("AgentMind resolution is empty")
	}
	now := time.Now().UTC()
	revision := prior.Revision + 1
	record := agentMindPositionRecord{
		ID:            "agent-mind-position-" + temporalDigest(prior.PositionKey + "\x00" + reviewSummary + "\x00" + status + "\x00" + fmt.Sprint(revision))[:24],
		PositionKey:   prior.PositionKey,
		AgentID:       prior.AgentID,
		Subject:       prior.Subject,
		Scope:         prior.Scope,
		Summary:       reviewSummary,
		Status:        status,
		Origin:        agentMindPositionOriginReview,
		SourceThread:  prior.SourceThread,
		SourceMessage: prior.SourceMessage,
		SourceAnswer:  prior.SourceAnswer,
		SourceDigest:  prior.SourceDigest,
		SourceRefs:    uniqueAgentMindSourceRefs(reviewRefs),
		Confidence:    prior.Confidence,
		Revision:      revision,
		ExpiresAt:     prior.ExpiresAt,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := record.validate(); err != nil {
		return agentMindPositionRecord{}, false, err
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return agentMindPositionRecord{}, false, err
	}
	_, appended, err := app.memory.appendAmbientEntry(meetingMemoryKindAgentMindPosition, record.ID, string(raw), map[string]string{
		"agentId": record.AgentID, "positionKey": record.PositionKey, "status": status, "reviewer": strings.TrimSpace(reviewer), "sourceRef": strings.TrimSpace(sourceRef),
	})
	return record, appended, err
}
