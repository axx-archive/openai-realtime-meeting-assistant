package main

// Multi-room W4 listen-only battery (§7): the per-sitting latch, the three
// independent suppression layers (window filter, choke-point backstops, the
// never-started tier), the §6.4 rollup INCLUSION (RATIFIED 2026-07-09 —
// listen-only material flows into day/company digests, the entity ledger and
// reflection provenance-stamped, cursors advancing), and the lazy office
// realtime lifecycle (§4.4 — never a dial before admission, never for a
// listen-only sitting).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newListenOnlyRoom creates a guest-enabled named room WITH an active guest
// link — the §7.1 "guest-enabled" condition — under the test's isolated data
// directory. Returns (roomID, linkID).
func newListenOnlyRoom(t *testing.T, name string) (string, string) {
	t.Helper()
	room, err := appRoomStore().create(name, "", "aj@shareability.com", true)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	_, link, err := appRoomStore().mintGuestLink(room.ID, "", "aj@shareability.com", 0)
	if err != nil {
		t.Fatalf("mint guest link: %v", err)
	}
	return room.ID, link.ID
}

func appendRoomBrain(t *testing.T, app *kanbanBoardApp, roomID string, id string, text string) {
	t.Helper()
	if _, appended, err := app.memory.appendBrainWriteUp(id, text, map[string]string{"roomId": roomID}); err != nil {
		t.Fatalf("append brain %s: %v", id, err)
	} else if !appended {
		t.Fatalf("brain %s appended=false", id)
	}
}

func failingResponder(t *testing.T, label string) openAITextResponder {
	return func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatalf("%s must not reach the model for a listen-only window", label)
		return "", nil
	}
}

/* ---------- §7.1 the latch ---------- */

func TestListenOnlyLatchSetAtAdmissionAndNeverUnlatches(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	roomID, linkID := newListenOnlyRoom(t, "war room")

	// (a) guest-enabled at the sitting's start: the first admission latches.
	app.noteMeetingAdmission(roomID, "AJ")
	record, ok := app.meetings.activeRecord(roomID)
	if !ok || !record.ListenOnly {
		t.Fatalf("record=%+v ok=%v, want an open listen-only record", record, ok)
	}
	if !app.meetingListenOnly(record.ID) {
		t.Fatal("meetingListenOnly must resolve the latched record")
	}

	// The latch NEVER unlatches within the sitting: revoking every link (the
	// last-guest-left / escape-hatch state) keeps the sitting listen-only.
	if err := appRoomStore().revokeGuestLink(roomID, linkID); err != nil {
		t.Fatalf("revoke link: %v", err)
	}
	if app.roomListenOnly(roomID) {
		t.Fatal("roomListenOnly must clear once every link is revoked and no guest is seated")
	}
	app.noteMeetingAdmission(roomID, "Tom")
	record, _ = app.meetings.activeRecord(roomID)
	if !record.ListenOnly {
		t.Fatal("the latch must persist after the guest exposure ends (per-sitting, one-way)")
	}
	if !app.sittingListenOnly(roomID) {
		t.Fatal("sittingListenOnly must keep reporting the latched sitting")
	}

	// The office never latches by default.
	app.noteMeetingAdmission(officeRoomID, "AJ")
	office, _ := app.meetings.activeRecord(officeRoomID)
	if office.ListenOnly || app.sittingListenOnly(officeRoomID) {
		t.Fatal("office must stay full mode")
	}

	// (b) mid-sitting first guest: a full-mode room latches when a guest seats.
	fullRoom, err := appRoomStore().create("full room", "", "aj@shareability.com", false)
	if err != nil {
		t.Fatalf("create full room: %v", err)
	}
	app.noteMeetingAdmission(fullRoom.ID, "AJ")
	if mid, _ := app.meetings.activeRecord(fullRoom.ID); mid.ListenOnly {
		t.Fatal("no-guest room must start full mode")
	}
	app.mu.Lock()
	state := app.roomLiveLocked(fullRoom.ID)
	state.guestSeats["guest-session-1"] = "Guest Sam"
	state.participants["Guest Sam"] = time.Now()
	state.participantCounts["Guest Sam"] = 1
	app.mu.Unlock()
	app.noteMeetingAdmission(fullRoom.ID, "Guest Sam")
	if mid, _ := app.meetings.activeRecord(fullRoom.ID); !mid.ListenOnly {
		t.Fatal("first mid-sitting guest admission must latch the record")
	}
}

/* ---------- §7.3 layer 1+2+3: the suppression battery ---------- */

func TestListenOnlySittingBuildsRecordButNeverActsProactively(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	roomID, _ := newListenOnlyRoom(t, "guest hall")
	app.noteMeetingAdmission(roomID, "AJ")
	record, _ := app.meetings.activeRecord(roomID)
	if !record.ListenOnly {
		t.Fatal("fixture: record must be latched")
	}

	// The record tier still runs: transcripts brain fine, stamped for §6.4.
	appendRoomTestTranscript(t, app, roomID, "lo-ts-1", "The guests walked through the partnership terms sheet.")
	brainResponder := func(context.Context, string, openAITextRequest) (string, error) {
		return "## Overview\nPartnership terms were discussed.", nil
	}
	brainEntry, err := app.runAmbientAgentOnceForRoom(meetingBrainAgent(), context.Background(), "test-key", brainResponder, 1, roomID)
	if err != nil {
		t.Fatalf("brain pass: %v", err)
	}
	if brainEntry.Metadata[listenOnlyMetadataKey] != "true" {
		t.Fatalf("brain metadata=%v, want the §6.4 listenOnly provenance stamp", brainEntry.Metadata)
	}
	if brainEntry.Metadata["meetingId"] != record.ID {
		t.Fatalf("brain meetingId=%q, want the latched sitting %q", brainEntry.Metadata["meetingId"], record.ID)
	}

	// The meeting digest (per-meeting tier) still runs, stamped.
	digestResponder := func(context.Context, string, openAITextRequest) (string, error) {
		return `{"meetingId":"x","day":"2026-07-08","topics":[{"t":"Partnership terms","importance":4}]}`, nil
	}
	if _, err := app.runAmbientAgentOnceForRoom(meetingDigestAgent(), context.Background(), "test-key", digestResponder, 1, roomID); err != nil {
		t.Fatalf("meeting digest pass: %v", err)
	}
	digests := app.memory.entriesOfKind(meetingMemoryKindMeetingDigest, 0)
	if len(digests) != 1 || digests[0].Metadata[listenOnlyMetadataKey] != "true" {
		t.Fatalf("digests=%v, want one listen-only-stamped digest", digests)
	}

	// Layer 1: the board worker's window filter — the pass never calls the
	// model, appends no artifact, and advances its baseline (a second pass
	// stays silent too).
	for pass := 0; pass < 2; pass++ {
		if _, err := app.runAmbientAgentOnceForRoom(meetingBoardAgent(), context.Background(), "test-key", failingResponder(t, "board worker"), 1, roomID); err != nil {
			t.Fatalf("board pass %d: %v", pass, err)
		}
	}
	if updates := app.memory.entriesOfKind(meetingMemoryKindBoardUpdate, 0); len(updates) != 0 {
		t.Fatalf("board updates=%v, want none for a listen-only sitting", updates)
	}

	// Layer 1: the suggestion agent's window filter.
	if _, err := app.runAmbientAgentOnceForRoom(researchSuggestionAgent(), context.Background(), "test-key", failingResponder(t, "suggestion agent"), 1, roomID); err != nil {
		t.Fatalf("suggestion pass: %v", err)
	}

	// Layer 2: the proposeCodexTask choke point — rejected, nothing minted,
	// no everyone-notification.
	if _, _, err := app.proposeCodexTask(map[string]any{
		"title":          "Research the partnership market",
		"mode":           "research",
		"query":          "compile a partnership landscape brief",
		"origin_room_id": roomID,
	}, "board_worker"); err == nil || !strings.Contains(err.Error(), "listen-only") {
		t.Fatalf("proposeCodexTask err=%v, want the listen-only rejection", err)
	}
	if proposals := app.memory.entriesOfKind(meetingMemoryKindCodexProposal, 0); len(proposals) != 0 {
		t.Fatalf("proposals=%v, want none", proposals)
	}
	app.mu.Lock()
	notificationCount := len(app.notifications)
	app.mu.Unlock()
	if notificationCount != 0 {
		t.Fatalf("notifications=%d, want zero (no confirm-to-launch nudge from a guest room)", notificationCount)
	}

	// Layer 2: applyMeetingBoardAnalysis refuses mutation ops for the
	// listen-only source; do_nothing still passes.
	result := app.applyMeetingBoardAnalysisForRoom(meetingBoardAnalysis{
		Summary: "should be refused",
		Operations: []meetingBoardOperation{
			{Tool: "create_ticket", Arguments: map[string]any{"title": "Leaked card", "notes": "n", "owner": "AJ", "status": "Backlog", "tags": []any{"build"}}},
			{Tool: "propose_codex_task", Arguments: map[string]any{"title": "Leaked proposal", "mode": "research", "query": "q"}},
			{Tool: "do_nothing", Arguments: map[string]any{"reason": "quiet"}},
		},
	}, roomID)
	if result.ChangedCount != 0 || result.ErrorCount != 2 {
		t.Fatalf("result=%+v, want two refused mutations and zero changes", result)
	}
	for _, application := range result.Applications[:2] {
		if !strings.Contains(application.Error, "listen-only") {
			t.Fatalf("application=%+v, want the listen-only refusal", application)
		}
	}

	// Layer 2: the workflow ticker declines listen-only origins; a pre-guest
	// proposal (no originRoomId stamp — or an office origin) still launches.
	now := time.Now()
	declined := meetingMemoryEntry{ID: "prop-lo", Kind: meetingMemoryKindCodexProposal, Metadata: map[string]string{
		"mode": "research", "query": "compile a market analysis", "status": codexProposalStatusProposed,
		"lane": codexProposalLaneAutoRun, "laneApprovedBy": "AJ", "originRoomId": roomID,
	}}
	if _, _, _, ok := app.workflowTickerEligible(declined, now); ok {
		t.Fatal("ticker must decline a listen-only-origin proposal")
	}
	preGuest := meetingMemoryEntry{ID: "prop-legacy", Kind: meetingMemoryKindCodexProposal, Metadata: map[string]string{
		"mode": "research", "query": "compile a market analysis", "status": codexProposalStatusProposed,
		"lane": codexProposalLaneAutoRun, "laneApprovedBy": "AJ",
	}}
	if _, _, _, ok := app.workflowTickerEligible(preGuest, now); !ok {
		t.Fatal("a pre-guest proposal (no origin stamp) must still launch")
	}
	officeOrigin := meetingMemoryEntry{ID: "prop-office", Kind: meetingMemoryKindCodexProposal, Metadata: map[string]string{
		"mode": "research", "query": "compile a market analysis", "status": codexProposalStatusProposed,
		"lane": codexProposalLaneAutoRun, "laneApprovedBy": "AJ", "originRoomId": officeRoomID,
	}}
	if _, _, _, ok := app.workflowTickerEligible(officeOrigin, now); !ok {
		t.Fatal("an office-origin proposal must still launch")
	}

	// Layer 3: the close flush skips the board stage for the listen-only
	// sitting — every stage that runs uses the injected responder; the board
	// stage would create a board_update artifact.
	appendRoomTestTranscript(t, app, roomID, "lo-ts-2", "The guests closed with a summary of the next steps.")
	app.flushAmbientAgentsForCloseWithResponder("idle-end", roomID, true, func(context.Context, string, openAITextRequest) (string, error) {
		return closeFlushSupersetJSON, nil
	})
	if updates := app.memory.entriesOfKind(meetingMemoryKindBoardUpdate, 0); len(updates) != 0 {
		t.Fatalf("close flush ran the board stage for a listen-only sitting: %v", updates)
	}
	if brains := app.memory.entriesOfKind(meetingMemoryKindBrain, 0); len(brains) < 2 {
		t.Fatalf("brains=%d, want the close flush to still summarize the tail", len(brains))
	}
}

/* ---------- §6.4 rollup inclusion (RATIFIED 2026-07-09) ---------- */

// seedCurrentMeetingDigest upserts a parseable T2 digest for one meeting with
// one topic and one decision, optionally listen-only-stamped.
func seedCurrentMeetingDigest(t *testing.T, app *kanbanBoardApp, meetingID string, day string, spanStart time.Time, spanEnd time.Time, marker string, listenOnly bool) {
	t.Helper()
	payload := fmt.Sprintf(`{"meetingId":%q,"day":%q,"topics":[{"t":"Topic %s","importance":4}],"decisions":[{"d":"Decision %s","by":"AJ","importance":5}]}`, meetingID, day, marker, marker)
	metadata := map[string]string{
		"meetingId":                meetingID,
		digestDayMetadataKey:       day,
		digestSpanStartMetadataKey: spanStart.UTC().Format(time.RFC3339),
		digestSpanEndMetadataKey:   spanEnd.UTC().Format(time.RFC3339),
	}
	if listenOnly {
		metadata[listenOnlyMetadataKey] = "true"
	}
	if _, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, meetingID, payload, metadata); err != nil {
		t.Fatalf("seed digest %s: %v", meetingID, err)
	}
}

// TestRollupsIncludeListenOnlySittingsProvenanceStamped pins the RATIFIED
// §6.4 contract end to end: a listen-only sitting's T2 digest flows into the
// day digest fold, the entity ledger, the company digest narrative and the
// reflection window EXACTLY like member-only material — the external
// meeting's memory must reach the brain so Scout can answer about it
// company-wide — while the write-time provenance stamp survives on the T2
// digest (the durable origin record a re-quarantine toggle would key on) and
// every pass cursor advances normally.
func TestRollupsIncludeListenOnlySittingsProvenanceStamped(t *testing.T) {
	t.Setenv("DAY_REFLECTION_DISABLED", "1")
	app := newIsolatedKanbanBoardApp(t)

	location := meetingTimeLocation()
	yesterdayNoon := time.Now().In(location).AddDate(0, 0, -1)
	day := dayBucket(yesterdayNoon)
	spanStart := yesterdayNoon.Add(-time.Hour)
	seedCurrentMeetingDigest(t, app, "meeting-lo", day, spanStart, yesterdayNoon, "GUESTMTG", true)
	seedCurrentMeetingDigest(t, app, "meeting-ok", day, spanStart, yesterdayNoon, "MEMBERMTG", false)

	// Day digest fold: BOTH meetings contribute (the fold is deterministic —
	// no model call) and the pass cursor advances past both inputs.
	if _, err := app.runAmbientAgentOnce(dayDigestAgent(), context.Background(), "test-key", failingResponder(t, "day digest fold"), 1); err != nil {
		t.Fatalf("day digest pass: %v", err)
	}
	dayDigests := app.memory.entriesOfKind(meetingMemoryKindDayDigest, 0)
	if len(dayDigests) != 1 {
		t.Fatalf("day digests=%d, want one", len(dayDigests))
	}
	if !strings.Contains(dayDigests[0].Text, "GUESTMTG") || !strings.Contains(dayDigests[0].Text, "MEMBERMTG") {
		t.Fatalf("day digest must fold the listen-only sitting alongside the member one:\n%s", dayDigests[0].Text)
	}
	if !strings.Contains(dayDigests[0].Metadata["meetingIds"], "meeting-lo") {
		t.Fatalf("day digest meetingIds=%q must include the listen-only meeting", dayDigests[0].Metadata["meetingIds"])
	}
	// The write-time provenance stamp survives on the T2 digest itself.
	stamped := false
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindMeetingDigest, 0) {
		if entry.Metadata["meetingId"] == "meeting-lo" && entry.Metadata[listenOnlyMetadataKey] == "true" {
			stamped = true
		}
	}
	if !stamped {
		t.Fatal("the listen-only T2 digest must keep its provenance stamp")
	}
	// Cursor: nothing re-feeds.
	if _, err := app.runAmbientAgentOnce(dayDigestAgent(), context.Background(), "test-key", failingResponder(t, "day digest refeed"), 1); err != nil {
		t.Fatalf("day digest re-pass: %v", err)
	}
	if passes := app.memory.entriesOfKind(meetingMemoryKindDayDigestPass, 0); len(passes) != 1 {
		t.Fatalf("day digest passes=%d, want exactly one (cursor advanced past both inputs)", len(passes))
	}

	// Entity ledger: BOTH sittings' facts consolidate into the canonical
	// registry; the pass artifact advances through the whole window.
	if _, err := app.runAmbientAgentOnce(entityLedgerAgent(), context.Background(), "test-key", failingResponder(t, "entity ledger"), 1); err != nil {
		t.Fatalf("entity ledger pass: %v", err)
	}
	foundGuest, foundMember := false, false
	for _, record := range app.memory.ledgerState() {
		if strings.Contains(record.Title, "GUESTMTG") {
			foundGuest = true
		}
		if strings.Contains(record.Title, "MEMBERMTG") {
			foundMember = true
		}
	}
	if !foundGuest || !foundMember {
		t.Fatalf("ledger guest=%v member=%v, want both sittings' facts consolidated", foundGuest, foundMember)
	}
	if _, err := app.runAmbientAgentOnce(entityLedgerAgent(), context.Background(), "test-key", failingResponder(t, "entity ledger refeed"), 1); err != nil {
		t.Fatalf("entity ledger re-pass: %v", err)
	}
	if passes := app.memory.entriesOfKind(meetingMemoryKindLedgerPass, 0); len(passes) != 1 {
		t.Fatalf("ledger passes=%d, want exactly one", len(passes))
	}

	// Company digest: the listen-only sitting's ledger deltas reach the
	// narrative pass input like any other material.
	companyInputs := []string{}
	companyResponder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		companyInputs = append(companyInputs, request.Input)
		return "The guest meeting's terms and the member decisions both moved forward.", nil
	}
	if _, err := app.runAmbientAgentOnce(companyDigestAgent(), context.Background(), "test-key", companyResponder, 1); err != nil {
		t.Fatalf("company digest pass: %v", err)
	}
	if len(companyInputs) != 1 || !strings.Contains(companyInputs[0], "GUESTMTG") || !strings.Contains(companyInputs[0], "MEMBERMTG") {
		t.Fatalf("company digest input must carry both sittings' deltas:\n%s", strings.Join(companyInputs, "\n"))
	}

	// Reflection: the listen-only digest counts as material and enters the
	// synthesis window alongside the member digest.
	t.Setenv("DAY_REFLECTION_DISABLED", "")
	reflectionInputs := []string{}
	reflectionResponder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		reflectionInputs = append(reflectionInputs, request.Input)
		return "## Recurring blockers\n- The GUESTMTG partnership question keeps resurfacing.", nil
	}
	now := time.Now().In(location)
	if _, appended, err := app.maybeEmitDailyReflection(context.Background(), "test-key", reflectionResponder, now.UTC()); err != nil {
		t.Fatalf("reflection: %v", err)
	} else if !appended {
		t.Fatal("reflection must append")
	}
	if len(reflectionInputs) != 1 || !strings.Contains(reflectionInputs[0], "GUESTMTG") || !strings.Contains(reflectionInputs[0], "MEMBERMTG") {
		t.Fatalf("reflection input must include the listen-only digest:\n%s", strings.Join(reflectionInputs, "\n"))
	}
}

// A day whose ONLY material came from listen-only sittings still reflects —
// the ratified §6.4 inclusion means a guest-meeting day is a real day in the
// company's memory, not a hole.
func TestReflectionIncludesDaysWithOnlyListenOnlyMaterial(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	location := meetingTimeLocation()
	yesterdayNoon := time.Now().In(location).AddDate(0, 0, -1)
	day := dayBucket(yesterdayNoon)
	seedCurrentMeetingDigest(t, app, "meeting-lo-only", day, yesterdayNoon.Add(-time.Hour), yesterdayNoon, "GUESTMTG", true)

	reflectionInputs := []string{}
	reflectionResponder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		reflectionInputs = append(reflectionInputs, request.Input)
		return "## Recurring blockers\n- The GUESTMTG follow-up is still open.", nil
	}
	if _, appended, err := app.maybeEmitDailyReflection(context.Background(), "test-key", reflectionResponder, time.Now().UTC()); err != nil {
		t.Fatalf("reflection: %v", err)
	} else if !appended {
		t.Fatal("a day whose only material is listen-only must still reflect (ratified inclusion)")
	}
	if len(reflectionInputs) != 1 || !strings.Contains(reflectionInputs[0], "GUESTMTG") {
		t.Fatalf("reflection input must carry the listen-only digest:\n%s", strings.Join(reflectionInputs, "\n"))
	}
}

/* ---------- §4.4 lazy office realtime (never for listen-only) ---------- */

func TestOfficeRealtimeIsLazyAndNeverStartsForListenOnly(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("MEETING_TRANSCRIPT_LANE_ENABLED", "0")
	t.Setenv("OPENAI_API_KEY", "test-key")

	var dials atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		dials.Add(1)
		http.Error(w, "no realtime in tests", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	previousURL := realtimeCallsURL
	realtimeCallsURL = server.URL
	t.Cleanup(func() { realtimeCallsURL = previousURL })

	// Boot no longer dials: JoinConferenceRoom starts the workers only.
	if err := app.JoinConferenceRoom(); err != nil {
		t.Fatalf("JoinConferenceRoom: %v", err)
	}
	t.Cleanup(func() { _ = app.Close() })
	app.mu.Lock()
	bootPeer := app.pc
	app.mu.Unlock()
	if bootPeer != nil || dials.Load() != 0 {
		t.Fatalf("boot started the realtime peer (pc=%v dials=%d), want lazy", bootPeer, dials.Load())
	}

	// A listen-only office sitting NEVER creates the peer.
	_, linkID, err := appRoomStore().mintGuestLink(officeRoomID, "", "aj@shareability.com", 0)
	if err != nil {
		t.Fatalf("mint office link: %v", err)
	}
	app.ensureRoomMedia(officeRoomID)
	app.mu.Lock()
	listenOnlyPeer := app.pc
	app.mu.Unlock()
	if listenOnlyPeer != nil || dials.Load() != 0 {
		t.Fatalf("listen-only office admission created a realtime peer (dials=%d)", dials.Load())
	}

	// Full mode again: the first admission creates the peer and dials.
	if err := appRoomStore().revokeGuestLink(officeRoomID, linkID.ID); err != nil {
		t.Fatalf("revoke office link: %v", err)
	}
	app.ensureRoomMedia(officeRoomID)
	deadline := time.Now().Add(20 * time.Second)
	for dials.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if dials.Load() == 0 {
		t.Fatal("full-mode office admission never dialed the realtime API")
	}
}

// TestNamedRoomMediaNeverTouchesTheOfficeRealtimePeer pins §7.3's
// never-started tier for named rooms: their lazy media path creates a mixer
// only, never a Scout realtime session.
func TestNamedRoomMediaNeverTouchesTheOfficeRealtimePeer(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("MEETING_TRANSCRIPT_LANE_ENABLED", "0")
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	app.ensureRoomMedia("room-nnnn1111")
	app.mu.Lock()
	peer := app.pc
	mixer := app.roomLiveLocked("room-nnnn1111").mixer
	app.mu.Unlock()
	if peer != nil {
		t.Fatal("named-room admission must not create a realtime peer")
	}
	if mixer == nil {
		t.Fatal("named-room admission must still create its mixer")
	}
}

// The office idle teardown bumps the office mediaGen so a queued realtime
// restart (the teardown-vs-restart race) aborts instead of resurrecting a
// peer for an empty room.
func TestOfficeIdleTeardownFencesRealtimeRestart(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	app.mu.Lock()
	genBefore := app.roomLiveLocked(officeRoomID).mediaGen
	app.mu.Unlock()

	app.teardownRoomMediaAfterIdle(officeRoomID)

	app.mu.Lock()
	genAfter := app.roomLiveLocked(officeRoomID).mediaGen
	app.mu.Unlock()
	if genAfter != genBefore+1 {
		t.Fatalf("office mediaGen=%d, want %d after idle teardown", genAfter, genBefore+1)
	}

	// A restart queued against the torn-down sitting is a no-op: pc stays nil
	// and the restarting flag never sticks.
	app.restartRealtimePeer("stale reconnect after teardown")
	app.mu.Lock()
	peer := app.pc
	restarting := app.restarting
	app.mu.Unlock()
	if peer != nil || restarting {
		t.Fatalf("restart after teardown resurrected the peer (pc=%v restarting=%v)", peer, restarting)
	}
}

/* ---------- private realtime endpoints bind to the caller's room ---------- */

func TestRealtimeOfferAndToolRefuseListenOnlyRoom(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	roomID, _ := newListenOnlyRoom(t, "guest suite")

	// Seat the member in the listen-only room.
	if _, _, err := app.admitParticipantSessionEndpointInRoom(roomID, "AJ", "aj-session-1", "endpoint-1"); err != nil {
		t.Fatalf("admit member: %v", err)
	}
	if got := app.memberCurrentRoom("aj@shareability.com"); got != roomID {
		t.Fatalf("memberCurrentRoom=%q, want %q", got, roomID)
	}
	if !app.sittingListenOnly(roomID) {
		t.Fatal("guest-linked room must be listen-only live")
	}
	// A member with no live seat binds to the office (full mode).
	if got := app.memberCurrentRoom("tom@shareability.com"); got != officeRoomID {
		t.Fatalf("seatless memberCurrentRoom=%q, want office", got)
	}
}

/* ---------- recap stays in its room ---------- */

func TestMeetingRecapForNamedRoomNeverReachesSignedInUnion(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	conn := dialIsolatedWebsocket(t, server, "tom@shareability.com")
	sendOfficeHello(t, conn)
	waitForKanbanEvent(t, conn, "codex_proposals", 5*time.Second)

	kanbanApp.mu.Lock()
	kanbanApp.apiKey = "test-key"
	kanbanApp.mu.Unlock()
	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		return "## Overview\nRoom B talked through the private diligence points.", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	roomB := "room-recap-bbbb"
	appendRoomTestTranscript(t, kanbanApp, roomB, "recap-roomb-1", "Private diligence points were talked through in room B.")
	result, _, err := kanbanApp.meetingRecap(map[string]any{"audience": "room"}, "", roomB)
	if err != nil {
		t.Fatalf("meetingRecap for room B: %v", err)
	}
	if !strings.Contains(asString(result["recap"]), "private diligence points") {
		t.Fatalf("recap=%v, want room B's write-up", result["recap"])
	}

	// Ordered-marker probe: anything the recap leaked onto the signed-in
	// union would arrive on the office socket BEFORE this marker.
	broadcastSignedInKanbanEvent("memory", []map[string]any{{"id": "recap-marker"}})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("read websocket: %v", err)
		}
		if message.Event != "kanban" {
			continue
		}
		var inner struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(message.Data), &inner); err != nil {
			t.Fatalf("decode kanban envelope: %v", err)
		}
		if inner.Event == "room_chat" {
			t.Fatalf("room B's recap leaked to the signed-in union: %s", inner.Data)
		}
		if inner.Event == "memory" && strings.Contains(string(inner.Data), "recap-marker") {
			break
		}
	}

	// The recap still landed durably in ROOM B's transcript stream.
	foundRoomChat := false
	for _, entry := range kanbanApp.memory.entriesOfKind(meetingMemoryKindTranscript, 0) {
		if strings.Contains(entry.Text, "Meeting recap:") {
			foundRoomChat = true
			if got := normalizeRoomID(entry.Metadata["roomId"]); got != roomB {
				t.Fatalf("recap chat entry roomId=%q, want %q", got, roomB)
			}
		}
	}
	if !foundRoomChat {
		t.Fatal("room B recap was never recorded to its transcript stream")
	}
}
