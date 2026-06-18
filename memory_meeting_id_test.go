package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryEntriesStampMeetingIDAndRotate(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	first, _, err := store.appendTranscript("event-1", "item-1", "Boot Barn kickoff planning notes.")
	if err != nil {
		t.Fatalf("append first transcript: %v", err)
	}
	second, _, err := store.appendTranscript("event-2", "item-2", "Boot Barn follow-up commitments.")
	if err != nil {
		t.Fatalf("append second transcript: %v", err)
	}

	meetingID := first.Metadata["meetingId"]
	if meetingID == "" {
		t.Fatal("first entry missing meetingId")
	}
	if second.Metadata["meetingId"] != meetingID {
		t.Fatalf("second meetingId=%q, want %q", second.Metadata["meetingId"], meetingID)
	}
	if store.currentMeetingID() != meetingID {
		t.Fatalf("currentMeetingID=%q, want %q", store.currentMeetingID(), meetingID)
	}

	store.rotateMeetingID()
	third, _, err := store.appendTranscript("event-3", "item-3", "Next meeting Boot Barn recap.")
	if err != nil {
		t.Fatalf("append third transcript: %v", err)
	}
	if third.Metadata["meetingId"] == "" || third.Metadata["meetingId"] == meetingID {
		t.Fatalf("post-rotation meetingId=%q, want a new id different from %q", third.Metadata["meetingId"], meetingID)
	}
}

func TestMemoryStoreResumesOpenMeetingAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	entry, _, err := store.appendTranscript("event-1", "item-1", "Boot Barn kickoff planning notes.")
	if err != nil {
		t.Fatalf("append transcript: %v", err)
	}

	reopened, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	if got := reopened.currentMeetingID(); got != entry.Metadata["meetingId"] {
		t.Fatalf("resumed meetingId=%q, want %q", got, entry.Metadata["meetingId"])
	}

	if _, _, err := reopened.appendArchive("meeting-archive-1", "archived the meeting", nil); err != nil {
		t.Fatalf("append archive: %v", err)
	}
	closed, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("reopen archived store: %v", err)
	}
	if got := closed.currentMeetingID(); got != "" {
		t.Fatalf("meetingId after archive=%q, want empty until the next entry", got)
	}
}

func TestMemoryEntriesWithoutMeetingIDStayReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	legacyLine := `{"id":"legacy-1","kind":"transcript","text":"Tom: Legacy Boot Barn note.","createdAt":"2026-01-05T10:00:00Z"}` + "\n"
	if err := os.WriteFile(path, []byte(legacyLine), 0o600); err != nil {
		t.Fatalf("write legacy memory file: %v", err)
	}

	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	entries := store.snapshot(10)
	if len(entries) != 1 || entries[0].ID != "legacy-1" {
		t.Fatalf("entries=%v, want the legacy entry", entries)
	}
	if entries[0].Metadata["meetingId"] != "" {
		t.Fatalf("legacy meetingId=%q, want empty", entries[0].Metadata["meetingId"])
	}
}

func TestArchiveMeetingRotatesMeetingID(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	first, _, err := app.memory.appendTranscript("event-1", "item-1", "Boot Barn kickoff planning notes.")
	if err != nil {
		t.Fatalf("append transcript: %v", err)
	}

	result, err := app.archiveMeeting("AJ")
	if err != nil {
		t.Fatalf("archiveMeeting: %v", err)
	}

	var archiveEntry meetingMemoryEntry
	for _, entry := range app.memorySnapshot(50) {
		if entry.Kind == meetingMemoryKindArchive && entry.ID == result.ID {
			archiveEntry = entry
		}
	}
	if archiveEntry.ID == "" {
		t.Fatal("archive entry not found in memory")
	}
	if archiveEntry.Metadata["meetingId"] != first.Metadata["meetingId"] {
		t.Fatalf("archive meetingId=%q, want the archived meeting %q", archiveEntry.Metadata["meetingId"], first.Metadata["meetingId"])
	}

	next, _, err := app.memory.appendTranscript("event-2", "item-2", "Next meeting Boot Barn recap.")
	if err != nil {
		t.Fatalf("append post-archive transcript: %v", err)
	}
	if next.Metadata["meetingId"] == first.Metadata["meetingId"] {
		t.Fatalf("post-archive meetingId=%q, want a new meeting id", next.Metadata["meetingId"])
	}
}

func TestArchiveMeetingIncludesOnlyCurrentMeetingMemory(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	old, _, err := app.memory.appendTranscript("event-old", "item-old", "AJ: We decided the old archive should not leak.")
	if err != nil {
		t.Fatalf("append old transcript: %v", err)
	}
	app.memory.rotateMeetingID()
	current, _, err := app.memory.appendTranscript("event-current", "item-current", "AJ: We decided the current archive should be scoped.")
	if err != nil {
		t.Fatalf("append current transcript: %v", err)
	}
	if current.Metadata["meetingId"] == "" || current.Metadata["meetingId"] == old.Metadata["meetingId"] {
		t.Fatalf("meeting ids old=%q current=%q, want distinct non-empty ids", old.Metadata["meetingId"], current.Metadata["meetingId"])
	}

	result, err := app.archiveMeeting("AJ")
	if err != nil {
		t.Fatalf("archiveMeeting: %v", err)
	}
	if result.MeetingID != current.Metadata["meetingId"] {
		t.Fatalf("result meetingId=%q, want %q", result.MeetingID, current.Metadata["meetingId"])
	}

	archivePath, err := meetingArchivePath(result.ID)
	if err != nil {
		t.Fatalf("meetingArchivePath: %v", err)
	}
	rawArchive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	var archive meetingArchive
	if err := json.Unmarshal(rawArchive, &archive); err != nil {
		t.Fatalf("decode archive: %v", err)
	}
	if archive.MeetingID != current.Metadata["meetingId"] {
		t.Fatalf("archive meetingId=%q, want %q", archive.MeetingID, current.Metadata["meetingId"])
	}
	if len(archive.Memory) != 1 {
		t.Fatalf("archive memory entries=%d, want 1: %#v", len(archive.Memory), archive.Memory)
	}
	if archive.Memory[0].ID != current.ID {
		t.Fatalf("archive memory id=%q, want current %q", archive.Memory[0].ID, current.ID)
	}
	if strings.Contains(archive.Notes.Text, old.Text) {
		t.Fatalf("notes leaked old meeting decision: %s", archive.Notes.Text)
	}
	if !strings.Contains(archive.Notes.Text, current.Text) {
		t.Fatalf("notes missing current meeting decision: %s", archive.Notes.Text)
	}
}

func TestArchiveMeetingCreatesClientMeetingArtifact(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	first, _, err := app.memory.appendTranscript("event-1", "item-1", "AJ: We decided to turn the meeting notes into an artifact.")
	if err != nil {
		t.Fatalf("append transcript: %v", err)
	}

	result, err := app.archiveMeeting("AJ")
	if err != nil {
		t.Fatalf("archiveMeeting: %v", err)
	}
	if result.Artifact == nil {
		t.Fatal("archive result missing meeting artifact")
	}
	if result.Artifact.Kind != meetingMemoryKindOSArtifact {
		t.Fatalf("artifact kind=%q, want %q", result.Artifact.Kind, meetingMemoryKindOSArtifact)
	}
	if result.Artifact.Metadata["mode"] != "meeting" || result.Artifact.Metadata["archiveId"] != result.ID {
		t.Fatalf("artifact metadata=%v, want meeting mode and archive id", result.Artifact.Metadata)
	}
	if !strings.Contains(result.Artifact.Metadata["downloadUrl"], "?key=") {
		t.Fatalf("client artifact downloadUrl=%q, want keyed URL", result.Artifact.Metadata["downloadUrl"])
	}
	if !strings.Contains(result.Artifact.Text, "Meeting artifact") || !strings.Contains(result.Artifact.Text, "Decisions") {
		t.Fatalf("artifact text=%q, want structured meeting artifact", result.Artifact.Text)
	}
	if result.Artifact.Metadata["meetingId"] != first.Metadata["meetingId"] {
		t.Fatalf("artifact meetingId=%q, want archived meeting %q", result.Artifact.Metadata["meetingId"], first.Metadata["meetingId"])
	}

	var persistedArtifact meetingMemoryEntry
	for _, entry := range app.memory.snapshot(50) {
		if entry.ID == result.Artifact.ID {
			persistedArtifact = entry
			break
		}
	}
	if persistedArtifact.ID == "" {
		t.Fatal("persisted meeting artifact not found")
	}
	if strings.Contains(persistedArtifact.Metadata["downloadUrl"], "?key=") {
		t.Fatalf("persisted downloadUrl=%q, should not include archive key", persistedArtifact.Metadata["downloadUrl"])
	}

	foundClientArtifact := false
	for _, entry := range app.osArtifactsSnapshot(10) {
		if entry.ID != result.Artifact.ID {
			continue
		}
		foundClientArtifact = true
		if !strings.Contains(entry.Metadata["downloadUrl"], "?key=") {
			t.Fatalf("client snapshot downloadUrl=%q, want keyed URL", entry.Metadata["downloadUrl"])
		}
	}
	if !foundClientArtifact {
		t.Fatalf("client artifacts missing meeting artifact %q", result.Artifact.ID)
	}
}
