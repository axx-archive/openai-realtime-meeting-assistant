package main

// Linkage graph + board auto-advance: proposals capture the board card they
// deliver at propose time, confirm stamps the card/proposal ids onto the
// thread artifact (and moves the card to In Progress), and the two terminal
// worker seams advance the linked card when the artifact lands (complete →
// Done, failed/error/approval_required → Blocked). No kanbanCard schema
// change — all linkage lives in memory-entry metadata (proposal: "cardId";
// artifact: "boardCardId","proposalId").

import (
	"strings"
)

// linkageFuzzyMatchThreshold is the minimum token-set Jaccard overlap for a
// title to bind to a board card without an explicit card_id.
const linkageFuzzyMatchThreshold = 0.6

// linkageAmbiguityMargin: when the two best fuzzy candidates score within
// this margin of each other the match is ambiguous and no link is made. A
// missed link is cheap; a wrong auto-move is not.
const linkageAmbiguityMargin = 0.1

// linkageMatchTokens normalizes a title/query into the comparable token set
// used for fuzzy card matching.
func linkageMatchTokens(value string) []string {
	return uniqueMemoryTokens(canonicalizeDomainTerms(strings.ToLower(canonicalizeBoardText(value))))
}

// tokenSetJaccard computes |A∩B| / |A∪B| over two token slices.
func tokenSetJaccard(a []string, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	setA := make(map[string]struct{}, len(a))
	for _, token := range a {
		setA[token] = struct{}{}
	}
	setB := make(map[string]struct{}, len(b))
	for _, token := range b {
		setB[token] = struct{}{}
	}
	intersection := 0
	for token := range setA {
		if _, ok := setB[token]; ok {
			intersection++
		}
	}
	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}

	return float64(intersection) / float64(union)
}

// matchBoardCard resolves the board card a title refers to. An explicit
// card id wins (any status) or fails outright — no fuzzy fallback for a
// stale id. Otherwise the title fuzzy-matches against non-Done cards:
// a candidate needs token-set Jaccard >= 0.6 or full normalized containment,
// and the single best candidate must beat the runner-up by more than 0.1 or
// the match is treated as ambiguous and dropped.
func (app *kanbanBoardApp) matchBoardCard(title string, explicitCardID string) (kanbanCard, bool) {
	if app == nil {
		return kanbanCard{}, false
	}
	explicitCardID = strings.TrimSpace(explicitCardID)
	if explicitCardID != "" {
		app.mu.Lock()
		card, ok := app.findCardLocked(explicitCardID)
		if !ok {
			app.mu.Unlock()
			return kanbanCard{}, false
		}
		cloned := cloneKanbanCard(*card)
		app.mu.Unlock()
		return cloned, true
	}

	titleNormalized := strings.ToLower(canonicalizeBoardText(title))
	titleTokens := linkageMatchTokens(title)
	if titleNormalized == "" || len(titleTokens) == 0 {
		return kanbanCard{}, false
	}

	best := kanbanCard{}
	bestScore := 0.0
	secondScore := 0.0
	for _, card := range app.snapshotState().Cards {
		if card.Status == kanbanStatusDone {
			continue
		}
		cardNormalized := strings.ToLower(canonicalizeBoardText(card.Title))
		if cardNormalized == "" {
			continue
		}
		score := tokenSetJaccard(titleTokens, linkageMatchTokens(card.Title))
		if strings.Contains(titleNormalized, cardNormalized) || strings.Contains(cardNormalized, titleNormalized) {
			score = 1.0
		}
		if score < linkageFuzzyMatchThreshold {
			continue
		}
		if score > bestScore {
			secondScore = bestScore
			bestScore = score
			best = card
		} else if score > secondScore {
			secondScore = score
		}
	}
	if bestScore < linkageFuzzyMatchThreshold {
		return kanbanCard{}, false
	}
	if secondScore > 0 && bestScore-secondScore < linkageAmbiguityMargin {
		return kanbanCard{}, false
	}

	return best, true
}

// advanceLinkedCard moves a linked card through the shared applyToolCallArgs
// dispatch — the same mutation path every other board writer uses, so
// moveTicket's idempotence (changed=false when already there) makes callback
// retries safe and unknown card ids are logged and swallowed. Every real move
// is broadcast: renderBoard's card diff already synthesizes a move toast, so
// the client needs zero new code for visibility.
func (app *kanbanBoardApp) advanceLinkedCard(cardID string, status kanbanStatus, why string) {
	cardID = strings.TrimSpace(cardID)
	if app == nil || cardID == "" {
		return
	}
	_, changed, err := app.applyToolCallArgs("move_ticket", map[string]any{
		"card_id": cardID,
		"status":  string(status),
	})
	if err != nil {
		log.Errorf("Failed to advance linked card %s to %s: %v", cardID, status, err)
		return
	}
	if !changed {
		return
	}
	broadcastSignedInKanbanEvent("board", app.snapshotState())
	broadcastSignedInKanbanEvent("undo_available", app.canUndoDelete())
	broadcastAssistantEvent("action", "Scout moved a linked card to "+string(status)+" — "+why, map[string]any{"kind": "board_linkage"})
	app.refreshRealtimeBoardContext("board linkage")
}

// syncLinkedCardForArtifact advances the board card linked to a finished
// thread artifact. Column semantics follow the board's own status rules
// ("completed/finished means Done; blocked/waiting means Blocked"): complete
// → Done because the deliverable exists; failed/error and approval_required
// → Blocked because a human gate IS a wait state. Artifact-content review
// stays the artifact surface's reviewGate concern, not a board column. When
// the artifact carries no boardCardId (direct launch_agent_thread path), a
// completion-time fuzzy match by title/query links it, and the id is stamped
// back onto the artifact so retries are stable.
func (app *kanbanBoardApp) syncLinkedCardForArtifact(artifact meetingMemoryEntry, terminalStatus string) {
	if app == nil || app.memory == nil || strings.TrimSpace(artifact.ID) == "" {
		return
	}
	var status kanbanStatus
	switch strings.ToLower(strings.TrimSpace(terminalStatus)) {
	case codexJobStatusComplete:
		status = kanbanStatusDone
	case codexJobStatusFailed, "error":
		status = kanbanStatusBlocked
	case codexJobStatusApprovalRequired:
		status = kanbanStatusBlocked
	default:
		return
	}

	// Package binder closure: a COMPLETED artifact that carries a propose-time
	// packageId files itself into its venture package. attachToPackage is
	// idempotent, so callback retries are safe; failures only lose the binder
	// link, never the board advance below.
	if status == kanbanStatusDone {
		if packageID := strings.TrimSpace(artifact.Metadata["packageId"]); packageID != "" {
			if _, err := app.attachToPackage(packageID, packageRefTypeArtifact, artifact.ID, scoutParticipantName); err != nil {
				log.Errorf("Failed to attach artifact %s to package %s: %v", artifact.ID, packageID, err)
			}
		}
	}

	title := strings.TrimSpace(artifact.Metadata["title"])
	threadQuery := strings.TrimSpace(artifact.Metadata["threadQuery"])
	cardID := strings.TrimSpace(artifact.Metadata["boardCardId"])
	if cardID == "" {
		// Completed artifacts usually carry a body-derived display title, so
		// try the stored title first, then the original launch prompt — the
		// string the board worker most likely mirrored into a card.
		card, ok := app.matchBoardCard(title, "")
		if !ok && threadQuery != "" && threadQuery != title {
			card, ok = app.matchBoardCard(threadQuery, "")
		}
		if !ok {
			return
		}
		cardID = card.ID
		if _, _, err := app.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "", map[string]string{"boardCardId": cardID}); err != nil {
			log.Errorf("Failed to stamp boardCardId on artifact %s: %v", artifact.ID, err)
		}
	}

	app.advanceLinkedCard(cardID, status, firstNonEmptyString(title, threadQuery, "linked work finished"))
}
