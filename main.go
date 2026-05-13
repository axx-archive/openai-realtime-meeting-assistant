package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/logging"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

// nolint
var (
	addr     = flag.String("addr", ":3000", "http service address")
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	indexTemplate = &template.Template{}

	// lock for peerConnections and trackLocals
	listLock        sync.RWMutex
	peerConnections []peerConnectionState
	trackLocals     map[string]*webrtc.TrackLocalStaticRTP

	log = logging.NewDefaultLoggerFactory().NewLogger("openai-realtime-meeting-assistant")

	kanbanApp *kanbanBoardApp
	roomMixer *audioMixer
)

type websocketMessage struct {
	Event string `json:"event"`
	Data  string `json:"data"`
}

type peerConnectionState struct {
	peerConnection *webrtc.PeerConnection
	websocket      *threadSafeWriter
	acceptTrack    func(*webrtc.TrackLocalStaticRTP) bool
	shouldSignal   func(desiredTrackCount int) bool
	signal         func(gatherComplete <-chan struct{}) error
}

func (p peerConnectionState) acceptsTrack(track *webrtc.TrackLocalStaticRTP) bool {
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
	roomMixer = newAudioMixer()
	defer roomMixer.close()
	kanbanApp = newKanbanBoardApp()
	defer kanbanApp.Close()
	if err := kanbanApp.JoinConferenceRoom(); err != nil {
		log.Errorf("Kanban Realtime peer disabled: %v", err)
	}

	// Read index.html from disk into memory, serve whenever anyone requests /
	indexHTML, err := os.ReadFile("index.html")
	if err != nil {
		panic(err)
	}
	indexTemplate = template.Must(template.New("").Parse(string(indexHTML)))

	// websocket handler
	http.HandleFunc("/websocket", websocketHandler)
	http.HandleFunc("/archives/", meetingArchiveHandler)

	// index.html handler
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if err = indexTemplate.Execute(w, "ws://"+r.Host+"/websocket"); err != nil {
			log.Errorf("Failed to parse index template: %v", err)
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

func newPeerConnection() (*webrtc.PeerConnection, error) {
	settingEngine := webrtc.SettingEngine{}
	if nat1To1IP := os.Getenv("PION_NAT1TO1_IP"); nat1To1IP != "" {
		settingEngine.SetNAT1To1IPs([]string{nat1To1IP}, webrtc.ICECandidateTypeHost)
	}
	if err := configureEphemeralUDPPortRange(&settingEngine); err != nil {
		return nil, err
	}

	return webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine)).NewPeerConnection(webrtc.Configuration{})
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

// Add to list of tracks and fire renegotation for all PeerConnections.
func addTrack(t *webrtc.TrackRemote) *webrtc.TrackLocalStaticRTP { // nolint
	listLock.Lock()
	defer func() {
		listLock.Unlock()
		signalPeerConnections()
	}()

	// Create a new TrackLocal with the same codec as our incoming
	trackLocal, err := webrtc.NewTrackLocalStaticRTP(t.Codec().RTPCodecCapability, t.ID(), t.StreamID())
	if err != nil {
		panic(err)
	}

	trackLocals[t.ID()] = trackLocal

	return trackLocal
}

// Remove from list of tracks and fire renegotation for all PeerConnections.
func removeTrack(t *webrtc.TrackLocalStaticRTP) {
	listLock.Lock()
	defer func() {
		listLock.Unlock()
		signalPeerConnections()
	}()

	delete(trackLocals, t.ID())
}

// signalPeerConnections updates each PeerConnection so that it is getting all the expected media tracks.
func signalPeerConnections() { // nolint
	listLock.Lock()
	defer func() {
		listLock.Unlock()
		dispatchKeyFrame()
	}()

	attemptSync := func() (tryAgain bool) {
		for i := range peerConnections {
			if peerConnections[i].peerConnection.ConnectionState() == webrtc.PeerConnectionStateClosed {
				peerConnections = append(peerConnections[:i], peerConnections[i+1:]...)

				return true // We modified the slice, start from the beginning
			}

			peer := &peerConnections[i]

			desiredTrackCount := 0
			for _, trackLocal := range trackLocals {
				if peer.acceptsTrack(trackLocal) {
					desiredTrackCount++
				}
			}
			if !peer.shouldSignalWithDesiredTrackCount(desiredTrackCount) {
				continue
			}

			// map of sender we already are seanding, so we don't double send
			existingSenders := map[string]bool{}

			for _, sender := range peer.peerConnection.GetSenders() {
				if sender.Track() == nil {
					continue
				}

				trackID := sender.Track().ID()
				existingSenders[trackID] = true

				// If we have a RTPSender that doesn't map to a existing track remove and signal
				trackLocal, ok := trackLocals[trackID]
				if !ok || !peer.acceptsTrack(trackLocal) {
					if err := peer.peerConnection.RemoveTrack(sender); err != nil {
						return true
					}
				}
			}

			// Don't receive videos we are sending, make sure we don't have loopback
			for _, receiver := range peer.peerConnection.GetReceivers() {
				if receiver.Track() == nil {
					continue
				}

				existingSenders[receiver.Track().ID()] = true
			}

			// Add all track we aren't sending yet to the PeerConnection
			for trackID, trackLocal := range trackLocals {
				if !peer.acceptsTrack(trackLocal) {
					continue
				}

				if _, ok := existingSenders[trackID]; !ok {
					if _, err := peer.peerConnection.AddTrack(trackLocal); err != nil {
						return true
					}
				}
			}

			offer, err := peer.peerConnection.CreateOffer(nil)
			if err != nil {
				return true
			}

			var gatherComplete <-chan struct{}
			if peer.signal != nil {
				gatherComplete = webrtc.GatheringCompletePromise(peer.peerConnection)
			}

			if err = peer.peerConnection.SetLocalDescription(offer); err != nil {
				return true
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
	listLock.Lock()
	defer listLock.Unlock()

	for i := range peerConnections {
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
	participantAccepted := false
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

	// When this frame returns close the Websocket
	defer c.Close() //nolint

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
			if err := peerConnection.Close(); err != nil {
				log.Errorf("Failed to close PeerConnection: %v", err)
			}
		case webrtc.PeerConnectionStateClosed:
			signalPeerConnections()
		default:
		}
	})

	peerConnection.OnTrack(func(t *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		log.Infof("Got remote track: Kind=%s, ID=%s, PayloadType=%d", t.Kind(), t.ID(), t.PayloadType())
		trackParticipantName := currentParticipantName()
		broadcastAssistantEvent("signal", fmt.Sprintf("received %s track from browser", t.Kind().String()), map[string]any{
			"participant": trackParticipantName,
			"trackId":     t.ID(),
			"streamId":    t.StreamID(),
			"payloadType": t.PayloadType(),
		})
		broadcastKanbanEvent("participant_track", map[string]any{
			"name":     trackParticipantName,
			"kind":     t.Kind().String(),
			"trackId":  t.ID(),
			"streamId": t.StreamID(),
		})

		// Create a track to fan out our incoming media to all browser peers.
		trackLocal := addTrack(t)
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
					roomMixer.submit(audioTrackKey, pcm)
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
			log.Errorf("Failed to read message: %v", err)

			return
		}

		log.Infof("Got message: %s", raw)

		if err := json.Unmarshal(raw, &message); err != nil {
			log.Errorf("Failed to unmarshal json to message: %v", err)

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
			setParticipantName(name)
			if participantAccepted {
				continue
			}
			participantAccepted = true
			kanbanApp.noteParticipant(name)
			listLock.Lock()
			peerConnections = append(peerConnections, peerConnectionState{
				peerConnection: peerConnection,
				websocket:      c,
			})
			listLock.Unlock()
			if err := sendKanbanEvent(c, "access_granted", map[string]any{
				"name": name,
			}); err != nil {
				log.Errorf("Failed to send access grant: %v", err)
			}
			if err := sendKanbanEvent(c, "board", kanbanApp.snapshotState()); err != nil {
				log.Errorf("Failed to send Kanban board state: %v", err)
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
				"name": name,
			})
			broadcastKanbanEvent("participants", kanbanApp.participantSnapshot())
			signalPeerConnections()
		case "candidate":
			candidate := webrtc.ICECandidateInit{}
			if err := json.Unmarshal([]byte(message.Data), &candidate); err != nil {
				log.Errorf("Failed to unmarshal json to candidate: %v", err)

				return
			}

			log.Infof("Got candidate: %v", candidate)

			if err := peerConnection.AddICECandidate(candidate); err != nil {
				log.Errorf("Failed to add ICE candidate: %v", err)

				return
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
			broadcastAssistantEvent("query", query.Query, nil)
			if _, _, err := kanbanApp.answerMemoryQuestion(map[string]any{"query": query.Query}); err != nil {
				log.Errorf("Failed to answer assistant query: %v", err)
				if writeErr := sendKanbanEvent(c, "assistant_event", map[string]any{
					"kind":      "error",
					"text":      err.Error(),
					"createdAt": time.Now().UTC().Format(time.RFC3339Nano),
				}); writeErr != nil {
					log.Errorf("Failed to send assistant query error: %v", writeErr)
				}
			}
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
		case "screen_share_started":
			if !participantAccepted {
				_ = sendKanbanEvent(c, "access_denied", "Enter the room before sharing your screen.")
				continue
			}
			broadcastKanbanEvent("screen_share_started", map[string]any{
				"name": currentParticipantName(),
			})
			broadcastAssistantEvent("status", currentParticipantName()+" started sharing their screen", nil)
		case "screen_share_stopped":
			if !participantAccepted {
				continue
			}
			broadcastKanbanEvent("screen_share_stopped", map[string]any{
				"name": currentParticipantName(),
			})
			broadcastAssistantEvent("status", currentParticipantName()+" stopped sharing their screen", nil)
		default:
			log.Errorf("unknown message: %+v", message)
		}
	}
}

// Helper to make Gorilla Websockets threadsafe.
type threadSafeWriter struct {
	*websocket.Conn
	sync.Mutex
}

func (t *threadSafeWriter) WriteJSON(v any) error {
	t.Lock()
	defer t.Unlock()

	return t.Conn.WriteJSON(v)
}
