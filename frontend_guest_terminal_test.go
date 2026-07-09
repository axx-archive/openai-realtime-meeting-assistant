package main

// Rooms UX RW4 (docs/plans/rooms-ux-2026-07-09.md §3.5/§3.7/§3.8): the guest
// terminal takeover ("you've left {room}." + one rejoin door via /guest/me,
// hashchange token reprocess, no member chrome, no stale landing beneath),
// the room_closed archive-close seam, the occupant-aware archive confirm,
// and the guest mobile hero inversion.

import (
	"os"
	"strings"
	"testing"
)

func readIndexHTMLForGuestTerminal(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(data)
}

// The terminal is a full-screen takeover: the login form (and every member
// affordance inside it) unmounts while the card shows, and the member
// pre-join landing never renders beneath a guest outside the room.
func TestIndexGuestTerminalMarkupExclusive(t *testing.T) {
	html := readIndexHTMLForGuestTerminal(t)

	for _, want := range []string{`id="guestExitCard"`, `id="guestExitTitle"`, `id="guestRejoin"`, `id="guestExitEndedLine"`} {
		if !strings.Contains(html, want) {
			t.Errorf("guest terminal markup missing %s", want)
		}
	}
	modeBody := functionBody(html, "function renderLoginMode()")
	if modeBody == "" {
		t.Fatal("could not extract renderLoginMode body")
	}
	if !strings.Contains(modeBody, "loginFormEl.hidden = mode === 'guest-exit'") {
		t.Error("the guest-exit takeover must unmount the login form wholesale")
	}
	if !strings.Contains(modeBody, "guestExitCardEl.hidden = mode !== 'guest-exit'") {
		t.Error("the terminal card must render for guest-exit mode ONLY")
	}
	// recon-27: the member pre-join landing (the lobby stage) must never
	// bleed beneath the gate/terminal for a guest outside the room
	selector := "#appShell.is-guest:not(.is-in-room) .hearth-presentation"
	at := strings.Index(html, selector)
	if at == -1 {
		t.Fatal("is-guest CSS must hide the pre-join stage outside the room (stale landing, recon-27)")
	}
	block := html[at:]
	if end := strings.Index(block, "}"); end != -1 {
		block = block[:end]
	}
	if !strings.Contains(block, "display: none !important") {
		t.Error("the guest stale-landing rule must display: none !important")
	}
}

// No Face ID (or any member affordance) in guest-exit mode, and no dead
// "Session ended" pill anywhere — the takeover carries exactly one door.
func TestIndexGuestTerminalNoFaceIDNoDeadPill(t *testing.T) {
	html := readIndexHTMLForGuestTerminal(t)

	modeBody := functionBody(html, "function renderLoginMode()")
	if !strings.Contains(modeBody, "passkeySignInButton.hidden = mode !== 'login'") {
		t.Error("Face ID must stay login-mode-only (never the guest gate or terminal)")
	}
	if !strings.Contains(modeBody, "guestMemberLinkEl.hidden = mode !== 'guest'") {
		t.Error("the demoted member link must not render on the terminal card")
	}
	if !strings.Contains(modeBody, "joinAccessButton.hidden = mode === 'guest-exit' || guestInviteExpired") {
		t.Error("the join pill must stay hidden in guest-exit mode")
	}
	if strings.Contains(html, "Session ended") {
		t.Error("the dead 'Session ended' pill copy must be gone — the terminal card owns the exit state")
	}
}

// The rejoin door re-runs the /guest/me resume (the 12h cookie seat needs no
// retyped name) and lands back on the gate; a 401 swaps the door for the
// honest mono ended line.
func TestIndexGuestTerminalRejoinViaGuestMe(t *testing.T) {
	html := readIndexHTMLForGuestTerminal(t)

	body := functionBody(html, "async function rejoinGuestRoom()")
	if body == "" {
		t.Fatal("could not extract rejoinGuestRoom body")
	}
	if !strings.Contains(body, "fetch('/guest/me'") {
		t.Error("rejoin must resume through GET /guest/me")
	}
	if !strings.Contains(body, "guestExitEnded = true") {
		t.Error("a dead session must flip the terminal to the ended state")
	}
	okAt := strings.Index(body, "guestSession = await response.json()")
	clearAt := strings.Index(body, "guestExitMessage = ''")
	if okAt == -1 || clearAt == -1 || clearAt < okAt {
		t.Error("a successful resume must clear the exit message and return to the gate")
	}
	if !strings.Contains(html, "this session has ended — ask for a fresh link.") {
		t.Error("the expired-session fallback line is missing")
	}
	if !strings.Contains(html, "guestRejoinButton.addEventListener('click', rejoinGuestRoom)") {
		t.Error("the rejoin button must be wired to rejoinGuestRoom")
	}
	// the takeover names the room: the name survives the session clear
	exitBody := functionBody(html, "function renderGuestExitState(message, options)")
	if !strings.Contains(exitBody, "guestExitRoomName = guestSession?.roomName || guestExitRoomName") {
		t.Error("renderGuestExitState must capture the room name before clearing the session")
	}
	if !strings.Contains(exitBody, "you've left ${guestExitRoomName || 'the room'}.") {
		t.Error("the default terminal message must name the room just left")
	}
}

// Re-navigating a /g#token URL in-tab returns to the gate: a hashchange
// re-runs the boot token parse and clears any terminal/expired state.
func TestIndexGuestTerminalHashchangeReprocess(t *testing.T) {
	html := readIndexHTMLForGuestTerminal(t)

	hashAt := strings.Index(html, "window.addEventListener('hashchange'")
	if hashAt == -1 {
		t.Fatal("guest mode must listen for hashchange (recon-27: in-tab renavigation did nothing)")
	}
	// the listener lives inside the guest boot branch: after the is-guest
	// class stamp, before the member-only preview poll branch
	classAt := strings.Index(html, "appShell.classList.add('is-guest')")
	pollAt := strings.Index(html, "window.setInterval(loadParticipantPreview, 15000)")
	if classAt == -1 || pollAt == -1 || hashAt < classAt || hashAt > pollAt {
		t.Error("the hashchange listener must sit inside the guest boot branch")
	}
	// a fixed window: brace-scanning trips over the token regex's `{64})`
	listener := html[hashAt:]
	if len(listener) > 900 {
		listener = listener[:900]
	}
	for _, want := range []string{
		"location.hash.match(/^#([a-f0-9]{64})$/)",
		"guestBootToken = token",
		"guestExitMessage = ''",
		"guestExitEnded = false",
		"guestInviteState = ''",
		"lookupGuestInvite()",
	} {
		if !strings.Contains(listener, want) {
			t.Errorf("hashchange listener must contain %q (re-parse + terminal/expired reset + room naming)", want)
		}
	}
}

// room_closed (§3.7): the kanban router scopes it by room and routes it —
// members step back into the lobby with the calm notice, guests get the
// terminal card with the same message.
func TestIndexRoomClosedRouterAndHandling(t *testing.T) {
	html := readIndexHTMLForGuestTerminal(t)

	routerBody := functionBody(html, "function handleKanbanMessage(message)")
	if routerBody == "" {
		t.Fatal("could not extract handleKanbanMessage body")
	}
	// once in the room-scoping gate, once in the dispatch switch
	if got := strings.Count(routerBody, "case 'room_closed':"); got != 2 {
		t.Errorf("room_closed must be listed in the scoping gate AND the dispatch switch, found %d", got)
	}
	if !strings.Contains(routerBody, "handleRoomClosed()") {
		t.Error("the router must dispatch room_closed to handleRoomClosed")
	}

	closedBody := functionBody(html, "function handleRoomClosed()")
	if closedBody == "" {
		t.Fatal("could not extract handleRoomClosed body")
	}
	leaveAt := strings.Index(closedBody, "leaveRoom()")
	// ended: true — a closed room can never be rejoined, so the guest
	// terminal must skip the rejoin door (gate-fix: the contradictory
	// primary "rejoin the room" button on a permanently closed room)
	guestAt := strings.Index(closedBody, "renderGuestExitState('this room was closed.', { ended: true })")
	lobbyAt := strings.Index(closedBody, "setActiveTool('room')")
	noticeAt := strings.Index(closedBody, "showRoomClosedNotice()")
	if leaveAt == -1 || guestAt == -1 || lobbyAt == -1 || noticeAt == -1 {
		t.Fatal("handleRoomClosed must leave the room, give guests the terminal card, and land members on the lobby with the notice")
	}
	if !(leaveAt < guestAt && guestAt < lobbyAt && lobbyAt < noticeAt) {
		t.Error("handleRoomClosed order must be leave → guest terminal (early return) → member lobby + notice")
	}
	if !strings.Contains(html, "'this room was closed.'") {
		t.Error("the member notice copy 'this room was closed.' is missing")
	}
}

// The archive confirm names the humans: an occupied room's count and the
// disconnect consequence appear before the irreversible click.
func TestIndexArchiveConfirmNamesOccupants(t *testing.T) {
	html := readIndexHTMLForGuestTerminal(t)

	body := functionBody(html, "async function archiveRoomFromList(room)")
	if body == "" {
		t.Fatal("could not extract archiveRoomFromList body")
	}
	if !strings.Contains(body, "Number(room.participantCount) || 0") {
		t.Error("the archive confirm must read the room's live occupant count")
	}
	if !strings.Contains(body, "inside — they'll be disconnected. its record is kept.") {
		t.Error("the occupied-room confirm must name the disconnect consequence")
	}
	if !strings.Contains(body, "window.confirm") {
		t.Error("archive keeps window.confirm (a custom dialog is out of scope)")
	}
}

// Guest mobile hero (§3.5, recon-25): the first remote participant headlines
// the hero slot — self rides the strip unless alone — and the stage meta
// clears the pin/flags cluster on the guest hero's top-right.
func TestIndexGuestMobileHeroInversion(t *testing.T) {
	html := readIndexHTMLForGuestTerminal(t)

	body := functionBody(html, "function syncMobileRoomHero(speakerName)")
	if body == "" {
		t.Fatal("could not extract syncMobileRoomHero body")
	}
	invertAt := strings.Index(body, "if (guestMode && mobileRoomHeroName === currentParticipantName) {")
	if invertAt == -1 {
		t.Fatal("syncMobileRoomHero must invert the self-hero default for guest sessions")
	}
	invertBlock := body[invertAt:]
	if !strings.Contains(invertBlock, "names.find(name => name && name !== currentParticipantName)") {
		t.Error("the guest inversion must pick the first remote tile for the hero slot")
	}
	// recon-25: the meta rides the guest hero's top-LEFT so it never collides
	// with the always-visible pin/flags cluster at the tile's top-right
	selector := `#appShell.is-guest.is-in-room[data-tool="room"] .stage-chrome__meeting`
	at := strings.Index(html, selector)
	if at == -1 {
		t.Fatal("the guest in-call meta reposition rule is missing (recon-25 pin/timestamp collision)")
	}
	block := html[at:]
	if end := strings.Index(block, "}"); end != -1 {
		block = block[:end]
	}
	if !strings.Contains(block, "left: 24px") || !strings.Contains(block, "right: auto") {
		t.Error("the guest meta must move to the top-left (left: 24px; right: auto)")
	}
}

// The verify pass proved the JS mode logic was right but VISUALLY defeated:
// .login-card/.login-passkey/.login-signin carry author display rules
// (flex/inline-flex), which beat the UA's [hidden]{display:none} — so the
// guest gate kept a co-equal Face ID button (item 20), the guest terminal
// kept the member card stacked above it (item 22), and the expired invite
// kept a live-looking dead join button (item 19). The hidden attribute must
// actually hide on every gate element with an author display rule.
func TestIndexGuestGateHiddenAttributeWins(t *testing.T) {
	html := readIndexHTMLForGuestTerminal(t)

	for _, selector := range []string{".login-card[hidden]", ".login-passkey[hidden]", ".login-signin[hidden]"} {
		at := strings.Index(html, selector)
		if at == -1 {
			t.Errorf("missing %s display override — the element's author display rule beats the UA hidden style and the mode logic silently loses", selector)
			continue
		}
		block := html[at:]
		if end := strings.Index(block, "}"); end != -1 {
			block = block[:end]
		}
		if !strings.Contains(block, "display: none") {
			t.Errorf("%s must set display: none", selector)
		}
	}
}

// Gate-fix: a room_closed ejection is PERMANENT — the terminal must skip the
// rejoin door instead of offering a primary button that can only degrade to
// the expired-session line. Leave/replace terminals keep the door (their
// 12h cookie seat may still resume).
func TestIndexGuestTerminalRoomClosedSkipsRejoinDoor(t *testing.T) {
	html := readIndexHTMLForGuestTerminal(t)

	exitBody := functionBody(html, "function renderGuestExitState(message, options)")
	if exitBody == "" {
		t.Fatal("could not extract renderGuestExitState body")
	}
	if !strings.Contains(exitBody, "guestExitEnded = Boolean(options?.ended) || (guestExitEnded && !message)") {
		t.Error("renderGuestExitState must honor options.ended (and keep the verdict across argless re-renders)")
	}
	// the render seam: ended hides the door and shows the honest line
	if !strings.Contains(html, "guestRejoinButton.hidden = guestExitEnded") {
		t.Error("the rejoin door must hide when the terminal is ended")
	}
	if !strings.Contains(html, "guestExitEndedLineEl.hidden = !guestExitEnded") {
		t.Error("the honest no-rejoin line must show when the terminal is ended")
	}
}
