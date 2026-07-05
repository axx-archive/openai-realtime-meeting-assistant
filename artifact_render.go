package main

// Sandboxed HTML render route (packaging OS §4 viewer item 1, Wave 2 item 9) —
// the highest fidelity-per-line-of-code build in the plan. An HTML deck stored
// as an os_artifact today displays as escaped source through the
// injection-safe markdown renderer; this route serves the SAME body as real
// text/html so the client can show it in <iframe sandbox="allow-scripts">
// (never allow-same-origin).
//
// SECURITY MODEL, per the spec exactly:
//   - The render route carries NO session-cookie authority: it never calls
//     userFromRequest. Even if a hostile deck could reach same-origin
//     endpoints (it can't — no allow-same-origin, plus the CSP below), the
//     route itself grants nothing a leaked URL wouldn't.
//   - Access is a short-lived per-artifact HMAC token `t`, minted by the
//     session-gated GET /artifacts/render-token neighbor. Precedent: the
//     archive token pair (participants.go archiveAccessToken/validArchiveKey,
//     served by meetingArchiveHandler) — same server secret, domain-separated
//     prefix, constant-time comparison; expiry is bound into the MAC so a
//     token cannot outlive its window.
//   - The response CSP is locked down (no network sources at all): the
//     packaging deck template is fully self-contained, so inline style/script
//     plus data: images/media suffice. X-Frame-Options SAMEORIGIN keeps the
//     route embeddable only by the OS itself.
//
// Non-HTML artifacts 404 here — the normal escaped viewer handles them; do
// NOT loosen that renderer (spec §4 item 4).

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// artifactRenderCSP is the pinned sandbox policy. default-src 'none'
	// blocks every network fetch class; the deck template is self-contained.
	// `sandbox allow-scripts` enforces the opaque origin SERVER-SIDE: even a
	// top-level navigation to this route (pasted/leaked token URL, or any
	// client affordance that escapes the viewer iframe) runs the deck with a
	// null origin — it can never read the app origin's DOM or ride the session
	// cookie into same-origin endpoints. `form-action 'none'` closes the one
	// hole default-src does NOT cover (form-action has no default-src
	// fallback): a hostile deck auto-submitting a same-origin form.
	artifactRenderCSP = "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:; media-src data:; form-action 'none'; sandbox allow-scripts"

	// artifactRenderTokenTTL bounds how long a minted render token lives. The
	// viewer mints per open, so the window only needs to cover one viewing
	// session; a leaked URL goes stale on its own.
	artifactRenderTokenTTL = 15 * time.Minute

	artifactRenderTokenPrefix = "bonfire-artifact-render:"
)

// artifactIsHTMLDocument decides whether an artifact is served by the
// sandboxed render route: declared metadata type=html_deck, or a body that
// starts (after whitespace) as an HTML document. Markdown that merely mentions
// an <html> tag mid-body stays on the escaped renderer.
func artifactIsHTMLDocument(artifact meetingMemoryEntry) bool {
	if strings.TrimSpace(artifact.Metadata["type"]) == "html_deck" {
		return true
	}
	body := strings.ToLower(strings.TrimSpace(artifact.Text))
	return strings.HasPrefix(body, "<!doctype html") || strings.HasPrefix(body, "<html")
}

// artifactRenderTokenMAC derives the keyed digest for one artifact + expiry
// pair. The expiry is inside the MAC, so tampering with either query value
// invalidates the token. Reuses the archive token secret with a distinct
// domain prefix (participants.go precedent): one lazily-created server
// secret, no cross-protocol token replay.
func artifactRenderTokenMAC(artifactID string, expiresUnix string) string {
	mac := hmac.New(sha256.New, archiveTokenSecret())
	mac.Write([]byte(artifactRenderTokenPrefix + strings.TrimSpace(artifactID) + ":" + expiresUnix))
	return hex.EncodeToString(mac.Sum(nil))
}

// mintArtifactRenderToken issues a render token valid until expires, encoded
// as "<unix-expiry>.<hex-mac>" so validation needs no server-side state.
func mintArtifactRenderToken(artifactID string, expires time.Time) string {
	expiresUnix := strconv.FormatInt(expires.Unix(), 10)
	return expiresUnix + "." + artifactRenderTokenMAC(artifactID, expiresUnix)
}

// validArtifactRenderToken accepts only an unexpired token whose MAC matches
// this artifact, compared in constant time (hash-then-compare, the
// validArchiveKey pattern, so attacker-controlled lengths leak nothing).
func validArtifactRenderToken(artifactID string, token string, now time.Time) bool {
	expiresUnix, macHex, ok := strings.Cut(strings.TrimSpace(token), ".")
	if !ok {
		return false
	}
	expires, err := strconv.ParseInt(expiresUnix, 10, 64)
	if err != nil || now.Unix() > expires {
		return false
	}

	providedHash := sha256.Sum256([]byte(macHex))
	expectedHash := sha256.Sum256([]byte(artifactRenderTokenMAC(artifactID, expiresUnix)))

	return subtle.ConstantTimeCompare(providedHash[:], expectedHash[:]) == 1
}

// artifactRenderTokenHandler serves GET /artifacts/render-token?id=... —
// session-gated exactly like its /artifacts neighbors (origin check, signed-in
// user, app availability), because the token it mints is the entire authority
// of the render route. 404s mirror the render route's contract so the client
// never holds a token that can only fail.
func artifactRenderTokenHandler(w http.ResponseWriter, r *http.Request) {
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
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "artifacts are unavailable")
		return
	}

	artifact, found := kanbanApp.osArtifactByID(strings.TrimSpace(r.URL.Query().Get("id")))
	if !found {
		writeAuthError(w, http.StatusNotFound, "artifact not found")
		return
	}
	if !artifactIsHTMLDocument(artifact) {
		writeAuthError(w, http.StatusNotFound, "artifact is not an html document")
		return
	}

	expires := time.Now().Add(artifactRenderTokenTTL)
	token := mintArtifactRenderToken(artifact.ID, expires)
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"token":     token,
		"url":       "/artifacts/render?id=" + url.QueryEscape(artifact.ID) + "&t=" + url.QueryEscape(token),
		"expiresAt": expires.UTC().Format(time.RFC3339),
	})
}

// artifactRenderHandler serves GET /artifacts/render?id=...&t=... — the
// artifact body as a real text/html document under the pinned sandbox CSP.
// Deliberately no session lookup on this path (see the file header); the
// token IS the credential. Missing credentials are 401, a wrong or expired
// token is 403, and anything that is not an HTML document is 404 for the
// normal viewer to handle.
func artifactRenderHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	artifactID := strings.TrimSpace(r.URL.Query().Get("id"))
	token := strings.TrimSpace(r.URL.Query().Get("t"))
	if artifactID == "" || token == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Token before lookup: an invalid credential learns nothing about which
	// artifact ids exist.
	if !validArtifactRenderToken(artifactID, token, time.Now()) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if kanbanApp == nil {
		http.Error(w, "artifacts are unavailable", http.StatusServiceUnavailable)
		return
	}

	artifact, found := kanbanApp.osArtifactByID(artifactID)
	if !found || !artifactIsHTMLDocument(artifact) {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Security-Policy", artifactRenderCSP)
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write([]byte(artifact.Text)); err != nil {
		log.Errorf("Failed to serve rendered artifact %s: %v", artifact.ID, err)
	}
}
