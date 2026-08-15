package main

import (
	"os"
	"strings"
	"testing"
)

func TestIPadChatRecoversFromKeyboardReducedViewport(t *testing.T) {
	htmlBytes, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(htmlBytes)
	for _, want := range []string{
		"height: calc(var(--app-viewport-height, 100dvh) - var(--shell-topbar-height, 0px));",
		"window.visualViewport?.addEventListener('resize', settleAppViewportHeight",
		"document.addEventListener('focusout', settleAppViewportHeight)",
		"[0, 80, 240, 520].map",
		"document.documentElement.style.setProperty('--app-viewport-height'",
		"Math.abs(height - appViewportLastHeight) < 2",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("iPad keyboard viewport recovery missing %q", want)
		}
	}
	if strings.Contains(html, "visualViewport?.addEventListener('scroll', syncAppViewportHeight") {
		t.Fatal("iPad Safari viewport scroll must not feed nested channel scrolling back into shell height")
	}
}

func TestIPadLongChannelDoesNotObserveClampedMessageHeight(t *testing.T) {
	htmlBytes, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(htmlBytes)
	mount := functionBody(html, "function mountScoutChatMessageOverflow(item, body, stack)")
	if mount == "" {
		t.Fatal("could not extract long-message overflow mount")
	}
	for _, forbidden := range []string{"new ResizeObserver", "observer.observe(item)"} {
		if strings.Contains(mount, forbidden) {
			t.Fatalf("iPad Safari long-message clamp has a self-resizing feedback loop: %q", forbidden)
		}
	}
	sources := functionBody(html, "function ensureScoutChatOverflowMeasurementSources()")
	for _, want := range []string{
		"scoutChatOverflowWidthObserver.observe(scoutChatThread)",
		"Math.abs(width - scoutChatOverflowWidth) < 1",
		"scheduleMountedScoutChatOverflowMeasurements()",
	} {
		if !strings.Contains(sources, want) {
			t.Fatalf("iPad Safari long-channel stability contract missing %q", want)
		}
	}
}
