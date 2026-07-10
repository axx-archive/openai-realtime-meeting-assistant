package main

// frontend_artifact_deeplink_test.go — the "open in Intelligence" escape hatch
// (kanban-card-102). Opening a deliverable from the stage header, a
// notification row, or an assistant action routes through openAgentArtifact,
// which must (1) unfold the artifact library (#intelLibrary starts hidden
// behind "Open library") and scroll it into view, mirroring openPackageArtifact,
// and (2) survive an out-of-window deep-link: an id the newest-100 light list
// dropped is fetched by id and inserted before selecting, and loadArtifacts
// preserves that selected entry across its wholesale refresh.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForArtifactDeeplink(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func TestIndexOpenAgentArtifactDeepLink(t *testing.T) {
	html := readIndexForArtifactDeeplink(t)

	open := functionBody(html, "function openAgentArtifact(entry)")
	if open == "" {
		t.Fatal("index.html missing openAgentArtifact")
	}
	// FIX 1: the data-room escape hatch unfolds the library and brings it into
	// view — landing on the bare Mission Intelligence canvas with the fold
	// closed is the reported bug (the deliverable is invisible behind a toggle).
	for _, want := range []string{
		"setIntelLibraryOpen(true)",
		"scrollIntoView",
		"requestAnimationFrame",
		// the open beacon stays first (wave11_palette_test.go pins it too)
		"beaconArtifactOpen(entry.id)",
		// in-window ids select directly
		"selectArtifact(id)",
	} {
		if !strings.Contains(open, want) {
			t.Errorf("openAgentArtifact body missing %q", want)
		}
	}

	// FIX 2: an out-of-window id (absent from the newest-100 artifactEntries)
	// must be fetched by id before selecting — a bare selectArtifact is reset to
	// the newest by ensureSelectedArtifact.
	if !strings.Contains(open, "artifactEntries.some(candidate => candidate.id === id)") {
		t.Error("openAgentArtifact must branch on whether the id is in the loaded window")
	}
	if !strings.Contains(open, "fetchArtifactByIdAndSelect(id)") {
		t.Error("openAgentArtifact must fetch an out-of-window id by id before selecting")
	}

	// the by-id miss fetch itself: single-flight GET /artifacts?id=, insert via
	// addArtifactEntry, THEN select — mirroring fetchGoalArtifactById.
	fetchBody := functionBody(html, "function fetchArtifactByIdAndSelect(id)")
	if fetchBody == "" {
		t.Fatal("index.html missing fetchArtifactByIdAndSelect")
	}
	for _, want := range []string{
		"/artifacts?id=",
		"deepLinkArtifactFetchesInFlight",
		"addArtifactEntry(artifact, { select: false })",
		"selectArtifact(artifact.id)",
	} {
		if !strings.Contains(fetchBody, want) {
			t.Errorf("fetchArtifactByIdAndSelect body missing %q", want)
		}
	}

	// A landed fetch must not clobber a newer user pick (or a newer deep-link)
	// made while it was in flight: it selects only while it is still the active
	// deep-link and the selection hasn't moved since the call began.
	for _, want := range []string{
		"pendingDeepLinkArtifactId = want",
		"const selectedAtCall = selectedArtifactId",
		"pendingDeepLinkArtifactId === want && selectedArtifactId === selectedAtCall",
	} {
		if !strings.Contains(fetchBody, want) {
			t.Errorf("fetchArtifactByIdAndSelect must guard the selection against a newer pick, missing %q", want)
		}
	}
}

// The wholesale /artifacts refresh (loadArtifacts, also fired when the library
// unfolds) must not clobber a deliberately-selected out-of-window artifact —
// otherwise the deep-link insert races the reload and vanishes.
func TestIndexLoadArtifactsPreservesSelectedOutOfWindow(t *testing.T) {
	html := readIndexForArtifactDeeplink(t)
	body := functionBody(html, "async function loadArtifacts()")
	if body == "" {
		t.Fatal("index.html missing loadArtifacts")
	}
	if !strings.Contains(body, "selectedArtifactId && !fresh.some(entry => entry.id === selectedArtifactId)") {
		t.Error("loadArtifacts must detect a selected id dropped by the fresh window")
	}
	if !strings.Contains(body, "fresh.push(kept)") {
		t.Error("loadArtifacts must re-append the selected out-of-window artifact so the reader/deep-link survives the wholesale replace")
	}
	// the goalcard-upgrade wiring pin lives on this same body — keep it reachable
	if !strings.Contains(body, "syncScoutChatResearchCards()") {
		t.Error("loadArtifacts must still reach the goalcard upgrade (syncScoutChatResearchCards)")
	}
}

// FIX 3: the assistant open_tool-with-artifact and select_artifact actions
// route through the fixed opener for consistency, while keeping the pinned
// open beacon (wave11_palette_test.go) firing.
func TestIndexAssistantArtifactActionsUseDeepLink(t *testing.T) {
	html := readIndexForArtifactDeeplink(t)
	actions := functionBody(html, "function handleOSAssistantActions(actions)")
	if actions == "" {
		t.Fatal("index.html missing handleOSAssistantActions")
	}
	// both artifact-opening branches now go through openAgentArtifact
	if strings.Count(actions, "openAgentArtifact({ id: artifactId })") < 2 {
		t.Error("the open_tool-with-artifact and select_artifact branches must both route through openAgentArtifact")
	}
	// the pinned select_artifact open beacon still fires
	if !strings.Contains(actions, "beaconArtifactOpen(artifactId)") {
		t.Error("the select_artifact action must still fire the open beacon")
	}
	// the artifacts-tool deep-link is gated on an artifactId being present
	if !strings.Contains(actions, "tool === 'artifacts' && artifactId") {
		t.Error("open_tool must deep-link only when it targets the artifacts tool with an artifactId")
	}
}
