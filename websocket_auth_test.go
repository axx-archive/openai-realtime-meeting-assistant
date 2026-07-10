package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

func TestWebsocketRejectsUnauthenticatedUpgrade(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BONFIRE_USERS_PATH", filepath.Join(dir, "users.json"))
	t.Setenv("BONFIRE_SESSIONS_PATH", filepath.Join(dir, "sessions.json"))

	server := httptest.NewServer(http.HandlerFunc(websocketHandler))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/websocket"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		conn.Close()
		t.Fatal("expected websocket dial without a session to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 before upgrade, got %+v", resp)
	}
}

func TestWebsocketAdmitsSessionIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BONFIRE_USERS_PATH", filepath.Join(dir, "users.json"))
	t.Setenv("BONFIRE_SESSIONS_PATH", filepath.Join(dir, "sessions.json"))
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))

	// The handler outlives httptest.Server.Close (the websocket is hijacked),
	// so the global app is left in place rather than restored to nil — a nil
	// kanbanApp would panic the handler's deferred cleanup. Drain the leaked
	// handler before the test returns so a later test's kanbanApp swap can't
	// race this connection's reads (runs after the conn/server close defers).
	if kanbanApp == nil {
		kanbanApp = newKanbanBoardApp()
	}
	t.Cleanup(func() { waitForWebsocketHandlersToDrain(t, 5*time.Second) })

	token, err := userSessionStore().create("aj@shareability.com")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(websocketHandler))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/websocket"
	header := http.Header{}
	header.Set("Cookie", sessionCookieName+"="+token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial with session cookie: %v", err)
	}
	defer conn.Close()

	// The payload name is attacker-controlled; the server must admit the
	// session identity instead.
	join := map[string]string{"event": "participant", "data": `{"name":"Tim","password":""}`}
	if err := conn.WriteJSON(join); err != nil {
		t.Fatalf("send participant event: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("read websocket message: %v", err)
		}
		if message.Event != "kanban" {
			continue
		}
		var inner struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(message.Data), &inner); err != nil {
			t.Fatalf("decode kanban envelope: %v", err)
		}
		if inner.Event == "access_denied" {
			t.Fatalf("expected admission, got access_denied: %s", inner.Data)
		}
		if inner.Event != "access_granted" {
			continue
		}
		var grant struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(inner.Data, &grant); err != nil {
			t.Fatalf("decode access grant: %v", err)
		}
		if grant.Name != "AJ" {
			t.Fatalf("expected session identity AJ to be admitted, got %q", grant.Name)
		}
		return
	}
}

func TestWebsocketAdmitsAccountEmailWhenDisplayNameChanges(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BONFIRE_USERS_PATH", filepath.Join(dir, "users.json"))
	t.Setenv("BONFIRE_SESSIONS_PATH", filepath.Join(dir, "sessions.json"))
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))

	previousApp := kanbanApp
	kanbanApp = newKanbanBoardApp()
	// Drain the hijacked handler before restoring the global, otherwise the
	// leaked goroutine's kanbanApp reads race this write (and the swap above
	// races any prior leaked handler — every websocket test drains, so none
	// leak past its own return).
	t.Cleanup(func() {
		waitForWebsocketHandlersToDrain(t, 5*time.Second)
		if previousApp != nil {
			kanbanApp = previousApp
		}
	})

	if _, err := accountStore().updateProfile("aj@shareability.com", "// aj", ""); err != nil {
		t.Fatalf("update profile: %v", err)
	}
	token, err := userSessionStore().create("aj@shareability.com")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(websocketHandler))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/websocket"
	header := http.Header{}
	header.Set("Cookie", sessionCookieName+"="+token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial with session cookie: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]string{"event": "participant", "data": `{}`}); err != nil {
		t.Fatalf("send participant event: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("read websocket message: %v", err)
		}
		if message.Event != "kanban" {
			continue
		}
		var inner struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(message.Data), &inner); err != nil {
			t.Fatalf("decode kanban envelope: %v", err)
		}
		if inner.Event == "access_denied" {
			t.Fatalf("expected admission from account email, got access_denied: %s", inner.Data)
		}
		if inner.Event != "access_granted" {
			continue
		}
		var grant struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(inner.Data, &grant); err != nil {
			t.Fatalf("decode access grant: %v", err)
		}
		if grant.Name != "AJ" {
			t.Fatalf("expected email-derived room identity AJ, got %q", grant.Name)
		}
		return
	}
}

func TestWebsocketNativeMediaReadyReceivesServerOffer(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BONFIRE_USERS_PATH", filepath.Join(dir, "users.json"))
	t.Setenv("BONFIRE_SESSIONS_PATH", filepath.Join(dir, "sessions.json"))
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))

	previousApp := kanbanApp
	kanbanApp = newKanbanBoardApp()
	// Drain the hijacked handler before restoring the global (see the twin note
	// in TestWebsocketAdmitsAccountEmailWhenDisplayNameChanges).
	t.Cleanup(func() {
		waitForWebsocketHandlersToDrain(t, 5*time.Second)
		if previousApp != nil {
			kanbanApp = previousApp
		}
	})

	listLock.Lock()
	previousPeerConnections := peerConnections
	previousActiveParticipantConnections := activeParticipantConnections
	peerConnections = nil
	activeParticipantConnections = map[string]peerConnectionState{}
	listLock.Unlock()
	t.Cleanup(func() {
		listLock.Lock()
		peerConnections = previousPeerConnections
		activeParticipantConnections = previousActiveParticipantConnections
		listLock.Unlock()
	})

	token, err := userSessionStore().create("tim@shareability.com")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(websocketHandler))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/websocket"
	header := http.Header{}
	header.Set("Cookie", sessionCookieName+"="+token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial with session cookie: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]string{
		"event": "participant",
		"data":  `{"client":{"platform":"ios","version":"test"}}`,
	}); err != nil {
		t.Fatalf("send native participant event: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("read websocket message before admission: %v", err)
		}
		if message.Event != "kanban" {
			continue
		}
		var inner struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(message.Data), &inner); err != nil {
			t.Fatalf("decode kanban envelope: %v", err)
		}
		if inner.Event == "access_denied" {
			t.Fatalf("expected native admission, got access_denied: %s", inner.Data)
		}
		if inner.Event == "access_granted" {
			break
		}
	}

	if err := conn.WriteJSON(map[string]string{
		"event": "media_ready",
		"data":  `{"client":{"platform":"ios"},"media":{"audio":true,"video":true}}`,
	}); err != nil {
		t.Fatalf("send native media_ready event: %v", err)
	}

	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("read websocket message after media_ready: %v", err)
		}
		if message.Event != "offer" {
			continue
		}
		var offer struct {
			Type string `json:"type"`
			SDP  string `json:"sdp"`
		}
		if err := json.Unmarshal([]byte(message.Data), &offer); err != nil {
			t.Fatalf("decode server offer: %v", err)
		}
		if offer.Type != "offer" || !strings.Contains(offer.SDP, "m=audio") || !strings.Contains(offer.SDP, "m=video") {
			t.Fatalf("unexpected offer: type=%q sdp=%q", offer.Type, offer.SDP)
		}
		return
	}
}

func TestWebsocketNativeAnswerCandidateRestartAndLayerSelection(t *testing.T) {
	conn := newIsolatedNativeWebsocket(t, "tom@shareability.com")

	writeNativeWebsocketEvent(t, conn, "participant", map[string]any{
		"client": map[string]string{"platform": "ios", "version": "test"},
	})
	waitForKanbanEvent(t, conn, "access_granted", 5*time.Second)

	writeNativeWebsocketEvent(t, conn, "media_ready", map[string]any{
		"client": map[string]string{"platform": "ios"},
		"media":  map[string]bool{"audio": true, "video": true},
	})
	offer := waitForServerOffer(t, conn, 5*time.Second)

	nativePeer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create native peer: %v", err)
	}
	t.Cleanup(func() {
		_ = nativePeer.Close()
	})

	candidateCh := make(chan webrtc.ICECandidateInit, 1)
	nativePeer.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		select {
		case candidateCh <- candidate.ToJSON():
		default:
		}
	})

	if err := nativePeer.SetRemoteDescription(offer); err != nil {
		t.Fatalf("native peer set remote offer: %v", err)
	}
	answer, err := nativePeer.CreateAnswer(nil)
	if err != nil {
		t.Fatalf("native peer create answer: %v", err)
	}
	gatherComplete := webrtc.GatheringCompletePromise(nativePeer)
	if err := nativePeer.SetLocalDescription(answer); err != nil {
		t.Fatalf("native peer set local answer: %v", err)
	}

	candidate := webrtc.ICECandidateInit{
		Candidate: "candidate:0 1 udp 2130706431 127.0.0.1 9 typ host",
	}
	select {
	case gathered := <-candidateCh:
		candidate = gathered
	case <-gatherComplete:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for native ICE gathering")
	}

	// Send the candidate before the answer to exercise the server's pending
	// candidate queue, which native clients can hit when trickle ICE wins the
	// race against SDP answer delivery.
	writeNativeWebsocketEvent(t, conn, "candidate", candidate)
	writeNativeWebsocketEvent(t, conn, "answer", nativePeer.LocalDescription())

	writeNativeWebsocketEvent(t, conn, "select_layer", map[string]string{"layer": "low"})
	waitForNativeLayerTier(t, "Tom", layerTierLow, 2*time.Second)

	initialUfrag := iceUfragFromSDP(t, offer.SDP)

	writeNativeWebsocketEvent(t, conn, "restart_ice", map[string]any{"reason": "native-network-change"})
	restartOffer := waitForServerOffer(t, conn, 8*time.Second)
	if restartOffer.Type != webrtc.SDPTypeOffer || !strings.Contains(restartOffer.SDP, "a=ice-ufrag:") {
		t.Fatalf("restart offer did not look like a server offer with ICE credentials: %+v", restartOffer)
	}
	// An ICE restart must roll FRESH ICE credentials (card-003 W4 gap 3): a
	// restart offer that reuses the initial ufrag is not actually restarting the
	// transport, so peers behind a broken path would never re-gather.
	restartUfrag := iceUfragFromSDP(t, restartOffer.SDP)
	if restartUfrag == initialUfrag {
		t.Fatalf("restart offer reused the initial ICE ufrag %q — the server did not perform an ICE restart", initialUfrag)
	}
}

// iceUfragFromSDP returns the first a=ice-ufrag value in an SDP, failing the
// test if the SDP carries no ICE ufrag at all.
func iceUfragFromSDP(t *testing.T, sdp string) string {
	t.Helper()
	const marker = "a=ice-ufrag:"
	idx := strings.Index(sdp, marker)
	if idx == -1 {
		t.Fatalf("SDP has no %s line: %s", marker, sdp)
	}
	rest := sdp[idx+len(marker):]
	if end := strings.IndexAny(rest, "\r\n"); end != -1 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

// TestWebsocketRestartIceRateLimited pins the card-003 W4 gap 1 wiring: a burst
// of restart_ice past the token-bucket budget must yield BOUNDED server offers
// (the burst), not one renegotiation per event. Each offer is answered so the
// server peer returns to a stable state — otherwise the signaling state, not the
// bucket, would cap the offers and the test would still pass with the rate limit
// removed.
func TestWebsocketRestartIceRateLimited(t *testing.T) {
	conn := newIsolatedNativeWebsocket(t, "tom@shareability.com")

	writeNativeWebsocketEvent(t, conn, "participant", map[string]any{
		"client": map[string]string{"platform": "ios", "version": "test"},
	})
	waitForKanbanEvent(t, conn, "access_granted", 5*time.Second)

	writeNativeWebsocketEvent(t, conn, "media_ready", map[string]any{
		"client": map[string]string{"platform": "ios"},
		"media":  map[string]bool{"audio": true, "video": true},
	})
	offer := waitForServerOffer(t, conn, 5*time.Second)

	nativePeer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create native peer: %v", err)
	}
	t.Cleanup(func() { _ = nativePeer.Close() })

	// Answer the initial offer so the server peer is stable before the storm.
	answerServerOffer(t, conn, nativePeer, offer)

	// Fire well past the burst, all inside one refill window (no token refills in
	// a handful of milliseconds).
	const restarts = 6
	for i := 0; i < restarts; i++ {
		writeNativeWebsocketEvent(t, conn, "restart_ice", map[string]any{"reason": "storm"})
	}

	offers := answerServerOffersUntilQuiet(t, conn, nativePeer, 2*time.Second, 8*time.Second)
	if offers < 1 {
		t.Fatal("a legitimate first restart_ice produced no server offer — the bucket must never throttle the first attempt")
	}
	if offers > iceRestartBucketBurst {
		t.Fatalf("%d rapid restart_ice events produced %d server offers; the token bucket must cap it at the burst (%d)", restarts, offers, int(iceRestartBucketBurst))
	}
}

// answerServerOffer answers a server offer on nativePeer and sends the answer
// back, returning the server peer to a stable signaling state. It waits for
// the native peer's ICE gathering to complete before returning: pion rejects
// SetRemoteDescription("attempting to gather candidates during gathering
// state") if the NEXT offer lands while the previous answer is still
// gathering, and rapid ICE-restart offers (bucket burst 4, refill 1/5s) make
// that overlap routine.
func answerServerOffer(t *testing.T, conn *websocket.Conn, nativePeer *webrtc.PeerConnection, offer webrtc.SessionDescription) {
	t.Helper()
	if err := nativePeer.SetRemoteDescription(offer); err != nil {
		t.Fatalf("native peer set remote offer: %v", err)
	}
	answer, err := nativePeer.CreateAnswer(nil)
	if err != nil {
		t.Fatalf("native peer create answer: %v", err)
	}
	gatherComplete := webrtc.GatheringCompletePromise(nativePeer)
	if err := nativePeer.SetLocalDescription(answer); err != nil {
		t.Fatalf("native peer set local answer: %v", err)
	}
	select {
	case <-gatherComplete:
	case <-time.After(5 * time.Second):
		t.Fatal("native peer ICE gathering did not complete within 5s")
	}
	writeNativeWebsocketEvent(t, conn, "answer", nativePeer.LocalDescription())
}

// answerServerOffersUntilQuiet reads server offers, answering each so the peer
// keeps returning to stable, and returns the total once no offer arrives for
// quiet (or maxWindow elapses). A read timeout is the normal terminator.
func answerServerOffersUntilQuiet(t *testing.T, conn *websocket.Conn, nativePeer *webrtc.PeerConnection, quiet, maxWindow time.Duration) int {
	t.Helper()
	offers := 0
	overallDeadline := time.Now().Add(maxWindow)
	for {
		readDeadline := time.Now().Add(quiet)
		if readDeadline.After(overallDeadline) {
			readDeadline = overallDeadline
		}
		if err := conn.SetReadDeadline(readDeadline); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			// a read timeout (the quiet gap, or the overall window) means the
			// burst has stopped producing offers — done counting
			return offers
		}
		if message.Event != "offer" {
			continue
		}
		var payload struct {
			Type string `json:"type"`
			SDP  string `json:"sdp"`
		}
		if err := json.Unmarshal([]byte(message.Data), &payload); err != nil {
			t.Fatalf("decode server offer: %v", err)
		}
		offers++
		answerServerOffer(t, conn, nativePeer, webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: payload.SDP})
		if time.Now().After(overallDeadline) {
			return offers
		}
	}
}

func newIsolatedNativeWebsocket(t *testing.T, email string) *websocket.Conn {
	t.Helper()

	server := newIsolatedWebsocketServer(t)
	return dialIsolatedWebsocket(t, server, email)
}

func newIsolatedWebsocketServer(t *testing.T) *httptest.Server {
	t.Helper()

	// Registered first so it runs LAST — after the conn.Close and server.Close
	// cleanups — guaranteeing the hijacked handler goroutine has fully returned
	// before this test's isolated globals go out of scope and the next test
	// (which may swap kanbanApp) begins.
	t.Cleanup(func() { waitForWebsocketHandlersToDrain(t, 5*time.Second) })

	dir := t.TempDir()
	t.Setenv("BONFIRE_USERS_PATH", filepath.Join(dir, "users.json"))
	t.Setenv("BONFIRE_SESSIONS_PATH", filepath.Join(dir, "sessions.json"))
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))

	// The websocket handler can outlive httptest.Server.Close after the
	// connection is hijacked. Leave a non-nil app installed for deferred peer
	// cleanup callbacks instead of restoring nil under an active goroutine.
	kanbanApp = newKanbanBoardApp()

	snapshotPeerState(t)
	listLock.Lock()
	peerConnections = nil
	officeConnections = map[string]officeConnectionState{}
	activeParticipantConnections = map[string]peerConnectionState{}
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{}
	trackParticipants = map[string]string{}
	trackParticipantSessions = map[string]string{}
	trackRooms = map[string]string{}
	trackSourceIDs = map[string]string{}
	trackLayerRIDs = map[string]string{}
	trackLayerGroups = map[string]string{}
	subscriberLayerTiers = map[string]string{}
	listLock.Unlock()

	signalRequestLock.Lock()
	if signalRequestTimer != nil {
		signalRequestTimer.Stop()
	}
	signalRequestTimer = nil
	signalRequestLock.Unlock()
	t.Cleanup(func() {
		signalRequestLock.Lock()
		if signalRequestTimer != nil {
			signalRequestTimer.Stop()
		}
		signalRequestTimer = nil
		signalRequestLock.Unlock()
	})

	server := httptest.NewServer(http.HandlerFunc(websocketHandler))
	t.Cleanup(server.Close)
	return server
}

func dialIsolatedWebsocket(t *testing.T, server *httptest.Server, email string) *websocket.Conn {
	t.Helper()

	token, err := userSessionStore().create(email)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/websocket"
	header := http.Header{}
	header.Set("Cookie", sessionCookieName+"="+token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial with session cookie: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})
	return conn
}

func writeNativeWebsocketEvent(t *testing.T, conn *websocket.Conn, event string, payload any) {
	t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s payload: %v", event, err)
	}
	if err := conn.WriteJSON(websocketMessage{
		Event: event,
		Data:  string(data),
	}); err != nil {
		t.Fatalf("send %s event: %v", event, err)
	}
}

func waitForKanbanEvent(t *testing.T, conn *websocket.Conn, event string, timeout time.Duration) json.RawMessage {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("read websocket waiting for kanban/%s: %v", event, err)
		}
		if message.Event != "kanban" {
			continue
		}
		var inner struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(message.Data), &inner); err != nil {
			t.Fatalf("decode kanban envelope: %v", err)
		}
		if inner.Event == "access_denied" {
			t.Fatalf("unexpected access_denied while waiting for %s: %s", event, inner.Data)
		}
		if inner.Event == event {
			return inner.Data
		}
	}
}

func waitForServerOffer(t *testing.T, conn *websocket.Conn, timeout time.Duration) webrtc.SessionDescription {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("read websocket waiting for server offer: %v", err)
		}
		if message.Event != "offer" {
			continue
		}
		var offer struct {
			Type string `json:"type"`
			SDP  string `json:"sdp"`
		}
		if err := json.Unmarshal([]byte(message.Data), &offer); err != nil {
			t.Fatalf("decode server offer: %v", err)
		}
		if offer.Type != "offer" || offer.SDP == "" {
			t.Fatalf("unexpected server offer payload: %+v", offer)
		}
		if message.OfferID == "" || message.Revision == 0 {
			t.Fatalf("server offer missing optional signaling metadata: %+v", message)
		}
		return webrtc.SessionDescription{
			Type: webrtc.SDPTypeOffer,
			SDP:  offer.SDP,
		}
	}
}

func waitForNativeLayerTier(t *testing.T, participant string, tier layerTier, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		listLock.RLock()
		state, ok := activeParticipantConnections[participant]
		got := ""
		if ok {
			got = subscriberLayerTiers[state.sessionID]
		}
		listLock.RUnlock()
		if ok && got == string(tier) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	listLock.RLock()
	defer listLock.RUnlock()
	t.Fatalf("subscriber layer for %s did not become %s; active=%v tiers=%v", participant, tier, activeParticipantConnections, subscriberLayerTiers)
}

// waitForWebsocketHandlersToDrain blocks until every in-flight websocketHandler
// goroutine has returned — including its deferred cleanup that reads package
// globals such as kanbanApp. A hijacked websocket outlives
// httptest.Server.Close, so a test must drain here before it (or the next test)
// swaps one of those globals, otherwise the leaked handler and the swap race.
func waitForWebsocketHandlersToDrain(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if activeWebsocketHandlers.Load() == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if remaining := activeWebsocketHandlers.Load(); remaining != 0 {
		t.Logf("warning: %d websocket handler(s) still active after %s drain wait", remaining, timeout)
	}
}
