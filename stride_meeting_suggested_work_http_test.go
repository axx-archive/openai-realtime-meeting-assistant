package main

// This test is the token-free product acceptance seam for meeting-origin
// Suggested Work. It intentionally enters through authenticated HTTP and
// derives sitting, media generation, participants, consent, recipients, and
// tenant from live server state. It does not call a model provider.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

const strideMeetingSuggestedWorkHTTPTestName = "TestSTRIDEProductMeetingSuggestionHTTPUsesCurrentConsentedMemberSet"

type strideMeetingSuggestedWorkHTTPResponse struct {
	OK            bool                      `json:"ok"`
	Available     bool                      `json:"available"`
	Source        string                    `json:"source"`
	ProviderCalls int                       `json:"providerCalls"`
	Suggestion    STRIDEProductWorkRecord   `json:"suggestion"`
	Suggestions   []STRIDEProductWorkRecord `json:"suggestions"`
	Runs          []STRIDEDurableWorkRun    `json:"runs"`
}

func TestSTRIDEProductMeetingSuggestionHTTPUsesCurrentConsentedMemberSet(t *testing.T) {
	for _, key := range []string{
		"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "FISCAL_API_KEY", "FISCAL_AI_API_KEY",
		"OPENAI_REALTIME_API_KEY", "OPENAI_TRANSCRIPTION_API_KEY",
	} {
		t.Setenv(key, "")
	}
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "meeting-memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "kanban-board.json"))
	t.Setenv("MEETINGS_PATH", filepath.Join(dir, "meetings.json"))
	t.Setenv("ADMISSION_ANCHORS_PATH", filepath.Join(dir, "admission-anchors.json"))
	t.Setenv("NOTIFICATIONS_PATH", filepath.Join(dir, "notifications.json"))
	t.Setenv("BONFIRE_USERS_PATH", filepath.Join(dir, "users.json"))
	t.Setenv("BONFIRE_SESSIONS_PATH", filepath.Join(dir, "sessions.json"))
	t.Setenv("BONFIRE_ROOMS_PATH", filepath.Join(dir, "rooms.json"))
	t.Setenv("BONFIRE_PUBLIC_URL", "https://bonfire.test")
	t.Setenv("BONFIRE_CANONICAL_TENANT_ID", "bonfire")
	t.Setenv("STRIDE_RUNTIME_ENABLED", "true")
	t.Setenv("STRIDE_RUNTIME_BOOTSTRAP_EMPTY", "true")
	t.Setenv("STRIDE_RUNTIME_MIN_GENERATION", "211")
	t.Setenv("STRIDE_RUNTIME_RECALL_THREAD_IDS", "team")
	t.Setenv("STRIDE_RUNTIME_SNAPSHOT_KEY_ID", "meeting_suggested_work_test_key")
	t.Setenv("STRIDE_RUNTIME_SNAPSHOT_MAC_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	t.Setenv("STRIDE_LOCAL_PRODUCT_PREVIEW_ENABLED", "true")
	t.Setenv("STRIDE_MEETING_SPECIALIST_CONTROL_ENABLED", "false")
	resetAuthRateLimitersForTest()

	previousApp := kanbanApp
	app := newKanbanBoardApp()
	kanbanApp = app
	t.Cleanup(func() {
		_ = app.Close()
		kanbanApp = previousApp
	})
	if health := app.strideRuntime.Health(); health.State != STRIDERuntimeStandby || !health.Configured {
		t.Fatalf("meeting Suggested Work runtime=%+v", health)
	}

	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)
	ajCookies := loginAs(t, "aj@shareability.com", defaultMeetingRoomPassword)
	timCookies := loginAs(t, "tim@shareability.com", defaultMeetingRoomPassword)
	tomCookies := loginAs(t, "tom@shareability.com", defaultMeetingRoomPassword)

	consent := newAmbientConsentAuthorityForTest(t)
	sittingID := app.prepareMeetingSittingID(officeRoomID)
	memberSessions := map[string]string{
		"aj@shareability.com":  "meeting-suggested-work-aj-session",
		"tim@shareability.com": "meeting-suggested-work-tim-session",
	}
	for email, sessionID := range memberSessions {
		name := participantNameForEmail(email)
		if _, _, err := app.admitParticipantWithAnchor(context.Background(), officeRoomID, name, sessionID, sessionID+"-endpoint", sittingID, memberAdmissionPrincipal(email)); err != nil {
			t.Fatalf("admit %s: %v", email, err)
		}
		if got := app.noteMeetingAdmissionForSitting(officeRoomID, name, sittingID); got != sittingID {
			t.Fatalf("open sitting for %s=%q want=%q", email, got, sittingID)
		}
	}
	// Authenticated employees inherit every consent lane from the internal
	// rules-of-the-road policy; no per-sitting permission ceremony is required.
	mediaGeneration := app.ensureRoomMedia(officeRoomID)
	if mediaGeneration == 0 || !app.roomMediaGenerationCurrent(officeRoomID, mediaGeneration) || app.activeParticipantCount(officeRoomID) != 2 {
		t.Fatalf("current meeting scope generation=%d participants=%d", mediaGeneration, app.activeParticipantCount(officeRoomID))
	}

	endpoint := strideRuntimeAPIBase + "work/suggestions/from-meeting"
	// Tenant, recipient, sitting, and evidence authority are never accepted
	// from the client. They are derived only after member authentication.
	unauthenticated := strideMeetingSuggestedWorkRequest(t, mux, http.MethodPost, endpoint, nil, map[string]any{"roomId": officeRoomID}, "")
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated meeting suggestion status=%d body=%s", unauthenticated.Code, unauthenticated.Body.String())
	}
	crossTenantQuery := strideMeetingSuggestedWorkRequest(t, mux, http.MethodPost, endpoint+"?tenantId=other_tenant", ajCookies, map[string]any{"roomId": officeRoomID}, "")
	if crossTenantQuery.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant query status=%d body=%s", crossTenantQuery.Code, crossTenantQuery.Body.String())
	}
	crossTenantBody := strideMeetingSuggestedWorkRequest(t, mux, http.MethodPost, endpoint, ajCookies, map[string]any{"roomId": officeRoomID, "tenantId": "other_tenant"}, "")
	if crossTenantBody.Code != http.StatusBadRequest {
		t.Fatalf("cross-tenant body status=%d body=%s", crossTenantBody.Code, crossTenantBody.Body.String())
	}
	forgedRecipients := strideMeetingSuggestedWorkRequest(t, mux, http.MethodPost, endpoint, ajCookies, map[string]any{
		"roomId": officeRoomID, "sittingId": sittingID, "recipientIds": []string{strideRuntimePrincipalForEmail("tom@shareability.com")},
	}, "")
	if forgedRecipients.Code != http.StatusBadRequest {
		t.Fatalf("client-forged scope status=%d body=%s", forgedRecipients.Code, forgedRecipients.Body.String())
	}
	wrongRoom := strideMeetingSuggestedWorkRequest(t, mux, http.MethodPost, endpoint, ajCookies, map[string]any{"roomId": "other-room"}, "")
	if wrongRoom.Code != http.StatusForbidden {
		t.Fatalf("cross-room suggestion status=%d body=%s", wrongRoom.Code, wrongRoom.Body.String())
	}
	nonParticipantCreate := strideMeetingSuggestedWorkRequest(t, mux, http.MethodPost, endpoint, tomCookies, map[string]any{"roomId": officeRoomID}, "")
	if nonParticipantCreate.Code != http.StatusForbidden {
		t.Fatalf("non-participant suggestion status=%d body=%s", nonParticipantCreate.Code, nonParticipantCreate.Body.String())
	}

	meeting, active := app.meetings.activeRecord(officeRoomID)
	if !active || meeting.ID != sittingID {
		t.Fatalf("active meeting=%+v active=%t", meeting, active)
	}
	startedAt, err := time.Parse(time.RFC3339Nano, meeting.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !startedAt.Add(3 * time.Millisecond).Before(time.Now().UTC()) {
		time.Sleep(4 * time.Millisecond)
	}
	recipients := uniqueSortedStrings([]string{
		strideRuntimePrincipalForEmail("aj@shareability.com"),
		strideRuntimePrincipalForEmail("tim@shareability.com"),
	})
	audience := STRIDEAudience{Visibility: "meeting", Principals: append([]string(nil), recipients...)}
	transcriptText := "The meeting agreed Scout should create an Insights & Opportunities report for Dog Perfect from this discussion."
	event := strideTemporalProductTranscriptEvent(canonicalTenantID(), officeRoomID, sittingID, mediaGeneration, startedAt.Add(time.Millisecond), startedAt.Add(2*time.Millisecond), audience, transcriptText)
	config := TemporalMeetingBrainConfig{TenantID: canonicalTenantID(), RoomID: officeRoomID, SittingID: sittingID, SittingStart: startedAt.UTC()}
	if err := app.strideRuntime.ApplyTemporalEvidence(canonicalTenantID(), config, event); err != nil {
		t.Fatalf("apply consent-authorized meeting evidence: %v", err)
	}

	createdRecorder := strideMeetingSuggestedWorkRequest(t, mux, http.MethodPost, endpoint, ajCookies, map[string]any{"roomId": officeRoomID}, "")
	if createdRecorder.Code != http.StatusCreated {
		t.Fatalf("meeting suggestion status=%d body=%s", createdRecorder.Code, createdRecorder.Body.String())
	}
	created := decodeMeetingSuggestedWorkResponse(t, createdRecorder)
	if !created.OK || created.Source != "consent_authorized_meeting" || created.ProviderCalls != 0 || created.Suggestion.ID == "" || created.Suggestion.Status != "suggested" || created.Suggestion.Revision != 1 || created.Suggestion.SourceThreadID != officeRoomID || created.Suggestion.SourceMessageID == "" || created.Suggestion.SourceEvent.Validate() != nil || created.Suggestion.SourceMessageID != created.Suggestion.SourceEvent.ID || created.Suggestion.ProviderExecutionFenced == false || !sameStringSet(created.Suggestion.RecipientIDs, recipients) || !containsString(created.Suggestion.Lifecycle, "recognized_from_consent_authorized_meeting") {
		t.Fatalf("consent-authorized meeting suggestion=%+v response=%+v", created.Suggestion, created)
	}
	if created.Suggestion.Outcome == "" || created.Suggestion.SourceSnippet == "" {
		t.Fatalf("meeting suggestion omitted authorized evidence summary=%+v", created.Suggestion)
	}

	replayedRecorder := strideMeetingSuggestedWorkRequest(t, mux, http.MethodPost, endpoint, ajCookies, map[string]any{"roomId": officeRoomID}, "")
	if replayedRecorder.Code != http.StatusCreated {
		t.Fatalf("meeting suggestion replay status=%d body=%s", replayedRecorder.Code, replayedRecorder.Body.String())
	}
	replayed := decodeMeetingSuggestedWorkResponse(t, replayedRecorder)
	if replayed.Suggestion.ID != created.Suggestion.ID || replayed.Suggestion.Revision != created.Suggestion.Revision || !sameStringSet(replayed.Suggestion.RecipientIDs, created.Suggestion.RecipientIDs) {
		t.Fatalf("meeting suggestion replay was not idempotent: first=%+v replay=%+v", created.Suggestion, replayed.Suggestion)
	}
	assertMeetingSuggestedWorkCount(t, mux, ajCookies, 1)
	assertMeetingSuggestedWorkCount(t, mux, timCookies, 1)
	assertMeetingSuggestedWorkCount(t, mux, tomCookies, 0)
	outsiderRead := strideMeetingSuggestedWorkRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work/suggestions/"+created.Suggestion.ID, tomCookies, nil, "")
	if outsiderRead.Code != http.StatusForbidden {
		t.Fatalf("non-recipient read status=%d body=%s", outsiderRead.Code, outsiderRead.Body.String())
	}
	recipientRead := strideMeetingSuggestedWorkRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work/suggestions/"+created.Suggestion.ID, timCookies, nil, "")
	if recipientRead.Code != http.StatusOK {
		t.Fatalf("current recipient read status=%d body=%s", recipientRead.Code, recipientRead.Body.String())
	}

	guestToken, err := userSessionStore().createGuest(officeRoomID, "Sam")
	if err != nil {
		t.Fatal(err)
	}
	guestCookies := []*http.Cookie{{Name: guestCookieName, Value: guestToken}, {Name: sessionCookieName, Value: guestToken}}
	guestCaller := strideMeetingSuggestedWorkRequest(t, mux, http.MethodPost, endpoint, guestCookies, map[string]any{"roomId": officeRoomID}, "")
	if guestCaller.Code != http.StatusUnauthorized {
		t.Fatalf("guest caller status=%d body=%s", guestCaller.Code, guestCaller.Body.String())
	}

	// Even a fully consented guest makes organization-member publication fail
	// closed: guests may participate in calls, but they never become company-work
	// recipients or widen meeting evidence into the company brain.
	guestKey := temporalDigest("meeting-suggested-work-guest")
	guestSessionID := "meeting-suggested-work-guest-session"
	guestDisplay, _, err := app.admitGuestWithAnchor(context.Background(), officeRoomID, guestKey, "Sam", guestSessionID, sittingID)
	if err != nil {
		t.Fatalf("admit current guest: %v", err)
	}
	if got := app.noteMeetingAdmissionForSitting(officeRoomID, guestDisplay, sittingID); got != sittingID {
		t.Fatalf("guest sitting=%q want=%q", got, sittingID)
	}
	grantMeetingSuggestedWorkConsent(t, app, consent, officeRoomID, sittingID, guestAdmissionPrincipal(guestKey),
		ConsentAudioCapture, ConsentTranscription, ConsentModelAnalysis, ConsentOrgMemory)
	guestPresent := strideMeetingSuggestedWorkRequest(t, mux, http.MethodPost, endpoint, ajCookies, map[string]any{"roomId": officeRoomID}, "")
	if guestPresent.Code != http.StatusForbidden {
		t.Fatalf("guest-present organization suggestion status=%d body=%s", guestPresent.Code, guestPresent.Body.String())
	}
	assertMeetingSuggestedWorkCount(t, mux, ajCookies, 1)

	removed, stillPresent := app.forgetParticipantSessionResultInRoom(officeRoomID, guestDisplay, guestSessionID)
	if !removed || stillPresent {
		t.Fatalf("remove guest removed=%t stillPresent=%t", removed, stillPresent)
	}
	app.releaseGuestSeatIfGone(officeRoomID, guestKey)
	removed, stillPresent = app.forgetParticipantSessionResultInRoom(officeRoomID, participantNameForEmail("tim@shareability.com"), memberSessions["tim@shareability.com"])
	if !removed || stillPresent || app.activeParticipantCount(officeRoomID) != 1 {
		t.Fatalf("remove current member removed=%t stillPresent=%t participants=%d", removed, stillPresent, app.activeParticipantCount(officeRoomID))
	}
	departedParticipant := strideMeetingSuggestedWorkRequest(t, mux, http.MethodPost, endpoint, ajCookies, map[string]any{"roomId": officeRoomID}, "")
	if departedParticipant.Code != http.StatusForbidden {
		t.Fatalf("single/current-participant gate status=%d body=%s", departedParticipant.Code, departedParticipant.Body.String())
	}
	assertMeetingSuggestedWorkCount(t, mux, ajCookies, 1)
}

func grantMeetingSuggestedWorkConsent(t *testing.T, app *kanbanBoardApp, authority *ConsentLaneAuthority, roomID, sittingID string, principal CanonicalPrincipalRef, scopes ...ConsentScope) {
	t.Helper()
	binding, err := app.consentBindingForPrincipal(context.Background(), principal, roomID, sittingID)
	if err != nil {
		t.Fatalf("resolve meeting consent binding for %s: %v", principal.ID, err)
	}
	for _, scope := range scopes {
		grantConsentScope(t, authority, binding, scope)
	}
}

func strideMeetingSuggestedWorkRequest(t *testing.T, handler http.Handler, method, path string, cookies []*http.Cookie, payload any, origin string) *httptest.ResponseRecorder {
	t.Helper()
	var body *bytes.Reader
	if payload == nil {
		body = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, path, body)
	request.Header.Set("Content-Type", "application/json")
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func decodeMeetingSuggestedWorkResponse(t *testing.T, recorder *httptest.ResponseRecorder) strideMeetingSuggestedWorkHTTPResponse {
	t.Helper()
	var response strideMeetingSuggestedWorkHTTPResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode meeting Suggested Work response: %v body=%s", err, recorder.Body.String())
	}
	return response
}

func assertMeetingSuggestedWorkCount(t *testing.T, handler http.Handler, cookies []*http.Cookie, want int) {
	t.Helper()
	recorder := strideMeetingSuggestedWorkRequest(t, handler, http.MethodGet, strideRuntimeAPIBase+"work", cookies, nil, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("work list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeMeetingSuggestedWorkResponse(t, recorder)
	if !response.OK || !response.Available || len(response.Suggestions) != want || len(response.Runs) != 0 {
		t.Fatalf("work list suggestions=%d runs=%d available=%t want=%d body=%s", len(response.Suggestions), len(response.Runs), response.Available, want, recorder.Body.String())
	}
}
