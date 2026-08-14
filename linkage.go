package main

// Linkage graph + board auto-advance: proposals capture the board card they
// deliver at propose time, confirm stamps the card/proposal ids onto the
// thread artifact (and moves the card to In Progress), and the two terminal
// worker seams advance the linked card when the artifact lands (complete →
// In Progress with the deliverable attached — a human decides Done;
// failed/error/approval_required → Blocked). No kanbanCard schema
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

// advanceLinkedCard is retained as a compatibility seam for historical
// artifacts that still carry boardCardId. The Board is read-only history now:
// new or retried Work must never mutate those archived cards.
func (app *kanbanBoardApp) advanceLinkedCard(cardID string, status kanbanStatus, why string) {
	_ = app
	_ = cardID
	_ = status
	_ = why
}

// syncLinkedCardForArtifact preserves the non-Board completion contract. A
// completed deliverable can still file into its explicit venture package, but
// it neither discovers nor mutates archived Kanban cards.
func (app *kanbanBoardApp) syncLinkedCardForArtifact(artifact meetingMemoryEntry, terminalStatus string) {
	if app == nil || app.memory == nil || strings.TrimSpace(artifact.ID) == "" {
		return
	}
	if strings.ToLower(strings.TrimSpace(terminalStatus)) != codexJobStatusComplete {
		return
	}

	// Package binder closure: a COMPLETED artifact that carries a propose-time
	// packageId files itself into its venture package. attachToPackage is
	// idempotent, so callback retries are safe; failures only lose the binder
	// link, never the board advance below.
	if packageID := strings.TrimSpace(artifact.Metadata["packageId"]); packageID != "" {
		if _, err := app.attachToPackage(packageID, packageRefTypeArtifact, artifact.ID, scoutParticipantName); err != nil {
			log.Errorf("Failed to attach artifact %s to package %s: %v", artifact.ID, packageID, err)
		}
	}
}
