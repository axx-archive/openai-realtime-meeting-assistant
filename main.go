package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/interceptor"
	"github.com/pion/logging"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

// nolint
var (
	addr     = flag.String("addr", ":3000", "http service address")
	upgrader = websocket.Upgrader{
		CheckOrigin: websocketOriginAllowed,
	}
	indexHTML []byte

	// lock for peerConnections and trackLocals
	listLock                     sync.RWMutex
	peerConnections              []peerConnectionState
	activeParticipantConnections map[string]peerConnectionState
	trackLocals                  map[string]*webrtc.TrackLocalStaticRTP
	trackParticipants            map[string]string
	trackParticipantSessions     map[string]string
	trackSourceIDs               map[string]string
	participantSessionSeq        atomic.Uint64

	log = logging.NewDefaultLoggerFactory().NewLogger("openai-realtime-meeting-assistant")

	kanbanApp *kanbanBoardApp
	roomMixer *audioMixer
)

type websocketMessage struct {
	Event string `json:"event"`
	Data  string `json:"data"`
}

type peerConnectionState struct {
	peerConnection  *webrtc.PeerConnection
	websocket       *threadSafeWriter
	participantName string
	sessionID       string
	acceptTrack     func(*webrtc.TrackLocalStaticRTP) bool
	shouldSignal    func(desiredTrackCount int) bool
	signal          func(gatherComplete <-chan struct{}) error
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
	if p.acceptTrack == nil {
		return true
	}

	return p.acceptTrack(track)
}

func (p peerConnectionState) shouldSignalWithDesiredTrackCount(desiredTrackCount int) bool {
	if p.shouldSignal == nil {
		return true
	}

	return p.shouldSignal(desiredTrackCount)
}

func main() {
	// Parse the flags passed to program
	flag.Parse()

	// Init other state
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{}
	trackParticipants = map[string]string{}
	trackParticipantSessions = map[string]string{}
	trackSourceIDs = map[string]string{}
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
	http.HandleFunc("/websocket", websocketHandler)
	http.HandleFunc("/archives/", meetingArchiveHandler)
	http.HandleFunc("/participants", participantsHandler)
	http.HandleFunc("/client-config", clientConfigHandler)

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

	// start HTTP server
	if err = http.ListenAndServe(*addr, nil); err != nil { //nolint: gosec
		log.Errorf("Failed to start http server: %v", err)
	}
}

func meetingArchiveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	archiveID := strings.TrimPrefix(r.URL.Path, "/archives/")
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

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"rtcConfiguration": browserRTCConfigurationFromEnv(),
	}); err != nil {
		log.Errorf("Failed to encode client config: %v", err)
	}
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
			MimeType:     webrtc.MimeTypeVP8,
			ClockRate:    90000,
			RTCPFeedback: []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}, {Type: "goog-remb"}},
		},
		PayloadType: 96,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, nil, fmt.Errorf("register vp8 codec: %w", err)
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

	registry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, registry); err != nil {
		return nil, nil, fmt.Errorf("register default interceptors: %w", err)
	}

	return mediaEngine, registry, nil
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
	trackLocals[trackLocal.ID()] = trackLocal
	trackParticipants[trackLocal.ID()] = canonicalParticipantName(participantName)
	trackParticipantSessions[trackLocal.ID()] = sessionID
	trackSourceIDs[trackLocal.ID()] = t.ID()
	listLock.Unlock()

	return trackLocal, nil
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

	listLock.Lock()
	defer func() {
		listLock.Unlock()
		signalPeerConnections()
	}()

	delete(trackLocals, t.ID())
	delete(trackParticipants, t.ID())
	delete(trackParticipantSessions, t.ID())
	delete(trackSourceIDs, t.ID())
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
	for _, urls := range [][]string{
		stunURLs,
		splitEnvList("MEETING_TURN_URLS"),
	} {
		if len(urls) == 0 {
			continue
		}
		server := map[string]any{"urls": urls}
		if strings.HasPrefix(strings.ToLower(urls[0]), "turn:") || strings.HasPrefix(strings.ToLower(urls[0]), "turns:") {
			if username := strings.TrimSpace(os.Getenv("MEETING_TURN_USERNAME")); username != "" {
				server["username"] = username
			}
			if credential := strings.TrimSpace(os.Getenv("MEETING_TURN_CREDENTIAL")); credential != "" {
				server["credential"] = credential
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
	listLock.Unlock()

	if removedConnection || removedTracks {
		signalPeerConnections()
	}
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
	signalPeerConnectionsWithRestart(nil)
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
				continue
			}

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
					if _, err := peer.peerConnection.AddTransceiverFromTrack(trackLocal, webrtc.RTPTransceiverInit{
						Direction: webrtc.RTPTransceiverDirectionSendonly,
					}); err != nil {
						log.Errorf("Failed to add sender track=%s: %v", trackID, err)
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

			if peer.websocket != nil {
				for _, snapshot := range participantTrackSnapshotsLocked(peer.participantName) {
					sendParticipantTrackSnapshot(peer.websocket, snapshot)
				}
			}

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

			log.Infof("Send offer to client: %v", offer)

			if err = peer.websocket.WriteJSON(&websocketMessage{
				Event: "offer",
				Data:  string(offerString),
			}); err != nil {
				return true
			}
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
	// Upgrade HTTP request to Websocket
	unsafeConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Errorf("Failed to upgrade HTTP to Websocket: ", err)

		return
	}

	c := &threadSafeWriter{unsafeConn, sync.Mutex{}} // nolint
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
			if err := peerConnection.Close(); err != nil {
				log.Errorf("Failed to close PeerConnection: %v", err)
			}
		case webrtc.PeerConnectionStateClosed:
			if mediaJoinedState.Load() {
				cleanupParticipantSession("", false)
			} else {
				signalPeerConnections()
			}
		default:
		}
	})

	peerConnection.OnTrack(func(t *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		log.Infof("Got remote track: Kind=%s, ID=%s, PayloadType=%d", t.Kind(), t.ID(), t.PayloadType())
		trackParticipantName := currentParticipantName()
		trackParticipantSessionID := participantSessionID
		forwardedTrackID := forwardedRemoteTrackID(t)
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

		for {
			packet, _, err := t.ReadRTP()
			if err != nil {
				return
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

			packet.Extension = false
			packet.Extensions = nil

			if err = trackLocal.WriteRTP(packet); err != nil {
				return
			}
		}
	})

	peerConnection.OnICEConnectionStateChange(func(is webrtc.ICEConnectionState) {
		log.Infof("ICE connection state changed: %s", is)
	})

	message := &websocketMessage{}
	for {
		_, raw, err := c.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
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
			payload := struct {
				Name     string `json:"name"`
				Password string `json:"password"`
			}{}
			if err := json.Unmarshal([]byte(message.Data), &payload); err != nil {
				log.Errorf("Failed to unmarshal participant payload: %v", err)
				_ = sendKanbanEvent(c, "access_denied", "Could not read participant access details.")
				continue
			}
			name := canonicalParticipantName(payload.Name)
			if name == "" || !validMeetingPassword(payload.Password) {
				_ = sendKanbanEvent(c, "access_denied", "Choose a listed participant and enter the room password.")
				continue
			}
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
			if err := sendKanbanEvent(c, "memory", kanbanApp.memorySnapshot(20)); err != nil {
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

			log.Infof("Got answer: %v", answer)

			if err := peerConnection.SetRemoteDescription(answer); err != nil {
				log.Errorf("Failed to set remote description: %v", err)

				return
			}
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
			log.Infof("Client requested ICE restart for participant=%s session=%s", currentParticipantName(), participantSessionID)
			signalPeerConnectionICE(peerConnection)
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
			broadcastKanbanEvent("memory", kanbanApp.memorySnapshot(20))
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
		case "screen_share_started":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "Enter the room before sharing your screen.")
				continue
			}
			broadcastKanbanEvent("participants", kanbanApp.setParticipantScreenSharing(currentParticipantName(), true))
			broadcastKanbanEvent("screen_share_started", map[string]any{
				"name": currentParticipantName(),
			})
			broadcastAssistantEvent("status", currentParticipantName()+" started sharing their screen", nil)
		case "screen_share_stopped":
			if !participantAccepted {
				continue
			}
			broadcastKanbanEvent("participants", kanbanApp.setParticipantScreenSharing(currentParticipantName(), false))
			broadcastKanbanEvent("screen_share_stopped", map[string]any{
				"name": currentParticipantName(),
			})
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

func (t *threadSafeWriter) WriteJSON(v any) error {
	if t == nil || t.Conn == nil {
		return fmt.Errorf("websocket is closed")
	}

	t.Lock()
	defer t.Unlock()

	return t.Conn.WriteJSON(v)
}
