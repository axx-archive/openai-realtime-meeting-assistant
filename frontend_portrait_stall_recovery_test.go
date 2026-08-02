package main

import (
	"os"
	"strings"
	"testing"
)

func TestIndexPortraitContainmentSurvivesBackdropLifecycle(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		"html:not(.is-mobile-device) .video-tile video.has-portrait-frame",
		"html:not(.is-mobile-device) .hearth-speaker video.has-portrait-frame",
		"html:not(.is-mobile-device) .screen-stage__pip video.has-portrait-frame",
		"html:not(.is-mobile-device) .pip-tile video.has-portrait-frame",
		"html:not(.is-mobile-device) .board-video-tile video.has-portrait-frame",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("portrait containment still depends on backdrop composition: missing %q", want)
		}
	}
}

func TestIndexVisibilityRebasePreservesStalledTrackRecovery(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	rebase := functionBody(html, "function rebaseRemoteVideoFreezeSamples()")
	if rebase == "" {
		t.Fatal("rebaseRemoteVideoFreezeSamples is missing")
	}
	for _, forbidden := range []string{
		"remoteVideoFreezeStates.clear()",
		"frozenRemoteVideoTrackIds.clear()",
	} {
		if strings.Contains(rebase, forbidden) {
			t.Errorf("visibility rebase must preserve recovery identity, found %q", forbidden)
		}
	}
	for _, want := range []string{
		"for (const state of remoteVideoFreezeStates.values())",
		"state.lastAdvanceAt = now",
		"state.lastRepairAt = 0",
	} {
		if !strings.Contains(rebase, want) {
			t.Errorf("visibility rebase is missing %q", want)
		}
	}

	sample := functionBody(html, "function noteRemoteVideoFreezeSample(track, inbound, now)")
	advanceAt := strings.Index(sample, "if (framesAdvanced) {")
	deafAt := strings.Index(sample, "const deafTile = deafSubscriptionRemoteTile(track, framesDecoded)")
	if advanceAt == -1 || deafAt == -1 || advanceAt >= deafAt {
		t.Fatal("could not isolate decoded-frame recovery branch")
	}
	recovery := sample[advanceAt:deafAt]
	if !strings.Contains(recovery, "tile.classList.remove('is-video-stalled')") {
		t.Fatal("decoded-frame recovery must clear the stale avatar cover")
	}
	if strings.Contains(recovery, "!track.muted") {
		t.Fatal("decoded-frame recovery must not be blocked by a lagging track.muted flag")
	}
}
