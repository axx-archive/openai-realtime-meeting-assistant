package main

// Tokened per-artifact share links (packaging OS §4 viewer item 3, Wave 3
// item 14) — the "send the deck to the investor" capability. A signed-in user
// mints a random capability token for ONE artifact; GET /a/<token> serves that
// artifact read-only at full fidelity with NO session: an html_deck rides the
// sandboxed render path's exact CSP, a pdf asset streams from the blob store,
// and everything else goes through the injection-safe server renderer (the
// Deal Room precedent — deal_room.go:569 — with the same random-token-as-
// capability model: revocation needs server-side state anyway, so the record
// IS the source of truth and no HMAC survives a revoke).
//
// GATING, server-side at both ends (spec: "status=final + human approval"):
// minting requires the artifact to be human-approved — status=approved, or
// the admin approve action's durable humanApprovedAt stamp on landed
// (complete/published) work — and the PUBLIC route re-checks on every open,
// so pulling an artifact's approval kills every live link instantly.
//
// STORAGE: data/share-links.json beside the memory store (the users.json/
// sessions.json/codex-runner-jobs precedent) — share links are pure workspace
// state and never belong in Scout recall, and this file needs no change to
// memory.go's kind registry. All mutations serialize on shareLinksMu.
//
// SIGNALS (§5 capture): a public open appends event=share_opened via
// recordSignalEvent — "the investor opened the deck" — debounced to at most
// one signal per link per hour (the producer is unauthenticated, so a
// crawler must never grow the JSONL store per hit); the cheap
// openCount/lastOpenedAt stamp on the record still counts every open for the
// mint UI. Dwell tracking is deliberately absent: nothing about it is
// trivially cheap on a static response. KEYLESS: pure disk + crypto/rand, no
// model calls, no sidecar.

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	shareLinkStatusActive  = "active"
	shareLinkStatusRevoked = "revoked"

	// shareLinkDefaultExpiryDays / shareLinkMaxExpiryDays bound the mint
	// request's expiresDays: a share link is a courtesy window, not a
	// permanent publication.
	shareLinkDefaultExpiryDays = 7
	shareLinkMaxExpiryDays     = 90

	// Signal event names owned by this wiring (signals.go owns the Wave-1
	// vocabulary; these two are the Wave-3 share/export seams).
	signalEventShareOpened = "share_opened"
	signalEventPDFExported = "pdf_exported"
)

// shareLinkRecord is one minted capability. Token is the public read
// capability; it is retired (cleared) on revoke — the deal-room precedent —
// so a leaked link can never be re-served.
type shareLinkRecord struct {
	ID         string `json:"id"`
	ArtifactID string `json:"artifactId"`
	// Token is legacy-only. Rows containing plaintext credentials fail closed;
	// new credentials are returned once and only TokenHash is persisted.
	Token         string `json:"token,omitempty"`
	TokenHash     string `json:"tokenHash,omitempty"`
	RawToken      string `json:"-"`
	TenantID      string `json:"tenantId,omitempty"`
	ObjectType    string `json:"objectType,omitempty"`
	Revision      int    `json:"revision,omitempty"`
	ContentDigest string `json:"contentDigest,omitempty"`
	Action        string `json:"action,omitempty"`
	Status        string `json:"status"`
	CreatedBy     string `json:"createdBy"`
	CreatedAt     string `json:"createdAt"`
	ExpiresAt     string `json:"expiresAt"`
	RevokedBy     string `json:"revokedBy,omitempty"`
	RevokedAt     string `json:"revokedAt,omitempty"`
	OpenCount     int    `json:"openCount,omitempty"`
	LastOpenedAt  string `json:"lastOpenedAt,omitempty"`
}

// shareLinksMu serializes every read-modify-write of the share-links file.
// Package-level (not an app field) so this file stays self-contained.
var shareLinksMu sync.Mutex

func shareLinksPath() string {
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "share-links.json")
}

// loadShareLinks reads the full record list; a missing file is an empty
// store, and a corrupt file is an error (never silently dropped links —
// tokens are capabilities).
func loadShareLinks() ([]shareLinkRecord, error) {
	raw, err := os.ReadFile(shareLinksPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read share links: %w", err)
	}
	var records []shareLinkRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("decode share links: %w", err)
	}
	return records, nil
}

func saveShareLinks(records []shareLinkRecord) error {
	return writeJSONFileAtomically(shareLinksPath(), "share links", records)
}

// newShareLinkToken mints a url-safe ~32-char capability token from
// crypto/rand (the newDealRoomToken precedent).
func newShareLinkToken() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("mint share link token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// Human-approval stamp keys: the admin approve action
// (artifactActionHandler, codex_runner_queue.go) writes these via the
// metadata-only path, and NOTHING else writes or clears them — unlike
// reviewGate/status, which the job machinery overwrites as work progresses,
// so this is the durable "a human signed off" record share links gate on.
const (
	artifactHumanApprovedAtKey = "humanApprovedAt"
	artifactHumanApprovedByKey = "humanApprovedBy"
)

// stampArtifactHumanApproval records the durable human-approval marker on an
// artifact when an approval admin approves it. Log-and-continue: the stamp
// unlocks sharing, it never blocks the approval itself.
func (app *kanbanBoardApp) stampArtifactHumanApproval(artifactID string, approver string) {
	if app == nil || app.memory == nil {
		return
	}
	if _, _, err := app.memory.updateOSArtifactMetadata(artifactID, map[string]string{
		artifactHumanApprovedAtKey: time.Now().UTC().Format(time.RFC3339Nano),
		artifactHumanApprovedByKey: canonicalRoomActorName(approver),
	}); err != nil {
		log.Errorf("Failed to stamp human approval on artifact %s: %v", artifactID, err)
	}
}

// artifactShareEligible is the server-side status gate (spec item 14:
// "final+approved gating"). Eligible when the artifact carries
// status=approved (the vocabulary's explicit human-approved-for-external
// value), or when the codebase's REAL human-approval record is present — the
// admin approve stamp — AND the work has landed (complete/published), so an
// approved-but-still-running external write never leaks early. The spec's
// untracked "final" alias is deliberately NOT honored: nothing produces it
// today, and a future gate-passed-but-unapproved "final" must not bypass the
// human-approval requirement.
func artifactShareEligible(entry meetingMemoryEntry) bool {
	if artifactStatus(entry) == artifactStatusApproved {
		return true
	}
	if strings.TrimSpace(entry.Metadata[artifactHumanApprovedAtKey]) == "" {
		return false
	}
	switch artifactStatus(entry) {
	case artifactStatusComplete, artifactStatusPublished:
		return true
	}
	return false
}

func shareLinkExpired(record shareLinkRecord, now time.Time) bool {
	expires, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(record.ExpiresAt))
	if err != nil {
		// An unparseable expiry fails CLOSED: a capability with no readable
		// window must not live forever.
		return true
	}
	return now.After(expires)
}

func shareLinkLive(record shareLinkRecord, now time.Time) bool {
	return record.Status == shareLinkStatusActive && record.Token == "" && validShareLinkBinding(record) && !shareLinkExpired(record, now)
}

func artifactCapabilityDigest(entry meetingMemoryEntry) string {
	assets := artifactAssets(entry)
	sort.Slice(assets, func(i, j int) bool {
		if assets[i].Kind != assets[j].Kind {
			return assets[i].Kind < assets[j].Kind
		}
		if assets[i].Ref != assets[j].Ref {
			return assets[i].Ref < assets[j].Ref
		}
		if assets[i].Mime != assets[j].Mime {
			return assets[i].Mime < assets[j].Mime
		}
		return assets[i].Name < assets[j].Name
	})
	canonical, _ := json.Marshal(struct {
		Title           string          `json:"title"`
		Body            string          `json:"body"`
		Type            string          `json:"type"`
		Kind            string          `json:"kind"`
		Assets          []artifactAsset `json:"assets"`
		GateOutcome     string          `json:"gateOutcome"`
		GoalPlan        string          `json:"goalPlan"`
		RubricScores    string          `json:"rubricScores"`
		RoomID          string          `json:"roomId"`
		SittingID       string          `json:"sittingId"`
		MediaGeneration uint64          `json:"mediaGeneration"`
	}{Title: firstNonEmptyString(entry.Metadata["title"], entry.Metadata["threadQuery"]), Body: entry.Text,
		Type: artifactType(entry), Kind: firstNonEmptyString(entry.Metadata["kind"], entry.Kind), Assets: assets,
		GateOutcome: entry.Metadata["gateOutcome"], GoalPlan: entry.Metadata["goalPlan"], RubricScores: entry.Metadata["rubricScores"],
		RoomID: normalizeRoomID(entry.Metadata["roomId"]), SittingID: firstNonEmptyString(strings.TrimSpace(entry.Metadata["sittingId"]), strings.TrimSpace(entry.Metadata["meetingId"])),
		MediaGeneration: artifactAuthorizationHeaderFromEntry(entry).MediaGeneration})
	digest := sha256.Sum256(canonical)
	return fmt.Sprintf("%x", digest[:])
}

func validShareLinkBinding(record shareLinkRecord) bool {
	return record.TokenHash != "" && isHexDigest(record.TokenHash) && record.TenantID == canonicalArtifactTenantID() && record.ObjectType == "artifact" &&
		record.ArtifactID != "" && record.Revision >= 1 && isHexDigest(record.ContentDigest) && record.Action == "read_content"
}

// shareLinkByToken resolves a public token via hash-then-constant-time
// comparison per candidate (the validArchiveKey pattern), so neither token
// length nor per-character matching leaks.
func shareLinkByToken(token string) (shareLinkRecord, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return shareLinkRecord{}, false
	}
	records, err := loadShareLinks()
	if err != nil {
		log.Errorf("Failed to load share links: %v", err)
		return shareLinkRecord{}, false
	}
	providedHash := sha256.Sum256([]byte(token))
	for _, record := range records {
		if !shareLinkLive(record, time.Now().UTC()) {
			continue
		}
		candidateHash, err := hex.DecodeString(record.TokenHash)
		if err == nil && subtle.ConstantTimeCompare(providedHash[:], candidateHash) == 1 {
			return record, true
		}
	}
	return shareLinkRecord{}, false
}

// shareLinkPayload shapes the wire form for the authed list/mint endpoints.
// The url is present only while the link is live (the token is the
// capability, so a revoked/expired row exposes history without authority).
func shareLinkPayload(record shareLinkRecord, now time.Time) map[string]any {
	payload := map[string]any{
		"id":           record.ID,
		"artifactId":   record.ArtifactID,
		"status":       record.Status,
		"createdBy":    record.CreatedBy,
		"createdAt":    record.CreatedAt,
		"expiresAt":    record.ExpiresAt,
		"openCount":    record.OpenCount,
		"lastOpenedAt": record.LastOpenedAt,
		"expired":      record.Status == shareLinkStatusActive && shareLinkExpired(record, now),
	}
	if shareLinkLive(record, now) && record.RawToken != "" {
		payload["url"] = "/a/" + record.RawToken
	}
	return payload
}

/* ---------- HTTP: authed /artifacts/share (mint / list / revoke) ---------- */

// artifactShareHandler serves the session-gated share-link management verbs
// on one route, its /artifacts neighbors' preamble on each:
//   - POST   {artifactId, expiresDays} mints a link (approved artifacts only)
//   - GET    ?artifactId=...           lists that artifact's links
//   - DELETE {id}                      revokes (creator or approval admin)
func artifactShareHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet && r.Method != http.MethodDelete {
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
		writeAuthError(w, http.StatusServiceUnavailable, "share links are unavailable")
		return
	}

	switch r.Method {
	case http.MethodPost:
		mintShareLinkHandler(w, r, user)
	case http.MethodGet:
		listShareLinksHandler(w, r, user)
	case http.MethodDelete:
		revokeShareLinkHandler(w, r, user)
	}
}

func mintShareLinkHandler(w http.ResponseWriter, r *http.Request, user *userAccount) {
	payload := struct {
		ArtifactID  string `json:"artifactId"`
		ExpiresDays int    `json:"expiresDays"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read share link request")
		return
	}

	artifact, found := authorizedArtifactForActions(r.Context(), user, strings.TrimSpace(payload.ArtifactID), ACLReadMetadata, ACLShare, ACLReadContent)
	if !found {
		writeAuthError(w, http.StatusNotFound, "artifact not found")
		return
	}
	// SERVER-SIDE STATUS GATE (spec item 14): only a human-approved artifact
	// may go behind an unauthenticated token — client affordances are not the
	// enforcement point.
	if !artifactShareEligible(artifact) {
		writeAuthError(w, http.StatusBadRequest, "share links need an approved artifact — current status is "+artifactStatus(artifact))
		return
	}

	expiresDays := payload.ExpiresDays
	if expiresDays <= 0 {
		expiresDays = shareLinkDefaultExpiryDays
	}
	if expiresDays > shareLinkMaxExpiryDays {
		expiresDays = shareLinkMaxExpiryDays
	}

	token, err := newShareLinkToken()
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now().UTC()
	record := shareLinkRecord{
		ID:         durableTimestampID("share-link", now),
		ArtifactID: artifact.ID,
		TokenHash:  fmt.Sprintf("%x", sha256.Sum256([]byte(token))),
		RawToken:   token,
		TenantID:   canonicalArtifactTenantID(), ObjectType: "artifact", Revision: artifactVersion(artifact),
		ContentDigest: artifactCapabilityDigest(artifact), Action: "read_content",
		Status:    shareLinkStatusActive,
		CreatedBy: normalizeAccountEmail(user.Email),
		CreatedAt: now.Format(time.RFC3339Nano),
		ExpiresAt: now.AddDate(0, 0, expiresDays).Format(time.RFC3339Nano),
	}

	shareLinksMu.Lock()
	defer shareLinksMu.Unlock()
	records, err := loadShareLinks()
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	records = append(records, record)
	if err := saveShareLinks(records); err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"link": shareLinkPayload(record, now),
	})
}

func listShareLinksHandler(w http.ResponseWriter, r *http.Request, user *userAccount) {
	artifactID := strings.TrimSpace(r.URL.Query().Get("artifactId"))
	if artifactID == "" {
		writeAuthError(w, http.StatusBadRequest, "artifactId is required")
		return
	}
	header, found := kanbanApp.memory.artifactAuthorizationHeaderByID(artifactID)
	if !found || !artifactHeaderAuthorized(r.Context(), user, ACLReadMetadata, header) || !artifactHeaderAuthorized(r.Context(), user, ACLShare, header) {
		writeAuthError(w, http.StatusNotFound, "artifact not found")
		return
	}
	records, err := loadShareLinks()
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now().UTC()
	links := []map[string]any{}
	for _, record := range records {
		if artifactID != "" && record.ArtifactID != artifactID {
			continue
		}
		links = append(links, shareLinkPayload(record, now))
	}
	sort.SliceStable(links, func(left, right int) bool {
		return fmt.Sprint(links[left]["createdAt"]) > fmt.Sprint(links[right]["createdAt"])
	})
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"links": links,
	})
}

func revokeShareLinkHandler(w http.ResponseWriter, r *http.Request, user *userAccount) {
	payload := struct {
		ID string `json:"id"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read share link revoke")
		return
	}
	id := strings.TrimSpace(payload.ID)
	if id == "" {
		writeAuthError(w, http.StatusBadRequest, "id is required")
		return
	}

	shareLinksMu.Lock()
	defer shareLinksMu.Unlock()
	records, err := loadShareLinks()
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now().UTC()
	for index, record := range records {
		if record.ID != id {
			continue
		}
		header, found := kanbanApp.memory.artifactAuthorizationHeaderByID(record.ArtifactID)
		if !found || !artifactHeaderAuthorized(r.Context(), user, ACLReadMetadata, header) || !artifactHeaderAuthorized(r.Context(), user, ACLShare, header) {
			writeAuthError(w, http.StatusNotFound, "share link not found")
			return
		}
		// The minter revokes their own link; the approval admin revokes any
		// (the same authority that approves artifacts for external shipping).
		if record.CreatedBy != normalizeAccountEmail(user.Email) && !isArtifactApprovalAdmin(user) {
			writeAuthError(w, http.StatusForbidden, "only the link's creator or the approval admin may revoke it")
			return
		}
		if record.Status != shareLinkStatusRevoked {
			record.Status = shareLinkStatusRevoked
			// Retire the token so a leaked link can never be re-served (the
			// deal-room precedent).
			record.Token = ""
			record.TokenHash = ""
			record.RevokedBy = normalizeAccountEmail(user.Email)
			record.RevokedAt = now.Format(time.RFC3339Nano)
			records[index] = record
			if err := saveShareLinks(records); err != nil {
				writeAuthError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok":   true,
			"link": shareLinkPayload(record, now),
		})
		return
	}
	writeAuthError(w, http.StatusNotFound, "share link not found")
}

/* ---------- HTTP: public GET /a/<token> ---------- */

// shareLinkOpenSignalInterval debounces the share_opened signal per link:
// this producer is a PUBLIC unauthenticated route, so a crawler or a
// link-prefetching mail client must never grow the RAM-held JSONL store one
// entry per hit. The cheap openCount/lastOpenedAt stamp still counts every
// open for the mint UI; at most one signal per link per hour reaches memory.
const shareLinkOpenSignalInterval = time.Hour

// recordShareLinkOpen stamps openCount/lastOpenedAt on the record for the
// mint UI (never fails the serve) and logs the §5 open signal at most once
// per shareLinkOpenSignalInterval per link, keyed on the lastOpenedAt stamp
// the same rewrite already maintains.
func recordShareLinkOpen(record shareLinkRecord, artifact meetingMemoryEntry) {
	now := time.Now().UTC()
	recordSignal := true

	shareLinksMu.Lock()
	records, err := loadShareLinks()
	if err != nil {
		// Fail CLOSED on the signal: with the debounce state unreadable, a
		// crawler on this unauthenticated route must not grow the RAM-held
		// store one entry per hit.
		recordSignal = false
		log.Errorf("Failed to stamp share link open %s: %v", record.ID, err)
	} else {
		for index, existing := range records {
			if existing.ID != record.ID {
				continue
			}
			if last, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(existing.LastOpenedAt)); parseErr == nil && now.Sub(last) < shareLinkOpenSignalInterval {
				recordSignal = false
			}
			records[index].OpenCount++
			records[index].LastOpenedAt = now.Format(time.RFC3339Nano)
			if err := saveShareLinks(records); err != nil {
				log.Errorf("Failed to stamp share link open %s: %v", record.ID, err)
			}
			break
		}
	}
	shareLinksMu.Unlock()

	if !recordSignal {
		return
	}
	kanbanApp.recordSignalEvent("external", signalEventShareOpened, signalValenceNeutral, artifact.ID, artifact.Metadata["packageId"], map[string]string{
		"shareId":      record.ID,
		"artifactType": artifactType(artifact),
	})
}

// firstArtifactAssetOfKind picks the newest asset of one kind (assets append
// chronologically, so the last match is the latest export).
func firstArtifactAssetOfKind(artifact meetingMemoryEntry, kind string) (artifactAsset, bool) {
	assets := artifactAssets(artifact)
	for index := len(assets) - 1; index >= 0; index-- {
		if assets[index].Kind == kind {
			return assets[index], true
		}
	}
	return artifactAsset{}, false
}

// shareLinkPublicHandler serves GET /a/<token> with NO session — the token IS
// the capability (the dealRoomPublicHandler precedent). Every miss (unknown,
// revoked, expired, artifact gone, approval pulled) is the same 404 page so
// the route enumerates nothing. Type dispatch at full fidelity:
//   - html_deck → the sandboxed render path's exact CSP (sandbox
//     allow-scripts server-side: the deck runs with a null origin and can
//     never ride anything on the app origin)
//   - pdf → the newest pdf asset streamed inline from the blob store
//   - everything else → the injection-safe server renderer
func shareLinkPublicHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := strings.Trim(strings.TrimPrefix(r.URL.Path, "/a/"), "/")
	if token == "" || strings.Contains(token, "/") || kanbanApp == nil {
		writeShareLinkNotFound(w)
		return
	}

	record, ok := shareLinkByToken(token)
	if !ok || !shareLinkLive(record, time.Now().UTC()) {
		writeShareLinkNotFound(w)
		return
	}
	artifact, found := kanbanApp.osArtifactByID(record.ArtifactID)
	// Re-check the status gate on EVERY open: pulling an artifact's approval
	// revokes its live links without touching the records.
	if !found || !artifactShareEligible(artifact) || artifactVersion(artifact) != record.Revision || artifactCapabilityDigest(artifact) != record.ContentDigest {
		writeShareLinkNotFound(w)
		return
	}

	boundArtifact := cloneMemoryEntry(artifact)
	recordShareLinkOpen(record, boundArtifact)

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	switch artifactType(boundArtifact) {
	case artifactTypeHTMLDeck:
		// The sandboxed render path's pinned policy, verbatim: full deck
		// fidelity, zero network reach, opaque origin even opened top-level.
		w.Header().Set("Content-Security-Policy", artifactRenderCSP)
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := w.Write([]byte(boundArtifact.Text)); err != nil {
			log.Errorf("Failed to serve shared deck %s: %v", artifact.ID, err)
		}
	case artifactTypePDF:
		asset, hasPDF := firstArtifactAssetOfKind(boundArtifact, "pdf")
		if !hasPDF {
			writeShareLinkNotFound(w)
			return
		}
		data, _, err := getBlob(asset.Ref)
		if err != nil {
			log.Errorf("Failed to read shared pdf blob %s: %v", asset.Ref, err)
			writeShareLinkNotFound(w)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", blobDownloadFilename(asset.Name, asset.Ref)))
		if _, err := w.Write(data); err != nil {
			log.Errorf("Failed to serve shared pdf %s: %v", artifact.ID, err)
		}
	default:
		// markdown/image/bundle: the injection-safe server renderer — every
		// artifact-derived span is escaped before it gets structure.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := w.Write([]byte(renderSharedArtifactPage(boundArtifact))); err != nil {
			log.Errorf("Failed to serve shared artifact %s: %v", artifact.ID, err)
		}
	}
}

func writeShareLinkNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>Link not available</title>` +
		`<style>body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:#0f1115;color:#e6e8ee;` +
		`display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}main{text-align:center;padding:2rem}` +
		`h1{font-size:1.4rem;margin:0 0 .5rem}p{color:#9aa3b2;margin:0}</style></head>` +
		`<body><main><h1>This link is not available</h1><p>It may have expired or been revoked.</p></main></body></html>`))
}

// renderSharedArtifactPage assembles the read-only page for non-deck,
// non-pdf artifacts: the Deal Room page chassis (same CSS, same escaped
// minimal-Markdown renderer) under the artifact's own title.
func renderSharedArtifactPage(artifact meetingMemoryEntry) string {
	title := html.EscapeString(strings.TrimSpace(artifact.Metadata["title"]))
	if title == "" {
		title = "Shared artifact"
	}
	generated := time.Now().UTC().Format("January 2, 2006 15:04 MST")

	var page strings.Builder
	page.WriteString("<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\">")
	page.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">")
	page.WriteString("<meta name=\"robots\" content=\"noindex, nofollow\">")
	page.WriteString("<title>" + title + " · Bonfire</title>")
	page.WriteString("<style>" + dealRoomPageCSS + "</style></head><body>")
	page.WriteString("<div class=\"sheet\">")
	page.WriteString("<header class=\"masthead\"><span class=\"badge\">Shared · read-only</span><h1>" + title + "</h1></header>")
	page.WriteString("<article class=\"binder\">")
	page.WriteString(renderDealRoomBinderHTML(artifact.Text))
	page.WriteString("</article>")
	page.WriteString("<footer class=\"provenance\"><ul><li><strong>Assembled by</strong> Bonfire</li>")
	page.WriteString("<li><strong>Generated:</strong> " + html.EscapeString(generated) + "</li></ul>")
	page.WriteString("<p class=\"chrome\">Read-only share · do not forward beyond your intended recipient.</p></footer>")
	page.WriteString("</div></body></html>")
	return page.String()
}
