package main

// Multi-room W3 test battery (docs/plans/multi-room-2026-07-08.md §11 W3):
// two-room SFU isolation, guest websocket containment (replay set, office
// hello, write-time allowlist), passcode admission, the §6.1 DoS caps
// (including the deferred guest PeerConnection), guest chat/transcription
// cost bounds, cross-room eviction, lazy media with mediaGen fencing, the
// per-room liveness reap, and guest attribution.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

// resetGuestSocketCapsForTest isolates the package-level pre-upgrade cap
// registry, restoring the previous maps when the test ends.
func resetGuestSocketCapsForTest(t *testing.T) {
	t.Helper()
	guestSocketCaps.mu.Lock()
	prevSessions := guestSocketCaps.perSession
	prevPreHello := guestSocketCaps.preHelloByIP
	guestSocketCaps.perSession = map[string]int{}
	guestSocketCaps.preHelloByIP = map[string]int{}
	guestSocketCaps.mu.Unlock()
	t.Cleanup(func() {
		guestSocketCaps.mu.Lock()
		guestSocketCaps.perSession = prevSessions
		guestSocketCaps.preHelloByIP = prevPreHello
		guestSocketCaps.mu.Unlock()
	})
}

// dialGuestWebsocket dials /websocket with a bonfire_guest cookie. It returns
// the connection (nil when the dial failed) and the HTTP response.
func dialGuestWebsocket(t *testing.T, server *httptest.Server, guestToken string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/websocket"
	header := http.Header{}
	header.Set("Cookie", guestCookieName+"="+guestToken)
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if conn != nil {
		t.Cleanup(func() { _ = conn.Close() })
	}
	return conn, resp, err
}

// mintGuestRoomAndSession creates a guest-enabled named room plus a redeemed
// guest session for it, returning (roomID, guestSessionToken).
func mintGuestRoomAndSession(t *testing.T, guestName string) (string, string) {
	t.Helper()
	room, err := appRoomStore().create("deal room", "", "aj@shareability.com", true)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	token, err := userSessionStore().createGuest(room.ID, guestName)
	if err != nil {
		t.Fatalf("create guest session: %v", err)
	}
	return room.ID, token
}

/* ---------- two-room SFU isolation ---------- */

func TestTwoRoomTrackIsolation(t *testing.T) {
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}
	roomATrack, err := webrtc.NewTrackLocalStaticRTP(codec, "aj-video", "aj-stream")
	if err != nil {
		t.Fatalf("create room A track: %v", err)
	}

	snapshotPeerState(t)
	listLock.Lock()
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{roomATrack.ID(): roomATrack}
	trackParticipants = map[string]string{roomATrack.ID(): "AJ"}
	trackParticipantSessions = map[string]string{roomATrack.ID(): "aj-1"}
	trackRooms = map[string]string{roomATrack.ID(): "room-aaaa"}
	trackLayerRIDs = map[string]string{}
	trackLayerGroups = map[string]string{}
	subscriberLayerTiers = map[string]string{}
	sameRoomSubscriber := peerConnectionState{participantName: "Tim", sessionID: "tim-1", roomID: "room-aaaa"}
	otherRoomSubscriber := peerConnectionState{participantName: "Erick", sessionID: "erick-1", roomID: "room-bbbb"}
	officeSubscriber := peerConnectionState{participantName: "Tom", sessionID: "tom-1"} // legacy empty == office
	listLock.Unlock()

	listLock.RLock()
	defer listLock.RUnlock()
	if !sameRoomSubscriber.acceptsTrack(roomATrack) {
		t.Fatal("same-room subscriber should be offered the track")
	}
	if otherRoomSubscriber.acceptsTrack(roomATrack) {
		t.Fatal("room A track was offered to a room B subscriber")
	}
	if officeSubscriber.acceptsTrack(roomATrack) {
		t.Fatal("room A track was offered to an office subscriber")
	}
}

// TestParticipantTrackSnapshotsAreRoomScoped pins the metadata plane to the
// same room fence as the RTP plane: the participant_track replay (media_ready /
// request_participant_tracks) must never name publishers of OTHER rooms — a
// guest seated in room B must not learn who is publishing in the office (§6.2:
// isolation holds server-side, not by client-side filtering).
func TestParticipantTrackSnapshotsAreRoomScoped(t *testing.T) {
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}
	officeTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "aj-video", "aj-stream")
	if err != nil {
		t.Fatalf("create office track: %v", err)
	}
	roomBTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "guest-video", "guest-stream")
	if err != nil {
		t.Fatalf("create room B track: %v", err)
	}

	snapshotPeerState(t)
	listLock.Lock()
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{
		officeTrack.ID(): officeTrack,
		roomBTrack.ID():  roomBTrack,
	}
	trackParticipants = map[string]string{
		officeTrack.ID(): "AJ",
		roomBTrack.ID():  "Guest Nia",
	}
	trackParticipantSessions = map[string]string{
		officeTrack.ID(): "aj-1",
		roomBTrack.ID():  "guest-1",
	}
	trackSourceIDs = map[string]string{
		officeTrack.ID(): "aj-camera-source",
		roomBTrack.ID():  "guest-camera-source",
	}
	// Legacy office entry has no trackRooms row at all (§9).
	trackRooms = map[string]string{roomBTrack.ID(): "room-bbbb"}
	trackLayerRIDs = map[string]string{}
	trackLayerGroups = map[string]string{}
	subscriberLayerTiers = map[string]string{}
	listLock.Unlock()

	roomBSnapshots := participantTrackSnapshots("room-bbbb", "")
	if len(roomBSnapshots) != 1 {
		t.Fatalf("room B snapshots=%v, want only the room B track — office publishers leaked", roomBSnapshots)
	}
	if roomBSnapshots[0].Name != "Guest Nia" || roomBSnapshots[0].TrackID != roomBTrack.ID() {
		t.Fatalf("room B snapshot=%+v, want Guest Nia's room-bbbb track", roomBSnapshots[0])
	}
	if roomBSnapshots[0].RoomID != "room-bbbb" {
		t.Fatalf("room B snapshot roomID=%q, want room-bbbb stamped for the client router", roomBSnapshots[0].RoomID)
	}

	officeSnapshots := participantTrackSnapshots("", "") // legacy empty == office
	if len(officeSnapshots) != 1 {
		t.Fatalf("office snapshots=%v, want only the office track — room B publishers leaked", officeSnapshots)
	}
	if officeSnapshots[0].Name != "AJ" || officeSnapshots[0].RoomID != officeRoomID {
		t.Fatalf("office snapshot=%+v, want AJ's track stamped office", officeSnapshots[0])
	}
}

func TestLegacyOfficeTracksStillForwardToOfficeSubscribers(t *testing.T) {
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}
	officeTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "tim-video", "tim-stream")
	if err != nil {
		t.Fatalf("create office track: %v", err)
	}

	snapshotPeerState(t)
	listLock.Lock()
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{officeTrack.ID(): officeTrack}
	trackParticipants = map[string]string{officeTrack.ID(): "Tim"}
	trackParticipantSessions = map[string]string{officeTrack.ID(): "tim-1"}
	// Legacy entry: no trackRooms row at all — absent means office (§9).
	trackRooms = map[string]string{}
	trackLayerRIDs = map[string]string{}
	trackLayerGroups = map[string]string{}
	subscriberLayerTiers = map[string]string{}
	officeSubscriber := peerConnectionState{participantName: "AJ", sessionID: "aj-1"}
	listLock.Unlock()

	listLock.RLock()
	defer listLock.RUnlock()
	if !officeSubscriber.acceptsTrack(officeTrack) {
		t.Fatal("legacy office track must keep forwarding to office subscribers")
	}
}

/* ---------- write-time guest event allowlist (§6.2) ---------- */

func TestGuestWriterAllowlistDropsMisroutedEvents(t *testing.T) {
	guestWriter := &threadSafeWriter{guest: true} // nil conn: any real write errors
	// A mis-routed board event to a guest writer is dropped BEFORE the write —
	// a nil-conn writer proves no write was attempted (it would error).
	for _, event := range []string{"board", "memory", "notification_backlog", "codex_proposals", "chat_thread", "server_shutdown"} {
		if err := sendKanbanEvent(guestWriter, event, map[string]any{"x": 1}); err != nil {
			t.Fatalf("event %q reached the guest writer instead of being dropped: %v", event, err)
		}
	}
	// Allowlisted events DO attempt the write (and error on the nil conn).
	if err := sendKanbanEvent(guestWriter, "room_chat", map[string]any{"x": 1}); err == nil {
		t.Fatal("allowlisted room_chat was unexpectedly dropped for the guest writer")
	}
	// Member writers are untouched by the gate.
	memberWriter := &threadSafeWriter{}
	if err := sendKanbanEvent(memberWriter, "board", map[string]any{"x": 1}); err == nil {
		t.Fatal("member writer should have attempted (and failed) the nil-conn write")
	}
	// Top-level envelopes: guests accept signaling only.
	if err := guestWriter.WriteJSON(&websocketMessage{Event: "office_pong"}); err != nil {
		t.Fatalf("non-signaling top-level frame should be dropped for guests, got write attempt: %v", err)
	}
	if err := guestWriter.WriteJSON(&websocketMessage{Event: "offer"}); err == nil {
		t.Fatal("signaling offer should pass the guest top-level gate")
	}
}

/* ---------- guest websocket containment (ws integration) ---------- */

func TestGuestWebsocketReplayWithholdsBoardMemoryAndOfficeHelloDenied(t *testing.T) {
	resetGuestSocketCapsForTest(t)
	resetAuthRateLimitersForTest()
	server := newIsolatedWebsocketServer(t)
	roomID, guestToken := mintGuestRoomAndSession(t, "Sam")

	conn, _, err := dialGuestWebsocket(t, server, guestToken)
	if err != nil {
		t.Fatalf("guest dial: %v", err)
	}

	if err := conn.WriteJSON(map[string]string{"event": "participant", "data": `{"name":"Erick"}`}); err != nil {
		t.Fatalf("send guest hello: %v", err)
	}

	// Collect every kanban event for a settle window after admission; the
	// replay plus the admission broadcasts must stay inside the allowlist and
	// must not include board/memory/notifications/proposals.
	granted := false
	grantedName := ""
	seen := map[string]bool{}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if err := conn.SetReadDeadline(time.Now().Add(700 * time.Millisecond)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			if granted {
				break // settle window drained
			}
			t.Fatalf("read while waiting for guest admission: %v", err)
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
		seen[inner.Event] = true
		if inner.Event == "access_denied" {
			t.Fatalf("guest admission denied: %s", inner.Data)
		}
		if inner.Event == "access_granted" {
			granted = true
			var grant struct {
				Name   string `json:"name"`
				RoomID string `json:"roomId"`
				Guest  bool   `json:"guest"`
			}
			if err := json.Unmarshal(inner.Data, &grant); err != nil {
				t.Fatalf("decode guest grant: %v", err)
			}
			if !grant.Guest || grant.RoomID != roomID {
				t.Fatalf("guest grant missing guest/room stamps: %+v", grant)
			}
			grantedName = grant.Name
		}
	}
	if !granted {
		t.Fatal("guest was never admitted")
	}
	// Server-side "Guest " prefix, whatever the payload claimed.
	if !strings.HasPrefix(grantedName, guestNamePrefix) || strings.Contains(grantedName, "Erick") {
		t.Fatalf("guest admitted as %q, want server-prefixed guest name", grantedName)
	}
	for _, forbidden := range []string{"board", "memory", "undo_available", "notification_backlog", "codex_proposals", "status"} {
		if seen[forbidden] {
			t.Fatalf("guest replay carried forbidden event %q (saw %v)", forbidden, seen)
		}
	}
	for event := range seen {
		if !guestWritableKanbanEvents[event] {
			t.Fatalf("guest socket received non-allowlisted event %q", event)
		}
	}
	for _, required := range []string{"participants", "room_chat_history", "meeting", "server_version"} {
		if !seen[required] {
			t.Fatalf("guest replay missing %q (saw %v)", required, seen)
		}
	}

	// The office hello from a guest is denied and the socket closed (§5.4a).
	if err := conn.WriteJSON(map[string]string{"event": "office", "data": `{}`}); err != nil {
		t.Fatalf("send guest office hello: %v", err)
	}
	deniedThenClosed := false
	for i := 0; i < 20; i++ {
		if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			deniedThenClosed = true
			break
		}
	}
	if !deniedThenClosed {
		t.Fatal("guest socket stayed open after an office hello")
	}
}

func TestGuestWebsocketRoomMismatchRejectedPreUpgrade(t *testing.T) {
	resetGuestSocketCapsForTest(t)
	server := newIsolatedWebsocketServer(t)
	_, guestToken := mintGuestRoomAndSession(t, "Sam")

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/websocket?room=office"
	header := http.Header{}
	header.Set("Cookie", guestCookieName+"="+guestToken)
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil {
		conn.Close()
		t.Fatal("guest dial into a mismatched room should fail pre-upgrade")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 pre-upgrade for room mismatch, got %+v", resp)
	}
}

func TestMemberWebsocketUnknownRoomRejectedPreUpgrade(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	token, err := userSessionStore().create("aj@shareability.com")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/websocket?room=room-doesnotexist"
	header := http.Header{}
	header.Set("Cookie", sessionCookieName+"="+token)
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil {
		conn.Close()
		t.Fatal("dial into an unknown room should fail pre-upgrade")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 pre-upgrade for unknown room, got %+v", resp)
	}
}

/* ---------- passcode admission (§4.5) ---------- */

func TestWrongRoomPasscodeDeniedThenRateLimited(t *testing.T) {
	resetAuthRateLimitersForTest()
	t.Cleanup(resetAuthRateLimitersForTest)
	server := newIsolatedWebsocketServer(t)
	room, err := appRoomStore().create("locked room", "hunter2", "aj@shareability.com", false)
	if err != nil {
		t.Fatalf("create passcoded room: %v", err)
	}

	token, err := userSessionStore().create("aj@shareability.com")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/websocket?room=" + room.ID
	header := http.Header{}
	header.Set("Cookie", sessionCookieName+"="+token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial passcoded room: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	readDenied := func() string {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if err := conn.SetReadDeadline(deadline); err != nil {
				t.Fatalf("set read deadline: %v", err)
			}
			var message websocketMessage
			if err := conn.ReadJSON(&message); err != nil {
				t.Fatalf("read waiting for access_denied: %v", err)
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
			if inner.Event == "access_granted" {
				t.Fatal("wrong passcode was admitted")
			}
			if inner.Event == "access_denied" {
				return string(inner.Data)
			}
		}
	}

	// Wrong passcode: denied with the passcode reason.
	if err := conn.WriteJSON(map[string]string{"event": "participant", "data": `{"passcode":"wrong"}`}); err != nil {
		t.Fatalf("send wrong-passcode hello: %v", err)
	}
	if denied := readDenied(); !strings.Contains(denied, "passcode") {
		t.Fatalf("denial %q does not name the passcode", denied)
	}

	// Hammer the gate past the shared limiter: the denial flips to the
	// rate-limit message even for a CORRECT passcode.
	for i := 0; i < loginAttemptLimit+1; i++ {
		if err := conn.WriteJSON(map[string]string{"event": "participant", "data": `{"passcode":"wrong"}`}); err != nil {
			t.Fatalf("send wrong-passcode hello %d: %v", i, err)
		}
		readDenied()
	}
	if err := conn.WriteJSON(map[string]string{"event": "participant", "data": `{"passcode":"hunter2"}`}); err != nil {
		t.Fatalf("send post-limit hello: %v", err)
	}
	if denied := readDenied(); !strings.Contains(denied, "too many passcode attempts") {
		t.Fatalf("post-limit denial %q, want the rate-limit message", denied)
	}

	// A fresh limiter window with the right passcode admits.
	resetAuthRateLimitersForTest()
	if err := conn.WriteJSON(map[string]string{"event": "participant", "data": `{"passcode":"hunter2"}`}); err != nil {
		t.Fatalf("send correct-passcode hello: %v", err)
	}
	waitForKanbanEvent(t, conn, "access_granted", 5*time.Second)
}

/* ---------- §6.1 caps + deferred guest PeerConnection ---------- */

func TestGuestThirdSocketOnOneSessionRejectedPreUpgrade(t *testing.T) {
	resetGuestSocketCapsForTest(t)
	server := newIsolatedWebsocketServer(t)
	_, guestToken := mintGuestRoomAndSession(t, "Sam")

	if _, _, err := dialGuestWebsocket(t, server, guestToken); err != nil {
		t.Fatalf("first guest socket: %v", err)
	}
	if _, _, err := dialGuestWebsocket(t, server, guestToken); err != nil {
		t.Fatalf("second guest socket: %v", err)
	}
	conn, resp, err := dialGuestWebsocket(t, server, guestToken)
	if err == nil {
		conn.Close()
		t.Fatal("third socket on one guest session should be rejected pre-upgrade")
	}
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for the third guest socket, got %+v", resp)
	}
}

func TestGuestPreHelloSocketsPerIPCapped(t *testing.T) {
	resetGuestSocketCapsForTest(t)
	server := newIsolatedWebsocketServer(t)
	room, err := appRoomStore().create("cap room", "", "aj@shareability.com", true)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	// maxGuestPreHelloPerIP unadmitted sockets under DISTINCT guest sessions
	// hold the IP budget; the next dial is rejected pre-upgrade.
	for i := 0; i < maxGuestPreHelloPerIP; i++ {
		token, err := userSessionStore().createGuest(room.ID, "Sam")
		if err != nil {
			t.Fatalf("create guest session %d: %v", i, err)
		}
		if _, _, err := dialGuestWebsocket(t, server, token); err != nil {
			t.Fatalf("guest socket %d: %v", i, err)
		}
	}
	token, err := userSessionStore().createGuest(room.ID, "Sam")
	if err != nil {
		t.Fatalf("create overflow guest session: %v", err)
	}
	conn, resp, err := dialGuestWebsocket(t, server, token)
	if err == nil {
		conn.Close()
		t.Fatal("fifth pre-hello guest socket from one IP should be rejected")
	}
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for the per-IP pre-hello cap, got %+v", resp)
	}
}

func TestGuestRoomSeatCapRejectsNewSessionPreUpgrade(t *testing.T) {
	resetGuestSocketCapsForTest(t)
	t.Setenv("BONFIRE_MAX_GUESTS_PER_ROOM", "1")
	server := newIsolatedWebsocketServer(t)
	roomID, firstToken := mintGuestRoomAndSession(t, "Sam")

	first, _, err := dialGuestWebsocket(t, server, firstToken)
	if err != nil {
		t.Fatalf("first guest dial: %v", err)
	}
	if err := first.WriteJSON(map[string]string{"event": "participant", "data": `{}`}); err != nil {
		t.Fatalf("first guest hello: %v", err)
	}
	waitForKanbanEvent(t, first, "access_granted", 5*time.Second)

	secondToken, err := userSessionStore().createGuest(roomID, "Pat")
	if err != nil {
		t.Fatalf("create second guest session: %v", err)
	}
	conn, resp, err := dialGuestWebsocket(t, server, secondToken)
	if err == nil {
		conn.Close()
		t.Fatal("second guest session should be rejected at the room seat cap")
	}
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 at the room guest cap, got %+v", resp)
	}
}

func TestUnadmittedGuestSocketAllocatesNoPeerConnection(t *testing.T) {
	resetGuestSocketCapsForTest(t)
	server := newIsolatedWebsocketServer(t)
	_, guestToken := mintGuestRoomAndSession(t, "Sam")

	before := websocketPeerAllocations.Load()
	conn, _, err := dialGuestWebsocket(t, server, guestToken)
	if err != nil {
		t.Fatalf("guest dial: %v", err)
	}
	// The socket is open but unadmitted: give the handler a moment, then
	// assert no PeerConnection was allocated (§6.1 deferred alloc).
	time.Sleep(200 * time.Millisecond)
	if got := websocketPeerAllocations.Load(); got != before {
		t.Fatalf("unadmitted guest socket allocated %d PeerConnection(s)", got-before)
	}

	// Admission allocates exactly one.
	if err := conn.WriteJSON(map[string]string{"event": "participant", "data": `{}`}); err != nil {
		t.Fatalf("guest hello: %v", err)
	}
	waitForKanbanEvent(t, conn, "access_granted", 5*time.Second)
	if got := websocketPeerAllocations.Load(); got != before+1 {
		t.Fatalf("admitted guest allocations=%d, want exactly one", got-before)
	}
}

/* ---------- §6.5 cost bounds ---------- */

func TestGuestChatTokenBucket(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	now := time.Now().UTC()

	// Burst of 5 passes, the 6th is rejected.
	for i := 0; i < guestChatBucketBurst; i++ {
		if !app.allowGuestRoomChat("room-x", "guest-key", now) {
			t.Fatalf("burst message %d was rejected", i)
		}
	}
	if app.allowGuestRoomChat("room-x", "guest-key", now) {
		t.Fatal("6th message inside the burst window should be rejected")
	}
	// 1 token refills per 3 seconds.
	if !app.allowGuestRoomChat("room-x", "guest-key", now.Add(guestChatBucketRefill+time.Millisecond)) {
		t.Fatal("refilled token was rejected")
	}
	if app.allowGuestRoomChat("room-x", "guest-key", now.Add(guestChatBucketRefill+2*time.Millisecond)) {
		t.Fatal("second message right after a single refill should be rejected")
	}
	// A different guest session has its own bucket.
	if !app.allowGuestRoomChat("room-x", "other-key", now) {
		t.Fatal("another session's bucket was drained by the first")
	}
}

func TestGuestTranscriptionCapFlipsRecordingOff(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	app := newKanbanBoardApp()
	roomID, _ := mintGuestRoomAndSession(t, "Sam")

	// Simulate the live sitting the cap was armed for.
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	state.mixer = newAudioMixer()
	state.mediaGen = 7
	app.mu.Unlock()
	t.Cleanup(func() {
		app.mu.Lock()
		mixer := state.mixer
		state.mixer = nil
		app.mu.Unlock()
		if mixer != nil {
			mixer.close()
		}
	})

	// A stale generation (an earlier, torn-down sitting) must be a no-op.
	app.enforceGuestTranscriptionCap(roomID, 6)
	if !app.transcriptRecordingActiveInRoom(roomID) {
		t.Fatal("stale-generation cap flipped recording off")
	}

	app.enforceGuestTranscriptionCap(roomID, 7)
	if app.transcriptRecordingActiveInRoom(roomID) {
		t.Fatal("cap did not flip recording off")
	}
	snapshot := app.roomSnapshotForRoom(roomID)
	recording, _ := snapshot["recording"].(roomRecordingState)
	if recording.UpdatedBy != guestTranscriptionCapActor {
		t.Fatalf("recording flipped by %q, want %q", recording.UpdatedBy, guestTranscriptionCapActor)
	}

	// A member flipping it back on grants another cap window (a fresh timer).
	app.setTranscriptRecordingInRoom(roomID, true, "AJ")
	if !app.transcriptRecordingActiveInRoom(roomID) {
		t.Fatal("member could not re-enable recording")
	}
	app.mu.Lock()
	rearmed := state.capTimer != nil
	if state.capTimer != nil {
		state.capTimer.Stop()
		state.capTimer = nil
	}
	app.mu.Unlock()
	if !rearmed {
		t.Fatal("re-enabling recording did not arm a fresh transcription-cap window")
	}
	// The office never arms a cap.
	if app.transcriptRecordingActiveInRoom(officeRoomID) != true {
		t.Fatal("office recording state disturbed")
	}
}

/* ---------- guest seats, dedupe, attribution ---------- */

func TestTwoGuestsNamedSamCoexist(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()

	first, firstNew, err := app.admitGuestParticipant("room-x", "session-a", "Sam", "sock-a")
	if err != nil {
		t.Fatalf("admit first Sam: %v", err)
	}
	second, secondNew, err := app.admitGuestParticipant("room-x", "session-b", "Sam", "sock-b")
	if err != nil {
		t.Fatalf("admit second Sam: %v", err)
	}
	if first != "Guest Sam" || second != "Guest Sam 2" {
		t.Fatalf("guest names %q / %q, want deduped Guest Sam / Guest Sam 2", first, second)
	}
	if !firstNew || !secondNew {
		t.Fatal("both guests should be first endpoints of their seats")
	}
	if count := app.activeParticipantCount("room-x"); count != 2 {
		t.Fatalf("room seats=%d, want 2", count)
	}

	// A second socket under the FIRST session shares its seat (no eviction,
	// same display name, still 2 seats).
	again, againNew, err := app.admitGuestParticipant("room-x", "session-a", "Sam", "sock-a2")
	if err != nil {
		t.Fatalf("re-admit first session: %v", err)
	}
	if again != "Guest Sam" || againNew {
		t.Fatalf("second socket got name=%q firstEndpoint=%t, want the shared seat", again, againNew)
	}
	if count := app.activeParticipantCount("room-x"); count != 2 {
		t.Fatalf("room seats=%d after second socket, want 2", count)
	}
}

func TestGuestTranscriptAttributionStoredAsGuestName(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	roomID := "room-attrib"
	guestSessionKey := strings.Repeat("a", 64)
	sittingID := app.prepareMeetingSittingID(roomID)

	admitted, _, err := app.admitGuestWithAnchor(context.Background(), roomID, guestSessionKey, "Sam", "sock-a", sittingID)
	if err != nil {
		t.Fatalf("admit guest: %v", err)
	}
	if admitted != "Guest Sam" {
		t.Fatalf("admitted=%q", admitted)
	}
	if got := app.noteMeetingAdmissionForSitting(roomID, admitted, sittingID); got != sittingID {
		t.Fatalf("opened sitting=%q want=%q", got, sittingID)
	}
	enableFullTranscriptConsentForTest(t, app, guestAdmissionPrincipal(guestSessionKey), roomID, sittingID)

	// Feed the room's attribution state exactly as its mixer listener would.
	now := time.Now().UTC()
	app.noteAudioActivityForRoom(roomID, now, []audioActivityLevel{{
		TrackKey: "t1", ParticipantName: "Guest Sam", RMS: 900,
	}})
	app.noteRealtimeSpeechStartedForRoom(roomID)
	app.noteRealtimeSpeechStoppedForRoom(roomID)
	app.freezeAttributionWindowAtCommitForRoom(roomID)

	app.rememberTranscript(roomID, kanbanRealtimeEvent{
		EventID:    "evt-1",
		Transcript: "hello from the guest",
	}, "transcript_lane", "test-model")

	entries := app.memory.snapshot(0)
	var stored *meetingMemoryEntry
	for i := range entries {
		if entries[i].Kind == meetingMemoryKindTranscript && strings.Contains(entries[i].Text, "hello from the guest") {
			stored = &entries[i]
		}
	}
	if stored == nil {
		t.Fatal("guest transcript was not persisted")
	}
	if speaker := stored.Metadata["speaker"]; speaker != "Guest Sam" {
		t.Fatalf("stored speaker=%q, want Guest Sam", speaker)
	}
	if stored.Metadata["roomId"] != roomID {
		t.Fatalf("stored roomId=%q, want %s", stored.Metadata["roomId"], roomID)
	}
}

/* ---------- cross-room eviction (§2) ---------- */

func TestCrossRoomAccountEvictionWithoutCrossRoomTrackTeardown(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}
	ajTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "aj-video", "aj-stream")
	if err != nil {
		t.Fatalf("create AJ track: %v", err)
	}
	timTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "tim-video", "tim-stream")
	if err != nil {
		t.Fatalf("create Tim track: %v", err)
	}

	snapshotPeerState(t)
	listLock.Lock()
	peerConnections = []peerConnectionState{
		{participantName: "AJ", sessionID: "aj-roomA", roomID: "room-a"},
		{participantName: "Tim", sessionID: "tim-roomB", roomID: "room-b"},
	}
	activeParticipantConnections = map[string]peerConnectionState{
		"AJ":  {participantName: "AJ", sessionID: "aj-roomA", roomID: "room-a"},
		"Tim": {participantName: "Tim", sessionID: "tim-roomB", roomID: "room-b"},
	}
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{ajTrack.ID(): ajTrack, timTrack.ID(): timTrack}
	trackParticipants = map[string]string{ajTrack.ID(): "AJ", timTrack.ID(): "Tim"}
	trackParticipantSessions = map[string]string{ajTrack.ID(): "aj-roomA", timTrack.ID(): "tim-roomB"}
	trackRooms = map[string]string{ajTrack.ID(): "room-a", timTrack.ID(): "room-b"}
	trackSourceIDs = map[string]string{}
	trackLayerRIDs = map[string]string{}
	trackLayerGroups = map[string]string{}
	subscriberLayerTiers = map[string]string{}
	listLock.Unlock()

	if _, _, err := app.admitParticipantSessionEndpointInRoom("room-a", "AJ", "aj-roomA", "endpoint-1"); err != nil {
		t.Fatalf("admit AJ into room A: %v", err)
	}
	if _, _, err := app.admitParticipantSessionEndpointInRoom("room-b", "AJ", "aj-roomB", "endpoint-1"); err != nil {
		t.Fatalf("admit AJ into room B: %v", err)
	}

	app.evictAccountFromOtherRooms("AJ", "room-b")

	if app.participantSessionCurrentInRoom("room-a", "AJ", "aj-roomA") {
		t.Fatal("room A seat survived the cross-room eviction")
	}
	if !app.participantSessionCurrentInRoom("room-b", "AJ", "aj-roomB") {
		t.Fatal("room B seat was wrongly evicted")
	}

	listLock.RLock()
	defer listLock.RUnlock()
	if _, ok := trackLocals[ajTrack.ID()]; ok {
		t.Fatal("evicted seat's room A track was not pruned")
	}
	if _, ok := trackLocals[timTrack.ID()]; !ok {
		t.Fatal("cross-room eviction tore down room B's unrelated track")
	}
}

/* ---------- lazy media lifecycle + mediaGen (§4.4) ---------- */

func TestOfficeMediaFirstStartIsPositiveAndConcurrentIdempotent(t *testing.T) {
	resetRoomMediaActorsForTest(t)
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("MEETING_TRANSCRIPT_LANE_ENABLED", "0")
	app := newKanbanBoardApp()

	const admissions = 32
	start := make(chan struct{})
	generations := make(chan uint64, admissions)
	var wait sync.WaitGroup
	for range admissions {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			generations <- app.ensureRoomMedia(officeRoomID)
		}()
	}
	close(start)
	wait.Wait()
	close(generations)
	for generation := range generations {
		if generation != 1 {
			t.Fatalf("concurrent office admission captured generation=%d, want 1", generation)
		}
	}
	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	generation, actor := state.mediaGen, state.mediaActor
	app.mu.Unlock()
	if generation != 1 || actor == nil || actor.generation != generation {
		t.Fatalf("office media owner mismatch generation=%d actor=%+v", generation, actor)
	}
}

func TestOfficeStaleStartupCannotPublishOrWedgeSuccessor(t *testing.T) {
	resetRoomMediaActorsForTest(t)
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	firstGeneration := app.ensureRoomMedia(officeRoomID)

	entered := make(chan struct{})
	release := make(chan struct{})
	officeRealtimeBeforePublishProbe = func() { close(entered); <-release }
	t.Cleanup(func() { officeRealtimeBeforePublishProbe = nil })
	app.mu.Lock()
	app.realtimeStarting = true
	app.realtimeStartToken++
	token := app.realtimeStartToken
	app.mu.Unlock()
	result := make(chan error, 1)
	go func() { result <- app.startRealtimePeer("test-key", "test-model", firstGeneration, token) }()
	<-entered
	app.teardownOfficeMediaAfterIdle()
	successorGeneration := app.ensureRoomMedia(officeRoomID)
	close(release)
	if err := <-result; !errors.Is(err, ErrRoomScoutFence) {
		t.Fatalf("stale peer startup err=%v, want generation fence", err)
	}
	app.mu.Lock()
	peer, starting := app.pc, app.realtimeStarting
	state := app.roomLiveLocked(officeRoomID)
	app.mu.Unlock()
	if peer != nil || starting || successorGeneration <= firstGeneration || state.mediaActor == nil || state.mediaGen != successorGeneration {
		t.Fatalf("stale startup wedged successor peer=%v starting=%v first=%d successor=%d stateGen=%d", peer, starting, firstGeneration, successorGeneration, state.mediaGen)
	}

	transcriptEntered := make(chan struct{})
	transcriptRelease := make(chan struct{})
	officeTranscriptionBeforePublishProbe = func() { close(transcriptEntered); <-transcriptRelease }
	t.Cleanup(func() { officeTranscriptionBeforePublishProbe = nil })
	app.mu.Lock()
	app.transcriptionStarting = true
	app.transcriptionStartToken++
	laneToken := app.transcriptionStartToken
	app.mu.Unlock()
	laneDone := make(chan struct{})
	go func() {
		app.startTranscriptionLane("test-key", successorGeneration, laneToken)
		close(laneDone)
	}()
	<-transcriptEntered
	app.teardownOfficeMediaAfterIdle()
	finalGeneration := app.ensureRoomMedia(officeRoomID)
	close(transcriptRelease)
	<-laneDone
	app.mu.Lock()
	lane, laneStarting := app.transcriptLane, app.transcriptionStarting
	app.mu.Unlock()
	if lane != nil || laneStarting || finalGeneration <= successorGeneration {
		t.Fatalf("stale lane startup wedged successor lane=%v starting=%v generation=%d", lane, laneStarting, finalGeneration)
	}
}

func TestStaleTranscriptionEventCannotConsumeSuccessorWindow(t *testing.T) {
	resetRoomMediaActorsForTest(t)
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	app.memory.ensureMeetingID(officeRoomID)
	generation := app.ensureRoomMedia(officeRoomID)
	app.mu.Lock()
	scope := RoomScoutScope{RoomID: officeRoomID, SittingID: app.roomLiveLocked(officeRoomID).mediaSittingID, MediaGeneration: generation}
	app.mu.Unlock()

	entered := make(chan struct{})
	release := make(chan struct{})
	transcriptionEventAfterScopeProbe = func() { close(entered); <-release }
	t.Cleanup(func() { transcriptionEventAfterScopeProbe = nil })
	done := make(chan struct{})
	go func() {
		app.handleTranscriptionLaneEventForScope(scope, []byte(`{"type":"conversation.item.input_audio_transcription.failed"}`), "test-model")
		close(done)
	}()
	<-entered
	app.teardownOfficeMediaAfterIdle()
	successorGeneration := app.ensureRoomMedia(officeRoomID)
	app.mu.Lock()
	successorState := app.roomLiveLocked(officeRoomID)
	successorScope := RoomScoutScope{RoomID: officeRoomID, SittingID: successorState.mediaSittingID, MediaGeneration: successorGeneration}
	successorState.pendingAttributionWindows = []attributionWindow{{startedAt: time.Now().UTC(), stoppedAt: time.Now().UTC()}}
	app.mu.Unlock()
	app.pushTranscriptionSegmentSecondsForLaneScope(successorScope, 7)
	app.pushTranscriptionSegmentSecondsForLaneScope(scope, 99)
	app.resetTranscriptionSegmentSecondsForLaneScope(scope)
	app.noteRealtimeSpeechStartedForScope(successorScope)
	app.discardRealtimeSpeechForScope(scope)
	close(release)
	<-done
	app.mu.Lock()
	finalState := app.roomLiveLocked(officeRoomID)
	remaining := len(finalState.pendingAttributionWindows)
	successorSpeechRetained := !finalState.currentSpeechStartedAt.IsZero()
	app.mu.Unlock()
	if remaining != 1 {
		t.Fatalf("stale transcription event consumed successor window: remaining=%d", remaining)
	}
	if !successorSpeechRetained {
		t.Fatal("stale transcription scope cleared successor speech markers")
	}
	if seconds := app.popTranscriptionSegmentSecondsForLaneScope(successorScope); seconds != 7 {
		t.Fatalf("stale transcription scope mutated successor meter FIFO: seconds=%v", seconds)
	}
}

func TestTranscriptPersistenceScopeRolloverCannotConsumeSuccessorWindow(t *testing.T) {
	resetRoomMediaActorsForTest(t)
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("MEETING_TRANSCRIPT_LANE_ENABLED", "0")
	app := newKanbanBoardApp()
	app.memory.ensureMeetingID(officeRoomID)
	generation := app.ensureRoomMedia(officeRoomID)
	app.mu.Lock()
	scope := RoomScoutScope{
		RoomID:          officeRoomID,
		SittingID:       app.roomLiveLocked(officeRoomID).mediaSittingID,
		MediaGeneration: generation,
	}
	app.roomLiveLocked(officeRoomID).pendingAttributionWindows = []attributionWindow{{startedAt: time.Now().UTC(), stoppedAt: time.Now().UTC()}}
	app.mu.Unlock()

	entered := make(chan struct{})
	release := make(chan struct{})
	transcriptPersistenceAfterScopeProbe = func() { close(entered); <-release }
	t.Cleanup(func() { transcriptPersistenceAfterScopeProbe = nil })
	done := make(chan struct{})
	go func() {
		app.rememberTranscriptForMediaScope(scope, kanbanRealtimeEvent{
			EventID:    "stale-persistence-event",
			ItemID:     "stale-persistence-item",
			Transcript: "This stale transcript must not enter the successor sitting.",
		}, "transcript_lane", "test-model")
		close(done)
	}()
	<-entered

	app.teardownOfficeMediaAfterIdle()
	successorGeneration := app.ensureRoomMedia(officeRoomID)
	app.mu.Lock()
	successorState := app.roomLiveLocked(officeRoomID)
	successorState.pendingAttributionWindows = []attributionWindow{{startedAt: time.Now().UTC(), stoppedAt: time.Now().UTC()}}
	successorSittingID := successorState.mediaSittingID
	app.mu.Unlock()
	close(release)
	<-done

	app.mu.Lock()
	remaining := len(app.roomLiveLocked(officeRoomID).pendingAttributionWindows)
	app.mu.Unlock()
	if remaining != 1 {
		t.Fatalf("stale persistence consumed successor attribution window: remaining=%d", remaining)
	}
	if successorGeneration <= generation || successorSittingID == "" {
		t.Fatalf("successor scope not installed: old=%d successor=%d sitting=%q", generation, successorGeneration, successorSittingID)
	}
	for _, entry := range app.memory.snapshot(0) {
		if entry.ID == "stale-persistence-event" || entry.Metadata["eventId"] == "stale-persistence-event" || strings.Contains(entry.Text, "stale transcript") {
			t.Fatalf("stale scoped transcript persisted into memory: %+v", entry)
		}
	}
}

func TestRestartRealtimePeerRolloverRetriggersSuccessorOwner(t *testing.T) {
	resetRoomMediaActorsForTest(t)
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("MEETING_TRANSCRIPT_LANE_ENABLED", "0")
	app := newKanbanBoardApp()
	app.memory.ensureMeetingID(officeRoomID)
	firstGeneration := app.ensureRoomMedia(officeRoomID)
	oldPeer, err := newPeerConnection()
	if err != nil {
		t.Fatalf("create old realtime peer: %v", err)
	}
	app.mu.Lock()
	app.apiKey = "test-key"
	app.pc = oldPeer
	app.realtimeMediaGen = firstGeneration
	app.mu.Unlock()

	entered := make(chan struct{})
	release := make(chan struct{})
	connectHold := make(chan struct{})
	var releaseOnce sync.Once
	var connectOnce sync.Once
	var publishCalls atomic.Int32
	previousPublishProbe := officeRealtimeBeforePublishProbe
	previousConnectProbe := officeRealtimeBeforeConnectProbe
	officeRealtimeBeforePublishProbe = func() {
		if publishCalls.Add(1) == 1 {
			close(entered)
			<-release
		}
	}
	officeRealtimeBeforeConnectProbe = func() bool {
		<-connectHold
		return true
	}
	t.Cleanup(func() {
		officeRealtimeBeforePublishProbe = previousPublishProbe
		officeRealtimeBeforeConnectProbe = previousConnectProbe
		releaseOnce.Do(func() { close(release) })
		_ = app.Close()
		connectOnce.Do(func() { close(connectHold) })
	})

	restartDone := make(chan struct{})
	go func() {
		app.restartRealtimePeer("scope-rollover-test")
		close(restartDone)
	}()
	<-entered
	app.mu.Lock()
	oldRestartToken := app.realtimeRestartToken
	oldStartToken := app.realtimeStartToken
	app.apiKey = ""
	app.mu.Unlock()
	if oldRestartToken == 0 || oldStartToken == 0 {
		t.Fatalf("restart did not install tokens: restart=%d start=%d", oldRestartToken, oldStartToken)
	}

	app.teardownOfficeMediaAfterIdle()
	successorGeneration := app.ensureRoomMedia(officeRoomID)
	app.mu.Lock()
	if app.pc != nil {
		app.mu.Unlock()
		t.Fatal("successor unexpectedly started before stale restart released")
	}
	app.apiKey = "test-key"
	app.mu.Unlock()
	releaseOnce.Do(func() { close(release) })
	<-restartDone

	deadline := time.Now().Add(3 * time.Second)
	var successorPeer *webrtc.PeerConnection
	for time.Now().Before(deadline) {
		app.mu.Lock()
		successorPeer = app.pc
		currentGeneration := app.realtimeMediaGen
		starting := app.realtimeStarting
		app.mu.Unlock()
		if successorPeer != nil && currentGeneration == successorGeneration && !starting {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	app.mu.Lock()
	currentPeer := app.pc
	currentGeneration := app.realtimeMediaGen
	currentRestartToken := app.realtimeRestartToken
	currentStartToken := app.realtimeStartToken
	state := app.roomLiveLocked(officeRoomID)
	app.mu.Unlock()
	if currentPeer == nil || currentPeer != successorPeer || currentPeer == oldPeer || currentGeneration != successorGeneration || state.mediaActor == nil || state.mediaGen != successorGeneration {
		t.Fatalf("automatic restart did not publish successor owner: peer=%p successor=%p old=%p realtimeGen=%d stateGen=%d successorGen=%d", currentPeer, successorPeer, oldPeer, currentGeneration, state.mediaGen, successorGeneration)
	}
	if currentRestartToken == oldRestartToken || currentStartToken == oldStartToken {
		t.Fatalf("stale restart tokens remained authoritative: restart=%d start=%d", currentRestartToken, currentStartToken)
	}
	if oldPeer.ConnectionState() != webrtc.PeerConnectionStateClosed {
		t.Fatalf("old peer state=%s, want closed", oldPeer.ConnectionState())
	}

	app.restartRealtimePeerIfStill(oldPeer, webrtc.PeerConnectionStateClosed, 0, "stale-old-peer")
	app.mu.Lock()
	afterStaleCallback := app.pc
	app.mu.Unlock()
	if afterStaleCallback != successorPeer {
		t.Fatalf("old peer callback displaced successor: got=%p want=%p", afterStaleCallback, successorPeer)
	}
}

func TestOfficeMediaTeardownRejoinAdvancesGenerationAndDeniesStaleOwner(t *testing.T) {
	resetRoomMediaActorsForTest(t)
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("MEETING_TRANSCRIPT_LANE_ENABLED", "0")
	app := newKanbanBoardApp()
	firstGeneration := app.ensureRoomMedia(officeRoomID)
	if firstGeneration == 0 {
		t.Fatal("first office media generation remained zero")
	}
	oldActor := roomMediaActorForGeneration(officeRoomID, firstGeneration)
	if oldActor == nil {
		t.Fatal("first office generation had no actor")
	}
	app.mu.Lock()
	seedState := app.roomLiveLocked(officeRoomID)
	seedState.audioActivity = []participantAudioFrame{{At: time.Now().UTC(), EnergyByParticipant: map[string]float64{"Old": 1}}}
	seedState.currentSpeechStartedAt = time.Now().UTC()
	seedState.currentSpeechStoppedAt = time.Now().UTC()
	seedState.pendingAttributionWindows = []attributionWindow{{startedAt: time.Now().UTC(), stoppedAt: time.Now().UTC()}}
	app.mu.Unlock()

	app.teardownOfficeMediaAfterIdle()
	teardownGeneration := app.roomMediaGeneration(officeRoomID)
	if teardownGeneration <= firstGeneration {
		t.Fatalf("teardown generation=%d, want > %d", teardownGeneration, firstGeneration)
	}
	app.mu.Lock()
	clearedState := app.roomLiveLocked(officeRoomID)
	retainedAttribution := len(clearedState.audioActivity) != 0 || !clearedState.currentSpeechStartedAt.IsZero() || !clearedState.currentSpeechStoppedAt.IsZero() || len(clearedState.pendingAttributionWindows) != 0
	app.mu.Unlock()
	if retainedAttribution {
		t.Fatal("office teardown retained prior-sitting attribution state")
	}
	if roomMediaActorForGeneration(officeRoomID, firstGeneration) != nil || requestRoomMediaCommandForGeneration(officeRoomID, firstGeneration, roomMediaCommandAdmit) {
		t.Fatal("old office generation remained authorized after teardown")
	}

	rejoinGeneration := app.ensureRoomMedia(officeRoomID)
	if rejoinGeneration <= teardownGeneration {
		t.Fatalf("rejoin generation=%d, want > teardown generation %d", rejoinGeneration, teardownGeneration)
	}
	if actor := roomMediaActorForGeneration(officeRoomID, rejoinGeneration); actor == nil || actor == oldActor {
		t.Fatalf("rejoin did not install a fresh generation owner: actor=%+v", actor)
	}
	if !app.roomMediaGenerationCurrent(officeRoomID, rejoinGeneration) || app.roomMediaGenerationCurrent(officeRoomID, firstGeneration) {
		t.Fatal("office generation-current fence accepted the stale sitting")
	}

	lane := newMeetingTranscriptionLaneForRoomGeneration(app, "test-key", "test-model", officeRoomID, rejoinGeneration)
	if lane.mediaGeneration != rejoinGeneration {
		t.Fatalf("transcript lane generation=%d, want %d", lane.mediaGeneration, rejoinGeneration)
	}
	app.mu.Lock()
	app.realtimeMediaGen = rejoinGeneration
	app.mu.Unlock()
	if got := app.currentRealtimeMediaGeneration(); got != rejoinGeneration {
		t.Fatalf("Scout generation=%d, want %d", got, rejoinGeneration)
	}
	staleSpeechStarted := []byte(`{"type":"input_audio_buffer.speech_started"}`)
	app.handleRealtimeEventForGeneration(firstGeneration, staleSpeechStarted)
	app.handleTranscriptionLaneEventForRoomGeneration(officeRoomID, firstGeneration, staleSpeechStarted, "test-model")
	app.mu.Lock()
	staleAttribution := app.roomLiveLocked(officeRoomID).currentSpeechStartedAt
	app.mu.Unlock()
	if !staleAttribution.IsZero() {
		t.Fatal("stale Scout/transcript generation changed successor attribution")
	}
	app.handleTranscriptionLaneEventForRoomGeneration(officeRoomID, rejoinGeneration, staleSpeechStarted, "test-model")
	app.mu.Lock()
	currentAttribution := app.roomLiveLocked(officeRoomID).currentSpeechStartedAt
	app.roomLiveLocked(officeRoomID).currentSpeechStartedAt = time.Time{}
	app.mu.Unlock()
	if currentAttribution.IsZero() {
		t.Fatal("current transcript generation did not reach attribution")
	}

	currentWriter := mediaSoakTestWriter(t)
	staleWriter := mediaSoakTestWriter(t)
	listLock.Lock()
	previousPeers := peerConnections
	peerConnections = []peerConnectionState{
		{sessionID: "office-current-generation", roomID: officeRoomID, sittingID: "office-sitting", mediaGeneration: rejoinGeneration, websocket: currentWriter},
		{sessionID: "office-stale-generation", roomID: officeRoomID, sittingID: "office-sitting", mediaGeneration: firstGeneration, websocket: staleWriter},
	}
	listLock.Unlock()
	t.Cleanup(func() {
		listLock.Lock()
		peerConnections = previousPeers
		listLock.Unlock()
	})
	acknowledgements, err := broadcastScopedRoomKanbanEventAcknowledged(RoomScoutScope{
		RoomID: officeRoomID, SittingID: "office-sitting", MediaGeneration: rejoinGeneration,
	}, "room_chat", map[string]any{"id": "office-generation-proof"})
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]scopedRoomDeliveryAcknowledgement{}
	for _, acknowledgement := range acknowledgements {
		results[acknowledgement.SessionID] = acknowledgement
	}
	if !results["office-current-generation"].Authorized || !results["office-current-generation"].Delivered {
		t.Fatalf("current office generation was denied: %+v", results)
	}
	if results["office-stale-generation"].Authorized || results["office-stale-generation"].Delivered {
		t.Fatalf("stale office generation received publication: %+v", results)
	}
}

func TestNamedRoomMediaLazyLifecycle(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	roomID := "room-media"

	// No media before first admission.
	if app.roomMixerFor(roomID) != nil {
		t.Fatal("named room had a mixer before any admission")
	}

	if _, _, err := app.admitParticipantSessionEndpointInRoom(roomID, "AJ", "aj-1", "endpoint-1"); err != nil {
		t.Fatalf("admit: %v", err)
	}
	app.ensureRoomMedia(roomID)
	mixer := app.roomMixerFor(roomID)
	if mixer == nil {
		t.Fatal("first admission did not create the room mixer")
	}
	app.mu.Lock()
	genAfterCreate := app.roomLiveLocked(roomID).mediaGen
	app.mu.Unlock()

	// Occupied room: teardown is refused (an admission raced the close).
	app.teardownRoomMediaAfterIdle(roomID)
	if app.roomMixerFor(roomID) != mixer {
		t.Fatal("teardown ran while the room was occupied")
	}

	// Empty room: teardown closes media and bumps the generation fence.
	if removed, _ := app.forgetParticipantSessionResultInRoom(roomID, "AJ", "aj-1"); !removed {
		t.Fatal("could not clear the seat")
	}
	app.teardownRoomMediaAfterIdle(roomID)
	if app.roomMixerFor(roomID) != nil {
		t.Fatal("teardown left the mixer installed")
	}
	app.mu.Lock()
	genAfterTeardown := app.roomLiveLocked(roomID).mediaGen
	app.mu.Unlock()
	if genAfterTeardown <= genAfterCreate {
		t.Fatalf("mediaGen %d after teardown, want > %d", genAfterTeardown, genAfterCreate)
	}

	// Rejoin after teardown restarts media on a fresh generation.
	if _, _, err := app.admitParticipantSessionEndpointInRoom(roomID, "AJ", "aj-2", "endpoint-1"); err != nil {
		t.Fatalf("re-admit: %v", err)
	}
	app.ensureRoomMedia(roomID)
	if app.roomMixerFor(roomID) == nil {
		t.Fatal("rejoin after teardown did not restart media")
	}
	app.mu.Lock()
	genAfterRestart := app.roomLiveLocked(roomID).mediaGen
	state := app.roomLiveLocked(roomID)
	app.mu.Unlock()
	if genAfterRestart <= genAfterTeardown {
		t.Fatalf("mediaGen %d after restart, want > %d", genAfterRestart, genAfterTeardown)
	}

	// Cleanup.
	app.mu.Lock()
	mixerToClose := state.mixer
	state.mixer = nil
	app.mu.Unlock()
	if mixerToClose != nil {
		mixerToClose.close()
	}
}

// TestNamedRoomMediaTeardownVsRejoinRace drives concurrent rejoins against
// teardowns under -race: the fence must never leave an occupied room without
// media or a torn-down room with a live mixer.
func TestNamedRoomMediaTeardownVsRejoinRace(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	roomID := "room-race"

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			sessionID := "session-" + strings.Repeat("x", i%3+1)
			if _, _, err := app.admitParticipantSessionEndpointInRoom(roomID, "AJ", sessionID, "endpoint-1"); err == nil {
				app.ensureRoomMedia(roomID)
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			app.forgetParticipantSessionResultInRoom(roomID, "AJ", "session-"+strings.Repeat("x", i%3+1))
			app.teardownRoomMediaAfterIdle(roomID)
		}(i)
	}
	wg.Wait()

	// Settle: clear the seat, tear down, and assert the invariant.
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	for name := range state.participants {
		delete(state.participants, name)
		delete(state.participantCounts, name)
		delete(state.participantEndpoints, name)
		delete(state.participantMedia, name)
	}
	app.mu.Unlock()
	app.teardownRoomMediaAfterIdle(roomID)
	if app.roomMixerFor(roomID) != nil {
		t.Fatal("empty room kept a live mixer after final teardown")
	}
}

/* ---------- per-room zombie reap ---------- */

func TestLivenessReapIsRoomScoped(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	snapshotPeerState(t)

	if _, _, err := app.admitParticipantSessionEndpointInRoom("room-a", "AJ", "aj-1", "endpoint-1"); err != nil {
		t.Fatalf("admit AJ room A: %v", err)
	}
	if _, _, err := app.admitParticipantSessionEndpointInRoom("room-b", "Tim", "tim-1", "endpoint-1"); err != nil {
		t.Fatalf("admit Tim room B: %v", err)
	}

	// AJ's room A endpoint stamp goes stale; Tim stays fresh in room B.
	app.mu.Lock()
	staleAt := time.Now().UTC().Add(-participantLivenessTimeout - time.Minute)
	roomA := app.roomLiveLocked("room-a")
	roomA.participants["AJ"] = staleAt
	roomA.participantSessionLiveness["AJ"]["aj-1"] = staleAt
	app.mu.Unlock()

	app.sweepStaleParticipantSessions()

	if app.activeParticipantCount("room-a") != 0 {
		t.Fatal("room A zombie was not reaped")
	}
	if app.activeParticipantCount("room-b") != 1 {
		t.Fatal("room B's fresh participant was reaped by room A's sweep")
	}
}
