package main

// Deal Room (Wave 14 capstone) — a one-tap, read-only, shareable export of a
// venture package's assembled binder: the studio hands an investor a clean
// linkable page. The EXTERNAL_WRITE approval gate rides HERE, at the share
// surface, not on the package_assembly tool (evals_test.go:537 keeps that tool
// ungated). Flow: a signed-in user REQUESTS a share of a package's binder
// artifact -> it lands PENDING for an admin to approve -> on approve a random
// tokenized read-only URL is minted -> served at GET /deal-room/{token} as
// server-rendered read-only HTML -> the token is revocable.
//
// STORAGE: memory kind "deal_room" (the venture-package precedent — entry.Text
// is the full record JSON), so it rides the existing JSONL durability, boot
// loading, and id dedupe. Records are UI/workspace state: the raw JSON never
// enters Scout search or the client memory timeline (registered in
// isUIStateMemoryKind + the timeline/meeting filters alongside "package").

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	// meetingMemoryKindDealRoom is a Deal Room share record — entry.Text is the
	// full dealRoomRecord JSON (the venture-package precedent). UI state: the
	// record JSON never grounds Scout and never renders in the memory timeline.
	meetingMemoryKindDealRoom = "deal_room"

	// osEventDealRoom is the unified-push kind for Deal Room lifecycle signals
	// (a new pending request for the admin, a minted/live room for the
	// requester). Title-only, like every OS event.
	osEventDealRoom = "deal_room"

	dealRoomStatusPending  = "pending"
	dealRoomStatusActive   = "active"
	dealRoomStatusRejected = "rejected"
	dealRoomStatusRevoked  = "revoked"
)

// dealRoomRecord is one share request/grant. RequestedBy holds the normalized
// requester email (the list-scoping identity); Token is minted only on approve
// and is the public read capability.
type dealRoomRecord struct {
	ID          string `json:"id"`
	PackageID   string `json:"packageId"`
	ArtifactID  string `json:"artifactId"`
	Status      string `json:"status"`
	Token       string `json:"token,omitempty"`
	RequestedBy string `json:"requestedBy"`
	RequestedAt string `json:"requestedAt"`
	ResolvedBy  string `json:"resolvedBy,omitempty"`
	ResolvedAt  string `json:"resolvedAt,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

func encodeDealRoom(record dealRoomRecord) (string, error) {
	raw, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("encode deal room: %w", err)
	}
	return string(raw), nil
}

// decodeDealRoomEntry mirrors decodeVenturePackageEntry: the record is the
// entry text, with entry id backfilling older rows.
func decodeDealRoomEntry(entry meetingMemoryEntry) (dealRoomRecord, bool) {
	if entry.Kind != meetingMemoryKindDealRoom {
		return dealRoomRecord{}, false
	}
	var record dealRoomRecord
	if err := json.Unmarshal([]byte(entry.Text), &record); err != nil {
		return dealRoomRecord{}, false
	}
	if strings.TrimSpace(record.ID) == "" {
		record.ID = entry.ID
	}
	if strings.TrimSpace(record.Status) == "" {
		record.Status = dealRoomStatusPending
	}
	if strings.TrimSpace(record.RequestedAt) == "" && !entry.CreatedAt.IsZero() {
		record.RequestedAt = entry.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	return record, true
}

// dealRoomsSnapshot returns every decodable Deal Room, newest-requested first.
func (app *kanbanBoardApp) dealRoomsSnapshot() []dealRoomRecord {
	if app == nil || app.memory == nil {
		return nil
	}
	records := []dealRoomRecord{}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindDealRoom, 0) {
		if record, ok := decodeDealRoomEntry(entry); ok {
			records = append(records, record)
		}
	}
	sort.SliceStable(records, func(left, right int) bool {
		return records[left].RequestedAt > records[right].RequestedAt
	})
	return records
}

func (app *kanbanBoardApp) dealRoomByID(id string) (dealRoomRecord, bool) {
	if app == nil || app.memory == nil {
		return dealRoomRecord{}, false
	}
	entry, ok := app.memory.entryByKindAndID(meetingMemoryKindDealRoom, strings.TrimSpace(id))
	if !ok {
		return dealRoomRecord{}, false
	}
	return decodeDealRoomEntry(entry)
}

// dealRoomByToken resolves a public token to its record (any status; the
// caller enforces active-only).
func (app *kanbanBoardApp) dealRoomByToken(token string) (dealRoomRecord, bool) {
	token = strings.TrimSpace(token)
	if token == "" || app == nil || app.memory == nil {
		return dealRoomRecord{}, false
	}
	for _, record := range app.dealRoomsSnapshot() {
		if record.Token != "" && record.Token == token {
			return record, true
		}
	}
	return dealRoomRecord{}, false
}

// persistDealRoom writes the record (create or whole-record update), keeping a
// cheap {status, packageId} metadata mirror in sync. Callers hold dealRoomMu.
func (app *kanbanBoardApp) persistDealRoom(record dealRoomRecord, create bool) (dealRoomRecord, error) {
	encoded, err := encodeDealRoom(record)
	if err != nil {
		return dealRoomRecord{}, err
	}
	metadata := map[string]string{
		"status":    record.Status,
		"packageId": record.PackageID,
	}
	if create {
		_, appended, appendErr := app.memory.appendEntry(meetingMemoryKindDealRoom, record.ID, encoded, metadata)
		if appendErr != nil {
			return dealRoomRecord{}, appendErr
		}
		if !appended {
			return dealRoomRecord{}, fmt.Errorf("deal room was not saved")
		}
	} else {
		if _, _, updateErr := app.memory.updateEntryWithMetadata(meetingMemoryKindDealRoom, record.ID, encoded, metadata); updateErr != nil {
			return dealRoomRecord{}, updateErr
		}
	}
	return record, nil
}

// newDealRoomToken mints a url-safe ~32-char capability token from crypto/rand.
func newDealRoomToken() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("mint deal room token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// artifactReadsAsBinder reports whether an artifact is the package's assembled
// binder: the package_assembly tool template / package_binder_v1 contract is
// the strong signal; title/body keywords are the fallback for older rows.
func artifactReadsAsBinder(artifact meetingMemoryEntry) bool {
	if strings.EqualFold(strings.TrimSpace(artifact.Metadata["toolTemplate"]), "package_assembly") {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(artifact.Metadata["artifactContract"]), "package_binder_v1") {
		return true
	}
	hay := strings.ToLower(artifact.Metadata["title"] + "\n" + artifact.Text)
	for _, needle := range []string{"binder", "assembled package", "package assembly", "deal room"} {
		if strings.Contains(hay, needle) {
			return true
		}
	}
	return false
}

// packageOwnsArtifact reports whether artifactID is one of the package's own
// attached artifacts. The membership gate for a client-supplied share target.
func packageOwnsArtifact(record venturePackageRecord, artifactID string) bool {
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return false
	}
	for _, id := range record.ArtifactIDs {
		if strings.TrimSpace(id) == artifactID {
			return true
		}
	}
	return false
}

// resolvePackageBinderArtifactID picks the artifact to share: the newest
// attached artifact that reads as the assembled binder; failing a confident
// match, the newest attached artifact that still exists. "" when the package
// has no resolvable artifact.
func (app *kanbanBoardApp) resolvePackageBinderArtifactID(record venturePackageRecord) string {
	for index := len(record.ArtifactIDs) - 1; index >= 0; index-- {
		artifact, ok := app.osArtifactByID(record.ArtifactIDs[index])
		if ok && artifactReadsAsBinder(artifact) {
			return record.ArtifactIDs[index]
		}
	}
	for index := len(record.ArtifactIDs) - 1; index >= 0; index-- {
		if _, ok := app.osArtifactByID(record.ArtifactIDs[index]); ok {
			return record.ArtifactIDs[index]
		}
	}
	return ""
}

// dealRoomPayload shapes the wire form for the list endpoint. The url path is
// only present while the room is active (the token is the capability).
func (app *kanbanBoardApp) dealRoomPayload(record dealRoomRecord) map[string]any {
	packageName := ""
	if pkg, ok := app.venturePackageByID(record.PackageID); ok {
		packageName = pkg.Name
	}
	payload := map[string]any{
		"id":          record.ID,
		"packageId":   record.PackageID,
		"packageName": packageName,
		"artifactId":  record.ArtifactID,
		"status":      record.Status,
		"requestedBy": record.RequestedBy,
		"requestedAt": record.RequestedAt,
		"resolvedBy":  record.ResolvedBy,
		"resolvedAt":  record.ResolvedAt,
		"reason":      record.Reason,
	}
	if record.Status == dealRoomStatusActive && record.Token != "" {
		payload["url"] = "/deal-room/" + record.Token
	}
	return payload
}

/* ---------- HTTP: authed /assistant/deal-room/* ---------- */

// dealRoomAuthedUser runs the shared /assistant/* preamble (method, origin,
// session, app-nil guards). It returns the signed-in user, or nil after having
// written the error response.
func dealRoomAuthedUser(w http.ResponseWriter, r *http.Request, method string) *userAccount {
	if r.Method != method {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return nil
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return nil
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return nil
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "deal rooms are unavailable")
		return nil
	}
	return user
}

// assistantDealRoomRequestHandler: POST /assistant/deal-room/request. Any
// signed-in user may request a share of a package's binder. Lands PENDING and
// pings the approval admin. Idempotent-ish: an existing pending/active room for
// the same package+artifact is returned rather than duplicated.
func assistantDealRoomRequestHandler(w http.ResponseWriter, r *http.Request) {
	user := dealRoomAuthedUser(w, r, http.MethodPost)
	if user == nil {
		return
	}

	payload := struct {
		PackageID  string `json:"packageId"`
		ArtifactID string `json:"artifactId"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read deal room request")
		return
	}

	packageID := strings.TrimSpace(payload.PackageID)
	if packageID == "" {
		writeAuthError(w, http.StatusBadRequest, "packageId is required")
		return
	}
	pkg, ok := kanbanApp.venturePackageByID(packageID)
	if !ok {
		writeAuthError(w, http.StatusNotFound, "package not found")
		return
	}

	artifactID := strings.TrimSpace(payload.ArtifactID)
	if artifactID != "" {
		// SECURITY: a client-supplied artifactId must belong to THIS package.
		// Existence alone is not enough — without the membership check any
		// signed-in user could request (and, once an admin approved, publish
		// behind an unauthenticated token) an arbitrary unrelated artifact's
		// content. The auto-resolve path below is already package-scoped; this
		// makes the explicit path match it.
		if !packageOwnsArtifact(pkg, artifactID) {
			writeAuthError(w, http.StatusBadRequest, "artifact is not part of this package")
			return
		}
		if _, found := kanbanApp.osArtifactByID(artifactID); !found {
			writeAuthError(w, http.StatusBadRequest, "artifact not found")
			return
		}
	} else {
		artifactID = kanbanApp.resolvePackageBinderArtifactID(pkg)
		if artifactID == "" {
			writeAuthError(w, http.StatusBadRequest, "package has no assembled binder to share; assemble the package first")
			return
		}
	}

	requester := normalizeAccountEmail(user.Email)

	record, existed, err := func() (dealRoomRecord, bool, error) {
		kanbanApp.dealRoomMu.Lock()
		defer kanbanApp.dealRoomMu.Unlock()

		for _, existing := range kanbanApp.dealRoomsSnapshot() {
			if existing.PackageID == packageID && existing.ArtifactID == artifactID &&
				(existing.Status == dealRoomStatusPending || existing.Status == dealRoomStatusActive) {
				return existing, true, nil
			}
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		record := dealRoomRecord{
			ID:          durableTimestampID("deal-room", time.Now()),
			PackageID:   packageID,
			ArtifactID:  artifactID,
			Status:      dealRoomStatusPending,
			RequestedBy: requester,
			RequestedAt: now,
		}
		saved, saveErr := kanbanApp.persistDealRoom(record, true)
		return saved, false, saveErr
	}()
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if !existed {
		// Ping the approval admin: a durable, targeted bell notification plus the
		// title-only OS event on the typed stream. Both admin-only — a pending
		// share is actionable by the admin alone.
		text := fmt.Sprintf("%s requested a Deal Room share of %q — approve or reject in settings.", firstNonEmptyString(participantNameForEmail(requester), requester), pkg.Name)
		if _, notifyErr := kanbanApp.createNotification(artifactLibraryAdminEmail, notificationKindTask, text, osEventDealRoom, "", "", false); notifyErr != nil {
			log.Errorf("Failed to notify admin of deal room request %s: %v", record.ID, notifyErr)
		}
		sendOSEventToUser(artifactLibraryAdminEmail, osEvent{
			Kind:          osEventDealRoom,
			Ref:           record.ID,
			Title:         "Deal Room requested · " + pkg.Name,
			OriginSurface: "deal_room",
			Actor:         firstNonEmptyString(participantNameForEmail(requester), requester),
		})
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"status": record.Status,
		"id":     record.ID,
	})
}

// assistantDealRoomResolveHandler: POST /assistant/deal-room/resolve. ADMIN
// ONLY. approve -> active + mint token; reject -> rejected. Notifies the
// requester either way.
func assistantDealRoomResolveHandler(w http.ResponseWriter, r *http.Request) {
	user := dealRoomAuthedUser(w, r, http.MethodPost)
	if user == nil {
		return
	}
	if !isArtifactApprovalAdmin(user) {
		writeAuthError(w, http.StatusForbidden, "only the approval admin may resolve a Deal Room")
		return
	}

	payload := struct {
		ID     string `json:"id"`
		Action string `json:"action"`
		Reason string `json:"reason"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read deal room resolution")
		return
	}
	action := strings.ToLower(strings.TrimSpace(payload.Action))
	if action != "approve" && action != "reject" {
		writeAuthError(w, http.StatusBadRequest, "action must be approve or reject")
		return
	}
	reason := strings.TrimSpace(payload.Reason)

	record, err := func() (dealRoomRecord, error) {
		kanbanApp.dealRoomMu.Lock()
		defer kanbanApp.dealRoomMu.Unlock()

		record, ok := kanbanApp.dealRoomByID(strings.TrimSpace(payload.ID))
		if !ok {
			return dealRoomRecord{}, fmt.Errorf("deal room not found")
		}
		if record.Status != dealRoomStatusPending {
			return dealRoomRecord{}, fmt.Errorf("deal room is already %s", record.Status)
		}
		record.ResolvedBy = normalizeAccountEmail(user.Email)
		record.ResolvedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if action == "approve" {
			token, tokenErr := newDealRoomToken()
			if tokenErr != nil {
				return dealRoomRecord{}, tokenErr
			}
			record.Status = dealRoomStatusActive
			record.Token = token
			record.Reason = ""
		} else {
			record.Status = dealRoomStatusRejected
			record.Reason = reason
		}
		return kanbanApp.persistDealRoom(record, false)
	}()
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		writeAuthError(w, status, err.Error())
		return
	}

	// Notify the requester of the outcome (durable bell + targeted OS event).
	packageName := ""
	if pkg, ok := kanbanApp.venturePackageByID(record.PackageID); ok {
		packageName = pkg.Name
	}
	if record.Status == dealRoomStatusActive {
		url := "/deal-room/" + record.Token
		text := fmt.Sprintf("Your Deal Room for %q is live: %s", packageName, url)
		if _, notifyErr := kanbanApp.createNotification(record.RequestedBy, notificationKindInfo, text, osEventDealRoom, "", "", false); notifyErr != nil {
			log.Errorf("Failed to notify requester of live deal room %s: %v", record.ID, notifyErr)
		}
		sendOSEventToUser(record.RequestedBy, osEvent{
			Kind:          osEventDealRoom,
			Ref:           record.ID,
			Title:         "Deal Room live · " + packageName,
			OriginSurface: "deal_room",
			Actor:         firstNonEmptyString(participantNameForEmail(record.ResolvedBy), record.ResolvedBy),
		})
	} else {
		text := fmt.Sprintf("Your Deal Room request for %q was declined.", packageName)
		if reason != "" {
			text += " Reason: " + reason
		}
		if _, notifyErr := kanbanApp.createNotification(record.RequestedBy, notificationKindInfo, text, osEventDealRoom, "", "", false); notifyErr != nil {
			log.Errorf("Failed to notify requester of rejected deal room %s: %v", record.ID, notifyErr)
		}
		sendOSEventToUser(record.RequestedBy, osEvent{
			Kind:          osEventDealRoom,
			Ref:           record.ID,
			Title:         "Deal Room declined · " + packageName,
			OriginSurface: "deal_room",
			Actor:         firstNonEmptyString(participantNameForEmail(record.ResolvedBy), record.ResolvedBy),
		})
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"dealRoom": kanbanApp.dealRoomPayload(record),
	})
}

// assistantDealRoomRevokeHandler: POST /assistant/deal-room/revoke. ADMIN
// ONLY. Flips an active room to revoked so the token route 404s.
func assistantDealRoomRevokeHandler(w http.ResponseWriter, r *http.Request) {
	user := dealRoomAuthedUser(w, r, http.MethodPost)
	if user == nil {
		return
	}
	if !isArtifactApprovalAdmin(user) {
		writeAuthError(w, http.StatusForbidden, "only the approval admin may revoke a Deal Room")
		return
	}

	payload := struct {
		ID string `json:"id"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read deal room revoke")
		return
	}

	record, err := func() (dealRoomRecord, error) {
		kanbanApp.dealRoomMu.Lock()
		defer kanbanApp.dealRoomMu.Unlock()

		record, ok := kanbanApp.dealRoomByID(strings.TrimSpace(payload.ID))
		if !ok {
			return dealRoomRecord{}, fmt.Errorf("deal room not found")
		}
		if record.Status == dealRoomStatusRevoked {
			return record, nil
		}
		record.Status = dealRoomStatusRevoked
		// Retire the token so a leaked link can never be re-served.
		record.Token = ""
		return kanbanApp.persistDealRoom(record, false)
	}()
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		writeAuthError(w, status, err.Error())
		return
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"dealRoom": kanbanApp.dealRoomPayload(record),
	})
}

// assistantDealRoomListHandler: GET /assistant/deal-room/list. Admin sees every
// room; a non-admin sees only the rooms they requested. canApprove drives the
// client's approve/reject affordances.
func assistantDealRoomListHandler(w http.ResponseWriter, r *http.Request) {
	user := dealRoomAuthedUser(w, r, http.MethodGet)
	if user == nil {
		return
	}
	admin := isArtifactApprovalAdmin(user)
	viewer := normalizeAccountEmail(user.Email)

	rooms := []map[string]any{}
	for _, record := range kanbanApp.dealRoomsSnapshot() {
		if !admin && record.RequestedBy != viewer {
			continue
		}
		rooms = append(rooms, kanbanApp.dealRoomPayload(record))
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"rooms":      rooms,
		"canApprove": admin,
	})
}

/* ---------- HTTP: public GET /deal-room/{token} ---------- */

// dealRoomPublicHandler serves GET /deal-room/{token} with NO auth — the token
// IS the capability. Not found / not active -> 404 plain page. Active -> a
// self-contained, server-rendered read-only HTML view of the binder.
func dealRoomPublicHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := strings.Trim(strings.TrimPrefix(r.URL.Path, "/deal-room/"), "/")
	if token == "" || strings.Contains(token, "/") || kanbanApp == nil {
		writeDealRoomNotFound(w)
		return
	}

	record, ok := kanbanApp.dealRoomByToken(token)
	if !ok || record.Status != dealRoomStatusActive {
		writeDealRoomNotFound(w)
		return
	}
	artifact, found := kanbanApp.osArtifactByID(record.ArtifactID)
	if !found {
		writeDealRoomNotFound(w)
		return
	}
	packageName := "Venture package"
	stage := ""
	if pkg, ok := kanbanApp.venturePackageByID(record.PackageID); ok {
		if strings.TrimSpace(pkg.Name) != "" {
			packageName = pkg.Name
		}
		stage = pkg.Stage
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(renderDealRoomPage(packageName, stage, artifact.Text)))
}

func writeDealRoomNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>Deal Room not found</title>` +
		`<style>body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:#0f1115;color:#e6e8ee;` +
		`display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}main{text-align:center;padding:2rem}` +
		`h1{font-size:1.4rem;margin:0 0 .5rem}p{color:#9aa3b2;margin:0}</style></head>` +
		`<body><main><h1>This Deal Room is not available</h1><p>The link may have been revoked or is no longer active.</p></main></body></html>`))
}

// renderDealRoomPage assembles the full self-contained read-only page: the
// binder body rendered from minimal, injection-safe Markdown plus a provenance
// appendix. All artifact-derived text is HTML-escaped before formatting.
func renderDealRoomPage(packageName string, stage string, binderBody string) string {
	title := html.EscapeString(strings.TrimSpace(packageName))
	if title == "" {
		title = "Deal Room"
	}
	generated := time.Now().UTC().Format("January 2, 2006 15:04 MST")

	provenance := "<footer class=\"provenance\"><h2>Provenance</h2><ul>"
	if strings.TrimSpace(stage) != "" {
		provenance += "<li><strong>Package stage:</strong> " + html.EscapeString(strings.TrimSpace(stage)) + "</li>"
	}
	provenance += "<li><strong>Assembled by</strong> Bonfire</li>"
	provenance += "<li><strong>Generated:</strong> " + html.EscapeString(generated) + "</li>"
	provenance += "</ul><p class=\"chrome\">Read-only Deal Room · shared by the studio · do not forward beyond your intended recipient.</p></footer>"

	var page strings.Builder
	page.WriteString("<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\">")
	page.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">")
	page.WriteString("<meta name=\"robots\" content=\"noindex, nofollow\">")
	page.WriteString("<title>" + title + " · Deal Room</title>")
	page.WriteString("<style>" + dealRoomPageCSS + "</style></head><body>")
	page.WriteString("<div class=\"sheet\">")
	page.WriteString("<header class=\"masthead\"><span class=\"badge\">Deal Room · read-only</span><h1>" + title + "</h1></header>")
	page.WriteString("<article class=\"binder\">")
	page.WriteString(renderDealRoomBinderHTML(binderBody))
	page.WriteString("</article>")
	page.WriteString(provenance)
	page.WriteString("</div></body></html>")
	return page.String()
}

const dealRoomPageCSS = `:root{color-scheme:light}
*{box-sizing:border-box}
body{margin:0;background:#eef1f6;color:#1a1d24;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;line-height:1.6}
.sheet{max-width:760px;margin:0 auto;padding:2.5rem 1.25rem 4rem}
.masthead{border-bottom:1px solid #d5dae4;padding-bottom:1.25rem;margin-bottom:1.75rem}
.badge{display:inline-block;font-size:.72rem;letter-spacing:.08em;text-transform:uppercase;color:#7a45ff;background:#efe9ff;padding:.25rem .6rem;border-radius:999px;font-weight:600}
.masthead h1{font-size:1.9rem;margin:.75rem 0 0;line-height:1.2}
.binder h1{font-size:1.5rem;margin:1.75rem 0 .75rem}
.binder h2{font-size:1.2rem;margin:1.5rem 0 .5rem}
.binder h3{font-size:1.02rem;margin:1.25rem 0 .4rem;color:#37404f}
.binder p{margin:.75rem 0}
.binder ul{margin:.6rem 0;padding-left:1.4rem}
.binder li{margin:.3rem 0}
.binder{overflow-x:auto}
.provenance{margin-top:2.5rem;padding-top:1.25rem;border-top:1px solid #d5dae4;font-size:.86rem;color:#5c6472}
.provenance h2{font-size:.78rem;letter-spacing:.08em;text-transform:uppercase;color:#8a92a2;margin:0 0 .5rem}
.provenance ul{list-style:none;padding:0;margin:0}
.provenance li{margin:.2rem 0}
.provenance .chrome{margin-top:.9rem;font-style:italic;color:#8a92a2}`

// renderDealRoomBinderHTML converts the binder Markdown to a minimal, safe HTML
// subset. SECURITY: every span of artifact-derived text is html.EscapeString-ed
// BEFORE it is wrapped in a tag, so injected HTML/script can never execute —
// only leading #/##/### headings, "- " list items, and blank-line-separated
// paragraph blocks are given structure.
func renderDealRoomBinderHTML(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	lines := strings.Split(body, "\n")

	var out strings.Builder
	var paragraph []string
	var list []string

	flushParagraph := func() {
		if len(paragraph) > 0 {
			out.WriteString("<p>" + strings.Join(paragraph, " ") + "</p>")
			paragraph = paragraph[:0]
		}
	}
	flushList := func() {
		if len(list) > 0 {
			out.WriteString("<ul>")
			for _, item := range list {
				out.WriteString("<li>" + item + "</li>")
			}
			out.WriteString("</ul>")
			list = list[:0]
		}
	}

	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		switch {
		case trimmed == "":
			flushList()
			flushParagraph()
		case strings.HasPrefix(trimmed, "### "):
			flushList()
			flushParagraph()
			out.WriteString("<h3>" + html.EscapeString(strings.TrimSpace(trimmed[4:])) + "</h3>")
		case strings.HasPrefix(trimmed, "## "):
			flushList()
			flushParagraph()
			out.WriteString("<h2>" + html.EscapeString(strings.TrimSpace(trimmed[3:])) + "</h2>")
		case strings.HasPrefix(trimmed, "# "):
			flushList()
			flushParagraph()
			out.WriteString("<h1>" + html.EscapeString(strings.TrimSpace(trimmed[2:])) + "</h1>")
		case strings.HasPrefix(trimmed, "- "):
			flushParagraph()
			list = append(list, html.EscapeString(strings.TrimSpace(trimmed[2:])))
		default:
			flushList()
			paragraph = append(paragraph, html.EscapeString(trimmed))
		}
	}
	flushList()
	flushParagraph()
	return out.String()
}
