package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
// after the room has been empty for a few minutes. The 4-minute default IS the
// rule; the env override stays.
func TestMeetingIdleEndGraceDefaultsToAFewMinutes(t *testing.T) {
	t.Setenv("MEETING_IDLE_END_GRACE", "")
	if got := meetingIdleEndGrace(); got != 4*time.Minute {
		t.Fatalf("meetingIdleEndGrace()=%v, want 4m (the founder's few-minutes sitting boundary)", got)
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
	app.forgetParticipant("AJ")

	if _, err := app.archiveMeeting("AJ"); err != nil {
		t.Fatalf("archiveMeeting: %v", err)
	}
	if record, ok := app.meetings.activeRecord(officeRoomID); ok {
		t.Fatalf("record=%#v, want no successor for an empty room", record)
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

func TestMeetingStorePersistsCapsAndToleratesBadFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meetings.json")

	store, err := loadMeetingStore(path)
	if err != nil {
		t.Fatalf("loadMeetingStore missing file: %v", err)
	}
	total := meetingStoreCap + 5
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
	if len(records) != meetingStoreCap {
		t.Fatalf("reloaded=%d, want cap %d", len(records), meetingStoreCap)
	}
	if records[0].ID != fmt.Sprintf("meeting-%03d", total-1) {
		t.Fatalf("newest=%q, want newest-first ordering", records[0].ID)
	}
	if oldest := records[len(records)-1]; oldest.ID != "meeting-005" {
		t.Fatalf("oldest survivor=%q, want meeting-005 (cap drops oldest)", oldest.ID)
	}
	if records[0].EndedReason != meetingEndedReasonIdle || len(records[0].Participants) != 1 {
		t.Fatalf("record=%#v, want ended record with participants intact", records[0])
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

	cardResult, _, err := kanbanApp.applyToolCallArgs("create_ticket", map[string]any{"title": "Add bandwidth estimation probe"})
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

/* ---------- Track-2 Wave 4: the idle-close boundary flush ---------- */

// The idle-close hole: before this wave, endMeetingForIdle rotated the id
// without any final rollup, so idle-closed meetings never got a digest and
// "what did I miss" silently skipped them. Now the close chain runs BEFORE the
// rotation, so the tail brain, the meeting digest, the day fold, the ledger
// consolidation, and the company digest all key to the CLOSING meeting id —
// and the auto-archive that follows embeds them.
func TestEndMeetingForIdleFlushesRollupChainBeforeRotation(t *testing.T) {
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
		case strings.Contains(request.Instructions, "meeting digest compiler"):
			return cannedMeetingDigestJSON(), nil
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

	// the flush ran BEFORE rotation: every rollup keys to the CLOSED meeting.
	brains := app.memory.entriesOfKind(meetingMemoryKindBrain, 0)
	if len(brains) != 1 || strings.TrimSpace(brains[0].Metadata["meetingId"]) != closedID {
		t.Fatalf("brains=%d meetingId=%q, want one final brain keyed to %s", len(brains), brains[0].Metadata["meetingId"], closedID)
	}
	digest, ok := app.memory.latestDigestPerMeeting()[closedID]
	if !ok {
		t.Fatalf("no meeting_digest for the idle-closed meeting %s", closedID)
	}
	if got := strings.TrimSpace(digest.Metadata["meetingId"]); got != closedID {
		t.Fatalf("digest meetingId=%q, want the closing id %s (pre-rotation flush)", got, closedID)
	}
	if entries := app.memory.entriesOfKind(meetingMemoryKindDayDigest, 0); len(entries) == 0 {
		t.Fatal("idle flush did not fold a day digest")
	}
	decisions := ledgerRecordsOfEntity(app.memory.ledgerState(), ledgerEntityDecision)
	if len(decisions) == 0 {
		t.Fatal("idle flush did not consolidate the digest facts into the ledger")
	}
	company, ok := app.memory.latestCompanyDigest()
	if !ok {
		t.Fatal("idle flush did not refresh the company digest")
	}
	if payload, parsed := parseCompanyDigest(company.Text); !parsed || payload.Narrative == "" || len(payload.State.Decisions) == 0 {
		t.Fatalf("company digest = %s, want state + narrative", company.Text)
	}

	// liveness: the id rotated and the silent auto-archive still landed,
	// pinned to the closed meeting.
	if got := app.memory.currentMeetingID(officeRoomID); got != "" {
		t.Fatalf("meeting id %q not rotated after the flush", got)
	}
	archives := app.memory.entriesOfKind(meetingMemoryKindArchive, 0)
	if len(archives) != 1 || strings.TrimSpace(archives[0].Metadata["meetingId"]) != closedID {
		t.Fatalf("archives=%d, want one idle auto-archive pinned to %s", len(archives), closedID)
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
		t.Fatalf("meeting id %q not rotated after a failed flush", got)
	}
	if digests := app.memory.entriesOfKind(meetingMemoryKindMeetingDigest, 0); len(digests) != 0 {
		t.Fatalf("digests=%d, want none persisted from a failed flush", len(digests))
	}
	archives := app.memory.entriesOfKind(meetingMemoryKindArchive, 0)
	if len(archives) != 1 || strings.TrimSpace(archives[0].Metadata["meetingId"]) != closedID {
		t.Fatalf("archives=%d, want the silent auto-archive despite the model failure", len(archives))
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
