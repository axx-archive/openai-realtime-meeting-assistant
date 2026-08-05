package main

import (
	"os"
	"strings"
	"testing"
)

func TestDesktopTikTokPreviewUsesPortraitSameOriginMediaCard(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	start := strings.Index(html, "function renderDesktopChatLinkPreview(card, fallbackURL, preview)")
	end := strings.Index(html[start:], "function renderDesktopChatLinkPreviewFallback(card, url)")
	if start < 0 || end < 0 {
		t.Fatal("desktop link preview renderer is missing")
	}
	renderer := html[start : start+end]
	for _, want := range []string{
		"previewKind === 'tiktok_video'",
		"isTikTok ? 'TikTok · video'",
		"desktop-chat-link-preview__creator",
		"desktop-chat-link-preview__visual",
		"desktop-chat-link-preview__brand",
		"desktop-chat-link-preview__play",
		"imageURL.startsWith('/assistant/link-preview/image?')",
		"image.loading = 'lazy'",
		"image.decoding = 'async'",
	} {
		if !strings.Contains(renderer, want) {
			t.Errorf("TikTok renderer missing %q", want)
		}
	}
	if strings.Contains(renderer, "iframe") || strings.Contains(renderer, "embed.js") || strings.Contains(renderer, "src = preview.imageUrl") {
		t.Fatal("desktop TikTok preview must use sanitized metadata and the same-origin image proxy only")
	}

	cssStart := strings.Index(html, "/* E4 desktop chat quality slice.")
	cssEnd := strings.Index(html[cssStart:], "/* ---------- Room chat")
	if cssStart < 0 || cssEnd < 0 {
		t.Fatal("desktop chat CSS section is missing")
	}
	css := html[cssStart : cssStart+cssEnd]
	for _, want := range []string{
		`.desktop-chat-link-preview[data-kind="tiktok_video"]`,
		"aspect-ratio: 9 / 16;",
		"object-fit: cover;",
		"outline: 1px solid rgba(0, 0, 0, 0.1);",
		"outline-color: rgba(255, 255, 255, 0.1);",
		"padding-left: 2px;",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("TikTok portrait card CSS missing %q", want)
		}
	}
}
