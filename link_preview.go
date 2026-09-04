package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	linkPreviewMaxURLBytes            = 4 << 10
	linkPreviewMaxHTMLBytes           = 768 << 10
	linkPreviewTimeout                = 7 * time.Second
	linkPreviewProviderTimeout        = 3 * time.Second
	linkPreviewImageEnrichmentTimeout = 800 * time.Millisecond
)

type linkPreview struct {
	URL          string `json:"url"`
	Kind         string `json:"kind,omitempty"`
	Title        string `json:"title,omitempty"`
	Description  string `json:"description,omitempty"`
	SiteName     string `json:"siteName,omitempty"`
	ImageURL     string `json:"imageUrl,omitempty"`
	ImageRole    string `json:"imageRole,omitempty"`
	MediaType    string `json:"mediaType,omitempty"`
	AuthorName   string `json:"authorName,omitempty"`
	AuthorHandle string `json:"authorHandle,omitempty"`
	PublishedAt  string `json:"publishedAt,omitempty"`
}

// assistantLinkPreviewHandler keeps arbitrary page fetches off the phone and
// behind one SSRF-hardened door. The response is metadata only; scripts and
// remote HTML never enter the app.
func assistantLinkPreviewHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if userFromRequest(r) == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	rawURL := strings.TrimSpace(r.URL.Query().Get("url"))
	parsed, err := normalizeLinkPreviewURL(rawURL)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), linkPreviewTimeout)
	defer cancel()
	preview, err := fetchLinkPreview(ctx, parsed)
	if err != nil {
		writeAuthError(w, http.StatusUnprocessableEntity, "link preview is unavailable")
		return
	}
	if preview.ImageURL != "" {
		preview.ImageURL = "/assistant/link-preview/image?url=" + url.QueryEscape(preview.ImageURL)
	}
	w.Header().Set("Cache-Control", "private, max-age=900")
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "preview": preview})
}

// assistantLinkPreviewImageHandler proxies only verified raster images. This
// keeps a reader's device IP and app referrer away from every site whose link
// appears in a company thread, while reusing the same DNS-pinned fetch path.
func assistantLinkPreviewImageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if userFromRequest(r) == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	target, err := normalizeLinkPreviewURL(strings.TrimSpace(r.URL.Query().Get("url")))
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), linkPreviewTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "image link is invalid")
		return
	}
	req.Header.Set("Accept", "image/webp,image/png,image/jpeg,image/gif")
	req.Header.Set("User-Agent", "BonfireOS-LinkPreview/1.0")
	client := linkPreviewHTTPClient()
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		writeAuthError(w, http.StatusUnprocessableEntity, "preview image is unavailable")
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		writeAuthError(w, http.StatusUnprocessableEntity, "preview image is unavailable")
		return
	}
	const imageLimit = 6 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, imageLimit+1))
	if err != nil || len(data) == 0 || len(data) > imageLimit {
		writeAuthError(w, http.StatusUnprocessableEntity, "preview image is unavailable")
		return
	}
	mime := http.DetectContentType(data)
	if !linkPreviewImageMime(mime) || !linkPreviewImageDimensionsAllowed(mime, data) {
		writeAuthError(w, http.StatusUnsupportedMediaType, "preview image format is unavailable")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// Inspect dimensions without allocating decoded pixel buffers. Raster magic
// alone permits tiny files that ask the reader to allocate enormous images.
func linkPreviewImageDimensionsAllowed(mime string, data []byte) bool {
	width, height := attachmentImageDimensions(mime, data)
	return width > 0 && height > 0 && width <= 12000 && height <= 12000 && int64(width)*int64(height) <= 32_000_000
}

func linkPreviewImageMime(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func normalizeLinkPreviewURL(raw string) (*url.URL, error) {
	if raw == "" || len(raw) > linkPreviewMaxURLBytes {
		return nil, fmt.Errorf("a valid link is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.User != nil {
		return nil, fmt.Errorf("a valid link is required")
	}
	// URL schemes are case-insensitive. Native keyboards can capitalize the
	// first character of a manually typed link, so canonicalize before applying
	// the protocol allowlist and before fetching metadata.
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("only http and https links can be previewed")
	}
	if parsed.Hostname() == "" {
		return nil, fmt.Errorf("a valid link host is required")
	}
	if port := parsed.Port(); port != "" && !((parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443")) {
		return nil, fmt.Errorf("link previews only support standard web ports")
	}
	if literal := net.ParseIP(parsed.Hostname()); literal != nil && !linkPreviewPublicIP(literal) {
		return nil, fmt.Errorf("private-network links cannot be previewed")
	}
	parsed.Fragment = ""
	return parsed, nil
}

func linkPreviewPublicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	// RFC 6598 carrier-grade NAT and RFC 2544 benchmarking ranges are not
	// public destinations even though net.IP.IsPrivate does not classify them.
	blocked := []string{"100.64.0.0/10", "198.18.0.0/15"}
	for _, raw := range blocked {
		_, network, _ := net.ParseCIDR(raw)
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func linkPreviewHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 4 * time.Second, KeepAlive: 20 * time.Second}
	transport := &http.Transport{
		// Never honor process proxy configuration here: a proxy would bypass the
		// IP-pinning DialContext below and reopen the SSRF boundary.
		Proxy: nil,
		DialContext: func(ctx context.Context, network string, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil || len(resolved) == 0 {
				return nil, fmt.Errorf("link host did not resolve")
			}
			// Reject the hostname if ANY answer is private. This is intentionally
			// stricter than choosing one public answer from a mixed DNS response.
			for _, answer := range resolved {
				if !linkPreviewPublicIP(answer.IP) {
					return nil, fmt.Errorf("link resolved to a private network")
				}
			}
			var lastErr error
			for _, answer := range resolved {
				conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(answer.IP.String(), port))
				if dialErr == nil {
					return conn, nil
				}
				lastErr = dialErr
			}
			return nil, lastErr
		},
		TLSHandshakeTimeout:    4 * time.Second,
		ResponseHeaderTimeout:  5 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		MaxIdleConns:           24,
		IdleConnTimeout:        30 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   linkPreviewTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 4 {
				return errors.New("too many link redirects")
			}
			if _, err := normalizeLinkPreviewURL(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
}

func fetchLinkPreview(ctx context.Context, target *url.URL) (linkPreview, error) {
	client := linkPreviewHTTPClient()
	defer client.CloseIdleConnections()
	return fetchLinkPreviewWithClient(ctx, target, client)
}

// One total deadline covers every provider and HTML request. A slow optional
// provider cannot consume the full fallback budget, and avatar enrichment
// cannot hold an already usable X post until the total deadline.
func fetchLinkPreviewWithClient(ctx context.Context, target *url.URL, client *http.Client) (linkPreview, error) {
	ctx, cancel := context.WithTimeout(ctx, linkPreviewTimeout)
	defer cancel()
	if xPostURL(target) || youtubeVideoURL(target) || tiktokVideoURL(target) {
		providerCtx, providerCancel := context.WithTimeout(ctx, linkPreviewProviderTimeout)
		var rich linkPreview
		var err error
		switch {
		case xPostURL(target):
			rich, err = fetchXPostPreview(providerCtx, target, client)
		case youtubeVideoURL(target):
			rich, err = fetchYouTubeVideoPreview(providerCtx, target, client)
		case tiktokVideoURL(target):
			rich, err = fetchTikTokVideoPreview(providerCtx, target, client)
		}
		providerCancel()
		if err == nil {
			if xPostURL(target) {
				imageCtx, imageCancel := context.WithTimeout(ctx, linkPreviewImageEnrichmentTimeout)
				if page, pageErr := fetchHTMLLinkPreview(imageCtx, target, client); pageErr == nil {
					page = normalizeProviderLinkPreview(target, page)
					rich.ImageURL, rich.ImageRole = page.ImageURL, page.ImageRole
				}
				imageCancel()
			}
			return rich, nil
		}
	}
	page, pageErr := fetchHTMLLinkPreview(ctx, target, client)
	if pageErr == nil {
		return normalizeProviderLinkPreview(target, page), nil
	}
	// Public providers can require sign-in or decline bots. Preserve the
	// original link, without claiming their login page is the shared content.
	if fallback, ok := recognizedProviderLinkPreview(target); ok {
		return fallback, nil
	}
	return linkPreview{}, pageErr
}

func fetchHTMLLinkPreview(ctx context.Context, target *url.URL, client *http.Client) (linkPreview, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return linkPreview{}, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9")
	req.Header.Set("User-Agent", "BonfireOS-LinkPreview/1.0")
	response, err := client.Do(req)
	if err != nil {
		return linkPreview{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return linkPreview{}, fmt.Errorf("preview upstream returned %d", response.StatusCode)
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "application/xhtml+xml") {
		return linkPreview{}, fmt.Errorf("preview upstream is not html")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, linkPreviewMaxHTMLBytes+1))
	if err != nil || len(body) > linkPreviewMaxHTMLBytes {
		return linkPreview{}, fmt.Errorf("preview page is too large")
	}
	preview := parseLinkPreviewHTML(response.Request.URL, body)
	if preview.URL == "" {
		preview.URL = target.String()
	}
	return preview, nil
}

type xOEmbedResponse struct {
	URL        string `json:"url"`
	AuthorName string `json:"author_name"`
	AuthorURL  string `json:"author_url"`
	HTML       string `json:"html"`
}

type youtubeOEmbedResponse struct {
	Title        string `json:"title"`
	AuthorName   string `json:"author_name"`
	ProviderName string `json:"provider_name"`
	ThumbnailURL string `json:"thumbnail_url"`
}

type tiktokOEmbedResponse struct {
	Title           string `json:"title"`
	AuthorName      string `json:"author_name"`
	AuthorURL       string `json:"author_url"`
	ProviderName    string `json:"provider_name"`
	ThumbnailURL    string `json:"thumbnail_url"`
	ThumbnailWidth  int    `json:"thumbnail_width"`
	ThumbnailHeight int    `json:"thumbnail_height"`
}

func youtubeVideoURL(target *url.URL) bool {
	if target == nil {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(target.Hostname()), "www.")
	path := strings.Trim(target.Path, "/")
	if host == "youtu.be" {
		return path != "" && !strings.Contains(path, "/")
	}
	if host != "youtube.com" && host != "m.youtube.com" {
		return false
	}
	if path == "watch" {
		return strings.TrimSpace(target.Query().Get("v")) != ""
	}
	parts := strings.Split(path, "/")
	return len(parts) == 2 && (parts[0] == "shorts" || parts[0] == "embed") && strings.TrimSpace(parts[1]) != ""
}

func fetchYouTubeVideoPreview(ctx context.Context, target *url.URL, client *http.Client) (linkPreview, error) {
	endpoint, _ := url.Parse("https://www.youtube.com/oembed")
	query := endpoint.Query()
	query.Set("url", target.String())
	query.Set("format", "json")
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return linkPreview{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "BonfireOS-LinkPreview/1.0")
	response, err := client.Do(req)
	if err != nil {
		return linkPreview{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return linkPreview{}, fmt.Errorf("youtube oembed returned %d", response.StatusCode)
	}
	var embed youtubeOEmbedResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, 256<<10))
	if err := decoder.Decode(&embed); err != nil {
		return linkPreview{}, err
	}
	title := previewText(embed.Title, 180)
	if title == "" {
		return linkPreview{}, fmt.Errorf("youtube oembed response is incomplete")
	}
	imageURL := ""
	if thumbnail, err := normalizeLinkPreviewURL(strings.TrimSpace(embed.ThumbnailURL)); err == nil {
		imageURL = thumbnail.String()
	}
	return linkPreview{
		URL: target.String(), Kind: "youtube_video", Title: title,
		Description: previewText(embed.AuthorName, 120), SiteName: firstNonEmptyString(previewText(embed.ProviderName, 80), "YouTube"),
		ImageURL: imageURL, MediaType: "video",
	}, nil
}

func tiktokCanonicalVideoURL(target *url.URL) bool {
	if target == nil {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(target.Hostname()), "www.")
	if host != "tiktok.com" && host != "m.tiktok.com" {
		return false
	}
	parts := strings.Split(strings.Trim(target.Path, "/"), "/")
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "@") || len(parts[0]) < 2 || parts[1] != "video" {
		return false
	}
	for _, character := range parts[2] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return parts[2] != ""
}

func tiktokShortVideoURL(target *url.URL) bool {
	if target == nil {
		return false
	}
	host := strings.ToLower(target.Hostname())
	parts := strings.Split(strings.Trim(target.Path, "/"), "/")
	if (host == "vm.tiktok.com" || host == "vt.tiktok.com") && len(parts) == 1 {
		return validTikTokShareCode(parts[0])
	}
	host = strings.TrimPrefix(host, "www.")
	return host == "tiktok.com" && len(parts) == 2 && parts[0] == "t" && validTikTokShareCode(parts[1])
}

func validTikTokShareCode(value string) bool {
	if len(value) < 4 || len(value) > 80 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func tiktokVideoURL(target *url.URL) bool {
	return tiktokCanonicalVideoURL(target) || tiktokShortVideoURL(target)
}

// resolveTikTokVideoURL follows only recognized TikTok share links through the
// same DNS-pinned, private-network-denying client as every other preview. The
// final destination must be a canonical TikTok video; a short link cannot turn
// this specialized lane into a general redirector.
func resolveTikTokVideoURL(ctx context.Context, target *url.URL, client *http.Client) (*url.URL, error) {
	if tiktokCanonicalVideoURL(target) {
		copy := *target
		return &copy, nil
	}
	if !tiktokShortVideoURL(target) {
		return nil, fmt.Errorf("tiktok video link is invalid")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9")
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("User-Agent", "BonfireOS-LinkPreview/1.0")
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("tiktok share link is unavailable")
	}
	canonical := response.Request.URL
	if !tiktokCanonicalVideoURL(canonical) {
		return nil, fmt.Errorf("tiktok share link did not resolve to a video")
	}
	copy := *canonical
	copy.Fragment = ""
	return &copy, nil
}

func fetchTikTokVideoPreview(ctx context.Context, target *url.URL, client *http.Client) (linkPreview, error) {
	canonical, err := resolveTikTokVideoURL(ctx, target, client)
	if err != nil {
		return linkPreview{}, err
	}
	endpoint, _ := url.Parse("https://www.tiktok.com/oembed")
	query := endpoint.Query()
	query.Set("url", canonical.String())
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return linkPreview{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "BonfireOS-LinkPreview/1.0")
	response, err := client.Do(req)
	if err != nil {
		return linkPreview{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return linkPreview{}, fmt.Errorf("tiktok oembed returned %d", response.StatusCode)
	}
	var embed tiktokOEmbedResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, 256<<10))
	if err := decoder.Decode(&embed); err != nil {
		return linkPreview{}, err
	}
	return tiktokPreviewFromOEmbed(canonical, embed)
}

func tiktokPreviewFromOEmbed(target *url.URL, embed tiktokOEmbedResponse) (linkPreview, error) {
	if !tiktokCanonicalVideoURL(target) {
		return linkPreview{}, fmt.Errorf("tiktok video link is invalid")
	}
	author := previewText(embed.AuthorName, 120)
	handle := tiktokAuthorHandle(target)
	title := previewText(embed.Title, 180)
	if title == "" && author != "" {
		title = "Video by " + author
	}
	if title == "" {
		return linkPreview{}, fmt.Errorf("tiktok oembed response is incomplete")
	}
	imageURL := ""
	if thumbnail, err := normalizeLinkPreviewURL(strings.TrimSpace(embed.ThumbnailURL)); err == nil {
		imageURL = thumbnail.String()
	}
	description := author
	if handle != "" {
		description = "@" + handle
		if author != "" && !strings.EqualFold(author, "@"+handle) {
			description = author + " · @" + handle
		}
	}
	return linkPreview{
		URL: target.String(), Kind: "tiktok_video", Title: title, Description: description,
		SiteName: firstNonEmptyString(previewText(embed.ProviderName, 80), "TikTok"), ImageURL: imageURL, MediaType: "video",
		AuthorName: author, AuthorHandle: previewText(handle, 80),
	}, nil
}

func tiktokAuthorHandle(target *url.URL) string {
	if !tiktokCanonicalVideoURL(target) {
		return ""
	}
	parts := strings.Split(strings.Trim(target.Path, "/"), "/")
	return strings.TrimPrefix(parts[0], "@")
}

func instagramContentKind(target *url.URL) string {
	if target == nil {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(target.Hostname()), "www.")
	if host != "instagram.com" && host != "m.instagram.com" {
		return ""
	}
	parts := strings.Split(strings.Trim(target.Path, "/"), "/")
	if len(parts) != 2 || !validInstagramShortcode(parts[1]) {
		return ""
	}
	switch strings.ToLower(parts[0]) {
	case "reel", "reels":
		return "instagram_reel"
	case "tv":
		return "instagram_video"
	case "p":
		return "instagram_post"
	default:
		return ""
	}
}

func validInstagramShortcode(value string) bool {
	if len(value) < 4 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func recognizedProviderLinkPreview(target *url.URL) (linkPreview, bool) {
	if target == nil {
		return linkPreview{}, false
	}
	if kind := instagramContentKind(target); kind != "" {
		title, mediaType := "Instagram post", "rich"
		switch kind {
		case "instagram_reel":
			title, mediaType = "Instagram reel", "video"
		case "instagram_video":
			title, mediaType = "Instagram video", "video"
		}
		return linkPreview{
			URL: target.String(), Kind: kind, Title: title, Description: "Open the original on Instagram",
			SiteName: "Instagram", MediaType: mediaType,
		}, true
	}
	if xArticleURL(target) {
		return linkPreview{URL: target.String(), Kind: "article", Title: "Article on X", Description: "Open the original article on X", SiteName: "X", MediaType: "article"}, true
	}
	if xPostURL(target) {
		return linkPreview{
			URL: target.String(), Kind: "x_post", Title: "Post on X", Description: "Open the original post on X",
			SiteName: "X", MediaType: "rich",
		}, true
	}
	if youtubeVideoURL(target) {
		return linkPreview{
			URL: target.String(), Kind: "youtube_video", Title: "YouTube video", Description: "Open the original video on YouTube",
			SiteName: "YouTube", MediaType: "video",
		}, true
	}
	if tiktokVideoURL(target) {
		return linkPreview{
			URL: target.String(), Kind: "tiktok_video", Title: "TikTok video", Description: "Open the original video on TikTok",
			SiteName: "TikTok", MediaType: "video",
		}, true
	}
	return linkPreview{}, false
}

func normalizeProviderLinkPreview(target *url.URL, preview linkPreview) linkPreview {
	fallback, recognized := recognizedProviderLinkPreview(target)
	if !recognized {
		return preview
	}
	final, err := normalizeLinkPreviewURL(preview.URL)
	if err != nil || !linkPreviewProviderSourceMatches(target, final) || linkPreviewProviderShellTitle(preview.Title) {
		return fallback
	}
	preview.URL = fallback.URL
	preview.Kind = fallback.Kind
	preview.SiteName = fallback.SiteName
	if preview.Title == "" || strings.EqualFold(preview.Title, fallback.SiteName) {
		preview.Title = fallback.Title
	}
	if preview.Description == "" {
		preview.Description = fallback.Description
	}
	preview.MediaType = fallback.MediaType
	return preview
}

// X Articles use a separate public permalink, not a post oEmbed endpoint.
// Only the observed canonical /i/article/<numeric-id> shape is recognized.
func xArticleURL(target *url.URL) bool {
	if target == nil {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(target.Hostname()), "www.")
	if host != "x.com" && host != "twitter.com" {
		return false
	}
	parts := strings.Split(strings.Trim(target.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "i" || parts[1] != "article" || parts[2] == "" {
		return false
	}
	for _, c := range parts[2] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func linkPreviewProviderIdentity(target *url.URL) string {
	if target == nil {
		return ""
	}
	parts := strings.Split(strings.Trim(target.Path, "/"), "/")
	switch {
	case xArticleURL(target):
		return "x_article:" + parts[2]
	case xPostURL(target):
		return "x_post:" + parts[2]
	case youtubeVideoURL(target):
		if strings.TrimPrefix(strings.ToLower(target.Hostname()), "www.") == "youtu.be" {
			return "youtube:" + parts[0]
		}
		if parts[0] == "watch" {
			return "youtube:" + target.Query().Get("v")
		}
		return "youtube:" + parts[1]
	case tiktokCanonicalVideoURL(target):
		return "tiktok:" + parts[2]
	case instagramContentKind(target) != "":
		return instagramContentKind(target) + ":" + parts[1]
	}
	return ""
}

func linkPreviewProviderSourceMatches(target, final *url.URL) bool {
	// A TikTok short link's content identity is only known after resolving it
	// through the DNS-pinned client to a canonical video URL.
	if tiktokShortVideoURL(target) {
		return tiktokCanonicalVideoURL(final)
	}
	identity := linkPreviewProviderIdentity(target)
	return identity != "" && identity == linkPreviewProviderIdentity(final)
}

func linkPreviewProviderShellTitle(title string) bool {
	switch strings.ToLower(strings.TrimSpace(title)) {
	case "log in to x / x", "log in / x", "sign in to x / x", "javascript is not available.", "login • instagram", "log in • instagram", "before you continue to youtube":
		return true
	}
	return false
}

func xPostURL(target *url.URL) bool {
	if target == nil {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(target.Hostname()), "www.")
	if host != "x.com" && host != "twitter.com" {
		return false
	}
	parts := strings.Split(strings.Trim(target.Path, "/"), "/")
	if len(parts) < 3 || !strings.EqualFold(parts[1], "status") || parts[0] == "" || parts[2] == "" {
		return false
	}
	for _, character := range parts[2] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func fetchXPostPreview(ctx context.Context, target *url.URL, client *http.Client) (linkPreview, error) {
	endpoint, _ := url.Parse("https://publish.twitter.com/oembed")
	query := endpoint.Query()
	query.Set("url", target.String())
	query.Set("omit_script", "true")
	query.Set("dnt", "true")
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return linkPreview{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "BonfireOS-LinkPreview/1.0")
	response, err := client.Do(req)
	if err != nil {
		return linkPreview{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return linkPreview{}, fmt.Errorf("x oembed returned %d", response.StatusCode)
	}
	var embed xOEmbedResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, 256<<10))
	if err := decoder.Decode(&embed); err != nil {
		return linkPreview{}, err
	}
	postText, publishedAt := parseXEmbedHTML(embed.HTML)
	if postText == "" || strings.TrimSpace(embed.AuthorName) == "" {
		return linkPreview{}, fmt.Errorf("x oembed response is incomplete")
	}
	handle := xAuthorHandle(embed.AuthorURL)
	canonicalURL := target.String()
	if embeddedURL, err := normalizeLinkPreviewURL(strings.TrimSpace(embed.URL)); err == nil && linkPreviewProviderSourceMatches(target, embeddedURL) {
		canonicalURL = embeddedURL.String()
	}
	return linkPreview{
		URL: canonicalURL, Kind: "x_post", Title: previewText(embed.AuthorName+" on X", 100),
		Description: previewMultilineText(postText, 700), SiteName: "X", MediaType: "rich",
		AuthorName: previewText(embed.AuthorName, 80), AuthorHandle: previewText(handle, 40),
		PublishedAt: previewText(publishedAt, 40),
	}, nil
}

func xAuthorHandle(raw string) string {
	authorURL, err := normalizeLinkPreviewURL(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(authorURL.Hostname()), "www.")
	if host != "x.com" && host != "twitter.com" {
		return ""
	}
	parts := strings.Split(strings.Trim(authorURL.Path, "/"), "/")
	if len(parts) != 1 || parts[0] == "" {
		return ""
	}
	return previewText(parts[0], 40)
}

func previewMultilineText(value string, limit int) string {
	rawLines := strings.Split(strings.TrimSpace(html.UnescapeString(value)), "\n")
	lines := make([]string, 0, len(rawLines))
	lastBlank := false
	for _, raw := range rawLines {
		line := strings.Join(strings.Fields(raw), " ")
		if line == "" {
			if len(lines) == 0 || lastBlank {
				continue
			}
			lastBlank = true
			lines = append(lines, "")
			continue
		}
		lastBlank = false
		lines = append(lines, line)
	}
	value = strings.TrimSpace(strings.Join(lines, "\n"))
	if len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}

func parseXEmbedHTML(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", ""
	}
	tokenizer := html.NewTokenizer(strings.NewReader(raw))
	inPost, inStatusLink := false, false
	var post strings.Builder
	publishedAt := ""
	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			break
		}
		token := tokenizer.Token()
		switch tokenType {
		case html.StartTagToken, html.SelfClosingTagToken:
			switch strings.ToLower(token.Data) {
			case "p":
				inPost = true
			case "br":
				if inPost {
					post.WriteByte('\n')
				}
			case "a":
				for _, attribute := range token.Attr {
					if strings.EqualFold(attribute.Key, "href") && strings.Contains(attribute.Val, "/status/") {
						inStatusLink = true
					}
				}
			}
		case html.TextToken:
			if inPost {
				post.WriteString(html.UnescapeString(token.Data))
			} else if inStatusLink {
				publishedAt += html.UnescapeString(token.Data)
			}
		case html.EndTagToken:
			switch strings.ToLower(token.Data) {
			case "p":
				inPost = false
			case "a":
				inStatusLink = false
			}
		}
	}
	lines := strings.Split(strings.TrimSpace(post.String()), "\n")
	for index := range lines {
		lines[index] = strings.Join(strings.Fields(lines[index]), " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), strings.TrimSpace(publishedAt)
}

func parseLinkPreviewHTML(base *url.URL, body []byte) linkPreview {
	preview := linkPreview{URL: base.String()}
	values := map[string]string{}
	tokenizer := html.NewTokenizer(strings.NewReader(string(body)))
	inTitle := false
	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			break
		}
		token := tokenizer.Token()
		switch tokenType {
		case html.StartTagToken, html.SelfClosingTagToken:
			switch strings.ToLower(token.Data) {
			case "title":
				inTitle = true
			case "meta":
				key, content := "", ""
				for _, attribute := range token.Attr {
					switch strings.ToLower(attribute.Key) {
					case "property", "name":
						key = strings.ToLower(strings.TrimSpace(attribute.Val))
					case "content":
						content = strings.TrimSpace(attribute.Val)
					}
				}
				if key != "" && content != "" && values[key] == "" {
					values[key] = content
				}
			}
		case html.TextToken:
			if inTitle && values["title"] == "" {
				values["title"] = strings.TrimSpace(token.Data)
			}
		case html.EndTagToken:
			if strings.EqualFold(token.Data, "title") {
				inTitle = false
			}
		}
	}
	preview.Title = previewText(firstNonEmptyString(values["og:title"], values["twitter:title"], values["title"]), 180)
	preview.Description = previewText(firstNonEmptyString(values["og:description"], values["twitter:description"], values["description"]), 320)
	preview.SiteName = previewText(firstNonEmptyString(values["og:site_name"], base.Hostname()), 80)
	preview.MediaType = previewText(firstNonEmptyString(values["og:type"], values["twitter:card"]), 60)
	preview.AuthorName = previewText(values["author"], 80)
	preview.PublishedAt = previewText(values["article:published_time"], 60)
	if xArticleURL(base) {
		preview.SiteName, preview.MediaType = "X", "article"
		preview.AuthorHandle = strings.TrimPrefix(previewText(values["twitter:creator"], 40), "@")
	}
	imageRaw := firstNonEmptyString(values["og:image:secure_url"], values["og:image"], values["twitter:image"])
	if imageRaw != "" {
		if image, err := base.Parse(imageRaw); err == nil {
			if normalized, err := normalizeLinkPreviewURL(image.String()); err == nil {
				preview.ImageURL = normalized.String()
			}
		}
	}
	preview.Kind = linkPreviewKind(base, preview.MediaType, preview.ImageURL)
	if preview.Kind == "x_post" && preview.ImageURL != "" {
		preview.ImageRole = linkPreviewXImageRole(preview.ImageURL)
	}
	return preview
}

// A post's Open Graph image is often the shared media, not its author. Only
// X's explicit profile-image path establishes an avatar; preserve everything
// else as content rather than cropping it into a tiny author circle.
func linkPreviewXImageRole(raw string) string {
	image, err := url.Parse(raw)
	if err == nil && strings.EqualFold(image.Hostname(), "pbs.twimg.com") && strings.HasPrefix(image.Path, "/profile_images/") {
		return "author_avatar"
	}
	return "content"
}

func linkPreviewKind(base *url.URL, mediaType, imageURL string) string {
	if base != nil {
		if youtubeVideoURL(base) {
			return "youtube_video"
		}
		if tiktokVideoURL(base) {
			return "tiktok_video"
		}
		if kind := instagramContentKind(base); kind != "" {
			return kind
		}
		if xArticleURL(base) {
			return "article"
		}
		if xPostURL(base) {
			return "x_post"
		}
	}
	normalizedMediaType := strings.ToLower(strings.TrimSpace(mediaType))
	if strings.HasPrefix(normalizedMediaType, "video") {
		return "video"
	}
	if strings.HasPrefix(normalizedMediaType, "article") {
		return "article"
	}
	if strings.TrimSpace(imageURL) != "" {
		return "website"
	}
	return "link"
}

func previewText(value string, limit int) string {
	value = strings.Join(strings.Fields(html.UnescapeString(strings.TrimSpace(value))), " ")
	if len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}
