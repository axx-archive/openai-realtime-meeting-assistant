package main

// Multi-room W7 acceptance (§11 W7): ONE end-to-end flow over a real
// httptest websocket server proving the whole objective — member creates a
// passcoded guest-enabled room, mints a link, a guest redeems it with a name,
// joins THAT room only, chats (which feeds the transcript), receives only
// allowlisted events while office board traffic flows to members, bounces off
// the member surface, and when the room empties the close chain builds the
// record tier (brain, digest, archive) with ZERO proactive actions while the
// company rollups INCLUDE the sitting's material provenance-stamped (§6.4,
// RATIFIED 2026-07-09) — all with zero OpenAI realtime dials and the office's
// legacy data fully recallable throughout. The exhaustive per-guarantee
// batteries live in guest_allowlist_test.go / room_live_test.go /
// listen_only_test.go / migration_boot_test.go; this is the story that ties
// them together, plus a coverage manifest pinning those batteries in place.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

func TestMultiRoomAcceptanceFlow(t *testing.T) {
	resetGuestSocketCapsForTest(t)
	resetAuthRateLimitersForTest()
	t.Setenv("MEETING_TRANSCRIPT_LANE_ENABLED", "0")
	t.Setenv("MEETING_IDLE_END_GRACE", "75ms")
	t.Setenv("DAY_REFLECTION_DISABLED", "1")
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	// Any OpenAI Realtime dial anywhere in this flow is a failure: a guest
	// room is listen-only end to end (§7.3 layer 3, §4.4 lazy lifecycle).
	var realtimeDials atomic.Int64
	realtimeMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		realtimeDials.Add(1)
		http.Error(w, "no realtime in the acceptance flow", http.StatusInternalServerError)
	}))
	t.Cleanup(realtimeMock.Close)
	previousRealtimeURL := realtimeCallsURL
	realtimeCallsURL = realtimeMock.URL
	t.Cleanup(func() { realtimeCallsURL = previousRealtimeURL })

	server := newIsolatedWebsocketServer(t)
	app := kanbanApp

	// The office's legacy (pre-room) memory: recallable before, during, and
	// after the guest sitting.
	if _, _, err := app.memory.appendEntry(meetingMemoryKindTranscript, "legacy-office-ts", "AJ: the office context that predates rooms.", map[string]string{"meetingId": "meeting-legacy-office", "speaker": "AJ"}); err != nil {
		t.Fatalf("seed legacy office entry: %v", err)
	}

	// ---- 1. member creates a passcoded, guest-enabled room.
	memberCookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	created := doRoomsRequest(t, roomsHandler, http.MethodPost, "/rooms",
		`{"name":"NBC External","passcode":"hunter2","guestAccess":true}`, memberCookies)
	if created.Code != http.StatusOK {
		t.Fatalf("create room = %d body %s", created.Code, created.Body.String())
	}
	var createdPayload struct {
		Room struct {
			ID               string `json:"id"`
			PasscodeRequired bool   `json:"passcodeRequired"`
			GuestEnabled     bool   `json:"guestEnabled"`
		} `json:"room"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdPayload); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	roomID := createdPayload.Room.ID
	if roomID == "" || !createdPayload.Room.PasscodeRequired || !createdPayload.Room.GuestEnabled {
		t.Fatalf("created room payload = %+v", createdPayload.Room)
	}

	// ---- 2. member mints a guest link; the token appears exactly once, as
	// the fragment-carried URL.
	minted := doRoomsRequest(t, roomActionHandler, http.MethodPost, "/rooms/"+roomID+"/guest-links",
		`{"label":"NBC sync"}`, memberCookies)
	if minted.Code != http.StatusOK {
		t.Fatalf("mint link = %d body %s", minted.Code, minted.Body.String())
	}
	var mintPayload struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(minted.Body.Bytes(), &mintPayload); err != nil {
		t.Fatalf("decode mint response: %v", err)
	}
	linkToken := strings.TrimPrefix(mintPayload.URL, "/g#")
	if !isGuestTokenShape(linkToken) {
		t.Fatalf("mint URL %q does not carry a fragment token", mintPayload.URL)
	}
	links, err := appRoomStore().listGuestLinks(roomID)
	if err != nil || len(links) != 1 || links[0].Hash == linkToken {
		t.Fatalf("the raw token must never be stored: links=%+v err=%v", links, err)
	}

	// ---- 3. guest redeems with a name (the link waives the passcode §5.2).
	joined := postGuestJoin(t, fmt.Sprintf(`{"token":%q,"name":"Nia"}`, linkToken))
	if joined.Code != http.StatusOK {
		t.Fatalf("guest join = %d body %s", joined.Code, joined.Body.String())
	}
	var joinPayload struct {
		RoomID    string `json:"roomId"`
		GuestName string `json:"guestName"`
	}
	if err := json.Unmarshal(joined.Body.Bytes(), &joinPayload); err != nil {
		t.Fatalf("decode join response: %v", err)
	}
	if joinPayload.RoomID != roomID || joinPayload.GuestName != "Nia" {
		t.Fatalf("join payload = %+v, want the minted room", joinPayload)
	}
	guestCookie := guestCookieFrom(t, joined)

	// ---- 4. the guest session opens a socket into THAT room only.
	mismatch := "ws" + strings.TrimPrefix(server.URL, "http") + "/websocket?room=office"
	header := http.Header{}
	header.Set("Cookie", guestCookieName+"="+guestCookie.Value)
	if conn, resp, err := websocket.DefaultDialer.Dial(mismatch, header); err == nil {
		conn.Close()
		t.Fatal("guest dial into the office should fail pre-upgrade")
	} else if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("office mismatch = %+v, want 403", resp)
	}

	guestConn, _, err := dialGuestWebsocket(t, server, guestCookie.Value)
	if err != nil {
		t.Fatalf("guest dial: %v", err)
	}
	if err := guestConn.WriteJSON(map[string]string{"event": "participant", "data": `{}`}); err != nil {
		t.Fatalf("guest hello: %v", err)
	}
	grantRaw := waitForKanbanEvent(t, guestConn, "access_granted", 5*time.Second)
	var grant struct {
		Name   string `json:"name"`
		RoomID string `json:"roomId"`
		Guest  bool   `json:"guest"`
	}
	if err := json.Unmarshal(grantRaw, &grant); err != nil {
		t.Fatalf("decode grant: %v", err)
	}
	if grant.Name != "Guest Nia" || grant.RoomID != roomID || !grant.Guest {
		t.Fatalf("grant = %+v, want the server-prefixed guest seated in %s", grant, roomID)
	}
	// Seed a live OFFICE publisher before media_ready: its metadata must not
	// reach the guest's participant_track replay (the drain below checks) —
	// the metadata plane shares the RTP plane's room fence, server-side.
	officeProbe, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, "office-probe-video", "office-probe-stream")
	if err != nil {
		t.Fatalf("create office probe track: %v", err)
	}
	listLock.Lock()
	trackLocals[officeProbe.ID()] = officeProbe
	trackParticipants[officeProbe.ID()] = "AJ"
	trackParticipantSessions[officeProbe.ID()] = "aj-probe"
	listLock.Unlock()
	t.Cleanup(func() {
		listLock.Lock()
		delete(trackLocals, officeProbe.ID())
		delete(trackParticipants, officeProbe.ID())
		delete(trackParticipantSessions, officeProbe.ID())
		listLock.Unlock()
	})
	// Enter the media fan-out pool (the client does this right after the
	// grant) and wait for the registration so room broadcasts reach the seat.
	if err := guestConn.WriteJSON(map[string]string{"event": "media_ready", "data": `{}`}); err != nil {
		t.Fatalf("guest media_ready: %v", err)
	}
	poolDeadline := time.Now().Add(5 * time.Second)
	for {
		pooled := false
		listLock.RLock()
		for _, state := range peerConnections {
			if state.websocket != nil && state.websocket.guest && normalizeRoomID(state.roomID) == roomID {
				pooled = true
			}
		}
		listLock.RUnlock()
		if pooled {
			break
		}
		if time.Now().After(poolDeadline) {
			t.Fatal("guest socket never entered the room fan-out pool")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The sitting latched listen-only at admission (§7.1).
	record, ok := app.meetings.activeRecord(roomID)
	if !ok || !record.ListenOnly {
		t.Fatalf("active record = %+v ok=%v, want a latched listen-only sitting", record, ok)
	}

	// ---- 5. office board traffic flows to members and NEVER to the guest.
	memberConn := dialIsolatedWebsocket(t, server, "tim@shareability.com")
	sendOfficeHello(t, memberConn)
	broadcastSignedInKanbanEvent("board", map[string]any{"marker": "ACCEPT-office-board"})
	// The hello replay delivers a real board snapshot first; read board
	// frames until the marker broadcast lands.
	markerDeadline := time.Now().Add(5 * time.Second)
	for {
		boardRaw := waitForKanbanEvent(t, memberConn, "board", 5*time.Second)
		if strings.Contains(string(boardRaw), "ACCEPT-office-board") {
			break
		}
		if time.Now().After(markerDeadline) {
			t.Fatal("member board marker never arrived")
		}
	}

	// ---- 6. guest chat feeds the transcript under the guest identity.
	if err := guestConn.WriteJSON(map[string]string{"event": "room_chat", "data": `{"text":"We agreed to send the NBC partnership terms. GUESTMTGMARKER"}`}); err != nil {
		t.Fatalf("guest room_chat: %v", err)
	}
	// Drain the guest socket to its own chat echo: everything it received
	// since admission must be allowlisted, and the office board marker must
	// not be among it.
	for {
		if err := guestConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatalf("guest read deadline: %v", err)
		}
		var message websocketMessage
		if err := guestConn.ReadJSON(&message); err != nil {
			t.Fatalf("guest read before the chat echo: %v", err)
		}
		if strings.Contains(message.Data, "ACCEPT-office-board") {
			t.Fatalf("office board frame leaked to the guest: %s", message.Data)
		}
		if message.Event != "kanban" {
			continue
		}
		var inner struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(message.Data), &inner); err != nil {
			t.Fatalf("decode guest envelope: %v", err)
		}
		if !guestWritableKanbanEvents[inner.Event] {
			t.Fatalf("guest received non-allowlisted event %q", inner.Event)
		}
		if inner.Event == "participant_track" {
			var track participantTrackSnapshot
			if err := json.Unmarshal(inner.Data, &track); err != nil {
				t.Fatalf("decode guest participant_track: %v", err)
			}
			if track.TrackID == officeProbe.ID() || (track.RoomID != "" && track.RoomID != roomID) {
				t.Fatalf("cross-room participant_track reached the guest: %+v", track)
			}
		}
		if inner.Event == "room_chat" && strings.Contains(string(inner.Data), "GUESTMTGMARKER") {
			break
		}
	}
	var chatEntry *meetingMemoryEntry
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindTranscript, 0) {
		if strings.Contains(entry.Text, "GUESTMTGMARKER") {
			copied := entry
			chatEntry = &copied
		}
	}
	if chatEntry == nil {
		t.Fatal("guest chat never landed in the transcript")
	}
	if chatEntry.Metadata["roomId"] != roomID || chatEntry.Metadata["meetingId"] != record.ID {
		t.Fatalf("guest transcript filed under %v, want room %s meeting %s", chatEntry.Metadata, roomID, record.ID)
	}
	if !strings.Contains(chatEntry.Text, "Guest Nia") && chatEntry.Metadata["speaker"] != "Guest Nia" {
		t.Fatalf("guest transcript must carry the prefixed guest identity: text=%q metadata=%v", chatEntry.Text, chatEntry.Metadata)
	}

	// ---- 7. the guest session bounces off the member surface (sample; the
	// exhaustive fail-closed walk is TestGuestRouteWalkAllowlistFailsClosed).
	req := httptest.NewRequest(http.MethodGet, "/assistant/board", nil)
	req.AddCookie(guestCookie)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: guestCookie.Value})
	rec := httptest.NewRecorder()
	assistantBoardHandler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("guest on /assistant/board = %d, want 401", rec.Code)
	}

	// ---- 8. the close chain: record tier built, board stage skipped, and
	// the company rollups CONSUME the sitting (§6.4 ratified). The digest
	// stage answers with the sitting's real topics; every other stage gets
	// the superset shape.
	app.mu.Lock()
	app.apiKey = "acceptance-key"
	app.mu.Unlock()
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Instructions == meetingDigestInstructions() {
			return `{"meetingId":"x","day":"2026-07-09","topics":[{"t":"NBC partnership terms GUESTMTGMARKER","importance":5}],"decisions":[{"d":"Send the NBC terms sheet GUESTMTGMARKER","by":"Guest Nia","importance":5}]}`, nil
		}
		return closeFlushSupersetJSON, nil
	}
	app.flushAmbientAgentsForCloseWithResponder("acceptance-close", roomID, true, responder)
	app.mu.Lock()
	app.apiKey = ""
	app.mu.Unlock()

	var brainEntry *meetingMemoryEntry
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindBrain, 0) {
		if entry.Metadata["meetingId"] == record.ID {
			copied := entry
			brainEntry = &copied
		}
	}
	if brainEntry == nil {
		t.Fatal("the close chain never brained the guest sitting")
	}
	if brainEntry.Metadata["roomId"] != roomID || brainEntry.Metadata[listenOnlyMetadataKey] != "true" {
		t.Fatalf("brain metadata = %v, want the room + §6.4 provenance stamp", brainEntry.Metadata)
	}
	digestStamped := false
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindMeetingDigest, 0) {
		if entry.Metadata["meetingId"] == record.ID && entry.Metadata[listenOnlyMetadataKey] == "true" {
			digestStamped = true
		}
	}
	if !digestStamped {
		t.Fatal("the sitting's T2 digest is missing its listen-only provenance stamp")
	}
	// The company-tier recall window sees the guest meeting: the entity
	// ledger (fed by the flush) consolidated the sitting's facts.
	ledgerSaw := false
	for _, ledgerRecord := range app.memory.ledgerState() {
		if strings.Contains(ledgerRecord.Title, "GUESTMTGMARKER") {
			ledgerSaw = true
		}
	}
	if !ledgerSaw {
		t.Fatal("company rollups must include the guest sitting's material (§6.4 ratified)")
	}
	// Zero proactive actions, all three layers' outcome (§7.3).
	if updates := app.memory.entriesOfKind(meetingMemoryKindBoardUpdate, 0); len(updates) != 0 {
		t.Fatalf("board updates = %+v, want none from a guest sitting", updates)
	}
	if proposals := app.memory.entriesOfKind(meetingMemoryKindCodexProposal, 0); len(proposals) != 0 {
		t.Fatalf("proposals = %+v, want none from a guest sitting", proposals)
	}
	app.mu.Lock()
	notificationCount := len(app.notifications)
	app.mu.Unlock()
	if notificationCount != 0 {
		t.Fatalf("notifications = %d, want zero everyone-nudges from a guest sitting", notificationCount)
	}

	// ---- 9. the room empties: the idle grace closes the record, archives
	// the sitting, rotates the meeting id, and tears the lazy media down.
	if err := guestConn.Close(); err != nil {
		t.Fatalf("close guest socket: %v", err)
	}
	closeDeadline := time.Now().Add(15 * time.Second)
	for {
		closed, _ := app.meetings.recordByID(record.ID)
		if closed.EndedAt != "" && closed.ArchiveID != "" && app.roomMixerFor(roomID) == nil && app.memory.currentMeetingID(roomID) == "" {
			if closed.EndedReason != meetingEndedReasonIdle {
				t.Fatalf("record closed with reason %q, want idle", closed.EndedReason)
			}
			break
		}
		if time.Now().After(closeDeadline) {
			t.Fatalf("idle close never completed: record=%+v mixer=%v resumedID=%q",
				closed, app.roomMixerFor(roomID) != nil, app.memory.currentMeetingID(roomID))
		}
		time.Sleep(25 * time.Millisecond)
	}
	archiveFiled := false
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindArchive, 0) {
		if entry.Metadata["meetingId"] == record.ID && entry.Metadata["roomId"] == roomID {
			archiveFiled = true
		}
	}
	if !archiveFiled {
		t.Fatal("the auto-archive never filed under the guest room")
	}

	// ---- 10. zero OpenAI realtime dials across the whole flow, and the
	// office's legacy memory is untouched and fully recallable.
	if dials := realtimeDials.Load(); dials != 0 {
		t.Fatalf("realtime dials = %d, want zero for a guest-room flow", dials)
	}
	legacy := app.memory.snapshotForMeeting("meeting-legacy-office", 0)
	if len(legacy) != 1 || legacy[0].ID != "legacy-office-ts" {
		t.Fatalf("legacy office recall = %+v, want the pre-room entry intact", legacy)
	}
	if resumed := app.memory.currentMeetingID(officeRoomID); resumed != "" && resumed != "meeting-legacy-office" {
		t.Fatalf("the guest sitting contaminated the office meeting id: %q", resumed)
	}
}

// TestMultiRoomAcceptanceCoverage is the W7 manifest (the sanctioned
// assert-they-exist pattern from acceptance_test.go): each multi-room
// guarantee stays tied to the battery that proves it, so deleting or renaming
// a pillar test turns the acceptance gate red.
func TestMultiRoomAcceptanceCoverage(t *testing.T) {
	pillars := []struct {
		guarantee string
		tests     []string
	}{
		{
			"§6.7 exhaustive route-walk allowlist (fails closed for future routes)",
			[]string{"TestGuestRouteWalkAllowlistFailsClosed"},
		},
		{
			"§6.2 fan-out leak sweep over recorded guest sockets",
			[]string{"TestGuestFanOutLeakSweepAcrossBroadcastSeams", "TestGuestWriterAllowlistDropsMisroutedEvents", "TestGuestWebsocketReplayWithholdsBoardMemoryAndOfficeHelloDenied"},
		},
		{
			"§6.1 DoS caps (unit seams + concurrency battery)",
			[]string{"TestGuestCapsBatteryUnderConcurrency", "TestGuestThirdSocketOnOneSessionRejectedPreUpgrade", "TestGuestPreHelloSocketsPerIPCapped", "TestGuestRoomSeatCapRejectsNewSessionPreUpgrade", "TestUnadmittedGuestSocketAllocatesNoPeerConnection"},
		},
		{
			"§9.9 migration dress rehearsal on prod-shaped data",
			[]string{"TestMigrationDressRehearsalProdShapedBoot"},
		},
		{
			"§4.4 lazy media lifecycle (zero dials pre-admission, mediaGen-fenced teardown)",
			[]string{"TestNamedRoomMediaLazyLifecycle", "TestNamedRoomMediaTeardownVsRejoinRace", "TestOfficeRealtimeIsLazyAndNeverStartsForListenOnly", "TestOfficeIdleTeardownFencesRealtimeRestart"},
		},
		{
			"§7.4 cursor isolation — the make-or-break",
			[]string{"TestRoomCursorIsolationAcrossInterleavedTranscripts", "TestLegacyArtifactsWithoutRoomIDAreOfficeCursors", "TestTwoRoomsCloseFlushConcurrentlyWithoutDeadlock"},
		},
		{
			"§7.1–§7.3 listen-only latch + three suppression layers",
			[]string{"TestListenOnlyLatchSetAtAdmissionAndNeverUnlatches", "TestListenOnlySittingBuildsRecordButNeverActsProactively"},
		},
		{
			"§6.4 rollup inclusion, provenance-stamped (RATIFIED 2026-07-09)",
			[]string{"TestRollupsIncludeListenOnlySittingsProvenanceStamped", "TestReflectionIncludesDaysWithOnlyListenOnlyMaterial"},
		},
		{
			"§3.2 guest identity never crosses the member resolver",
			[]string{"TestGuestSessionNeverSatisfiesUserFromRequest", "TestLegacySessionRowsStillResolveAsUsers"},
		},
	}

	suite := loadTestSuiteSource(t)
	for _, pillar := range pillars {
		for _, name := range pillar.tests {
			if !strings.Contains(suite, "func "+name+"(") {
				t.Errorf("multi-room pillar %q: missing coverage test %s", pillar.guarantee, name)
			}
		}
	}
}
