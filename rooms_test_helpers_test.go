package main

import (
	"path/filepath"
	"testing"
	"time"
)

// installNamedRoomIDsForTest gives low-level admission tests the same durable
// room authority production requires. These tests intentionally construct the
// app beneath the HTTP room-creation layer, so their fixture must register any
// synthetic room IDs before exercising anchored admission.
func installNamedRoomIDsForTest(t *testing.T, roomIDs ...string) {
	t.Helper()
	t.Setenv("BONFIRE_ROOMS_PATH", filepath.Join(t.TempDir(), "rooms.json"))
	store := appRoomStore()
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, roomID := range roomIDs {
		roomID = normalizeRoomID(roomID)
		if _, found := store.roomByIDLocked(roomID); found {
			continue
		}
		store.rooms = append(store.rooms, roomRecord{ID: roomID, Name: roomID, CreatedBy: "test", CreatedAt: time.Now().UTC(), GuestEnabled: true})
	}
}
