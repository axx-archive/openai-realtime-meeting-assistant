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

	"github.com/pion/webrtc/v4"
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

	// §6.5 hardening (2026-07-10 incident): the media roster/self-heal and
	// telemetry inbound events became guest-reachable, so a hostile guest-link
	// holder could spam them at socket line rate. They get the same per-guest
	// token-bucket treatment as chat.
	//   - state/repair (participant_media_state + request_participant_tracks):
	//     each accepted event fans out a room-wide roster broadcast or a global
	//     peer-sync walk (amplification × room size), so the ceiling is
	//     chat-tight.
	//   - telemetry (media_quality + media_error): log-only writes; the legit
	//     client emits every ~4-12s, so a smaller burst with a slower refill
	//     clears real traffic while still capping a flood.
	guestMediaStateBucketBurst  = 5.0
	guestMediaStateBucketRefill = 3 * time.Second
	guestTelemetryBucketBurst   = 3.0
	guestTelemetryBucketRefill  = 5 * time.Second

	// 2026-07-10 keyframe-spiral incident: the MEMBER request_participant_tracks
	// path had NO rate limit — 193 repair messages in ~4 minutes, each running
	// sendParticipantTrackSnapshots plus a GLOBAL signal walk ending in a
	// keyframe dispatch to every publisher, which sustained the egress-melting
	// spiral. Members get a generous bucket (they are authenticated and their
	// client's legit repair cadence is ~1 per 6s): the burst absorbs a
	// just-joined member's first snapshot request plus a reconnect flurry, and
	// the refill still lets steady-state self-heal through while capping a
	// repair storm. Keyed by the per-socket participant session id; dropped in
	// the session cleanup seam.
	memberMediaRepairBucketBurst  = 4.0
	memberMediaRepairBucketRefill = 5 * time.Second

	// card-003 W4 ICE-restart hardening: restart_ice was the last media-inbound
	// event left unbucketed after the keyframe-spiral damping wave — each one
	// forces a full ICERestart renegotiation plus a dispatchKeyFrame walk, so a
	// socket-line-rate flood re-melts the room the same way the repair storm
	// did. The bucket is shared by members and guests (one pair of consts wired
	// through allowMemberIceRestart + allowGuestIceRestart).
	//
	// Sizing MUST clear the client's OWN bounded recovery ladder or a member
	// that would have healed on a throttled rung is ejected instead. That
	// ladder (index.html: iceRestartThrottleMs 3500, maxIceRestartAttempts 5,
	// backoff [0,1,2,4,8]s, recursive re-arm) fires restart_ice at
	// t ≈ 0 / 3.5 / 7 / 11 / 19s — the 5th rung landing ~1s before the 20s
	// connectionRecovery eject. The budgeted stale-tile ladder (2/60s) draws
	// from this SAME per-session bucket, so a stale-tile restart can spend a
	// token just before an outage. Burst 4 / refill 1 per 5s (the
	// memberMediaRepair refill class) admits all five rungs with headroom:
	// pre-charge tokens run 4, 3.7, 3.4, 3.2, 3.8 cold, and identically after a
	// pre-spend at t=-5s because the 5s gap refills a full token and fully
	// repays it — always ≥ 1.
	//
	// The OLD burst 2 / refill 1 per 15s was arithmetically self-refuting: five
	// rungs need five tokens but only ~3.27 are ever available (burst 2 + ~1.27
	// refilled by t=19s), so rungs 3 (0.467) and 4 (0.733) were SILENTLY denied
	// and only the t≈19s rung slipped through — ~1s before the eject — ejecting
	// members who had already healed. A genuine flood at socket line rate
	// (100s/sec) is still capped to ~burst + 12/min.
	iceRestartBucketBurst  = 4.0
	iceRestartBucketRefill = 5 * time.Second

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
	// participantSessionLiveness is the endpoint-scoped proof-of-life plane:
	// participant name -> current session id -> last app-level frame. The
	// account-level participants stamp remains the aggregate/legacy fallback,
	// but the sweeper uses these stamps to reap a frozen phone without evicting
	// the same account's healthy laptop.
	participantSessionLiveness map[string]map[string]time.Time
	// participantAdmissionLeases are pointer-stable, per-socket authority
	// tokens. Presence changes mint/retire them while app.mu is held; media
	// callbacks retain the pointer and read its atomic current bit without
	// taking app.mu on every RTP packet.
	participantAdmissionLeases map[string]*participantAdmissionLease
	participantMedia           map[string]participantMediaState
	// participantEndpointMedia is the authoritative per-device media plane:
	// participant name -> stable endpoint id -> current media state. The
	// participantMedia map remains the backwards-compatible account projection
	// consumed by older web clients.
	participantEndpointMedia map[string]map[string]participantMediaState
	// guestSeats maps a guest session key to the room-unique display name the
	// server minted for it ("Guest Sam", "Guest Sam 2"). Seats are per guest
	// SESSION: a second socket under the same session shares the seat.
	guestSeats map[string]string

	// per-room transcript recording toggle.
	recordingEnabled   bool
	recordingUpdatedAt time.Time
	recordingUpdatedBy string
	// recordingEpoch is a hard capture boundary. Every on/off transition bumps
	// it so provider callbacks from audio buffered before the transition cannot
	// persist into the next recording interval.
	recordingEpoch uint64

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
	realtime *roomRealtimeBundle
	// Scout is a room participant only while this sitting-scoped invitation is
	// current. Transcription owns a separate lane and never consults these
	// fields, so dismissing or degrading Scout cannot interrupt capture.
	scoutInvited          bool
	scoutInvitationID     string
	scoutInvitedAt        time.Time
	scoutInvitedBy        string
	scoutConsentFences    []ConsentFence
	scoutRuntimeStatus    RoomScoutStatus
	scoutVoiceState       string
	scoutLastStatusReason string
	// mediaActor is the Pion control-plane owner for exactly this sitting.
	// Lifecycle code detaches this exact pointer, so an old teardown can never
	// close a successor sitting's actor in the package registry.
	mediaActor     *roomMediaActor
	mediaGen       uint64
	mediaSittingID string
	capTimer       *time.Timer

	// §6.5 per-guest-session token buckets. chatBuckets caps room chat;
	// mediaStateBuckets and telemetryBuckets cap the guest-reachable media
	// inbound events (2026-07-10 incident hardening). All three share the plain
	// guestChatBucket shape.
	chatBuckets       map[string]*guestChatBucket
	mediaStateBuckets map[string]*guestChatBucket
	telemetryBuckets  map[string]*guestChatBucket
	// memberRepairBuckets caps MEMBER request_participant_tracks per
	// participant session (2026-07-10 keyframe-spiral incident). Same bucket
	// shape, keyed by the per-socket participant session id instead of a guest
	// session key.
	memberRepairBuckets map[string]*guestChatBucket
	// memberIceRestartBuckets / guestIceRestartBuckets cap restart_ice per
	// principal (card-003 W4). The member bucket keys on the per-socket
	// participant session id and is released in the same seams as
	// memberRepairBuckets (session cleanup + liveness reap); the guest bucket
	// keys on the guest session key and rides the guest seat like the other
	// guest buckets.
	memberIceRestartBuckets map[string]*guestChatBucket
	guestIceRestartBuckets  map[string]*guestChatBucket
}

// participantAdmissionLease binds every post-admission side effect to the
// exact socket session that won the app.mu admission transaction. The atomic
// bit is the media hot-path fence. gate serializes the one-time registry/grant
// commit against retirement: once the post-app.mu drain returns, a delayed
// pre-transfer handler cannot install itself or send access_granted.
type participantAdmissionLease struct {
	current atomic.Bool
	gate    sync.Mutex

	roomID    string
	name      string
	sessionID string
}

type participantSessionRetirement struct {
	roomID    string
	name      string
	sessionID string
	message   string
	lease     *participantAdmissionLease
}

type participantAdmissionResult struct {
	name          string
	firstEndpoint bool
	lease         *participantAdmissionLease
	retired       []participantSessionRetirement
}

func newParticipantAdmissionLease(roomID, name, sessionID string) *participantAdmissionLease {
	lease := &participantAdmissionLease{
		roomID:    normalizeRoomID(roomID),
		name:      canonicalRoomParticipantName(name),
		sessionID: sessionID,
	}
	lease.current.Store(true)
	return lease
}

func (lease *participantAdmissionLease) isCurrent() bool {
	return lease != nil && lease.current.Load()
}

func (lease *participantAdmissionLease) retire() {
	if lease == nil {
		return
	}
	lease.current.Store(false)
}

func (lease *participantAdmissionLease) drain() {
	if lease == nil {
		return
	}
	lease.gate.Lock()
	lease.gate.Unlock()
}

// whileCurrent runs a bounded, non-app.mu operation while the lease is still
// authoritative. Admission uses it for registry installation + access_granted;
// OnTrack uses it once for its initial enqueue/broadcast. RTP forwarding uses
// isCurrent instead, keeping the packet path lock-free.
func (lease *participantAdmissionLease) whileCurrent(fn func()) bool {
	if lease == nil {
		return false
	}
	lease.gate.Lock()
	defer lease.gate.Unlock()
	if !lease.current.Load() {
		return false
	}
	fn()
	return true
}

// guestChatBucket is a plain token bucket (tokens + last-refill stamp) reused
// by every §6.5 per-guest-session limit, not just chat.
type guestChatBucket struct {
	tokens float64
	last   time.Time
}

func newRoomLiveState(roomID string, now time.Time) *roomLiveState {
	return &roomLiveState{
		id:                         normalizeRoomID(roomID),
		participants:               map[string]time.Time{},
		participantCounts:          map[string]int{},
		participantEndpoints:       map[string]map[string]string{},
		participantSessionLiveness: map[string]map[string]time.Time{},
		participantAdmissionLeases: map[string]*participantAdmissionLease{},
		participantMedia:           map[string]participantMediaState{},
		participantEndpointMedia:   map[string]map[string]participantMediaState{},
		guestSeats:                 map[string]string{},
		recordingEnabled:           true,
		recordingUpdatedAt:         now,
		recordingEpoch:             1,
		chatBuckets:                map[string]*guestChatBucket{},
		mediaStateBuckets:          map[string]*guestChatBucket{},
		telemetryBuckets:           map[string]*guestChatBucket{},
		memberRepairBuckets:        map[string]*guestChatBucket{},
		memberIceRestartBuckets:    map[string]*guestChatBucket{},
		guestIceRestartBuckets:     map[string]*guestChatBucket{},
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
	delete(state.mediaStateBuckets, sessionKey)
	delete(state.telemetryBuckets, sessionKey)
	delete(state.guestIceRestartBuckets, sessionKey)
}

/* ---------- §6.5 guest token buckets ---------- */

// chargeGuestBucket applies elapsed-time refill (capped at burst) then charges
// one token, reporting whether the action may proceed. It is the single
// refill/charge rule behind every §6.5 guest bucket so the limits can't drift.
func chargeGuestBucket(bucket *guestChatBucket, burst float64, refill time.Duration, now time.Time) bool {
	if elapsed := now.Sub(bucket.last); elapsed > 0 {
		bucket.tokens += float64(elapsed) / float64(refill)
		if bucket.tokens > burst {
			bucket.tokens = burst
		}
	}
	bucket.last = now
	if bucket.tokens < 1 {
		return false
	}
	bucket.tokens--
	return true
}

// allowGuestBucket charges the session's bucket in the map returned by pick
// (created full on first use) and reports whether the action proceeds.
// Callers hold no lock; app.mu is taken here. Despite the name it is the
// shared machinery for every §6.5 per-session bucket: the guest buckets key
// on the guest session key, and the member repair bucket (2026-07-10
// keyframe-spiral incident) keys on the per-socket participant session id.
func (app *kanbanBoardApp) allowGuestBucket(roomID string, sessionKey string, pick func(*roomLiveState) map[string]*guestChatBucket, burst float64, refill time.Duration, now time.Time) bool {
	app.mu.Lock()
	defer app.mu.Unlock()
	buckets := pick(app.roomLiveLocked(roomID))
	bucket := buckets[sessionKey]
	if bucket == nil {
		bucket = &guestChatBucket{tokens: burst, last: now}
		buckets[sessionKey] = bucket
	}
	return chargeGuestBucket(bucket, burst, refill, now)
}

// allowGuestRoomChat charges the §6.5 chat bucket (burst 5, refill 1 per 3s)
// and reports whether the message may proceed.
func (app *kanbanBoardApp) allowGuestRoomChat(roomID string, sessionKey string, now time.Time) bool {
	return app.allowGuestBucket(roomID, sessionKey,
		func(s *roomLiveState) map[string]*guestChatBucket { return s.chatBuckets },
		guestChatBucketBurst, guestChatBucketRefill, now)
}

// allowGuestMediaStateEvent charges the state/repair bucket shared by
// participant_media_state and request_participant_tracks — the two inbound
// events that fan out a room-wide roster broadcast / a global peer-sync walk.
func (app *kanbanBoardApp) allowGuestMediaStateEvent(roomID string, sessionKey string, now time.Time) bool {
	return app.allowGuestBucket(roomID, sessionKey,
		func(s *roomLiveState) map[string]*guestChatBucket { return s.mediaStateBuckets },
		guestMediaStateBucketBurst, guestMediaStateBucketRefill, now)
}

// allowGuestTelemetryEvent charges the telemetry bucket shared by media_quality
// and media_error — otherwise unbounded log writes.
func (app *kanbanBoardApp) allowGuestTelemetryEvent(roomID string, sessionKey string, now time.Time) bool {
	return app.allowGuestBucket(roomID, sessionKey,
		func(s *roomLiveState) map[string]*guestChatBucket { return s.telemetryBuckets },
		guestTelemetryBucketBurst, guestTelemetryBucketRefill, now)
}

// allowMemberMediaRepair charges a MEMBER's request_participant_tracks bucket
// (burst 4, refill 1 per 5s — 2026-07-10 keyframe-spiral incident). The burst
// starts full, so a just-joined member's first snapshot request always
// succeeds; only a sustained repair storm is dropped.
func (app *kanbanBoardApp) allowMemberMediaRepair(roomID string, participantSessionID string, now time.Time) bool {
	return app.allowGuestBucket(roomID, participantSessionID,
		func(s *roomLiveState) map[string]*guestChatBucket { return s.memberRepairBuckets },
		memberMediaRepairBucketBurst, memberMediaRepairBucketRefill, now)
}

// dropMemberMediaRepairBucket releases a member socket's repair bucket when
// its session is cleaned up — the key is unique per socket, so without this
// the map grows one entry per connection for the life of the room.
func (app *kanbanBoardApp) dropMemberMediaRepairBucket(roomID string, participantSessionID string) {
	app.mu.Lock()
	defer app.mu.Unlock()
	delete(app.roomLiveLocked(roomID).memberRepairBuckets, participantSessionID)
}

// allowMemberIceRestart charges a MEMBER's restart_ice bucket (burst 4, refill
// 1 per 5s — card-003 W4). restart_ice renegotiates the whole transport and
// walks a keyframe dispatch, but the sizing still admits every rung of the
// client's bounded 5-attempt restart ladder (t ≈ 0/3.5/7/11/19s) even when a
// budgeted stale-tile restart spent a token just before the outage — see the
// iceRestartBucket const comment for the rung-by-rung math.
func (app *kanbanBoardApp) allowMemberIceRestart(roomID string, participantSessionID string, now time.Time) bool {
	return app.allowGuestBucket(roomID, participantSessionID,
		func(s *roomLiveState) map[string]*guestChatBucket { return s.memberIceRestartBuckets },
		iceRestartBucketBurst, iceRestartBucketRefill, now)
}

// allowGuestIceRestart charges a GUEST's restart_ice bucket, keyed on the guest
// session key like the other guest buckets (burst 4, refill 1 per 5s — same
// shared sizing as the member bucket, which clears the client restart ladder).
func (app *kanbanBoardApp) allowGuestIceRestart(roomID string, sessionKey string, now time.Time) bool {
	return app.allowGuestBucket(roomID, sessionKey,
		func(s *roomLiveState) map[string]*guestChatBucket { return s.guestIceRestartBuckets },
		iceRestartBucketBurst, iceRestartBucketRefill, now)
}

// dropMemberIceRestartBucket releases a member socket's restart_ice bucket when
// its session is cleaned up — same per-socket key lifetime as
// dropMemberMediaRepairBucket, or the map grows one entry per connection.
func (app *kanbanBoardApp) dropMemberIceRestartBucket(roomID string, participantSessionID string) {
	app.mu.Lock()
	defer app.mu.Unlock()
	delete(app.roomLiveLocked(roomID).memberIceRestartBuckets, participantSessionID)
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
	// Room-scoped share presence (2026-07-10 incident, defect b): guests
	// already see the sharer through the participants snapshot; without the
	// start/stop events their tile only recovered via the roster fallback.
	"screen_share_started": true,
	"screen_share_stopped": true,
	"offer":                true,
	"answer":               true,
	"candidate":            true,
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
	// Media roster + self-heal parity (2026-07-10 incident, defect a). All
	// four handlers are participantAccepted-gated (unadmitted sends get
	// access_denied or are ignored) and none touches the PeerConnection, so
	// a pre-admission guest socket cannot panic or mutate room state:
	//  - participant_media_state writes only the sender's OWN roster row
	//    (name is the server-minted seat, never the payload);
	//  - request_participant_tracks replays room-fenced track snapshots the
	//    guest is already entitled to and triggers the same resignal members
	//    get (the frozen-tile self-heal);
	//  - media_quality/media_error are log-only telemetry, no state writes.
	"participant_media_state":    true,
	"request_participant_tracks": true,
	"media_quality":              true,
	"media_error":                true,
}

/* ---------- lazy media lifecycle (§4.4) ---------- */

// roomAudioActivityListener feeds a named room's mixer activity into that
// room's attribution state — the office listener stays kanbanApp itself.
type roomAudioActivityListener struct {
	app        *kanbanBoardApp
	roomID     string
	sittingID  string
	generation uint64
}

func (l *roomAudioActivityListener) NoteAudioActivity(at time.Time, levels []audioActivityLevel) {
	l.app.noteAudioActivityForScope(RoomScoutScope{RoomID: l.roomID, SittingID: l.sittingID, MediaGeneration: l.generation}, at, levels)
}

// roomLaneAudioSink is a room's consent-gated mixer sink. Transcript recording
// controls only the transcription lane; an explicitly invited Scout continues
// to receive the separately authorized model-analysis lane without forcing the
// meeting transcript to be persisted.
type roomLaneAudioSink struct {
	app    *kanbanBoardApp
	roomID string
	lane   ConsentLane
}

func (s *roomLaneAudioSink) WriteMixedPCM(roomPCM []int16) error {
	// Live room capture requires contributor fences; a plain mixer call is not
	// authority and therefore fails closed.
	return nil
}

func (s *roomLaneAudioSink) WriteMixedPCMWithConsent(roomPCM []int16, fences []ConsentFence) error {
	if len(roomPCM) == 0 || pcmIsZero(roomPCM) {
		return nil
	}
	authority := currentConsentLaneAuthority()
	if len(fences) == 0 {
		return nil
	}
	for _, fence := range fences {
		if fence.lane != s.lane || authority.ValidateIngressFence(fence) != nil {
			return nil
		}
	}
	switch s.lane {
	case ConsentLaneTranscription:
		if !s.app.transcriptRecordingActiveInRoom(s.roomID) {
			return nil
		}
		s.app.mu.Lock()
		var transcriptLane *meetingTranscriptionLane
		if normalizeRoomID(s.roomID) == officeRoomID {
			transcriptLane = s.app.transcriptLane
		} else {
			transcriptLane = s.app.roomLiveLocked(s.roomID).lane
		}
		s.app.mu.Unlock()
		if transcriptLane != nil {
			transcriptLane.enqueueWithConsent(roomPCM, fences)
		}
	case ConsentLaneModelAnalysis:
		if normalizeRoomID(s.roomID) == officeRoomID {
			_ = s.app.writeRealtimeMixedPCM(roomPCM)
			return nil
		}
		s.app.mu.Lock()
		realtime := s.app.roomLiveLocked(s.roomID).realtime
		s.app.mu.Unlock()
		// Scout/provider errors are isolated from direct room media.
		realtime.writeMixedPCMWithConsent(roomPCM, fences)
	}
	return nil
}

func (s *roomLaneAudioSink) WriteSourcePCMWithConsent(trackKey string, participantName string, roomPCM []int16, fence ConsentFence) error {
	if s == nil || s.app == nil || s.lane != ConsentLaneTranscription || len(roomPCM) == 0 || pcmIsZero(roomPCM) {
		return nil
	}
	if fence.lane != ConsentLaneTranscription || currentConsentLaneAuthority().ValidateIngressFence(fence) != nil {
		return nil
	}
	s.app.mu.Lock()
	state := s.app.roomLiveLocked(s.roomID)
	if !state.recordingEnabled {
		s.app.mu.Unlock()
		return nil
	}
	epoch := state.recordingEpoch
	var transcriptLane *meetingTranscriptionLane
	if normalizeRoomID(s.roomID) == officeRoomID {
		transcriptLane = s.app.transcriptLane
	} else {
		transcriptLane = state.lane
	}
	s.app.mu.Unlock()
	if transcriptLane != nil {
		transcriptLane.enqueueSourceWithConsent(trackKey, participantName, roomPCM, fence, epoch)
	}
	return nil
}

func (s *roomLaneAudioSink) RemoveSource(trackKey string) {
	if s == nil || s.app == nil || s.lane != ConsentLaneTranscription {
		return
	}
	s.app.mu.Lock()
	var transcriptLane *meetingTranscriptionLane
	if normalizeRoomID(s.roomID) == officeRoomID {
		transcriptLane = s.app.transcriptLane
	} else {
		transcriptLane = s.app.roomLiveLocked(s.roomID).lane
	}
	s.app.mu.Unlock()
	if transcriptLane != nil {
		transcriptLane.removeSource(trackKey)
	}
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
func (app *kanbanBoardApp) ensureRoomMedia(roomID string) uint64 {
	roomID = normalizeRoomID(roomID)
	if app == nil {
		return 0
	}
	if roomID == officeRoomID {
		return app.ensureOfficeMedia()
	}
	sittingID := ""
	if app.memory != nil {
		sittingID = app.memory.currentMeetingID(roomID)
	}

	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	if state.mixer != nil {
		generation := state.mediaGen
		app.mu.Unlock()
		return generation
	}
	state.mediaGen++
	gen := state.mediaGen
	mixer := newAudioMixer()
	mixer.setActivityListener(&roomAudioActivityListener{app: app, roomID: roomID, sittingID: sittingID, generation: gen})
	state.mixer = mixer

	apiKey := app.apiKey
	var lane *meetingTranscriptionLane
	if strings.TrimSpace(apiKey) != "" && transcriptionLaneEnabled() {
		lane = newMeetingTranscriptionSourceManagerForRoomGeneration(app, apiKey, transcriptionLaneModel(), roomID, gen)
		// Started before it becomes observable through state.lane, so a
		// racing teardown can never close() a lane whose run loop (the one
		// that signals done) has not launched yet.
		lane.start()
		state.lane = lane
	}
	authority := currentConsentLaneAuthority()
	mixer.setConsentSink(realtimeMixedAudioSinkKey+":transcription:"+roomID, ConsentLaneTranscription, authority, &roomLaneAudioSink{app: app, roomID: roomID, lane: ConsentLaneTranscription})
	mixer.setConsentSink(realtimeMixedAudioSinkKey+":model:"+roomID, ConsentLaneModelAnalysis, authority, &roomLaneAudioSink{app: app, roomID: roomID, lane: ConsentLaneModelAnalysis})
	guestEnabled := false
	if room, ok := appRoomStore().byID(roomID); ok {
		guestEnabled = room.GuestEnabled
	}
	if guestEnabled {
		app.armGuestTranscriptionCapLocked(state, gen)
	}
	state.mediaActor = actorForRoomGeneration(roomID, gen)
	state.mediaSittingID = sittingID
	app.mu.Unlock()
	log.Infof("room_media_started room=%s gen=%d lane=%t", roomID, gen, lane != nil)
	return gen
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
	realtime := state.realtime
	mediaActor := state.mediaActor
	capTimer := state.capTimer
	state.mixer = nil
	state.lane = nil
	state.realtime = nil
	state.scoutInvited = false
	state.scoutInvitationID = ""
	state.scoutInvitedAt = time.Time{}
	state.scoutInvitedBy = ""
	state.scoutConsentFences = nil
	state.scoutRuntimeStatus = RoomScoutClosed
	state.scoutVoiceState = ""
	state.scoutLastStatusReason = ""
	state.mediaActor = nil
	state.mediaSittingID = ""
	state.capTimer = nil
	state.mediaGen++
	gen := state.mediaGen
	closeRoomMediaActorOwned(roomID, mediaActor)
	app.mu.Unlock()

	if capTimer != nil {
		capTimer.Stop()
	}
	if realtime != nil {
		_ = realtime.close()
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
	app.transcriptionStartToken++
	app.transcriptionStarting = false
	app.realtimeStartToken++
	app.realtimeStarting = false
	app.realtimeRestartToken = 0
	mediaActor := state.mediaActor
	state.mediaActor = nil
	state.mediaSittingID = ""
	state.scoutInvited = false
	state.scoutInvitationID = ""
	state.scoutInvitedAt = time.Time{}
	state.scoutInvitedBy = ""
	state.scoutConsentFences = nil
	state.scoutRuntimeStatus = RoomScoutClosed
	state.scoutVoiceState = ""
	state.scoutLastStatusReason = ""
	state.audioActivity = nil
	state.currentSpeechStartedAt = time.Time{}
	state.currentSpeechStoppedAt = time.Time{}
	state.pendingAttributionWindows = nil
	state.activeSpeakerName = ""
	state.activeSpeakerCandidate = ""
	state.activeSpeakerCandidateAt = time.Time{}
	state.activeSpeakerPayload = nil
	state.mediaGen++
	gen := state.mediaGen
	closeRoomMediaActorOwned(officeRoomID, mediaActor)
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

func (app *kanbanBoardApp) roomMediaGeneration(roomID string) uint64 {
	if app == nil {
		return 0
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	return app.roomLiveLocked(roomID).mediaGen
}

func (app *kanbanBoardApp) roomMediaGenerationCurrent(roomID string, generation uint64) bool {
	if app == nil || generation == 0 {
		return false
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)
	return state.mediaGen == generation && state.mediaActor != nil
}

func (app *kanbanBoardApp) roomMediaScopeCurrent(scope RoomScoutScope) bool {
	if app == nil || !scope.valid() {
		return false
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(scope.RoomID)
	return state.mediaGen == scope.MediaGeneration && state.mediaActor != nil && state.mediaSittingID == strings.TrimSpace(scope.SittingID)
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

	app.mu.Lock()
	retired := app.retireParticipantSeatsOutsideRoomLocked(name, joinedRoomID)
	app.mu.Unlock()
	drainParticipantAdmissionRetirements(retired)

	byRoom := map[string]int{}
	for _, retirement := range retired {
		notifySessionReplacedAndClose(retirement.sessionID)
		closeSessionMedia(retirement.sessionID)
		unregisterParticipantSession(retirement.name, retirement.sessionID)
		byRoom[retirement.roomID]++
	}
	for roomID, count := range byRoom {
		log.Infof("room_seat_evicted participant=%s from=%s joined=%s sessions=%d", name, roomID, joinedRoomID, count)
		broadcastRoomKanbanEvent(roomID, "participant_left", map[string]any{
			"name":   name,
			"roomId": roomID,
		})
		broadcastRoomKanbanEvent(roomID, "participants", app.roomSnapshotForRoom(roomID))
		app.noteMeetingOccupancy(roomID)
	}
	if len(retired) > 0 {
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

// closeSessionMedia tears down every transport a session still holds — its
// websockets AND its *webrtc.PeerConnections — scanning both the media pool and
// the admitted-only index under listLock, deduped exactly like
// closeSessionSockets / closeParticipantConnections. The liveness sweep needs
// this because unregisterParticipantSession only clears map/slice bookkeeping;
// it never closes the peer connection. In the sweep's own failure modes (a
// wedged read loop, a deadline setup that failed, an onclose defer that never
// ran) the PeerConnection, its ICE/DTLS/SRTP transports, and the OnTrack /
// forwardSubscriberRTCP goroutines pumping it would otherwise leak. pc.Close()
// synchronously releases those transports and errors every ReadRTP/ReadRTCP, so
// the media pumps exit and run their defers (publisherSilence.forget, mixer
// removeTrack). Like closeSessionSockets it writes nothing to the sockets — the
// reap's participant_left broadcast already told the room why.
func closeSessionMedia(sessionID string) {
	var writers []*threadSafeWriter
	var peerConns []*webrtc.PeerConnection
	seenWriters := map[*threadSafeWriter]bool{}
	seenPeers := map[*webrtc.PeerConnection]bool{}
	listLock.RLock()
	for i := range peerConnections {
		if peerConnections[i].sessionID != sessionID {
			continue
		}
		if peerConnections[i].websocket != nil && !seenWriters[peerConnections[i].websocket] {
			seenWriters[peerConnections[i].websocket] = true
			writers = append(writers, peerConnections[i].websocket)
		}
		if peerConnections[i].peerConnection != nil && !seenPeers[peerConnections[i].peerConnection] {
			seenPeers[peerConnections[i].peerConnection] = true
			peerConns = append(peerConns, peerConnections[i].peerConnection)
		}
	}
	for _, state := range activeParticipantConnections {
		if state.sessionID != sessionID {
			continue
		}
		if state.websocket != nil && !seenWriters[state.websocket] {
			seenWriters[state.websocket] = true
			writers = append(writers, state.websocket)
		}
		if state.peerConnection != nil && !seenPeers[state.peerConnection] {
			seenPeers[state.peerConnection] = true
			peerConns = append(peerConns, state.peerConnection)
		}
	}
	listLock.RUnlock()

	for _, writer := range writers {
		if err := writer.Close(); err != nil {
			log.Errorf("Failed to close reaped session websocket session=%s: %v", sessionID, err)
		}
	}
	for _, peerConnection := range peerConns {
		if err := peerConnection.Close(); err != nil {
			log.Errorf("Failed to close reaped session PeerConnection session=%s: %v", sessionID, err)
		}
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
		leases     []*participantAdmissionLease
	}
	var seats []closedSeat
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	for name := range state.participantCounts {
		sessionIDs := make([]string, 0, len(state.participantEndpoints[name]))
		retiredLeases := make([]*participantAdmissionLease, 0, len(state.participantEndpoints[name]))
		seenSessions := map[string]bool{}
		for _, sessionID := range state.participantEndpoints[name] {
			if sessionID == "" || seenSessions[sessionID] {
				continue
			}
			seenSessions[sessionID] = true
			sessionIDs = append(sessionIDs, sessionID)
			retiredLeases = append(retiredLeases, retireParticipantAdmissionLeaseLocked(state, sessionID))
		}
		for sessionID, lease := range state.participantAdmissionLeases {
			if lease == nil || !sameParticipantName(lease.name, name) || seenSessions[sessionID] {
				continue
			}
			seenSessions[sessionID] = true
			sessionIDs = append(sessionIDs, sessionID)
			retiredLeases = append(retiredLeases, retireParticipantAdmissionLeaseLocked(state, sessionID))
		}
		delete(state.participants, name)
		delete(state.participantCounts, name)
		delete(state.participantEndpoints, name)
		delete(state.participantSessionLiveness, name)
		delete(state.participantMedia, name)
		delete(state.participantEndpointMedia, name)
		if isGuestDisplayName(name) {
			for sessionKey, display := range state.guestSeats {
				if strings.EqualFold(display, name) {
					delete(state.guestSeats, sessionKey)
					delete(state.chatBuckets, sessionKey)
					delete(state.mediaStateBuckets, sessionKey)
					delete(state.telemetryBuckets, sessionKey)
					delete(state.guestIceRestartBuckets, sessionKey)
				}
			}
		}
		seats = append(seats, closedSeat{name: name, sessionIDs: sessionIDs, leases: retiredLeases})
	}
	app.mu.Unlock()
	app.revokeMeetingSpecialistParticipantAuthority(roomID, "participant_authority_changed")

	for _, seat := range seats {
		drainParticipantAdmissionLeases(seat.leases)
		for _, sessionID := range seat.sessionIDs {
			closeSessionMedia(sessionID)
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
				if app.meetingSpecialists != nil {
					app.meetingSpecialists.CloseScope(roomID, closed.ID, "room_closed")
				}
				app.flushDeferredNotifications("meeting_end")
				app.flushAmbientAgentsForClose("room-archive", roomID, closed.ListenOnly)
				app.flushRoomFollowThroughForMeeting(roomID, closed.ID, "room_archive")
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
