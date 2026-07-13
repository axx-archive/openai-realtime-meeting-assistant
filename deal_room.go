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
//
// GALLERY (packaging OS §4 "Deal Room becomes an artifact gallery", Wave 4
// item 19): the binder narrative stays as the escaped cover; below it the
// package's final/approved artifacts render as a gallery — title, type badge,
// version, gate outcome + rubric score (the Wave 3 provenance accessors) —
// with full-fidelity links minted at page-build time. Eligibility is
// artifactShareEligible, the SAME server-side final+approved rule share links
// enforce, so a Deal Room token never exposes unapproved work.

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
	"net/url"
	"sort"
	"strconv"
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
	ID                   string                             `json:"id"`
	PackageID            string                             `json:"packageId"`
	ArtifactID           string                             `json:"artifactId"`
	Status               string                             `json:"status"`
	Token                string                             `json:"token,omitempty"` // legacy plaintext: never accepted
	TokenHash            string                             `json:"tokenHash,omitempty"`
	RawToken             string                             `json:"-"`
	TenantID             string                             `json:"tenantId,omitempty"`
	Revision             int                                `json:"revision,omitempty"`
	ContentDigest        string                             `json:"contentDigest,omitempty"`
	Action               string                             `json:"action,omitempty"`
	ExpiresAt            string                             `json:"expiresAt,omitempty"`
	BoundArtifacts       map[string]dealRoomArtifactBinding `json:"boundArtifacts,omitempty"`
	PackageNameSnapshot  string                             `json:"packageNameSnapshot,omitempty"`
	PackageStageSnapshot string                             `json:"packageStageSnapshot,omitempty"`
	RequestedBy          string                             `json:"requestedBy"`
	RequestedAt          string                             `json:"requestedAt"`
	ResolvedBy           string                             `json:"resolvedBy,omitempty"`
	ResolvedAt           string                             `json:"resolvedAt,omitempty"`
	Reason               string                             `json:"reason,omitempty"`
	// ArtifactOpens maps artifactId -> lastOpenedAt (RFC3339Nano): the
	// per-artifact debounce stamp for the public gallery's open signal
	// (recordDealRoomArtifactOpen — the shareLinkRecord.LastOpenedAt twin).
	ArtifactOpens map[string]string `json:"artifactOpens,omitempty"`
}

type dealRoomArtifactBinding struct {
	Revision      int    `json:"revision"`
	ContentDigest string `json:"contentDigest"`
}

func sameStringSequence(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
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
		if !dealRoomCapabilityLive(record, time.Now().UTC()) {
			continue
		}
		provided := sha256.Sum256([]byte(token))
		stored, err := hex.DecodeString(record.TokenHash)
		if err == nil && subtle.ConstantTimeCompare(provided[:], stored) == 1 {
			record.RawToken = token
			return record, true
		}
	}
	return dealRoomRecord{}, false
}

func dealRoomCapabilityLive(record dealRoomRecord, now time.Time) bool {
	expires, err := time.Parse(time.RFC3339Nano, record.ExpiresAt)
	return err == nil && now.Before(expires) && record.Status == dealRoomStatusActive && record.Token == "" && isHexDigest(record.TokenHash) &&
		record.TenantID == canonicalArtifactTenantID() && record.Action == "read_deal_room" && record.Revision >= 1 && isHexDigest(record.ContentDigest)
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

/* ---------- gallery (spec §4, Wave 4 item 19) ---------- */

// dealRoomGalleryEntry is one gallery row: display fields pre-resolved and the
// full-fidelity link pre-minted at page-build time (renderDealRoomPage only
// escapes and lays out).
type dealRoomGalleryEntry struct {
	Title       string
	TypeBadge   string
	Version     int
	GateOutcome string
	RubricScore string
	Href        string
}

// dealRoomGalleryEntries assembles the gallery for one active room: the
// package's attached artifacts newest-first (the packagePayload order),
// EXCLUDING the cover binder, gated to artifactShareEligible — the share-link
// final+approved rule, server-side, so the gallery can never surface
// unapproved work behind the public token. Links:
//   - html_deck -> the sandboxed render route, with a short-lived render token
//     minted HERE at page-build time. The Deal Room token already authorizes
//     this viewer, so a per-page-load token (artifactRenderTokenTTL) is the
//     correct lifetime — a leaked page goes stale on its own.
//   - pdf -> the deal-room-scoped asset serve on this same route
//     (?artifact=<id>). The session-gated /artifacts/blob is never handed to
//     visitors; serveDealRoomGalleryPDF re-authorizes with the room token and
//     narrows to THIS package's artifacts only.
//   - everything else -> badge-only, no link (the escaped cover is the
//     narrative surface; nothing widens beyond the two full-fidelity types).
func (app *kanbanBoardApp) dealRoomGalleryEntries(record dealRoomRecord, pkg venturePackageRecord) []dealRoomGalleryEntry {
	entries := []dealRoomGalleryEntry{}
	if app == nil {
		return entries
	}
	for index := len(pkg.ArtifactIDs) - 1; index >= 0; index-- {
		artifactID := strings.TrimSpace(pkg.ArtifactIDs[index])
		if artifactID == "" || artifactID == record.ArtifactID {
			continue
		}
		binding, bound := record.BoundArtifacts[artifactID]
		artifact, ok := app.osArtifactByID(artifactID)
		if !bound || !ok || !artifactShareEligible(artifact) || artifactVersion(artifact) != binding.Revision || artifactCapabilityDigest(artifact) != binding.ContentDigest {
			continue
		}
		entry := dealRoomGalleryEntry{
			Title:   firstNonEmptyString(artifact.Metadata["title"], artifact.Metadata["threadQuery"], "untitled artifact"),
			Version: artifactVersion(artifact),
		}
		switch kind := artifactType(artifact); kind {
		case artifactTypeHTMLDeck:
			entry.TypeBadge = "deck"
			entry.Href = "/artifacts/render?id=" + url.QueryEscape(artifact.ID) +
				"&t=" + url.QueryEscape(mintArtifactRenderToken(artifact.ID, time.Now().Add(artifactRenderTokenTTL)))
		case artifactTypePDF:
			entry.TypeBadge = artifactTypePDF
			if _, hasPDF := firstArtifactAssetOfKind(artifact, "pdf"); hasPDF {
				entry.Href = "/deal-room/" + record.RawToken + "?artifact=" + url.QueryEscape(artifact.ID)
			}
		default:
			entry.TypeBadge = kind
		}
		provenance := artifactProvenance(artifact)
		entry.GateOutcome = provenance.GateOutcome
		best := 0.0
		for _, score := range provenance.RubricScores {
			if score > best {
				best = score
			}
		}
		if best > 0 {
			entry.RubricScore = strconv.FormatFloat(best, 'f', 1, 64)
		}
		entries = append(entries, entry)
	}
	return entries
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
	if dealRoomCapabilityLive(record, time.Now().UTC()) && record.RawToken != "" {
		payload["url"] = "/deal-room/" + record.RawToken
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
	// Sharing the binder delegates durable content to a public capability.
	// Authorize every artifact the package can expose (binder and gallery)
	// before resolving or using any of their bodies. One denied/missing item
	// collapses to the same opaque response as a missing package.
	packageArtifacts := append([]string(nil), pkg.ArtifactIDs...)
	for _, referencedID := range packageArtifacts {
		header, found := kanbanApp.memory.artifactAuthorizationHeaderByID(strings.TrimSpace(referencedID))
		if found {
			header = resolveArtifactHeaderOwner(header)
		}
		if !found || !artifactHeaderAuthorized(r.Context(), user, ACLReadContent, header) || !artifactHeaderAuthorized(r.Context(), user, ACLShare, header) {
			writeAuthError(w, http.StatusNotFound, "package not found")
			return
		}
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
			artifact, found := authorizedArtifactForActions(r.Context(), user, record.ArtifactID, ACLReadContent, ACLShare, ACLApprove)
			if !found {
				return dealRoomRecord{}, fmt.Errorf("artifact not found")
			}
			record.TokenHash = fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
			record.RawToken = token
			record.TenantID = canonicalArtifactTenantID()
			record.Revision = artifactVersion(artifact)
			record.ContentDigest = artifactCapabilityDigest(artifact)
			record.Action = "read_deal_room"
			record.ExpiresAt = time.Now().UTC().AddDate(0, 0, 30).Format(time.RFC3339Nano)
			record.BoundArtifacts = map[string]dealRoomArtifactBinding{}
			pkg, ok := kanbanApp.venturePackageByID(record.PackageID)
			if !ok || !packageOwnsArtifact(pkg, record.ArtifactID) {
				return dealRoomRecord{}, fmt.Errorf("package not found")
			}
			record.PackageNameSnapshot = pkg.Name
			record.PackageStageSnapshot = pkg.Stage
			for _, artifactID := range pkg.ArtifactIDs {
				candidate, found := authorizedArtifactForActions(r.Context(), user, artifactID, ACLReadContent, ACLShare)
				if !found {
					return dealRoomRecord{}, fmt.Errorf("artifact not found")
				}
				if !artifactShareEligible(candidate) && candidate.ID != record.ArtifactID {
					continue
				}
				record.BoundArtifacts[candidate.ID] = dealRoomArtifactBinding{Revision: artifactVersion(candidate), ContentDigest: artifactCapabilityDigest(candidate)}
			}
			// Optimistic atomic fence: package membership and every exact bound
			// snapshot must still match immediately before persistence.
			freshPkg, ok := kanbanApp.venturePackageByID(record.PackageID)
			if !ok || !sameStringSequence(pkg.ArtifactIDs, freshPkg.ArtifactIDs) {
				return dealRoomRecord{}, fmt.Errorf("package changed during approval")
			}
			for id, binding := range record.BoundArtifacts {
				fresh, ok := kanbanApp.osArtifactByID(id)
				if !ok || artifactVersion(fresh) != binding.Revision || artifactCapabilityDigest(fresh) != binding.ContentDigest {
					return dealRoomRecord{}, fmt.Errorf("artifact changed during approval")
				}
			}
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
	if record.Status == dealRoomStatusActive {
		if _, _, approvalErr := kanbanApp.memory.updateOSArtifactMetadata(record.ArtifactID, map[string]string{
			"status": artifactStatusApproved, artifactHumanApprovedAtKey: time.Now().UTC().Format(time.RFC3339Nano), artifactHumanApprovedByKey: normalizeAccountEmail(user.Email),
		}); approvalErr != nil {
			kanbanApp.dealRoomMu.Lock()
			record.Status, record.TokenHash = dealRoomStatusRevoked, ""
			_, _ = kanbanApp.persistDealRoom(record, false)
			kanbanApp.dealRoomMu.Unlock()
			writeAuthError(w, http.StatusInternalServerError, "could not finalize Deal Room approval")
			return
		}
	}

	// Notify the requester of the outcome (durable bell + targeted OS event).
	packageName := ""
	if pkg, ok := kanbanApp.venturePackageByID(record.PackageID); ok {
		packageName = pkg.Name
	}
	if record.Status == dealRoomStatusActive {
		// The raw capability is returned once in this resolve response. Never
		// persist it inside notification text; only TokenHash belongs at rest.
		text := fmt.Sprintf("Your Deal Room for %q is approved. Copy the one-time link from the approval response.", packageName)
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
		record.TokenHash = ""
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
	// Gallery asset serve: ?artifact=<id> streams ONE package-owned pdf under
	// this room's own authority — see serveDealRoomGalleryPDF for the scope.
	if assetArtifactID := strings.TrimSpace(r.URL.Query().Get("artifact")); assetArtifactID != "" {
		serveDealRoomGalleryPDF(w, record, assetArtifactID)
		return
	}
	artifact, found := kanbanApp.osArtifactByID(record.ArtifactID)
	if !found || !artifactShareEligible(artifact) || artifactVersion(artifact) != record.Revision || artifactCapabilityDigest(artifact) != record.ContentDigest {
		writeDealRoomNotFound(w)
		return
	}
	packageName := firstNonEmptyString(record.PackageNameSnapshot, "Venture package")
	stage := record.PackageStageSnapshot
	gallery := []dealRoomGalleryEntry{}
	if pkg, ok := kanbanApp.venturePackageByID(record.PackageID); ok {
		gallery = kanbanApp.dealRoomGalleryEntries(record, pkg)
	}

	// §5 capture: the gallery page open is the share_opened analog, recorded
	// against the cover binder and debounced like share links.
	boundArtifact := cloneMemoryEntry(artifact)
	kanbanApp.recordDealRoomArtifactOpen(record, boundArtifact)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(renderDealRoomPage(packageName, stage, boundArtifact.Text, gallery)))
}

// serveDealRoomGalleryPDF streams one gallery pdf under the Deal Room token's
// own authority. SECURITY: /artifacts/blob is session-gated and STAYS that way
// — Deal Room visitors never touch it. This serve re-authorizes with the
// (already validated, active) room token and then narrows to an artifact that
// (1) the token's package OWNS, (2) is share-eligible (final/approved — the
// gallery rule re-checked per open, so pulling approval kills the link), and
// (3) is pdf-typed with a stored pdf asset — so blob access never widens
// beyond the package the token grants. Every miss is the same 404 page (no
// enumeration).
func serveDealRoomGalleryPDF(w http.ResponseWriter, record dealRoomRecord, artifactID string) {
	binding, bound := record.BoundArtifacts[artifactID]
	if !bound {
		writeDealRoomNotFound(w)
		return
	}
	artifact, found := kanbanApp.osArtifactByID(artifactID)
	if !found || !artifactShareEligible(artifact) || artifactVersion(artifact) != binding.Revision || artifactCapabilityDigest(artifact) != binding.ContentDigest {
		writeDealRoomNotFound(w)
		return
	}
	asset, hasPDF := firstArtifactAssetOfKind(artifact, "pdf")
	if !hasPDF {
		writeDealRoomNotFound(w)
		return
	}
	data, _, err := getBlob(asset.Ref)
	if err != nil {
		log.Errorf("Failed to read deal room pdf blob %s: %v", asset.Ref, err)
		writeDealRoomNotFound(w)
		return
	}

	kanbanApp.recordDealRoomArtifactOpen(record, artifact)

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", blobDownloadFilename(asset.Name, asset.Ref)))
	if _, err := w.Write(data); err != nil {
		log.Errorf("Failed to serve deal room pdf %s: %v", artifact.ID, err)
	}
}

// dealRoomOpenSignalInterval debounces the open signal per (room, artifact):
// /deal-room/{token} is a PUBLIC unauthenticated route, so a crawler or a
// link-prefetching mail client must never grow the RAM-held JSONL store one
// entry per hit (the shareLinkOpenSignalInterval posture, mirrored).
const dealRoomOpenSignalInterval = time.Hour

// signalEventDealRoomArtifactOpened is the §5 share_opened analog for the Deal
// Room gallery: an external viewer opened this package artifact.
const signalEventDealRoomArtifactOpened = "deal_room_artifact_opened"

// recordDealRoomArtifactOpen stamps the per-artifact last-open on the room
// record and logs the §5 open signal at most once per
// dealRoomOpenSignalInterval per (room, artifact). Fail CLOSED on the signal
// when the debounce state is unreadable (the recordShareLinkOpen precedent);
// the stamp write itself is log-and-continue — it never fails the serve.
func (app *kanbanBoardApp) recordDealRoomArtifactOpen(record dealRoomRecord, artifact meetingMemoryEntry) {
	if app == nil || app.memory == nil {
		return
	}
	now := time.Now().UTC()
	recordSignal := true

	app.dealRoomMu.Lock()
	fresh, ok := app.dealRoomByID(record.ID)
	if !ok {
		// Fail CLOSED: with the debounce state unreadable, this unauthenticated
		// producer must not grow the store one entry per hit.
		recordSignal = false
		log.Errorf("Failed to stamp deal room open %s: record not found", record.ID)
	} else {
		if last, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(fresh.ArtifactOpens[artifact.ID])); parseErr == nil && now.Sub(last) < dealRoomOpenSignalInterval {
			recordSignal = false
		}
		if fresh.ArtifactOpens == nil {
			fresh.ArtifactOpens = map[string]string{}
		}
		fresh.ArtifactOpens[artifact.ID] = now.Format(time.RFC3339Nano)
		if _, err := app.persistDealRoom(fresh, false); err != nil {
			log.Errorf("Failed to stamp deal room open %s: %v", record.ID, err)
		}
	}
	app.dealRoomMu.Unlock()

	if !recordSignal {
		return
	}
	app.recordSignalEvent("external", signalEventDealRoomArtifactOpened, signalValenceNeutral, artifact.ID, record.PackageID, map[string]string{
		"dealRoomId":   record.ID,
		"artifactType": artifactType(artifact),
	})
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
// binder body rendered from minimal, injection-safe Markdown (the escaped
// cover, unchanged), the final/approved artifact gallery, and a provenance
// appendix. All artifact-derived text is HTML-escaped before formatting.
func renderDealRoomPage(packageName string, stage string, binderBody string, gallery []dealRoomGalleryEntry) string {
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
	page.WriteString(renderDealRoomGalleryHTML(gallery))
	page.WriteString(provenance)
	page.WriteString("</div></body></html>")
	return page.String()
}

// renderDealRoomGalleryHTML lays out the gallery rows. Every artifact-derived
// span (title, badge, gate outcome) is html.EscapeString-ed before it gets
// structure — the binder renderer's rule. An empty gallery renders nothing:
// the cover-only page stays exactly what it was.
func renderDealRoomGalleryHTML(gallery []dealRoomGalleryEntry) string {
	if len(gallery) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString("<section class=\"gallery\"><h2>Package artifacts</h2><ul>")
	for _, entry := range gallery {
		title := html.EscapeString(strings.TrimSpace(entry.Title))
		out.WriteString("<li>")
		if entry.Href != "" {
			out.WriteString("<a href=\"" + html.EscapeString(entry.Href) + "\" target=\"_blank\" rel=\"noopener\">" + title + "</a>")
		} else {
			out.WriteString("<span class=\"title\">" + title + "</span>")
		}
		out.WriteString("<span class=\"meta\"><span class=\"type\">" + html.EscapeString(entry.TypeBadge) + "</span>")
		out.WriteString("<span class=\"version\">v" + strconv.Itoa(entry.Version) + "</span>")
		if entry.GateOutcome != "" || entry.RubricScore != "" {
			gate := strings.TrimSpace(strings.Join([]string{entry.GateOutcome, entry.RubricScore}, " · "))
			gate = strings.Trim(gate, " ·")
			out.WriteString("<span class=\"gate\">" + html.EscapeString(gate) + "</span>")
		}
		out.WriteString("</span></li>")
	}
	out.WriteString("</ul></section>")
	return out.String()
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
.gallery{margin-top:2rem;padding-top:1.25rem;border-top:1px solid #d5dae4}
.gallery h2{font-size:.78rem;letter-spacing:.08em;text-transform:uppercase;color:#8a92a2;margin:0 0 .6rem}
.gallery ul{list-style:none;padding:0;margin:0}
.gallery li{display:flex;align-items:baseline;gap:.6rem;flex-wrap:wrap;padding:.55rem 0;border-bottom:1px solid #e2e6ee}
.gallery a{color:#5a2fd0;font-weight:600;text-decoration:none}
.gallery a:hover{text-decoration:underline}
.gallery .title{font-weight:600}
.gallery .meta{display:inline-flex;gap:.45rem;align-items:baseline;font-size:.78rem;color:#5c6472}
.gallery .type{text-transform:uppercase;letter-spacing:.06em;background:#e6eaf2;border-radius:4px;padding:.1rem .4rem;font-weight:600}
.gallery .version{color:#8a92a2}
.gallery .gate{color:#2f7a4f;font-weight:600}
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
