package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// jsFunction rebuilds a whole client function (signature + body) so a node
// probe can call it directly. functionBody returns only the braces.
func jsFunction(source string, signature string) string {
	body := functionBody(source, signature)
	if body == "" {
		return ""
	}
	return signature + " " + body
}

// runClientProbe executes the assembled client source under node with a fixed
// timezone, so a clock-formatting assertion means the same thing everywhere.
func runClientProbe(t *testing.T, script string) {
	t.Helper()
	cmd := exec.Command("node", "--input-type=module", "-e", script)
	cmd.Env = append(os.Environ(), "TZ=UTC")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("client probe failed: %v\n%s", err, output)
	}
}

func readIndexHTML(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// On 2026-09-02 a green "Live transcription" pill sat over a 34-minute hole in
// which the server made ZERO transcription calls. Every term the pill consulted
// proved only that the lane was allowed and its socket was open. The pill must
// have a state for "audio is not landing", and it must only enter it on an
// explicit server signal — an older server says nothing and must not be read as
// either healthy or stalled. Green likewise requires explicit captured progress.
func TestRoomTranscriptionPillHasAStalledStateGatedOnExplicitCaptureTruth(t *testing.T) {
	html := readIndexHTML(t)
	presentation := functionBody(html, "function roomTranscriptionPresentation()")
	if presentation == "" {
		t.Fatal("roomTranscriptionPresentation is missing")
	}
	for _, invariant := range []string{
		"roomRecordingCapturing === false",
		"roomRecordingCapturing !== true",
		"state: 'stalled'",
		"state: 'pending'",
		"Confirming transcription…",
		"Transcription stalled — reconnecting",
	} {
		if !strings.Contains(presentation, invariant) {
			t.Fatalf("transcription pill cannot report a stalled capture: missing %q", invariant)
		}
	}
	// Ordering is the whole point: the stall check must be reached BEFORE the
	// function can conclude "live", or the pill stays green over the hole.
	stalledAt := strings.Index(presentation, "roomRecordingCapturing === false")
	liveAt := strings.Index(presentation, "state: 'live'")
	if liveAt < 0 || stalledAt > liveAt {
		t.Fatal("the stalled check must be evaluated before the pill may conclude 'live'")
	}
	// Unknown (an older server, a frame that predates the watchdog, or the reset
	// side of a reconnect) is a strict boolean read, never a truthiness test that
	// would trip on absence or manufacture green.
	if !strings.Contains(functionBody(html, "function applyRoomRecordingState(recording, roomId = '')"),
		"typeof recording.capturing === 'boolean' ? recording.capturing : null") {
		t.Fatal("an absent capturing field must degrade to null, not to false")
	}
	for _, marker := range []string{
		`.room-transcription-pill[data-state="stalled"] {`,
		`.room-transcription-pill[data-state="stalled"] .room-transcription-pill__dot { animation: none; }`,
		`stalled: 'Stalled'`,
	} {
		if !strings.Contains(html, marker) {
			t.Fatalf("stalled pill presentation missing %q", marker)
		}
	}
	// The amber ground is the signal; assert it is amber and not the live green.
	stalledRuleAt := strings.Index(html, `.room-transcription-pill[data-state="stalled"] {`)
	stalledRule := html[stalledRuleAt:]
	if end := strings.Index(stalledRule, "}"); end > 0 {
		stalledRule = stalledRule[:end]
	}
	if !strings.Contains(stalledRule, "var(--warn-soft)") || !strings.Contains(stalledRule, "var(--warn-text)") {
		t.Fatalf("a stalled capture must read amber, got rule %q", stalledRule)
	}
	if strings.Contains(stalledRule, "--ember") || strings.Contains(stalledRule, "#FF5A19") {
		t.Fatalf("a capture warning must use the semantic amber, not earned ember: %q", stalledRule)
	}
	if !strings.Contains(html, "animation: bf-breathe var(--dur-breathe) var(--ease) infinite") {
		t.Fatal("the stalled retry signal must use the design system motion tokens")
	}
	// A stalled room still has recording enabled; taking the pause control away
	// in the one state a participant most wants to intervene would be perverse.
	toolbar := functionBody(html, "function roomMeetingTranscriptToolbar()")
	if !strings.Contains(toolbar, "['live', 'paused', 'stalled'].includes(transcription.state)") {
		t.Fatal("the transcript toolbar must keep the pause control reachable while capture is stalled")
	}
}

// The recap header was the ONLY surface that moved during the blackout, and it
// moved in the reassuring direction: no snapshot plus a "live" pill returned
// "Notes are getting ready" for 34 minutes. A stalled capture must say so, and
// name the last line the record actually holds.
func TestMeetingRecapHeaderTellsTheTruthWhileCaptureIsStalled(t *testing.T) {
	html := readIndexHTML(t)
	label := jsFunction(html, "function meetingIntelligenceStatusLabel(snapshot)")
	coverage := jsFunction(html, "function meetingIntelligenceCoverageLabel(snapshot)")
	timeLabel := jsFunction(html, "function meetingIntelligenceTimeLabel(value)")
	since := jsFunction(html, "function roomTranscriptCaptureSince()")
	if label == "" || coverage == "" || timeLabel == "" || since == "" {
		t.Fatal("recap header source unavailable")
	}
	script := timeLabel + "\n" + since + "\n" + label + "\n" + coverage + `
let pillState = { state: 'stalled', label: 'Transcription stalled — reconnecting' }
let roomRecordingLastSegmentAt = '2026-09-02T20:53:15.026Z'
let roomRecordingStalledSince = '2026-09-02T20:54:00.000Z'
let roomTranscriptStallOpenSince = ''
let stallNotices = []
function roomTranscriptionPresentation() { return pillState }
function roomMeetingTranscriptEntries() { return [] }
function roomTranscriptStallNoticesForMeeting() { return stallNotices }

const stalledNoSnapshot = meetingIntelligenceStatusLabel(null)
if (stalledNoSnapshot === 'Notes are getting ready') {
  throw new Error('a stalled capture still claims notes are getting ready')
}
if (stalledNoSnapshot !== 'No new transcript since 8:53 PM') {
  throw new Error('stalled header did not name the last captured moment: ' + stalledNoSnapshot)
}

// A snapshot in hand must not talk the header back into reassurance.
const snapshot = { notes: { state: 'current' } }
if (meetingIntelligenceStatusLabel(snapshot) !== 'No new transcript since 8:53 PM') {
  throw new Error('a stalled capture must outrank a stale "Notes current"')
}

// Without any timestamp at all it still refuses to reassure.
roomRecordingLastSegmentAt = ''
roomRecordingStalledSince = ''
if (meetingIntelligenceStatusLabel(null) !== 'No new transcript — capture stalled') {
  throw new Error('an unanchored stall must still read as a stall')
}

// Nothing about the healthy paths changed.
pillState = { state: 'live', label: 'Live transcription' }
if (meetingIntelligenceStatusLabel(null) !== 'Notes are getting ready') {
  throw new Error('live-with-no-snapshot copy regressed')
}
if (meetingIntelligenceStatusLabel(snapshot) !== 'Notes current') {
  throw new Error('live-with-snapshot copy regressed')
}

// A client-observed gap is stronger evidence than a stale full snapshot.
stallNotices = [{ text: '34 minutes were not captured' }]
const coverageAfterGap = meetingIntelligenceCoverageLabel({ notes: { coverage: 'full', groundedThrough: '' }, scout: {} })
if (!coverageAfterGap.startsWith('Partial coverage · transcript gaps')) {
  throw new Error('a stale full-coverage snapshot outranked the client-observed gap: ' + coverageAfterGap)
}
`
	runClientProbe(t, script)
	if !strings.Contains(functionBody(html, "function renderMeetingRecap()"),
		"Any recap will be missing this stretch.") {
		t.Fatal("the recap card must warn that a stalled stretch is absent from whatever recap lands")
	}
	if !strings.Contains(functionBody(html, "function renderMeetingRecap()"),
		"any recap will be missing this stretch.") {
		t.Fatal("the stalled no-snapshot body must not fall through to the join-the-meeting copy")
	}
	if !strings.Contains(functionBody(html, "function renderMeetingRecap()"),
		"Any recap excludes that uncaptured window.") {
		t.Fatal("the recap must retain the sized recovery window after capture resumes")
	}
}

// Participants must be told the record has a hole, because the recap is
// synthesized from the partial transcript and will otherwise read as complete.
func TestTranscriptRecoveryLineNamesTheUncapturedWindow(t *testing.T) {
	html := readIndexHTML(t)
	parts := []string{
		jsFunction(html, "function roomTranscriptGapLabel(gapMs)"),
		jsFunction(html, "function roomTranscriptWindowLabel(startMs, endMs)"),
		jsFunction(html, "function meetingIntelligenceTimeLabel(value)"),
		jsFunction(html, "function noteRoomTranscriptCaptureRecovered(startedAt)"),
	}
	for index, part := range parts {
		if part == "" {
			t.Fatalf("recovery-notice source %d unavailable", index)
		}
	}
	script := strings.Join(parts, "\n") + `
let roomTranscriptStallNotices = []
let roomRecordingLastSegmentAt = '2026-09-02T21:27:34.007Z'
const activeJoin = { roomId: 'office' }
const toasts = []
function roomTranscriptMeetingId() { return 'meeting-20260902-163957-989042240' }
function showToast(payload) { toasts.push(payload) }

noteRoomTranscriptCaptureRecovered('2026-09-02T20:53:15.026Z')
if (roomTranscriptStallNotices.length !== 1) throw new Error('no recovery notice was recorded')
const notice = roomTranscriptStallNotices[0]
if (notice.text !== 'Transcription recovered — 34 minutes were not captured (8:53–9:27 PM).') {
  throw new Error('recovery line did not name the window: ' + notice.text)
}
if (notice.roomId !== 'office' || !notice.meetingId) throw new Error('recovery notice lost its room/meeting scope')
if (toasts.length !== 1 || !toasts[0].text.includes('34 minutes were not captured')) {
  throw new Error('recovery must also reach a participant who is not on the transcript tab')
}

// Replaying the same recovery must not stack duplicate holes.
noteRoomTranscriptCaptureRecovered('2026-09-02T20:53:15.026Z')
if (roomTranscriptStallNotices.length !== 1) throw new Error('recovery notice duplicated on replay')

// Finding 3: the amount carries its own verb, and a short hole is reported in
// seconds instead of being rounded up into a whole minute it never filled.
for (const [ms, label, verb] of [
  [1000, '1 second', 'was'],
  [31000, '31 seconds', 'were'],
  [61000, '1 minute', 'was'],
  [119000, '1 minute', 'was'],
  [125000, '2 minutes', 'were']
]) {
  const gap = roomTranscriptGapLabel(ms)
  if (gap.label !== label || gap.verb !== verb) {
    throw new Error('gap label for ' + ms + 'ms: ' + JSON.stringify(gap))
  }
}

// End to end through the sentence, which is where the disagreement showed.
roomTranscriptStallNotices = []
roomRecordingLastSegmentAt = '2026-09-02T21:28:37.000Z'
noteRoomTranscriptCaptureRecovered('2026-09-02T21:27:35.000Z')
const singular = roomTranscriptStallNotices[roomTranscriptStallNotices.length - 1].text
if (!singular.includes('1 minute was not captured')) {
  throw new Error('a one-minute hole must not read as a plural: ' + singular)
}
roomRecordingLastSegmentAt = '2026-09-02T21:30:06.000Z'
noteRoomTranscriptCaptureRecovered('2026-09-02T21:29:35.000Z')
const subMinute = roomTranscriptStallNotices[roomTranscriptStallNotices.length - 1].text
if (!subMinute.includes('31 seconds were not captured')) {
  throw new Error('a 31-second hole must be reported in seconds: ' + subMinute)
}

// An unmeasurable window still admits the hole rather than inventing a number.
roomRecordingLastSegmentAt = ''
noteRoomTranscriptCaptureRecovered('')
if (!roomTranscriptStallNotices.some(row => row.text === 'Transcription recovered — part of this stretch was not captured.')) {
  throw new Error('an unmeasurable gap must still be admitted')
}
`
	runClientProbe(t, script)
	// The hole renders inside the record, at the seam it describes.
	render := functionBody(html, "function renderMeetingTranscript()")
	for _, invariant := range []string{"roomTranscriptStallNoticesForMeeting()", "flushNoticesUpTo"} {
		if !strings.Contains(render, invariant) {
			t.Fatalf("transcript render does not place capture gaps in the record: missing %q", invariant)
		}
	}
	if !strings.Contains(html, ".room-meeting-transcript__notice {") {
		t.Fatal("the capture-gap row has no styling of its own and would pass for something somebody said")
	}
	// Recovery is driven by an observed capture edge, not by a timer — and it is
	// judged against the RETAINED stall rather than the previous frame, because a
	// reconnect in between turns false->true into null->true (finding 1).
	apply := functionBody(html, "function applyRoomRecordingState(recording, roomId = '')")
	for _, invariant := range []string{
		"roomRecordingCapturing === false && !roomTranscriptStallOpenSince",
		"roomRecordingCapturing === true && roomTranscriptStallOpenSince",
	} {
		if !strings.Contains(apply, invariant) {
			t.Fatalf("recovery must be judged against the retained stall: missing %q", invariant)
		}
	}
	if strings.Contains(apply, "previousCapturing === false && roomRecordingCapturing === true") {
		t.Fatal("recovery read off the previous frame alone loses every stall that ends across a reconnect")
	}
}

// End to end over the wiring, not the strings: a server frame carrying
// capturing:false must turn the pill amber, and the recovering frame must both
// return it to green and leave a sized hole behind in the record.
func TestCaptureStallFrameFlipsThePillAndRecoveryLeavesTheHoleBehind(t *testing.T) {
	html := readIndexHTML(t)
	parts := []string{
		jsFunction(html, "function roomRecordingAuthorityOrder(recording)"),
		jsFunction(html, "function roomRecordingAuthorityIsCurrent(recording, roomId)"),
		jsFunction(html, "function applyRoomRecordingState(recording, roomId = '')"),
		jsFunction(html, "function roomTranscriptionPresentation()"),
		jsFunction(html, "function roomTranscriptGapLabel(gapMs)"),
		jsFunction(html, "function roomTranscriptWindowLabel(startMs, endMs)"),
		jsFunction(html, "function meetingIntelligenceTimeLabel(value)"),
		jsFunction(html, "function noteRoomTranscriptCaptureRecovered(startedAt)"),
	}
	for index, part := range parts {
		if part == "" {
			t.Fatalf("capture-stall wiring source %d unavailable", index)
		}
	}
	script := strings.Join(parts, "\n") + `
let roomRecordingEnabled = true
let roomRecordingAvailable = true
let roomRecordingConnected = true
let roomRecordingRevision = 0
let roomRecordingStatusRevision = 0
let roomRecordingRoomId = 'office'
let roomRecordingKnown = true
let roomRecordingSocketConfirmed = true
let roomRecordingPendingDesired = null
let roomRecordingPendingRevision = 0
let roomRecordingPendingTimer = 0
let roomRecordingUpdatedAt = ''
let roomRecordingUpdatedBy = ''
let roomRecordingCapturing = null
let roomRecordingStalledSince = ''
let roomRecordingLastSegmentAt = ''
let roomRecordingStallReason = ''
let roomTranscriptStallOpenSince = ''
let roomTranscriptStallOpenLastSegment = ''
let roomTranscriptStallOpenRoomId = ''
let roomTranscriptStallNotices = []
const activeJoin = { roomId: 'office' }
const appShell = { classList: { contains: () => true } }
const ws = { readyState: 1 }
const pc = { connectionState: 'connected' }
const WebSocket = { OPEN: 1 }
const window = { clearTimeout() {} }
const toasts = []
function roomMediaActive() { return true }
function updateRoomRecordingControls() {}
let listeningStatusApplied = 0
let recapRepaints = 0
function applyRoomListeningStatus() { listeningStatusApplied += 1 }
function renderMeetingRecap() { recapRepaints += 1 }
function showToast(payload) { toasts.push(payload) }
function roomTranscriptMeetingId() { return 'meeting-a' }

const frame = (statusRevision, extra) => Object.assign({
  enabled: true, available: true, connected: true, revision: 1, statusRevision
}, extra)

// A server that says nothing about capture must not manufacture green.
applyRoomRecordingState(frame(1), 'office')
if (roomTranscriptionPresentation().state !== 'pending') throw new Error('an unaware server must stay neutral')
if (roomTranscriptionPresentation().label !== 'Confirming transcription…') throw new Error('unknown capture label is not honest')
if (roomRecordingCapturing !== null) throw new Error('absent capture truth must degrade to unknown')
if (recapRepaints !== 0) throw new Error('an unaware server must not churn the recap')

// Capture is landing.
applyRoomRecordingState(frame(2, { capturing: true, lastSegmentAt: '2026-09-02T20:53:15.026Z' }), 'office')
if (roomTranscriptionPresentation().state !== 'live') throw new Error('a healthy capture must read live')

// Equal authority is replay-only. It cannot reverse a capture edge and turn a
// stalled pill green without a newer server status revision.
applyRoomRecordingState(frame(3, { capturing: false, stalledSince: '2026-09-02T20:54:00.000Z', lastSegmentAt: '2026-09-02T20:53:15.026Z' }), 'office')
if (roomTranscriptionPresentation().state !== 'stalled') throw new Error('the precondition stall did not land')
if (applyRoomRecordingState(frame(3, { capturing: true, lastSegmentAt: '2026-09-02T21:27:34.007Z' }), 'office') !== false) {
  throw new Error('a contradictory same-authority replay was accepted')
}
if (roomTranscriptionPresentation().state !== 'stalled') throw new Error('a stale replay manufactured green')

// The watchdog trips.
applyRoomRecordingState(frame(4, {
  capturing: false,
  stalledSince: '2026-09-02T20:54:00.000Z',
  lastSegmentAt: '2026-09-02T20:53:15.026Z',
  stallReason: 'fence_refresh_failed'
}), 'office')
const stalled = roomTranscriptionPresentation()
if (stalled.state !== 'stalled') throw new Error('a stalled capture still reads: ' + stalled.state)
if (stalled.label !== 'Transcription stalled — reconnecting') throw new Error('stalled label: ' + stalled.label)
if (roomTranscriptStallNotices.length !== 0) throw new Error('a hole is reported on recovery, not on the trip')
const repaintsAtTrip = recapRepaints
if (repaintsAtTrip === 0) throw new Error('the stall never reached the recap header')

// Capture comes back.
applyRoomRecordingState(frame(5, { capturing: true, lastSegmentAt: '2026-09-02T21:27:34.007Z' }), 'office')
if (roomTranscriptionPresentation().state !== 'live') throw new Error('recovery did not return the pill to live')
if (roomTranscriptStallNotices.length !== 1) throw new Error('recovery left no record of the hole')
if (roomTranscriptStallNotices[0].text !== 'Transcription recovered — 34 minutes were not captured (8:53–9:27 PM).') {
  throw new Error('recovery line: ' + roomTranscriptStallNotices[0].text)
}
if (toasts.length !== 1) throw new Error('recovery must reach a participant who is not on the transcript tab')

// A pause is still a pause, not a stall.
applyRoomRecordingState(frame(6, { enabled: false, capturing: false }), 'office')
if (roomTranscriptionPresentation().state !== 'paused') throw new Error('a paused room must not be reported as stalled')
if (listeningStatusApplied < 5) throw new Error('every capture edge must repaint the room status surfaces')
if (recapRepaints <= repaintsAtTrip) throw new Error('recovery never reached the recap header')
`
	runClientProbe(t, script)
}

// Fix 4(a) regression pin, narrowed by review (findings 2 + 4).
// `meetingSnapshot` returns nil whenever it finds no ACTIVE record for the room
// it was asked about — including the office-shell grant, which asks about
// `office` while this socket's principal may be seated elsewhere. Clearing on
// THAT frame wiped the intelligence snapshot and the transcript history, after
// which every live line was silently dropped for the rest of the tab and the
// recap header read "Notes are getting ready".
//
// But a null is only uninformative when the office shell sent it. A ROOM
// socket's null was asked about this very seat: holding it left a finished
// meeting reading as active with the shared clock anchored to it. So the frame
// carries its source, and meetingRecordRoomId — which used to be written and
// never read — decides whether a held record is even this seat's to keep.
func TestNullMeetingFrameIsNoInformationNotNoMeeting(t *testing.T) {
	html := readIndexHTML(t)
	handle := jsFunction(html, "function handleMeetingRecord(payload)")
	if handle == "" {
		t.Fatal("handleMeetingRecord source unavailable")
	}
	script := handle + `
let meetingRecord = null
let meetingRecordRoomId = ''
let currentMeetingIntelligence = null
let roomMeetingTranscriptHistory = []
let roomTranscriptStallNotices = []
let roomTranscriptStallOpenSince = ''
let roomTranscriptStallOpenLastSegment = ''
let roomTranscriptStallOpenRoomId = ''
let roomMeetingTranscriptAdoptedMeetingId = ''
let meetingClockOffsetMs = 0
// handleKanbanFrame tags every frame with the socket it arrived on; the office
// shell is the source whose null is uninformative.
let kanbanFrameSource = 'office'
let sharedMeetingStartMs = 0
let roomStartedAt = 0
const activeJoin = { roomId: 'office' }
const appShell = { dataset: { tool: 'room' } }
function loadMeetingsForMemory() {}
function updateRoomClock() {}
function syncMeetingIdentityLabel() {}
function renderMeetingRecap() {}
function renderMeetingTranscript() {}

const live = { id: 'meeting-a', roomId: 'office', active: true, startedAt: '2026-09-02T20:00:46.000Z' }
handleMeetingRecord(live)
if (meetingRecord?.id !== 'meeting-a') throw new Error('a real record did not seed')
currentMeetingIntelligence = { meetingId: 'meeting-a' }
roomMeetingTranscriptHistory = [{ id: 'transcript-1' }]

// The frame at the heart of the bug.
handleMeetingRecord(null)
if (meetingRecord?.id !== 'meeting-a') throw new Error('a null meeting frame cleared the held record')
if (currentMeetingIntelligence === null) throw new Error('a null meeting frame cleared the intelligence snapshot')
if (roomMeetingTranscriptHistory.length !== 1) throw new Error('a null meeting frame cleared the transcript history')

// So does the id-less variant the office shell can send.
handleMeetingRecord({ roomId: 'office' })
if (meetingRecord?.id !== 'meeting-a' || roomMeetingTranscriptHistory.length !== 1) {
  throw new Error('an id-less meeting frame cleared derived state')
}

// A genuine change of sitting still clears everything derived.
handleMeetingRecord({ id: 'meeting-b', roomId: 'office', active: true, startedAt: '2026-09-02T22:10:00.000Z' })
if (meetingRecord?.id !== 'meeting-b') throw new Error('a new sitting did not take')
if (currentMeetingIntelligence !== null || roomMeetingTranscriptHistory.length !== 0) {
  throw new Error('a genuine meeting-id change must clear derived state')
}

// A closed record (how an ending ACTUALLY arrives) is still accepted.
handleMeetingRecord({ id: 'meeting-b', roomId: 'office', active: false, startedAt: '2026-09-02T22:10:00.000Z', endedAt: '2026-09-02T22:40:00.000Z' })
if (meetingRecord?.active !== false || sharedMeetingStartMs !== 0) {
  throw new Error('a closed record must still land and release the shared clock')
}

// And an empty client is not broken by a null frame either.
meetingRecord = null
handleMeetingRecord(null)
if (meetingRecord !== null) throw new Error('a null frame on an empty client must be a no-op')

// Finding 2: a ROOM socket's null is authoritative for this seat. Holding it
// would keep an ended meeting reading as live, clock and all.
kanbanFrameSource = 'office'
handleMeetingRecord({ id: 'meeting-c', roomId: 'office', active: true, startedAt: '2026-09-03T01:00:00.000Z' })
if (sharedMeetingStartMs === 0) throw new Error('a live record did not anchor the shared clock')
currentMeetingIntelligence = { meetingId: 'meeting-c' }
roomMeetingTranscriptHistory = [{ id: 'transcript-9' }]
roomTranscriptStallNotices = [{ id: 'capture-gap-1' }]
roomTranscriptStallOpenSince = '2026-09-03T01:05:00.000Z'
kanbanFrameSource = 'room'
handleMeetingRecord(null)
if (meetingRecord !== null) throw new Error("a room socket's null was held as a live meeting")
if (meetingRecord?.active) throw new Error('an ended meeting still reads as active')
if (sharedMeetingStartMs !== 0) throw new Error('an ended meeting kept the shared clock anchored')
if (currentMeetingIntelligence !== null || roomMeetingTranscriptHistory.length !== 0 || roomTranscriptStallNotices.length !== 0) {
  throw new Error('an authoritative null must clear the state derived from that sitting')
}
if (roomTranscriptStallOpenSince !== '') throw new Error('the sitting ended with an open stall still armed')

// Finding 4: meetingRecordRoomId is load-bearing, not decoration. A held
// record belonging to another room is not this seat's to vouch for, so even
// the office shell's null drops it.
kanbanFrameSource = 'office'
handleMeetingRecord({ id: 'meeting-d', roomId: 'studio', active: true, startedAt: '2026-09-03T02:00:00.000Z' })
if (meetingRecordRoomId !== 'studio') throw new Error('the held record did not remember its room: ' + meetingRecordRoomId)
handleMeetingRecord(null)
if (meetingRecord !== null) throw new Error('a record belonging to another room was held across a null')
`
	runClientProbe(t, script)
	// The leave seam is what clears now, so it has to exist and be wired.
	if functionBody(html, "function clearMeetingRecordForRoomLeave()") == "" {
		t.Fatal("nothing clears the held meeting record on room leave")
	}
	if !strings.Contains(html, "clearMeetingRecordForRoomLeave()\n        renderHomeLiveNow()") {
		t.Fatal("the room-leave path does not clear the held meeting record")
	}
	// The source is tagged where it is known — at the two sockets — instead of
	// being guessed inside the router.
	for _, wiring := range []string{
		"handleKanbanFrame(JSON.parse(message.data), 'office')",
		"handleKanbanFrame(JSON.parse(message.data), 'room')",
		"kanbanFrameSource = String(source || '')",
		"const source = String(kanbanFrameSource || '')",
	} {
		if !strings.Contains(html, wiring) {
			t.Fatalf("a meeting frame's source is not threaded from its socket: missing %q", wiring)
		}
	}
	// And the leave seam forgets the open stall with the rest of the sitting.
	if !strings.Contains(functionBody(html, "function clearMeetingRecordForRoomLeave()"), "roomTranscriptStallOpenSince = ''") {
		t.Fatal("leaving the room must not carry an open stall into the next sitting")
	}
}

// Fix 4(b). A live line that already cleared the server's room + sitting +
// generation fence must not be thrown away because the client's own header is
// uninformed — that is what turned one null frame into a dead transcript.
func TestLiveTranscriptLineSurvivesAnUninformedMeetingHeader(t *testing.T) {
	html := readIndexHTML(t)
	parts := []string{
		jsFunction(html, "function roomMeetingRecord(value)"),
		jsFunction(html, "function roomMeetingIdentifier(value)"),
		jsFunction(html, "function roomMeetingTimestamp(value)"),
		jsFunction(html, "function roomMeetingText(value, max = 500)"),
		jsFunction(html, "function parseRoomMeetingTranscriptEntry(payload)"),
		jsFunction(html, "function roomMeetingTranscriptOrder(left, right)"),
		jsFunction(html, "function roomTranscriptMeetingId()"),
		jsFunction(html, "function roomMeetingTranscriptEntries()"),
		jsFunction(html, "function appendRoomMeetingTranscriptEntry(payload)"),
	}
	for index, part := range parts {
		if part == "" {
			t.Fatalf("live transcript append source %d unavailable", index)
		}
	}
	script := strings.Join(parts, "\n") + `
let meetingRecord = null
let currentMeetingIntelligence = null
let roomMeetingTranscriptAdoptedMeetingId = ''
let roomMeetingTranscriptHistory = []
const activeJoin = { roomId: 'office' }
const captions = []
function markMeetingIntelligenceBehindTranscript() {}
function renderMeetingTranscript() {}
function pushRoomCaption(entry) { captions.push(entry) }

const line = seq => ({
  id: 'transcript-' + seq,
  kind: 'transcript',
  text: 'Tyler: can you hear me?',
  createdAt: '2026-09-02T21:27:34.847Z',
  metadata: { roomId: 'office', meetingId: 'meeting-a', captureSequence: String(seq) }
})

// The tab knows nothing about the meeting — the exact post-null-frame state.
appendRoomMeetingTranscriptEntry(line(296))
if (roomMeetingTranscriptHistory.length !== 1) throw new Error('a live line was dropped by an uninformed header')
if (roomMeetingTranscriptEntries().length !== 1) throw new Error('an adopted line does not render')
if (captions.length !== 1) throw new Error('an adopted line never reached the captions overlay')

// A line addressed to a DIFFERENT sitting is a genuine mismatch and stays out.
const other = line(297)
other.metadata.meetingId = 'meeting-b'
appendRoomMeetingTranscriptEntry(other)
if (roomMeetingTranscriptHistory.length !== 1) throw new Error('a foreign sitting was adopted')

// So is a line addressed to another room.
const elsewhere = line(298)
elsewhere.metadata.roomId = 'studio'
appendRoomMeetingTranscriptEntry(elsewhere)
if (roomMeetingTranscriptHistory.length !== 1) throw new Error('a foreign room was accepted')

// A line with no sitting at all is still refused — adoption needs a real id.
const anonymous = line(299)
delete anonymous.metadata.meetingId
appendRoomMeetingTranscriptEntry(anonymous)
if (roomMeetingTranscriptHistory.length !== 1) throw new Error('an unaddressed line was accepted')

// Once the record lands it is the authority again.
meetingRecord = { id: 'meeting-a' }
appendRoomMeetingTranscriptEntry(line(300))
if (roomMeetingTranscriptHistory.length !== 2) throw new Error('the record-backed path regressed')
`
	runClientProbe(t, script)
}

// Rendered: the amber is real, the recap card says the true thing, and the
// hole lands inside the record as a row a participant can actually read.
// Source pins prove the branches exist; only a render proves what is on screen.
func TestRenderedStalledCapturePillRecapAndGapRow(t *testing.T) {
	if testing.Short() {
		t.Skip("rendered browser contract")
	}
	indexPath, err := filepath.Abs("index.html")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const fs=require('fs');
const http=require('http');
const assert=require('assert/strict');
const {chromium}=require('playwright');
const html=fs.readFileSync(process.env.CAPTURE_HONESTY_INDEX,'utf8');
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@example.test',name:'AJ'}));}
  if(req.url.startsWith('/participants')){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({roomId:'office',participants:['AJ'],occupiedSeats:1,capacity:10,mediaStates:{},endpointCounts:{AJ:1},recording:{enabled:true,available:true,connected:true,revision:4,statusRevision:4,updatedAt:'2026-09-02T20:00:00Z'}}));}
  if(req.url==='/api/stride/v1/mobile/surfaces/organizations'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({availability:'available',surface:'organizations',revision:1,items:[]}));}
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
const rgb=value=>value.replace(/[^0-9.,]/g,'').split(',').map(Number);
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true});
 const page=await browser.newPage({viewport:{width:1280,height:900}});
 const errors=[];
 page.on('pageerror',error=>errors.push(String(error)));
 await page.goto('http://127.0.0.1:'+server.address().port+'/video',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed');
 await page.waitForFunction(()=>document.getElementById('appShell')?.dataset.tool==='room');
 await page.waitForLoadState('networkidle');
 await page.waitForTimeout(400);
 await page.evaluate(()=>{for(let timer=1;timer<50000;timer++){clearTimeout(timer);clearInterval(timer)}});
 await page.evaluate(()=>{
   const shell=document.getElementById('appShell');
   shell.dataset.tool='room';shell.classList.add('is-in-room','is-authed');
   activeJoin={roomId:'office',passcode:'',guest:false};guestMode=false;
   localStream={};pc={connectionState:'connected'};
   ws={readyState:WebSocket.OPEN,send:()=>{}};
   handleMeetingRecord({id:'meeting-a',roomId:'office',active:true,startedAt:'2026-09-02T20:00:46.000Z'});
   appendRoomMeetingTranscriptEntry({id:'transcript-295',kind:'transcript',text:"Tyler: I think it's",createdAt:'2026-09-02T20:53:15.026Z',metadata:{roomId:'office',meetingId:'meeting-a',captureSequence:'295'}});
   applyRoomRecordingState({enabled:true,available:true,connected:true,revision:8,statusRevision:8,capturing:true,lastSegmentAt:'2026-09-02T20:53:15.026Z',updatedAt:'2026-09-02T20:53:20.000Z'},'office');
   setRoomChatOpen(true);setRoomMeetingMode('transcript');
 });
 let live=await page.evaluate(()=>{const pill=document.getElementById('roomTranscriptPill');return {state:pill.dataset.state,label:pill.textContent.trim(),background:getComputedStyle(pill).backgroundColor}});
 assert.equal(live.state,'live',JSON.stringify(live));
 assert.equal(live.label,'Live transcription');

 // The watchdog trips. The pill must LEAVE green, in amber, and say why.
 await page.evaluate(()=>applyRoomRecordingState({enabled:true,available:true,connected:true,revision:8,statusRevision:9,capturing:false,stalledSince:'2026-09-02T20:54:00.000Z',lastSegmentAt:'2026-09-02T20:53:15.026Z',stallReason:'fence_refresh_failed',updatedAt:'2026-09-02T20:54:00.000Z'},'office'));
 // the pill cross-fades its ground; a computed colour read before the
 // transition settles reports the colour we just left, not the one on screen
 await page.waitForTimeout(600);
 const stalled=await page.evaluate(()=>{
   const pill=document.getElementById('roomTranscriptPill');
   const status=document.querySelector('#roomMeetingRecap .room-meeting-status');
   const empty=document.querySelector('#roomMeetingRecap .room-meeting-empty');
   return {
     state:pill.dataset.state,
     label:pill.textContent.trim(),
     title:pill.title,
     background:getComputedStyle(pill).backgroundColor,
     recapClass:status?.className||'',
     recapText:status?.textContent||'',
     recapEmpty:empty?.textContent||''
   };
 });
 assert.equal(stalled.state,'stalled',JSON.stringify(stalled));
 assert.equal(stalled.label,'Transcription stalled — reconnecting');
 assert.ok(stalled.title.includes('fence_refresh_failed'),stalled.title);
 assert.notEqual(stalled.background,live.background,'a stalled pill must not keep the live ground');
 const amber=rgb(stalled.background);
 assert.ok(amber[0]>amber[1]&&amber[1]>amber[2],'stalled ground is not amber: '+stalled.background);
 assert.ok(stalled.recapClass.includes('is-stalled'),stalled.recapClass);
 assert.ok(stalled.recapText.includes('No new transcript since'),stalled.recapText);
 assert.ok(!stalled.recapText.includes('Notes are getting ready'),stalled.recapText);
 assert.ok(stalled.recapText.includes('missing this stretch'),stalled.recapText);
 assert.ok(stalled.recapEmpty.includes('Existing captured moments remain'),stalled.recapEmpty);
 assert.ok(stalled.recapEmpty.includes('any recap will be missing this stretch'),stalled.recapEmpty);
 assert.ok(!stalled.recapEmpty.includes('Join the meeting'),stalled.recapEmpty);

 // The retry pulse is useful motion, but the design canon requires a still
 // state under the user's reduced-motion preference.
 await page.emulateMedia({reducedMotion:'reduce'});
 const reducedAnimation=await page.evaluate(()=>getComputedStyle(document.querySelector('.room-transcription-pill__dot')).animationName);
 assert.equal(reducedAnimation,'none',reducedAnimation);

 // Recovery: green returns, and the record keeps the hole.
 await page.evaluate(()=>applyRoomRecordingState({enabled:true,available:true,connected:true,revision:8,statusRevision:10,capturing:true,lastSegmentAt:'2026-09-02T21:27:34.007Z',updatedAt:'2026-09-02T21:27:34.007Z'},'office'));
 await page.evaluate(()=>appendRoomMeetingTranscriptEntry({id:'transcript-296',kind:'transcript',text:'Tyler: can you hear me?',createdAt:'2026-09-02T21:27:34.847Z',metadata:{roomId:'office',meetingId:'meeting-a',captureSequence:'296'}}));
 const recovered=await page.evaluate(()=>{
   const rows=[...document.querySelectorAll('#roomMeetingTranscript .room-meeting-transcript__entry, #roomMeetingTranscript .room-meeting-transcript__notice')];
   const recapStatus=document.querySelector('#roomMeetingRecap .room-meeting-status')?.textContent||'';
   return {
     state:document.getElementById('roomTranscriptPill').dataset.state,
     order:rows.map(row=>row.className),
     notice:document.querySelector('#roomMeetingTranscript .room-meeting-transcript__notice')?.textContent||'',
     recapStatus
   };
 });
 assert.equal(recovered.state,'live',JSON.stringify(recovered));
 assert.equal(recovered.order.length,3,JSON.stringify(recovered.order));
 assert.ok(recovered.order[1].includes('__notice'),'the gap must sit at the seam it describes: '+JSON.stringify(recovered.order));
 assert.ok(recovered.notice.includes('34 minutes were not captured'),recovered.notice);
 assert.ok(recovered.recapStatus.includes('34 minutes were not captured'),recovered.recapStatus);
 assert.ok(recovered.recapStatus.includes('recap excludes that uncaptured window'),recovered.recapStatus);
 assert.deepEqual(errors,[]);
 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "CAPTURE_HONESTY_INDEX="+indexPath, "TZ=UTC")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rendered capture-honesty harness: %v\n%s", err, output)
	}
}

// Finding 1 (CRITICAL). resetRoomRecordingAuthority runs on every socket
// (re)connect and room re-arm, and it returns roomRecordingCapturing to null.
// A stall that ends across that seam therefore arrives as null->true, never
// false->true — so a recovery judged off the previous frame reported NOTHING:
// the pill slid quietly back to green and the hole was never written into the
// record, which is the exact failure this whole slice exists to prevent. The
// open stall is held outside the recording authority for this sequence.
func TestTranscriptCaptureStallOutlivesTheRecordingAuthorityReset(t *testing.T) {
	html := readIndexHTML(t)
	parts := []string{
		jsFunction(html, "function roomRecordingAuthorityOrder(recording)"),
		jsFunction(html, "function roomRecordingAuthorityIsCurrent(recording, roomId)"),
		jsFunction(html, "function applyRoomRecordingState(recording, roomId = '')"),
		jsFunction(html, "function resetRoomRecordingAuthority(roomId = '')"),
		jsFunction(html, "function roomTranscriptionPresentation()"),
		jsFunction(html, "function roomTranscriptGapLabel(gapMs)"),
		jsFunction(html, "function roomTranscriptWindowLabel(startMs, endMs)"),
		jsFunction(html, "function meetingIntelligenceTimeLabel(value)"),
		jsFunction(html, "function noteRoomTranscriptCaptureRecovered(startedAt)"),
	}
	for index, part := range parts {
		if part == "" {
			t.Fatalf("reconnect-stall source %d unavailable", index)
		}
	}
	script := strings.Join(parts, "\n") + `
let roomRecordingEnabled = true
let roomRecordingAvailable = true
let roomRecordingConnected = true
let roomRecordingRevision = 0
let roomRecordingStatusRevision = 0
let roomRecordingRoomId = 'office'
let roomRecordingKnown = true
let roomRecordingSocketConfirmed = true
let roomRecordingPendingDesired = null
let roomRecordingPendingRevision = 0
let roomRecordingPendingTimer = 0
let roomRecordingUpdatedAt = ''
let roomRecordingUpdatedBy = ''
let roomRecordingCapturing = null
let roomRecordingStalledSince = ''
let roomRecordingLastSegmentAt = ''
let roomRecordingStallReason = ''
let roomTranscriptStallOpenSince = ''
let roomTranscriptStallOpenLastSegment = ''
let roomTranscriptStallOpenRoomId = ''
let roomTranscriptStallNotices = []
const activeJoin = { roomId: 'office' }
const appShell = { classList: { contains: () => true } }
const ws = { readyState: 1 }
const pc = { connectionState: 'connected' }
const WebSocket = { OPEN: 1 }
const window = { clearTimeout() {} }
const toasts = []
function roomMediaActive() { return true }
function updateRoomRecordingControls() {}
function applyRoomListeningStatus() {}
function renderMeetingRecap() {}
function showToast(payload) { toasts.push(payload) }
function roomTranscriptMeetingId() { return 'meeting-a' }

const frame = (statusRevision, extra) => Object.assign({
  enabled: true, available: true, connected: true, revision: 1, statusRevision
}, extra)

// Capture is landing, then the watchdog trips.
applyRoomRecordingState(frame(2, { capturing: true, lastSegmentAt: '2026-09-02T20:53:15.026Z' }), 'office')
applyRoomRecordingState(frame(3, {
  capturing: false,
  stalledSince: '2026-09-02T20:54:00.000Z',
  lastSegmentAt: '2026-09-02T20:53:15.026Z',
  stallReason: 'fence_refresh_failed'
}), 'office')
if (roomTranscriptionPresentation().state !== 'stalled') throw new Error('the stall never landed')
if (roomTranscriptStallOpenSince !== '2026-09-02T20:53:15.026Z') {
  throw new Error('the open hole was not retained outside the authority: ' + roomTranscriptStallOpenSince)
}

// THE SEAM: the socket drops and reconnects (or the server restarts), which
// re-arms the recording authority from scratch.
resetRoomRecordingAuthority('office')
if (roomRecordingCapturing !== null) throw new Error('the reconnect did not clear the recording authority')
if (roomRecordingStalledSince !== '' || roomRecordingLastSegmentAt !== '') {
  throw new Error('the reconnect kept authority fields it must re-learn from the server')
}
if (roomTranscriptStallOpenSince !== '2026-09-02T20:53:15.026Z') {
  throw new Error('the reconnect forgot the open hole: ' + roomTranscriptStallOpenSince)
}

// Capture comes back on the NEW connection: null -> true, not false -> true.
applyRoomRecordingState(frame(4, { capturing: true, lastSegmentAt: '2026-09-02T21:27:34.007Z' }), 'office')
if (roomTranscriptionPresentation().state !== 'live') throw new Error('recovery did not return the pill to live')
if (roomTranscriptStallNotices.length !== 1) throw new Error('the hole was never recorded across the reconnect')
if (roomTranscriptStallNotices[0].text !== 'Transcription recovered — 34 minutes were not captured (8:53–9:27 PM).') {
  throw new Error('recovery line: ' + roomTranscriptStallNotices[0].text)
}
if (toasts.length !== 1 || !toasts[0].text.includes('34 minutes were not captured')) {
  throw new Error('recovery must also reach a participant who is not on the transcript tab')
}
if (roomTranscriptStallOpenSince !== '' || roomTranscriptStallOpenLastSegment !== '') {
  throw new Error('a reported hole stayed armed and will be reported again')
}

// A healthy frame after the recovery must not re-report the closed hole, and a
// second reconnect while healthy must not invent one.
applyRoomRecordingState(frame(5, { capturing: true, lastSegmentAt: '2026-09-02T21:28:00.000Z' }), 'office')
resetRoomRecordingAuthority('office')
applyRoomRecordingState(frame(6, { capturing: true, lastSegmentAt: '2026-09-02T21:29:00.000Z' }), 'office')
if (roomTranscriptStallNotices.length !== 1) throw new Error('a closed hole was reported twice')
if (toasts.length !== 1) throw new Error('a closed hole toasted twice')

// A stall that opens on a frame naming no timestamps is still detectable, so
// its recovery is still admitted (unmeasured, never silent).
applyRoomRecordingState(frame(7, { capturing: false }), 'office')
if (!roomTranscriptStallOpenSince) throw new Error('an unanchored stall left nothing to recover from')
applyRoomRecordingState(frame(8, { capturing: true, lastSegmentAt: '2026-09-02T21:40:00.000Z' }), 'office')
if (roomTranscriptStallNotices.length !== 2) throw new Error('an unanchored stall recovered silently')

// Surviving a reconnect is the point; surviving a change of SEAT is not. A
// hole in the office record must never be admitted into another room's.
applyRoomRecordingState(frame(9, {
  capturing: false,
  stalledSince: '2026-09-02T22:00:00.000Z',
  lastSegmentAt: '2026-09-02T21:59:00.000Z'
}), 'office')
if (roomTranscriptStallOpenRoomId !== 'office') throw new Error('the open stall did not record its room')
activeJoin.roomId = 'studio'
applyRoomRecordingState(frame(10, { capturing: true, lastSegmentAt: '2026-09-02T22:30:00.000Z' }), 'studio')
if (roomTranscriptStallNotices.length !== 2) throw new Error("another room's hole was reported into this one")
if (roomTranscriptStallOpenSince !== '') throw new Error('a misattributed stall stayed armed')
`
	runClientProbe(t, script)
	// The reset must not forget the open stall — that is the whole finding.
	reset := functionBody(html, "function resetRoomRecordingAuthority(roomId = '')")
	if reset == "" {
		t.Fatal("resetRoomRecordingAuthority source unavailable")
	}
	if strings.Contains(reset, "roomTranscriptStallOpenSince = ''") ||
		strings.Contains(reset, "roomTranscriptStallOpenLastSegment = ''") {
		t.Fatal("resetRoomRecordingAuthority must not clear the open stall: a reconnect would turn the recovery into a silent null->true edge")
	}
}

// Rendered companion to the finding-1 pin: the same reconnect sequence driven
// through the real page, because only a render proves the hole reaches the
// record a participant actually reads.
func TestFrontendTranscriptCaptureGapSurvivesASocketReconnect(t *testing.T) {
	if testing.Short() {
		t.Skip("rendered browser contract")
	}
	indexPath, err := filepath.Abs("index.html")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const fs=require('fs');
const http=require('http');
const assert=require('assert/strict');
const {chromium}=require('playwright');
const html=fs.readFileSync(process.env.CAPTURE_HONESTY_INDEX,'utf8');
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@example.test',name:'AJ'}));}
  if(req.url.startsWith('/participants')){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({roomId:'office',participants:['AJ'],occupiedSeats:1,capacity:10,mediaStates:{},endpointCounts:{AJ:1},recording:{enabled:true,available:true,connected:true,revision:4,statusRevision:4,updatedAt:'2026-09-02T20:00:00Z'}}));}
  if(req.url==='/api/stride/v1/mobile/surfaces/organizations'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({availability:'available',surface:'organizations',revision:1,items:[]}));}
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true});
 const page=await browser.newPage({viewport:{width:1280,height:900}});
 const errors=[];
 page.on('pageerror',error=>errors.push(String(error)));
 await page.goto('http://127.0.0.1:'+server.address().port+'/video',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed');
 await page.waitForFunction(()=>document.getElementById('appShell')?.dataset.tool==='room');
 await page.waitForLoadState('networkidle');
 await page.waitForTimeout(400);
 await page.evaluate(()=>{for(let timer=1;timer<50000;timer++){clearTimeout(timer);clearInterval(timer)}});
 await page.evaluate(()=>{
   const shell=document.getElementById('appShell');
   shell.dataset.tool='room';shell.classList.add('is-in-room','is-authed');
   activeJoin={roomId:'office',passcode:'',guest:false};guestMode=false;
   localStream={};pc={connectionState:'connected'};
   ws={readyState:WebSocket.OPEN,send:()=>{}};
   handleMeetingRecord({id:'meeting-a',roomId:'office',active:true,startedAt:'2026-09-02T20:00:46.000Z'});
   appendRoomMeetingTranscriptEntry({id:'transcript-295',kind:'transcript',text:"Tyler: I think it's",createdAt:'2026-09-02T20:53:15.026Z',metadata:{roomId:'office',meetingId:'meeting-a',captureSequence:'295'}});
   applyRoomRecordingState({enabled:true,available:true,connected:true,revision:8,statusRevision:8,capturing:true,lastSegmentAt:'2026-09-02T20:53:15.026Z',updatedAt:'2026-09-02T20:53:20.000Z'},'office');
   setRoomChatOpen(true);setRoomMeetingMode('transcript');
 });
 assert.equal(await page.evaluate(()=>document.getElementById('roomTranscriptPill').dataset.state),'live');

 // The watchdog trips: amber, and the hole is now open.
 await page.evaluate(()=>applyRoomRecordingState({enabled:true,available:true,connected:true,revision:8,statusRevision:9,capturing:false,stalledSince:'2026-09-02T20:54:00.000Z',lastSegmentAt:'2026-09-02T20:53:15.026Z',stallReason:'fence_refresh_failed',updatedAt:'2026-09-02T20:54:00.000Z'},'office'));
 const stalled=await page.evaluate(()=>({state:document.getElementById('roomTranscriptPill').dataset.state,open:roomTranscriptStallOpenSince}));
 assert.equal(stalled.state,'stalled',JSON.stringify(stalled));
 assert.equal(stalled.open,'2026-09-02T20:53:15.026Z',JSON.stringify(stalled));

 // THE SEAM: the socket reconnects mid-stall and the recording authority is
 // re-armed from scratch, exactly as room_live's (re)connect path does.
 const reconnected=await page.evaluate(()=>{
   resetRoomRecordingAuthority(activeJoin.roomId||'office');
   return {capturing:roomRecordingCapturing,state:document.getElementById('roomTranscriptPill').dataset.state,open:roomTranscriptStallOpenSince};
 });
 assert.equal(reconnected.capturing,null,JSON.stringify(reconnected));
 assert.equal(reconnected.state,'unavailable','the reset must clear the old authority instead of preserving green');
 assert.equal(reconnected.open,'2026-09-02T20:53:15.026Z','the reconnect forgot the open hole: '+JSON.stringify(reconnected));

 // Capture returns on the new connection (null -> true) and the record must
 // still carry the sized hole where it happened.
 await page.evaluate(()=>{
   applyRoomRecordingState({enabled:true,available:true,connected:true,revision:8,statusRevision:10,capturing:true,lastSegmentAt:'2026-09-02T21:27:34.007Z',updatedAt:'2026-09-02T21:27:34.007Z'},'office');
   appendRoomMeetingTranscriptEntry({id:'transcript-296',kind:'transcript',text:'Tyler: can you hear me?',createdAt:'2026-09-02T21:27:34.847Z',metadata:{roomId:'office',meetingId:'meeting-a',captureSequence:'296'}});
 });
 const recovered=await page.evaluate(()=>{
   const rows=[...document.querySelectorAll('#roomMeetingTranscript .room-meeting-transcript__entry, #roomMeetingTranscript .room-meeting-transcript__notice')];
   return {
     state:document.getElementById('roomTranscriptPill').dataset.state,
     open:roomTranscriptStallOpenSince,
     order:rows.map(row=>row.className),
     notice:document.querySelector('#roomMeetingTranscript .room-meeting-transcript__notice')?.textContent||''
   };
 });
 assert.equal(recovered.state,'live',JSON.stringify(recovered));
 assert.equal(recovered.order.length,3,JSON.stringify(recovered.order));
 assert.ok(recovered.order[1].includes('__notice'),'the gap must sit at the seam it describes: '+JSON.stringify(recovered.order));
 assert.ok(recovered.notice.includes('34 minutes were not captured'),recovered.notice);
 assert.ok(recovered.notice.includes('8:53–9:27 PM'),recovered.notice);
 assert.equal(recovered.open,'','a reported hole must not stay armed');
 assert.deepEqual(errors,[]);
 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "CAPTURE_HONESTY_INDEX="+indexPath, "TZ=UTC")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rendered reconnect-stall harness: %v\n%s", err, output)
	}
}
