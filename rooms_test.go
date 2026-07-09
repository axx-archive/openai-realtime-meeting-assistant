package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupRoomsTestEnv isolates auth + rooms state in temp files and returns a
// signed-in member's cookies for the session-gated surface.
func setupRoomsTestEnv(t *testing.T) []*http.Cookie {
	t.Helper()
	setupAuthTestEnv(t)
	t.Setenv("BONFIRE_ROOMS_PATH", filepath.Join(t.TempDir(), "rooms.json"))
	return loginAs(t, "aj@shareability.com", "B0NFIRE!")
}

func doRoomsRequest(t *testing.T, handler http.HandlerFunc, method, path, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, req)
	return recorder
}

/* ---------- store ---------- */

func TestRoomStoreSeedsOfficeAndPersistsRooms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rooms.json")

	store := newRoomStore(path)
	office, ok := store.byID(officeRoomID)
	if !ok {
		t.Fatal("expected a fresh room store to seed the office room")
	}
	if office.Name != officeRoomName || office.PasscodeHash != "" || office.GuestEnabled || office.Archived {
		t.Fatalf("office seed must preserve one-click join defaults, got %+v", office)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected the office seed to persist rooms.json at boot: %v", err)
	}

	created, err := store.create("War Room", "hunter2", "aj@shareability.com", true)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if !strings.HasPrefix(created.ID, "room-") || len(created.ID) != len("room-")+16 {
		t.Fatalf("room id should be room-+hex(8B), got %q", created.ID)
	}
	if created.PasscodeHash == "" || created.PasscodeHash == "hunter2" {
		t.Fatalf("passcode must be stored hashed, got %q", created.PasscodeHash)
	}
	if !created.GuestEnabled {
		t.Fatal("guestAccess=true at create should enable guests")
	}

	reloaded := newRoomStore(path)
	rooms := reloaded.list()
	if len(rooms) != 2 {
		t.Fatalf("expected office + created room after reload, got %d", len(rooms))
	}
	if rooms[0].ID != officeRoomID {
		t.Fatalf("office must sort first, got %q", rooms[0].ID)
	}
	if rooms[1].ID != created.ID || rooms[1].Name != "War Room" {
		t.Fatalf("created room must survive reload, got %+v", rooms[1])
	}
}

func TestRoomStorePasscodeSetClearAndArchive(t *testing.T) {
	store := newRoomStore(filepath.Join(t.TempDir(), "rooms.json"))
	room, err := store.create("Deal Desk", "", "aj@shareability.com", false)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	if !roomPasscodeOK(room, "anything") {
		t.Fatal("a room without a gate must admit regardless of passcode")
	}

	if err := store.setPasscode(room.ID, "s3cret!"); err != nil {
		t.Fatalf("set passcode: %v", err)
	}
	gated, _ := store.byID(room.ID)
	if !roomPasscodeOK(gated, "s3cret!") || roomPasscodeOK(gated, "wrong") {
		t.Fatal("bcrypt passcode gate must admit the right passcode only")
	}

	if err := store.setPasscode(room.ID, ""); err != nil {
		t.Fatalf("clear passcode: %v", err)
	}
	cleared, _ := store.byID(room.ID)
	if cleared.PasscodeHash != "" {
		t.Fatal("empty passcode must clear the gate")
	}

	if err := store.archive(officeRoomID); err == nil {
		t.Fatal("the office must never be archivable")
	}
	if err := store.archive(room.ID); err != nil {
		t.Fatalf("archive room: %v", err)
	}
	archived, _ := store.byID(room.ID)
	if !archived.Archived {
		t.Fatal("expected room to be archived")
	}
	if err := store.setPasscode("room-doesnotexist", "x"); err != errRoomNotFound {
		t.Fatalf("expected errRoomNotFound, got %v", err)
	}
}

func TestGuestLinkMintRedeemRevokeExpiry(t *testing.T) {
	store := newRoomStore(filepath.Join(t.TempDir(), "rooms.json"))
	room, err := store.create("Guest Room", "", "aj@shareability.com", false)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	token, link, err := store.mintGuestLink(room.ID, "board meeting", "aj@shareability.com", 0)
	if err != nil {
		t.Fatalf("mint guest link: %v", err)
	}
	if !isGuestTokenShape(token) {
		t.Fatalf("token must be 64 lowercase hex, got %q", token)
	}
	if strings.Contains(link.Hash, token) || link.Hash == token {
		t.Fatal("the raw token must never be stored")
	}
	if link.ID != link.Hash[:8] {
		t.Fatalf("link id must be the hash prefix revoke handle, got %q vs %q", link.ID, link.Hash)
	}
	wantExpiry := time.Now().Add(guestLinkDefaultTTL)
	if link.Expires.Before(wantExpiry.Add(-time.Minute)) || link.Expires.After(wantExpiry.Add(time.Minute)) {
		t.Fatalf("default expiry should be 7 days out, got %v", link.Expires)
	}

	minted, _ := store.byID(room.ID)
	if !minted.GuestEnabled {
		t.Fatal("minting the first link must flip GuestEnabled")
	}

	// Redeem re-checks liveness on EVERY use, not only at mint.
	if redeemed, ok := store.redeemGuestToken(token); !ok || redeemed.ID != room.ID {
		t.Fatalf("expected live token to redeem to its room, got ok=%v room=%+v", ok, redeemed)
	}
	if _, ok := store.redeemGuestToken(strings.Repeat("0", 64)); ok {
		t.Fatal("an unknown token must not redeem")
	}

	if err := store.archive(room.ID); err != nil {
		t.Fatalf("archive room: %v", err)
	}
	if _, ok := store.redeemGuestToken(token); ok {
		t.Fatal("a token for an archived room must not redeem")
	}
	store.mu.Lock()
	index, _ := store.roomByIDLocked(room.ID)
	store.rooms[index].Archived = false
	store.mu.Unlock()

	if err := store.revokeGuestLink(room.ID, link.ID); err != nil {
		t.Fatalf("revoke guest link: %v", err)
	}
	if _, ok := store.redeemGuestToken(token); ok {
		t.Fatal("a revoked token must not redeem")
	}

	// Expiry: backdate a fresh link, confirm redeem fails, then confirm the
	// session-persist-seam sweep drops the row.
	expiredToken, expiredLink, err := store.mintGuestLink(room.ID, "", "aj@shareability.com", time.Hour)
	if err != nil {
		t.Fatalf("mint second link: %v", err)
	}
	store.mu.Lock()
	index, _ = store.roomByIDLocked(room.ID)
	for i := range store.rooms[index].GuestLinks {
		if store.rooms[index].GuestLinks[i].ID == expiredLink.ID {
			store.rooms[index].GuestLinks[i].Expires = time.Now().Add(-time.Minute)
		}
	}
	store.mu.Unlock()
	if _, ok := store.redeemGuestToken(expiredToken); ok {
		t.Fatal("an expired token must not redeem")
	}

	store.sweepExpiredGuestLinks()
	links, err := store.listGuestLinks(room.ID)
	if err != nil {
		t.Fatalf("list guest links: %v", err)
	}
	for _, remaining := range links {
		if remaining.ID == expiredLink.ID {
			t.Fatal("the sweep must drop expired link rows")
		}
	}
	// The revoked-but-unexpired link stays listed as history.
	found := false
	for _, remaining := range links {
		if remaining.ID == link.ID && remaining.Revoked {
			found = true
		}
	}
	if !found {
		t.Fatal("revoked unexpired links must stay listed for the revoke UI")
	}
}

/* ---------- HTTP: /rooms surface ---------- */

func TestRoomsEndpointsRequireMemberSession(t *testing.T) {
	setupRoomsTestEnv(t)

	if code := doRoomsRequest(t, roomsHandler, http.MethodGet, "/rooms", "", nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("GET /rooms signed-out = %d, want 401", code)
	}
	if code := doRoomsRequest(t, roomsHandler, http.MethodPost, "/rooms", `{"name":"X"}`, nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("POST /rooms signed-out = %d, want 401", code)
	}
	if code := doRoomsRequest(t, roomActionHandler, http.MethodPost, "/rooms/office/archive", "", nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("POST /rooms/office/archive signed-out = %d, want 401", code)
	}

	req := httptest.NewRequest(http.MethodGet, "/rooms", nil)
	req.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()
	roomsHandler(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin /rooms = %d, want 403", recorder.Code)
	}
}

func TestRoomsHandlerCreateListAndActions(t *testing.T) {
	cookies := setupRoomsTestEnv(t)

	recorder := doRoomsRequest(t, roomsHandler, http.MethodPost, "/rooms", `{"name":"  War   Room ","passcode":"hunter2","guestAccess":true}`, cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("create room = %d body %s", recorder.Code, recorder.Body.String())
	}
	var created struct {
		Room struct {
			ID               string `json:"id"`
			Name             string `json:"name"`
			PasscodeRequired bool   `json:"passcodeRequired"`
			GuestEnabled     bool   `json:"guestEnabled"`
		} `json:"room"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	if created.Room.Name != "War Room" || !created.Room.PasscodeRequired || !created.Room.GuestEnabled {
		t.Fatalf("unexpected created room payload: %+v", created.Room)
	}

	recorder = doRoomsRequest(t, roomsHandler, http.MethodGet, "/rooms", "", cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list rooms = %d body %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	// The wire form never carries the bcrypt hash or any link hash (§4.1).
	for _, leaked := range []string{"passcodeHash", "hash", "guestLinks", "hunter2"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("GET /rooms must not leak %q, got %s", leaked, body)
		}
	}
	var listed struct {
		Rooms []struct {
			ID               string `json:"id"`
			Live             bool   `json:"live"`
			ParticipantCount int    `json:"participantCount"`
		} `json:"rooms"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(listed.Rooms) != 2 || listed.Rooms[0].ID != officeRoomID {
		t.Fatalf("expected office-first list of 2, got %+v", listed.Rooms)
	}

	// Passcode clear then archive; the office rejects archive.
	if code := doRoomsRequest(t, roomActionHandler, http.MethodPost, "/rooms/"+created.Room.ID+"/passcode", `{"passcode":""}`, cookies).Code; code != http.StatusOK {
		t.Fatalf("clear passcode = %d", code)
	}
	room, _ := appRoomStore().byID(created.Room.ID)
	if room.PasscodeHash != "" {
		t.Fatal("passcode clear did not land")
	}
	if code := doRoomsRequest(t, roomActionHandler, http.MethodPost, "/rooms/office/archive", "", cookies).Code; code != http.StatusBadRequest {
		t.Fatalf("archive office = %d, want 400", code)
	}
	if code := doRoomsRequest(t, roomActionHandler, http.MethodPost, "/rooms/"+created.Room.ID+"/archive", "", cookies).Code; code != http.StatusOK {
		t.Fatalf("archive room = %d", code)
	}
	if code := doRoomsRequest(t, roomActionHandler, http.MethodPost, "/rooms/room-missing/archive", "", cookies).Code; code != http.StatusNotFound {
		t.Fatalf("archive missing room = %d, want 404", code)
	}
}

// guestLinkActive on GET /rooms rows is the honest input to the lobby's
// listen-only badge: true only while an unexpired, unrevoked link lives (the
// §7.1 latch predicate), never the sticky guestEnabled flag.
func TestRoomsListGuestLinkActive(t *testing.T) {
	cookies := setupRoomsTestEnv(t)

	fetchActive := func() map[string]bool {
		t.Helper()
		recorder := doRoomsRequest(t, roomsHandler, http.MethodGet, "/rooms", "", cookies)
		if recorder.Code != http.StatusOK {
			t.Fatalf("list rooms = %d body %s", recorder.Code, recorder.Body.String())
		}
		var listed struct {
			Rooms []struct {
				ID              string `json:"id"`
				GuestLinkActive bool   `json:"guestLinkActive"`
			} `json:"rooms"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil {
			t.Fatalf("unmarshal list: %v", err)
		}
		active := map[string]bool{}
		for _, room := range listed.Rooms {
			active[room.ID] = room.GuestLinkActive
		}
		return active
	}

	if active := fetchActive(); active[officeRoomID] {
		t.Fatal("office must not report guestLinkActive before any link is minted")
	}

	recorder := doRoomsRequest(t, roomActionHandler, http.MethodPost, "/rooms/office/guest-links", `{}`, cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("mint office guest link = %d body %s", recorder.Code, recorder.Body.String())
	}
	var minted struct {
		Link struct {
			ID string `json:"id"`
		} `json:"link"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &minted); err != nil {
		t.Fatalf("unmarshal mint response: %v", err)
	}
	if active := fetchActive(); !active[officeRoomID] {
		t.Fatal("office must report guestLinkActive while an unrevoked, unexpired link lives")
	}

	// GuestEnabled alone must never keep the flag on: revoking the only link
	// clears it (the next sitting returns to full mode).
	if code := doRoomsRequest(t, roomActionHandler, http.MethodPost, "/rooms/office/guest-links/revoke", fmt.Sprintf(`{"id":%q}`, minted.Link.ID), cookies).Code; code != http.StatusOK {
		t.Fatalf("revoke guest link = %d", code)
	}
	if active := fetchActive(); active[officeRoomID] {
		t.Fatal("guestLinkActive must clear once the last link is revoked")
	}
}

func TestGuestLinkEndpointsMintListRevoke(t *testing.T) {
	cookies := setupRoomsTestEnv(t)
	room, err := appRoomStore().create("Client Room", "", "aj@shareability.com", false)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	recorder := doRoomsRequest(t, roomActionHandler, http.MethodPost, "/rooms/"+room.ID+"/guest-links", `{"label":"friday demo","ttlHours":48}`, cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("mint guest link = %d body %s", recorder.Code, recorder.Body.String())
	}
	var minted struct {
		URL  string `json:"url"`
		Link struct {
			ID      string    `json:"id"`
			Label   string    `json:"label"`
			Expires time.Time `json:"expires"`
		} `json:"link"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &minted); err != nil {
		t.Fatalf("unmarshal mint response: %v", err)
	}
	// Fragment-carried token (§6.3): the one-time URL is /g#<64hex>.
	if !strings.HasPrefix(minted.URL, "/g#") || !isGuestTokenShape(strings.TrimPrefix(minted.URL, "/g#")) {
		t.Fatalf("mint URL must be fragment-form /g#<64hex>, got %q", minted.URL)
	}
	token := strings.TrimPrefix(minted.URL, "/g#")
	wantExpiry := time.Now().Add(48 * time.Hour)
	if minted.Link.Expires.Before(wantExpiry.Add(-time.Minute)) || minted.Link.Expires.After(wantExpiry.Add(time.Minute)) {
		t.Fatalf("ttlHours=48 should expire ~48h out, got %v", minted.Link.Expires)
	}

	recorder = doRoomsRequest(t, roomActionHandler, http.MethodGet, "/rooms/"+room.ID+"/guest-links", "", cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list guest links = %d", recorder.Code)
	}
	listBody := recorder.Body.String()
	if strings.Contains(listBody, token) || strings.Contains(listBody, `"hash"`) {
		t.Fatalf("guest-link list must never carry the token or hash, got %s", listBody)
	}
	if !strings.Contains(listBody, "friday demo") {
		t.Fatalf("guest-link list should show the label, got %s", listBody)
	}

	if code := doRoomsRequest(t, roomActionHandler, http.MethodPost, "/rooms/"+room.ID+"/guest-links/revoke", fmt.Sprintf(`{"id":%q}`, minted.Link.ID), cookies).Code; code != http.StatusOK {
		t.Fatalf("revoke guest link = %d", code)
	}
	if _, ok := appRoomStore().redeemGuestToken(token); ok {
		t.Fatal("a revoked link must stop redeeming immediately")
	}
}

/* ---------- HTTP: /g page + shim ---------- */

func TestGuestPageHeadersAndShimRedirect(t *testing.T) {
	setupRoomsTestEnv(t)

	previousIndex := indexHTML
	indexHTML = []byte("<!doctype html><title>bonfire test</title>")
	t.Cleanup(func() { indexHTML = previousIndex })

	recorder := doRoomsRequest(t, guestPageHandler, http.MethodGet, "/g", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /g = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("GET /g Referrer-Policy = %q, want no-referrer", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("GET /g Cache-Control = %q, want no-store", got)
	}
	if !strings.Contains(recorder.Body.String(), "bonfire test") {
		t.Fatal("GET /g must serve the index bytes")
	}

	token := strings.Repeat("ab", 32)
	recorder = doRoomsRequest(t, guestPageHandler, http.MethodGet, "/g/"+token, "", nil)
	if recorder.Code != http.StatusFound {
		t.Fatalf("GET /g/<token> = %d, want 302", recorder.Code)
	}
	if got := recorder.Header().Get("Location"); got != "/g#"+token {
		t.Fatalf("shim Location = %q, want /g#%s", got, token)
	}
	if got := recorder.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("shim Referrer-Policy = %q, want no-referrer", got)
	}
	// The shim writes no HTML body: nothing may echo the token.
	if recorder.Body.Len() != 0 {
		t.Fatalf("shim response must not echo the token in a body, got %q", recorder.Body.String())
	}

	if code := doRoomsRequest(t, guestPageHandler, http.MethodGet, "/g/not-a-token", "", nil).Code; code != http.StatusNotFound {
		t.Fatalf("GET /g/<garbage> = %d, want 404", code)
	}
	if code := doRoomsRequest(t, guestPageHandler, http.MethodGet, "/g/"+strings.ToUpper(token), "", nil).Code; code != http.StatusNotFound {
		t.Fatalf("GET /g/<uppercase> = %d, want 404 (tokens are lowercase hex)", code)
	}
}
