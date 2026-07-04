package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// dealRoomTestEnv wires the auth env + an isolated app installed as the global
// kanbanApp (the handlers read the global), and returns admin + member cookies.
// admin is aj@shareability.com (isArtifactApprovalAdmin); member is not.
func dealRoomTestEnv(t *testing.T) (adminCookies, memberCookies []*http.Cookie) {
	t.Helper()
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	return loginAs(t, "aj@shareability.com", "B0NFIRE!"), loginAs(t, "tim@shareability.com", "B0NFIRE!")
}

func dealRoomRequest(t *testing.T, method, path, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	switch {
	case path == "/assistant/deal-room/request":
		assistantDealRoomRequestHandler(recorder, req)
	case path == "/assistant/deal-room/resolve":
		assistantDealRoomResolveHandler(recorder, req)
	case path == "/assistant/deal-room/revoke":
		assistantDealRoomRevokeHandler(recorder, req)
	case path == "/assistant/deal-room/list":
		assistantDealRoomListHandler(recorder, req)
	case strings.HasPrefix(path, "/deal-room/"):
		dealRoomPublicHandler(recorder, req)
	default:
		t.Fatalf("unknown deal room path %q", path)
	}
	return recorder
}

// seedPackageWithBinder creates a package and attaches a binder artifact whose
// body carries an injected <script> to prove the public renderer escapes it.
func seedPackageWithBinder(t *testing.T, body string) (packageID, artifactID string) {
	t.Helper()
	pkg, err := kanbanApp.createVenturePackage("Aurora", "an IP thesis", "AJ")
	if err != nil {
		t.Fatalf("create package: %v", err)
	}
	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Aurora binder", body, "AJ", map[string]string{
		"toolTemplate":     "package_assembly",
		"artifactContract": "package_binder_v1",
		"title":            "Aurora — assembled binder",
	})
	if err != nil {
		t.Fatalf("create binder artifact: %v", err)
	}
	if _, err := kanbanApp.attachToPackage(pkg.ID, "artifact", artifact.ID, "AJ"); err != nil {
		t.Fatalf("attach binder: %v", err)
	}
	return pkg.ID, artifact.ID
}

func decodeJSON(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode body %q: %v", recorder.Body.String(), err)
	}
	return payload
}

func TestDealRoomRequestLandsPending(t *testing.T) {
	_, member := dealRoomTestEnv(t)
	packageID, _ := seedPackageWithBinder(t, "# Aurora\n\nA strong deal.")

	recorder := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, packageID), member)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	payload := decodeJSON(t, recorder)
	if payload["status"] != dealRoomStatusPending {
		t.Fatalf("status=%v, want pending", payload["status"])
	}
	if strings.TrimSpace(fmt.Sprint(payload["id"])) == "" {
		t.Fatal("expected an id")
	}

	// Idempotent-ish: a second identical request returns the same pending room.
	second := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, packageID), member)
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	if decodeJSON(t, second)["id"] != payload["id"] {
		t.Fatal("second request must return the same pending room id")
	}
}

func TestDealRoomRequestWithoutBinderIs400(t *testing.T) {
	_, member := dealRoomTestEnv(t)
	pkg, err := kanbanApp.createVenturePackage("Empty", "no artifacts", "AJ")
	if err != nil {
		t.Fatalf("create package: %v", err)
	}

	recorder := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, pkg.ID), member)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(strings.ToLower(recorder.Body.String()), "binder") {
		t.Fatalf("expected a clear no-binder message, got %s", recorder.Body.String())
	}
}

// TestDealRoomRequestRejectsForeignArtifact proves the SECURITY membership gate:
// a client-supplied artifactId that exists but does NOT belong to the package is
// rejected, so an approved share can never publish an unrelated artifact behind
// a public token.
func TestDealRoomRequestRejectsForeignArtifact(t *testing.T) {
	_, member := dealRoomTestEnv(t)
	packageID, ownArtifactID := seedPackageWithBinder(t, "# Aurora\n\nA strong deal.")

	// A real artifact that exists but is attached to NO package (a private memo).
	foreign, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Secret memo", "# Confidential\n\nnot for sharing", "AJ", map[string]string{"title": "Secret memo"})
	if err != nil {
		t.Fatalf("create foreign artifact: %v", err)
	}

	// The foreign artifactId must be rejected as not part of the package.
	reject := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q,"artifactId":%q}`, packageID, foreign.ID), member)
	if reject.Code != http.StatusBadRequest {
		t.Fatalf("foreign artifact status=%d body=%s, want 400", reject.Code, reject.Body.String())
	}
	if !strings.Contains(strings.ToLower(reject.Body.String()), "not part of this package") {
		t.Fatalf("expected a membership-rejection message, got %s", reject.Body.String())
	}

	// The package's OWN artifactId is still accepted on the explicit path.
	accept := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q,"artifactId":%q}`, packageID, ownArtifactID), member)
	if accept.Code != http.StatusOK {
		t.Fatalf("own artifact status=%d body=%s, want 200", accept.Code, accept.Body.String())
	}
}

func TestDealRoomNonAdminResolveForbidden(t *testing.T) {
	_, member := dealRoomTestEnv(t)
	packageID, _ := seedPackageWithBinder(t, "# Aurora\n\nBody.")
	reqRec := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, packageID), member)
	id := fmt.Sprint(decodeJSON(t, reqRec)["id"])

	recorder := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/resolve", fmt.Sprintf(`{"id":%q,"action":"approve"}`, id), member)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s, want 403", recorder.Code, recorder.Body.String())
	}
}

func TestDealRoomApproveMintsTokenAndServesEscapedBinder(t *testing.T) {
	admin, member := dealRoomTestEnv(t)
	packageID, _ := seedPackageWithBinder(t, "# Aurora\n\nHello <script>alert('xss')</script> world\n\n- one item")
	reqRec := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, packageID), member)
	id := fmt.Sprint(decodeJSON(t, reqRec)["id"])

	// admin approve -> active + minted token
	resolveRec := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/resolve", fmt.Sprintf(`{"id":%q,"action":"approve"}`, id), admin)
	if resolveRec.Code != http.StatusOK {
		t.Fatalf("resolve status=%d body=%s", resolveRec.Code, resolveRec.Body.String())
	}
	room, ok := decodeJSON(t, resolveRec)["dealRoom"].(map[string]any)
	if !ok {
		t.Fatalf("no dealRoom payload: %s", resolveRec.Body.String())
	}
	if room["status"] != dealRoomStatusActive {
		t.Fatalf("status=%v, want active", room["status"])
	}
	url, _ := room["url"].(string)
	if !strings.HasPrefix(url, "/deal-room/") || len(strings.TrimPrefix(url, "/deal-room/")) < 24 {
		t.Fatalf("url=%q, want a minted /deal-room/<token>", url)
	}

	// public GET serves the binder with the injected script escaped
	pageRec := dealRoomRequest(t, http.MethodGet, url, "", nil)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("public status=%d", pageRec.Code)
	}
	if ct := pageRec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type=%q", ct)
	}
	pageBody := pageRec.Body.String()
	if strings.Contains(pageBody, "<script>alert(") {
		t.Fatal("injected <script> must be escaped, not served raw")
	}
	if !strings.Contains(pageBody, "&lt;script&gt;") {
		t.Fatalf("expected escaped script marker in page: %s", pageBody)
	}
	if !strings.Contains(pageBody, "read-only") || !strings.Contains(pageBody, "Provenance") {
		t.Fatal("expected read-only provenance chrome")
	}
}

func TestDealRoomRevokeThen404(t *testing.T) {
	admin, member := dealRoomTestEnv(t)
	packageID, _ := seedPackageWithBinder(t, "# Aurora\n\nBody.")
	reqRec := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, packageID), member)
	id := fmt.Sprint(decodeJSON(t, reqRec)["id"])
	resolveRec := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/resolve", fmt.Sprintf(`{"id":%q,"action":"approve"}`, id), admin)
	room := decodeJSON(t, resolveRec)["dealRoom"].(map[string]any)
	url := room["url"].(string)

	// live before revoke
	if live := dealRoomRequest(t, http.MethodGet, url, "", nil); live.Code != http.StatusOK {
		t.Fatalf("expected 200 before revoke, got %d", live.Code)
	}

	if rev := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/revoke", fmt.Sprintf(`{"id":%q}`, id), admin); rev.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", rev.Code, rev.Body.String())
	}

	if after := dealRoomRequest(t, http.MethodGet, url, "", nil); after.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after revoke, got %d", after.Code)
	}
}

func TestDealRoomListScoping(t *testing.T) {
	admin, member := dealRoomTestEnv(t)
	packageID, _ := seedPackageWithBinder(t, "# Aurora\n\nBody.")
	// member's own request
	dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, packageID), member)

	// member sees their own room, no canApprove
	memberList := dealRoomRequest(t, http.MethodGet, "/assistant/deal-room/list", "", member)
	memberPayload := decodeJSON(t, memberList)
	if memberPayload["canApprove"] != false {
		t.Fatalf("member canApprove=%v, want false", memberPayload["canApprove"])
	}
	memberRooms, _ := memberPayload["rooms"].([]any)
	if len(memberRooms) != 1 {
		t.Fatalf("member rooms=%d, want 1", len(memberRooms))
	}

	// admin sees all rooms and canApprove true
	adminList := dealRoomRequest(t, http.MethodGet, "/assistant/deal-room/list", "", admin)
	adminPayload := decodeJSON(t, adminList)
	if adminPayload["canApprove"] != true {
		t.Fatalf("admin canApprove=%v, want true", adminPayload["canApprove"])
	}
	adminRooms, _ := adminPayload["rooms"].([]any)
	if len(adminRooms) != 1 {
		t.Fatalf("admin rooms=%d, want 1", len(adminRooms))
	}
}

func TestDealRoomUnknownToken404(t *testing.T) {
	dealRoomTestEnv(t)
	recorder := dealRoomRequest(t, http.MethodGet, "/deal-room/this-token-does-not-exist", "", nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", recorder.Code)
	}
	if ct := recorder.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type=%q, want html 404 page", ct)
	}
}
