package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

/* ---------- Wave 6 in-call: reactions, raised hands, host controls ---------- */

// newInCallRoomServer boots the isolated websocket server on a throwaway room
// registry and creates one guest-enabled named room owned by AJ.
func newInCallRoomServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	t.Setenv("BONFIRE_ROOMS_PATH", filepath.Join(t.TempDir(), "rooms.json"))
	resetGuestSocketCapsForTest(t)
	resetAuthRateLimitersForTest()
	server := newIsolatedWebsocketServer(t)
	room, err := appRoomStore().create("in-call room", "", "aj@shareability.com", true)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	return server, room.ID
}

func dialInCallMember(t *testing.T, server *httptest.Server, email, roomID string) *websocket.Conn {
	t.Helper()
	token, err := userSessionStore().create(email)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/websocket?room=" + roomID
	header := http.Header{}
	header.Set("Cookie", sessionCookieName+"="+token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial %s into %s: %v", email, roomID, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// admitInCallMember sends the hello, waits for the grant, and joins media so
// the socket sits in the room fan-out pool.
func admitInCallMember(t *testing.T, conn *websocket.Conn, roomID string) string {
	t.Helper()
	writeNativeWebsocketEvent(t, conn, "participant", map[string]any{})
	name := joinInCallMedia(t, conn, roomID)
	return name
}

// joinInCallMedia waits for the grant already requested on conn, sends
// media_ready, and blocks until the socket is registered in the room's
// fan-out pool — the only state that makes a later broadcast reach it.
func joinInCallMedia(t *testing.T, conn *websocket.Conn, roomID string) string {
	t.Helper()
	raw := waitForKanbanEvent(t, conn, "access_granted", 5*time.Second)
	var grant struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &grant); err != nil {
		t.Fatalf("decode grant: %v", err)
	}
	writeNativeWebsocketEvent(t, conn, "media_ready", map[string]any{})
	deadline := time.Now().Add(5 * time.Second)
	for {
		pooled := false
		listLock.RLock()
		for _, state := range peerConnections {
			if state.websocket != nil && sameParticipantName(state.participantName, grant.Name) && normalizeRoomID(state.roomID) == normalizeRoomID(roomID) {
				pooled = true
			}
		}
		listLock.RUnlock()
		if pooled {
			return grant.Name
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never entered the %s fan-out pool", grant.Name, roomID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// readKanbanEventWithin returns the next kanban event of the given name, or
// ("", nil) when the deadline passes without it. Unlike waitForKanbanEvent it
// does not fail on access_denied — lock tests need to observe exactly that.
func readKanbanEventWithin(t *testing.T, conn *websocket.Conn, event string, timeout time.Duration) (string, json.RawMessage) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			return "", nil
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
		if inner.Event == event || inner.Event == "access_denied" {
			return inner.Event, inner.Data
		}
	}
	return "", nil
}

// assertNoRoomEventBeforeSentinel proves conn did NOT receive `forbidden`:
// it lowers conn's own hand (an allowlisted room_hand that always echoes back
// to conn's room) and drains until that sentinel arrives. Ordered delivery
// means every earlier frame for the socket was already read. A read deadline
// is deliberately avoided: gorilla marks a timed-out connection permanently
// unreadable.
func assertNoRoomEventBeforeSentinel(t *testing.T, conn *websocket.Conn, forbidden string) {
	t.Helper()
	if err := conn.WriteJSON(map[string]string{"event": "room_hand", "data": `{"raised":false}`}); err != nil {
		t.Fatalf("send sentinel: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("read waiting for sentinel: %v", err)
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
		if inner.Event == forbidden {
			t.Fatalf("socket received forbidden %s: %s", forbidden, inner.Data)
		}
		if inner.Event == "room_hand" {
			return
		}
	}
}

func TestRoomReactionAllowlistAndRateLimit(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpointInRoom(officeRoomID, "Tom", "tom-session", "tom-endpoint"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := app.recordRoomReaction(officeRoomID, "Tom", "tom-session", "💩", now); err != errRoomReactionUnknown {
		t.Fatalf("off-allowlist emoji error=%v, want %v", err, errRoomReactionUnknown)
	}
	if _, err := app.recordRoomReaction(officeRoomID, "Tim", "tim-session", "👍", now); err != errRoomParticipantAbsent {
		t.Fatalf("absent participant error=%v, want %v", err, errRoomParticipantAbsent)
	}
	payload, err := app.recordRoomReaction(officeRoomID, "Tom", "tom-session", " 👍 ", now)
	if err != nil {
		t.Fatalf("first reaction: %v", err)
	}
	if payload["emoji"] != "👍" || payload["name"] != "Tom" || payload["participantId"] != "tom-session" || payload["roomId"] != officeRoomID {
		t.Fatalf("reaction payload=%+v", payload)
	}
	if _, err := time.Parse(time.RFC3339Nano, asString(payload["at"])); err != nil {
		t.Fatalf("reaction at=%q not RFC3339Nano: %v", payload["at"], err)
	}
	if _, err := app.recordRoomReaction(officeRoomID, "Tom", "tom-session", "❤️", now.Add(500*time.Millisecond)); err != errRoomReactionTooFast {
		t.Fatalf("second reaction within 1s error=%v, want %v", err, errRoomReactionTooFast)
	}
	if _, err := app.recordRoomReaction(officeRoomID, "Tom", "tom-session", "❤️", now.Add(roomReactionMinInterval)); err != nil {
		t.Fatalf("reaction after the interval: %v", err)
	}
	for _, emoji := range roomReactionAllowlist {
		if _, ok := roomReactionAllowed(emoji); !ok {
			t.Fatalf("allowlisted emoji %q refused", emoji)
		}
	}
	if len(roomReactionAllowlist) != 8 {
		t.Fatalf("allowlist size=%d, want 8", len(roomReactionAllowlist))
	}
}

func TestRoomHandRaiseOrdersRosterAndClearsOnLeave(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	for _, seat := range [][3]string{{"Tom", "tom-session", "tom-endpoint"}, {"Tim", "tim-session", "tim-endpoint"}} {
		if _, _, err := app.admitParticipantSessionEndpointInRoom(officeRoomID, seat[0], seat[1], seat[2]); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now()
	if _, _, err := app.setRoomHandRaised(officeRoomID, "Erick", "erick-session", true, base); err != errRoomParticipantAbsent {
		t.Fatalf("absent hand raise error=%v, want %v", err, errRoomParticipantAbsent)
	}
	payload, snapshot, err := app.setRoomHandRaised(officeRoomID, "Tim", "tim-session", true, base)
	if err != nil {
		t.Fatal(err)
	}
	if payload["raised"] != true || payload["name"] != "Tim" || payload["participantId"] != "tim-session" {
		t.Fatalf("hand payload=%+v", payload)
	}
	if _, _, err := app.setRoomHandRaised(officeRoomID, "Tom", "tom-session", true, base.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// Re-raising keeps the original stamp, so Tim stays first — and is a
	// no-op for the room: no roster snapshot is built (nil), nothing fans out.
	if _, snapshot, err = app.setRoomHandRaised(officeRoomID, "Tim", "tim-session", true, base.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	} else if snapshot != nil {
		t.Fatalf("re-raise built a roster snapshot %v, want nil (unchanged hand is a no-op)", snapshot)
	}
	snapshot = app.roomSnapshotForRoom(officeRoomID)
	raised, _ := snapshot["raisedHands"].([]string)
	if len(raised) != 2 || raised[0] != "Tim" || raised[1] != "Tom" {
		t.Fatalf("raisedHands=%v, want [Tim Tom]", raised)
	}
	handRaisedAt, _ := snapshot["handRaisedAt"].(map[string]string)
	if handRaisedAt["Tim"] != base.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("handRaisedAt[Tim]=%q, want the first raise stamp", handRaisedAt["Tim"])
	}
	// Late joiners read the same projection from the plain snapshot.
	late := app.roomSnapshotForRoom(officeRoomID)
	if lateRaised, _ := late["raisedHands"].([]string); len(lateRaised) != 2 || lateRaised[0] != "Tim" {
		t.Fatalf("late-joiner raisedHands=%v", lateRaised)
	}
	// Lowering removes only that hand.
	if _, snapshot, err = app.setRoomHandRaised(officeRoomID, "Tim", "tim-session", false, base.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	if raised, _ = snapshot["raisedHands"].([]string); len(raised) != 1 || raised[0] != "Tom" {
		t.Fatalf("after lowering Tim raisedHands=%v, want [Tom]", raised)
	}
	// Leaving lowers the hand without an explicit room_hand.
	if removed, still := app.forgetParticipantSessionResultInRoom(officeRoomID, "Tom", "tom-session"); !removed || still {
		t.Fatalf("forget Tom removed=%t stillPresent=%t", removed, still)
	}
	after := app.roomSnapshotForRoom(officeRoomID)
	if raised, _ = after["raisedHands"].([]string); len(raised) != 0 {
		t.Fatalf("raisedHands after Tom left=%v, want none", raised)
	}
	if handRaisedAt, _ = after["handRaisedAt"].(map[string]string); len(handRaisedAt) != 0 {
		t.Fatalf("handRaisedAt after Tom left=%v, want empty", handRaisedAt)
	}
	if after["locked"] != false {
		t.Fatalf("snapshot locked=%v, want false", after["locked"])
	}
}

// Reactions reach exactly the sender's room — members and guests seated there
// — and never an office socket.
func TestWebsocketRoomReactionFansOutToRoomOnlyIncludingGuests(t *testing.T) {
	server, roomID := newInCallRoomServer(t)

	officeConn := dialIsolatedWebsocket(t, server, "tyler@shareability.com")
	admitInCallMember(t, officeConn, officeRoomID)

	memberConn := dialInCallMember(t, server, "tom@shareability.com", roomID)
	admitInCallMember(t, memberConn, roomID)

	guestToken, err := userSessionStore().createGuest(roomID, "Sam")
	if err != nil {
		t.Fatalf("create guest session: %v", err)
	}
	guestConn, _, err := dialGuestWebsocket(t, server, guestToken)
	if err != nil {
		t.Fatalf("guest dial: %v", err)
	}
	if err := guestConn.WriteJSON(map[string]string{"event": "participant", "data": `{}`}); err != nil {
		t.Fatalf("guest hello: %v", err)
	}
	joinInCallMedia(t, guestConn, roomID)

	// Off-allowlist first: nothing may fan out.
	writeNativeWebsocketEvent(t, memberConn, "room_reaction", map[string]any{"emoji": "💩"})
	writeNativeWebsocketEvent(t, memberConn, "room_reaction", map[string]any{"emoji": "🎉"})

	var reaction struct {
		ParticipantID string `json:"participantId"`
		Name          string `json:"name"`
		Emoji         string `json:"emoji"`
		At            string `json:"at"`
		RoomID        string `json:"roomId"`
	}
	if err := json.Unmarshal(waitForKanbanEvent(t, memberConn, "room_reaction", 5*time.Second), &reaction); err != nil {
		t.Fatalf("decode member reaction: %v", err)
	}
	if reaction.Emoji != "🎉" || reaction.Name != "Tom" || reaction.ParticipantID == "" || reaction.RoomID != roomID || reaction.At == "" {
		t.Fatalf("member reaction=%+v", reaction)
	}
	if err := json.Unmarshal(waitForKanbanEvent(t, guestConn, "room_reaction", 5*time.Second), &reaction); err != nil {
		t.Fatalf("decode guest reaction: %v", err)
	}
	if reaction.Emoji != "🎉" || reaction.Name != "Tom" {
		t.Fatalf("guest saw reaction=%+v", reaction)
	}
	// A second reaction inside the 1s window is dropped for everyone.
	writeNativeWebsocketEvent(t, memberConn, "room_reaction", map[string]any{"emoji": "🔥"})
	assertNoRoomEventBeforeSentinel(t, guestConn, "room_reaction")
	// The office socket never hears the room.
	assertNoRoomEventBeforeSentinel(t, officeConn, "room_reaction")

	// Guests can raise a hand; the roster carries the order for everyone.
	if err := guestConn.WriteJSON(map[string]string{"event": "room_hand", "data": `{"raised":true}`}); err != nil {
		t.Fatalf("guest hand: %v", err)
	}
	var hand struct {
		Name   string `json:"name"`
		Raised bool   `json:"raised"`
	}
	// The sentinel lowers above fanned out too; read through to the raise.
	for !hand.Raised {
		hand.Name, hand.Raised = "", false
		if err := json.Unmarshal(waitForKanbanEvent(t, memberConn, "room_hand", 5*time.Second), &hand); err != nil {
			t.Fatalf("decode hand: %v", err)
		}
	}
	if !strings.HasPrefix(hand.Name, guestNamePrefix) {
		t.Fatalf("guest hand payload=%+v", hand)
	}
	snapshot := kanbanApp.roomSnapshotForRoom(roomID)
	if raised, _ := snapshot["raisedHands"].([]string); len(raised) != 1 || raised[0] != hand.Name {
		t.Fatalf("raisedHands=%v, want [%s]", raised, hand.Name)
	}
	// A guest may not moderate: the inbound allowlist drops the frame and the
	// room stays unlocked.
	if err := guestConn.WriteJSON(map[string]string{"event": "room_moderate", "data": `{"action":"lock"}`}); err != nil {
		t.Fatalf("guest moderate: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if kanbanApp.roomJoinLocked(roomID) {
		t.Fatal("guest room_moderate locked the room")
	}
}

func TestWebsocketRoomModerateRejectsNonManager(t *testing.T) {
	server, roomID := newInCallRoomServer(t)
	tomConn := dialInCallMember(t, server, "tom@shareability.com", roomID)
	admitInCallMember(t, tomConn, roomID)
	ajConn := dialInCallMember(t, server, "aj@shareability.com", roomID)
	admitInCallMember(t, ajConn, roomID)

	writeNativeWebsocketEvent(t, tomConn, "room_moderate", map[string]any{"action": "lock"})
	var reply struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(waitForKanbanEvent(t, tomConn, "room_moderate", 5*time.Second), &reply); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if reply.OK || reply.Error != errRoomModerationForbidden.Error() {
		t.Fatalf("non-manager reply=%+v", reply)
	}
	if kanbanApp.roomJoinLocked(roomID) {
		t.Fatal("non-manager lock took effect")
	}
	writeNativeWebsocketEvent(t, tomConn, "room_moderate", map[string]any{"action": "remove", "participantId": "AJ"})
	if err := json.Unmarshal(waitForKanbanEvent(t, tomConn, "room_moderate", 5*time.Second), &reply); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if reply.OK {
		t.Fatalf("non-manager remove accepted: %+v", reply)
	}
	// AJ is still seated and never heard a removal.
	assertNoRoomEventBeforeSentinel(t, ajConn, "room_participant_removed")
	if participants := kanbanApp.participantSnapshotForRoom(roomID); len(participants) != 2 {
		t.Fatalf("participants=%v after rejected moderation, want both", participants)
	}
	// Bad actions from the owner are answered, not fanned out.
	writeNativeWebsocketEvent(t, ajConn, "room_moderate", map[string]any{"action": "explode"})
	if err := json.Unmarshal(waitForKanbanEvent(t, ajConn, "room_moderate", 5*time.Second), &reply); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if reply.OK || reply.Error != errRoomModerationInvalid.Error() {
		t.Fatalf("invalid action reply=%+v", reply)
	}
}

func TestRoomModerateMuteEnforcementDropsOnlyAudioUnlessComplied(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpointInRoom(officeRoomID, "Tom", "tom-session", "tom-endpoint"); err != nil {
		t.Fatal(err)
	}
	audio, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "tom-audio", "tom-stream")
	if err != nil {
		t.Fatal(err)
	}
	video, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, "tom-video", "tom-stream")
	if err != nil {
		t.Fatal(err)
	}
	snapshotPeerState(t)
	listLock.Lock()
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{audio.ID(): audio, video.ID(): video}
	trackParticipants = map[string]string{audio.ID(): "Tom", video.ID(): "Tom"}
	trackParticipantSessions = map[string]string{audio.ID(): "tom-session", video.ID(): "tom-session"}
	trackRooms = map[string]string{audio.ID(): officeRoomID, video.ID(): officeRoomID}
	trackSourceIDs = map[string]string{}
	trackLayerRIDs = map[string]string{}
	trackLayerGroups = map[string]string{}
	listLock.Unlock()
	generation := app.roomMediaGeneration(officeRoomID)

	// Complied: the client reported micMuted before the grace ran out.
	if _, err := app.setParticipantEndpointMediaStateInRoom(officeRoomID, "Tom", "tom-endpoint", "tom-session", participantMediaState{MicMuted: true}); err != nil {
		t.Fatal(err)
	}
	app.enforceRoomMute(officeRoomID, "Tom", generation, "AJ", []string{"tom-session"})
	listLock.RLock()
	_, audioKept := trackLocals[audio.ID()]
	listLock.RUnlock()
	if !audioKept {
		t.Fatal("a complied mute still dropped the audio track")
	}
	if hostMuted, _ := app.roomSnapshotForRoom(officeRoomID)["hostMutedAt"].(map[string]string); len(hostMuted) != 0 {
		t.Fatalf("complied mute stamped hostMutedAt=%v", hostMuted)
	}

	// Not complied: audio is dropped server-side, video stays, roster is honest.
	if _, err := app.setParticipantEndpointMediaStateInRoom(officeRoomID, "Tom", "tom-endpoint", "tom-session", participantMediaState{MicMuted: false}); err != nil {
		t.Fatal(err)
	}
	app.enforceRoomMute(officeRoomID, "Tom", generation, "AJ", []string{"tom-session"})
	listLock.RLock()
	_, audioKept = trackLocals[audio.ID()]
	_, videoKept := trackLocals[video.ID()]
	_, audioParticipant := trackParticipants[audio.ID()]
	listLock.RUnlock()
	if audioKept || audioParticipant {
		t.Fatal("server enforcement left the audio track forwarding")
	}
	if !videoKept {
		t.Fatal("server enforcement dropped the video track too")
	}
	snapshot := app.roomSnapshotForRoom(officeRoomID)
	mediaStates, _ := snapshot["mediaStates"].(map[string]participantMediaState)
	if !mediaStates["Tom"].MicMuted {
		t.Fatalf("roster mediaStates after enforcement=%+v, want micMuted", mediaStates["Tom"])
	}
	if hostMuted, _ := snapshot["hostMutedAt"].(map[string]string); hostMuted["Tom"] == "" {
		t.Fatalf("hostMutedAt=%v, want Tom stamped", hostMuted)
	}
	// A stale generation (the sitting ended and restarted) never enforces.
	app.mu.Lock()
	app.roomLiveLocked(officeRoomID).hostMutedAt = map[string]time.Time{}
	app.mu.Unlock()
	app.enforceRoomMute(officeRoomID, "Tom", generation+1, "AJ", []string{"tom-session"})
	if hostMuted, _ := app.roomSnapshotForRoom(officeRoomID)["hostMutedAt"].(map[string]string); len(hostMuted) != 0 {
		t.Fatalf("stale-generation enforcement stamped hostMutedAt=%v", hostMuted)
	}
}

func TestWebsocketRoomModerateMuteAsksTargetThenEnforces(t *testing.T) {
	previousGrace := roomModerateMuteGrace
	roomModerateMuteGrace = 250 * time.Millisecond
	t.Cleanup(func() { roomModerateMuteGrace = previousGrace })

	server, roomID := newInCallRoomServer(t)
	ajConn := dialInCallMember(t, server, "aj@shareability.com", roomID)
	admitInCallMember(t, ajConn, roomID)
	tomConn := dialInCallMember(t, server, "tom@shareability.com", roomID)
	admitInCallMember(t, tomConn, roomID)

	// Enforcement is scoped to the sessions that were ASKED (the socket's own
	// participant session id — the same id production stamps into
	// trackParticipantSessions when a track registers). The fake track must
	// carry that exact id; a second track under a session that was never
	// asked must survive enforcement untouched.
	asked := kanbanApp.participantSessionIDsInRoom(roomID, "Tom")
	if len(asked) != 1 {
		t.Fatalf("asked sessions=%v, want exactly Tom's socket session", asked)
	}
	audio, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "tom-ws-audio", "tom-ws-stream")
	if err != nil {
		t.Fatal(err)
	}
	unaskedAudio, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "tom-unasked-audio", "tom-unasked-stream")
	if err != nil {
		t.Fatal(err)
	}
	listLock.Lock()
	trackLocals[audio.ID()] = audio
	trackParticipants[audio.ID()] = "Tom"
	trackParticipantSessions[audio.ID()] = asked[0]
	trackRooms[audio.ID()] = roomID
	trackLocals[unaskedAudio.ID()] = unaskedAudio
	trackParticipants[unaskedAudio.ID()] = "Tom"
	trackParticipantSessions[unaskedAudio.ID()] = "tom-unasked-session"
	trackRooms[unaskedAudio.ID()] = roomID
	listLock.Unlock()
	t.Cleanup(func() {
		listLock.Lock()
		for _, id := range []string{audio.ID(), unaskedAudio.ID()} {
			delete(trackLocals, id)
			delete(trackParticipants, id)
			delete(trackParticipantSessions, id)
			delete(trackRooms, id)
		}
		listLock.Unlock()
	})

	writeNativeWebsocketEvent(t, ajConn, "room_moderate", map[string]any{"action": "mute", "participantId": "Tom"})

	var request struct {
		Action string `json:"action"`
		By     string `json:"by"`
		RoomID string `json:"roomId"`
	}
	if err := json.Unmarshal(waitForKanbanEvent(t, tomConn, "room_moderate_request", 5*time.Second), &request); err != nil {
		t.Fatalf("decode mute request: %v", err)
	}
	if request.Action != "mute" || request.By != "AJ" || request.RoomID != roomID {
		t.Fatalf("mute request=%+v", request)
	}
	// The request went to Tom only; AJ sees the room-wide ack instead.
	var ack struct {
		OK       bool   `json:"ok"`
		Action   string `json:"action"`
		Name     string `json:"name"`
		Enforced bool   `json:"enforced"`
	}
	if err := json.Unmarshal(waitForKanbanEvent(t, ajConn, "room_moderate", 5*time.Second), &ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	if !ack.OK || ack.Action != "mute" || ack.Name != "Tom" || ack.Enforced {
		t.Fatalf("mute ack=%+v", ack)
	}
	// Tom ignores the request; after the grace the server enforces.
	if err := json.Unmarshal(waitForKanbanEvent(t, ajConn, "room_moderate", 5*time.Second), &ack); err != nil {
		t.Fatalf("decode enforcement: %v", err)
	}
	if !ack.OK || ack.Action != "mute" || ack.Name != "Tom" || !ack.Enforced {
		t.Fatalf("enforcement ack=%+v", ack)
	}
	listLock.RLock()
	_, audioKept := trackLocals[audio.ID()]
	_, unaskedKept := trackLocals[unaskedAudio.ID()]
	listLock.RUnlock()
	if audioKept {
		t.Fatal("server enforcement left Tom's audio track forwarding")
	}
	if !unaskedKept {
		t.Fatal("server enforcement dropped a track from a session that was never asked")
	}
	snapshot := kanbanApp.roomSnapshotForRoom(roomID)
	mediaStates, _ := snapshot["mediaStates"].(map[string]participantMediaState)
	if !mediaStates["Tom"].MicMuted {
		t.Fatalf("roster after enforcement=%+v, want Tom micMuted", mediaStates["Tom"])
	}
}

func TestWebsocketRoomModerateRemoveClosesSession(t *testing.T) {
	server, roomID := newInCallRoomServer(t)
	ajConn := dialInCallMember(t, server, "aj@shareability.com", roomID)
	admitInCallMember(t, ajConn, roomID)
	tomConn := dialInCallMember(t, server, "tom@shareability.com", roomID)
	admitInCallMember(t, tomConn, roomID)

	writeNativeWebsocketEvent(t, ajConn, "room_moderate", map[string]any{"action": "remove", "participantId": "Tom"})

	var notice struct {
		Name string `json:"name"`
		By   string `json:"by"`
		Self bool   `json:"self"`
	}
	if err := json.Unmarshal(waitForKanbanEvent(t, tomConn, "room_participant_removed", 5*time.Second), &notice); err != nil {
		t.Fatalf("decode removal notice: %v", err)
	}
	if notice.Name != "Tom" || notice.By != "AJ" || !notice.Self {
		t.Fatalf("removal notice=%+v", notice)
	}
	// Tom's socket is closed by the server.
	if err := tomConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for {
		var message websocketMessage
		if err := tomConn.ReadJSON(&message); err != nil {
			break
		}
	}
	// The room hears the removal (without the target-only self stamp) and
	// the ordinary leave.
	var roomNotice struct {
		Name string `json:"name"`
		By   string `json:"by"`
		Self bool   `json:"self"`
	}
	if err := json.Unmarshal(waitForKanbanEvent(t, ajConn, "room_participant_removed", 5*time.Second), &roomNotice); err != nil {
		t.Fatalf("decode room removal: %v", err)
	}
	if roomNotice.Name != "Tom" || roomNotice.By != "AJ" || roomNotice.Self {
		t.Fatalf("room removal notice=%+v", roomNotice)
	}
	var left struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(waitForKanbanEvent(t, ajConn, "participant_left", 5*time.Second), &left); err != nil {
		t.Fatalf("decode participant_left: %v", err)
	}
	if left.Name != "Tom" {
		t.Fatalf("participant_left=%+v, want Tom", left)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		participants := kanbanApp.participantSnapshotForRoom(roomID)
		if len(participants) == 1 && participants[0] == "AJ" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("participants=%v after removal, want [AJ]", participants)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestWebsocketRoomLockBlocksNewJoinsUntilUnlock(t *testing.T) {
	server, roomID := newInCallRoomServer(t)
	ajConn := dialInCallMember(t, server, "aj@shareability.com", roomID)
	admitInCallMember(t, ajConn, roomID)

	writeNativeWebsocketEvent(t, ajConn, "room_moderate", map[string]any{"action": "lock"})
	var ack struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Locked bool   `json:"locked"`
	}
	if err := json.Unmarshal(waitForKanbanEvent(t, ajConn, "room_moderate", 5*time.Second), &ack); err != nil {
		t.Fatalf("decode lock ack: %v", err)
	}
	if !ack.OK || ack.Action != "lock" || !ack.Locked {
		t.Fatalf("lock ack=%+v", ack)
	}
	if snapshot := kanbanApp.roomSnapshotForRoom(roomID); snapshot["locked"] != true {
		t.Fatalf("roster locked=%v, want true", snapshot["locked"])
	}

	// A new member is refused at admission while the lock holds.
	tomConn := dialInCallMember(t, server, "tom@shareability.com", roomID)
	writeNativeWebsocketEvent(t, tomConn, "participant", map[string]any{})
	event, data := readKanbanEventWithin(t, tomConn, "access_granted", 5*time.Second)
	if event != "access_denied" || !strings.Contains(string(data), errRoomLocked.Error()) {
		t.Fatalf("locked join answered %s %s, want access_denied room is locked", event, data)
	}
	if participants := kanbanApp.participantSnapshotForRoom(roomID); len(participants) != 1 || participants[0] != "AJ" {
		t.Fatalf("participants while locked=%v, want [AJ]", participants)
	}

	// A new guest is refused with 423 at /guest/join; the link itself stays valid.
	guestToken, _, err := appRoomStore().mintGuestLink(roomID, "lock test", "aj@shareability.com", time.Hour)
	if err != nil {
		t.Fatalf("mint guest link: %v", err)
	}
	body, _ := json.Marshal(map[string]string{"token": guestToken, "name": "Sam"})
	recorder := httptest.NewRecorder()
	guestJoinHandler(recorder, httptest.NewRequest(http.MethodPost, "/guest/join", bytes.NewReader(body)))
	if recorder.Code != http.StatusLocked {
		t.Fatalf("locked guest join status=%d body=%s, want 423", recorder.Code, recorder.Body.String())
	}

	// The seated owner keeps its seat: a second device of AJ still joins.
	ajPhone := dialInCallMember(t, server, "aj@shareability.com", roomID)
	writeNativeWebsocketEvent(t, ajPhone, "participant", map[string]any{"endpointId": "aj-phone"})
	if event, data := readKanbanEventWithin(t, ajPhone, "access_granted", 5*time.Second); event != "access_granted" {
		t.Fatalf("owner's second device answered %s %s while locked, want access_granted", event, data)
	}

	writeNativeWebsocketEvent(t, ajConn, "room_moderate", map[string]any{"action": "unlock"})
	if err := json.Unmarshal(waitForKanbanEvent(t, ajConn, "room_moderate", 5*time.Second), &ack); err != nil {
		t.Fatalf("decode unlock ack: %v", err)
	}
	if !ack.OK || ack.Action != "unlock" || ack.Locked {
		t.Fatalf("unlock ack=%+v", ack)
	}

	writeNativeWebsocketEvent(t, tomConn, "participant", map[string]any{})
	if event, data := readKanbanEventWithin(t, tomConn, "access_granted", 5*time.Second); event != "access_granted" {
		t.Fatalf("join after unlock answered %s %s, want access_granted", event, data)
	}
	resetAuthRateLimitersForTest()
	body, _ = json.Marshal(map[string]string{"token": guestToken, "name": "Sam"})
	recorder = httptest.NewRecorder()
	guestJoinHandler(recorder, httptest.NewRequest(http.MethodPost, "/guest/join", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("guest join after unlock status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
}

func TestRoomLockClearsWhenRoomEmpties(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpointInRoom(officeRoomID, "AJ", "aj-session", "aj-endpoint"); err != nil {
		t.Fatal(err)
	}
	app.setRoomLocked(officeRoomID, true, "AJ", time.Now())
	if !app.roomJoinLocked(officeRoomID) {
		t.Fatal("lock did not take")
	}
	if _, _, err := app.admitParticipantSessionEndpointInRoom(officeRoomID, "Tom", "tom-session", "tom-endpoint"); err != errRoomLocked {
		t.Fatalf("locked admission error=%v, want %v", err, errRoomLocked)
	}
	// The room empties without an explicit unlock; the next sitting's first
	// arrival must not be stranded, and the second must not be refused.
	app.forgetParticipantSessionResultInRoom(officeRoomID, "AJ", "aj-session")
	if app.roomJoinLocked(officeRoomID) {
		t.Fatal("an empty room still reports itself locked to joiners")
	}
	if _, _, err := app.admitParticipantSessionEndpointInRoom(officeRoomID, "Tom", "tom-session", "tom-endpoint"); err != nil {
		t.Fatalf("first arrival into an emptied locked room: %v", err)
	}
	if _, _, err := app.admitParticipantSessionEndpointInRoom(officeRoomID, "Tim", "tim-session", "tim-endpoint"); err != nil {
		t.Fatalf("second arrival after the stale lock cleared: %v", err)
	}
	if app.roomSnapshotForRoom(officeRoomID)["locked"] != false {
		t.Fatal("stale lock survived the empty room")
	}
}

// Reviewer case: the target leaves and rejoins under the same name inside the
// grace window. The new session never received room_moderate_request, so the
// timer bound to the asked session must not drop its audio; a session that
// was asked and stays is still enforced.
func TestRoomModerateMuteSkipsSameNameRejoinDuringGrace(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpointInRoom(officeRoomID, "AJ", "aj-session", "aj-endpoint"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.admitParticipantSessionEndpointInRoom(officeRoomID, "Tom", "tom-session-1", "tom-endpoint"); err != nil {
		t.Fatal(err)
	}
	generation := app.roomMediaGeneration(officeRoomID)
	asked := app.participantSessionIDsInRoom(officeRoomID, "Tom")
	if len(asked) != 1 || asked[0] != "tom-session-1" {
		t.Fatalf("asked sessions=%v, want [tom-session-1]", asked)
	}

	// Tom leaves and rejoins (same name, same generation: AJ kept the room alive).
	if removed, still := app.forgetParticipantSessionResultInRoom(officeRoomID, "Tom", "tom-session-1"); !removed || still {
		t.Fatalf("forget removed=%t still=%t", removed, still)
	}
	if _, _, err := app.admitParticipantSessionEndpointInRoom(officeRoomID, "Tom", "tom-session-2", "tom-endpoint"); err != nil {
		t.Fatal(err)
	}
	if app.roomMediaGeneration(officeRoomID) != generation {
		t.Fatalf("generation moved on rejoin: %d != %d", app.roomMediaGeneration(officeRoomID), generation)
	}
	audio, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "tom-rejoin-audio", "tom-rejoin-stream")
	if err != nil {
		t.Fatal(err)
	}
	snapshotPeerState(t)
	listLock.Lock()
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{audio.ID(): audio}
	trackParticipants = map[string]string{audio.ID(): "Tom"}
	trackParticipantSessions = map[string]string{audio.ID(): "tom-session-2"}
	trackRooms = map[string]string{audio.ID(): officeRoomID}
	trackSourceIDs = map[string]string{}
	trackLayerRIDs = map[string]string{}
	trackLayerGroups = map[string]string{}
	listLock.Unlock()

	// The timer armed for tom-session-1 fires: the fresh, un-asked seat is untouched.
	app.enforceRoomMute(officeRoomID, "Tom", generation, "AJ", asked)
	listLock.RLock()
	_, audioKept := trackLocals[audio.ID()]
	listLock.RUnlock()
	if !audioKept {
		t.Fatal("enforcement dropped audio on a rejoined session that was never asked")
	}
	snapshot := app.roomSnapshotForRoom(officeRoomID)
	if mediaStates, _ := snapshot["mediaStates"].(map[string]participantMediaState); mediaStates["Tom"].MicMuted {
		t.Fatalf("rejoined Tom marked muted: %+v", mediaStates["Tom"])
	}
	if hostMuted, _ := snapshot["hostMutedAt"].(map[string]string); len(hostMuted) != 0 {
		t.Fatalf("rejoined Tom stamped hostMutedAt=%v", hostMuted)
	}

	// A request aimed at the current session still enforces on it.
	app.enforceRoomMute(officeRoomID, "Tom", generation, "AJ", app.participantSessionIDsInRoom(officeRoomID, "Tom"))
	listLock.RLock()
	_, audioKept = trackLocals[audio.ID()]
	listLock.RUnlock()
	if audioKept {
		t.Fatal("enforcement on the asked current session left audio forwarding")
	}
	if hostMuted, _ := app.roomSnapshotForRoom(officeRoomID)["hostMutedAt"].(map[string]string); hostMuted["Tom"] == "" {
		t.Fatalf("hostMutedAt=%v, want Tom stamped", hostMuted)
	}
}

/* ---------- code-review fixes: hand no-op + bucket, host remove ejection, host-mute stamp ---------- */

// R2: an unchanged hand is a true no-op (no roster snapshot, nothing to fan
// out) and every session — guest or member alike — spends the same small
// per-socket room_hand bucket, released with the socket.
func TestRoomHandUnchangedIsNoOpAndEverySessionIsBucketed(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpointInRoom(officeRoomID, "Tom", "tom-session", "tom-endpoint"); err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	// Lowering a hand that is not raised changes nothing: sender-only payload, no snapshot.
	payload, snapshot, err := app.setRoomHandRaised(officeRoomID, "Tom", "tom-session", false, base)
	if err != nil || snapshot != nil || payload["raised"] != false || payload["name"] != "Tom" {
		t.Fatalf("lower-while-down payload=%v snapshot=%v err=%v, want a sender-only payload and NO snapshot", payload, snapshot, err)
	}
	if _, snapshot, err = app.setRoomHandRaised(officeRoomID, "Tom", "tom-session", true, base); err != nil || snapshot == nil {
		t.Fatalf("first raise snapshot=%v err=%v, want a roster snapshot", snapshot, err)
	}
	payload, snapshot, err = app.setRoomHandRaised(officeRoomID, "Tom", "tom-session", true, base.Add(time.Second))
	if err != nil || snapshot != nil || payload["at"] != base.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("re-raise payload=%v snapshot=%v err=%v, want the original stamp and NO snapshot", payload, snapshot, err)
	}
	if _, snapshot, err = app.setRoomHandRaised(officeRoomID, "Tom", "tom-session", false, base.Add(2*time.Second)); err != nil || snapshot == nil {
		t.Fatalf("lower snapshot=%v err=%v, want a roster snapshot", snapshot, err)
	}

	// The bucket keys on the participant session, guests and members alike.
	for _, sessionID := range []string{"member-socket", "guest-socket"} {
		for i := 0; i < int(roomHandBucketBurst); i++ {
			if !app.allowRoomHandEvent(officeRoomID, sessionID, base) {
				t.Fatalf("%s: hand %d inside the burst refused", sessionID, i)
			}
		}
		if app.allowRoomHandEvent(officeRoomID, sessionID, base) {
			t.Fatalf("%s: hand past the burst allowed", sessionID)
		}
		if !app.allowRoomHandEvent(officeRoomID, sessionID, base.Add(roomHandBucketRefill)) {
			t.Fatalf("%s: hand after one refill refused", sessionID)
		}
	}
	if !app.allowRoomHandEvent(officeRoomID, "third-socket", base) {
		t.Fatal("a fresh session must start with a full burst")
	}
	app.dropRoomHandBucket(officeRoomID, "member-socket")
	app.mu.Lock()
	_, kept := app.roomLiveLocked(officeRoomID).handBuckets["member-socket"]
	app.mu.Unlock()
	if kept {
		t.Fatal("dropRoomHandBucket left the socket's bucket behind")
	}
}

// R2 (live): a member's repeated raise reaches the room ONCE (the sender
// alone hears the unchanged echoes), and a toggle storm past the burst is
// dropped — the room hears exactly the burst.
func TestWebsocketRoomHandRepeatIsNoOpAndStormIsBucketed(t *testing.T) {
	server, roomID := newInCallRoomServer(t)
	ajConn := dialInCallMember(t, server, "aj@shareability.com", roomID)
	admitInCallMember(t, ajConn, roomID)
	tomConn := dialInCallMember(t, server, "tom@shareability.com", roomID)
	admitInCallMember(t, tomConn, roomID)

	for i := 0; i < 3; i++ {
		if err := tomConn.WriteJSON(map[string]string{"event": "room_hand", "data": `{"raised":true}`}); err != nil {
			t.Fatalf("raise %d: %v", i, err)
		}
	}
	if counts := countKanbanEventsByNameUntilQuiet(t, ajConn, []string{"room_hand"}, 1200*time.Millisecond, 6*time.Second); counts["room_hand"] != 1 {
		t.Fatalf("three identical raises produced %d room_hand broadcasts for the room, want exactly 1", counts["room_hand"])
	}
	if echoes := countKanbanEventsByNameUntilQuiet(t, tomConn, []string{"room_hand"}, 1200*time.Millisecond, 6*time.Second); echoes["room_hand"] != 3 {
		t.Fatalf("the sender heard %d room_hand frames, want 3 (one broadcast + two sender-only echoes)", echoes["room_hand"])
	}

	// A fresh sender toggles past its burst; a fresh observer hears the burst only.
	erickConn := dialInCallMember(t, server, "e@shareability.com", roomID)
	admitInCallMember(t, erickConn, roomID)
	timConn := dialInCallMember(t, server, "tim@shareability.com", roomID)
	admitInCallMember(t, timConn, roomID)
	storm := int(roomHandBucketBurst) + 4
	for i := 0; i < storm; i++ {
		raised := i%2 == 0
		if err := timConn.WriteJSON(map[string]string{"event": "room_hand", "data": fmt.Sprintf(`{"raised":%t}`, raised)}); err != nil {
			t.Fatalf("toggle %d: %v", i, err)
		}
	}
	if counts := countKanbanEventsByNameUntilQuiet(t, erickConn, []string{"room_hand"}, 1200*time.Millisecond, 6*time.Second); counts["room_hand"] != int(roomHandBucketBurst) {
		t.Fatalf("%d hand toggles produced %d room_hand broadcasts, want exactly the burst (%d)", storm, counts["room_hand"], int(roomHandBucketBurst))
	}
}

// R4: a host remove is remembered for the sitting. The removed guest's own
// link/session is refused on reload with honest copy, while a different
// guest still joins; the ejection clears with the sitting's in-call state.
func TestWebsocketRoomModerateRemoveRefusesGuestReloadForSitting(t *testing.T) {
	server, roomID := newInCallRoomServer(t)
	ajConn := dialInCallMember(t, server, "aj@shareability.com", roomID)
	admitInCallMember(t, ajConn, roomID)
	samToken, err := userSessionStore().createGuest(roomID, "Sam")
	if err != nil {
		t.Fatalf("create guest session: %v", err)
	}
	samConn, _, err := dialGuestWebsocket(t, server, samToken)
	if err != nil {
		t.Fatalf("guest dial: %v", err)
	}
	if err := samConn.WriteJSON(map[string]string{"event": "participant", "data": `{}`}); err != nil {
		t.Fatalf("guest hello: %v", err)
	}
	samName := joinInCallMedia(t, samConn, roomID)

	writeNativeWebsocketEvent(t, ajConn, "room_moderate", map[string]any{"action": "remove", "participantId": samName})
	var notice struct {
		Name string `json:"name"`
		Self bool   `json:"self"`
	}
	if err := json.Unmarshal(waitForKanbanEvent(t, samConn, "room_participant_removed", 5*time.Second), &notice); err != nil {
		t.Fatalf("decode removal notice: %v", err)
	}
	if notice.Name != samName || !notice.Self {
		t.Fatalf("removal notice=%+v", notice)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		participants := kanbanApp.participantSnapshotForRoom(roomID)
		if len(participants) == 1 && participants[0] == "AJ" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("participants=%v after removal, want [AJ]", participants)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Reload with the same guest link: the pre-upgrade socket cap releases on
	// handler exit, so retry the dial briefly, then expect the honest refusal.
	var reload *websocket.Conn
	for attempt := 0; attempt < 100; attempt++ {
		reload, _, err = dialGuestWebsocket(t, server, samToken)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("guest reload dial: %v", err)
	}
	if err := reload.WriteJSON(map[string]string{"event": "participant", "data": `{}`}); err != nil {
		t.Fatalf("guest reload hello: %v", err)
	}
	event, data := readKanbanEventWithin(t, reload, "access_granted", 5*time.Second)
	if event != "access_denied" || !strings.Contains(string(data), "removed from this meeting by the host") {
		t.Fatalf("reload after remove got %s %s, want access_denied with the removed-by-host copy", event, data)
	}
	if participants := kanbanApp.participantSnapshotForRoom(roomID); len(participants) != 1 {
		t.Fatalf("participants=%v after the refused reload, want [AJ]", participants)
	}

	// A different guest is unaffected.
	patToken, err := userSessionStore().createGuest(roomID, "Pat")
	if err != nil {
		t.Fatalf("create second guest session: %v", err)
	}
	patConn, _, err := dialGuestWebsocket(t, server, patToken)
	if err != nil {
		t.Fatalf("second guest dial: %v", err)
	}
	if err := patConn.WriteJSON(map[string]string{"event": "participant", "data": `{}`}); err != nil {
		t.Fatalf("second guest hello: %v", err)
	}
	if patName := joinInCallMedia(t, patConn, roomID); !strings.HasPrefix(patName, guestNamePrefix+"Pat") {
		t.Fatalf("second guest seated as %q", patName)
	}
}

// R4 (unit): the ejection record keys on the guest session key, the
// normalized display name and the exact participant session; members are
// refused only for that exact session; the record dies with the sitting.
func TestRoomEjectionRecordIsSittingScoped(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpointInRoom(officeRoomID, "Guest Sam", "sam-session", "sam-session"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.admitParticipantSessionEndpointInRoom(officeRoomID, "Tom", "tom-session", "tom-endpoint"); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.roomLiveLocked(officeRoomID).guestSeats["sam-guest-key"] = "Guest Sam"
	app.mu.Unlock()
	now := time.Now()
	app.recordRoomEjection(officeRoomID, "guest sam", now)
	app.recordRoomEjection(officeRoomID, "Tom", now)

	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	cases := []struct {
		label                 string
		guestKey, name, seson string
		refused               bool
	}{
		{"same guest link", "sam-guest-key", "Guest Sam 2", "fresh-session", true},
		{"same guest display name, new link", "other-guest-key", "Guest Sam", "fresh-session", true},
		{"different guest", "other-guest-key", "Guest Pat", "fresh-session", false},
		{"member: exact closed session", "", "Tom", "tom-session", true},
		{"member: fresh session, same name", "", "Tom", "tom-session-2", false},
	}
	for _, testCase := range cases {
		err := roomEjectionRefusalLocked(state, testCase.guestKey, testCase.name, testCase.seson)
		if (err != nil) != testCase.refused {
			app.mu.Unlock()
			t.Fatalf("%s: err=%v, want refused=%t", testCase.label, err, testCase.refused)
		}
		if err != nil && !strings.Contains(err.Error(), "removed from this meeting by the host") {
			app.mu.Unlock()
			t.Fatalf("%s: err=%q, want the honest removed-by-host copy", testCase.label, err)
		}
	}
	resetRoomInCallStateLocked(state)
	cleared := roomEjectionRefusalLocked(state, "sam-guest-key", "Guest Sam", "tom-session")
	app.mu.Unlock()
	if cleared != nil {
		t.Fatalf("ejection survived the sitting reset: %v", cleared)
	}
}

// R3: enforceRoomMute is scoped to the sessions that were asked, so when a
// NEW session of a host-muted name registers the roster must stop claiming
// the mute — hostMutedAt[name] clears at admission.
func TestHostMuteStampClearsWhenNewSessionRegisters(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	sittingID := app.prepareMeetingSittingID(officeRoomID)
	principal := memberAdmissionPrincipal("tom@shareability.com")
	if _, _, err := app.admitParticipantWithAnchor(context.Background(), officeRoomID, "Tom", "tom-session-1", "tom-endpoint-1", sittingID, principal); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.roomLiveLocked(officeRoomID).hostMutedAt["Tom"] = time.Now().UTC()
	app.mu.Unlock()
	if hostMuted, _ := app.roomSnapshotForRoom(officeRoomID)["hostMutedAt"].(map[string]string); hostMuted["Tom"] == "" {
		t.Fatalf("hostMutedAt=%v, want Tom stamped before the new session", hostMuted)
	}
	// A second device (new session) of Tom registers: never asked, audio not
	// dropped — the stamp clears.
	if _, _, err := app.admitParticipantWithAnchor(context.Background(), officeRoomID, "Tom", "tom-session-2", "tom-endpoint-2", sittingID, principal); err != nil {
		t.Fatal(err)
	}
	if hostMuted, _ := app.roomSnapshotForRoom(officeRoomID)["hostMutedAt"].(map[string]string); len(hostMuted) != 0 {
		t.Fatalf("hostMutedAt=%v after a new session registered, want cleared", hostMuted)
	}
}
