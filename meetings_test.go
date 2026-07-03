package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fireIdleEndNow invokes the idle-end seam exactly as a live grace timer
// would: with the generation the timer captured at arm time.
func fireIdleEndNow(app *kanbanBoardApp) {
	app.meetings.mu.Lock()
	generation := app.meetings.idleGeneration
	app.meetings.mu.Unlock()
	app.endMeetingForIdle(generation)
}

func TestMeetingAdmissionOpensRecordAlignedWithMemoryID(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	app.noteMeetingAdmission("AJ")

	record, ok := app.meetings.activeRecord()
	if !ok {
		t.Fatal("admission did not open a meeting record")
	}
	if record.ID == "" || record.ID != app.memory.ensureMeetingID() {
		t.Fatalf("record id=%q, want the memory store's meeting id %q", record.ID, app.memory.ensureMeetingID())
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
	app.noteMeetingAdmission("Tim")
	second, ok := app.meetings.activeRecord()
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
	app.noteMeetingAdmission("AJ")
	open, ok := app.meetings.activeRecord()
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

	closed, active := app.meetings.activeRecord()
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
	app.noteMeetingAdmission("AJ")
	endedID := app.memory.currentMeetingID()
	if _, _, err := app.memory.appendTranscript("event-1", "item-1", "Boot Barn kickoff planning notes."); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	app.forgetParticipant("AJ")
	fireIdleEndNow(app) // closes the record; the rotation is in-process only

	// routine deploy: the process restarts and the memory store resumes from
	// the JSONL tail, which is the ENDED meeting's transcript.
	reopened := newKanbanBoardApp()
	if got := reopened.memory.currentMeetingID(); got == endedID {
		t.Fatalf("boot resumed the ended meeting id %q; reconciliation must rotate it", got)
	}

	// next day's join: a FRESH id and record, never a duplicate.
	reopened.noteMeetingAdmission("AJ")
	record, ok := reopened.meetings.activeRecord()
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
	app.noteMeetingAdmission("AJ")
	open, ok := app.meetings.activeRecord()
	if !ok {
		t.Fatal("no open record after admission")
	}
	app.forgetParticipant("AJ")
	app.noteMeetingOccupancy() // arms the timer
	app.meetings.mu.Lock()
	armTimeGeneration := app.meetings.idleGeneration
	app.meetings.mu.Unlock()

	// the timer fires and passes its occupancy check; the admission lands in
	// that window — cancelIdleEnd bumps the generation even though the timer
	// can no longer be stopped.
	app.noteMeetingAdmission("AJ")

	// the fired timer's close is a no-op against the stale generation.
	app.endMeetingForIdle(armTimeGeneration)

	record, ok := app.meetings.activeRecord()
	if !ok || record.ID != open.ID || record.EndedAt != "" {
		t.Fatalf("record=%#v, want meeting %q still open", record, open.ID)
	}
	if got := app.memory.currentMeetingID(); got != open.ID {
		t.Fatalf("memory id=%q, want %q un-rotated", got, open.ID)
	}

	// a genuinely empty room still idle-ends with the live generation.
	app.noteMeetingOccupancy()
	fireIdleEndNow(app)
	if record, stillOpen := app.meetings.activeRecord(); stillOpen {
		t.Fatalf("record=%#v, want the fresh-generation idle end to close it", record)
	}
}

// The other half of the idle race: the fire's endMeeting landed but its
// rotation has not — the admission must mint a FRESH id (never reopen the
// ended one), and the closer's conditional rotation must not clobber it.
func TestAdmissionNeverReMintsEndedMeetingID(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.noteMeetingAdmission("AJ")
	endedID := app.memory.currentMeetingID()
	if _, changed := app.meetings.endMeeting(endedID, time.Now().UTC(), meetingEndedReasonIdle, ""); !changed {
		t.Fatal("endMeeting did not close the record")
	}

	app.noteMeetingAdmission("Tim")
	record, ok := app.meetings.activeRecord()
	if !ok {
		t.Fatal("admission did not open a record")
	}
	if record.ID == endedID {
		t.Fatalf("admission re-minted the ended id %q", endedID)
	}
	if got := app.memory.currentMeetingID(); got != record.ID {
		t.Fatalf("memory id=%q, want aligned with the new record %q", got, record.ID)
	}

	// the racing closer's rotation arrives last: conditional, so the fresh id
	// survives.
	app.memory.rotateMeetingIDIfCurrent(endedID)
	if got := app.memory.currentMeetingID(); got != record.ID {
		t.Fatalf("memory id=%q after the stale rotation, want %q intact", got, record.ID)
	}
}

func TestRejoinWithinGraceCancelsIdleEnd(t *testing.T) {
	t.Setenv("MEETING_IDLE_END_GRACE", "1h")
	app := newIsolatedKanbanBoardApp(t)
	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("admitParticipant: %v", err)
	}
	app.noteMeetingAdmission("AJ")
	open, ok := app.meetings.activeRecord()
	if !ok {
		t.Fatal("no open record after admission")
	}

	app.forgetParticipant("AJ")
	app.noteMeetingOccupancy()
	app.meetings.mu.Lock()
	armed := app.meetings.idleTimer != nil
	app.meetings.mu.Unlock()
	if !armed {
		t.Fatal("last leave did not arm the idle-end timer")
	}

	// a rejoin inside the grace window cancels the pending idle end.
	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("re-admit: %v", err)
	}
	app.noteMeetingAdmission("AJ")
	app.meetings.mu.Lock()
	stillArmed := app.meetings.idleTimer != nil
	app.meetings.mu.Unlock()
	if stillArmed {
		t.Fatal("rejoin did not cancel the idle-end timer")
	}
	record, ok := app.meetings.activeRecord()
	if !ok || record.ID != open.ID || record.EndedAt != "" {
		t.Fatalf("record=%#v, want the same meeting still open", record)
	}

	// occupancy check: a non-empty room never arms the timer.
	app.noteMeetingOccupancy()
	app.meetings.mu.Lock()
	armedWhileOccupied := app.meetings.idleTimer != nil
	app.meetings.mu.Unlock()
	if armedWhileOccupied {
		t.Fatal("noteMeetingOccupancy armed the timer while the room is occupied")
	}
}

func TestArchiveMeetingClosesRecordEmbedsItAndOpensSuccessor(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("admitParticipant: %v", err)
	}
	app.noteMeetingAdmission("AJ")
	open, ok := app.meetings.activeRecord()
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
	successor, ok := app.meetings.activeRecord()
	if !ok {
		t.Fatal("mid-occupancy archive left no active record")
	}
	if successor.ID == open.ID {
		t.Fatalf("successor id=%q, want a new meeting id", successor.ID)
	}
	if successor.ID != app.memory.currentMeetingID() {
		t.Fatalf("successor id=%q, want the rotated memory id %q", successor.ID, app.memory.currentMeetingID())
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
	app.noteMeetingAdmission("AJ")
	meetingID := app.memory.currentMeetingID()
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
	record, ok := app.meetings.activeRecord()
	if !ok || record.ID != meetingID || record.EndedAt != "" {
		t.Fatalf("record=%#v, want meeting %q still open after the failed write", record, meetingID)
	}
	if got := app.memory.currentMeetingID(); got != meetingID {
		t.Fatalf("memory id=%q, want %q un-rotated after the failed write", got, meetingID)
	}
	if app.meetingSnapshot() == nil {
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
	app.noteMeetingAdmission("AJ")
	if _, _, err := app.memory.appendTranscript("event-1", "item-1", "Boot Barn kickoff planning notes."); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	app.forgetParticipant("AJ")

	if _, err := app.archiveMeeting("AJ"); err != nil {
		t.Fatalf("archiveMeeting: %v", err)
	}
	if record, ok := app.meetings.activeRecord(); ok {
		t.Fatalf("record=%#v, want no successor for an empty room", record)
	}
}

func TestSetAutoTitleFromMissionInsight(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.noteMeetingAdmission("AJ")
	meetingID := app.memory.currentMeetingID()

	insight := missionInsightPayload{Themes: []missionInsightTheme{
		{Label: "buyer proof", Mentions: 2},
		{Label: "Realtime as UI", Mentions: 5},
		{Label: "late tie", Mentions: 5},
	}}
	if got := dominantMissionTheme(insight); got != "Realtime as UI" {
		t.Fatalf("dominantMissionTheme=%q, want max mentions with first-wins ties", got)
	}
	if got := dominantMissionTheme(missionInsightPayload{}); got != "" {
		t.Fatalf("dominantMissionTheme(empty)=%q, want empty", got)
	}

	record, changed := app.meetings.setAutoTitle(meetingID, dominantMissionTheme(insight))
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
	if active, ok := app.meetings.activeRecord(); !ok || active.Title != "Realtime as UI" {
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
		if _, changed := store.startMeeting(id, startedAt.Add(time.Duration(index)*time.Minute), []string{"AJ"}); !changed {
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
	if _, ok := nilStore.activeRecord(); ok {
		t.Fatal("nil store reported an active record")
	}
	if _, changed := nilStore.startMeeting("meeting-x", time.Now(), nil); changed {
		t.Fatal("nil store accepted a start")
	}
	if got := nilStore.recent(5); len(got) != 0 {
		t.Fatalf("nil store recent=%v, want empty", got)
	}
	nilStore.armIdleEnd(func(uint64) {})
	nilStore.cancelIdleEnd()
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
	if record, ok := app.meetings.activeRecord(); ok {
		t.Fatalf("record=%#v, want the stale record closed at boot", record)
	}
	records := app.meetings.recent(1)
	if len(records) != 1 || records[0].EndedReason != meetingEndedReasonRestart || records[0].EndedAt == "" {
		t.Fatalf("records=%#v, want the stale record ended with reason restart", records)
	}
}

func TestBootReconciliationResumesMatchingOpenRecord(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.noteMeetingAdmission("AJ")
	if _, _, err := app.memory.appendTranscript("event-1", "item-1", "Boot Barn kickoff planning notes."); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	meetingID := app.memory.currentMeetingID()

	// a restart resumes the same in-flight meeting: the record stays open and
	// the idle timer arms (a join within the grace window cancels it).
	reopened := newKanbanBoardApp()
	record, ok := reopened.meetings.activeRecord()
	if !ok || record.ID != meetingID {
		t.Fatalf("record=%#v, want the resumed open meeting %q", record, meetingID)
	}
	reopened.meetings.mu.Lock()
	armed := reopened.meetings.idleTimer != nil
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
	kanbanApp.meetings.startMeeting("meeting-20260630-first", earlier, []string{"AJ"})
	kanbanApp.meetings.endMeeting("meeting-20260630-first", earlier.Add(45*time.Minute), meetingEndedReasonArchive, "meeting-archive-1")
	kanbanApp.meetings.startMeeting("meeting-20260701-second", earlier.Add(24*time.Hour), []string{"Tim"})

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
	kanbanApp.meetings.startMeeting(meetingID, started, []string{"AJ", "Tim"})

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
	app.meetings.startMeeting("meeting-hist-1", time.Now().UTC(), []string{"AJ"})

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
