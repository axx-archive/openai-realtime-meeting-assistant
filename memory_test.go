package main

import (
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestMeetingMemoryPersistsAndSearchesTranscripts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")

	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	if _, appended, err := store.appendTranscript("event-1", "item-1", "  Alice said launch is blocked by billing review. "); err != nil {
		t.Fatalf("appendTranscript: %v", err)
	} else if !appended {
		t.Fatal("appendTranscript appended=false, want true")
	}

	reloaded, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("reload memory store: %v", err)
	}

	matches := reloaded.search("billing review", 3)
	if len(matches) != 1 {
		t.Fatalf("search matches=%d, want 1", len(matches))
	}
	if got := matches[0].Entry.Text; !strings.Contains(got, "billing review") {
		t.Fatalf("match text %q does not include expected phrase", got)
	}
}

func TestMeetingMemoryDedupesEventIDs(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	if _, appended, err := store.appendTranscript("event-1", "item-1", "First version."); err != nil {
		t.Fatalf("first append: %v", err)
	} else if !appended {
		t.Fatal("first append appended=false, want true")
	}
	if _, appended, err := store.appendTranscript("event-1", "item-1", "Duplicate version."); err != nil {
		t.Fatalf("second append: %v", err)
	} else if appended {
		t.Fatal("second append appended=true, want false")
	}

	entries := store.snapshot(10)
	if len(entries) != 1 {
		t.Fatalf("entries=%d, want 1", len(entries))
	}
}

func TestMeetingMemorySnapshotsOnlyRequestedMeeting(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	oldEntry, _, err := store.appendTranscript("event-old", "item-old", "Old meeting decision should stay out.")
	if err != nil {
		t.Fatalf("append old transcript: %v", err)
	}
	oldMeetingID := oldEntry.Metadata["meetingId"]
	store.rotateMeetingID(officeRoomID)
	currentEntry, _, err := store.appendTranscript("event-current", "item-current", "Current meeting decision belongs here.")
	if err != nil {
		t.Fatalf("append current transcript: %v", err)
	}
	currentMeetingID := currentEntry.Metadata["meetingId"]
	if currentMeetingID == "" || currentMeetingID == oldMeetingID {
		t.Fatalf("current meetingId=%q old=%q, want distinct ids", currentMeetingID, oldMeetingID)
	}

	entries := store.snapshotForMeeting(currentMeetingID, 10)
	if len(entries) != 1 {
		t.Fatalf("meeting snapshot entries=%d, want 1", len(entries))
	}
	if entries[0].ID != currentEntry.ID {
		t.Fatalf("meeting snapshot entry=%q, want %q", entries[0].ID, currentEntry.ID)
	}
	if leaked := store.snapshotForMeeting(oldMeetingID, 10); len(leaked) != 1 || leaked[0].ID != oldEntry.ID {
		t.Fatalf("old meeting snapshot=%v, want old entry only", leaked)
	}
	if empty := store.snapshotForMeeting("", 10); len(empty) != 0 {
		t.Fatalf("empty meeting snapshot=%v, want none", empty)
	}
}

func TestDurableTimestampIDDifferentiatesSameSecondEvents(t *testing.T) {
	first := time.Date(2026, 6, 17, 12, 34, 56, 123, time.UTC)
	second := time.Date(2026, 6, 17, 12, 34, 56, 456, time.UTC)

	firstID := durableTimestampID("brain", first)
	secondID := durableTimestampID("brain", second)
	if firstID == secondID {
		t.Fatalf("same-second durable ids collided: %q", firstID)
	}
	if !strings.HasSuffix(firstID, "-000000123") || !strings.HasSuffix(secondID, "-000000456") {
		t.Fatalf("durable ids do not include nanoseconds: %q %q", firstID, secondID)
	}
}

func TestMeetingMemoryCanonicalizesAndSkipsWeakTranscriptFragments(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	entry, appended, err := store.appendTranscript("event-1", "item-1", " Suit Barn rollout is blocked by Web RTC review. ")
	if err != nil {
		t.Fatalf("appendTranscript: %v", err)
	}
	if !appended {
		t.Fatal("appendTranscript appended=false, want true")
	}
	if !strings.Contains(entry.Text, "Boot Barn") || !strings.Contains(entry.Text, "WebRTC") {
		t.Fatalf("entry text was not canonicalized: %q", entry.Text)
	}

	if _, appended, err := store.appendTranscript("event-2", "item-2", "the"); err != nil {
		t.Fatalf("weak append: %v", err)
	} else if appended {
		t.Fatal("weak transcript appended=true, want false")
	}

	if entries := store.snapshot(10); len(entries) != 1 {
		t.Fatalf("entries=%d, want 1", len(entries))
	}
}

func TestMeetingMemoryAttributesTranscriptSpeaker(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	entry, appended, err := store.appendAttributedTranscript("event-1", "item-1", "tom", "dominant", "Boot Barn meeting went well.")
	if err != nil {
		t.Fatalf("appendAttributedTranscript: %v", err)
	}
	if !appended {
		t.Fatal("appendAttributedTranscript appended=false, want true")
	}
	if entry.Text != "Tom: Boot Barn meeting went well." {
		t.Fatalf("entry text=%q, want speaker-prefixed transcript", entry.Text)
	}
	if entry.Metadata["speaker"] != "Tom" {
		t.Fatalf("speaker metadata=%q, want Tom", entry.Metadata["speaker"])
	}
	if entry.Metadata["speakerConfidence"] != "dominant" {
		t.Fatalf("speaker confidence=%q, want dominant", entry.Metadata["speakerConfidence"])
	}
}

func TestMeetingMemoryLoadsLargeEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	largeTranscript := strings.Repeat("billing review is still blocking launch. ", 3000)
	if _, appended, err := store.appendTranscript("event-large", "item-large", largeTranscript); err != nil {
		t.Fatalf("append large transcript: %v", err)
	} else if !appended {
		t.Fatal("large transcript appended=false, want true")
	}

	reloaded, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("reload memory store: %v", err)
	}
	if entries := reloaded.snapshot(10); len(entries) != 1 {
		t.Fatalf("entries=%d, want 1", len(entries))
	}
}

func TestMeetingMemoryPersistsOSArtifactsWithStructure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	body := "Research brief\n\n1. Evidence lane\n2. Contrarian lane"
	entry, appended, err := store.appendOSArtifact("artifact-1", body, map[string]string{
		"mode":  "research",
		"title": "Research brief",
	})
	if err != nil {
		t.Fatalf("appendOSArtifact: %v", err)
	}
	if !appended {
		t.Fatal("appendOSArtifact appended=false, want true")
	}
	if entry.Kind != meetingMemoryKindOSArtifact {
		t.Fatalf("kind=%q, want %q", entry.Kind, meetingMemoryKindOSArtifact)
	}
	if !strings.Contains(entry.Text, "\n\n1. Evidence lane") {
		t.Fatalf("artifact text lost structure: %q", entry.Text)
	}

	reloaded, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("reload memory store: %v", err)
	}
	entries := reloaded.snapshot(10)
	if len(entries) != 1 || entries[0].Kind != meetingMemoryKindOSArtifact {
		t.Fatalf("entries=%v, want one OS artifact", entries)
	}
}

func TestMeetingMemoryUpdatesOSArtifactAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	if _, appended, err := store.appendOSArtifact("artifact-1", "Draft body", map[string]string{
		"mode":  "design",
		"title": "Draft",
	}); err != nil {
		t.Fatalf("appendOSArtifact: %v", err)
	} else if !appended {
		t.Fatal("appendOSArtifact appended=false, want true")
	}

	updated, changed, err := store.updateOSArtifact("artifact-1", "Edited title", "Edited body\n\nKeep structure.", "AJ")
	if err != nil {
		t.Fatalf("updateOSArtifact: %v", err)
	}
	if !changed {
		t.Fatal("updateOSArtifact changed=false, want true")
	}
	if updated.Text != "Edited body\n\nKeep structure." {
		t.Fatalf("updated text=%q, want edited structured body", updated.Text)
	}
	if updated.Metadata["title"] != "Edited title" || updated.Metadata["updatedBy"] != "AJ" || updated.Metadata["updatedAt"] == "" {
		t.Fatalf("updated metadata=%v, want title/updater/timestamp", updated.Metadata)
	}

	reloaded, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("reload memory store: %v", err)
	}
	entries := reloaded.snapshot(10)
	if len(entries) != 1 {
		t.Fatalf("entries=%d, want 1", len(entries))
	}
	if entries[0].Text != updated.Text || entries[0].Metadata["title"] != "Edited title" {
		t.Fatalf("reloaded entry=%#v, want edited artifact", entries[0])
	}
}

// A body edit mints version+1 with lineage intact — no matter which writer
// performed it, because every writer funnels through
// updateOSArtifactWithMetadata — while title- and metadata-only rewrites never
// mint versions. Lineage survives a reload.
func TestOSArtifactBodyEditMintsVersionLineage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	created, appended, err := store.appendOSArtifact("artifact-1", "Draft body v1", map[string]string{
		"mode":      "research",
		"title":     "Draft",
		"createdBy": "Scout",
	})
	if err != nil || !appended {
		t.Fatalf("appendOSArtifact appended=%v err=%v, want true/nil", appended, err)
	}
	if got := artifactVersion(created); got != 1 {
		t.Fatalf("fresh artifactVersion=%d, want 1", got)
	}
	if got := artifactParentVersionID(created); got != "" {
		t.Fatalf("fresh parentVersionId=%q, want empty", got)
	}

	edited, changed, err := store.updateOSArtifact("artifact-1", "Draft", "Edited body v2", "AJ")
	if err != nil || !changed {
		t.Fatalf("updateOSArtifact changed=%v err=%v, want true/nil", changed, err)
	}
	if got := artifactVersion(edited); got != 2 {
		t.Fatalf("edited artifactVersion=%d, want 2", got)
	}
	if got := artifactParentVersionID(edited); got != "artifact-1@v1" {
		t.Fatalf("parentVersionId=%q, want artifact-1@v1", got)
	}
	history := artifactVersionHistory(edited)
	if len(history) != 1 || history[0].V != 1 {
		t.Fatalf("version history=%+v, want one superseded v1 record", history)
	}
	if history[0].EditedBy != "Scout" || history[0].At == "" {
		t.Fatalf("v1 record=%+v, want the superseded version's editor and timestamp", history[0])
	}

	// A second body edit chains the lineage: v3 pointing at v2, journal keeps v1+v2.
	edited, changed, err = store.updateOSArtifact("artifact-1", "", "Edited body v3", "Tom")
	if err != nil || !changed {
		t.Fatalf("second updateOSArtifact changed=%v err=%v, want true/nil", changed, err)
	}
	if got := artifactVersion(edited); got != 3 {
		t.Fatalf("artifactVersion=%d, want 3", got)
	}
	if got := artifactParentVersionID(edited); got != "artifact-1@v2" {
		t.Fatalf("parentVersionId=%q, want artifact-1@v2", got)
	}
	history = artifactVersionHistory(edited)
	if len(history) != 2 || history[0].V != 1 || history[1].V != 2 {
		t.Fatalf("version history=%+v, want v1 then v2", history)
	}
	if history[1].EditedBy != "AJ" {
		t.Fatalf("v2 record editedBy=%q, want AJ (the editor who authored v2)", history[1].EditedBy)
	}

	// Metadata-only rewrites are bookkeeping, not edits: no version mint.
	if _, changed, err := store.updateOSArtifactWithMetadata("artifact-1", "", edited.Text, "AJ", map[string]string{
		"status": "published",
	}); err != nil || !changed {
		t.Fatalf("metadata-only update changed=%v err=%v, want true/nil", changed, err)
	}
	if _, changed, err := store.updateOSArtifactMetadata("artifact-1", map[string]string{"openedAt": "2026-07-05T00:00:00Z"}); err != nil || !changed {
		t.Fatalf("bookkeeping stamp changed=%v err=%v, want true/nil", changed, err)
	}

	reloaded, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("reload memory store: %v", err)
	}
	entry, found := reloaded.entryByKindAndID(meetingMemoryKindOSArtifact, "artifact-1")
	if !found {
		t.Fatal("artifact missing after reload")
	}
	if got := artifactVersion(entry); got != 3 {
		t.Fatalf("reloaded artifactVersion=%d, want 3 (metadata-only writes must not mint versions)", got)
	}
	if got := len(artifactVersionHistory(entry)); got != 2 {
		t.Fatalf("reloaded history length=%d, want 2", got)
	}
}

// The blob seam degrades exactly like an absent codex sidecar: wired, the
// superseded body snapshot lands as a bodyBlobRef; erroring or absent (nil),
// the version record is kept without a ref and the edit still succeeds.
func TestOSArtifactVersionBlobSeamDegradesGracefully(t *testing.T) {
	previousSeam := artifactVersionBlobStore
	t.Cleanup(func() { artifactVersionBlobStore = previousSeam })

	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	if _, appended, err := store.appendOSArtifact("artifact-1", "Body v1", map[string]string{"mode": "research", "title": "T"}); err != nil || !appended {
		t.Fatalf("appendOSArtifact appended=%v err=%v, want true/nil", appended, err)
	}

	// Wired seam: the superseded body reaches the blob store and its ref rides
	// the version record.
	var gotBody string
	artifactVersionBlobStore = func(artifactID string, version int, body string) (string, error) {
		if artifactID != "artifact-1" || version != 1 {
			t.Fatalf("blob seam called with %s v%d, want artifact-1 v1", artifactID, version)
		}
		gotBody = body
		return "blob:abc123", nil
	}
	edited, _, err := store.updateOSArtifact("artifact-1", "", "Body v2", "AJ")
	if err != nil {
		t.Fatalf("updateOSArtifact: %v", err)
	}
	if gotBody != "Body v1" {
		t.Fatalf("blob seam received body=%q, want the superseded v1 body", gotBody)
	}
	if history := artifactVersionHistory(edited); len(history) != 1 || history[0].BodyBlobRef != "blob:abc123" {
		t.Fatalf("history=%+v, want v1 record carrying blob:abc123", history)
	}

	// Erroring seam: lineage is kept without the ref; the edit never fails.
	artifactVersionBlobStore = func(string, int, string) (string, error) {
		return "", filepath.ErrBadPattern
	}
	edited, changed, err := store.updateOSArtifact("artifact-1", "", "Body v3", "AJ")
	if err != nil || !changed {
		t.Fatalf("updateOSArtifact with erroring seam changed=%v err=%v, want true/nil", changed, err)
	}
	history := artifactVersionHistory(edited)
	if len(history) != 2 || history[1].V != 2 || history[1].BodyBlobRef != "" {
		t.Fatalf("history=%+v, want a refless v2 record", history)
	}

	// Absent seam (keyless-style deploy before blobs.go wires it): same shape.
	artifactVersionBlobStore = nil
	edited, changed, err = store.updateOSArtifact("artifact-1", "", "Body v4", "AJ")
	if err != nil || !changed {
		t.Fatalf("updateOSArtifact with nil seam changed=%v err=%v, want true/nil", changed, err)
	}
	if got := artifactVersion(edited); got != 4 {
		t.Fatalf("artifactVersion=%d, want 4", got)
	}
}

// Old artifacts (written before the model was formalized) read back
// identically: version 1, no parent, no history — and a malformed journal
// degrades to no history instead of an error.
func TestArtifactVersionAccessorsBackwardCompat(t *testing.T) {
	old := meetingMemoryEntry{ID: "artifact-old", Kind: meetingMemoryKindOSArtifact, Text: "Body"}
	if got := artifactVersion(old); got != 1 {
		t.Fatalf("no-metadata artifactVersion=%d, want 1", got)
	}
	if got := artifactParentVersionID(old); got != "" {
		t.Fatalf("no-metadata parentVersionId=%q, want empty", got)
	}
	if got := artifactVersionHistory(old); got != nil {
		t.Fatalf("no-metadata history=%v, want nil", got)
	}

	malformed := meetingMemoryEntry{Metadata: map[string]string{
		artifactVersionMetadataKey:  "not-a-number",
		artifactVersionsMetadataKey: "{broken json",
	}}
	if got := artifactVersion(malformed); got != 1 {
		t.Fatalf("malformed artifactVersion=%d, want 1", got)
	}
	if got := artifactVersionHistory(malformed); got != nil {
		t.Fatalf("malformed history=%v, want nil", got)
	}
	// The journal restarts on a malformed value rather than blocking the edit.
	if appended := appendArtifactVersionRecord("{broken json", artifactVersionRecord{V: 1}); !strings.Contains(appended, `"v":1`) {
		t.Fatalf("appendArtifactVersionRecord=%q, want a fresh journal containing the record", appended)
	}
}

func TestMeetingMemoryUpdatesOSArtifactMetadataAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	if _, appended, err := store.appendOSArtifact("artifact-1", "Draft body", map[string]string{
		"mode":      "workflow",
		"title":     "Draft",
		"published": "false",
		"status":    "draft",
	}); err != nil {
		t.Fatalf("appendOSArtifact: %v", err)
	} else if !appended {
		t.Fatal("appendOSArtifact appended=false, want true")
	}

	updated, changed, err := store.updateOSArtifactWithMetadata("artifact-1", "Draft", "Draft body", "AJ", map[string]string{
		"published":   "true",
		"status":      "published",
		"publishedBy": "AJ",
	})
	if err != nil {
		t.Fatalf("updateOSArtifactWithMetadata: %v", err)
	}
	if !changed {
		t.Fatal("updateOSArtifactWithMetadata changed=false, want true")
	}
	if updated.Text != "Draft body" || updated.Metadata["published"] != "true" || updated.Metadata["status"] != "published" {
		t.Fatalf("updated entry=%#v, want metadata-only publish", updated)
	}

	reloaded, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("reload memory store: %v", err)
	}
	entries := reloaded.snapshot(10)
	if len(entries) != 1 || entries[0].Metadata["published"] != "true" || entries[0].Metadata["publishedBy"] != "AJ" {
		t.Fatalf("reloaded entries=%#v, want published metadata", entries)
	}
}

// A packaging deck (Wave 3 print chassis + Wave 5 base64-inlined imagery) is a
// multi-megabyte artifact filed as ONE JSONL line. The loader's bufio.Scanner
// capped tokens at 1MB and hard-FAILED the entire load on a longer line — so
// one shipped deck disabled ALL meeting memory on the next restart (live prod
// incident). The loader must read arbitrarily long lines and never let one
// oversized entry drop the rest.
func TestMeetingMemoryLoadsMultiMegabyteArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	// A 3MB deck body — larger than the old 1MB scanner cap.
	bigDeck := "<!doctype html><html><body>" + strings.Repeat("<section class=\"pg\">slide with data:image/png;base64,"+strings.Repeat("A", 4000)+"</section>", 800) + "</body></html>"
	if len(bigDeck) < 2<<20 {
		t.Fatalf("test deck is only %d bytes, want >2MB to exceed the old cap", len(bigDeck))
	}
	if _, _, err := store.appendOSArtifact("os-artifact-workflow-bigdeck", bigDeck, map[string]string{"source": "packaging_studio_ship", "type": "html_deck"}); err != nil {
		t.Fatalf("appendOSArtifact: %v", err)
	}
	// A normal entry AFTER the big one — proves an oversized line never drops
	// the tail of the file.
	if _, _, err := store.appendOSArtifact("os-artifact-workflow-small", "a small follow-up artifact", map[string]string{"source": "scout_thread"}); err != nil {
		t.Fatalf("appendOSArtifact small: %v", err)
	}

	reloaded, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("reload with a multi-MB artifact must not fail: %v", err)
	}
	deck, ok := reloaded.entryByID("os-artifact-workflow-bigdeck")
	if !ok || len(deck.Text) != len(bigDeck) {
		t.Fatalf("big deck reloaded ok=%v len=%d, want the full %d-byte body", ok, len(deck.Text), len(bigDeck))
	}
	if _, ok := reloaded.entryByID("os-artifact-workflow-small"); !ok {
		t.Fatal("the entry after the oversized deck was dropped on reload")
	}
}

// buildMemoryAnswer is the keyless/error fallback for answer_memory_question.
// Full-text search can surface a large artifact (e.g. a packaging deck) for a
// loosely related query; inlining that body verbatim once produced a 2.65M-char
// answer that overflowed the Fable orchestrator's context ceiling (400). The
// fallback must stay compact and still name the item.
func TestBuildMemoryAnswerCapsHugeMatchBody(t *testing.T) {
	huge := strings.Repeat("A", 2_600_000) // ~2.6MB, mimics an inlined base64 deck body
	matches := []meetingMemoryMatch{
		{Entry: meetingMemoryEntry{
			ID:   "os-artifact-deck-1",
			Kind: "os_artifact",
			Text: huge,
			// A pathological title must not smuggle the bulk back in either.
			Metadata: map[string]string{"title": "Package Ember Analytics" + strings.Repeat("!", 2_000_000)},
		}},
	}
	answer := buildMemoryAnswer("samsung tv audience viewership", matches)
	if len(answer) > 5000 {
		t.Fatalf("buildMemoryAnswer len=%d, want compact; a full artifact body or title must never be inlined into a recall answer", len(answer))
	}
	if !strings.Contains(answer, "Package Ember Analytics") {
		t.Fatalf("recall answer should still name the item by title: %q", answer)
	}
	if strings.Contains(answer, strings.Repeat("A", 1000)) {
		t.Fatal("recall answer still contains a large raw body run")
	}
}

// --- Track-2 Wave 0: store-layer prompt-body cap (stripOversizeBody) ---
//
// Root-cause regression for the observed 2,505,990 > 1,000,000 token 400: a
// 2.6MB base64-image os_artifact rode visibleEntriesLocked into snapshot-fed
// prompts whole. The cap stubs that class at the store boundary while leaving
// real transcripts, the summary layer, and record-JSON kinds untouched; the
// full body stays in the store for the artifact-open path (entriesOfKind).

func TestStripOversizeBodyStubsBase64Artifact(t *testing.T) {
	body := `<html><img src="data:image/png;base64,` + strings.Repeat("iVBORw0KGgoAAAANSUhEUg", 100_000) + `"></html>`
	entry := meetingMemoryEntry{
		ID:       "artifact-huge-1",
		Kind:     meetingMemoryKindOSArtifact,
		Text:     body,
		Metadata: map[string]string{"title": "Packaging deck", "status": "complete"},
	}

	capped := stripOversizeBody(entry)
	if strings.Contains(capped.Text, "base64,i") || len(capped.Text) > 256 {
		t.Fatalf("base64 body not stubbed: len=%d", len(capped.Text))
	}
	if !strings.Contains(capped.Text, "artifact id=artifact-huge-1") || !strings.Contains(capped.Text, "bytes omitted") {
		t.Fatalf("stub must name the id for drill-down: %q", capped.Text)
	}
	if capped.ID != entry.ID || capped.Kind != entry.Kind {
		t.Fatalf("stub changed identity: id=%q kind=%q", capped.ID, capped.Kind)
	}
	if capped.Metadata["title"] != "Packaging deck" || capped.Metadata["status"] != "complete" {
		t.Fatalf("stub dropped metadata: %v", capped.Metadata)
	}
	if capped.Metadata[promptBodyOmittedMetadataKey] != strconv.Itoa(len(body)) {
		t.Fatalf("omission stamp=%q, want original byte size %d", capped.Metadata[promptBodyOmittedMetadataKey], len(body))
	}
	// the input entry (and its shared metadata map) must never be mutated —
	// visibleEntriesLocked hands stripOversizeBody entries that still share
	// maps with the store.
	if entry.Text != body {
		t.Fatal("stripOversizeBody mutated the input entry text")
	}
	if _, stamped := entry.Metadata[promptBodyOmittedMetadataKey]; stamped {
		t.Fatal("stripOversizeBody mutated the input entry metadata map")
	}
}

func TestStripOversizeBodyExemptsSummariesRecordsAndKeepsTranscripts(t *testing.T) {
	// a real-size transcript (max observed ~7.2KB) passes untouched.
	transcript := strings.Repeat("Alice said the launch date moves to Friday. ", 160)
	if got := stripOversizeBody(meetingMemoryEntry{ID: "t-1", Kind: meetingMemoryKindTranscript, Text: transcript}); got.Text != transcript {
		t.Fatalf("transcript under the cap was modified: len=%d", len(got.Text))
	}

	// the summary layer is exempt BY KIND, not by size: a 20KB brain (or a
	// future Wave-1 digest) must survive whole so recall keeps its rollups.
	long := strings.Repeat("## Summary\nA long, load-bearing write-up line.\n", 500)
	for _, kind := range []string{meetingMemoryKindBrain, "meeting_digest", "day_digest", "company_digest"} {
		if got := stripOversizeBody(meetingMemoryEntry{ID: "s-1", Kind: kind, Text: long}); got.Text != long {
			t.Fatalf("summary kind %q was capped", kind)
		}
	}

	// UI-state record kinds carry decoded-verbatim JSON (thread lists, the
	// blob sweep, channel linkage all read them through snapshot(0)) —
	// stubbing them would corrupt records.
	threadJSON := `{"id":"chat-1","ownerEmail":"a@b.c","messages":[{"text":"` + strings.Repeat("x", 50_000) + `"}]}`
	if got := stripOversizeBody(meetingMemoryEntry{ID: "chat-1", Kind: meetingMemoryKindScoutChat, Text: threadJSON}); got.Text != threadJSON {
		t.Fatal("scout_chat_thread record JSON was capped; snapshot readers would fail to decode it")
	}
}

func TestStripOversizeBodyTruncatesOversizeProseRuneSafe(t *testing.T) {
	prose := strings.Repeat("é ", 8_000) // 24KB of multi-byte text, no base64
	capped := stripOversizeBody(meetingMemoryEntry{ID: "big-1", Kind: meetingMemoryKindTranscript, Text: prose})
	if len(capped.Text) > maxPromptBodyBytes+128 {
		t.Fatalf("truncated body len=%d, want <= cap+marker", len(capped.Text))
	}
	if !strings.HasPrefix(capped.Text, "é") {
		t.Fatalf("truncation must keep a recallable prefix, got %q…", capped.Text[:16])
	}
	if !strings.Contains(capped.Text, "full entry id=big-1") {
		t.Fatalf("truncation marker must name the id: %q", capped.Text[len(capped.Text)-96:])
	}
	if !utf8.ValidString(capped.Text) {
		t.Fatal("truncation split a multi-byte rune")
	}
}

func TestSnapshotLanesNoOversizeBodiesEscape(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	transcriptEntry, _, err := store.appendTranscript("event-1", "item-1", "Alice said the billing review blocks the launch.")
	if err != nil {
		t.Fatalf("appendTranscript: %v", err)
	}
	meetingID := transcriptEntry.Metadata["meetingId"]
	base64Chunk := "U0FNU1VOR1RWQVVESUVOQ0U"                                                               // distinctive marker to prove absence
	body := `<html><img src="data:image/png;base64,` + strings.Repeat(base64Chunk, 120_000) + `"/></html>` // ~2.7MB
	if _, appended, err := store.appendOSArtifact("artifact-huge", body, map[string]string{"title": "Samsung deck", "status": "complete"}); err != nil || !appended {
		t.Fatalf("appendOSArtifact appended=%v err=%v", appended, err)
	}

	assertCapped := func(lane string, entries []meetingMemoryEntry) {
		t.Helper()
		if len(entries) == 0 {
			t.Fatalf("%s returned no entries", lane)
		}
		total := 0
		sawArtifact := false
		for _, entry := range entries {
			total += len(entry.Text)
			if strings.Contains(entry.Text, base64Chunk) {
				t.Fatalf("%s leaked the base64 body via entry %s", lane, entry.ID)
			}
			if entry.ID == "artifact-huge" {
				sawArtifact = true
				if !strings.Contains(entry.Text, "bytes omitted") {
					t.Fatalf("%s artifact body not stubbed: %q", lane, entry.Text[:min(len(entry.Text), 120)])
				}
			}
		}
		if !sawArtifact {
			t.Fatalf("%s dropped the artifact entry instead of stubbing it", lane)
		}
		if total > 64*1024 {
			t.Fatalf("%s summed body bytes=%d, want a sane prompt bound", lane, total)
		}
	}

	assertCapped("store.snapshot", store.snapshot(250))
	assertCapped("store.snapshotForMeeting", store.snapshotForMeeting(meetingID, 0))
	app := &kanbanBoardApp{memory: store}
	assertCapped("memorySnapshotForClients", app.memorySnapshotForClients(20))
	// the archive embed lane (archiveMeeting/autoArchiveIdleMeeting both build
	// from memorySnapshotForMeeting(id, 2000)).
	assertCapped("memorySnapshotForMeeting(archive embed)", app.memorySnapshotForMeeting(meetingID, 2000))

	// the store itself is never rewritten — full body stays durable for the
	// artifact-open path and keyword search.
	full := store.entriesOfKind(meetingMemoryKindOSArtifact, 0)
	if len(full) != 1 || len(full[0].Text) < 2_000_000 {
		t.Fatalf("stored artifact body was not preserved: n=%d", len(full))
	}
}

// The artifact library/render/share/export path must BYPASS the cap or
// artifacts visually break: osArtifactsSnapshot (feeding /artifacts and every
// osArtifactByID consumer) reads full bodies via entriesOfKind while still
// hiding quarantined/expired artifacts like the snapshot lane did.
func TestOSArtifactsSnapshotKeepsFullBodiesForArtifactOpen(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	body := `<html><img src="data:image/png;base64,` + strings.Repeat("QUJDREVGRw", 260_000) + `"/></html>`
	if _, _, err := store.appendOSArtifact("artifact-huge", body, map[string]string{"title": "Deck", "status": "complete"}); err != nil {
		t.Fatalf("appendOSArtifact: %v", err)
	}
	if _, _, err := store.appendOSArtifact("artifact-hidden", "quarantined body", map[string]string{relevanceMetadataKey: relevanceQuarantined}); err != nil {
		t.Fatalf("append quarantined artifact: %v", err)
	}

	app := &kanbanBoardApp{memory: store}
	artifacts := app.osArtifactsSnapshot(0)
	if len(artifacts) != 1 {
		t.Fatalf("osArtifactsSnapshot n=%d, want 1 (quarantined hidden)", len(artifacts))
	}
	if artifacts[0].ID != "artifact-huge" || artifacts[0].Text != body {
		t.Fatalf("artifact open path lost the full body: id=%q len=%d want=%d", artifacts[0].ID, len(artifacts[0].Text), len(body))
	}
	if got, found := app.osArtifactByID("artifact-huge"); !found || got.Text != body {
		t.Fatalf("osArtifactByID lost the full body: found=%v len=%d", found, len(got.Text))
	}
}

// End-to-end regression for the 2,505,990-token 400: with the 2.6MB artifact
// in the store AND matched by the query, the built model input stays bounded
// and carries zero base64.
func TestMemoryQuestionInputBoundedWithOversizeArtifact(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	if _, _, err := store.appendTranscript("event-1", "item-1", "Alice said the Samsung TV audience skews older."); err != nil {
		t.Fatalf("appendTranscript: %v", err)
	}
	base64Chunk := "U0FNU1VOR1RWQVVESUVOQ0U"
	body := `Samsung TV audience deck <img src="data:image/png;base64,` + strings.Repeat(base64Chunk, 120_000) + `"/>`
	if _, _, err := store.appendOSArtifact("artifact-huge", body, map[string]string{"title": "Samsung TV audience deck", "threadStatus": "complete", "status": "complete"}); err != nil {
		t.Fatalf("appendOSArtifact: %v", err)
	}

	query := "what did we learn about the Samsung TV audience?"
	entries := store.contextEntriesForQuery(query, 60, time.Now())
	if len(entries) == 0 {
		t.Fatal("contextEntriesForQuery returned nothing")
	}
	input := buildMemoryQuestionInput(query, entries, time.Now())
	if strings.Contains(input, base64Chunk) {
		t.Fatal("model input still contains the base64 body")
	}
	if len(input) > 200_000 {
		t.Fatalf("model input bytes=%d, want well under the token ceiling", len(input))
	}
}

// --- Digest tiers (Track-2 Wave 1): store primitives.

func TestUpsertDigestSupersedesInPlaceAndHidesStale(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	first, err := store.upsertDigest(meetingMemoryKindMeetingDigest, "m1", `{"meetingId":"m1","topics":[{"t":"pricing zebra draft"}]}`, map[string]string{"meetingId": "m1"})
	if err != nil {
		t.Fatalf("first upsertDigest: %v", err)
	}
	second, err := store.upsertDigest(meetingMemoryKindMeetingDigest, "m1", `{"meetingId":"m1","topics":[{"t":"pricing zebra revised"}]}`, map[string]string{"meetingId": "m1"})
	if err != nil {
		t.Fatalf("second upsertDigest: %v", err)
	}

	stored := store.entriesOfKind(meetingMemoryKindMeetingDigest, 0)
	if len(stored) != 2 {
		t.Fatalf("stored digests = %d, want 2 (append-only supersede)", len(stored))
	}
	var staleStored, currentStored meetingMemoryEntry
	for _, entry := range stored {
		switch entry.ID {
		case first.ID:
			staleStored = entry
		case second.ID:
			currentStored = entry
		}
	}
	if staleStored.ID == "" || currentStored.ID == "" {
		t.Fatalf("expected both digest generations in the store, got %+v", stored)
	}
	if got := memoryEntryRelevance(staleStored); got != relevanceArchived {
		t.Fatalf("superseded digest relevance = %q, want %q", got, relevanceArchived)
	}
	if got := staleStored.Metadata[digestCurrentMetadataKey]; got != digestCurrentFalse {
		t.Fatalf("superseded digest current = %q, want %q", got, digestCurrentFalse)
	}
	if got := staleStored.Metadata[digestSupersededByMetadataKey]; got != second.ID {
		t.Fatalf("supersededBy = %q, want %q", got, second.ID)
	}
	if !memoryEntryHiddenFromRecall(staleStored) {
		t.Fatalf("superseded digest must be hidden from recall")
	}
	if !digestEntryCurrent(currentStored) || memoryEntryHiddenFromRecall(currentStored) {
		t.Fatalf("replacement digest must be current and recall-visible: %+v", currentStored.Metadata)
	}

	for _, entry := range store.snapshot(0) {
		if entry.ID == first.ID {
			t.Fatalf("superseded digest leaked into snapshot")
		}
	}
	sawCurrentInSnapshot := false
	for _, entry := range store.snapshot(0) {
		if entry.ID == second.ID {
			sawCurrentInSnapshot = true
		}
	}
	if !sawCurrentInSnapshot {
		t.Fatalf("current digest missing from snapshot")
	}

	matches := store.search("zebra", 10)
	if len(matches) != 1 || matches[0].Entry.ID != second.ID {
		ids := make([]string, 0, len(matches))
		for _, match := range matches {
			ids = append(ids, match.Entry.ID)
		}
		t.Fatalf("search matched %v, want only the current digest %s", ids, second.ID)
	}

	latest := store.latestDigestPerMeeting()
	if got, ok := latest["m1"]; !ok || got.ID != second.ID {
		t.Fatalf("latestDigestPerMeeting[m1] = %+v, want %s", got, second.ID)
	}
}

func TestUpsertDigestNeverLeavesTwoCurrent(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	var newest meetingMemoryEntry
	for round := 0; round < 3; round++ {
		newest, err = store.upsertDigest(meetingMemoryKindMeetingDigest, "m1", `{"meetingId":"m1","round":`+strconv.Itoa(round)+`}`, nil)
		if err != nil {
			t.Fatalf("upsertDigest round %d: %v", round, err)
		}
	}
	other, err := store.upsertDigest(meetingMemoryKindMeetingDigest, "m2", `{"meetingId":"m2"}`, nil)
	if err != nil {
		t.Fatalf("upsertDigest m2: %v", err)
	}

	currentPerKey := map[string][]string{}
	for _, entry := range store.entriesOfKind(meetingMemoryKindMeetingDigest, 0) {
		if digestEntryCurrent(entry) {
			key := digestEntryKey(entry)
			currentPerKey[key] = append(currentPerKey[key], entry.ID)
		}
	}
	if len(currentPerKey["m1"]) != 1 || currentPerKey["m1"][0] != newest.ID {
		t.Fatalf("current digests for m1 = %v, want exactly [%s]", currentPerKey["m1"], newest.ID)
	}
	if len(currentPerKey["m2"]) != 1 || currentPerKey["m2"][0] != other.ID {
		t.Fatalf("current digests for m2 = %v, want exactly [%s] (cross-key supersede)", currentPerKey["m2"], other.ID)
	}
}

func TestUpsertDigestStampsKeyAndSurvivesReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	// nil metadata: digestKey/current must still be stamped, or every range
	// and per-meeting read helper silently matches nothing.
	first, err := store.upsertDigest(meetingMemoryKindDayDigest, "2026-06-29", `{"day":"2026-06-29","v":1}`, nil)
	if err != nil {
		t.Fatalf("first upsertDigest: %v", err)
	}
	if got := first.Metadata[digestKeyMetadataKey]; got != "2026-06-29" {
		t.Fatalf("digestKey stamp = %q, want 2026-06-29", got)
	}
	if got := first.Metadata[digestCurrentMetadataKey]; got != digestCurrentTrue {
		t.Fatalf("current stamp = %q, want %q", got, digestCurrentTrue)
	}
	second, err := store.upsertDigest(meetingMemoryKindDayDigest, "2026-06-29", `{"day":"2026-06-29","v":2}`, map[string]string{digestDayMetadataKey: "2026-06-29"})
	if err != nil {
		t.Fatalf("second upsertDigest: %v", err)
	}

	reloaded, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	stored := reloaded.entriesOfKind(meetingMemoryKindDayDigest, 0)
	if len(stored) != 2 {
		t.Fatalf("reloaded digests = %d, want 2", len(stored))
	}
	for _, entry := range stored {
		switch entry.ID {
		case first.ID:
			if digestEntryCurrent(entry) || memoryEntryRelevance(entry) != relevanceArchived {
				t.Fatalf("superseded state did not survive reload: %+v", entry.Metadata)
			}
		case second.ID:
			if !digestEntryCurrent(entry) {
				t.Fatalf("current state did not survive reload: %+v", entry.Metadata)
			}
		default:
			t.Fatalf("unexpected digest id %s", entry.ID)
		}
	}
}

func TestDayBucketFilesLateNightToLocalDay(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")

	// 2026-06-30 06:30 UTC is 23:30 PDT on 2026-06-29: the late-night entry
	// files to the local day the team lived it in, not the UTC date.
	lateNight := time.Date(2026, 6, 30, 6, 30, 0, 0, time.UTC)
	if got := dayBucket(lateNight); got != "2026-06-29" {
		t.Fatalf("dayBucket(late night UTC) = %q, want 2026-06-29", got)
	}
	if got := dayBucket(time.Date(2026, 6, 30, 19, 0, 0, 0, time.UTC)); got != "2026-06-30" {
		t.Fatalf("dayBucket(midday) = %q, want 2026-06-30", got)
	}
}

func TestDigestsInRangeBucketsByDay(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	wantDays := []string{"2026-06-29", "2026-06-30", "2026-07-01"}
	for _, day := range wantDays {
		if _, err := store.upsertDigest(meetingMemoryKindDayDigest, day, `{"day":"`+day+`"}`, map[string]string{digestDayMetadataKey: day}); err != nil {
			t.Fatalf("upsert day digest %s: %v", day, err)
		}
	}
	if _, err := store.upsertDigest(meetingMemoryKindDayDigest, "2026-07-04", `{"day":"2026-07-04"}`, map[string]string{digestDayMetadataKey: "2026-07-04"}); err != nil {
		t.Fatalf("upsert out-of-range day digest: %v", err)
	}
	// supersede one in-range day: only the replacement may match.
	replacement, err := store.upsertDigest(meetingMemoryKindDayDigest, "2026-06-30", `{"day":"2026-06-30","v":2}`, map[string]string{digestDayMetadataKey: "2026-06-30"})
	if err != nil {
		t.Fatalf("supersede day digest: %v", err)
	}
	// meeting digest spanning late Sunday into Monday 01:00 overlaps the range
	// by span even though its day stamp (2026-06-28) is out of range.
	overlapping, err := store.upsertDigest(meetingMemoryKindMeetingDigest, "m-sunday", `{"meetingId":"m-sunday"}`, map[string]string{
		"meetingId":                "m-sunday",
		digestDayMetadataKey:       "2026-06-28",
		digestSpanStartMetadataKey: time.Date(2026, 6, 28, 22, 0, 0, 0, location).Format(time.RFC3339),
		digestSpanEndMetadataKey:   time.Date(2026, 6, 29, 1, 0, 0, 0, location).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("upsert overlapping meeting digest: %v", err)
	}
	if _, err := store.upsertDigest(meetingMemoryKindMeetingDigest, "m-early", `{"meetingId":"m-early"}`, map[string]string{
		"meetingId":                "m-early",
		digestSpanStartMetadataKey: time.Date(2026, 6, 27, 9, 0, 0, 0, location).Format(time.RFC3339),
		digestSpanEndMetadataKey:   time.Date(2026, 6, 27, 11, 0, 0, 0, location).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("upsert out-of-range meeting digest: %v", err)
	}

	rangeStart := time.Date(2026, 6, 29, 0, 0, 0, 0, location)
	rangeEnd := time.Date(2026, 7, 1, 23, 59, 59, 0, location)
	got := store.digestsInRange(rangeStart, rangeEnd)

	gotIDs := map[string]bool{}
	for _, entry := range got {
		gotIDs[entry.ID] = true
	}
	if len(got) != 4 {
		keys := make([]string, 0, len(got))
		for _, entry := range got {
			keys = append(keys, entry.Kind+":"+digestEntryKey(entry))
		}
		t.Fatalf("digestsInRange returned %d entries (%v), want 4 (3 day digests + 1 overlapping meeting digest)", len(got), keys)
	}
	if !gotIDs[replacement.ID] {
		t.Fatalf("replacement day digest missing from range")
	}
	if !gotIDs[overlapping.ID] {
		t.Fatalf("span-overlapping meeting digest missing from range")
	}
	for _, entry := range got {
		if !digestEntryCurrent(entry) {
			t.Fatalf("superseded digest leaked into range: %s", entry.ID)
		}
		if key := digestEntryKey(entry); key == "2026-07-04" || key == "m-early" {
			t.Fatalf("out-of-range digest %s leaked into range", key)
		}
	}
	// oldest-first by covered window: the Sunday-spanning meeting digest
	// (starts 06-28 22:00) leads the Monday day digest.
	if got[0].ID != overlapping.ID {
		t.Fatalf("range not ordered oldest-first: got %s first", digestEntryKey(got[0]))
	}

	// the late-night rule end to end: a digest keyed by dayBucket of a
	// 23:30-PDT instant files to (and is found under) that local day.
	lateNightDay := dayBucket(time.Date(2026, 6, 30, 6, 30, 0, 0, time.UTC))
	if lateNightDay != "2026-06-29" {
		t.Fatalf("late-night bucket = %q, want 2026-06-29", lateNightDay)
	}
	mondayOnly := store.digestsInRange(time.Date(2026, 6, 29, 0, 0, 0, 0, location), time.Date(2026, 6, 29, 23, 59, 59, 0, location))
	foundLateNightDay := false
	for _, entry := range mondayOnly {
		if entry.Kind == meetingMemoryKindDayDigest && digestEntryKey(entry) == lateNightDay {
			foundLateNightDay = true
		}
	}
	if !foundLateNightDay {
		t.Fatalf("late-night local day %s not selected by its own day range", lateNightDay)
	}
}

func TestLatestCompanyDigestLatestOnly(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	if _, ok := store.latestCompanyDigest(); ok {
		t.Fatalf("empty store must have no company digest")
	}
	if _, err := store.upsertDigest(meetingMemoryKindCompanyDigest, companyDigestKey, `{"themes":["v1"]}`, nil); err != nil {
		t.Fatalf("first company upsert: %v", err)
	}
	second, err := store.upsertDigest(meetingMemoryKindCompanyDigest, companyDigestKey, `{"themes":["v2"]}`, nil)
	if err != nil {
		t.Fatalf("second company upsert: %v", err)
	}

	got, ok := store.latestCompanyDigest()
	if !ok || got.ID != second.ID {
		t.Fatalf("latestCompanyDigest = (%+v, %v), want the newest fold %s", got, ok, second.ID)
	}
	currentCount := 0
	for _, entry := range store.entriesOfKind(meetingMemoryKindCompanyDigest, 0) {
		if digestEntryCurrent(entry) {
			currentCount++
		}
	}
	if currentCount != 1 {
		t.Fatalf("current company digests = %d, want exactly 1 (latest-only fold)", currentCount)
	}
}

func TestTranscriptWindowAroundReturnsNeighborsSameMeeting(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	appendTranscript := func(id string, meetingID string, extra map[string]string) {
		metadata := map[string]string{"meetingId": meetingID}
		for key, value := range extra {
			metadata[key] = value
		}
		if _, _, err := store.appendEntry(meetingMemoryKindTranscript, id, "line for "+id, metadata); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}

	// interleave two meetings, a non-transcript entry, and a quarantined line.
	appendTranscript("t1", "m1", nil)
	appendTranscript("u1", "m2", nil)
	appendTranscript("t2", "m1", nil)
	appendTranscript("t3", "m1", nil)
	if _, _, err := store.appendEntry(meetingMemoryKindBrain, "b1", "brain write-up", map[string]string{"meetingId": "m1"}); err != nil {
		t.Fatalf("append brain: %v", err)
	}
	appendTranscript("t4", "m1", nil)
	appendTranscript("tq", "m1", map[string]string{relevanceMetadataKey: relevanceQuarantined})
	appendTranscript("u2", "m2", nil)
	appendTranscript("t5", "m1", nil)
	appendTranscript("t6", "m1", nil)

	// pin strictly-increasing CreatedAt in append order so the CreatedAt
	// ordering and the non-transcript insertion point are deterministic even
	// if two appends shared a clock tick.
	store.mu.Lock()
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	for index := range store.entries {
		store.entries[index].CreatedAt = base.Add(time.Duration(index) * time.Second)
	}
	store.mu.Unlock()

	window := store.transcriptWindowAround("t3", 2)
	gotIDs := make([]string, 0, len(window))
	for _, entry := range window {
		gotIDs = append(gotIDs, entry.ID)
		if got := strings.TrimSpace(entry.Metadata["meetingId"]); got != "m1" {
			t.Fatalf("window crossed meetingId: %s in %s", entry.ID, got)
		}
	}
	// radius 2 around t3 within m1 transcripts [t1 t2 t3 t4 t5 t6] (brain
	// excluded by kind, tq hidden, u* other meeting): [t1..t5].
	want := []string{"t1", "t2", "t3", "t4", "t5"}
	if strings.Join(gotIDs, ",") != strings.Join(want, ",") {
		t.Fatalf("window = %v, want %v", gotIDs, want)
	}

	// clamped at the head of the meeting.
	head := store.transcriptWindowAround("t1", 2)
	headIDs := make([]string, 0, len(head))
	for _, entry := range head {
		headIDs = append(headIDs, entry.ID)
	}
	if strings.Join(headIDs, ",") != "t1,t2,t3" {
		t.Fatalf("head window = %v, want [t1 t2 t3]", headIDs)
	}

	// an anchor in the other meeting only ever sees its own meeting.
	other := store.transcriptWindowAround("u1", 5)
	otherIDs := make([]string, 0, len(other))
	for _, entry := range other {
		otherIDs = append(otherIDs, entry.ID)
	}
	if strings.Join(otherIDs, ",") != "u1,u2" {
		t.Fatalf("other-meeting window = %v, want [u1 u2]", otherIDs)
	}

	// a non-transcript anchor (brain, between t3 and t4) centers on its
	// insertion point in time (t4), so radius 1 spans its surrounding lines.
	brainWindow := store.transcriptWindowAround("b1", 1)
	brainIDs := make([]string, 0, len(brainWindow))
	for _, entry := range brainWindow {
		brainIDs = append(brainIDs, entry.ID)
	}
	if strings.Join(brainIDs, ",") != "t3,t4,t5" {
		t.Fatalf("non-transcript anchor window = %v, want [t3 t4 t5]", brainIDs)
	}

	if got := store.transcriptWindowAround("missing", 2); got != nil {
		t.Fatalf("unknown anchor must return nil, got %v", got)
	}
}

func TestBootResumeSkipsDigestEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	if _, _, err := store.appendEntry(meetingMemoryKindTranscript, "t1", "we are live", nil); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	liveMeetingID := store.currentMeetingID(officeRoomID)
	if liveMeetingID == "" {
		t.Fatalf("expected a minted meeting id")
	}

	// an ambient producer digests a PAST meeting mid-flight: its meetingId
	// stamp names the digested meeting, and a day digest carries none at all.
	if _, err := store.upsertDigest(meetingMemoryKindMeetingDigest, "old-meeting", `{"meetingId":"old-meeting"}`, map[string]string{"meetingId": "old-meeting"}); err != nil {
		t.Fatalf("upsert past-meeting digest: %v", err)
	}
	if _, err := store.upsertDigest(meetingMemoryKindDayDigest, "2026-06-29", `{"day":"2026-06-29"}`, nil); err != nil {
		t.Fatalf("upsert day digest: %v", err)
	}

	reloaded, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.currentMeetingID(officeRoomID); got != liveMeetingID {
		t.Fatalf("boot resumed meeting id %q, want the in-flight meeting %q (digest lines must not clear or redirect it)", got, liveMeetingID)
	}

	// after an archive, a trailing digest must not resurrect the meeting.
	if _, _, err := store.appendArchive("a1", "meeting archived", nil); err != nil {
		t.Fatalf("append archive: %v", err)
	}
	if _, err := store.upsertDigest(meetingMemoryKindDayDigest, "2026-06-30", `{"day":"2026-06-30"}`, nil); err != nil {
		t.Fatalf("upsert trailing day digest: %v", err)
	}
	closed, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("reload after archive: %v", err)
	}
	if got := closed.currentMeetingID(officeRoomID); got != "" {
		t.Fatalf("boot resumed %q after archive, want closed meeting (empty id)", got)
	}
}

func TestUpsertDigestConcurrentSingleWinner(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	// the plan's top Wave-1 risk: two workers rebuilding the same digest key
	// concurrently must never leave two current digests (mark-stale + append
	// share one critical section).
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for round := 0; round < 5; round++ {
				if _, err := store.upsertDigest(meetingMemoryKindDayDigest, "2026-06-29", `{"day":"2026-06-29","worker":`+strconv.Itoa(worker)+`,"round":`+strconv.Itoa(round)+`}`, nil); err != nil {
					t.Errorf("worker %d round %d: %v", worker, round, err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()

	stored := store.entriesOfKind(meetingMemoryKindDayDigest, 0)
	if len(stored) != 40 {
		t.Fatalf("stored digests = %d, want 40 (append-only history)", len(stored))
	}
	currentIDs := []string{}
	for _, entry := range stored {
		if digestEntryCurrent(entry) {
			currentIDs = append(currentIDs, entry.ID)
		}
	}
	if len(currentIDs) != 1 {
		t.Fatalf("current digests = %v, want exactly one winner", currentIDs)
	}
	// the single winner must also be what recall sees.
	visible := 0
	for _, entry := range store.snapshot(0) {
		if entry.Kind == meetingMemoryKindDayDigest {
			visible++
		}
	}
	if visible != 1 {
		t.Fatalf("recall-visible day digests = %d, want 1", visible)
	}
}
