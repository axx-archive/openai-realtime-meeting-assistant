package main

// Seat-duel regression tests (rooms-UX fix wave 2026-07-09).
//
// Root cause found live: a /g# guest tab in a browser that is ALSO signed in
// as a member carries both cookies. The websocket handler's "member session
// wins" rule seated that tab as THE MEMBER, under the tab's localStorage
// endpoint id — which the member tab shares. Every (re)join of one tab then
// replace-evicted the other, and because closeParticipantConnections cut the
// loser's socket abruptly (no session_replaced), the losing tab treated it as
// a network blip and re-dialed: an endless mutual eviction at the reconnect
// cadence. The member looked seated to everyone else while receiving no room
// broadcasts at all — the "joins the room deaf and blind" verify blocker.
//
// Fixes pinned here:
//  1. a guest-mode dial (?as=guest) NARROWS the socket to its guest session
//     even when a member session cookie rides along, and fails closed (401)
//     when there is no live guest session;
//  2. closeParticipantConnections tells the evicted socket session_replaced
//     BEFORE closing, so the losing tab stops cleanly instead of dueling.

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func quietRoomMediaForTest(t *testing.T) {
	t.Helper()
	t.Setenv("MEETING_TRANSCRIPT_LANE_ENABLED", "0")
	t.Setenv("DAY_REFLECTION_DISABLED", "1")
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))
}

// A tab that says it is a guest must be seated on its GUEST session even when
// the browser also holds a member session — the member cookie silently taking
// the seat is exactly the identity collision behind the seat duel.
func TestGuestModeDialNarrowsToGuestPrincipal(t *testing.T) {
	resetGuestSocketCapsForTest(t)
	quietRoomMediaForTest(t)
	server := newIsolatedWebsocketServer(t)
	roomID, guestToken := mintGuestRoomAndSession(t, "Sam")

	memberToken, err := userSessionStore().create("aj@shareability.com")
	if err != nil {
		t.Fatalf("create member session: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/websocket?room=" + roomID + "&as=guest"
	header := http.Header{}
	header.Set("Cookie", sessionCookieName+"="+memberToken+"; "+guestCookieName+"="+guestToken)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial as=guest with both cookies: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// A hostile hello claiming the member's name and a member endpoint id
	// must not matter: guest identity comes from the guest session.
	if err := conn.WriteJSON(map[string]string{
		"event": "participant",
		"data":  `{"name":"AJ","endpointId":"shared-endpoint"}`,
	}); err != nil {
		t.Fatalf("guest hello: %v", err)
	}
	grantRaw := waitForKanbanEvent(t, conn, "access_granted", 5*time.Second)
	var grant struct {
		Name   string `json:"name"`
		RoomID string `json:"roomId"`
		Guest  bool   `json:"guest"`
	}
	if err := json.Unmarshal(grantRaw, &grant); err != nil {
		t.Fatalf("decode grant: %v", err)
	}
	if !grant.Guest {
		t.Fatalf("as=guest dial was seated as a member: %+v", grant)
	}
	if grant.RoomID != roomID || !strings.HasPrefix(grant.Name, "Guest ") || !strings.Contains(grant.Name, "Sam") {
		t.Fatalf("as=guest grant = %+v, want a Guest Sam seat in %s", grant, roomID)
	}
}

// as=guest with no live guest session fails closed — never a silent fallback
// to the member session (that fallback IS the hijack).
func TestAsGuestDialWithoutGuestSessionFailsClosed(t *testing.T) {
	resetGuestSocketCapsForTest(t)
	server := newIsolatedWebsocketServer(t)

	memberToken, err := userSessionStore().create("aj@shareability.com")
	if err != nil {
		t.Fatalf("create member session: %v", err)
	}
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/websocket?room=office&as=guest"
	header := http.Header{}
	header.Set("Cookie", sessionCookieName+"="+memberToken)
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil {
		conn.Close()
		t.Fatal("as=guest with only a member session must fail pre-upgrade")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("as=guest without a guest session = %+v, want 401", resp)
	}
}

// The evicted side of a same-endpoint replacement must be TOLD (the
// notifySessionReplacedAndClose pattern), not just cut: an abrupt close reads
// as a network blip and the client's signaling reconnect re-dials, evicting
// the replacement right back — the self-sustaining duel that left both tabs
// deaf while the room looked fine to everyone else.
func TestReplaceEvictionTellsSessionReplaced(t *testing.T) {
	resetGuestSocketCapsForTest(t)
	resetAuthRateLimitersForTest()
	quietRoomMediaForTest(t)
	t.Setenv("MEETING_IDLE_END_GRACE", "75ms")
	server := newIsolatedWebsocketServer(t)

	hello := map[string]string{
		"event": "participant",
		"data":  `{"name":"AJ","endpointId":"dup-endpoint"}`,
	}

	first := dialIsolatedWebsocket(t, server, "aj@shareability.com")
	if err := first.WriteJSON(hello); err != nil {
		t.Fatalf("first hello: %v", err)
	}
	waitForKanbanEvent(t, first, "access_granted", 5*time.Second)

	second := dialIsolatedWebsocket(t, server, "aj@shareability.com")
	if err := second.WriteJSON(hello); err != nil {
		t.Fatalf("second hello: %v", err)
	}
	waitForKanbanEvent(t, second, "access_granted", 5*time.Second)

	// The first socket must now hear session_replaced BEFORE the close lands.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := first.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var message websocketMessage
		if err := first.ReadJSON(&message); err != nil {
			t.Fatalf("first socket was cut without session_replaced (the duel seam): %v", err)
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
		if inner.Event == "session_replaced" {
			return
		}
	}
}
