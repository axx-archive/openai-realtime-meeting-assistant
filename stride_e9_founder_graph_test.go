package main

// This file is the integrated E9 founder graph. It deliberately exercises the
// authenticated product handlers and the signed durable runtimes while every
// provider credential is blank. It is not provider, media-device, production,
// deployment, or release evidence.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
		Title                   string `json:"title"`
		Summary                 string `json:"summary"`
		ApprovedOutcome         string `json:"approvedOutcome"`
		SourceSnippet           string `json:"sourceSnippet"`
		SourceHref              string `json:"sourceHref"`
		DestinationThreadID     string `json:"destinationThreadId"`
		ProviderExecutionFenced bool   `json:"providerExecutionFenced"`
	} `json:"artifact"`
	Evidence struct {
		ThreadID      string          `json:"threadId"`
		MessageID     string          `json:"messageId"`
		SourceSnippet string          `json:"sourceSnippet"`
		SourceEvent   STRIDEReference `json:"sourceEvent"`
		BrainLinked   bool            `json:"brainLinked"`
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
	OK        bool                       `json:"ok"`
	Available bool                       `json:"available"`
	Reason    string                     `json:"reason"`
	Meetings  []map[string]any           `json:"meetings"`
	Answer    STRIDETemporalRecallResult `json:"answer"`
}

type e9FounderSpecialistResponse struct {
	OK                     bool                            `json:"ok"`
	Specialists            MeetingSpecialistProductStatus  `json:"specialists"`
	Invitation             meetingSpecialistInvitationView `json:"invitation"`
	ProviderSessionStarted bool                            `json:"providerSessionStarted"`
}

type e9FounderChatResponse struct {
	OK                      bool                   `json:"ok"`
	Thread                  scoutChatThreadRecord  `json:"thread"`
	Message                 scoutChatMessageRecord `json:"message"`
	Answer                  scoutChatMessageRecord `json:"answer"`
	ProviderCalls           int                    `json:"providerCalls"`
	ProviderExecutionFenced bool                   `json:"providerExecutionFenced"`
}

func TestE9DeterministicFounderGraphThroughProductEndpoints(t *testing.T) {
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
	t.Setenv("STRIDE_RUNTIME_MIN_GENERATION", "131")
	t.Setenv("STRIDE_RUNTIME_RECALL_THREAD_IDS", "team")
	t.Setenv("STRIDE_RUNTIME_SNAPSHOT_KEY_ID", "e9_founder_graph_key")
	macKey := []byte("0123456789abcdef0123456789abcdef")
	t.Setenv("STRIDE_RUNTIME_SNAPSHOT_MAC_KEY", base64.StdEncoding.EncodeToString(macKey))
	t.Setenv("STRIDE_LOCAL_PRODUCT_PREVIEW_ENABLED", "true")
	t.Setenv("STRIDE_MEETING_SPECIALIST_CONTROL_ENABLED", "true")
	activationPath := filepath.Join(dir, "meeting-specialist-control.json")
	t.Setenv(meetingSpecialistControlActivationEnv, activationPath)
	e9WriteFounderSpecialistActivation(t, activationPath, STRIDESnapshotMACAuthority{KeyID: "e9_founder_graph_key", Key: macKey}, 1000)
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
		t.Fatalf("fresh founder runtime health=%+v", health)
	}
	if app.meetingSpecialists == nil || !app.meetingSpecialists.enabled {
		t.Fatal("signed meeting-specialist control plane did not activate")
	}

	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)
	registerMeetingSpecialistProductRoutes(mux)
	mux.HandleFunc("/assistant/chat-threads/", assistantChatThreadHandler)
	ajCookies := loginAs(t, "aj@shareability.com", defaultMeetingRoomPassword)
	tomCookies := loginAs(t, "tom@shareability.com", defaultMeetingRoomPassword)

	// The source is the actual durable #team write path. Recognition is
	// automatic; the explicit suggestion endpoint is then replayed to prove the
	// same evidence cannot manufacture a duplicate.
	now := time.Now().UTC().Add(-time.Minute)
	team := scoutChatThreadRecord{
		ID: "team", Title: "team", Preview: "company group chat", OwnerEmail: "aj@shareability.com", CreatedBy: "AJ",
		Visibility: scoutChatVisibilityPublic, Table: true, CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano),
	}
	encodedTeam, err := encodeScoutChatThread(team)
	if err != nil {
		t.Fatal(err)
	}
	if _, appended, err := app.memory.appendScoutChatThread(team.ID, encodedTeam, scoutChatThreadMetadata(team)); err != nil || !appended {
		t.Fatalf("seed durable #team: appended=%t err=%v", appended, err)
	}
	sourceMessage := scoutChatMessageRecord{
		ID: "e9_founder_team_outcome", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: "aj@shareability.com",
		Text:      "We need Scout to create an Insights & Opportunities report for Dog Perfect, grounded in this company conversation.",
		CreatedAt: now.Add(time.Second).Format(time.RFC3339Nano),
	}
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", team.ID, sourceMessage); err != nil {
		t.Fatalf("commit #team outcome: %v", err)
	}
	privateThread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Private founder notes", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	privateMessage := scoutChatMessageRecord{
		ID: "e9_founder_private_outcome", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: "aj@shareability.com",
		Text: "Create an Insights & Opportunities report from this private note.", CreatedAt: now.Add(2 * time.Second).Format(time.RFC3339Nano),
	}
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", privateThread.ID, privateMessage); err != nil {
		t.Fatal(err)
	}

	unauthorized := e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work", nil, nil, "")
	if unauthorized.Code != http.StatusUnauthorized || unauthorized.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unauthenticated work status=%d headers=%v body=%s", unauthorized.Code, unauthorized.Header(), unauthorized.Body.String())
	}
	foreign := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions", ajCookies, map[string]any{"threadId": team.ID, "messageId": sourceMessage.ID}, "https://untrusted.example")
	if foreign.Code != http.StatusForbidden {
		t.Fatalf("cross-origin suggestion status=%d body=%s", foreign.Code, foreign.Body.String())
	}
	tenantOverride := e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work?tenantId=other_tenant", ajCookies, nil, "")
	if tenantOverride.Code != http.StatusForbidden && tenantOverride.Code != http.StatusBadRequest {
		t.Fatalf("tenant query override status=%d body=%s", tenantOverride.Code, tenantOverride.Body.String())
	}
	bodyOverride := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions", ajCookies, map[string]any{"tenantId": "other_tenant", "threadId": team.ID, "messageId": sourceMessage.ID}, "")
	if bodyOverride.Code != http.StatusBadRequest {
		t.Fatalf("tenant body override status=%d body=%s", bodyOverride.Code, bodyOverride.Body.String())
	}

	work := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work", ajCookies, nil, ""), http.StatusOK))
	if !work.OK || !work.Available || work.Reason != "deterministic_local" || len(work.Suggestions) != 1 || len(work.Runs) != 0 {
		t.Fatalf("initial work surface=%+v", work)
	}
	suggestion := work.Suggestions[0]
	wantRecipients := []string{strideRuntimePrincipalForEmail("aj@shareability.com"), strideRuntimePrincipalForEmail("joel@shareability.com")}
	if suggestion.Status != "suggested" || suggestion.Revision != 1 || suggestion.SourceThreadID != team.ID || suggestion.SourceMessageID != sourceMessage.ID || !suggestion.ProviderExecutionFenced || !sameStringSet(suggestion.RecipientIDs, wantRecipients) {
		t.Fatalf("recognized suggestion=%+v wantRecipients=%v", suggestion, wantRecipients)
	}

	replayed := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions", ajCookies, map[string]any{
		"threadId": team.ID, "messageId": sourceMessage.ID, "title": "Insights & Opportunities report", "outcome": sourceMessage.Text,
	}, "")
	if replayed.Code != http.StatusCreated {
		t.Fatalf("idempotent suggestion replay status=%d body=%s", replayed.Code, replayed.Body.String())
	}
	replayedWork := e9DecodeFounder[e9FounderWorkResponse](t, replayed)
	if replayedWork.Suggestion.ID != suggestion.ID || replayedWork.Suggestion.Revision != suggestion.Revision {
		t.Fatalf("suggestion replay=%+v want id=%s revision=%d", replayedWork.Suggestion, suggestion.ID, suggestion.Revision)
	}
	privateAttempt := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions", ajCookies, map[string]any{"threadId": privateThread.ID, "messageId": privateMessage.ID}, "")
	if privateAttempt.Code != http.StatusForbidden {
		t.Fatalf("private suggestion status=%d body=%s", privateAttempt.Code, privateAttempt.Body.String())
	}
	outsiderWork := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work", tomCookies, nil, ""), http.StatusOK))
	if len(outsiderWork.Suggestions) != 0 || len(outsiderWork.Runs) != 0 {
		t.Fatalf("non-recipient observed work=%+v", outsiderWork)
	}
	outsiderRead := e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work/suggestions/"+suggestion.ID, tomCookies, nil, "")
	if outsiderRead.Code != http.StatusForbidden {
		t.Fatalf("non-recipient suggestion read status=%d body=%s", outsiderRead.Code, outsiderRead.Body.String())
	}

	teamDestination := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions/"+suggestion.ID+"/destination", ajCookies, map[string]any{
		"revision": suggestion.Revision, "mode": "existing", "threadId": team.ID,
	}, "")
	if teamDestination.Code != http.StatusForbidden {
		t.Fatalf("source #team accepted as work destination status=%d body=%s", teamDestination.Code, teamDestination.Body.String())
	}
	afterTeamDestinationDenial := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work/suggestions/"+suggestion.ID, ajCookies, nil, ""), http.StatusOK)).Suggestion
	if afterTeamDestinationDenial.Revision != suggestion.Revision || afterTeamDestinationDenial.DestinationThreadID != "" || afterTeamDestinationDenial.DestinationMode != "" {
		t.Fatalf("denied #team destination mutated suggestion=%+v", afterTeamDestinationDenial)
	}

	dogPerfect, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Dog Perfect", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create Dog Perfect project thread: %v", err)
	}
	destinationRequest := map[string]any{
		"revision": suggestion.Revision, "mode": "existing", "threadId": dogPerfect.ID,
	}
	destinationRecorder := e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions/"+suggestion.ID+"/destination", ajCookies, destinationRequest, ""), http.StatusOK)
	destination := e9DecodeFounder[e9FounderWorkResponse](t, destinationRecorder).Suggestion
	if destination.Revision != 2 || destination.DestinationMode != "existing" || destination.DestinationThreadID != dogPerfect.ID || destination.DestinationTitle != dogPerfect.Title {
		t.Fatalf("explicit destination=%+v", destination)
	}
	destinationReplay := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions/"+suggestion.ID+"/destination", ajCookies, destinationRequest, ""), http.StatusOK)).Suggestion
	if destinationReplay.Revision != destination.Revision || destinationReplay.DestinationThreadID != destination.DestinationThreadID {
		t.Fatalf("destination replay was not idempotent: first=%+v replay=%+v", destination, destinationReplay)
	}
	staleApproval := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions/"+suggestion.ID+"/approve", ajCookies, map[string]any{"revision": suggestion.Revision}, "")
	if staleApproval.Code != http.StatusConflict {
		t.Fatalf("stale approval status=%d body=%s", staleApproval.Code, staleApproval.Body.String())
	}
	wrongRecipientApproval := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions/"+suggestion.ID+"/approve", tomCookies, map[string]any{"revision": destination.Revision}, "")
	if wrongRecipientApproval.Code != http.StatusConflict && wrongRecipientApproval.Code != http.StatusForbidden {
		t.Fatalf("wrong-recipient approval status=%d body=%s", wrongRecipientApproval.Code, wrongRecipientApproval.Body.String())
	}

	approvedRecorder := e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions/"+suggestion.ID+"/approve", ajCookies, map[string]any{"revision": destination.Revision}, ""), http.StatusOK)
	approved := e9DecodeFounder[e9FounderWorkResponse](t, approvedRecorder)
	if approved.Suggestion.Status != "completed" || approved.Suggestion.RunID == "" || approved.Suggestion.ArtifactID == "" || approved.Suggestion.ArtifactHref == "" || approved.Suggestion.BrainHref == "" || !approved.Suggestion.CompletionPosted || approved.ProviderCalls != 0 || approved.InputTokens != 0 || approved.OutputTokens != 0 {
		t.Fatalf("approved deterministic work=%+v", approved)
	}
	projectAfterCompletion, _, err := app.scoutChatThreadByID("aj@shareability.com", dogPerfect.ID)
	if err != nil || len(projectAfterCompletion.Messages) != 1 || !strings.Contains(projectAfterCompletion.Messages[0].Text, approved.Suggestion.ArtifactHref) {
		t.Fatalf("project completion thread=%+v err=%v", projectAfterCompletion, err)
	}

	duplicateApproval := e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions/"+suggestion.ID+"/approve", ajCookies, map[string]any{"revision": destination.Revision}, ""), http.StatusOK)
	duplicate := e9DecodeFounder[e9FounderWorkResponse](t, duplicateApproval)
	projectAfterDuplicate, _, err := app.scoutChatThreadByID("aj@shareability.com", dogPerfect.ID)
	if err != nil || duplicate.Suggestion.RunID != approved.Suggestion.RunID || duplicate.Suggestion.ArtifactID != approved.Suggestion.ArtifactID || len(projectAfterDuplicate.Messages) != 1 {
		t.Fatalf("duplicate approval escaped idempotency: duplicate=%+v messages=%d err=%v", duplicate.Suggestion, len(projectAfterDuplicate.Messages), err)
	}

	completedWork := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work", ajCookies, nil, ""), http.StatusOK))
	if len(completedWork.Suggestions) != 1 || len(completedWork.Runs) != 1 || completedWork.Runs[0].ID != approved.Suggestion.RunID || completedWork.Runs[0].Status != STRIDERunCompleted || completedWork.Runs[0].CurrentStage != "insights_opportunities_v1" {
		t.Fatalf("durable completed work=%+v", completedWork)
	}
	artifact := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, approved.Suggestion.ArtifactHref, ajCookies, nil, ""), http.StatusOK))
	if artifact.Artifact.ID != approved.Suggestion.ArtifactID || artifact.Artifact.DestinationThreadID != dogPerfect.ID || artifact.Artifact.SourceHref != approved.Suggestion.BrainHref || !artifact.Artifact.ProviderExecutionFenced || artifact.Artifact.Summary == "" {
		t.Fatalf("artifact product link=%+v", artifact.Artifact)
	}
	evidence := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, approved.Suggestion.BrainHref, ajCookies, nil, ""), http.StatusOK))
	if evidence.Evidence.ThreadID != team.ID || evidence.Evidence.MessageID != sourceMessage.ID || evidence.Evidence.SourceEvent != suggestion.SourceEvent || !evidence.Evidence.BrainLinked {
		t.Fatalf("company-brain evidence link=%+v", evidence.Evidence)
	}

	// Marketplace discovery is honest: these are first-party deterministic
	// fixtures, not live/provider-qualified listings. The signed local product
	// scope makes the lifecycle reachable without changing that availability.
	marketplace := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"marketplace", ajCookies, nil, ""), http.StatusOK))
	if marketplace.Available || !marketplace.ProductReachable || !marketplace.LiveAdmissionFenced || len(marketplace.Listings) != 5 {
		t.Fatalf("marketplace discovery=%+v", marketplace)
	}
	wantListings := map[string]bool{"insights-analyst": true, "mary-marketing": true, "rowan-research": true, "jules-design": true, "kit-builder": true}
	var maryListing STRIDEProductMarketplaceCandidate
	for _, listing := range marketplace.Listings {
		if !wantListings[listing.ID] {
			t.Fatalf("unexpected marketplace candidate=%+v", listing)
		}
		delete(wantListings, listing.ID)
		if listing.ID == "mary-marketing" {
			maryListing = listing
		}
	}
	if len(wantListings) != 0 {
		t.Fatalf("missing marketplace candidates=%v", wantListings)
	}
	if maryListing.ID == "" || maryListing.DisplayName != "Mary" || maryListing.Availability != "internal_preview" || maryListing.LiveAvailable || !maryListing.ProviderExecutionFenced || maryListing.ReceiptStatus["providerQuality"] || maryListing.ReceiptStatus["humanAdmission"] {
		t.Fatalf("Mary candidate escaped honest fence: %+v", maryListing)
	}
	listingDetail := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"marketplace/"+maryListing.ID, ajCookies, nil, ""), http.StatusOK))
	if listingDetail.Listing.ID != maryListing.ID || !listingDetail.LiveAdmissionFenced {
		t.Fatalf("listing detail=%+v", listingDetail)
	}
	nonAdminTrial := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"marketplace/"+maryListing.ID+"/trial", tomCookies, nil, "")
	if nonAdminTrial.Code != http.StatusForbidden {
		t.Fatalf("non-admin trial status=%d body=%s", nonAdminTrial.Code, nonAdminTrial.Body.String())
	}
	trial := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"marketplace/"+maryListing.ID+"/trial", ajCookies, nil, ""), http.StatusOK))
	if trial.Seat.ID != "agent_mary-marketing" || trial.Seat.DisplayName != "Mary" || trial.Seat.Status != "trial" || trial.Seat.Revision != 1 || !trial.Seat.ProviderExecutionFenced || !trial.Seat.AccessRevoked || trial.ProviderSessionStarted {
		t.Fatalf("Mary trial=%+v", trial)
	}
	trialReplay := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"marketplace/"+maryListing.ID+"/trial", ajCookies, nil, ""), http.StatusOK))
	if trialReplay.Seat.ID != trial.Seat.ID || trialReplay.Seat.Revision != trial.Seat.Revision {
		t.Fatalf("trial replay was not idempotent: first=%+v replay=%+v", trial.Seat, trialReplay.Seat)
	}
	staleHire := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"marketplace/"+maryListing.ID+"/hire", ajCookies, map[string]any{"revision": 0}, "")
	if staleHire.Code != http.StatusConflict {
		t.Fatalf("stale hire status=%d body=%s", staleHire.Code, staleHire.Body.String())
	}
	hired := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"marketplace/"+maryListing.ID+"/hire", ajCookies, map[string]any{"revision": trial.Seat.Revision}, ""), http.StatusOK))
	if hired.Seat.Status != "hired_fenced" || hired.Seat.Revision != 2 || hired.Seat.DirectThreadID == "" || hired.Seat.AccessRevoked || !hired.Seat.ProviderExecutionFenced || !hired.ScoutIntroductionPosted || hired.ProviderSessionStarted {
		t.Fatalf("Mary hire=%+v", hired)
	}
	directThread, _, err := app.scoutChatThreadByID("aj@shareability.com", hired.Seat.DirectThreadID)
	if err != nil || len(directThread.Messages) != 1 || directThread.Messages[0].AuthorName != "Scout" || !strings.Contains(directThread.Messages[0].Text, "Meet Mary") || !strings.Contains(directThread.Messages[0].Text, "remain off") {
		t.Fatalf("Scout direct-thread introduction=%+v err=%v", directThread, err)
	}
	hireReplay := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"marketplace/"+maryListing.ID+"/hire", ajCookies, map[string]any{"revision": trial.Seat.Revision}, ""), http.StatusOK))
	directThreadAfterReplay, _, err := app.scoutChatThreadByID("aj@shareability.com", hired.Seat.DirectThreadID)
	if err != nil || hireReplay.Seat.ID != hired.Seat.ID || hireReplay.Seat.DirectThreadID != hired.Seat.DirectThreadID || scoutChatThreadVisibility(directThreadAfterReplay) != scoutChatVisibilityPrivate || len(directThreadAfterReplay.Messages) != 1 {
		t.Fatalf("hire replay escaped idempotency: replay=%+v messages=%d err=%v", hireReplay.Seat, len(directThreadAfterReplay.Messages), err)
	}

	// The founder can send Mary a real authenticated message in her private
	// direct thread. With providers fenced, a reply is neither required nor
	// evidence here; the durable human message is the product contract. Private
	// direct material must not silently enter the shared conversation ledger or
	// mint a Suggested Work record.
	sharedEventsBeforeDirect := 0
	if err := app.strideRuntime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
		snapshot, err := domains.ConversationLedger.Snapshot()
		if err != nil {
			return err
		}
		sharedEventsBeforeDirect = len(snapshot.Events)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	directMessageText := "Mary, please review the Dog Perfect launch positioning and keep every public claim evidence-backed."
	directPost := e9DecodeFounder[e9FounderChatResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, "/assistant/chat-threads/"+hired.Seat.DirectThreadID+"/messages", ajCookies, map[string]any{"text": directMessageText}, ""), http.StatusOK))
	if !directPost.OK || directPost.Message.ID == "" || directPost.Message.Role != "user" || directPost.Message.AuthorEmail != "aj@shareability.com" || directPost.Message.Text != directMessageText || scoutChatThreadVisibility(directPost.Thread) != scoutChatVisibilityPrivate || directPost.Answer.ID != "" || directPost.ProviderCalls != 0 || !directPost.ProviderExecutionFenced {
		t.Fatalf("authenticated Mary direct message=%+v", directPost)
	}
	directAfterMessage, _, err := app.scoutChatThreadByID("aj@shareability.com", hired.Seat.DirectThreadID)
	if err != nil || scoutChatMessageIndex(directAfterMessage, directPost.Message.ID) < 0 {
		t.Fatalf("Mary direct message did not persist: thread=%+v err=%v", directAfterMessage, err)
	}
	sharedEventsAfterDirect := 0
	if err := app.strideRuntime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
		snapshot, err := domains.ConversationLedger.Snapshot()
		if err != nil {
			return err
		}
		sharedEventsAfterDirect = len(snapshot.Events)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if sharedEventsAfterDirect != sharedEventsBeforeDirect {
		t.Fatalf("private Mary message entered shared conversation ledger: before=%d after=%d", sharedEventsBeforeDirect, sharedEventsAfterDirect)
	}
	privateDirectSuggestion := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions", ajCookies, map[string]any{"threadId": hired.Seat.DirectThreadID, "messageId": directPost.Message.ID}, "")
	if privateDirectSuggestion.Code != http.StatusForbidden {
		t.Fatalf("private Mary message minted shared work status=%d body=%s", privateDirectSuggestion.Code, privateDirectSuggestion.Body.String())
	}
	workAfterDirect := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work", ajCookies, nil, ""), http.StatusOK))
	if len(workAfterDirect.Suggestions) != 1 || len(workAfterDirect.Runs) != 1 {
		t.Fatalf("private Mary message changed shared work graph=%+v", workAfterDirect)
	}

	configuredRequest := map[string]any{
		"revision": hired.Seat.Revision,
		"config": map[string]any{
			"personalityNotes": "Warm, incisive, commercially curious; challenge fuzzy positioning without losing the room.",
			"memberships":      []string{"team", "meeting", "dog-perfect"}, "perRunBudgetCents": 25, "dailyBudgetCents": 100, "proactivity": "quiet",
		},
	}
	legacyConfigure := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/configure", ajCookies, configuredRequest, "")
	if legacyConfigure.Code != http.StatusForbidden {
		t.Fatalf("legacy configure route did not fail closed status=%d body=%s", legacyConfigure.Code, legacyConfigure.Body.String())
	}
	repeatedLegacyConfigure := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/configure", ajCookies, map[string]any{
		"revision": hired.Seat.Revision,
		"config": map[string]any{
			"personalityNotes": "A different stale overwrite.",
			"memberships":      []string{"team"}, "perRunBudgetCents": 5, "dailyBudgetCents": 10, "proactivity": "disabled",
		},
	}, "")
	if repeatedLegacyConfigure.Code != http.StatusForbidden {
		t.Fatalf("repeated legacy configure did not fail closed status=%d body=%s", repeatedLegacyConfigure.Code, repeatedLegacyConfigure.Body.String())
	}

	assignmentRequest := map[string]any{
		"revision": hired.Seat.Revision, "projectOrChannel": "dog-perfect", "role": "marketer",
		"responsibility": "Pressure-test positioning and keep launch opportunities tied to evidence.", "destination": dogPerfect.ID,
	}
	assigned := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/assign", ajCookies, assignmentRequest, ""), http.StatusOK))
	if assigned.Seat.Revision != 3 || len(assigned.Seat.Assignments) != 1 || assigned.Seat.Assignments[0].Destination != dogPerfect.ID || assigned.Seat.Assignments[0].Status != "active_fenced" {
		t.Fatalf("Mary assignment=%+v", assigned.Seat)
	}
	assignedReplay := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/assign", ajCookies, assignmentRequest, ""), http.StatusOK))
	if assignedReplay.Seat.Revision != assigned.Seat.Revision || len(assignedReplay.Seat.Assignments) != 1 || assignedReplay.Seat.Assignments[0].ID != assigned.Seat.Assignments[0].ID {
		t.Fatalf("assignment replay was not idempotent: first=%+v replay=%+v", assigned.Seat, assignedReplay.Seat)
	}

	learningRequest := map[string]any{
		"revision": assigned.Seat.Revision, "subject": "dog-perfect", "scope": "project-positioning", "summary": "The team prefers playful claims even when evidence is not ready.",
	}
	learned := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/learning", ajCookies, learningRequest, ""), http.StatusOK))
	if learned.Seat.Revision != 4 || len(learned.Seat.Learning) != 1 || learned.Seat.Learning[0].Status != "reviewed" {
		t.Fatalf("Mary learning=%+v", learned.Seat)
	}
	learnedReplay := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/learning", ajCookies, learningRequest, ""), http.StatusOK))
	if learnedReplay.Seat.Revision != learned.Seat.Revision || len(learnedReplay.Seat.Learning) != 1 || learnedReplay.Seat.Learning[0].ID != learned.Seat.Learning[0].ID {
		t.Fatalf("learning replay was not idempotent: first=%+v replay=%+v", learned.Seat, learnedReplay.Seat)
	}
	learningID := learned.Seat.Learning[0].ID
	corrected := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/learning/"+learningID+"/correct", ajCookies, map[string]any{
		"revision": learned.Seat.Revision, "summary": "The team likes playful positioning, but every public claim must remain evidence-backed.",
	}, ""), http.StatusOK))
	if corrected.Seat.Revision != 5 || corrected.Seat.Learning[0].Status != "corrected" || corrected.Seat.Learning[0].Revision != 2 || !strings.Contains(corrected.Seat.Learning[0].Summary, "evidence-backed") {
		t.Fatalf("corrected learning=%+v", corrected.Seat)
	}
	staleCorrection := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/learning/"+learningID+"/correct", ajCookies, map[string]any{
		"revision": learned.Seat.Revision, "summary": "stale overwrite",
	}, "")
	if staleCorrection.Code != http.StatusConflict {
		t.Fatalf("stale learning correction status=%d body=%s", staleCorrection.Code, staleCorrection.Body.String())
	}

	candidateConfig := corrected.Seat.Config
	candidateConfig.PersonalityNotes = "Sharper launch challenger while preserving Mary's warm, evidence-first voice."
	updateRequest := map[string]any{
		"revision": corrected.Seat.Revision, "summary": "Trial a sharper launch critique overlay", "candidate": candidateConfig,
	}
	proposed := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/updates", ajCookies, updateRequest, ""), http.StatusOK))
	if proposed.Seat.Revision != 6 || len(proposed.Seat.Updates) != 1 || proposed.Seat.Updates[0].Status != "pending" || proposed.Seat.Updates[0].SemanticDiff.Digest == "" {
		t.Fatalf("proposed Mary update=%+v", proposed.Seat)
	}
	proposedReplay := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/updates", ajCookies, updateRequest, ""), http.StatusOK))
	if proposedReplay.Seat.Revision != proposed.Seat.Revision || len(proposedReplay.Seat.Updates) != 1 || proposedReplay.Seat.Updates[0].ID != proposed.Seat.Updates[0].ID {
		t.Fatalf("update proposal replay was not idempotent: first=%+v replay=%+v", proposed.Seat, proposedReplay.Seat)
	}
	updateID := proposed.Seat.Updates[0].ID
	staleUpdateApproval := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/updates/"+updateID+"/approve", ajCookies, map[string]any{"revision": corrected.Seat.Revision}, "")
	if staleUpdateApproval.Code != http.StatusConflict {
		t.Fatalf("stale update approval status=%d body=%s", staleUpdateApproval.Code, staleUpdateApproval.Body.String())
	}
	updateApproved := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/updates/"+updateID+"/approve", ajCookies, map[string]any{"revision": proposed.Seat.Revision}, ""), http.StatusOK))
	if updateApproved.Seat.Revision != 7 || updateApproved.Seat.Updates[0].Status != "approved" || updateApproved.Seat.Config.PersonalityNotes != candidateConfig.PersonalityNotes {
		t.Fatalf("approved Mary update=%+v", updateApproved.Seat)
	}
	updateApprovalReplay := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/updates/"+updateID+"/approve", ajCookies, map[string]any{"revision": proposed.Seat.Revision}, ""), http.StatusOK))
	if updateApprovalReplay.Seat.Revision != updateApproved.Seat.Revision || updateApprovalReplay.Seat.Updates[0].Status != updateApproved.Seat.Updates[0].Status {
		t.Fatalf("update approval replay was not idempotent: first=%+v replay=%+v", updateApproved.Seat, updateApprovalReplay.Seat)
	}
	rolledBack := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/updates/"+updateID+"/rollback", ajCookies, map[string]any{"revision": updateApproved.Seat.Revision}, ""), http.StatusOK))
	if rolledBack.Seat.Revision != 8 || rolledBack.Seat.Updates[0].Status != "rolled_back" || rolledBack.Seat.Config.PersonalityNotes != hired.Seat.Config.PersonalityNotes {
		t.Fatalf("rolled-back Mary update=%+v", rolledBack.Seat)
	}
	rollbackReplay := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/updates/"+updateID+"/rollback", ajCookies, map[string]any{"revision": updateApproved.Seat.Revision}, ""), http.StatusOK))
	if rollbackReplay.Seat.Revision != rolledBack.Seat.Revision || rollbackReplay.Seat.Updates[0].Status != rolledBack.Seat.Updates[0].Status {
		t.Fatalf("rollback replay was not idempotent: first=%+v replay=%+v", rolledBack.Seat, rollbackReplay.Seat)
	}
	exported := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/export", ajCookies, nil, ""), http.StatusOK))
	if exported.Export.Schema != "stride.agent_export.v1" || exported.Export.ListingID != hired.Seat.ListingID ||
		exported.Export.Category != hired.Seat.Category || exported.Export.LifecycleStatus != rolledBack.Seat.Status ||
		!isHexDigest(exported.Export.HistoricalAttributionHash) || !exported.Export.ProviderExecutionFenced ||
		exported.Export.ContainsTenantData || exported.Export.ContainsCredentials || exported.Export.ContainsMemory ||
		exported.Export.ContainsAssignments || exported.Export.ContainsPrivateEvidence || exported.ProviderRuntimeExported {
		t.Fatalf("safe Mary export=%+v", exported)
	}

	// Hiring and a generic project assignment never grant meeting access. The
	// founder explicitly proposes and separately approves an exact room
	// membership, then records the revision-bound meeting-specialist assignment
	// that the meeting authority joins with the canonical Workforce seat.
	meetingConfig := rolledBack.Seat.Config
	meetingConfig.Memberships = uniqueSortedStrings(append(meetingConfig.Memberships, officeRoomID))
	meetingUpdate := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/updates", ajCookies, map[string]any{
		"revision": rolledBack.Seat.Revision, "summary": "Allow Mary to be explicitly invited into the office room.", "candidate": meetingConfig,
	}, ""), http.StatusOK))
	if meetingUpdate.Seat.Revision != rolledBack.Seat.Revision+1 || len(meetingUpdate.Seat.Updates) != 2 || meetingUpdate.Seat.Updates[1].Status != "pending" {
		t.Fatalf("meeting membership proposal=%+v", meetingUpdate.Seat)
	}
	meetingUpdateID := meetingUpdate.Seat.Updates[1].ID
	meetingConfigured := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/updates/"+meetingUpdateID+"/approve", ajCookies, map[string]any{
		"revision": meetingUpdate.Seat.Revision,
	}, ""), http.StatusOK))
	if meetingConfigured.Seat.Revision != meetingUpdate.Seat.Revision+1 || meetingConfigured.Seat.Updates[1].Status != "approved" || !strideWorkContainsString(meetingConfigured.Seat.Config.Memberships, officeRoomID) {
		t.Fatalf("approved meeting membership=%+v", meetingConfigured.Seat)
	}
	meetingAssigned := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/assign", ajCookies, map[string]any{
		"revision": meetingConfigured.Seat.Revision, "projectOrChannel": officeRoomID, "role": meetingSpecialistAssignmentRole,
		"responsibility": "Join this room only after an eligible human explicitly approves the invitation.", "destination": officeRoomID,
	}, ""), http.StatusOK))
	if meetingAssigned.Seat.Revision != meetingConfigured.Seat.Revision+1 || len(meetingAssigned.Seat.Assignments) != 2 || meetingAssigned.Seat.Assignments[1].ProjectOrChannel != officeRoomID || meetingAssigned.Seat.Assignments[1].Role != meetingSpecialistAssignmentRole {
		t.Fatalf("meeting specialist assignment=%+v", meetingAssigned.Seat)
	}

	// A live meeting uses the app's real admission, media-generation, meeting,
	// and consent authorities. The deterministic transcript goes straight into
	// the server-side temporal reducer, then the founder asks through the actual
	// authenticated five-minute product endpoint.
	sittingID := admitMemberWithTranscriptConsentForTest(t, app, officeRoomID, "aj@shareability.com")
	mediaGeneration := app.ensureRoomMedia(officeRoomID)
	if mediaGeneration == 0 || !app.roomMediaGenerationCurrent(officeRoomID, mediaGeneration) {
		t.Fatal("founder meeting media scope did not start")
	}
	meeting, active := app.meetings.activeRecord(officeRoomID)
	if !active || meeting.ID != sittingID {
		t.Fatalf("founder active meeting=%+v active=%t sitting=%s", meeting, active, sittingID)
	}
	startedAt, err := time.Parse(time.RFC3339Nano, meeting.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !startedAt.Add(3 * time.Millisecond).Before(time.Now().UTC()) {
		time.Sleep(4 * time.Millisecond)
	}
	temporalAudience := STRIDEAudience{Visibility: "meeting", Principals: []string{strideRuntimePrincipalForEmail("aj@shareability.com")}}
	temporalEvent := strideTemporalProductTranscriptEvent(canonicalTenantID(), officeRoomID, sittingID, mediaGeneration, startedAt.Add(time.Millisecond), startedAt.Add(2*time.Millisecond), temporalAudience,
		"Erick shared the Dog Perfect launch brief in #team. AJ approved an Insights & Opportunities report and Mary will pressure-test positioning.")
	if err := app.strideRuntime.ApplyTemporalEvidence(canonicalTenantID(), TemporalMeetingBrainConfig{TenantID: canonicalTenantID(), RoomID: officeRoomID, SittingID: sittingID, SittingStart: startedAt.UTC()}, temporalEvent); err != nil {
		t.Fatalf("apply deterministic meeting transcript: %v", err)
	}
	if err := app.strideRuntime.Save(); err != nil {
		t.Fatal(err)
	}
	temporalSurface := e9DecodeFounder[e9FounderTemporalResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"temporal", ajCookies, nil, ""), http.StatusOK))
	if !temporalSurface.OK || !temporalSurface.Available || temporalSurface.Reason != "" || len(temporalSurface.Meetings) != 1 {
		t.Fatalf("temporal product surface=%+v", temporalSurface)
	}
	temporalAnswer := e9DecodeFounder[e9FounderTemporalResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"temporal/answer", ajCookies, map[string]any{"roomId": officeRoomID, "window": "last_5_minutes"}, ""), http.StatusOK))
	if temporalAnswer.Answer.Window != TemporalQueryLastFiveMinutes || temporalAnswer.Answer.RoomID != officeRoomID || temporalAnswer.Answer.SittingID != sittingID || temporalAnswer.Answer.TranscriptHighWater == 0 || len(temporalAnswer.Answer.Evidence) == 0 || !strings.Contains(temporalAnswer.Answer.Text, "Dog Perfect") {
		t.Fatalf("five-minute temporal answer=%+v", temporalAnswer.Answer)
	}
	badTemporalWindow := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"temporal/answer", ajCookies, map[string]any{"roomId": officeRoomID, "window": "all_time"}, "")
	if badTemporalWindow.Code != http.StatusBadRequest {
		t.Fatalf("unbounded temporal window status=%d body=%s", badTemporalWindow.Code, badTemporalWindow.Body.String())
	}

	// The real specialist control endpoint now discovers Mary only because the
	// human-approved hire was projected into the canonical Workforce roster.
	// A qualified production joiner with fake dependencies is installed only
	// after the pending-provider surface is proved; it makes the first attempt
	// fail before session creation and the second join through the sole compiled
	// approval-to-provider path without starting a paid provider.
	fakeProvider := &fakeMeetingSpecialistProvider{}
	joinAttempts := 0

	specialists := e9DecodeFounder[e9FounderSpecialistResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, "/api/stride/v1/meeting-specialists?roomId="+officeRoomID, ajCookies, nil, ""), http.StatusOK))
	if specialists.Specialists.Available || !specialists.Specialists.CanInvite || specialists.Specialists.Reason != "provider_qualification_pending" || specialists.Specialists.RoomID != officeRoomID || specialists.Specialists.SittingID != sittingID || len(specialists.Specialists.Candidates) != 1 || specialists.Specialists.Candidates[0].AgentID != hired.Seat.ID || specialists.Specialists.Candidates[0].DisplayName != "Mary" {
		t.Fatalf("Scout-assisted specialist selection=%+v", specialists.Specialists)
	}
	wrongRoomInvite := e9FounderRequest(t, mux, http.MethodPost, "/api/stride/v1/meeting-specialists/invitations", ajCookies, map[string]any{"roomId": "other-room", "agentId": hired.Seat.ID, "purpose": "review", "idempotencyKey": "e9-wrong-room"}, "")
	if wrongRoomInvite.Code != http.StatusForbidden {
		t.Fatalf("wrong-room invite status=%d body=%s", wrongRoomInvite.Code, wrongRoomInvite.Body.String())
	}
	requested := e9DecodeFounder[e9FounderSpecialistResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, "/api/stride/v1/meeting-specialists/invitations", ajCookies, map[string]any{
		"roomId": officeRoomID, "agentId": hired.Seat.ID, "purpose": "Pressure-test Dog Perfect positioning", "idempotencyKey": "e9-founder-mary-join",
	}, ""), http.StatusOK))
	if requested.Invitation.Revision != 1 || requested.Invitation.AgentID != hired.Seat.ID || requested.Invitation.DisplayName != "Mary" || requested.Invitation.Decision != "requested" || requested.Invitation.Status != "awaiting_approval" || requested.ProviderSessionStarted {
		t.Fatalf("requested Mary invitation=%+v", requested)
	}
	requestReplay := e9DecodeFounder[e9FounderSpecialistResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, "/api/stride/v1/meeting-specialists/invitations", ajCookies, map[string]any{
		"roomId": officeRoomID, "agentId": hired.Seat.ID, "purpose": "Pressure-test Dog Perfect positioning", "idempotencyKey": "e9-founder-mary-join",
	}, ""), http.StatusOK))
	if requestReplay.Invitation.ID != requested.Invitation.ID || requestReplay.Invitation.Revision != requested.Invitation.Revision {
		t.Fatalf("specialist request replay=%+v first=%+v", requestReplay.Invitation, requested.Invitation)
	}
	staleSpecialistApproval := e9FounderRequest(t, mux, http.MethodPost, "/api/stride/v1/meeting-specialists/invitations/"+requested.Invitation.ID, ajCookies, map[string]any{"roomId": officeRoomID, "revision": 0, "decision": "approved"}, "")
	if staleSpecialistApproval.Code != http.StatusConflict {
		t.Fatalf("stale specialist approval status=%d body=%s", staleSpecialistApproval.Code, staleSpecialistApproval.Body.String())
	}
	app.meetingSpecialists.mu.Lock()
	joinCandidate := app.meetingSpecialists.invitations[requested.Invitation.ID].Agent
	app.meetingSpecialists.mu.Unlock()
	var fixtureFactoryCalls atomic.Int64
	productionJoin, _ := productionJoinFixture(time.Now().UTC(), fakeProvider, &fixtureFactoryCalls)
	productionJoin.qualificationTarget.TenantID = canonicalTenantID()
	productionJoin.qualificationTarget.SpecialistProfile = joinCandidate.Profile
	productionJoin.qualificationTarget.SpecialistCapability = joinCandidate.Capability
	qualification := productionJoin.qualification.(*fakeMeetingSpecialistQualificationAuthority)
	qualification.status.SubjectDigest, _ = MeetingSpecialistQualificationSubjectDigest(productionJoin.qualificationTarget)
	providerFactory := productionJoin.providerFactory
	productionJoin.providerFactory = func(ctx context.Context, launch MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
		joinAttempts++
		if joinAttempts == 1 {
			return nil, fmt.Errorf("deterministic specialist provider failure")
		}
		return providerFactory(ctx, launch)
	}
	app.meetingSpecialists.productionJoin = productionJoin
	failedJoin := e9DecodeFounder[e9FounderSpecialistResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, "/api/stride/v1/meeting-specialists/invitations/"+requested.Invitation.ID, ajCookies, map[string]any{"roomId": officeRoomID, "revision": requested.Invitation.Revision, "decision": "approved"}, ""), http.StatusOK))
	if failedJoin.Invitation.Status != "approved_session_failed" || failedJoin.ProviderSessionStarted || fakeProvider.briefs != 0 || joinAttempts != 1 {
		t.Fatalf("isolated specialist failure=%+v briefs=%d attempts=%d", failedJoin, fakeProvider.briefs, joinAttempts)
	}
	meetingAfterFailure, meetingStillActive := app.meetings.activeRecord(officeRoomID)
	if !meetingStillActive || meetingAfterFailure.ID != sittingID || !app.roomMediaGenerationCurrent(officeRoomID, mediaGeneration) || app.activeParticipantCount(officeRoomID) != 1 {
		t.Fatalf("specialist failure affected human meeting: meeting=%+v active=%t generation=%d participants=%d", meetingAfterFailure, meetingStillActive, app.roomMediaGeneration(officeRoomID), app.activeParticipantCount(officeRoomID))
	}

	joinedRequested := e9DecodeFounder[e9FounderSpecialistResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, "/api/stride/v1/meeting-specialists/invitations", ajCookies, map[string]any{
		"roomId": officeRoomID, "agentId": hired.Seat.ID, "purpose": "Pressure-test Dog Perfect positioning after recovery", "idempotencyKey": "e9-founder-mary-recovery",
	}, ""), http.StatusOK))
	joined := e9DecodeFounder[e9FounderSpecialistResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, "/api/stride/v1/meeting-specialists/invitations/"+joinedRequested.Invitation.ID, ajCookies, map[string]any{"roomId": officeRoomID, "revision": joinedRequested.Invitation.Revision, "decision": "approved"}, ""), http.StatusOK))
	if joined.Invitation.Revision != 2 || joined.Invitation.Status != "joined_session" || joined.Invitation.Decision != "approved" || !joined.ProviderSessionStarted || fakeProvider.briefs != 1 || joinAttempts != 2 {
		t.Fatalf("joined fake specialist=%+v briefs=%d attempts=%d", joined, fakeProvider.briefs, joinAttempts)
	}
	app.meetingSpecialists.mu.Lock()
	failedRecord := app.meetingSpecialists.invitations[requested.Invitation.ID]
	joinedRecord := app.meetingSpecialists.invitations[joinedRequested.Invitation.ID]
	app.meetingSpecialists.mu.Unlock()
	if failedRecord.Runtime != nil || joinedRecord.Runtime == nil || joinedRecord.Runtime.Snapshot().Session == nil {
		t.Fatalf("specialist runtime isolation failed: joined=%+v failed=%+v", joinedRecord.Runtime, failedRecord.Runtime)
	}

	// Source-level evidence binds the founder journey to the new composer
	// behavior without pretending this Go test rendered a browser or device.
	webSource, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`<script src="/public/composer-dictation.js"></script>`,
		`mount(scoutChatForm, scoutChatInput, 'chat')`,
		`mount(roomChatForm, roomChatInput, 'chat')`,
		`agentToolForms.forEach(form => mount`,
		`stopRealtimeVoiceConversation({ notifyServer: false, terminalReason: 'superseded_by_dictation' })`,
		`park('superseded_by_meeting_media')`,
	} {
		if !bytes.Contains(webSource, []byte(required)) {
			t.Fatalf("web composer source is missing %q", required)
		}
	}
	canvasSource, err := os.ReadFile(filepath.Join("mobile", "src", "screens", "CanvasScreen.tsx"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"useDictation", "useComposerDictation", "stopVoiceForComposer", "audioFocusRuntime.acquire('personal_realtime'", "composerDictation.commit()"} {
		if !bytes.Contains(canvasSource, []byte(required)) {
			t.Fatalf("mobile Canvas composer source is missing %q", required)
		}
	}

	firstRuntimeGeneration := app.strideRuntime.Health().Generation
	if err := app.strideRuntime.Save(); err != nil {
		t.Fatal(err)
	}
	// Simulate process replacement rather than a graceful meeting close. Revoke
	// the fake transport in memory, but leave the last signed product snapshot
	// as joined so restore must turn it into reauthorization-required.
	oldSpecialists := app.meetingSpecialists
	var replacedRuntimes []*MeetingSpecialistRuntime
	oldSpecialists.mu.Lock()
	for id, record := range oldSpecialists.invitations {
		if record.Runtime != nil {
			replacedRuntimes = append(replacedRuntimes, record.Runtime)
			record.Runtime = nil
			oldSpecialists.invitations[id] = record
		}
	}
	oldSpecialists.mu.Unlock()
	for _, runtime := range replacedRuntimes {
		runtime.RevokeGates("simulated_process_replacement")
	}
	app.meetingSpecialists = nil
	if err := app.Close(); err != nil {
		t.Fatalf("close first founder app: %v", err)
	}
	activeApp = nil

	restarted := newKanbanBoardApp()
	activeApp = restarted
	kanbanApp = restarted
	if health := restarted.strideRuntime.Health(); health.State != STRIDERuntimeStandby || !health.Restored || health.Generation <= firstRuntimeGeneration {
		t.Fatalf("restarted founder runtime health=%+v firstGeneration=%d", health, firstRuntimeGeneration)
	}
	restartedSittingID := admitMemberWithTranscriptConsentForTest(t, restarted, officeRoomID, "aj@shareability.com")
	restartedGeneration := restarted.ensureRoomMedia(officeRoomID)
	if restartedSittingID != sittingID || restartedGeneration == 0 {
		t.Fatalf("restarted meeting scope sitting=%s want=%s generation=%d", restartedSittingID, sittingID, restartedGeneration)
	}
	restoredWork := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work", ajCookies, nil, ""), http.StatusOK))
	if len(restoredWork.Suggestions) != 1 || len(restoredWork.Runs) != 1 || restoredWork.Suggestions[0].RunID != approved.Suggestion.RunID || restoredWork.Runs[0].Status != STRIDERunCompleted {
		t.Fatalf("restored work graph=%+v", restoredWork)
	}
	restoredRoster := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"roster", ajCookies, nil, ""), http.StatusOK))
	if len(restoredRoster.Seats) != 1 || restoredRoster.Seats[0].ID != hired.Seat.ID || restoredRoster.Seats[0].Revision != meetingAssigned.Seat.Revision || restoredRoster.Seats[0].Status != "hired_fenced" || len(restoredRoster.Seats[0].Assignments) != 2 || len(restoredRoster.Seats[0].Learning) != 1 || len(restoredRoster.Seats[0].Updates) != 2 {
		t.Fatalf("restored Mary history=%+v", restoredRoster)
	}
	restoredProject, _, projectErr := restarted.scoutChatThreadByID("aj@shareability.com", dogPerfect.ID)
	restoredDirect, _, directErr := restarted.scoutChatThreadByID("aj@shareability.com", hired.Seat.DirectThreadID)
	introCount := 0
	directHumanCount := 0
	for _, message := range restoredDirect.Messages {
		if strings.HasPrefix(message.ID, "stride-agent-intro-") {
			introCount++
		}
		if message.ID == directPost.Message.ID && message.Role == "user" && message.AuthorEmail == "aj@shareability.com" && message.Text == directMessageText {
			directHumanCount++
		}
	}
	if projectErr != nil || directErr != nil || len(restoredProject.Messages) != 1 || scoutChatThreadVisibility(restoredDirect) != scoutChatVisibilityPrivate || introCount != 1 || directHumanCount != 1 {
		t.Fatalf("restart duplicated/missed product messages: project=%d direct=%d projectErr=%v directErr=%v", len(restoredProject.Messages), len(restoredDirect.Messages), projectErr, directErr)
	}
	sharedEventsAfterRestart := 0
	if err := restarted.strideRuntime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
		snapshot, err := domains.ConversationLedger.Snapshot()
		if err != nil {
			return err
		}
		sharedEventsAfterRestart = len(snapshot.Events)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if sharedEventsAfterRestart != sharedEventsAfterDirect {
		t.Fatalf("restart projected private Mary history into shared ledger: before=%d after=%d", sharedEventsAfterDirect, sharedEventsAfterRestart)
	}
	restoredSpecialists := e9DecodeFounder[e9FounderSpecialistResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, "/api/stride/v1/meeting-specialists?roomId="+officeRoomID, ajCookies, nil, ""), http.StatusOK))
	var restoredJoined, restoredFailed meetingSpecialistInvitationView
	for _, invitation := range restoredSpecialists.Specialists.Invitations {
		switch invitation.ID {
		case requested.Invitation.ID:
			restoredFailed = invitation
		case joinedRequested.Invitation.ID:
			restoredJoined = invitation
		}
	}
	if restoredJoined.Status != "approved_reauthorization_required" || restoredFailed.Status != "approved_session_failed" || len(restoredSpecialists.Specialists.Candidates) != 1 || restoredSpecialists.Specialists.Candidates[0].DisplayName != "Mary" {
		t.Fatalf("restored specialist control state=%+v", restoredSpecialists.Specialists)
	}
	restarted.meetingSpecialists.mu.Lock()
	for id, record := range restarted.meetingSpecialists.invitations {
		if record.Runtime != nil {
			restarted.meetingSpecialists.mu.Unlock()
			t.Fatalf("restart resurrected transient specialist runtime %s", id)
		}
	}
	restarted.meetingSpecialists.mu.Unlock()

	paused := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/pause", ajCookies, map[string]any{"revision": meetingAssigned.Seat.Revision}, ""), http.StatusOK))
	if paused.Seat.Status != "paused" || !paused.Seat.AccessRevoked || paused.Seat.Revision != meetingAssigned.Seat.Revision+1 {
		t.Fatalf("paused Mary=%+v", paused.Seat)
	}
	pauseReplay := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/pause", ajCookies, map[string]any{"revision": meetingAssigned.Seat.Revision}, ""), http.StatusOK))
	if pauseReplay.Seat.Revision != paused.Seat.Revision || pauseReplay.Seat.Status != paused.Seat.Status {
		t.Fatalf("pause replay was not idempotent: first=%+v replay=%+v", paused.Seat, pauseReplay.Seat)
	}
	noPausedCandidate := e9DecodeFounder[e9FounderSpecialistResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, "/api/stride/v1/meeting-specialists?roomId="+officeRoomID, ajCookies, nil, ""), http.StatusOK))
	if noPausedCandidate.Specialists.CanInvite || len(noPausedCandidate.Specialists.Candidates) != 0 {
		t.Fatalf("paused Mary remained selectable=%+v", noPausedCandidate.Specialists)
	}
	offboarded := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/offboard", ajCookies, map[string]any{"revision": paused.Seat.Revision}, ""), http.StatusOK))
	if offboarded.Seat.Status != "offboarded" || !offboarded.Seat.AccessRevoked || offboarded.Seat.Revision != paused.Seat.Revision+1 || len(offboarded.Seat.Assignments) != 2 || len(offboarded.Seat.Learning) != 1 || len(offboarded.Seat.Updates) != 2 {
		t.Fatalf("offboarded Mary=%+v", offboarded.Seat)
	}
	offboardReplay := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/offboard", ajCookies, map[string]any{"revision": paused.Seat.Revision}, ""), http.StatusOK))
	if offboardReplay.Seat.Revision != offboarded.Seat.Revision || offboardReplay.Seat.Status != "offboarded" {
		t.Fatalf("offboard replay was not idempotent: first=%+v replay=%+v", offboarded.Seat, offboardReplay.Seat)
	}
	postOffboardMutation := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+hired.Seat.ID+"/configure", ajCookies, map[string]any{"revision": offboarded.Seat.Revision, "config": hired.Seat.Config}, "")
	if postOffboardMutation.Code != http.StatusForbidden {
		t.Fatalf("offboarded Mary accepted mutation status=%d body=%s", postOffboardMutation.Code, postOffboardMutation.Body.String())
	}
	if err := restarted.strideRuntime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
		var found *STRIDEWorkforceSeat
		for _, seat := range domains.Workforce.ScoutRosterView().Seats {
			if seat.ID == hired.Seat.ID {
				copy := seat
				found = &copy
			}
		}
		if found == nil || found.Status != "offboarded" || !found.AccessRevoked {
			t.Fatalf("canonical Workforce offboard state=%+v", found)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	secondRuntimeGeneration := restarted.strideRuntime.Health().Generation
	if err := restarted.Close(); err != nil {
		t.Fatalf("close restarted founder app: %v", err)
	}
	activeApp = nil
	t.Setenv("STRIDE_LOCAL_PRODUCT_PREVIEW_ENABLED", "false")
	t.Setenv("STRIDE_MEETING_SPECIALIST_CONTROL_ENABLED", "false")
	fenced := newKanbanBoardApp()
	activeApp = fenced
	kanbanApp = fenced
	if health := fenced.strideRuntime.Health(); health.State != STRIDERuntimeStandby || !health.Restored || health.Generation <= secondRuntimeGeneration {
		t.Fatalf("default-off restart health=%+v priorGeneration=%d", health, secondRuntimeGeneration)
	}
	fencedWork := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work", ajCookies, nil, ""), http.StatusOK))
	fencedMarketplace := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"marketplace", ajCookies, nil, ""), http.StatusOK))
	fencedRoster := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"roster", ajCookies, nil, ""), http.StatusOK))
	if fencedWork.Available || len(fencedWork.Suggestions) != 0 || len(fencedWork.Runs) != 0 || fencedMarketplace.ProductReachable || len(fencedMarketplace.Listings) != 0 || fencedRoster.Available || len(fencedRoster.Seats) != 0 {
		t.Fatalf("default-off product surface leaked state: work=%+v marketplace=%+v roster=%+v", fencedWork, fencedMarketplace, fencedRoster)
	}
	fencedMutation := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"marketplace/"+maryListing.ID+"/trial", ajCookies, nil, "")
	if fencedMutation.Code != http.StatusServiceUnavailable {
		t.Fatalf("default-off mutation status=%d body=%s", fencedMutation.Code, fencedMutation.Body.String())
	}
	fencedSpecialists := e9DecodeFounder[e9FounderSpecialistResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, "/api/stride/v1/meeting-specialists?roomId="+officeRoomID, ajCookies, nil, ""), http.StatusOK))
	if fencedSpecialists.Specialists.Available || fencedSpecialists.Specialists.CanInvite || fencedSpecialists.Specialists.Reason != "feature_disabled" {
		t.Fatalf("default-off specialist surface=%+v", fencedSpecialists.Specialists)
	}
	if err := fenced.strideRuntime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
		product, err := domains.Product.Snapshot()
		if err != nil {
			return err
		}
		if len(product.Work) != 1 || len(product.Agents) != 1 || product.Agents[0].Status != "offboarded" || domains.WorkOrchestrator.Store == nil || len(domains.WorkOrchestrator.Store.Runs) != 1 {
			runCount := 0
			if domains.WorkOrchestrator.Store != nil {
				runCount = len(domains.WorkOrchestrator.Store.Runs)
			}
			t.Fatalf("default-off restart lost durable history: product=%+v runs=%d", product, runCount)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	t.Log("E9 founder graph passed with zero provider credentials: #team -> Suggested Work -> revision-bound approval -> Dog Perfect -> completed insights_opportunities_v1 WorkRun/artifact/brain link; internal-preview Marketplace -> Mary trial/hire/config/assign/learning correction/update rollback -> Scout introduction and consent-bound fake specialist success/failure -> signed restart -> pause/offboard -> default-off fence.")
	t.Log("Excluded claims: paid-provider quality or compatibility, live Realtime/STT, real WebRTC/media-device behavior, physical-device acceptance, production data/restore/HA/soak, deployment, and release qualification.")
}

func e9WriteFounderSpecialistActivation(t *testing.T, path string, authority STRIDESnapshotMACAuthority, generation uint64) {
	t.Helper()
	now := time.Now().UTC()
	payload := meetingSpecialistControlActivationPayload{
		Format: meetingSpecialistControlActivationFormat, TenantID: "bonfire", Generation: generation, KeyID: authority.KeyID,
		Features: append([]STRIDEFeature(nil), meetingSpecialistControlFeatures...), EvidenceDigest: temporalDigest("e9_founder_specialist_control"),
		StateMinimumGeneration: 1, BootstrapEmpty: true, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour),
	}
	digest, err := STRIDEContractDigest(payload)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := strideSnapshotMAC(authority, meetingSpecialistControlActivationDomain, payload.Generation, digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFileAtomically(path, "E9 founder specialist activation", meetingSpecialistControlActivationEnvelope{Payload: payload, Digest: digest, Signature: signature}); err != nil {
		t.Fatal(err)
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
