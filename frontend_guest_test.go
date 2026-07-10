package main

// Multi-room W6 (§8.3): the guest boot mode. A guest tab lives on /g with the
// link token in the URL FRAGMENT (never sent to a server, never logged), joins
// through POST /guest/join, resumes through GET /guest/me, and owns exactly
// one surface — the room stage. These pins hold the TDZ-safe boot placement,
// the no-authed-fetches rule, the fragment-strip timing (§6.3: only AFTER a
// successful join), the office-socket exclusion, the is-guest chrome axis,
// and the terminal exit states (a guest never lands on the member office).

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func readIndexHTMLForGuest(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(data)
}

// The guest boot state is read by the boot render pass (renderLoginMode runs
// before the boot block's auth guard), so every piece must be var-declared
// AND initialized before the boot anchor — the 2026-07-05 TDZ outage class.
func TestIndexGuestBootStateVarDeclaredBeforeBoot(t *testing.T) {
	html := readIndexHTMLForGuest(t)

	boot := strings.Index(html, "setConnectionState('idle', 'not connected')")
	if boot == -1 {
		t.Fatal("boot anchor setConnectionState('idle', 'not connected') not found")
	}
	authGuard := strings.Index(html, "if (!resetToken && !guestMode) {")
	if authGuard == -1 {
		t.Fatal("the boot block must gate refreshAuthState behind !guestMode")
	}
	for _, name := range []string{"guestBootToken", "guestMode", "guestSession", "guestExitMessage"} {
		if regexp.MustCompile(`(?m)^\s*(let|const)\s+` + name + `\b`).MatchString(html) {
			t.Fatalf("%s is declared with let/const — boot-order TDZ landmine; declare it with var", name)
		}
		decl := regexp.MustCompile(`(?m)^\s*var\s+` + name + `\b`).FindStringIndex(html)
		if decl == nil {
			t.Fatalf("%s var declaration is missing from index.html", name)
		}
		if decl[0] > boot {
			t.Errorf("%s must be initialized before the boot block", name)
		}
		if decl[0] > authGuard {
			t.Errorf("%s must be initialized before the boot refreshAuthState guard reads it", name)
		}
	}
	// the fragment parse: 64-hex token, fragment-only, /g paths only
	if !strings.Contains(html, "location.hash.match(/^#([a-f0-9]{64})$/)") {
		t.Error("guestBootToken must parse a 64-hex token from the URL fragment")
	}
	if !strings.Contains(html, "var guestBootToken = (location.pathname === '/g' || location.pathname.indexOf('/g/') === 0)") {
		t.Error("guestBootToken must only parse on the /g pages")
	}
}

// A guest tab never fires an authed fetch: the boot block skips
// refreshAuthState and the participant preview poll, and refreshAuthState
// itself refuses guest mode (socket onerror paths also land there).
func TestIndexGuestSkipsAuthedBoot(t *testing.T) {
	html := readIndexHTMLForGuest(t)

	if !strings.Contains(html, "if (!resetToken && !guestMode) {") {
		t.Error("the boot block must skip refreshAuthState in guest mode")
	}
	authBody := functionBody(html, "async function refreshAuthState()")
	if authBody == "" {
		t.Fatal("could not extract refreshAuthState body")
	}
	guard := strings.Index(authBody, "if (guestMode) {")
	fetchAt := strings.Index(authBody, "fetch('/auth/me'")
	if guard == -1 || fetchAt == -1 || guard > fetchAt {
		t.Error("refreshAuthState must bail out for guest mode BEFORE the /auth/me fetch")
	}

	// the preview poll lives in the boot block's member branch, after the
	// guest branch that classes the shell
	classAt := strings.Index(html, "appShell.classList.add('is-guest')")
	pollAt := strings.Index(html, "window.setInterval(loadParticipantPreview, 15000)")
	if classAt == -1 || pollAt == -1 || classAt > pollAt {
		t.Error("the guest boot branch (is-guest class) must gate the participant preview poll into the member branch")
	}
	if strings.Count(html, "window.setInterval(loadParticipantPreview, 15000)") != 1 {
		t.Error("the participant preview poll must exist exactly once (inside the member boot branch)")
	}
	// the login-gate presence poll (signed-out /participants hint) also stays off
	if !strings.Contains(html, "const shouldPoll = !authedUser && !guestMode") {
		t.Error("syncLoginPresencePolling must exclude guest mode")
	}
}

// Deploy-refresh survival (§6.3): a reloaded guest tab with a cookie but no
// fragment token resumes via GET /guest/me straight to the confirmed prejoin.
func TestIndexGuestResumeProbe(t *testing.T) {
	html := readIndexHTMLForGuest(t)

	resumeBody := functionBody(html, "async function resumeGuestSession()")
	if resumeBody == "" {
		t.Fatal("could not extract resumeGuestSession body")
	}
	if !strings.Contains(resumeBody, "fetch('/guest/me'") {
		t.Error("resumeGuestSession must probe GET /guest/me")
	}
	if !strings.Contains(resumeBody, "renderGuestExitState(") {
		t.Error("a dead guest session must land on the terminal card, not the member gate")
	}
	// the probe fires only on cookie-without-token boots
	classAt := strings.Index(html, "appShell.classList.add('is-guest')")
	probeGate := strings.Index(html, "if (!guestBootToken) {")
	callAt := strings.Index(html, "resumeGuestSession()")
	if classAt == -1 || probeGate == -1 || callAt == -1 || probeGate < classAt || callAt < probeGate {
		t.Error("the boot block must probe resumeGuestSession only when guest mode holds no fragment token")
	}
}

// §6.3: the fragment is history.replaceState-stripped ONLY after a successful
// join — pre-join it stays put so a reload can re-present the token.
func TestIndexGuestReplaceStateStripOnlyAfterJoin(t *testing.T) {
	html := readIndexHTMLForGuest(t)

	strip := "history.replaceState(null, '', location.pathname + location.search)"
	if got := strings.Count(html, strip); got != 1 {
		t.Fatalf("the fragment strip must appear exactly once (inside joinGuestRoom, after the join succeeds), found %d", got)
	}
	joinBody := functionBody(html, "async function joinGuestRoom()")
	if joinBody == "" {
		t.Fatal("could not extract joinGuestRoom body")
	}
	postAt := strings.Index(joinBody, "postAuthJSON('/guest/join'")
	stripAt := strings.Index(joinBody, strip)
	if postAt == -1 {
		t.Fatal("joinGuestRoom must join through POST /guest/join")
	}
	if stripAt == -1 || stripAt < postAt {
		t.Error("the fragment strip must sit inside joinGuestRoom AFTER the /guest/join POST")
	}
	// the failure path returns before the strip
	failAt := strings.Index(joinBody, "if (!result.ok) {")
	if failAt == -1 || failAt > stripAt {
		t.Error("a failed join must return before the fragment strip (the token is the retry credential)")
	}
	// the join proceeds through the standard room flow, guest-flagged
	if !strings.Contains(joinBody, "joinRoom({ guest: true })") {
		t.Error("joinGuestRoom must enter through joinRoom({ guest: true })")
	}
	if !strings.Contains(joinBody, "activeJoin = { roomId: guestSession.roomId || 'office', passcode: '', guest: true }") {
		t.Error("joinGuestRoom must seat activeJoin from the guest session's server-assigned room")
	}
}

// Guests never hold an office socket: the signed-in event union
// (board/memory/notifications) is a member surface.
func TestIndexGuestOfficeSocketGuardExcludesGuestMode(t *testing.T) {
	html := readIndexHTMLForGuest(t)

	officeBody := functionBody(html, "function ensureOfficeSocket()")
	if officeBody == "" {
		t.Fatal("could not extract ensureOfficeSocket body")
	}
	if !strings.Contains(officeBody, "if (!authedUser || guestMode) {") {
		t.Error("ensureOfficeSocket must refuse guest mode explicitly, not just rely on authedUser being null")
	}
}

// The is-guest chrome axis: rail, topbar (bell + account menu live there),
// Scout rail, and board rail never exist for a guest tab. The stage, meeting
// bar, and room chat panel are the whole surface — and because #roomChatPanel
// is a CHILD of .scout-rail, the rail hide carries exactly ONE exception: the
// room-chat-open reveal (see frontend_guest_chat_test.go), which re-shows the
// rail as a chat-only host while the Scout panel stays pinned hidden. A naive
// unhide of the rail chrome breaks this pin; a naive blanket hide breaks
// guest room chat (the 2026-07-10 invisible-chat incident).
func TestIndexGuestChromeCSSHidesShellRegions(t *testing.T) {
	html := readIndexHTMLForGuest(t)

	for _, region := range []string{".tool-rail", ".topbar", ".scout-rail", ".board-rail"} {
		selector := "#appShell.is-guest " + region
		at := strings.Index(html, selector)
		if at == -1 {
			t.Errorf("is-guest CSS must hide %s", region)
			continue
		}
		block := html[at:]
		if end := strings.Index(block, "}"); end != -1 {
			block = block[:end]
		}
		if !strings.Contains(block, "display: none") {
			t.Errorf("the is-guest rule for %s must display: none", region)
		}
	}
	// the one sanctioned exception: room chat re-shows the rail when open
	if !strings.Contains(html, "#appShell.is-guest.is-room-chat-open") {
		t.Error("the rail hide must carry the room-chat-open reveal exception (guest room chat lives inside .scout-rail)")
	}
	// the boot block stamps the axis
	if !strings.Contains(html, "appShell.classList.add('is-guest')") {
		t.Error("the guest boot branch must class the shell is-guest")
	}
}

// Terminal exit states: leave, access_denied, and session_replaced all land a
// guest on the "you've left" card — never setActiveTool('office'), never the
// member login gate, and setActiveTool itself pins guests to the room.
func TestIndexGuestLeaveIsTerminal(t *testing.T) {
	html := readIndexHTMLForGuest(t)

	viewBody := functionBody(html, "function setRoomView(inRoom)")
	if viewBody == "" {
		t.Fatal("could not extract setRoomView body")
	}
	guardAt := strings.Index(viewBody, "if (guestMode) {")
	exitAt := strings.Index(viewBody, "renderGuestExitState()")
	if guardAt == -1 || exitAt == -1 || exitAt < guardAt {
		t.Error("setRoomView's leave branch must route guests to renderGuestExitState")
	}
	// RW4 §3.8: the member else lands on the LOBBY (never the office bounce,
	// never the guest terminal)
	if exitAt == -1 || !strings.Contains(viewBody[exitAt:], "setActiveTool('room')") {
		t.Error("setRoomView's member leave branch must land on the lobby (setActiveTool('room') in the member else)")
	}
	if strings.Contains(viewBody, "setActiveTool('office')") {
		t.Error("setRoomView must not bounce a leaving member to the office — the lobby is the landing (§3.8)")
	}

	toolBody := functionBody(html, "function setActiveTool(tool)")
	if toolBody == "" {
		t.Fatal("could not extract setActiveTool body")
	}
	toolGuard := strings.Index(toolBody, "if (guestMode) {")
	roomPin := strings.Index(toolBody, "applyToolState('room')")
	pushAt := strings.Index(toolBody, "history.pushState")
	if toolGuard == -1 || roomPin == -1 || roomPin < toolGuard {
		t.Error("setActiveTool must pin guest tabs to the room surface")
	}
	if pushAt != -1 && pushAt < roomPin {
		t.Error("the guest pin must return before the back-stack pushState")
	}

	deniedBody := functionBody(html, "function handleAccessDenied(message)")
	if !strings.Contains(deniedBody, "renderGuestExitState(message)") {
		t.Error("handleAccessDenied must give guests the terminal card with the server's reason")
	}
	replacedBody := functionBody(html, "function handleSessionReplaced(message)")
	if !strings.Contains(replacedBody, "renderGuestExitState(") {
		t.Error("handleSessionReplaced must give guests the terminal card")
	}

	// leaveRoom's member refetches stay off for guests
	leaveBody := functionBody(html, "function leaveRoom()")
	if !strings.Contains(leaveBody, "if (!guestMode) {\n          loadBoardSnapshot({ force: true })") {
		t.Error("leaveRoom must skip the board refetch for guests (no board surface; the fetch would 401)")
	}
	if !strings.Contains(leaveBody, "if (!guestMode) {\n          loadParticipantPreview()") {
		t.Error("leaveRoom must skip the participant preview refetch for guests")
	}
}

// The guest path never manufactures a member: authedUser is never assigned,
// and joinRoom's inline /auth/login branch is skipped for guest joins.
func TestIndexGuestNeverSetsAuthedUser(t *testing.T) {
	html := readIndexHTMLForGuest(t)

	for _, fn := range []string{
		"async function joinGuestRoom()",
		"async function resumeGuestSession()",
		"function renderGuestExitState(message, options)",
	} {
		body := functionBody(html, fn)
		if body == "" {
			t.Fatalf("could not extract %s body", fn)
		}
		if regexp.MustCompile(`\bauthedUser\s*=`).MatchString(body) {
			t.Errorf("%s must never assign authedUser", fn)
		}
	}
	if !strings.Contains(html, "if (!authedUser && !activeJoin.guest) {") {
		t.Error("joinRoom must skip the inline /auth/login branch for guest joins")
	}
}

// The guest name gate: free text, no roster select, no password, no passkey
// chrome; the form submit routes guests through joinGuestRoom.
func TestIndexGuestNameGate(t *testing.T) {
	html := readIndexHTMLForGuest(t)

	for _, want := range []string{`id="guestNameField"`, `id="guestNameInput"`} {
		if !strings.Contains(html, want) {
			t.Errorf("guest name gate markup missing %s", want)
		}
	}
	if !strings.Contains(html, "return Boolean(guestSession) || guestNameInput.value.trim() !== ''") {
		t.Error("hasValidAccess must accept guest-name-present (or a resumed session) in guest mode")
	}
	if !strings.Contains(html, "} else if (guestMode) {\n          joinGuestRoom()") {
		t.Error("the login form submit must route guest mode through joinGuestRoom")
	}
	modeBody := functionBody(html, "function renderLoginMode()")
	if modeBody == "" {
		t.Fatal("could not extract renderLoginMode body")
	}
	if !strings.Contains(modeBody, "guestMode ? (guestExitMessage ? 'guest-exit' : 'guest')") {
		t.Error("renderLoginMode must speak the guest and guest-exit states")
	}
	if !strings.Contains(modeBody, "guestNameFieldEl.hidden = mode !== 'guest' || Boolean(guestSession)") {
		t.Error("the name field must show only for the un-joined guest gate (a resumed session is name-confirmed)")
	}
}

// Canon Surface 2 column (state 1): the guest gate is-guest-scoped so the
// member sign-in gate is untouched — a 440px column, a 44px app icon (r-lg), a
// headline wordmark, the invite line (type-label/text-3), and ONE glass panel
// at 16px gap. The static markup order is icon → wordmark → invite line →
// glass card, and inside the card the name field precedes the join button
// precedes the escape-hatch link (the green room re-parents between them).
func TestIndexGuestGateCanonColumn(t *testing.T) {
	html := readIndexHTMLForGuest(t)

	if body := cssRuleBody(html, "#appShell.is-guest .access-panel"); !strings.Contains(body, "width: min(440px") {
		t.Error("the guest gate column must widen to the canon max-width 440")
	}
	if body := cssRuleBody(html, "#appShell.is-guest .login-mark"); !strings.Contains(body, "width: 44px") || !strings.Contains(body, "border-radius: var(--r-lg)") {
		t.Error("the guest gate app icon must be 44px at r-lg (canon)")
	}
	if body := cssRuleBody(html, "#appShell.is-guest .login-wordmark"); !strings.Contains(body, "font: var(--type-headline)") {
		t.Error("the guest gate wordmark must use the headline type token")
	}
	if body := cssRuleBody(html, "#appShell.is-guest .login-subline"); !strings.Contains(body, "font: var(--type-label)") || !strings.Contains(body, "color: var(--text-3)") {
		t.Error("the invite line must be type-label / text-3 (canon)")
	}
	if !strings.Contains(html, "#appShell.is-guest .login-card { gap: 16px; }") {
		t.Error("the guest glass panel must use the canon 16px gap")
	}

	// canon guest green-room sizing (Surface 2), scoped to the gate mount
	for _, want := range []string{
		".greenroom--gate .greenroom__ctl { width: 48px; height: 48px; }",
		".greenroom--gate .greenroom__micbadge { width: 24px; height: 24px; }",
		".greenroom--gate .greenroom__avatar { width: 56px; height: 56px; }",
		".greenroom--gate .greenroom__filters { width: 270px; right: 58px; }",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("guest green-room sizing missing %q", want)
		}
	}

	// static markup order: icon → wordmark → invite line → glass card
	markAt := strings.Index(html, `class="login-mark"`)
	wordAt := strings.Index(html, `class="login-wordmark"`)
	sublineAt := strings.Index(html, `class="login-subline"`)
	cardAt := strings.Index(html, `id="loginForm"`)
	if !(markAt != -1 && markAt < wordAt && wordAt < sublineAt && sublineAt < cardAt) {
		t.Error("the gate column order must be app icon → wordmark → invite line → glass panel")
	}
	// inside the card: name field → join button → escape-hatch link
	nameAt := strings.Index(html, `id="guestNameField"`)
	joinAt := strings.Index(html, `id="joinAccess"`)
	escapeAt := strings.Index(html, `id="guestMemberLink"`)
	if !(nameAt != -1 && nameAt < joinAt && joinAt < escapeAt) {
		t.Error("the glass panel order must be name field → join button → escape-hatch link")
	}
	if !strings.Contains(html, "joining as a member? sign in") {
		t.Error("the canon escape-hatch link copy is missing")
	}
}

// Canon states 2/6/7: the preview avatar initials track the typed name live
// (fallback "guest"); the join button reads "join as a guest" → "joining…"
// (disabled, opacity .7 via the --joining class) → "you're in" (the --joined
// secondary + check handoff frame); and the "fine-tune in settings" line stays
// off the guest popover.
func TestIndexGuestGateInitialsAndJoinCopy(t *testing.T) {
	html := readIndexHTMLForGuest(t)

	// name → initials: the shared initials source reads the gate field first
	initialsBody := functionBody(html, "function greenRoomAvatarInitials()")
	if !strings.Contains(initialsBody, "guestNameInput.value.trim() || currentParticipantName || guestSession?.guestName || 'guest'") {
		t.Error("greenRoomAvatarInitials must derive guest initials from the typed name (fallback \"guest\")")
	}
	// the input listener repaints the preview avatar on every keystroke
	listenAt := strings.Index(html, "guestNameInput.addEventListener('input'")
	if listenAt == -1 {
		t.Fatal("the guest name input listener is missing")
	}
	listener := html[listenAt:]
	if len(listener) > 400 {
		listener = listener[:400]
	}
	if !strings.Contains(listener, "greenRoomAvatarEl.textContent = greenRoomAvatarInitials()") {
		t.Error("typing a guest name must live-update the preview avatar initials (canon state 2)")
	}

	// join button label sequence (states 1/6/7)
	modeBody := functionBody(html, "function renderLoginMode()")
	if !strings.Contains(modeBody, "guestSession ? `join ${guestSession.roomName || 'the room'}` : 'join as a guest'") {
		t.Error("the idle guest join button must read the canon lowercase \"join as a guest\"")
	}
	if !strings.Contains(modeBody, `joinAccessButton.textContent = "you're in"`) || !strings.Contains(modeBody, "joinAccessButton.classList.add('login-signin--joined')") {
		t.Error("the connected handoff frame must render \"you're in\" with the secondary --joined class")
	}
	if !strings.Contains(modeBody, "joinAccessButton.classList.remove('login-signin--joining', 'login-signin--joined')") {
		t.Error("renderLoginMode must clear the transient join-frame classes before re-applying them")
	}
	joinBody := functionBody(html, "async function joinGuestRoom()")
	if !strings.Contains(joinBody, "joinAccessButton.textContent = 'joining…'") || !strings.Contains(joinBody, "joinAccessButton.classList.add('login-signin--joining')") {
		t.Error("joinGuestRoom must flip the button to the canon \"joining…\" frame")
	}
	// canon state 6 holds "joining…" through the media + dial phase, not just
	// the /guest/join POST: both the entry-flag seam and any mid-entry
	// renderLoginMode re-render must keep the busy frame.
	affordanceBody := functionBody(html, "function syncRoomEntryAffordance()")
	if !strings.Contains(affordanceBody, "appShell.classList.contains('is-guest')") || !strings.Contains(affordanceBody, "joinAccessButton.textContent = 'joining…'") {
		t.Error("syncRoomEntryAffordance must hold the guest gate button in the \"joining…\" frame while roomEntryInProgress")
	}
	if !strings.Contains(modeBody, "} else if (roomEntryInProgress) {") {
		t.Error("a renderLoginMode re-render mid-entry must not drop the guest button back to the idle label")
	}
	if !strings.Contains(joinBody, "joinAccessButton.classList.remove('login-signin--joining')") {
		t.Error("a failed join must restore the button out of the joining frame")
	}
	// the joining frame carries the canon .7 opacity; the connected frame is
	// the secondary look and beats [disabled]
	if !strings.Contains(html, ".login-signin--joining { opacity: 0.7; }") {
		t.Error("the joining frame must dim to opacity .7 (canon)")
	}
	if !strings.Contains(html, ".login-signin.login-signin--joined {") {
		t.Error("the connected frame must override [disabled] via the compound .login-signin.login-signin--joined selector")
	}

	// the full settings escape hatch stays off the guest popover
	renderBody := functionBody(html, "function renderGreenRoom()")
	if !strings.Contains(renderBody, "greenRoomFineTuneEl.hidden = guestMode") {
		t.Error("the \"fine-tune in settings\" line must be hidden on the guest popover")
	}
}
