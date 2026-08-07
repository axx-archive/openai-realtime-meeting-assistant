package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	conversationContinuityStatusActive      = "active"
	conversationContinuityStatusInvalidated = "invalidated"
	conversationContinuityRawTailLimit      = 16
)

// conversationContinuityCheckpoint is a body-free, revisioned projection of
// one chat thread. Raw turn bodies remain in the ACL-governed chat thread; this
// record carries only typed classifications, source ids, digests, and gaps so
// it can be safely reauthorized before entering a coworker context envelope.
type conversationContinuityCheckpoint struct {
	ID                      string    `json:"id"`
	ThreadID                string    `json:"threadId"`
	Revision                int64     `json:"revision"`
	Status                  string    `json:"status"`
	InvalidationReason      string    `json:"invalidationReason,omitempty"`
	AudienceDigest          string    `json:"audienceDigest"`
	SourceDigest            string    `json:"sourceDigest"`
	SourceMessageIDs        []string  `json:"sourceMessageIds,omitempty"`
	CurrentIntentClass      string    `json:"currentIntentClass"`
	CurrentIntentDigest     string    `json:"currentIntentDigest,omitempty"`
	ResolvedReferenceIDs    []string  `json:"resolvedReferenceIds,omitempty"`
	UnresolvedReferenceIDs  []string  `json:"unresolvedReferenceIds,omitempty"`
	EstablishedPositionRefs []string  `json:"establishedPositionRefs,omitempty"`
	DisagreementSourceIDs   []string  `json:"disagreementSourceIds,omitempty"`
	CorrectionSourceIDs     []string  `json:"correctionSourceIds,omitempty"`
	OpenLoopSourceIDs       []string  `json:"openLoopSourceIds,omitempty"`
	ActiveWorkSourceIDs     []string  `json:"activeWorkSourceIds,omitempty"`
	KnownGaps               []string  `json:"knownGaps,omitempty"`
	SourceHighWater         uint64    `json:"sourceHighWater"`
	LastSourceAt            time.Time `json:"lastSourceAt"`
	CreatedAt               time.Time `json:"createdAt"`
	UpdatedAt               time.Time `json:"updatedAt"`
}

func (checkpoint conversationContinuityCheckpoint) validate() error {
	if !strideIdentifier(checkpoint.ID) || !strideIdentifier(checkpoint.ThreadID) || checkpoint.Revision < 1 ||
		!oneOf(checkpoint.Status, conversationContinuityStatusActive, conversationContinuityStatusInvalidated) ||
		!isHexDigest(checkpoint.AudienceDigest) || !isHexDigest(checkpoint.SourceDigest) ||
		!oneOf(checkpoint.CurrentIntentClass, "unknown", "discussion", "question", "research", "execution", "status") ||
		!continuityIDsValid(checkpoint.SourceMessageIDs) || !continuityIDsValid(checkpoint.ResolvedReferenceIDs) ||
		!continuityIDsValid(checkpoint.UnresolvedReferenceIDs) || !continuityIDsValid(checkpoint.EstablishedPositionRefs) ||
		!continuityIDsValid(checkpoint.DisagreementSourceIDs) || !continuityIDsValid(checkpoint.CorrectionSourceIDs) ||
		!continuityIDsValid(checkpoint.OpenLoopSourceIDs) || !continuityIDsValid(checkpoint.ActiveWorkSourceIDs) ||
		!continuityIDsValid(checkpoint.KnownGaps) || checkpoint.LastSourceAt.IsZero() || checkpoint.CreatedAt.IsZero() || checkpoint.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid ConversationContinuity checkpoint")
	}
	if checkpoint.Status == conversationContinuityStatusActive && len(checkpoint.SourceMessageIDs) == 0 {
		return fmt.Errorf("active ConversationContinuity checkpoint has no source")
	}
	return nil
}

func continuityIDsValid(values []string) bool {
	if len(values) == 0 {
		return true
	}
	return uniqueSTRIDEIDs(values)
}

type conversationContinuitySource struct {
	ID       string `json:"id"`
	Role     string `json:"role"`
	Author   string `json:"author"`
	Digest   string `json:"digest"`
	ReplyTo  string `json:"replyTo,omitempty"`
	EditedAt string `json:"editedAt,omitempty"`
}

func decodeConversationContinuity(entry meetingMemoryEntry) (conversationContinuityCheckpoint, bool) {
	if entry.Kind != meetingMemoryKindConversationContinuity {
		return conversationContinuityCheckpoint{}, false
	}
	var checkpoint conversationContinuityCheckpoint
	if json.Unmarshal([]byte(entry.Text), &checkpoint) != nil || checkpoint.validate() != nil {
		return conversationContinuityCheckpoint{}, false
	}
	return checkpoint, true
}

func (app *kanbanBoardApp) latestConversationContinuity(threadID string) conversationContinuityCheckpoint {
	if app == nil || app.memory == nil || strings.TrimSpace(threadID) == "" {
		return conversationContinuityCheckpoint{}
	}
	var latest conversationContinuityCheckpoint
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindConversationContinuity, 0) {
		checkpoint, ok := decodeConversationContinuity(entry)
		if !ok || checkpoint.ThreadID != threadID {
			continue
		}
		if latest.ID == "" || checkpoint.Revision > latest.Revision ||
			(checkpoint.Revision == latest.Revision && checkpoint.UpdatedAt.After(latest.UpdatedAt)) {
			latest = checkpoint
		}
	}
	return latest
}

func (app *kanbanBoardApp) conversationContinuityForViewer(viewerEmail string, thread scoutChatThreadRecord) (conversationContinuityCheckpoint, bool) {
	if app == nil || app.memory == nil || !scoutChatThreadAllowsViewer(thread, viewerEmail) || thread.ArchivedAt != "" {
		return conversationContinuityCheckpoint{}, false
	}
	checkpoint := app.latestConversationContinuity(thread.ID)
	if checkpoint.ID == "" || checkpoint.Status != conversationContinuityStatusActive {
		return conversationContinuityCheckpoint{}, false
	}
	currentAudienceDigest := conversationContinuityAudienceDigest(thread)
	if checkpoint.AudienceDigest != currentAudienceDigest {
		return conversationContinuityCheckpoint{}, false
	}
	currentSourceDigest, _, _ := conversationContinuitySourceDigest(thread, currentAudienceDigest)
	if currentSourceDigest == "" || checkpoint.SourceDigest != currentSourceDigest {
		return conversationContinuityCheckpoint{}, false
	}
	return checkpoint, true
}

func conversationContinuityAudienceDigest(thread scoutChatThreadRecord) string {
	if audience, aclVersion, err := strideRuntimeChatAudienceAuthority(thread); err == nil {
		if digest, digestErr := STRIDEContractDigest(struct {
			Audience   STRIDEAudience `json:"audience"`
			ACLVersion int64          `json:"aclVersion"`
		}{audience, aclVersion}); digestErr == nil {
			return digest
		}
	}
	digest, err := STRIDEContractDigest(struct {
		ThreadID     string   `json:"threadId"`
		Visibility   string   `json:"visibility"`
		OwnerEmail   string   `json:"ownerEmail"`
		MemberEmails []string `json:"memberEmails"`
		ArchivedAt   string   `json:"archivedAt"`
	}{thread.ID, scoutChatThreadVisibility(thread), normalizeAccountEmail(thread.OwnerEmail), scoutChatThreadMemberEmails(thread), strings.TrimSpace(thread.ArchivedAt)})
	if err == nil {
		return digest
	}
	return sha256Hex([]byte(thread.ID + "\x00" + scoutChatThreadVisibility(thread) + "\x00" + normalizeAccountEmail(thread.OwnerEmail)))
}

func conversationContinuitySourceDigest(thread scoutChatThreadRecord, audienceDigest string) (string, []conversationContinuitySource, []string) {
	sources := make([]conversationContinuitySource, 0, len(thread.Messages))
	sourceIDs := make([]string, 0, len(thread.Messages))
	knownGaps := []string{}
	for _, message := range thread.Messages {
		if !strideIdentifier(message.ID) {
			knownGaps = append(knownGaps, "invalid_source_id")
			continue
		}
		digest, err := strideChatMessageContentDigest(false, message)
		if err != nil {
			knownGaps = append(knownGaps, "source_digest_unavailable:"+message.ID)
			continue
		}
		sources = append(sources, conversationContinuitySource{ID: message.ID, Role: strings.ToLower(strings.TrimSpace(message.Role)), Author: normalizeAccountEmail(message.AuthorEmail), Digest: digest, EditedAt: strings.TrimSpace(message.EditedAt), ReplyTo: conversationContinuityReplyID(message)})
		sourceIDs = append(sourceIDs, message.ID)
	}
	digest, err := STRIDEContractDigest(struct {
		ThreadID       string                         `json:"threadId"`
		AudienceDigest string                         `json:"audienceDigest"`
		Sources        []conversationContinuitySource `json:"sources"`
	}{thread.ID, audienceDigest, sources})
	if err != nil {
		digest = sha256Hex([]byte(thread.ID + "\x00" + audienceDigest))
	}
	return digest, sources, sortedUniqueContinuityStrings(knownGaps)
}

func conversationContinuityReplyID(message scoutChatMessageRecord) string {
	if message.ReplyTo == nil {
		return ""
	}
	return strings.TrimSpace(message.ReplyTo.MessageID)
}

func classifyConversationContinuityIntent(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	switch {
	case strings.Contains(lower, "research") || strings.Contains(lower, "compare") || strings.Contains(lower, "analy"):
		return "research"
	case strings.Contains(lower, "?") || strings.Contains(lower, "should we") || strings.Contains(lower, "what do you"):
		// A question about shipping, testing, or launching is still a question;
		// preserve the conversational decision point instead of collapsing it
		// into execution merely because the proposed action is concrete.
		return "question"
	case strings.Contains(lower, "ship") || strings.Contains(lower, "launch") || strings.Contains(lower, "build") || strings.Contains(lower, "test") || strings.Contains(lower, "deploy"):
		return "execution"
	case strings.Contains(lower, "status") || strings.Contains(lower, "update") || strings.Contains(lower, "done"):
		return "status"
	case lower != "":
		return "discussion"
	default:
		return "unknown"
	}
}

func conversationContinuityMessageIDs(thread scoutChatThreadRecord) (currentIntentClass, currentIntentDigest string, resolved, unresolved, corrections, disagreements, openLoops, activeWork []string) {
	latestHuman := ""
	for _, message := range thread.Messages {
		if message.Role == "user" && strings.TrimSpace(message.Text) != "" {
			latestHuman = strings.TrimSpace(message.Text)
		}
		if message.EditedAt != "" {
			corrections = append(corrections, message.ID)
		}
		if message.ReplyTo != nil {
			if scoutChatMessageIndex(thread, message.ReplyTo.MessageID) >= 0 {
				resolved = append(resolved, message.ReplyTo.MessageID)
			} else {
				unresolved = append(unresolved, message.ReplyTo.MessageID)
			}
		}
		lower := strings.ToLower(strings.TrimSpace(message.Text))
		if strings.Contains(lower, "i disagree") || strings.Contains(lower, "not convinced") || strings.Contains(lower, "counterargument") || strings.Contains(lower, "however") {
			disagreements = append(disagreements, message.ID)
		}
		if strings.Contains(lower, "?") || strings.Contains(lower, "follow up") || strings.Contains(lower, "next step") || strings.Contains(lower, "pending") || strings.Contains(lower, "open question") || strings.Contains(lower, "i disagree") || strings.Contains(lower, "not convinced") || strings.Contains(lower, "counterargument") {
			openLoops = append(openLoops, message.ID)
		}
		if strings.Contains(lower, "working") || strings.Contains(lower, "ship") || strings.Contains(lower, "launch") || strings.Contains(lower, "build") || strings.Contains(lower, "research") || strings.Contains(lower, "test") || strings.Contains(lower, "deploy") {
			activeWork = append(activeWork, message.ID)
		}
	}
	currentIntentClass = classifyConversationContinuityIntent(latestHuman)
	if latestHuman != "" {
		currentIntentDigest = sha256Hex([]byte(normalizeMemoryText(latestHuman)))
	}
	return currentIntentClass, currentIntentDigest, sortedUniqueContinuityStrings(resolved), sortedUniqueContinuityStrings(unresolved), sortedUniqueContinuityStrings(corrections), sortedUniqueContinuityStrings(disagreements), sortedUniqueContinuityStrings(openLoops), sortedUniqueContinuityStrings(activeWork)
}

func (app *kanbanBoardApp) buildConversationContinuityCheckpoint(thread scoutChatThreadRecord, reason string, prior conversationContinuityCheckpoint) (conversationContinuityCheckpoint, error) {
	if !strideIdentifier(thread.ID) || !scoutChatThreadAllowsViewer(thread, thread.OwnerEmail) {
		return conversationContinuityCheckpoint{}, fmt.Errorf("ConversationContinuity source thread is unauthorized")
	}
	audienceDigest := conversationContinuityAudienceDigest(thread)
	sourceDigest, sources, knownGaps := conversationContinuitySourceDigest(thread, audienceDigest)
	currentIntentClass, currentIntentDigest, resolved, unresolved, corrections, disagreements, openLoops, activeWork := conversationContinuityMessageIDs(thread)
	establishedPositionRefs := []string{}
	for _, position := range app.agentMindPositions(agentMindScoutID, "") {
		if position.Scope == thread.ID {
			established := sha256Hex([]byte(position.PositionKey + "\x00" + fmt.Sprint(position.Revision)))
			establishedPositionRefs = append(establishedPositionRefs, established)
		}
	}
	if len(unresolved) > 0 {
		knownGaps = append(knownGaps, "unresolved_reply_reference")
	}
	if len(thread.Messages) > conversationContinuityRawTailLimit {
		knownGaps = append(knownGaps, "raw_tail_bounded")
	}
	if strings.TrimSpace(reason) != "" && reason != "message" {
		knownGaps = append(knownGaps, "rebuild:"+strings.TrimSpace(reason))
	}
	knownGaps = sortedUniqueContinuityStrings(knownGaps)
	lastSourceAt := time.Now().UTC()
	if parsed, err := parseSTRIDEChatTime(thread.UpdatedAt); err == nil {
		lastSourceAt = parsed
	}
	if len(sources) > 0 {
		if parsed, err := parseSTRIDEChatTime(thread.Messages[len(thread.Messages)-1].CreatedAt); err == nil && parsed.After(lastSourceAt) {
			lastSourceAt = parsed
		}
	}
	highWater := uint64(len(sources))
	if app != nil && app.strideRuntime != nil {
		_ = app.strideRuntime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
			snapshot, err := domains.ConversationLedger.Snapshot()
			if err == nil {
				highWater = snapshot.Checkpoint.HighWater
			}
			return nil
		})
	}
	now := time.Now().UTC()
	revision := int64(1)
	if prior.Revision > 0 {
		revision = prior.Revision + 1
	}
	checkpoint := conversationContinuityCheckpoint{
		ID:                      "conversation-continuity-" + temporalDigest(thread.ID + "\x00" + sourceDigest + "\x00" + fmt.Sprint(revision))[:24],
		ThreadID:                thread.ID,
		Revision:                revision,
		Status:                  conversationContinuityStatusActive,
		AudienceDigest:          audienceDigest,
		SourceDigest:            sourceDigest,
		SourceMessageIDs:        sourceIDsFromContinuitySources(sources),
		CurrentIntentClass:      currentIntentClass,
		CurrentIntentDigest:     currentIntentDigest,
		ResolvedReferenceIDs:    resolved,
		UnresolvedReferenceIDs:  unresolved,
		EstablishedPositionRefs: establishedPositionRefs,
		DisagreementSourceIDs:   disagreements,
		CorrectionSourceIDs:     corrections,
		OpenLoopSourceIDs:       openLoops,
		ActiveWorkSourceIDs:     activeWork,
		KnownGaps:               knownGaps,
		SourceHighWater:         highWater,
		LastSourceAt:            lastSourceAt,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	if err := checkpoint.validate(); err != nil {
		return conversationContinuityCheckpoint{}, err
	}
	return checkpoint, nil
}

func sourceIDsFromContinuitySources(sources []conversationContinuitySource) []string {
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		ids = append(ids, source.ID)
	}
	return sortedUniqueStrings(ids)
}

func (app *kanbanBoardApp) appendConversationContinuity(checkpoint conversationContinuityCheckpoint) (bool, error) {
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		return false, err
	}
	_, appended, err := app.memory.appendAmbientEntry(meetingMemoryKindConversationContinuity, checkpoint.ID, string(raw), map[string]string{
		"threadId": checkpoint.ThreadID, "revision": fmt.Sprint(checkpoint.Revision), "status": checkpoint.Status,
		"audienceDigest": checkpoint.AudienceDigest, "sourceDigest": checkpoint.SourceDigest,
	})
	return appended, err
}

func (app *kanbanBoardApp) rebuildConversationContinuity(thread scoutChatThreadRecord, reason string) (conversationContinuityCheckpoint, bool, error) {
	if app == nil || app.memory == nil || !scoutChatThreadAllowsViewer(thread, thread.OwnerEmail) {
		return conversationContinuityCheckpoint{}, false, fmt.Errorf("ConversationContinuity is unavailable")
	}
	prior := app.latestConversationContinuity(thread.ID)
	currentAudience := conversationContinuityAudienceDigest(thread)
	currentSource, sources, _ := conversationContinuitySourceDigest(thread, currentAudience)
	if prior.Status == conversationContinuityStatusActive && prior.AudienceDigest == currentAudience && prior.SourceDigest == currentSource {
		return prior, false, nil
	}
	if prior.ID != "" && prior.Status == conversationContinuityStatusActive {
		invalidated := prior
		invalidated.ID = "conversation-continuity-invalidation-" + temporalDigest(prior.ID + "\x00" + reason + "\x00" + fmt.Sprint(time.Now().UnixNano()))[:24]
		invalidated.Status = conversationContinuityStatusInvalidated
		invalidated.InvalidationReason = firstNonEmptyString(strings.TrimSpace(reason), "source_changed")
		invalidated.UpdatedAt = time.Now().UTC()
		if err := invalidated.validate(); err != nil {
			return conversationContinuityCheckpoint{}, false, err
		}
		if _, err := app.appendConversationContinuity(invalidated); err != nil {
			return conversationContinuityCheckpoint{}, false, err
		}
		if len(sources) == 0 {
			return invalidated, true, nil
		}
	}
	if len(sources) == 0 {
		return prior, false, nil
	}
	checkpoint, err := app.buildConversationContinuityCheckpoint(thread, reason, prior)
	if err != nil {
		return conversationContinuityCheckpoint{}, false, err
	}
	appended, err := app.appendConversationContinuity(checkpoint)
	return checkpoint, appended, err
}

// reconcileConversationContinuityAtStartup closes the crash window between
// durable thread persistence and its derived checkpoint append. It rebuilds
// from the latest ACL-governed thread snapshot only; it never promotes private
// content into the organization conversation ledger.
func (app *kanbanBoardApp) reconcileConversationContinuityAtStartup() {
	if app == nil || app.memory == nil {
		return
	}
	latest := map[string]scoutChatThreadRecord{}
	for _, entry := range app.memory.snapshot(0) {
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok || !strideIdentifier(thread.ID) {
			continue
		}
		latest[thread.ID] = thread
	}
	threadIDs := make([]string, 0, len(latest))
	for threadID := range latest {
		threadIDs = append(threadIDs, threadID)
	}
	sort.Strings(threadIDs)
	for _, threadID := range threadIDs {
		thread := latest[threadID]
		if strings.TrimSpace(thread.ArchivedAt) != "" || len(thread.Messages) == 0 || !scoutChatThreadAllowsViewer(thread, thread.OwnerEmail) {
			continue
		}
		if _, _, err := app.rebuildConversationContinuity(thread, "startup_reconcile"); err != nil {
			log.Errorf("ConversationContinuity startup reconciliation unavailable for %s: %v", thread.ID, err)
		}
	}
}

// Private threads do not enter the organization STRIDE conversation ledger,
// but they still need the same bounded continuity projection for the owner’s
// next turn. Keep this hook beside the public observer so every mutation path
// can refresh private continuity without widening its audience.
func (app *kanbanBoardApp) rebuildPrivateConversationContinuity(thread scoutChatThreadRecord, reason string) {
	if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic {
		return
	}
	if _, _, err := app.rebuildConversationContinuity(thread, reason); err != nil {
		log.Errorf("ConversationContinuity private-thread rebuild unavailable: %v", err)
	}
}

func (app *kanbanBoardApp) prepareConversationContinuityModelQuery(viewerEmail string, thread scoutChatThreadRecord, query string) string {
	checkpoint, ok := app.conversationContinuityForViewer(viewerEmail, thread)
	if !ok {
		return query
	}
	return query + "\n\n[STRIDE conversation continuity: revision=" + fmt.Sprint(checkpoint.Revision) + "; intent=" + checkpoint.CurrentIntentClass +
		"; source_digest=" + checkpoint.SourceDigest + "; source_messages=" + strings.Join(checkpoint.SourceMessageIDs, ",") +
		"; resolved_refs=" + strings.Join(checkpoint.ResolvedReferenceIDs, ",") + "; unresolved_refs=" + strings.Join(checkpoint.UnresolvedReferenceIDs, ",") +
		"; open_loops=" + strings.Join(checkpoint.OpenLoopSourceIDs, ",") + "; active_work=" + strings.Join(checkpoint.ActiveWorkSourceIDs, ",") +
		"; gaps=" + strings.Join(checkpoint.KnownGaps, ",") + "; bodies remain in the current ACL-authorized chat history and must not be inferred from this envelope.]"
}

func sortedUniqueContinuityStrings(values []string) []string {
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
