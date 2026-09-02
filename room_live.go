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
	"encoding/json"
	"errors"
	"os"
	"sort"
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

	// room_hand had no limit at all (guests AND members): every frame ran a
	// full roster snapshot under app.mu plus two room-wide broadcasts, and a
	// repeated raise re-broadcast an unchanged state. An unchanged hand is now
	// a true no-op (sender-only echo, no snapshot, no fan-out) and every
	// session — guest or member — spends this small per-socket bucket first.
	// Burst 4 covers a nervous raise/lower/raise; refill 1 per 2s clears any
	// human cadence.
	roomHandBucketBurst  = 4.0
	roomHandBucketRefill = 2 * time.Second

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
	// recordingStatusRevision orders every user-visible transcription status
	// edge, including provider connect/disconnect refinements that do not change
	// the hard capture epoch. It is guarded by app.mu and never reused.
	recordingStatusRevision uint64

	// speaker attribution + active speaker, fed by THIS room's mixer activity
	// listener (roomAudioActivityListener).
	audioActivity             []participantAudioFrame
	audioActivityStart        int
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
	// scoutMode is the seat kind of the current invitation: "voice" (the
	// qualified realtime lane) or "text" (a chat-only seat answering @Scout
	// through the server-owned room-chat path, no provider audio). Empty when
	// Scout is not invited.
	scoutMode string
	// Wave 6 in-call state. handRaised keys raised hands by participant name
	// (first raise wins the ordering; lowered explicitly or on leave via the
	// roster projection). reactionStamps rate-limits room_reaction per
	// participant. hostMutedAt records a server-enforced host mute. locked is
	// the host lock: new names are refused admission while current
	// participants stay; an empty room clears it.
	handRaised     map[string]time.Time
	reactionStamps map[string]time.Time
	hostMutedAt    map[string]time.Time
	locked         bool
	lockedBy       string
	lockedAt       time.Time
	// Host "remove" ejections for the current sitting. Closing the target's
	// sockets alone let the same guest link (or the same session) re-seat
	// itself on reload, so the ejection is remembered three ways — the guest
	// session key, the normalized display name, and the exact participant
	// session ids that were closed — and honoured at admission until the
	// sitting's in-call state resets (media teardown / room close).
	ejectedNames               map[string]time.Time
	ejectedGuestSessions       map[string]time.Time
	ejectedParticipantSessions map[string]time.Time
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
	// handBuckets caps room_hand per participant session (guests and members
	// alike — the key is the per-socket participant session id) and is
	// released in the socket cleanup seam like the member buckets.
	handBuckets map[string]*guestChatBucket
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
	name           string
	firstEndpoint  bool
	lease          *participantAdmissionLease
	retired        []participantSessionRetirement
	meeting        meetingRecord
	meetingChanged bool
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
		handRaised:                 map[string]time.Time{},
		reactionStamps:             map[string]time.Time{},
		hostMutedAt:                map[string]time.Time{},
		recordingEnabled:           true,
		recordingUpdatedAt:         now,
		recordingEpoch:             1,
		recordingStatusRevision:    1,
		chatBuckets:                map[string]*guestChatBucket{},
		mediaStateBuckets:          map[string]*guestChatBucket{},
		telemetryBuckets:           map[string]*guestChatBucket{},
		memberRepairBuckets:        map[string]*guestChatBucket{},
		memberIceRestartBuckets:    map[string]*guestChatBucket{},
		guestIceRestartBuckets:     map[string]*guestChatBucket{},
		handBuckets:                map[string]*guestChatBucket{},
		ejectedNames:               map[string]time.Time{},
		ejectedGuestSessions:       map[string]time.Time{},
		ejectedParticipantSessions: map[string]time.Time{},
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
// participant_media_state, request_participant_tracks and screen_share_started
// — the inbound events that fan out a room-wide roster broadcast / a global
// peer-sync walk. Teardown is never charged: screen_share_stopped and a
// participant_media_state that clears an active share always apply, or a
// rate-limited guest's dead share track stays registered and un-paused
// (frozen last frame for receivers, roster still "sharing", silence watchdog
// repairing a track that will never come back).
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

// allowRoomHandEvent charges the room_hand bucket (burst 4, refill 1 per 2s)
// for ANY seated socket — guest or member — keyed on the per-socket
// participant session id.
func (app *kanbanBoardApp) allowRoomHandEvent(roomID string, participantSessionID string, now time.Time) bool {
	return app.allowGuestBucket(roomID, participantSessionID,
		func(s *roomLiveState) map[string]*guestChatBucket { return s.handBuckets },
		roomHandBucketBurst, roomHandBucketRefill, now)
}

// dropRoomHandBucket releases a socket's room_hand bucket in the session
// cleanup seam (same per-socket key lifetime as the member buckets).
func (app *kanbanBoardApp) dropRoomHandBucket(roomID string, participantSessionID string) {
	app.mu.Lock()
	defer app.mu.Unlock()
	delete(app.roomLiveLocked(roomID).handBuckets, participantSessionID)
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
	// Wave 6 in-call: reactions and raised hands are room-wide ephemera every
	// seat sees; host controls must reach the guest they target (mute request,
	// removal notice) and the room-wide lock/mute/remove announcements carry
	// no member-only data.
	"room_reaction":            true,
	"room_hand":                true,
	"room_moderate":            true,
	"room_moderate_request":    true,
	"room_participant_removed": true,
	"offer":                    true,
	"answer":                   true,
	"candidate":                true,
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
	// Wave 6 in-call: a guest may react and raise a hand (both allowlisted,
	// rate-limited, name from the server-minted seat). room_moderate is
	// deliberately absent — host controls never come from a guest socket.
	"room_reaction": true,
	"room_hand":     true,
	// Wave 6 D4: guests publish a share on the same second m-line pair as
	// members; both handlers touch only the sender's own roster row and
	// its own forwarded tracks.
	"screen_share_started": true,
	"screen_share_stopped": true,
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
	state.scoutMode = ""
	resetRoomInCallStateLocked(state)
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
	state.scoutMode = ""
	resetRoomInCallStateLocked(state)
	state.audioActivity = nil
	state.audioActivityStart = 0
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

type officeManualArchiveMediaRollover struct {
	oldGeneration uint64
	lane          *meetingTranscriptionLane
	changed       bool
}

// rolloverOfficeMediaAfterManualArchiveLocked publishes the successor media
// identity while the caller holds app.mu. Manual archive invokes it in the same
// critical section that installs the already-durable successor meeting id, so
// no observer can see a new generation without its anchors and meeting record.
func (app *kanbanBoardApp) rolloverOfficeMediaAfterManualArchiveLocked(previousSittingID, successorSittingID string) officeManualArchiveMediaRollover {
	if app == nil {
		return officeManualArchiveMediaRollover{}
	}
	previousSittingID = strings.TrimSpace(previousSittingID)
	successorSittingID = strings.TrimSpace(successorSittingID)
	if previousSittingID == "" || previousSittingID == successorSittingID {
		return officeManualArchiveMediaRollover{}
	}
	state := app.roomLiveLocked(officeRoomID)
	if strings.TrimSpace(state.mediaSittingID) != previousSittingID {
		return officeManualArchiveMediaRollover{}
	}
	oldGeneration := state.mediaGen
	lane := app.transcriptLane
	app.transcriptLane = nil
	app.transcriptionStartToken++
	app.transcriptionStarting = false
	mediaActor := state.mediaActor
	state.mediaActor = nil
	state.mediaSittingID = successorSittingID
	state.pendingAttributionWindows = nil
	state.currentSpeechStartedAt = time.Time{}
	state.currentSpeechStoppedAt = time.Time{}
	state.activeSpeakerName = ""
	state.activeSpeakerCandidate = ""
	state.activeSpeakerCandidateAt = time.Time{}
	state.activeSpeakerPayload = nil
	state.mediaGen++
	if state.mediaGen == 0 {
		state.mediaGen++
	}
	closeRoomMediaActorOwned(officeRoomID, mediaActor)
	app.clearOfficeScoutRequesterBindingsLocked()
	return officeManualArchiveMediaRollover{oldGeneration: oldGeneration, lane: lane, changed: true}
}

// finishOfficeMediaAfterManualArchive performs potentially blocking transport
// teardown after the identity/generation transition and its lifecycle fence
// are released. Any stale callback is still rejected by the exact
// sitting/generation checks, while socket/provider latency cannot stall current
// successor input.
func (app *kanbanBoardApp) finishOfficeMediaAfterManualArchive(rollover officeManualArchiveMediaRollover) {
	if app == nil || !rollover.changed {
		return
	}
	app.teardownRealtimePeerForIdle()
	if rollover.lane != nil {
		rollover.lane.close()
	}
	closeRoomGenerationConnectionsForRestart(officeRoomID, rollover.oldGeneration)
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

const (
	roomArchiveCloseRetryBase = time.Second
	roomArchiveCloseRetryMax  = 30 * time.Second
)

func (app *kanbanBoardApp) scheduleRoomArchiveCloseRetry(roomID string) {
	if app == nil {
		return
	}
	roomID = normalizeRoomID(roomID)
	app.roomArchiveCloseRetryMu.Lock()
	if app.roomArchiveCloseRetryTimers == nil {
		app.roomArchiveCloseRetryTimers = map[string]*time.Timer{}
	}
	if app.roomArchiveCloseRetryAttempts == nil {
		app.roomArchiveCloseRetryAttempts = map[string]int{}
	}
	app.roomArchiveCloseRetryAttempts[roomID]++
	delay := roomArchiveCloseRetryBase
	for attempt := 1; attempt < app.roomArchiveCloseRetryAttempts[roomID] && delay < roomArchiveCloseRetryMax; attempt++ {
		delay *= 2
		if delay > roomArchiveCloseRetryMax {
			delay = roomArchiveCloseRetryMax
		}
	}
	if existing := app.roomArchiveCloseRetryTimers[roomID]; existing != nil {
		existing.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(delay, func() {
		app.roomArchiveCloseRetryMu.Lock()
		if app.roomArchiveCloseRetryTimers[roomID] != timer {
			app.roomArchiveCloseRetryMu.Unlock()
			return
		}
		delete(app.roomArchiveCloseRetryTimers, roomID)
		app.roomArchiveCloseRetryMu.Unlock()
		app.closeRoomForArchive(roomID)
	})
	app.roomArchiveCloseRetryTimers[roomID] = timer
	app.roomArchiveCloseRetryMu.Unlock()
}

func (app *kanbanBoardApp) cancelRoomArchiveCloseRetry(roomID string) {
	if app == nil {
		return
	}
	roomID = normalizeRoomID(roomID)
	app.roomArchiveCloseRetryMu.Lock()
	if timer := app.roomArchiveCloseRetryTimers[roomID]; timer != nil {
		timer.Stop()
		delete(app.roomArchiveCloseRetryTimers, roomID)
	}
	delete(app.roomArchiveCloseRetryAttempts, roomID)
	app.roomArchiveCloseRetryMu.Unlock()
}

func (app *kanbanBoardApp) stopRoomArchiveCloseRetries() {
	if app == nil {
		return
	}
	app.roomArchiveCloseRetryMu.Lock()
	for roomID, timer := range app.roomArchiveCloseRetryTimers {
		if timer != nil {
			timer.Stop()
		}
		delete(app.roomArchiveCloseRetryTimers, roomID)
	}
	app.roomArchiveCloseRetryAttempts = map[string]int{}
	app.roomArchiveCloseRetryMu.Unlock()
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
	app.meetingLifecycleMu.Lock()
	defer app.meetingLifecycleMu.Unlock()
	// This runs async after the archive response; a restore may have landed in
	// the gap. Restore is an undo — if the room is live again, leave the
	// sitting and its occupants alone.
	if room, ok := appRoomStore().byID(roomID); !ok || !room.Archived {
		app.cancelRoomArchiveCloseRetry(roomID)
		return
	}
	if app.meetings == nil {
		// Without durable record truth we cannot prove teardown is safe. The
		// archived room flag survives restart, and the bounded retry handles a
		// store that becomes available again in this process.
		app.scheduleRoomArchiveCloseRetry(roomID)
		return
	}

	// Close the record before broadcasting or touching any seat/media state.
	// If the write definitely failed, the sitting remains intact and the
	// durable archived flag drives bounded same-process plus boot recovery. If
	// the atomic replacement committed ambiguously, the meeting store reconciles
	// the exact postimage and returns changed=true so this chain continues.
	var closed meetingRecord
	if record, ok := app.meetings.activeRecord(roomID); ok {
		app.beginMeetingArchivePublication(record.ID)
		publicationOpen := true
		defer func() {
			if publicationOpen {
				app.endMeetingArchivePublication(record.ID, false)
			}
		}()
		source := app.meetingFinalizationSource(record.ID)
		var changed bool
		var closeErr error
		closed, changed, closeErr = app.meetings.endMeetingWithFinalization(record.ID, time.Now().UTC(), meetingEndedReasonRoomClosed, "", source)
		if closeErr != nil {
			log.Errorf("Could not durably begin room-archive finalization for %s: %v", record.ID, closeErr)
			app.scheduleRoomArchiveCloseRetry(roomID)
			return
		}
		if !changed && closed.EndedAt == "" {
			app.scheduleRoomArchiveCloseRetry(roomID)
			return
		}
		if app.canonicalReconcileAfterMeetingClosed != nil {
			app.canonicalReconcileAfterMeetingClosed()
		}
		defer func() {
			if publicationOpen {
				app.endMeetingArchivePublication(record.ID, true)
				publicationOpen = false
			}
		}()
	}
	if stillOpen, ok := app.meetings.activeRecord(roomID); ok {
		log.Errorf("Refusing room-archive teardown while meeting %s remains open", stillOpen.ID)
		app.scheduleRoomArchiveCloseRetry(roomID)
		return
	}
	app.cancelRoomArchiveCloseRetry(roomID)

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

	if closed.ID != "" {
		if app.meetingSpecialists != nil {
			app.meetingSpecialists.CloseScope(roomID, closed.ID, "room_closed")
		}
		app.flushDeferredNotifications("meeting_end")
		if app.memory != nil {
			app.memory.rotateMeetingIDIfCurrent(roomID, closed.ID)
		}
		app.broadcastMeetingRecord(closed)
		app.flushRoomFollowThroughForMeeting(roomID, closed.ID, "room_archive")
		app.autoArchiveIdleMeeting(closed)
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

/* ---------- Wave 6 in-call: reactions, raised hands, host controls ---------- */

// roomReactionAllowlist is the exact emoji row the control island offers.
// Anything else is refused server-side so a client cannot fan out arbitrary
// text through the reaction lane.
var roomReactionAllowlist = []string{"👍", "❤️", "😂", "😮", "👏", "🎉", "🔥", "🤔"}

// roomReactionMinInterval is the per-participant reaction rate limit.
const roomReactionMinInterval = time.Second

// roomModerateMuteGrace is how long a mute request waits for the target
// client to comply (report micMuted) before the server drops its audio
// forwarding. Tests shorten it.
var roomModerateMuteGrace = 3 * time.Second

var (
	errRoomReactionUnknown     = errors.New("that reaction is not available")
	errRoomReactionTooFast     = errors.New("one reaction per second")
	errRoomParticipantAbsent   = errors.New("that participant is not in the room")
	errRoomModerationInvalid   = errors.New("choose mute, remove, lock, or unlock")
	errRoomModerationSelf      = errors.New("you cannot moderate yourself")
	errRoomModerationForbidden = errors.New("only the room owner can moderate this room")
	// errRoomParticipantRemoved answers a re-admission attempt by a seat the
	// host ejected during the current sitting (guest session key, display
	// name, or the exact closed participant session).
	errRoomParticipantRemoved = errors.New("you were removed from this meeting by the host")
)

func roomReactionAllowed(emoji string) (string, bool) {
	emoji = strings.TrimSpace(emoji)
	for _, candidate := range roomReactionAllowlist {
		if candidate == emoji {
			return candidate, true
		}
	}
	return "", false
}

// resetRoomInCallStateLocked clears the sitting-scoped in-call state when a
// room's media is torn down. Callers hold app.mu.
func resetRoomInCallStateLocked(state *roomLiveState) {
	if state == nil {
		return
	}
	state.handRaised = map[string]time.Time{}
	state.reactionStamps = map[string]time.Time{}
	state.hostMutedAt = map[string]time.Time{}
	state.locked = false
	state.lockedBy = ""
	state.lockedAt = time.Time{}
	// Ejections last exactly one sitting: the next sitting starts clean.
	state.ejectedNames = map[string]time.Time{}
	state.ejectedGuestSessions = map[string]time.Time{}
	state.ejectedParticipantSessions = map[string]time.Time{}
}

// recordRoomReaction validates the emoji against the allowlist, charges the
// per-participant 1/s limit, and shapes the room_reaction fan-out payload.
// Identity comes from the admitted seat (name + participant session), never
// the payload.
func (app *kanbanBoardApp) recordRoomReaction(roomID, name, sessionID, emoji string, now time.Time) (map[string]any, error) {
	emoji, ok := roomReactionAllowed(emoji)
	if !ok {
		return nil, errRoomReactionUnknown
	}
	name = canonicalRoomParticipantName(name)
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)
	if name == "" || state.participantCounts[name] <= 0 {
		return nil, errRoomParticipantAbsent
	}
	if state.reactionStamps == nil {
		state.reactionStamps = map[string]time.Time{}
	}
	if last, seen := state.reactionStamps[name]; seen && now.Sub(last) < roomReactionMinInterval {
		return nil, errRoomReactionTooFast
	}
	state.reactionStamps[name] = now
	return map[string]any{
		"roomId":        state.id,
		"participantId": sessionID,
		"name":          name,
		"emoji":         emoji,
		"at":            now.UTC().Format(time.RFC3339Nano),
	}, nil
}

// setRoomHandRaised raises or lowers the participant's hand. A raised hand
// keeps its original stamp so the roster ordering is stable while the hand
// stays up. Returns the room_hand payload and the refreshed roster snapshot.
// An UNCHANGED hand (re-raise while up, lower while down) is a true no-op:
// the payload still describes the current state for a sender-only echo, but
// the snapshot is nil — no roster projection is built and callers must not
// fan anything out.
func (app *kanbanBoardApp) setRoomHandRaised(roomID, name, sessionID string, raised bool, now time.Time) (map[string]any, map[string]any, error) {
	name = canonicalRoomParticipantName(name)
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)
	if name == "" || state.participantCounts[name] <= 0 {
		return nil, nil, errRoomParticipantAbsent
	}
	if state.handRaised == nil {
		state.handRaised = map[string]time.Time{}
	}
	at := now.UTC()
	changed := false
	if raised {
		if existing, already := state.handRaised[name]; already {
			at = existing
		} else {
			state.handRaised[name] = at
			changed = true
		}
	} else if _, wasRaised := state.handRaised[name]; wasRaised {
		delete(state.handRaised, name)
		changed = true
	}
	payload := map[string]any{
		"roomId":        state.id,
		"participantId": sessionID,
		"name":          name,
		"raised":        raised,
		"at":            at.Format(time.RFC3339Nano),
	}
	if !changed {
		return payload, nil, nil
	}
	return payload, app.roomSnapshotLockedForRoom(state, configuredMeetingRoomCapacity()), nil
}

// decorateRoomSnapshotLocked adds the Wave 6 in-call fields to the roster
// projection: handRaisedAt (name -> RFC3339Nano), raisedHands (names in raise
// order), hostMutedAt, and locked. It also prunes per-name state for anyone no
// longer present, which is what lowers a hand on leave. Callers hold app.mu.
func (app *kanbanBoardApp) decorateRoomSnapshotLocked(state *roomLiveState, participants []string, snapshot map[string]any) {
	if state == nil || snapshot == nil {
		return
	}
	present := make(map[string]bool, len(participants))
	for _, name := range participants {
		present[name] = true
	}
	type raisedHand struct {
		name string
		at   time.Time
	}
	ordered := make([]raisedHand, 0, len(state.handRaised))
	handRaisedAt := make(map[string]string, len(state.handRaised))
	for name, at := range state.handRaised {
		if !present[name] {
			delete(state.handRaised, name)
			continue
		}
		handRaisedAt[name] = at.UTC().Format(time.RFC3339Nano)
		ordered = append(ordered, raisedHand{name: name, at: at})
	}
	sort.Slice(ordered, func(i, j int) bool {
		if !ordered[i].at.Equal(ordered[j].at) {
			return ordered[i].at.Before(ordered[j].at)
		}
		return ordered[i].name < ordered[j].name
	})
	raisedHands := make([]string, 0, len(ordered))
	for _, hand := range ordered {
		raisedHands = append(raisedHands, hand.name)
	}
	for name := range state.reactionStamps {
		if !present[name] {
			delete(state.reactionStamps, name)
		}
	}
	// hostMutedAt is informational on the wire: clients store it but render
	// nothing from it today; mediaStates[name].MicMuted is the rendered truth.
	hostMutedAt := make(map[string]string, len(state.hostMutedAt))
	for name, at := range state.hostMutedAt {
		if !present[name] {
			delete(state.hostMutedAt, name)
			continue
		}
		hostMutedAt[name] = at.UTC().Format(time.RFC3339Nano)
	}
	snapshot["handRaisedAt"] = handRaisedAt
	snapshot["raisedHands"] = raisedHands
	snapshot["hostMutedAt"] = hostMutedAt
	snapshot["locked"] = state.locked
}

// setRoomLocked flips the host lock and returns the refreshed roster snapshot.
func (app *kanbanBoardApp) setRoomLocked(roomID string, locked bool, by string, now time.Time) map[string]any {
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)
	state.locked = locked
	if locked {
		state.lockedBy = canonicalRoomActorName(by)
		state.lockedAt = now.UTC()
	} else {
		state.lockedBy = ""
		state.lockedAt = time.Time{}
	}
	return app.roomSnapshotLockedForRoom(state, configuredMeetingRoomCapacity())
}

// roomJoinLocked answers the HTTP join surfaces (guest join): a locked room
// with people in it refuses new arrivals.
func (app *kanbanBoardApp) roomJoinLocked(roomID string) bool {
	if app == nil {
		return false
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)
	return state.locked && app.activeParticipantCountInRoomLocked(state) > 0
}

// roomAdmissionLockedLocked is the admission-seam half of the host lock. A
// name already present (another device, a refreshed tab) always passes — the
// lock keeps current participants, it does not evict them. An empty room
// clears the lock so a stale flag can never strand the next sitting's first
// arrival. Callers hold app.mu.
func (app *kanbanBoardApp) roomAdmissionLockedLocked(state *roomLiveState, name string) bool {
	if state == nil || !state.locked {
		return false
	}
	if state.participantCounts[name] > 0 {
		return false
	}
	if app.activeParticipantCountInRoomLocked(state) == 0 {
		state.locked = false
		state.lockedBy = ""
		state.lockedAt = time.Time{}
		return false
	}
	return true
}

type roomModerationRequest struct {
	Action        string `json:"action"`
	ParticipantID string `json:"participantId"`
}

// resolveRoomModerationTarget maps the client-supplied participantId — either
// a participant session id from a room_reaction/room_hand payload or the
// roster name itself — onto a present participant.
func (app *kanbanBoardApp) resolveRoomModerationTarget(roomID, participantID string) (string, error) {
	participantID = strings.TrimSpace(participantID)
	if participantID == "" {
		return "", errRoomParticipantAbsent
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)
	for name, endpoints := range state.participantEndpoints {
		for _, sessionID := range endpoints {
			if sessionID == participantID && state.participantCounts[name] > 0 {
				return name, nil
			}
		}
	}
	if name := canonicalRoomParticipantName(participantID); name != "" && state.participantCounts[name] > 0 {
		return name, nil
	}
	return "", errRoomParticipantAbsent
}

// moderateRoom is the server half of the room_moderate verb. The websocket
// case has already proven the requester is the room owner (roomManagedByUser;
// guests never reach here). It returns the room-wide room_moderate payload.
func (app *kanbanBoardApp) moderateRoom(roomID, actor, raw string) (map[string]any, error) {
	if app == nil {
		return nil, errRoomModerationInvalid
	}
	roomID = normalizeRoomID(roomID)
	actor = canonicalRoomParticipantName(actor)
	var request roomModerationRequest
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &request); err != nil {
			return nil, errRoomModerationInvalid
		}
	}
	action := strings.ToLower(strings.TrimSpace(request.Action))
	now := time.Now().UTC()
	payload := map[string]any{
		"ok":     true,
		"roomId": roomID,
		"action": action,
		"by":     actor,
		"at":     now.Format(time.RFC3339Nano),
	}
	switch action {
	case "lock", "unlock":
		snapshot := app.setRoomLocked(roomID, action == "lock", actor, now)
		payload["locked"] = action == "lock"
		broadcastRoomKanbanEvent(roomID, "participants", snapshot)
		return payload, nil
	case "mute", "remove":
		target, err := app.resolveRoomModerationTarget(roomID, request.ParticipantID)
		if err != nil {
			return nil, err
		}
		if sameParticipantName(target, actor) {
			return nil, errRoomModerationSelf
		}
		payload["name"] = target
		payload["participantId"] = strings.TrimSpace(request.ParticipantID)
		if action == "mute" {
			// Ask first: the target's own client mutes and reports it through
			// participant_media_state. If it has not complied when the grace
			// window closes, the server stops forwarding its audio.
			sendRoomKanbanEventToParticipant(roomID, target, "room_moderate_request", map[string]any{
				"roomId": roomID,
				"action": "mute",
				"by":     actor,
				"at":     now.Format(time.RFC3339Nano),
			})
			// The grace timer is bound to the exact sessions that were asked. A
			// same-name rejoin inside the window is a fresh seat that never saw
			// the request, so enforcement must not touch it.
			generation := app.roomMediaGeneration(roomID)
			askedSessions := app.participantSessionIDsInRoom(roomID, target)
			time.AfterFunc(roomModerateMuteGrace, func() {
				app.enforceRoomMute(roomID, target, generation, actor, askedSessions)
			})
			payload["enforced"] = false
			return payload, nil
		}
		// Remember the ejection for this sitting BEFORE the sockets close, so a
		// reload racing the close cannot re-seat the same guest link/session.
		app.recordRoomEjection(roomID, target, now)
		payload["removed"] = removeRoomParticipantConnections(roomID, target, strings.TrimSpace(request.ParticipantID), actor, now)
		return payload, nil
	default:
		return nil, errRoomModerationInvalid
	}
}

// participantSessionIDsInRoom lists the participant session ids currently
// holding endpoints for one name in one room.
func (app *kanbanBoardApp) participantSessionIDsInRoom(roomID, name string) []string {
	name = canonicalRoomParticipantName(name)
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)
	sessions := make([]string, 0, len(state.participantEndpoints[name]))
	for _, sessionID := range state.participantEndpoints[name] {
		if sessionID != "" {
			sessions = append(sessions, sessionID)
		}
	}
	sort.Strings(sessions)
	return sessions
}

// enforceRoomMute runs when the mute grace window closes. It acts only on the
// sessions that were asked (askedSessions) and still hold the seat: a session
// that complied (reports micMuted) is left alone; a session that left, or a
// same-name rejoin that was never asked, is never touched. For the remaining
// asked sessions the audio tracks are dropped from the forwarding registry
// (video and share stay) and their roster rows are stamped so the room sees an
// honest "muted by host".
func (app *kanbanBoardApp) enforceRoomMute(roomID, name string, generation uint64, actor string, askedSessions []string) {
	if app == nil || len(askedSessions) == 0 {
		return
	}
	roomID = normalizeRoomID(roomID)
	name = canonicalRoomParticipantName(name)
	asked := make(map[string]bool, len(askedSessions))
	for _, sessionID := range askedSessions {
		asked[sessionID] = true
	}
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	if name == "" || state.mediaGen != generation || state.participantCounts[name] <= 0 {
		app.mu.Unlock()
		return
	}
	// Asked sessions that are still seated and have not complied.
	pending := map[string]bool{}
	for endpointID, sessionID := range state.participantEndpoints[name] {
		if !asked[sessionID] {
			continue
		}
		endpointState, known := state.participantEndpointMedia[name][endpointID]
		if !known {
			endpointState = state.participantMedia[name]
		}
		if !endpointState.MicMuted {
			pending[sessionID] = true
		}
	}
	app.mu.Unlock()
	if len(pending) == 0 {
		return
	}

	dropped := dropParticipantAudioTracks(roomID, name, pending)

	app.mu.Lock()
	state = app.roomLiveLocked(roomID)
	if state.mediaGen != generation || state.participantCounts[name] <= 0 {
		app.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	if state.participantEndpointMedia == nil {
		state.participantEndpointMedia = map[string]map[string]participantMediaState{}
	}
	endpointMedia := state.participantEndpointMedia[name]
	if endpointMedia == nil {
		endpointMedia = map[string]participantMediaState{}
		state.participantEndpointMedia[name] = endpointMedia
	}
	stamped := false
	for endpointID, sessionID := range state.participantEndpoints[name] {
		if !pending[sessionID] {
			continue
		}
		current, known := endpointMedia[endpointID]
		if !known {
			current = state.participantMedia[name]
		}
		current.MicMuted = true
		current.UpdatedAt = stamp
		endpointMedia[endpointID] = current
		stamped = true
	}
	if !stamped {
		// Every pending session left between the drop and the stamp.
		app.mu.Unlock()
		return
	}
	state.participantMedia[name] = participantMediaProjectionLocked(state, name, stamp)
	if state.hostMutedAt == nil {
		state.hostMutedAt = map[string]time.Time{}
	}
	state.hostMutedAt[name] = now
	snapshot := app.roomSnapshotLockedForRoom(state, configuredMeetingRoomCapacity())
	app.mu.Unlock()

	if dropped {
		requestRoomMediaCommand(roomID, roomMediaCommandLeave)
	}
	log.Infof("room_moderate_mute_enforced room=%s participant=%s by=%s sessions=%d dropped_audio=%t", roomID, mediaLogPrincipal(name), mediaLogPrincipal(actor), len(pending), dropped)
	broadcastRoomKanbanEvent(roomID, "participants", snapshot)
	broadcastRoomKanbanEvent(roomID, "room_moderate", map[string]any{
		"ok":       true,
		"roomId":   roomID,
		"action":   "mute",
		"name":     name,
		"by":       actor,
		"enforced": true,
		"at":       stamp,
	})
}

// dropParticipantAudioTracks is the audio-only form of
// removeParticipantTracksLocked: it detaches the forwarded audio tracks that
// belong to the given participant sessions in one room and leaves video/share
// (and any session not listed) untouched.
func dropParticipantAudioTracks(roomID, name string, sessions map[string]bool) bool {
	roomID = normalizeRoomID(roomID)
	listLock.Lock()
	defer listLock.Unlock()
	dropped := false
	for trackID, participantName := range trackParticipants {
		if !sameParticipantName(participantName, name) || normalizeRoomID(trackRooms[trackID]) != roomID {
			continue
		}
		if !sessions[trackParticipantSessions[trackID]] {
			continue
		}
		trackLocal := trackLocals[trackID]
		if trackLocal == nil || trackLocal.Kind() != webrtc.RTPCodecTypeAudio {
			continue
		}
		delete(trackLocals, trackID)
		delete(trackParticipants, trackID)
		delete(trackParticipantSessions, trackID)
		delete(trackRooms, trackID)
		delete(trackSourceIDs, trackID)
		delete(trackLayerRIDs, trackID)
		delete(trackLayerGroups, trackID)
		delete(trackMediaOwners, trackID)
		delete(trackSources, trackID)
		delete(trackPaused, trackID)
		subscriberKeyframeThrottle.forget(trackID)
		dropped = true
	}
	return dropped
}

// participantWritersInRoom collects the distinct websocket writers a
// participant currently holds in one room — admitted sockets and media-joined
// sockets alike — so a targeted event reaches every device of that person.
func participantWritersInRoom(roomID, name string) []peerConnectionState {
	roomID = normalizeRoomID(roomID)
	listLock.RLock()
	defer listLock.RUnlock()
	seen := map[*threadSafeWriter]bool{}
	var states []peerConnectionState
	collect := func(state peerConnectionState) {
		if state.websocket == nil || seen[state.websocket] {
			return
		}
		if !sameParticipantName(state.participantName, name) || normalizeRoomID(state.roomID) != roomID {
			return
		}
		seen[state.websocket] = true
		states = append(states, state)
	}
	for _, state := range peerConnections {
		collect(state)
	}
	for _, state := range activeParticipantConnections {
		collect(state)
	}
	return states
}

// sendRoomKanbanEventToParticipant delivers one event to exactly one
// participant's sockets in one room (never the whole room).
func sendRoomKanbanEventToParticipant(roomID, name, event string, data any) int {
	states := participantWritersInRoom(roomID, name)
	if len(states) == 0 {
		return 0
	}
	raw, err := encodeKanbanEvent(event, data)
	if err != nil {
		log.Errorf("Failed to encode %s for %s: %v", event, mediaLogPrincipal(name), err)
		return 0
	}
	writers := make([]*threadSafeWriter, 0, len(states))
	for _, state := range states {
		writers = append(writers, state.websocket)
	}
	_, delivered := deliverKanbanEventAcknowledged(event, writers, raw)
	return delivered
}

// removeRoomParticipantConnections ejects a participant. Ordering is the
// contract: the target's own sockets hear room_participant_removed{self:true}
// first, then the whole room hears room_participant_removed, and only then do
// the target's PeerConnection and websocket close — so the socket's own
// handler cleanup (presence retirement + participant_left, exactly as an
// ordinary leave) can never precede the announcement.
func removeRoomParticipantConnections(roomID, name, participantID, actor string, now time.Time) bool {
	roomID = normalizeRoomID(roomID)
	states := participantWritersInRoom(roomID, name)
	if len(states) == 0 {
		return false
	}
	at := now.UTC().Format(time.RFC3339Nano)
	notice := map[string]any{
		"roomId":        roomID,
		"name":          name,
		"participantId": participantID,
		"by":            actor,
		"at":            at,
		"self":          true,
	}
	for _, state := range states {
		_ = sendKanbanEvent(state.websocket, "room_participant_removed", notice)
	}
	broadcastRoomKanbanEvent(roomID, "room_participant_removed", map[string]any{
		"roomId":        roomID,
		"name":          name,
		"participantId": participantID,
		"by":            actor,
		"at":            at,
	})
	closed := map[*webrtc.PeerConnection]bool{}
	for _, state := range states {
		if state.peerConnection != nil && !closed[state.peerConnection] {
			closed[state.peerConnection] = true
			if err := state.peerConnection.Close(); err != nil {
				log.Errorf("Failed to close removed participant PeerConnection: %v", err)
			}
		}
		_ = state.websocket.Close()
	}
	log.Infof("room_moderate_removed room=%s participant=%s by=%s sockets=%d", normalizeRoomID(roomID), mediaLogPrincipal(name), mediaLogPrincipal(actor), len(states))
	return true
}

// normalizedEjectionName is the display-name key of the ejection record.
func normalizedEjectionName(name string) string {
	return strings.ToLower(canonicalRoomParticipantName(name))
}

// recordRoomEjection remembers a host "remove" for the current sitting: the
// normalized display name, every guest session key seated under that name,
// and every participant session id currently holding the seat. Admission
// honours all three until resetRoomInCallStateLocked clears them.
func (app *kanbanBoardApp) recordRoomEjection(roomID, name string, now time.Time) {
	if app == nil {
		return
	}
	roomID = normalizeRoomID(roomID)
	name = canonicalRoomParticipantName(name)
	if name == "" {
		return
	}
	sessionIDs := map[string]bool{}
	for _, state := range participantWritersInRoom(roomID, name) {
		if state.sessionID != "" {
			sessionIDs[state.sessionID] = true
		}
	}
	at := now.UTC()
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)
	if state.ejectedNames == nil {
		state.ejectedNames = map[string]time.Time{}
	}
	if state.ejectedGuestSessions == nil {
		state.ejectedGuestSessions = map[string]time.Time{}
	}
	if state.ejectedParticipantSessions == nil {
		state.ejectedParticipantSessions = map[string]time.Time{}
	}
	state.ejectedNames[normalizedEjectionName(name)] = at
	for sessionKey, display := range state.guestSeats {
		if sameParticipantName(display, name) {
			state.ejectedGuestSessions[sessionKey] = at
		}
	}
	for _, sessionID := range state.participantEndpoints[name] {
		if sessionID != "" {
			sessionIDs[sessionID] = true
		}
	}
	for sessionID := range sessionIDs {
		state.ejectedParticipantSessions[sessionID] = at
	}
}

// roomEjectionRefusalLocked is the admission half of a host remove. A guest
// is refused when its session key OR its (deduped) display name was ejected
// this sitting; a member is refused only for the exact participant session
// that was closed. Callers hold app.mu. Empty keys never match.
func roomEjectionRefusalLocked(state *roomLiveState, guestSessionKey, name, participantSessionID string) error {
	if state == nil {
		return nil
	}
	if guestSessionKey != "" {
		if _, ejected := state.ejectedGuestSessions[guestSessionKey]; ejected {
			return errRoomParticipantRemoved
		}
		if key := normalizedEjectionName(name); key != "" {
			if _, ejected := state.ejectedNames[key]; ejected {
				return errRoomParticipantRemoved
			}
		}
	}
	if participantSessionID != "" {
		if _, ejected := state.ejectedParticipantSessions[participantSessionID]; ejected {
			return errRoomParticipantRemoved
		}
	}
	return nil
}

// clearHostMuteForNewSessionLocked drops the roster's "muted by host" stamp
// when a NEW session of that name registers. enforceRoomMute is scoped to
// the exact sessions that were asked (a fresh seat was never asked and its
// audio is not dropped), so keeping the stamp would claim a mute that is not
// in force. Callers hold app.mu.
func clearHostMuteForNewSessionLocked(state *roomLiveState, name string) {
	if state == nil || state.hostMutedAt == nil {
		return
	}
	delete(state.hostMutedAt, canonicalRoomParticipantName(name))
}

// participantEndpointScreenSharingInRoom reports whether the exact endpoint
// session currently has an active share on the roster — the check that lets
// a share TEARDOWN bypass the guest state bucket.
func (app *kanbanBoardApp) participantEndpointScreenSharingInRoom(roomID, name, endpointID, sessionID string) bool {
	if app == nil {
		return false
	}
	name = canonicalRoomParticipantName(name)
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)
	if name == "" || state.participantCounts[name] <= 0 {
		return false
	}
	if current, ok := state.participantEndpoints[name][endpointID]; !ok || current != sessionID {
		return false
	}
	if endpointState, known := state.participantEndpointMedia[name][endpointID]; known {
		return endpointState.ScreenSharing
	}
	return state.participantMedia[name].ScreenSharing
}

// seatedMemberAccountEmailsInRoom lists the authenticated account emails
// behind a name's live sockets in one room — the principal that was actually
// admitted, which in production need not equal the roster-derived
// "<name>@…" address. Empty for guests or a seat with no live socket.
func seatedMemberAccountEmailsInRoom(roomID, name string) []string {
	seen := map[string]bool{}
	var emails []string
	for _, state := range participantWritersInRoom(roomID, name) {
		email := normalizeAccountEmail(state.sessionEmail)
		if email == "" || seen[email] {
			continue
		}
		seen[email] = true
		emails = append(emails, email)
	}
	sort.Strings(emails)
	return emails
}

// participantSeatedInRoom reports whether the name currently holds presence
// in the room's live state (never creates the room state).
func (app *kanbanBoardApp) participantSeatedInRoom(roomID, name string) bool {
	if app == nil {
		return false
	}
	name = canonicalRoomParticipantName(name)
	if name == "" {
		return false
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	state, ok := app.roomLive[normalizeRoomID(roomID)]
	return ok && state.participantCounts[name] > 0
}
