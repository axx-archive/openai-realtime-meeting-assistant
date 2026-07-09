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
	if store.currentMeetingID(officeRoomID) != meetingID {
		t.Fatalf("currentMeetingID=%q, want %q", store.currentMeetingID(officeRoomID), meetingID)
	}

	store.rotateMeetingID(officeRoomID)
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
	if got := reopened.currentMeetingID(officeRoomID); got != entry.Metadata["meetingId"] {
		t.Fatalf("resumed meetingId=%q, want %q", got, entry.Metadata["meetingId"])
	}

	if _, _, err := reopened.appendArchive("meeting-archive-1", "archived the meeting", nil); err != nil {
		t.Fatalf("append archive: %v", err)
	}
	closed, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("reopen archived store: %v", err)
	}
	if got := closed.currentMeetingID(officeRoomID); got != "" {
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
	app.memory.rotateMeetingID(officeRoomID)
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

/* ---------- multi-room W2: per-room meeting ids ---------- */

// Each room mints and rotates its meeting id independently, and every new
// entry is stamped with metadata.roomId alongside meetingId (§3.4).
func TestMemoryMeetingIDsPerRoomIndependent(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	office, _, err := store.appendTranscript("event-office", "item-1", "Boot Barn kickoff planning notes.")
	if err != nil {
		t.Fatalf("append office transcript: %v", err)
	}
	if office.Metadata["roomId"] != officeRoomID {
		t.Fatalf("office entry roomId=%q, want %q stamped on every new entry", office.Metadata["roomId"], officeRoomID)
	}
	roomB, _, err := store.appendAttributedTranscriptEntry("room-b", "event-b", "item-b", "", "", "Suit Barn side meeting notes for the record.", nil, true, "")
	if err != nil {
		t.Fatalf("append room-b transcript: %v", err)
	}
	if roomB.Metadata["roomId"] != "room-b" {
		t.Fatalf("room-b entry roomId=%q, want room-b", roomB.Metadata["roomId"])
	}
	if roomB.Metadata["meetingId"] == "" || roomB.Metadata["meetingId"] == office.Metadata["meetingId"] {
		t.Fatalf("room-b meetingId=%q, want a fresh id distinct from the office's %q", roomB.Metadata["meetingId"], office.Metadata["meetingId"])
	}
	if got := store.currentMeetingID("room-b"); got != roomB.Metadata["meetingId"] {
		t.Fatalf("currentMeetingID(room-b)=%q, want %q", got, roomB.Metadata["meetingId"])
	}

	// rotating the office never disturbs room B — and vice versa.
	store.rotateMeetingID(officeRoomID)
	if got := store.currentMeetingID(officeRoomID); got != "" {
		t.Fatalf("office id=%q after rotation, want empty", got)
	}
	if got := store.currentMeetingID("room-b"); got != roomB.Metadata["meetingId"] {
		t.Fatalf("room-b id=%q after the OFFICE rotation, want %q intact", got, roomB.Metadata["meetingId"])
	}

	// the conditional rotation is room-scoped: room B keyed with the office's
	// old id is a no-op; keyed with its own id it lands.
	if store.rotateMeetingIDIfCurrent("room-b", office.Metadata["meetingId"]) {
		t.Fatal("rotateMeetingIDIfCurrent(room-b, office id) landed, want a no-op")
	}
	if !store.rotateMeetingIDIfCurrent("room-b", roomB.Metadata["meetingId"]) {
		t.Fatal("rotateMeetingIDIfCurrent(room-b, own id) did not land")
	}
	next, _, err := store.appendAttributedTranscriptEntry("room-b", "event-b2", "item-b2", "", "", "Fresh Suit Barn sitting notes for the record.", nil, true, "")
	if err != nil {
		t.Fatalf("append post-rotation room-b transcript: %v", err)
	}
	if next.Metadata["meetingId"] == "" || next.Metadata["meetingId"] == roomB.Metadata["meetingId"] {
		t.Fatalf("post-rotation room-b meetingId=%q, want a new id different from %q", next.Metadata["meetingId"], roomB.Metadata["meetingId"])
	}
}

// The expectedMeetingID append gate validates against the ROOM's active id —
// one room's live id can never satisfy another room's gate.
func TestMemoryExpectedMeetingIDGateIsRoomScoped(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	officeID := store.ensureMeetingID(officeRoomID)
	roomBID := store.ensureMeetingID("room-b")

	if _, appended, err := store.appendEntryForMeeting("room-b", meetingMemoryKindTranscript, "gated-own", "Tim: gated side room note.", nil, roomBID); err != nil || !appended {
		t.Fatalf("append gated on room-b's own id: appended=%v err=%v, want it to land", appended, err)
	}
	if _, appended, err := store.appendEntryForMeeting("room-b", meetingMemoryKindTranscript, "gated-cross", "Tim: cross-room gated note.", nil, officeID); err != nil || appended {
		t.Fatalf("append gated on the OFFICE id in room-b: appended=%v err=%v, want a silent skip", appended, err)
	}
}

// Boot resume runs per room: legacy no-roomId entries resume as the OFFICE's
// sitting even when a named room's entry is the newer JSONL tail, a named
// room resumes its own id, and a room whose newest entry is an archive
// resumes nothing.
func TestMemoryBootResumePerRoom(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	lines := []string{
		`{"id":"legacy-1","kind":"transcript","text":"Tom: Legacy Boot Barn note.","createdAt":"2026-01-05T10:00:00Z","metadata":{"meetingId":"meeting-legacy-1"}}`,
		`{"id":"b-1","kind":"transcript","text":"Tim: Side room note.","createdAt":"2026-01-05T10:05:00Z","metadata":{"meetingId":"meeting-b-1","roomId":"room-b"}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write memory file: %v", err)
	}

	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	if got := store.currentMeetingID(officeRoomID); got != "meeting-legacy-1" {
		t.Fatalf("office resumed id=%q, want the legacy no-roomId entry's meeting-legacy-1", got)
	}
	if got := store.currentMeetingID("room-b"); got != "meeting-b-1" {
		t.Fatalf("room-b resumed id=%q, want meeting-b-1", got)
	}

	// room B archives; its next boot resumes nothing while the office's
	// in-flight sitting still resumes.
	if _, appended, err := store.appendEntryForMeeting("room-b", meetingMemoryKindArchive, "archive-b-1", "archived the side room meeting", map[string]string{"meetingId": "meeting-b-1"}, ""); err != nil || !appended {
		t.Fatalf("append room-b archive: appended=%v err=%v", appended, err)
	}
	reopened, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	if got := reopened.currentMeetingID("room-b"); got != "" {
		t.Fatalf("room-b id after archive=%q, want empty until the next entry", got)
	}
	if got := reopened.currentMeetingID(officeRoomID); got != "meeting-legacy-1" {
		t.Fatalf("office resumed id=%q after room-b archived, want meeting-legacy-1 intact", got)
	}
}

// Room chat rides the same room-dimensioned append: a message recorded for a
// named room stamps that room's id, never the office's.
func TestRoomChatMessageCarriesRoomID(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	officeID := app.memory.ensureMeetingID(officeRoomID)

	if _, ok := app.recordRoomChatMessageForMeeting("room-b", "Tom", "ship the side room release notes tonight", nil, ""); !ok {
		t.Fatal("recordRoomChatMessageForMeeting ok=false, want true")
	}
	entries := app.memory.entriesOfKind(meetingMemoryKindTranscript, 0)
	if len(entries) != 1 {
		t.Fatalf("transcript entries=%d, want exactly the chat message", len(entries))
	}
	chat := entries[0]
	if chat.Metadata["source"] != transcriptSourceRoomChat {
		t.Fatalf("chat source=%q, want %q", chat.Metadata["source"], transcriptSourceRoomChat)
	}
	if chat.Metadata["roomId"] != "room-b" {
		t.Fatalf("chat roomId=%q, want room-b", chat.Metadata["roomId"])
	}
	if chat.Metadata["meetingId"] == "" || chat.Metadata["meetingId"] == officeID {
		t.Fatalf("chat meetingId=%q, want room-b's own id (office=%q)", chat.Metadata["meetingId"], officeID)
	}
}

// Brain-intake material is ungated (empty expectedMeetingID), so it is pinned
// to the OFFICE room explicitly — intake can never land in a named room's
// live meeting (§4.3).
func TestBrainIntakeContributionsPinnedToOfficeRoom(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	// a named room holds the newest sitting; the pin must ignore it.
	app.memory.ensureMeetingID("room-b")

	user := &userAccount{Email: "aj@thebonfire.xyz", Name: "AJ"}
	message := scoutChatMessageRecord{ID: "msg-1", Text: "Shareability was founded in 2014; the pivot to branded viral video came in 2016."}
	app.appendBrainIntakeContribution(user, "company_history", message)

	var filed meetingMemoryEntry
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindTranscript, 0) {
		if entry.ID == "brain-intake-msg-1" {
			filed = entry
		}
	}
	if filed.ID == "" {
		t.Fatal("brain intake answer was not filed")
	}
	if filed.Metadata["source"] != "brain_intake" {
		t.Fatalf("source=%q, want brain_intake", filed.Metadata["source"])
	}
	if filed.Metadata["roomId"] != officeRoomID {
		t.Fatalf("brain intake roomId=%q, want pinned to %q", filed.Metadata["roomId"], officeRoomID)
	}
	if filed.Metadata["meetingId"] != app.memory.currentMeetingID(officeRoomID) {
		t.Fatalf("brain intake meetingId=%q, want the office's %q", filed.Metadata["meetingId"], app.memory.currentMeetingID(officeRoomID))
	}
}
