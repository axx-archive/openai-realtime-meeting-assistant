package main

// This integrated graph exercises authenticated product handlers and signed
// durable runtimes with every provider credential blank. It is not evidence of
// provider quality, media-device behavior, deployment, or release readiness.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const e9FounderGraphTestName = "TestE9DeterministicFounderGraphThroughProductEndpoints"

type e9FounderWorkResponse struct {
	OK               bool                      `json:"ok"`
	Available        bool                      `json:"available"`
	Reason           string                    `json:"reason"`
	Suggestion       STRIDEProductWorkRecord   `json:"suggestion"`
	Suggestions      []STRIDEProductWorkRecord `json:"suggestions"`
	Runs             []STRIDEDurableWorkRun    `json:"runs"`
	ProviderCalls    int                       `json:"providerCalls"`
	InputTokens      int                       `json:"inputTokens"`
	OutputTokens     int                       `json:"outputTokens"`
	ProductReachable bool                      `json:"productReachable"`
	Artifact         struct {
		ID                      string `json:"id"`
		Summary                 string `json:"summary"`
		SourceHref              string `json:"sourceHref"`
		DestinationThreadID     string `json:"destinationThreadId"`
		ProviderExecutionFenced bool   `json:"providerExecutionFenced"`
	} `json:"artifact"`
	Evidence struct {
		ThreadID    string          `json:"threadId"`
		MessageID   string          `json:"messageId"`
		SourceEvent STRIDEReference `json:"sourceEvent"`
		BrainLinked bool            `json:"brainLinked"`
	} `json:"evidence"`
}

type e9FounderMarketplaceResponse struct {
	OK                       bool                                `json:"ok"`
	Available                bool                                `json:"available"`
	ProductReachable         bool                                `json:"productReachable"`
	LiveAdmissionFenced      bool                                `json:"liveAdmissionFenced"`
	ProviderRuntimeAvailable bool                                `json:"providerRuntimeAvailable"`
	ProviderSessionStarted   bool                                `json:"providerSessionStarted"`
	ScoutIntroductionPosted  bool                                `json:"scoutIntroductionPosted"`
	Listings                 []STRIDEProductMarketplaceCandidate `json:"listings"`
	Listing                  STRIDEProductMarketplaceCandidate   `json:"listing"`
	Seats                    []STRIDEProductTeamAgent            `json:"seats"`
	Seat                     STRIDEProductTeamAgent              `json:"seat"`
	Export                   STRIDEProductAgentExport            `json:"export"`
	ContainsCredentials      bool                                `json:"containsCredentials"`
	ProviderRuntimeExported  bool                                `json:"providerRuntimeExported"`
}

type e9FounderTemporalResponse struct {
	Answer STRIDETemporalRecallResult `json:"answer"`
}

func TestE9DeterministicFounderGraphThroughProductEndpoints(t *testing.T) {
	t.Setenv(strideLegacyRosterMutationEnv, "")
	t.Setenv(legacyMeetingSpecialistCustomerMutationsEnvironment, "")
	for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "FISCAL_API_KEY", "FISCAL_AI_API_KEY", "OPENAI_REALTIME_API_KEY", "OPENAI_TRANSCRIPTION_API_KEY"} {
		t.Setenv(key, "")
	}
	dir := t.TempDir()
	for key, name := range map[string]string{
		"MEETING_MEMORY_PATH": "meeting-memory.jsonl", "KANBAN_BOARD_PATH": "kanban-board.json", "MEETINGS_PATH": "meetings.json",
		"ADMISSION_ANCHORS_PATH": "admission-anchors.json", "NOTIFICATIONS_PATH": "notifications.json", "BONFIRE_USERS_PATH": "users.json",
		"BONFIRE_SESSIONS_PATH": "sessions.json", "BONFIRE_ROOMS_PATH": "rooms.json",
	} {
		t.Setenv(key, filepath.Join(dir, name))
	}
	t.Setenv("BONFIRE_PUBLIC_URL", "https://bonfire.test")
	t.Setenv("BONFIRE_CANONICAL_TENANT_ID", "bonfire")
	t.Setenv("STRIDE_RUNTIME_ENABLED", "true")
	t.Setenv("STRIDE_RUNTIME_BOOTSTRAP_EMPTY", "true")
	t.Setenv("STRIDE_RUNTIME_MIN_GENERATION", "131")
	t.Setenv("STRIDE_RUNTIME_RECALL_THREAD_IDS", "team")
	t.Setenv("STRIDE_RUNTIME_SNAPSHOT_KEY_ID", "e9_founder_graph_key")
	t.Setenv("STRIDE_RUNTIME_SNAPSHOT_MAC_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	t.Setenv("STRIDE_LOCAL_PRODUCT_PREVIEW_ENABLED", "true")
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
	activeApp, kanbanApp = app, app
	if health := app.strideRuntime.Health(); health.State != STRIDERuntimeStandby || !health.Configured || health.Restored {
		t.Fatalf("fresh runtime health=%+v", health)
	}
	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)
	registerMeetingSpecialistProductRoutes(mux)
	ajCookies := loginAs(t, "aj@shareability.com", defaultMeetingRoomPassword)
	tomCookies := loginAs(t, "tom@shareability.com", defaultMeetingRoomPassword)

	now := time.Now().UTC().Add(-time.Minute)
	team := scoutChatThreadRecord{ID: "team", Title: "team", Preview: "company group chat", OwnerEmail: "aj@shareability.com", CreatedBy: "AJ", Visibility: scoutChatVisibilityPublic, Table: true, CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano)}
	encoded, err := encodeScoutChatThread(team)
	if err != nil {
		t.Fatal(err)
	}
	if _, appended, err := app.memory.appendScoutChatThread(team.ID, encoded, scoutChatThreadMetadata(team)); err != nil || !appended {
		t.Fatalf("seed #team: appended=%t err=%v", appended, err)
	}
	source := scoutChatMessageRecord{ID: "e9_founder_team_outcome", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: "aj@shareability.com", Text: "We need Scout to create an Insights & Opportunities report for Dog Perfect, grounded in this company conversation.", CreatedAt: now.Add(time.Second).Format(time.RFC3339Nano)}
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", team.ID, source); err != nil {
		t.Fatal(err)
	}

	unauthorized := e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work", nil, nil, "")
	if unauthorized.Code != http.StatusUnauthorized || unauthorized.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unauthenticated work status=%d headers=%v", unauthorized.Code, unauthorized.Header())
	}
	foreign := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions", ajCookies, map[string]any{"threadId": team.ID, "messageId": source.ID}, "https://untrusted.example")
	if foreign.Code != http.StatusForbidden {
		t.Fatalf("cross-origin suggestion status=%d body=%s", foreign.Code, foreign.Body.String())
	}
	work := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work", ajCookies, nil, ""), http.StatusOK))
	if !work.OK || !work.Available || work.Reason != "deterministic_local" || len(work.Suggestions) != 1 || len(work.Runs) != 0 {
		t.Fatalf("initial work=%+v", work)
	}
	suggestion := work.Suggestions[0]
	wantRecipients := []string{strideRuntimePrincipalForEmail("aj@shareability.com"), strideRuntimePrincipalForEmail("joel@shareability.com")}
	if suggestion.Status != "suggested" || suggestion.SourceMessageID != source.ID || !suggestion.ProviderExecutionFenced || !sameStringSet(suggestion.RecipientIDs, wantRecipients) {
		t.Fatalf("suggestion=%+v", suggestion)
	}
	outsider := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work", tomCookies, nil, ""), http.StatusOK))
	if len(outsider.Suggestions) != 0 || len(outsider.Runs) != 0 {
		t.Fatalf("non-recipient observed work=%+v", outsider)
	}
	dogPerfect, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Dog Perfect", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	destination := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions/"+suggestion.ID+"/destination", ajCookies, map[string]any{"revision": suggestion.Revision, "mode": "existing", "threadId": dogPerfect.ID}, ""), http.StatusOK)).Suggestion
	approved := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions/"+suggestion.ID+"/approve", ajCookies, map[string]any{"revision": destination.Revision}, ""), http.StatusOK))
	if approved.Suggestion.Status != "completed" || approved.Suggestion.RunID == "" || approved.Suggestion.ArtifactID == "" || approved.ProviderCalls != 0 || approved.InputTokens != 0 || approved.OutputTokens != 0 {
		t.Fatalf("approved work=%+v", approved)
	}
	duplicate := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions/"+suggestion.ID+"/approve", ajCookies, map[string]any{"revision": destination.Revision}, ""), http.StatusOK))
	if duplicate.Suggestion.RunID != approved.Suggestion.RunID || duplicate.Suggestion.ArtifactID != approved.Suggestion.ArtifactID {
		t.Fatalf("approval replay=%+v", duplicate.Suggestion)
	}
	artifact := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, approved.Suggestion.ArtifactHref, ajCookies, nil, ""), http.StatusOK))
	evidence := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, approved.Suggestion.BrainHref, ajCookies, nil, ""), http.StatusOK))
	if artifact.Artifact.ID != approved.Suggestion.ArtifactID || artifact.Artifact.DestinationThreadID != dogPerfect.ID || artifact.Artifact.SourceHref != approved.Suggestion.BrainHref || !artifact.Artifact.ProviderExecutionFenced || artifact.Artifact.Summary == "" || evidence.Evidence.ThreadID != team.ID || evidence.Evidence.MessageID != source.ID || evidence.Evidence.SourceEvent != suggestion.SourceEvent || !evidence.Evidence.BrainLinked {
		t.Fatalf("artifact/evidence mismatch: artifact=%+v evidence=%+v", artifact.Artifact, evidence.Evidence)
	}

	marketplace := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"marketplace", ajCookies, nil, ""), http.StatusOK))
	if marketplace.Available || !marketplace.ProductReachable || !marketplace.LiveAdmissionFenced || marketplace.ProviderRuntimeAvailable || len(marketplace.Listings) != 3 {
		t.Fatalf("marketplace=%+v", marketplace)
	}
	wantListings := map[string]bool{"scout": true, "researcher": true, "presenter": true}
	for _, listing := range marketplace.Listings {
		if !wantListings[listing.ID] || listing.Availability != "included" || !listing.LiveAvailable || listing.ProviderExecutionFenced || !listing.ReceiptStatus["providerQuality"] || !listing.ReceiptStatus["humanAdmission"] {
			t.Fatalf("included listing=%+v", listing)
		}
		delete(wantListings, listing.ID)
	}
	if len(wantListings) != 0 {
		t.Fatalf("missing included roles=%v", wantListings)
	}
	trial := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"marketplace/researcher/trial", ajCookies, nil, "")
	if trial.Code != http.StatusGone || !strings.Contains(trial.Body.String(), "retired") {
		t.Fatalf("retired trial status=%d body=%s", trial.Code, trial.Body.String())
	}

	sittingID := admitMemberWithTranscriptConsentForTest(t, app, officeRoomID, "aj@shareability.com")
	mediaGeneration := app.ensureRoomMedia(officeRoomID)
	meeting, active := app.meetings.activeRecord(officeRoomID)
	if !active || meeting.ID != sittingID || mediaGeneration == 0 || app.activeParticipantCount(officeRoomID) != 1 {
		t.Fatalf("meeting=%+v active=%t generation=%d participants=%d", meeting, active, mediaGeneration, app.activeParticipantCount(officeRoomID))
	}
	startedAt, err := time.Parse(time.RFC3339Nano, meeting.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	audience := STRIDEAudience{Visibility: "meeting", Principals: []string{strideRuntimePrincipalForEmail("aj@shareability.com")}}
	event := strideTemporalProductTranscriptEvent(canonicalTenantID(), officeRoomID, sittingID, mediaGeneration, startedAt.Add(time.Millisecond), startedAt.Add(2*time.Millisecond), audience, "AJ approved a Dog Perfect report. Researcher will verify sources and Presenter will build the deck through governed Work.")
	if err := app.strideRuntime.ApplyTemporalEvidence(canonicalTenantID(), TemporalMeetingBrainConfig{TenantID: canonicalTenantID(), RoomID: officeRoomID, SittingID: sittingID, SittingStart: startedAt.UTC()}, event); err != nil {
		t.Fatal(err)
	}
	temporal := e9DecodeFounder[e9FounderTemporalResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"temporal/answer", ajCookies, map[string]any{"roomId": officeRoomID, "window": "last_5_minutes"}, ""), http.StatusOK))
	if temporal.Answer.SittingID != sittingID || temporal.Answer.TranscriptHighWater == 0 || len(temporal.Answer.Evidence) == 0 || !strings.Contains(temporal.Answer.Text, "Dog Perfect") {
		t.Fatalf("temporal answer=%+v", temporal.Answer)
	}
	for _, retired := range []*httptest.ResponseRecorder{
		e9FounderRequest(t, mux, http.MethodGet, "/api/stride/v1/meeting-specialists?roomId="+officeRoomID, ajCookies, nil, ""),
		e9FounderRequest(t, mux, http.MethodPost, "/api/stride/v1/meeting-specialists/invitations", ajCookies, map[string]any{"roomId": officeRoomID, "agentId": "researcher"}, ""),
	} {
		if retired.Code != http.StatusGone || !strings.Contains(retired.Body.String(), "Scout is the only meeting participant agent") {
			t.Fatalf("specialist retirement status=%d body=%s", retired.Code, retired.Body.String())
		}
	}
	meetingAfter, stillActive := app.meetings.activeRecord(officeRoomID)
	if !stillActive || meetingAfter.ID != sittingID || !app.roomMediaGenerationCurrent(officeRoomID, mediaGeneration) || app.activeParticipantCount(officeRoomID) != 1 {
		t.Fatalf("retired specialist request affected meeting=%+v active=%t participants=%d", meetingAfter, stillActive, app.activeParticipantCount(officeRoomID))
	}

	firstGeneration := app.strideRuntime.Health().Generation
	if err := app.strideRuntime.Save(); err != nil {
		t.Fatal(err)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	activeApp = nil
	restarted := newKanbanBoardApp()
	activeApp, kanbanApp = restarted, restarted
	if health := restarted.strideRuntime.Health(); health.State != STRIDERuntimeStandby || !health.Restored || health.Generation <= firstGeneration {
		t.Fatalf("restart health=%+v", health)
	}
	restoredWork := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work", ajCookies, nil, ""), http.StatusOK))
	restoredRoster := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"roster", ajCookies, nil, ""), http.StatusOK))
	if len(restoredWork.Suggestions) != 1 || len(restoredWork.Runs) != 1 || restoredWork.Suggestions[0].RunID != approved.Suggestion.RunID || len(restoredRoster.Seats) != 0 {
		t.Fatalf("restored graph: work=%+v roster=%+v", restoredWork, restoredRoster)
	}
	if retired := e9FounderRequest(t, mux, http.MethodGet, "/api/stride/v1/meeting-specialists?roomId="+officeRoomID, ajCookies, nil, ""); retired.Code != http.StatusGone {
		t.Fatalf("restart revived specialist endpoint status=%d", retired.Code)
	}

	secondGeneration := restarted.strideRuntime.Health().Generation
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
	activeApp = nil
	t.Setenv("STRIDE_LOCAL_PRODUCT_PREVIEW_ENABLED", "false")
	fenced := newKanbanBoardApp()
	activeApp, kanbanApp = fenced, fenced
	if health := fenced.strideRuntime.Health(); health.State != STRIDERuntimeStandby || !health.Restored || health.Generation <= secondGeneration {
		t.Fatalf("default-off health=%+v", health)
	}
	fencedWork := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work", ajCookies, nil, ""), http.StatusOK))
	fencedMarketplace := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"marketplace", ajCookies, nil, ""), http.StatusOK))
	if fencedWork.Available || len(fencedWork.Suggestions) != 0 || len(fencedWork.Runs) != 0 || fencedMarketplace.ProductReachable || len(fencedMarketplace.Listings) != 0 {
		t.Fatalf("default-off leaked state: work=%+v marketplace=%+v", fencedWork, fencedMarketplace)
	}
	if err := fenced.strideRuntime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
		product, err := domains.Product.Snapshot()
		if err != nil {
			return err
		}
		if len(product.Work) != 1 || len(product.Agents) != 0 || domains.WorkOrchestrator.Store == nil || len(domains.WorkOrchestrator.Store.Runs) != 1 {
			t.Fatalf("default-off lost durable history: product=%+v", product)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if retired := e9FounderRequest(t, mux, http.MethodPost, "/api/stride/v1/meeting-specialists/invitations", ajCookies, map[string]any{"roomId": officeRoomID, "agentId": "researcher"}, ""); retired.Code != http.StatusGone {
		t.Fatalf("default-off changed retirement status=%d", retired.Code)
	}
}

func e9FounderRequest(t *testing.T, handler http.Handler, method, path string, cookies []*http.Cookie, body any, origin string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
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

func e9FounderExpect(t *testing.T, recorder *httptest.ResponseRecorder, status int) *httptest.ResponseRecorder {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("HTTP status=%d want=%d body=%s", recorder.Code, status, recorder.Body.String())
	}
	return recorder
}

func e9DecodeFounder[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode founder response: %v body=%s", err, recorder.Body.String())
	}
	return value
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		counts[value]--
		if counts[value] < 0 {
			return false
		}
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}
