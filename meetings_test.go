package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fireIdleEndNow invokes the office idle-end seam exactly as a live grace
// timer would: with the generation the timer captured at arm time.
func fireIdleEndNow(app *kanbanBoardApp) {
	fireIdleEndNowInRoom(app, officeRoomID)
}

// fireIdleEndNowInRoom is fireIdleEndNow with the multi-room W2 dimension:
// the fired generation is the ROOM's counter, exactly as a per-room grace
// timer would capture it.
func fireIdleEndNowInRoom(app *kanbanBoardApp, roomID string) {
	app.meetings.mu.Lock()
	generation := app.meetings.idleGenerations[roomID]
	app.meetings.mu.Unlock()
	app.endMeetingForIdle(roomID, generation)
}

// meetingArchiveFilesOnDisk lists archive JSON files under the isolated data
// dir (the MEETING_MEMORY_PATH sibling "archives" directory).
func meetingArchiveFilesOnDisk(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(meetingMemoryPath()), "archives"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read archives dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// The session-end rule (card 078, founder decision 2026-07-08): a sitting ends
// after the room has been empty for five minutes. The exact default is the
// reconnection contract; the env override stays.
func TestMeetingIdleEndGraceDefaultsToAFewMinutes(t *testing.T) {
	t.Setenv("MEETING_IDLE_END_GRACE", "")
	if got := meetingIdleEndGrace(); got != 5*time.Minute {
		t.Fatalf("meetingIdleEndGrace()=%v, want 5m reconnection boundary", got)
	}
	t.Setenv("MEETING_IDLE_END_GRACE", "45m")
	if got := meetingIdleEndGrace(); got != 45*time.Minute {
		t.Fatalf("meetingIdleEndGrace()=%v with override, want 45m", got)
	}
}

func TestMeetingAdmissionOpensRecordAlignedWithMemoryID(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	app.noteMeetingAdmission(officeRoomID, "AJ")

	record, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("admission did not open a meeting record")
	}
	if record.ID == "" || record.ID != app.memory.ensureMeetingID(officeRoomID) {
		t.Fatalf("record id=%q, want the memory store's meeting id %q", record.ID, app.memory.ensureMeetingID(officeRoomID))
	}
	if record.StartedAt == "" || record.EndedAt != "" {
		t.Fatalf("record=%#v, want an open record with a start stamp", record)
	}
	if len(record.Participants) != 1 || record.Participants[0] != "AJ" {
		t.Fatalf("participants=%v, want [AJ]", record.Participants)
	}

	// entries appended during the meeting stamp the SAME id the record adopted.
	entry, _, err := app.memory.appendTranscript("event-1", "item-1", "Boot Barn kickoff planning notes.")
	if err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	if entry.Metadata["meetingId"] != record.ID {
		t.Fatalf("entry meetingId=%q, want record id %q", entry.Metadata["meetingId"], record.ID)
	}

	// a second admission unions participants without opening a new record.
	app.noteMeetingAdmission(officeRoomID, "Tim")
	second, ok := app.meetings.activeRecord(officeRoomID)
	if !ok || second.ID != record.ID {
		t.Fatalf("second admission record=%#v, want the same open record %q", second, record.ID)
	}
	if len(second.Participants) != 2 || second.Participants[0] != "AJ" || second.Participants[1] != "Tim" {
		t.Fatalf("participants=%v, want roster-ordered [AJ Tim]", second.Participants)
	}
	if got := len(app.meetings.recent(0)); got != 1 {
		t.Fatalf("records=%d, want exactly one record", got)
	}
}

func TestIdleEndClosesRecordAndRotatesMemoryID(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("admitParticipant: %v", err)
	}
	app.noteMeetingAdmission(officeRoomID, "AJ")
	open, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("no open record after admission")
	}
	before, _, err := app.memory.appendTranscript("event-1", "item-1", "Boot Barn kickoff planning notes.")
	if err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	if before.Metadata["meetingId"] != open.ID {
		t.Fatalf("pre-idle meetingId=%q, want %q", before.Metadata["meetingId"], open.ID)
	}

	app.forgetParticipant("AJ")
	// fire the grace callback directly instead of sleeping through the timer.
	fireIdleEndNow(app)

	closed, active := app.meetings.activeRecord(officeRoomID)
	if active {
		t.Fatalf("record=%#v, want no active record after idle end", closed)
	}
	records := app.meetings.recent(1)
	if len(records) != 1 || records[0].ID != open.ID {
		t.Fatalf("records=%#v, want the closed record %q", records, open.ID)
	}
	if records[0].EndedAt == "" || records[0].EndedReason != meetingEndedReasonIdle {
		t.Fatalf("record=%#v, want endedReason %q", records[0], meetingEndedReasonIdle)
	}

	// the alignment invariant: idle end rotates the memory meeting id, so the
	// next entry starts a fresh meeting.
	after, _, err := app.memory.appendTranscript("event-2", "item-2", "Next meeting Boot Barn recap.")
	if err != nil {
		t.Fatalf("append post-idle transcript: %v", err)
	}
	if after.Metadata["meetingId"] == "" || after.Metadata["meetingId"] == open.ID {
		t.Fatalf("post-idle meetingId=%q, want a new id different from %q", after.Metadata["meetingId"], open.ID)
	}

	// re-firing after the record closed is a no-op.
	fireIdleEndNow(app)
	if got := len(app.meetings.recent(0)); got != 1 {
		t.Fatalf("records=%d after duplicate idle fire, want 1", got)
	}
}

// Card 078: the idle end silently auto-archives a meeting that captured
// content — real archive file, email skipped, ArchiveID stamped on the closed
// record, and the memory entries pinned to the ENDED meeting id.
func TestIdleEndAutoArchivesMeetingWithContent(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("admitParticipant: %v", err)
	}
	app.noteMeetingAdmission(officeRoomID, "AJ")
	open, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("no open record after admission")
	}
	if _, _, err := app.memory.appendTranscript("event-1", "item-1", "We decided to launch Boot Barn next week."); err != nil {
		t.Fatalf("append transcript: %v", err)
	}

	app.forgetParticipant("AJ")
	fireIdleEndNow(app)

	records := app.meetings.recent(1)
	if len(records) != 1 || records[0].ID != open.ID {
		t.Fatalf("records=%#v, want the closed record %q", records, open.ID)
	}
	closed := records[0]
	if closed.EndedAt == "" || closed.EndedReason != meetingEndedReasonIdle {
		t.Fatalf("record=%#v, want an idle-ended record", closed)
	}
	if closed.ArchiveID == "" {
		t.Fatal("idle end did not stamp an ArchiveID (auto-archive missing)")
	}

	// the archive file is durable and silent: email skipped, the embedded
	// record is the idle-closed meeting.
	archivePath, err := meetingArchivePath(closed.ArchiveID)
	if err != nil {
		t.Fatalf("meetingArchivePath: %v", err)
	}
	raw, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read idle auto-archive: %v", err)
	}
	var archive meetingArchive
	if err := json.Unmarshal(raw, &archive); err != nil {
		t.Fatalf("decode idle auto-archive: %v", err)
	}
	if archive.MeetingID != open.ID {
		t.Fatalf("archive meetingId=%q, want %q", archive.MeetingID, open.ID)
	}
	if !archive.Email.Skipped || archive.Email.Sent || archive.Email.Attempted {
		t.Fatalf("archive email=%#v, want silently skipped (idle auto-archive never emails)", archive.Email)
	}
	if archive.Meeting == nil || archive.Meeting.ID != open.ID || archive.Meeting.EndedReason != meetingEndedReasonIdle || archive.Meeting.ArchiveID != closed.ArchiveID {
		t.Fatalf("embedded record=%#v, want the idle-closed meeting with the archive id", archive.Meeting)
	}
	if len(archive.Memory) == 0 {
		t.Fatal("archive memory snapshot is empty, want the meeting transcript")
	}

	// the archive + artifact memory entries pin the ENDED meeting id — never
	// the successor the rotation would lazily mint.
	var archiveEntry, artifactEntry *meetingMemoryEntry
	for _, entry := range app.memory.snapshot(0) {
		entry := entry
		switch entry.Kind {
		case meetingMemoryKindArchive:
			archiveEntry = &entry
		case meetingMemoryKindOSArtifact:
			artifactEntry = &entry
		}
	}
	if archiveEntry == nil {
		t.Fatal("no archive memory entry appended")
	}
	if archiveEntry.Metadata["meetingId"] != open.ID {
		t.Fatalf("archive entry meetingId=%q, want the ended meeting %q", archiveEntry.Metadata["meetingId"], open.ID)
	}
	if !strings.Contains(archiveEntry.Text, "Archived meeting "+closed.ArchiveID+" with") {
		t.Fatalf("archive summary %q does not match the Memory tool's archive-row format", archiveEntry.Text)
	}
	if artifactEntry == nil {
		t.Fatal("no os_artifact memory entry appended")
	}
	if artifactEntry.Metadata["meetingId"] != open.ID || artifactEntry.Metadata["archiveId"] != closed.ArchiveID {
		t.Fatalf("artifact entry metadata=%#v, want meetingId %q and archiveId %q", artifactEntry.Metadata, open.ID, closed.ArchiveID)
	}

	// the next join starts a fresh context, untouched by the archive appends.
	app.noteMeetingAdmission(officeRoomID, "Tim")
	fresh, ok := app.meetings.activeRecord(officeRoomID)
	if !ok || fresh.ID == open.ID {
		t.Fatalf("fresh record=%#v, want a new meeting id after the idle reset", fresh)
	}
}

// Card 078: a contentless session leaves no artifact — no archive file, no
// ArchiveID, no archive/os_artifact memory entries.
func TestIdleEndSkipsArchiveForEmptyMeeting(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("admitParticipant: %v", err)
	}
	app.noteMeetingAdmission(officeRoomID, "AJ")
	open, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("no open record after admission")
	}

	app.forgetParticipant("AJ")
	fireIdleEndNow(app)

	records := app.meetings.recent(1)
	if len(records) != 1 || records[0].ID != open.ID || records[0].EndedReason != meetingEndedReasonIdle {
		t.Fatalf("records=%#v, want the idle-closed record %q", records, open.ID)
	}
	if records[0].ArchiveID != "" {
		t.Fatalf("ArchiveID=%q, want empty — a contentless session leaves no artifact", records[0].ArchiveID)
	}
	if paths := meetingArchiveFilesOnDisk(t); len(paths) != 0 {
		t.Fatalf("archive files=%v, want none for an empty meeting", paths)
	}
	for _, entry := range app.memory.snapshot(0) {
		if entry.Kind == meetingMemoryKindArchive || entry.Kind == meetingMemoryKindOSArtifact {
			t.Fatalf("empty meeting appended a %s entry: %#v", entry.Kind, entry)
		}
	}
}

// BLOCKER regression: an idle-ended meeting's id must never be resumed after
// a restart. Pre-fix, newMeetingMemoryStore resumed the ended id (the last
// JSONL entry is not an archive), boot reconciliation returned early because
// no record was open, and the next admission opened a SECOND record with the
// ended meeting's id — merging two meetings' transcripts under one id.
func TestRestartAfterIdleEndNeverDuplicatesMeetingID(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("admitParticipant: %v", err)
	}
	app.noteMeetingAdmission(officeRoomID, "AJ")
	endedID := app.memory.currentMeetingID(officeRoomID)
	if _, _, err := app.memory.appendTranscript("event-1", "item-1", "Boot Barn kickoff planning notes."); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	app.forgetParticipant("AJ")
	fireIdleEndNow(app) // closes the record; the rotation is in-process only

	// routine deploy: the process restarts and the memory store resumes from
	// the JSONL tail, which is the ENDED meeting's transcript.
	reopened := newKanbanBoardApp()
	if got := reopened.memory.currentMeetingID(officeRoomID); got == endedID {
		t.Fatalf("boot resumed the ended meeting id %q; reconciliation must rotate it", got)
	}

	// next day's join: a FRESH id and record, never a duplicate.
	reopened.noteMeetingAdmission(officeRoomID, "AJ")
	record, ok := reopened.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("admission after restart did not open a record")
	}
	if record.ID == endedID {
		t.Fatalf("admission re-minted the ended meeting id %q onto a second record", endedID)
	}
	counts := map[string]int{}
	for _, stored := range reopened.meetings.recent(0) {
		counts[stored.ID]++
	}
	for id, count := range counts {
		if count > 1 {
			t.Fatalf("meetings store holds %d records with id %q, want unique ids", count, id)
		}
	}
	// transcripts of the new meeting stamp the new id, so a later archive can
	// never merge the ended meeting's transcripts into the new one.
	entry, _, err := reopened.memory.appendTranscript("event-2", "item-2", "Fresh morning Boot Barn recap.")
	if err != nil {
		t.Fatalf("append post-restart transcript: %v", err)
	}
	if entry.Metadata["meetingId"] == endedID || entry.Metadata["meetingId"] != record.ID {
		t.Fatalf("post-restart transcript meetingId=%q, want the new record id %q", entry.Metadata["meetingId"], record.ID)
	}
}

// A join landing between the fired idle timer's occupancy check and its close
// bumps the generation (cancelIdleEnd), so the stale fire can neither end the
// meeting nor rotate the id underneath the admission.
func TestIdleFireInvalidatedByAdmissionGeneration(t *testing.T) {
	t.Setenv("MEETING_IDLE_END_GRACE", "1h")
	app := newIsolatedKanbanBoardApp(t)
	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("admitParticipant: %v", err)
	}
	app.noteMeetingAdmission(officeRoomID, "AJ")
	open, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("no open record after admission")
	}
	// give the meeting content so a stray auto-archive would be detectable.
	if _, _, err := app.memory.appendTranscript("event-1", "item-1", "Boot Barn kickoff planning notes."); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	app.forgetParticipant("AJ")
	app.noteMeetingOccupancy(officeRoomID) // arms the timer
	app.meetings.mu.Lock()
	armTimeGeneration := app.meetings.idleGenerations[officeRoomID]
	app.meetings.mu.Unlock()

	// the timer fires and passes its occupancy check; the admission lands in
	// that window — cancelIdleEnd bumps the generation even though the timer
	// can no longer be stopped.
	app.noteMeetingAdmission(officeRoomID, "AJ")

	// the fired timer's close is a no-op against the stale generation.
	app.endMeetingForIdle(officeRoomID, armTimeGeneration)

	record, ok := app.meetings.activeRecord(officeRoomID)
	if !ok || record.ID != open.ID || record.EndedAt != "" {
		t.Fatalf("record=%#v, want meeting %q still open", record, open.ID)
	}
	if got := app.memory.currentMeetingID(officeRoomID); got != open.ID {
		t.Fatalf("memory id=%q, want %q un-rotated", got, open.ID)
	}
	// the invalidated fire short-circuits before the auto-archive: no stray
	// archive file lands and the open record carries no ArchiveID.
	if record.ArchiveID != "" {
		t.Fatalf("ArchiveID=%q after the invalidated fire, want empty", record.ArchiveID)
	}
	if paths := meetingArchiveFilesOnDisk(t); len(paths) != 0 {
		t.Fatalf("archive files=%v after the invalidated fire, want none", paths)
	}

	// a genuinely empty room still idle-ends with the live generation.
	app.noteMeetingOccupancy(officeRoomID)
	fireIdleEndNow(app)
	if record, stillOpen := app.meetings.activeRecord(officeRoomID); stillOpen {
		t.Fatalf("record=%#v, want the fresh-generation idle end to close it", record)
	}
	// ... and the genuine close auto-archives the captured content.
	closed := app.meetings.recent(1)
	if len(closed) != 1 || closed[0].ArchiveID == "" {
		t.Fatalf("records=%#v, want the genuine idle end to stamp an ArchiveID", closed)
	}
	if paths := meetingArchiveFilesOnDisk(t); len(paths) != 1 {
		t.Fatalf("archive files=%v after the genuine fire, want exactly one", paths)
	}
}

// The other half of the idle race: the fire's endMeeting landed but its
// rotation has not — the admission must mint a FRESH id (never reopen the
// ended one), and the closer's conditional rotation must not clobber it.
func TestAdmissionNeverReMintsEndedMeetingID(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.noteMeetingAdmission(officeRoomID, "AJ")
	endedID := app.memory.currentMeetingID(officeRoomID)
	if _, changed := app.meetings.endMeeting(endedID, time.Now().UTC(), meetingEndedReasonIdle, ""); !changed {
		t.Fatal("endMeeting did not close the record")
	}

	app.noteMeetingAdmission(officeRoomID, "Tim")
	record, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("admission did not open a record")
	}
	if record.ID == endedID {
		t.Fatalf("admission re-minted the ended id %q", endedID)
	}
	if got := app.memory.currentMeetingID(officeRoomID); got != record.ID {
		t.Fatalf("memory id=%q, want aligned with the new record %q", got, record.ID)
	}

	// the racing closer's rotation arrives last: conditional, so the fresh id
	// survives.
	app.memory.rotateMeetingIDIfCurrent(officeRoomID, endedID)
	if got := app.memory.currentMeetingID(officeRoomID); got != record.ID {
		t.Fatalf("memory id=%q after the stale rotation, want %q intact", got, record.ID)
	}
}

func TestRejoinWithinGraceCancelsIdleEnd(t *testing.T) {
	t.Setenv("MEETING_IDLE_END_GRACE", "1h")
	app := newIsolatedKanbanBoardApp(t)
	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("admitParticipant: %v", err)
	}
	app.noteMeetingAdmission(officeRoomID, "AJ")
	open, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("no open record after admission")
	}

	app.forgetParticipant("AJ")
	app.noteMeetingOccupancy(officeRoomID)
	app.meetings.mu.Lock()
	armed := app.meetings.idleTimers[officeRoomID] != nil
	app.meetings.mu.Unlock()
	if !armed {
		t.Fatal("last leave did not arm the idle-end timer")
	}

	// a rejoin inside the grace window cancels the pending idle end.
	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("re-admit: %v", err)
	}
	app.noteMeetingAdmission(officeRoomID, "AJ")
	app.meetings.mu.Lock()
	stillArmed := app.meetings.idleTimers[officeRoomID] != nil
	app.meetings.mu.Unlock()
	if stillArmed {
		t.Fatal("rejoin did not cancel the idle-end timer")
	}
	record, ok := app.meetings.activeRecord(officeRoomID)
	if !ok || record.ID != open.ID || record.EndedAt != "" {
		t.Fatalf("record=%#v, want the same meeting still open", record)
	}

	// occupancy check: a non-empty room never arms the timer.
	app.noteMeetingOccupancy(officeRoomID)
	app.meetings.mu.Lock()
	armedWhileOccupied := app.meetings.idleTimers[officeRoomID] != nil
	app.meetings.mu.Unlock()
	if armedWhileOccupied {
		t.Fatal("noteMeetingOccupancy armed the timer while the room is occupied")
	}
}

func TestIdleDeadlinePersistsAndRejoinClearsItDurably(t *testing.T) {
	t.Setenv("MEETING_IDLE_END_GRACE", "1h")
	app := newIsolatedKanbanBoardApp(t)
	app.noteMeetingAdmission(officeRoomID, "AJ")
	open, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("no open record after admission")
	}
	app.noteMeetingOccupancy(officeRoomID)
	empty, ok := app.meetings.activeRecord(officeRoomID)
	if !ok || strings.TrimSpace(empty.IdleDeadlineAt) == "" {
		t.Fatalf("empty sitting did not persist its grace deadline: %+v", empty)
	}
	deadline := empty.IdleDeadlineAt

	app.meetings.stopIdleEndsAndWait()
	reloaded, err := loadMeetingStore(meetingsPath())
	if err != nil {
		t.Fatalf("reload meetings: %v", err)
	}
	persisted, ok := reloaded.activeRecord(officeRoomID)
	if !ok || persisted.ID != open.ID || persisted.IdleDeadlineAt != deadline {
		t.Fatalf("restart changed sitting/deadline: got %+v want id=%s deadline=%s", persisted, open.ID, deadline)
	}

	// A reconnect inside that exact grace preserves the sitting but clears the
	// durable empty boundary in the same record write.
	app.meetings = reloaded
	app.noteMeetingAdmission(officeRoomID, "AJ")
	rejoined, ok := app.meetings.activeRecord(officeRoomID)
	if !ok || rejoined.ID != open.ID || rejoined.IdleDeadlineAt != "" {
		t.Fatalf("rejoin did not durably preserve/clear the sitting: %+v", rejoined)
	}
	reloadedAgain, err := loadMeetingStore(meetingsPath())
	if err != nil {
		t.Fatalf("reload cleared deadline: %v", err)
	}
	confirmed, _ := reloadedAgain.activeRecord(officeRoomID)
	if confirmed.IdleDeadlineAt != "" {
		t.Fatalf("cleared idle deadline was not durable: %+v", confirmed)
	}
}

func TestIdleCloseReconcilesAmbiguousCommittedReplacementAndRunsCloseEffects(t *testing.T) {
	t.Setenv("MEETING_IDLE_END_GRACE", "1h")
	app := newIsolatedKanbanBoardApp(t)
	app.noteMeetingAdmission(officeRoomID, "AJ")
	open, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("no open record after admission")
	}
	app.noteMeetingOccupancy(officeRoomID)
	armed, _ := app.meetings.activeRecord(officeRoomID)
	if strings.TrimSpace(armed.IdleDeadlineAt) == "" {
		t.Fatal("empty sitting lacks durable idle deadline")
	}

	persistCalls := 0
	app.meetings.persistState = func(state meetingStoreState) error {
		persistCalls++
		if err := writeJSONFileAtomically(app.meetings.path, "ambiguous idle close fixture", state); err != nil {
			return err
		}
		if persistCalls == 1 {
			return fmt.Errorf("%w: injected parent directory fsync uncertainty", ErrDurableReplaceAmbiguous)
		}
		return nil
	}

	fireIdleEndNow(app)
	closed, found := app.meetings.recordByID(open.ID)
	if !found || closed.EndedAt == "" || closed.EndedReason != meetingEndedReasonIdle || closed.Finalization == nil {
		t.Fatalf("ambiguous committed close was not reconciled: %+v found=%v", closed, found)
	}
	if _, active := app.meetings.activeRecord(officeRoomID); active {
		t.Fatal("ambiguous committed close remained active")
	}
	if current := app.memory.currentMeetingID(officeRoomID); current != "" {
		t.Fatalf("idempotent close effects did not rotate memory id: %q", current)
	}
	app.meetings.mu.Lock()
	calls := persistCalls
	app.meetings.mu.Unlock()
	if calls < 1 {
		t.Fatal("test did not exercise the injected ambiguous replacement")
	}
}

func TestIdleCloseDefinitePersistenceFailureRetainsDeadlineAndRetriesInProcess(t *testing.T) {
	t.Setenv("MEETING_IDLE_END_GRACE", "1h")
	app := newIsolatedKanbanBoardApp(t)
	app.noteMeetingAdmission(officeRoomID, "AJ")
	open, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("no open record after admission")
	}
	app.noteMeetingOccupancy(officeRoomID)
	armed, _ := app.meetings.activeRecord(officeRoomID)
	deadline := armed.IdleDeadlineAt
	if strings.TrimSpace(deadline) == "" {
		t.Fatal("empty sitting lacks durable idle deadline")
	}

	persistCalls := 0
	app.meetings.persistState = func(state meetingStoreState) error {
		persistCalls++
		if persistCalls == 1 {
			return errors.New("injected definite meetings write failure")
		}
		return writeJSONFileAtomically(app.meetings.path, "idle close retry fixture", state)
	}

	fireIdleEndNow(app)
	stillOpen, active := app.meetings.activeRecord(officeRoomID)
	if !active || stillOpen.ID != open.ID || stillOpen.EndedAt != "" || stillOpen.IdleDeadlineAt != deadline {
		t.Fatalf("definite failure did not preserve the past-due close authority: %+v active=%v", stillOpen, active)
	}
	app.meetings.mu.Lock()
	attemptsAfterFailure := persistCalls
	retryArmed := app.meetings.idleTimers[officeRoomID] != nil
	app.meetings.mu.Unlock()
	if attemptsAfterFailure != 1 || !retryArmed {
		t.Fatalf("definite failure calls=%d retryArmed=%v, want one failed write and a bounded retry", attemptsAfterFailure, retryArmed)
	}

	waitDeadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(waitDeadline) {
		closed, found := app.meetings.recordByID(open.ID)
		if found && closed.EndedAt != "" {
			if closed.EndedReason != meetingEndedReasonIdle {
				t.Fatalf("retry ended as %q, want idle", closed.EndedReason)
			}
			app.meetings.mu.Lock()
			calls := persistCalls
			app.meetings.mu.Unlock()
			if calls < 2 {
				t.Fatalf("close repaired without retrying persistence: calls=%d", calls)
			}
			if current := app.memory.currentMeetingID(officeRoomID); current != "" {
				t.Fatalf("retry closed record but stranded memory id %q", current)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("definite idle close failure stranded the past-due sitting instead of retrying")
}

func TestBootUsesExpiredDurableIdleDeadlineInsteadOfGrantingFreshGrace(t *testing.T) {
	t.Setenv("MEETING_IDLE_END_GRACE", "1h")
	app := newIsolatedKanbanBoardApp(t)
	app.noteMeetingAdmission(officeRoomID, "AJ")
	open, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("no open record after admission")
	}
	if _, _, err := app.memory.appendTranscript("restart-deadline-source", "", "Durable source keeps the sitting identity resumable."); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	app.meetings.mu.Lock()
	index := app.meetings.openRecordIndexLocked(officeRoomID)
	app.meetings.records[index].IdleDeadlineAt = time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	if err := app.meetings.persistLocked(); err != nil {
		app.meetings.mu.Unlock()
		t.Fatalf("persist expired deadline: %v", err)
	}
	app.meetings.mu.Unlock()

	restarted := newKanbanBoardApp()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		closed, found := restarted.meetings.recordByID(open.ID)
		if found && closed.EndedAt != "" {
			if closed.EndedReason != meetingEndedReasonIdle {
				t.Fatalf("expired durable deadline closed as %q, want idle", closed.EndedReason)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("boot granted a fresh one-hour grace instead of honoring the expired durable deadline")
}

func TestMeetingIdleTimerShutdownJoinsInFlightCallback(t *testing.T) {
	t.Setenv("MEETING_IDLE_END_GRACE", "1ms")
	store := &meetingStore{
		idleTimers:      map[string]*time.Timer{},
		idleGenerations: map[string]uint64{},
	}
	started := make(chan struct{})
	release := make(chan struct{})
	store.armIdleEnd(officeRoomID, func(uint64) {
		close(started)
		<-release
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("idle callback did not start")
	}

	stopped := make(chan struct{})
	go func() {
		store.stopIdleEndsAndWait()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("shutdown returned before the in-flight callback completed")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join the completed callback")
	}

	firedAgain := make(chan struct{}, 1)
	store.armIdleEnd(officeRoomID, func(uint64) { firedAgain <- struct{}{} })
	select {
	case <-firedAgain:
		t.Fatal("a permanently stopped meeting store re-armed an idle timer")
	case <-time.After(25 * time.Millisecond):
	}
}

func TestKanbanBoardAppCloseJoinsIdleMeetingCallbacksBeforeReturn(t *testing.T) {
	t.Setenv("MEETING_IDLE_END_GRACE", "1ms")
	app := newIsolatedKanbanBoardApp(t)
	started := make(chan struct{})
	release := make(chan struct{})
	app.meetings.armIdleEnd(officeRoomID, func(uint64) {
		close(started)
		<-release
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("idle callback did not start")
	}

	closed := make(chan error, 1)
	go func() { closed <- app.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned before the idle callback completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not join the completed idle callback")
	}
}

// A zombie/backgrounded socket that never runs its onclose defer keeps
// activeParticipantCount() above zero, so the empty-room idle end never arms and
// the meeting id never rotates (the live ~22h/two-sitting accretion). The
// liveness sweep reaps the stale session, occupancy reaches zero, the sitting
// finalizes, and the NEXT admission mints a fresh id on a fresh record.
func TestLivenessSweepReapsZombieThenFinalizesAndMintsFreshID(t *testing.T) {
	// long grace so the armed timer never fires on its own — we finalize by hand.
	t.Setenv("MEETING_IDLE_END_GRACE", "1h")
	app := newIsolatedKanbanBoardApp(t)

	admitted, err := app.admitParticipant("AJ")
	if err != nil {
		t.Fatalf("admitParticipant: %v", err)
	}
	app.noteMeetingAdmission(officeRoomID, "AJ")
	first, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("no open record after admission")
	}
	if app.activeParticipantCount(officeRoomID) != 1 {
		t.Fatalf("activeParticipantCount()=%d after admission, want 1", app.activeParticipantCount(officeRoomID))
	}

	// A fresh liveness stamp is NOT reaped: the participant is really here.
	app.sweepStaleParticipantSessions()
	if app.activeParticipantCount(officeRoomID) != 1 {
		t.Fatalf("sweep reaped a live participant (count=%d)", app.activeParticipantCount(officeRoomID))
	}
	if record, _ := app.meetings.activeRecord(officeRoomID); record.EndedAt != "" {
		t.Fatal("sweep finalized the meeting with a live participant present")
	}

	// The socket goes zombie: no clean close ever ran, so the presence stayed
	// but its liveness stamp is now stale past the timeout.
	app.mu.Lock()
	app.roomLive[officeRoomID].participants[admitted] = time.Now().UTC().Add(-participantLivenessTimeout - time.Minute)
	app.mu.Unlock()

	app.sweepStaleParticipantSessions()
	if app.activeParticipantCount(officeRoomID) != 0 {
		t.Fatalf("liveness sweep did not reap the zombie session (count=%d)", app.activeParticipantCount(officeRoomID))
	}
	app.meetings.mu.Lock()
	armed := app.meetings.idleTimers[officeRoomID] != nil
	app.meetings.mu.Unlock()
	if !armed {
		t.Fatal("sweep drove occupancy to zero but did not arm the idle end")
	}

	// The empty room stays empty past the grace: the sitting finalizes.
	fireIdleEndNow(app)
	if _, active := app.meetings.activeRecord(officeRoomID); active {
		t.Fatal("record still active after the empty-room idle end")
	}
	records := app.meetings.recent(1)
	if len(records) != 1 || records[0].ID != first.ID {
		t.Fatalf("records=%#v, want the closed first sitting %q", records, first.ID)
	}
	closed := records[0]
	if closed.EndedAt == "" || closed.EndedReason != meetingEndedReasonIdle {
		t.Fatalf("record=%#v, want the first sitting closed with reason idle", closed)
	}
	if app.memory.currentMeetingID(officeRoomID) != "" {
		t.Fatalf("memory id=%q after finalize, want it rotated (empty)", app.memory.currentMeetingID(officeRoomID))
	}

	// The next entry is a NEW sitting: a fresh id on a fresh open record.
	app.noteMeetingAdmission(officeRoomID, "AJ")
	second, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("no open record after the next sitting's admission")
	}
	if second.ID == first.ID {
		t.Fatalf("second sitting reused id %q; want a freshly minted id", second.ID)
	}
	if second.EndedAt != "" {
		t.Fatalf("second sitting record=%#v, want it open", second)
	}
	if !strings.HasPrefix(second.ID, "meeting-") {
		t.Fatalf("fresh id=%q, want a meeting-... mint", second.ID)
	}
}

// A brief drop + rejoin inside the grace must NOT finalize the sitting: the
// rejoin cancels the armed idle end AND re-stamps liveness, so a sweep that runs
// right after cannot reap the rejoiner and the same meeting stays open. Also
// guards against a rejoin double-counting the participant.
func TestDropRejoinWithinGraceDoesNotFinalize(t *testing.T) {
	t.Setenv("MEETING_IDLE_END_GRACE", "1h")
	app := newIsolatedKanbanBoardApp(t)

	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("admitParticipant: %v", err)
	}
	app.noteMeetingAdmission(officeRoomID, "AJ")
	open, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("no open record after admission")
	}

	// Ungraceful drop: last device leaves, idle end arms.
	app.forgetParticipant("AJ")
	app.noteMeetingOccupancy(officeRoomID)
	app.meetings.mu.Lock()
	armed := app.meetings.idleTimers[officeRoomID] != nil
	app.meetings.mu.Unlock()
	if !armed {
		t.Fatal("last leave did not arm the idle end")
	}

	// Rejoin inside the grace: cancels the timer and re-stamps liveness.
	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("re-admit: %v", err)
	}
	app.noteMeetingAdmission(officeRoomID, "AJ")
	if app.activeParticipantCount(officeRoomID) != 1 {
		t.Fatalf("rejoin double-counted the participant (count=%d, want 1)", app.activeParticipantCount(officeRoomID))
	}

	// A sweep right after the rejoin must NOT reap the fresh session.
	app.sweepStaleParticipantSessions()
	if app.activeParticipantCount(officeRoomID) != 1 {
		t.Fatalf("sweep reaped the rejoiner (count=%d)", app.activeParticipantCount(officeRoomID))
	}
	record, ok := app.meetings.activeRecord(officeRoomID)
	if !ok || record.ID != open.ID || record.EndedAt != "" {
		t.Fatalf("record=%#v, want the same sitting %q still open", record, open.ID)
	}
	app.meetings.mu.Lock()
	stillArmed := app.meetings.idleTimers[officeRoomID] != nil
	app.meetings.mu.Unlock()
	if stillArmed {
		t.Fatal("rejoin left the idle end armed; a later fire could finalize a live sitting")
	}
}

func TestArchiveMeetingClosesRecordEmbedsItAndOpensSuccessor(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("admitParticipant: %v", err)
	}
	app.noteMeetingAdmission(officeRoomID, "AJ")
	open, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("no open record after admission")
	}
	if _, _, err := app.memory.appendTranscript("event-1", "item-1", "Boot Barn kickoff planning notes."); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	if _, changed := app.meetings.setAutoTitle(open.ID, "Boot Barn launch"); !changed {
		t.Fatal("setAutoTitle did not land on the open record")
	}

	result, err := app.archiveMeeting("AJ")
	if err != nil {
		t.Fatalf("archiveMeeting: %v", err)
	}

	// the record closed with reason archive and the archive id stamped.
	var closed meetingRecord
	for _, record := range app.meetings.recent(0) {
		if record.ID == open.ID {
			closed = record
		}
	}
	if closed.ID == "" {
		t.Fatal("archived meeting record not found")
	}
	if closed.EndedAt == "" || closed.EndedReason != meetingEndedReasonArchive || closed.ArchiveID != result.ID {
		t.Fatalf("closed record=%#v, want reason archive and archiveId %q", closed, result.ID)
	}

	// the archive JSON embeds the closed record self-containedly.
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
	if archive.Meeting == nil || archive.Meeting.ID != open.ID {
		t.Fatalf("archive.Meeting=%#v, want embedded record %q", archive.Meeting, open.ID)
	}
	if archive.Meeting.Title != "Boot Barn launch" || archive.Meeting.EndedReason != meetingEndedReasonArchive {
		t.Fatalf("embedded record=%#v, want the titled closed record", archive.Meeting)
	}

	// the artifact title prefers the record title over the notes subject.
	if result.Artifact == nil {
		t.Fatal("archive result missing meeting artifact")
	}
	if title := result.Artifact.Metadata["title"]; len(title) < len("Boot Barn launch") || title[:len("Boot Barn launch")] != "Boot Barn launch" {
		t.Fatalf("artifact title=%q, want the meeting record title first", title)
	}

	// AJ never left, so a successor record opens immediately on the new id.
	successor, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("mid-occupancy archive left no active record")
	}
	if successor.ID == open.ID {
		t.Fatalf("successor id=%q, want a new meeting id", successor.ID)
	}
	if successor.ID != app.memory.currentMeetingID(officeRoomID) {
		t.Fatalf("successor id=%q, want the rotated memory id %q", successor.ID, app.memory.currentMeetingID(officeRoomID))
	}
	if len(successor.Participants) != 1 || successor.Participants[0] != "AJ" {
		t.Fatalf("successor participants=%v, want [AJ]", successor.Participants)
	}
}

// A transient archive-write failure must leave the meeting OPEN (record and
// memory id), so the archive can be retried cleanly — never an ended record
// whose archiveId 404s while the room keeps talking.
func TestArchiveMeetingWriteFailureLeavesMeetingOpen(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("admitParticipant: %v", err)
	}
	app.noteMeetingAdmission(officeRoomID, "AJ")
	meetingID := app.memory.currentMeetingID(officeRoomID)
	if _, _, err := app.memory.appendTranscript("event-1", "item-1", "Boot Barn kickoff planning notes."); err != nil {
		t.Fatalf("append transcript: %v", err)
	}

	// break the archives directory: a FILE occupies its path, so the atomic
	// write's MkdirAll fails like a transient disk error would.
	archivesDir := filepath.Join(filepath.Dir(meetingMemoryPath()), "archives")
	if err := os.WriteFile(archivesDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("occupy archives path: %v", err)
	}

	if _, err := app.archiveMeeting("AJ"); err == nil {
		t.Fatal("archiveMeeting must surface the write failure")
	}
	record, ok := app.meetings.activeRecord(officeRoomID)
	if !ok || record.ID != meetingID || record.EndedAt != "" {
		t.Fatalf("record=%#v, want meeting %q still open after the failed write", record, meetingID)
	}
	if got := app.memory.currentMeetingID(officeRoomID); got != meetingID {
		t.Fatalf("memory id=%q, want %q un-rotated after the failed write", got, meetingID)
	}
	if app.meetingSnapshot(officeRoomID) == nil {
		t.Fatal("meetingSnapshot (the room clock) must survive a failed archive")
	}

	// the retry succeeds and ends the meeting exactly once, with the record's
	// archiveId pointing at an archive file that actually exists.
	if err := os.Remove(archivesDir); err != nil {
		t.Fatalf("clear archives path: %v", err)
	}
	result, err := app.archiveMeeting("AJ")
	if err != nil {
		t.Fatalf("retried archiveMeeting: %v", err)
	}
	endedCount := 0
	for _, stored := range app.meetings.recent(0) {
		if stored.ID != meetingID {
			continue
		}
		if stored.EndedAt == "" || stored.EndedReason != meetingEndedReasonArchive || stored.ArchiveID != result.ID {
			t.Fatalf("record=%#v, want ended by archive %q", stored, result.ID)
		}
		endedCount++
	}
	if endedCount != 1 {
		t.Fatalf("records with id %q=%d, want exactly one", meetingID, endedCount)
	}
	archivePath, err := meetingArchivePath(result.ID)
	if err != nil {
		t.Fatalf("meetingArchivePath: %v", err)
	}
	if _, statErr := os.Stat(archivePath); statErr != nil {
		t.Fatalf("archive file missing after the successful retry: %v", statErr)
	}
}

func TestArchiveMeetingWithEmptyRoomLeavesNoSuccessor(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("admitParticipant: %v", err)
	}
	app.noteMeetingAdmission(officeRoomID, "AJ")
	if _, _, err := app.memory.appendTranscript("event-1", "item-1", "Boot Barn kickoff planning notes."); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	oldGeneration := app.ensureRoomMedia(officeRoomID)
	app.mu.Lock()
	if got := app.roomLiveLocked(officeRoomID).mediaSittingID; got != app.memory.currentMeetingID(officeRoomID) {
		app.mu.Unlock()
		t.Fatalf("media sitting=%q before archive, want active meeting", got)
	}
	app.mu.Unlock()
	app.forgetParticipant("AJ")

	if _, err := app.archiveMeeting("AJ"); err != nil {
		t.Fatalf("archiveMeeting: %v", err)
	}
	if record, ok := app.meetings.activeRecord(officeRoomID); ok {
		t.Fatalf("record=%#v, want no successor for an empty room", record)
	}
	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	mediaSittingID, mediaActor, generation := state.mediaSittingID, state.mediaActor, state.mediaGen
	app.mu.Unlock()
	if mediaSittingID != "" || mediaActor != nil || generation <= oldGeneration {
		t.Fatalf("empty archive retained predecessor media: sitting=%q actor=%v generation=%d old=%d", mediaSittingID, mediaActor != nil, generation, oldGeneration)
	}
}

func TestSetAutoTitleFromMissionInsight(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.noteMeetingAdmission(officeRoomID, "AJ")
	meetingID := app.memory.currentMeetingID(officeRoomID)

	record, changed := app.meetings.setAutoTitle(meetingID, "Realtime as UI")
	if !changed {
		t.Fatal("setAutoTitle did not change the record")
	}
	if record.Title != "Realtime as UI" || record.TitleSource != meetingTitleSourceAuto {
		t.Fatalf("record=%#v, want auto title", record)
	}

	// identical title is a no-op; unknown id never lands anywhere.
	if _, changed := app.meetings.setAutoTitle(meetingID, "Realtime as UI"); changed {
		t.Fatal("identical title reported a change")
	}
	if _, changed := app.meetings.setAutoTitle("meeting-unknown-id", "stray"); changed {
		t.Fatal("unknown meeting id accepted a title")
	}
	if active, ok := app.meetings.activeRecord(officeRoomID); !ok || active.Title != "Realtime as UI" {
		t.Fatalf("record=%#v, want the auto title intact", active)
	}
}

func TestMeetingStorePersistsPermanentDirectoryAndToleratesBadFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meetings.json")

	store, err := loadMeetingStore(path)
	if err != nil {
		t.Fatalf("loadMeetingStore missing file: %v", err)
	}
	total := meetingDirectoryScanLimit + 5
	startedAt := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	for index := 0; index < total; index++ {
		id := fmt.Sprintf("meeting-%03d", index)
		if _, changed := store.startMeeting(officeRoomID, id, startedAt.Add(time.Duration(index)*time.Minute), []string{"AJ"}); !changed {
			t.Fatalf("startMeeting %s reported no change", id)
		}
		if _, changed := store.endMeeting(id, startedAt.Add(time.Duration(index)*time.Minute+30*time.Second), meetingEndedReasonIdle, ""); !changed {
			t.Fatalf("endMeeting %s reported no change", id)
		}
	}

	reloaded, err := loadMeetingStore(path)
	if err != nil {
		t.Fatalf("reload meetings: %v", err)
	}
	records := reloaded.recent(0)
	if len(records) != total {
		t.Fatalf("reloaded=%d, want all %d permanent records", len(records), total)
	}
	if records[0].ID != fmt.Sprintf("meeting-%03d", total-1) {
		t.Fatalf("newest=%q, want newest-first ordering", records[0].ID)
	}
	if oldest := records[len(records)-1]; oldest.ID != "meeting-000" {
		t.Fatalf("oldest=%q, want meeting-000 preserved past the former cap", oldest.ID)
	}
	if oldest, found := reloaded.recordByID("meeting-000"); !found || oldest.ID != "meeting-000" {
		t.Fatalf("oldest exact detail identity=%+v found=%t, want restart-preserved meeting-000", oldest, found)
	}
	page, cursor, hasMore := reloaded.recentPage(meetingDirectoryScanLimit, "")
	if len(page) != meetingDirectoryScanLimit || !hasMore || cursor != meetingDirectoryCursorForID("meeting-005") || strings.Contains(cursor, "meeting-") {
		t.Fatalf("first page len=%d cursor=%q hasMore=%t, want %d/opaque/true", len(page), cursor, hasMore, meetingDirectoryScanLimit)
	}
	older, nextCursor, olderHasMore := reloaded.recentPage(meetingDirectoryScanLimit, cursor)
	if len(older) != 5 || olderHasMore || nextCursor != meetingDirectoryCursorForID("meeting-000") || strings.Contains(nextCursor, "meeting-") {
		t.Fatalf("older page len=%d cursor=%q hasMore=%t, want 5/opaque/false", len(older), nextCursor, olderHasMore)
	}
	if records[0].EndedReason != meetingEndedReasonIdle || len(records[0].Participants) != 1 {
		t.Fatalf("record=%#v, want ended record with participants intact", records[0])
	}
	beforeDuplicate := len(reloaded.recent(0))
	if existing, changed := reloaded.startMeeting(officeRoomID, " meeting-000 ", startedAt.Add(48*time.Hour), []string{"Tim"}); changed || existing.ID != "meeting-000" {
		t.Fatalf("duplicate ended identity result=%+v changed=%t, want exact rejection", existing, changed)
	}
	if afterDuplicate := len(reloaded.recent(0)); afterDuplicate != beforeDuplicate {
		t.Fatalf("duplicate start changed permanent directory size %d -> %d", beforeDuplicate, afterDuplicate)
	}

	duplicatePath := filepath.Join(dir, "duplicate.json")
	duplicateState := `{"meetings":[{"id":"meeting-duplicate","startedAt":"2026-07-01T09:00:00Z"},{"id":" meeting-duplicate ","startedAt":"2026-07-02T09:00:00Z"}]}`
	if err := os.WriteFile(duplicatePath, []byte(duplicateState), 0o600); err != nil {
		t.Fatalf("write duplicate meetings: %v", err)
	}
	if _, err := loadMeetingStore(duplicatePath); err == nil || !strings.Contains(err.Error(), "duplicate meeting id") {
		t.Fatalf("duplicate meeting load err=%v, want permanent identity rejection", err)
	}

	// malformed file: load fails cleanly and the app runs with a nil store.
	malformedPath := filepath.Join(dir, "malformed.json")
	if err := os.WriteFile(malformedPath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write malformed file: %v", err)
	}
	if _, err := loadMeetingStore(malformedPath); err == nil {
		t.Fatal("malformed meetings.json did not error")
	}

	// records missing id or startedAt are dropped on load.
	sparsePath := filepath.Join(dir, "sparse.json")
	sparse := `{"meetings":[{"id":"","startedAt":"2026-07-01T09:00:00Z"},{"id":"meeting-ok","startedAt":""},{"id":"meeting-good","startedAt":"2026-07-01T09:05:00Z","participants":["AJ"]}]}`
	if err := os.WriteFile(sparsePath, []byte(sparse), 0o600); err != nil {
		t.Fatalf("write sparse file: %v", err)
	}
	sparseStore, err := loadMeetingStore(sparsePath)
	if err != nil {
		t.Fatalf("load sparse meetings: %v", err)
	}
	if got := sparseStore.recent(0); len(got) != 1 || got[0].ID != "meeting-good" {
		t.Fatalf("sparse records=%#v, want only the complete record", got)
	}

	// nil store stays inert everywhere the app touches it.
	var nilStore *meetingStore
	if _, ok := nilStore.activeRecord(officeRoomID); ok {
		t.Fatal("nil store reported an active record")
	}
	if _, changed := nilStore.startMeeting(officeRoomID, "meeting-x", time.Now(), nil); changed {
		t.Fatal("nil store accepted a start")
	}
	if got := nilStore.recent(5); len(got) != 0 {
		t.Fatalf("nil store recent=%v, want empty", got)
	}
	nilStore.armIdleEnd(officeRoomID, func(uint64) {})
	nilStore.cancelIdleEnd(officeRoomID)
}

func TestMeetingRecordPermanentLibraryPagesPastFormerCapAndOpensOldest(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	started := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	total := meetingDirectoryScanLimit + 5
	records := make([]meetingRecord, 0, total)
	for index := 0; index < total; index++ {
		id := fmt.Sprintf("permanent-meeting-%03d", index)
		at := started.Add(time.Duration(index) * time.Hour)
		records = append(records, meetingRecord{ID: id, StartedAt: at.Format(time.RFC3339Nano), EndedAt: at.Add(30 * time.Minute).Format(time.RFC3339Nano), EndedReason: meetingEndedReasonIdle, Participants: []string{"Tim"}})
		scope := map[string]string{"meetingId": id, "visibility": "organization"}
		if index == 100 {
			scope = map[string]string{"meetingId": id, "visibility": "private", "ownerEmail": "aj@shareability.com"}
		}
		if _, _, err := kanbanApp.memory.appendAttributedTranscriptWithMetadata("permanent-segment-"+id, "item-"+id, "Tim", "high", "Permanent source for "+id, scope); err != nil {
			t.Fatalf("append source %s: %v", id, err)
		}
	}
	kanbanApp.meetings.mu.Lock()
	kanbanApp.meetings.records = records
	kanbanApp.meetings.rebuildDirectoryCursorIndexesLocked()
	if err := kanbanApp.meetings.persistLocked(); err != nil {
		kanbanApp.meetings.mu.Unlock()
		t.Fatalf("persist permanent directory: %v", err)
	}
	kanbanApp.meetings.mu.Unlock()

	cookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	request := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantMeetingsHandler(recorder, req)
		return recorder
	}
	cursor := ""
	seen := map[string]struct{}{}
	for page := 0; page < 3; page++ {
		path := "/assistant/meetings?view=index&limit=100"
		if cursor != "" {
			path += "&meetingCursor=" + cursor
		}
		recorder := request(path)
		if recorder.Code != http.StatusOK {
			t.Fatalf("page %d status=%d body=%s", page, recorder.Code, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), "permanent-meeting-100") {
			t.Fatalf("page %d leaked private meeting identity in rows or cursor: %s", page, recorder.Body.String())
		}
		payload := struct {
			Meetings   []meetingRecordIndexItem `json:"meetings"`
			NextCursor string                   `json:"nextCursor"`
			HasMore    bool                     `json:"hasMore"`
		}{}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode page %d: %v", page, err)
		}
		for _, meeting := range payload.Meetings {
			if _, duplicate := seen[meeting.ID]; duplicate {
				t.Fatalf("meeting %s repeated across permanent pages", meeting.ID)
			}
			seen[meeting.ID] = struct{}{}
		}
		cursor = payload.NextCursor
		if page < 2 && !payload.HasMore {
			t.Fatalf("page %d ended early after %d permanent records", page, len(seen))
		}
		if page == 2 && payload.HasMore {
			t.Fatal("final permanent page still reports more")
		}
	}
	if len(seen) != total-1 {
		t.Fatalf("paged records=%d, want all %d authorized permanent records", len(seen), total-1)
	}
	oldest := request("/assistant/meetings/permanent-meeting-000")
	if oldest.Code != http.StatusOK || !strings.Contains(oldest.Body.String(), "permanent-meeting-000") {
		t.Fatalf("oldest detail status=%d body=%s", oldest.Code, oldest.Body.String())
	}
}

func TestMeetingRecordClaimProjectRequiresDurableLinkNotMatchingTag(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	tim := accountStore().findUser("tim@shareability.com")
	if tim == nil {
		t.Fatal("seeded Tim account missing")
	}
	project, err := app.createScoutChatThread(tim.Email, tim.Name, "Ambiguous Project", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create same-title Project: %v", err)
	}
	result, changed, err := app.applyRetiredMeetingBoardToolCallArgs("create_ticket", map[string]any{
		"title": "Tag-only follow-up",
		"tags":  []any{"Ambiguous Project"},
	})
	if err != nil || !changed {
		t.Fatalf("create tag-only Work changed=%t err=%v", changed, err)
	}
	card, ok := result["card"].(kanbanCard)
	if !ok {
		t.Fatalf("tag-only Work result=%#v", result)
	}
	var tagProjection boardCardViewerProjection
	for _, row := range app.boardProjectionForViewer(context.Background(), tim).Cards {
		if row.CardID == card.ID {
			tagProjection = row
			break
		}
	}
	if tagProjection.ProjectResolution != "tag" || tagProjection.ProjectID != project.ID {
		t.Fatalf("board projection=%+v, want the adversarial inferred tag edge", tagProjection)
	}
	detail := &meetingMemoryDetail{
		CardIDs:      []string{card.ID},
		ClaimCardIDs: map[string][]string{"segment-tag-only": {card.ID}},
	}
	references := app.meetingRecordReferencesForViewer(context.Background(), tim, detail)
	if len(references.Work) != 0 {
		t.Fatalf("work references=%+v, retired card without a successor artifact must stay unresolved", references.Work)
	}
	if len(references.Projects) != 0 {
		t.Fatalf("global Projects=%+v, inferred tag edge must not become Meeting truth", references.Projects)
	}
	claim := references.Claims["segment-tag-only"]
	if len(claim.Work) != 0 || len(claim.Projects) != 0 {
		t.Fatalf("claim references=%+v, want no retired-card Work or inferred Project", claim)
	}
}

func TestBootReconciliationClosesStaleOpenRecord(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))

	stale := meetingStoreState{Meetings: []meetingRecord{{
		ID:           "meeting-stale-001",
		StartedAt:    "2026-06-30T09:00:00Z",
		Participants: []string{"AJ"},
	}}}
	rawState, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("marshal stale state: %v", err)
	}
	if err := os.WriteFile(meetingsPath(), rawState, 0o600); err != nil {
		t.Fatalf("write stale meetings.json: %v", err)
	}

	app := newKanbanBoardApp()
	if record, ok := app.meetings.activeRecord(officeRoomID); ok {
		t.Fatalf("record=%#v, want the stale record closed at boot", record)
	}
	records := app.meetings.recent(1)
	if len(records) != 1 || records[0].EndedReason != meetingEndedReasonRestart || records[0].EndedAt == "" {
		t.Fatalf("records=%#v, want the stale record ended with reason restart", records)
	}
}

func TestBootReconciliationResumesMatchingOpenRecord(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.noteMeetingAdmission(officeRoomID, "AJ")
	if _, _, err := app.memory.appendTranscript("event-1", "item-1", "Boot Barn kickoff planning notes."); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	meetingID := app.memory.currentMeetingID(officeRoomID)

	// a restart resumes the same in-flight meeting: the record stays open and
	// the idle timer arms (a join within the grace window cancels it).
	reopened := newKanbanBoardApp()
	record, ok := reopened.meetings.activeRecord(officeRoomID)
	if !ok || record.ID != meetingID {
		t.Fatalf("record=%#v, want the resumed open meeting %q", record, meetingID)
	}
	reopened.meetings.mu.Lock()
	armed := reopened.meetings.idleTimers[officeRoomID] != nil
	reopened.meetings.mu.Unlock()
	if !armed {
		t.Fatal("boot with a resumed open meeting did not arm the idle-end timer")
	}
}

func TestAssistantMeetingsHandlerAuthAndShape(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	// signed-out reads stay rejected.
	recorder := httptest.NewRecorder()
	assistantMeetingsHandler(recorder, httptest.NewRequest(http.MethodGet, "/assistant/meetings", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out status=%d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	earlier := time.Date(2026, 6, 30, 9, 0, 0, 0, time.UTC)
	kanbanApp.meetings.startMeeting(officeRoomID, "meeting-20260630-first", earlier, []string{"AJ"})
	kanbanApp.meetings.endMeeting("meeting-20260630-first", earlier.Add(45*time.Minute), meetingEndedReasonArchive, "meeting-archive-1")
	kanbanApp.meetings.startMeeting(officeRoomID, "meeting-20260701-second", earlier.Add(24*time.Hour), []string{"Tim"})
	// The directory is identity, not read authority. Seed one organization-
	// visible source for each legacy-style record so the signed member's
	// principal-filtered recall store proves both rows are readable.
	if _, _, err := kanbanApp.memory.appendBrainWriteUp("meeting-list-source-first", "First meeting source.", map[string]string{"meetingId": "meeting-20260630-first"}); err != nil {
		t.Fatalf("append first meeting source: %v", err)
	}
	if _, _, err := kanbanApp.memory.appendBrainWriteUp("meeting-list-source-second", "Second meeting source.", map[string]string{"meetingId": "meeting-20260701-second"}); err != nil {
		t.Fatalf("append second meeting source: %v", err)
	}

	fetchMeetings := func(query string) (items []map[string]any, serverNow string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/assistant/meetings"+query, nil)
		for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantMeetingsHandler(recorder, req)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
		}
		var payload struct {
			OK        bool             `json:"ok"`
			Meetings  []map[string]any `json:"meetings"`
			ServerNow string           `json:"serverNow"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode meetings: %v", err)
		}
		if !payload.OK {
			t.Fatalf("payload=%s, want ok", recorder.Body.String())
		}
		return payload.Meetings, payload.ServerNow
	}

	meetings, serverNow := fetchMeetings("")
	if serverNow == "" {
		t.Fatal("payload missing the top-level serverNow skew anchor")
	}
	if len(meetings) != 2 {
		t.Fatalf("meetings=%d, want 2", len(meetings))
	}
	if meetings[0]["id"] != "meeting-20260701-second" || meetings[1]["id"] != "meeting-20260630-first" {
		t.Fatalf("order=%v,%v, want newest first", meetings[0]["id"], meetings[1]["id"])
	}
	if meetings[0]["active"] != true || meetings[1]["active"] != false {
		t.Fatalf("active flags=%v,%v, want true,false", meetings[0]["active"], meetings[1]["active"])
	}
	if _, hasPerItemAnchor := meetings[0]["serverNow"]; hasPerItemAnchor {
		t.Fatal("per-item serverNow should be dropped in favor of the top-level anchor")
	}
	if meetings[1]["archiveId"] != "meeting-archive-1" || meetings[1]["endedReason"] != meetingEndedReasonArchive {
		t.Fatalf("closed meeting=%#v, want archive stamps", meetings[1])
	}
	if duration, ok := meetings[1]["durationSeconds"].(float64); !ok || int64(duration) != int64(45*time.Minute/time.Second) {
		t.Fatalf("durationSeconds=%v, want 2700", meetings[1]["durationSeconds"])
	}

	limited, _ := fetchMeetings("?limit=1")
	if len(limited) != 1 || limited[0]["id"] != "meeting-20260701-second" {
		t.Fatalf("limited=%#v, want only the newest record", limited)
	}
}

// The Memory tool's day-grouped meeting cards (D15) ride the meetings list:
// each item carries the brain-derived summary, the active-decision checklist,
// capped log rows, and board-card links resolved against the live board.
func TestAssistantMeetingsPayloadCarriesMemoryDetail(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	started := time.Now().UTC().Add(-40 * time.Minute)
	meetingID := "meeting-detail-0000001"
	kanbanApp.meetings.startMeeting(officeRoomID, meetingID, started, []string{"AJ", "Tim"})

	cardResult, _, err := kanbanApp.applyRetiredMeetingBoardToolCallArgs("create_ticket", map[string]any{"title": "Add bandwidth estimation probe"})
	if err != nil {
		t.Fatalf("create card: %v", err)
	}
	card, ok := cardResult["card"].(kanbanCard)
	if !ok {
		t.Fatalf("create result=%#v, want a card", cardResult)
	}

	stamp := map[string]string{"meetingId": meetingID}
	if _, _, err := kanbanApp.memory.appendAttributedTranscriptWithMetadata("event-1", "item-1", "Tim", "", "keep the buffer bounded, two seconds max", stamp); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	brainText := "## Overview\nAligned on bounding the retransmission buffer at two seconds.\n\n## Decisions\n- bound the buffer"
	if _, _, err := kanbanApp.memory.appendBrainWriteUp("brain-detail-1", brainText, map[string]string{"meetingId": meetingID}); err != nil {
		t.Fatalf("append brain: %v", err)
	}
	if _, _, err := kanbanApp.memory.appendBoardUpdate("board-detail-1", "## Summary\nDrafted the probe card.", map[string]string{"meetingId": meetingID, "cardIds": card.ID + ",card-gone"}); err != nil {
		t.Fatalf("append board update: %v", err)
	}
	if _, _, err := kanbanApp.memory.appendDecision("decision-detail-1", "bound the retransmission buffer at two seconds", map[string]string{"meetingId": meetingID, "status": decisionStatusActive}); err != nil {
		t.Fatalf("append decision: %v", err)
	}
	if _, _, err := kanbanApp.memory.appendDecision("decision-detail-2", "superseded pick", map[string]string{"meetingId": meetingID, "status": "superseded"}); err != nil {
		t.Fatalf("append superseded decision: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/assistant/meetings", nil)
	for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantMeetingsHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Meetings []struct {
			ID         string              `json:"id"`
			Summary    string              `json:"summary"`
			Decisions  []string            `json:"decisions"`
			Log        []map[string]string `json:"log"`
			Links      []map[string]string `json:"links"`
			EntryCount int                 `json:"entryCount"`
		} `json:"meetings"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode meetings: %v", err)
	}
	if len(payload.Meetings) != 1 || payload.Meetings[0].ID != meetingID {
		t.Fatalf("meetings=%#v, want the one detail meeting", payload.Meetings)
	}
	item := payload.Meetings[0]
	if item.Summary != "Aligned on bounding the retransmission buffer at two seconds." {
		t.Fatalf("summary=%q, want the brain Overview text", item.Summary)
	}
	if len(item.Decisions) != 1 || item.Decisions[0] != "bound the retransmission buffer at two seconds" {
		t.Fatalf("decisions=%#v, want only the active decision", item.Decisions)
	}
	if item.EntryCount != 3 {
		t.Fatalf("entryCount=%d, want 3 visible entries (transcript+brain+board)", item.EntryCount)
	}
	if len(item.Log) != 3 || item.Log[0]["kind"] != "transcript" || item.Log[2]["kind"] != "board_update" {
		t.Fatalf("log=%#v, want 3 chronological rows starting at the transcript", item.Log)
	}
	if item.Log[0]["text"] == "" || item.Log[0]["at"] == "" {
		t.Fatalf("log row=%#v, want text + timestamp", item.Log[0])
	}
	if len(item.Links) != 1 || item.Links[0]["cardId"] != card.ID || item.Links[0]["title"] != "Add bandwidth estimation probe" {
		t.Fatalf("links=%#v, want one link resolved to the live card (dead ids dropped)", item.Links)
	}
}

func TestAssistantMeetingRecordIndexAndDetailAreGroundedAuthorizedAndBounded(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	started := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)
	meetingID := "meeting-rich-record"
	kanbanApp.meetings.startMeeting(officeRoomID, meetingID, started, []string{"Tim", "AJ", "Tom"})
	kanbanApp.meetings.endMeeting(meetingID, started.Add(30*time.Minute), meetingEndedReasonIdle, "")
	first, _, err := kanbanApp.memory.appendAttributedTranscriptWithMetadata("meeting-rich-segment-1", "item-1", "Tim", "high",
		"We decided to ship the governed pilot on Friday.", map[string]string{"meetingId": meetingID, "visibility": "organization"})
	if err != nil {
		t.Fatalf("append first transcript: %v", err)
	}
	second, _, err := kanbanApp.memory.appendAttributedTranscriptWithMetadata("meeting-rich-segment-2", "item-2", "AJ", "high",
		"I will prepare the pilot checklist.", map[string]string{"meetingId": meetingID, "visibility": "organization"})
	if err != nil {
		t.Fatalf("append second transcript: %v", err)
	}
	payload := meetingDigestPayload{
		MeetingID: meetingID, Title: "Governed pilot launch", Day: "2026-08-13", Attendees: []string{"Tim", "AJ", "Tom"},
		Topics: []meetingDigestTopic{{T: "The governed pilot is ready for launch.", Anchor: first.ID, Importance: 5}},
		Decisions: []meetingDigestDecision{
			{D: "Ship the governed pilot on Friday.", By: "Tim", Status: "decided", Anchor: first.ID, Importance: 5},
			{D: "This stale analysis source must never render.", Status: "decided", Anchor: "missing-segment", Importance: 5},
		},
		ActionItems: []meetingDigestAction{
			{A: "Publish the rollout note.", Owner: "Tim", Status: "open", Anchor: first.ID, Importance: 4},
			{A: "Prepare the pilot checklist.", Owner: "AJ", Status: "open", Anchor: second.ID, Importance: 4},
		},
		OpenQuestions: []meetingDigestQuestion{{Q: "Who verifies the rollout receipt?", Anchor: second.ID, Importance: 3}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal digest: %v", err)
	}
	if _, err := kanbanApp.memory.upsertDigest(meetingMemoryKindMeetingDigest, meetingID, string(body), map[string]string{
		"meetingId": meetingID, "visibility": "organization", digestCoverageMetadataKey: coverageLabelFull,
		digestSpanEndMetadataKey:                      started.Add(30 * time.Minute).Format(time.RFC3339),
		meetingRecordDigestSourceRevisionsMetadataKey: meetingRecordDigestSourceRevisionMetadata(payload, meetingRecordSegments(kanbanApp.memory.snapshotForMeeting(meetingID, 0), meetingID)),
	}); err != nil {
		t.Fatalf("upsert meeting digest: %v", err)
	}
	tim := accountStore().findUser("tim@shareability.com")
	if tim == nil {
		t.Fatal("seeded Tim account missing")
	}
	projectThread, err := kanbanApp.createScoutChatThread(tim.Email, tim.Name, "Governed Pilot", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create linked Project channel: %v", err)
	}
	workCard := kanbanApp.snapshotState().Cards[0]
	secondCardResult, _, err := kanbanApp.applyRetiredMeetingBoardToolCallArgs("create_ticket", map[string]any{"title": "Prepare the pilot checklist"})
	if err != nil {
		t.Fatalf("create second linked Work: %v", err)
	}
	secondWorkCard, ok := secondCardResult["card"].(kanbanCard)
	if !ok {
		t.Fatalf("second card result=%#v", secondCardResult)
	}
	secondProjectThread, err := kanbanApp.createScoutChatThread(tim.Email, tim.Name, "Pilot Checklist", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create second linked Project channel: %v", err)
	}
	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Governed pilot checklist", "# Checklist\n\nDelivered.", tim.Name, map[string]string{
		"source": "scout_thread", "status": "complete", "threadStatus": "complete", "boardCardId": workCard.ID,
		"originKind": agentThreadOriginChannel, "originId": projectThread.ID, "requestedBy": tim.Email, "createdBy": tim.Email,
	})
	if err != nil {
		t.Fatalf("create linked artifact: %v", err)
	}
	secondArtifact, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Pilot checklist delivery", "# Checklist\n\nDelivered.", tim.Name, map[string]string{
		"source": "scout_thread", "status": "complete", "threadStatus": "complete", "boardCardId": secondWorkCard.ID,
		"originKind": agentThreadOriginChannel, "originId": secondProjectThread.ID, "requestedBy": tim.Email, "createdBy": tim.Email,
	})
	if err != nil {
		t.Fatalf("create second linked artifact: %v", err)
	}
	claimLinks, err := json.Marshal([]meetingBoardClaimCardLink{{SegmentID: first.ID, CardID: workCard.ID}, {SegmentID: second.ID, CardID: secondWorkCard.ID}})
	if err != nil {
		t.Fatalf("marshal claim links: %v", err)
	}
	if _, _, err := kanbanApp.memory.appendBoardUpdate("meeting-rich-board-link", "Linked governed Work.", map[string]string{
		"meetingId": meetingID, "cardIds": workCard.ID + "," + secondWorkCard.ID, "claimCardLinks": string(claimLinks),
	}); err != nil {
		t.Fatalf("append linked meeting Work: %v", err)
	}

	// A directory row plus a private source is not a grant to another member.
	hiddenID := "meeting-private-record"
	kanbanApp.meetings.startMeeting("private-room", hiddenID, started.Add(time.Hour), []string{"AJ"})
	if _, _, err := kanbanApp.memory.appendBrainWriteUp("private-meeting-source", "AJ private meeting body.", map[string]string{
		"meetingId": hiddenID, "roomId": "private-room", "visibility": "private", "ownerEmail": "aj@shareability.com",
	}); err != nil {
		t.Fatalf("append private meeting source: %v", err)
	}

	request := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantMeetingsHandler(recorder, req)
		return recorder
	}

	indexRecorder := request("/assistant/meetings?view=index&limit=60")
	if indexRecorder.Code != http.StatusOK {
		t.Fatalf("index status=%d body=%s", indexRecorder.Code, indexRecorder.Body.String())
	}
	var indexPayload struct {
		Contract string                   `json:"contract"`
		Meetings []meetingRecordIndexItem `json:"meetings"`
	}
	if err := json.Unmarshal(indexRecorder.Body.Bytes(), &indexPayload); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	if indexPayload.Contract != meetingRecordContractVersion || len(indexPayload.Meetings) != 1 {
		t.Fatalf("index=%+v, want one authorized %s row", indexPayload, meetingRecordContractVersion)
	}
	row := indexPayload.Meetings[0]
	if row.ID != meetingID || row.Title != "Governed pilot launch" || row.OutcomePreview != "Ship the governed pilot on Friday." || row.TranscriptCount != 2 || row.DecisionCount != 1 {
		t.Fatalf("row=%+v, want honest title/outcome/counts", row)
	}
	if len(row.Participants) != 2 || slices.Contains(row.Participants, "Tom") {
		t.Fatalf("participants=%v, directory/digest-only attendee must not disclose without an authorized transcript segment", row.Participants)
	}
	if strings.Contains(indexRecorder.Body.String(), "prepare the pilot checklist") || strings.Contains(indexRecorder.Body.String(), hiddenID) {
		t.Fatalf("bounded index leaked detail/private meeting: %s", indexRecorder.Body.String())
	}
	// The index path must remain body-free inside the server, not merely omit
	// bodies from JSON. Transcript bodies are represented by their server-owned
	// digest metadata; only the bounded digest body needed for the honest row
	// label is copied. Exact detail hydration is separately scoped to one id.
	principal := recallPrincipalForUser(tim)
	visits := 0
	kanbanApp.memory.mu.Lock()
	kanbanApp.memory.meetingEntryVisitHook = func() { visits++ }
	kanbanApp.memory.mu.Unlock()
	t.Cleanup(func() {
		kanbanApp.memory.mu.Lock()
		kanbanApp.memory.meetingEntryVisitHook = nil
		kanbanApp.memory.mu.Unlock()
	})
	indexProjections, indexStore := kanbanApp.meetingRecordProjectionsForPrincipal(context.Background(), principal, meetingRecordIndexLimit, "", false)
	baselineVisits := visits
	if len(indexProjections) != 1 || indexProjections[0].index.RecordRevision != row.RecordRevision {
		t.Fatalf("body-free index projections=%+v, want same exact row revision", indexProjections)
	}
	if baselineVisits == 0 {
		t.Fatal("body-free index did not inspect its selected meeting entries")
	}
	// Mature brains contain years of unrelated Chat, artifact, and reflection
	// rows. Add a large unrelated ledger tail without touching the maintained
	// meeting directory, then prove the exact same index request visits exactly
	// the same number of durable rows. Response cardinality alone would not catch
	// the old O(total-ledger) scan and allocation regression.
	kanbanApp.memory.mu.Lock()
	for index := 0; index < 5000; index++ {
		kanbanApp.memory.entries = append(kanbanApp.memory.entries, meetingMemoryEntry{
			ID:        fmt.Sprintf("unrelated-ledger-%04d", index),
			Kind:      meetingMemoryKindReflection,
			Text:      "Unrelated historical body that a Meeting index must never inspect or clone.",
			CreatedAt: started.Add(-time.Duration(index+1) * time.Minute),
			Metadata:  map[string]string{"visibility": "organization"},
		})
	}
	kanbanApp.memory.mu.Unlock()
	visits = 0
	stressProjections, _ := kanbanApp.meetingRecordProjectionsForPrincipal(context.Background(), principal, meetingRecordIndexLimit, "", false)
	if len(stressProjections) != 1 || visits != baselineVisits {
		t.Fatalf("unrelated ledger growth changed Meeting index work: projections=%d visits=%d want visits=%d", len(stressProjections), visits, baselineVisits)
	}
	for _, entry := range indexStore.snapshot(0) {
		if entry.Kind == meetingMemoryKindTranscript && entry.Text != "" {
			t.Fatalf("index cloned transcript body for %s", entry.ID)
		}
		if (entry.Kind == meetingMemoryKindTranscript || isMeetingDigestKind(entry.Kind)) && strings.TrimSpace(entry.BodyDigest) == "" {
			t.Fatalf("index entry %s lacks server-owned body digest", entry.ID)
		}
	}
	detailProjections, detailStore := kanbanApp.meetingRecordProjectionsForPrincipal(context.Background(), principal, 1, meetingID, true)
	if len(detailProjections) != 1 {
		t.Fatalf("exact detail projections=%d, want one", len(detailProjections))
	}
	for _, entry := range detailStore.snapshot(0) {
		if strings.TrimSpace(entry.Metadata["meetingId"]) != meetingID {
			t.Fatalf("exact detail hydrated unrelated meeting body %s/%s", entry.Metadata["meetingId"], entry.ID)
		}
	}

	detailRecorder := request("/assistant/meetings/" + meetingID + "?transcriptLimit=1")
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detailRecorder.Code, detailRecorder.Body.String())
	}
	var detailPayload struct {
		Meeting meetingRecordDetail `json:"meeting"`
	}
	if err := json.Unmarshal(detailRecorder.Body.Bytes(), &detailPayload); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	detail := detailPayload.Meeting
	if len(detail.Decisions) != 1 || detail.Decisions[0].Sources[0].SegmentID != first.ID || detail.Decisions[0].Sources[0].Revision == "" {
		t.Fatalf("decisions=%+v, want only the currently grounded decision", detail.Decisions)
	}
	if len(detail.Commitments) != 2 || detail.Commitments[0].Owner != "Tim" || detail.Commitments[1].Owner != "AJ" {
		t.Fatalf("commitments=%+v, want two exact anchored commitments", detail.Commitments)
	}
	if firstCommitment, secondCommitment := detail.Commitments[0], detail.Commitments[1]; firstCommitment.DueState != "unresolved" || firstCommitment.WorkState != "resolved" || firstCommitment.ProjectState != "resolved" ||
		len(firstCommitment.Work) != 1 || firstCommitment.Work[0].ID != workCard.ID || firstCommitment.Work[0].OpenKind != "artifact" || firstCommitment.Work[0].OpenID != artifact.ID ||
		len(firstCommitment.Projects) != 1 || firstCommitment.Projects[0].ID != projectThread.ID || firstCommitment.Projects[0].OpenKind != "project" || firstCommitment.Projects[0].OpenID != projectThread.ID ||
		secondCommitment.DueState != "unresolved" || secondCommitment.WorkState != "resolved" || secondCommitment.ProjectState != "resolved" ||
		len(secondCommitment.Work) != 1 || secondCommitment.Work[0].ID != secondWorkCard.ID || secondCommitment.Work[0].OpenKind != "artifact" || secondCommitment.Work[0].OpenID != secondArtifact.ID ||
		len(secondCommitment.Projects) != 1 || secondCommitment.Projects[0].ID != secondProjectThread.ID || secondCommitment.Projects[0].OpenKind != "project" || secondCommitment.Projects[0].OpenID != secondProjectThread.ID {
		t.Fatalf("commitment links=%+v, want exact 2x2 Work/Project associations with no crossing", detail.Commitments)
	}
	if len(detail.Work) != 2 || len(detail.Projects) != 2 || len(detail.Artifacts) != 2 ||
		!slices.ContainsFunc(detail.Work, func(reference meetingRecordReference) bool {
			return reference.ID == workCard.ID && reference.OpenKind == "artifact" && reference.OpenID == artifact.ID
		}) ||
		!slices.ContainsFunc(detail.Work, func(reference meetingRecordReference) bool {
			return reference.ID == secondWorkCard.ID && reference.OpenKind == "artifact" && reference.OpenID == secondArtifact.ID
		}) ||
		!slices.ContainsFunc(detail.Projects, func(reference meetingRecordReference) bool { return reference.ID == projectThread.ID }) ||
		!slices.ContainsFunc(detail.Projects, func(reference meetingRecordReference) bool { return reference.ID == secondProjectThread.ID }) ||
		!slices.ContainsFunc(detail.Artifacts, func(reference meetingRecordReference) bool { return reference.ID == artifact.ID }) ||
		!slices.ContainsFunc(detail.Artifacts, func(reference meetingRecordReference) bool { return reference.ID == secondArtifact.ID }) {
		t.Fatalf("references work=%+v projects=%+v artifacts=%+v, want exact viewer-authorized Work/Project/artifact identities", detail.Work, detail.Projects, detail.Artifacts)
	}
	if detail.Coverage.UnavailableClaims != 1 || len(detail.Coverage.Gaps) == 0 {
		t.Fatalf("coverage=%+v, want one withheld stale claim and an honest gap", detail.Coverage)
	}
	if len(detail.Transcript.Segments) != 1 || !detail.Transcript.HasMore || detail.Transcript.NextCursor != first.ID || detail.Transcript.Segments[0].Text != "We decided to ship the governed pilot on Friday." {
		t.Fatalf("transcript=%+v, want bounded speaker-attributed first page", detail.Transcript)
	}
	nextRecorder := request("/assistant/meetings/" + meetingID + "?transcriptLimit=1&cursor=" + first.ID)
	var nextPayload struct {
		Meeting meetingRecordDetail `json:"meeting"`
	}
	if err := json.Unmarshal(nextRecorder.Body.Bytes(), &nextPayload); err != nil || len(nextPayload.Meeting.Transcript.Segments) != 1 || nextPayload.Meeting.Transcript.Segments[0].ID != second.ID || nextPayload.Meeting.Transcript.HasMore {
		t.Fatalf("next page body=%s err=%v", nextRecorder.Body.String(), err)
	}
	searchRecorder := request("/assistant/meetings/" + meetingID + "?q=checklist")
	var searchPayload struct {
		Meeting meetingRecordDetail `json:"meeting"`
	}
	if err := json.Unmarshal(searchRecorder.Body.Bytes(), &searchPayload); err != nil || len(searchPayload.Meeting.Transcript.Segments) != 1 || searchPayload.Meeting.Transcript.Segments[0].ID != second.ID {
		t.Fatalf("search body=%s err=%v", searchRecorder.Body.String(), err)
	}
	exactSegmentRecorder := request("/assistant/meetings/" + meetingID + "?segmentId=" + second.ID + "&transcriptLimit=1")
	var exactSegmentPayload struct {
		Meeting meetingRecordDetail `json:"meeting"`
	}
	if err := json.Unmarshal(exactSegmentRecorder.Body.Bytes(), &exactSegmentPayload); err != nil || len(exactSegmentPayload.Meeting.Transcript.Segments) != 1 || exactSegmentPayload.Meeting.Transcript.Segments[0].ID != second.ID {
		t.Fatalf("exact segment body=%s err=%v", exactSegmentRecorder.Body.String(), err)
	}

	postMeetingBody := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantMeetingsHandler(recorder, req)
		return recorder
	}
	if recorder := postMeetingBody("/assistant/meetings", `{}`); recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("collection POST status=%d body=%s, want method not allowed", recorder.Code, recorder.Body.String())
	}

	postConversation := func(revision string) *httptest.ResponseRecorder {
		t.Helper()
		body := fmt.Sprintf(`{"recordRevision":%q}`, revision)
		return postMeetingBody("/assistant/meetings/"+meetingID+"/conversation", body)
	}
	if recorder := postMeetingBody("/assistant/meetings/"+meetingID+"/conversation", fmt.Sprintf(`{"recordRevision":%q,"extra":true}`, row.RecordRevision)); recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown conversation field status=%d body=%s, want bad request", recorder.Code, recorder.Body.String())
	}
	if recorder := postMeetingBody("/assistant/meetings/"+meetingID+"/conversation", fmt.Sprintf(`{"recordRevision":%q}{}`, row.RecordRevision)); recorder.Code != http.StatusBadRequest {
		t.Fatalf("trailing conversation body status=%d body=%s, want bad request", recorder.Code, recorder.Body.String())
	}
	conversationRecorder := postConversation(row.RecordRevision)
	if conversationRecorder.Code != http.StatusCreated || strings.Contains(conversationRecorder.Body.String(), "meetingRecord") {
		t.Fatalf("conversation status=%d body=%s, want created private thread with server binding stripped", conversationRecorder.Code, conversationRecorder.Body.String())
	}
	var conversationPayload struct {
		Thread scoutChatThreadRecord `json:"thread"`
	}
	if err := json.Unmarshal(conversationRecorder.Body.Bytes(), &conversationPayload); err != nil || conversationPayload.Thread.ID == "" || scoutChatThreadVisibility(conversationPayload.Thread) != scoutChatVisibilityPrivate {
		t.Fatalf("decode conversation body=%s err=%v", conversationRecorder.Body.String(), err)
	}
	rawConversation, _, err := kanbanApp.scoutChatThreadByID("tim@shareability.com", conversationPayload.Thread.ID)
	if err != nil || rawConversation.MeetingRecord == nil || rawConversation.MeetingRecord.MeetingID != meetingID || rawConversation.MeetingRecord.RecordRevision != row.RecordRevision {
		t.Fatalf("raw conversation=%+v err=%v, want exact durable Meeting Record binding", rawConversation.MeetingRecord, err)
	}
	if replay := postConversation(row.RecordRevision); replay.Code != http.StatusOK {
		t.Fatalf("conversation replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	if stale := postConversation(strings.Repeat("0", 64)); stale.Code != http.StatusConflict {
		t.Fatalf("stale conversation status=%d body=%s, want conflict", stale.Code, stale.Body.String())
	}

	kanbanApp.mu.Lock()
	kanbanApp.apiKey = "meeting-record-test"
	kanbanApp.mu.Unlock()
	var answerInput string
	providerCalls := 0
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		providerCalls++
		switch request.Workflow {
		case "scout_route":
			if strings.Contains(request.Input, "Create a governed follow-up") {
				return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
					Outcome: string(conversationIntentStartPrivateWork), Route: "workstream", Mode: "research",
					Objective: "Create a governed follow-up from the pilot meeting",
				}), nil
			}
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Outcome: string(conversationIntentConversationalReply)}), nil
		case "scout_chat":
			answerInput = request.Input
			return "Transcript: We decided to ship the governed pilot on Friday. [segment:" + first.ID + "]", nil
		default:
			t.Fatalf("unexpected workflow %q", request.Workflow)
			return "", nil
		}
	})
	previousProbe := recallModelContextProbe
	recallModelContextProbe = func(entries []meetingMemoryEntry) {
		if len(entries) != 0 {
			t.Fatalf("revision-bound meeting answer widened into general recall: %+v", entries)
		}
	}
	t.Cleanup(func() { recallModelContextProbe = previousProbe })
	messageReq := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+conversationPayload.Thread.ID+"/messages", strings.NewReader(`{"text":"What did we decide?","operationId":"meeting-record-question-0001"}`))
	messageReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
		messageReq.AddCookie(cookie)
	}
	messageRecorder := httptest.NewRecorder()
	assistantChatThreadHandler(messageRecorder, messageReq)
	if messageRecorder.Code != http.StatusOK {
		t.Fatalf("meeting question status=%d body=%s", messageRecorder.Code, messageRecorder.Body.String())
	}
	var messagePayload struct {
		Answer scoutChatMessageRecord `json:"answer"`
	}
	if err := json.Unmarshal(messageRecorder.Body.Bytes(), &messagePayload); err != nil || len(messagePayload.Answer.Sources) != 1 {
		t.Fatalf("meeting answer body=%s err=%v", messageRecorder.Body.String(), err)
	}
	answerSource := messagePayload.Answer.Sources[0]
	if answerSource.Kind != "meeting_transcript" || answerSource.MeetingID != meetingID || answerSource.SegmentID != first.ID || answerSource.Revision == "" {
		t.Fatalf("meeting source=%+v, want exact transcript interval", answerSource)
	}
	if !strings.Contains(answerInput, "Exact Meeting Record transcript context") || !strings.Contains(answerInput, first.ID) || !strings.Contains(answerInput, second.ID) || strings.Contains(answerInput, "AJ private meeting body") {
		t.Fatalf("meeting answer input crossed source boundary: %s", answerInput)
	}
	if providerCalls != 2 {
		t.Fatalf("provider calls=%d, want router plus exact transcript answer", providerCalls)
	}
	workReq := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+conversationPayload.Thread.ID+"/messages", strings.NewReader(`{"text":"Create a governed follow-up from this meeting.","operationId":"meeting-record-work-0001"}`))
	workReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
		workReq.AddCookie(cookie)
	}
	workRecorder := httptest.NewRecorder()
	assistantChatThreadHandler(workRecorder, workReq)
	var workPayload struct {
		IntentOutcome    string                 `json:"intentOutcome"`
		ApprovalRequired bool                   `json:"approvalRequired"`
		Proposal         scoutRouterProposal    `json:"proposal"`
		Answer           scoutChatMessageRecord `json:"answer"`
	}
	if workRecorder.Code != http.StatusOK || json.Unmarshal(workRecorder.Body.Bytes(), &workPayload) != nil ||
		workPayload.IntentOutcome != string(conversationIntentApprovalRequired) || !workPayload.ApprovalRequired {
		t.Fatalf("Meeting follow-up status=%d body=%s, want held governed proposal", workRecorder.Code, workRecorder.Body.String())
	}
	expectedMeetingRef := meetingRecordContextRef(meetingID, row.RecordRevision)
	if refs := decodeAssistantContextRefs(workPayload.Proposal.ContextRefs); len(refs) != 1 || refs[0] != expectedMeetingRef || strings.Contains(workRecorder.Body.String(), `"agentThread"`) {
		t.Fatalf("Meeting follow-up proposal=%+v, want exact record ref and no launched work", workPayload.Proposal)
	}
	if entry, readable := kanbanApp.assistantContextEntryForRef(context.Background(), principal, expectedMeetingRef); !readable || !strings.Contains(entry.Text, first.ID) || !strings.Contains(entry.Text, second.ID) {
		t.Fatalf("Meeting follow-up context readable=%v entry=%+v, want exact transcript-bound worker context", readable, entry)
	}
	if providerCalls != 3 {
		t.Fatalf("Meeting follow-up provider calls=%d, want router only and no work/provider launch", providerCalls)
	}
	if workPayload.Answer.ID == "" {
		t.Fatalf("Meeting follow-up response omitted its persisted proposal message: %s", workRecorder.Body.String())
	}
	beforeCards := len(kanbanApp.snapshotState().Cards)
	beforeArtifacts := 0
	for _, entry := range kanbanApp.memory.snapshot(0) {
		if entry.Kind == meetingMemoryKindOSArtifact {
			beforeArtifacts++
		}
	}
	if _, deleted, deleteErr := kanbanApp.memory.deleteEntryByID(first.ID); deleteErr != nil || !deleted {
		t.Fatalf("withdraw transcript source deleted=%v err=%v", deleted, deleteErr)
	}
	if _, readable := kanbanApp.assistantContextEntryForRef(context.Background(), principal, expectedMeetingRef); readable {
		t.Fatal("withdrawn Meeting Record remained readable at the worker-admission seam")
	}
	if _, acceptErr := kanbanApp.resolveScoutChatProposal(context.Background(), tim, conversationPayload.Thread.ID, scoutChatProposalAction{
		Action: "accepted", MessageID: workPayload.Answer.ID, Objective: workPayload.Proposal.Objective,
	}); acceptErr == nil || !strings.Contains(acceptErr.Error(), "source") {
		t.Fatalf("withdrawn Meeting proposal accept err=%v, want current-source rejection", acceptErr)
	}
	afterArtifacts := 0
	for _, entry := range kanbanApp.memory.snapshot(0) {
		if entry.Kind == meetingMemoryKindOSArtifact {
			afterArtifacts++
		}
	}
	if providerCalls != 3 || len(kanbanApp.snapshotState().Cards) != beforeCards || afterArtifacts != beforeArtifacts {
		t.Fatalf("withdrawn Meeting proposal caused effects: provider=%d cards=%d/%d artifacts=%d/%d", providerCalls, len(kanbanApp.snapshotState().Cards), beforeCards, afterArtifacts, beforeArtifacts)
	}
	threadReq := httptest.NewRequest(http.MethodGet, "/assistant/chat-threads/"+conversationPayload.Thread.ID, nil)
	for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
		threadReq.AddCookie(cookie)
	}
	threadRecorder := httptest.NewRecorder()
	assistantChatThreadHandler(threadRecorder, threadReq)
	var staleThreadPayload struct {
		Thread scoutChatThreadRecord `json:"thread"`
	}
	if err := json.Unmarshal(threadRecorder.Body.Bytes(), &staleThreadPayload); err != nil || len(staleThreadPayload.Thread.Messages) < 4 {
		t.Fatalf("stale thread body=%s err=%v", threadRecorder.Body.String(), err)
	}
	staleAnswer := scoutChatMessageRecord{}
	staleProposal := scoutChatMessageRecord{}
	for _, candidate := range staleThreadPayload.Thread.Messages {
		if candidate.ID == messagePayload.Answer.ID {
			staleAnswer = candidate
		}
		if candidate.Kind == scoutChatMessageKindProposal {
			staleProposal = candidate
		}
	}
	if staleAnswer.IntentOutcome != string(conversationIntentUnavailable) || len(staleAnswer.Sources) != 0 || strings.Contains(staleAnswer.Text, "ship the governed pilot") {
		t.Fatalf("stale Meeting Record answer survived source withdrawal: %+v", staleAnswer)
	}
	if staleProposal.ID == "" || staleProposal.IntentOutcome != string(conversationIntentUnavailable) || staleProposal.Proposal != nil || staleProposal.Choices != nil || staleProposal.Manifest != nil || staleProposal.Thread != nil || staleProposal.Work != nil || strings.Contains(staleProposal.Text, "follow-up") {
		t.Fatalf("stale Meeting Record proposal survived source withdrawal: %+v", staleProposal)
	}
	retryReq := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+conversationPayload.Thread.ID+"/messages", strings.NewReader(`{"text":"What did we decide?","operationId":"meeting-record-question-0001"}`))
	retryReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
		retryReq.AddCookie(cookie)
	}
	retryRecorder := httptest.NewRecorder()
	assistantChatThreadHandler(retryRecorder, retryReq)
	if retryRecorder.Code != http.StatusOK || providerCalls != 3 || !strings.Contains(retryRecorder.Body.String(), "source revision is no longer authorized") {
		t.Fatalf("stale replay status=%d providerCalls=%d body=%s", retryRecorder.Code, providerCalls, retryRecorder.Body.String())
	}
	corrected, _, correctionErr := kanbanApp.memory.appendAttributedTranscriptWithMetadata("meeting-rich-segment-2-corrected", "item-2-corrected", "AJ", "high",
		"I will prepare the launch checklist after legal review.", map[string]string{
			"meetingId": meetingID, "visibility": "organization", "correctionState": "corrected", "supersedesId": second.ID,
		})
	if correctionErr != nil {
		t.Fatalf("append corrected transcript: %v", correctionErr)
	}
	correctedRecorder := request("/assistant/meetings/" + meetingID)
	var correctedPayload struct {
		Meeting meetingRecordDetail `json:"meeting"`
	}
	if err := json.Unmarshal(correctedRecorder.Body.Bytes(), &correctedPayload); err != nil {
		t.Fatalf("decode corrected Meeting Record body=%s err=%v", correctedRecorder.Body.String(), err)
	}
	if len(correctedPayload.Meeting.Transcript.Segments) != 1 || correctedPayload.Meeting.Transcript.Segments[0].ID != corrected.ID || correctedPayload.Meeting.Transcript.Segments[0].CorrectionState != "corrected" ||
		len(correctedPayload.Meeting.Decisions) != 0 || len(correctedPayload.Meeting.Commitments) != 0 || strings.Contains(correctedPayload.Meeting.OutcomePreview, "pilot checklist") {
		t.Fatalf("corrected Meeting Record=%+v, want replacement transcript and stale analysis withheld", correctedPayload.Meeting)
	}
	if hidden := request("/assistant/meetings/" + hiddenID); hidden.Code != http.StatusNotFound || strings.Contains(hidden.Body.String(), "private") {
		t.Fatalf("hidden status=%d body=%s, want generic 404", hidden.Code, hidden.Body.String())
	}
}

// The intel stat tiles and pulse chart are fed by real ingestion counts.
func TestMissionPulseCarriesHistogramAndRealCounters(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	if _, _, err := app.memory.appendTranscript("event-hist-1", "item-1", "Tim: two seconds max on the buffer."); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	if _, _, err := app.memory.appendTranscript("event-hist-2", "item-2", "AJ: agreed, keep the retransmission bounded."); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	app.meetings.startMeeting(officeRoomID, "meeting-hist-1", time.Now().UTC(), []string{"AJ"})

	snapshot := app.missionIntelligenceSnapshot(time.Now().UTC())
	pulse, ok := snapshot["pulse"].(map[string]any)
	if !ok {
		t.Fatalf("pulse type=%T, want map", snapshot["pulse"])
	}
	histogram, ok := pulse["histogram"].([]int)
	if !ok || len(histogram) != missionPulseHistogramBuckets {
		t.Fatalf("histogram=%#v, want %d buckets", pulse["histogram"], missionPulseHistogramBuckets)
	}
	total := 0
	for _, count := range histogram {
		total += count
	}
	if total != 2 {
		t.Fatalf("histogram total=%d, want the two fresh transcripts in-window", total)
	}
	if histogram[missionPulseHistogramBuckets-1] != 2 {
		t.Fatalf("histogram=%v, want just-appended entries in the newest bucket", histogram)
	}
	if lines, _ := pulse["transcriptLines"].(int); lines != 2 {
		t.Fatalf("transcriptLines=%v, want 2", pulse["transcriptLines"])
	}
	if today, _ := pulse["meetingsToday"].(int); today != 1 {
		t.Fatalf("meetingsToday=%v, want 1", pulse["meetingsToday"])
	}
	if week, _ := pulse["meetingsThisWeek"].(int); week != 1 {
		t.Fatalf("meetingsThisWeek=%v, want 1", pulse["meetingsThisWeek"])
	}
}

/* ---------- durable idle-close finalization ---------- */

// Rotation and archive publication happen against a durable closing receipt;
// core, meeting-scoped outputs then settle asynchronously and refresh that
// archive. Wider day/company/ledger rollups remain queued and cannot lengthen
// the media close path.
func TestEndMeetingForIdleRotatesBeforeCoreAndRefreshesArchiveTruth(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	originalResponder := createOpenAITextResponse
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })
	createOpenAITextResponse = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		switch {
		case strings.Contains(request.Instructions, "decision ledger"):
			return `{"decisions":[]}`, nil
		case strings.Contains(request.Instructions, "board intelligence"):
			return `{"summary":"No actionable board changes.","operations":[]}`, nil
		case strings.Contains(request.Instructions, "mission intelligence"):
			return `{"themes":[],"openQuestions":[],"alignments":[]}`, nil
		case strings.Contains(request.Instructions, "narrative maintainer"):
			return `{"narratives":[]}`, nil
		case strings.Contains(request.Instructions, "meeting digest compiler"):
			return cannedArchiveMeetingDigestJSON("tx-idle-1"), nil
		case strings.Contains(request.Instructions, "entity-ledger adjudicator"):
			t.Error("idle flush must not spend an adjudication call on all-new facts")
			return "", nil
		case strings.Contains(request.Instructions, "end-of-day reflection"):
			return "", nil
		case strings.Contains(request.Instructions, "company digest narrator"):
			return "The Zebra packaging pilot is decided; the pricing sheet is underway.", nil
		default: // meeting brain
			return "## Overview\nVendor Zebra chosen for the packaging pilot.\n## Transcript reference\ntx-idle-1", nil
		}
	}

	app.noteMeetingAdmission(officeRoomID, "AJ")
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	appendTestTranscript(t, app, "tx-idle-1", "We choose vendor Zebra for the packaging pilot.")
	closedID := app.memory.currentMeetingID(officeRoomID)
	if closedID == "" {
		t.Fatal("expected an active meeting id before the idle end")
	}

	fireIdleEndNow(app)

	var closed meetingRecord
	for _, record := range app.meetings.recent(0) {
		if record.ID == closedID {
			closed = record
		}
	}
	if closed.EndedAt == "" || closed.EndedReason != meetingEndedReasonIdle {
		t.Fatalf("record=%+v, want ended for idle", closed)
	}
	if got := app.memory.currentMeetingID(officeRoomID); got != "" {
		t.Fatalf("meeting id %q not rotated before asynchronous core finalization", got)
	}
	closed = waitForMeetingFinalizationState(t, app, closedID, meetingFinalizationFinalized)
	closed = waitForMeetingArchiveFinalizationSync(t, app, closedID)
	if closed.Finalization == nil || closed.Finalization.State != meetingFinalizationFinalized || closed.Finalization.Brain.State != meetingFinalizationStageComplete || closed.Finalization.Digest.State != meetingFinalizationStageComplete || closed.Finalization.Actions.State != meetingFinalizationStageComplete {
		t.Fatalf("core finalization receipt=%+v, want finalized stages", closed.Finalization)
	}
	// Async core synthesis is explicitly keyed to the CLOSED meeting even though
	// the room already released its current id for a successor.
	brains := app.memory.entriesOfKind(meetingMemoryKindBrain, 0)
	if len(brains) != 1 {
		t.Fatalf("brains=%d, want one final brain keyed to %s", len(brains), closedID)
	}
	if got := strings.TrimSpace(brains[0].Metadata["meetingId"]); got != closedID {
		t.Fatalf("brain meetingId=%q, want %s", got, closedID)
	}
	digest, ok := app.memory.latestDigestPerMeeting()[closedID]
	if !ok {
		t.Fatalf("no meeting_digest for the idle-closed meeting %s", closedID)
	}
	if got := strings.TrimSpace(digest.Metadata["meetingId"]); got != closedID {
		t.Fatalf("digest meetingId=%q, want the closed id %s", got, closedID)
	}
	// The silent auto-archive landed before model completion, then was refreshed
	// to the same finalized receipt.
	archives := app.memory.entriesOfKind(meetingMemoryKindArchive, 0)
	if len(archives) != 1 || strings.TrimSpace(archives[0].Metadata["meetingId"]) != closedID {
		t.Fatalf("archives=%d, want one idle auto-archive pinned to %s", len(archives), closedID)
	}
	files := meetingArchiveFilesOnDisk(t)
	if len(files) != 1 {
		t.Fatalf("archive files=%v, want one", files)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(meetingMemoryPath()), "archives", files[0]))
	if err != nil {
		t.Fatal(err)
	}
	var archived meetingArchive
	if err := json.Unmarshal(raw, &archived); err != nil {
		t.Fatal(err)
	}
	if archived.Meeting == nil || archived.Meeting.Finalization == nil || archived.Meeting.Finalization.State != meetingFinalizationFinalized {
		t.Fatalf("archive embedded finalization=%+v, want finalized", archived.Meeting)
	}
}

// Model failure at the idle boundary is best-effort: no rollup lands, but the
// rotation and the silent auto-archive ALWAYS proceed — liveness never
// depends on the model.
func TestEndMeetingForIdleModelFailureNeverBlocksRotation(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	originalResponder := createOpenAITextResponse
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		return "", fmt.Errorf("model down")
	}

	app.noteMeetingAdmission(officeRoomID, "AJ")
	appendTestTranscript(t, app, "tx-idle-2", "We choose vendor Zebra for the packaging pilot.")
	closedID := app.memory.currentMeetingID(officeRoomID)

	fireIdleEndNow(app)

	var closed meetingRecord
	for _, record := range app.meetings.recent(0) {
		if record.ID == closedID {
			closed = record
		}
	}
	if closed.EndedAt == "" || closed.EndedReason != meetingEndedReasonIdle {
		t.Fatalf("record=%+v, want ended for idle despite the model failure", closed)
	}
	if got := app.memory.currentMeetingID(officeRoomID); got != "" {
		t.Fatalf("meeting id %q not rotated before the failed model call settled", got)
	}
	closed = waitForMeetingFinalizationState(t, app, closedID, meetingFinalizationDegraded)
	closed = waitForMeetingArchiveFinalizationSync(t, app, closedID)
	if closed.Finalization == nil || closed.Finalization.State != meetingFinalizationDegraded {
		t.Fatalf("failed model receipt=%+v, want degraded", closed.Finalization)
	}
	if digests := app.memory.entriesOfKind(meetingMemoryKindMeetingDigest, 0); len(digests) != 0 {
		t.Fatalf("digests=%d, want none persisted from a failed flush", len(digests))
	}
	archives := app.memory.entriesOfKind(meetingMemoryKindArchive, 0)
	if len(archives) != 1 || strings.TrimSpace(archives[0].Metadata["meetingId"]) != closedID {
		t.Fatalf("archives=%d, want the silent auto-archive despite the model failure", len(archives))
	}
	files := meetingArchiveFilesOnDisk(t)
	if len(files) != 1 {
		t.Fatalf("archive files=%v, want one", files)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(meetingMemoryPath()), "archives", files[0]))
	if err != nil {
		t.Fatal(err)
	}
	var archived meetingArchive
	if err := json.Unmarshal(raw, &archived); err != nil {
		t.Fatal(err)
	}
	if archived.Meeting == nil || archived.Meeting.Finalization == nil || archived.Meeting.Finalization.State != meetingFinalizationDegraded {
		t.Fatalf("archive falsely implied finalized: %+v", archived.Meeting)
	}
}

/* ---------- multi-room W2: per-room sitting spine (record layer) ---------- */

// Two rooms hold independent sittings: records, meeting ids, and idle closes
// are all room-scoped, and closing room B never rotates the office's id or
// touches its record — the record-layer half of the cursor-corruption fence.
func TestMeetingRecordsAndIdleEndPerRoom(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("admitParticipant: %v", err)
	}
	app.noteMeetingAdmission(officeRoomID, "AJ")
	app.noteMeetingAdmission("room-b", "Tim")

	office, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("no open office record after admission")
	}
	roomB, ok := app.meetings.activeRecord("room-b")
	if !ok {
		t.Fatal("no open room-b record after admission")
	}
	if office.ID == roomB.ID {
		t.Fatalf("both rooms share meeting id %q, want independent ids", office.ID)
	}
	// office records keep the pre-room shape (empty RoomID reads as office);
	// named rooms stamp theirs.
	if office.RoomID != "" || meetingRoomID(office) != officeRoomID {
		t.Fatalf("office RoomID=%q, want empty (reads as office)", office.RoomID)
	}
	if roomB.RoomID != "room-b" {
		t.Fatalf("room-b RoomID=%q, want room-b", roomB.RoomID)
	}

	// entries stamp the room's own meeting id plus metadata.roomId.
	entryB, _, err := app.memory.appendAttributedTranscriptEntry("room-b", "event-b", "", "Tim", "", "Suit Barn side meeting notes for the record.", nil, true, "")
	if err != nil {
		t.Fatalf("append room-b transcript: %v", err)
	}
	if entryB.Metadata["meetingId"] != roomB.ID {
		t.Fatalf("room-b entry meetingId=%q, want %q", entryB.Metadata["meetingId"], roomB.ID)
	}
	if entryB.Metadata["roomId"] != "room-b" {
		t.Fatalf("room-b entry roomId=%q, want room-b", entryB.Metadata["roomId"])
	}

	// room B idle-ends (named rooms have no live plane in W2, so the fire's
	// occupancy check reads zero); the office sitting must be untouched.
	fireIdleEndNowInRoom(app, "room-b")
	if record, active := app.meetings.activeRecord("room-b"); active {
		t.Fatalf("room-b record=%#v, want closed after idle end", record)
	}
	if got := app.memory.currentMeetingID("room-b"); got != "" {
		t.Fatalf("room-b memory id=%q after idle end, want rotated (empty)", got)
	}
	stillOpen, active := app.meetings.activeRecord(officeRoomID)
	if !active || stillOpen.ID != office.ID || stillOpen.EndedAt != "" {
		t.Fatalf("office record=%#v, want %q still open after room-b closed", stillOpen, office.ID)
	}
	if got := app.memory.currentMeetingID(officeRoomID); got != office.ID {
		t.Fatalf("office memory id=%q, want %q un-rotated by room-b's close", got, office.ID)
	}

	// room B's next sitting mints a fresh id, never the closed one.
	app.noteMeetingAdmission("room-b", "Tim")
	successor, ok := app.meetings.activeRecord("room-b")
	if !ok || successor.ID == roomB.ID {
		t.Fatalf("successor=%#v, want a fresh room-b record distinct from %q", successor, roomB.ID)
	}
}

// The admission-vs-fired-timer race, ported per room: a join landing between
// room B's fired idle timer and its close bumps ROOM B's generation, so the
// stale fire can neither end B's meeting nor rotate its id — and one room's
// seam can never validate against another room's counter.
func TestIdleFireInvalidatedByAdmissionGenerationPerRoom(t *testing.T) {
	t.Setenv("MEETING_IDLE_END_GRACE", "1h")
	app := newIsolatedKanbanBoardApp(t)
	app.noteMeetingAdmission("room-b", "AJ")
	open, ok := app.meetings.activeRecord("room-b")
	if !ok {
		t.Fatal("no open room-b record after admission")
	}

	app.noteMeetingOccupancy("room-b") // arms room B's timer
	app.meetings.mu.Lock()
	armTimeGeneration := app.meetings.idleGenerations["room-b"]
	app.meetings.mu.Unlock()

	// the admission lands in the fired timer's window — cancelIdleEnd bumps
	// room B's generation even though the timer can no longer be stopped.
	app.noteMeetingAdmission("room-b", "AJ")

	// the fired timer's close is a no-op against the stale generation.
	app.endMeetingForIdle("room-b", armTimeGeneration)
	record, ok := app.meetings.activeRecord("room-b")
	if !ok || record.ID != open.ID || record.EndedAt != "" {
		t.Fatalf("record=%#v, want room-b meeting %q still open", record, open.ID)
	}
	if got := app.memory.currentMeetingID("room-b"); got != open.ID {
		t.Fatalf("room-b memory id=%q, want %q un-rotated", got, open.ID)
	}

	// another room's idle seam never touches room B: the office fire is a
	// no-op (no office record), whatever generation it carries.
	app.meetings.mu.Lock()
	liveGeneration := app.meetings.idleGenerations["room-b"]
	app.meetings.mu.Unlock()
	app.endMeetingForIdle(officeRoomID, liveGeneration)
	if record, stillOpen := app.meetings.activeRecord("room-b"); !stillOpen || record.EndedAt != "" {
		t.Fatalf("record=%#v, want room-b untouched by the office seam", record)
	}

	// a genuinely empty room B still idle-ends with the live generation.
	app.noteMeetingOccupancy("room-b")
	fireIdleEndNowInRoom(app, "room-b")
	if record, stillOpen := app.meetings.activeRecord("room-b"); stillOpen {
		t.Fatalf("record=%#v, want the fresh-generation idle end to close it", record)
	}
}

// The hasEndedRecord re-mint guard, ported per room: room B's admission after
// its id ended mints a FRESH id, and the racing closer's conditional rotation
// — for room B or any other room — can never clobber it.
func TestAdmissionNeverReMintsEndedMeetingIDPerRoom(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.noteMeetingAdmission("room-b", "AJ")
	endedID := app.memory.currentMeetingID("room-b")
	if _, changed := app.meetings.endMeeting(endedID, time.Now().UTC(), meetingEndedReasonIdle, ""); !changed {
		t.Fatal("endMeeting did not close the room-b record")
	}

	app.noteMeetingAdmission("room-b", "Tim")
	record, ok := app.meetings.activeRecord("room-b")
	if !ok {
		t.Fatal("admission did not open a room-b record")
	}
	if record.ID == endedID {
		t.Fatalf("admission re-minted the ended id %q", endedID)
	}
	if got := app.memory.currentMeetingID("room-b"); got != record.ID {
		t.Fatalf("room-b memory id=%q, want aligned with the new record %q", got, record.ID)
	}

	// the racing closer's rotation arrives last: conditional AND room-scoped.
	app.memory.rotateMeetingIDIfCurrent("room-b", endedID)
	if got := app.memory.currentMeetingID("room-b"); got != record.ID {
		t.Fatalf("room-b memory id=%q after the stale rotation, want %q intact", got, record.ID)
	}
	// an office-keyed rotation with room B's live id is a different room's
	// seam entirely: no-op.
	app.memory.rotateMeetingIDIfCurrent(officeRoomID, record.ID)
	if got := app.memory.currentMeetingID("room-b"); got != record.ID {
		t.Fatalf("room-b memory id=%q after a cross-room rotation, want %q intact", got, record.ID)
	}
}

// startMeeting's defensive restart-close is room-scoped by construction: the
// office starting a new sitting under a mismatched id closes only the OFFICE's
// stale open record, never another room's.
func TestStartMeetingDefensiveCloseIsRoomScoped(t *testing.T) {
	store, err := loadMeetingStore(filepath.Join(t.TempDir(), "meetings.json"))
	if err != nil {
		t.Fatalf("loadMeetingStore: %v", err)
	}
	now := time.Now().UTC()
	if _, changed := store.startMeeting(officeRoomID, "meeting-office-1", now, []string{"AJ"}); !changed {
		t.Fatal("startMeeting office did not open a record")
	}
	if _, changed := store.startMeeting("room-b", "meeting-b-1", now, []string{"Tim"}); !changed {
		t.Fatal("startMeeting room-b did not open a record")
	}

	// office restart under a different id: the defensive close hits the office
	// record only.
	if _, changed := store.startMeeting(officeRoomID, "meeting-office-2", now.Add(time.Minute), []string{"AJ"}); !changed {
		t.Fatal("startMeeting office-2 did not open a record")
	}
	roomB, ok := store.activeRecord("room-b")
	if !ok || roomB.ID != "meeting-b-1" || roomB.EndedAt != "" {
		t.Fatalf("room-b record=%#v, want meeting-b-1 still open after the office restart", roomB)
	}
	office, ok := store.activeRecord(officeRoomID)
	if !ok || office.ID != "meeting-office-2" {
		t.Fatalf("office record=%#v, want meeting-office-2 open", office)
	}
	for _, record := range store.recent(0) {
		if record.ID == "meeting-office-1" && record.EndedReason != meetingEndedReasonRestart {
			t.Fatalf("displaced office record=%#v, want reason restart", record)
		}
	}
}

// Boot reconciliation runs per room: the office's in-flight sitting resumes
// while room B's idle-ended sitting rotates away, and room B's next admission
// mints a fresh id — never the ended one.
func TestBootReconcileResumesEachRoomIndependently(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.noteMeetingAdmission(officeRoomID, "AJ")
	officeRecord, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("no open office record after admission")
	}
	if _, _, err := app.memory.appendTranscript("event-office", "item-1", "Boot Barn kickoff planning notes."); err != nil {
		t.Fatalf("append office transcript: %v", err)
	}
	app.noteMeetingAdmission("room-b", "Tim")
	bRecord, ok := app.meetings.activeRecord("room-b")
	if !ok {
		t.Fatal("no open room-b record after admission")
	}
	if _, _, err := app.memory.appendAttributedTranscriptEntry("room-b", "event-b", "", "Tim", "", "Suit Barn side meeting notes for the record.", nil, true, ""); err != nil {
		t.Fatalf("append room-b transcript: %v", err)
	}
	fireIdleEndNowInRoom(app, "room-b") // closes B; the rotation is in-process only

	// routine deploy: the process restarts on the same data dir.
	reopened := newKanbanBoardApp()

	// the office resumed its in-flight sitting on the same id and open record.
	if got := reopened.memory.currentMeetingID(officeRoomID); got != officeRecord.ID {
		t.Fatalf("office resumed id=%q, want %q", got, officeRecord.ID)
	}
	resumed, ok := reopened.meetings.activeRecord(officeRoomID)
	if !ok || resumed.ID != officeRecord.ID {
		t.Fatalf("office record=%#v, want %q still open across the restart", resumed, officeRecord.ID)
	}

	// room B's ended sitting must not resume (its JSONL tail is the archive
	// artifact, whose id matches an ENDED record — reconciliation rotates it).
	if got := reopened.memory.currentMeetingID("room-b"); got == bRecord.ID {
		t.Fatalf("boot resumed room-b's ended meeting id %q; reconciliation must rotate it", got)
	}
	reopened.noteMeetingAdmission("room-b", "Tim")
	fresh, ok := reopened.meetings.activeRecord("room-b")
	if !ok {
		t.Fatal("room-b admission after restart did not open a record")
	}
	if fresh.ID == bRecord.ID {
		t.Fatalf("room-b admission re-minted the ended meeting id %q", bRecord.ID)
	}
}
