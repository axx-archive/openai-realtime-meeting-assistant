package main

// card-003 W4 ICE-restart hardening, client half. Pins:
//   gap 2  performIceRestart refreshes TURN credentials and re-applies them
//          (setConfiguration) before restarting, and the memoized /client-config
//          is age-stamped + refetched once it ages past half its TTL;
//   gap 4  a server-initiated media disconnect re-dials through the signaling
//          reconnect seam (which rebuilds the PC) instead of firing a futile
//          restart_ice against the already-closed server PC;
//   gap 5  a proactive window 'online' listener kicks the bounded ICE-restart
//          ladder after a network handoff.

import (
	"os"
	"strings"
	"testing"
)

func readIndexHTMLForIceRestart(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(data)
}

// gap 2: performIceRestart must await fresh config, re-apply it via
// setConfiguration, then restartIce — in that order — so a session outliving the
// TURN TTL re-gathers with live relay credentials. The required pin is the
// setConfiguration reference (task gap 2).
func TestIndexPerformIceRestartRefreshesConfig(t *testing.T) {
	html := readIndexHTMLForIceRestart(t)

	body := functionBody(html, "function performIceRestart(reason, attempt)")
	if body == "" {
		t.Fatal("could not extract performIceRestart body")
	}
	freshAt := strings.Index(body, "await freshRTCConfiguration()")
	setCfgAt := strings.Index(body, "pc.setConfiguration?.(rtcConfiguration)")
	restartAt := strings.Index(body, "pc.restartIce?.()")
	if freshAt == -1 {
		t.Error("performIceRestart must await freshRTCConfiguration() before restarting")
	}
	if setCfgAt == -1 {
		t.Error("performIceRestart must re-apply the refreshed config via setConfiguration (task gap 2 pin)")
	}
	if restartAt == -1 {
		t.Error("performIceRestart lost its pc.restartIce() call")
	}
	if freshAt != -1 && setCfgAt != -1 && restartAt != -1 && !(freshAt < setCfgAt && setCfgAt < restartAt) {
		t.Error("performIceRestart order must be: await fresh config, setConfiguration, restartIce")
	}
	// after the await, the reconnect seam may have swapped pc — abandon a stale
	// attempt rather than restart the wrong peer
	if !strings.Contains(body, "pc !== restartPeer") {
		t.Error("performIceRestart must abandon a stale attempt if the peer was replaced while awaiting fresh config")
	}
}

// gap 2: the memoized client config is age-stamped and refetched once it ages
// past half its advertised TURN credential TTL (capped at 1h), and the
// reconnect re-dial rebuilds the PC from that fresh config.
func TestIndexClientConfigStalenessRefresh(t *testing.T) {
	html := readIndexHTMLForIceRestart(t)

	for _, want := range []string{
		"let clientConfigLoadedAt = 0",
		"let clientConfigTtlSeconds = 0",
		"clientConfigTtlSeconds = Number(config?.turnCredentialTTLSeconds) || 0",
		"async function loadFreshClientConfig()",
		"async function freshRTCConfiguration()",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html is missing the client-config aging piece %q", want)
		}
	}

	stale := functionBody(html, "function clientConfigIsStale(now = Date.now())")
	if stale == "" {
		t.Fatal("clientConfigIsStale is missing")
	}
	for _, want := range []string{
		"clientConfigTtlSeconds <= 0",
		"clientConfigTtlSeconds * 500",
		"clientConfigMaxAgeCapMs",
	} {
		if !strings.Contains(stale, want) {
			t.Errorf("clientConfigIsStale is missing %q", want)
		}
	}

	// beginMediaSession builds the PC for every (re)dial. functionBody can't
	// scope it (its `(options = {})` default-param brace fools the matcher), so
	// scope by the PC-build line and confirm it is fed fresh config.
	pcBuildAt := strings.Index(html, "pc = new RTCPeerConnection(rtcConfiguration)")
	if pcBuildAt == -1 {
		t.Fatal("beginMediaSession lost its RTCPeerConnection build")
	}
	windowStart := pcBuildAt - 400
	if windowStart < 0 {
		windowStart = 0
	}
	if !strings.Contains(html[windowStart:pcBuildAt], "await freshRTCConfiguration()") {
		t.Error("beginMediaSession (the reconnect re-dial path) must rebuild the PC from freshRTCConfiguration()")
	}
}

// gap 4: a server-initiated media disconnect must re-dial through the signaling
// reconnect seam (which rebuilds the PC), NOT fire a restart_ice that the closed
// server PC can never answer.
func TestIndexMediaDisconnectRedialsInsteadOfRestart(t *testing.T) {
	html := readIndexHTMLForIceRestart(t)

	body := functionBody(html, "function handleMediaDisconnected(detail)")
	if body == "" {
		t.Fatal("could not extract handleMediaDisconnected body")
	}
	for _, want := range []string{
		"roomCanReconnectSignal()",
		"prepareRoomForSignalingReconnect(",
		"scheduleSignalingReconnect(",
		"staleSocket.onclose = null",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("handleMediaDisconnected is missing the re-dial wiring %q", want)
		}
	}
	if strings.Contains(body, "requestIceRestart(") {
		t.Error("handleMediaDisconnected must NOT fire a futile restart_ice against a server-closed PC; it must re-dial")
	}
}

// gap 5: a proactive window 'online' listener kicks the bounded ICE-restart
// ladder when the room peer is already disconnected/failed after a network
// handoff.
func TestIndexOnlineListenerKicksIceRestart(t *testing.T) {
	html := readIndexHTMLForIceRestart(t)

	idx := strings.Index(html, "window.addEventListener('online'")
	if idx == -1 {
		t.Fatal("index.html is missing the proactive 'online' network-change listener")
	}
	tail := html[idx:]
	end := strings.Index(tail, "})")
	if end == -1 {
		t.Fatal("could not bound the 'online' listener body")
	}
	listener := tail[:end]
	for _, want := range []string{
		"pc.connectionState",
		"'disconnected'",
		"'failed'",
		"requestIceRestart('network change')",
	} {
		if !strings.Contains(listener, want) {
			t.Errorf("the 'online' listener is missing %q", want)
		}
	}
}

// FINDING A hardening: the /client-config refetch that precedes an ICE restart
// must be timeout-guarded (a lossy network is exactly when it hangs), must never
// poison the memo / stamps with a non-ok {} (which would leave every later
// re-dial with zero iceServers), and must never let setConfiguration wipe live
// STUN/TURN off the peer.
func TestIndexClientConfigRefreshGuards(t *testing.T) {
	html := readIndexHTMLForIceRestart(t)

	// (a) the fetch is bounded by a 3s abort with a Safari fallback, and
	// fetchClientConfigOnce passes that signal to the /client-config fetch.
	signal := functionBody(html, "function clientConfigAbortSignal()")
	if signal == "" {
		t.Fatal("clientConfigAbortSignal (the ICE-restart config-refetch timeout) is missing")
	}
	for _, want := range []string{
		"AbortSignal.timeout(clientConfigFetchTimeoutMs)",
		"new AbortController()",
		"controller.abort()",
	} {
		if !strings.Contains(signal, want) {
			t.Errorf("clientConfigAbortSignal is missing %q (timeout + Safari fallback)", want)
		}
	}
	if !strings.Contains(html, "const clientConfigFetchTimeoutMs = 3000") {
		t.Error("the config-refetch timeout budget must be 3000ms so the restart is never blocked past it")
	}

	fetchOnce := functionBody(html, "async function fetchClientConfigOnce()")
	if fetchOnce == "" {
		t.Fatal("fetchClientConfigOnce is missing")
	}
	if !strings.Contains(fetchOnce, "signal: clientConfigAbortSignal()") {
		t.Error("fetchClientConfigOnce must pass the timeout signal to the /client-config fetch")
	}

	// (b) a non-ok / empty payload must NOT overwrite the stamps or the memoized
	// config — only a usable payload age-stamps + records last-good, and a miss
	// falls back to the previous good config.
	if !strings.Contains(fetchOnce, "clientConfigIsUsable(config)") {
		t.Error("fetchClientConfigOnce must only accept a usable payload before age-stamping")
	}
	if !strings.Contains(fetchOnce, "lastGoodClientConfig = config") {
		t.Error("fetchClientConfigOnce must record the last good config for synchronous fallback")
	}
	if !strings.Contains(fetchOnce, "return { config: lastGoodClientConfig || {}, usable: false }") {
		t.Error("a non-ok / empty fetch must fall back to the previous good config, not clobber it with {}")
	}
	usable := functionBody(html, "function clientConfigIsUsable(config)")
	if usable == "" {
		t.Fatal("clientConfigIsUsable is missing")
	}
	for _, want := range []string{
		"config.rtcConfiguration?.iceServers",
		"config.protocolVersion != null",
	} {
		if !strings.Contains(usable, want) {
			t.Errorf("clientConfigIsUsable must key on iceServers or a server envelope marker, missing %q", want)
		}
	}
	// loadClientConfig must drop a failed attempt from the memo so a later restart
	// retries the refresh instead of reusing a poisoned {}.
	load := functionBody(html, "async function loadClientConfig()")
	if load == "" {
		t.Fatal("loadClientConfig is missing")
	}
	if !strings.Contains(load, "!result.usable && clientConfigPromise === pending") {
		t.Error("loadClientConfig must drop the memo on a failed refresh (only if still this attempt) so the next load retries")
	}

	// (c) performIceRestart must not setConfiguration a config lacking iceServers
	// when the live peer had them — that wipes STUN/TURN off a working peer.
	restart := functionBody(html, "function performIceRestart(reason, attempt)")
	if restart == "" {
		t.Fatal("performIceRestart is missing")
	}
	if !strings.Contains(restart, "rtcConfigurationHasIceServers(restartPeer.getConfiguration?.())") {
		t.Error("performIceRestart must check whether the live peer already has iceServers before re-applying config")
	}
	if !strings.Contains(restart, "if (!liveHasIceServers || rtcConfigurationHasIceServers(rtcConfiguration))") {
		t.Error("performIceRestart must guard setConfiguration so it never wipes live STUN/TURN with an empty config")
	}
}
