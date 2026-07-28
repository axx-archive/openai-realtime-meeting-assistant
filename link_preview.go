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
	linkPreviewMaxURLBytes  = 4 << 10
	linkPreviewMaxHTMLBytes = 768 << 10
	linkPreviewTimeout      = 7 * time.Second
)

type linkPreview struct {
	URL          string `json:"url"`
	Kind         string `json:"kind,omitempty"`
	Title        string `json:"title,omitempty"`
	Description  string `json:"description,omitempty"`
	SiteName     string `json:"siteName,omitempty"`
	ImageURL     string `json:"imageUrl,omitempty"`
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
	req.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,image/gif")
	req.Header.Set("User-Agent", "BonfireOS-LinkPreview/1.0")
	response, err := linkPreviewHTTPClient().Do(req)
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
	if !linkPreviewImageMime(mime) {
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
		TLSHandshakeTimeout:   4 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		MaxIdleConns:          24,
		IdleConnTimeout:       30 * time.Second,
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
	if xPostURL(target) {
		post, postErr := fetchXPostPreview(ctx, target)
		if postErr == nil {
			// X's official oEmbed response carries the post copy and author but no
			// thumbnail. Its page Open Graph image is the author's avatar, which is
			// useful in the dedicated post layout (never stretched as a hero image).
			if page, pageErr := fetchHTMLLinkPreview(ctx, target); pageErr == nil {
				post.ImageURL = page.ImageURL
			}
			return post, nil
		}
	}
	if youtubeVideoURL(target) {
		if video, videoErr := fetchYouTubeVideoPreview(ctx, target); videoErr == nil {
			return video, nil
		}
	}
	return fetchHTMLLinkPreview(ctx, target)
}

func fetchHTMLLinkPreview(ctx context.Context, target *url.URL) (linkPreview, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return linkPreview{}, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9")
	req.Header.Set("User-Agent", "BonfireOS-LinkPreview/1.0")
	response, err := linkPreviewHTTPClient().Do(req)
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

func youtubeVideoURL(target *url.URL) bool {
	if target == nil {
		return false
	}
	host := strings.ToLower(strings.TrimPrefix(target.Hostname(), "www."))
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

func fetchYouTubeVideoPreview(ctx context.Context, target *url.URL) (linkPreview, error) {
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
	response, err := linkPreviewHTTPClient().Do(req)
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
		URL: target.String(), Kind: "video", Title: title,
		Description: previewText(embed.AuthorName, 120), SiteName: firstNonEmptyString(previewText(embed.ProviderName, 80), "YouTube"),
		ImageURL: imageURL, MediaType: "video",
	}, nil
}

func xPostURL(target *url.URL) bool {
	if target == nil {
		return false
	}
	host := strings.ToLower(strings.TrimPrefix(target.Hostname(), "www."))
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

func fetchXPostPreview(ctx context.Context, target *url.URL) (linkPreview, error) {
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
	response, err := linkPreviewHTTPClient().Do(req)
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
	handle := ""
	if authorURL, err := url.Parse(embed.AuthorURL); err == nil {
		handle = strings.Trim(strings.TrimSpace(authorURL.Path), "/")
	}
	canonicalURL := strings.TrimSpace(embed.URL)
	if canonicalURL == "" {
		canonicalURL = target.String()
	}
	return linkPreview{
		URL: canonicalURL, Kind: "x_post", Title: strings.TrimSpace(embed.AuthorName) + " on X",
		Description: previewMultilineText(postText, 700), SiteName: "X", MediaType: "rich",
		AuthorName: previewText(embed.AuthorName, 80), AuthorHandle: previewText(handle, 40),
		PublishedAt: previewText(publishedAt, 40),
	}, nil
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
	imageRaw := firstNonEmptyString(values["og:image:secure_url"], values["og:image"], values["twitter:image"])
	if imageRaw != "" {
		if image, err := base.Parse(imageRaw); err == nil {
			if normalized, err := normalizeLinkPreviewURL(image.String()); err == nil {
				preview.ImageURL = normalized.String()
			}
		}
	}
	preview.Kind = linkPreviewKind(base, preview.MediaType, preview.ImageURL)
	return preview
}

func linkPreviewKind(base *url.URL, mediaType, imageURL string) string {
	if base != nil {
		host := strings.ToLower(strings.TrimPrefix(base.Hostname(), "www."))
		if host == "youtube.com" || host == "youtu.be" || host == "m.youtube.com" {
			return "video"
		}
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(mediaType)), "video") {
		return "video"
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
