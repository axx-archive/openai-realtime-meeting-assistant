package main

// Multi-room W5 (§8.1/§8.2) + rooms-UX RW1 (docs/plans/rooms-ux-2026-07-09.md):
// the rail's room tool is the LOBBY (hero + rooms rail + create + ⋯ popover
// with invite/links/passcode/archive), home keeps only the live-now strip,
// and the wire shape stays pinned: ws ?room= dial, passcode-in-hello, roomId
// scoping in the kanban router, TDZ-safe state placement (activeJoin is var,
// initialized before the boot render pass).

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

// activeJoin/roomsList/pendingRoomBootParam — and the lobby's render-pass
// state (passcode memory, popover handle, rebuild signatures) — are read by
// the boot render pass (setConnectionState → updateOfficeHome → renderLobby),
// so they must be var-declared AND initialized before the boot block executes
// — the 2026-07-05 TDZ outage class.
func TestIndexRoomsStateVarDeclaredBeforeBoot(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	for _, name := range []string{
		"activeJoin", "roomsList", "pendingRoomBootParam",
		"lobbyPasscodeMemory", "lobbyPopoverEl", "lobbyListSignature",
		"homeLiveSignature", "lobbyPasscodeGateRoomId",
	} {
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
		t.Error("handleKanbanMessage must route the `rooms` office event to the lobby + home strip")
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
		t.Error("refreshAuthState must hydrate the rooms surfaces inside its auth fan-out")
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

// RW1: the fresh-login hydration hole (recon-01) — BOTH interactive sign-in
// fan-outs (password + passkey) hydrate the rooms list, so the lobby and the
// home strip are never empty until reload.
func TestIndexRoomsLobbyHydrationBothSignInPaths(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	for _, fn := range []string{"async function signInToOffice()", "async function signInWithPasskey()"} {
		body := functionBody(html, fn)
		if body == "" {
			t.Fatalf("could not extract %s body", fn)
		}
		if !strings.Contains(body, "loadRoomsList()") {
			t.Errorf("%s must call loadRoomsList() in its success fan-out", fn)
		}
	}
}

// activeJoin resets in leaveRoom — to the room JUST LEFT (RW4 §3.8: the lobby
// keeps the selection, rejoin is one click) — and the office default lives in
// exactly two places: the var declaration and the room_closed handler (an
// archived room can't stay selected). A signaling reconnect (which never
// calls leaveRoom) must re-send the same room identity + passcode, the
// reconnect re-seating trap.
func TestIndexRoomsActiveJoinClearedOnlyInLeaveRoom(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	leaveReset := "activeJoin = { roomId: leftRoomId, passcode: '', guest: false }"
	if got := strings.Count(html, leaveReset); got != 1 {
		t.Fatalf("the just-left-room activeJoin reset must appear exactly once (leaveRoom), found %d", got)
	}
	leaveBody := functionBody(html, "function leaveRoom()")
	if leaveBody == "" {
		t.Fatal("could not extract leaveRoom body")
	}
	if !strings.Contains(leaveBody, "const leftRoomId = activeJoin.roomId || 'office'") {
		t.Error("leaveRoom must capture the room being left before any teardown")
	}
	if !strings.Contains(leaveBody, leaveReset) {
		t.Error("leaveRoom must reset activeJoin to the room just left (§3.8)")
	}
	officeDefault := "activeJoin = { roomId: 'office', passcode: '', guest: false }"
	// exactly four deliberate seams: the var declaration, handleRoomClosed's
	// fallback, reconcileLobbySelection (archived/vanished selection on a
	// fresh snapshot), and archiveRoomFromList (the archiver's own hero)
	if got := strings.Count(html, officeDefault); got != 4 {
		t.Fatalf("the office-default activeJoin assignment must appear exactly four times (declaration + handleRoomClosed + reconcileLobbySelection + archiveRoomFromList), found %d", got)
	}
	closedBody := functionBody(html, "function handleRoomClosed()")
	if !strings.Contains(closedBody, officeDefault) {
		t.Error("handleRoomClosed must fall the selection back to the office — the closed room no longer exists")
	}
	for _, fn := range []string{"function reconcileLobbySelection()", "async function archiveRoomFromList(room)"} {
		if !strings.Contains(functionBody(html, fn), officeDefault) {
			t.Errorf("%s must fall the selection back to the office", fn)
		}
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

/* ---------- RW1: the lobby (room tool) ---------- */

// The room tool's pre-join surface IS the lobby: hero (badges + passcode
// gate + one primary join) plus the rooms rail (list, + new room form, ⋯
// per-row management). All of it lives inside the stage's roomNotConnected
// block — home carries none of it.
func TestIndexRoomsLobbyMarkupAndActions(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	stageAt := strings.Index(html, `id="hearthStage"`)
	lobbyAt := strings.Index(html, `id="roomNotConnected"`)
	railAt := strings.Index(html, `id="lobbyRail"`)
	if stageAt == -1 || lobbyAt == -1 || railAt == -1 || !(stageAt < lobbyAt && lobbyAt < railAt) {
		t.Error("the lobby (roomNotConnected + lobbyRail) must render inside the room stage")
	}
	for _, want := range []string{
		`id="lobbyBadges"`,
		`id="lobbyPasscodeGate"`,
		`id="lobbyPasscodeInput"`,
		`id="lobbyPasscodeJoin"`,
		`id="lobbyPasscodeError"`,
		`id="lobbyRoomsList"`,
		`id="lobbyNewRoom"`,
		`id="lobbyCreateForm"`,
		`id="lobbyCreateName"`,
		`id="lobbyCreatePasscode"`,
		`id="lobbyCreateGuests"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("lobby markup missing %s", want)
		}
	}

	rowBody := functionBody(html, "function lobbyRoomRow(room, selectedId)")
	if rowBody == "" {
		t.Fatal("could not extract lobbyRoomRow body")
	}
	if !strings.Contains(rowBody, "room.live ? ' is-live' : ''") {
		t.Error("lobby rows must carry the live dot state")
	}
	if !strings.Contains(rowBody, "row.setAttribute('aria-current', 'true')") {
		t.Error("the selected lobby row must be marked aria-current")
	}
	if !strings.Contains(rowBody, "room.passcodeRequired") || !strings.Contains(rowBody, "room.guestEnabled") {
		t.Error("lobby rows must surface the lock and guest glyphs")
	}
	// selection is browsing, join is consent: a row click selects, never joins
	if !strings.Contains(rowBody, "selectLobbyRoom(room.id)") {
		t.Error("a lobby row click must select the room (selectLobbyRoom)")
	}
	if strings.Contains(rowBody, "joinRoom(") {
		t.Error("a lobby row click must NEVER join — selection is browsing, join is consent")
	}
	selectBody := functionBody(html, "function selectLobbyRoom(roomId)")
	if selectBody == "" || strings.Contains(selectBody, "joinRoom(") {
		t.Error("selectLobbyRoom must re-point activeJoin without joining")
	}

	createBody := functionBody(html, "async function createRoomFromForm(event)")
	if !strings.Contains(createBody, "postAuthJSON('/rooms'") {
		t.Error("the + new room form must create through POST /rooms")
	}
	if !strings.Contains(createBody, "guestAccess: Boolean(guestsInput?.checked)") {
		t.Error("the + new room form must carry the allow-guests toggle")
	}
	if !strings.Contains(createBody, "form.hidden = true") {
		t.Error("the + new room form must dismiss on success")
	}
}

// The lobby hero speaks the SELECTED room: a named room's quiet state uses
// its own name and its occupancy from the rooms snapshot, never the office
// /participants preview.
func TestIndexRoomsLobbyHeroPerRoom(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	body := functionBody(html, "function renderLobby()")
	if body == "" {
		t.Fatal("could not extract renderLobby body")
	}
	if !strings.Contains(body, "activeJoinRoomRecord()") {
		t.Error("renderLobby must resolve the selected room record")
	}
	if !strings.Contains(body, "Number(selectedRoom.participantCount) || 0") {
		t.Error("a named room's occupancy must come from the rooms snapshot, not the office participant preview")
	}
	// the hero renders badges, the passcode gate, and the rooms rail in the
	// same pass — one seam for every rooms re-render
	for _, call := range []string{"renderLobbyBadges()", "renderLobbyPasscodeGate()", "renderLobbyRoomsList()"} {
		if !strings.Contains(body, call) {
			t.Errorf("renderLobby must call %s", call)
		}
	}
	// office listen-only badge: honest, link-derived signal from GET /rooms
	badgesBody := functionBody(html, "function renderLobbyBadges()")
	if !strings.Contains(badgesBody, "record.guestLinkActive") {
		t.Error("the lobby badge row must key the listen-only badge on guestLinkActive")
	}
	if !strings.Contains(html, "listen-only · guest link live") {
		t.Error("the office hero must wear the `listen-only · guest link live` badge while a link lives")
	}
}

// Post-leave honesty (verify2 stale-hero, recon-09/11 family): pre-join the
// hero derives title AND occupancy from the rooms snapshot for EVERY room
// including the office — never from the sitting-scoped in-call counters
// (occupiedSeats/participantPreview) or the topbar meeting label, which go
// stale the moment the seat drops ("meeting · jul 9" + "live now · 1 inside"
// after leaving an empty office). leaveRoom also zeroes the dead sitting's
// counters and refreshes the snapshot.
func TestIndexRoomsLobbyHeroSnapshotTruthAfterLeave(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	body := functionBody(html, "function renderLobby()")
	if body == "" {
		t.Fatal("could not extract renderLobby body")
	}
	// the ONLY non-snapshot occupancy read sits inside the inRoom branch;
	// pre-join reads selectedRoom.live/participantCount from the snapshot
	if !strings.Contains(body, "selectedRoom?.live ? Number(selectedRoom.participantCount) || 0 : 0") {
		t.Error("pre-join hero occupancy must come from the rooms snapshot (live + participantCount)")
	}
	if strings.Contains(body, "participantPreview") {
		t.Error("renderLobby must not read participantPreview — it is sitting-scoped and stale after a leave")
	}
	if !strings.Contains(body, "const selectedRoom = inRoom ? null : activeJoinRoomRecord()") {
		t.Error("pre-join the hero must resolve EVERY room (office included) from the snapshot record")
	}
	// the meeting label may only title the hero while actually seated
	labelAt := strings.Index(body, "topbarProjectEl?.textContent")
	inRoomAt := strings.Index(body, "roomEmptyName.textContent = inRoom")
	if labelAt == -1 || inRoomAt == -1 || labelAt < inRoomAt {
		t.Error("the topbar meeting label may only title the hero inside the inRoom branch")
	}

	leaveBody := functionBody(html, "function leaveRoom()")
	if !strings.Contains(leaveBody, "occupiedSeats = 0") {
		t.Error("leaveRoom must zero occupiedSeats — the sitting's occupancy dies with the seat")
	}
	if !strings.Contains(leaveBody, "participantPreview = []") {
		t.Error("leaveRoom must clear the stale participant preview (loadParticipantPreview refetches)")
	}
	if !strings.Contains(leaveBody, "loadRoomsList()") {
		t.Error("leaveRoom must refresh the rooms snapshot so the hero + rail drop the just-left seat")
	}
}

// Home slims to the live-now strip: only rooms with people inside (else one
// quiet row that opens the lobby), zero management chrome — no create, no
// mint, no archive, no passcode anywhere on the office home.
func TestIndexRoomsLobbyHomeStripSlim(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	stripAt := strings.Index(html, `id="homeLiveNow"`)
	briefAt := strings.Index(html, `id="officeBriefCard"`)
	if stripAt == -1 || briefAt == -1 || stripAt > briefAt {
		t.Error("the live-now strip must render above Morning Brief on the office home")
	}
	toolAt := strings.Index(html, `<section id="officeTool"`)
	overlayAt := strings.Index(html, `id="briefOverlay"`)
	if toolAt == -1 || overlayAt == -1 || toolAt > overlayAt {
		t.Fatal("could not slice the office home markup")
	}
	home := html[toolAt:overlayAt]
	for _, banned := range []string{"new room", "invite", "guest-links", "mint", "lobbyCreate", "type=\"password\""} {
		if strings.Contains(strings.ToLower(home), strings.ToLower(banned)) {
			t.Errorf("the office home markup must carry no management chrome, found %q", banned)
		}
	}

	stripBody := functionBody(html, "function renderHomeLiveNow()")
	if stripBody == "" {
		t.Fatal("could not extract renderHomeLiveNow body")
	}
	if !strings.Contains(stripBody, "room.live") {
		t.Error("the live-now strip must render only live rooms")
	}
	for _, banned := range []string{"/guest-links", "postAuthJSON", "window.confirm", "passcodeRequired"} {
		if strings.Contains(stripBody, banned) {
			t.Errorf("renderHomeLiveNow must carry no management action, found %q", banned)
		}
	}
	rowBody := functionBody(html, "function homeLiveNowRow(room)")
	if !strings.Contains(rowBody, "selectLobbyRoom(room.id)") || !strings.Contains(rowBody, "setActiveTool('room')") {
		t.Error("a live-now row must deep-link to the lobby with the room selected — never join")
	}
	if strings.Contains(rowBody, "joinRoom(") {
		t.Error("a live-now row must NEVER join directly")
	}
	quietBody := functionBody(html, "function homeLiveNowQuietRow()")
	if !strings.Contains(quietBody, "all quiet") || !strings.Contains(quietBody, "setActiveTool('room')") {
		t.Error("the quiet state must be one `rooms · all quiet` row that opens the lobby")
	}
}

// Wrong passcode lands ON the lobby gate: the access_denied passcode seam
// keeps the denied room selected, shakes the field, prints `wrong passcode.`
// inline, and never falls through to the sign-in panel (the member IS signed
// in). recon-35/36.
func TestIndexRoomsLobbyPasscodeGateFeedback(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	deniedBody := functionBody(html, "function handleAccessDenied(message)")
	if deniedBody == "" {
		t.Fatal("could not extract handleAccessDenied body")
	}
	if !strings.Contains(deniedBody, "/passcode/i.test(String(message || ''))") {
		t.Error("handleAccessDenied must detect the passcode denial reason")
	}
	if !strings.Contains(deniedBody, "activeJoin.roomId = deniedRoomId") {
		t.Error("a passcode denial must keep the denied room selected in the lobby (leaveRoom resets it to office)")
	}
	if !strings.Contains(deniedBody, "showLobbyPasscodeError(message)") {
		t.Error("a passcode denial must land on the lobby gate, never a silent close")
	}

	errorBody := functionBody(html, "function showLobbyPasscodeError(message)")
	if errorBody == "" {
		t.Fatal("could not extract showLobbyPasscodeError body")
	}
	if !strings.Contains(errorBody, "'wrong passcode.'") {
		t.Error("the inline error line must read `wrong passcode.`")
	}
	if !strings.Contains(errorBody, "input.classList.add('is-shake')") {
		t.Error("the passcode field must shake on a denial")
	}
	if !strings.Contains(errorBody, "input.focus()") {
		t.Error("the passcode field must clear and refocus on a denial")
	}
	if !strings.Contains(html, "@keyframes lobby-shake") {
		t.Error("the is-shake class needs its lobby-shake keyframes")
	}

	// the gate submits through the standard join flow with the passcode on
	// activeJoin (the hello carries it — never the URL)
	submitBody := functionBody(html, "function submitLobbyPasscode()")
	if !strings.Contains(submitBody, "activeJoin.passcode = value") || !strings.Contains(submitBody, "joinRoom()") {
		t.Error("submitLobbyPasscode must set activeJoin.passcode and enter through joinRoom")
	}
}

// The ⋯ popover is a real dismissible layer: outside click, Escape, and tool
// switch all close it; one open at a time; a data re-render closes it too
// (the rebuild signature guard makes that rare).
func TestIndexRoomsLobbyPopoverDismiss(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	openBody := functionBody(html, "function openLobbyPopover(row, room)")
	if openBody == "" {
		t.Fatal("could not extract openLobbyPopover body")
	}
	if !strings.Contains(openBody, "closeLobbyPopover()") {
		t.Error("opening a popover must close any other (one open at a time)")
	}
	if !strings.Contains(html, "if (lobbyPopoverEl && !lobbyPopoverEl.contains(event.target)") {
		t.Error("a document-level pointerdown listener must close the popover on outside click")
	}
	escapeAt := strings.Index(html, "if (event.key !== 'Escape') {")
	if escapeAt == -1 || !strings.Contains(html[escapeAt:escapeAt+400], "closeLobbyPopover()") {
		t.Error("Escape must dismiss the popover")
	}
	toolBody := functionBody(html, "function applyToolState(tool)")
	if !strings.Contains(toolBody, "closeLobbyPopover()") {
		t.Error("a tool switch must dismiss the popover")
	}
	listBody := functionBody(html, "function renderLobbyRoomsList()")
	if !strings.Contains(listBody, "signature === lobbyListSignature") {
		t.Error("the lobby list must skip identical rebuilds so the popover survives ambient re-renders")
	}
	// a DATA rebuild must keep the open popover alive (it lives on the rail,
	// not in the rebuilt rows) and re-anchor it — the mint itself refreshes
	// the list (badge state) and closing here would discard the just-minted
	// one-time token
	if !strings.Contains(listBody, "positionLobbyPopover()") {
		t.Error("a lobby list rebuild must re-anchor the open portaled popover onto its room's fresh row")
	}
}

// The ⋯ popover PORTALS onto the rail — never inside a .lobby__row. The rows
// live inside the .lobby__list scrollbox (overflow-y:auto, ~91px tall with
// two rooms), which clipped the entire management layer — the office consent
// interstitial, the one-time minted link, the guest-links/revoke panel — to
// an invisible sliver (verify2-07/08). The portal positions from the anchor
// row's rect and flips above when the viewport would cut it off.
func TestIndexRoomsLobbyPopoverEscapesListClip(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	openBody := functionBody(html, "function openLobbyPopover(row, room)")
	if openBody == "" {
		t.Fatal("could not extract openLobbyPopover body")
	}
	if !strings.Contains(openBody, "rail.append(pop)") {
		t.Error("the popover must mount on the rail (portal), outside the .lobby__list scrollbox")
	}
	if strings.Contains(openBody, "row.append(pop)") {
		t.Error("the popover must NOT mount inside the row — the list's overflow-y:auto clips it to a sliver")
	}
	if !strings.Contains(openBody, "positionLobbyPopover()") {
		t.Error("opening the popover must position it against its anchor row")
	}

	posBody := functionBody(html, "function positionLobbyPopover()")
	if posBody == "" {
		t.Fatal("could not extract positionLobbyPopover body")
	}
	if !strings.Contains(posBody, "closeLobbyPopover()") {
		t.Error("a popover whose anchor room left the list must close, never orphan")
	}
	if !strings.Contains(posBody, "rowRect.top + 6 - popHeight") {
		t.Error("the popover must flip above the row when the viewport would cut it off")
	}

	// the rail is the positioning context; the pop is absolute within it
	railRuleAt := strings.Index(html, ".lobby__rail {")
	if railRuleAt == -1 {
		t.Fatal("missing .lobby__rail rule")
	}
	railRule := html[railRuleAt : railRuleAt+strings.Index(html[railRuleAt:], "}")]
	if !strings.Contains(railRule, "position: relative") {
		t.Error(".lobby__rail must be position: relative — it is the portaled popover's positioning context")
	}

	// body re-renders (menu → consent → minted → links) change the pop's
	// height; the observer re-anchors it so the flip stays honest
	if !strings.Contains(html, "var lobbyPopoverResize") {
		t.Error("the popover resize observer must be var-declared (boot-reach TDZ rule)")
	}
	if !strings.Contains(openBody, "lobbyPopoverResize?.observe(pop)") {
		t.Error("opening the popover must observe its size (consent/minted/links re-renders re-anchor)")
	}
	closeBody := functionBody(html, "function closeLobbyPopover()")
	if !strings.Contains(closeBody, "lobbyPopoverResize?.disconnect()") {
		t.Error("closing the popover must disconnect the resize observer")
	}
	// scrolling the list under the portaled pop must re-anchor it
	if !strings.Contains(html, "addEventListener('scroll', () => positionLobbyPopover(), { passive: true })") {
		t.Error("the rooms list scroll must re-anchor the portaled popover")
	}
}

// D4: office minting is allowed but NEVER silent — the consent interstitial
// states the listen-only consequence before the mint; cancel mints nothing;
// non-office rooms mint without the interstitial.
func TestIndexRoomsLobbyOfficeConsent(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	if !strings.Contains(html, "inviting a guest quiets scout. while any office guest link lives, office sittings are listen-only") {
		t.Error("the office consent copy must state the listen-only consequence")
	}
	menuBody := functionBody(html, "function renderLobbyPopMenu(pop, room)")
	if menuBody == "" {
		t.Fatal("could not extract renderLobbyPopMenu body")
	}
	officeGateAt := strings.Index(menuBody, "room.id === 'office'")
	consentAt := strings.Index(menuBody, "renderLobbyPopOfficeConsent(pop, room)")
	mintAt := strings.Index(menuBody, "renderLobbyPopMinted(pop, room)")
	if officeGateAt == -1 || consentAt == -1 || mintAt == -1 || !(officeGateAt < consentAt && consentAt < mintAt) {
		t.Error("invite-a-guest must route office through the consent step BEFORE any mint")
	}
	consentBody := functionBody(html, "function renderLobbyPopOfficeConsent(pop, room)")
	if !strings.Contains(consentBody, "'mint the link'") || !strings.Contains(consentBody, "'cancel'") {
		t.Error("the consent step must offer mint-the-link / cancel")
	}
	// office shows no archive row (room zero is permanent)
	if !strings.Contains(menuBody, "room.id !== 'office'") {
		t.Error("the archive row must be non-office only")
	}
}

// The guest-links panel closes the mint-only gap: list over the existing GET
// endpoint, revoke over POST .../guest-links/revoke, with honest copy about
// the revoke limitation.
func TestIndexRoomsLobbyGuestLinksRevoke(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	linksBody := functionBody(html, "async function renderLobbyPopLinks(pop, room)")
	if linksBody == "" {
		t.Fatal("could not extract renderLobbyPopLinks body")
	}
	if !strings.Contains(linksBody, "/guest-links`, { cache: 'no-store' }") {
		t.Error("the guest-links panel must list from GET /rooms/{id}/guest-links")
	}
	if !strings.Contains(linksBody, "/guest-links/revoke`, { id: link.id }") {
		t.Error("revoke must post to /rooms/{id}/guest-links/revoke with the link id")
	}
	if !strings.Contains(linksBody, "loadRoomsList()") {
		t.Error("a revoke must refresh the rooms snapshot (the listen-only badge clears when the last link dies)")
	}
	if !strings.Contains(linksBody, "revoking stops new joins; guests already inside stay until they leave.") {
		t.Error("the panel must carry the honest revoke-limitation copy")
	}
}

// The minted one-time link renders labeled (shown once · expiry), with a copy
// button, and the token leaves the DOM when the popover closes — it never
// lingers after dismissal (recon-07).
func TestIndexRoomsLobbyMintedOneTime(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	mintBody := functionBody(html, "async function mintGuestLinkForRoom(room)")
	if mintBody == "" {
		t.Fatal("could not extract mintGuestLinkForRoom body")
	}
	if !strings.Contains(mintBody, "/guest-links") {
		t.Error("invite guest must mint through POST /rooms/{id}/guest-links")
	}
	if !strings.Contains(mintBody, "${window.location.origin}${result.data?.url || ''}") {
		t.Error("the minted /g# URL must surface once, verbatim from the server response")
	}

	mintedBody := functionBody(html, "async function renderLobbyPopMinted(pop, room)")
	if !strings.Contains(mintedBody, "'shown once · expires in 7 days'") {
		t.Error("the minted block must be labeled `shown once · expires in 7 days`")
	}
	if !strings.Contains(mintedBody, "navigator.clipboard.writeText(out.value)") {
		t.Error("the minted block must carry a working copy button")
	}

	closeBody := functionBody(html, "function closeLobbyPopover()")
	if !strings.Contains(closeBody, "input.value = ''") {
		t.Error("dismissing the popover must clear the one-time token from the DOM")
	}
}

// Guest security invariant: the lobby's management chrome (rooms rail,
// badges, passcode gate) is member-only — hidden by is-guest CSS AND guarded
// in the render (a guest tab's /rooms fetch 401s regardless).
func TestIndexRoomsLobbyGuestChromeGuard(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	guardAt := strings.Index(html, "#appShell.is-guest .lobby__rail")
	if guardAt == -1 {
		t.Fatal("is-guest CSS must hide the lobby rail")
	}
	rule := html[guardAt : strings.Index(html[guardAt:], "}")+guardAt]
	if !strings.Contains(rule, "display: none !important") {
		t.Error("the is-guest lobby rule must display: none !important")
	}
	for _, selector := range []string{".lobby__badges", ".lobby__passgate"} {
		if !strings.Contains(rule, "#appShell.is-guest "+selector) {
			t.Errorf("is-guest CSS must hide %s", selector)
		}
	}
	listBody := functionBody(html, "function renderLobbyRoomsList()")
	if !strings.Contains(listBody, "rail.hidden = !authedUser || guestMode") {
		t.Error("renderLobbyRoomsList must hide the rail for guests and signed-out tabs")
	}
}

// The mount cascade is one-shot: bootstrapMount must RELEASE .is-mounting
// once the longest mount-rise finishes and converge on the fast-mount state.
// Leaving it on for the tab's life let the room pane's higher-specificity
// bf-tabin rule take the animation shorthand from mount-rise (forwards) and
// then drop the pane back to the .mount-stagger opacity-0 base when the 0.4s
// rise ended — a first-session lobby that flashed in and stayed invisible
// until a reload took the sessionStorage fast-mount branch. Belt and
// suspenders: the tab-pane bf-tabin rule itself must hold its final frame
// (`both`) so no window between the rise ending and the class swap blanks it.
func TestIndexRoomsLobbyMountCascadeReleases(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	paneRuleAt := strings.Index(html, `#appShell.is-authed[data-tool="room"]:not(.is-in-room) .hearth-presentation {`)
	if paneRuleAt == -1 {
		t.Fatal("index.html missing the room tab-pane entry rule")
	}
	paneRule := html[paneRuleAt : paneRuleAt+strings.Index(html[paneRuleAt:], "}")]
	if !strings.Contains(paneRule, "animation: bf-tabin 0.4s var(--ease) both") {
		t.Error("the room pane's bf-tabin must hold its final frame (both) — over the opacity-0 .mount-stagger base a bare fill blanks the lobby when the rise ends")
	}

	bootAt := strings.Index(html, "(function bootstrapMount()")
	if bootAt == -1 {
		t.Fatal("index.html missing bootstrapMount")
	}
	bootEnd := bootAt + 1600
	if bootEnd > len(html) {
		bootEnd = len(html)
	}
	boot := html[bootAt:bootEnd]
	removeAt := strings.Index(boot, "shell.classList.remove('is-mounting')")
	if removeAt == -1 {
		t.Fatal("bootstrapMount must remove .is-mounting after the cascade — leaving it on lets later animation rules fall back to the opacity-0 base")
	}
	if !strings.Contains(boot[removeAt:], "shell.classList.add('is-fast-mount')") {
		t.Error("bootstrapMount must converge on .is-fast-mount (the pinned-visible state every reload already uses) when the cascade releases")
	}
}

// Seat-duel client seams (fix wave 2026-07-09): a /g# tab in a signed-in
// browser must dial as=guest so the member cookie can never silently take the
// seat under the tab's shared endpoint id, and a session that keeps being
// granted then cut within seconds must stop reconnecting instead of
// replace-evicting the other tab forever (the deaf-in-room loop).
func TestIndexRoomsSeatDuelClientSeams(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	if !strings.Contains(html, "${guestMode ? '&as=guest' : ''}") {
		t.Error("openRoomWebSocket must dial as=guest for guest-mode tabs — otherwise a member session cookie in the same browser takes the seat and the two tabs evict each other")
	}

	// functionBody trips on the `(options = {})` default-param braces — slice
	// from the function anchor to the next top-level function instead.
	closeAt := strings.Index(html, "function handleRoomWebSocketClose(event, options = {})")
	nextAt := strings.Index(html, "function prepareRoomForSignalingReconnect(reason)")
	if closeAt == -1 || nextAt == -1 || nextAt < closeAt {
		t.Fatal("could not locate handleRoomWebSocketClose body")
	}
	closeBody := html[closeAt:nextAt]
	breakerAt := strings.Index(closeBody, "rapidSeatLossCount")
	reconnectAt := strings.Index(closeBody, "scheduleSignalingReconnect")
	if breakerAt == -1 {
		t.Fatal("handleRoomWebSocketClose must count rapid grant→cut cycles (seat-duel breaker)")
	}
	if reconnectAt != -1 && breakerAt > reconnectAt {
		t.Error("the seat-duel breaker must run BEFORE the reconnect is scheduled")
	}
	if !strings.Contains(closeBody, "failSignalingReconnect('this seat was taken over by another session in this browser')") {
		t.Error("three rapid seat losses must end in an honest failSignalingReconnect, not endless churn")
	}

	grantedBody := functionBody(html, "async function handleAccessGranted(participant)")
	if !strings.Contains(grantedBody, "roomSessionGrantedAt = Date.now()") {
		t.Error("handleAccessGranted must stamp roomSessionGrantedAt so the breaker can tell a replace-eviction from a network blip")
	}
}

// Gate-fix (verify-phase major): archiving the SELECTED room must never leave
// a ghost hero — a live join button over a door that can only 403. Selection
// falls back to the office on every fresh snapshot (this tab's archive, any
// other session's archive, an ambient rooms event), mirroring the rule
// handleRoomClosed applies for seated members.
func TestIndexRoomsArchiveSelectionFallsBackToOffice(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	recBody := functionBody(html, "function activeJoinRoomRecord()")
	if recBody == "" {
		t.Fatal("could not extract activeJoinRoomRecord body")
	}
	// GET /rooms keeps archived rows (archived: true) — the hero must not
	// front one
	if !strings.Contains(recBody, "!record.archived") {
		t.Error("activeJoinRoomRecord must treat an archived record as gone — an archived room never fronts the hero")
	}

	recAt := strings.Index(html, "function reconcileLobbySelection()")
	if recAt == -1 {
		t.Fatal("reconcileLobbySelection missing — the archived-selection office fallback is gone")
	}
	reconcileBody := functionBody(html, "function reconcileLobbySelection()")
	if !strings.Contains(reconcileBody, "activeJoin = { roomId: 'office', passcode: '', guest: false }") {
		t.Error("reconcileLobbySelection must fall the selection back to the office")
	}
	// seated/entering tabs are exempt: the seat owns activeJoin (room_closed
	// seam handles archive-under-them); a mid-entry yank would re-dial the
	// wrong room
	for _, guard := range []string{"is-in-room", "is-room-entering", "guestMode"} {
		if !strings.Contains(reconcileBody, guard) {
			t.Errorf("reconcileLobbySelection must skip when %s", guard)
		}
	}

	// both snapshot-landing seams reconcile: the push event and the fetch
	snapshotBody := functionBody(html, "function handleRoomsSnapshot(payload)")
	if !strings.Contains(snapshotBody, "reconcileLobbySelection()") {
		t.Error("handleRoomsSnapshot must reconcile the lobby selection when the list lands")
	}
	loadBody := functionBody(html, "async function loadRoomsList()")
	if !strings.Contains(loadBody, "reconcileLobbySelection()") {
		t.Error("loadRoomsList must reconcile the lobby selection when the list lands")
	}

	// the archiver's own hero falls back synchronously — the ghost must not
	// survive even the one in-flight snapshot fetch
	archiveBody := functionBody(html, "async function archiveRoomFromList(room)")
	fallbackAt := strings.Index(archiveBody, "activeJoin = { roomId: 'office', passcode: '', guest: false }")
	loadAt := strings.Index(archiveBody, "loadRoomsList()")
	if fallbackAt == -1 {
		t.Error("archiveRoomFromList must fall its own selection back to the office immediately")
	}
	if fallbackAt != -1 && loadAt != -1 && fallbackAt > loadAt {
		t.Error("the archiver's office fallback must run BEFORE the async snapshot refresh")
	}
	if !strings.Contains(archiveBody, "renderLobby()") {
		t.Error("archiveRoomFromList must re-render the lobby after the fallback")
	}
}

// Gate-fix (verify-phase major, second half): an INITIAL dial the server
// refused — the socket never opened (403 on an archived room, revoked
// access) — must fail fast and PRINT where the user is looking, never enter
// the six-retry reconnect churn whose give-up only reaches the invisible
// meeting-bar setLog (§3.1 never a silent close).
func TestIndexRoomsInitialDialRefusedFailsFast(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	// the handshake-truth stamp and its ride on the close options
	if !strings.Contains(html, "socket.__everOpened = true") {
		t.Error("openRoomWebSocket's onopen must stamp socket.__everOpened — onclose needs handshake truth")
	}
	if !strings.Contains(html, "handleRoomWebSocketClose(event, { reconnect, opened: Boolean(socket.__everOpened) })") {
		t.Error("onclose must pass opened: to handleRoomWebSocketClose")
	}

	// functionBody trips on the `(options = {})` default-param braces — slice
	// from the function anchor to the next top-level function instead.
	closeAt := strings.Index(html, "function handleRoomWebSocketClose(event, options = {})")
	nextAt := strings.Index(html, "function prepareRoomForSignalingReconnect(reason)")
	if closeAt == -1 || nextAt == -1 || nextAt < closeAt {
		t.Fatal("could not locate handleRoomWebSocketClose body")
	}
	closeBody := html[closeAt:nextAt]
	refusedAt := strings.Index(closeBody, "if (!options.reconnect && !options.opened) {")
	scheduleAt := strings.Index(closeBody, "scheduleSignalingReconnect")
	if refusedAt == -1 {
		t.Fatal("handleRoomWebSocketClose must fail an initial never-opened dial fast (refused handshake)")
	}
	if scheduleAt != -1 && refusedAt > scheduleAt {
		t.Error("the refused-dial fast-fail must run BEFORE any reconnect is scheduled")
	}
	refusedBody := closeBody[refusedAt:]
	for _, want := range []string{"leaveRoom()", "showLobbyJoinError(refusedMessage)", "accessHint.textContent = refusedMessage"} {
		if !strings.Contains(refusedBody, want) {
			t.Errorf("the refused-dial branch must contain %s — the reason prints in the lobby hero (members) and the gate hint (guests)", want)
		}
	}

	// the churn give-up (mid-meeting loss) also lands on the lobby — it must
	// print there too, not only in the meeting-bar log
	failBody := functionBody(html, "function failSignalingReconnect(reason)")
	if !strings.Contains(failBody, "showLobbyJoinError(") {
		t.Error("failSignalingReconnect must surface the give-up through the lobby error line")
	}
}

// Gate-fix (§3.1 typography): the hero title is the room's NAME at person
// scale in Google Sans Flex; the koan meta reads mono directly beneath it.
// Two stacked mono koans left the lobby's centerpiece with no hierarchy —
// weaker than its own rail rows.
func TestIndexRoomsLobbyHeroTitleTypography(t *testing.T) {
	html := readIndexHTMLForRooms(t)

	// CSS: person-scale sans, not the 13px mono label token
	titleCSSAt := strings.Index(html, ".room-empty__title {")
	if titleCSSAt == -1 {
		t.Fatal(".room-empty__title rule missing")
	}
	titleCSS := html[titleCSSAt:]
	if end := strings.Index(titleCSS, "}"); end != -1 {
		titleCSS = titleCSS[:end]
	}
	if !strings.Contains(titleCSS, "var(--type-title-2)") {
		t.Error(".room-empty__title must use the person-scale sans token (--type-title-2), not a mono label token")
	}
	if strings.Contains(titleCSS, "--type-label") {
		t.Error(".room-empty__title must not fall back to a mono label token")
	}

	// markup order: name, then the mono koan meta directly beneath, then badges
	nameAt := strings.Index(html, `id="roomEmptyName"`)
	metaAt := strings.Index(html, `id="roomEmptyMeta"`)
	badgesAt := strings.Index(html, `id="lobbyBadges"`)
	if nameAt == -1 || metaAt == -1 || badgesAt == -1 || !(nameAt < metaAt && metaAt < badgesAt) {
		t.Error("hero order must be title → koan meta → badges (the meta reads beneath the name, not at the hero foot)")
	}

	// the renderer titles with the NAME — the quiet koan never rides the title
	lobbyBody := functionBody(html, "function renderLobby()")
	if !strings.Contains(lobbyBody, ": (roomName || 'the office')") {
		t.Error("pre-join the hero title must be the room's name (office fallback)")
	}
	if strings.Contains(lobbyBody, "is quiet.") {
		t.Error("the quiet koan must not ride the hero title — it belongs to the mono meta beneath")
	}
}
