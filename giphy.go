package main

// Authenticated GIPHY search proxy for the native chat composer. The API key
// never crosses the server boundary, user input can only reach GIPHY's fixed
// HTTPS search endpoint as encoded query parameters, and the response is
// projected down to the small set of trusted giphy.com URLs the client needs.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	giphySearchDefaultLimit = 12
	giphySearchMaxLimit     = 25
	giphySearchMaxQuery     = 120
	giphySearchMaxResponse  = 1 << 20
	giphyImportMaxBody      = 8 << 10
)

var (
	giphySearchEndpoint   = "https://api.giphy.com/v1/gifs/search"
	giphyTrendingEndpoint = "https://api.giphy.com/v1/gifs/trending"
	// Search and import stay behind the authenticated server proxy: the key is
	// never sent to a client, result URLs remain allowlisted to GIPHY HTTPS
	// hosts, and imports are byte/MIME validated into the ordinary attachment
	// store. Tests may flip this fail-safe off to prove the closed behavior.
	giphyIntegrationEnabled = true
	giphySearchClient       = &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	giphyImportClient = &http.Client{
		Timeout: 12 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
)

type giphyRendition struct {
	URL    string `json:"url"`
	Width  string `json:"width"`
	Height string `json:"height"`
}

type giphyUpstreamResult struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Rating string `json:"rating"`
	Images struct {
		FixedWidth      giphyRendition `json:"fixed_width"`
		FixedWidthStill giphyRendition `json:"fixed_width_still"`
		Downsized       giphyRendition `json:"downsized"`
	} `json:"images"`
}

type giphySearchResult struct {
	ID         string `json:"id"`
	Title      string `json:"title,omitempty"`
	PageURL    string `json:"pageUrl,omitempty"`
	PreviewURL string `json:"previewUrl"`
	StillURL   string `json:"stillUrl,omitempty"`
	MediaURL   string `json:"mediaUrl"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	Rating     string `json:"rating,omitempty"`
}

type giphyImportRequest struct {
	URL      string `json:"url"`
	Name     string `json:"name,omitempty"`
	Title    string `json:"title,omitempty"`
	ID       string `json:"id,omitempty"`
	ThreadID string `json:"threadId"`
}

func giphyAPIKey() string {
	return strings.TrimSpace(os.Getenv("GIPHY_API_KEY"))
}

func trustedGiphyURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "giphy.com" && !strings.HasSuffix(host, ".giphy.com") {
		return ""
	}
	return parsed.String()
}

// trustedGiphyMediaURL narrows the broader search-result URL allowlist to an
// actual GIF asset. The import endpoint follows no redirects and validates
// the downloaded bytes, so user-controlled URLs can never turn it into a
// general-purpose fetch proxy or smuggle a non-image payload into storage.
func trustedGiphyMediaURL(raw string) string {
	trusted := trustedGiphyURL(raw)
	if trusted == "" {
		return ""
	}
	parsed, err := url.Parse(trusted)
	if err != nil || !strings.EqualFold(pathExtension(parsed.Path), ".gif") {
		return ""
	}
	return trusted
}

func pathExtension(path string) string {
	lastSlash := strings.LastIndex(path, "/")
	if lastSlash >= 0 {
		path = path[lastSlash+1:]
	}
	lastDot := strings.LastIndex(path, ".")
	if lastDot < 0 {
		return ""
	}
	return path[lastDot:]
}

func giphyDimension(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 1 || value > 12_000 {
		return 0
	}
	return value
}

func projectGiphyResult(item giphyUpstreamResult) (giphySearchResult, bool) {
	preview := trustedGiphyURL(item.Images.FixedWidth.URL)
	media := trustedGiphyURL(item.Images.Downsized.URL)
	if media == "" {
		media = preview
	}
	if preview == "" {
		preview = media
	}
	if strings.TrimSpace(item.ID) == "" || preview == "" || media == "" {
		return giphySearchResult{}, false
	}
	return giphySearchResult{
		ID:         trimForStorage(item.ID, 100),
		Title:      trimForStorage(item.Title, 180),
		PageURL:    trustedGiphyURL(item.URL),
		PreviewURL: preview,
		StillURL:   trustedGiphyURL(item.Images.FixedWidthStill.URL),
		MediaURL:   media,
		Width:      giphyDimension(firstNonEmptyString(item.Images.Downsized.Width, item.Images.FixedWidth.Width)),
		Height:     giphyDimension(firstNonEmptyString(item.Images.Downsized.Height, item.Images.FixedWidth.Height)),
		Rating:     trimForStorage(item.Rating, 12),
	}, true
}

func searchGiphy(ctx context.Context, user *userAccount, query string, limit int) ([]giphySearchResult, error) {
	if user == nil {
		return nil, fmt.Errorf("GIF search is unavailable")
	}
	key := giphyAPIKey()
	if key == "" {
		return nil, fmt.Errorf("GIF search is not configured")
	}
	upstreamEndpoint := giphySearchEndpoint
	if query == "" {
		upstreamEndpoint = giphyTrendingEndpoint
	}
	endpoint, err := url.Parse(upstreamEndpoint)
	if err != nil {
		return nil, fmt.Errorf("GIF search is unavailable")
	}
	values := endpoint.Query()
	values.Set("api_key", key)
	values.Set("limit", strconv.Itoa(limit))
	values.Set("offset", "0")
	values.Set("rating", "g")
	values.Set("bundle", "messaging_non_clips")
	values.Set("country_code", "US")
	if query != "" {
		values.Set("q", query)
		values.Set("lang", "en")
	}
	endpoint.RawQuery = values.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("GIF search is unavailable")
	}
	request.Header.Set("Accept", "application/json")
	response, err := giphySearchClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("GIF search is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return nil, fmt.Errorf("GIF search is unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, giphySearchMaxResponse+1))
	if err != nil || len(raw) > giphySearchMaxResponse {
		return nil, fmt.Errorf("GIF search returned an invalid response")
	}
	var payload struct {
		Data []giphyUpstreamResult `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("GIF search returned an invalid response")
	}
	results := make([]giphySearchResult, 0, len(payload.Data))
	for _, item := range payload.Data {
		if projected, ok := projectGiphyResult(item); ok {
			results = append(results, projected)
		}
	}
	return results, nil
}

func assistantGiphySearchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if !giphyIntegrationEnabled {
		writeAuthError(w, http.StatusServiceUnavailable, "GIF search is temporarily unavailable")
		return
	}
	if giphyAPIKey() == "" {
		writeAuthError(w, http.StatusServiceUnavailable, "GIF search is not configured")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) > giphySearchMaxQuery {
		writeAuthError(w, http.StatusBadRequest, fmt.Sprintf("GIF search query must be %d characters or fewer", giphySearchMaxQuery))
		return
	}
	limit := giphySearchDefaultLimit
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 || parsed > giphySearchMaxLimit {
			writeAuthError(w, http.StatusBadRequest, fmt.Sprintf("GIF search limit must be between 1 and %d", giphySearchMaxLimit))
			return
		}
		limit = parsed
	}
	results, err := searchGiphy(r.Context(), user, query, limit)
	if err != nil {
		writeAuthError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "results": results})
}

// assistantGiphyImportHandler copies one selected, allowlisted GIPHY GIF into
// Bonfire's content-addressed blob store. This keeps the native app from
// downloading the media to device storage and uploading it again, while the
// ordinary attachment validation, size cap, MIME pinning, and dedupe contract
// remain identical to /assistant/attachments.
func assistantGiphyImportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "GIF import is unavailable")
		return
	}
	if !giphyIntegrationEnabled {
		writeAuthError(w, http.StatusServiceUnavailable, "GIF import is temporarily unavailable")
		return
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, giphyImportMaxBody))
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read GIF import request")
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	payload := giphyImportRequest{}
	if err := decoder.Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read GIF import request")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeAuthError(w, http.StatusBadRequest, "could not read GIF import request")
		return
	}
	destination, _, err := kanbanApp.scoutChatThreadByID(user.Email, strings.TrimSpace(payload.ThreadID))
	if err != nil {
		writeScoutChatThreadError(w, err)
		return
	}
	if destination.ArchivedAt != "" {
		writeAuthError(w, http.StatusConflict, "chat thread is archived")
		return
	}
	mediaURL := trustedGiphyMediaURL(payload.URL)
	if mediaURL == "" {
		writeAuthError(w, http.StatusBadRequest, "GIF URL is not a trusted GIPHY media URL")
		return
	}

	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, mediaURL, nil)
	if err != nil {
		writeAuthError(w, http.StatusBadGateway, "GIF could not be imported")
		return
	}
	request.Header.Set("Accept", "image/gif")
	response, err := giphyImportClient.Do(request)
	if err != nil {
		writeAuthError(w, http.StatusBadGateway, "GIF could not be imported")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		writeAuthError(w, http.StatusBadGateway, "GIF could not be imported")
		return
	}
	if canonicalAttachmentUploadMime(response.Header.Get("Content-Type")) != "image/gif" {
		writeAuthError(w, http.StatusUnsupportedMediaType, "GIPHY media is not a GIF")
		return
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, attachmentUploadMaxBytes+1))
	if err != nil {
		writeAuthError(w, http.StatusBadGateway, "GIF could not be imported")
		return
	}
	if len(data) > attachmentUploadMaxBytes {
		writeAuthError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("GIF exceeds the %dMB cap", attachmentUploadMaxBytes>>20))
		return
	}
	if err := validateAttachmentBytes("image/gif", data); err != nil {
		writeAuthError(w, http.StatusUnsupportedMediaType, "GIPHY media is not a valid GIF")
		return
	}
	ref, err := putBlob(data, "image/gif")
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "GIF could not be stored")
		return
	}
	meta, err := blobStatForRef(ref)
	if err != nil {
		meta = blobMeta{Mime: "image/gif", Size: int64(len(data))}
	}
	name := trimForStorage(firstNonEmptyString(strings.TrimSpace(payload.Name), strings.TrimSpace(payload.Title)), 180)
	if name == "" {
		name = "GIPHY.gif"
	} else if !strings.HasSuffix(strings.ToLower(name), ".gif") {
		name += ".gif"
	}
	grant, err := kanbanApp.grantPendingAttachmentUpload(user, destination, ref, meta)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "GIF could not be authorized")
		return
	}
	file := scoutChatFileAttachment{Name: name, Kind: "gif", Size: meta.Size, Ref: ref, Mime: meta.Mime, SourceID: grant.SourceID, SourceRevision: grant.SourceRevision}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "file": file})
}
