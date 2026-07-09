package main

import (
	"os"
	"strings"
	"testing"
)

// TestIndexMediaQualityReportCarriesClientVersion pins the staleness telemetry:
// the server's media-quality log line reads client.version from the payload
// (main.go logClientMediaQualityReport), so the client must actually send it —
// without it a stale tab is unprovable from prod logs.
func TestIndexMediaQualityReportCarriesClientVersion(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		"client: { version: buildVersionGuard.booted },",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html is missing %q", want)
		}
	}
}

// TestIndexParticipantPreviewIsRoomScoped pins the named-room roster poll: the
// 8s preview refresh must ask about the tab's OWN room. A bare /participants
// reconciles a named-room seat against the office roster and tears down every
// remote tile each cycle.
func TestIndexParticipantPreviewIsRoomScoped(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	want := "fetch(`/participants?room=${encodeURIComponent(activeJoin.roomId || 'office')}`, { cache: 'no-store' })"
	if !strings.Contains(html, want) {
		t.Fatalf("index.html is missing %q", want)
	}
	// The signed-out login-gate seat-count hint is the ONLY remaining bare
	// /participants caller; a second one means the preview poll regressed.
	if got := strings.Count(html, "fetch('/participants', { cache: 'no-store' })"); got != 1 {
		t.Fatalf("bare /participants fetches=%d, want exactly 1 (the login-gate presence hint)", got)
	}
}

// TestIndexJoinPathChecksBuildFreshness pins the join-time version gate: the
// office-socket build push can lose the wake→tap-join race, so the join path
// itself asks /healthz before seating, parks the join intent, and reloads on a
// mismatch. It must fail open (fetch error/timeout => join as today) and never
// touch the in-call deferral doctrine.
func TestIndexJoinPathChecksBuildFreshness(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		"async function joinBlockedByStaleBuild()",
		"const staleJoinIntentKey = 'bonfire.staleJoinIntent.v1'",
		"window.setTimeout(() => controller.abort(), 1500)",
		"fetch('/healthz', { cache: 'no-store', signal: controller.signal })",
		"if (await joinBlockedByStaleBuild()) {",
		"window.sessionStorage?.setItem(staleJoinIntentKey, activeJoin.roomId || 'office')",
		"window.sessionStorage.removeItem(staleJoinIntentKey)",
		// the in-call deferral seam stays intact
		"function flushPendingBuildReload()",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html is missing %q", want)
		}
	}
}

// TestIndexRepairLoopDampening pins the anti-flash guards: a repair pass must
// not force a srcObject re-attach (a visible blink) when the same track id is
// already attached and the element has rendered frames — recovery stays
// signaling-only — and the mute cover waits out 1-2s congestion gaps.
func TestIndexRepairLoopDampening(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		// rebind path: skip only when the attached same-id track is healthy —
		// an ended/muted attachment must still be re-attached (dead-track strand)
		"if (currentTracks.some(current => current.id === track.id && liveTrack(current) && !current.muted)",
		// frozen-video repair: keep the PLI/track-refresh, skip the rebuild
		"if (attachedTrack && attachedTrack.id === videoTrack.id",
		// mute cover raised 1600 -> 3000 so short congestion gaps stop blinking
		"remoteTileRemovers.get(tile)?.(track)\n            }\n          }, 3000)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html is missing %q", want)
		}
	}
}
