package main

// Rooms-UX RW2 (docs/plans/rooms-ux-2026-07-09.md §3.2/§3.5/§3.6): the green
// room (pre-join AV check) + join hardening pins. The invariants: the preview
// camera lights ONLY inside the two explicit-tap handlers (never a boot or
// render path); joinRoom acquires media BEFORE the websocket dials, under a
// 12s watchdog with an honest lobby failure state; bonfire:joinAV is read
// after media resolves and carried through the real mute machinery; the guest
// gate names its room via POST /guest/lookup and demotes member sign-in to a
// quiet text link.

import (
	"os"
	"strings"
	"testing"
)

func readIndexHTMLForGreenRoom(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(data)
}

// joinRoom's signature has default-param braces that functionBody trips on;
// slice it the way frontend_latency_test.go does.
func joinRoomSource(t *testing.T, html string) string {
	t.Helper()
	start := strings.Index(html, "async function joinRoom(options = {})")
	if start == -1 {
		t.Fatal("index.html missing joinRoom")
	}
	end := strings.Index(html[start:], "function joinMediaWithWatchdog(voiceOnly)")
	if end == -1 {
		t.Fatal("index.html missing joinMediaWithWatchdog after joinRoom")
	}
	return html[start : start+end]
}

// The green-room block: one component, two mounts. It renders inside the
// lobby hero, and the guest boot re-parents it into the name gate.
func TestIndexGreenRoomMarkupAndSharedMount(t *testing.T) {
	html := readIndexHTMLForGreenRoom(t)

	heroAt := strings.Index(html, `id="roomNotConnected"`)
	blockAt := strings.Index(html, `id="greenRoom"`)
	railAt := strings.Index(html, `id="lobbyRail"`)
	if heroAt == -1 || blockAt == -1 || railAt == -1 || !(heroAt < blockAt && blockAt < railAt) {
		t.Error("the green-room block must render inside the lobby hero (before the rooms rail)")
	}
	for _, want := range []string{
		`id="greenRoomTile"`,
		`id="greenRoomVideo"`,
		`id="greenRoomMicChip"`,
		`id="greenRoomCamChip"`,
		`id="greenRoomLooks"`,
		`id="greenRoomFineTune"`,
		`id="lobbyJoinError"`,
		"check your camera",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("green-room markup missing %s", want)
		}
	}
	// the compact looks row drives the SAME persisted look state as settings:
	// its chips carry the shared class + data-look values, so the existing
	// videoLookChips wiring and render pass pick them up with zero new plumbing
	looksAt := strings.Index(html, `id="greenRoomLooks"`)
	looksBlock := html[looksAt : looksAt+1400]
	for _, look := range []string{`data-look="none"`, `data-look="bonfire-warm"`, `data-look="blur"`} {
		if !strings.Contains(looksBlock, look) {
			t.Errorf("green-room looks row missing chip %s", look)
		}
	}
	if !strings.Contains(looksBlock, "video-look-chip") {
		t.Error("green-room look chips must share the video-look-chip class (one wiring, one render pass)")
	}
	// the self-preview mirrors like every local tile
	if !strings.Contains(html, ".greenroom__video") || !strings.Contains(html, "transform: scaleX(-1)") {
		t.Error("the green-room self-preview must mirror (scaleX(-1))")
	}

	// guest mount: the boot branch re-parents the ONE block into the gate
	mountBody := functionBody(html, "function mountGreenRoomInGuestGate()")
	if mountBody == "" {
		t.Fatal("could not extract mountGreenRoomInGuestGate body")
	}
	if !strings.Contains(mountBody, "insertBefore(greenRoomEl, joinAccessButton)") {
		t.Error("the guest gate mount must re-parent the green room above the join button")
	}
	guestBootAt := strings.Index(html, "appShell.classList.add('is-guest')")
	mountAt := strings.Index(html, "mountGreenRoomInGuestGate()")
	if guestBootAt == -1 || mountAt == -1 || mountAt < guestBootAt || mountAt-guestBootAt > 600 {
		t.Error("the guest boot branch must mount the green room into the gate")
	}
}

// Camera policy: the preview acquires ONLY inside the two explicit-tap
// handlers (tile tap, cam chip flipping on). No boot pass, no render pass,
// no lobby re-render may light the camera.
func TestIndexGreenRoomPreviewExplicitTapOnly(t *testing.T) {
	html := readIndexHTMLForGreenRoom(t)

	// the green-room preview enters the shared pipeline through exactly one
	// door, inside the acquire function
	if got := strings.Count(html, "startVideoLookPreview(greenRoomVideoEl)"); got != 1 {
		t.Fatalf("startVideoLookPreview(greenRoomVideoEl) must appear exactly once (inside greenRoomAcquirePreview), found %d", got)
	}
	acquireBody := functionBody(html, "async function greenRoomAcquirePreview()")
	if acquireBody == "" {
		t.Fatal("could not extract greenRoomAcquirePreview body")
	}
	if !strings.Contains(acquireBody, "startVideoLookPreview(greenRoomVideoEl)") {
		t.Error("greenRoomAcquirePreview must drive the shared preview pipeline at the green-room tile")
	}

	// exactly three greenRoomAcquirePreview( sites: the definition, the tile
	// tap handler, and the cam-chip-on branch
	if got := strings.Count(html, "greenRoomAcquirePreview("); got != 3 {
		t.Fatalf("greenRoomAcquirePreview must have exactly 3 occurrences (definition + tile tap + cam-chip-on), found %d", got)
	}
	toggleBody := functionBody(html, "function toggleGreenRoomChip(kind)")
	if !strings.Contains(toggleBody, "greenRoomAcquirePreview()") {
		t.Error("flipping the cam chip on must start the preview (explicit intent)")
	}
	tileWireAt := strings.Index(html, "greenRoomTileEl.addEventListener('click'")
	if tileWireAt == -1 {
		t.Fatal("the tile tap handler is missing")
	}
	if !strings.Contains(html[tileWireAt:tileWireAt+700], "greenRoomAcquirePreview()") {
		t.Error("the tile tap handler must be the second explicit acquire seam")
	}

	// render passes never acquire
	for _, fn := range []string{
		"function renderGreenRoom()",
		"function renderLobby()",
		"function renderLoginMode()",
	} {
		body := functionBody(html, fn)
		if body == "" {
			t.Fatalf("could not extract %s body", fn)
		}
		if strings.Contains(body, "greenRoomAcquirePreview") || strings.Contains(body, "startVideoLookPreview") {
			t.Errorf("%s must never acquire the preview camera (browsing never lights it)", fn)
		}
	}
}

// Both watchdogs + full teardown: the 8s preview watchdog flips the cam chip
// off with honest copy (and a late camera never stays hot); the preview stops
// on join, on deselect, on tool switch away, and on the guest terminal card.
func TestIndexGreenRoomWatchdogAndTeardownSeams(t *testing.T) {
	html := readIndexHTMLForGreenRoom(t)

	acquireBody := functionBody(html, "async function greenRoomAcquirePreview()")
	if !strings.Contains(acquireBody, "}, 8000)") {
		t.Error("the preview acquire must race an 8s watchdog")
	}
	if !strings.Contains(acquireBody, "camera didn't answer — you can join with it off.") &&
		!strings.Contains(html, "camera didn't answer — you can join with it off.") {
		t.Error("the timeout state must speak the honest copy")
	}
	// late resolve: the superseded/timed-out branch releases the camera
	if !strings.Contains(acquireBody, "stopVideoLookPreview()") {
		t.Error("a camera that answers after teardown/timeout must be released, never left hot")
	}

	stopBody := functionBody(html, "function stopGreenRoomPreview(nextState)")
	if stopBody == "" {
		t.Fatal("could not extract stopGreenRoomPreview body")
	}
	if !strings.Contains(stopBody, "greenRoomPreviewGen++") || !strings.Contains(stopBody, "stopVideoLookPreview()") {
		t.Error("stopGreenRoomPreview must invalidate in-flight acquires and release the shared pipeline")
	}
	// teardown seams: join, deselect, tool switch away, guest terminal
	joinSource := joinRoomSource(t, html)
	if !strings.Contains(joinSource, "stopGreenRoomPreview()") {
		t.Error("joinRoom must stop the green-room preview before acquiring join media")
	}
	if !strings.Contains(functionBody(html, "function selectLobbyRoom(roomId)"), "stopGreenRoomPreview()") {
		t.Error("deselecting/re-selecting a room must release the preview camera")
	}
	toolBody := functionBody(html, "function applyToolState(tool)")
	if !strings.Contains(toolBody, "stopGreenRoomPreview()") {
		t.Error("switching tools away must release the preview camera")
	}
	if !strings.Contains(functionBody(html, "function renderGuestExitState(message, options)"), "stopGreenRoomPreview()") {
		t.Error("the guest terminal card must never keep a hot preview camera")
	}

	// the generalized pipeline: one preview at a time, the target is released
	// and reset on stop
	stopPreviewBody := functionBody(html, "function stopVideoLookPreview()")
	if !strings.Contains(stopPreviewBody, "previewLookTargetEl = null") {
		t.Error("stopVideoLookPreview must reset the preview target")
	}
}

// bonfire:joinAV: chips persist per device, and joinRoom reads the record
// AFTER media resolves, applying it through the real mute machinery so the
// meeting bar reflects it (tracks still acquired when off — fast unmute).
func TestIndexGreenRoomJoinAVCarriedAfterMediaResolve(t *testing.T) {
	html := readIndexHTMLForGreenRoom(t)

	loadBody := functionBody(html, "function loadJoinAVPreferences()")
	saveBody := functionBody(html, "function saveJoinAVPreferences(prefs)")
	if !strings.Contains(loadBody, "'bonfire:joinAV'") || !strings.Contains(saveBody, "'bonfire:joinAV'") {
		t.Error("mic/cam chips must persist to localStorage bonfire:joinAV")
	}

	joinSource := joinRoomSource(t, html)
	mediaAt := strings.Index(joinSource, "localStream = await createLocalMediaStream(captureStream)")
	readAt := strings.Index(joinSource, "loadJoinAVPreferences()")
	if mediaAt == -1 || readAt == -1 || readAt < mediaAt {
		t.Error("joinRoom must read bonfire:joinAV AFTER the join media resolves")
	}
	if !strings.Contains(joinSource, "setLocalCameraOff(true)") || !strings.Contains(joinSource, "setLocalMute(true)") {
		t.Error("the joinAV carry must ride the existing mute machinery (track.enabled + meeting-bar state)")
	}

	// beginMediaSession derives mute honestly from track.enabled so the carry
	// (and mid-call mutes on reconnect) survive the grant. functionBody trips
	// on the default-param braces, so slice a window instead.
	start := strings.Index(html, "async function beginMediaSession(options = {})")
	if start == -1 {
		t.Fatal("index.html missing beginMediaSession")
	}
	sessionBody := html[start : start+4000]
	if !strings.Contains(sessionBody, "localStream.getAudioTracks().every(track => !track.enabled)") ||
		!strings.Contains(sessionBody, "localStream.getVideoTracks().every(track => !track.enabled)") {
		t.Error("beginMediaSession must derive isMicMuted/isCameraOff from track.enabled — never force-unmute a joinAV carry")
	}
}

// Join hardening (recon-09): media BEFORE the dial, a 12s watchdog, and an
// honest failure state — the shell never sticks in is-room-entering and no
// socket (no phantom seat) ever opens without media.
func TestIndexGreenRoomJoinOrderSwapAndWatchdog(t *testing.T) {
	html := readIndexHTMLForGreenRoom(t)
	joinSource := joinRoomSource(t, html)

	acquireAt := strings.Index(joinSource, "await joinMediaWithWatchdog(voiceOnly)")
	dialAt := strings.Index(joinSource, "openRoomWebSocket()")
	if acquireAt == -1 || dialAt == -1 || acquireAt > dialAt {
		t.Error("joinRoom must acquire media BEFORE openRoomWebSocket() — the seat is only taken after media resolves")
	}

	watchdogBody := functionBody(html, "function joinMediaWithWatchdog(voiceOnly)")
	if watchdogBody == "" {
		t.Fatal("could not extract joinMediaWithWatchdog body")
	}
	if !strings.Contains(watchdogBody, "}, 12000)") {
		t.Error("the join media acquisition must race a 12s watchdog")
	}
	// a capture resolving after the timeout is stopped on arrival
	if !strings.Contains(watchdogBody, "stream.getTracks().forEach(track => { try { track.stop() } catch (_) {} })") {
		t.Error("a late-resolving capture must be stopped, never left hot")
	}

	// default-param braces trip functionBody — slice a window instead
	failAt := strings.Index(html, "function failRoomEntryForMedia(error, options = {})")
	if failAt == -1 {
		t.Fatal("index.html missing failRoomEntryForMedia")
	}
	failBody := html[failAt : failAt+2200]
	if !strings.Contains(failBody, "setRoomEntryInProgress(false)") {
		t.Error("a media failure must clear the entry flag — no stuck is-room-entering shell")
	}
	if !strings.Contains(failBody, "couldn't reach your camera or microphone — check permissions and try again.") {
		t.Error("the failure state must speak the honest lobby copy")
	}
	if !strings.Contains(failBody, "roomEntryMediaFailed = true") || !strings.Contains(failBody, "showLobbyJoinError(message)") {
		t.Error("the failure must land on the lobby error line")
	}
	// the join affordance flips to `try again` until the next attempt
	affordanceBody := functionBody(html, "function syncRoomEntryAffordance()")
	if !strings.Contains(affordanceBody, "roomEntryMediaFailed ? 'try again'") {
		t.Error("the lobby join button must read `try again` after a media failure")
	}
	// cleared on the next attempt
	if !strings.Contains(joinSource, "clearLobbyJoinError()") {
		t.Error("a fresh join attempt must clear the failure state")
	}

	// reconnects untouched: the reconnect gate still requires live localStream
	if !strings.Contains(html, "return Boolean(!isLeavingRoom && (authedUser || (guestMode && guestSession)) && currentParticipantName && localStream)") {
		t.Error("roomCanReconnectSignal must keep requiring localStream (reconnects re-use media, never re-acquire through the watchdog)")
	}
}

// The guest gate (RW2): names its room through POST /guest/lookup on boot,
// shows the honest expired state on a dead token, and demotes member sign-in
// to a quiet text link — Face ID chrome never renders on the gate.
func TestIndexGreenRoomGuestGateLookupAndDemotedMemberDoor(t *testing.T) {
	html := readIndexHTMLForGreenRoom(t)

	lookupBody := functionBody(html, "async function lookupGuestInvite()")
	if lookupBody == "" {
		t.Fatal("could not extract lookupGuestInvite body")
	}
	if !strings.Contains(lookupBody, "postAuthJSON('/guest/lookup', { token: guestBootToken })") {
		t.Error("the gate must name its room through POST /guest/lookup with the fragment token")
	}
	// a dead token still tries the cookie resume before declaring expiry
	if !strings.Contains(lookupBody, "fetch('/guest/me'") || !strings.Contains(lookupBody, "guestInviteState = 'expired'") {
		t.Error("a dead token must fall back to the /guest/me resume before the expired state")
	}
	// boot wiring: token present → lookup; cookie-only → resume (unchanged)
	bootGateAt := strings.Index(html, "if (!guestBootToken) {")
	if bootGateAt == -1 || !strings.Contains(html[bootGateAt:bootGateAt+700], "lookupGuestInvite()") {
		t.Error("the guest boot branch must run the lookup when a fragment token is present")
	}

	modeBody := functionBody(html, "function renderLoginMode()")
	if !strings.Contains(modeBody, "you're invited to ${guestSession?.roomName || guestInviteRoomName}") {
		t.Error("the gate subline must name the room from the lookup (pre-join) or the session (post-join)")
	}
	if !strings.Contains(modeBody, "'this invite has expired or been revoked.'") {
		t.Error("a dead invite must speak the honest expired line")
	}
	if !strings.Contains(modeBody, "joinAccessButton.hidden = mode === 'guest-exit' || guestInviteExpired") {
		t.Error("a dead invite must not render a live join button")
	}
	// Face ID stays off the gate; the member door is a quiet text link
	if !strings.Contains(modeBody, "passkeySignInButton.hidden = mode !== 'login'") {
		t.Error("the passkey button must stay hidden outside the member login mode")
	}
	if !strings.Contains(html, `id="guestMemberLink"`) || !strings.Contains(html, "joining as a member? sign in") {
		t.Error("the gate must carry the demoted `joining as a member? sign in` text link")
	}
	if !strings.Contains(modeBody, "guestMemberLinkEl.hidden = mode !== 'guest'") {
		t.Error("the member door must render on the guest gate only")
	}
}
