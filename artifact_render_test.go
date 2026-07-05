package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testDeckBody = "<!doctype html>\n<html><head><title>Deck</title></head><body><h1>Bonfire</h1></body></html>"

// The detection contract: metadata type=html_deck OR a body that starts
// (after whitespace) as an HTML document. Markdown that merely mentions HTML
// stays on the escaped renderer — that renderer must never be loosened.
func TestArtifactIsHTMLDocumentDetectionMatrix(t *testing.T) {
	cases := []struct {
		name     string
		metadata map[string]string
		text     string
		want     bool
	}{
		{"doctype prefix", nil, "<!doctype html><html></html>", true},
		{"doctype uppercase", nil, "<!DOCTYPE HTML>\n<html></html>", true},
		{"doctype after whitespace", nil, "\n\n  \t<!doctype html><html></html>", true},
		{"html tag prefix", nil, `<html lang="en"><body></body></html>`, true},
		{"html tag uppercase", nil, "<HTML><body></body></HTML>", true},
		{"metadata html_deck plain body", map[string]string{"type": "html_deck"}, "deck body not yet rendered", true},
		{"metadata html_deck padded", map[string]string{"type": " html_deck "}, "deck body", true},
		{"markdown body", nil, "# Thesis\n\nA plain research brief.", false},
		{"html mentioned mid-body", nil, "The deck ships as <html> inside an iframe.", false},
		{"metadata markdown type", map[string]string{"type": "markdown"}, "# Thesis", false},
		{"empty body", nil, "", false},
	}
	for _, tc := range cases {
		artifact := meetingMemoryEntry{Text: tc.text, Metadata: tc.metadata}
		if got := artifactIsHTMLDocument(artifact); got != tc.want {
			t.Errorf("%s: artifactIsHTMLDocument=%v, want %v", tc.name, got, tc.want)
		}
	}
}

// The render route's entire authority is the short-lived HMAC token: no
// credential 401s, a bad/expired/cross-artifact token 403s, the minted token
// 200s — and the 200 carries the pinned sandbox headers exactly.
func TestArtifactRenderRequiresToken(t *testing.T) {
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	artifact, _, err := kanbanApp.createOSArtifact("design", "investor deck", testDeckBody, "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}

	renderStatus := func(target string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		artifactRenderHandler(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		return recorder
	}

	if recorder := renderStatus("/artifacts/render?id=" + artifact.ID); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("no-token status=%d, want 401", recorder.Code)
	}
	if recorder := renderStatus("/artifacts/render?id=" + artifact.ID + "&t=not-a-token"); recorder.Code != http.StatusForbidden {
		t.Fatalf("bad-token status=%d, want 403", recorder.Code)
	}
	expired := mintArtifactRenderToken(artifact.ID, time.Now().Add(-time.Minute))
	if recorder := renderStatus("/artifacts/render?id=" + artifact.ID + "&t=" + expired); recorder.Code != http.StatusForbidden {
		t.Fatalf("expired-token status=%d, want 403", recorder.Code)
	}
	crossArtifact := mintArtifactRenderToken("some-other-artifact", time.Now().Add(artifactRenderTokenTTL))
	if recorder := renderStatus("/artifacts/render?id=" + artifact.ID + "&t=" + crossArtifact); recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-artifact-token status=%d, want 403", recorder.Code)
	}

	token := mintArtifactRenderToken(artifact.ID, time.Now().Add(artifactRenderTokenTTL))
	recorder := renderStatus("/artifacts/render?id=" + artifact.ID + "&t=" + token)
	if recorder.Code != http.StatusOK {
		t.Fatalf("valid-token status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Body.String(); got != testDeckBody {
		t.Fatalf("body=%q, want the artifact body verbatim", got)
	}

	// Sandbox headers, pinned per spec §4 — the deck template is fully
	// self-contained, so no network source class may be reachable, forms may
	// not submit anywhere, and the CSP sandbox directive forces an opaque
	// origin even when the route is loaded as a top-level document.
	if got := recorder.Header().Get("Content-Security-Policy"); got != "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:; media-src data:; form-action 'none'; sandbox allow-scripts" {
		t.Fatalf("Content-Security-Policy=%q, want the pinned sandbox policy", got)
	}
	if got := recorder.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Fatalf("X-Frame-Options=%q, want SAMEORIGIN", got)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type=%q, want text/html; charset=utf-8", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", got)
	}
	if got := recorder.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy=%q, want no-referrer", got)
	}
}

// Non-HTML artifacts 404 even with a valid token — the escaped viewer owns
// them; this route must never become a generic body-serving endpoint.
func TestArtifactRenderNonHTMLArtifact404(t *testing.T) {
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	artifact, _, err := kanbanApp.createOSArtifact("research", "demand validation", "# Thesis\n\nMarkdown brief only.", "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}

	token := mintArtifactRenderToken(artifact.ID, time.Now().Add(artifactRenderTokenTTL))
	recorder := httptest.NewRecorder()
	artifactRenderHandler(recorder, httptest.NewRequest(http.MethodGet, "/artifacts/render?id="+artifact.ID+"&t="+token, nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404 for a non-html artifact", recorder.Code)
	}

	// Unknown artifact id: also 404 (token is valid for the id, entry gone).
	missingToken := mintArtifactRenderToken("os-artifact-missing", time.Now().Add(artifactRenderTokenTTL))
	recorder = httptest.NewRecorder()
	artifactRenderHandler(recorder, httptest.NewRequest(http.MethodGet, "/artifacts/render?id=os-artifact-missing&t="+missingToken, nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404 for a missing artifact", recorder.Code)
	}
}

// The mint endpoint is the session boundary: signed-out callers get 401
// (same contract as artifactsHandler), signed-in callers get a token that the
// render route accepts, and non-HTML/missing artifacts 404 so the client
// never holds a token that can only fail.
func TestArtifactRenderTokenMintIsSessionGated(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	deck, _, err := kanbanApp.createOSArtifactWithMetadata("design", "investor deck", "deck body pending render", "AJ", map[string]string{"type": "html_deck"})
	if err != nil {
		t.Fatalf("createOSArtifactWithMetadata: %v", err)
	}
	brief, _, err := kanbanApp.createOSArtifact("research", "demand validation", "# Thesis\n\nMarkdown brief only.", "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}

	// Session gate: no cookie -> 401, same contract as artifactsHandler.
	recorder := httptest.NewRecorder()
	artifactRenderTokenHandler(recorder, httptest.NewRequest(http.MethodGet, "/artifacts/render-token?id="+deck.ID, nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out status=%d, want 401", recorder.Code)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	mint := func(id string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/artifacts/render-token?id="+id, nil)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		artifactRenderTokenHandler(recorder, req)
		return recorder
	}

	if recorder := mint("os-artifact-missing"); recorder.Code != http.StatusNotFound {
		t.Fatalf("missing-artifact mint status=%d, want 404", recorder.Code)
	}
	if recorder := mint(brief.ID); recorder.Code != http.StatusNotFound {
		t.Fatalf("non-html mint status=%d, want 404", recorder.Code)
	}

	recorder = mint(deck.ID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("mint status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	payload := struct {
		OK        bool   `json:"ok"`
		Token     string `json:"token"`
		URL       string `json:"url"`
		ExpiresAt string `json:"expiresAt"`
	}{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode mint response: %v", err)
	}
	if !payload.OK || payload.Token == "" || payload.ExpiresAt == "" {
		t.Fatalf("mint payload=%+v, want ok with a token and expiry", payload)
	}
	if !strings.HasPrefix(payload.URL, "/artifacts/render?id=") || !strings.Contains(payload.URL, "&t=") {
		t.Fatalf("mint url=%q, want a ready-to-open render URL", payload.URL)
	}

	// The minted token is the render route's credential — no cookie attached.
	renderRecorder := httptest.NewRecorder()
	artifactRenderHandler(renderRecorder, httptest.NewRequest(http.MethodGet, payload.URL, nil))
	if renderRecorder.Code != http.StatusOK {
		t.Fatalf("render with minted token status=%d body=%s, want 200", renderRecorder.Code, renderRecorder.Body.String())
	}
	if got := renderRecorder.Body.String(); got != "deck body pending render" {
		t.Fatalf("render body=%q, want the stored deck body", got)
	}
}
