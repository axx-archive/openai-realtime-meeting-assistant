package main

// Rooms UX RW4 (docs/plans/rooms-ux-2026-07-09.md §3.7): archiving a room must
// never maroon its occupants in a half-dead room. The archive verb broadcasts
// a room-scoped room_closed (guest-allowlisted — guests are exactly who must
// hear it), forgets the room's presence, and ends the sitting through the
// same close chain as idle end.

import (
	"path/filepath"
	"testing"
	"time"
)

// room_closed rides the room-scoped fan-out that reaches guest sockets, so it
// must be on the §6.2 write-time allowlist or the writer drops it.
func TestGuestWriteAllowlistIncludesRoomClosed(t *testing.T) {
	if !guestWritableKanbanEvents["room_closed"] {
		t.Fatal("room_closed must be on the guest write allowlist — guests seated in an archived room are exactly who must hear it")
	}
}

// closeRoomForArchive ends the sitting unconditionally: the open meeting
// record closes with reason room_closed, the room's seats are forgotten, and
// the memory meeting id rotates so the record lifecycle stays aligned.
func TestCloseRoomForArchiveEndsSittingAndForgetsSeats(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("BONFIRE_ROOMS_PATH", filepath.Join(t.TempDir(), "rooms.json"))
	app := newIsolatedKanbanBoardApp(t)

	room, err := appRoomStore().create("war room", "", "aj@shareability.com", false)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, _, err := app.admitParticipantSessionEndpointInRoom(room.ID, "AJ", "sess-1", "ep-1"); err != nil {
		t.Fatalf("admit participant: %v", err)
	}
	app.noteMeetingAdmission(room.ID, "AJ")
	record, ok := app.meetings.activeRecord(room.ID)
	if !ok {
		t.Fatal("admission did not open a meeting record")
	}

	if err := appRoomStore().archive(room.ID); err != nil {
		t.Fatalf("archive room: %v", err)
	}
	app.closeRoomForArchive(room.ID)

	if _, still := app.meetings.activeRecord(room.ID); still {
		t.Fatal("archive close must end the room's active meeting record")
	}
	app.meetings.mu.Lock()
	reason := ""
	for _, stored := range app.meetings.records {
		if stored.ID == record.ID {
			reason = stored.EndedReason
		}
	}
	app.meetings.mu.Unlock()
	if reason != meetingEndedReasonRoomClosed {
		t.Fatalf("ended reason=%q, want %q", reason, meetingEndedReasonRoomClosed)
	}
	if got := app.activeParticipantCount(room.ID); got != 0 {
		t.Fatalf("archive close left %d seats occupied, want 0", got)
	}
	if app.memory.currentMeetingID(room.ID) == record.ID {
		t.Fatal("archive close must rotate the room's memory meeting id off the closed record")
	}
}

// Restore is an undo. The close chain runs async after the archive response,
// so a restore can land in the gap — when it does, the close must no-op:
// occupants keep their seats and the sitting stays open.
func TestCloseRoomForArchiveNoopsAfterRestore(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("BONFIRE_ROOMS_PATH", filepath.Join(t.TempDir(), "rooms.json"))
	app := newIsolatedKanbanBoardApp(t)

	room, err := appRoomStore().create("war room", "", "aj@shareability.com", false)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, _, err := app.admitParticipantSessionEndpointInRoom(room.ID, "AJ", "sess-1", "ep-1"); err != nil {
		t.Fatalf("admit participant: %v", err)
	}
	app.noteMeetingAdmission(room.ID, "AJ")
	if _, ok := app.meetings.activeRecord(room.ID); !ok {
		t.Fatal("admission did not open a meeting record")
	}

	if err := appRoomStore().archive(room.ID); err != nil {
		t.Fatalf("archive room: %v", err)
	}
	if err := appRoomStore().restore(room.ID); err != nil {
		t.Fatalf("restore room: %v", err)
	}
	app.closeRoomForArchive(room.ID)

	if _, ok := app.meetings.activeRecord(room.ID); !ok {
		t.Fatal("close chain must no-op after a restore — the sitting was ended")
	}
	if got := app.activeParticipantCount(room.ID); got != 1 {
		t.Fatalf("close chain after restore forgot seats: %d occupied, want 1", got)
	}
}

// The office is room zero: the store already refuses to archive it, and the
// close seam must refuse to end its sitting even if called directly.
func TestCloseRoomForArchiveRefusesOffice(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("BONFIRE_ROOMS_PATH", filepath.Join(t.TempDir(), "rooms.json"))
	app := newIsolatedKanbanBoardApp(t)

	app.noteMeetingAdmission(officeRoomID, "AJ")
	app.closeRoomForArchive(officeRoomID)

	if _, ok := app.meetings.activeRecord(officeRoomID); !ok {
		t.Fatal("closeRoomForArchive must never end the office sitting")
	}
}

// The HTTP archive verb triggers the close chain (asynchronously — the flush
// can take a while and the archive itself is already durable), so a seated
// room's record ends without any client cooperating.
func TestArchiveHandlerRunsRoomCloseChain(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	cookies := setupRoomsTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	room, err := appRoomStore().create("deal room", "", "aj@shareability.com", false)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, _, err := kanbanApp.admitParticipantSessionEndpointInRoom(room.ID, "AJ", "sess-1", "ep-1"); err != nil {
		t.Fatalf("admit participant: %v", err)
	}
	kanbanApp.noteMeetingAdmission(room.ID, "AJ")
	if _, ok := kanbanApp.meetings.activeRecord(room.ID); !ok {
		t.Fatal("admission did not open a meeting record")
	}

	recorder := doRoomsRequest(t, roomActionHandler, "POST", "/rooms/"+room.ID+"/archive", "{}", cookies)
	if recorder.Code != 200 {
		t.Fatalf("archive returned %d: %s", recorder.Code, recorder.Body.String())
	}

	// the close runs on its own goroutine — poll, never sleep-and-hope
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, ok := kanbanApp.meetings.activeRecord(room.ID); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the archive verb did not end the room's sitting within 3s")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := kanbanApp.activeParticipantCount(room.ID); got != 0 {
		t.Fatalf("archive left %d seats occupied, want 0", got)
	}
}
