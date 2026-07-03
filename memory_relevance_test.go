package main

import (
	"testing"
	"time"
)

// backdateMemoryEntry rewrites an entry's CreatedAt so age-gated logic (the
// 7-day slop eligibility gate) can be exercised without waiting.
func backdateMemoryEntry(store *meetingMemoryStore, id string, age time.Duration) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.entries {
		if store.entries[index].ID == id {
			store.entries[index].CreatedAt = time.Now().UTC().Add(-age)
		}
	}
}

func searchContainsID(matches []meetingMemoryMatch, id string) bool {
	for _, match := range matches {
		if match.Entry.ID == id {
			return true
		}
	}
	return false
}

func TestSearchExcludesQuarantinedAndExpired(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, ok, err := app.memory.appendTranscript("t-keep", "", "The runway spans eighteen months of operating cash."); err != nil || !ok {
		t.Fatalf("append t-keep: ok=%v err=%v", ok, err)
	}
	if _, ok, err := app.memory.appendTranscript("t-quar", "", "The runway note here is redundant chatter about runway."); err != nil || !ok {
		t.Fatalf("append t-quar: ok=%v err=%v", ok, err)
	}
	if _, ok, err := app.memory.appendTranscript("t-exp", "", "Another stray runway remark that has expired."); err != nil || !ok {
		t.Fatalf("append t-exp: ok=%v err=%v", ok, err)
	}

	if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindTranscript, "t-quar", "The runway note here is redundant chatter about runway.", map[string]string{relevanceMetadataKey: relevanceQuarantined}); err != nil {
		t.Fatalf("quarantine t-quar: %v", err)
	}
	if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindTranscript, "t-exp", "Another stray runway remark that has expired.", map[string]string{relevanceMetadataKey: relevanceExpired}); err != nil {
		t.Fatalf("expire t-exp: %v", err)
	}

	matches := app.memory.search("runway", 10)
	if !searchContainsID(matches, "t-keep") {
		t.Fatal("active entry t-keep must remain searchable")
	}
	if searchContainsID(matches, "t-quar") {
		t.Fatal("quarantined entry t-quar must be excluded from search")
	}
	if searchContainsID(matches, "t-exp") {
		t.Fatal("expired entry t-exp must be excluded from search")
	}

	// snapshot (the client-timeline funnel) hides them too.
	for _, entry := range app.memory.snapshot(0) {
		if entry.ID == "t-quar" || entry.ID == "t-exp" {
			t.Fatalf("snapshot must not surface hidden entry %s", entry.ID)
		}
	}
}

func TestRestoreReincludesEntryInSearch(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, ok, err := app.memory.appendTranscript("t-1", "", "The pricing tier for the grill product is finalized."); err != nil || !ok {
		t.Fatalf("append t-1: ok=%v err=%v", ok, err)
	}
	if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindTranscript, "t-1", "The pricing tier for the grill product is finalized.", map[string]string{relevanceMetadataKey: relevanceQuarantined}); err != nil {
		t.Fatalf("quarantine t-1: %v", err)
	}
	if searchContainsID(app.memory.search("pricing", 10), "t-1") {
		t.Fatal("quarantined entry must be excluded before restore")
	}

	if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindTranscript, "t-1", "The pricing tier for the grill product is finalized.", map[string]string{relevanceMetadataKey: relevanceActive}); err != nil {
		t.Fatalf("restore t-1: %v", err)
	}
	if !searchContainsID(app.memory.search("pricing", 10), "t-1") {
		t.Fatal("restored entry must re-enter search immediately")
	}
}

func TestSearchDownRanksArchived(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, ok, err := app.memory.appendTranscript("t-active", "", "The competitor pricing comparison is our live pricing benchmark."); err != nil || !ok {
		t.Fatalf("append active: ok=%v err=%v", ok, err)
	}
	if _, ok, err := app.memory.appendTranscript("t-archived", "", "The competitor pricing comparison from the killed deal, our pricing benchmark."); err != nil || !ok {
		t.Fatalf("append archived: ok=%v err=%v", ok, err)
	}
	if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindTranscript, "t-archived", "The competitor pricing comparison from the killed deal, our pricing benchmark.", map[string]string{relevanceMetadataKey: relevanceArchived}); err != nil {
		t.Fatalf("archive t-archived: %v", err)
	}

	matches := app.memory.search("pricing benchmark comparison", 10)
	if !searchContainsID(matches, "t-archived") {
		t.Fatal("archived entry must remain searchable (down-ranked, not excluded)")
	}
	if len(matches) < 2 || matches[0].Entry.ID != "t-active" {
		t.Fatalf("active entry must out-rank archived; got order %v", func() []string {
			ids := make([]string, 0, len(matches))
			for _, m := range matches {
				ids = append(ids, m.Entry.ID)
			}
			return ids
		}())
	}
}

func TestQueryExpansionSurfacesSynonym(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	// The entry speaks of "runway"; the question asks about "cash burn". Only
	// query expansion (runway ⇆ cash/burn) bridges the vocabulary gap — the raw
	// query tokens never appear in the entry text.
	if _, ok, err := app.memory.appendTranscript("t-runway", "", "Our runway extends eighteen months at the current spend."); err != nil || !ok {
		t.Fatalf("append: ok=%v err=%v", ok, err)
	}

	if !searchContainsID(app.memory.search("how much cash burn do we have", 10), "t-runway") {
		t.Fatal("query expansion should surface the runway entry for a cash-burn question")
	}
	// sanity: an unrelated synonym-free query must NOT surface it.
	if searchContainsID(app.memory.search("what did tyler design", 10), "t-runway") {
		t.Fatal("unrelated query must not match the runway entry")
	}
}

// TestReconciliationRanksArtifactBodyAboveBoardCard protects the simulation's
// D→A fix: a reconciliation-flavored question that names a completed artifact
// skips the board short-circuit (queryPrefersArtifactContext) and the artifact
// BODY enters model context, so Scout answers from the numbers, not the card.
func TestReconciliationRanksArtifactBodyAboveBoardCard(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	body := "# Comp Set Analysis\nThe comparable-company median revenue multiple is 4.2x.\nJoel said the median was 4.2x on the call."
	metadata := map[string]string{
		"title":        "Comp Set Analysis",
		"query":        "comp set median revenue multiple",
		"status":       "complete",
		"threadStatus": "complete",
	}
	if _, ok, err := app.memory.appendOSArtifact("os-artifact-comp", body, metadata); err != nil || !ok {
		t.Fatalf("append artifact: ok=%v err=%v", ok, err)
	}

	query := "does the comp set median match what Joel said, reconcile it"
	if !app.queryPrefersArtifactContext(query) {
		t.Fatal("a reconciliation question naming the artifact must prefer artifact context over the board card")
	}

	context := app.memory.contextEntriesForQuery(query, defaultMemoryQuestionContextLimit, time.Now())
	found := false
	for _, entry := range context {
		if entry.ID == "os-artifact-comp" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("the completed artifact body must enter recall context for the reconciliation question")
	}
}
