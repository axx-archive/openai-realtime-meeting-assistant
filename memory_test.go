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
