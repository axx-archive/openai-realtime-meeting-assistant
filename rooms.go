package main

// Multi-room W1 (docs/plans/multi-room-2026-07-08.md §3.1/§4.1/§5): the room
// registry and the guest capability layer — persistence and HTTP surface
// only, zero behavior change to the live room. data/rooms.json holds the
// room records (office seeded at boot, one-click join preserved) plus each
// room's guest links. A guest link token is 32 bytes of crypto/rand returned
// ONCE as /g#<token>; only its sha256 is stored (guestLinkRecord.Hash), so a
// leaked rooms.json hands out no admission. Redemption re-checks expiry,
// revocation, and room archival on EVERY use (the share_links.go idiom), and
// tokens ride the URL FRAGMENT so they never reach server/proxy logs or a
// Referer header (§6.3) — the /g/<token> path form exists only as a 302 shim
// to the fragment form and must never log the token.
//
// Passcodes are bcrypt-hashed (the accounts.go idiom) and are an
// admission-only credential checked at the W3 websocket hello — NEVER an API
// credential anywhere else (participants.go:96-98 oracle lesson).

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const (
	officeRoomID   = "office"
	officeRoomName = "the office"

	// Guest links default to 7 days (§10) and are member-revocable anytime;
	// the mint request's ttlHours is bounded so a typo cannot mint a
	// permanent capability.
	guestLinkDefaultTTL = 7 * 24 * time.Hour
	guestLinkMaxTTL     = 30 * 24 * time.Hour

	maxRoomNameRunes       = 60
	maxGuestLinkLabelRunes = 60
	maxGuestNameRunes      = 40
)

var errRoomNotFound = errors.New("room not found")

// normalizeRoomID maps the migration invariant (§9: absent roomId == office)
// onto every record-layer lookup: a blank room id — legacy entries, legacy
// meeting records, callers that predate rooms — always means the office.
func normalizeRoomID(roomID string) string {
	if trimmed := strings.TrimSpace(roomID); trimmed != "" {
		return trimmed
	}
	return officeRoomID
}

type roomRecord struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	CreatedBy    string            `json:"createdBy,omitempty"`
	CreatedAt    time.Time         `json:"createdAt"`
	PasscodeHash string            `json:"passcodeHash,omitempty"`
	GuestEnabled bool              `json:"guestEnabled"`
	GuestLinks   []guestLinkRecord `json:"guestLinks,omitempty"`
	Archived     bool              `json:"archived,omitempty"`
}

type guestLinkRecord struct {
	ID        string    `json:"id"` // first 8 hex of Hash — revoke handle, safe to list
	Hash      string    `json:"hash"`
	Label     string    `json:"label,omitempty"`
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
	Expires   time.Time `json:"expires"`
	Revoked   bool      `json:"revoked,omitempty"`
}

// roomStore persists the room registry with the sessions.json idiom: one
// mutex, write-tmp-then-rename, office seeded when the file is missing.
type roomStore struct {
	mu    sync.Mutex
	path  string
	rooms []roomRecord
}

func newRoomStore(path string) *roomStore {
	store := &roomStore{path: path}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &store.rooms); err != nil {
			log.Errorf("Ignoring malformed room store at %s: %v", path, err)
			store.rooms = nil
		}
	}
	if _, ok := store.roomByIDLocked(officeRoomID); !ok {
		// §9.1: the existing office becomes the default room. No passcode,
		// guests off — exactly today's behavior.
		store.rooms = append([]roomRecord{{
			ID:        officeRoomID,
			Name:      officeRoomName,
			CreatedAt: time.Now().UTC(),
		}}, store.rooms...)
		store.persistLocked()
	}
	return store
}

func (s *roomStore) persistLocked() {
	if err := writeJSONFileAtomically(s.path, "room store", s.rooms); err != nil {
		log.Errorf("Failed to persist room store: %v", err)
	}
}

func (s *roomStore) roomByIDLocked(id string) (int, bool) {
	for index, room := range s.rooms {
		if room.ID == id {
			return index, true
		}
	}
	return -1, false
}

func (s *roomStore) byID(id string) (roomRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, ok := s.roomByIDLocked(id)
	if !ok {
		return roomRecord{}, false
	}
	return cloneRoomRecord(s.rooms[index]), true
}

func (s *roomStore) list() []roomRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	rooms := make([]roomRecord, 0, len(s.rooms))
	for _, room := range s.rooms {
		rooms = append(rooms, cloneRoomRecord(room))
	}
	sort.SliceStable(rooms, func(left, right int) bool {
		if (rooms[left].ID == officeRoomID) != (rooms[right].ID == officeRoomID) {
			return rooms[left].ID == officeRoomID
		}
		return rooms[left].CreatedAt.Before(rooms[right].CreatedAt)
	})
	return rooms
}

func cloneRoomRecord(room roomRecord) roomRecord {
	room.GuestLinks = append([]guestLinkRecord(nil), room.GuestLinks...)
	return room
}

func cleanRoomName(raw string) (string, error) {
	name := strings.Join(strings.Fields(raw), " ")
	if name == "" {
		return "", errors.New("room name is required")
	}
	if utf8.RuneCountInString(name) > maxRoomNameRunes {
		return "", errors.New("room name must be 60 characters or fewer")
	}
	return name, nil
}

func (s *roomStore) create(name, passcode, createdBy string, guestEnabled bool) (roomRecord, error) {
	cleaned, err := cleanRoomName(name)
	if err != nil {
		return roomRecord{}, err
	}
	passcodeHash, err := hashRoomPasscode(passcode)
	if err != nil {
		return roomRecord{}, err
	}
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return roomRecord{}, err
	}
	record := roomRecord{
		ID:           "room-" + hex.EncodeToString(raw),
		Name:         cleaned,
		CreatedBy:    normalizeAccountEmail(createdBy),
		CreatedAt:    time.Now().UTC(),
		PasscodeHash: passcodeHash,
		GuestEnabled: guestEnabled,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.rooms = append(s.rooms, record)
	s.persistLocked()
	return cloneRoomRecord(record), nil
}

func hashRoomPasscode(passcode string) (string, error) {
	if strings.TrimSpace(passcode) == "" {
		return "", nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(passcode), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// setPasscode sets (non-empty) or clears (empty) a room's passcode gate.
func (s *roomStore) setPasscode(id, passcode string) error {
	passcodeHash, err := hashRoomPasscode(passcode)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index, ok := s.roomByIDLocked(id)
	if !ok {
		return errRoomNotFound
	}
	s.rooms[index].PasscodeHash = passcodeHash
	s.persistLocked()
	return nil
}

// roomPasscodeOK is the single admission-time comparison (bcrypt, W3 hello).
// A room without a gate admits regardless of what was typed.
func roomPasscodeOK(room roomRecord, passcode string) bool {
	if room.PasscodeHash == "" {
		return true
	}
	return bcrypt.CompareHashAndPassword([]byte(room.PasscodeHash), []byte(passcode)) == nil
}

func (s *roomStore) archive(id string) error {
	if id == officeRoomID {
		return errors.New("the office cannot be archived")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index, ok := s.roomByIDLocked(id)
	if !ok {
		return errRoomNotFound
	}
	s.rooms[index].Archived = true
	s.persistLocked()
	return nil
}

func (s *roomStore) mintGuestLink(roomID, label, createdBy string, ttl time.Duration) (string, guestLinkRecord, error) {
	if ttl <= 0 {
		ttl = guestLinkDefaultTTL
	}
	if ttl > guestLinkMaxTTL {
		ttl = guestLinkMaxTTL
	}
	label = strings.Join(strings.Fields(label), " ")
	if utf8.RuneCountInString(label) > maxGuestLinkLabelRunes {
		return "", guestLinkRecord{}, errors.New("label must be 60 characters or fewer")
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", guestLinkRecord{}, err
	}
	token := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	now := time.Now().UTC()
	record := guestLinkRecord{
		ID:        hash[:8],
		Hash:      hash,
		Label:     label,
		CreatedBy: normalizeAccountEmail(createdBy),
		CreatedAt: now,
		Expires:   now.Add(ttl),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	index, ok := s.roomByIDLocked(roomID)
	if !ok {
		return "", guestLinkRecord{}, errRoomNotFound
	}
	if s.rooms[index].Archived {
		return "", guestLinkRecord{}, errors.New("archived rooms cannot mint guest links")
	}
	s.rooms[index].GuestLinks = append(s.rooms[index].GuestLinks, record)
	// Minting the first link flips the room guest-enabled (§3.1); the
	// per-sitting listen-only latch reads this in W4.
	s.rooms[index].GuestEnabled = true
	s.persistLocked()
	return token, record, nil
}

func (s *roomStore) listGuestLinks(roomID string) ([]guestLinkRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, ok := s.roomByIDLocked(roomID)
	if !ok {
		return nil, errRoomNotFound
	}
	return append([]guestLinkRecord(nil), s.rooms[index].GuestLinks...), nil
}

func (s *roomStore) revokeGuestLink(roomID, linkID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	roomIndex, ok := s.roomByIDLocked(roomID)
	if !ok {
		return errRoomNotFound
	}
	for linkIndex, link := range s.rooms[roomIndex].GuestLinks {
		if link.ID == linkID {
			s.rooms[roomIndex].GuestLinks[linkIndex].Revoked = true
			s.persistLocked()
			return nil
		}
	}
	return errors.New("guest link not found")
}

// redeemGuestToken resolves a raw 64-hex token to its room. Hash-then-
// constant-time comparison per candidate (the shareLinkByToken pattern), and
// the liveness conditions — expiry, revocation, room archival — are
// re-checked on EVERY redeem, never only at mint (§5.1).
func (s *roomStore) redeemGuestToken(token string) (roomRecord, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return roomRecord{}, false
	}
	sum := sha256.Sum256([]byte(token))
	providedHash := []byte(hex.EncodeToString(sum[:]))

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, room := range s.rooms {
		for _, link := range room.GuestLinks {
			if subtle.ConstantTimeCompare(providedHash, []byte(link.Hash)) != 1 {
				continue
			}
			if link.Revoked || now.After(link.Expires) || room.Archived {
				return roomRecord{}, false
			}
			return cloneRoomRecord(room), true
		}
	}
	return roomRecord{}, false
}

// sweepExpiredGuestLinks drops expired link rows (revoked-but-unexpired rows
// stay listed as history). Rewrites only when something actually expired.
func (s *roomStore) sweepExpiredGuestLinks() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	changed := false
	for index, room := range s.rooms {
		kept := room.GuestLinks[:0]
		for _, link := range room.GuestLinks {
			if now.After(link.Expires) {
				changed = true
				continue
			}
			kept = append(kept, link)
		}
		if len(kept) == 0 {
			kept = nil
		}
		s.rooms[index].GuestLinks = kept
	}
	if changed {
		s.persistLocked()
	}
}

/* ---------- store accessor ---------- */

var (
	roomStoreMu    sync.Mutex
	roomStoreCache = map[string]*roomStore{}
)

func roomsFilePath() string {
	if path := strings.TrimSpace(os.Getenv("BONFIRE_ROOMS_PATH")); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "rooms.json")
}

func appRoomStore() *roomStore {
	path := roomsFilePath()
	roomStoreMu.Lock()
	defer roomStoreMu.Unlock()
	if store, ok := roomStoreCache[path]; ok {
		return store
	}
	store := newRoomStore(path)
	roomStoreCache[path] = store
	return store
}

// sweepExpiredGuestLinksIfOpen runs on the session-persist seam (§5.1). Only
// an already-opened store is swept, so a session write can never conjure a
// rooms.json into a data directory that has none.
func sweepExpiredGuestLinksIfOpen() {
	roomStoreMu.Lock()
	store := roomStoreCache[roomsFilePath()]
	roomStoreMu.Unlock()
	if store != nil {
		store.sweepExpiredGuestLinks()
	}
}

/* ---------- payload shaping ---------- */

// roomLiveStats reports live occupancy for the rooms list — real per-room
// presence since the W3 liveRoom extraction.
func roomLiveStats(roomID string) (bool, int) {
	if kanbanApp == nil {
		return false, 0
	}
	count := kanbanApp.activeParticipantCount(roomID)
	return count > 0, count
}

// roomListPayload never carries the passcode hash or any link hash (§4.1).
func roomListPayload(room roomRecord) map[string]any {
	live, count := roomLiveStats(room.ID)
	return map[string]any{
		"id":               room.ID,
		"name":             room.Name,
		"live":             live,
		"participantCount": count,
		"passcodeRequired": room.PasscodeHash != "",
		"guestEnabled":     room.GuestEnabled,
		"createdBy":        room.CreatedBy,
		"archived":         room.Archived,
	}
}

// guestLinkPayload lists the revoke handle and lifecycle stamps only — the
// hash never leaves the store, and the token was never in it (§4.1).
func guestLinkPayload(link guestLinkRecord) map[string]any {
	return map[string]any{
		"id":        link.ID,
		"label":     link.Label,
		"createdBy": link.CreatedBy,
		"createdAt": link.CreatedAt,
		"expires":   link.Expires,
		"revoked":   link.Revoked,
	}
}

/* ---------- HTTP: member-session room surface ---------- */

// roomsHandler serves GET /rooms (list) and POST /rooms (create). Member
// session + origin gated; creating a room starts NO media (§4.1).
func roomsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
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

	switch r.Method {
	case http.MethodGet:
		rooms := []map[string]any{}
		for _, room := range appRoomStore().list() {
			rooms = append(rooms, roomListPayload(room))
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "rooms": rooms})
	case http.MethodPost:
		payload := struct {
			Name        string `json:"name"`
			Passcode    string `json:"passcode"`
			GuestAccess bool   `json:"guestAccess"`
		}{}
		if err := decodeAuthBody(r, &payload); err != nil {
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		record, err := appRoomStore().create(payload.Name, payload.Passcode, user.Email, payload.GuestAccess)
		if err != nil {
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		// W3: the rooms card on every office tab updates live on create.
		broadcastRoomsSnapshot()
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "room": roomListPayload(record)})
	}
}

// roomActionHandler serves the /rooms/{id}/... verbs: passcode set/clear,
// archive, guest-link mint/list/revoke.
func roomActionHandler(w http.ResponseWriter, r *http.Request) {
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/rooms/"), "/")
	roomID, action, _ := strings.Cut(rest, "/")
	if roomID == "" {
		http.NotFound(w, r)
		return
	}

	switch {
	case action == "passcode" && r.Method == http.MethodPost:
		payload := struct {
			Passcode string `json:"passcode"`
		}{}
		if err := decodeAuthBody(r, &payload); err != nil {
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := appRoomStore().setPasscode(roomID, payload.Passcode); err != nil {
			writeRoomActionError(w, err)
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "passcodeRequired": strings.TrimSpace(payload.Passcode) != ""})
	case action == "archive" && r.Method == http.MethodPost:
		if err := appRoomStore().archive(roomID); err != nil {
			writeRoomActionError(w, err)
			return
		}
		broadcastRoomsSnapshot()
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true})
	case action == "guest-links" && r.Method == http.MethodPost:
		payload := struct {
			Label    string `json:"label"`
			TTLHours int    `json:"ttlHours"`
		}{}
		if err := decodeAuthBody(r, &payload); err != nil {
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		token, record, err := appRoomStore().mintGuestLink(roomID, payload.Label, user.Email, time.Duration(payload.TTLHours)*time.Hour)
		if err != nil {
			writeRoomActionError(w, err)
			return
		}
		// The raw token appears exactly ONCE, in this response body, as the
		// fragment-carried URL (§6.3). It is not stored and not re-listable.
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok":   true,
			"url":  "/g#" + token,
			"link": guestLinkPayload(record),
		})
	case action == "guest-links" && r.Method == http.MethodGet:
		links, err := appRoomStore().listGuestLinks(roomID)
		if err != nil {
			writeRoomActionError(w, err)
			return
		}
		payload := []map[string]any{}
		for _, link := range links {
			payload = append(payload, guestLinkPayload(link))
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "links": payload})
	case action == "guest-links/revoke" && r.Method == http.MethodPost:
		payload := struct {
			ID string `json:"id"`
		}{}
		if err := decodeAuthBody(r, &payload); err != nil {
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := appRoomStore().revokeGuestLink(roomID, strings.TrimSpace(payload.ID)); err != nil {
			writeRoomActionError(w, err)
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		http.NotFound(w, r)
	}
}

func writeRoomActionError(w http.ResponseWriter, err error) {
	if errors.Is(err, errRoomNotFound) {
		writeAuthError(w, http.StatusNotFound, err.Error())
		return
	}
	writeAuthError(w, http.StatusBadRequest, err.Error())
}

/* ---------- HTTP: guest surface (§5) ---------- */

func isGuestTokenShape(token string) bool {
	if len(token) != 64 {
		return false
	}
	for _, r := range token {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// guestPageHandler serves GET /g (the guest boot page: index.html bytes, the
// token stays client-side in the fragment) and the legacy path shim
// GET /g/<64hex> → 302 /g#<token>. No token validation at serve time — no
// 404 oracle (§5.2). Referrer-Policy + no-store ride every response,
// including the shim redirect (§6.3 defense in depth). The token must never
// be logged from this handler.
func guestPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")

	if r.URL.Path == "/g" || r.URL.Path == "/g/" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := w.Write(indexHTML); err != nil {
			log.Errorf("Failed to serve guest page: %v", err)
		}
		return
	}

	token := strings.Trim(strings.TrimPrefix(r.URL.Path, "/g/"), "/")
	if !isGuestTokenShape(token) {
		http.NotFound(w, r)
		return
	}
	// Path→fragment conversion: after this one request (the last time the
	// token can appear in a request line) it lives only in the fragment.
	// Location is set by hand so no HTML redirect body echoes the token.
	w.Header().Set("Location", "/g#"+token)
	w.WriteHeader(http.StatusFound)
}

// sanitizeGuestName trims, strips control/unprintable runes, collapses
// whitespace, and bounds the result to 1–40 runes (§5.2).
func sanitizeGuestName(raw string) (string, error) {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, raw)
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if cleaned == "" {
		return "", errors.New("a name is required")
	}
	if utf8.RuneCountInString(cleaned) > maxGuestNameRunes {
		return "", errors.New("name must be 40 characters or fewer")
	}
	return cleaned, nil
}

// guestJoinHandler is the public POST /guest/join {token, name}: validates
// the capability, sanitizes the display name, rejects roster impersonation,
// and mints the bonfire_guest session (§5.2). Origin-checked because it sets
// a cookie; rate-limited because it is a token-guessing surface.
func guestJoinHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if !authAttemptAllowedForKeys("guestjoin|" + clientIPForRateLimit(r)) {
		writeAuthError(w, http.StatusTooManyRequests, "too many join attempts; try again in a few minutes")
		return
	}

	payload := struct {
		Token string `json:"token"`
		Name  string `json:"name"`
	}{}
	if err := decodeAuthBody(r, &payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}

	name, err := sanitizeGuestName(payload.Name)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Roster-collision check (§5.2): compare against the names the seeded
	// roster actually resolves to — a guest naming themselves after a member
	// is rejected here, and W3 additionally stamps the server-side "Guest "
	// prefix at admission. Legitimate non-roster names must pass.
	if guestNameCollidesWithRoster(name) {
		writeAuthError(w, http.StatusBadRequest, "that name belongs to a team member; pick another")
		return
	}

	token := strings.TrimSpace(payload.Token)
	if !isGuestTokenShape(token) {
		writeAuthError(w, http.StatusForbidden, "that guest link is no longer valid")
		return
	}
	room, ok := appRoomStore().redeemGuestToken(token)
	if !ok {
		writeAuthError(w, http.StatusForbidden, "that guest link is no longer valid")
		return
	}

	sessionToken, err := userSessionStore().createGuest(room.ID, name)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not start a guest session")
		return
	}
	clearAuthAttempts("guestjoin|" + clientIPForRateLimit(r))
	setGuestSessionCookie(w, r, sessionToken, int(guestSessionTTL/time.Second))
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"roomId":    room.ID,
		"roomName":  room.Name,
		"guestName": name,
	})
}

// guestMeHandler is the guest-cookie-only boot-resume probe (§5.2): a
// reloaded or deploy-refreshed guest tab re-enters its room from the cookie
// without re-presenting the link token.
func guestMeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	principal := guestFromRequest(r)
	if principal == nil {
		writeAuthError(w, http.StatusUnauthorized, "no guest session")
		return
	}
	room, ok := appRoomStore().byID(principal.RoomID)
	if !ok || room.Archived {
		// The room is gone (or unjoinable) — fail closed rather than resume
		// a seat that admission would reject anyway.
		writeAuthError(w, http.StatusUnauthorized, "no guest session")
		return
	}
	live, count := roomLiveStats(room.ID)
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"roomId":           room.ID,
		"roomName":         room.Name,
		"guestName":        principal.Name,
		"live":             live,
		"participantCount": count,
	})
}
