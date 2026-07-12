package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/nack"
	"github.com/pion/logging"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
)

// nolint
var (
	addr               = flag.String("addr", ":3000", "http service address")
	codexRunnerWorker  = flag.Bool("codex-runner", false, "run the Codex sidecar queue worker")
	renderRunnerWorker = flag.Bool("render-runner", false, "run the render sidecar queue worker (PDF export)")
	upgrader           = websocket.Upgrader{
		CheckOrigin: websocketOriginAllowed,
	}

	// maxWebsocketMessageBytes caps a single inbound Websocket frame. Signaling
	// payloads (SDP, ICE candidates, board events) are well under this; the cap
	// blocks memory-amplification from oversized frames.
	maxWebsocketMessageBytes int64 = 256 << 10 // 256 KiB
	indexHTML                []byte

	// serverBuildVersion identifies the running build. It is baked into the
	// served index.html and pushed over every websocket admission so a tab
	// holding old JS across a deploy can detect the skew on reconnect and
	// refresh itself (see deploy-refresh flow). Computed once at boot.
	serverBuildVersion string

	// lock for peerConnections and trackLocals
	listLock        sync.RWMutex
	peerConnections []peerConnectionState
	// officeConnections tracks authenticated sockets that opted into office
	// event delivery (the `office` hello) without room admission. Keyed by
	// participantSessionID and guarded by listLock; they receive the
	// signed-in-safe broadcast set, never room media or signaling.
	officeConnections            = map[string]officeConnectionState{}
	activeParticipantConnections map[string]peerConnectionState
	trackLocals                  map[string]*webrtc.TrackLocalStaticRTP
	trackParticipants            map[string]string
	trackParticipantSessions     map[string]string
	// trackRooms maps a forwarded track ID to the room it was published in
	// (multi-room W3). acceptsTrack rejects cross-room forwards, so room A's
	// media is never offered to room B's subscribers.
	trackRooms            map[string]string
	trackSourceIDs        map[string]string
	trackLayerRIDs        map[string]string // forwarded track ID -> simulcast RID ("" when not simulcast)
	trackLayerGroups      map[string]string // forwarded track ID -> source group key (shared by sibling layers)
	subscriberLayerTiers  map[string]string // subscriber session ID -> requested layer tier
	signalRequestLock     sync.Mutex
	signalRequestTimer    *time.Timer
	participantSessionSeq atomic.Uint64
	// activeWebsocketHandlers counts in-flight websocketHandler goroutines. A
	// hijacked websocket outlives httptest.Server.Close, so tests that swap
	// package globals the handler reads (e.g. kanbanApp) must wait for handlers
	// to drain first to stay race-clean. Production never waits on this — it is
	// a plain atomic counter with negligible overhead.
	activeWebsocketHandlers atomic.Int64

	log = logging.NewDefaultLoggerFactory().NewLogger("openai-realtime-meeting-assistant")

	kanbanApp *kanbanBoardApp
	roomMixer *audioMixer
)

const peerSignalDebounce = 250 * time.Millisecond

// Negotiation watchdog thresholds. A subscriber sitting in a non-stable
// signaling state means our offer is unanswered; sync attempts skip it, so
// without intervention a client that silently dropped the offer never gets
// renegotiated again (new participants stay invisible to it). After
// negotiationResendAfter we re-send the pending offer once — a healthy client
// simply answers the duplicate. After negotiationCloseAfter we close the
// PeerConnection so the client's media_disconnected/reconnect path takes over.
const (
	negotiationResendAfter = 8 * time.Second
	negotiationCloseAfter  = 30 * time.Second
	nativeClientProtocolV1 = "native-room-v1"
)

// negotiationAction is the watchdog's verdict for a peer stuck mid-negotiation.
type negotiationAction int

const (
	negotiationActionNone negotiationAction = iota
	negotiationActionResend
	negotiationActionClose
)

// negotiationWatchdogAction decides how to recover a subscriber that has been
// in a non-stable signaling state since firstNonStable: nothing inside the
// resend window, re-send the pending offer once past it (unless already
// resent), and close the connection past the close threshold.
func negotiationWatchdogAction(firstNonStable time.Time, offerResent bool, now time.Time) negotiationAction {
	if firstNonStable.IsZero() {
		return negotiationActionNone
	}

	stuck := now.Sub(firstNonStable)
	switch {
	case stuck >= negotiationCloseAfter:
		return negotiationActionClose
	case stuck >= negotiationResendAfter && !offerResent:
		return negotiationActionResend
	default:
		return negotiationActionNone
	}
}

// iceDisconnectGrace is how long a media peer may sit in ICE "disconnected"
// before the server proactively triggers an ICE restart. ICE disconnects are
// often transient (a brief network blip) and self-heal, so we wait out a short
// grace window before spending a renegotiation; a real network change (e.g.
// Wi-Fi to cellular) persists past it and gets recovered.
const iceDisconnectGrace = 2500 * time.Millisecond

// iceRestartRequestCooldown coalesces restart requests that are not attached to
// an ICE-disconnected episode (for example the stale-tile repair ladder). An
// actual disconnected episode is stricter: it gets one restart window until
// ICE reports connected/completed again, at which point a later outage starts a
// new generation and may restart immediately.
const iceRestartRequestCooldown = 5 * time.Second

// iceStateNeedsRecovery reports whether an ICE connection state indicates a
// recoverable connectivity loss that a server-side ICE restart should address.
// Only "disconnected" qualifies: it is the transient, network-change signal an
// ICE restart is designed to repair. "failed" is intentionally excluded because
// the PeerConnection-level failure handler tears that path down and ejects the
// participant instead of trying to revive it.
func iceStateNeedsRecovery(state webrtc.ICEConnectionState) bool {
	return state == webrtc.ICEConnectionStateDisconnected
}

// maxPendingRemoteICECandidates bounds browser trickle candidates waiting for
// the matching answer generation. A normal browser emits only a handful per
// generation; retaining the newest 128 leaves ample headroom while preventing
// an authenticated socket from growing this queue without limit.
const maxPendingRemoteICECandidates = 128

type pendingRemoteICECandidateQueue struct {
	candidates []webrtc.ICECandidateInit
}

// enqueue deduplicates a candidate and retains the newest bounded window.
// queued is false for a duplicate; evicted reports that the oldest candidate
// was dropped to preserve the bound.
func (q *pendingRemoteICECandidateQueue) enqueue(candidate webrtc.ICECandidateInit) (queued bool, evicted bool) {
	key := remoteICECandidateKey(candidate)
	for _, existing := range q.candidates {
		if remoteICECandidateKey(existing) == key {
			return false, false
		}
	}
	if len(q.candidates) >= maxPendingRemoteICECandidates {
		copy(q.candidates, q.candidates[1:])
		q.candidates = q.candidates[:len(q.candidates)-1]
		evicted = true
	}
	q.candidates = append(q.candidates, candidate)

	return true, evicted
}

// takeMatching drains the queue after an answer is accepted. Only candidates
// whose explicit username fragment belongs to that new remote description
// survive; an older explicit generation is discarded instead of being handed
// to pion against the wrong ICE agent. Legacy/Safari candidates may omit the
// fragment, so they get one post-answer AddICECandidate attempt for backwards
// compatibility (pion remains the final validator).
func (q *pendingRemoteICECandidateQueue) takeMatching(description *webrtc.SessionDescription) (matching []webrtc.ICECandidateInit, discarded int) {
	activeUfrags := remoteDescriptionICEUfrags(description)
	for _, candidate := range q.candidates {
		ufrag := remoteICECandidateUsernameFragment(candidate)
		_, explicitMatch := activeUfrags[ufrag]
		if ufrag == "" || explicitMatch {
			matching = append(matching, candidate)
			continue
		}
		discarded++
	}
	q.candidates = nil

	return matching, discarded
}

// remoteICECandidateShouldQueue protects the restart race where the browser
// starts trickling candidates for its new answer while pion still exposes the
// previous RemoteDescription. Matching/legacy candidates can be applied now;
// a future generation (or no description yet) waits for the answer.
func remoteICECandidateShouldQueue(candidate webrtc.ICECandidateInit, description *webrtc.SessionDescription) bool {
	if description == nil {
		return true
	}
	ufrag := remoteICECandidateUsernameFragment(candidate)
	if ufrag == "" {
		return false // legacy clients omit usernameFragment; preserve existing behavior
	}
	_, matchesCurrentDescription := remoteDescriptionICEUfrags(description)[ufrag]

	return !matchesCurrentDescription
}

func remoteDescriptionICEUfrags(description *webrtc.SessionDescription) map[string]struct{} {
	ufrags := map[string]struct{}{}
	if description == nil {
		return ufrags
	}
	for _, line := range strings.Split(strings.ReplaceAll(description.SDP, "\r\n", "\n"), "\n") {
		const prefix = "a=ice-ufrag:"
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if ufrag := strings.TrimSpace(strings.TrimPrefix(line, prefix)); ufrag != "" {
			ufrags[ufrag] = struct{}{}
		}
	}

	return ufrags
}

func remoteICECandidateUsernameFragment(candidate webrtc.ICECandidateInit) string {
	if candidate.UsernameFragment != nil {
		if ufrag := strings.TrimSpace(*candidate.UsernameFragment); ufrag != "" {
			return ufrag
		}
	}
	// Pion and legacy clients may omit ICECandidateInit.UsernameFragment while
	// still serializing RFC 5245 extension attributes in the candidate line.
	// Recover the generation from "... ufrag <value>" before treating it as
	// unscoped.
	fields := strings.Fields(candidate.Candidate)
	for i := 0; i+1 < len(fields); i++ {
		if strings.EqualFold(fields[i], "ufrag") {
			return strings.TrimSpace(fields[i+1])
		}
	}

	return ""
}

func remoteICECandidateKey(candidate webrtc.ICECandidateInit) string {
	mid := ""
	if candidate.SDPMid != nil {
		mid = *candidate.SDPMid
	}
	mLineIndex := ""
	if candidate.SDPMLineIndex != nil {
		mLineIndex = strconv.FormatUint(uint64(*candidate.SDPMLineIndex), 10)
	}

	return strings.Join([]string{candidate.Candidate, mid, mLineIndex, remoteICECandidateUsernameFragment(candidate)}, "\x00")
}

// peerICERestartState is the server-side restart coalescer for one participant
// PeerConnection. Callers mutate it only while holding listLock.
//
// Browser and server recovery can observe the same outage independently. The
// outage generation makes those two triggers spend one restart, while a later
// disconnected transition after a healthy state gets a fresh generation. A
// connected manual repair is bounded by a short cooldown instead.
type peerICERestartState struct {
	outageActive     bool
	outageGeneration uint64
	restartedOutage  uint64
	queued           bool
	inFlight         bool
	lastStarted      time.Time
}

func (s *peerICERestartState) observeConnectionState(state webrtc.ICEConnectionState) {
	switch state {
	case webrtc.ICEConnectionStateDisconnected:
		if !s.outageActive {
			s.outageActive = true
			s.outageGeneration++
		}
	case webrtc.ICEConnectionStateConnected, webrtc.ICEConnectionStateCompleted:
		s.outageActive = false
		s.queued = false
	}
}

// queue requests one restart and reports whether this call created new work.
// A distinct outage that arrives while an earlier restart is still in flight is
// retained as one queued follow-up; duplicate triggers for the same outage are
// dropped.
func (s *peerICERestartState) queue(state webrtc.ICEConnectionState, now time.Time) bool {
	s.observeConnectionState(state)
	if s.queued {
		return false
	}
	if s.inFlight {
		if s.outageActive && s.restartedOutage != s.outageGeneration {
			s.queued = true
			return true
		}
		return false
	}
	if s.outageActive {
		if s.restartedOutage == s.outageGeneration {
			return false
		}
	} else if !s.lastStarted.IsZero() && now.Sub(s.lastStarted) < iceRestartRequestCooldown {
		return false
	}
	s.queued = true

	return true
}

func (s *peerICERestartState) start(now time.Time) {
	s.queued = false
	s.inFlight = true
	s.lastStarted = now
	if s.outageActive {
		s.restartedOutage = s.outageGeneration
	}
}

func (s *peerICERestartState) complete() {
	s.inFlight = false
}

type websocketMessage struct {
	Event    string `json:"event"`
	Data     string `json:"data"`
	OfferID  string `json:"offerId,omitempty"`
	Revision uint64 `json:"revision,omitempty"`
}

type signalingOfferMetadata struct {
	OfferID  string
	Revision uint64
}

func (m signalingOfferMetadata) empty() bool {
	return strings.TrimSpace(m.OfferID) == "" && m.Revision == 0
}

type peerConnectionState struct {
	peerConnection  *webrtc.PeerConnection
	websocket       *threadSafeWriter
	participantName string
	sessionID       string
	// endpointID identifies the device this session belongs to. Two endpoints
	// of one account coexist; a stale session is only replaced when a newer
	// session arrives on the SAME endpoint. Empty for legacy/native clients,
	// which keeps the original single-slot-per-name behaviour.
	endpointID string
	// sessionEmail is the server-side authenticated account email resolved by
	// websocketHandler at admission. Targeted fan-out (sendKanbanEventToUser)
	// filters on it; it is never taken from a client payload. Empty for guests.
	sessionEmail string
	// roomID is the room this socket was admitted into (multi-room W3): empty
	// means office (the migration invariant), so every legacy entry — and
	// every existing test that constructs states without it — keeps its exact
	// behavior. Room-scoped fan-out and track acceptance filter on it.
	roomID       string
	acceptTrack  func(*webrtc.TrackLocalStaticRTP) bool
	shouldSignal func(desiredTrackCount int) bool
	signal       func(gatherComplete <-chan struct{}) error

	// Negotiation watchdog bookkeeping, mutated only under listLock by
	// signalPeerConnectionsWithRestart.
	nonStableSince time.Time
	offerResent    bool

	// Optional signaling correlation for newer clients. Legacy clients ignore
	// these fields and can still answer with plain SDP in websocketMessage.Data.
	signalingRevision    uint64
	pendingOfferID       string
	pendingOfferRevision uint64
	iceRestart           peerICERestartState
}

// officeConnectionState is the registry entry for an authenticated websocket
// that sent the `office` hello instead of taking a room seat. It carries no
// media state — just the writer for signed-in-safe fan-out and the
// server-resolved account email for targeted sends (never client-supplied).
type officeConnectionState struct {
	websocket    *threadSafeWriter
	sessionEmail string
}

type participantTrackSnapshot struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	TrackID       string `json:"trackId"`
	SourceTrackID string `json:"sourceTrackId,omitempty"`
	StreamID      string `json:"streamId,omitempty"`
	RoomID        string `json:"roomId"`
}

func (p peerConnectionState) acceptsTrack(track *webrtc.TrackLocalStaticRTP) bool {
	if track != nil && sameParticipantName(trackParticipants[track.ID()], p.participantName) {
		return false
	}
	// Two-room isolation (multi-room W3): a track only forwards to subscribers
	// seated in the room it was published in. Absent room ids mean office on
	// both sides (§9 migration invariant).
	if track != nil && normalizeRoomID(trackRooms[track.ID()]) != normalizeRoomID(p.roomID) {
		return false
	}
	if p.acceptTrack != nil {
		return p.acceptTrack(track)
	}
	if track == nil {
		return true
	}

	// Simulcast forwarding control: when the source sent multiple layers, forward
	// only the one matching this subscriber's requested tier. Non-simulcast
	// sources (a group of one) always forward, so the common case is unchanged.
	return subscriberAcceptsLayerLocked(p.sessionID, track.ID())
}

func (p peerConnectionState) shouldSignalWithDesiredTrackCount(desiredTrackCount int) bool {
	if p.shouldSignal == nil {
		return true
	}

	return p.shouldSignal(desiredTrackCount)
}

func startPendingOfferMetadata(peer *peerConnectionState) signalingOfferMetadata {
	if peer == nil {
		return signalingOfferMetadata{}
	}
	peer.signalingRevision++
	metadata := signalingOfferMetadata{
		OfferID:  signalingOfferID(peer.sessionID, peer.signalingRevision),
		Revision: peer.signalingRevision,
	}
	peer.pendingOfferID = metadata.OfferID
	peer.pendingOfferRevision = metadata.Revision

	return metadata
}

func pendingOfferMetadata(peer *peerConnectionState) signalingOfferMetadata {
	if peer == nil {
		return signalingOfferMetadata{}
	}

	return signalingOfferMetadata{OfferID: peer.pendingOfferID, Revision: peer.pendingOfferRevision}
}

func signalingOfferID(sessionID string, revision uint64) string {
	return fmt.Sprintf("%s-offer-%d", mediaIDPart(sessionID, "session"), revision)
}

func signalingMetadataFromMessage(message websocketMessage) signalingOfferMetadata {
	metadata := signalingOfferMetadata{
		OfferID:  strings.TrimSpace(message.OfferID),
		Revision: message.Revision,
	}
	if strings.TrimSpace(message.Data) == "" {
		return metadata
	}

	var payload struct {
		OfferID  string `json:"offerId"`
		Revision uint64 `json:"revision"`
	}
	if err := json.Unmarshal([]byte(message.Data), &payload); err != nil {
		return metadata
	}
	if metadata.OfferID == "" {
		metadata.OfferID = strings.TrimSpace(payload.OfferID)
	}
	if metadata.Revision == 0 {
		metadata.Revision = payload.Revision
	}

	return metadata
}

func shouldIgnoreAnswerForPendingOffer(answerMetadata signalingOfferMetadata, pendingMetadata signalingOfferMetadata) (bool, string) {
	if answerMetadata.empty() {
		return false, ""
	}
	if pendingMetadata.empty() {
		return true, "no pending offer"
	}
	if answerMetadata.OfferID != "" && pendingMetadata.OfferID != "" && answerMetadata.OfferID != pendingMetadata.OfferID {
		return true, "offer id mismatch"
	}
	if answerMetadata.Revision != 0 && pendingMetadata.Revision != 0 && answerMetadata.Revision != pendingMetadata.Revision {
		return true, "revision mismatch"
	}

	return false, ""
}

func currentPendingOfferMetadata(peerConnection *webrtc.PeerConnection) signalingOfferMetadata {
	if peerConnection == nil {
		return signalingOfferMetadata{}
	}

	listLock.RLock()
	defer listLock.RUnlock()
	for i := range peerConnections {
		if peerConnections[i].peerConnection == peerConnection {
			return pendingOfferMetadata(&peerConnections[i])
		}
	}

	return signalingOfferMetadata{}
}

func clearPendingOfferMetadata(peerConnection *webrtc.PeerConnection) {
	if peerConnection == nil {
		return
	}

	listLock.Lock()
	defer listLock.Unlock()
	for i := range peerConnections {
		if peerConnections[i].peerConnection == peerConnection {
			peerConnections[i].pendingOfferID = ""
			peerConnections[i].pendingOfferRevision = 0
			return
		}
	}
}

func countPeerSenders(peerConnection *webrtc.PeerConnection) int {
	if peerConnection == nil {
		return 0
	}
	count := 0
	for _, sender := range peerConnection.GetSenders() {
		if sender.Track() != nil {
			count++
		}
	}

	return count
}

func countPeerReceivers(peerConnection *webrtc.PeerConnection) int {
	if peerConnection == nil {
		return 0
	}
	count := 0
	for _, receiver := range peerConnection.GetReceivers() {
		if receiver.Track() != nil {
			count++
		}
	}

	return count
}

func forwardedTrackCountsLocked() (total int, audio int, video int) {
	for _, trackLocal := range trackLocals {
		if trackLocal == nil {
			continue
		}
		total++
		switch trackLocal.Kind() {
		case webrtc.RTPCodecTypeAudio:
			audio++
		case webrtc.RTPCodecTypeVideo:
			video++
		default:
		}
	}

	return total, audio, video
}

func snapshotForwardedTrackCounts() (total int, audio int, video int) {
	listLock.RLock()
	defer listLock.RUnlock()

	return forwardedTrackCountsLocked()
}

func rtcpFeedbackSummary(feedback []webrtc.RTCPFeedback) string {
	if len(feedback) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(feedback))
	for _, item := range feedback {
		value := strings.TrimSpace(item.Type)
		if item.Parameter != "" {
			value += "/" + strings.TrimSpace(item.Parameter)
		}
		if value != "" {
			parts = append(parts, value)
		}
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return "[]"
	}

	return strings.Join(parts, ",")
}

// Identity-scoped SDES header extension URIs (RFC 8843 §9, RFC 8852). Their
// ids AND values are meaningful only on the transport they were negotiated
// on: the MID/RID bytes name the PUBLISHER's m-lines, and the ids may map to
// entirely different URIs on a subscriber's transport. Transport-wide CC is
// likewise hop-by-hop: its sequence belongs to the publisher->SFU transport
// and must not coexist with the fresh sequence generated for SFU->subscriber.
const (
	sdesMidExtensionURI                 = "urn:ietf:params:rtp-hdrext:sdes:mid"
	sdesRTPStreamIDExtensionURI         = "urn:ietf:params:rtp-hdrext:sdes:rtp-stream-id"
	sdesRepairedRTPStreamIDExtensionURI = "urn:ietf:params:rtp-hdrext:sdes:repaired-rtp-stream-id"
)

// transportScopedRTPExtensionIDs resolves which extension ids the PUBLISHER's
// transport negotiated for identity-scoped SDES or hop-by-hop transport-wide
// CC — the ids the forward path must strip before fan-out.
func transportScopedRTPExtensionIDs(receiver *webrtc.RTPReceiver) []uint8 {
	if receiver == nil {
		return nil
	}
	return transportScopedRTPExtensionIDsFromParameters(receiver.GetParameters())
}

func transportScopedRTPExtensionIDsFromParameters(parameters webrtc.RTPParameters) []uint8 {
	var ids []uint8
	for _, extension := range parameters.HeaderExtensions {
		switch extension.URI {
		case sdesMidExtensionURI, sdesRTPStreamIDExtensionURI, sdesRepairedRTPStreamIDExtensionURI, sdp.TransportCCURI:
			ids = append(ids, uint8(extension.ID))
		}
	}
	return ids
}

// stripTransportScopedRTPExtensions deletes the publisher's identity/
// transport-scoped header extensions from a packet before fan-out. RTP header
// extension ids are negotiated PER TRANSPORT (RFC 8285 §6): forwarding the
// publisher's raw MID/RID bytes to subscribers — whose demux resolves the same
// id against THEIR OWN negotiation — is spec-incorrect and can confuse
// subscriber-side demux after m-line churn (the permanent per-subscriber
// freeze class in the 2026-07-10 incident). Publisher transport-wide CC is
// also removed because its sequence space terminates at the SFU. Everything
// else is preserved: commit 0a46b50 deliberately keeps media-scoped extensions
// (video orientation/rotation, abs-send-time, audio-level) because a
// strip-everything forward made phone video look unstable to subscribers.
func stripTransportScopedRTPExtensions(packet *rtp.Packet, ids []uint8) {
	if packet == nil || !packet.Header.Extension || len(ids) == 0 {
		return
	}
	for _, id := range ids {
		if packet.Header.GetExtension(id) != nil {
			_ = packet.Header.DelExtension(id)
		}
	}
	if len(packet.Header.Extensions) == 0 {
		// Never marshal a zero-length extension block.
		packet.Header.Extension = false
		packet.Header.ExtensionProfile = 0
	}
}

// forwardPublisherRTP is the single write seam from a publisher's inbound RTP
// to the room's fan-out track: strip publisher-transport identity/TWCC
// extensions, keep the media-scoped ones, write.
func forwardPublisherRTP(trackLocal *webrtc.TrackLocalStaticRTP, packet *rtp.Packet, stripExtensionIDs []uint8) error {
	stripTransportScopedRTPExtensions(packet, stripExtensionIDs)
	return trackLocal.WriteRTP(packet)
}

func rtpExtensionIDSummary(ids []uint8) string {
	if len(ids) == 0 {
		return "[]"
	}
	copied := append([]uint8(nil), ids...)
	sort.Slice(copied, func(i, j int) bool { return copied[i] < copied[j] })
	parts := make([]string, 0, len(copied))
	for _, id := range copied {
		parts = append(parts, strconv.Itoa(int(id)))
	}

	return strings.Join(parts, ",")
}

// sourceGroupLayersLocked returns the simulcast layers that belong to the same
// source group as trackID. Callers must already hold listLock.
func sourceGroupLayersLocked(trackID string) []layerOption {
	group := trackLayerGroups[trackID]
	if group == "" {
		return nil
	}

	var layers []layerOption
	for id, g := range trackLayerGroups {
		if g == group {
			layers = append(layers, layerOption{trackID: id, rid: trackLayerRIDs[id]})
		}
	}

	return layers
}

// subscriberAcceptsLayerLocked decides whether the subscriber identified by
// sessionID should be forwarded trackID, honouring its requested layer tier when
// the source is simulcast. Callers must already hold listLock.
func subscriberAcceptsLayerLocked(sessionID string, trackID string) bool {
	layers := sourceGroupLayersLocked(trackID)
	if !isSimulcastGroup(layers) {
		return true // not simulcast (untracked, lone layer, or RID-less duplicates): forward unchanged
	}

	return subscriberWantsLayer(trackID, normalizeLayerTier(subscriberLayerTiers[sessionID]), layers)
}

// setSubscriberLayerTier records a subscriber's requested simulcast tier and
// reports whether it changed (so the caller can avoid a needless renegotiation).
func setSubscriberLayerTier(sessionID string, tier layerTier) bool {
	if sessionID == "" {
		return false
	}

	listLock.Lock()
	defer listLock.Unlock()
	if subscriberLayerTiers == nil {
		subscriberLayerTiers = map[string]string{}
	}
	if layerTier(subscriberLayerTiers[sessionID]) == tier {
		return false
	}
	subscriberLayerTiers[sessionID] = string(tier)

	return true
}

func main() {
	// Parse the flags passed to program
	flag.Parse()
	if err := validateCanonicalModeConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Canonical persistence configuration is invalid: %v\n", err)
		os.Exit(2)
	}

	if *codexRunnerWorker {
		if err := runCodexRunnerLoop(context.Background()); err != nil {
			log.Errorf("Codex runner stopped: %v", err)
		}
		return
	}

	if *renderRunnerWorker {
		if err := runRenderRunnerLoop(context.Background()); err != nil {
			log.Errorf("Render runner stopped: %v", err)
		}
		return
	}

	// Init other state
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{}
	trackParticipants = map[string]string{}
	trackParticipantSessions = map[string]string{}
	trackRooms = map[string]string{}
	trackSourceIDs = map[string]string{}
	trackLayerRIDs = map[string]string{}
	trackLayerGroups = map[string]string{}
	subscriberLayerTiers = map[string]string{}
	activeParticipantConnections = map[string]peerConnectionState{}
	roomMixer = newAudioMixer()
	defer roomMixer.close()
	kanbanApp = newKanbanBoardApp()
	roomMixer.setActivityListener(kanbanApp)
	defer kanbanApp.Close()
	if err := kanbanApp.JoinConferenceRoom(); err != nil {
		log.Errorf("Kanban Realtime peer disabled: %v", err)
		broadcastAssistantEvent("error", "OpenAI Realtime disabled: "+err.Error(), nil)
	}
	// The slop classifier + expiry sweep is a slow background worker (default
	// 6h) that runs independent of any meeting, so it starts at boot rather than
	// at room join. No-ops without OPENAI_API_KEY, like every ambient worker.
	kanbanApp.startSlopClassifierWorker(strings.TrimSpace(os.Getenv("OPENAI_API_KEY")))
	// The liveness sweep backstops the per-socket read-deadline cleanup so a
	// zombie/backgrounded room socket can never hold occupancy above zero and
	// keep a finished sitting from finalizing into a fresh meeting id.
	kanbanApp.startParticipantLivenessSweeper()
	// Memory-architecture study §6 Phase 0.1 (gap #1): nightly encrypted snapshot
	// of the whole data dir to a local ring + optional offsite S3/Spaces, so a
	// droplet disk loss is no longer total permanent loss of the company brain.
	// Boot-once background worker (not room-scoped, no app lock); the first
	// snapshot runs a few minutes after boot so a fresh deploy proves the path.
	// No-ops when BACKUP_DISABLED / BACKUP_INTERVAL_HOURS=0, and never fires under
	// `go test` (main() isn't invoked there; the first-run delay is a second guard).
	startBackupTicker()
	// Card 069: the DEFAULT approval-governance decision rides the ledger as
	// PROPOSED from boot (keyless included — this is a store write, not a
	// worker) so the team can see and ratify it in Mission Intelligence.
	kanbanApp.seedProposedGovernanceDecision()
	// Card 083: the Gmail consent + scope proposal publishes as a fixed-ID
	// artifact with a broadcast bell nudge on first boot (idempotent across
	// restarts). No Gmail code ships — the team ratifies via the doc's
	// Decision sentence, which the decision ledger extracts verbatim.
	seedGmailConsentProposal(kanbanApp)
	// W1-12 tail: validate the realtime/transcription dials once at boot so a
	// whisper-family pin, a typo'd model id, or an out-of-enum effort value is
	// loud in the deploy logs instead of failing per-request in prod.
	validateRealtimeConfig()
	// W0-9: the usage-rollup worker folds the LLM usage/eval ledgers into the
	// living "LLM Spend & Health" artifact and runs the alert engine
	// (spend cap, unknown models, parse-failure/board-op/router spikes,
	// no-vocab warnings, transcript drop-off) with a 6h per-kind dedupe.
	// Killable via USAGE_ROLLUP_DISABLED / USAGE_ALERTS_DISABLED.
	startUsageRollupWorker(kanbanApp)

	// Read index.html from disk into memory, serve whenever anyone requests /
	var err error
	indexHTML, err = os.ReadFile("index.html")
	if err != nil {
		panic(err)
	}

	// Stamp the running build into the served HTML. The version is computed from
	// the raw file (with the placeholder still in it) plus the binary's identity,
	// so it changes on every deploy that ships a new binary or a new index.html
	// but NOT on a plain restart of the same image — a crash-restart therefore
	// never nags open tabs to reload, while a real deploy always does.
	serverBuildVersion = computeBuildVersion(indexHTML)
	indexHTML = bytes.ReplaceAll(indexHTML, []byte(buildVersionPlaceholder), []byte(serverBuildVersion))

	// Card 089: mint (or load) the Web Push VAPID keypair at boot so the
	// public key is ready the moment a client asks to subscribe. Persists to
	// data/vapid-keys.json, which survives deploys like users.json.
	loadOrCreateVAPIDKeys()

	// Multi-room W1: seed data/rooms.json with the office room at boot so the
	// registry exists before any request needs it (§9.1 — no passcode on the
	// office, one-click join preserved).
	appRoomStore()

	// websocket handler
	http.HandleFunc("/healthz", healthHandler)
	http.HandleFunc("/livez", liveHandler)
	http.HandleFunc("/readyz", readinessHandler)
	http.HandleFunc("/capabilities", capabilitiesHandler)
	http.HandleFunc("/websocket", websocketHandler)
	http.HandleFunc("/auth/", authHandler)
	http.HandleFunc("/assistant/query", assistantQueryHandler)
	http.HandleFunc("/assistant/chat-threads", assistantChatThreadsHandler)
	http.HandleFunc("/assistant/chat-threads/", assistantChatThreadHandler)
	http.HandleFunc("/assistant/attachments", assistantAttachmentUploadHandler)
	http.HandleFunc("/assistant/threads", assistantThreadsHandler)
	http.HandleFunc("/assistant/threads/follow-up", assistantThreadFollowUpHandler)
	http.HandleFunc("/assistant/goal", assistantGoalHandler)
	http.HandleFunc("/assistant/goal/cancel", assistantGoalCancelHandler)
	http.HandleFunc("/assistant/decisions/supersede", assistantDecisionSupersedeHandler)
	http.HandleFunc("/assistant/decisions/ratify", assistantDecisionRatifyHandler)
	http.HandleFunc("/assistant/tools", assistantToolsHandler)
	http.HandleFunc("/assistant/notifications", assistantNotificationsHandler)
	http.HandleFunc("/assistant/notifications/read", assistantNotificationsReadHandler)
	http.HandleFunc("/assistant/notifications/clear", assistantNotificationsClearHandler)
	http.HandleFunc("/assistant/push/config", assistantPushConfigHandler)
	http.HandleFunc("/assistant/push/subscribe", assistantPushSubscribeHandler)
	http.HandleFunc("/assistant/push/unsubscribe", assistantPushUnsubscribeHandler)
	http.HandleFunc("/assistant/push/prefs", assistantPushPrefsHandler)
	http.HandleFunc("/assistant/board", assistantBoardHandler)
	http.HandleFunc("/assistant/board/drafts/", assistantBoardDraftActionHandler)
	http.HandleFunc("/assistant/memory", assistantMemoryHandler)
	http.HandleFunc("/assistant/files", assistantFilesHandler)
	http.HandleFunc("/assistant/files/upload", assistantFileUploadHandler)
	http.HandleFunc("/assistant/files/folders", assistantFileFoldersHandler)
	http.HandleFunc("/assistant/files/move", assistantFileMoveHandler)
	http.HandleFunc("/assistant/files/save", assistantFileSaveHandler)
	http.HandleFunc("/assistant/meetings", assistantMeetingsHandler)
	http.HandleFunc("/assistant/mission", assistantMissionHandler)
	http.HandleFunc("/assistant/mission/refresh", assistantMissionRefreshHandler)
	http.HandleFunc("/assistant/proposals/", assistantProposalActionHandler)
	http.HandleFunc("/assistant/quarantine", assistantQuarantineHandler)
	http.HandleFunc("/assistant/quarantine/", assistantQuarantineActionHandler)
	http.HandleFunc("/assistant/packages", assistantPackagesHandler)
	http.HandleFunc("/assistant/packages/", assistantPackageActionHandler)
	http.HandleFunc("/assistant/deal-room/request", assistantDealRoomRequestHandler)
	http.HandleFunc("/assistant/deal-room/resolve", assistantDealRoomResolveHandler)
	http.HandleFunc("/assistant/deal-room/revoke", assistantDealRoomRevokeHandler)
	http.HandleFunc("/assistant/deal-room/list", assistantDealRoomListHandler)
	http.HandleFunc("/deal-room/", dealRoomPublicHandler)
	http.HandleFunc("/assistant/brief", assistantBriefHandler)
	http.HandleFunc("/assistant/portfolio", assistantPortfolioHandler)
	http.HandleFunc("/assistant/realtime-offer", assistantRealtimeOfferHandler)
	http.HandleFunc("/assistant/realtime-tool", assistantRealtimeToolHandler)
	// W0 private-voice usage beacon (founder decision 3): the browser owns the
	// private voice peer, so the page posts each response.done usage object
	// here and the server ledgers it under seat voice_private.
	http.HandleFunc("/assistant/realtime/usage", assistantRealtimeUsageHandler)
	// W0-9: signed-in JSON twin of the living Spend & Health artifact.
	http.HandleFunc("/api/usage/rollup", usageRollupHandler)
	http.HandleFunc("/internal/codex/jobs/result", internalCodexRunnerResultHandler)
	http.HandleFunc("/artifacts", artifactsHandler)
	http.HandleFunc("/artifacts/action", artifactRunnerActionHandler)
	http.HandleFunc("/artifacts/open", artifactOpenHandler)
	http.HandleFunc("/artifacts/render", artifactRenderHandler)
	http.HandleFunc("/artifacts/render-token", artifactRenderTokenHandler)
	http.HandleFunc("/artifacts/blob", artifactBlobHandler)
	http.HandleFunc("/artifacts/share", artifactShareHandler)
	http.HandleFunc("/a/", shareLinkPublicHandler)
	http.HandleFunc("/artifacts/export-pdf", artifactExportPDFHandler)
	http.HandleFunc("/calendar/event.ics", calendarICSHandler)
	http.HandleFunc("/internal/render/jobs/result", internalRenderRunnerResultHandler)
	http.HandleFunc("/signals/survey", signalSurveyHandler)
	http.HandleFunc("/archives/", meetingArchiveHandler)
	http.HandleFunc("/participants", participantsHandler)
	http.HandleFunc("/client-config", clientConfigHandler)
	http.HandleFunc("/native/config", nativeClientConfigHandler)
	// Multi-room W1: room registry + guest capability surface (rooms.go).
	http.HandleFunc("/rooms", roomsHandler)
	http.HandleFunc("/rooms/", roomActionHandler)
	http.HandleFunc("/g", guestPageHandler)
	http.HandleFunc("/g/", guestPageHandler)
	http.HandleFunc("/guest/join", guestJoinHandler)
	// Rooms-UX RW2: token-gated room naming for the guest gate — mints nothing.
	http.HandleFunc("/guest/lookup", guestLookupHandler)
	http.HandleFunc("/guest/me", guestMeHandler)
	http.HandleFunc("/ice-test", iceTestHandler)
	http.HandleFunc("/public/", publicAssetHandler)
	// The service worker must be served from the ORIGIN ROOT so its scope is
	// "/" (a worker under /public/ could only control /public/*). This exact
	// ServeMux pattern beats the "/" catch-all, so shouldServeIndexHTML never
	// sees it. Served no-store so a redeploy's worker propagates immediately —
	// the worker itself caches nothing (index.html is intentionally no-store).
	http.HandleFunc("/sw.js", serviceWorkerHandler)

	// index.html handler. The SPA is served for "/" and browser navigations
	// only: unknown API-ish paths (e.g. /assistant/*, /brain/state) must 404
	// instead of answering 200 with HTML, which buries client errors.
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !shouldServeIndexHTML(r) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, writeErr := w.Write(indexHTML); writeErr != nil {
			log.Errorf("Failed to serve index page: %v", writeErr)
		}
	})

	// Sweep publisher tracks for silently-stalled uplinks every 3 seconds
	// (2026-07-10 silent-uplink incident). Keyframes are requested only for a
	// concrete subscriber decoder event, video-liveness stall, or screen-share
	// transition; a perpetual room-wide PLI walk makes every encoder spike in
	// lockstep and causes visible flicker on constrained Safari/mobile devices.
	go func() {
		for range time.NewTicker(time.Second * 3).C {
			sweepPublisherSilence()
		}
	}()

	// start HTTP server with header/idle timeouts to bound slowloris-style abuse.
	// ReadHeaderTimeout protects the request-line/header read; IdleTimeout reaps
	// idle keep-alive conns. ReadTimeout/WriteTimeout are intentionally left unset
	// so they do not sever long-lived Websocket/WebRTC signaling sockets (gorilla
	// hijacks the connection on upgrade, after which these would not apply anyway).
	srv := &http.Server{
		Addr:              *addr,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	shutdownCtx, stopShutdownSignal := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopShutdownSignal()
	go func() {
		<-shutdownCtx.Done()
		stopShutdownSignal()
		broadcastServerShutdown(3000)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if shutdownErr := srv.Shutdown(ctx); shutdownErr != nil {
			log.Errorf("Failed to gracefully shut down http server: %v", shutdownErr)
		}
	}()
	if err = srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Errorf("Failed to start http server: %v", err)
	}
}

func broadcastServerShutdown(retryAfterMs int) {
	if kanbanApp != nil {
		kanbanApp.mu.Lock()
		kanbanApp.restarting = true
		kanbanApp.mu.Unlock()
	}
	log.Infof("room_server_shutdown retry_after_ms=%d", retryAfterMs)
	broadcastSignedInKanbanEvent("server_shutdown", map[string]any{
		"message":      "Server restarting",
		"retryAfterMs": retryAfterMs,
	})
}

// buildVersionPlaceholder is the token the served index.html carries in a
// <meta> tag; it is rewritten to serverBuildVersion at boot. Kept in sync with
// the same literal in index.html.
const buildVersionPlaceholder = "__BONFIRE_BUILD_VERSION__"

// computeBuildVersion derives a stable id for the running build. An explicit
// BONFIRE_BUILD_VERSION wins (e.g. a git sha injected at image build time);
// otherwise it hashes the server binary's size+modtime together with the
// frontend bytes. Both change on a real deploy (rebuild → new binary, or an
// edited index.html) yet stay constant across a plain process restart, so the
// client-side skew guard only fires when the code actually changed.
func computeBuildVersion(indexBytes []byte) string {
	if v := strings.TrimSpace(os.Getenv("BONFIRE_BUILD_VERSION")); v != "" {
		return v
	}
	h := sha256.New()
	if exe, err := os.Executable(); err == nil {
		if info, statErr := os.Stat(exe); statErr == nil {
			fmt.Fprintf(h, "exe:%d:%d\n", info.Size(), info.ModTime().UnixNano())
		}
	}
	h.Write(indexBytes)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	writeSystemStatusJSON(w, r, http.StatusOK, map[string]any{
		"ok":      true,
		"service": "meetingassist",
		"version": serverBuildVersion,
		"time":    time.Now().UTC().Format(time.RFC3339Nano),
		// W0-9: effective realtime/transcription lane models + vocab posture +
		// ledger/alert switches, so a whisper-family fossil pin or a dead
		// ledger is visible from the outside. Additive — the existing
		// ok/service/version/time contract is untouched.
		"telemetry": healthTelemetrySnapshot(),
	})
}

func readinessHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	traffic := trafficReadiness()
	appAvailable := traffic.appAvailable
	memoryAvailable := traffic.memoryAvailable
	memoryCheck := traffic.memoryCheck
	boardCheck := traffic.boardCheck
	realtime := map[string]any{"connected": false, "voiceControl": false}
	if appAvailable {
		kanbanApp.mu.Lock()
		realtime["connected"] = kanbanApp.connected
		realtime["voiceControl"] = kanbanApp.voiceControlActive
		kanbanApp.mu.Unlock()
	}

	ready := traffic.ready
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}

	capabilities, capabilityDegraded := capabilitySnapshot(time.Now())
	degraded := append([]string{}, capabilityDegraded...)
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		degraded = append([]string{"openai_api_key_missing"}, degraded...)
	}

	writeSystemStatusJSON(w, r, status, map[string]any{
		"ok":           ready,
		"service":      "meetingassist",
		"time":         time.Now().UTC().Format(time.RFC3339Nano),
		"degraded":     degraded,
		"capabilities": capabilities,
		"checks": map[string]any{
			"app":         appAvailable,
			"memoryStore": memoryAvailable,
			"memoryFile":  memoryCheck,
			"boardFile":   boardCheck,
			"realtime":    realtime,
			"backup":      readinessBackupSnapshot(),
			"agents": map[string]any{
				"brain":          readinessAgentSnapshot(meetingBrainAgent()),
				"board":          readinessAgentSnapshot(meetingBoardAgent()),
				"missionIntel":   readinessAgentSnapshot(missionIntelligenceAgent()),
				"codexRunner":    readinessCodexRunnerSnapshot(),
				"renderRunner":   readinessRenderRunnerSnapshot(),
				"workflowTicker": readinessWorkflowTickerSnapshot(),
			},
		},
	})
}

func writeSystemStatusJSON(w http.ResponseWriter, r *http.Request, status int, payload map[string]any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Errorf("Failed to encode system status: %v", err)
	}
}

func readinessStateFileCheck(path string) map[string]any {
	result := map[string]any{
		"ok":       false,
		"writable": false,
	}
	dir := filepath.Dir(strings.TrimSpace(path))
	if dir == "" || dir == "." {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		result["error"] = "create_directory_failed"
		return result
	}
	tempFile, err := os.CreateTemp(dir, ".readyz-*")
	if err != nil {
		result["error"] = "create_temp_file_failed"
		return result
	}
	tempPath := tempFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := tempFile.Write([]byte("ok")); err != nil {
		_ = tempFile.Close()
		result["error"] = "write_temp_file_failed"
		return result
	}
	if err := tempFile.Close(); err != nil {
		result["error"] = "close_temp_file_failed"
		return result
	}
	if err := os.Remove(tempPath); err != nil {
		result["error"] = "remove_temp_file_failed"
		return result
	}
	cleanup = false
	result["ok"] = true
	result["writable"] = true
	return result
}

func readinessCheckOK(check map[string]any) bool {
	ok, _ := check["ok"].(bool)
	return ok
}

func readinessAgentSnapshot(agent ambientAgentConfig) map[string]any {
	interval := agent.interval()
	enabled := interval > 0 && !boolEnv(agent.disabledEnv)
	return map[string]any{
		"enabled":         enabled,
		"intervalSeconds": int(interval.Seconds()),
		"minBatch":        agent.minBatch(),
		"maxBatch":        agent.maxBatch(),
	}
}

// renderSidecarAvailable reports whether the render-runner sidecar is live:
// a fresh heartbeat on the shared volume (readinessRenderRunnerSnapshot, the
// codex-runner twin). Missing or stale is exactly the keyless/sidecar-absent
// degradation — the trigger route answers 503 with a clear operator message
// instead of queueing a job nothing will ever claim.
func renderSidecarAvailable() bool {
	heartbeatOK, _ := readinessRenderRunnerSnapshot()["heartbeatOK"].(bool)
	return heartbeatOK
}

// artifactExportPDFHandler serves POST /artifacts/export-pdf
// {artifactId, kind} (packaging OS §4 item 14b) — session-gated exactly like
// its /artifacts neighbors. It enqueues an export_pdf job for the
// render-runner sidecar (enqueueRenderExportPDFJob) and stamps renderJobId on
// the artifact so the callback can verify job identity, mirroring the codex
// runnerJobId contract.
func artifactExportPDFHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if userFromRequest(r) == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "artifacts are unavailable")
		return
	}

	payload := struct {
		ArtifactID string `json:"artifactId"`
		Kind       string `json:"kind"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read PDF export request")
		return
	}

	artifact, found := kanbanApp.osArtifactByID(strings.TrimSpace(payload.ArtifactID))
	if !found {
		writeAuthError(w, http.StatusNotFound, "artifact not found")
		return
	}
	// Decks and paper-kit documents print their own HTML (deck: flatten law;
	// paper: text-native direct print). A markdown body — the research-report
	// contract — has nothing for chromium to lay out, so the server converts
	// it into the branded BonfireOS print document
	// (renderResearchReportPrintHTML) and ships it down the text-native paper
	// path.
	printHTML := artifact.Text
	markdownReport := !artifactIsHTMLDocument(artifact)
	if markdownReport {
		printHTML = renderResearchReportPrintHTML(artifact)
	}
	if !renderSidecarAvailable() {
		writeAuthError(w, http.StatusServiceUnavailable, "render sidecar not available — start the render-runner container (or run with -render-runner) to export PDFs")
		return
	}

	// The flatten law is server-owned: the client may request an export, not
	// choose the print path (a deck exported as "paper" would ship the layered
	// print). Kind derives from the artifact's own declaration; a stated kind
	// that disagrees is rejected rather than silently rewritten. A markdown
	// report is text-native by construction — always paper, never a flatten.
	kind := serverRenderKindForArtifact(artifact)
	if markdownReport {
		kind = renderJobKindPaper
	}
	if requested := strings.TrimSpace(payload.Kind); requested != "" && normalizeRenderJobKind(requested) != kind {
		writeAuthError(w, http.StatusBadRequest, "export kind is derived from the artifact (paper is only for paper-kit documents and markdown reports) — omit kind or match it")
		return
	}
	job, err := enqueueRenderExportPDFJob(artifact.ID, kind, printHTML, artifact.Metadata["title"])
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Bookkeeping stamp via the metadata-only path (the openedAt precedent):
	// a failure loses the job-identity check, never the queued job — log and
	// continue.
	if _, _, err := kanbanApp.memory.updateOSArtifactMetadata(artifact.ID, map[string]string{
		"renderJobId":  job.ID,
		"renderStatus": renderJobStatusQueued,
		"renderKind":   kind,
	}); err != nil {
		log.Errorf("Failed to stamp renderJobId on artifact %s: %v", artifact.ID, err)
	}

	writeAuthJSON(w, http.StatusAccepted, map[string]any{
		"ok":    true,
		"jobId": job.ID,
		"kind":  kind,
	})
}

// renderCallbackMaxBytes bounds the render callback body: base64 of a
// cap-sized PDF (~4/3 × 64MB) plus JSON framing headroom.
const renderCallbackMaxBytes = blobMaxBytes + blobMaxBytes/2

// resolveRenderQueueFile is the ONE trust check for every shared-volume path a
// sidecar callback names (the PDF fallback and the page JPEGs). The sidecar is
// the least-trusted box in the system — it executes untrusted artifact HTML —
// so a lexical prefix check is not enough: a compromised sidecar can write
// queue/page.jpg -> /opt/meetingassist/.env and have a naive os.ReadFile follow
// the symlink. The path must (1) sit inside the render queue BEFORE resolution,
// (2) resolve (filepath.EvalSymlinks) to a path still inside the RESOLVED queue
// root, and (3) be a regular file (os.Lstat on the resolved path — never a
// device, socket, or dangling link). Returns the resolved path and its
// FileInfo so callers can bound the read by size before it happens.
func resolveRenderQueueFile(rawPath string) (string, os.FileInfo, error) {
	path := filepath.Clean(strings.TrimSpace(rawPath))
	if path == "" || path == "." {
		return "", nil, fmt.Errorf("empty path")
	}
	queueRoot := renderRunnerQueuePath()
	if !strings.HasPrefix(path, queueRoot+string(os.PathSeparator)) {
		return "", nil, fmt.Errorf("path is outside the render queue")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, fmt.Errorf("resolve path: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(queueRoot)
	if err != nil {
		return "", nil, fmt.Errorf("resolve render queue root: %w", err)
	}
	if !strings.HasPrefix(resolved, resolvedRoot+string(os.PathSeparator)) {
		return "", nil, fmt.Errorf("path resolves outside the render queue")
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", nil, fmt.Errorf("stat path: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("path is not a regular file")
	}
	return resolved, info, nil
}

// renderCallbackPDFBytes extracts the exported PDF from the callback: the
// base64 payload wins; the shared-volume path is the fallback transport and
// is only honored INSIDE the render queue directory, symlink-resolved and
// size-bounded (resolveRenderQueueFile) — the bearer token authenticates the
// sidecar, it does not make the callback a file-read oracle.
func renderCallbackPDFBytes(payload renderRunnerCallbackPayload) ([]byte, error) {
	if encoded := strings.TrimSpace(payload.PDFBase64); encoded != "" {
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("decode callback pdf: %w", err)
		}
		return data, nil
	}
	if strings.TrimSpace(payload.PDFPath) == "" {
		return nil, fmt.Errorf("callback carried no PDF")
	}
	path, info, err := resolveRenderQueueFile(payload.PDFPath)
	if err != nil {
		return nil, fmt.Errorf("callback pdf path: %w", err)
	}
	if info.Size() > blobMaxBytes {
		return nil, fmt.Errorf("callback pdf is %dMB, above the %dMB blob cap", info.Size()>>20, blobMaxBytes>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read callback pdf: %w", err)
	}
	return data, nil
}

// internalRenderRunnerResultHandler serves POST /internal/render/jobs/result —
// the render sidecar's authenticated callback (Bearer BONFIRE_RUNNER_TOKEN,
// the internalCodexRunnerResultHandler twin). A complete job stores the PDF
// in the blob store, appends a {kind: pdf} asset on the artifact, and records
// the pdf_exported signal; running/failed callbacks only stamp status
// metadata so the viewer can narrate progress.
func internalRenderRunnerResultHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !runnerCallbackAuthorized(r) {
		writeSystemStatusJSON(w, r, http.StatusUnauthorized, map[string]any{
			"ok":    false,
			"error": "runner callback not authorized",
		})
		return
	}
	if kanbanApp == nil {
		writeSystemStatusJSON(w, r, http.StatusServiceUnavailable, map[string]any{
			"ok":    false,
			"error": "artifacts are unavailable",
		})
		return
	}

	var payload renderRunnerCallbackPayload
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, renderCallbackMaxBytes)).Decode(&payload); err != nil {
		writeSystemStatusJSON(w, r, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "could not read render callback",
		})
		return
	}
	artifactID := strings.TrimSpace(payload.ArtifactID)
	if artifactID == "" {
		writeSystemStatusJSON(w, r, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "artifact_id is required",
		})
		return
	}
	existing, exists := kanbanApp.osArtifactByID(artifactID)
	if !exists {
		writeSystemStatusJSON(w, r, http.StatusNotFound, map[string]any{
			"ok":    false,
			"error": "artifact not found",
		})
		return
	}
	// The job-identity check is MANDATORY, never best-effort: the render
	// sidecar is the least-trusted box in the system (it executes untrusted
	// artifact HTML), so a callback binds to an artifact ONLY through the
	// renderJobId stamp the export trigger wrote server-side. A callback with
	// no job_id, an artifact with no pending stamp, or a mismatched id is
	// rejected for EVERY status — a hostile holder of the runner token can
	// neither attach assets to nor scribble progress on arbitrary artifacts.
	expectedJobID := strings.TrimSpace(existing.Metadata["renderJobId"])
	callbackJobID := strings.TrimSpace(payload.JobID)
	if callbackJobID == "" {
		writeSystemStatusJSON(w, r, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "job_id is required",
		})
		return
	}
	if expectedJobID == "" || callbackJobID != expectedJobID {
		writeSystemStatusJSON(w, r, http.StatusConflict, map[string]any{
			"ok":    false,
			"error": "render job does not match artifact",
		})
		return
	}

	status := strings.ToLower(strings.TrimSpace(payload.Status))
	// Kind comes from the server's own enqueue-time stamp, never the callback
	// payload — the sidecar reports, the server decides what the flatten law
	// requires. A missing stamp normalizes to deck, the strict path.
	kind := normalizeRenderJobKind(existing.Metadata["renderKind"])
	// renderJobId is deliberately NOT written from the callback payload: the
	// export trigger is its only setter, so a callback can never re-point the
	// stamp the identity check above depends on. (The success path below does
	// clear it server-side, closing the completed-job replay window.)
	metadata := map[string]string{
		"renderStatus": status,
	}
	if payload.Error != "" {
		metadata["renderError"] = payload.Error
	}

	if status != renderJobStatusComplete {
		if _, _, err := kanbanApp.memory.updateOSArtifactMetadata(artifactID, metadata); err != nil {
			writeSystemStatusJSON(w, r, http.StatusBadRequest, map[string]any{
				"ok":    false,
				"error": err.Error(),
			})
			return
		}
		writeSystemStatusJSON(w, r, http.StatusOK, map[string]any{"ok": true})
		return
	}

	// THE FLATTEN LAW at the trust boundary too: a deck deliverable that is
	// not the flattened raster never lands as an asset.
	if kind == renderJobKindDeck && !payload.Flattened {
		writeSystemStatusJSON(w, r, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "deck exports must be flattened — the layered print never ships",
		})
		return
	}

	pdfBytes, err := renderCallbackPDFBytes(payload)
	if err != nil {
		writeSystemStatusJSON(w, r, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	ref, err := putBlob(pdfBytes, "application/pdf")
	if err != nil {
		writeSystemStatusJSON(w, r, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	assetName := firstNonEmptyString(strings.TrimSpace(existing.Metadata["title"]), "artifact") + ".pdf"
	if _, err := kanbanApp.appendArtifactAsset(artifactID, artifactAsset{
		Ref:  ref,
		Mime: "application/pdf",
		Name: assetName,
		Kind: "pdf",
	}); err != nil {
		writeSystemStatusJSON(w, r, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	if payload.PageCount > 0 {
		metadata["renderPageCount"] = strconv.Itoa(payload.PageCount)
	}
	// Wave 5 item 21: the page JPEGs (previously dropped here) persist as
	// {kind: image} assets — the rendered pages the vision slide juries see.
	// Same path-trust rule as the PDF above; per-page failures degrade to
	// fewer pages inside the helper, never a failed callback.
	if persisted := persistRenderPageImageAssets(kanbanApp, artifactID, payload); persisted > 0 {
		metadata["renderPageImages"] = strconv.Itoa(persisted)
	}
	metadata["renderFlattened"] = strconv.FormatBool(payload.Flattened)
	// Disclosure guard: a deck that flattened to a single page did not paginate
	// (dropped print CSS, or a genuinely one-slide deck). Stamp it and warn so
	// it surfaces instead of shipping silently as a valid multi-slide export.
	if payload.DeckSinglePage {
		metadata["renderDeckSinglePage"] = "true"
		log.Warnf("Render callback: deck artifact %s flattened to a single page — the exported PDF did not paginate (missing print CSS or a one-slide deck)", artifactID)
	}
	// A completed job is spent: clearing the stamp makes the identity check
	// above reject any replay of this callback.
	metadata["renderJobId"] = ""
	if _, _, err := kanbanApp.memory.updateOSArtifactMetadata(artifactID, metadata); err != nil {
		log.Errorf("Failed to stamp render completion on artifact %s: %v", artifactID, err)
	}

	// §5 capture: the export is a deliverable landing, one signal.
	kanbanApp.recordSignalEvent("render_runner", signalEventPDFExported, signalValenceNeutral, artifactID, existing.Metadata["packageId"], map[string]string{
		"jobId":          callbackJobID,
		"kind":           kind,
		"pageCount":      strconv.Itoa(payload.PageCount),
		"flattened":      strconv.FormatBool(payload.Flattened),
		"deckSinglePage": strconv.FormatBool(payload.DeckSinglePage),
	})
	// Let open viewers see the new asset without a reload (the codex
	// callback's memory fan-out).
	broadcastSignedInKanbanEvent("memory", kanbanApp.memorySnapshotForClients(20))

	writeSystemStatusJSON(w, r, http.StatusOK, map[string]any{
		"ok":  true,
		"ref": ref,
	})
}

func meetingArchiveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// archives hold full meeting transcripts; gate listing and files behind a
	// per-archive HMAC token (room password accepted as a fallback for links
	// assembled client-side). clients get a tokenized URL in the
	// meeting_archived payload.
	archiveID := strings.TrimPrefix(r.URL.Path, "/archives/")
	if !validArchiveKey(archiveID, r.URL.Query().Get("key")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	archivePath, err := meetingArchivePath(archiveID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := os.Stat(archivePath); err != nil {
		http.NotFound(w, r)
		return
	}

	filename := filepath.Base(archivePath)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	http.ServeFile(w, r, archivePath)
}

func participantsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "room state is unavailable")
		return
	}
	if userFromRequest(r) == nil {
		// login-gate presence hint (D8): signed-out callers get the seat
		// count and nothing else — no names, no media state, no capacity.
		// The occupancy signal itself was accepted by the v1 audit.
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"occupiedSeats": kanbanApp.activeParticipantCount(officeRoomID),
		}); err != nil {
			log.Errorf("Failed to encode presence summary: %v", err)
		}
		return
	}

	// Members may ask about any room (?room=<id>); the bare call keeps the
	// legacy office shape. Named rooms are never enumerable pre-auth — the
	// signed-out branch above returns before this.
	roomID := normalizeRoomID(r.URL.Query().Get("room"))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(kanbanApp.roomSnapshotForRoom(roomID)); err != nil {
		log.Errorf("Failed to encode participant snapshot: %v", err)
	}
}

func clientConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if userFromRequest(r) == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(nativeRoomClientConfig()); err != nil {
		log.Errorf("Failed to encode client config: %v", err)
	}
}

func nativeClientConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Session-gated (multi-room §5.3): this payload carries the full member
	// roster, which must not be readable unauthenticated once guest links put
	// outsiders on this origin. The native app owns an account session.
	if userFromRequest(r) == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"protocolVersion": nativeClientProtocolV1,
		"auth": map[string]any{
			"mode":       "cookie",
			"loginPath":  "/auth/login",
			"mePath":     "/auth/me",
			"logoutPath": "/auth/logout",
		},
		"room": map[string]any{
			"clientConfigPath": "/client-config",
			"websocketPath":    "/websocket",
			"participants":     nativeRosterParticipants(),
			"maxParticipants":  configuredMeetingRoomCapacity(),
		},
	}); err != nil {
		log.Errorf("Failed to encode native client config: %v", err)
	}
}

func assistantQueryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}

	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "assistant is unavailable")
		return
	}

	payload := struct {
		Query   string                 `json:"query"`
		Mode    string                 `json:"mode"`
		History []scoutChatTurnPayload `json:"history"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read assistant query")
		return
	}

	query := strings.TrimSpace(payload.Query)
	if query == "" {
		writeAuthError(w, http.StatusBadRequest, "query is required")
		return
	}

	mode := normalizeOSAssistantMode(payload.Mode)
	result, err := kanbanApp.resolveAssistantQueryContext(r.Context(), query, scoutChatHistoryFromPayload(payload.History))
	if err != nil {
		log.Errorf("Failed to answer OS assistant query for %s: %v", user.Email, err)
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	result = buildOSAssistantModeAnswer(mode, result, kanbanApp.snapshotState(), kanbanApp.memorySnapshotForClients(12))

	response := map[string]any{
		"ok":           true,
		"query":        result.query,
		"answer":       result.answer,
		"source":       result.source,
		"matchedCards": result.matchedCards,
		"matches":      result.matches,
		"context":      result.contextSize,
		"mode":         mode,
		"user":         user.Name,
	}
	var artifact meetingMemoryEntry
	if mode != "chat" {
		var appended bool
		var artifactErr error
		artifact, appended, artifactErr = kanbanApp.createOSArtifact(mode, result.query, result.answer, user.Name)
		if artifactErr != nil {
			log.Errorf("Failed to save OS artifact for %s: %v", user.Email, artifactErr)
			response["artifactSaved"] = false
			response["artifactError"] = artifactErr.Error()
		} else if strings.TrimSpace(artifact.ID) != "" {
			response["artifact"] = artifact
			response["artifactSaved"] = appended
		}
	}
	response["actions"] = kanbanApp.osAssistantActions(result.query, mode, artifact)

	writeAuthJSON(w, http.StatusOK, response)
}

func assistantThreadsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}

	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "assistant is unavailable")
		return
	}

	payload := struct {
		Query string `json:"query"`
		Mode  string `json:"mode"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read assistant thread request")
		return
	}

	thread, err := kanbanApp.launchAgentThreadWithOrigin(payload.Mode, payload.Query, user.Name, map[string]string{
		"originKind":  agentThreadOriginTool,
		"requestedBy": normalizeAccountEmail(user.Email),
	})
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeAuthJSON(w, http.StatusAccepted, map[string]any{
		"ok":       true,
		"thread":   thread,
		"artifact": thread.Artifact,
		"actions":  thread.Actions,
	})
}

// assistantGoalHandler is the /goal text door: the composer's "/goal
// <objective>" parser and (later) the quick-select palette POST here to emit the
// SAME goal spec the voice initiate_goal tool does. The goal always launches as
// the signed-in requester, and it can NEVER set external_write — that authority
// is earned only at the ship gate with admin approval. Same origin+session
// gates as assistantThreadsHandler.
func assistantGoalHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}

	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "assistant is unavailable")
		return
	}

	payload := struct {
		Objective     string `json:"objective"`
		Package       string `json:"package"`
		PackageID     string `json:"packageId"`
		ToolTemplate  string `json:"toolTemplate"`
		AuthorityHint string `json:"authorityHint"`
		OriginSurface string `json:"originSurface"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read goal request")
		return
	}
	if strings.TrimSpace(payload.Objective) == "" {
		writeAuthError(w, http.StatusBadRequest, "objective is required")
		return
	}

	// Clamp authority exactly like the voice tool: never external_write.
	authority := codexJobAuthorityWorkspaceWrite
	if strings.EqualFold(strings.TrimSpace(payload.AuthorityHint), "read_only") {
		authority = codexJobAuthorityReadOnly
	}

	origin := map[string]string{}
	if surface := strings.TrimSpace(payload.OriginSurface); surface != "" {
		origin["originSurface"] = surface
	}

	// The palette Run form and the voice initiate_goal path both send "package";
	// accept "packageId" as an alias so the binder/library doors can reuse the
	// same door without a second field name.
	packageID := strings.TrimSpace(payload.Package)
	if packageID == "" {
		packageID = strings.TrimSpace(payload.PackageID)
	}

	thread, err := kanbanApp.launchGoalThread(goalLaunchSpec{
		Objective:    payload.Objective,
		CreatedBy:    user.Email,
		Authority:    authority,
		PackageID:    packageID,
		ToolTemplate: strings.TrimSpace(payload.ToolTemplate),
		Origin:       origin,
	})
	if err != nil {
		if errors.Is(err, errAgentWorkerNotConfigured) {
			writeAuthError(w, http.StatusServiceUnavailable, "the goal engine is not configured here")
			return
		}
		// Per-user in-flight cap: 429 with the blocking goals (id+title) so the
		// UI can render "finish these first" instead of a bare failure.
		var capErr *errGoalUserCapExceeded
		if errors.As(err, &capErr) {
			writeAuthJSON(w, http.StatusTooManyRequests, map[string]any{
				"ok":       false,
				"error":    capErr.Error(),
				"cap":      capErr.Cap,
				"inFlight": capErr.Goals,
			})
			return
		}
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeAuthJSON(w, http.StatusAccepted, map[string]any{
		"ok":       true,
		"thread":   thread,
		"artifact": thread.Artifact,
		"actions":  thread.Actions,
	})
}

// assistantGoalCancelHandler is the one-tap misfire escape (spec §2 "misfire
// economics", Wave 2 item 8c): POST {goalId} parks a running goal at
// needs_attention, halts further subtask dispatch, and frees the requester's
// in-flight cap slot — a wrong launch costs one tap, not six subtasks. Same
// origin+session gates as assistantGoalHandler. Permitted to the goal's own
// requester (requestedBy) or the approval admin, mirroring
// artifactRunnerActionHandler's authorization split: cancel is the requester's
// escape hatch, never a way for one teammate to kill another's running goal.
func assistantGoalCancelHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}

	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "assistant is unavailable")
		return
	}

	payload := struct {
		GoalID string `json:"goalId"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read goal cancel request")
		return
	}
	goalID := strings.TrimSpace(payload.GoalID)
	if goalID == "" {
		writeAuthError(w, http.StatusBadRequest, "goal id is required")
		return
	}
	artifact, found := kanbanApp.osArtifactByID(goalID)
	if !found {
		writeAuthError(w, http.StatusNotFound, "goal not found")
		return
	}
	if artifact.Metadata["mode"] != "goal" {
		writeAuthError(w, http.StatusBadRequest, "artifact is not a goal")
		return
	}
	requester := normalizeAccountEmail(artifact.Metadata["requestedBy"])
	if !isArtifactApprovalAdmin(user) && (requester == "" || normalizeAccountEmail(user.Email) != requester) {
		writeAuthError(w, http.StatusForbidden, "only the goal's requester or an admin can cancel it")
		return
	}

	if err := kanbanApp.cancelGoalThread(goalID, user.Email); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, _ := kanbanApp.osArtifactByID(goalID)
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"artifact": updated,
	})
}

// assistantThreadFollowUpHandler is the headless follow-up trigger (package
// binder / artifact library): POST {artifactId, text} re-runs an existing
// agent-thread artifact in place. Same origin+session gates as
// assistantThreadsHandler; any signed-in user.
func assistantThreadFollowUpHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}

	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "assistant is unavailable")
		return
	}

	payload := struct {
		ArtifactID string `json:"artifactId"`
		Text       string `json:"text"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read follow-up request")
		return
	}

	thread, err := kanbanApp.dispatchArtifactFollowUp(payload.ArtifactID, payload.Text, user.Name, nil)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeAuthJSON(w, http.StatusAccepted, map[string]any{
		"ok":       true,
		"thread":   thread,
		"artifact": thread.Artifact,
		"actions":  thread.Actions,
	})
}

// shouldServeIndexHTML gates the "/" catch-all: the root path and text/html
// navigations get the SPA; everything else (unknown /assistant/* endpoints,
// fetch() calls to API-ish paths) gets a real 404.
func shouldServeIndexHTML(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	path := r.URL.Path
	if path == "/" || path == "/index.html" {
		return true
	}
	for _, apiPrefix := range []string{"/assistant/", "/auth/", "/api/", "/internal/", "/artifacts/", "/brain/"} {
		if strings.HasPrefix(path, apiPrefix) {
			return false
		}
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// assistantBoardHandler serves the kanban board snapshot to any authenticated
// session. Reads must not require joining the video call: the board state is
// server-side, and office/chat sessions have no room websocket. Writes and
// board editing keep their existing room-scoped gates.
func assistantBoardHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "board is unavailable")
		return
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"board": kanbanApp.snapshotState(),
	})
}

// assistantMemoryHandler serves the memory timeline (same projection the room
// websocket pushes) to any authenticated session, so the memory tool works
// without joining a call.
func assistantMemoryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "memory is unavailable")
		return
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"memory": kanbanApp.memorySnapshotForClients(20),
	})
}

func assistantRealtimeOfferHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}

	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "assistant is unavailable")
		return
	}
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		writeAuthError(w, http.StatusServiceUnavailable, "OpenAI Realtime is not configured")
		return
	}
	// §7.3 (W4): the private voice binds to the caller's CURRENT room and a
	// listen-only sitting refuses it — a member seated in a guest-exposed room
	// gets no Scout voice, and can never attach the assistant to another
	// room's context (the old room-agnostic hole).
	if kanbanApp.sittingListenOnly(kanbanApp.memberCurrentRoom(user.Email)) {
		writeAuthError(w, http.StatusForbidden, "Scout voice is off while this room is listen-only")
		return
	}

	payload := struct {
		SDP string `json:"sdp"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read realtime offer")
		return
	}

	offerSDP := payload.SDP
	if strings.TrimSpace(offerSDP) == "" {
		writeAuthError(w, http.StatusBadRequest, "sdp is required")
		return
	}

	answerSDP, err := kanbanApp.createPrivateRealtimeVoiceCall(apiKey, realtimeModel(), offerSDP)
	if err != nil {
		log.Errorf("Failed to create private Realtime voice call for %s: %v", user.Email, err)
		if message, status, ok := openAIAPIRequestUserMessage(err); ok {
			writeAuthError(w, status, "Scout voice is unavailable: "+message)
			return
		}
		writeAuthError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":  true,
		"sdp": answerSDP,
	})
}

func assistantRealtimeToolHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}

	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "assistant is unavailable")
		return
	}

	// §7.3 (W4): tools ride the same room binding as the offer — a caller
	// seated in a listen-only room gets no assistant actions.
	if kanbanApp.sittingListenOnly(kanbanApp.memberCurrentRoom(user.Email)) {
		writeAuthError(w, http.StatusForbidden, "Scout is off while this room is listen-only")
		return
	}

	payload := struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read realtime tool request")
		return
	}

	result, changed, err := kanbanApp.applyPrivateRealtimeVoiceTool(user.Email, payload.Name, payload.Arguments)
	ok := err == nil
	if err != nil {
		result = map[string]any{
			"ok":    false,
			"error": err.Error(),
		}
		log.Errorf("Private Realtime tool %q failed for %s: %v", payload.Name, user.Email, err)
	}

	// RW1 (kanban-card-108): a private-dashboard Scout-voice board mutation has
	// to reach every signed-in client the way the room-voice path (the
	// applyToolCallArgs caller at kanban.go ~2861) and manual ws edits
	// (broadcastManualBoardMutation) already do — otherwise the office board only
	// picks up the change on a manual browser reload. The board/undo snapshots are
	// idempotent, so the harmless extra snapshot from a non-board changed tool
	// (create_package etc.) is sanctioned by the fan-out doctrine; changed is
	// never true on the error path. refreshRealtimeBoardContext refreshes only the
	// OFFICE realtime session (safe — the private session is browser-owned).
	if changed {
		broadcastSignedInKanbanEvent("board", kanbanApp.snapshotState())
		broadcastSignedInKanbanEvent("undo_available", kanbanApp.canUndoDelete())
		kanbanApp.refreshRealtimeBoardContext(payload.Name)
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":       ok,
		"changed":  changed,
		"result":   result,
		"actions":  result["actions"],
		"artifact": result["artifact"],
	})
}

const artifactLibraryAdminEmail = "aj@shareability.com"

// isArtifactApprovalAdmin gates ONLY the external-write approval actions on
// Codex artifacts (approve/reject). Artifact bodies, listing, editing, and
// publishing are readable/writable by every signed-in user — the trust
// boundary is the seeded team, and publish/update stamping keeps the audit
// trail.
func isArtifactApprovalAdmin(user *userAccount) bool {
	return user != nil && normalizeAccountEmail(user.Email) == artifactLibraryAdminEmail
}

// artifactListExcerptRunes bounds the body carried in an /artifacts LIST
// response. Full bodies turn the list into a multi-megabyte payload that stalls
// first paint and only grows as users accumulate months of artifacts (measured
// live: 100 artifacts = 5.1 MB, one packaging deck alone 2.6 MB of base64
// imagery). The list ships this excerpt — enough for card teasers and for
// deck-sniffing the leading "<!doctype" — and the client fetches the full body
// on demand via GET /artifacts?id=<id> only when a deliverable is opened.
const artifactListExcerptRunes = 1500

// artifactListMetaFieldCap bounds the free-text metadata fields the list carries.
// query / threadQuery / objective are near-duplicate copies of the user's
// request (measured live: 1.3 MB across 100 artifacts, one objective 35 KB) and
// are only needed at full length by the reader, which fetches the full entry via
// ?id=. Capping them is safe: they serve only as short title fallbacks in the
// list. goalPlan/workflowStages are structured and drive goalcard rendering, so
// they are NOT capped.
const artifactListMetaFieldCap = 300

var artifactListHeavyMetaFields = []string{"query", "threadQuery", "objective"}

// artifactListView returns lightweight COPIES of the given artifacts for a list
// response: an entry whose body exceeds the excerpt is trimmed to the leading
// runes, and its heavy free-text metadata fields are capped. Anything trimmed is
// flagged bodyTrimmed so the client fetches the full entry (body AND full
// metadata) via ?id= on open. It never mutates the stored entries or their
// metadata maps (the memory store owns them; search/context/recall still need
// the full values), so the metadata map is deep-copied before anything changes.
func artifactListView(entries []meetingMemoryEntry) []meetingMemoryEntry {
	out := make([]meetingMemoryEntry, len(entries))
	for i, entry := range entries {
		trimmedBody := false
		text := entry.Text
		if runes := []rune(entry.Text); len(runes) > artifactListExcerptRunes {
			text = string(runes[:artifactListExcerptRunes])
			trimmedBody = true
		}
		meta := make(map[string]string, len(entry.Metadata)+1)
		trimmedMeta := false
		for key, value := range entry.Metadata {
			meta[key] = value
		}
		for _, field := range artifactListHeavyMetaFields {
			if runes := []rune(meta[field]); len(runes) > artifactListMetaFieldCap {
				meta[field] = string(runes[:artifactListMetaFieldCap])
				trimmedMeta = true
			}
		}
		if !trimmedBody && !trimmedMeta {
			out[i] = entry
			continue
		}
		meta["bodyTrimmed"] = "true"
		copyEntry := entry
		copyEntry.Text = text
		copyEntry.Metadata = meta
		out[i] = copyEntry
	}
	return out
}

func artifactsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPatch {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}

	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "artifacts are unavailable")
		return
	}

	if r.Method == http.MethodPatch {
		payload := struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			Text      string `json:"text"`
			Published *bool  `json:"published"`
		}{}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&payload); err != nil {
			writeAuthError(w, http.StatusBadRequest, "could not read artifact update")
			return
		}
		metadata := map[string]string{}
		if payload.Published != nil {
			if *payload.Published {
				metadata["published"] = "true"
				metadata["status"] = "published"
				metadata["publishedBy"] = user.Name
				metadata["publishedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
			} else {
				metadata["published"] = "false"
				metadata["status"] = "draft"
				metadata["unpublishedBy"] = user.Name
				metadata["unpublishedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
			}
		}
		// Signal capture (signals.go): snapshot the prior body BEFORE the update
		// so a real human edit can store its section-level diff summary.
		prior, hadPrior := kanbanApp.osArtifactByID(payload.ID)
		artifact, updated, err := kanbanApp.updateOSArtifactWithMetadata(payload.ID, payload.Title, payload.Text, user.Name, metadata)
		if err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			}
			writeAuthError(w, status, err.Error())
			return
		}
		if updated && hadPrior && prior.Text != artifact.Text {
			kanbanApp.recordSignalEvent(user.Name, signalEventArtifactEdited, signalValenceNeutral, artifact.ID, artifact.Metadata["packageId"], summarizeArtifactDiff(prior.Text, artifact.Text))
		}
		if updated && hadPrior && payload.Published != nil && *payload.Published && !artifactIsPublished(prior) {
			kanbanApp.recordSignalEvent(user.Name, signalEventArtifactPublished, signalValencePositive, artifact.ID, artifact.Metadata["packageId"], nil)
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"updated":  updated,
			"artifact": artifact,
		})
		return
	}

	// Single-artifact window (additive, like pagination): ?id=<artifact-id>
	// returns exactly that artifact in the same {artifacts: [...]} shape. The
	// newest-100 default window drops a goal parent buried under 100+ of its
	// own stage children; the mounted goalcard fetches it here so a parked
	// checkpoint still renders its choices.
	if wantID := strings.TrimSpace(r.URL.Query().Get("id")); wantID != "" {
		artifact, found := kanbanApp.osArtifactByID(wantID)
		if !found {
			writeAuthError(w, http.StatusNotFound, "artifact not found")
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"artifacts": []meetingMemoryEntry{artifact},
		})
		return
	}

	// Cursor pagination (spec §4, Wave 3 item 13) — additive params only. A
	// bare GET keeps today's exact response shape so the existing UI works
	// unchanged; ?before=<artifact-id>&limit=<n> opens an older window for
	// history scrollback. Windows preserve snapshot order (oldest → newest),
	// matching what the client already renders.
	beforeID := strings.TrimSpace(r.URL.Query().Get("before"))
	limitParam := strings.TrimSpace(r.URL.Query().Get("limit"))
	if beforeID == "" && limitParam == "" {
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok":                 true,
			"artifacts":          artifactListView(kanbanApp.osArtifactsSnapshot(100)),
			"publishedArtifacts": artifactListView(kanbanApp.publishedOSArtifactsSnapshot(10)),
		})
		return
	}

	limit := 100
	if limitParam != "" {
		parsed, err := strconv.Atoi(limitParam)
		if err != nil || parsed < 1 {
			writeAuthError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		if parsed > 500 {
			parsed = 500
		}
		limit = parsed
	}

	artifacts := kanbanApp.osArtifactsSnapshot(0)
	end := len(artifacts)
	if beforeID != "" {
		end = -1
		for index, artifact := range artifacts {
			if artifact.ID == beforeID {
				end = index
				break
			}
		}
		if end < 0 {
			writeAuthError(w, http.StatusNotFound, "cursor artifact not found")
			return
		}
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	window := artifacts[start:end]

	payload := map[string]any{
		"ok":                 true,
		"artifacts":          artifactListView(window),
		"publishedArtifacts": artifactListView(kanbanApp.publishedOSArtifactsSnapshot(10)),
		"hasMore":            start > 0,
	}
	if start > 0 && len(window) > 0 {
		// nextBefore is the oldest id in this window — pass it back as
		// ?before= to continue into strictly older artifacts.
		payload["nextBefore"] = window[0].ID
	}
	writeAuthJSON(w, http.StatusOK, payload)
}

func publicAssetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if strings.HasPrefix(r.URL.Path, "/public/video-blur/") {
		// Version-pinned, immutable ML segmentation assets (~9 MB wasm + model). Cache
		// them hard so a blur user fetches the payload once, not per visit — everything
		// else stays no-store.
		w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.HasSuffix(r.URL.Path, ".webmanifest") {
		// Go's mime table has no .webmanifest entry; with nosniff set the
		// PWA manifest must declare its type explicitly.
		w.Header().Set("Content-Type", "application/manifest+json")
	}
	http.StripPrefix("/public/", http.FileServer(http.Dir("public"))).ServeHTTP(w, r)
}

// serviceWorkerHandler serves public/sw.js from the origin root so the worker's
// scope is "/". no-store keeps a redeployed worker from being pinned by an
// HTTP cache; the worker's own body caches nothing, so the app shell stays as
// no-store as index.html itself.
func serviceWorkerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, "public/sw.js")
}

func newPeerConnection() (*webrtc.PeerConnection, error) {
	return newPeerConnectionWithConfiguration(webrtc.Configuration{})
}

func newPeerConnectionWithConfiguration(configuration webrtc.Configuration) (*webrtc.PeerConnection, error) {
	settingEngine := webrtc.SettingEngine{}
	if nat1To1IP := os.Getenv("PION_NAT1TO1_IP"); nat1To1IP != "" {
		settingEngine.SetNAT1To1IPs([]string{nat1To1IP}, webrtc.ICECandidateTypeHost)
	}
	if err := configureEphemeralUDPPortRange(&settingEngine); err != nil {
		return nil, err
	}

	mediaEngine, registry, err := stableRoomMediaEngine()
	if err != nil {
		return nil, err
	}

	return webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(registry),
		webrtc.WithSettingEngine(settingEngine),
	).NewPeerConnection(configuration)
}

// newRoomPeerConnection builds the server side of a room participant session.
// Unlike newPeerConnection (also used for the OpenAI Realtime peer, which
// must not wait on relay gathering before its non-trickle handshake), room
// connections include the configured TURN servers so the server itself offers
// relay candidates. Historically the server offered only host candidates
// (public VPS IP via PION_NAT1TO1_IP): desktop networks reach those fine, but
// mobile networks that filter inbound-unrelated UDP to arbitrary high ports
// never complete a candidate pair even though the browser holds valid TURN
// credentials — the browser-relay<->server-host pair is the only option and
// some carrier NATs drop it. With server-side relay candidates the pair
// browser<->server-relay routes via coturn's well-known port from both ends.
// Browsers use trickle ICE here, so the extra relay gathering never delays
// signaling; if TURN is unreachable pion simply proceeds with host candidates.
func newRoomPeerConnection() (*webrtc.PeerConnection, error) {
	iceServers := serverICEServersFromEnv()
	if len(iceServers) == 0 {
		return newPeerConnection()
	}

	peerConnection, err := newPeerConnectionWithConfiguration(webrtc.Configuration{ICEServers: iceServers})
	if err != nil {
		// A malformed MEETING_TURN_URLS entry fails pion's constructor-time URL
		// validation even though browsers may tolerate it. Never let that take
		// the room down — fall back to the host-only behaviour we shipped with.
		log.Errorf("Failed to create room PeerConnection with server-side TURN relay (%v); falling back to host-only ICE", err)
		return newPeerConnection()
	}

	return peerConnection, nil
}

// serverICEServersFromEnv resolves the TURN servers the SERVER dials for its
// own relay candidates. It reuses the browser-facing MEETING_TURN_URLS and the
// ephemeral HMAC credential mint, so no new deployment configuration is
// required. Returns nil (host-only ICE, the pre-existing behaviour) when TURN
// is unconfigured, when credentials cannot be minted (pion rejects TURN URLs
// without credentials at construction), or when explicitly opted out via
// MEETING_DISABLE_SERVER_TURN.
func serverICEServersFromEnv() []webrtc.ICEServer {
	if boolEnv("MEETING_DISABLE_SERVER_TURN") {
		return nil
	}
	turnURLs := splitEnvList("MEETING_TURN_URLS")
	if len(turnURLs) == 0 {
		return nil
	}
	username, credential := turnCredentialsFromEnv()
	if username == "" || credential == "" {
		return nil
	}

	return []webrtc.ICEServer{{
		URLs:       turnURLs,
		Username:   username,
		Credential: credential,
	}}
}

// Room RTP codecs. stableRoomMediaEngine registers exactly this set, and
// preferSourceTrackCodec references the video primary+RTX pairs so a
// subscriber-facing SetCodecPreferences advertises the identical payload types
// the engine negotiated. A single definition keeps the two sites from drifting:
// SetCodecPreferences rejects any codec whose parameters don't match a
// registered one, and silently drops an RTX codec whose apt payload type has no
// primary alongside it in the list.
//
// RTX repair codecs (RFC 4588) are what let a subscriber recover a lost packet
// via NACK instead of a full keyframe. Without an apt codec registered, pion
// strips RTX from browser offers (publisher uplink loss is PLI-only — every
// prod track logged has_rtx=false) and never allocates repair SSRCs on its own
// senders, so the NACK responder would retransmit on the media SSRC instead of
// a proper repair stream. TrackRemote.ReadRTP unwraps inbound RTX transparently,
// so the forwarding pump needs no changes.
var (
	roomOpusCodec = webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   48000,
			Channels:    2,
			SDPFmtpLine: "minptime=10;useinbandfec=1",
		},
		PayloadType: 111,
	}
	roomH264Codec = webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeH264,
			ClockRate:    90000,
			SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
			RTCPFeedback: []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}, {Type: "goog-remb"}},
		},
		PayloadType: 102,
	}
	roomH264RTXCodec = webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeRTX,
			ClockRate:   90000,
			SDPFmtpLine: "apt=102",
		},
		PayloadType: 103,
	}
	roomVP8Codec = webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeVP8,
			ClockRate:    90000,
			RTCPFeedback: []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}, {Type: "goog-remb"}},
		},
		PayloadType: 96,
	}
	roomVP8RTXCodec = webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeRTX,
			ClockRate:   90000,
			SDPFmtpLine: "apt=96",
		},
		PayloadType: 97,
	}
)

func stableRoomMediaEngine() (*webrtc.MediaEngine, *interceptor.Registry, error) {
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterCodec(roomOpusCodec, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, nil, fmt.Errorf("register opus codec: %w", err)
	}
	if err := mediaEngine.RegisterCodec(roomH264Codec, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, nil, fmt.Errorf("register h264 codec: %w", err)
	}
	if err := mediaEngine.RegisterCodec(roomH264RTXCodec, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, nil, fmt.Errorf("register h264 rtx codec: %w", err)
	}
	if err := mediaEngine.RegisterCodec(roomVP8Codec, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, nil, fmt.Errorf("register vp8 codec: %w", err)
	}
	if err := mediaEngine.RegisterCodec(roomVP8RTXCodec, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, nil, fmt.Errorf("register vp8 rtx codec: %w", err)
	}

	registry := &interceptor.Registry{}
	// Mirror webrtc.RegisterDefaultInterceptors, but register NACK ourselves with
	// an explicit, bounded retransmission buffer (see configureRoomNack) instead of
	// the library default. Everything else matches the upstream default set so we
	// keep RTCP reports, simulcast header extensions, stats, and TWCC congestion
	// feedback intact.
	if err := configureRoomNack(mediaEngine, registry); err != nil {
		return nil, nil, fmt.Errorf("configure nack: %w", err)
	}
	if err := webrtc.ConfigureRTCPReports(registry); err != nil {
		return nil, nil, fmt.Errorf("configure rtcp reports: %w", err)
	}
	if err := webrtc.ConfigureSimulcastExtensionHeaders(mediaEngine); err != nil {
		return nil, nil, fmt.Errorf("configure simulcast extension headers: %w", err)
	}
	if err := webrtc.ConfigureStatsInterceptor(registry); err != nil {
		return nil, nil, fmt.Errorf("configure stats interceptor: %w", err)
	}
	if err := webrtc.ConfigureTWCCSender(mediaEngine, registry); err != nil {
		return nil, nil, fmt.Errorf("configure twcc sender: %w", err)
	}

	return mediaEngine, registry, nil
}

// defaultNackResponderPackets is the per-stream retransmission buffer depth used
// when ROOM_NACK_BUFFER_PACKETS is unset. 1024 packets matches Pion's default and,
// at a ~1200-byte MTU, bounds the responder buffer to ~1.2 MiB per active outbound
// stream — recent enough to satisfy NACK-driven retransmission for typical RTT.
const defaultNackResponderPackets uint16 = 1024

// configureRoomNack registers the NACK generator (so the SFU requests missing
// packets from publishers) and a responder whose send buffer is explicitly sized.
// The responder retains recent outbound RTP packets so subscribers can recover
// losses via NACK; sizing it explicitly (rather than relying on the library
// default) makes the worst-case memory bound a documented, tunable knob and
// guards against unbounded growth.
func configureRoomNack(mediaEngine *webrtc.MediaEngine, registry *interceptor.Registry) error {
	responder, err := nack.NewResponderInterceptor(nack.ResponderSize(roomNackResponderSize()))
	if err != nil {
		return fmt.Errorf("create nack responder: %w", err)
	}
	generator, err := nack.NewGeneratorInterceptor()
	if err != nil {
		return fmt.Errorf("create nack generator: %w", err)
	}

	mediaEngine.RegisterFeedback(webrtc.RTCPFeedback{Type: "nack"}, webrtc.RTPCodecTypeVideo)
	mediaEngine.RegisterFeedback(webrtc.RTCPFeedback{Type: "nack", Parameter: "pli"}, webrtc.RTPCodecTypeVideo)
	registry.Add(responder)
	registry.Add(generator)

	return nil
}

// roomNackResponderSize resolves the retransmission buffer depth from
// ROOM_NACK_BUFFER_PACKETS. The Pion responder requires a power of two in
// [1, 32768]; any unset, unparseable, out-of-range, or non-power-of-two value
// falls back to defaultNackResponderPackets so a bad config can never enlarge the
// buffer without bound or fail peer-connection setup.
func roomNackResponderSize() uint16 {
	raw := strings.TrimSpace(os.Getenv("ROOM_NACK_BUFFER_PACKETS"))
	if raw == "" {
		return defaultNackResponderPackets
	}

	parsed, err := strconv.ParseUint(raw, 10, 16)
	if err != nil || parsed == 0 || parsed > 32768 || parsed&(parsed-1) != 0 {
		log.Errorf("Ignoring invalid ROOM_NACK_BUFFER_PACKETS=%q (must be a power of two in [1, 32768]); using %d", raw, defaultNackResponderPackets)
		return defaultNackResponderPackets
	}

	return uint16(parsed)
}

func configureEphemeralUDPPortRange(settingEngine *webrtc.SettingEngine) error {
	rawPortRange := strings.TrimSpace(os.Getenv("PION_UDP_PORT_RANGE"))
	if rawPortRange == "" {
		return nil
	}

	parts := strings.Split(rawPortRange, "-")
	if len(parts) != 2 {
		return fmt.Errorf("PION_UDP_PORT_RANGE must be formatted as min-max, got %q", rawPortRange)
	}

	minPort, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 16)
	if err != nil {
		return fmt.Errorf("parse PION_UDP_PORT_RANGE minimum: %w", err)
	}
	maxPort, err := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 16)
	if err != nil {
		return fmt.Errorf("parse PION_UDP_PORT_RANGE maximum: %w", err)
	}

	if err := settingEngine.SetEphemeralUDPPortRange(uint16(minPort), uint16(maxPort)); err != nil {
		return fmt.Errorf("configure PION_UDP_PORT_RANGE: %w", err)
	}

	return nil
}

func websocketOriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}

	for _, allowedOrigin := range strings.Split(os.Getenv("MEETING_ALLOWED_ORIGINS"), ",") {
		if strings.EqualFold(strings.TrimSpace(allowedOrigin), origin) {
			return true
		}
	}

	parsedOrigin, err := url.Parse(origin)
	if err != nil {
		return false
	}

	return strings.EqualFold(parsedOrigin.Host, r.Host)
}

// Add to list of tracks. Callers publish track metadata before renegotiating.
func addTrack(roomID string, t *webrtc.TrackRemote, participantName string, sessionID string) (*webrtc.TrackLocalStaticRTP, error) { // nolint
	// Create a new TrackLocal with the same codec as our incoming
	trackLocal, err := webrtc.NewTrackLocalStaticRTP(t.Codec().RTPCodecCapability, forwardedRemoteTrackID(t), t.StreamID())
	if err != nil {
		return nil, err
	}

	listLock.Lock()
	if trackLocals == nil {
		trackLocals = map[string]*webrtc.TrackLocalStaticRTP{}
	}
	if trackParticipants == nil {
		trackParticipants = map[string]string{}
	}
	if trackParticipantSessions == nil {
		trackParticipantSessions = map[string]string{}
	}
	if trackRooms == nil {
		trackRooms = map[string]string{}
	}
	if trackSourceIDs == nil {
		trackSourceIDs = map[string]string{}
	}
	if trackLayerRIDs == nil {
		trackLayerRIDs = map[string]string{}
	}
	if trackLayerGroups == nil {
		trackLayerGroups = map[string]string{}
	}
	groupKey := layerGroupKey(t.StreamID(), t.ID())
	reapStaleLayerTwinsLocked(groupKey, t.RID(), trackLocal.ID())
	trackLocals[trackLocal.ID()] = trackLocal
	trackParticipants[trackLocal.ID()] = canonicalRoomParticipantName(participantName)
	trackParticipantSessions[trackLocal.ID()] = sessionID
	trackRooms[trackLocal.ID()] = normalizeRoomID(roomID)
	trackSourceIDs[trackLocal.ID()] = t.ID()
	trackLayerRIDs[trackLocal.ID()] = t.RID()
	trackLayerGroups[trackLocal.ID()] = groupKey
	totalTracks, audioTracks, videoTracks := forwardedTrackCountsLocked()
	listLock.Unlock()

	codec := t.Codec()
	log.Infof("room_track_added participant=%s session=%s kind=%s track_id=%s source_track_id=%s stream_id=%s rid=%q ssrc=%d payload_type=%d codec=%s clock_rate=%d channels=%d fmtp=%q feedback=%s total_tracks=%d audio_tracks=%d video_tracks=%d",
		canonicalRoomParticipantName(participantName), sessionID, t.Kind(), trackLocal.ID(), t.ID(), t.StreamID(), t.RID(), t.SSRC(), t.PayloadType(), codec.MimeType, codec.ClockRate, codec.Channels, codec.SDPFmtpLine, rtcpFeedbackSummary(codec.RTCPFeedback), totalTracks, audioTracks, videoTracks)

	return trackLocal, nil
}

// reapStaleLayerTwinsLocked drops the bookkeeping for older forwarded tracks
// that share a new track's source group and RID. After renegotiation or SSRC
// churn the same source re-publishes under a new forwarded ID while the old
// reader stays blocked in ReadRTP for the rest of the session; the newest track
// must win or subscribers keep a frozen ghost tile. Callers must hold listLock
// and renegotiate (signalPeerConnections) afterwards.
func reapStaleLayerTwinsLocked(groupKey string, rid string, keepTrackID string) {
	for staleID, group := range trackLayerGroups {
		if staleID == keepTrackID || group != groupKey || trackLayerRIDs[staleID] != rid {
			continue
		}
		delete(trackLocals, staleID)
		delete(trackParticipants, staleID)
		delete(trackParticipantSessions, staleID)
		delete(trackRooms, staleID)
		delete(trackSourceIDs, staleID)
		delete(trackLayerRIDs, staleID)
		delete(trackLayerGroups, staleID)
		subscriberKeyframeThrottle.forget(staleID)
	}
}

func participantTrackPayload(name string, t *webrtc.TrackRemote) map[string]any {
	return map[string]any{
		"name":          canonicalRoomParticipantName(name),
		"kind":          t.Kind().String(),
		"trackId":       forwardedRemoteTrackID(t),
		"sourceTrackId": t.ID(),
		"streamId":      t.StreamID(),
	}
}

func forwardedRemoteTrackID(t *webrtc.TrackRemote) string {
	return forwardedTrackLocalID(t.StreamID(), t.ID(), uint32(t.SSRC()))
}

func forwardedTrackLocalID(streamID string, trackID string, ssrc uint32) string {
	return fmt.Sprintf("%s:%s:%d", mediaIDPart(streamID, "stream"), mediaIDPart(trackID, "track"), ssrc)
}

// layerGroupKey identifies the source media that a set of simulcast layers share.
// Sibling layers carry the same stream and source-track IDs and differ only by
// RID/SSRC, so dropping the SSRC groups them; non-simulcast tracks each form a
// group of one.
func layerGroupKey(streamID string, trackID string) string {
	return fmt.Sprintf("%s:%s", mediaIDPart(streamID, "stream"), mediaIDPart(trackID, "track"))
}

func mediaIDPart(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}

	return strings.Join(strings.Fields(value), "_")
}

func sameParticipantName(a string, b string) bool {
	a = canonicalRoomParticipantName(a)
	b = canonicalRoomParticipantName(b)
	return a != "" && b != "" && strings.EqualFold(a, b)
}

// Remove from list of tracks and fire renegotation for all PeerConnections.
func removeTrack(t *webrtc.TrackLocalStaticRTP) {
	if t == nil {
		return
	}

	var participantName string
	var sessionID string
	var totalTracks, audioTracks, videoTracks int
	listLock.Lock()
	defer func() {
		listLock.Unlock()
		log.Infof("room_track_removed participant=%s session=%s kind=%s track_id=%s total_tracks=%d audio_tracks=%d video_tracks=%d",
			participantName, sessionID, t.Kind(), t.ID(), totalTracks, audioTracks, videoTracks)
		signalPeerConnections()
	}()

	participantName = trackParticipants[t.ID()]
	sessionID = trackParticipantSessions[t.ID()]
	if trackLocals[t.ID()] != t {
		// A same-ID republish already replaced this entry; the stale
		// track's unpublish must not tear down the fresh one.
		return
	}
	delete(trackLocals, t.ID())
	delete(trackParticipants, t.ID())
	delete(trackParticipantSessions, t.ID())
	delete(trackRooms, t.ID())
	delete(trackSourceIDs, t.ID())
	delete(trackLayerRIDs, t.ID())
	delete(trackLayerGroups, t.ID())
	subscriberKeyframeThrottle.forget(t.ID())
	totalTracks, audioTracks, videoTracks = forwardedTrackCountsLocked()
}

func removeParticipantTracksLocked(name string, sessionID string) bool {
	removedTracks := false
	for trackID, participantName := range trackParticipants {
		if !sameParticipantName(participantName, name) {
			continue
		}
		if sessionID != "" && trackParticipantSessions[trackID] != sessionID {
			continue
		}
		delete(trackLocals, trackID)
		delete(trackParticipants, trackID)
		delete(trackParticipantSessions, trackID)
		delete(trackRooms, trackID)
		delete(trackSourceIDs, trackID)
		delete(trackLayerRIDs, trackID)
		delete(trackLayerGroups, trackID)
		subscriberKeyframeThrottle.forget(trackID)
		removedTracks = true
	}

	return removedTracks
}

func participantTrackSnapshots(roomID string, excludeParticipant string) []participantTrackSnapshot {
	listLock.RLock()
	defer listLock.RUnlock()

	return participantTrackSnapshotsLocked(roomID, excludeParticipant)
}

func participantTrackSnapshotsLocked(roomID string, excludeParticipant string) []participantTrackSnapshot {
	// Room isolation must hold server-side (§6.2): the replay mirrors
	// acceptsTrack — only tracks published in the requester's room, each
	// stamped with roomId. Absent room ids mean office on both sides (§9).
	snapshotRoomID := normalizeRoomID(roomID)
	snapshots := make([]participantTrackSnapshot, 0, len(trackLocals))
	for trackID, trackLocal := range trackLocals {
		if trackLocal == nil {
			continue
		}
		if normalizeRoomID(trackRooms[trackID]) != snapshotRoomID {
			continue
		}
		name := canonicalRoomParticipantName(trackParticipants[trackID])
		if sameParticipantName(name, excludeParticipant) {
			continue
		}
		snapshots = append(snapshots, participantTrackSnapshot{
			Name:          name,
			Kind:          trackLocal.Kind().String(),
			TrackID:       trackID,
			SourceTrackID: trackSourceIDs[trackID],
			StreamID:      trackLocal.StreamID(),
			RoomID:        snapshotRoomID,
		})
	}
	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].Name != snapshots[j].Name {
			return snapshots[i].Name < snapshots[j].Name
		}
		if snapshots[i].Kind != snapshots[j].Kind {
			return snapshots[i].Kind < snapshots[j].Kind
		}
		return snapshots[i].TrackID < snapshots[j].TrackID
	})

	return snapshots
}

func sendParticipantTrackSnapshot(websocket *threadSafeWriter, snapshot participantTrackSnapshot) {
	if err := sendKanbanEvent(websocket, "participant_track", snapshot); err != nil {
		log.Errorf("Failed to replay participant track metadata: %v", err)
	}
}

func sendParticipantTrackSnapshots(websocket *threadSafeWriter, roomID string, excludeParticipant string) {
	for _, snapshot := range participantTrackSnapshots(roomID, excludeParticipant) {
		sendParticipantTrackSnapshot(websocket, snapshot)
	}
}

func browserRTCConfigurationFromEnv() map[string]any {
	iceServers := make([]map[string]any, 0)
	stunURLs := splitEnvList("MEETING_STUN_URLS")
	if len(stunURLs) == 0 && !boolEnv("MEETING_DISABLE_DEFAULT_STUN") {
		stunURLs = []string{"stun:stun.l.google.com:19302"}
	}
	turnUsername, turnCredential := turnCredentialsFromEnv()
	for _, urls := range [][]string{
		stunURLs,
		splitEnvList("MEETING_TURN_URLS"),
	} {
		if len(urls) == 0 {
			continue
		}
		server := map[string]any{"urls": urls}
		if strings.HasPrefix(strings.ToLower(urls[0]), "turn:") || strings.HasPrefix(strings.ToLower(urls[0]), "turns:") {
			if turnUsername != "" {
				server["username"] = turnUsername
			}
			if turnCredential != "" {
				server["credential"] = turnCredential
				server["credentialType"] = "password"
			}
		}
		iceServers = append(iceServers, server)
	}

	if raw := strings.TrimSpace(os.Getenv("MEETING_ICE_SERVERS_JSON")); raw != "" {
		var configured []map[string]any
		if err := json.Unmarshal([]byte(raw), &configured); err != nil {
			log.Errorf("Failed to parse MEETING_ICE_SERVERS_JSON: %v", err)
		} else {
			iceServers = append(iceServers, configured...)
		}
	}

	if len(iceServers) == 0 {
		return map[string]any{}
	}

	return map[string]any{"iceServers": iceServers}
}

func nativeRoomClientConfig() map[string]any {
	return map[string]any{
		"rtcConfiguration": browserRTCConfigurationFromEnv(),
		// How long the ICE servers' TURN credentials stay valid (card-003 W4
		// gap 2). 0 when creds are static or absent; non-zero on the HMAC mint
		// path so the client refreshes this config before an ICE restart or
		// reconnect re-dials with expired relay creds.
		"turnCredentialTTLSeconds": turnCredentialTTLSecondsForClient(),
		"protocolVersion":          nativeClientProtocolV1,
		"auth":                     "cookie",
		"calendar":                 calendarCapabilities(),
		"websocketPath":            "/websocket",
		"signalingRole":            "server-offer",
		"supportedLayers":          []string{string(layerTierLow), string(layerTierMedium), string(layerTierHigh)},
		"nativeHints": map[string]any{
			"participantEvent":    "participant",
			"mediaReadyEvent":     "media_ready",
			"answerEvent":         "answer",
			"candidateEvent":      "candidate",
			"restartIceEvent":     "restart_ice",
			"roomEventEnvelope":   "kanban",
			"offerMetadataFields": []string{"offerId", "revision"},
			"mediaCodecs":         []string{webrtc.MimeTypeOpus, webrtc.MimeTypeH264, webrtc.MimeTypeVP8},
		},
	}
}

func nativeRosterParticipants() []map[string]string {
	participants := make([]map[string]string, 0, len(meetingParticipantNames))
	for _, name := range meetingParticipantNames {
		name = canonicalParticipantName(name)
		if name == "" {
			continue
		}
		participants = append(participants, map[string]string{
			"name":  name,
			"email": participantEmail(name),
		})
	}
	return participants
}

func turnCredentialsFromEnv() (string, string) {
	username := strings.TrimSpace(os.Getenv("MEETING_TURN_USERNAME"))
	credential := strings.TrimSpace(os.Getenv("MEETING_TURN_CREDENTIAL"))
	if username != "" && credential != "" {
		return username, credential
	}

	secret := strings.TrimSpace(os.Getenv("MEETING_TURN_SECRET"))
	if secret == "" {
		return username, credential
	}

	ttlSeconds := resolvedTurnTTLSeconds()
	userPrefix := strings.TrimSpace(os.Getenv("MEETING_TURN_USER_PREFIX"))
	if userPrefix == "" {
		userPrefix = "bonfire"
	}
	expiresAt := time.Now().UTC().Add(time.Duration(ttlSeconds) * time.Second).Unix()
	username = fmt.Sprintf("%d:%s", expiresAt, userPrefix)
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write([]byte(username))
	credential = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return username, credential
}

// resolvedTurnTTLSeconds returns the lifetime of an HMAC-minted TURN credential:
// the 12h default or a MEETING_TURN_TTL_SECONDS override clamped to [5m, 7d].
func resolvedTurnTTLSeconds() int64 {
	ttlSeconds := int64(12 * 60 * 60)
	if rawTTL := strings.TrimSpace(os.Getenv("MEETING_TURN_TTL_SECONDS")); rawTTL != "" {
		if parsedTTL, err := strconv.ParseInt(rawTTL, 10, 64); err == nil && parsedTTL >= 300 && parsedTTL <= 7*24*60*60 {
			ttlSeconds = parsedTTL
		}
	}
	return ttlSeconds
}

// turnCredentialTTLSecondsForClient reports how long the browser's minted TURN
// credentials stay valid so a long-lived session can refresh /client-config
// before they expire (card-003 W4 gap 2). It is non-zero ONLY on the HMAC-secret
// mint path: static MEETING_TURN_USERNAME/CREDENTIAL creds don't expire (0 =
// never refresh), and it is 0 when no TURN secret is configured.
func turnCredentialTTLSecondsForClient() int64 {
	if strings.TrimSpace(os.Getenv("MEETING_TURN_USERNAME")) != "" &&
		strings.TrimSpace(os.Getenv("MEETING_TURN_CREDENTIAL")) != "" {
		return 0
	}
	if strings.TrimSpace(os.Getenv("MEETING_TURN_SECRET")) == "" {
		return 0
	}
	return resolvedTurnTTLSeconds()
}

func splitEnvList(name string) []string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}

	values := make([]string, 0)
	for _, value := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\t' || r == ' '
	}) {
		value = strings.TrimSpace(value)
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}

func logClientMediaQualityReport(rawData string, participantName string, sessionID string) {
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(rawData), &payload); err != nil {
		log.Errorf("Failed to unmarshal client media quality report: %v", err)
		return
	}

	client := mapFromPayload(payload, "client")
	browser := mapFromPayload(payload, "browser")
	audio := mapFromPayload(payload, "audio")
	video := mapFromPayload(payload, "video")
	remote := mapFromPayload(payload, "remote")
	render := mapFromPayload(payload, "render")
	stats := mapFromPayload(payload, "stats")
	deltas := mapFromPayload(payload, "deltas")
	viewport := mapFromPayload(browser, "viewport")
	candidatePair := mapFromPayload(stats, "candidatePair")
	audioOutput := mapFromPayload(audio, "outputSettings")
	audioProcessorState := mapFromPayload(audio, "processorState")
	voiceFocusMetrics := mapFromPayload(audio, "voiceFocusMetrics")
	videoSettings := mapFromPayload(video, "settings")
	remoteAudioPlaybackPaths := mapFromPayload(remote, "remoteAudioPlaybackPaths")
	fmt.Printf(
		"Client media quality participant=%q session=%s platform=%s clientVersion=%s safari=%v laggy=%v viewport=%dx%d visual=%dx%d orientation=%s/%d mobile=%v roomLayout=%s stageMode=%s boardExpanded=%v screenShare=%s attachmentRevision=%d auxTargets=%d constrained=%v audioMode=%s audioProfile=%s voiceFocus=%v processor=%s workletHealth=%s rnnoiseReady=%v sampleRate=%d frameSize=%d vfGain=%.3f vfSuppressionDb=%.1f vfBias=%.4f vfSpeech=%.2f localAudio=%s/%v localVideo=%s/%v outAudioKbps=%.0f outVideoKbps=%.0f outAudioPackets=%d outVideoFrames=%d rttMs=%.0f inboundVideoJitterMs=%.0f inboundAudioJitterMs=%.0f inboundVideoLossPct=%.1f inboundAudioLossPct=%.1f localCandidate=%s remoteCandidate=%s protocol=%s network=%s remoteVideo=%d remoteAudio=%d remoteAudioLevel=%.5f remoteAudible=%d playbackElement=%d playbackWebAudio=%d playbackNone=%d audioCtx=%s missingVideo=%d missingAudio=%d duplicateVideo=%d duplicateAudio=%d placeholderVideo=%d placeholderAudio=%d stalledVideo=%d pendingAudio=%d\n",
		participantName,
		sessionID,
		stringFromPayload(client, "platform"),
		stringFromPayload(client, "version"),
		boolFromPayload(browser, "safari"),
		boolFromPayload(payload, "laggy"),
		int(floatFromPayload(viewport, "width")),
		int(floatFromPayload(viewport, "height")),
		int(floatFromPayload(viewport, "visualWidth")),
		int(floatFromPayload(viewport, "visualHeight")),
		stringFromPayload(viewport, "orientationType"),
		int(floatFromPayload(viewport, "orientationAngle")),
		boolFromPayload(viewport, "mobile"),
		stringFromPayload(render, "roomLayout"),
		stringFromPayload(render, "stageMode"),
		boolFromPayload(render, "boardExpanded"),
		stringFromPayload(render, "activeScreenShareParticipant"),
		int(floatFromPayload(render, "attachmentRevision")),
		arrayLenFromPayload(render, "auxiliaryTargets"),
		boolFromPayload(video, "constrained"),
		stringFromPayload(audio, "mode"),
		stringFromPayload(audio, "profile"),
		boolFromPayload(audio, "voiceFocus"),
		stringFromPayload(audio, "processor"),
		stringFromPayload(audioProcessorState, "workletHealth"),
		boolFromPayload(audioProcessorState, "rnnoiseReady"),
		int(floatFromPayload(audioProcessorState, "sampleRate")),
		int(floatFromPayload(audioProcessorState, "frameSize")),
		floatFromPayload(voiceFocusMetrics, "gain"),
		floatFromPayload(voiceFocusMetrics, "suppressionDb"),
		floatFromPayload(voiceFocusMetrics, "noiseBias"),
		floatFromPayload(voiceFocusMetrics, "speechConfidence"),
		stringFromPayload(audioOutput, "readyState"),
		boolFromPayload(audioOutput, "enabled"),
		stringFromPayload(videoSettings, "readyState"),
		boolFromPayload(videoSettings, "enabled"),
		kbpsFromDelta(floatFromPayload(deltas, "outboundAudioBytesSent"), floatFromPayload(deltas, "elapsedMs")),
		kbpsFromDelta(floatFromPayload(deltas, "outboundVideoBytesSent"), floatFromPayload(deltas, "elapsedMs")),
		int(floatFromPayload(deltas, "outboundAudioPacketsSent")),
		int(floatFromPayload(deltas, "outboundVideoFramesSent")),
		secondsToMillis(floatFromPayload(stats, "outboundRtt")),
		secondsToMillis(floatFromPayload(stats, "inboundVideoJitter")),
		secondsToMillis(floatFromPayload(stats, "inboundAudioJitter")),
		packetLossPercentFromDelta(deltas, "inboundVideoPacketsLost", "inboundVideoPacketsReceived"),
		packetLossPercentFromDelta(deltas, "inboundAudioPacketsLost", "inboundAudioPacketsReceived"),
		stringFromPayload(candidatePair, "localCandidateType"),
		stringFromPayload(candidatePair, "remoteCandidateType"),
		stringFromPayload(candidatePair, "protocol"),
		stringFromPayload(candidatePair, "networkType"),
		int(floatFromPayload(remote, "remoteVideoTiles")),
		int(floatFromPayload(remote, "remoteAudioMonitors")),
		floatFromPayload(remote, "remoteAudioMaxLevel"),
		int(floatFromPayload(remote, "remoteAudioAudibleMonitors")),
		int(floatFromPayload(remoteAudioPlaybackPaths, "element")),
		int(floatFromPayload(remoteAudioPlaybackPaths, "webaudio")),
		int(floatFromPayload(remoteAudioPlaybackPaths, "none")),
		stringFromPayload(remote, "audioContextState"),
		arrayLenFromPayload(remote, "missingVideoNames"),
		arrayLenFromPayload(remote, "missingAudioNames"),
		arrayLenFromPayload(remote, "duplicateVideoNames"),
		arrayLenFromPayload(remote, "duplicateAudioNames"),
		int(floatFromPayload(remote, "placeholderVideoTiles")),
		int(floatFromPayload(remote, "placeholderAudioMonitors")),
		arrayLenFromPayload(remote, "stalledVideoNames"),
		int(floatFromPayload(remote, "audiblePendingRemotePlayback")),
	)
}

func logClientMediaErrorReport(rawData string, participantName string, sessionID string) {
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(rawData), &payload); err != nil {
		log.Errorf("Failed to unmarshal client media error report: %v", err)
		return
	}

	browser := mapFromPayload(payload, "browser")
	audio := mapFromPayload(payload, "audio")
	errPayload := mapFromPayload(payload, "error")
	fmt.Printf(
		"Client media error participant=%q session=%s safari=%v stage=%s audioMode=%s processor=%s errorName=%s constraint=%s attempts=%d message=%q\n",
		participantName,
		sessionID,
		boolFromPayload(browser, "safari"),
		stringFromPayload(payload, "stage"),
		stringFromPayload(audio, "mode"),
		stringFromPayload(audio, "processor"),
		stringFromPayload(errPayload, "name"),
		stringFromPayload(errPayload, "constraint"),
		arrayLenFromPayload(errPayload, "attempts"),
		stringFromPayload(errPayload, "message"),
	)
}

func mapFromPayload(payload map[string]any, key string) map[string]any {
	value, _ := payload[key].(map[string]any)
	if value == nil {
		return map[string]any{}
	}
	return value
}

func arrayLenFromPayload(payload map[string]any, key string) int {
	value, _ := payload[key].([]any)
	return len(value)
}

// stringFromPayload extracts a string field from a decoded client report and
// scrubs it for logging. These helpers feed ONLY the media-quality/error log
// lines (one fmt.Printf key=value line per report), and guests can now reach
// those seams (§5.4), so an attacker-controlled value must not be able to forge
// a log line or bury one. sanitizeLogField does the neutralizing; printable
// content survives so the existing log-grep recipes keep matching.
func stringFromPayload(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return sanitizeLogField(value)
}

// sanitizeLogField neutralizes a client-supplied string for single-line
// key=value logging: CR/LF and other control characters — which could forge a
// new log line and poison incident forensics (the 2026-07-10 diagnosis leaned
// on exactly these logs) — are dropped, and the result is capped so one field
// can't bury a line. Ordinary printable text passes through unchanged.
func sanitizeLogField(s string) string {
	const maxRunes = 256
	var b strings.Builder
	count := 0
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' || r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		count++
		if count >= maxRunes {
			break
		}
	}
	return b.String()
}

// participantTrackRefreshReason extracts the client-supplied reason from a
// request_participant_tracks payload ({"reason": "..."}) and scrubs it for
// single-line logging (2026-07-10 incident: 193 member repair requests were
// completely invisible in server forensics; the reason strings — "frozen
// remote video", "media quality monitor", … — are exactly what the next
// diagnosis needs, but they are attacker-shaped input so they go through
// sanitizeLogField). Garbage payloads degrade to "".
func participantTrackRefreshReason(data string) string {
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return ""
	}
	return stringFromPayload(payload, "reason")
}

func boolFromPayload(payload map[string]any, key string) bool {
	value, _ := payload[key].(bool)
	return value
}

func floatFromPayload(payload map[string]any, key string) float64 {
	switch value := payload[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case json.Number:
		number, _ := value.Float64()
		return number
	default:
		return 0
	}
}

func kbpsFromDelta(byteDelta float64, elapsedMs float64) float64 {
	if byteDelta <= 0 || elapsedMs <= 0 {
		return 0
	}
	return byteDelta * 8 / elapsedMs
}

func packetLossPercentFromDelta(payload map[string]any, lostKey string, receivedKey string) float64 {
	lost := floatFromPayload(payload, lostKey)
	received := floatFromPayload(payload, receivedKey)
	total := lost + received
	if total <= 0 {
		return 0
	}
	return lost / total * 100
}

func secondsToMillis(seconds float64) float64 {
	return seconds * 1000
}

// participantConnectionKey indexes activeParticipantConnections by device so
// two endpoints of one account can hold separate entries. Legacy clients (empty
// endpoint id) key on the bare name, preserving the original single-slot map
// key exactly — this is what keeps the existing participant tests green.
func participantConnectionKey(name string, endpointID string) string {
	name = canonicalParticipantName(name)
	if endpointID == "" {
		return name
	}
	return name + "\x00" + endpointID
}

// sanitizeEndpointID bounds and cleans a client-supplied device id. Anything
// unexpected degrades to "" (legacy single-slot behaviour) rather than being
// trusted verbatim, since the id participates in a connection map key.
func sanitizeEndpointID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if len(raw) > 64 {
		raw = raw[:64]
	}
	for _, r := range raw {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' {
			return ""
		}
	}
	return raw
}

func replaceExistingParticipantSession(name string, sessionID string, currentPeerConnection *webrtc.PeerConnection, currentWebsocket *threadSafeWriter, sessionEmail string) {
	replaceExistingParticipantSessionEndpoint(name, sessionID, "", currentPeerConnection, currentWebsocket, sessionEmail)
}

// replaceExistingParticipantSessionEndpoint installs the current session for one
// endpoint and evicts only a stale session on that SAME endpoint (a refreshed
// tab). Other endpoints of the same account — the mandated laptop+phone case —
// are left fully intact: their connections and forwarded tracks are untouched.
func replaceExistingParticipantSessionEndpoint(name string, sessionID string, endpointID string, currentPeerConnection *webrtc.PeerConnection, currentWebsocket *threadSafeWriter, sessionEmail string) {
	replaceExistingParticipantSessionEndpointInRoom(officeRoomID, name, sessionID, endpointID, currentPeerConnection, currentWebsocket, sessionEmail)
}

func replaceExistingParticipantSessionEndpointInRoom(roomID string, name string, sessionID string, endpointID string, currentPeerConnection *webrtc.PeerConnection, currentWebsocket *threadSafeWriter, sessionEmail string) {
	name = canonicalRoomParticipantName(name)
	if name == "" {
		return
	}

	key := participantConnectionKey(name, endpointID)
	var staleConnections []peerConnectionState
	removedTracks := false

	listLock.Lock()
	if activeParticipantConnections == nil {
		activeParticipantConnections = map[string]peerConnectionState{}
	}
	if existing, ok := activeParticipantConnections[key]; ok && existing.sessionID != sessionID {
		staleConnections = append(staleConnections, existing)
	}
	activeParticipantConnections[key] = peerConnectionState{
		peerConnection:  currentPeerConnection,
		websocket:       currentWebsocket,
		participantName: name,
		sessionID:       sessionID,
		endpointID:      endpointID,
		sessionEmail:    normalizeAccountEmail(sessionEmail),
		roomID:          normalizeRoomID(roomID),
	}

	retainedConnections := peerConnections[:0]
	for _, state := range peerConnections {
		isCurrentConnection := currentPeerConnection != nil && state.peerConnection == currentPeerConnection
		sameEndpoint := sameParticipantName(state.participantName, name) && state.endpointID == endpointID
		if isCurrentConnection || !sameEndpoint || state.sessionID == sessionID {
			retainedConnections = append(retainedConnections, state)
			continue
		}
		staleConnections = append(staleConnections, state)
	}
	peerConnections = retainedConnections

	// Remove only the evicted sessions' forwarded tracks, never the account's
	// other live endpoint. A brand-new session has no tracks yet at admission,
	// so this only prunes the replaced tab's leftovers.
	for _, stale := range staleConnections {
		if removeParticipantTracksLocked(name, stale.sessionID) {
			removedTracks = true
		}
	}
	listLock.Unlock()

	closeParticipantConnections(staleConnections)

	if len(staleConnections) > 0 || removedTracks {
		signalPeerConnections()
	}
}

// unregisterParticipantSession tears down exactly one session when its socket
// closes, scoped by sessionID so a person's other device (a different endpoint,
// a different session) keeps its connection, tracks, and layer tier.
func unregisterParticipantSession(name string, sessionID string) {
	name = canonicalRoomParticipantName(name)
	if name == "" {
		return
	}

	removedConnection := false
	removedTracks := false

	listLock.Lock()
	for key, state := range activeParticipantConnections {
		if sameParticipantName(state.participantName, name) && state.sessionID == sessionID {
			delete(activeParticipantConnections, key)
			removedConnection = true
		}
	}
	retainedConnections := peerConnections[:0]
	for _, state := range peerConnections {
		if sameParticipantName(state.participantName, name) && state.sessionID == sessionID {
			removedConnection = true
			continue
		}
		retainedConnections = append(retainedConnections, state)
	}
	peerConnections = retainedConnections
	removedTracks = removeParticipantTracksLocked(name, sessionID)
	delete(subscriberLayerTiers, sessionID)
	listLock.Unlock()

	if removedConnection || removedTracks {
		signalPeerConnections()
	}
}

// prunePeerConnectionPool drops a peer connection from the fan-out pool and the
// active-participant index immediately. It is called the moment a connection
// reaches Failed/Closed so a dead peer stops occupying a forwarding slot right
// away, rather than lingering until the next debounced signaling cycle prunes
// it. The PeerConnection's own Close() releases its ICE/DTLS/SRTP transports;
// this only clears our bookkeeping. Idempotent and safe to call repeatedly.
func prunePeerConnectionPool(peerConnection *webrtc.PeerConnection) bool {
	if peerConnection == nil {
		return false
	}

	listLock.Lock()
	defer listLock.Unlock()

	removed := false
	retained := peerConnections[:0]
	for _, state := range peerConnections {
		if state.peerConnection == peerConnection {
			removed = true
			continue
		}
		retained = append(retained, state)
	}
	peerConnections = retained

	for name, state := range activeParticipantConnections {
		if state.peerConnection == peerConnection {
			delete(activeParticipantConnections, name)
			removed = true
		}
	}

	return removed
}

// peerConnectionPoolSize reports the current fan-out pool depth under listLock,
// for the liveness-reap observability line — a pool that never shrinks after a
// reap is the leak this cleanup exists to close.
func peerConnectionPoolSize() int {
	listLock.RLock()
	defer listLock.RUnlock()
	return len(peerConnections)
}

func closeParticipantConnections(states []peerConnectionState) {
	closedPeerConnections := map[*webrtc.PeerConnection]struct{}{}
	closedWebsockets := map[*threadSafeWriter]struct{}{}
	for _, state := range states {
		if state.peerConnection != nil {
			if _, ok := closedPeerConnections[state.peerConnection]; !ok {
				closedPeerConnections[state.peerConnection] = struct{}{}
				if err := state.peerConnection.Close(); err != nil {
					log.Errorf("Failed to close replaced-session PeerConnection: %v", err)
				}
			}
		}
		if state.websocket != nil {
			if _, ok := closedWebsockets[state.websocket]; !ok {
				closedWebsockets[state.websocket] = struct{}{}
				// Tell the evicted tab WHY before cutting the socket (the
				// notifySessionReplacedAndClose pattern). An abrupt close
				// reads as a network blip, and the client's signaling
				// reconnect re-dials — evicting THIS admission right back, a
				// self-sustaining seat duel where both tabs churn forever.
				// session_replaced makes the losing tab stop cleanly.
				_ = sendKanbanEvent(state.websocket, "session_replaced", "This browser session was replaced by a newer room join.")
				_ = state.websocket.Close()
			}
		}
	}
}

func nextParticipantSessionID() string {
	return fmt.Sprintf("participant-%d-%d", time.Now().UnixNano(), participantSessionSeq.Add(1))
}

// signalPeerConnections updates each PeerConnection so that it is getting all the expected media tracks.
func signalPeerConnections() { // nolint
	requestPeerConnectionSignal()
}

func requestPeerConnectionSignal() {
	signalRequestLock.Lock()
	defer signalRequestLock.Unlock()

	if signalRequestTimer != nil {
		signalRequestTimer.Reset(peerSignalDebounce)
		return
	}

	signalRequestTimer = time.AfterFunc(peerSignalDebounce, func() {
		signalRequestLock.Lock()
		signalRequestTimer = nil
		signalRequestLock.Unlock()
		signalPeerConnectionsWithRestart()
	})
}

func signalPeerConnectionICE(peerConnection *webrtc.PeerConnection) bool {
	if peerConnection == nil {
		return false
	}

	now := time.Now()
	state := peerConnection.ICEConnectionState()
	queued := false
	listLock.Lock()
	for i := range peerConnections {
		if peerConnections[i].peerConnection != peerConnection {
			continue
		}
		queued = peerConnections[i].iceRestart.queue(state, now)
		break
	}
	listLock.Unlock()
	if !queued {
		return false
	}

	signalPeerConnectionsWithRestart()
	return true
}

func notePeerConnectionICEState(peerConnection *webrtc.PeerConnection, state webrtc.ICEConnectionState) {
	if peerConnection == nil {
		return
	}
	listLock.Lock()
	defer listLock.Unlock()
	for i := range peerConnections {
		if peerConnections[i].peerConnection == peerConnection {
			peerConnections[i].iceRestart.observeConnectionState(state)
			return
		}
	}
}

func completePeerConnectionICERestart(peerConnection *webrtc.PeerConnection) {
	if peerConnection == nil {
		return
	}
	listLock.Lock()
	defer listLock.Unlock()
	for i := range peerConnections {
		if peerConnections[i].peerConnection == peerConnection {
			peerConnections[i].iceRestart.complete()
			return
		}
	}
}

func schedulePeerConnectionSignal() {
	go func() {
		time.Sleep(750 * time.Millisecond)
		signalPeerConnectionsWithRestart()
	}()
}

func signalPeerConnectionsWithRestart() { // nolint
	listLock.Lock()
	retryLater := false
	defer func() {
		listLock.Unlock()
		if retryLater {
			schedulePeerConnectionSignal()
		}
	}()

	attemptSync := func() (tryAgain bool) {
		for i := range peerConnections {
			if peerConnections[i].peerConnection == nil || peerConnections[i].peerConnection.ConnectionState() == webrtc.PeerConnectionStateClosed {
				peerConnections = append(peerConnections[:i], peerConnections[i+1:]...)

				return true // We modified the slice, start from the beginning
			}

			peer := &peerConnections[i]
			forceSignal := peer.iceRestart.queued

			if peer.peerConnection.SignalingState() != webrtc.SignalingStateStable {
				retryLater = true
				now := time.Now()
				if peer.nonStableSince.IsZero() {
					peer.nonStableSince = now
				}
				switch negotiationWatchdogAction(peer.nonStableSince, peer.offerResent, now) {
				case negotiationActionResend:
					peer.offerResent = true
					resendPendingOffer(peer)
				case negotiationActionClose:
					log.Errorf("Negotiation stuck >%s for participant=%s session=%s; closing peer connection so the client can reconnect", negotiationCloseAfter, peer.participantName, peer.sessionID)
					stuckPeerConnection := peer.peerConnection
					stuckWebsocket := peer.websocket
					go func() {
						// Tell the client why it is being ejected before the
						// teardown. Without this the stale session's next
						// message earns a misleading "session_replaced";
						// media_disconnected drives the client's honest
						// reconnect affordances. Closing the websocket lets
						// the read loop run the full session cleanup.
						if stuckWebsocket != nil {
							_ = sendKanbanEvent(stuckWebsocket, "media_disconnected", "media negotiation stalled; rejoin the room.")
							_ = stuckWebsocket.Close()
						}
						if err := stuckPeerConnection.Close(); err != nil {
							log.Errorf("Failed to close negotiation-stuck PeerConnection: %v", err)
						}
					}()
					peer.nonStableSince = time.Time{}
					peer.offerResent = false
				}
				continue
			}
			peer.nonStableSince = time.Time{}
			peer.offerResent = false

			desiredTrackCount := 0
			for _, trackLocal := range trackLocals {
				if peer.acceptsTrack(trackLocal) {
					desiredTrackCount++
				}
			}
			if !forceSignal && !peer.shouldSignalWithDesiredTrackCount(desiredTrackCount) {
				continue
			}

			needsOffer := forceSignal || peer.peerConnection.LocalDescription() == nil

			// Map senders we already have, so we do not double-send tracks.
			existingSenders := map[string]bool{}

			for _, sender := range peer.peerConnection.GetSenders() {
				currentTrack := sender.Track()
				if currentTrack == nil {
					continue
				}

				trackID := currentTrack.ID()
				existingSenders[trackID] = true

				// If we have an RTPSender that does not map to an existing track, remove and signal.
				trackLocal, ok := trackLocals[trackID]
				if !ok || !peer.acceptsTrack(trackLocal) {
					if err := peer.peerConnection.RemoveTrack(sender); err != nil {
						log.Errorf("Failed to remove stale sender track=%s: %v", trackID, err)
						return true
					}
					delete(existingSenders, trackID)
					needsOffer = true
					continue
				}

				// A publisher may stop and republish with the same forwarded track
				// ID. The map then points at a new TrackLocalStaticRTP even though
				// the subscriber's sender is still bound to the old object. Comparing
				// IDs alone leaves the replacement unbound and the remote tile frozen.
				// Rebind compatible replacements in place so media resumes without a
				// renegotiation; if pion cannot safely do that, remove/add below and
				// let the normal offer path renegotiate the sender.
				if currentTrack != trackLocal {
					if senderTrackReplacementCompatible(currentTrack, trackLocal) {
						if err := sender.ReplaceTrack(trackLocal); err == nil {
							log.Infof("room_sender_track_rebound track_id=%s participant=%s session=%s", trackID, peer.participantName, peer.sessionID)
							continue
						} else {
							log.Errorf("Failed to replace republished sender track=%s in place; renegotiating: %v", trackID, err)
						}
					}

					if err := peer.peerConnection.RemoveTrack(sender); err != nil {
						log.Errorf("Failed to remove replaced sender track=%s: %v", trackID, err)
						return true
					}
					delete(existingSenders, trackID)
					needsOffer = true
				}
			}

			// Don't receive videos we are sending, make sure we don't have loopback
			for _, receiver := range peer.peerConnection.GetReceivers() {
				if receiver.Track() == nil {
					continue
				}

				existingSenders[receiver.Track().ID()] = true
			}

			// Add every track we are not sending yet to the PeerConnection.
			for trackID, trackLocal := range trackLocals {
				if !peer.acceptsTrack(trackLocal) {
					continue
				}

				if _, ok := existingSenders[trackID]; !ok {
					transceiver, err := peer.peerConnection.AddTransceiverFromTrack(trackLocal, webrtc.RTPTransceiverInit{
						Direction: webrtc.RTPTransceiverDirectionSendonly,
					})
					if err != nil {
						log.Errorf("Failed to add sender track=%s: %v", trackID, err)
						return true
					}
					if err := preferSourceTrackCodec(transceiver, trackLocal); err != nil {
						log.Errorf("Failed to prefer source codec for sender track=%s: %v", trackID, err)
						return true
					}
					// Every forwarded-track sender is created exactly here, so
					// each gets exactly one RTCP drain goroutine. It blocks in
					// Read until the sender starts and exits when the sender is
					// stopped (RemoveTrack above / PeerConnection close).
					go forwardSubscriberRTCP(transceiver.Sender(), trackID, peer.participantName, peer.sessionID)
					needsOffer = true
				}
			}

			if !needsOffer {
				continue
			}

			var offerOptions *webrtc.OfferOptions
			if forceSignal {
				offerOptions = &webrtc.OfferOptions{ICERestart: true}
			}

			offer, err := peer.peerConnection.CreateOffer(offerOptions)
			if err != nil {
				log.Errorf("Failed to create offer: %v", err)
				retryLater = true
				return true
			}

			var gatherComplete <-chan struct{}
			if peer.signal != nil {
				gatherComplete = webrtc.GatheringCompletePromise(peer.peerConnection)
			}

			if err = peer.peerConnection.SetLocalDescription(offer); err != nil {
				log.Errorf("Failed to set local offer: %v", err)
				retryLater = true
				return true
			}
			if forceSignal {
				peer.iceRestart.start(time.Now())
			}
			offerMetadata := startPendingOfferMetadata(peer)

			if peer.websocket != nil {
				for _, snapshot := range participantTrackSnapshotsLocked(peer.roomID, peer.participantName) {
					sendParticipantTrackSnapshot(peer.websocket, snapshot)
				}
			}

			totalTracks, audioTracks, videoTracks := forwardedTrackCountsLocked()
			log.Infof("room_signal_offer participant=%s session=%s offer_id=%s revision=%d restart=%t desired_tracks=%d sender_tracks=%d receiver_tracks=%d total_tracks=%d audio_tracks=%d video_tracks=%d signaling_state=%s",
				peer.participantName, peer.sessionID, offerMetadata.OfferID, offerMetadata.Revision, forceSignal, desiredTrackCount, countPeerSenders(peer.peerConnection), countPeerReceivers(peer.peerConnection), totalTracks, audioTracks, videoTracks, peer.peerConnection.SignalingState())

			if peer.signal != nil {
				if err = peer.signal(gatherComplete); err != nil {
					log.Errorf("Failed to signal peer: %v", err)
					return true
				}

				continue
			}

			offerString, err := json.Marshal(offer)
			if err != nil {
				log.Errorf("Failed to marshal offer to json: %v", err)

				return true
			}

			log.Infof("room_signal_offer_payload participant=%s session=%s offer_id=%s revision=%d sdp_bytes=%d",
				peer.participantName, peer.sessionID, offerMetadata.OfferID, offerMetadata.Revision, len(offer.SDP))

			if err = peer.websocket.WriteJSON(&websocketMessage{
				Event:    "offer",
				Data:     string(offerString),
				OfferID:  offerMetadata.OfferID,
				Revision: offerMetadata.Revision,
			}); err != nil {
				return true
			}

			// Start the negotiation watchdog clock and schedule a follow-up pass so
			// a dropped offer is noticed even when no other signaling occurs. The
			// restart (if any) was delivered, so follow-ups must not force again.
			peer.nonStableSince = time.Now()
			peer.offerResent = false
			retryLater = true
		}

		return tryAgain
	}

	for syncAttempt := 0; ; syncAttempt++ {
		if syncAttempt == 25 {
			// Release the lock and attempt a sync in 3 seconds. We might be blocking a RemoveTrack or AddTrack
			go func() {
				time.Sleep(time.Second * 3)
				signalPeerConnections()
			}()

			return
		}

		if !attemptSync() {
			break
		}
	}
}

// resendPendingOffer re-sends a subscriber's pending local offer over its
// websocket. The negotiation watchdog uses it when a peer has sat in
// have-local-offer long enough that the client likely dropped the original
// offer; a healthy client simply answers the duplicate. Callers hold listLock.
func resendPendingOffer(peer *peerConnectionState) {
	if peer.websocket == nil || peer.peerConnection.SignalingState() != webrtc.SignalingStateHaveLocalOffer {
		return
	}
	description := peer.peerConnection.LocalDescription()
	if description == nil {
		return
	}

	offerString, err := json.Marshal(description)
	if err != nil {
		log.Errorf("Failed to marshal pending offer for resend: %v", err)
		return
	}

	metadata := pendingOfferMetadata(peer)
	if metadata.empty() {
		metadata = startPendingOfferMetadata(peer)
	}
	log.Infof("Negotiation stuck >%s for participant=%s session=%s offer_id=%s revision=%d; re-sending pending offer", negotiationResendAfter, peer.participantName, peer.sessionID, metadata.OfferID, metadata.Revision)
	if err := peer.websocket.WriteJSON(&websocketMessage{
		Event:    "offer",
		Data:     string(offerString),
		OfferID:  metadata.OfferID,
		Revision: metadata.Revision,
	}); err != nil {
		log.Errorf("Failed to re-send pending offer: %v", err)
	}
}

// senderTrackReplacementCompatible reports whether pion can bind desired onto
// an already-negotiated sender without changing the sender's media envelope.
// Pointer identity is deliberately handled by the caller: this helper is only
// for the same-ID, different-TrackLocal republish case.
func senderTrackReplacementCompatible(current webrtc.TrackLocal, desired *webrtc.TrackLocalStaticRTP) bool {
	if current == nil || desired == nil {
		return false
	}
	currentStatic, ok := current.(*webrtc.TrackLocalStaticRTP)
	if !ok || currentStatic.Kind() != desired.Kind() || currentStatic.ID() != desired.ID() || currentStatic.StreamID() != desired.StreamID() {
		return false
	}

	currentCodec := currentStatic.Codec()
	desiredCodec := desired.Codec()

	return strings.EqualFold(strings.TrimSpace(currentCodec.MimeType), strings.TrimSpace(desiredCodec.MimeType)) &&
		currentCodec.ClockRate == desiredCodec.ClockRate &&
		currentCodec.Channels == desiredCodec.Channels &&
		strings.EqualFold(strings.TrimSpace(currentCodec.SDPFmtpLine), strings.TrimSpace(desiredCodec.SDPFmtpLine))
}

// preferSourceTrackCodec restricts a subscriber-facing sender to the codec the
// forwarded source is actually publishing and — for video — also advertises that
// codec's RTX repair codec, so the offered m-line carries the rtx codec and an
// a=ssrc-group:FID and SFU→subscriber retransmissions negotiate. Preferring only
// the primary codec (as this used to) made SetCodecPreferences filter RTX out of
// the offer: the ready NACK responder then had no repair stream to send on and
// downstream loss recovery fell back to full keyframes. Audio and any codec the
// engine doesn't pair with RTX keep the single-codec preference unchanged.
func preferSourceTrackCodec(transceiver *webrtc.RTPTransceiver, trackLocal *webrtc.TrackLocalStaticRTP) error {
	if transceiver == nil || trackLocal == nil {
		return nil
	}
	codec := trackLocal.Codec()
	if strings.TrimSpace(codec.MimeType) == "" {
		return nil
	}

	preferences := []webrtc.RTPCodecParameters{roomCodecPreferenceWithTransportCC(webrtc.RTPCodecParameters{RTPCodecCapability: codec})}
	if primary, rtx, ok := roomVideoCodecPairFor(codec.MimeType); ok {
		// Use the engine's registered parameters (payload types included) so
		// SetCodecPreferences matches exactly and, critically, keeps the RTX
		// codec attached to its primary (its apt payload type must be present
		// in the same list or the RTX entry is dropped). ConfigureTWCCSender
		// adds transport-cc feedback to the MediaEngine after these canonical
		// structs are registered, so mirror it into every explicit preference;
		// otherwise pion intersects it away on source-specific m-sections and
		// Chrome disables congestion control for the entire PeerConnection.
		preferences = []webrtc.RTPCodecParameters{
			roomCodecPreferenceWithTransportCC(primary),
			roomCodecPreferenceWithTransportCC(rtx),
		}
	}

	return transceiver.SetCodecPreferences(preferences)
}

// roomCodecPreferenceWithTransportCC clones a codec preference and includes
// the feedback ConfigureTWCCSender registered on the MediaEngine. Keeping this
// at the explicit-preference seam avoids mutating the canonical codec globals
// (which would make ConfigureTWCCSender register a duplicate feedback entry).
func roomCodecPreferenceWithTransportCC(codec webrtc.RTPCodecParameters) webrtc.RTPCodecParameters {
	feedback := append([]webrtc.RTCPFeedback(nil), codec.RTCPFeedback...)
	for _, entry := range feedback {
		if strings.EqualFold(strings.TrimSpace(entry.Type), webrtc.TypeRTCPFBTransportCC) {
			codec.RTCPFeedback = feedback
			return codec
		}
	}
	codec.RTCPFeedback = append(feedback, webrtc.RTCPFeedback{Type: webrtc.TypeRTCPFBTransportCC})

	return codec
}

// roomVideoCodecPairFor returns the registered primary and RTX repair codec
// parameters for a forwarded video mime type. ok is false for audio and any
// codec the room media engine does not pair with RTX, so the caller keeps its
// single-codec preference.
func roomVideoCodecPairFor(mimeType string) (primary, rtx webrtc.RTPCodecParameters, ok bool) {
	switch {
	case strings.EqualFold(mimeType, webrtc.MimeTypeH264):
		return roomH264Codec, roomH264RTXCodec, true
	case strings.EqualFold(mimeType, webrtc.MimeTypeVP8):
		return roomVP8Codec, roomVP8RTXCodec, true
	default:
		return webrtc.RTPCodecParameters{}, webrtc.RTPCodecParameters{}, false
	}
}

// keyframeRequestThrottle coalesces subscriber keyframe requests per forwarded
// source so N subscribers asking at once (or one subscriber spamming PLI while
// its decoder is stuck) produce at most one upstream request per interval —
// one fresh keyframe satisfies every subscriber because the SFU fans the same
// RTP out to all of them.
type keyframeRequestThrottle struct {
	mu       sync.Mutex
	interval time.Duration
	last     map[string]time.Time
}

func newKeyframeRequestThrottle(interval time.Duration) *keyframeRequestThrottle {
	return &keyframeRequestThrottle{interval: interval, last: map[string]time.Time{}}
}

// allow reports whether a keyframe request for sourceKey may be forwarded at
// time now, recording the request when allowed.
func (t *keyframeRequestThrottle) allow(sourceKey string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if last, ok := t.last[sourceKey]; ok && now.Sub(last) < t.interval {
		return false
	}
	t.last[sourceKey] = now

	return true
}

// forget drops throttle state for a removed source so the map never outgrows
// the set of live forwarded tracks.
func (t *keyframeRequestThrottle) forget(sourceKey string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.last, sourceKey)
}

// subscriberKeyframeInterval caps keyframe requests toward a publisher at
// roughly one per source per 2.5s — the shared budget for concrete subscriber,
// screen-share-transition, and video-liveness requests. A first request for a
// source always passes (empty throttle entry); the floor only caps sustained
// re-requests. It was 1s
// until the 2026-07-10 spiral, where a lossy mobile subscriber sustained ~1
// forwarded keyframe per source per 1-2s and the resulting keyframe-heavy
// fanout saturated egress — 2.5s keeps recovery snappy while a storm costs
// each publisher at most ~24 keyframes a minute.
const subscriberKeyframeInterval = 2500 * time.Millisecond

var subscriberKeyframeThrottle = newKeyframeRequestThrottle(subscriberKeyframeInterval)

// Throttled subscriber keyframe requests used to be dropped SILENTLY, which
// hid the forward path's behavior during the 2026-07-10 frozen-tile
// diagnosis. Every suppressed request is counted; the log line is emitted at
// most once per interval so a PLI storm stays cheap.
const keyframeThrottleDropLogInterval = time.Second

// memberMediaRepairLogInterval rate-limits the S2 member repair log lines
// (accepted + dropped) to one per session per interval with a suppressed
// count, so the observability fix can't itself become a log storm during the
// exact incident it exists to expose.
const memberMediaRepairLogInterval = 10 * time.Second

var (
	keyframeThrottleDrops        atomic.Int64
	keyframeThrottleDropLogStamp atomic.Int64 // unix nanos of the last drop log
)

func noteThrottledKeyframeRequestDrop(forwardedTrackID string, subscriberName string, subscriberSession string) {
	total := keyframeThrottleDrops.Add(1)
	now := time.Now().UnixNano()
	last := keyframeThrottleDropLogStamp.Load()
	if now-last < int64(keyframeThrottleDropLogInterval) || !keyframeThrottleDropLogStamp.CompareAndSwap(last, now) {
		return
	}
	log.Infof("room_keyframe_throttled track_id=%s requested_by=%s session=%s dropped_total=%d",
		forwardedTrackID, subscriberName, subscriberSession, total)
}

// Per-publisher-track silence watchdog — 2026-07-10 silent-uplink incident.
// Tim's Safari outbound AUDIO sender stopped producing RTP for 7+ minutes while
// his PeerConnection stayed healthy; the read pump (ReadRTP has no deadline) saw
// zero signal and the track looked alive until EOF at leave. Only client
// telemetry caught it. The watchdog makes a stalled uplink visible server-side:
// the read pump stamps last-RTP time per packet (atomic, hot path) and a
// periodic sweep flags any track whose PeerConnection is Connected yet whose
// last RTP is older than the threshold — logging it (rate-limited) and, for
// VIDEO, sending one PLI through the EXISTING keyframe throttle (a stalled video
// uplink sometimes restarts on PLI; audio has no PLI, so it is log-only).
const (
	// publisherSilenceThreshold is the age past which a track's inbound RTP is
	// considered stalled. 5s clears ordinary jitter/DTX gaps while catching a
	// real stall within a sweep of crossing it.
	publisherSilenceThreshold = 5 * time.Second
	// publisherSilenceLogInterval rate-limits the repeat "still silent" log per
	// track so a minutes-long stall (the incident was 7+ min) cannot become its
	// own log storm; onset and recovery always log.
	publisherSilenceLogInterval = 30 * time.Second
	// publisherSilenceNudgeRequester tags the watchdog's PLIs in the existing
	// room_keyframe_forwarded/throttled accounting.
	publisherSilenceNudgeRequester = "silence_watchdog"
)

// publisherSilenceAction is the sweep's verdict for one watched track.
type publisherSilenceAction int

const (
	publisherSilenceNone      publisherSilenceAction = iota // healthy, or never produced RTP
	publisherSilenceOnset                                   // just crossed the threshold
	publisherSilenceOngoing                                 // still silent, past a log interval
	publisherSilenceRecovered                               // RTP resumed after a silent period
)

// publisherSilenceObservation carries what the sweep should log/do for a track.
type publisherSilenceObservation struct {
	action   publisherSilenceAction
	silentMs int64
	repeat   int // suppressed observations since onset (Ongoing only)
}

// publisherTrackWatch tracks one publisher media track's inbound-RTP liveness.
// lastRTPNanos is written by the read-pump goroutine (per packet, atomically)
// and read by the sweep; every other field is set once at register time
// (published to the sweep through the registry mutex) EXCEPT the silence-state
// fields below, which are touched ONLY by the single sweep goroutine and so
// need no lock of their own.
type publisherTrackWatch struct {
	lastRTPNanos atomic.Int64 // unix nanos of the last RTP read; 0 = none yet

	sourceKey   string // forwardedRemoteTrackID — the PLI/throttle key
	participant string
	session     string
	kind        webrtc.RTPCodecType
	pc          *webrtc.PeerConnection

	// sweep-goroutine-owned silence state
	silent          bool
	silentSinceNs   int64 // last-RTP time at the moment silence was declared
	lastSilentLogNs int64 // nanos of the last emitted silent log (rate limit)
	silentRepeat    int   // silent sweeps observed since onset
}

// evaluate advances the watch's silence state for a sweep at nowNs and reports
// what to log/do. Callers must invoke it only from the single sweep goroutine.
func (w *publisherTrackWatch) evaluate(nowNs int64) publisherSilenceObservation {
	last := w.lastRTPNanos.Load()
	if last == 0 {
		// No RTP ever seen — a join-muted track, not the "was producing then
		// stopped" stall this watchdog exists to catch.
		return publisherSilenceObservation{action: publisherSilenceNone}
	}
	age := nowNs - last
	if age < int64(publisherSilenceThreshold) {
		if w.silent {
			// RTP resumed. The silent gap runs from the last pre-silence packet
			// (silentSinceNs) to the newest packet (last); the small overcount
			// from post-resume packets arriving before this sweep is bounded by
			// the sweep interval and fine for observability.
			silentMs := (last - w.silentSinceNs) / int64(time.Millisecond)
			w.resetSilenceState()
			return publisherSilenceObservation{action: publisherSilenceRecovered, silentMs: silentMs}
		}
		return publisherSilenceObservation{action: publisherSilenceNone}
	}
	if !w.silent {
		w.silent = true
		w.silentSinceNs = last
		w.lastSilentLogNs = nowNs
		w.silentRepeat = 0
		return publisherSilenceObservation{action: publisherSilenceOnset, silentMs: age / int64(time.Millisecond)}
	}
	w.silentRepeat++
	if nowNs-w.lastSilentLogNs < int64(publisherSilenceLogInterval) {
		return publisherSilenceObservation{action: publisherSilenceNone}
	}
	w.lastSilentLogNs = nowNs
	return publisherSilenceObservation{action: publisherSilenceOngoing, silentMs: age / int64(time.Millisecond), repeat: w.silentRepeat}
}

func (w *publisherTrackWatch) resetSilenceState() {
	w.silent = false
	w.silentSinceNs = 0
	w.lastSilentLogNs = 0
	w.silentRepeat = 0
}

// nudgeIfVideo asks a silently-stalled VIDEO publisher for a keyframe through
// the SAME per-source throttle as every other PLI, so the watchdog can never
// outspend the keyframe budget (2026-07-10 spiral). Audio has no PLI equivalent.
func (w *publisherTrackWatch) nudgeIfVideo() {
	if w.kind != webrtc.RTPCodecTypeVideo {
		return
	}
	requestSourceKeyframe(w.sourceKey, publisherSilenceNudgeRequester, w.session)
}

// publisherSilenceRegistry holds the live per-track watches. The map is guarded
// by mu; each watch's silence state is owned by the sweep goroutine.
type publisherSilenceRegistry struct {
	mu     sync.Mutex
	tracks map[string]*publisherTrackWatch
}

func newPublisherSilenceRegistry() *publisherSilenceRegistry {
	return &publisherSilenceRegistry{tracks: map[string]*publisherTrackWatch{}}
}

func (r *publisherSilenceRegistry) register(sourceKey string, participant string, session string, kind webrtc.RTPCodecType, pc *webrtc.PeerConnection) *publisherTrackWatch {
	w := &publisherTrackWatch{
		sourceKey:   sourceKey,
		participant: participant,
		session:     session,
		kind:        kind,
		pc:          pc,
	}
	r.mu.Lock()
	r.tracks[sourceKey] = w
	r.mu.Unlock()

	return w
}

// forget drops a watch, but only if the registry still holds THIS exact watch —
// a same-key republish (same stream/track/SSRC) may have replaced it, and the
// stale reader's teardown must not evict the fresh entry (mirrors removeTrack).
func (r *publisherSilenceRegistry) forget(w *publisherTrackWatch) {
	if w == nil {
		return
	}
	r.mu.Lock()
	if r.tracks[w.sourceKey] == w {
		delete(r.tracks, w.sourceKey)
	}
	r.mu.Unlock()
}

func (r *publisherSilenceRegistry) snapshot() []*publisherTrackWatch {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.tracks) == 0 {
		return nil
	}
	out := make([]*publisherTrackWatch, 0, len(r.tracks))
	for _, w := range r.tracks {
		out = append(out, w)
	}

	return out
}

func (r *publisherSilenceRegistry) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.tracks)
}

var publisherSilence = newPublisherSilenceRegistry()

// sweepPublisherSilence runs on the 3s liveness ticker. For each watched track
// whose PeerConnection is Connected (a stall while the transport itself is down
// is explained by the transport, not a silent uplink), it advances the silence
// state and logs/nudges accordingly.
func sweepPublisherSilence() {
	nowNs := time.Now().UnixNano()
	for _, w := range publisherSilence.snapshot() {
		if w.pc == nil || w.pc.ConnectionState() != webrtc.PeerConnectionStateConnected {
			// Reset so a reconnect starts clean and RTP resuming afterward does
			// not fire a phantom "recovered".
			w.resetSilenceState()
			continue
		}
		obs := w.evaluate(nowNs)
		switch obs.action {
		case publisherSilenceOnset:
			log.Infof("room_publisher_silent participant=%s session=%s kind=%s track_id=%s silent_ms=%d repeat=0",
				w.participant, w.session, w.kind.String(), w.sourceKey, obs.silentMs)
			w.nudgeIfVideo()
		case publisherSilenceOngoing:
			log.Infof("room_publisher_silent participant=%s session=%s kind=%s track_id=%s silent_ms=%d repeat=%d",
				w.participant, w.session, w.kind.String(), w.sourceKey, obs.silentMs, obs.repeat)
			w.nudgeIfVideo()
		case publisherSilenceRecovered:
			log.Infof("room_publisher_recovered participant=%s session=%s kind=%s track_id=%s silent_ms=%d",
				w.participant, w.session, w.kind.String(), w.sourceKey, obs.silentMs)
		case publisherSilenceNone:
		}
	}
}

// forwardSubscriberRTCP drains RTCP from a subscriber-facing RTPSender and
// forwards keyframe requests (PLI/FIR) to the publisher of the forwarded
// track. Without this read loop the sender's inbound RTCP is never consumed,
// so a subscriber whose decoder lost state (packet loss, backgrounded tab,
// renderer restart) asks for a keyframe and is ignored forever — the tile
// stays frozen until the publisher happens to emit one. Draining also feeds
// the sender's interceptor chain (NACK responder, RTCP reports), which only
// observes inbound RTCP when something reads it. The goroutine exits cleanly
// when the sender is stopped — RemoveTrack calls sender.Stop() and closing
// the PeerConnection stops every sender — because Read then returns an error.
func forwardSubscriberRTCP(sender *webrtc.RTPSender, forwardedTrackID string, subscriberName string, subscriberSession string) {
	if sender == nil {
		return
	}
	for {
		packets, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		if !rtcpHasKeyframeRequest(packets) {
			continue
		}
		requestSourceKeyframe(forwardedTrackID, subscriberName, subscriberSession)
	}
}

// rtcpHasKeyframeRequest reports whether a compound RTCP batch contains a
// keyframe request (PLI or FIR).
func rtcpHasKeyframeRequest(packets []rtcp.Packet) bool {
	for _, packet := range packets {
		switch packet.(type) {
		case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
			return true
		}
	}

	return false
}

// requestSourceKeyframe sends a PLI to the publisher of a forwarded track,
// coalesced through subscriberKeyframeThrottle so concurrent subscriber
// requests cost one keyframe.
func requestSourceKeyframe(forwardedTrackID string, subscriberName string, subscriberSession string) {
	if !subscriberKeyframeThrottle.allow(forwardedTrackID, time.Now()) {
		// another subscriber already asked within the window
		noteThrottledKeyframeRequestDrop(forwardedTrackID, subscriberName, subscriberSession)
		return
	}

	publisherConnection, sourceSSRC, ok := publisherKeyframeTarget(forwardedTrackID)
	if !ok {
		return // publisher already gone; the forwarded track is on its way out
	}

	if err := publisherConnection.WriteRTCP([]rtcp.Packet{
		&rtcp.PictureLossIndication{MediaSSRC: sourceSSRC},
	}); err != nil {
		log.Errorf("room_keyframe_forward_failed track_id=%s ssrc=%d requested_by=%s session=%s error=%v",
			forwardedTrackID, sourceSSRC, subscriberName, subscriberSession, err)
		return
	}
	log.Infof("room_keyframe_forwarded track_id=%s ssrc=%d requested_by=%s session=%s",
		forwardedTrackID, sourceSSRC, subscriberName, subscriberSession)
}

// requestParticipantVideoKeyframes scopes a server-originated keyframe nudge
// to one publisher session and its current video sources. It is used for
// screen-share transitions, where a fresh frame is useful, without making an
// unrelated participant's camera produce a synchronized keyframe burst.
func requestParticipantVideoKeyframes(participantName string, participantSession string, reason string) {
	for _, trackID := range participantVideoTrackIDs(participantName, participantSession) {
		requestSourceKeyframe(trackID, reason, participantSession)
	}
}

func participantVideoTrackIDs(participantName string, participantSession string) []string {
	listLock.RLock()
	defer listLock.RUnlock()

	trackIDs := make([]string, 0)
	for trackID, trackLocal := range trackLocals {
		if trackLocal == nil || trackLocal.Kind() != webrtc.RTPCodecTypeVideo {
			continue
		}
		if !sameParticipantName(trackParticipants[trackID], participantName) || trackParticipantSessions[trackID] != participantSession {
			continue
		}
		trackIDs = append(trackIDs, trackID)
	}
	sort.Strings(trackIDs)

	return trackIDs
}

// publisherKeyframeTarget maps a forwarded track ID back to its source: the
// publisher's PeerConnection (via the trackParticipantSessions bookkeeping)
// and the publisher-side SSRC embedded in the forwarded ID by
// forwardedTrackLocalID, which is the MediaSSRC a PLI toward the publisher
// must carry.
func publisherKeyframeTarget(forwardedTrackID string) (*webrtc.PeerConnection, uint32, bool) {
	sourceSSRC, ok := forwardedTrackSSRC(forwardedTrackID)
	if !ok {
		return nil, 0, false
	}

	listLock.RLock()
	defer listLock.RUnlock()
	trackLocal := trackLocals[forwardedTrackID]
	if trackLocal == nil || trackLocal.Kind() != webrtc.RTPCodecTypeVideo {
		return nil, 0, false
	}
	sessionID := trackParticipantSessions[forwardedTrackID]
	if sessionID == "" {
		return nil, 0, false
	}
	for i := range peerConnections {
		if peerConnections[i].sessionID == sessionID && peerConnections[i].peerConnection != nil {
			return peerConnections[i].peerConnection, sourceSSRC, true
		}
	}

	return nil, 0, false
}

// forwardedTrackSSRC extracts the source SSRC that forwardedTrackLocalID
// appended as the final ":"-separated segment of a forwarded track ID.
func forwardedTrackSSRC(forwardedTrackID string) (uint32, bool) {
	separator := strings.LastIndex(forwardedTrackID, ":")
	if separator < 0 {
		return 0, false
	}
	ssrc, err := strconv.ParseUint(forwardedTrackID[separator+1:], 10, 32)
	if err != nil {
		return 0, false
	}

	return uint32(ssrc), true
}

// logSelectedCandidatePair records which ICE candidate pair a session settled
// on. Mobile telemetry showed sessions dying with an EMPTY selected pair and
// this is the server-side counterpart signal: after a fix attempt (server-side
// TURN relay candidates) it tells us whether mobile sessions now select a
// relay pair, a srflx pair, or still nothing at all.
func logSelectedCandidatePair(peerConnection *webrtc.PeerConnection, participantName string, sessionID string) {
	if peerConnection == nil {
		return
	}
	sctpTransport := peerConnection.SCTP()
	if sctpTransport == nil {
		return
	}
	dtlsTransport := sctpTransport.Transport()
	if dtlsTransport == nil {
		return
	}
	iceTransport := dtlsTransport.ICETransport()
	if iceTransport == nil {
		return
	}
	pair, err := iceTransport.GetSelectedCandidatePair()
	if err != nil || pair == nil || pair.Local == nil || pair.Remote == nil {
		log.Infof("ice_selected_pair participant=%s session=%s pair=none error=%v", participantName, sessionID, err)
		return
	}
	log.Infof("ice_selected_pair participant=%s session=%s local_type=%s local_protocol=%s local=%s:%d remote_type=%s remote_protocol=%s remote=%s:%d",
		participantName, sessionID,
		pair.Local.Typ, pair.Local.Protocol, pair.Local.Address, pair.Local.Port,
		pair.Remote.Typ, pair.Remote.Protocol, pair.Remote.Address, pair.Remote.Port)
}

// Handle incoming websockets.
// sendServerBuildVersion pushes the running build id to a freshly admitted
// socket. A stale tab — old JS whose websocket reconnected across a deploy —
// compares it to the version baked into its HTML and refreshes once when they
// diverge, so joins never flake on version skew. Best-effort.
func sendServerBuildVersion(c *threadSafeWriter) {
	if err := sendKanbanEvent(c, "server_version", serverBuildVersion); err != nil {
		log.Errorf("Failed to send server build version: %v", err)
	}
}

// websocketFrameForLog scrubs secrets out of a raw inbound room frame before
// it reaches the read-loop Info log. The participant hello carries the room
// passcode (§4.5 — "never in URL/logs"), and prod runs PION_LOG_INFO=all, so
// the raw frame must never be echoed verbatim once it mentions one. Frames
// with no "passcode" substring pass through untouched (the hot path: ICE
// candidates, answers, chat); frames that mention one are re-serialized with
// the passcode value replaced, and withheld entirely if they will not parse
// (fail closed — a malformed frame could still hold the secret in cleartext).
func websocketFrameForLog(raw []byte) string {
	if !bytes.Contains(raw, []byte("passcode")) {
		return string(raw)
	}
	var envelope websocketMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Sprintf("[unparseable %d-byte frame withheld: mentions a passcode]", len(raw))
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(envelope.Data), &payload); err == nil {
		if _, ok := payload["passcode"]; ok {
			payload["passcode"] = "[redacted]"
		}
		if scrubbed, err := json.Marshal(payload); err == nil {
			envelope.Data = string(scrubbed)
		} else {
			envelope.Data = "[payload withheld: mentions a passcode]"
		}
	} else {
		envelope.Data = "[payload withheld: mentions a passcode]"
	}
	out, err := json.Marshal(&envelope)
	if err != nil {
		return "[frame withheld: mentions a passcode]"
	}
	return string(out)
}

func websocketHandler(w http.ResponseWriter, r *http.Request) { // nolint
	// Principal + room resolved BEFORE the upgrade (multi-room §4.5): a member
	// session wins; a guest session is accepted only for its own room; neither
	// principal is a cheap pre-upgrade 401 — an unauthenticated socket never
	// allocates a PeerConnection or chat session.
	//
	// EXCEPT when the tab says it is a guest (?as=guest): a /g# tab in a
	// browser that is ALSO signed in as a member carries both cookies, and
	// letting the member session win seated that tab as THE MEMBER under the
	// tab's shared localStorage endpoint id — colliding with the member tab's
	// own seat, so the two tabs replace-evicted each other in an endless duel
	// (both deaf in the room while everyone else saw them fine). A guest-mode
	// dial explicitly NARROWS itself to its guest session; with no live guest
	// session it fails closed with 401, never falls back to the member.
	sessionUser := userFromRequest(r)
	var guest *guestPrincipal
	if r.URL.Query().Get("as") == "guest" {
		sessionUser = nil
		guest = guestFromRequest(r)
	} else if sessionUser == nil {
		guest = guestFromRequest(r)
	}
	if sessionUser == nil && guest == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Missing ?room means office — mid-deploy back-compat for stale tabs; the
	// version-gated auto-refresh reloads them. Guests have their room FORCED
	// from the session: a mismatched ?room is a 403 before any upgrade cost.
	requestedRoom := strings.TrimSpace(r.URL.Query().Get("room"))
	connRoomID := normalizeRoomID(requestedRoom)
	if guest != nil {
		if requestedRoom != "" && connRoomID != normalizeRoomID(guest.RoomID) {
			http.Error(w, "guests can only join their own room", http.StatusForbidden)
			return
		}
		connRoomID = normalizeRoomID(guest.RoomID)
	}
	if room, ok := appRoomStore().byID(connRoomID); !ok {
		http.Error(w, "room not found", http.StatusNotFound)
		return
	} else if room.Archived {
		http.Error(w, "room is archived", http.StatusForbidden)
		return
	}

	// §6.1 numeric guest caps, checked pre-upgrade and rejected with 429:
	// sockets per guest session, pre-hello sockets per client IP, and the
	// per-room guest seat cap (advisory here, authoritative at admission).
	clientIP := clientIPForRateLimit(r)
	var admitGuestCaps func()
	var releaseGuestCaps func()
	if guest != nil {
		admit, release, ok := guestSocketCaps.acquire(guest.SessionKey, clientIP)
		if !ok {
			http.Error(w, "too many guest connections", http.StatusTooManyRequests)
			return
		}
		if kanbanApp != nil && kanbanApp.guestRoomAtCapacity(connRoomID, guest.SessionKey) {
			release()
			http.Error(w, "this room already has its maximum number of guests", http.StatusTooManyRequests)
			return
		}
		admitGuestCaps, releaseGuestCaps = admit, release
	}

	// Upgrade HTTP request to Websocket
	unsafeConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Errorf("Failed to upgrade HTTP to Websocket: ", err)
		if releaseGuestCaps != nil {
			releaseGuestCaps()
		}

		return
	}

	// A hijacked websocket outlives httptest.Server.Close; count this goroutine
	// so tests can wait for it to fully drain (all deferred cleanup that touches
	// package globals has run) before mutating those globals. Decremented last,
	// after every other defer below.
	activeWebsocketHandlers.Add(1)
	defer activeWebsocketHandlers.Add(-1)
	if releaseGuestCaps != nil {
		defer releaseGuestCaps()
	}

	// Bound per-message memory: signaling frames (SDP/ICE/board events) are small;
	// reject oversized frames so an unauthenticated client cannot stream an
	// arbitrarily large frame and force the server to buffer it before parsing.
	unsafeConn.SetReadLimit(maxWebsocketMessageBytes)

	c := &threadSafeWriter{Conn: unsafeConn, guest: guest != nil} // nolint
	scoutChat := newScoutChatSession(c)
	// Stop the chat worker and cancel any queued/in-flight model calls as
	// soon as this connection ends.
	defer func() { scoutChat.close() }()
	sessionEmail := ""
	if sessionUser != nil {
		sessionEmail = sessionUser.Email
	}
	participantName := "participant"
	participantSessionID := nextParticipantSessionID()
	// endpointID is the stable per-device id from the participant hello. It is
	// set once at admission and only ever read afterwards on this same read-loop
	// goroutine, so it needs no lock. Empty for legacy/native clients; guests
	// use their per-socket session id (each socket is an endpoint of the seat).
	endpointID := ""
	participantAccepted := false
	officeAccepted := false
	mediaJoined := false
	// Per-socket rate-limited logging for member request_participant_tracks
	// (S2, 2026-07-10 incident): only this read-loop goroutine touches these.
	memberRepairLogAt := time.Time{}
	memberRepairLogsSuppressed := 0
	memberRepairDropLogAt := time.Time{}
	memberRepairDropsSuppressed := 0
	// Per-socket rate-limited logging for restart_ice denials (card-003 W4):
	// only this read-loop goroutine touches these. Accepted restarts already
	// log unconditionally below (and are now bucket-capped, so they can't
	// storm) — only the drops need the 10s throttle.
	iceRestartDropLogAt := time.Time{}
	iceRestartDropsSuppressed := 0
	var participantAcceptedState atomic.Bool
	var mediaJoinedState atomic.Bool
	var cleanupOnce sync.Once
	pendingRemoteCandidates := pendingRemoteICECandidateQueue{}
	participantMu := sync.Mutex{}
	currentParticipantName := func() string {
		participantMu.Lock()
		defer participantMu.Unlock()
		return participantName
	}
	setParticipantName := func(name string) {
		participantMu.Lock()
		participantName = name
		participantMu.Unlock()
	}
	cleanupParticipantSession := func(reason string, closeSocket bool) {
		cleanupOnce.Do(func() {
			if participantAcceptedState.Load() {
				name := currentParticipantName()
				unregisterParticipantSession(name, participantSessionID)
				if guest == nil {
					// Member repair + ice-restart buckets key on this per-socket
					// session id — release them or the room's maps grow one entry
					// per connection.
					kanbanApp.dropMemberMediaRepairBucket(connRoomID, participantSessionID)
					kanbanApp.dropMemberIceRestartBucket(connRoomID, participantSessionID)
				}
				if removed, stillPresent := kanbanApp.forgetParticipantSessionResultInRoom(connRoomID, name, participantSessionID); removed {
					// participant_left means a PERSON left. When one of an
					// account's two devices drops but another stays connected,
					// the person is still here: suppress the "left" (peers would
					// otherwise tear down that account's live tile) but still
					// refresh the roster so the "· 2 devices" affordance drops
					// back to one. Only arm the idle-end timer once the last
					// device is gone.
					if !stillPresent {
						broadcastRoomKanbanEvent(connRoomID, "participant_left", map[string]any{
							"name":   name,
							"roomId": connRoomID,
						})
					}
					broadcastRoomKanbanEvent(connRoomID, "participants", kanbanApp.roomSnapshotForRoom(connRoomID))
					if guest != nil {
						kanbanApp.releaseGuestSeatIfGone(connRoomID, guest.SessionKey)
					}
					if !stillPresent {
						kanbanApp.noteMeetingOccupancy(connRoomID)
					}
					broadcastRoomsSnapshot()
				}
			}
			if closeSocket {
				if reason != "" {
					_ = sendKanbanEvent(c, "media_disconnected", reason)
				}
				_ = c.Close()
			}
		})
	}

	// When this frame returns close the Websocket
	defer c.Close() //nolint
	stopHeartbeat := startRoomWebsocketHeartbeat(c, currentParticipantName, participantSessionID)
	defer stopHeartbeat()
	defer cleanupParticipantSession("", false)
	// Office sockets registered via the `office` hello are reaped
	// unconditionally when the read loop exits; participantSessionID is
	// unique per socket, so this never touches another connection's entry.
	defer func() {
		listLock.Lock()
		delete(officeConnections, participantSessionID)
		listLock.Unlock()
	}()

	// The PeerConnection is created immediately for members (today's flow) but
	// DEFERRED for guests until their participant hello is admitted (§6.1: the
	// strongest fix for the pre-hello allocation DoS — an unadmitted guest
	// socket never owns transceivers or ICE state). Only the read-loop
	// goroutine assigns peerConnection; the media callbacks capture the pc
	// they were installed on.
	var peerConnection *webrtc.PeerConnection
	defer func() {
		if peerConnection != nil {
			if err := peerConnection.Close(); err != nil {
				log.Errorf("Failed to close read-loop PeerConnection session=%s: %v", participantSessionID, err)
			}
		}
	}()
	cancelICERecovery := func() {}
	defer func() { cancelICERecovery() }()

	setupPeerConnection := func() error {
		pc, err := newRoomPeerConnection()
		if err != nil {
			return err
		}
		websocketPeerAllocations.Add(1)

		// Accept one audio and one video track incoming
		for _, typ := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio} {
			if _, err := pc.AddTransceiverFromKind(typ, webrtc.RTPTransceiverInit{
				Direction: webrtc.RTPTransceiverDirectionRecvonly,
			}); err != nil {
				_ = pc.Close()
				return err
			}
		}

		// Trickle ICE. Emit server candidate to client
		pc.OnICECandidate(func(i *webrtc.ICECandidate) {
			if i == nil {
				return
			}
			// If you are serializing a candidate make sure to use ToJSON
			// Using Marshal will result in errors around `sdpMid`
			candidateString, err := json.Marshal(i.ToJSON())
			if err != nil {
				log.Errorf("Failed to marshal candidate to json: %v", err)

				return
			}

			log.Infof("Send candidate to client: %s", candidateString)

			if writeErr := c.WriteJSON(&websocketMessage{
				Event: "candidate",
				Data:  string(candidateString),
			}); writeErr != nil {
				log.Errorf("Failed to write JSON: %v", writeErr)
			}
		})

		// If PeerConnection is closed remove it from global list
		pc.OnConnectionStateChange(func(p webrtc.PeerConnectionState) {
			log.Infof("Connection state change: %s", p)

			switch p {
			case webrtc.PeerConnectionStateFailed:
				if mediaJoinedState.Load() {
					cleanupParticipantSession("media connection failed; rejoin the room.", true)
				}
				// Drop the dead peer from the fan-out pool before closing so it stops
				// receiving forwarded media immediately, then release its transports.
				prunePeerConnectionPool(pc)
				if err := pc.Close(); err != nil {
					log.Errorf("Failed to close PeerConnection: %v", err)
				}
			case webrtc.PeerConnectionStateClosed:
				prunePeerConnectionPool(pc)
				if mediaJoinedState.Load() {
					cleanupParticipantSession("", false)
				} else {
					signalPeerConnections()
				}
			default:
			}
		})

		pc.OnTrack(func(t *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
			trackParticipantName := currentParticipantName()
			trackParticipantSessionID := participantSessionID
			forwardedTrackID := forwardedRemoteTrackID(t)
			// The ids THIS publisher negotiated for sdes:mid/rid/repaired-rid;
			// forwardPublisherRTP strips them per packet (transport-scoped,
			// never valid on a subscriber's transport).
			stripExtensionIDs := transportScopedRTPExtensionIDs(receiver)
			codec := t.Codec()
			log.Infof("room_ontrack_start participant=%s session=%s room=%s kind=%s track_id=%s source_track_id=%s stream_id=%s rid=%q ssrc=%d rtx_ssrc=%d payload_type=%d codec=%s clock_rate=%d channels=%d fmtp=%q feedback=%s has_rtx=%t",
				trackParticipantName, trackParticipantSessionID, connRoomID, t.Kind(), forwardedTrackID, t.ID(), t.StreamID(), t.RID(), t.SSRC(), t.RtxSSRC(), t.PayloadType(), codec.MimeType, codec.ClockRate, codec.Channels, codec.SDPFmtpLine, rtcpFeedbackSummary(codec.RTCPFeedback), t.HasRTX())
			broadcastAssistantEvent("signal", fmt.Sprintf("received %s track from browser", t.Kind().String()), map[string]any{
				"participant":   trackParticipantName,
				"trackId":       forwardedTrackID,
				"sourceTrackId": t.ID(),
				"streamId":      t.StreamID(),
				"payloadType":   t.PayloadType(),
			})

			// Create a track to fan out our incoming media to all browser peers
			// of THIS room only (trackRooms + acceptsTrack, multi-room W3).
			trackLocal, err := addTrack(connRoomID, t, trackParticipantName, trackParticipantSessionID)
			if err != nil {
				log.Errorf("Failed to create local track for remote track=%s: %v", t.ID(), err)
				return
			}
			trackPayload := participantTrackPayload(trackParticipantName, t)
			trackPayload["roomId"] = connRoomID
			broadcastRoomKanbanEvent(connRoomID, "participant_track", trackPayload)
			signalPeerConnections()
			defer removeTrack(trackLocal)

			// Silence watchdog: watch this publisher track for a stalled uplink
			// (RTP stops while the PeerConnection stays Connected). The read loop
			// below stamps lastRTPNanos per packet; the 3s sweep flags a stall.
			silenceWatch := publisherSilence.register(forwardedTrackID, trackParticipantName, trackParticipantSessionID, t.Kind(), pc)
			defer publisherSilence.forget(silenceWatch)

			audioDecoder, audioChannels, err := newRoomAudioDecoder(t)
			if err != nil {
				log.Errorf("Failed to create audio decoder for track=%s: %v", t.ID(), err)
			}
			audioTrackKey := roomAudioTrackKey(t)
			if audioDecoder != nil {
				// remove from whatever mixer the room holds at teardown time —
				// the lazy lifecycle may have replaced it mid-session.
				defer func() {
					kanbanApp.roomMixerFor(connRoomID).removeTrack(audioTrackKey)
				}()
			}
			audioDecodeBuffer := make([]int16, roomAudioDecodeBufferSize(audioChannels))
			announcedAudioPacket := false
			announcedDecodedAudio := false
			announcedRTPDetails := false
			onTrackStartedAt := time.Now()
			packetsForwarded := 0
			payloadBytesForwarded := 0
			defer func() {
				log.Infof("room_ontrack_end participant=%s session=%s kind=%s track_id=%s source_track_id=%s stream_id=%s packets=%d payload_bytes=%d duration=%s",
					trackParticipantName, trackParticipantSessionID, t.Kind(), forwardedTrackID, t.ID(), t.StreamID(), packetsForwarded, payloadBytesForwarded, time.Since(onTrackStartedAt).Round(time.Millisecond))
			}()

			for {
				packet, _, err := t.ReadRTP()
				if err != nil {
					log.Infof("room_ontrack_read_end participant=%s session=%s kind=%s track_id=%s source_track_id=%s packets=%d error=%v",
						trackParticipantName, trackParticipantSessionID, t.Kind(), forwardedTrackID, t.ID(), packetsForwarded, err)
					return
				}
				// Hot-path silence stamp: one atomic store of the receive time —
				// the sweep reads it to detect a stalled uplink. Kept branch-free
				// and dwarfed by the per-packet decode/forward already below.
				silenceWatch.lastRTPNanos.Store(time.Now().UnixNano())
				if !announcedRTPDetails {
					announcedRTPDetails = true
					log.Infof("room_ontrack_first_rtp participant=%s session=%s kind=%s track_id=%s source_track_id=%s sequence=%d marker=%t payload_type=%d payload_bytes=%d extension_profile=0x%x extension_ids=%s",
						trackParticipantName, trackParticipantSessionID, t.Kind(), forwardedTrackID, t.ID(), packet.SequenceNumber, packet.Marker, packet.PayloadType, len(packet.Payload), packet.ExtensionProfile, rtpExtensionIDSummary(packet.GetExtensionIDs()))
				}

				if audioDecoder != nil {
					if !announcedAudioPacket {
						announcedAudioPacket = true
						broadcastAssistantEvent("audio", "browser microphone packets are reaching the server", nil)
					}
					pcm, decodeErr := decodeOpusToRoomPCM(audioDecoder, audioDecodeBuffer, audioChannels, packet.Payload)
					if decodeErr != nil {
						log.Errorf("Failed to decode room audio for track=%s: %v", t.ID(), decodeErr)
						if !announcedDecodedAudio {
							broadcastAssistantEvent("error", "server could not decode microphone audio: "+decodeErr.Error(), nil)
							announcedDecodedAudio = true
						}
					} else {
						if !announcedDecodedAudio && len(pcm) > 0 {
							announcedDecodedAudio = true
							broadcastAssistantEvent("audio", "browser microphone audio decoded on the server", nil)
						}
						// nil-mixer frames (a join racing the lazy teardown) are
						// dropped by the nil-safe mixer methods.
						kanbanApp.roomMixerFor(connRoomID).submit(audioTrackKey, trackParticipantName, pcm)
					}
				}

				// Preserve RTP header extensions from the publisher when they are
				// media-scoped (mobile browsers carry video orientation/rotation and
				// congestion metadata there; stripping them makes phone video look
				// unstable to subscribers — 0a46b50) while dropping the
				// Transport-scoped SDES/TWCC ids, which are only meaningful on the
				// publisher's own transport (see stripTransportScopedRTPExtensions).
				if err = forwardPublisherRTP(trackLocal, packet, stripExtensionIDs); err != nil {
					log.Errorf("room_ontrack_write_failed participant=%s session=%s kind=%s track_id=%s source_track_id=%s sequence=%d payload_type=%d extension_profile=0x%x extension_ids=%s packets=%d error=%v",
						trackParticipantName, trackParticipantSessionID, t.Kind(), forwardedTrackID, t.ID(), packet.SequenceNumber, packet.PayloadType, packet.ExtensionProfile, rtpExtensionIDSummary(packet.GetExtensionIDs()), packetsForwarded, err)
					return
				}
				packetsForwarded++
				payloadBytesForwarded += len(packet.Payload)
			}
		})

		// Proactively recover from network changes. When ICE drops to
		// "disconnected" we wait out a short grace window (transient blips self-heal)
		// and, if still unhealthy, trigger a server-side ICE restart that refreshes
		// ICE credentials and renegotiates — reconnecting the peer without making the
		// browser notice. Returning to "connected"/"completed" cancels a pending
		// attempt.
		var iceRecoveryMu sync.Mutex
		var iceRecoveryTimer *time.Timer
		cancelICERecovery = func() {
			iceRecoveryMu.Lock()
			defer iceRecoveryMu.Unlock()
			if iceRecoveryTimer != nil {
				iceRecoveryTimer.Stop()
				iceRecoveryTimer = nil
			}
		}
		scheduleICERecovery := func() {
			iceRecoveryMu.Lock()
			defer iceRecoveryMu.Unlock()
			if iceRecoveryTimer != nil {
				return // recovery already pending for this disconnect episode
			}
			iceRecoveryTimer = time.AfterFunc(iceDisconnectGrace, func() {
				iceRecoveryMu.Lock()
				iceRecoveryTimer = nil
				iceRecoveryMu.Unlock()

				if !mediaJoinedState.Load() {
					return
				}
				if !iceStateNeedsRecovery(pc.ICEConnectionState()) {
					return // recovered on its own during the grace window
				}
				totalTracks, audioTracks, videoTracks := snapshotForwardedTrackCounts()
				log.Infof("ICE still disconnected after grace; restarting ICE for participant=%s session=%s total_tracks=%d audio_tracks=%d video_tracks=%d", currentParticipantName(), participantSessionID, totalTracks, audioTracks, videoTracks)
				signalPeerConnectionICE(pc)
			})
		}

		pc.OnICEConnectionStateChange(func(is webrtc.ICEConnectionState) {
			log.Infof("ICE connection state changed: %s", is)
			notePeerConnectionICEState(pc, is)
			switch {
			case iceStateNeedsRecovery(is):
				scheduleICERecovery()
			case is == webrtc.ICEConnectionStateConnected || is == webrtc.ICEConnectionStateCompleted:
				cancelICERecovery()
				logSelectedCandidatePair(pc, currentParticipantName(), participantSessionID)
			}
		})

		peerConnection = pc
		return nil
	}

	if guest == nil {
		if err := setupPeerConnection(); err != nil {
			log.Errorf("Failed to creates a PeerConnection: %v", err)

			return
		}
	}

	message := &websocketMessage{}
	for {
		_, raw, err := c.ReadMessage()
		if err != nil {
			if isWebsocketReadTimeout(err) {
				log.Infof("room_ws_read_timeout participant=%s session=%s timeout=%s; cleaning up half-open session", currentParticipantName(), participantSessionID, websocketReadTimeout)
			} else if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
				log.Errorf("Failed to read message: %v", err)
			} else {
				log.Infof("WebSocket closed: %v", err)
			}

			return
		}

		log.Infof("Got message: %s", websocketFrameForLog(raw))

		// An inbound room frame is proof of life for the liveness sweep, alongside
		// the heartbeat pong (no-op until this socket is admitted).
		kanbanApp.touchParticipantSessionLivenessInRoom(connRoomID, currentParticipantName(), participantSessionID)

		if err := json.Unmarshal(raw, &message); err != nil {
			log.Errorf("Failed to unmarshal json to message: %v", err)

			return
		}

		// §5.4 guest inbound containment: the hello, signaling, liveness, and
		// room chat pass; the office hello is denied and closed; anything else
		// is dropped and logged.
		if guest != nil {
			if message.Event == "office" {
				_ = sendKanbanEvent(c, "access_denied", "Guests cannot open office delivery.")
				return
			}
			if !guestInboundEvents[message.Event] {
				log.Infof("guest_inbound_dropped event=%s session=%s", message.Event, participantSessionID)
				continue
			}
		}

		if participantAccepted && message.Event != "participant" && !kanbanApp.participantSessionCurrentInRoom(connRoomID, currentParticipantName(), participantSessionID) {
			_ = sendKanbanEvent(c, "session_replaced", "This browser session was replaced by a newer room join.")
			return
		}

		switch message.Event {
		case "participant":
			if participantAccepted {
				continue
			}
			if guest != nil {
				// Guest identity comes from the guest session, never the hello
				// payload; the display name is server-prefixed and deduped, and
				// the seat keys on the guest session (two guests named Sam
				// coexist; a second socket of one session shares its seat).
				admittedName, firstEndpoint, err := kanbanApp.admitGuestParticipant(connRoomID, guest.SessionKey, guest.Name, participantSessionID)
				if err != nil {
					_ = sendKanbanEvent(c, "access_denied", err.Error()+".")
					continue
				}
				endpointID = participantSessionID
				setParticipantName(admittedName)
				participantAccepted = true
				participantAcceptedState.Store(true)
				if admitGuestCaps != nil {
					admitGuestCaps()
				}
				// The PeerConnection exists only NOW (§6.1 deferred alloc).
				if peerConnection == nil {
					if err := setupPeerConnection(); err != nil {
						log.Errorf("Failed to create guest PeerConnection: %v", err)
						_ = sendKanbanEvent(c, "access_denied", "could not start media for this room.")
						return
					}
				}
				replaceExistingParticipantSessionEndpointInRoom(connRoomID, admittedName, participantSessionID, endpointID, peerConnection, c, "")
				kanbanApp.noteMeetingAdmission(connRoomID, admittedName)
				kanbanApp.ensureRoomMedia(connRoomID)
				// §5.4(d): the guest replay branch withholds board/memory/
				// notifications/proposals — only room-scoped, allowlisted state.
				if err := sendKanbanEvent(c, "access_granted", map[string]any{
					"name":   admittedName,
					"roomId": connRoomID,
					"guest":  true,
				}); err != nil {
					log.Errorf("Failed to send guest access grant: %v", err)
				}
				if err := sendKanbanEvent(c, "participants", kanbanApp.roomSnapshotForRoom(connRoomID)); err != nil {
					log.Errorf("Failed to send participant state: %v", err)
				}
				if err := sendKanbanEvent(c, "room_chat_history", kanbanApp.roomChatHistoryForRoom(connRoomID, roomChatHistoryLimit)); err != nil {
					log.Errorf("Failed to send room chat history: %v", err)
				}
				if err := sendKanbanEvent(c, "meeting", kanbanApp.meetingSnapshot(connRoomID)); err != nil {
					log.Errorf("Failed to send meeting record: %v", err)
				}
				sendServerBuildVersion(c)
				if firstEndpoint {
					broadcastRoomKanbanEvent(connRoomID, "participant_joined", map[string]any{
						"name":   admittedName,
						"roomId": connRoomID,
					})
				}
				broadcastRoomKanbanEvent(connRoomID, "participants", kanbanApp.roomSnapshotForRoom(connRoomID))
				broadcastRoomsSnapshot()
				continue
			}

			// Identity comes from the authenticated session, never from the
			// payload: a client cannot join as anyone but their own account.
			name := participantNameForAccount(sessionUser)
			// The endpoint id is an additive, optional hint carried in the
			// hello payload (never identity — that still comes from the
			// session). Absent/invalid ids fall back to the legacy single-slot
			// behaviour. The passcode rides the same hello (§4.5) — never the
			// URL, never logged.
			helloPasscode := ""
			if trimmed := strings.TrimSpace(message.Data); trimmed != "" {
				var hello struct {
					EndpointID string `json:"endpointId"`
					Passcode   string `json:"passcode"`
				}
				if err := json.Unmarshal([]byte(trimmed), &hello); err == nil {
					endpointID = sanitizeEndpointID(hello.EndpointID)
					helloPasscode = hello.Passcode
				}
			}
			room, roomOK := appRoomStore().byID(connRoomID)
			if !roomOK || room.Archived {
				_ = sendKanbanEvent(c, "access_denied", "this room is no longer joinable.")
				continue
			}
			// The passcode is admission-only — NEVER an API credential
			// anywhere else. bcrypt compare behind the shared auth limiter.
			if room.PasscodeHash != "" {
				if !authAttemptAllowedForKeys("roompass:"+connRoomID, "roompass-ip:"+clientIP) {
					_ = sendKanbanEvent(c, "access_denied", "too many passcode attempts; try again in a few minutes.")
					continue
				}
				if !roomPasscodeOK(room, helloPasscode) {
					_ = sendKanbanEvent(c, "access_denied", "wrong room passcode.")
					continue
				}
				clearAuthAttempts("roompass:"+connRoomID, "roompass-ip:"+clientIP)
			}
			admittedName, firstEndpoint, err := kanbanApp.admitParticipantSessionEndpointInRoom(connRoomID, name, participantSessionID, endpointID)
			if err != nil {
				_ = sendKanbanEvent(c, "access_denied", err.Error()+".")
				continue
			}
			setParticipantName(admittedName)
			participantAccepted = true
			participantAcceptedState.Store(true)
			replaceExistingParticipantSessionEndpointInRoom(connRoomID, admittedName, participantSessionID, endpointID, peerConnection, c, sessionEmail)
			// One account, one live room (§2): joining here evicts the
			// account's seat in any other room via session_replaced.
			kanbanApp.evictAccountFromOtherRooms(admittedName, connRoomID)
			// admission opens (or extends) the first-class meeting record and
			// cancels any pending idle-end from a briefly empty room; named
			// rooms lazily start their mixer + transcription lane here.
			kanbanApp.noteMeetingAdmission(connRoomID, admittedName)
			kanbanApp.ensureRoomMedia(connRoomID)
			if err := sendKanbanEvent(c, "access_granted", map[string]any{
				"name":   admittedName,
				"roomId": connRoomID,
			}); err != nil {
				log.Errorf("Failed to send access grant: %v", err)
			}
			if err := sendKanbanEvent(c, "participants", kanbanApp.roomSnapshotForRoom(connRoomID)); err != nil {
				log.Errorf("Failed to send participant state: %v", err)
			}
			if err := sendKanbanEvent(c, "board", kanbanApp.snapshotState()); err != nil {
				log.Errorf("Failed to send Kanban board state: %v", err)
			}
			if err := sendKanbanEvent(c, "undo_available", kanbanApp.canUndoDelete()); err != nil {
				log.Errorf("Failed to send undo state: %v", err)
			}
			if err := sendKanbanEvent(c, "memory", kanbanApp.memorySnapshotForClients(20)); err != nil {
				log.Errorf("Failed to send meeting memory: %v", err)
			}
			// Direct send: this socket is not in peerConnections until
			// media_ready, so broadcasts cannot reach it yet.
			if err := sendKanbanEvent(c, "room_chat_history", kanbanApp.roomChatHistoryForRoom(connRoomID, roomChatHistoryLimit)); err != nil {
				log.Errorf("Failed to send room chat history: %v", err)
			}
			// Meeting record snapshot (null clears client state): the shared
			// clock anchor must land before media join.
			if err := sendKanbanEvent(c, "meeting", kanbanApp.meetingSnapshot(connRoomID)); err != nil {
				log.Errorf("Failed to send meeting record: %v", err)
			}
			// Unread notification backlog is per-account, so it replays as a
			// direct send scoped to this socket's session user.
			if err := sendKanbanEvent(c, "notification_backlog", kanbanApp.unreadNotificationsFor(sessionEmail, notificationListLimit)); err != nil {
				log.Errorf("Failed to send notification backlog: %v", err)
			}
			// Codex proposals render as confirm/dismiss cards in the room;
			// replay them directly so a fresh joiner can act on pending ones.
			if err := sendKanbanEvent(c, "codex_proposals", kanbanApp.codexProposalsSnapshot(codexProposalHistoryLimit)); err != nil {
				log.Errorf("Failed to send codex proposals: %v", err)
			}
			if err := sendKanbanEvent(c, "status", "Connected to conference room"); err != nil {
				log.Errorf("Failed to send Kanban status: %v", err)
			}
			sendServerBuildVersion(c)
			if assistantStatus := kanbanApp.assistantStatusSnapshot(); assistantStatus != nil {
				if err := sendKanbanEvent(c, "assistant_event", assistantStatus); err != nil {
					log.Errorf("Failed to send assistant status: %v", err)
				}
			}
			if activeSpeaker := kanbanApp.activeSpeakerSnapshotForRoom(connRoomID); activeSpeaker != nil {
				if err := sendKanbanEvent(c, "active_speaker", activeSpeaker); err != nil {
					log.Errorf("Failed to send active speaker snapshot: %v", err)
				}
			}
			// participant_joined announces a PERSON arriving (status line +
			// roster media reset on peers). Fire it only when this admission
			// brought the account from absent to present; a second device of an
			// already-present account is not a new arrival and must not make
			// peers nuke that account's existing tile or log a false "joined".
			// The roster snapshot below still refreshes unconditionally so the
			// "· 2 devices" affordance updates.
			if firstEndpoint {
				broadcastRoomKanbanEvent(connRoomID, "participant_joined", map[string]any{
					"name":   admittedName,
					"roomId": connRoomID,
				})
			}
			broadcastRoomKanbanEvent(connRoomID, "participants", kanbanApp.roomSnapshotForRoom(connRoomID))
			broadcastRoomsSnapshot()
		case "office":
			// Office hello: an authenticated tab claims signed-in event
			// delivery without taking a room seat. Identity comes from the
			// session cookie, never the payload; repeat hellos are
			// idempotent. Room-gated cases below still require admission, so
			// an office socket that tries to edit the board or speak gets
			// access_denied like any other unadmitted socket. (Guests never
			// reach here — their office hello was denied above.)
			if officeAccepted {
				continue
			}
			officeAccepted = true
			listLock.Lock()
			officeConnections[participantSessionID] = officeConnectionState{
				websocket:    c,
				sessionEmail: normalizeAccountEmail(sessionEmail),
			}
			listLock.Unlock()
			if err := sendKanbanEvent(c, "office_granted", map[string]any{
				"email": sessionEmail,
				"name":  participantNameForAccount(sessionUser),
			}); err != nil {
				log.Errorf("Failed to send office grant: %v", err)
			}
			// Direct replay of the signed-in-safe state: this socket is not
			// in peerConnections, so broadcasts alone cannot seed it.
			if err := sendKanbanEvent(c, "board", kanbanApp.snapshotState()); err != nil {
				log.Errorf("Failed to send Kanban board state: %v", err)
			}
			if err := sendKanbanEvent(c, "undo_available", kanbanApp.canUndoDelete()); err != nil {
				log.Errorf("Failed to send undo state: %v", err)
			}
			if err := sendKanbanEvent(c, "memory", kanbanApp.memorySnapshotForClients(20)); err != nil {
				log.Errorf("Failed to send meeting memory: %v", err)
			}
			// Meeting record snapshot (null clears client state) keeps the
			// office shell's shared clock anchored while out of the room.
			if err := sendKanbanEvent(c, "meeting", kanbanApp.meetingSnapshot(officeRoomID)); err != nil {
				log.Errorf("Failed to send meeting record: %v", err)
			}
			// Room-chat history seeds the out-of-room unread pip.
			if err := sendKanbanEvent(c, "room_chat_history", kanbanApp.roomChatHistory(roomChatHistoryLimit)); err != nil {
				log.Errorf("Failed to send room chat history: %v", err)
			}
			// Unread notification backlog is per-account, so it replays as a
			// direct send scoped to this socket's session user.
			if err := sendKanbanEvent(c, "notification_backlog", kanbanApp.unreadNotificationsFor(sessionEmail, notificationListLimit)); err != nil {
				log.Errorf("Failed to send notification backlog: %v", err)
			}
			if err := sendKanbanEvent(c, "codex_proposals", kanbanApp.codexProposalsSnapshot(codexProposalHistoryLimit)); err != nil {
				log.Errorf("Failed to send codex proposals: %v", err)
			}
			sendServerBuildVersion(c)
		case "office_ping":
			// Client-side liveness probe: the office tab pings so it can tell
			// a healthy-but-quiet socket from a dead one (and re-dial before
			// live chat_thread delivery silently stops) without forcing a
			// page refresh — which would drop a live video room seat. Answer
			// on the same socket, top-level like the signaling events; no
			// state changes, no auth beyond the session that opened the
			// socket.
			if err := c.WriteJSON(&websocketMessage{Event: "office_pong"}); err != nil {
				log.Errorf("Failed to send office pong: %v", err)
			}
		case "room_ping":
			// Client room-liveness heartbeat (mirrors office_ping). The read loop
			// already refreshed this participant's liveness stamp on this inbound
			// frame above; unlike the network-layer pong it STOPS when the tab is
			// backgrounded/frozen, so its absence is precisely what lets the
			// liveness sweep reap a zombie seat and finalize an empty sitting. No
			// reply is needed — the server side is the only consumer.
		case "media_ready":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "Enter the room before joining media.")
				continue
			}
			if mediaJoined || peerConnection == nil {
				continue
			}
			mediaJoined = true
			mediaJoinedState.Store(true)
			listLock.Lock()
			peerConnections = append(peerConnections, peerConnectionState{
				peerConnection:  peerConnection,
				websocket:       c,
				participantName: currentParticipantName(),
				sessionID:       participantSessionID,
				endpointID:      endpointID,
				sessionEmail:    normalizeAccountEmail(sessionEmail),
				roomID:          connRoomID,
			})
			listLock.Unlock()
			if guest == nil {
				if err := sendKanbanEvent(c, "board", kanbanApp.snapshotState()); err != nil {
					log.Errorf("Failed to send Kanban board state after media join: %v", err)
				}
				if err := sendKanbanEvent(c, "undo_available", kanbanApp.canUndoDelete()); err != nil {
					log.Errorf("Failed to send undo state after media join: %v", err)
				}
			}
			sendParticipantTrackSnapshots(c, connRoomID, currentParticipantName())
			broadcastRoomKanbanEvent(connRoomID, "participants", kanbanApp.roomSnapshotForRoom(connRoomID))
			signalPeerConnections()
		case "request_participant_tracks":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "Enter the room before requesting media labels.")
				continue
			}
			// §6.5 hardening: replays room track snapshots + runs a global
			// peer-sync walk, so token-bucket guests to keep a guest-link holder
			// from forcing that fan-out at socket line rate.
			if guest != nil && !kanbanApp.allowGuestMediaStateEvent(connRoomID, guest.SessionKey, time.Now()) {
				log.Infof("guest_media_repair_rate_limited session=%s room=%s", participantSessionID, connRoomID)
				continue
			}
			// 2026-07-10 keyframe-spiral incident: members were unbucketed AND
			// unlogged — 193 repair messages in ~4 minutes each ran the global
			// walk below and were invisible in forensics. Members now spend a
			// generous per-session bucket (burst 4 covers a just-joined member's
			// first snapshot request; refill 1 per 5s clears the client's ~6s
			// legit repair cadence). On limit: drop silently toward the client,
			// with a rate-limited drop log. Accepted requests are logged at most
			// once per 10s per session with a suppressed-count so a repair storm
			// can't become a log storm.
			if guest == nil {
				repairNow := time.Now()
				reason := participantTrackRefreshReason(message.Data)
				if !kanbanApp.allowMemberMediaRepair(connRoomID, participantSessionID, repairNow) {
					memberRepairDropsSuppressed++
					if repairNow.Sub(memberRepairDropLogAt) >= memberMediaRepairLogInterval {
						log.Infof("member_media_repair_rate_limited session=%s room=%s participant=%s reason=%q dropped=%d",
							participantSessionID, connRoomID, currentParticipantName(), reason, memberRepairDropsSuppressed)
						memberRepairDropLogAt = repairNow
						memberRepairDropsSuppressed = 0
					}
					continue
				}
				if repairNow.Sub(memberRepairLogAt) >= memberMediaRepairLogInterval {
					log.Infof("member_media_repair session=%s room=%s participant=%s reason=%q suppressed=%d",
						participantSessionID, connRoomID, currentParticipantName(), reason, memberRepairLogsSuppressed)
					memberRepairLogAt = repairNow
					memberRepairLogsSuppressed = 0
				} else {
					memberRepairLogsSuppressed++
				}
			}
			sendParticipantTrackSnapshots(c, connRoomID, currentParticipantName())
			signalPeerConnections()
		case "candidate":
			if peerConnection == nil {
				continue
			}
			candidate := webrtc.ICECandidateInit{}
			if err := json.Unmarshal([]byte(message.Data), &candidate); err != nil {
				log.Errorf("Failed to unmarshal json to candidate: %v", err)

				return
			}

			log.Infof("Got candidate: %v", candidate)

			if remoteICECandidateShouldQueue(candidate, peerConnection.RemoteDescription()) {
				queued, evicted := pendingRemoteCandidates.enqueue(candidate)
				switch {
				case !queued:
					log.Infof("Coalesced duplicate ICE candidate while waiting for matching remote description")
				case evicted:
					log.Infof("Queued ICE candidate for a future remote description; evicted oldest candidate at bound=%d", maxPendingRemoteICECandidates)
				default:
					log.Infof("Queued ICE candidate until its matching remote description is set")
				}
				continue
			}

			if err := peerConnection.AddICECandidate(candidate); err != nil {
				log.Errorf("Failed to add ICE candidate: %v", err)
			}
		case "answer":
			if peerConnection == nil {
				continue
			}
			answer := webrtc.SessionDescription{}
			if err := json.Unmarshal([]byte(message.Data), &answer); err != nil {
				log.Errorf("Failed to unmarshal json to answer: %v", err)

				return
			}

			answerMetadata := signalingMetadataFromMessage(*message)
			pendingMetadata := currentPendingOfferMetadata(peerConnection)
			if ignore, reason := shouldIgnoreAnswerForPendingOffer(answerMetadata, pendingMetadata); ignore {
				log.Infof("room_signal_answer_stale participant=%s session=%s reason=%q answer_offer_id=%s answer_revision=%d pending_offer_id=%s pending_revision=%d signaling_state=%s",
					currentParticipantName(), participantSessionID, reason, answerMetadata.OfferID, answerMetadata.Revision, pendingMetadata.OfferID, pendingMetadata.Revision, peerConnection.SignalingState())
				continue
			}

			log.Infof("room_signal_answer participant=%s session=%s answer_offer_id=%s answer_revision=%d pending_offer_id=%s pending_revision=%d signaling_state=%s sdp_bytes=%d",
				currentParticipantName(), participantSessionID, answerMetadata.OfferID, answerMetadata.Revision, pendingMetadata.OfferID, pendingMetadata.Revision, peerConnection.SignalingState(), len(answer.SDP))

			if err := peerConnection.SetRemoteDescription(answer); err != nil {
				// A failed answer must not kill the websocket session. The
				// common case is a duplicate: a slow-but-healthy client answers
				// both the original offer and the watchdog's resend, and the
				// second answer fails in stable state. Drop it and keep the
				// session alive; the negotiation watchdog recovers any peer
				// that is genuinely stuck.
				if peerConnection.SignalingState() == webrtc.SignalingStateStable {
					log.Infof("Dropping stray answer in stable signaling state (likely a duplicate after an offer resend): %v", err)
				} else {
					log.Errorf("Failed to set remote description: %v", err)
				}

				continue
			}
			completePeerConnectionICERestart(peerConnection)
			clearPendingOfferMetadata(peerConnection)
			matchingCandidates, discardedCandidates := pendingRemoteCandidates.takeMatching(peerConnection.RemoteDescription())
			if discardedCandidates > 0 {
				log.Infof("Discarded %d queued ICE candidates from stale explicit generations", discardedCandidates)
			}
			for _, candidate := range matchingCandidates {
				if err := peerConnection.AddICECandidate(candidate); err != nil {
					log.Errorf("Failed to add queued ICE candidate: %v", err)
				}
			}
			signalPeerConnections()
		case "restart_ice":
			if !participantAccepted || !mediaJoined || peerConnection == nil {
				continue
			}
			// card-003 W4: restart_ice is token-bucketed at the socket boundary;
			// peerICERestartState then coalesces browser and server observations of
			// the same outage into one ICE-restart offer. The bucket preserves the
			// client's legitimate retry ladder while capping a socket-line-rate
			// flood. On deny, drop silently toward the client with a rate-limited
			// drop log.
			iceRestartNow := time.Now()
			iceRestartAllowed := true
			if guest != nil {
				iceRestartAllowed = kanbanApp.allowGuestIceRestart(connRoomID, guest.SessionKey, iceRestartNow)
			} else {
				iceRestartAllowed = kanbanApp.allowMemberIceRestart(connRoomID, participantSessionID, iceRestartNow)
			}
			if !iceRestartAllowed {
				iceRestartDropsSuppressed++
				if iceRestartNow.Sub(iceRestartDropLogAt) >= memberMediaRepairLogInterval {
					log.Infof("restart_ice_rate_limited session=%s room=%s participant=%s dropped=%d",
						participantSessionID, connRoomID, currentParticipantName(), iceRestartDropsSuppressed)
					iceRestartDropLogAt = iceRestartNow
					iceRestartDropsSuppressed = 0
				}
				continue
			}
			totalTracks, audioTracks, videoTracks := snapshotForwardedTrackCounts()
			if signalPeerConnectionICE(peerConnection) {
				log.Infof("Client requested ICE restart for participant=%s session=%s total_tracks=%d audio_tracks=%d video_tracks=%d", currentParticipantName(), participantSessionID, totalTracks, audioTracks, videoTracks)
			} else {
				log.Infof("Client ICE restart coalesced for participant=%s session=%s", currentParticipantName(), participantSessionID)
			}
		case "select_layer":
			if !participantAccepted || !mediaJoined {
				continue
			}
			layerRequest := struct {
				Layer string `json:"layer"`
			}{}
			if err := json.Unmarshal([]byte(message.Data), &layerRequest); err != nil {
				log.Errorf("Failed to unmarshal layer selection: %v", err)
				continue
			}
			tier := normalizeLayerTier(layerRequest.Layer)
			// Only renegotiate when the preference actually changed; switching the
			// forwarded layer adds/removes senders, so we let signalPeerConnections
			// reconcile which layer this subscriber receives.
			if setSubscriberLayerTier(participantSessionID, tier) {
				log.Infof("Participant=%s session=%s selected simulcast layer tier=%s", currentParticipantName(), participantSessionID, tier)
				signalPeerConnections()
			}
		case "assistant_query":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "Enter the room before asking the assistant.")
				continue
			}
			query := struct {
				Query string `json:"query"`
			}{}
			if err := json.Unmarshal([]byte(message.Data), &query); err != nil {
				log.Errorf("Failed to unmarshal assistant query: %v", err)
				if writeErr := sendKanbanEvent(c, "assistant_event", map[string]any{
					"kind":      "error",
					"text":      "could not read assistant question",
					"createdAt": time.Now().UTC().Format(time.RFC3339Nano),
				}); writeErr != nil {
					log.Errorf("Failed to send assistant query error: %v", writeErr)
				}
				continue
			}
			assistantQuery := query.Query
			broadcastAssistantEvent("query", assistantQuery, nil)
			broadcastAssistantEvent("status", "Scout is checking the board and memory.", nil)
			go answerAssistantQueryForClient(c, assistantQuery)
		case "scout_chat_reset":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "enter the room before starting a Scout thread")
				continue
			}
			scoutChat.close()
			scoutChat = newScoutChatSession(c)
			_ = sendKanbanEvent(c, "scout_chat", map[string]any{
				"kind": "reset",
				"text": "new Scout thread started",
				"ts":   time.Now().UTC().Format(time.RFC3339Nano),
			})
		case "scout_chat":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "enter the room before chatting with Scout")
				continue
			}
			chat := struct {
				Text string `json:"text"`
			}{}
			if err := json.Unmarshal([]byte(message.Data), &chat); err != nil {
				log.Errorf("Failed to unmarshal scout chat payload: %v", err)
				_ = sendKanbanEvent(c, "scout_chat", map[string]any{
					"kind": "error",
					"text": "could not read chat message",
					"ts":   time.Now().UTC().Format(time.RFC3339Nano),
				})
				continue
			}
			// echo synchronously on this read loop (so the message visibly
			// lands in send order), then hand off to the session's FIFO
			// worker; model calls never block the websocket read path.
			scoutChat.submit(kanbanApp, chat.Text, currentParticipantName())
		case "room_chat":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "enter the room before sending room chat")
				continue
			}
			// §6.5: guest chat is token-bucketed per guest session (burst 5,
			// refill 1 per 3s) BEFORE anything persists.
			if guest != nil && !kanbanApp.allowGuestRoomChat(connRoomID, guest.SessionKey, time.Now()) {
				log.Infof("guest_chat_rate_limited session=%s room=%s", participantSessionID, connRoomID)
				continue
			}
			chat := struct {
				Text string `json:"text"`
			}{}
			if err := json.Unmarshal([]byte(message.Data), &chat); err != nil {
				log.Errorf("Failed to unmarshal room chat payload: %v", err)
				continue
			}
			// Identity comes from the participant session, never the payload;
			// persistence happens first so the echo-on-broadcast carries the
			// durable id and everyone (sender included) renders one ordering.
			// The authorEmail stamp is what later authorizes the sender — and
			// only the sender — to delete the message. Guests carry the
			// server-minted display name instead (they cannot delete).
			chatMetadata := map[string]string{
				"authorEmail": normalizeAccountEmail(sessionEmail),
			}
			if guest != nil {
				chatMetadata = map[string]string{
					"speaker": currentParticipantName(),
					"guest":   "true",
				}
			}
			payload, ok := kanbanApp.recordRoomChatMessageWithMetadata(connRoomID, currentParticipantName(), chat.Text, chatMetadata)
			if !ok {
				continue
			}
			payload["roomId"] = connRoomID
			broadcastRoomAudienceKanbanEvent(connRoomID, "room_chat", payload)
		case "room_chat_delete":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "enter the room before deleting room chat")
				continue
			}
			deletion := struct {
				ID string `json:"id"`
			}{}
			if err := json.Unmarshal([]byte(message.Data), &deletion); err != nil {
				log.Errorf("Failed to unmarshal room chat delete payload: %v", err)
				continue
			}
			// The payload only names the entry; whether the requester may
			// remove it is decided from the session identity server-side.
			payload, ok := kanbanApp.deleteRoomChatMessage(deletion.ID, sessionEmail, currentParticipantName())
			if !ok {
				continue
			}
			payload["roomId"] = connRoomID
			broadcastRoomAudienceKanbanEvent(connRoomID, "room_chat_delete", payload)
		case "manual_create_ticket":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "Enter the room before editing the board.")
				continue
			}
			args, err := manualBoardArgs(message)
			if err != nil {
				sendManualBoardError(c, err)
				continue
			}
			// human-created cards are never drafts (D4) — only the board
			// worker may set the draft flag
			delete(args, "draft")
			broadcastManualBoardMutation(c, currentParticipantName(), "created a card", func() (map[string]any, bool, error) {
				return kanbanApp.createTicket(args)
			})
		case "manual_update_ticket":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "Enter the room before editing the board.")
				continue
			}
			args, err := manualBoardArgs(message)
			if err != nil {
				sendManualBoardError(c, err)
				continue
			}
			broadcastManualBoardMutation(c, currentParticipantName(), "updated a card", func() (map[string]any, bool, error) {
				return kanbanApp.updateTicketDetails(args)
			})
		case "manual_delete_ticket":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "Enter the room before editing the board.")
				continue
			}
			args, err := manualBoardArgs(message)
			if err != nil {
				sendManualBoardError(c, err)
				continue
			}
			broadcastManualBoardMutation(c, currentParticipantName(), "deleted a card", func() (map[string]any, bool, error) {
				return kanbanApp.deleteTicket(args)
			})
		case "undo_delete_ticket":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "Enter the room before editing the board.")
				continue
			}
			broadcastManualBoardMutation(c, currentParticipantName(), "restored the last deleted card", func() (map[string]any, bool, error) {
				return kanbanApp.restoreLastDeletedTicket()
			})
		case "archive_meeting":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "Enter the room before archiving the meeting.")
				continue
			}
			if connRoomID != officeRoomID {
				// Named rooms archive automatically at idle-end; the manual
				// archive verb stays office-scoped until the W4 close chain.
				_ = sendKanbanEvent(c, "assistant_event", map[string]any{
					"kind":      "error",
					"text":      "manual archiving is available in the office; this room archives itself when it empties",
					"createdAt": time.Now().UTC().Format(time.RFC3339Nano),
				})
				continue
			}
			result, err := kanbanApp.archiveMeeting(currentParticipantName())
			if err != nil {
				log.Errorf("Failed to archive meeting: %v", err)
				_ = sendKanbanEvent(c, "assistant_event", map[string]any{
					"kind":      "error",
					"text":      "could not archive the meeting: " + err.Error(),
					"createdAt": time.Now().UTC().Format(time.RFC3339Nano),
				})
				continue
			}
			broadcastSignedInKanbanEvent("meeting_archived", result)
			broadcastSignedInKanbanEvent("memory", kanbanApp.memorySnapshotForClients(20))
		case "set_recording":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "Enter the room before changing recording.")
				continue
			}
			payload := struct {
				Enabled bool `json:"enabled"`
			}{}
			if err := json.Unmarshal([]byte(message.Data), &payload); err != nil {
				log.Errorf("Failed to unmarshal recording state: %v", err)
				continue
			}
			snapshot := kanbanApp.setTranscriptRecordingInRoom(connRoomID, payload.Enabled, currentParticipantName())
			recording, _ := snapshot["recording"].(roomRecordingState)
			broadcastRoomKanbanEvent(connRoomID, "participants", snapshot)
			broadcastAssistantEvent("answer", roomRecordingAnnouncementText(recording), map[string]any{
				"tool":       "set_recording",
				"recording":  recording,
				"voiceState": "talking",
			})
		case "participant_media_state":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "Enter the room before publishing media state.")
				continue
			}
			// §6.5 hardening: each accepted update fans a room-wide participants
			// broadcast (amplification × room size); token-bucket guests, members
			// unbucketed.
			if guest != nil && !kanbanApp.allowGuestMediaStateEvent(connRoomID, guest.SessionKey, time.Now()) {
				log.Infof("guest_media_state_rate_limited session=%s room=%s", participantSessionID, connRoomID)
				continue
			}
			payload := struct {
				MicMuted      bool `json:"micMuted"`
				CameraOff     bool `json:"cameraOff"`
				ScreenSharing bool `json:"screenSharing"`
			}{}
			if err := json.Unmarshal([]byte(message.Data), &payload); err != nil {
				log.Errorf("Failed to unmarshal participant media state: %v", err)
				continue
			}
			snapshot, err := kanbanApp.setParticipantMediaStateInRoom(connRoomID, currentParticipantName(), participantMediaState{
				MicMuted:      payload.MicMuted,
				CameraOff:     payload.CameraOff,
				ScreenSharing: payload.ScreenSharing,
			})
			if err != nil {
				log.Errorf("Failed to update participant media state: %v", err)
				continue
			}
			broadcastRoomKanbanEvent(connRoomID, "participants", snapshot)
		case "voice_control":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "Enter the room before changing voice control.")
				continue
			}
			payload := struct {
				Enabled bool `json:"enabled"`
			}{}
			if err := json.Unmarshal([]byte(message.Data), &payload); err != nil {
				log.Errorf("Failed to unmarshal voice control state: %v", err)
				continue
			}
			kanbanApp.setVoiceControlActive(payload.Enabled, currentParticipantName())
		case "media_quality":
			if !participantAccepted {
				continue
			}
			// §6.5 hardening: unbounded log write; token-bucket guests (members
			// unbucketed). The legit client emits every ~4-12s.
			if guest != nil && !kanbanApp.allowGuestTelemetryEvent(connRoomID, guest.SessionKey, time.Now()) {
				log.Infof("guest_media_telemetry_rate_limited event=media_quality session=%s room=%s", participantSessionID, connRoomID)
				continue
			}
			logClientMediaQualityReport(message.Data, currentParticipantName(), participantSessionID)
		case "media_error":
			if !participantAccepted {
				continue
			}
			if guest != nil && !kanbanApp.allowGuestTelemetryEvent(connRoomID, guest.SessionKey, time.Now()) {
				log.Infof("guest_media_telemetry_rate_limited event=media_error session=%s room=%s", participantSessionID, connRoomID)
				continue
			}
			logClientMediaErrorReport(message.Data, currentParticipantName(), participantSessionID)
		case "screen_share_started":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "Enter the room before sharing your screen.")
				continue
			}
			broadcastRoomKanbanEvent(connRoomID, "participants", kanbanApp.setParticipantScreenSharingInRoom(connRoomID, currentParticipantName(), true))
			broadcastRoomKanbanEvent(connRoomID, "screen_share_started", map[string]any{
				"name":   currentParticipantName(),
				"roomId": connRoomID,
			})
			go requestParticipantVideoKeyframes(currentParticipantName(), participantSessionID, "screen_share_started")
			broadcastAssistantEvent("status", currentParticipantName()+" started sharing their screen", nil)
		case "screen_share_stopped":
			if !participantAccepted {
				continue
			}
			broadcastRoomKanbanEvent(connRoomID, "participants", kanbanApp.setParticipantScreenSharingInRoom(connRoomID, currentParticipantName(), false))
			broadcastRoomKanbanEvent(connRoomID, "screen_share_stopped", map[string]any{
				"name":   currentParticipantName(),
				"roomId": connRoomID,
			})
			go requestParticipantVideoKeyframes(currentParticipantName(), participantSessionID, "screen_share_stopped")
			broadcastAssistantEvent("status", currentParticipantName()+" stopped sharing their screen", nil)
		default:
			log.Errorf("unknown message: %+v", message)
		}
	}
}

// websocketPeerAllocations counts room PeerConnection allocations inside
// websocketHandler. Tests pin the §6.1 guarantee on it: an unadmitted guest
// socket never allocates a PeerConnection.
var websocketPeerAllocations atomic.Int64

func manualBoardArgs(message *websocketMessage) (map[string]any, error) {
	args := map[string]any{}
	if strings.TrimSpace(message.Data) == "" {
		return args, nil
	}
	if err := json.Unmarshal([]byte(message.Data), &args); err != nil {
		return nil, fmt.Errorf("could not read board edit: %w", err)
	}

	return args, nil
}

func sendManualBoardError(c *threadSafeWriter, err error) {
	if writeErr := sendKanbanEvent(c, "assistant_event", map[string]any{
		"kind":      "error",
		"text":      err.Error(),
		"createdAt": time.Now().UTC().Format(time.RFC3339Nano),
	}); writeErr != nil {
		log.Errorf("Failed to send manual board error: %v", writeErr)
	}
}

func answerAssistantQueryForClient(c *threadSafeWriter, query string) {
	if _, _, err := kanbanApp.answerAssistantQuery(query); err != nil {
		log.Errorf("Failed to answer assistant query: %v", err)
		if writeErr := sendKanbanEvent(c, "assistant_event", map[string]any{
			"kind":      "error",
			"text":      err.Error(),
			"createdAt": time.Now().UTC().Format(time.RFC3339Nano),
		}); writeErr != nil {
			log.Errorf("Failed to send assistant query error: %v", writeErr)
		}
	}
}

func broadcastManualBoardMutation(c *threadSafeWriter, actor string, action string, apply func() (map[string]any, bool, error)) {
	_, changed, err := apply()
	if err != nil {
		sendManualBoardError(c, err)
		return
	}
	if !changed {
		return
	}

	broadcastSignedInKanbanEvent("board", kanbanApp.snapshotState())
	broadcastSignedInKanbanEvent("undo_available", kanbanApp.canUndoDelete())
	broadcastAssistantEvent("action", fmt.Sprintf("%s %s", actor, action), nil)
	kanbanApp.refreshRealtimeBoardContext(action)
}

// Helper to make Gorilla Websockets threadsafe.
type threadSafeWriter struct {
	*websocket.Conn
	sync.Mutex
	// guest marks the writer's principal class (multi-room §6.2): set once at
	// connection setup, read by the write-time event allowlist so a guest
	// socket can only ever receive allowlisted events — whichever broadcast
	// pool mis-routes to it.
	guest bool
}

func (t *threadSafeWriter) Close() error {
	if t == nil || t.Conn == nil {
		return nil
	}

	t.Lock()
	defer t.Unlock()

	return t.Conn.Close()
}

// websocketWriteTimeout bounds every signaling/board write. Signaling passes
// (including the negotiation watchdog's offer resend, which targets exactly
// the stalled-socket population) write under listLock; without a deadline one
// wedged client's full send buffer could block that write for minutes and
// freeze signaling for every participant.
const (
	websocketWriteTimeout = 5 * time.Second
	websocketReadTimeout  = 75 * time.Second
	websocketPingInterval = 25 * time.Second
)

const (
	// participantLivenessTimeout reaps a room participant whose socket has been
	// silent (no pong, no inbound room frame) for longer than the read deadline
	// plus a ping cycle. The per-socket read-deadline defer is the first line of
	// defense; this is the backstop that guarantees activeParticipantCount()
	// still reaches zero — so the empty-room idle end can arm and the next
	// sitting mints a fresh meeting id — even when a read loop is wedged, its
	// deadline setup failed, or its onclose defer never ran. It is strictly
	// longer than websocketReadTimeout so the normal clean-close path always
	// gets first crack, and comfortably longer than one ping round so a present
	// participant (whose socket keeps ponging) is never reaped between pings.
	participantLivenessTimeout = websocketReadTimeout + websocketPingInterval
	// participantLivenessSweepInterval is how often the backstop sweep runs.
	participantLivenessSweepInterval = websocketPingInterval
)

func roomWebsocketHeartbeatTimingsValid() bool {
	return websocketWriteTimeout > 0 &&
		websocketPingInterval > websocketWriteTimeout &&
		websocketReadTimeout > websocketPingInterval+websocketWriteTimeout
}

func isWebsocketReadTimeout(err error) bool {
	if err == nil {
		return false
	}
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}

	return false
}

func startRoomWebsocketHeartbeat(c *threadSafeWriter, participantName func() string, sessionID string) func() {
	if c == nil || c.Conn == nil {
		return func() {}
	}
	if participantName == nil {
		participantName = func() string { return "" }
	}
	if err := c.Conn.SetReadDeadline(time.Now().Add(websocketReadTimeout)); err != nil {
		log.Errorf("room_ws_heartbeat_set_deadline_failed session=%s error=%v", sessionID, err)
	}
	c.Conn.SetPongHandler(func(string) error {
		// A network-layer pong keeps the READ DEADLINE alive (the connection is
		// technically up) but deliberately does NOT refresh the liveness stamp.
		// A backgrounded/frozen browser tab keeps auto-ponging at the network
		// layer even while its JS is throttled, so a pong is NOT proof a
		// participant is really present — that was the ~22h zombie sitting. The
		// liveness stamp is refreshed only by APP-level frames (the client
		// room_ping heartbeat + real room activity, stamped per-message in the
		// read loop), so a truly-gone tab goes stale and the idle-end sweep can
		// finalize the empty sitting.
		if err := c.Conn.SetReadDeadline(time.Now().Add(websocketReadTimeout)); err != nil {
			log.Errorf("room_ws_pong_deadline_failed participant=%s session=%s error=%v", participantName(), sessionID, err)
			return err
		}
		return nil
	})

	done := make(chan struct{})
	var stopOnce sync.Once
	go func() {
		ticker := time.NewTicker(websocketPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := c.WriteControl(websocket.PingMessage, []byte("room"), time.Now().Add(websocketWriteTimeout)); err != nil {
					log.Infof("room_ws_heartbeat_failed participant=%s session=%s error=%v", participantName(), sessionID, err)
					_ = c.Close()
					return
				}
			case <-done:
				return
			}
		}
	}()

	return func() {
		stopOnce.Do(func() {
			close(done)
		})
	}
}

func (t *threadSafeWriter) WriteJSON(v any) error {
	// §6.2: a guest writer accepts only the kanban envelope (inner event gated
	// in sendKanbanEvent/deliverKanbanEvent) and raw signaling frames. The
	// drop happens before any connection state is consulted.
	if t != nil && t.guest {
		if message, ok := v.(*websocketMessage); ok && !guestTopLevelEvents[message.Event] {
			guestEventsDropped.Add(1)
			log.Infof("guest_event_dropped event=%s total=%d", message.Event, guestEventsDropped.Load())
			return nil
		}
	}
	if t == nil || t.Conn == nil {
		return fmt.Errorf("websocket is closed")
	}

	t.Lock()
	defer t.Unlock()

	_ = t.Conn.SetWriteDeadline(time.Now().Add(websocketWriteTimeout))
	err := t.Conn.WriteJSON(v)
	_ = t.Conn.SetWriteDeadline(time.Time{})

	return err
}

func (t *threadSafeWriter) WriteControl(messageType int, data []byte, deadline time.Time) error {
	if t == nil || t.Conn == nil {
		return fmt.Errorf("websocket is closed")
	}

	t.Lock()
	defer t.Unlock()

	return t.Conn.WriteControl(messageType, data, deadline)
}
