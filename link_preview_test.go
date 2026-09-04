package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
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
	if preview.SiteName != "Example News" || preview.ImageURL != "https://example.com/media/cover.jpg" || preview.MediaType != "article" || preview.Kind != "article" {
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
	if preview.Kind != "youtube_video" || preview.Title != "A useful video" || preview.SiteName != "YouTube" {
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

func TestXAuthorHandleRequiresCanonicalProviderProfile(t *testing.T) {
	if got := xAuthorHandle("https://twitter.com/OpenAI"); got != "OpenAI" {
		t.Fatalf("canonical X author handle=%q", got)
	}
	for _, raw := range []string{
		"https://example.com/OpenAI",
		"https://x.com/OpenAI/status/123",
		"javascript:alert(1)",
	} {
		if got := xAuthorHandle(raw); got != "" {
			t.Errorf("xAuthorHandle(%q)=%q, want refusal", raw, got)
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

func TestTikTokVideoURLRecognizesCanonicalAndShortShareLinks(t *testing.T) {
	for _, raw := range []string{
		"https://www.tiktok.com/@scout2015/video/6718335390845095173?_r=1",
		"https://m.tiktok.com/@scout2015/video/6718335390845095173",
		"https://vm.tiktok.com/ZMabcdef1/",
		"https://vt.tiktok.com/ZSabcdef2/",
		"https://www.tiktok.com/t/ZMabcdef3/",
	} {
		parsed, _ := url.Parse(raw)
		if !tiktokVideoURL(parsed) {
			t.Fatalf("%q not recognized as a TikTok video", raw)
		}
	}
	for _, raw := range []string{
		"https://www.tiktok.com/@scout2015",
		"https://www.tiktok.com/@scout2015/video/not-a-number",
		"https://evil-tiktok.com/@scout2015/video/6718335390845095173",
		"https://vm.tiktok.com/",
		"https://vm.tiktok.com/a/b",
		"https://vt.tiktok.com/../../admin",
	} {
		parsed, _ := url.Parse(raw)
		if tiktokVideoURL(parsed) {
			t.Fatalf("%q incorrectly recognized as a TikTok video", raw)
		}
	}
}

func TestTikTokOEmbedBecomesBoundedPortraitVideoMetadata(t *testing.T) {
	target, _ := url.Parse("https://www.tiktok.com/@scout2015/video/6718335390845095173")
	preview, err := tiktokPreviewFromOEmbed(target, tiktokOEmbedResponse{
		Title:           "Scramble up ur name & I’ll try to guess it😍❤️ #foryoupage",
		AuthorName:      "Scout & Suki",
		ProviderName:    "TikTok",
		ThumbnailURL:    "https://p16.muscdn.com/obj/tos-maliva-p-0068/cover.jpeg",
		ThumbnailWidth:  720,
		ThumbnailHeight: 1280,
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Kind != "tiktok_video" || preview.MediaType != "video" || preview.SiteName != "TikTok" ||
		preview.AuthorName != "Scout & Suki" || preview.AuthorHandle != "scout2015" ||
		preview.Description != "Scout & Suki · @scout2015" || preview.ImageURL != "https://p16.muscdn.com/obj/tos-maliva-p-0068/cover.jpeg" {
		t.Fatalf("tiktok preview=%+v", preview)
	}
	if len([]rune(preview.Title)) > 180 || preview.URL != target.String() {
		t.Fatalf("unbounded or noncanonical TikTok preview=%+v", preview)
	}
}

func TestInstagramContentKindRecognizesOnlyPublicContentPermalinks(t *testing.T) {
	cases := map[string]string{
		"https://www.instagram.com/reel/C9x_Ab-1234/?utm_source=share": "instagram_reel",
		"https://m.instagram.com/reels/C9x_Ab-1234/":                   "instagram_reel",
		"https://www.instagram.com/tv/C9x_Ab-1234/":                    "instagram_video",
		"https://instagram.com/p/C9x_Ab-1234/":                         "instagram_post",
	}
	for raw, want := range cases {
		parsed, _ := url.Parse(raw)
		if got := instagramContentKind(parsed); got != want {
			t.Errorf("instagramContentKind(%q)=%q, want %q", raw, got, want)
		}
	}
	for _, raw := range []string{
		"https://instagram.com/scout/",
		"https://instagram.com/reel/",
		"https://instagram.com/reel/not.valid/",
		"https://instagram.com/p/abc/extra",
		"https://evil-instagram.com/reel/C9x_Ab-1234/",
	} {
		parsed, _ := url.Parse(raw)
		if got := instagramContentKind(parsed); got != "" {
			t.Errorf("instagramContentKind(%q)=%q, want refusal", raw, got)
		}
	}
}

func TestRecognizedProviderFallbacksStayHonestAndTyped(t *testing.T) {
	cases := []struct {
		raw, kind, site, mediaType string
	}{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "youtube_video", "YouTube", "video"},
		{"https://www.tiktok.com/@scout2015/video/6718335390845095173", "tiktok_video", "TikTok", "video"},
		{"https://www.instagram.com/reel/C9x_Ab-1234/", "instagram_reel", "Instagram", "video"},
		{"https://www.instagram.com/p/C9x_Ab-1234/", "instagram_post", "Instagram", "rich"},
		{"https://x.com/OpenAI/status/123", "x_post", "X", "rich"},
	}
	for _, test := range cases {
		parsed, _ := url.Parse(test.raw)
		preview, ok := recognizedProviderLinkPreview(parsed)
		if !ok || preview.URL != test.raw || preview.Kind != test.kind || preview.SiteName != test.site || preview.MediaType != test.mediaType || preview.Title == "" || preview.Description == "" || preview.ImageURL != "" {
			t.Errorf("recognizedProviderLinkPreview(%q)=%+v, ok=%v", test.raw, preview, ok)
		}
	}
	ordinary, _ := url.Parse("https://example.com/story")
	if preview, ok := recognizedProviderLinkPreview(ordinary); ok || preview != (linkPreview{}) {
		t.Fatalf("ordinary website unexpectedly received a provider fallback: %+v, ok=%v", preview, ok)
	}
}

func TestNormalizeProviderLinkPreviewKeepsSafeMetadataAndProviderContract(t *testing.T) {
	target, _ := url.Parse("https://www.instagram.com/reel/C9x_Ab-1234/")
	preview := normalizeProviderLinkPreview(target, linkPreview{
		URL: target.String(), Title: "Instagram", Description: "", SiteName: "redirect.example.net",
		ImageURL: "https://cdn.example.net/poster.jpg",
	})
	if preview.URL != target.String() || preview.Kind != "instagram_reel" || preview.SiteName != "Instagram" || preview.Title != "Instagram reel" ||
		preview.Description != "Open the original on Instagram" || preview.MediaType != "video" || preview.ImageURL != "https://cdn.example.net/poster.jpg" {
		t.Fatalf("normalized Instagram preview=%+v", preview)
	}
}

func TestLinkPreviewKindKeepsTikTokDistinctFromGenericVideo(t *testing.T) {
	tiktok, _ := url.Parse("https://www.tiktok.com/@scout2015/video/6718335390845095173")
	if got := linkPreviewKind(tiktok, "video", "https://example.com/poster.jpg"); got != "tiktok_video" {
		t.Fatalf("TikTok kind=%q, want tiktok_video", got)
	}
	evil, _ := url.Parse("https://evil-tiktok.com/@scout2015/video/6718335390845095173")
	if got := linkPreviewKind(evil, "video", "https://example.com/poster.jpg"); got != "video" {
		t.Fatalf("non-TikTok kind=%q, want generic video", got)
	}
}

func TestLinkPreviewKindKeepsProvidersAndArticlesDistinct(t *testing.T) {
	youtube, _ := url.Parse("https://youtu.be/dQw4w9WgXcQ")
	if got := linkPreviewKind(youtube, "video.other", "https://example.com/poster.jpg"); got != "youtube_video" {
		t.Fatalf("YouTube kind=%q", got)
	}
	instagram, _ := url.Parse("https://instagram.com/p/C9x_Ab-1234/")
	if got := linkPreviewKind(instagram, "article", "https://example.com/poster.jpg"); got != "instagram_post" {
		t.Fatalf("Instagram kind=%q", got)
	}
	x, _ := url.Parse("https://x.com/OpenAI/status/123")
	if got := linkPreviewKind(x, "article", ""); got != "x_post" {
		t.Fatalf("X kind=%q", got)
	}
	article, _ := url.Parse("https://example.com/story")
	if got := linkPreviewKind(article, "article", "https://example.com/cover.jpg"); got != "article" {
		t.Fatalf("article kind=%q", got)
	}
}

func TestParseXEmbedHTMLExtractsPostCopyAndDate(t *testing.T) {
	post, date := parseXEmbedHTML(`<blockquote><p>I went on a hike.<br><br>Work can be done anywhere.</p>&mdash; Alex <a href="https://x.com/Alex/status/123">July 26, 2026</a></blockquote>`)
	if post != "I went on a hike.\n\nWork can be done anywhere." || date != "July 26, 2026" {
		t.Fatalf("post=%q date=%q", post, date)
	}
}

func TestXArticlePreviewUsesPublicMetadataAndExistingArticleContract(t *testing.T) {
	target, _ := normalizeLinkPreviewURL("https://x.com/i/article/2034593904100618240")
	requests := 0
	client := &http.Client{Transport: linkPreviewRoundTrip(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.String() != target.String() {
			t.Fatalf("article used unexpected endpoint %s", req.URL)
		}
		return linkPreviewResponse(req, 200, "text/html", `<head><meta property="og:title" content="A practical expansion plan"><meta property="og:description" content="What the pilot taught us."><meta property="og:image" content="https://pbs.twimg.com/media/cover.jpg"><meta name="author" content="Morgan"><meta name="twitter:creator" content="@morgan"><meta property="article:published_time" content="2026-09-04T12:00:00Z"></head><script>throw new Error('must never execute')</script>`), nil
	})}
	preview, err := fetchLinkPreviewWithClient(context.Background(), target, client)
	if err != nil || requests != 1 || preview.Kind != "article" || preview.MediaType != "article" || preview.SiteName != "X" || preview.Title != "A practical expansion plan" || preview.AuthorName != "Morgan" || preview.AuthorHandle != "morgan" || preview.ImageURL != "https://pbs.twimg.com/media/cover.jpg" || preview.PublishedAt == "" {
		t.Fatalf("preview=%+v requests=%d err=%v", preview, requests, err)
	}
	for _, raw := range []string{"https://x.com/i/article/not-numeric", "https://x.com/i/article/123/extra", "https://evil-x.com/i/article/123", "https://x.com/morgan/article/123"} {
		parsed, _ := url.Parse(raw)
		if xArticleURL(parsed) {
			t.Fatalf("recognized unsupported article %s", raw)
		}
	}
}

func TestProviderLoginRedirectOrDifferentContentCannotMasqueradeAsOriginal(t *testing.T) {
	for _, pair := range [][2]string{
		{"https://x.com/i/article/123", "https://x.com/i/flow/login"},
		{"https://x.com/i/article/123", "https://x.com/i/article/456"},
		{"https://x.com/person/status/123", "https://x.com/person/status/456"},
		{"https://instagram.com/reel/ABCD1234", "https://elsewhere.example/post"},
		{"https://youtube.com/watch?v=one", "https://youtube.com/watch?v=two"},
	} {
		target, _ := url.Parse(pair[0])
		final, _ := url.Parse(pair[1])
		client := &http.Client{Transport: linkPreviewRoundTrip(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/oembed" {
				return linkPreviewResponse(req, 404, "text/plain", "unavailable"), nil
			}
			response := linkPreviewResponse(req, 200, "text/html", `<meta property="og:title" content="Unrelated title"><meta property="og:image" content="https://elsewhere.example/image.png">`)
			response.Request = &http.Request{URL: final}
			return response, nil
		})}
		got, err := fetchLinkPreviewWithClient(context.Background(), target, client)
		want, _ := recognizedProviderLinkPreview(target)
		if err != nil || got != want {
			t.Fatalf("%s -> %s: got=%+v want=%+v err=%v", pair[0], pair[1], got, want, err)
		}
	}
	target, _ := url.Parse("https://x.com/i/article/123")
	shell := normalizeProviderLinkPreview(target, linkPreview{URL: target.String(), Title: "Log in to X / X", Description: "Enter your password", ImageURL: "https://x.com/login.png"})
	if shell.Title != "Article on X" || shell.ImageURL != "" || shell.Description == "Enter your password" {
		t.Fatalf("login shell reused: %+v", shell)
	}
}

func TestProviderFallbackDeadlineLeavesBudgetForHTML(t *testing.T) {
	target, _ := url.Parse("https://www.youtube.com/watch/?v=123")
	calls := 0
	client := &http.Client{Transport: linkPreviewRoundTrip(func(req *http.Request) (*http.Response, error) {
		calls++
		deadline, ok := req.Context().Deadline()
		if !ok {
			t.Fatal("unbounded request")
		}
		remaining := time.Until(deadline)
		if req.URL.Path == "/oembed" {
			if remaining > linkPreviewProviderTimeout {
				t.Fatalf("provider has entire budget: %v", remaining)
			}
			return nil, context.DeadlineExceeded
		}
		if remaining <= linkPreviewProviderTimeout {
			t.Fatalf("fallback inherited provider cancellation: %v", remaining)
		}
		return linkPreviewResponse(req, 200, "text/html", `<meta property="og:title" content="Recovered public metadata">`), nil
	})}
	preview, err := fetchLinkPreviewWithClient(context.Background(), target, client)
	if err != nil || calls != 2 || preview.Title != "Recovered public metadata" || preview.Kind != "youtube_video" {
		t.Fatalf("preview=%+v calls=%d err=%v", preview, calls, err)
	}
}

func TestXPostOptionalImageCannotHoldUsablePostForFullDeadline(t *testing.T) {
	target, _ := url.Parse("https://x.com/Morgan/status/123")
	client := &http.Client{Transport: linkPreviewRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.URL.Hostname() == "publish.twitter.com" {
			return linkPreviewResponse(req, 200, "application/json", `{"author_name":"Morgan","author_url":"https://x.com/Morgan","html":"<blockquote><p>Public post copy</p></blockquote>"}`), nil
		}
		deadline, ok := req.Context().Deadline()
		if !ok || time.Until(deadline) > linkPreviewImageEnrichmentTimeout {
			t.Fatal("optional image has full deadline")
		}
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	start := time.Now()
	preview, err := fetchLinkPreviewWithClient(context.Background(), target, client)
	if err != nil || preview.Description != "Public post copy" || preview.ImageURL != "" || time.Since(start) > 2*time.Second {
		t.Fatalf("usable post delayed/lost: %+v %v %v", preview, err, time.Since(start))
	}
}

func TestProviderFallbackRemainsUsefulWhenXArticleDeclinesPublicMetadata(t *testing.T) {
	target, _ := url.Parse("https://x.com/i/article/123")
	client := &http.Client{Transport: linkPreviewRoundTrip(func(req *http.Request) (*http.Response, error) {
		return linkPreviewResponse(req, 403, "text/html", "sign in required"), nil
	})}
	preview, err := fetchLinkPreviewWithClient(context.Background(), target, client)
	if err != nil || preview.Kind != "article" || preview.Title != "Article on X" || preview.URL != target.String() || preview.ImageURL != "" {
		t.Fatalf("fallback=%+v err=%v", preview, err)
	}
}

func TestLinkPreviewYouTubeNonVideoPagesDoNotBecomePlayableCards(t *testing.T) {
	for _, raw := range []string{"https://youtube.com/", "https://youtube.com/@creator", "https://youtube.com/playlist?list=123"} {
		target, _ := url.Parse(raw)
		if kind := linkPreviewKind(target, "website", ""); kind == "youtube_video" {
			t.Fatalf("%s classified playable", raw)
		}
	}
}

func TestLinkPreviewRasterDimensionsRejectHeaderOnlyAndOversizedImages(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if !linkPreviewImageDimensionsAllowed("image/png", encoded.Bytes()) {
		t.Fatal("valid small PNG refused")
	}
	if linkPreviewImageDimensionsAllowed("image/png", []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("signature-only PNG accepted")
	}
	hugeGIF := append([]byte("GIF89a"), []byte{0xff, 0xff, 0xff, 0xff, 0, 0, 0}...)
	if linkPreviewImageDimensionsAllowed("image/gif", hugeGIF) {
		t.Fatal("oversized GIF accepted")
	}
	if linkPreviewImageDimensionsAllowed("image/webp", []byte("RIFFxxxxWEBPVP8X")) {
		t.Fatal("signature-only WebP accepted")
	}
}

func TestLinkPreviewClientRejectsPrivateRedirectAndDirectDial(t *testing.T) {
	client := linkPreviewHTTPClient()
	defer client.CloseIdleConnections()
	target, _ := url.Parse("http://127.0.0.1/")
	if err := client.CheckRedirect(&http.Request{URL: target}, nil); err == nil {
		t.Fatal("private redirect accepted")
	}
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, target.String(), nil)
	if response, err := client.Do(request); err == nil {
		response.Body.Close()
		t.Fatal("private dial accepted")
	}
}

type linkPreviewRoundTrip func(*http.Request) (*http.Response, error)

func (fn linkPreviewRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }
func linkPreviewResponse(req *http.Request, status int, mime, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: fmt.Sprint(status), Header: http.Header{"Content-Type": []string{mime}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}
}
