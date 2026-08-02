package main

// This file is the E9 application/runtime integration drill. Its evidence is
// deliberately narrow: local deterministic integration only. It does not call
// a model provider, open a WebRTC session, exercise redundant infrastructure,
// touch production data, or qualify a release.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const e9VerticalDrillTestName = "TestE9LocalDeterministicVerticalIntegrationDrill"

type e9STRIDEStatusResponse struct {
	OK      bool `json:"ok"`
	Runtime struct {
		State            STRIDERuntimeState              `json:"state"`
		Configured       bool                            `json:"configured"`
		Restored         bool                            `json:"restored"`
		Generation       uint64                          `json:"generation"`
		ActivationFenced bool                            `json:"activationFenced"`
		Capabilities     []STRIDERuntimeCapabilityHealth `json:"capabilities"`
		Features         []STRIDEFeatureState            `json:"features"`
	} `json:"runtime"`
}

type e9STRIDEReadResponse struct {
	OK              bool              `json:"ok"`
	Available       bool              `json:"available"`
	Reason          string            `json:"reason"`
	Listings        []json.RawMessage `json:"listings"`
	Suggestions     []json.RawMessage `json:"suggestions"`
	Runs            []json.RawMessage `json:"runs"`
	Seats           []json.RawMessage `json:"seats"`
	Recommendations []json.RawMessage `json:"recommendations"`
}

func TestE9LocalDeterministicVerticalIntegrationDrill(t *testing.T) {
	// The drill must remain runnable when the paid accounts are empty. Blank
	// credentials are part of its boundary, not a substitute for provider-call
	// instrumentation or E10 quality evidence.
	for _, key := range []string{
		"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "FISCAL_API_KEY", "FISCAL_AI_API_KEY",
		"OPENAI_REALTIME_API_KEY", "OPENAI_TRANSCRIPTION_API_KEY",
	} {
		t.Setenv(key, "")
	}

	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "meeting-memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "kanban-board.json"))
	t.Setenv("BONFIRE_USERS_PATH", filepath.Join(dir, "users.json"))
	t.Setenv("BONFIRE_SESSIONS_PATH", filepath.Join(dir, "sessions.json"))
	t.Setenv("BONFIRE_PUBLIC_URL", "https://bonfire.test")
	t.Setenv("BONFIRE_CANONICAL_TENANT_ID", "bonfire")
	t.Setenv("STRIDE_RUNTIME_ENABLED", "true")
	t.Setenv("STRIDE_RUNTIME_BOOTSTRAP_EMPTY", "true")
	t.Setenv("STRIDE_RUNTIME_MIN_GENERATION", "91")
	t.Setenv("STRIDE_RUNTIME_RECALL_THREAD_IDS", "team")
	t.Setenv("STRIDE_RUNTIME_SNAPSHOT_KEY_ID", "e9_local_deterministic_key")
	t.Setenv("STRIDE_RUNTIME_SNAPSHOT_MAC_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	resetAuthRateLimitersForTest()

	previousApp := kanbanApp
	var activeApp *kanbanBoardApp
	t.Cleanup(func() {
		if activeApp != nil {
			_ = activeApp.Close()
		}
		kanbanApp = previousApp
	})

	app := newKanbanBoardApp()
	activeApp = app
	kanbanApp = app
	if health := app.strideRuntime.Health(); health.State != STRIDERuntimeStandby || !health.Configured || health.Restored {
		t.Fatalf("fresh app runtime health=%+v, want configured local standby without a restore claim", health)
	}

	// Authenticate through the actual login handler before using the STRIDE
	// product routes. Login also persists the real member roster used to derive
	// the public-channel audience; the test never supplies an audience itself.
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")

	// Production has one permanent #team record. Seed that durable source with
	// its stable identity, then use the real, serialized chat commit path for
	// the message. This intentionally avoids appending directly to STRIDE.
	now := time.Date(2026, 7, 30, 21, 0, 0, 0, time.UTC)
	team := scoutChatThreadRecord{
		ID: "team", Title: "team", Preview: "new team channel",
		OwnerEmail: "aj@shareability.com", CreatedBy: "AJ",
		Visibility: scoutChatVisibilityPublic, Table: true,
		CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano),
	}
	encodedTeam, err := encodeScoutChatThread(team)
	if err != nil {
		t.Fatal(err)
	}
	if _, appended, err := app.memory.appendScoutChatThread(team.ID, encodedTeam, scoutChatThreadMetadata(team)); err != nil || !appended {
		t.Fatalf("seed durable #team source: appended=%t err=%v", appended, err)
	}
	publicMessage := scoutChatMessageRecord{
		ID: "e9_team_message_1", Kind: "message", Role: "user",
		Text: "Erick shared the launch brief in #team.", CreatedAt: now.Add(time.Minute).Format(time.RFC3339Nano),
		AuthorName: "AJ", AuthorEmail: "aj@shareability.com",
	}
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", team.ID, publicMessage); err != nil {
		t.Fatalf("commit real #team message: %v", err)
	}

	// A private thread travels through the same durable product write path but
	// must never be observed by the organization conversation adapter.
	privateThread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Private planning", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	privateMessage := scoutChatMessageRecord{
		ID: "e9_private_message_1", Kind: "message", Role: "user",
		Text: "This private canary must never reach STRIDE.", CreatedAt: now.Add(2 * time.Minute).Format(time.RFC3339Nano),
		AuthorName: "AJ", AuthorEmail: "aj@shareability.com",
	}
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", privateThread.ID, privateMessage); err != nil {
		t.Fatalf("commit private source canary: %v", err)
	}

	memberPrincipal := strideRuntimePrincipalForEmail("aj@shareability.com")
	outsiderPrincipal := strideRuntimePrincipalForEmail("outsider@example.com")
	if memberPrincipal == "" || outsiderPrincipal == "" {
		t.Fatal("test principals were not derived")
	}
	if err := app.strideRuntime.WithTenantDomains("bonfire", func(domains STRIDERuntimeDomains) error {
		projection, projectErr := domains.ConversationLedger.ProjectForTenantPrincipal("bonfire", memberPrincipal)
		if projectErr != nil || len(projection) != 1 || projection[0].ThreadID != team.ID || projection[0].SourceID != publicMessage.ID || !projection[0].RecallEligible {
			t.Fatalf("#team projection=%+v err=%v", projection, projectErr)
		}
		outsider, projectErr := domains.ConversationLedger.ProjectForTenantPrincipal("bonfire", outsiderPrincipal)
		if projectErr != nil || len(outsider) != 0 {
			t.Fatalf("outsider projection=%+v err=%v", outsider, projectErr)
		}
		snapshot, snapshotErr := domains.ConversationLedger.Snapshot()
		if snapshotErr != nil || len(snapshot.Events) != 1 || snapshot.Events[0].Append.Event.SourceID != publicMessage.ID {
			t.Fatalf("conversation snapshot=%+v err=%v", snapshot, snapshotErr)
		}

		// Store a complete-shaped marketplace candidate inside the runtime. E8
		// must still mark it unavailable, and the product API must hide it until
		// a separately reviewed activation receipt exists.
		manifest := strideMarketplaceManifestForTest()
		actor := STRIDEWorkforceActor{ID: "member_aj", IsAdmin: true}
		if _, ingestErr := domains.Marketplace.IngestPackage(actor, manifest, strideMarketplaceManifestReference(manifest), now); ingestErr != nil {
			return ingestErr
		}
		record, reviewErr := domains.Marketplace.ReviewListing(actor, strideMarketplaceListingForTest(manifest, true), strideTestDigest("e"), now)
		if reviewErr != nil || record.Available || record.State != STRIDEListingUnavailable {
			t.Fatalf("marketplace candidate escaped E8 fence: record=%+v err=%v", record, reviewErr)
		}
		for _, feature := range []STRIDEFeature{
			STRIDEFeatureSuggestedWorkDetection,
			STRIDEFeatureSuggestedWorkExecution,
			STRIDEFeatureMarketplaceDiscovery,
			STRIDEFeatureMarketplaceTrial,
			STRIDEFeatureModelRouteCanary,
		} {
			if enableErr := domains.Registry.SetFeatureEnabled(feature, true); !errors.Is(enableErr, ErrSTRIDEActivationFenced) {
				t.Fatalf("feature %s activation=%v, want E9 fence", feature, enableErr)
			}
		}
		if domains.WorkOrchestrator.Enabled || domains.WorkOrchestrator.Activation != nil {
			t.Fatal("app-owned work orchestrator unexpectedly has launch authority")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	calledWrongTenant := false
	if err := app.strideRuntime.WithTenantDomains("other_tenant", func(STRIDERuntimeDomains) error {
		calledWrongTenant = true
		return nil
	}); !errors.Is(err, ErrSTRIDERuntimeTenantDenied) || calledWrongTenant {
		t.Fatalf("cross-tenant callback err=%v called=%t", err, calledWrongTenant)
	}
	if _, err := app.strideRuntime.AdmitSuggestedWorkCandidate(context.Background(), "bonfire", STRIDEWorkIntentCandidate{}); !errors.Is(err, ErrSTRIDEWorkDisabled) {
		t.Fatalf("suggested-work admission=%v, want disabled before candidate parsing or provider work", err)
	}
	if err := app.strideRuntime.Save(); err != nil {
		t.Fatalf("persist integrated state: %v", err)
	}

	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)

	unauthorized := e9STRIDERequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"status", nil, "")
	if unauthorized.Code != http.StatusUnauthorized || unauthorized.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unauthenticated status=%d headers=%v body=%s", unauthorized.Code, unauthorized.Header(), unauthorized.Body.String())
	}
	foreignOrigin := e9STRIDERequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"status", cookies, "https://untrusted.example")
	if foreignOrigin.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status=%d body=%s", foreignOrigin.Code, foreignOrigin.Body.String())
	}
	wrongMethod := e9STRIDERequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"status", cookies, "")
	if wrongMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("mutation method status=%d body=%s", wrongMethod.Code, wrongMethod.Body.String())
	}

	status := e9ReadSTRIDEStatus(t, mux, cookies)
	if status.Runtime.State != STRIDERuntimeStandby || !status.Runtime.Configured || status.Runtime.Restored || status.Runtime.Generation < 91 || !status.Runtime.ActivationFenced {
		t.Fatalf("fresh authenticated runtime status=%+v", status.Runtime)
	}
	e9AssertEveryFeatureOff(t, status.Runtime.Features)
	e9AssertUnavailableReadSurface(t, mux, cookies, "marketplace", "listings")
	e9AssertUnavailableReadSurface(t, mux, cookies, "work", "suggestions")
	e9AssertUnavailableReadSurface(t, mux, cookies, "roster", "seats")

	// Public operational health may report typed state, but it must not grant
	// authority or leak the runtime's key/path details.
	capabilityRecorder := httptest.NewRecorder()
	capabilitiesHandler(capabilityRecorder, httptest.NewRequest(http.MethodGet, "/capabilities", nil))
	if capabilityRecorder.Code != http.StatusOK {
		t.Fatalf("capabilities status=%d body=%s", capabilityRecorder.Code, capabilityRecorder.Body.String())
	}
	var capabilityPayload struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(capabilityRecorder.Body.Bytes(), &capabilityPayload); err != nil {
		t.Fatal(err)
	}
	var runtimeCapability struct {
		Status           string             `json:"status"`
		State            STRIDERuntimeState `json:"state"`
		ActivationFenced bool               `json:"activationFenced"`
	}
	if err := json.Unmarshal(capabilityPayload.Capabilities["strideRuntime"], &runtimeCapability); err != nil {
		t.Fatal(err)
	}
	if runtimeCapability.Status != "standby" || runtimeCapability.State != STRIDERuntimeStandby || !runtimeCapability.ActivationFenced {
		t.Fatalf("public runtime capability=%+v", runtimeCapability)
	}
	if strings.Contains(capabilityRecorder.Body.String(), "e9_local_deterministic_key") || strings.Contains(capabilityRecorder.Body.String(), dir) {
		t.Fatalf("public capability leaked runtime authority detail: %s", capabilityRecorder.Body.String())
	}

	firstGeneration := app.strideRuntime.Health().Generation
	if err := app.Close(); err != nil {
		t.Fatalf("close first app: %v", err)
	}
	activeApp = nil

	// Boot a new application instance over the same durable product and STRIDE
	// files. Startup replay must remain idempotent, restore the hidden
	// marketplace candidate as unavailable, and still exclude the private row.
	restarted := newKanbanBoardApp()
	activeApp = restarted
	kanbanApp = restarted
	restoredHealth := restarted.strideRuntime.Health()
	if restoredHealth.State != STRIDERuntimeStandby || !restoredHealth.Restored || restoredHealth.Generation <= firstGeneration {
		t.Fatalf("restart health=%+v firstGeneration=%d", restoredHealth, firstGeneration)
	}
	if err := restarted.strideRuntime.WithTenantDomains("bonfire", func(domains STRIDERuntimeDomains) error {
		projection, projectErr := domains.ConversationLedger.ProjectForTenantPrincipal("bonfire", memberPrincipal)
		if projectErr != nil || len(projection) != 1 || projection[0].SourceID != publicMessage.ID {
			t.Fatalf("restored #team projection=%+v err=%v", projection, projectErr)
		}
		snapshot, snapshotErr := domains.ConversationLedger.Snapshot()
		if snapshotErr != nil || len(snapshot.Events) != 1 {
			t.Fatalf("restored conversation snapshot events=%d err=%v", len(snapshot.Events), snapshotErr)
		}
		listing, listingErr := domains.Marketplace.ListingView("listing_marketing_v1")
		if listingErr != nil || listing.Available || listing.State != STRIDEListingUnavailable {
			t.Fatalf("restored marketplace listing=%+v err=%v", listing, listingErr)
		}
		if enableErr := domains.Registry.SetFeatureEnabled(STRIDEFeatureModelRouteCanary, true); !errors.Is(enableErr, ErrSTRIDEActivationFenced) {
			t.Fatalf("restored provider-route activation=%v", enableErr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	restoredStatus := e9ReadSTRIDEStatus(t, mux, cookies)
	if !restoredStatus.Runtime.Restored || restoredStatus.Runtime.Generation != restoredHealth.Generation {
		t.Fatalf("authenticated restored status=%+v health=%+v", restoredStatus.Runtime, restoredHealth)
	}
	e9AssertEveryFeatureOff(t, restoredStatus.Runtime.Features)
	e9AssertUnavailableReadSurface(t, mux, cookies, "marketplace", "listings")
	e9AssertUnavailableReadSurface(t, mux, cookies, "work", "suggestions")

	snapshotRaw, err := os.ReadFile(filepath.Join(dir, defaultSTRIDERuntimeSnapshot))
	if err != nil {
		t.Fatal(err)
	}
	for _, privateCanary := range []string{privateThread.ID, privateMessage.ID, privateMessage.Text} {
		if strings.Contains(string(snapshotRaw), privateCanary) {
			t.Fatalf("private canary %q reached STRIDE snapshot", privateCanary)
		}
	}
	if strings.Contains(string(snapshotRaw), publicMessage.Text) {
		t.Fatal("conversation body crossed into the body-free STRIDE runtime snapshot")
	}

	t.Log("E9_EVIDENCE_CLASS=local_deterministic_integration")
	t.Log("E9_PROVED=authenticated_runtime_http,durable_team_projection,private_exclusion,tenant_boundary,default_off_work_marketplace_provider_routes,signed_restart_restore")
	t.Log("E9_NOT_PROVED=paid_provider_quality,realtime_audio_or_video,physical_devices,production_data,production_restore,ha_failover,live_soak,release_or_deployment")
}

func e9STRIDERequest(t *testing.T, handler http.Handler, method, path string, cookies []*http.Cookie, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func e9ReadSTRIDEStatus(t *testing.T, handler http.Handler, cookies []*http.Cookie) e9STRIDEStatusResponse {
	t.Helper()
	recorder := e9STRIDERequest(t, handler, http.MethodGet, strideRuntimeAPIBase+"status", cookies, "")
	if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("authenticated status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	var response e9STRIDEStatusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("authenticated status payload=%+v", response)
	}
	return response
}

func e9AssertEveryFeatureOff(t *testing.T, features []STRIDEFeatureState) {
	t.Helper()
	if len(features) == 0 {
		t.Fatal("runtime returned no independently fenced features")
	}
	seen := map[STRIDEFeature]bool{}
	for _, feature := range features {
		if feature.Enabled {
			t.Fatalf("feature %s was enabled in E9", feature.Feature)
		}
		seen[feature.Feature] = true
	}
	for _, required := range []STRIDEFeature{
		STRIDEFeatureSuggestedWorkDetection,
		STRIDEFeatureSuggestedWorkExecution,
		STRIDEFeatureMarketplaceDiscovery,
		STRIDEFeatureMarketplaceTrial,
		STRIDEFeatureModelRouteCanary,
	} {
		if !seen[required] {
			t.Fatalf("runtime status omitted feature fence %s", required)
		}
	}
}

func e9AssertUnavailableReadSurface(t *testing.T, handler http.Handler, cookies []*http.Cookie, route, collection string) {
	t.Helper()
	recorder := e9STRIDERequest(t, handler, http.MethodGet, strideRuntimeAPIBase+route, cookies, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s read status=%d body=%s", route, recorder.Code, recorder.Body.String())
	}
	var response e9STRIDEReadResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.Available || !oneOf(response.Reason, "feature_disabled", "product_preview_disabled") {
		t.Fatalf("%s read escaped activation fence: %+v", route, response)
	}
	var count int
	switch collection {
	case "listings":
		count = len(response.Listings)
	case "suggestions":
		count = len(response.Suggestions) + len(response.Runs)
	case "seats":
		count = len(response.Seats) + len(response.Recommendations)
	default:
		t.Fatalf("unknown collection %q", collection)
	}
	if count != 0 {
		t.Fatalf("%s exposed %d unavailable records", route, count)
	}
}
