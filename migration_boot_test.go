package main

// Multi-room W7 migration dress rehearsal (§9.9, graft from Design B): boot
// the app against a PROD-SHAPED data/ directory — meeting-memory.jsonl with
// no roomId stamps, a meetings.json written by the pre-room binary, a legacy
// sessions.json row, and NO rooms.json — and prove the §9 invariants: the
// office is seeded, every historical record resolves to the office, the
// office agent cursors resume exactly where they left off, new rooms baseline
// at now, and no member is logged out.

import (
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

// writeProdShapedFixtures lays down the data directory exactly as the
// pre-room binary would have left it. Every shape here is deliberately
// roomId-free.
func writeProdShapedFixtures(t *testing.T, dir string) {
	t.Helper()

	now := time.Now().UTC()
	entries := []meetingMemoryEntry{
		{
			ID: "legacy-ts-1", Kind: meetingMemoryKindTranscript,
			Text:      "AJ: we agreed to ship the Samsung deck on Friday.",
			CreatedAt: now.Add(-3 * time.Hour),
			Metadata:  map[string]string{"meetingId": "meeting-legacy-A", "speaker": "AJ"},
		},
		{
			ID: "legacy-brain-1", Kind: meetingMemoryKindBrain,
			Text:      "## Overview\nThe Samsung deck ships Friday.",
			CreatedAt: now.Add(-170 * time.Minute),
			Metadata: map[string]string{
				"meetingId": "meeting-legacy-A",
				// the pre-room brain cursor: consumed through legacy-ts-1.
				meetingBrainAgent().cursorMetadataKey: "legacy-ts-1",
			},
		},
		{
			ID: "legacy-archive-1", Kind: meetingMemoryKindArchive,
			Text:      "Archived meeting meeting-archive-legacy with 1 transcript item(s).",
			CreatedAt: now.Add(-160 * time.Minute),
			Metadata:  map[string]string{"meetingId": "meeting-legacy-A", "archiveId": "meeting-archive-legacy"},
		},
		{
			ID: "legacy-ts-2", Kind: meetingMemoryKindTranscript,
			Text:      "Tim: picking the thread back up on distribution.",
			CreatedAt: now.Add(-10 * time.Minute),
			Metadata:  map[string]string{"meetingId": "meeting-legacy-B", "speaker": "Tim"},
		},
	}
	var jsonl strings.Builder
	for _, entry := range entries {
		raw, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("marshal fixture entry %s: %v", entry.ID, err)
		}
		jsonl.Write(raw)
		jsonl.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "memory.jsonl"), []byte(jsonl.String()), 0o600); err != nil {
		t.Fatalf("write memory fixture: %v", err)
	}

	// meetings.json as the pre-room binary wrote it: no roomId, no listenOnly.
	meetings := meetingStoreState{
		Meetings: []meetingRecord{
			{
				ID:           "meeting-legacy-A",
				StartedAt:    now.Add(-3 * time.Hour).Format(time.RFC3339Nano),
				EndedAt:      now.Add(-160 * time.Minute).Format(time.RFC3339Nano),
				EndedReason:  meetingEndedReasonIdle,
				ArchiveID:    "meeting-archive-legacy",
				Participants: []string{"AJ"},
			},
			{
				ID:           "meeting-legacy-B",
				StartedAt:    now.Add(-15 * time.Minute).Format(time.RFC3339Nano),
				Participants: []string{"Tim"},
			},
		},
		UpdatedAt: now.Format(time.RFC3339Nano),
	}
	rawMeetings, err := json.MarshalIndent(meetings, "", "  ")
	if err != nil {
		t.Fatalf("marshal meetings fixture: %v", err)
	}
	if strings.Contains(string(rawMeetings), "roomId") || strings.Contains(string(rawMeetings), "listenOnly") {
		t.Fatalf("fixture must stay byte-compatible with the pre-room shape:\n%s", rawMeetings)
	}
	if err := os.WriteFile(filepath.Join(dir, "meetings.json"), rawMeetings, 0o600); err != nil {
		t.Fatalf("write meetings fixture: %v", err)
	}
}

func TestMigrationDressRehearsalProdShapedBoot(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
	t.Setenv("BONFIRE_USERS_PATH", filepath.Join(dir, "users.json"))
	sessionsPath := filepath.Join(dir, "sessions.json")
	t.Setenv("BONFIRE_SESSIONS_PATH", sessionsPath)
	roomsPath := filepath.Join(dir, "rooms.json")
	t.Setenv("BONFIRE_ROOMS_PATH", roomsPath)
	resetAuthRateLimitersForTest()

	writeProdShapedFixtures(t, dir)

	// A legacy member session row, persisted before the Kind field existed.
	legacyToken := strings.Repeat("ab", 32)
	legacyRow := fmt.Sprintf(`{%q: {"email":"aj@shareability.com","expires":%q}}`,
		hashResetToken(legacyToken), time.Now().Add(24*time.Hour).Format(time.RFC3339Nano))
	if err := os.WriteFile(sessionsPath, []byte(legacyRow), 0o600); err != nil {
		t.Fatalf("write legacy sessions.json: %v", err)
	}
	if _, err := os.Stat(roomsPath); !os.IsNotExist(err) {
		t.Fatalf("fixture precondition: rooms.json must not exist yet (err=%v)", err)
	}

	// ---- boot, exactly the main() order: app (memory+meetings+reconcile),
	// then the room-store seed.
	app := newKanbanBoardApp()
	store := appRoomStore()

	// §9.1 office seed: rooms.json now exists with the office room only —
	// no passcode, guests off, unarchived. One-click join preserved.
	if _, err := os.Stat(roomsPath); err != nil {
		t.Fatalf("boot must seed rooms.json: %v", err)
	}
	rooms := store.list()
	if len(rooms) != 1 || rooms[0].ID != officeRoomID {
		t.Fatalf("rooms after seed = %+v, want the office only", rooms)
	}
	if rooms[0].PasscodeHash != "" || rooms[0].GuestEnabled || rooms[0].Archived {
		t.Fatalf("office seed must preserve one-click defaults, got %+v", rooms[0])
	}

	// §9.2 boot resume: the newest roomId-less non-bookkeeping entry resumes
	// the OFFICE's in-flight meeting; no phantom named rooms appear.
	if got := app.memory.currentMeetingID(officeRoomID); got != "meeting-legacy-B" {
		t.Fatalf("office resumed meeting id = %q, want meeting-legacy-B", got)
	}
	if roomIDs := app.memory.meetingRoomIDs(); len(roomIDs) != 1 || roomIDs[0] != officeRoomID {
		t.Fatalf("resumed meeting rooms = %v, want [office]", roomIDs)
	}

	// §9.3 records: the open legacy record survives reconcile (its id matches
	// the resumed memory id) and both records resolve to the office.
	active, ok := app.meetings.activeRecord(officeRoomID)
	if !ok || active.ID != "meeting-legacy-B" {
		t.Fatalf("active office record = %+v ok=%v, want the open legacy meeting", active, ok)
	}
	if active.ListenOnly {
		t.Fatal("legacy records must read as full mode")
	}
	closed, ok := app.meetings.recordByID("meeting-legacy-A")
	if !ok || closed.EndedAt == "" || closed.ArchiveID != "meeting-archive-legacy" {
		t.Fatalf("closed legacy record = %+v ok=%v", closed, ok)
	}
	for _, record := range []meetingRecord{active, closed} {
		if meetingRoomID(record) != officeRoomID {
			t.Fatalf("legacy record %s resolves to room %q, want office", record.ID, meetingRoomID(record))
		}
	}

	// Historical recall intact: the archived meeting's transcript is still
	// addressable by its meeting id (nothing was rewritten).
	recalled := app.memory.snapshotForMeeting("meeting-legacy-A", 0)
	foundTranscript := false
	for _, entry := range recalled {
		if entry.ID == "legacy-ts-1" {
			foundTranscript = true
		}
	}
	if !foundTranscript {
		t.Fatalf("legacy meeting recall lost its transcript: %+v", recalled)
	}

	// §9.5 cursors: the roomId-less brain artifact IS the office cursor — the
	// office pipeline resumes AFTER it (only the new transcript is unconsumed)
	// instead of re-summarizing history.
	brain := meetingBrainAgent()
	unconsumed := app.memory.unconsumedEntriesAfterForRoom(brain.inputKind, brain.artifactKind, brain.cursorMetadataKey, 10, "", officeRoomID)
	if len(unconsumed) != 1 || unconsumed[0].ID != "legacy-ts-2" {
		t.Fatalf("office unconsumed window = %+v, want exactly the post-cursor transcript", unconsumed)
	}
	if got := app.memory.bootBaselineIDOfKindForRoom(meetingMemoryKindBrain, officeRoomID); got != "legacy-brain-1" {
		t.Fatalf("office brain boot baseline = %q, want the legacy artifact", got)
	}
	// A room born after this boot has no pre-boot history: baseline-at-now,
	// and its window never picks up the office's legacy entries.
	if got := app.memory.bootBaselineIDOfKindForRoom(meetingMemoryKindBrain, "room-newborn"); got != "" {
		t.Fatalf("new room boot baseline = %q, want empty (baseline-at-now)", got)
	}
	if leaked := app.memory.unconsumedEntriesAfterForRoom(brain.inputKind, brain.artifactKind, brain.cursorMetadataKey, 10, "", "room-newborn"); len(leaked) != 0 {
		t.Fatalf("a new room's window consumed office history: %+v", leaked)
	}

	// §9.4 zero logouts: the legacy session row resolves as a member session
	// after boot…
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: legacyToken})
	if user := userFromRequest(req); user == nil || user.Email != "aj@shareability.com" {
		t.Fatalf("legacy session must survive the deploy, got %+v", user)
	}
	// …and survives the first post-deploy WRITE to sessions.json (a guest
	// session landing in the same store must not strip or retype legacy rows).
	if _, err := userSessionStore().createGuest(officeRoomID, "Nia"); err != nil {
		t.Fatalf("create guest session: %v", err)
	}
	reloaded := newSessionStore(sessionsPath)
	record, ok := reloaded.lookupRecord(legacyToken)
	if !ok || record.Kind != "" || record.Email != "aj@shareability.com" {
		t.Fatalf("legacy row after rewrite = %+v ok=%v, want an untyped member row", record, ok)
	}
}
