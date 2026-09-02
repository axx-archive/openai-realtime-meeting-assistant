package main

import (
	"os"
	"strings"
	"testing"
)

// Wave 6 (Rooms in-call) static pins: live captions, reactions + hand raise,
// camera/speaker pickers, screen share on its own transceiver, host controls,
// the per-tile quality badge, the text-only Scout seat, room-chat parity and
// the dock's chrome-tier migration. Each pin names the exact seam a regression
// would have to remove, so the failure message reads as the missing feature.

func readIndexHTMLForWave6(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(body)
}

func requireAll(t *testing.T, haystack, where string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(haystack, want) {
			t.Errorf("%s is missing %q", where, want)
		}
	}
}

func TestIndexRoomsWave6LiveCaptionsToggleAndReducedMotion(t *testing.T) {
	html := readIndexHTMLForWave6(t)
	requireAll(t, html, "captions markup",
		`id="roomCaptionsToggle" class="btn btn--ghost room-island-toggle" type="button" aria-label="Show live captions" aria-pressed="false"`,
		`id="roomCaptions" class="room-captions" role="log" aria-live="polite"`,
		`captions: ['M4 5h16a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2Z'`,
		`strideIcon('captions', { size: 20 })`,
		`var roomCaptionsEnabled = false`,
		`var roomCaptionsHoldMs = 6000`,
	)
	body := functionBody(html, "function pushRoomCaption(entry)")
	if body == "" {
		t.Fatal("pushRoomCaption is missing")
	}
	requireAll(t, body, "pushRoomCaption",
		"if (!roomCaptionsEnabled",
		"room-captions__who",
		"roomCaptionsEl.children.length > 2",
		"if (reducedMotion.matches) {",
		"line.classList.add('is-fading')",
		"roomCaptionsHoldMs",
		"=== 'room_chat') return",
	)
	// phones fold captions / hand / lock off the island; the reaction row keeps
	// the hand as its last item so the verb never disappears
	requireAll(t, html, "phone island fold",
		`#appShell.is-in-room[data-tool="room"] .meeting-bar .controls #roomCaptionsToggle,`,
		"{ id: 'hand', label: '✋', title: 'Raise hand', onSelect: () => sendRoomHand(!roomHandRaisedSelf()) }",
	)
	// the transcript stream feeds the overlay
	feed := functionBody(html, "function appendRoomMeetingTranscriptEntry(payload)")
	if !strings.Contains(feed, "pushRoomCaption(entry)") {
		t.Error("appendRoomMeetingTranscriptEntry no longer feeds the captions overlay")
	}
	// mono speaker, token-timed fade so reduced motion zeroes it
	requireAll(t, html, "captions CSS",
		".room-captions__who {\n        font: 500 11px/1.2 var(--font-mono);",
		".room-captions__line {",
		"transition: opacity var(--dur-slow) var(--ease);\n      }\n      .room-captions__line.is-fading { opacity: 0; }",
	)
}

func TestIndexRoomsWave6ReactionsMatchServerAllowlist(t *testing.T) {
	html := readIndexHTMLForWave6(t)
	want := "var roomReactionAllowlist = ['" + strings.Join(roomReactionAllowlist, "', '") + "']"
	if !strings.Contains(html, want) {
		t.Fatalf("the client reaction row must equal the server allowlist: want %s", want)
	}
	send := functionBody(html, "function sendRoomReaction(emoji)")
	requireAll(t, send, "sendRoomReaction",
		"roomReactionAllowlist.includes(value)",
		"now - roomReactionLastSentAt < 1000",
		"sendRoomSocketEvent('room_reaction', { emoji: value })",
	)
	menu := functionBody(html, "function ensureRoomReactionMenu()")
	requireAll(t, menu, "reaction menu", "bfMenu(roomReactToggleButton, {", "orientation: 'horizontal'", "fixed: true")
	burst := functionBody(html, "function spawnRoomReactionBurst(name, emoji)")
	requireAll(t, burst, "reaction burst", "if (reducedMotion.matches) {", "burst.classList.add('is-still')", "animationend")
	requireAll(t, html, "reaction CSS",
		"animation: room-reaction-rise calc(var(--dur-slow) * 4) var(--ease) forwards;",
		"@keyframes room-reaction-rise {",
	)
	if strings.Contains(html, "@keyframes room-reaction-rise {\n        0% { top:") {
		t.Error("reaction bursts must ride transform + opacity, never layout properties")
	}
	requireAll(t, html, "reaction dispatch", "case 'room_reaction':\n            handleRoomReaction(message.data)")
}

func TestIndexRoomsWave6HandRaiseRosterStrip(t *testing.T) {
	html := readIndexHTMLForWave6(t)
	requireAll(t, html, "hand raise markup",
		`id="roomHandToggle" class="btn btn--ghost room-island-toggle" type="button" aria-label="Raise hand" aria-pressed="false"`,
		`id="roomHandsStrip" class="room-hands" role="list" aria-label="Raised hands" hidden`,
		"sendRoomSocketEvent('room_hand', { raised: Boolean(raised) })",
		"['hand', 'hand raised']",
		".video-tile.is-hand-raised .media-flag--hand { display: inline-flex; }",
	)
	snapshot := functionBody(html, "function applyRoomInCallSnapshot(payload)")
	requireAll(t, snapshot, "applyRoomInCallSnapshot", "payload.raisedHands", "payload.handRaisedAt", "payload.hostMutedAt", "payload.locked", "renderRoomHandsStrip()")
	strip := functionBody(html, "function renderRoomHandsStrip()")
	requireAll(t, strip, "renderRoomHandsStrip", "roomRaisedHands", "room-hands__order", "String(index + 1)", "strideIcon('hand', { size: 12 })")
	if !strings.Contains(functionBody(html, "function applyRoomSnapshot(payload)"), "applyRoomInCallSnapshot(payload)") {
		t.Error("the participants snapshot no longer carries raisedHands into the in-call state")
	}
	tile := functionBody(html, "function applyRoomInCallTileState(tile, name)")
	requireAll(t, tile, "tile hand flag", "tile.classList.toggle('is-hand-raised', raised)", "media-flag--hand")
}

func TestIndexRoomsWave6CameraAndSpeakerPickers(t *testing.T) {
	html := readIndexHTMLForWave6(t)
	requireAll(t, html, "device pickers",
		`<select id="cameraSelect" class="device-select" disabled>`,
		`<select id="speakerSelect" class="device-select" disabled>`,
		`<select id="greenRoomCameraSelect" class="device-select device-select--compact" aria-label="camera" disabled>`,
		`<select id="greenRoomSpeakerSelect" class="device-select device-select--compact" aria-label="speaker" disabled>`,
		"typeof HTMLMediaElement.prototype.setSinkId === 'function'",
		"device.kind === 'videoinput'",
		"device.kind === 'audiooutput'",
		"var roomDevicePreferencesStorageKey = 'bonfire.devices.v1'",
	)
	save := functionBody(html, "function saveRoomDevicePreference(kind, device)")
	requireAll(t, save, "saveRoomDevicePreference", "try {", "window.localStorage?.setItem(roomDevicePreferencesStorageKey", "} catch {")
	load := functionBody(html, "function loadRoomDevicePreferences()")
	requireAll(t, load, "loadRoomDevicePreferences", "try {", "} catch {")
	// the speaker picker only mounts where setSinkId exists
	refresh := functionBody(html, "async function refreshRoomDevicePickers()")
	requireAll(t, refresh, "refreshRoomDevicePickers", "audioOutputSelectionSupported()", `closest('[data-device="speaker"]')?.toggleAttribute('hidden', !speakerVisible)`)
	// the remembered camera rides every capture path
	for _, site := range []string{
		"video: preferredCameraConstraints(mediaConstraints.video),",
		"{ label: 'camera recovery', constraints: preferredCameraConstraints(mediaConstraints.video) },",
		"getUserMedia({ video: preferredCameraConstraints(true), audio: false })",
	} {
		if !strings.Contains(html, site) {
			t.Errorf("camera preference is not applied at %q", site)
		}
	}
	if !strings.Contains(functionBody(html, "function createRemoteAudioElement(stream, name)"), "applyPreferredSpeakerToElement(audio)") {
		t.Error("new remote audio elements do not route to the chosen speaker")
	}
}

func TestIndexRoomsWave6ScreenShareRidesItsOwnTransceiver(t *testing.T) {
	html := readIndexHTMLForWave6(t)
	start := functionBody(html, "async function startScreenShare()")
	if start == "" {
		t.Fatal("startScreenShare is missing")
	}
	requireAll(t, start, "startScreenShare",
		"audio: true",
		"screenShareUplinkTransceivers(pc)",
		"sender = shareUplink.video?.sender || cameraSender",
		"attachScreenShareToOwnUplink(shareUplink, screenTrack, screenAudioTrack)",
		"attachScreenShareToCameraSender(sender, screenTrack)",
		"screenShareRidesOwnSender = Boolean(shareUplink.video)",
	)
	for _, forbidden := range []string{"cameraSender.replaceTrack(", "audio: false"} {
		if strings.Contains(start, forbidden) {
			t.Errorf("startScreenShare must not %s — the share has its own sender and asks for audio", forbidden)
		}
	}
	uplink := functionBody(html, "function screenShareUplinkTransceivers(peer = pc)")
	requireAll(t, uplink, "screenShareUplinkTransceivers", "section.direction !== 'recvonly'", "if (slot === 1) result[section.kind] = transceiver")
	answer := functionBody(html, "async function attachLocalTracksToOffer(offer, sessionPeer = pc, isCurrent = () => pc === sessionPeer)")
	requireAll(t, answer, "attachLocalTracksToOffer", "outboundTrackForUplink(section, slot)", "if (slot > 0 && transceiver.direction !== 'sendonly') {")
	stop := functionBody(html, "async function stopScreenShare()")
	requireAll(t, stop, "stopScreenShare", "detachScreenShareFromOwnUplink(pc)", "cameraRestored = true")
	camera := functionBody(html, "function outboundTrackForUplink(section, slot)")
	if !strings.Contains(camera, "section.kind === 'video' && screenShareRidesOwnSender ? localVideoTrack() : track") {
		t.Error("uplink slot 0 must keep the camera while the share rides its own uplink")
	}
	// receiver half: one seat per (participant, source)
	track := functionBody(html, "function handleParticipantTrack(track)")
	requireAll(t, track, "handleParticipantTrack", "track?.source", "adoptRemoteShareByKeys(")
	if !strings.Contains(functionBody(html, "function remoteScreenShareStream(name)"), "remoteShareStreamForParticipant(participantName)") {
		t.Error("the screen stage does not prefer the share's own stream")
	}
	if !strings.Contains(functionBody(html, "function handleScreenShareStopped(event)"), "removeRemoteShareMediaByName(name)") {
		t.Error("screen_share_stopped must drop the share holder (zombie guard)")
	}
}

func TestIndexRoomsWave6HostControls(t *testing.T) {
	html := readIndexHTMLForWave6(t)
	requireAll(t, html, "moderation dispatch",
		"case 'room_moderate':\n            handleRoomModerate(message.data)",
		"case 'room_moderate_request':\n            handleRoomModerateRequest(message.data)",
		"case 'room_participant_removed':\n            handleRoomParticipantRemoved(message.data)",
		`id="roomLockToggle" class="btn btn--ghost room-island-toggle" type="button" aria-label="Lock room" aria-pressed="false" title="Lock room" hidden disabled`,
	)
	// room-scoped like every other room event
	guardAt := strings.Index(html, "case 'room_participant_removed':\n            if (kanbanEventRoomId(message.data) !== (activeJoin.roomId || 'office')) {")
	if guardAt < 0 {
		t.Error("the Wave 6 room events are not room-scoped in handleKanbanMessage")
	}
	request := functionBody(html, "function handleRoomModerateRequest(data)")
	requireAll(t, request, "handleRoomModerateRequest", "setLocalMute(true, { announce: false })", "'The host muted you'")
	removed := functionBody(html, "function handleRoomParticipantRemoved(data)")
	requireAll(t, removed, "handleRoomParticipantRemoved", "data?.self === true", "handleRemovedByHost(by)")
	self := functionBody(html, "function handleRemovedByHost(by)")
	requireAll(t, self, "handleRemovedByHost", "leaveRoom()", "renderGuestExitState(message, { ended: true })", "showRoomRemovedNotice(message)", "you were removed from the room by")
	menu := functionBody(html, "function ensureTileHostMenu(tile, name)")
	requireAll(t, menu, "tile host menu", "roomHostControlsAvailable()", "tile-host-menu", "bfMenu(button, {", "label: 'Mute microphone'", "label: 'Remove from room', danger: true", "sendRoomModerate('mute'", "sendRoomModerate('remove'")
	lock := functionBody(html, "function wireRoomInCallControls()")
	if !strings.Contains(lock, "sendRoomModerate(roomLocked ? 'unlock' : 'lock')") {
		t.Error("the island lock toggle does not send room_moderate lock/unlock")
	}
	if !strings.Contains(functionBody(html, "function syncRoomInCallControls()"), "roomLockToggleButton.hidden = !manager") {
		t.Error("the lock toggle must hide for non-managers")
	}
}

func TestIndexRoomsWave6QualityBadge(t *testing.T) {
	html := readIndexHTMLForWave6(t)
	requireAll(t, html, "quality badge",
		"inboundVideoByParticipant: {},",
		"snapshot.inboundVideoByParticipant[videoParticipant.toLowerCase()] = {",
		"renderTileQualityBadges(snapshot, previousMediaQualityStats)",
		".tile-quality {",
		"font: 500 10px/1.2 var(--font-mono);",
		`.video-tile[data-quality="poor"] .tile-quality { color: var(--warn); }`,
	)
	badge := functionBody(html, "function ensureTileQualityBadge(tile, name)")
	requireAll(t, badge, "ensureTileQualityBadge", "if (!grade || grade.level === 'good') {", "delete tile.dataset.quality")
	grade := functionBody(html, "function renderTileQualityBadges(snapshot, previous)")
	requireAll(t, grade, "renderTileQualityBadges", "packetsLost", "freezeCount", "outboundVideoQualityLimitationReason", "level = 'poor'", "level = 'fair'")
	// hidden while good: the base rule is display:none, only fair/poor show
	at := strings.Index(html, ".tile-quality {")
	if at < 0 || !strings.Contains(html[at:at+400], "display: none;") {
		t.Error("the quality badge must stay hidden while the seat is good")
	}
}

func TestIndexRoomsWave6ScoutTextSeat(t *testing.T) {
	html := readIndexHTMLForWave6(t)
	sync := functionBody(html, "function syncRoomScoutQuickAction()")
	requireAll(t, sync, "syncRoomScoutQuickAction",
		"'Scout · text only'",
		"'Add Scout · text only'",
		"if (!guestMode && roomScoutTextAvailability?.enabled) roomScoutQuickActionButton.hidden = false",
	)
	invite := functionBody(html, "async function setDesktopRoomScout(action)")
	requireAll(t, invite, "setDesktopRoomScout", "const mode = roomScoutInviteMode()", "...(action === 'invite' && mode ? { mode } : {})", "normalizeScoutTextAvailability(payload.scoutText)", "'Scout joined the room chat — text only'")
	requireAll(t, html, "agent mode projection",
		"mode: String(value.mode || 'voice').trim().toLowerCase() === 'text' ? 'text' : 'voice',",
		"name.textContent = agent.mode === 'text' ? `${agent.name} · text only` : agent.name",
		"if (String(agent?.mode || '') === 'text') return 'In the chat'",
	)
	if strings.Contains(html, "roomScoutQuickActionButton?.addEventListener('click', () => {\n        if (!roomScoutVoiceAvailability.enabled) return") {
		t.Error("the Add-Scout click is still gated on voice alone")
	}
}

func TestIndexRoomsWave6RoomChatParityAndDockTier(t *testing.T) {
	html := readIndexHTMLForWave6(t)
	node := functionBody(html, "function roomChatMessageNode(message)")
	if !strings.Contains(node, "mountDesktopChatLinkPreview(item, String(message?.text || ''))") {
		t.Error("room chat no longer mounts link previews for new messages")
	}
	requireAll(t, html, "own vs peer bubbles",
		".scout-chat-msg--user .scout-chat-text {\n        background: var(--accent);",
		".scout-chat-msg--peer .scout-chat-text {\n        background: var(--surface-2);",
	)
	// live QA: the composer ring renders whole (a side lane inside the
	// scrolling tab panel) and the reaction row sizes to its nine squares
	requireAll(t, html, "room chat composer lane", ".room-chat__form {\n        flex: none;", "margin: 0 4px var(--sp-2);")
	requireAll(t, html, "reaction row geometry",
		".bf-menu--horizontal.room-react__menu {\n        display: inline-flex;",
		"width: max-content;",
		".bf-menu--horizontal.room-react__menu .bf-menu__item {\n        display: grid;\n        place-items: center;",
		".bf-menu--horizontal.room-react__menu .bf-menu__item[aria-checked=\"true\"] { background: var(--well); }",
	)
	// the in-call dock rides the chrome glass tier now
	at := strings.Index(html, "      .controls {\n        position: relative;")
	if at < 0 {
		t.Fatal("the .controls rule moved")
	}
	rule := html[at : strings.Index(html[at:], "}")+at]
	requireAll(t, rule, ".controls", "background: var(--glass-chrome-fill);", "backdrop-filter: var(--glass-chrome-filter);", "border: 1px solid var(--line);")
	for _, literal := range []string{"rgba(24, 24, 27, 0.6)", "rgba(18, 18, 21, 0.72)", "rgba(18, 18, 21, 0.9)"} {
		if strings.Contains(html, literal) {
			t.Errorf("dock literal %s survived the chrome-tier migration", literal)
		}
	}
}
