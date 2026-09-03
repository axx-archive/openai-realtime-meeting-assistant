package main

import (
	"os"
	"strings"
	"testing"
)

// Wave 7 (Rooms around-call) static pins: the upcoming list + schedule form
// on the lobby rail card with its timed-ICS link, the /?room= and /?record=
// deep links, the recording control (default off, manager switch, the
// in-call disc gated on the room setting, the 5 MB chunk sequence with the
// `final` flush on stop/leave, the consent branch) and the Meeting Record's
// recording playback + recap card. Each pin names the exact seam a
// regression would have to remove.

func readIndexHTMLForWave7(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(body)
}

func requireAllWave7(t *testing.T, haystack, where string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(haystack, want) {
			t.Errorf("%s is missing %q", where, want)
		}
	}
}

func TestIndexRoomsWave7UpcomingListAndScheduleForm(t *testing.T) {
	html := readIndexHTMLForWave7(t)
	// markup: the upcoming block lives INSIDE the rail card, after + new room
	railAt := strings.Index(html, `id="lobbyRail"`)
	createAt := strings.Index(html, `id="lobbyCreateForm"`)
	upcomingAt := strings.Index(html, `id="lobbyUpcoming"`)
	asideEnd := strings.Index(html[upcomingAt:], "</aside>")
	if railAt == -1 || createAt == -1 || upcomingAt == -1 || asideEnd == -1 || !(railAt < createAt && createAt < upcomingAt) {
		t.Error("the upcoming list must render on the rail card below the + new room form")
	}
	requireAllWave7(t, html, "schedule markup",
		`<span class="lobby__rail-label lobby__upcoming-label">upcoming</span>`,
		`id="lobbyUpcomingList" class="lobby__upcoming-list" role="list"`,
		`id="lobbyScheduleOpen" class="lobby__new lobby__schedule pressable" type="button" aria-expanded="false" aria-controls="lobbyScheduleForm"`,
		`id="lobbyScheduleForm" class="lobby__form lobby__schedule-form" hidden`,
		`<select id="lobbyScheduleRoom" class="lobby__input lobby__select" aria-label="Room"></select>`,
		`id="lobbyScheduleTitle"`,
		`id="lobbyScheduleStart" class="lobby__input lobby__input--mono" type="datetime-local"`,
		`id="lobbyScheduleDuration" class="lobby__chips" role="radiogroup" aria-label="Duration"`,
		`data-minutes="15"`, `data-minutes="30"`, `data-minutes="45"`, `data-minutes="60"`,
		`id="lobbyScheduleAttendees" class="chat-member-picker lobby__attendees" data-member-picker="schedule"`,
		`id="lobbyScheduleError" class="lobby__joinerror lobby__schedule-error" role="alert" hidden`,
		`id="lobbyScheduleCancel"`, `id="lobbyScheduleSubmit"`,
	)
	// the when-column is mono (a machine fact), the join verb is a real button
	requireAllWave7(t, html, "upcoming CSS",
		".lobby__upcoming-when {", "font: 500 11px/1.25 var(--font-mono);",
		".lobby__upcoming-row.is-live .lobby__upcoming-when strong { color: var(--live-text); }", // semantic hue as text goes through --*-text (UX audit rule; shell pass 2026-09-02)
		".lobby__upcoming-join {",
	)
	// the list reads GET ?upcoming=1, keeps 14 days, joins inside the 10-min lead
	load := functionBody(html, "async function loadLobbyScheduledMeetings(force = false)")
	requireAllWave7(t, load, "loadLobbyScheduledMeetings", "'/assistant/meetings/scheduled?upcoming=1'", "renderLobbyUpcomingRows()")
	requireAllWave7(t, html, "upcoming window",
		"var lobbyUpcomingWindowMs = 14 * 24 * 60 * 60 * 1000",
		"var lobbyUpcomingJoinLeadMs = 10 * 60 * 1000",
	)
	row := functionBody(html, "function lobbyUpcomingRow(entry, now)")
	requireAllWave7(t, row, "lobbyUpcomingRow",
		"const live = Boolean(meeting.inProgress) || (startMs <= now && now < endMs)",
		"const joinable = live || (startMs > now && startMs - now <= lobbyUpcomingJoinLeadMs)",
		"chatPersonAvatarNode(email)",
		"lobbyUpcomingJoin(meeting)",
		"openLobbyUpcomingMenu(more, meeting)",
	)
	// join is consent: the row joins through joinRoom(), never on selection
	join := functionBody(html, "function lobbyUpcomingJoin(meeting)")
	requireAllWave7(t, join, "lobbyUpcomingJoin", "selectLobbyRoom(String(meeting?.roomId || 'office'))", "record.passcodeRequired && !lobbyPasscodeMemory[record.id]", "joinRoom()")
	// the schedule form posts local → RFC 3339 UTC, the four duration chips,
	// attendees from the member picker; edit = PATCH; server copy on failure
	submit := functionBody(html, "async function submitLobbySchedule(event)")
	requireAllWave7(t, submit, "submitLobbySchedule",
		"startsAt: new Date(startMs).toISOString()",
		"durationMinutes: lobbyScheduleDurationMinutes",
		"attendees: lobbyScheduleAttendeePicker?.selected?.() || []",
		"w7RequestJSON('PATCH', `/assistant/meetings/scheduled/${encodeURIComponent(editing)}`, payload)",
		"w7RequestJSON('POST', '/assistant/meetings/scheduled', payload)",
		"lobbyScheduleShowError(result.data?.error ||",
		"result.status === 403", "result.status === 409",
	)
	requireAllWave7(t, html, "duration chips", "var lobbyScheduleDurations = [15, 30, 45, 60]")
	start := functionBody(html, "function startLobbySchedule(meeting = null)")
	requireAllWave7(t, start, "startLobbySchedule", "createChatMemberPicker(els.attendees, { placeholder: 'add attendees…', label: 'Attendees', initial })", "els.room.disabled = Boolean(meeting)")
	// the member picker accepts an initial selection so edit pre-fills chips
	requireAllWave7(t, html, "member picker initial", "(Array.isArray(options.initial) ? options.initial : [])", "if (state.selected.length) renderChips()")
	// organizer/manager verbs + the timed .ics link
	menu := functionBody(html, "function openLobbyUpcomingMenu(trigger, meeting)")
	requireAllWave7(t, menu, "openLobbyUpcomingMenu",
		"label: 'Add to calendar'", "lobbyScheduleDownloadICS(meeting)",
		"if (lobbyScheduleManaged(meeting))", "label: 'Edit'", "label: 'Cancel meeting'", "danger: true",
		"bfMenu(trigger, { items, fixed: true, bindTrigger: false",
	)
	ics := functionBody(html, "function lobbyScheduleDownloadICS(meeting)")
	requireAllWave7(t, ics, "lobbyScheduleDownloadICS", "String(meeting?.icsPath || '')", "link.download = ''")
	cancel := functionBody(html, "async function cancelLobbyScheduledMeeting(meeting)")
	requireAllWave7(t, cancel, "cancelLobbyScheduledMeeting", "w7RequestJSON('DELETE', `/assistant/meetings/scheduled/${encodeURIComponent(id)}`)", "result.status === 403")
	// renderLobby stays the one seam that repaints every lobby piece
	lobby := functionBody(html, "function renderLobby()")
	requireAllWave7(t, lobby, "renderLobby", "renderLobbyUpcoming()", "syncRoomRecordToggle()")
	// glyphs come from the icon system, never pasted svg
	requireAllWave7(t, html, "icons", "calendar: ['M5 4h14a2 2 0 0 1 2 2v13a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2Z'", "strideIcon('plus', { size: 13 })", "strideIcon('calendar')")
}

func TestIndexRoomsWave7DeepLinks(t *testing.T) {
	html := readIndexHTMLForWave7(t)
	requireAllWave7(t, html, "boot block",
		"var pendingMeetingRecordBootParam = ''",
		"pendingMeetingRecordBootParam = new URLSearchParams(window.location.search).get('record') || ''",
	)
	loader := functionBody(html, "async function loadRoomsList()")
	// /?room= → lobby with the room preselected (no auto-join), consumed once
	requireAllWave7(t, loader, "loadRoomsList", "pendingRoomBootParam = ''", "openLobbyForBootRoom()", "consumeMeetingRecordBootParam()")
	if strings.Contains(loader, "joinRoom(") {
		t.Error("the /?room= deep link must never auto-join")
	}
	lobbyBoot := functionBody(html, "function openLobbyForBootRoom()")
	requireAllWave7(t, lobbyBoot, "openLobbyForBootRoom", "history.replaceState({ view: 'pd1', destination: 'Video' }, '', PD1_PATHS.Video)", "selectPD1Destination('Video', { push: false, focus: false })")
	// /?record= → Meeting Records (Conversations · meeting-records) then the card
	record := functionBodyAfterSignature(html, "function openMeetingRecordDeepLink(meetingId, options = {})")
	requireAllWave7(t, record, "openMeetingRecordDeepLink",
		"selectPD1Destination('Conversations', { mode: 'meeting-records', push: false, focus: false })",
		"return jumpToMemoryMeeting(meetingId)",
		"history.replaceState(state, '', w7MeetingRecordsPath)",
	)
	requireAllWave7(t, html, "meeting records path", "var w7MeetingRecordsPath = '/memory'")
	consume := functionBody(html, "function consumeMeetingRecordBootParam()")
	requireAllWave7(t, consume, "consumeMeetingRecordBootParam", "pendingMeetingRecordBootParam = ''", "!appShell.classList.contains('is-authed')) return", "if (appShell.classList.contains('is-in-room')) return", "openMeetingRecordDeepLink(meetingId, { replace: true })")
}

func TestIndexRoomsWave7RecordingControl(t *testing.T) {
	html := readIndexHTMLForWave7(t)
	// the disc sits on the island beside the Wave 6 toggles, hidden + disabled
	// until the room's recording setting is on; the dot and timer ride it
	lockAt := strings.Index(html, `id="roomLockToggle"`)
	recordAt := strings.Index(html, `id="roomRecordToggle"`)
	consentAt := strings.Index(html, `id="consentToggle"`)
	if lockAt == -1 || recordAt == -1 || consentAt == -1 || !(lockAt < recordAt && recordAt < consentAt) {
		t.Error("#roomRecordToggle must mount on the island right after #roomLockToggle")
	}
	requireAllWave7(t, html, "recording markup",
		`id="roomRecordToggle" class="btn btn--ghost room-island-toggle room-record-toggle" type="button" aria-label="Start recording" aria-pressed="false" title="Record this meeting" hidden disabled`,
		`<span class="room-record-toggle__dot" aria-hidden="true"></span>`,
		`id="roomRecordTimer" class="room-record-toggle__timer" hidden>00:00</span>`,
		"record: ['M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18Z'",
		"strideIcon('record', { size: 20 })",
	)
	// the dot is --danger only while recording; the timer is mono; pre-join hidden
	requireAllWave7(t, html, "recording CSS",
		"#roomRecordToggle.is-recording .room-record-toggle__dot {", "background: var(--danger);",
		"animation: room-record-pulse var(--dur-breathe) var(--ease) infinite;",
		"#roomRecordToggle.is-recording .room-record-toggle__dot { animation: none; }",
		"font: 600 10px/1.4 var(--font-mono);",
		"#appShell:not(.is-in-room) #roomRecordToggle { display: none; }",
	)
	// gating: in-room + not guest + recordingEnabled from the rooms snapshot
	sync := functionBody(html, "function syncRoomRecordToggle()")
	requireAllWave7(t, sync, "syncRoomRecordToggle",
		"const enabled = inRoom && !guestMode && roomMediaRecordingEnabledForRoom(roomMediaRecordingRoomId())",
		"button.hidden = !(enabled || active)",
		"button.setAttribute('aria-pressed', active ? 'true' : 'false')",
		"button.classList.toggle('is-recording', active)",
	)
	enabled := functionBody(html, "function roomMediaRecordingEnabledForRoom(roomId)")
	requireAllWave7(t, enabled, "roomMediaRecordingEnabledForRoom", "typeof room.recordingEnabled === 'boolean'")
	// the manager switch in the lobby ⋮ (GET → PATCH; manageable:false is read-only copy)
	menu := functionBody(html, "function renderLobbyPopMenu(pop, room)")
	requireAllWave7(t, menu, "renderLobbyPopMenu", "lobbyPopItem('recording', () => renderLobbyPopRecording(pop, room))")
	setting := functionBody(html, "async function renderLobbyPopRecording(pop, room)")
	requireAllWave7(t, setting, "renderLobbyPopRecording",
		"w7RequestJSON('GET', `/assistant/meetings/recording/settings?roomId=${encodeURIComponent(room.id)}`)",
		"if (!setting.manageable) {", "only the room's manager can change it",
		"w7RequestJSON('PATCH', '/assistant/meetings/recording/settings', { roomId: room.id, recordingEnabled: input.checked })",
		"input.checked = !input.checked",
	)
	// the recorder: mixed stream (mic + every receiver + share/camera), webm
	// with a codec fallback, 5 MB chunks in order, `final` on stop
	requireAllWave7(t, html, "chunking", "var roomMediaRecordingChunkBytes = 5 * 1024 * 1024", "var roomMediaRecordingFlushMs = 20000", "var roomMediaRecordingUploadPath = '/assistant/meetings/recording/upload'")
	build := functionBody(html, "function roomMediaRecordingBuildStream()")
	requireAllWave7(t, build, "roomMediaRecordingBuildStream", "context.createMediaStreamDestination()", "context.createMediaStreamSource(new MediaStream([track]))", "roomMediaRecordingVideoTrack()")
	audio := functionBody(html, "function roomMediaRecordingAudioTracks()")
	requireAllWave7(t, audio, "roomMediaRecordingAudioTracks", "localStream?.getAudioTracks?.()", "pc?.getReceivers?.()", "screenShareStream?.getAudioTracks?.()")
	mime := functionBody(html, "function roomMediaRecordingPickMime(hasVideo)")
	requireAllWave7(t, mime, "roomMediaRecordingPickMime", "'video/webm;codecs=vp9,opus', 'video/webm;codecs=vp8,opus', 'video/webm'", "'audio/webm;codecs=opus', 'audio/webm'", "MediaRecorder.isTypeSupported(type)")
	start := functionBodyAfterSignature(html, "async function startRoomMediaRecording(options = {})")
	requireAllWave7(t, start, "startRoomMediaRecording",
		"if (!options.stream && !roomMediaRecordingEnabledForRoom(roomId))",
		"new MediaRecorder(built.stream, { mimeType })",
		"recorder.addEventListener('dataavailable', event => roomMediaRecordingOnData(state, event))",
		"recorder.addEventListener('stop', () => roomMediaRecordingOnStop(state))",
		// time-based flush: buffered media leaves every 20 s regardless of size
		"state.flushTimer = window.setInterval(() => {",
		"if (!state.stopped && !state.failed && state.buffer.length) roomMediaRecordingFlush(state, false)",
		"}, roomMediaRecordingFlushMs)",
	)
	// the first blob uploads at once (the first point the server can refuse),
	// then size or cadence decides
	data := functionBody(html, "function roomMediaRecordingOnData(state, event)")
	requireAllWave7(t, data, "roomMediaRecordingOnData", "if (!blob || !blob.size || state.failed) return", "if (!state.firstFlushed) {", "state.firstFlushed = true", "if (state.buffered >= roomMediaRecordingChunkBytes) roomMediaRecordingFlush(state, false)")
	flush := functionBody(html, "function roomMediaRecordingFlush(state, final)")
	requireAllWave7(t, flush, "roomMediaRecordingFlush", "if (final) state.finalSent = true", "state.chain = state.chain")
	if strings.Contains(flush, "state.partIndex++") {
		t.Error("the part index must be assigned when the upload runs (so a 409 resync reaches queued parts), not when it is queued")
	}
	upload := functionBody(html, "async function roomMediaRecordingUpload(state, chunk, final, durationSeconds, resynced = false)")
	requireAllWave7(t, upload, "roomMediaRecordingUpload",
		"const partIndex = state.partIndex++",
		"form.append('meetingId', state.meetingId)", "form.append('partIndex', String(partIndex))", "form.append('final', final ? '1' : '0')",
		"form.append('mime', state.mime)", "if (final) form.append('durationSeconds', String(durationSeconds))",
		"form.append('chunk', chunk, `part-${partIndex}.webm`)",
		"fetch(roomMediaRecordingUploadPath, { method: 'POST', body: form })",
		// 409: the server's partCount is authoritative — resync + resend once; no partCount → stop
		"if (response.status === 409 && !resynced && Number.isInteger(payload?.partCount) && payload.partCount >= 0) {",
		"state.partIndex = Number(payload.partCount)",
		"return roomMediaRecordingUpload(state, chunk, final, durationSeconds, true)",
		"roomMediaRecordingErrorCopy(response.status, payload)",
	)
	// stop → final flush; leave → stop first; failure never blocks human media
	stop := functionBody(html, "function roomMediaRecordingOnStop(state)")
	requireAllWave7(t, stop, "roomMediaRecordingOnStop", "if (!state.failed) roomMediaRecordingFlush(state, true)")
	leave := functionBody(html, "function leaveRoom()")
	requireAllWave7(t, leave, "leaveRoom", "stopRoomMediaRecording('left the room')")
	if strings.Index(leave, "stopRoomMediaRecording('left the room')") > strings.Index(leave, "peer.close()") {
		t.Error("the recording must stop before the peer connection closes")
	}
	// consent branch: the server's 403 copy stops the recorder and, for
	// guests, opens the consent panel where the recording lane lives
	copyBody := functionBody(html, "function roomMediaRecordingErrorCopy(status, payload)")
	requireAllWave7(t, copyBody, "roomMediaRecordingErrorCopy", "if (status === 403 && /consent/i.test(server)) return server", "status === 413", "if (status === 409) return 'the room refused a segment'")
	consent := functionBody(html, "function roomMediaRecordingConsentPrompt(copy)")
	requireAllWave7(t, consent, "roomMediaRecordingConsentPrompt", "if (guestMode) {", "setConsentPanelOpen(true)", "loadConsentStatus()")
	requireAllWave7(t, html, "consent lane", "['recording', 'Recording', 'Lets a host store this sitting’s audio and video on the Meeting Record. Off unless you allow it.'],")
	fail := functionBody(html, "function roomMediaRecordingFail(state, copy)")
	requireAllWave7(t, fail, "roomMediaRecordingFail", "state.buffer = []", "state.buffered = 0", "Your call continues.", "roomMediaRecordingConsentPrompt(state.failed)")
	stopFn := functionBodyAfterSignature(html, "function stopRoomMediaRecording(reason, options = {})")
	requireAllWave7(t, stopFn, "stopRoomMediaRecording", "window.clearInterval(state.flushTimer)")
}

func TestIndexRoomsWave7RecordPlaybackAndRecapCard(t *testing.T) {
	html := readIndexHTMLForWave7(t)
	body := functionBody(html, "function renderMemoryMeetingBody(meeting)")
	requireAllWave7(t, body, "renderMemoryMeetingBody", "meetingRecordRecordingNode(meetingId, detail)", "meetingRecordRecapBlockNode(meetingId, detail)")
	recording := functionBody(html, "function meetingRecordRecordingNode(meetingId, detail)")
	requireAllWave7(t, recording, "meetingRecordRecordingNode",
		"if (!recording?.playbackPath) return document.createDocumentFragment()",
		"document.createElement(mime.startsWith('audio/') ? 'audio' : 'video')",
		"media.controls = true", "media.src = String(recording.playbackPath)",
		"roomMediaRecordingClock(", "roomMediaRecordingSizeLabel(Number(recording.size) || 0)",
	)
	requireAllWave7(t, html, "recording CSS", ".meeting-record__media {", "audio.meeting-record__media {")
	// AJ 2026-09-02: compact recap card — the top THREE decisions, a mono
	// overflow footer (+N decisions · M action items · K open), one primary
	// Open record button; no action-item list (the record carries the lists)
	card := functionBodyAfterSignature(html, "function meetingRecapCardNode(spec = {})")
	requireAllWave7(t, card, "meetingRecapCardNode",
		"const decisions = allDecisions.slice(0, 3)", "listSection('Decisions', decisions)", "meeting-recap-card__more",
		"moreDecisions: allDecisions.length - decisions.length", "String(spec.footer || '').trim() || meetingRecapCardFooter({",
		"'Open record'", "openMeetingRecordDeepLink(spec.meetingId)", "'Open in conversation'",
	)
	for _, gone := range []string{".slice(0, 5)", "listSection('Action items'", "meeting-recap-card__owner"} {
		if strings.Contains(card, gone) {
			t.Errorf("compact recap card (AJ 2026-09-02) must not contain %q", gone)
		}
	}
	footer := functionBodyAfterSignature(html, "function meetingRecapCardFooter(counts = {})")
	requireAllWave7(t, footer, "meetingRecapCardFooter", "parts.push(`+${plural(more, 'decision', 'decisions')}`)", "plural(actions, 'action item', 'action items')", "parts.push(`${open} open`)", "return parts.join(' · ')")
	requireAllWave7(t, html, "recap CSS", ".meeting-recap-card {", ".meeting-recap-card__open {", ".meeting-recap-card__more {\n        font: var(--type-label);")
	// the channel message meeting-recap-card-<id> renders as the card, in both
	// the desktop thread and room chat; the text parser reads the server shape
	requireAllWave7(t, html, "recap id", "if (!id.startsWith('meeting-recap-card-')) return ''")
	timeline := functionBody(html, "function scoutChatMessageRecordNode(message)")
	requireAllWave7(t, timeline, "scoutChatMessageRecordNode", "if (meetingRecapCardMessageId(message)) {", "return meetingRecapCardMessageNode(message)")
	room := functionBody(html, "function roomChatMessageNode(message)")
	requireAllWave7(t, room, "roomChatMessageNode", "if (meetingRecapCardMessageId(message)) return meetingRecapCardMessageNode(message)")
	parse := functionBody(html, "function parseMeetingRecapCardText(text)")
	requireAllWave7(t, parse, "parseMeetingRecapCardText", "/^Meeting Record:/i", "/[?&]record=([^&\\s]+)/", "/^Decisions$/i", "/^Action items$/i", "body.lastIndexOf(' — ')",
		// the compact footer line parses as its own field (legacy Action items blocks still parse)
		"out.footer = line")
	block := functionBody(html, "function meetingRecordRecapBlockNode(meetingId, detail)")
	requireAllWave7(t, block, "meetingRecordRecapBlockNode", "detail.decisions", "detail.commitments", "recapCardThreadId", "inRecord: true", "openCount: Number(detail?.unresolvedCount) || 0")
	// the Project reference chips and the recap card's channel link share one
	// opener that exists (it used to be a dangling reference): Conversations
	// forward, select + hydrate, honest toast when the thread is gone
	if !strings.Contains(html, "chip.addEventListener('click', () => openScoutChatThread(openId))") {
		t.Error("Meeting Record Project chips must open through openScoutChatThread")
	}
	opener := functionBody(html, "async function openScoutChatThread(threadId)")
	if opener == "" {
		t.Fatal("openScoutChatThread is missing")
	}
	requireAllWave7(t, opener, "openScoutChatThread",
		"scoutChatThreads.find(thread => String(thread?.id || '') === threadId)",
		"that conversation is no longer available",
		"if (!selectPD1Destination('Conversations', { mode: 'chat' })) setActiveTool('chat')",
		"selectScoutChatThread(target.id)", "await hydrateScoutChatThread(target.id)", "setMobileChatView('convo')",
	)
	requireAllWave7(t, card, "recap card thread link", "openScoutChatThread(spec.threadId)")
}

// Production bug (AJ, 2026-09-01): a click on the schedule form's title or
// datetime field opened the room dropdown instead of taking focus. The
// designed-dropdown enhancer resolved the whole <form> as the select's host
// (`:has(> select)`) and preventDefault()ed the mousedown. The fix is in
// resolveStrideSelect: a press on a sibling control never resolves, and in a
// composite host the select opens from its own box only. This pin holds the
// form-level seam and the time-zone hint beside the datetime input.
func TestIndexRoomsWave7ScheduleFormFieldsTakeFocus(t *testing.T) {
	html := readIndexHTMLForWave7(t)
	resolve := functionBody(html, "function resolveStrideSelect(target, press)")
	if resolve == "" {
		t.Fatal("could not extract resolveStrideSelect")
	}
	requireAllWave7(t, resolve, "resolveStrideSelect",
		"const control = target.closest(strideSelectSiblingControls)",
		"if (control && control !== host && host.contains(control)) return null",
		"const composite = selects.length > 1 || host.querySelector(strideSelectSiblingControls) !== null",
		"if (composite && !strideSelectPressInside(qualified, press)) continue",
	)
	requireAllWave7(t, html, "sibling controls",
		"const strideSelectSiblingControls = 'input, textarea, button, a[href],",
		"function strideSelectPressInside(select, press)",
		"const select = resolveStrideSelect(event.target, event)",
	)
	// no form-level focus steal: the schedule form wires the open toggle,
	// cancel, submit, chips and Escape — never a click/mousedown/pointerdown
	// on the form itself, and never a select focus or showPicker()
	wire := functionBody(html, "function wireLobbySchedule()")
	if wire == "" {
		t.Fatal("could not extract wireLobbySchedule")
	}
	for _, forbidden := range []string{"els.form.addEventListener('click'", "els.form.addEventListener('mousedown'", "els.form.addEventListener('pointerdown'", "els.room.focus(", "showPicker("} {
		if strings.Contains(wire, forbidden) {
			t.Errorf("wireLobbySchedule must not steal focus at the form level: found %q", forbidden)
		}
	}
	start := functionBody(html, "function startLobbySchedule(meeting = null)")
	if strings.Contains(start, "els.room?.focus(") || strings.Contains(start, "els.room.focus(") {
		t.Error("startLobbySchedule must focus the title, never the room select")
	}
	requireAllWave7(t, start, "startLobbySchedule", "els.title?.focus()", "syncLobbyScheduleZone()")
	// the time-zone hint: mono, beside the datetime input, short name + UTC
	// offset from Intl; the row's aria-label names the zone; the wire value
	// still posts UTC through the one local→RFC 3339 conversion
	requireAllWave7(t, html, "zone hint markup",
		`<div id="lobbyScheduleStartRow" class="lobby__start" role="group" aria-label="Starts at (local time)">`,
		`<span id="lobbyScheduleZone" class="lobby__zone"`,
		".lobby__zone {", "font: 500 11px var(--font-mono);",
	)
	zone := functionBody(html, "function lobbyScheduleZoneLabel(at = Date.now())")
	requireAllWave7(t, zone, "lobbyScheduleZoneLabel",
		"new Intl.DateTimeFormat(undefined, { timeZoneName: 'short' }).formatToParts(date)",
		"part.type === 'timeZoneName'",
		"-date.getTimezoneOffset()",
		"return `${short} · ${utc}`",
	)
	sync := functionBody(html, "function syncLobbyScheduleZone()")
	requireAllWave7(t, sync, "syncLobbyScheduleZone",
		"row?.setAttribute('aria-label', `Starts at (${label})`)",
		"els.start?.setAttribute('aria-label', `Starts at (${label})`)",
	)
	requireAllWave7(t, wire, "zone follows the picked date", "els.start?.addEventListener('input', syncLobbyScheduleZone)")
	submit := functionBody(html, "async function submitLobbySchedule(event)")
	requireAllWave7(t, submit, "still posts UTC", "startsAt: new Date(startMs).toISOString()")
}
