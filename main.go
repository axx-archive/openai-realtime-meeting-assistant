package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
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
	"github.com/pion/webrtc/v4"
)

// nolint
var (
	addr              = flag.String("addr", ":3000", "http service address")
	codexRunnerWorker = flag.Bool("codex-runner", false, "run the Codex sidecar queue worker")
	upgrader          = websocket.Upgrader{
		CheckOrigin: websocketOriginAllowed,
	}

	// maxWebsocketMessageBytes caps a single inbound Websocket frame. Signaling
	// payloads (SDP, ICE candidates, board events) are well under this; the cap
	// blocks memory-amplification from oversized frames.
	maxWebsocketMessageBytes int64 = 256 << 10 // 256 KiB
	indexHTML                []byte

	// lock for peerConnections and trackLocals
	listLock                     sync.RWMutex
	peerConnections              []peerConnectionState
	activeParticipantConnections map[string]peerConnectionState
	trackLocals                  map[string]*webrtc.TrackLocalStaticRTP
	trackParticipants            map[string]string
	trackParticipantSessions     map[string]string
	trackSourceIDs               map[string]string
	trackLayerRIDs               map[string]string // forwarded track ID -> simulcast RID ("" when not simulcast)
	trackLayerGroups             map[string]string // forwarded track ID -> source group key (shared by sibling layers)
	subscriberLayerTiers         map[string]string // subscriber session ID -> requested layer tier
	signalRequestLock            sync.Mutex
	signalRequestTimer           *time.Timer
	participantSessionSeq        atomic.Uint64

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

// iceStateNeedsRecovery reports whether an ICE connection state indicates a
// recoverable connectivity loss that a server-side ICE restart should address.
// Only "disconnected" qualifies: it is the transient, network-change signal an
// ICE restart is designed to repair. "failed" is intentionally excluded because
// the PeerConnection-level failure handler tears that path down and ejects the
// participant instead of trying to revive it.
func iceStateNeedsRecovery(state webrtc.ICEConnectionState) bool {
	return state == webrtc.ICEConnectionStateDisconnected
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
	acceptTrack     func(*webrtc.TrackLocalStaticRTP) bool
	shouldSignal    func(desiredTrackCount int) bool
	signal          func(gatherComplete <-chan struct{}) error

	// Negotiation watchdog bookkeeping, mutated only under listLock by
	// signalPeerConnectionsWithRestart.
	nonStableSince time.Time
	offerResent    bool

	// Optional signaling correlation for newer clients. Legacy clients ignore
	// these fields and can still answer with plain SDP in websocketMessage.Data.
	signalingRevision    uint64
	pendingOfferID       string
	pendingOfferRevision uint64
}

type participantTrackSnapshot struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	TrackID       string `json:"trackId"`
	SourceTrackID string `json:"sourceTrackId,omitempty"`
	StreamID      string `json:"streamId,omitempty"`
}

func (p peerConnectionState) acceptsTrack(track *webrtc.TrackLocalStaticRTP) bool {
	if track != nil && sameParticipantName(trackParticipants[track.ID()], p.participantName) {
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

	if *codexRunnerWorker {
		if err := runCodexRunnerLoop(context.Background()); err != nil {
			log.Errorf("Codex runner stopped: %v", err)
		}
		return
	}

	// Init other state
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{}
	trackParticipants = map[string]string{}
	trackParticipantSessions = map[string]string{}
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

	// Read index.html from disk into memory, serve whenever anyone requests /
	var err error
	indexHTML, err = os.ReadFile("index.html")
	if err != nil {
		panic(err)
	}

	// websocket handler
	http.HandleFunc("/healthz", healthHandler)
	http.HandleFunc("/readyz", readinessHandler)
	http.HandleFunc("/websocket", websocketHandler)
	http.HandleFunc("/auth/", authHandler)
	http.HandleFunc("/assistant/query", assistantQueryHandler)
	http.HandleFunc("/assistant/chat-threads", assistantChatThreadsHandler)
	http.HandleFunc("/assistant/chat-threads/", assistantChatThreadHandler)
	http.HandleFunc("/assistant/threads", assistantThreadsHandler)
	http.HandleFunc("/assistant/realtime-offer", assistantRealtimeOfferHandler)
	http.HandleFunc("/assistant/realtime-tool", assistantRealtimeToolHandler)
	http.HandleFunc("/internal/codex/jobs/result", internalCodexRunnerResultHandler)
	http.HandleFunc("/artifacts", artifactsHandler)
	http.HandleFunc("/artifacts/action", artifactRunnerActionHandler)
	http.HandleFunc("/archives/", meetingArchiveHandler)
	http.HandleFunc("/participants", participantsHandler)
	http.HandleFunc("/client-config", clientConfigHandler)
	http.HandleFunc("/native/config", nativeClientConfigHandler)
	http.HandleFunc("/ice-test", iceTestHandler)
	http.HandleFunc("/public/", publicAssetHandler)

	// index.html handler
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, writeErr := w.Write(indexHTML); writeErr != nil {
			log.Errorf("Failed to serve index page: %v", writeErr)
		}
	})

	// request a keyframe every 3 seconds
	go func() {
		for range time.NewTicker(time.Second * 3).C {
			dispatchKeyFrame()
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
	broadcastKanbanEvent("server_shutdown", map[string]any{
		"message":      "Server restarting",
		"retryAfterMs": retryAfterMs,
	})
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	writeSystemStatusJSON(w, r, http.StatusOK, map[string]any{
		"ok":      true,
		"service": "meetingassist",
		"time":    time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func readinessHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	appAvailable := kanbanApp != nil
	memoryAvailable := appAvailable && kanbanApp.memory != nil
	memoryCheck := readinessStateFileCheck(meetingMemoryPath())
	boardCheck := readinessStateFileCheck(kanbanBoardPath())
	realtime := map[string]any{"connected": false, "voiceControl": false}
	if appAvailable {
		kanbanApp.mu.Lock()
		realtime["connected"] = kanbanApp.connected
		realtime["voiceControl"] = kanbanApp.voiceControlActive
		kanbanApp.mu.Unlock()
	}

	ready := appAvailable && memoryAvailable && readinessCheckOK(memoryCheck) && readinessCheckOK(boardCheck)
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}

	degraded := []string{}
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		degraded = append(degraded, "openai_api_key_missing")
	}

	writeSystemStatusJSON(w, r, status, map[string]any{
		"ok":       ready,
		"service":  "meetingassist",
		"time":     time.Now().UTC().Format(time.RFC3339Nano),
		"degraded": degraded,
		"checks": map[string]any{
			"app":         appAvailable,
			"memoryStore": memoryAvailable,
			"memoryFile":  memoryCheck,
			"boardFile":   boardCheck,
			"realtime":    realtime,
			"agents": map[string]any{
				"brain":       readinessAgentSnapshot(meetingBrainAgent()),
				"board":       readinessAgentSnapshot(meetingBoardAgent()),
				"codexRunner": readinessCodexRunnerSnapshot(),
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
	if userFromRequest(r) == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "room state is unavailable")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(kanbanApp.roomSnapshot()); err != nil {
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

	thread, err := kanbanApp.launchAgentThread(payload.Mode, payload.Query, user.Name)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	viewerThread := agentThreadForViewer(thread, canAccessArtifactLibrary(user))

	writeAuthJSON(w, http.StatusAccepted, map[string]any{
		"ok":       true,
		"thread":   viewerThread,
		"artifact": viewerThread.Artifact,
		"actions":  viewerThread.Actions,
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

	payload := struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read realtime tool request")
		return
	}

	result, changed, err := kanbanApp.applyPrivateRealtimeVoiceTool(payload.Name, payload.Arguments)
	ok := err == nil
	if err != nil {
		result = map[string]any{
			"ok":    false,
			"error": err.Error(),
		}
		log.Errorf("Private Realtime tool %q failed for %s: %v", payload.Name, user.Email, err)
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

func canAccessArtifactLibrary(user *userAccount) bool {
	return user != nil && normalizeAccountEmail(user.Email) == artifactLibraryAdminEmail
}

func artifactForViewer(entry meetingMemoryEntry, canViewArtifact bool) meetingMemoryEntry {
	if canViewArtifact {
		return entry
	}
	if entry.Metadata != nil {
		metadata := make(map[string]string, len(entry.Metadata)+1)
		for key, value := range entry.Metadata {
			metadata[key] = value
		}
		metadata["restricted"] = "true"
		entry.Metadata = metadata
	} else {
		entry.Metadata = map[string]string{"restricted": "true"}
	}
	entry.Text = ""
	return entry
}

func agentThreadForViewer(thread scoutAgentThread, canViewArtifact bool) scoutAgentThread {
	thread.Artifact = artifactForViewer(thread.Artifact, canViewArtifact)
	return thread
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
	if !canAccessArtifactLibrary(user) {
		writeAuthError(w, http.StatusForbidden, "artifacts are admin-only")
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
		artifact, updated, err := kanbanApp.updateOSArtifactWithMetadata(payload.ID, payload.Title, payload.Text, user.Name, metadata)
		if err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			}
			writeAuthError(w, status, err.Error())
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"updated":  updated,
			"artifact": artifact,
		})
		return
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":                 true,
		"artifacts":          kanbanApp.osArtifactsSnapshot(100),
		"publishedArtifacts": kanbanApp.publishedOSArtifactsSnapshot(10),
	})
}

func publicAssetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.StripPrefix("/public/", http.FileServer(http.Dir("public"))).ServeHTTP(w, r)
}

func newPeerConnection() (*webrtc.PeerConnection, error) {
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
	).NewPeerConnection(webrtc.Configuration{})
}

func stableRoomMediaEngine() (*webrtc.MediaEngine, *interceptor.Registry, error) {
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   48000,
			Channels:    2,
			SDPFmtpLine: "minptime=10;useinbandfec=1",
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, nil, fmt.Errorf("register opus codec: %w", err)
	}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeH264,
			ClockRate:    90000,
			SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
			RTCPFeedback: []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}, {Type: "goog-remb"}},
		},
		PayloadType: 102,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, nil, fmt.Errorf("register h264 codec: %w", err)
	}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeVP8,
			ClockRate:    90000,
			RTCPFeedback: []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}, {Type: "goog-remb"}},
		},
		PayloadType: 96,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, nil, fmt.Errorf("register vp8 codec: %w", err)
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
func addTrack(t *webrtc.TrackRemote, participantName string, sessionID string) (*webrtc.TrackLocalStaticRTP, error) { // nolint
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
	trackParticipants[trackLocal.ID()] = canonicalParticipantName(participantName)
	trackParticipantSessions[trackLocal.ID()] = sessionID
	trackSourceIDs[trackLocal.ID()] = t.ID()
	trackLayerRIDs[trackLocal.ID()] = t.RID()
	trackLayerGroups[trackLocal.ID()] = groupKey
	totalTracks, audioTracks, videoTracks := forwardedTrackCountsLocked()
	listLock.Unlock()

	codec := t.Codec()
	log.Infof("room_track_added participant=%s session=%s kind=%s track_id=%s source_track_id=%s stream_id=%s rid=%q ssrc=%d payload_type=%d codec=%s clock_rate=%d channels=%d fmtp=%q feedback=%s total_tracks=%d audio_tracks=%d video_tracks=%d",
		canonicalParticipantName(participantName), sessionID, t.Kind(), trackLocal.ID(), t.ID(), t.StreamID(), t.RID(), t.SSRC(), t.PayloadType(), codec.MimeType, codec.ClockRate, codec.Channels, codec.SDPFmtpLine, rtcpFeedbackSummary(codec.RTCPFeedback), totalTracks, audioTracks, videoTracks)

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
		delete(trackSourceIDs, staleID)
		delete(trackLayerRIDs, staleID)
		delete(trackLayerGroups, staleID)
	}
}

func participantTrackPayload(name string, t *webrtc.TrackRemote) map[string]any {
	return map[string]any{
		"name":          canonicalParticipantName(name),
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
	a = canonicalParticipantName(a)
	b = canonicalParticipantName(b)
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
	delete(trackLocals, t.ID())
	delete(trackParticipants, t.ID())
	delete(trackParticipantSessions, t.ID())
	delete(trackSourceIDs, t.ID())
	delete(trackLayerRIDs, t.ID())
	delete(trackLayerGroups, t.ID())
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
		delete(trackSourceIDs, trackID)
		delete(trackLayerRIDs, trackID)
		delete(trackLayerGroups, trackID)
		removedTracks = true
	}

	return removedTracks
}

func participantTrackSnapshots(excludeParticipant string) []participantTrackSnapshot {
	listLock.RLock()
	defer listLock.RUnlock()

	return participantTrackSnapshotsLocked(excludeParticipant)
}

func participantTrackSnapshotsLocked(excludeParticipant string) []participantTrackSnapshot {
	snapshots := make([]participantTrackSnapshot, 0, len(trackLocals))
	for trackID, trackLocal := range trackLocals {
		if trackLocal == nil {
			continue
		}
		name := canonicalParticipantName(trackParticipants[trackID])
		if sameParticipantName(name, excludeParticipant) {
			continue
		}
		snapshots = append(snapshots, participantTrackSnapshot{
			Name:          name,
			Kind:          trackLocal.Kind().String(),
			TrackID:       trackID,
			SourceTrackID: trackSourceIDs[trackID],
			StreamID:      trackLocal.StreamID(),
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

func sendParticipantTrackSnapshots(websocket *threadSafeWriter, excludeParticipant string) {
	for _, snapshot := range participantTrackSnapshots(excludeParticipant) {
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
		"protocolVersion":  nativeClientProtocolV1,
		"auth":             "cookie",
		"websocketPath":    "/websocket",
		"signalingRole":    "server-offer",
		"supportedLayers":  []string{string(layerTierLow), string(layerTierMedium), string(layerTierHigh)},
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

	ttlSeconds := int64(12 * 60 * 60)
	if rawTTL := strings.TrimSpace(os.Getenv("MEETING_TURN_TTL_SECONDS")); rawTTL != "" {
		if parsedTTL, err := strconv.ParseInt(rawTTL, 10, 64); err == nil && parsedTTL >= 300 && parsedTTL <= 7*24*60*60 {
			ttlSeconds = parsedTTL
		}
	}
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
		"Client media quality participant=%q session=%s platform=%s clientVersion=%s safari=%v laggy=%v viewport=%dx%d visual=%dx%d orientation=%s/%d mobile=%v stage=%s boardExpanded=%v screenShare=%s attachmentRevision=%d auxTargets=%d constrained=%v audioMode=%s audioProfile=%s voiceFocus=%v processor=%s workletHealth=%s rnnoiseReady=%v sampleRate=%d frameSize=%d vfGain=%.3f vfSuppressionDb=%.1f vfBias=%.4f vfSpeech=%.2f localAudio=%s/%v localVideo=%s/%v outAudioKbps=%.0f outVideoKbps=%.0f outAudioPackets=%d outVideoFrames=%d rttMs=%.0f inboundVideoJitterMs=%.0f inboundAudioJitterMs=%.0f inboundVideoLossPct=%.1f inboundAudioLossPct=%.1f localCandidate=%s remoteCandidate=%s protocol=%s network=%s remoteVideo=%d remoteAudio=%d remoteAudioLevel=%.5f remoteAudible=%d playbackElement=%d playbackWebAudio=%d playbackNone=%d audioCtx=%s missingVideo=%d missingAudio=%d duplicateVideo=%d duplicateAudio=%d placeholderVideo=%d placeholderAudio=%d stalledVideo=%d pendingAudio=%d\n",
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

func stringFromPayload(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
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

func replaceExistingParticipantSession(name string, sessionID string, currentPeerConnection *webrtc.PeerConnection, currentWebsocket *threadSafeWriter) {
	name = canonicalParticipantName(name)
	if name == "" {
		return
	}

	var staleConnections []peerConnectionState
	removedTracks := false

	listLock.Lock()
	if activeParticipantConnections == nil {
		activeParticipantConnections = map[string]peerConnectionState{}
	}
	if existing, ok := activeParticipantConnections[name]; ok && existing.sessionID != sessionID {
		staleConnections = append(staleConnections, existing)
	}
	activeParticipantConnections[name] = peerConnectionState{
		peerConnection:  currentPeerConnection,
		websocket:       currentWebsocket,
		participantName: name,
		sessionID:       sessionID,
	}

	retainedConnections := peerConnections[:0]
	for _, state := range peerConnections {
		isCurrentConnection := currentPeerConnection != nil && state.peerConnection == currentPeerConnection
		if isCurrentConnection || !sameParticipantName(state.participantName, name) || state.sessionID == sessionID {
			retainedConnections = append(retainedConnections, state)
			continue
		}
		staleConnections = append(staleConnections, state)
	}
	peerConnections = retainedConnections

	removedTracks = removeParticipantTracksLocked(name, "")
	listLock.Unlock()

	closeParticipantConnections(staleConnections)

	if len(staleConnections) > 0 || removedTracks {
		signalPeerConnections()
	}
}

func unregisterParticipantSession(name string, sessionID string) {
	name = canonicalParticipantName(name)
	if name == "" {
		return
	}

	removedConnection := false
	removedTracks := false

	listLock.Lock()
	if activeParticipantConnections != nil {
		if current, ok := activeParticipantConnections[name]; ok && current.sessionID == sessionID {
			delete(activeParticipantConnections, name)
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

func closeParticipantConnections(states []peerConnectionState) {
	closedPeerConnections := map[*webrtc.PeerConnection]struct{}{}
	closedWebsockets := map[*threadSafeWriter]struct{}{}
	for _, state := range states {
		if state.peerConnection != nil {
			if _, ok := closedPeerConnections[state.peerConnection]; !ok {
				closedPeerConnections[state.peerConnection] = struct{}{}
				_ = state.peerConnection.Close()
			}
		}
		if state.websocket != nil {
			if _, ok := closedWebsockets[state.websocket]; !ok {
				closedWebsockets[state.websocket] = struct{}{}
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
		signalPeerConnectionsWithRestart(nil)
	})
}

func signalPeerConnectionICE(peerConnection *webrtc.PeerConnection) {
	if peerConnection == nil {
		return
	}

	signalPeerConnectionsWithRestart(peerConnection)
}

func schedulePeerConnectionSignal(restartPeer *webrtc.PeerConnection) {
	go func() {
		time.Sleep(750 * time.Millisecond)
		signalPeerConnectionsWithRestart(restartPeer)
	}()
}

func signalPeerConnectionsWithRestart(restartPeer *webrtc.PeerConnection) { // nolint
	listLock.Lock()
	retryLater := false
	defer func() {
		listLock.Unlock()
		dispatchKeyFrame()
		if retryLater {
			schedulePeerConnectionSignal(restartPeer)
		}
	}()

	attemptSync := func() (tryAgain bool) {
		for i := range peerConnections {
			if peerConnections[i].peerConnection == nil || peerConnections[i].peerConnection.ConnectionState() == webrtc.PeerConnectionStateClosed {
				peerConnections = append(peerConnections[:i], peerConnections[i+1:]...)

				return true // We modified the slice, start from the beginning
			}

			peer := &peerConnections[i]
			forceSignal := restartPeer != nil && peer.peerConnection == restartPeer

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
						_ = stuckPeerConnection.Close()
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
				if sender.Track() == nil {
					continue
				}

				trackID := sender.Track().ID()
				existingSenders[trackID] = true

				// If we have an RTPSender that does not map to an existing track, remove and signal.
				trackLocal, ok := trackLocals[trackID]
				if !ok || !peer.acceptsTrack(trackLocal) {
					if err := peer.peerConnection.RemoveTrack(sender); err != nil {
						log.Errorf("Failed to remove stale sender track=%s: %v", trackID, err)
						return true
					}
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
			offerMetadata := startPendingOfferMetadata(peer)

			if peer.websocket != nil {
				for _, snapshot := range participantTrackSnapshotsLocked(peer.participantName) {
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
			if forceSignal {
				restartPeer = nil
			}
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

func preferSourceTrackCodec(transceiver *webrtc.RTPTransceiver, trackLocal *webrtc.TrackLocalStaticRTP) error {
	if transceiver == nil || trackLocal == nil {
		return nil
	}
	codec := trackLocal.Codec()
	if strings.TrimSpace(codec.MimeType) == "" {
		return nil
	}

	return transceiver.SetCodecPreferences([]webrtc.RTPCodecParameters{{
		RTPCodecCapability: codec,
	}})
}

// dispatchKeyFrame sends a keyframe to all PeerConnections, used everytime a new user joins the call.
func dispatchKeyFrame() {
	listLock.RLock()
	defer listLock.RUnlock()

	for i := range peerConnections {
		if peerConnections[i].peerConnection == nil {
			continue
		}
		for _, receiver := range peerConnections[i].peerConnection.GetReceivers() {
			if receiver.Track() == nil {
				continue
			}

			_ = peerConnections[i].peerConnection.WriteRTCP([]rtcp.Packet{
				&rtcp.PictureLossIndication{
					MediaSSRC: uint32(receiver.Track().SSRC()),
				},
			})
		}
	}
}

// Handle incoming websockets.
func websocketHandler(w http.ResponseWriter, r *http.Request) { // nolint
	// Accounts gate the room: resolve the session cookie before upgrading so
	// an unauthenticated socket never allocates a PeerConnection or chat
	// session.
	sessionUser := userFromRequest(r)
	if sessionUser == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Upgrade HTTP request to Websocket
	unsafeConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Errorf("Failed to upgrade HTTP to Websocket: ", err)

		return
	}

	// Bound per-message memory: signaling frames (SDP/ICE/board events) are small;
	// reject oversized frames so an unauthenticated client cannot stream an
	// arbitrarily large frame and force the server to buffer it before parsing.
	unsafeConn.SetReadLimit(maxWebsocketMessageBytes)

	c := &threadSafeWriter{unsafeConn, sync.Mutex{}} // nolint
	scoutChat := newScoutChatSession(c, canAccessArtifactLibrary(sessionUser))
	// Stop the chat worker and cancel any queued/in-flight model calls as
	// soon as this connection ends.
	defer func() { scoutChat.close() }()
	participantName := "participant"
	participantSessionID := nextParticipantSessionID()
	participantAccepted := false
	mediaJoined := false
	var participantAcceptedState atomic.Bool
	var mediaJoinedState atomic.Bool
	var cleanupOnce sync.Once
	pendingRemoteCandidates := make([]webrtc.ICECandidateInit, 0)
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
				if kanbanApp.forgetParticipantSession(name, participantSessionID) {
					broadcastKanbanEvent("participant_left", map[string]any{
						"name": name,
					})
					broadcastKanbanEvent("participants", kanbanApp.roomSnapshot())
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

	// Create new PeerConnection
	peerConnection, err := newPeerConnection()
	if err != nil {
		log.Errorf("Failed to creates a PeerConnection: %v", err)

		return
	}

	// When this frame returns close the PeerConnection
	defer peerConnection.Close() //nolint

	// Accept one audio and one video track incoming
	for _, typ := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio} {
		if _, err := peerConnection.AddTransceiverFromKind(typ, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
			log.Errorf("Failed to add transceiver: %v", err)

			return
		}
	}

	// Trickle ICE. Emit server candidate to client
	peerConnection.OnICECandidate(func(i *webrtc.ICECandidate) {
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
	peerConnection.OnConnectionStateChange(func(p webrtc.PeerConnectionState) {
		log.Infof("Connection state change: %s", p)

		switch p {
		case webrtc.PeerConnectionStateFailed:
			if mediaJoinedState.Load() {
				cleanupParticipantSession("media connection failed; rejoin the room.", true)
			}
			// Drop the dead peer from the fan-out pool before closing so it stops
			// receiving forwarded media immediately, then release its transports.
			prunePeerConnectionPool(peerConnection)
			if err := peerConnection.Close(); err != nil {
				log.Errorf("Failed to close PeerConnection: %v", err)
			}
		case webrtc.PeerConnectionStateClosed:
			prunePeerConnectionPool(peerConnection)
			if mediaJoinedState.Load() {
				cleanupParticipantSession("", false)
			} else {
				signalPeerConnections()
			}
		default:
		}
	})

	peerConnection.OnTrack(func(t *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		trackParticipantName := currentParticipantName()
		trackParticipantSessionID := participantSessionID
		forwardedTrackID := forwardedRemoteTrackID(t)
		codec := t.Codec()
		log.Infof("room_ontrack_start participant=%s session=%s kind=%s track_id=%s source_track_id=%s stream_id=%s rid=%q ssrc=%d rtx_ssrc=%d payload_type=%d codec=%s clock_rate=%d channels=%d fmtp=%q feedback=%s has_rtx=%t",
			trackParticipantName, trackParticipantSessionID, t.Kind(), forwardedTrackID, t.ID(), t.StreamID(), t.RID(), t.SSRC(), t.RtxSSRC(), t.PayloadType(), codec.MimeType, codec.ClockRate, codec.Channels, codec.SDPFmtpLine, rtcpFeedbackSummary(codec.RTCPFeedback), t.HasRTX())
		broadcastAssistantEvent("signal", fmt.Sprintf("received %s track from browser", t.Kind().String()), map[string]any{
			"participant":   trackParticipantName,
			"trackId":       forwardedTrackID,
			"sourceTrackId": t.ID(),
			"streamId":      t.StreamID(),
			"payloadType":   t.PayloadType(),
		})

		// Create a track to fan out our incoming media to all browser peers.
		trackLocal, err := addTrack(t, trackParticipantName, trackParticipantSessionID)
		if err != nil {
			log.Errorf("Failed to create local track for remote track=%s: %v", t.ID(), err)
			return
		}
		broadcastKanbanEvent("participant_track", participantTrackPayload(trackParticipantName, t))
		signalPeerConnections()
		defer removeTrack(trackLocal)

		audioDecoder, audioChannels, err := newRoomAudioDecoder(t)
		if err != nil {
			log.Errorf("Failed to create audio decoder for track=%s: %v", t.ID(), err)
		}
		audioTrackKey := roomAudioTrackKey(t)
		if audioDecoder != nil {
			defer roomMixer.removeTrack(audioTrackKey)
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
					roomMixer.submit(audioTrackKey, trackParticipantName, pcm)
				}
			}

			// Preserve RTP header extensions from the publisher. Mobile browsers
			// can carry video orientation/rotation and congestion metadata there;
			// stripping them makes phone video look unstable to subscribers.
			if err = trackLocal.WriteRTP(packet); err != nil {
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
	cancelICERecovery := func() {
		iceRecoveryMu.Lock()
		defer iceRecoveryMu.Unlock()
		if iceRecoveryTimer != nil {
			iceRecoveryTimer.Stop()
			iceRecoveryTimer = nil
		}
	}
	defer cancelICERecovery()
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
			if !iceStateNeedsRecovery(peerConnection.ICEConnectionState()) {
				return // recovered on its own during the grace window
			}
			totalTracks, audioTracks, videoTracks := snapshotForwardedTrackCounts()
			log.Infof("ICE still disconnected after grace; restarting ICE for participant=%s session=%s total_tracks=%d audio_tracks=%d video_tracks=%d", currentParticipantName(), participantSessionID, totalTracks, audioTracks, videoTracks)
			signalPeerConnectionICE(peerConnection)
		})
	}

	peerConnection.OnICEConnectionStateChange(func(is webrtc.ICEConnectionState) {
		log.Infof("ICE connection state changed: %s", is)
		switch {
		case iceStateNeedsRecovery(is):
			scheduleICERecovery()
		case is == webrtc.ICEConnectionStateConnected || is == webrtc.ICEConnectionStateCompleted:
			cancelICERecovery()
		}
	})

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

		log.Infof("Got message: %s", raw)

		if err := json.Unmarshal(raw, &message); err != nil {
			log.Errorf("Failed to unmarshal json to message: %v", err)

			return
		}

		if participantAccepted && message.Event != "participant" && !kanbanApp.participantSessionCurrent(currentParticipantName(), participantSessionID) {
			_ = sendKanbanEvent(c, "session_replaced", "This browser session was replaced by a newer room join.")
			return
		}

		switch message.Event {
		case "participant":
			// Identity comes from the authenticated session, never from the
			// payload: a client cannot join as anyone but their own account.
			name := participantNameForAccount(sessionUser)
			if participantAccepted {
				continue
			}
			admittedName, err := kanbanApp.admitParticipantSession(name, participantSessionID)
			if err != nil {
				_ = sendKanbanEvent(c, "access_denied", err.Error()+".")
				continue
			}
			setParticipantName(admittedName)
			participantAccepted = true
			participantAcceptedState.Store(true)
			replaceExistingParticipantSession(admittedName, participantSessionID, peerConnection, c)
			if err := sendKanbanEvent(c, "access_granted", map[string]any{
				"name": admittedName,
			}); err != nil {
				log.Errorf("Failed to send access grant: %v", err)
			}
			if err := sendKanbanEvent(c, "participants", kanbanApp.roomSnapshot()); err != nil {
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
			if err := sendKanbanEvent(c, "status", "Connected to conference room"); err != nil {
				log.Errorf("Failed to send Kanban status: %v", err)
			}
			if assistantStatus := kanbanApp.assistantStatusSnapshot(); assistantStatus != nil {
				if err := sendKanbanEvent(c, "assistant_event", assistantStatus); err != nil {
					log.Errorf("Failed to send assistant status: %v", err)
				}
			}
			if activeSpeaker := kanbanApp.activeSpeakerSnapshot(); activeSpeaker != nil {
				if err := sendKanbanEvent(c, "active_speaker", activeSpeaker); err != nil {
					log.Errorf("Failed to send active speaker snapshot: %v", err)
				}
			}
			broadcastKanbanEvent("participant_joined", map[string]any{
				"name": admittedName,
			})
			broadcastKanbanEvent("participants", kanbanApp.roomSnapshot())
		case "media_ready":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "Enter the room before joining media.")
				continue
			}
			if mediaJoined {
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
			})
			listLock.Unlock()
			if err := sendKanbanEvent(c, "board", kanbanApp.snapshotState()); err != nil {
				log.Errorf("Failed to send Kanban board state after media join: %v", err)
			}
			if err := sendKanbanEvent(c, "undo_available", kanbanApp.canUndoDelete()); err != nil {
				log.Errorf("Failed to send undo state after media join: %v", err)
			}
			sendParticipantTrackSnapshots(c, currentParticipantName())
			broadcastKanbanEvent("participants", kanbanApp.roomSnapshot())
			signalPeerConnections()
		case "request_participant_tracks":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "Enter the room before requesting media labels.")
				continue
			}
			sendParticipantTrackSnapshots(c, currentParticipantName())
		case "candidate":
			candidate := webrtc.ICECandidateInit{}
			if err := json.Unmarshal([]byte(message.Data), &candidate); err != nil {
				log.Errorf("Failed to unmarshal json to candidate: %v", err)

				return
			}

			log.Infof("Got candidate: %v", candidate)

			if peerConnection.RemoteDescription() == nil {
				pendingRemoteCandidates = append(pendingRemoteCandidates, candidate)
				log.Infof("Queued ICE candidate until remote description is set")
				continue
			}

			if err := peerConnection.AddICECandidate(candidate); err != nil {
				log.Errorf("Failed to add ICE candidate: %v", err)
			}
		case "answer":
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
			clearPendingOfferMetadata(peerConnection)
			for _, candidate := range pendingRemoteCandidates {
				if err := peerConnection.AddICECandidate(candidate); err != nil {
					log.Errorf("Failed to add queued ICE candidate: %v", err)
				}
			}
			pendingRemoteCandidates = pendingRemoteCandidates[:0]
			signalPeerConnections()
		case "restart_ice":
			if !participantAccepted || !mediaJoined {
				continue
			}
			totalTracks, audioTracks, videoTracks := snapshotForwardedTrackCounts()
			log.Infof("Client requested ICE restart for participant=%s session=%s total_tracks=%d audio_tracks=%d video_tracks=%d", currentParticipantName(), participantSessionID, totalTracks, audioTracks, videoTracks)
			signalPeerConnectionICE(peerConnection)
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
			scoutChat = newScoutChatSession(c, canAccessArtifactLibrary(sessionUser))
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
			broadcastKanbanEvent("meeting_archived", result)
			broadcastKanbanEvent("memory", kanbanApp.memorySnapshotForClients(20))
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
			snapshot := kanbanApp.setTranscriptRecording(payload.Enabled, currentParticipantName())
			recording, _ := snapshot["recording"].(roomRecordingState)
			broadcastKanbanEvent("participants", snapshot)
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
			payload := struct {
				MicMuted      bool `json:"micMuted"`
				CameraOff     bool `json:"cameraOff"`
				ScreenSharing bool `json:"screenSharing"`
			}{}
			if err := json.Unmarshal([]byte(message.Data), &payload); err != nil {
				log.Errorf("Failed to unmarshal participant media state: %v", err)
				continue
			}
			snapshot, err := kanbanApp.setParticipantMediaState(currentParticipantName(), participantMediaState{
				MicMuted:      payload.MicMuted,
				CameraOff:     payload.CameraOff,
				ScreenSharing: payload.ScreenSharing,
			})
			if err != nil {
				log.Errorf("Failed to update participant media state: %v", err)
				continue
			}
			broadcastKanbanEvent("participants", snapshot)
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
			logClientMediaQualityReport(message.Data, currentParticipantName(), participantSessionID)
		case "media_error":
			if !participantAccepted {
				continue
			}
			logClientMediaErrorReport(message.Data, currentParticipantName(), participantSessionID)
		case "screen_share_started":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "Enter the room before sharing your screen.")
				continue
			}
			broadcastKanbanEvent("participants", kanbanApp.setParticipantScreenSharing(currentParticipantName(), true))
			broadcastKanbanEvent("screen_share_started", map[string]any{
				"name": currentParticipantName(),
			})
			go dispatchKeyFrame()
			broadcastAssistantEvent("status", currentParticipantName()+" started sharing their screen", nil)
		case "screen_share_stopped":
			if !participantAccepted {
				continue
			}
			broadcastKanbanEvent("participants", kanbanApp.setParticipantScreenSharing(currentParticipantName(), false))
			broadcastKanbanEvent("screen_share_stopped", map[string]any{
				"name": currentParticipantName(),
			})
			go dispatchKeyFrame()
			broadcastAssistantEvent("status", currentParticipantName()+" stopped sharing their screen", nil)
		default:
			log.Errorf("unknown message: %+v", message)
		}
	}
}

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

	broadcastKanbanEvent("board", kanbanApp.snapshotState())
	broadcastKanbanEvent("undo_available", kanbanApp.canUndoDelete())
	broadcastAssistantEvent("action", fmt.Sprintf("%s %s", actor, action), nil)
	kanbanApp.refreshRealtimeBoardContext(action)
}

// Helper to make Gorilla Websockets threadsafe.
type threadSafeWriter struct {
	*websocket.Conn
	sync.Mutex
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
