package main

// Multi-room W5 (§8.1/§8.2): the member client grows a rooms card, a create
// flow, passcode join, and — the part that keeps two live rooms honest —
// roomId scoping in the kanban router. These pins hold the wire shape (ws
// ?room= dial, passcode-in-hello), the TDZ-safe state placement (activeJoin
// is var, initialized before the boot render pass), and the §6.3 copy-link
// rule (canonical /?room= URL, never location.href).

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func readIndexHTMLForRooms(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(data)
}

// The room socket dials with the tab's room identity; the office socket keeps
// its bare dial (missing ?room == office on the server, the mid-deploy
// back-compat rule) — it must never take a room seat.
func TestIndexRoomsWebsocketDialCarriesRoom(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	// functionBody trips on the `(options = {})` default-param braces, so pin
	// the dial line itself — it exists nowhere else.
	if !strings.Contains(html, "/websocket?room=${encodeURIComponent(activeJoin.roomId || 'office')}") {
		t.Error("openRoomWebSocket must dial /websocket?room=<activeJoin.roomId> so reconnects re-seat into the SAME room")
	}

	officeBody := functionBody(html, "function ensureOfficeSocket()")
	if officeBody == "" {
		t.Fatal("could not extract ensureOfficeSocket body")
	}
	if strings.Contains(officeBody, "?room=") {
		t.Error("the office socket must keep the bare /websocket dial — it never takes a room seat")
	}
}

// The passcode rides the participant hello as an additive JSON field — never
// the URL, where it would land in server/proxy logs. `|| undefined` keeps the
// office hello byte-shape unchanged (JSON.stringify drops undefined).
func TestIndexRoomsPasscodeRidesHello(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	// the hello block: name, endpointId, passcode — one JSON.stringify shape
	if !strings.Contains(html, "passcode: activeJoin.passcode || undefined") {
		t.Error("the participant hello must carry passcode: activeJoin.passcode || undefined")
	}
	helloAt := strings.Index(html, "passcode: activeJoin.passcode || undefined")
	nameAt := strings.Index(html, "name: currentParticipantName,")
	if helloAt == -1 || nameAt == -1 || helloAt < nameAt || helloAt-nameAt > 400 {
		t.Error("the passcode field must sit inside the participant hello payload")
	}
	if strings.Contains(html, "websocket?room=${encodeURIComponent(activeJoin.roomId || 'office')}&passcode") {
		t.Error("the passcode must never ride the websocket URL")
	}
}

// activeJoin/roomsList/pendingRoomBootParam are read by the boot render pass
// (setConnectionState → updateOfficeHome → renderRoomLandingState), so they
// must be var-declared AND initialized before the boot block executes — the
// 2026-07-05 TDZ outage class.
func TestIndexRoomsStateVarDeclaredBeforeBoot(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	for _, name := range []string{"activeJoin", "roomsList", "pendingRoomBootParam"} {
		if regexp.MustCompile(`(?m)^\s*(let|const)\s+` + name + `\b`).MatchString(html) {
			t.Fatalf("%s is declared with let/const — boot-order TDZ landmine; declare it with var", name)
		}
		decl := regexp.MustCompile(`(?m)^\s*var\s+` + name + `\b`).FindStringIndex(html)
		if decl == nil {
			t.Fatalf("%s var declaration is missing from index.html", name)
		}
		boot := strings.Index(html, "setConnectionState('idle', 'not connected')")
		if boot == -1 {
			t.Fatal("boot anchor setConnectionState('idle', 'not connected') not found")
		}
		if decl[0] > boot {
			t.Errorf("%s must be initialized before the boot block (declared after the boot anchor)", name)
		}
	}
	if !strings.Contains(html, "var activeJoin = { roomId: 'office', passcode: '', guest: false }") {
		t.Error("activeJoin must default to the office room so every existing one-click join affordance keeps working")
	}
}

// The kanban router drops room-scoped events stamped for another room: a
// second live room can never overwrite this tab's roster, meeting
// title/clock, or transcript. Memory entries carry the room in metadata.
func TestIndexRoomsKanbanRouterScopesRoomEvents(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	routerBody := functionBody(html, "function handleKanbanMessage(message)")
	if routerBody == "" {
		t.Fatal("could not extract handleKanbanMessage body")
	}
	if !strings.Contains(routerBody, "if (kanbanEventRoomId(message.data) !== (activeJoin.roomId || 'office')) {") {
		t.Error("handleKanbanMessage must drop room-scoped events whose roomId is not this tab's activeJoin room")
	}
	for _, event := range []string{
		"participants", "participant_joined", "participant_left", "participant_track",
		"active_speaker", "meeting", "memory_transcript", "room_chat",
	} {
		// each room-scoped case must appear TWICE: once in the scoping gate,
		// once in the dispatch switch
		if strings.Count(routerBody, "case '"+event+"':") != 2 {
			t.Errorf("room-scoped event %q must be listed in the scoping gate AND the dispatch switch", event)
		}
	}
	// the rooms-list snapshot event lands in the router
	if !strings.Contains(routerBody, "case 'rooms':") {
		t.Error("handleKanbanMessage must route the `rooms` office event to the rooms card")
	}
	helperBody := functionBody(html, "function kanbanEventRoomId(payload)")
	if !strings.Contains(helperBody, "payload?.roomId || payload?.metadata?.roomId || 'office'") {
		t.Error("kanbanEventRoomId must fall back to metadata.roomId (memory entries) and default absent to office")
	}
}

// §6.3: copyRoomLink copies the canonical internal /?room= URL — never
// location.href, which in a guest tab could still carry admission in the
// fragment.
func TestIndexRoomsCopyRoomLinkCanonical(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	body := functionBody(html, "async function copyRoomLink()")
	if body == "" {
		t.Fatal("could not extract copyRoomLink body")
	}
	if !strings.Contains(body, "/?room=${encodeURIComponent(activeJoin.roomId || 'office')}") {
		t.Error("copyRoomLink must copy the canonical /?room=<id> URL")
	}
	if strings.Contains(body, "location.href") {
		t.Error("copyRoomLink must never copy location.href")
	}
}

// Rooms hydration lives inside the auth fan-out — behind the /auth/me await,
// after a confirmed signed-in state — and the loader itself refuses to run
// signed out (no authed fetch ever fires for a guest/anonymous tab).
func TestIndexRoomsHydrationInsideAuthFanout(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	authBody := functionBody(html, "async function refreshAuthState()")
	if authBody == "" {
		t.Fatal("could not extract refreshAuthState body")
	}
	if !strings.Contains(authBody, "loadRoomsList()") {
		t.Error("refreshAuthState must hydrate the rooms card inside its auth fan-out")
	}
	authedGate := strings.Index(authBody, "if (authedUser) {")
	loadCall := strings.Index(authBody, "loadRoomsList()")
	if authedGate == -1 || loadCall < authedGate {
		t.Error("loadRoomsList must sit inside the authedUser branch of refreshAuthState")
	}

	loaderBody := functionBody(html, "async function loadRoomsList()")
	if loaderBody == "" {
		t.Fatal("could not extract loadRoomsList body")
	}
	if !strings.Contains(loaderBody, "if (!authedUser) {") {
		t.Error("loadRoomsList must refuse to fetch signed out")
	}
	if !strings.Contains(loaderBody, "fetch('/rooms'") {
		t.Error("loadRoomsList must hydrate from GET /rooms")
	}
}

// activeJoin resets to the office default in leaveRoom and NOWHERE else — a
// signaling reconnect (which never calls leaveRoom) must re-send the same
// room identity + passcode, the reconnect re-seating trap.
func TestIndexRoomsActiveJoinClearedOnlyInLeaveRoom(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	reset := "activeJoin = { roomId: 'office', passcode: '', guest: false }"
	// exactly twice: the var declaration and the leaveRoom reset
	if got := strings.Count(html, reset); got != 2 {
		t.Fatalf("the office-default activeJoin assignment must appear exactly twice (declaration + leaveRoom), found %d", got)
	}
	leaveBody := functionBody(html, "function leaveRoom()")
	if leaveBody == "" {
		t.Fatal("could not extract leaveRoom body")
	}
	if !strings.Contains(leaveBody, reset) {
		t.Error("leaveRoom must reset activeJoin to the office default")
	}
	// the reconnect path must NOT touch activeJoin
	reconnectBody := functionBody(html, "function prepareRoomForSignalingReconnect(reason)")
	if strings.Contains(reconnectBody, "activeJoin") {
		t.Error("the signaling reconnect path must not touch activeJoin — it re-seats into the same room")
	}
}

// /?room=<id> preselects a room once the list hydrates: consumed once, only
// outside a live seat, only for a joinable room. Captured in the boot block
// (assignment only; the var initializes earlier).
func TestIndexRoomsBootParamPreselect(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	if !strings.Contains(html, "pendingRoomBootParam = new URLSearchParams(window.location.search).get('room') || ''") {
		t.Error("the boot block must capture the /?room= param into pendingRoomBootParam")
	}
	loaderBody := functionBody(html, "async function loadRoomsList()")
	if !strings.Contains(loaderBody, "pendingRoomBootParam = ''") {
		t.Error("loadRoomsList must consume the boot preselect exactly once")
	}
	if !strings.Contains(loaderBody, "!room.archived") {
		t.Error("the boot preselect must ignore archived rooms")
	}
	if !strings.Contains(loaderBody, "!appShell.classList.contains('is-in-room')") {
		t.Error("the boot preselect must never re-point a tab that already holds a seat")
	}
}

// The rooms card block renders on the office home above Morning Brief: live
// dot, count, lock and guest glyphs, Open (disabled for the room you're in),
// inline passcode gate, guest-link mint with the one-time /g# URL, and the
// + New room form.
func TestIndexRoomsCardMarkupAndActions(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	roomsCard := strings.Index(html, `id="officeRoomsCard"`)
	briefCard := strings.Index(html, `id="officeBriefCard"`)
	if roomsCard == -1 || briefCard == -1 || roomsCard > briefCard {
		t.Error("the rooms card must render above Morning Brief on the office home")
	}
	for _, want := range []string{
		`id="officeRoomsList"`,
		`id="officeRoomsCreateForm"`,
		`id="officeRoomsCreateName"`,
		`id="officeRoomsCreatePasscode"`,
		`id="officeRoomsCreateGuests"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rooms card markup missing %s", want)
		}
	}

	rowBody := functionBody(html, "function roomsListRow(room)")
	if rowBody == "" {
		t.Fatal("could not extract roomsListRow body")
	}
	if !strings.Contains(rowBody, "room.live ? ' is-live' : ''") {
		t.Error("rooms rows must carry the live dot state")
	}
	if !strings.Contains(rowBody, "open.disabled = appShell.classList.contains('is-in-room') && (activeJoin.roomId || 'office') === room.id") {
		t.Error("Open must be disabled for the room the tab is already seated in")
	}
	if !strings.Contains(rowBody, "room.passcodeRequired") || !strings.Contains(rowBody, "room.guestEnabled") {
		t.Error("rooms rows must surface the lock and guest badges")
	}

	openBody := functionBody(html, "function openRoomFromList(room, passcode)")
	if !strings.Contains(openBody, "leaveRoom()") {
		t.Error("switching rooms must leave the current seat first (one seat per account)")
	}
	if !strings.Contains(openBody, "joinRoom()") {
		t.Error("openRoomFromList must enter through the standard joinRoom flow")
	}

	mintBody := functionBody(html, "async function mintGuestLinkForRoom(room, strip)")
	if !strings.Contains(mintBody, "/guest-links") {
		t.Error("invite guest must mint through POST /rooms/{id}/guest-links")
	}
	if !strings.Contains(mintBody, "${window.location.origin}${result.data?.url || ''}") {
		t.Error("the minted /g# URL must surface once, verbatim from the server response")
	}

	createBody := functionBody(html, "async function createRoomFromForm(event)")
	if !strings.Contains(createBody, "postAuthJSON('/rooms'") {
		t.Error("the + New room form must create through POST /rooms")
	}
	if !strings.Contains(createBody, "guestAccess: Boolean(guestsInput?.checked)") {
		t.Error("the + New room form must carry the allow-guests toggle")
	}
}

// The landing quiet-state is per-selected-room: a named room speaks its own
// name and its occupancy from the rooms snapshot, never the office
// /participants preview.
func TestIndexRoomsLandingStatePerRoom(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	body := functionBody(html, "function renderRoomLandingState()")
	if body == "" {
		t.Fatal("could not extract renderRoomLandingState body")
	}
	if !strings.Contains(body, "activeJoinRoomRecord()") {
		t.Error("renderRoomLandingState must resolve the selected room record")
	}
	if !strings.Contains(body, "Number(selectedRoom.participantCount) || 0") {
		t.Error("a named room's occupancy must come from the rooms snapshot, not the office participant preview")
	}
}
