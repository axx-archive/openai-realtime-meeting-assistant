package main

import (
	"net"
	"net/url"
	"strings"
	"testing"
)

func TestNormalizeLinkPreviewURLRejectsUnsafeTargets(t *testing.T) {
	for _, raw := range []string{
		"file:///etc/passwd",
		"http://127.0.0.1/admin",
		"http://[::1]/admin",
		"http://169.254.169.254/latest/meta-data",
		"http://10.0.0.8/internal",
		"http://user:pass@example.com/",
		"https://example.com:8443/admin",
	} {
		if _, err := normalizeLinkPreviewURL(raw); err == nil {
			t.Fatalf("normalizeLinkPreviewURL(%q) succeeded, want refusal", raw)
		}
	}
	parsed, err := normalizeLinkPreviewURL("https://example.com/story#tracking")
	if err != nil {
		t.Fatalf("public url: %v", err)
	}
	if parsed.String() != "https://example.com/story" {
		t.Fatalf("normalized=%q, want fragment removed", parsed.String())
	}
	capitalized, err := normalizeLinkPreviewURL("HTTPS://example.com/story")
	if err != nil || capitalized.Scheme != "https" {
		t.Fatalf("capitalized scheme normalization=%v, err=%v", capitalized, err)
	}
}

func TestLinkPreviewPublicIPBlocksNonPublicRanges(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "::1", "10.0.0.1", "100.64.0.1", "198.18.0.1", "169.254.1.1", "fc00::1"} {
		if linkPreviewPublicIP(net.ParseIP(raw)) {
			t.Fatalf("%s classified public", raw)
		}
	}
	if !linkPreviewPublicIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("8.8.8.8 classified private")
	}
}

func TestParseLinkPreviewHTMLPrefersOpenGraphAndResolvesImage(t *testing.T) {
	base, _ := url.Parse("https://example.com/posts/one")
	body := `<html><head>
		<title>Fallback title</title>
		<meta property="og:title" content="A &amp; B">
		<meta property="og:description" content="  A useful   summary. ">
		<meta property="og:site_name" content="Example News">
		<meta property="og:image" content="/media/cover.jpg">
		<meta property="og:type" content="article">
	</head></html>`
	preview := parseLinkPreviewHTML(base, []byte(body))
	if preview.Title != "A & B" || preview.Description != "A useful summary." {
		t.Fatalf("preview copy=%+v", preview)
	}
	if preview.SiteName != "Example News" || preview.ImageURL != "https://example.com/media/cover.jpg" || preview.MediaType != "article" || preview.Kind != "website" {
		t.Fatalf("preview metadata=%+v", preview)
	}
}

func TestParseLinkPreviewHTMLClassifiesPlayableMedia(t *testing.T) {
	base, _ := url.Parse("https://www.youtube.com/watch?v=abc123")
	body := `<html><head>
		<meta property="og:title" content="A useful video">
		<meta property="og:site_name" content="YouTube">
		<meta property="og:image" content="https://i.ytimg.com/vi/abc123/maxresdefault.jpg">
		<meta property="og:type" content="video.other">
	</head></html>`
	preview := parseLinkPreviewHTML(base, []byte(body))
	if preview.Kind != "video" || preview.Title != "A useful video" || preview.SiteName != "YouTube" {
		t.Fatalf("video preview=%+v", preview)
	}
}

func TestPreviewTextIsBounded(t *testing.T) {
	got := previewText(strings.Repeat("word ", 100), 24)
	if len([]rune(got)) > 24 || !strings.HasSuffix(got, "…") {
		t.Fatalf("preview text=%q runes=%d", got, len([]rune(got)))
	}
}

func TestLinkPreviewImageMimeRejectsScriptableAndUnknownTypes(t *testing.T) {
	for _, mime := range []string{"image/png", "image/jpeg", "image/gif", "image/webp"} {
		if !linkPreviewImageMime(mime) {
			t.Fatalf("%s refused", mime)
		}
	}
	for _, mime := range []string{"image/svg+xml", "text/html", "application/octet-stream"} {
		if linkPreviewImageMime(mime) {
			t.Fatalf("%s accepted", mime)
		}
	}
}

func TestXPostURLRequiresCanonicalHostStatusAndNumericID(t *testing.T) {
	for _, raw := range []string{
		"https://x.com/alexfinn/status/2081408374315602338?s=46",
		"https://twitter.com/OpenAI/status/123",
	} {
		parsed, _ := url.Parse(raw)
		if !xPostURL(parsed) {
			t.Fatalf("%q not recognized as an X post", raw)
		}
	}
	for _, raw := range []string{
		"https://x.com/home", "https://evil-x.com/a/status/123", "https://x.com/a/status/not-a-number",
	} {
		parsed, _ := url.Parse(raw)
		if xPostURL(parsed) {
			t.Fatalf("%q incorrectly recognized as an X post", raw)
		}
	}
}

func TestYouTubeVideoURLRecognizesWatchShortAndShareLinks(t *testing.T) {
	for _, raw := range []string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://m.youtube.com/shorts/dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ",
	} {
		parsed, _ := url.Parse(raw)
		if !youtubeVideoURL(parsed) {
			t.Fatalf("%q not recognized as a YouTube video", raw)
		}
	}
	for _, raw := range []string{
		"https://youtube.com/", "https://youtube.com/watch", "https://evil-youtube.com/watch?v=abc",
	} {
		parsed, _ := url.Parse(raw)
		if youtubeVideoURL(parsed) {
			t.Fatalf("%q incorrectly recognized as a YouTube video", raw)
		}
	}
}

func TestParseXEmbedHTMLExtractsPostCopyAndDate(t *testing.T) {
	post, date := parseXEmbedHTML(`<blockquote><p>I went on a hike.<br><br>Work can be done anywhere.</p>&mdash; Alex <a href="https://x.com/Alex/status/123">July 26, 2026</a></blockquote>`)
	if post != "I went on a hike.\n\nWork can be done anywhere." || date != "July 26, 2026" {
		t.Fatalf("post=%q date=%q", post, date)
	}
}
