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
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("iPad keyboard viewport recovery missing %q", want)
		}
	}
}
