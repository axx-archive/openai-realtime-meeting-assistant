package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func setupGiphyTest(t *testing.T) []*http.Cookie {
	t.Helper()
	setupAuthTestEnv(t)
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	previousEndpoint := giphySearchEndpoint
	previousTrendingEndpoint := giphyTrendingEndpoint
	previousClient := giphySearchClient
	t.Cleanup(func() {
		giphySearchEndpoint = previousEndpoint
		giphyTrendingEndpoint = previousTrendingEndpoint
		giphySearchClient = previousClient
	})
	return loginAs(t, "aj@shareability.com", "B0NFIRE!")
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
		if customer := query.Get("customer_id"); customer == "" || strings.Contains(customer, "aj@") {
			t.Errorf("customer_id=%q, want stable pseudonym", customer)
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
