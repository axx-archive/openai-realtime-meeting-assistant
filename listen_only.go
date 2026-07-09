package main

// Listen-only pipeline policy (multi-room W4, docs/plans/multi-room-2026-07-08.md §7).
//
// A sitting is LISTEN-ONLY when its room is exposed to guests: the room holds
// any unexpired, unrevoked guest link when the sitting starts, or a guest is
// admitted mid-sitting. The latch lives on the meeting record
// (meetingRecord.ListenOnly) and NEVER unlatches within the sitting — a guest
// leaving (or links being revoked) mid-meeting must not let proactive workers
// act on a window guests contributed to. Revoking every link returns the NEXT
// sitting to full mode.
//
// What keeps running in listen-only (§7.2 — the record-building tier):
// transcription + attribution, the brain worker (subject to the §6.5
// guests-only deferral), meeting record + mission-intel auto-title, narrative
// maintainer, decision ledger, meeting digest, recap, the close-flush chain,
// and auto-archive. What is suppressed (§7.3, three independent layers): the
// board worker's and suggestion agent's analysis windows (filterListenOnly —
// cursor advances past excluded entries), proposeCodexTask / board-analysis /
// workflow-ticker choke points, and the never-started tier (no Scout realtime
// peer, no wake pulse, realtime-offer/-tool refusal).
//
// §6.4 rollup inclusion (RATIFIED by AJ 2026-07-09): listen-only-sitting
// content is provenance-stamped (metadata.listenOnly=true on brains/digests)
// and INCLUDED in the company-global rollups (day digest, entity ledger,
// company digest, reflection) exactly like member-only material — the whole
// point of running guests through external rooms is that the meeting's memory
// reaches the brain, so Scout can answer about it company-wide. The accepted
// residual (guest-spoken content reaching the full-mode office recall
// surface) is documented in the plan; re-quarantining is a read-side filter
// keyed on the stamps, which stay at write time for exactly that reason.

import (
	"strings"
	"time"
)

// listenOnlyMetadataKey is the provenance stamp §6.4 puts on brains and
// digests born of a listen-only sitting; the meeting record's latch stays the
// source of truth for entries without it. Rollups no longer filter on it
// (inclusion ratified 2026-07-09) — it is the durable origin record recall
// surfaces can display and a re-quarantine toggle would key on.
const listenOnlyMetadataKey = "listenOnly"

// hasActiveGuestLink reports whether the room holds any unexpired, unrevoked
// guest link — the §7.1 "guest-enabled" input to the latch. Computed from the
// links themselves (not the sticky GuestEnabled flag) so revoking every link
// returns the next sitting to full mode.
func (s *roomStore) hasActiveGuestLink(roomID string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index, ok := s.roomByIDLocked(normalizeRoomID(roomID))
	if !ok {
		return false
	}
	now := time.Now().UTC()
	for _, link := range s.rooms[index].GuestLinks {
		if !link.Revoked && link.Expires.After(now) {
			return true
		}
	}
	return false
}

// roomListenOnly is the LIVE §7.1 check: the room is guest-enabled (holds an
// active guest link) or has guests seated right now. Used to SET the latch at
// admission; workers over historical windows use meetingListenOnly instead.
func (app *kanbanBoardApp) roomListenOnly(roomID string) bool {
	if app == nil {
		return false
	}
	roomID = normalizeRoomID(roomID)
	app.mu.Lock()
	guestsPresent := len(app.roomLiveLocked(roomID).guestSeats) > 0
	app.mu.Unlock()
	if guestsPresent {
		return true
	}
	// Only an already-open room store is consulted (the
	// sweepExpiredGuestLinksIfOpen idiom): boot opens it in production, and a
	// data directory without one holds no links to begin with.
	roomStoreMu.Lock()
	store := roomStoreCache[roomsFilePath()]
	roomStoreMu.Unlock()
	return store.hasActiveGuestLink(roomID)
}

// meetingListenOnly is the record lookup workers use over historical windows:
// whether the meeting's sitting latched listen-only. Unknown ids are full
// mode (legacy records predate the field and read false).
func (app *kanbanBoardApp) meetingListenOnly(meetingID string) bool {
	if app == nil || app.meetings == nil {
		return false
	}
	meetingID = strings.TrimSpace(meetingID)
	if meetingID == "" {
		return false
	}
	record, ok := app.meetings.recordByID(meetingID)
	return ok && record.ListenOnly
}

// sittingListenOnly answers "is the room's CURRENT sitting listen-only?" for
// the live enforcement seams (wake pulse, realtime offer/tool, proposal
// choke points): the active record's latch when one is open, else the live
// room state — so enforcement holds even before the first admission opens a
// record.
func (app *kanbanBoardApp) sittingListenOnly(roomID string) bool {
	if app == nil {
		return false
	}
	roomID = normalizeRoomID(roomID)
	if app.meetings != nil {
		if record, ok := app.meetings.activeRecord(roomID); ok && record.ListenOnly {
			return true
		}
	}
	return app.roomListenOnly(roomID)
}

// entryFromListenOnlySitting resolves one memory entry's provenance: the
// cheap §6.4 stamp when present, else the meeting record its meetingId points
// at. Entries with neither (legacy, ambient bookkeeping) are full mode.
func (app *kanbanBoardApp) entryFromListenOnlySitting(entry meetingMemoryEntry) bool {
	if strings.EqualFold(strings.TrimSpace(entry.Metadata[listenOnlyMetadataKey]), "true") {
		return true
	}
	return app.meetingListenOnly(entry.Metadata["meetingId"])
}

// filterListenOnly is the shared §7.3 layer-1 window filter for the two
// proactive workers (board worker, suggestion agent): entries from
// listen-only sittings are EXCLUDED from the analysis window while the
// caller's cursor/baseline still advances past them (skip-while-advancing).
// Muting nudges alone is NOT enough — the board worker's ticker floor still
// fires — so the filter lives at window selection.
func (app *kanbanBoardApp) filterListenOnly(entries []meetingMemoryEntry) ([]meetingMemoryEntry, int) {
	kept := make([]meetingMemoryEntry, 0, len(entries))
	dropped := 0
	for _, entry := range entries {
		if app.entryFromListenOnlySitting(entry) {
			dropped++
			continue
		}
		kept = append(kept, entry)
	}
	return kept, dropped
}

// windowIncludesListenOnly reports whether any entry of an input window came
// from a listen-only sitting — the §6.4 provenance stamp derived artifacts
// (brains, digests) inherit at write time.
func (app *kanbanBoardApp) windowIncludesListenOnly(entries []meetingMemoryEntry) bool {
	for _, entry := range entries {
		if app.entryFromListenOnlySitting(entry) {
			return true
		}
	}
	return false
}

// memberCurrentRoom resolves the room a signed-in member currently holds a
// live seat in — the binding the private realtime voice endpoints use so a
// member in room A can never attach the assistant to room B's context. No
// live seat means the office (the dashboard default).
func (app *kanbanBoardApp) memberCurrentRoom(email string) string {
	if app == nil {
		return officeRoomID
	}
	name := participantNameForEmail(email)
	if name == "" {
		return officeRoomID
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	for roomID, state := range app.roomLive {
		if state.participantCounts[name] > 0 {
			return roomID
		}
	}
	return officeRoomID
}

// roomGuestsOnly reports whether the room's live seats are guests only (at
// least one guest, zero member seats) — the §6.5 brain-deferral condition: an
// unattended guest cannot drive summarization spend; transcription continues
// and the close-flush chain still runs one bounded pass.
func (app *kanbanBoardApp) roomGuestsOnly(roomID string) bool {
	if app == nil {
		return false
	}
	roomID = normalizeRoomID(roomID)
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)
	if len(state.guestSeats) == 0 {
		return false
	}
	guestNames := make(map[string]struct{}, len(state.guestSeats))
	for _, display := range state.guestSeats {
		guestNames[strings.ToLower(display)] = struct{}{}
	}
	for name := range state.participants {
		if state.participantCounts[name] <= 0 {
			continue
		}
		if _, isGuest := guestNames[strings.ToLower(name)]; !isGuest {
			return false // a member seat is live
		}
	}
	return true
}
