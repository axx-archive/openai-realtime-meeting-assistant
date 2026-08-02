package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func setupGiphyTest(t *testing.T) []*http.Cookie {
	t.Helper()
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	previousEndpoint := giphySearchEndpoint
	previousTrendingEndpoint := giphyTrendingEndpoint
	previousClient := giphySearchClient
	previousImportClient := giphyImportClient
	previousEnabled := giphyIntegrationEnabled
	giphyIntegrationEnabled = true
	t.Cleanup(func() {
		kanbanApp = previousApp
		giphySearchEndpoint = previousEndpoint
		giphyTrendingEndpoint = previousTrendingEndpoint
		giphySearchClient = previousClient
		giphyImportClient = previousImportClient
		giphyIntegrationEnabled = previousEnabled
	})
	return loginAs(t, "aj@shareability.com", "B0NFIRE!")
}

func giphyTestThreadID(t *testing.T) string {
	t.Helper()
	user := accountStore().findUser("aj@shareability.com")
	thread, err := kanbanApp.createScoutChatThread(user.Email, user.Name, "GIF test", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create GIF destination: %v", err)
	}
	return thread.ID
}

type giphyRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn giphyRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func tinyGIF(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	pixel := image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.Black, color.White})
	if err := gif.Encode(&buffer, pixel, nil); err != nil {
		t.Fatalf("encode GIF fixture: %v", err)
	}
	return buffer.Bytes()
}

func giphyImportRequestForTest(t *testing.T, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/assistant/giphy/import", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantGiphyImportHandler(recorder, request)
	return recorder
}

func giphyRequest(t *testing.T, method, path string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantGiphySearchHandler(recorder, request)
	return recorder
}

func TestAssistantGiphyLaneIsCompileTimeDisabledPendingPrivacyAndProvenance(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	previousEnabled := giphyIntegrationEnabled
	giphyIntegrationEnabled = false
	t.Cleanup(func() {
		kanbanApp = previousApp
		giphyIntegrationEnabled = previousEnabled
	})
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	t.Setenv("GIPHY_API_KEY", "server-secret")
	if recorder := giphyRequest(t, http.MethodGet, "/assistant/giphy/search?q=hello", cookies); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled search status=%d body=%s, want 503", recorder.Code, recorder.Body.String())
	}
	if recorder := giphyImportRequestForTest(t, `{"threadId":"not-used","url":"https://media.giphy.com/media/test/giphy.gif"}`, cookies); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled import status=%d body=%s, want 503", recorder.Code, recorder.Body.String())
	}
}

func TestAssistantGiphySearchHandlerAuthConfigurationAndInputGates(t *testing.T) {
	cookies := setupGiphyTest(t)
	t.Setenv("GIPHY_API_KEY", "")

	if recorder := giphyRequest(t, http.MethodPost, "/assistant/giphy/search", nil); recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d, want 405", recorder.Code)
	}
	if recorder := giphyRequest(t, http.MethodGet, "/assistant/giphy/search?q=hello", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out status=%d body=%s, want 401 before config disclosure", recorder.Code, recorder.Body.String())
	}
	if recorder := giphyRequest(t, http.MethodGet, "/assistant/giphy/search?q=hello", cookies); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured status=%d body=%s, want 503", recorder.Code, recorder.Body.String())
	}

	t.Setenv("GIPHY_API_KEY", "server-secret")
	for _, path := range []string{
		"/assistant/giphy/search?q=hello&limit=0",
		"/assistant/giphy/search?q=hello&limit=26",
		"/assistant/giphy/search?q=hello&limit=nope",
		"/assistant/giphy/search?q=" + strings.Repeat("a", giphySearchMaxQuery+1),
	} {
		if recorder := giphyRequest(t, http.MethodGet, path, cookies); recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid path %q status=%d body=%s, want 400", path, recorder.Code, recorder.Body.String())
		}
	}

	crossOrigin := httptest.NewRequest(http.MethodGet, "/assistant/giphy/search?q=hello", nil)
	crossOrigin.Header.Set("Origin", "https://evil.example")
	for _, cookie := range cookies {
		crossOrigin.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantGiphySearchHandler(recorder, crossOrigin)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status=%d, want 403", recorder.Code)
	}
}

func TestAssistantGiphySearchHandlerUsesTrendingForEmptyQuery(t *testing.T) {
	cookies := setupGiphyTest(t)
	t.Setenv("GIPHY_API_KEY", "server-secret")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/gifs/trending" {
			t.Errorf("upstream path=%q, want fixed trending endpoint", r.URL.Path)
		}
		if r.URL.Query().Get("q") != "" || r.URL.Query().Get("limit") != "5" || r.URL.Query().Get("api_key") != "server-secret" {
			t.Errorf("trending query=%v", r.URL.Query())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"trend-1","title":"Trending","images":{"fixed_width":{"url":"https://media.giphy.com/media/trend-1/200w.gif","width":"200","height":"100"}}}]}`))
	}))
	defer upstream.Close()
	giphyTrendingEndpoint = upstream.URL + "/v1/gifs/trending"
	giphySearchClient = upstream.Client()

	recorder := giphyRequest(t, http.MethodGet, "/assistant/giphy/search?limit=5", cookies)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"id":"trend-1"`) {
		t.Fatalf("trending status=%d body=%s, want projected result", recorder.Code, recorder.Body.String())
	}
}

func TestAssistantGiphySearchHandlerProxiesExactQueryAndProjectsTrustedURLs(t *testing.T) {
	cookies := setupGiphyTest(t)
	t.Setenv("GIPHY_API_KEY", "server-secret")
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if r.URL.Path != "/v1/gifs/search" || r.Method != http.MethodGet {
			t.Errorf("upstream request=%s %s", r.Method, r.URL.Path)
		}
		query := r.URL.Query()
		if query.Get("api_key") != "server-secret" || query.Get("q") != "party @cats + dogs" || query.Get("limit") != "7" {
			t.Errorf("upstream query=%v", query)
		}
		if query.Get("rating") != "g" || query.Get("bundle") != "messaging_non_clips" || query.Get("country_code") != "US" {
			t.Errorf("upstream safety/locality params=%v", query)
		}
		if customer := query.Get("customer_id"); customer != "" {
			t.Errorf("customer_id=%q, want no user-derived provider identifier", customer)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
          "data": [
            {
              "id": "gif-1",
              "title": "Celebration",
              "url": "https://giphy.com/gifs/gif-1",
              "rating": "g",
              "images": {
                "fixed_width": {"url":"https://media1.giphy.com/media/gif-1/200w.gif","width":"200","height":"120"},
                "fixed_width_still": {"url":"https://media1.giphy.com/media/gif-1/200w_s.gif","width":"200","height":"120"},
                "downsized": {"url":"https://media2.giphy.com/media/gif-1/down.gif","width":"400","height":"240"}
              }
            },
            {
              "id": "gif-2",
              "title": "Fallback",
              "url": "https://tracker.example/gifs/gif-2",
              "rating": "g",
              "images": {
                "fixed_width": {"url":"https://media.giphy.com/media/gif-2/200w.gif","width":"200","height":"bad"},
                "fixed_width_still": {"url":"https://tracker.example/still.gif"},
                "downsized": {}
              }
            },
            {
              "id": "gif-evil",
              "images": {"fixed_width":{"url":"https://evil.example/pixel.gif"}}
            }
          ]
        }`))
	}))
	defer upstream.Close()
	giphySearchEndpoint = upstream.URL + "/v1/gifs/search"
	giphySearchClient = upstream.Client()

	path := "/assistant/giphy/search?q=" + url.QueryEscape("party @cats + dogs") + "&limit=7"
	recorder := giphyRequest(t, http.MethodGet, path, cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("search status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	if upstreamCalls != 1 {
		t.Fatalf("upstream calls=%d, want 1", upstreamCalls)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" || strings.Contains(recorder.Body.String(), "server-secret") || strings.Contains(recorder.Body.String(), "evil.example") || strings.Contains(recorder.Body.String(), "tracker.example") {
		t.Fatalf("unsafe response headers/body: %s", recorder.Body.String())
	}
	var response struct {
		OK      bool                `json:"ok"`
		Results []giphySearchResult `json:"results"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.OK || len(response.Results) != 2 {
		t.Fatalf("response=%s, want two trusted results", recorder.Body.String())
	}
	if response.Results[0].MediaURL != "https://media2.giphy.com/media/gif-1/down.gif" || response.Results[0].PreviewURL != "https://media1.giphy.com/media/gif-1/200w.gif" || response.Results[0].Width != 400 || response.Results[0].Height != 240 {
		t.Fatalf("first projection=%+v", response.Results[0])
	}
	if response.Results[1].PageURL != "" || response.Results[1].StillURL != "" || response.Results[1].MediaURL != response.Results[1].PreviewURL || response.Results[1].Height != 0 {
		t.Fatalf("fallback projection=%+v", response.Results[1])
	}
}

func TestAssistantGiphySearchHandlerHidesUpstreamFailures(t *testing.T) {
	cookies := setupGiphyTest(t)
	t.Setenv("GIPHY_API_KEY", "server-secret")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "api_key=server-secret internal detail", http.StatusTooManyRequests)
	}))
	defer upstream.Close()
	giphySearchEndpoint = upstream.URL
	giphySearchClient = upstream.Client()

	recorder := giphyRequest(t, http.MethodGet, "/assistant/giphy/search?q=hello", cookies)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("upstream failure status=%d body=%s, want 502", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "server-secret") || strings.Contains(recorder.Body.String(), "internal detail") {
		t.Fatalf("upstream detail leaked: %s", recorder.Body.String())
	}
}

func TestAssistantGiphyImportHandlerAuthenticatesBeforeFetchAndRejectsUntrustedURLs(t *testing.T) {
	cookies := setupGiphyTest(t)
	threadID := giphyTestThreadID(t)
	requests := 0
	giphyImportClient = &http.Client{Transport: giphyRoundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, io.EOF
	})}

	if recorder := giphyImportRequestForTest(t, `{"url":"https://media.giphy.com/media/ok/giphy.gif"}`, nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out status=%d body=%s, want 401", recorder.Code, recorder.Body.String())
	}
	for _, body := range []string{
		`{"url":"http://media.giphy.com/media/ok/giphy.gif"}`,
		`{"url":"https://evil.example/media/ok/giphy.gif"}`,
		`{"url":"https://media.giphy.com/media/ok/not-a-gif.webp"}`,
		`{"url":"https://giphy.com/gifs/this-is-a-page"}`,
	} {
		body = strings.TrimSuffix(body, "}") + fmt.Sprintf(`,"threadId":%q}`, threadID)
		if recorder := giphyImportRequestForTest(t, body, cookies); recorder.Code != http.StatusBadRequest {
			t.Fatalf("untrusted body=%s status=%d response=%s, want 400", body, recorder.Code, recorder.Body.String())
		}
	}
	if requests != 0 {
		t.Fatalf("untrusted or unsigned imports reached upstream %d times", requests)
	}
}

func TestAssistantGiphyImportHandlerStoresValidatedGIF(t *testing.T) {
	cookies := setupGiphyTest(t)
	threadID := giphyTestThreadID(t)
	data := tinyGIF(t)
	giphyImportClient = &http.Client{Transport: giphyRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://media.giphy.com/media/celebrate/giphy.gif" || request.Header.Get("Accept") != "image/gif" {
			t.Errorf("unexpected import request: %s accept=%q", request.URL, request.Header.Get("Accept"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"image/gif"}},
			Body:       io.NopCloser(bytes.NewReader(data)),
			Request:    request,
		}, nil
	})}

	recorder := giphyImportRequestForTest(t, fmt.Sprintf(`{"url":"https://media.giphy.com/media/celebrate/giphy.gif","title":"Celebration","id":"celebrate","threadId":%q}`, threadID), cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("import status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	var response struct {
		OK   bool                    `json:"ok"`
		File scoutChatFileAttachment `json:"file"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode import response: %v", err)
	}
	if !response.OK || response.File.Name != "Celebration.gif" || response.File.Kind != "gif" || response.File.Mime != "image/gif" || response.File.Size != int64(len(data)) || response.File.Ref == "" || response.File.SourceID == "" || response.File.SourceRevision == "" {
		t.Fatalf("unexpected import response: %+v", response)
	}
	stored, meta, err := getBlob(response.File.Ref)
	if err != nil || meta.Mime != "image/gif" || !bytes.Equal(stored, data) {
		t.Fatalf("stored GIF mismatch: mime=%q bytes=%d err=%v", meta.Mime, len(stored), err)
	}
}

func TestAssistantGiphyImportHandlerRejectsBadUpstreamMedia(t *testing.T) {
	cookies := setupGiphyTest(t)
	threadID := giphyTestThreadID(t)
	for _, test := range []struct {
		name        string
		status      int
		contentType string
		body        []byte
		wantStatus  int
	}{
		{name: "upstream status", status: http.StatusFound, contentType: "image/gif", body: tinyGIF(t), wantStatus: http.StatusBadGateway},
		{name: "wrong mime", status: http.StatusOK, contentType: "image/png", body: tinyGIF(t), wantStatus: http.StatusUnsupportedMediaType},
		{name: "malformed gif", status: http.StatusOK, contentType: "image/gif", body: []byte("GIF89a-not-valid"), wantStatus: http.StatusUnsupportedMediaType},
	} {
		t.Run(test.name, func(t *testing.T) {
			giphyImportClient = &http.Client{Transport: giphyRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Header: http.Header{"Content-Type": []string{test.contentType}}, Body: io.NopCloser(bytes.NewReader(test.body)), Request: request}, nil
			})}
			recorder := giphyImportRequestForTest(t, fmt.Sprintf(`{"url":"https://media.giphy.com/media/fixture/giphy.gif","threadId":%q}`, threadID), cookies)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), test.wantStatus)
			}
		})
	}
}
