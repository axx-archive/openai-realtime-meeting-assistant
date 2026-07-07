package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	store.rotateMeetingID()
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
