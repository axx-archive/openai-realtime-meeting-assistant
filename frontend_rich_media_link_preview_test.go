package main

import (
	"os"
	"strings"
	"testing"
)

func TestDesktopRichMediaPreviewsHaveDistinctScriptFreeProviderSurfaces(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	start := strings.Index(html, "function renderDesktopChatLinkPreview(card, fallbackURL, preview)")
	endOffset := strings.Index(html[start:], "function renderDesktopChatLinkPreviewFallback(card, url)")
	if start < 0 || endOffset < 0 {
		t.Fatal("desktop link preview renderer is missing")
	}
	renderer := html[start : start+endOffset]
	for _, want := range []string{
		"previewKind === 'youtube_video'",
		"previewKind === 'x_post'",
		"previewKind.startsWith('instagram_')",
		"previewKind === 'instagram_reel'",
		"desktop-chat-link-preview__post-header",
		"desktop-chat-link-preview__post-text",
		"desktop-chat-link-preview__provider-fallback",
		"const brand = isTikTok ? 'TikTok' : isYouTube ? 'YouTube' : 'Instagram'",
		"imageURL.startsWith('/assistant/link-preview/image?')",
		// Previously pinned image.loading = 'lazy' / image.decoding = 'async'.
		// Native lazy-loading never fires for images appended into the chat
		// scroller — measured on production 2026-08-29, all 15 lazy images in a
		// hydrated thread stayed at naturalWidth 0, two of them inside the
		// viewport — so every preview rendered blank. bfChatImage is the shared
		// chat-media path that assigns the same-origin proxy URL and sets
		// decoding = 'async'. The same-origin and metadata-only guarantees below
		// are unchanged.
		"bfChatImage(image, imageURL)",
	} {
		if !strings.Contains(renderer, want) {
			t.Errorf("desktop provider renderer missing %q", want)
		}
	}
	if strings.Contains(renderer, "iframe") || strings.Contains(renderer, "embed.js") || strings.Contains(renderer, "src = preview.imageUrl") {
		t.Fatal("desktop previews must remain metadata-only and same-origin-image-only")
	}

	cssStart := strings.Index(html, "/* E4 desktop chat quality slice.")
	cssEndOffset := strings.Index(html[cssStart:], "/* ---------- Room chat")
	if cssStart < 0 || cssEndOffset < 0 {
		t.Fatal("desktop chat CSS section is missing")
	}
	css := html[cssStart : cssStart+cssEndOffset]
	for _, want := range []string{
		`[data-kind="youtube_video"]`,
		`[data-kind="x_post"]`,
		`[data-kind^="instagram_"]`,
		`[data-kind="instagram_post"]`,
		"aspect-ratio: 16 / 9;",
		"aspect-ratio: 4 / 5;",
		"aspect-ratio: 1;",
		"white-space: pre-line;",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("desktop provider CSS missing %q", want)
		}
	}
}
