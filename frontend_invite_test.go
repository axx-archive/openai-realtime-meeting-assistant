package main

// Rooms-UX RW3 (docs/plans/rooms-ux-2026-07-09.md §3.4/§3.6): the in-call
// surface — the member meeting bar gains ONE Invite door (teammate link +
// guest mint sharing the lobby's consent/one-time pipeline), the topbar
// names the room where Copy link used to sit, the stage keeps exactly one
// listening pill (the topbar's), occupancy counts this seat, and the
// meeting bar re-centers when the room chat column shrinks the stage.
// Guest chrome is untouchable: invite lives inside the member-only hidden
// group and never renders for a guest tab.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func readIndexHTMLForInvite(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(data)
}

// The Invite trigger is member chrome: it sits inside the meeting bar's
// controls, the is-guest member-only rule hides it, and the toggle refuses
// guest mode in JS as well (belt + suspenders — the server 401s the mint
// endpoint for guest sessions regardless).
func TestIndexInviteToggleMemberOnly(t *testing.T) {
	html := readIndexHTMLForInvite(t)

	barAt := strings.Index(html, `<footer class="meeting-bar`)
	if barAt == -1 {
		t.Fatal("could not find the meeting bar markup")
	}
	barEnd := strings.Index(html[barAt:], "</footer>")
	if barEnd == -1 {
		t.Fatal("could not slice the meeting bar markup")
	}
	bar := html[barAt : barAt+barEnd]
	inviteAt := strings.Index(bar, `id="inviteToggle"`)
	archiveAt := strings.Index(bar, `id="archiveMeeting"`)
	if inviteAt == -1 {
		t.Fatal("the member meeting bar must carry the #inviteToggle button")
	}
	if archiveAt == -1 || inviteAt > archiveAt {
		t.Error("Invite must sit next to Send notes (before #archiveMeeting) in the member control group")
	}

	// the member-only hide rule covers it
	guardAt := strings.Index(html, "#appShell.is-guest #roomBoardToggle")
	if guardAt == -1 {
		t.Fatal("could not find the is-guest member-only meeting-bar rule")
	}
	rule := html[guardAt : guardAt+strings.Index(html[guardAt:], "}")]
	if !strings.Contains(rule, "#appShell.is-guest #inviteToggle") {
		t.Error("the is-guest member-only rule must hide #inviteToggle")
	}
	if !strings.Contains(rule, "display: none !important") {
		t.Error("the is-guest member-only rule must display: none !important")
	}

	// invite is an in-call act — hidden pre-join
	if !strings.Contains(html, "#appShell:not(.is-in-room) #inviteToggle") {
		t.Error("#inviteToggle must be hidden while not in a room")
	}

	if !strings.Contains(html, "inviteToggleButton.addEventListener('click', toggleInvitePopover)") {
		t.Error("the Invite button must be wired to toggleInvitePopover")
	}
	toggleBody := functionBody(html, "function toggleInvitePopover()")
	if toggleBody == "" {
		t.Fatal("could not extract toggleInvitePopover body")
	}
	if !strings.Contains(toggleBody, "if (guestMode || !appShell.classList.contains('is-in-room')) {") {
		t.Error("toggleInvitePopover must refuse guest mode and pre-join states")
	}
}

// D4 in the in-call mount: office mints ONLY through the listen-only consent
// step; cancel returns to the invite menu without minting; non-office rooms
// mint directly. Both mounts speak the same consent copy through one helper.
func TestIndexInviteMintGatedBehindOfficeConfirm(t *testing.T) {
	html := readIndexHTMLForInvite(t)

	menuBody := functionBody(html, "function renderInvitePopMenu(pop, room)")
	if menuBody == "" {
		t.Fatal("could not extract renderInvitePopMenu body")
	}
	officeGateAt := strings.Index(menuBody, "room.id === 'office'")
	consentAt := strings.Index(menuBody, "renderInvitePopConsent(pop, room)")
	mintAt := strings.Index(menuBody, "renderLobbyPopMinted(pop, room)")
	if officeGateAt == -1 || consentAt == -1 || mintAt == -1 || !(officeGateAt < consentAt && consentAt < mintAt) {
		t.Error("mint guest link must route office through the consent step BEFORE any mint")
	}

	consentBody := functionBody(html, "function renderInvitePopConsent(pop, room)")
	if consentBody == "" {
		t.Fatal("could not extract renderInvitePopConsent body")
	}
	if !strings.Contains(consentBody, "officeMintConsentCopy()") {
		t.Error("the in-call consent step must speak the shared consent copy")
	}
	if !strings.Contains(consentBody, "'mint the link'") || !strings.Contains(consentBody, "'cancel'") {
		t.Error("the consent step must offer mint-the-link / cancel")
	}
	if !strings.Contains(consentBody, "renderLobbyPopMinted(pop, room)") {
		t.Error("mint-the-link must proceed through the shared one-time mint block")
	}
	if !strings.Contains(consentBody, "renderInvitePopMenu(pop, room)") {
		t.Error("cancel must return to the invite menu — minting nothing")
	}

	// one consent voice for both mounts
	copyBody := functionBody(html, "function officeMintConsentCopy()")
	if !strings.Contains(copyBody, "inviting a guest quiets scout.") {
		t.Error("officeMintConsentCopy must carry the listen-only consent copy")
	}
	lobbyConsentBody := functionBody(html, "function renderLobbyPopOfficeConsent(pop, room)")
	if !strings.Contains(lobbyConsentBody, "officeMintConsentCopy()") {
		t.Error("the lobby consent step must share the same consent copy helper")
	}

	// the shared mint body survives in EITHER mount — "dismissed" means
	// neither popover still hosts it
	mintedBody := functionBody(html, "async function renderLobbyPopMinted(pop, room)")
	if !strings.Contains(mintedBody, "mintPopDismissed(pop)") {
		t.Error("renderLobbyPopMinted must use the mount-agnostic dismissal check")
	}
	dismissBody := functionBody(html, "function mintPopDismissed(pop)")
	if !strings.Contains(dismissBody, "lobbyPopoverEl !== pop && invitePopoverEl !== pop") {
		t.Error("mintPopDismissed must accept either mount as the live host")
	}
}

// The topbar's copy-link slot is retired for the ROOM's name (recon-11 —
// the room name appeared nowhere in-call): activeJoinRoomRecord()?.name with
// the office fallback, lowercase, only while seated.
func TestIndexInviteTopbarRoomNameReplacesCopyLink(t *testing.T) {
	html := readIndexHTMLForInvite(t)

	if strings.Contains(html, `id="copyRoomLink"`) {
		t.Error("the topbar #copyRoomLink button is retired — link sharing lives in the Invite popover")
	}
	if !strings.Contains(html, `id="topbarRoomName"`) {
		t.Error("the topbar must carry the #topbarRoomName slot")
	}
	if !strings.Contains(html, "String(activeJoinRoomRecord()?.name || '').toLowerCase() || 'the office'") {
		t.Error("the topbar room name must source activeJoinRoomRecord()?.name with the `the office` fallback")
	}
	// the copy logic itself survives (canonical /?room= URL) as the Invite
	// popover's teammates row
	if !strings.Contains(html, "async function copyRoomLink()") {
		t.Error("copyRoomLink (the canonical /?room= copy) must survive the relocation")
	}
	menuBody := functionBody(html, "function renderInvitePopMenu(pop, room)")
	if !strings.Contains(menuBody, "copyRoomLink()") {
		t.Error("the invite popover's teammates row must copy through copyRoomLink()")
	}
	if !strings.Contains(menuBody, "'copied'") {
		t.Error("the teammates row must confirm with a `copied` label swap")
	}
}

// Exactly one `the room is listening` pill: the topbar status pill keeps the
// label; the floating stage caption duplicate is gone (recon-11 dedupe).
func TestIndexInviteSingleListeningPill(t *testing.T) {
	html := readIndexHTMLForInvite(t)

	if strings.Contains(html, "scoutCaptionPill") || strings.Contains(html, "scout-caption") {
		t.Error("the floating stage caption pill must be fully retired (markup, CSS, and JS)")
	}
	// the topbar pill keeps the one honest label
	labelBody := functionBody(html, "function roomListeningLabel()")
	if !strings.Contains(labelBody, "'the room is listening'") {
		t.Error("the topbar status pill must keep the `the room is listening` label")
	}
	if got := strings.Count(html, "'the room is listening'"); got != 1 {
		t.Errorf("the `the room is listening` pill label must exist exactly once, found %d", got)
	}
	// gate-fix: the LEFT subtitle slot must not mirror the RIGHT status pill —
	// in-call the desktop subtitle names the ROOM, never the listening state
	subtitleBody := functionBody(html, "function toolSubtitle(tool)")
	if subtitleBody == "" {
		t.Fatal("could not extract toolSubtitle body")
	}
	if strings.Contains(subtitleBody, "roomListeningLabel()") {
		t.Error("toolSubtitle must not mirror roomListeningLabel — the bar would read `the room is listening` twice end-to-end")
	}
	if !strings.Contains(subtitleBody, "activeJoinRoomRecord()?.name || guestSession?.roomName") {
		t.Error("the in-call room subtitle must name the seated room (snapshot record, guest session fallback)")
	}
}

// Occupancy honesty: `in room · n` includes THIS seat — never 0 while seated
// (recon-11), in both the topbar meta pill and the stage chrome.
func TestIndexInviteOccupancyCountsSelf(t *testing.T) {
	html := readIndexHTMLForInvite(t)

	if !strings.Contains(html, "`in room · ${Math.max(1, occupiedSeats, participantsInRoom.length)}`") {
		t.Error("the topbar in-room count must include self: Math.max(1, ...) while seated")
	}
	if !strings.Contains(html, "`in room · ${Math.max(roomMediaActive() ? 1 : 0, occupiedSeats, participantsInRoom.length)}`") {
		t.Error("the stage occupancy line must include self while seated")
	}
}

// With the room chat column open (≥861px), the fixed meeting bar re-centers
// on the shrunken stage — offset by the chat panel's own width (recon-14).
func TestIndexInviteMeetingBarChatOffset(t *testing.T) {
	html := readIndexHTMLForInvite(t)

	selector := `#appShell.is-room-chat-open.is-in-room[data-tool="room"]:not(.is-board-expanded) .meeting-bar`
	at := strings.Index(html, selector)
	if at == -1 {
		t.Fatal("the chat-open meeting-bar offset rule is missing")
	}
	block := html[at : at+strings.Index(html[at:], "}")]
	if !strings.Contains(block, "clamp(320px, 28vw, 420px)") {
		t.Error("the meeting-bar offset must equal the chat panel column width (clamp(320px, 28vw, 420px))")
	}
	// the offset only makes sense where the chat renders as a column — the
	// workspace grid rule and the offset live in the same ≥861px media block
	gridAt := strings.Index(html, "grid-template-columns: minmax(0, 1fr) clamp(320px, 28vw, 420px)")
	if gridAt == -1 || at < gridAt {
		t.Error("the offset rule must sit with the ≥861px chat-column grid rules")
	}
}

// The one-time /g# token never lingers: dismissing the Invite popover clears
// every input, and all four dismissal seams exist (outside click, Escape,
// tool switch, leaving the room). The popover handle is boot-safe (var).
func TestIndexInviteTokenHygieneAndDismissSeams(t *testing.T) {
	html := readIndexHTMLForInvite(t)

	closeBody := functionBody(html, "function closeInvitePopover()")
	if closeBody == "" {
		t.Fatal("could not extract closeInvitePopover body")
	}
	if !strings.Contains(closeBody, "input.value = ''") {
		t.Error("dismissing the Invite popover must clear the one-time token from the DOM")
	}

	if !strings.Contains(html, "if (invitePopoverEl && !invitePopoverEl.contains(event.target)") {
		t.Error("a document-level pointerdown listener must close the Invite popover on outside click")
	}
	escapeAt := strings.Index(html, "if (event.key !== 'Escape') {")
	if escapeAt == -1 || !strings.Contains(html[escapeAt:escapeAt+600], "closeInvitePopover()") {
		t.Error("Escape must dismiss the Invite popover")
	}
	toolBody := functionBody(html, "function applyToolState(tool)")
	if !strings.Contains(toolBody, "closeInvitePopover()") {
		t.Error("a tool switch must dismiss the Invite popover")
	}
	viewBody := functionBody(html, "function setRoomView(inRoom)")
	if !strings.Contains(viewBody, "closeInvitePopover()") {
		t.Error("leaving the room must dismiss the Invite popover")
	}

	// boot-order TDZ law — closeInvitePopover runs from applyToolState on
	// the boot render pass
	if regexp.MustCompile(`(?m)^\s*(let|const)\s+invitePopoverEl\b`).MatchString(html) {
		t.Fatal("invitePopoverEl is declared with let/const — boot-order TDZ landmine; declare it with var")
	}
	decl := regexp.MustCompile(`(?m)^\s*var\s+invitePopoverEl\b`).FindStringIndex(html)
	if decl == nil {
		t.Fatal("invitePopoverEl var declaration is missing from index.html")
	}
	boot := strings.Index(html, "setConnectionState('idle', 'not connected')")
	if boot == -1 || decl[0] > boot {
		t.Error("invitePopoverEl must be initialized before the boot block")
	}
}
