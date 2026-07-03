package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// waitForOSEvent reads kanban envelopes on conn until it sees an os_event of
// the requested kind, then returns the decoded event. The office replay and
// other event kinds are skipped, so a producer that fires several os_events
// (e.g. a proposal that also creates a notification) is tolerated.
func waitForOSEvent(t *testing.T, conn *websocket.Conn, kind string, timeout time.Duration) osEvent {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("read websocket waiting for os_event/%s: %v", kind, err)
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
		if inner.Event != osEventName {
			continue
		}
		var event osEvent
		if err := json.Unmarshal(inner.Data, &event); err != nil {
			t.Fatalf("decode os_event payload: %v", err)
		}
		if event.Kind == kind {
			return event
		}
	}
}

// drainOfficeReplay consumes the ordered admission replay so the reads that
// follow observe only newly produced events. codex_proposals is the last event
// in the replay set (see the office_socket_test.go admission test).
func drainOfficeReplay(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	waitForKanbanEvent(t, conn, "codex_proposals", 5*time.Second)
}

// TestUnifiedPushChannelTwoSessionAcceptance is the wave's gate: a change made
// through one authenticated session's action lands on a second authenticated
// session's office socket — with no room join by either — within 2 seconds.
// This is what "finished work is visible to everyone instantly" means.
func TestUnifiedPushChannelTwoSessionAcceptance(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	// Session A (the actor) and session B (the observer). Both are signed in
	// on office sockets only; neither takes a room seat.
	sessionA := dialIsolatedWebsocket(t, server, "aj@shareability.com")
	sessionB := dialIsolatedWebsocket(t, server, "tim@shareability.com")
	sendOfficeHello(t, sessionA)
	sendOfficeHello(t, sessionB)
	drainOfficeReplay(t, sessionA)
	drainOfficeReplay(t, sessionB)

	// Session A's action: propose a task. Represents a real signed-in action;
	// it never joins the room.
	result, _, err := kanbanApp.proposeCodexTask(map[string]any{
		"title": "Nimbus comp set",
		"mode":  "research",
		"query": "pull three comparable creator-economy raises",
	}, "aj@shareability.com")
	if err != nil {
		t.Fatalf("session A proposeCodexTask: %v", err)
	}
	ref, _ := result["proposal"].(map[string]any)["id"].(string)
	if strings.TrimSpace(ref) == "" {
		t.Fatalf("expected a proposal id from session A's action, got %v", result)
	}

	// Session B sees it on its own socket within 2s, no room join.
	event := waitForOSEvent(t, sessionB, osEventProposal, 2*time.Second)
	if event.Ref != ref {
		t.Fatalf("session B got the wrong proposal ref: got %q want %q", event.Ref, ref)
	}
	if !strings.Contains(event.Title, "Nimbus comp set") {
		t.Fatalf("session B os_event carried the wrong title: %q", event.Title)
	}

	// Neither session admitted into the room: the channel is auth-scoped, not
	// room-scoped.
	snapshot := kanbanApp.roomSnapshot()
	if occupied, ok := snapshot["occupiedSeats"].(int); !ok || occupied != 0 {
		t.Fatalf("expected zero occupied seats, got %v", snapshot["occupiedSeats"])
	}
}

// TestOSEventArtifactProgressThenCompleted proves the artifact producer emits
// progress on a worker scaffold and completed on the terminal update, and that
// a bookkeeping re-write with unchanged user-visible state is deduped (no
// second completed event).
func TestOSEventArtifactProgressThenCompleted(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	conn := dialIsolatedWebsocket(t, server, "aj@shareability.com")
	sendOfficeHello(t, conn)
	drainOfficeReplay(t, conn)

	// A worker scaffold (latestThreadRun marks it in-flight) → progress.
	artifact, appended, err := kanbanApp.createOSArtifactWithMetadata(
		"research", "creator market", "Working…", "AJ",
		map[string]string{"latestThreadRun": "thread-os-1", "title": "Creator market scan"},
	)
	if err != nil || !appended {
		t.Fatalf("create scaffold artifact: appended=%v err=%v", appended, err)
	}
	progress := waitForOSEvent(t, conn, osEventArtifactProgress, 5*time.Second)
	if progress.Ref != artifact.ID {
		t.Fatalf("progress event ref=%q want %q", progress.Ref, artifact.ID)
	}
	if strings.Contains(strings.ToLower(progress.Title), "working") {
		t.Fatalf("progress title must be the artifact title, not the body: %q", progress.Title)
	}

	// Two execute-phase ticks mirroring goal_engine.go persist(): goalStatus
	// and status hold steady while progressPercent/currentStage climb. Both
	// must fan out — the second is the regression guard: if the dedup
	// signature ignored progressPercent, this tick would be swallowed and the
	// live stage rail would freeze.
	for _, pct := range []string{"40", "60"} {
		if _, changed, err := kanbanApp.updateOSArtifactWithMetadata(
			artifact.ID, "Creator market scan", "Partial scan, still running.", "AJ",
			map[string]string{"goalStatus": "running", "currentStage": "execute_in_order", "progressPercent": pct},
		); err != nil || !changed {
			t.Fatalf("progress tick %s: changed=%v err=%v", pct, changed, err)
		}
		tick := waitForOSEvent(t, conn, osEventArtifactProgress, 5*time.Second)
		if tick.Ref != artifact.ID {
			t.Fatalf("progress tick %s ref=%q want %q", pct, tick.Ref, artifact.ID)
		}
	}

	// The terminal update → completed.
	if _, changed, err := kanbanApp.updateOSArtifactWithMetadata(
		artifact.ID, "Creator market scan", "The full scan body with receipts.", "AJ",
		map[string]string{"status": "complete"},
	); err != nil || !changed {
		t.Fatalf("complete artifact: changed=%v err=%v", changed, err)
	}
	completed := waitForOSEvent(t, conn, osEventArtifactCompleted, 5*time.Second)
	if completed.Ref != artifact.ID {
		t.Fatalf("completed event ref=%q want %q", completed.Ref, artifact.ID)
	}

	// A deliveredAt stamp leaves status/title unchanged → must NOT re-emit.
	if _, _, err := kanbanApp.updateOSArtifactWithMetadata(
		artifact.ID, "Creator market scan", "The full scan body with receipts.", "AJ",
		map[string]string{"deliveredAt": time.Now().UTC().Format(time.RFC3339Nano)},
	); err != nil {
		t.Fatalf("stamp deliveredAt: %v", err)
	}
	// A distinct marker bounds the negative check: the next os_event on this
	// socket must be the marker, never a second artifact_completed.
	broadcastOSEvent(osEvent{Kind: osEventProposal, Ref: "marker-after-delivery", Title: "marker"})
	marker := waitForOSEvent(t, conn, osEventProposal, 5*time.Second)
	if marker.Ref != "marker-after-delivery" {
		t.Fatalf("expected the marker after the deliveredAt stamp, got ref %q — a bookkeeping re-write leaked an event", marker.Ref)
	}
}

// TestOSEventNotificationFanout proves a broadcast notification produces a
// notification os_event on an office socket that never joined the room.
func TestOSEventNotificationFanout(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	conn := dialIsolatedWebsocket(t, server, "aj@shareability.com")
	sendOfficeHello(t, conn)
	drainOfficeReplay(t, conn)

	record, err := kanbanApp.createNotification("", notificationKindInfo, "overnight results ready", "room", "", "", false)
	if err != nil {
		t.Fatalf("create notification: %v", err)
	}
	event := waitForOSEvent(t, conn, osEventNotification, 5*time.Second)
	if event.Ref != record.ID {
		t.Fatalf("notification os_event ref=%q want %q", event.Ref, record.ID)
	}
}

// TestOSEventChannelPostCarriesTitleOnly proves a channel post fans out a
// channel_post os_event whose title is the channel name — never the message
// body (the trust boundary).
func TestOSEventChannelPostCarriesTitleOnly(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	conn := dialIsolatedWebsocket(t, server, "aj@shareability.com")
	sendOfficeHello(t, conn)
	drainOfficeReplay(t, conn)

	if _, _, err := kanbanApp.createChannelByVoice(map[string]any{"name": "dealflow"}, "aj@shareability.com"); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	const secretBody = "the confidential deal terms nobody outside chat should see"
	if _, _, err := kanbanApp.postToChannel(map[string]any{"channel": "dealflow", "text": secretBody}, "aj@shareability.com"); err != nil {
		t.Fatalf("post to channel: %v", err)
	}

	// One postToChannel fires BOTH a channel_post event and a chat notification
	// event (the bell nudge, whose record.Text embeds the message body). Scan
	// every OS event up to and including the channel_post and assert none of
	// them — notification kind included — carries the body. Guards the leak the
	// prior version missed by only inspecting the channel_post kind.
	var channelPost osEvent
	sawChannelPost := false
	deadline := time.Now().Add(5 * time.Second)
	for !sawChannelPost {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("read websocket while scanning os_events: %v", err)
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
		if inner.Event != osEventName {
			continue
		}
		var event osEvent
		if err := json.Unmarshal(inner.Data, &event); err != nil {
			t.Fatalf("decode os_event payload: %v", err)
		}
		if strings.Contains(event.Title, secretBody) || strings.Contains(event.Title, "confidential") {
			t.Fatalf("os_event kind %q leaked the message body across the trust boundary: %+v", event.Kind, event)
		}
		if event.Kind == osEventChannelPost {
			channelPost = event
			sawChannelPost = true
		}
	}
	if !strings.Contains(channelPost.Title, "dealflow") {
		t.Fatalf("channel_post title should name the channel, got %q", channelPost.Title)
	}
}

// TestOSEventPackageAdvanced proves a stage advance fans out a package_advanced
// os_event referencing the package.
func TestOSEventPackageAdvanced(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	conn := dialIsolatedWebsocket(t, server, "aj@shareability.com")
	sendOfficeHello(t, conn)
	drainOfficeReplay(t, conn)

	pkg, err := kanbanApp.createVenturePackage("Nimbus", "creator-economy rails", "AJ")
	if err != nil {
		t.Fatalf("create package: %v", err)
	}
	if _, err := kanbanApp.advancePackageStage(pkg.ID, "", "AJ"); err != nil {
		t.Fatalf("advance package: %v", err)
	}
	event := waitForOSEvent(t, conn, osEventPackageAdvanced, 5*time.Second)
	if event.Ref != pkg.ID {
		t.Fatalf("package_advanced ref=%q want %q", event.Ref, pkg.ID)
	}
	if !strings.Contains(event.Title, "Nimbus") {
		t.Fatalf("package_advanced title should name the package, got %q", event.Title)
	}
}

// TestOSEventClassifierUnit exercises the kind classification directly across
// the metadata shapes the producers hand it.
func TestOSEventClassifierUnit(t *testing.T) {
	cases := []struct {
		name     string
		metadata map[string]string
		want     string
	}{
		{"published", map[string]string{"published": "true"}, osEventArtifactCompleted},
		{"status complete", map[string]string{"status": "complete"}, osEventArtifactCompleted},
		{"terminal goal", map[string]string{"goalStatus": "verified"}, osEventArtifactCompleted},
		{"worker scaffold", map[string]string{"latestThreadRun": "t1", "status": "draft"}, osEventArtifactProgress},
		{"non-terminal goal", map[string]string{"workflow": "codex_goal_loop", "goalStatus": "scaffolded"}, osEventArtifactProgress},
		{"direct save", map[string]string{"status": "draft"}, osEventArtifactCompleted},
	}
	for _, tc := range cases {
		if got := osArtifactEventKind(tc.metadata); got != tc.want {
			t.Errorf("%s: osArtifactEventKind=%q want %q", tc.name, got, tc.want)
		}
	}
}

// TestIndexUnifiedPushChannelConsumer pins the frontend consumer router: one
// router by kind, idempotent by (kind, ref, at), light + rich consumer classes,
// the brief counters, and the documented osEventHandlers extension point.
func TestIndexUnifiedPushChannelConsumer(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	for _, want := range []string{
		// dispatch case on the office socket
		"case 'os_event':",
		"osEventsRoute(message.data)",
		// the router + idempotence key
		"function osEventsRoute(event)",
		"function osEventsKey(event)",
		"if (osEventsSeen.has(key)) {",
		// brief counters (light) + extension point
		"const osEventsBriefCounts = {",
		"const osEventHandlers = []",
		"osEventsDispatchHandlers(event)",
		// rich consumers fetch-by-ref, coalesced so a burst is one fetch
		"function osEventsRefetchPackages()",
		"function osEventsRefetchBoard()",
		"loadPackages(true)",
		"const osEventsRefetchDebounceMs = 500",
		"osEventsPackagesTimer = setTimeout(",
		"osEventsBoardTimer = setTimeout(",
		// every kind is routed
		"case 'artifact_completed':",
		"case 'artifact_progress':",
		"case 'package_advanced':",
		"case 'proposal':",
		"case 'notification':",
		"case 'channel_post':",
		"case 'quarantine_change':",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing unified push channel consumer marker %q", want)
		}
	}
}
