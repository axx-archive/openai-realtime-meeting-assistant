package main

// Multi-room W7 (§6.7): the exhaustive route-walk allowlist test, built from
// the ACTUAL mux registrations in main.go — every registered route is probed
// with a minted guest session and must fail closed unless it appears on the
// explicit §5.3 allowlist. A route added later without a row here turns the
// suite red until its author makes a conscious allowlist decision. Also in
// this file: the §6.2 fan-out leak sweep over real recorded sockets and the
// §6.1 DoS-cap battery under concurrency.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// registeredHTTPRoutes parses main.go's http.HandleFunc registrations — the
// same source-of-truth pinning idiom the frontend TestIndex* tests use. The
// one inline registration ("/", the SPA shell closure) is detected separately.
func registeredHTTPRoutes(t *testing.T) map[string]string {
	t.Helper()
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	pattern := regexp.MustCompile(`http\.HandleFunc\("([^"]+)",\s*([A-Za-z0-9_]+)\)`)
	routes := map[string]string{}
	for _, match := range pattern.FindAllStringSubmatch(string(source), -1) {
		routes[match[1]] = match[2]
	}
	if strings.Contains(string(source), `http.HandleFunc("/", func(`) {
		routes["/"] = "(inline index closure)"
	}
	if len(routes) < 40 {
		t.Fatalf("route parse looks broken: found only %d registrations", len(routes))
	}
	return routes
}

// guestRouteProbe is one route's conscious allowlist decision. A nil handler
// documents a route the walk cannot invoke directly (the inline SPA closure);
// everything else is executed with the guest session planted in BOTH cookie
// slots.
type guestRouteProbe struct {
	handler http.HandlerFunc
	// memberGated probes GET and POST and requires every answer in
	// {401,403,405} with at least one hard auth rejection — the fail-closed
	// contract for the ~45 member routes.
	memberGated bool
	// method/path/body + allowed run one explicit probe instead (the guest
	// allowlist and the token-gated public endpoints, which must keep their
	// EXISTING scoping — never a member gate, never a broadening).
	method  string
	path    string
	body    string
	allowed []int
	check   func(t *testing.T, rec *httptest.ResponseRecorder)
}

func TestGuestRouteWalkAllowlistFailsClosed(t *testing.T) {
	setupRoomsTestEnv(t)
	resetGuestSocketCapsForTest(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	_, linkToken := mintGuestRoomAndToken(t)
	joined := postGuestJoin(t, fmt.Sprintf(`{"token":%q,"name":"Priya"}`, linkToken))
	if joined.Code != http.StatusOK {
		t.Fatalf("guest join = %d body %s", joined.Code, joined.Body.String())
	}
	guestCookie := guestCookieFrom(t, joined)
	resetAuthRateLimitersForTest()

	deadToken := strings.Repeat("0", 64)
	// The §5.3 allowlist + the member-gated remainder. EVERY route registered
	// in main.go must have a row; every row must still be registered.
	probes := map[string]guestRouteProbe{
		// ---- public statics + health (§5.3 "already-public statics")
		"/":        {handler: nil}, // inline SPA closure: static shell bytes, no data; guests boot from it
		"/healthz": {handler: healthHandler, method: http.MethodGet, path: "/healthz", allowed: []int{http.StatusOK}},
		"/readyz":  {handler: readinessHandler, method: http.MethodGet, path: "/readyz", allowed: []int{http.StatusOK, http.StatusServiceUnavailable}},
		"/sw.js":   {handler: serviceWorkerHandler, method: http.MethodGet, path: "/sw.js", allowed: []int{http.StatusOK}},
		"/public/": {handler: publicAssetHandler, method: http.MethodGet, path: "/public/route-walk-not-a-file.js", allowed: []int{http.StatusNotFound}},

		// ---- the guest surface (§5.3 allowlist)
		"/g": {handler: guestPageHandler, method: http.MethodGet, path: "/g", allowed: []int{http.StatusOK},
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				if rec.Header().Get("Referrer-Policy") != "no-referrer" || rec.Header().Get("Cache-Control") != "no-store" {
					t.Errorf("/g must keep its token-secrecy headers, got %v", rec.Header())
				}
			}},
		"/g/": {handler: guestPageHandler, method: http.MethodGet, path: "/g/" + deadToken, allowed: []int{http.StatusFound},
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				if rec.Header().Get("Location") != "/g#"+deadToken {
					t.Errorf("path shim must 302 to the fragment form, got %q", rec.Header().Get("Location"))
				}
			}},
		"/guest/join": {handler: guestJoinHandler, method: http.MethodPost, path: "/guest/join",
			body: fmt.Sprintf(`{"token":%q,"name":"Priya"}`, deadToken), allowed: []int{http.StatusForbidden}},
		"/guest/me": {handler: guestMeHandler, method: http.MethodGet, path: "/guest/me", allowed: []int{http.StatusOK}},
		// The guest principal passes the pre-upgrade gate (room forced from
		// the session); only the missing websocket handshake fails, proving
		// the route is on the allowlist without opening a socket.
		"/websocket": {handler: websocketHandler, method: http.MethodGet, path: "/websocket", allowed: []int{http.StatusBadRequest}},

		// ---- signed-out-shaped public presence (never the roster)
		"/participants": {handler: participantsHandler, method: http.MethodGet, path: "/participants", allowed: []int{http.StatusOK},
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				var summary map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
					t.Errorf("presence summary decode: %v", err)
					return
				}
				if _, ok := summary["participants"]; ok {
					t.Errorf("guest must get the seat count only, got %s", rec.Body.String())
				}
			}},

		// ---- token/HMAC-gated public endpoints: existing scoping, unchanged (§6.7)
		"/a/":               {handler: shareLinkPublicHandler, method: http.MethodGet, path: "/a/" + deadToken, allowed: []int{http.StatusNotFound}},
		"/deal-room/":       {handler: dealRoomPublicHandler, method: http.MethodGet, path: "/deal-room/" + deadToken, allowed: []int{http.StatusNotFound}},
		"/archives/":        {handler: meetingArchiveHandler, method: http.MethodGet, path: "/archives/meeting-20260709-000000", allowed: []int{http.StatusUnauthorized}},
		"/artifacts/render": {handler: artifactRenderHandler, method: http.MethodGet, path: "/artifacts/render?id=art-1&t=bogus", allowed: []int{http.StatusForbidden}},

		// ---- everything else: member-gated, guest fails closed
		"/auth/":                         {handler: authHandler, memberGated: true, path: "/auth/me"},
		"/assistant/query":               {handler: assistantQueryHandler, memberGated: true},
		"/assistant/chat-threads":        {handler: assistantChatThreadsHandler, memberGated: true},
		"/assistant/chat-threads/":       {handler: assistantChatThreadHandler, memberGated: true, path: "/assistant/chat-threads/thread-1"},
		"/assistant/attachments":         {handler: assistantAttachmentUploadHandler, memberGated: true},
		"/assistant/threads":             {handler: assistantThreadsHandler, memberGated: true},
		"/assistant/threads/follow-up":   {handler: assistantThreadFollowUpHandler, memberGated: true},
		"/assistant/goal":                {handler: assistantGoalHandler, memberGated: true},
		"/assistant/goal/cancel":         {handler: assistantGoalCancelHandler, memberGated: true},
		"/assistant/decisions/supersede": {handler: assistantDecisionSupersedeHandler, memberGated: true},
		"/assistant/decisions/ratify":    {handler: assistantDecisionRatifyHandler, memberGated: true},
		"/assistant/tools":               {handler: assistantToolsHandler, memberGated: true},
		"/assistant/notifications":       {handler: assistantNotificationsHandler, memberGated: true},
		"/assistant/notifications/read":  {handler: assistantNotificationsReadHandler, memberGated: true},
		"/assistant/push/config":         {handler: assistantPushConfigHandler, memberGated: true},
		"/assistant/push/subscribe":      {handler: assistantPushSubscribeHandler, memberGated: true},
		"/assistant/push/unsubscribe":    {handler: assistantPushUnsubscribeHandler, memberGated: true},
		"/assistant/push/prefs":          {handler: assistantPushPrefsHandler, memberGated: true},
		"/assistant/board":               {handler: assistantBoardHandler, memberGated: true},
		"/assistant/board/drafts/":       {handler: assistantBoardDraftActionHandler, memberGated: true, path: "/assistant/board/drafts/draft-1"},
		"/assistant/memory":              {handler: assistantMemoryHandler, memberGated: true},
		"/assistant/files":               {handler: assistantFilesHandler, memberGated: true},
		"/assistant/files/upload":        {handler: assistantFileUploadHandler, memberGated: true},
		"/assistant/files/folders":       {handler: assistantFileFoldersHandler, memberGated: true},
		"/assistant/files/move":          {handler: assistantFileMoveHandler, memberGated: true},
		"/assistant/meetings":            {handler: assistantMeetingsHandler, memberGated: true},
		"/assistant/mission":             {handler: assistantMissionHandler, memberGated: true},
		"/assistant/mission/refresh":     {handler: assistantMissionRefreshHandler, memberGated: true},
		"/assistant/proposals/":          {handler: assistantProposalActionHandler, memberGated: true, path: "/assistant/proposals/prop-1/approve"},
		"/assistant/quarantine":          {handler: assistantQuarantineHandler, memberGated: true},
		"/assistant/quarantine/":         {handler: assistantQuarantineActionHandler, memberGated: true, path: "/assistant/quarantine/q-1/restore"},
		"/assistant/packages":            {handler: assistantPackagesHandler, memberGated: true},
		"/assistant/packages/":           {handler: assistantPackageActionHandler, memberGated: true, path: "/assistant/packages/pkg-1/approve"},
		"/assistant/deal-room/request":   {handler: assistantDealRoomRequestHandler, memberGated: true},
		"/assistant/deal-room/resolve":   {handler: assistantDealRoomResolveHandler, memberGated: true},
		"/assistant/deal-room/revoke":    {handler: assistantDealRoomRevokeHandler, memberGated: true},
		"/assistant/deal-room/list":      {handler: assistantDealRoomListHandler, memberGated: true},
		"/assistant/brief":               {handler: assistantBriefHandler, memberGated: true},
		"/assistant/portfolio":           {handler: assistantPortfolioHandler, memberGated: true},
		"/assistant/realtime-offer":      {handler: assistantRealtimeOfferHandler, memberGated: true},
		"/assistant/realtime-tool":       {handler: assistantRealtimeToolHandler, memberGated: true},
		"/internal/codex/jobs/result":    {handler: internalCodexRunnerResultHandler, memberGated: true},
		"/internal/render/jobs/result":   {handler: internalRenderRunnerResultHandler, memberGated: true},
		"/artifacts":                     {handler: artifactsHandler, memberGated: true},
		"/artifacts/action":              {handler: artifactRunnerActionHandler, memberGated: true},
		"/artifacts/open":                {handler: artifactOpenHandler, memberGated: true},
		"/artifacts/render-token":        {handler: artifactRenderTokenHandler, memberGated: true},
		"/artifacts/blob":                {handler: artifactBlobHandler, memberGated: true},
		"/artifacts/share":               {handler: artifactShareHandler, memberGated: true},
		"/artifacts/export-pdf":          {handler: artifactExportPDFHandler, memberGated: true},
		"/calendar/event.ics":            {handler: calendarICSHandler, memberGated: true},
		"/signals/survey":                {handler: signalSurveyHandler, memberGated: true},
		"/client-config":                 {handler: clientConfigHandler, memberGated: true},
		"/native/config":                 {handler: nativeClientConfigHandler, memberGated: true},
		"/rooms":                         {handler: roomsHandler, memberGated: true},
		"/rooms/":                        {handler: roomActionHandler, memberGated: true, path: "/rooms/office/archive"},
		"/ice-test":                      {handler: iceTestHandler, memberGated: true},
	}

	// ---- fail closed in BOTH directions.
	routes := registeredHTTPRoutes(t)
	for route := range routes {
		if _, ok := probes[route]; !ok {
			t.Errorf("route %q is registered in main.go with NO guest-allowlist decision — add a probe row (member-gated unless §5.3 says otherwise)", route)
		}
	}
	for route := range probes {
		if _, ok := routes[route]; !ok {
			t.Errorf("probe row %q no longer matches any main.go registration — remove or fix it", route)
		}
	}

	send := func(handler http.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		// The guest session rides BOTH cookie slots at once: neither resolver
		// may honor it on a member route.
		req.AddCookie(guestCookie)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: guestCookie.Value})
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}

	for route, probe := range probes {
		if probe.handler == nil {
			continue
		}
		path := probe.path
		if path == "" {
			path = route
		}
		if probe.memberGated {
			hardRejected := false
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				rec := send(probe.handler, method, path, probe.body)
				switch rec.Code {
				case http.StatusUnauthorized, http.StatusForbidden:
					hardRejected = true
				case http.StatusMethodNotAllowed, http.StatusNotFound:
					// acceptable for the probe's unsupported method (the
					// auth gate may sit behind a method/subpath dispatch —
					// authHandler 404s a POST /auth/me); the hardRejected
					// requirement below still forces a real 401/403.
				default:
					t.Errorf("%s %s: guest session got %d, want 401/403 (or 404/405 on the unsupported method)", method, route, rec.Code)
				}
			}
			if !hardRejected {
				t.Errorf("%s: no method produced a hard 401/403 for the guest session — the member gate is missing", route)
			}
			continue
		}
		rec := send(probe.handler, probe.method, path, probe.body)
		allowed := false
		for _, code := range probe.allowed {
			if rec.Code == code {
				allowed = true
			}
		}
		if !allowed {
			t.Errorf("%s %s: got %d, want one of %v (existing scoping must not change)", probe.method, route, rec.Code, probe.allowed)
			continue
		}
		if probe.check != nil {
			probe.check(t, rec)
		}
	}
}

/* ---------- §6.2 fan-out leak sweep over real recorded sockets ---------- */

// admitGuestSocket dials, sends the participant hello, and drains until the
// grant, returning the open connection.
func admitGuestSocket(t *testing.T, server *httptest.Server, guestToken string) *websocket.Conn {
	t.Helper()
	conn, _, err := dialGuestWebsocket(t, server, guestToken)
	if err != nil {
		t.Fatalf("guest dial: %v", err)
	}
	if err := conn.WriteJSON(map[string]string{"event": "participant", "data": `{}`}); err != nil {
		t.Fatalf("guest hello: %v", err)
	}
	waitForKanbanEvent(t, conn, "access_granted", 5*time.Second)
	return conn
}

// TestGuestFanOutLeakSweepAcrossBroadcastSeams drives the REAL broadcast
// seams (memory/artifact snapshots, notifications, proposals, shutdown) plus
// deliberately mis-routed room-pool frames against a live admitted guest
// socket and a member office socket. The member sees every marker; the guest
// socket — recorded end to end — receives allowlisted events only and never a
// marker byte (§6.2's belt-and-suspenders on the wire, not just at the unit
// seam).
func TestGuestFanOutLeakSweepAcrossBroadcastSeams(t *testing.T) {
	resetGuestSocketCapsForTest(t)
	resetAuthRateLimitersForTest()
	server := newIsolatedWebsocketServer(t)

	memberConn := dialIsolatedWebsocket(t, server, "aj@shareability.com")
	sendOfficeHello(t, memberConn)

	roomID, guestToken := mintGuestRoomAndSession(t, "Sam")
	guestConn := admitGuestSocket(t, server, guestToken)

	// Enter the media fan-out pool (the pool §6.2 protects) and wait for the
	// registration to land before firing any broadcast.
	if err := guestConn.WriteJSON(map[string]string{"event": "media_ready", "data": `{}`}); err != nil {
		t.Fatalf("guest media_ready: %v", err)
	}
	registration := time.Now().Add(5 * time.Second)
	for {
		pooled := false
		listLock.RLock()
		for _, state := range peerConnections {
			if state.websocket != nil && state.websocket.guest && normalizeRoomID(state.roomID) == roomID {
				pooled = true
			}
		}
		listLock.RUnlock()
		if pooled {
			break
		}
		if time.Now().After(registration) {
			t.Fatal("guest socket never entered the room fan-out pool")
		}
		time.Sleep(10 * time.Millisecond)
	}

	droppedBefore := guestEventsDropped.Load()

	// ---- the real seams, each with a marker that must never reach the guest.
	broadcastSignedInKanbanEvent("memory", map[string]any{"marker": "LEAK-artifact-os-event"})
	if _, err := kanbanApp.createNotification("", notificationKindInfo, "LEAK-notification", "room", "", "", false); err != nil {
		t.Fatalf("create notification: %v", err)
	}
	broadcastOfficeKanbanEvent("codex_proposal", map[string]any{"marker": "LEAK-proposal"})
	broadcastServerShutdown(2500) // the deploy-refresh/restart seam ("LEAK"-free payload, non-allowlisted event)

	// ---- deliberate mis-routes INTO the guest's own room pool: these reach
	// the guest writer itself and must be dropped at write time.
	for _, misrouted := range []string{"board", "memory", "notification_backlog", "codex_proposals", "server_shutdown"} {
		broadcastRoomKanbanEvent(roomID, misrouted, map[string]any{"marker": "LEAK-misrouted-" + misrouted})
	}
	// Ordered-delivery sentinel: once the guest sees this allowlisted frame,
	// every earlier frame for its socket was either delivered or dropped.
	broadcastRoomKanbanEvent(roomID, "meeting", map[string]any{"sentinel": "leak-sweep-done"})

	// The member office socket proves each real seam actually fired.
	memberSaw := map[string]bool{}
	for !memberSaw["LEAK-artifact-os-event"] || !memberSaw["LEAK-notification"] || !memberSaw["LEAK-proposal"] || !memberSaw["server_shutdown"] {
		if err := memberConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatalf("member read deadline: %v", err)
		}
		var message websocketMessage
		if err := memberConn.ReadJSON(&message); err != nil {
			t.Fatalf("member read (saw %v): %v", memberSaw, err)
		}
		for _, marker := range []string{"LEAK-artifact-os-event", "LEAK-notification", "LEAK-proposal", "server_shutdown"} {
			if strings.Contains(message.Data, marker) {
				memberSaw[marker] = true
			}
		}
	}

	// Drain the guest socket to the sentinel: allowlisted events only, zero
	// marker bytes.
	for {
		if err := guestConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatalf("guest read deadline: %v", err)
		}
		var message websocketMessage
		if err := guestConn.ReadJSON(&message); err != nil {
			t.Fatalf("guest read before the sentinel arrived: %v", err)
		}
		if strings.Contains(message.Data, "LEAK-") {
			t.Fatalf("marker leaked to the guest socket: %s", message.Data)
		}
		if message.Event != "kanban" {
			continue
		}
		var inner struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(message.Data), &inner); err != nil {
			t.Fatalf("decode guest kanban envelope: %v", err)
		}
		if !guestWritableKanbanEvents[inner.Event] {
			t.Fatalf("guest socket received non-allowlisted event %q", inner.Event)
		}
		if inner.Event == "meeting" && strings.Contains(string(inner.Data), "leak-sweep-done") {
			break
		}
	}
	if dropped := guestEventsDropped.Load() - droppedBefore; dropped < 5 {
		t.Fatalf("write-time allowlist counted %d drops, want at least the 5 mis-routes", dropped)
	}
}

/* ---------- §6.1 DoS-cap battery under concurrency ---------- */

// stormGuestDials fires count concurrent dials and returns
// (connected, rejected429); any other outcome fails the test.
func stormGuestDials(t *testing.T, server *httptest.Server, tokens []string) (int64, int64) {
	t.Helper()
	var connected, rejected atomic.Int64
	var wg sync.WaitGroup
	for _, token := range tokens {
		wg.Add(1)
		go func(token string) {
			defer wg.Done()
			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/websocket"
			header := http.Header{}
			header.Set("Cookie", guestCookieName+"="+token)
			conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
			if err == nil {
				connected.Add(1)
				t.Cleanup(func() { _ = conn.Close() })
				return
			}
			if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
				rejected.Add(1)
				return
			}
			t.Errorf("storm dial failed with neither an upgrade nor a 429: err=%v resp=%+v", err, resp)
		}(token)
	}
	wg.Wait()
	return connected.Load(), rejected.Load()
}

func TestGuestCapsBatteryUnderConcurrency(t *testing.T) {
	resetGuestSocketCapsForTest(t)
	resetAuthRateLimitersForTest()
	t.Setenv("BONFIRE_MAX_GUESTS_PER_ROOM", "2")
	server := newIsolatedWebsocketServer(t)
	roomID, sessionToken := mintGuestRoomAndSession(t, "Sam")

	// ---- per-session cap: a concurrent storm on ONE session never exceeds
	// maxGuestSocketsPerSession, and every overflow is a pre-upgrade 429.
	storm := make([]string, maxGuestSocketsPerSession+6)
	for i := range storm {
		storm[i] = sessionToken
	}
	connected, rejected := stormGuestDials(t, server, storm)
	if connected != int64(maxGuestSocketsPerSession) || rejected != 6 {
		t.Fatalf("session-cap storm: connected=%d rejected=%d, want %d/%d", connected, rejected, maxGuestSocketsPerSession, 6)
	}

	// ---- per-IP pre-hello cap: distinct unadmitted sessions from one IP
	// never exceed maxGuestPreHelloPerIP under concurrency. (The two sockets
	// above already hold pre-hello slots; a fresh registry isolates the runs.)
	resetGuestSocketCapsForTest(t)
	perIP := make([]string, maxGuestPreHelloPerIP+4)
	for i := range perIP {
		token, err := userSessionStore().createGuest(roomID, "Sam")
		if err != nil {
			t.Fatalf("create storm guest session %d: %v", i, err)
		}
		perIP[i] = token
	}
	connected, rejected = stormGuestDials(t, server, perIP)
	if connected != int64(maxGuestPreHelloPerIP) || rejected != 4 {
		t.Fatalf("per-IP storm: connected=%d rejected=%d, want %d/%d", connected, rejected, maxGuestPreHelloPerIP, 4)
	}

	// ---- room seat cap: with the room full (2 admitted), a concurrent storm
	// of fresh sessions is rejected pre-upgrade and no extra seat ever
	// appears. 3 dials stay under the per-IP pre-hello budget, so every 429
	// is attributable to the seat cap.
	resetGuestSocketCapsForTest(t)
	for i := 0; i < 2; i++ {
		token, err := userSessionStore().createGuest(roomID, fmt.Sprintf("Seat%d", i))
		if err != nil {
			t.Fatalf("create seated guest session %d: %v", i, err)
		}
		admitGuestSocket(t, server, token)
	}
	if seats := kanbanApp.activeParticipantCount(roomID); seats != 2 {
		t.Fatalf("fixture: room seats=%d, want 2", seats)
	}
	overflow := make([]string, 3)
	for i := range overflow {
		token, err := userSessionStore().createGuest(roomID, "Late")
		if err != nil {
			t.Fatalf("create overflow guest session %d: %v", i, err)
		}
		overflow[i] = token
	}
	connected, rejected = stormGuestDials(t, server, overflow)
	if connected != 0 || rejected != 3 {
		t.Fatalf("seat-cap storm: connected=%d rejected=%d, want 0/3", connected, rejected)
	}
	if seats := kanbanApp.activeParticipantCount(roomID); seats != 2 {
		t.Fatalf("seat-cap storm changed occupancy: seats=%d, want 2", seats)
	}
}
