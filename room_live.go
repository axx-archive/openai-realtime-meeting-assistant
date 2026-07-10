package main

// Multi-room W3 (docs/plans/multi-room-2026-07-08.md §4.2/§4.5/§5.4/§6): the
// per-room live plane. Every piece of runtime state that was implicitly "the
// office" — presence, recording, speaker attribution, the audio mixer and the
// transcription lane — moves into a roomLiveState keyed by room id on
// kanbanBoardApp (the registry the spec calls roomManager; it is guarded by
// app.mu like the fields it replaced). The office room is seeded at
// construction and keeps its boot-started mixer/lane; named rooms create
// media lazily on first admission and tear it down after the idle-end close
// chain, fenced by mediaGen so a rejoin racing a teardown can never resurrect
// a closed lane. Guest containment (socket caps, chat token bucket, the
// write-time event allowlist) lives here too so main.go's websocket handler
// stays a router.

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// §6.1 pre-upgrade DoS caps: a guest link must not widen the known
	// pre-hello allocation surface beyond a bounded blast radius.
	maxGuestSocketsPerSession = 2
	maxGuestPreHelloPerIP     = 4
	defaultMaxGuestsPerRoom   = 5

	// §6.5 guest chat token bucket: burst 5, refill 1 per 3 seconds.
	guestChatBucketBurst  = 5.0
	guestChatBucketRefill = 3 * time.Second

	// §6.5 transcription ceiling for guest-enabled rooms, member-extendable by
	// flipping recording back on.
	defaultGuestTranscriptionCapMinutes = 120
	guestTranscriptionCapActor          = "system:guest-cap"
)

// roomLiveState owns everything that is per-room at runtime. Guarded by
// app.mu (the same lock that guarded these fields when they lived directly on
// kanbanBoardApp), except where a field documents otherwise.
type roomLiveState struct {
	id string

	// presence: canonical (or guest display) name -> liveness stamp, endpoint
	// sessions, media state. Same shapes and semantics as the old office-only
	// fields — the laptop+phone endpoint contract is untouched.
	participants         map[string]time.Time
	participantCounts    map[string]int
	participantEndpoints map[string]map[string]string
	participantMedia     map[string]participantMediaState
	// guestSeats maps a guest session key to the room-unique display name the
	// server minted for it ("Guest Sam", "Guest Sam 2"). Seats are per guest
	// SESSION: a second socket under the same session shares the seat.
	guestSeats map[string]string

	// per-room transcript recording toggle.
	recordingEnabled   bool
	recordingUpdatedAt time.Time
	recordingUpdatedBy string

	// speaker attribution + active speaker, fed by THIS room's mixer activity
	// listener (roomAudioActivityListener).
	audioActivity             []participantAudioFrame
	currentSpeechStartedAt    time.Time
	currentSpeechStoppedAt    time.Time
	pendingAttributionWindows []attributionWindow
	activeSpeakerName         string
	activeSpeakerCandidate    string
	activeSpeakerCandidateAt  time.Time
	activeSpeakerPayload      *activeSpeakerPayload

	// lazy media (named rooms; the office keeps the boot-started globals until
	// the W4 realtime extraction). mediaGen fences teardown vs rejoin: every
	// create and every teardown bumps it, and deferred work (the guest
	// transcription cap timer) only acts when its captured gen is still live.
	mixer    *audioMixer
	lane     *meetingTranscriptionLane
	mediaGen uint64
	capTimer *time.Timer

	// §6.5 per-guest-session chat token buckets.
	chatBuckets map[string]*guestChatBucket
}

type guestChatBucket struct {
	tokens float64
	last   time.Time
}

func newRoomLiveState(roomID string, now time.Time) *roomLiveState {
	return &roomLiveState{
		id:                   normalizeRoomID(roomID),
		participants:         map[string]time.Time{},
		participantCounts:    map[string]int{},
		participantEndpoints: map[string]map[string]string{},
		participantMedia:     map[string]participantMediaState{},
		guestSeats:           map[string]string{},
		recordingEnabled:     true,
		recordingUpdatedAt:   now,
		chatBuckets:          map[string]*guestChatBucket{},
	}
}

// roomLiveLocked returns (creating if needed) the room's live state. Callers
// must hold app.mu.
func (app *kanbanBoardApp) roomLiveLocked(roomID string) *roomLiveState {
	roomID = normalizeRoomID(roomID)
	if app.roomLive == nil {
		app.roomLive = map[string]*roomLiveState{}
	}
	state, ok := app.roomLive[roomID]
	if !ok {
		state = newRoomLiveState(roomID, time.Now().UTC())
		app.roomLive[roomID] = state
	}
	return state
}

// liveRoomIDs snapshots the ids of rooms that currently hold live state.
func (app *kanbanBoardApp) liveRoomIDs() []string {
	app.mu.Lock()
	defer app.mu.Unlock()
	ids := make([]string, 0, len(app.roomLive))
	for id := range app.roomLive {
		ids = append(ids, id)
	}
	return ids
}

/* ---------- guest seats ---------- */

func maxGuestsPerRoom() int {
	raw := strings.TrimSpace(os.Getenv("BONFIRE_MAX_GUESTS_PER_ROOM"))
	if raw == "" {
		return defaultMaxGuestsPerRoom
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return defaultMaxGuestsPerRoom
	}
	return value
}

// guestSeatCount reports how many guest sessions currently hold a seat in the
// room (for the §6.1 per-room guest cap).
func (app *kanbanBoardApp) guestSeatCount(roomID string) int {
	app.mu.Lock()
	defer app.mu.Unlock()
	return len(app.roomLiveLocked(roomID).guestSeats)
}

// guestRoomAtCapacity reports whether a NEW guest session would exceed the
// room's guest cap. A session that already holds a seat is never at capacity
// (its second socket shares the existing seat).
func (app *kanbanBoardApp) guestRoomAtCapacity(roomID string, sessionKey string) bool {
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)
	if _, seated := state.guestSeats[sessionKey]; seated {
		return false
	}
	return len(state.guestSeats) >= maxGuestsPerRoom()
}

// admitGuestParticipant seats a guest session in its room: the display name
// is the server-enforced "Guest "+name, deduped with a numeric suffix against
// everything already present in the room (two guests named Sam coexist as
// "Guest Sam" and "Guest Sam 2"). Seats key on the guest session, so a second
// socket under the same session resumes the same seat as another endpoint
// rather than evicting the first. Capacity and the guest cap are enforced
// here — the pre-upgrade check is advisory, this one is authoritative.
func (app *kanbanBoardApp) admitGuestParticipant(roomID string, sessionKey string, requestedName string, participantSessionID string) (string, bool, error) {
	roomID = normalizeRoomID(roomID)
	base := strings.TrimSpace(requestedName)
	if base == "" {
		base = "Guest"
	}

	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	display, seated := state.guestSeats[sessionKey]
	if !seated {
		if len(state.guestSeats) >= maxGuestsPerRoom() {
			app.mu.Unlock()
			return "", false, errGuestRoomFull
		}
		display = dedupeGuestDisplayNameLocked(state, guestNamePrefix+base)
		state.guestSeats[sessionKey] = display
	}
	app.mu.Unlock()

	admitted, firstEndpoint, err := app.admitParticipantSessionEndpointInRoom(roomID, display, participantSessionID, participantSessionID)
	if err != nil && !seated {
		app.mu.Lock()
		delete(app.roomLiveLocked(roomID).guestSeats, sessionKey)
		app.mu.Unlock()
	}
	return admitted, firstEndpoint, err
}

// dedupeGuestDisplayNameLocked appends " 2", " 3", … until the display name
// is unique among the room's present participants and other guest seats.
// Callers hold app.mu.
func dedupeGuestDisplayNameLocked(state *roomLiveState, display string) string {
	taken := func(candidate string) bool {
		if _, present := state.participants[candidate]; present {
			return true
		}
		for _, existing := range state.guestSeats {
			if strings.EqualFold(existing, candidate) {
				return true
			}
		}
		return false
	}
	if !taken(display) {
		return display
	}
	for suffix := 2; ; suffix++ {
		candidate := display + " " + strconv.Itoa(suffix)
		if !taken(candidate) {
			return candidate
		}
	}
}

// releaseGuestSeatIfGone drops the session's seat mapping once its display
// name no longer holds presence in the room (the last socket left or was
// reaped). Chat buckets go with it.
func (app *kanbanBoardApp) releaseGuestSeatIfGone(roomID string, sessionKey string) {
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)
	display, ok := state.guestSeats[sessionKey]
	if !ok {
		return
	}
	if state.participantCounts[display] > 0 {
		return
	}
	delete(state.guestSeats, sessionKey)
	delete(state.chatBuckets, sessionKey)
}

/* ---------- §6.5 guest chat token bucket ---------- */

// allowGuestRoomChat charges one token from the guest session's bucket
// (burst 5, refill 1 per 3s) and reports whether the message may proceed.
func (app *kanbanBoardApp) allowGuestRoomChat(roomID string, sessionKey string, now time.Time) bool {
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)
	bucket := state.chatBuckets[sessionKey]
	if bucket == nil {
		bucket = &guestChatBucket{tokens: guestChatBucketBurst, last: now}
		state.chatBuckets[sessionKey] = bucket
	}
	if elapsed := now.Sub(bucket.last); elapsed > 0 {
		bucket.tokens += float64(elapsed) / float64(guestChatBucketRefill)
		if bucket.tokens > guestChatBucketBurst {
			bucket.tokens = guestChatBucketBurst
		}
	}
	bucket.last = now
	if bucket.tokens < 1 {
		return false
	}
	bucket.tokens--
	return true
}

/* ---------- §6.1 pre-upgrade guest socket caps ---------- */

var errGuestRoomFull = &guestCapError{"this room already has its maximum number of guests"}

type guestCapError struct{ message string }

func (e *guestCapError) Error() string { return e.message }

// guestSocketCapRegistry tracks live guest sockets per session key and
// pre-hello (unadmitted) guest sockets per client IP. It is package-level —
// like the peer-connection tables — because the checks run BEFORE the
// websocket upgrade, and counters must decrement on socket close even when
// admission never happened.
type guestSocketCapRegistry struct {
	mu           sync.Mutex
	perSession   map[string]int
	preHelloByIP map[string]int
}

var guestSocketCaps = &guestSocketCapRegistry{
	perSession:   map[string]int{},
	preHelloByIP: map[string]int{},
}

// acquire reserves a guest socket slot pre-upgrade. It returns admit (call
// once the participant hello is accepted, releasing the pre-hello IP slot)
// and release (call when the socket closes), or ok=false when a cap is hit.
func (r *guestSocketCapRegistry) acquire(sessionKey string, clientIP string) (admit func(), release func(), ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.perSession[sessionKey] >= maxGuestSocketsPerSession {
		return nil, nil, false
	}
	if r.preHelloByIP[clientIP] >= maxGuestPreHelloPerIP {
		return nil, nil, false
	}
	r.perSession[sessionKey]++
	r.preHelloByIP[clientIP]++

	var admitOnce, releaseOnce sync.Once
	admitted := false
	admit = func() {
		admitOnce.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			admitted = true
			r.decrementPreHelloLocked(clientIP)
		})
	}
	release = func() {
		releaseOnce.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.perSession[sessionKey] <= 1 {
				delete(r.perSession, sessionKey)
			} else {
				r.perSession[sessionKey]--
			}
			if !admitted {
				r.decrementPreHelloLocked(clientIP)
			}
		})
	}
	return admit, release, true
}

func (r *guestSocketCapRegistry) decrementPreHelloLocked(clientIP string) {
	if r.preHelloByIP[clientIP] <= 1 {
		delete(r.preHelloByIP, clientIP)
	} else {
		r.preHelloByIP[clientIP]--
	}
}

/* ---------- §6.2 write-time guest event allowlist ---------- */

// guestWritableKanbanEvents is the exhaustive set of kanban-envelope events a
// guest socket may ever receive. Anything else written to a guest writer is
// dropped and counted — the belt-and-suspenders that survives future
// mis-routed broadcasts, since guests share the media fan-out pool.
var guestWritableKanbanEvents = map[string]bool{
	"access_granted":     true,
	"access_denied":      true,
	"session_replaced":   true,
	"server_version":     true,
	"participants":       true,
	"participant_joined": true,
	"participant_left":   true,
	"participant_track":  true,
	"active_speaker":     true,
	"meeting":            true,
	"room_chat":          true,
	"room_chat_history":  true,
	// §3.7 archive close: guests seated in an archived room are exactly who
	// must hear that their room is gone.
	"room_closed": true,
	"offer":       true,
	"answer":      true,
	"candidate":   true,
}

// guestTopLevelEvents are the raw websocketMessage envelopes a guest writer
// accepts: the kanban envelope (inner event gated above) plus signaling.
var guestTopLevelEvents = map[string]bool{
	"kanban":    true,
	"offer":     true,
	"answer":    true,
	"candidate": true,
}

// guestEventsDropped counts allowlist drops (metric/log, §6.2).
var guestEventsDropped atomic.Int64

func guestWriterAllowsKanbanEvent(w *threadSafeWriter, event string) bool {
	if w == nil || !w.guest {
		return true
	}
	if guestWritableKanbanEvents[event] {
		return true
	}
	guestEventsDropped.Add(1)
	log.Infof("guest_event_dropped event=%s total=%d", event, guestEventsDropped.Load())
	return false
}

// guestInboundEvents is the §5.4 inbound allowlist: the hello, signaling,
// liveness, and room chat. Every other inbound event kind from a guest socket
// is dropped and logged ("office" is special-cased to access_denied+close in
// the handler).
var guestInboundEvents = map[string]bool{
	"participant":  true,
	"media_ready":  true,
	"candidate":    true,
	"answer":       true,
	"restart_ice":  true,
	"select_layer": true,
	"room_ping":    true,
	"room_chat":    true,
}

/* ---------- lazy media lifecycle (§4.4) ---------- */

// roomAudioActivityListener feeds a named room's mixer activity into that
// room's attribution state — the office listener stays kanbanApp itself.
type roomAudioActivityListener struct {
	app    *kanbanBoardApp
	roomID string
}

func (l *roomAudioActivityListener) NoteAudioActivity(at time.Time, levels []audioActivityLevel) {
	l.app.noteAudioActivityForRoom(l.roomID, at, levels)
}

// roomLaneAudioSink is a named room's mixer sink (key
// realtimeMixedAudioSinkKey+":"+roomID): recording-gated lane feed, no
// Realtime write — the per-room Scout peer is W4, and listen-only rooms never
// get one.
type roomLaneAudioSink struct {
	app    *kanbanBoardApp
	roomID string
}

func (s *roomLaneAudioSink) WriteMixedPCM(roomPCM []int16) error {
	if len(roomPCM) == 0 || pcmIsZero(roomPCM) {
		return nil
	}
	if !s.app.transcriptRecordingActiveInRoom(s.roomID) {
		return nil
	}
	s.app.mu.Lock()
	lane := s.app.roomLiveLocked(s.roomID).lane
	s.app.mu.Unlock()
	if lane != nil {
		lane.enqueue(roomPCM)
	}
	return nil
}

// roomMixerFor returns the mixer that room audio should decode into: the
// boot-started global for the office, the lazy per-room mixer otherwise (nil
// while the room has no media — frames from a join racing a teardown are
// dropped by the nil-safe mixer methods).
func (app *kanbanBoardApp) roomMixerFor(roomID string) *audioMixer {
	roomID = normalizeRoomID(roomID)
	if roomID == officeRoomID {
		return roomMixer
	}
	if app == nil {
		return nil
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	return app.roomLiveLocked(roomID).mixer
}

// ensureRoomMedia lazily creates a room's media on first admission (§4.4).
// Named rooms get their mixer + transcription lane here; the office (W4) gets
// its lane + Scout Realtime peer via ensureOfficeMedia — lazy for every room,
// ending the always-on boot spend. Idempotent per sitting.
func (app *kanbanBoardApp) ensureRoomMedia(roomID string) {
	roomID = normalizeRoomID(roomID)
	if app == nil {
		return
	}
	if roomID == officeRoomID {
		app.ensureOfficeMedia()
		return
	}

	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	if state.mixer != nil {
		app.mu.Unlock()
		return
	}
	state.mediaGen++
	gen := state.mediaGen
	mixer := newAudioMixer()
	mixer.setActivityListener(&roomAudioActivityListener{app: app, roomID: roomID})
	state.mixer = mixer

	apiKey := app.apiKey
	var lane *meetingTranscriptionLane
	if strings.TrimSpace(apiKey) != "" && transcriptionLaneEnabled() {
		lane = newMeetingTranscriptionLaneForRoom(app, apiKey, transcriptionLaneModel(), roomID)
		// Started before it becomes observable through state.lane, so a
		// racing teardown can never close() a lane whose run loop (the one
		// that signals done) has not launched yet.
		lane.start()
		state.lane = lane
		mixer.setSink(realtimeMixedAudioSinkKey+":"+roomID, &roomLaneAudioSink{app: app, roomID: roomID})
	}
	guestEnabled := false
	if room, ok := appRoomStore().byID(roomID); ok {
		guestEnabled = room.GuestEnabled
	}
	if guestEnabled {
		app.armGuestTranscriptionCapLocked(state, gen)
	}
	app.mu.Unlock()

	log.Infof("room_media_started room=%s gen=%d lane=%t", roomID, gen, lane != nil)
}

// teardownRoomMediaAfterIdle runs at the tail of a named room's idle-end
// close chain: close the lane, close the mixer, bump mediaGen so any deferred
// work fenced on the old gen goes quiet. A rejoin during the grace window
// cancels the idle end upstream and never reaches here; a rejoin after this
// simply recreates media via ensureRoomMedia.
func (app *kanbanBoardApp) teardownRoomMediaAfterIdle(roomID string) {
	roomID = normalizeRoomID(roomID)
	if app == nil {
		return
	}
	if roomID == officeRoomID {
		app.teardownOfficeMediaAfterIdle()
		return
	}

	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	if app.activeParticipantCountInRoomLocked(state) > 0 {
		// an admission raced the close-flush; the new sitting keeps its media.
		app.mu.Unlock()
		return
	}
	mixer := state.mixer
	lane := state.lane
	capTimer := state.capTimer
	state.mixer = nil
	state.lane = nil
	state.capTimer = nil
	state.mediaGen++
	gen := state.mediaGen
	app.mu.Unlock()

	if capTimer != nil {
		capTimer.Stop()
	}
	if lane != nil {
		lane.close()
	}
	if mixer != nil {
		mixer.close()
	}
	if mixer != nil || lane != nil {
		log.Infof("room_media_torn_down room=%s gen=%d", roomID, gen)
	}
}

// teardownOfficeMediaAfterIdle is the office's W4 idle teardown: close the
// lane, drop the mixer sink once nothing consumes it, close the Scout peer
// (no restart), and bump the office mediaGen so any queued reconnect fenced
// on the old generation goes quiet. The shared roomMixer itself stays up — it
// is boot-owned by main. A rejoin during the grace window cancels the idle
// end upstream; a rejoin after this recreates media via ensureOfficeMedia.
func (app *kanbanBoardApp) teardownOfficeMediaAfterIdle() {
	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	if app.activeParticipantCountInRoomLocked(state) > 0 {
		// an admission raced the close-flush; the new sitting keeps its media.
		app.mu.Unlock()
		return
	}
	lane := app.transcriptLane
	app.transcriptLane = nil
	state.mediaGen++
	gen := state.mediaGen
	app.mu.Unlock()

	app.teardownRealtimePeerForIdle()
	if lane != nil {
		lane.close()
	}
	app.removeRoomMixerSinkIfIdle()
	if lane != nil {
		log.Infof("room_media_torn_down room=%s gen=%d", officeRoomID, gen)
	}
}

/* ---------- §6.5 guest transcription cap ---------- */

func guestRoomTranscriptionCap() time.Duration {
	raw := strings.TrimSpace(os.Getenv("BONFIRE_GUEST_ROOM_TRANSCRIPTION_CAP_MIN"))
	if raw == "" {
		return defaultGuestTranscriptionCapMinutes * time.Minute
	}
	minutes, err := strconv.Atoi(raw)
	if err != nil || minutes < 1 {
		return defaultGuestTranscriptionCapMinutes * time.Minute
	}
	return time.Duration(minutes) * time.Minute
}

// armGuestTranscriptionCapLocked schedules the per-sitting lane-time ceiling
// for a guest-enabled room. Callers hold app.mu. The fired timer re-checks
// mediaGen so a cap armed for a torn-down sitting can never flip the next one.
func (app *kanbanBoardApp) armGuestTranscriptionCapLocked(state *roomLiveState, gen uint64) {
	if state.capTimer != nil {
		state.capTimer.Stop()
	}
	roomID := state.id
	state.capTimer = time.AfterFunc(guestRoomTranscriptionCap(), func() {
		app.enforceGuestTranscriptionCap(roomID, gen)
	})
}

// enforceGuestTranscriptionCap flips the room's recording off with the
// system:guest-cap actor when the sitting is still the one the cap was armed
// for. Members see the existing recording-off state; flipping it back on
// grants another cap window (setTranscriptRecordingInRoom re-arms).
func (app *kanbanBoardApp) enforceGuestTranscriptionCap(roomID string, gen uint64) {
	if app == nil {
		return
	}
	roomID = normalizeRoomID(roomID)
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	if state.mediaGen != gen || !state.recordingEnabled {
		app.mu.Unlock()
		return
	}
	app.mu.Unlock()

	snapshot := app.setTranscriptRecordingInRoom(roomID, false, guestTranscriptionCapActor)
	log.Infof("guest_transcription_cap_hit room=%s cap=%s", roomID, guestRoomTranscriptionCap())
	broadcastRoomKanbanEvent(roomID, "participants", snapshot)
}

/* ---------- one account, one live room seat (§2) ---------- */

// evictAccountFromOtherRooms enforces the one-account-one-room rule: when an
// account is admitted into joinedRoomID, every seat it holds in any OTHER
// room is session_replaced-evicted — presence forgotten, sockets told and
// closed, forwarded tracks pruned (name+session scoped, so the new room's
// media is untouched).
func (app *kanbanBoardApp) evictAccountFromOtherRooms(name string, joinedRoomID string) {
	name = canonicalRoomParticipantName(name)
	if app == nil || name == "" {
		return
	}
	joinedRoomID = normalizeRoomID(joinedRoomID)

	type evictedRoom struct {
		roomID     string
		sessionIDs []string
	}
	var evictions []evictedRoom
	app.mu.Lock()
	for roomID, state := range app.roomLive {
		if roomID == joinedRoomID || state.participantCounts[name] <= 0 {
			continue
		}
		sessionIDs := make([]string, 0, len(state.participantEndpoints[name]))
		for _, sessionID := range state.participantEndpoints[name] {
			sessionIDs = append(sessionIDs, sessionID)
		}
		delete(state.participants, name)
		delete(state.participantCounts, name)
		delete(state.participantEndpoints, name)
		delete(state.participantMedia, name)
		evictions = append(evictions, evictedRoom{roomID: roomID, sessionIDs: sessionIDs})
	}
	app.mu.Unlock()

	for _, eviction := range evictions {
		for _, sessionID := range eviction.sessionIDs {
			notifySessionReplacedAndClose(sessionID)
			unregisterParticipantSession(name, sessionID)
		}
		log.Infof("room_seat_evicted participant=%s from=%s joined=%s sessions=%d", name, eviction.roomID, joinedRoomID, len(eviction.sessionIDs))
		broadcastRoomKanbanEvent(eviction.roomID, "participant_left", map[string]any{
			"name":   name,
			"roomId": eviction.roomID,
		})
		broadcastRoomKanbanEvent(eviction.roomID, "participants", app.roomSnapshotForRoom(eviction.roomID))
		app.noteMeetingOccupancy(eviction.roomID)
	}
	if len(evictions) > 0 {
		broadcastRoomsSnapshot()
	}
}

// notifySessionReplacedAndClose tells the session's socket why it is going
// away and closes it, scanning both the media pool and the admitted-only
// index under listLock.
func notifySessionReplacedAndClose(sessionID string) {
	var writers []*threadSafeWriter
	seen := map[*threadSafeWriter]bool{}
	listLock.RLock()
	for i := range peerConnections {
		if peerConnections[i].sessionID == sessionID && peerConnections[i].websocket != nil && !seen[peerConnections[i].websocket] {
			seen[peerConnections[i].websocket] = true
			writers = append(writers, peerConnections[i].websocket)
		}
	}
	for _, state := range activeParticipantConnections {
		if state.sessionID == sessionID && state.websocket != nil && !seen[state.websocket] {
			seen[state.websocket] = true
			writers = append(writers, state.websocket)
		}
	}
	listLock.RUnlock()

	for _, writer := range writers {
		_ = sendKanbanEvent(writer, "session_replaced", "You joined another room; this seat was released.")
		_ = writer.Close()
	}
}

/* ---------- archive close (rooms UX §3.7) ---------- */

// closeSessionSockets closes every socket a session holds, scanning both the
// media pool and the admitted-only index under listLock. Unlike
// notifySessionReplacedAndClose it writes nothing — the room-scoped
// room_closed broadcast has already told the tab why.
func closeSessionSockets(sessionID string) {
	var writers []*threadSafeWriter
	seen := map[*threadSafeWriter]bool{}
	listLock.RLock()
	for i := range peerConnections {
		if peerConnections[i].sessionID == sessionID && peerConnections[i].websocket != nil && !seen[peerConnections[i].websocket] {
			seen[peerConnections[i].websocket] = true
			writers = append(writers, peerConnections[i].websocket)
		}
	}
	for _, state := range activeParticipantConnections {
		if state.sessionID == sessionID && state.websocket != nil && !seen[state.websocket] {
			seen[state.websocket] = true
			writers = append(writers, state.websocket)
		}
	}
	listLock.RUnlock()

	for _, writer := range writers {
		_ = writer.Close()
	}
}

// closeRoomForArchive ends an archived room's live sitting so occupants are
// never marooned in a half-dead room: every seated socket hears room_closed
// (on the guest write allowlist — guests are exactly who must be told),
// presence is forgotten and the sockets/tracks torn down, then the sitting
// closes through the SAME chain as idle end (deferred-notification flush,
// close-flush, id rotation, silent auto-archive, media teardown). The office
// is room zero and never archives; the room store already refused it.
func (app *kanbanBoardApp) closeRoomForArchive(roomID string) {
	roomID = normalizeRoomID(roomID)
	if app == nil || roomID == officeRoomID {
		return
	}
	// This runs async after the archive response; a restore may have landed in
	// the gap. Restore is an undo — if the room is live again, leave the
	// sitting and its occupants alone.
	if room, ok := appRoomStore().byID(roomID); ok && !room.Archived {
		return
	}

	broadcastRoomKanbanEvent(roomID, "room_closed", map[string]any{"roomId": roomID})

	type closedSeat struct {
		name       string
		sessionIDs []string
	}
	var seats []closedSeat
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	for name := range state.participantCounts {
		sessionIDs := make([]string, 0, len(state.participantEndpoints[name]))
		for _, sessionID := range state.participantEndpoints[name] {
			sessionIDs = append(sessionIDs, sessionID)
		}
		delete(state.participants, name)
		delete(state.participantCounts, name)
		delete(state.participantEndpoints, name)
		delete(state.participantMedia, name)
		if isGuestDisplayName(name) {
			for sessionKey, display := range state.guestSeats {
				if strings.EqualFold(display, name) {
					delete(state.guestSeats, sessionKey)
					delete(state.chatBuckets, sessionKey)
				}
			}
		}
		seats = append(seats, closedSeat{name: name, sessionIDs: sessionIDs})
	}
	app.mu.Unlock()

	for _, seat := range seats {
		for _, sessionID := range seat.sessionIDs {
			closeSessionSockets(sessionID)
			unregisterParticipantSession(seat.name, sessionID)
		}
		log.Infof("room_seat_closed participant=%s room=%s sessions=%d; room archived", seat.name, roomID, len(seat.sessionIDs))
	}

	// The sitting close chain — endMeetingForIdle without the idle generation
	// gate (an archive is an unconditional close; presence above is already
	// zero, so no admission can race the record back open on the OLD id — a
	// post-archive join is refused by the room store regardless).
	if app.meetings != nil {
		if record, ok := app.meetings.activeRecord(roomID); ok {
			if closed, changed := app.meetings.endMeeting(record.ID, time.Now().UTC(), meetingEndedReasonRoomClosed, ""); changed {
				app.flushDeferredNotifications("meeting_end")
				app.flushAmbientAgentsForClose("room-archive", roomID, closed.ListenOnly)
				if app.memory != nil {
					app.memory.rotateMeetingIDIfCurrent(roomID, closed.ID)
				}
				app.broadcastMeetingRecord(closed)
				app.autoArchiveIdleMeeting(closed)
			}
		}
	}
	app.teardownRoomMediaAfterIdle(roomID)
	broadcastRoomsSnapshot()
}

/* ---------- rooms-list office event (§4.5) ---------- */

// broadcastRoomsSnapshot pushes the rooms-list snapshot on the office tier so
// the members' rooms card stays live across create/join/leave/reap/archive.
// Only an already-open room store is read (the sweepExpiredGuestLinksIfOpen
// pattern) so a presence sweep can never conjure a rooms.json into a data
// directory that has none.
func broadcastRoomsSnapshot() {
	if kanbanApp == nil {
		return
	}
	roomStoreMu.Lock()
	store := roomStoreCache[roomsFilePath()]
	roomStoreMu.Unlock()
	if store == nil {
		return
	}
	rooms := []map[string]any{}
	for _, room := range store.list() {
		rooms = append(rooms, roomListPayload(room))
	}
	broadcastOfficeKanbanEvent("rooms", map[string]any{"rooms": rooms})
}
