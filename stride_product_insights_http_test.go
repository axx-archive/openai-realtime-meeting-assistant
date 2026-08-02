package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type strideProductInsightsArtifactHTTPResponse struct {
	OK       bool `json:"ok"`
	Artifact struct {
		ID               string                                  `json:"id"`
		ReportAvailable  bool                                    `json:"reportAvailable"`
		ReportCurrent    bool                                    `json:"reportCurrent"`
		FeedbackRevision int64                                   `json:"feedbackRevision"`
		Report           StrideInsightsReport                    `json:"report"`
		ReportArtifact   StrideInsightsArtifact                  `json:"reportArtifact"`
		Revisions        []strideProductInsightsArtifactRevision `json:"revisions"`
		Feedback         []StrideInsightsFeedback                `json:"feedback"`
	} `json:"artifact"`
}

type strideProductInsightsFeedbackHTTPResponse struct {
	OK               bool                                    `json:"ok"`
	Replayed         bool                                    `json:"replayed"`
	FeedbackRevision int64                                   `json:"feedbackRevision"`
	Feedback         StrideInsightsFeedback                  `json:"feedback"`
	Report           StrideInsightsReport                    `json:"report"`
	ReportArtifact   StrideInsightsArtifact                  `json:"reportArtifact"`
	Revisions        []strideProductInsightsArtifactRevision `json:"revisions"`
	FeedbackLineage  []StrideInsightsFeedback                `json:"feedbackLineage"`
	ProviderCalls    int                                     `json:"providerCalls"`
	InputTokens      int                                     `json:"inputTokens"`
	OutputTokens     int                                     `json:"outputTokens"`
}

func strideProductInsightsRequest(t *testing.T, handler http.Handler, method, path string, cookies []*http.Cookie, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func decodeSTRIDEProductInsightsResponse[T any](t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int) T {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, wantStatus, recorder.Body.String())
	}
	var result T
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func completeSTRIDEProductInsightsFixture(t *testing.T, fixture strideProjectAuthorityFixture, mux http.Handler, cookies []*http.Cookie) STRIDEProductWorkRecord {
	t.Helper()
	destination := decodeSTRIDEProductInsightsResponse[struct {
		Suggestion STRIDEProductWorkRecord `json:"suggestion"`
	}](t, strideProductInsightsRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions/"+fixture.suggestion.ID+"/destination", cookies, map[string]any{
		"revision": fixture.suggestion.Revision, "mode": "new", "title": "Durable Insights",
	}), http.StatusOK).Suggestion
	return decodeSTRIDEProductInsightsResponse[struct {
		Suggestion STRIDEProductWorkRecord `json:"suggestion"`
	}](t, strideProductInsightsRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions/"+fixture.suggestion.ID+"/approve", cookies, map[string]any{
		"revision": destination.Revision,
	}), http.StatusOK).Suggestion
}

func TestSTRIDEProductInsightsReportFeedbackAndRevisionSurviveSignedRestart(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)
	ownerCookies := loginAs(t, fixture.user.Email, defaultMeetingRoomPassword)
	reviewerCookies := loginAs(t, "caitlyn@shareability.com", defaultMeetingRoomPassword)
	outsiderCookies := loginAs(t, artifactLibraryAdminEmail, defaultMeetingRoomPassword)
	completed := completeSTRIDEProductInsightsFixture(t, fixture, mux, ownerCookies)
	if completed.Status != "completed" || completed.ArtifactHref == "" {
		t.Fatalf("completed work=%+v", completed)
	}

	initial := decodeSTRIDEProductInsightsResponse[strideProductInsightsArtifactHTTPResponse](t, strideProductInsightsRequest(t, mux, http.MethodGet, completed.ArtifactHref, ownerCookies, nil), http.StatusOK)
	if !initial.OK || !initial.Artifact.ReportAvailable || !initial.Artifact.ReportCurrent || initial.Artifact.FeedbackRevision != 1 || initial.Artifact.ID != completed.ArtifactID {
		t.Fatalf("initial artifact envelope=%+v", initial.Artifact)
	}
	if initial.Artifact.Report.ReportDigest == "" || len(initial.Artifact.Report.Claims) == 0 || len(initial.Artifact.Report.Opportunities) == 0 || initial.Artifact.ReportArtifact.ReportDigest != initial.Artifact.Report.ReportDigest || len(initial.Artifact.Revisions) != 1 {
		t.Fatalf("initial durable artifact=%+v", initial.Artifact)
	}
	initialReport := initial.Artifact.Report
	if recorder := strideProductInsightsRequest(t, mux, http.MethodGet, completed.ArtifactHref, outsiderCookies, nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("outsider artifact status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	if recorder := strideProductInsightsRequest(t, mux, http.MethodPost, completed.ArtifactHref[:strings.LastIndex(completed.ArtifactHref, "/")]+"/feedback", outsiderCookies, map[string]any{
		"feedbackRevision": 1, "reportDigest": initialReport.ReportDigest, "action": insightsFeedbackCorrect,
		"correction": "The owner should remain the product lead.", "idempotencyKey": "outsider-feedback-0001",
	}); recorder.Code != http.StatusForbidden {
		t.Fatalf("outsider feedback status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	feedbackPath := completed.ArtifactHref[:strings.LastIndex(completed.ArtifactHref, "/")] + "/feedback"
	correctionRequest := map[string]any{"feedbackRevision": 1, "reportDigest": initialReport.ReportDigest, "action": insightsFeedbackCorrect,
		"correction": "The owner should remain the product lead.", "idempotencyKey": "durable-correction-0001"}
	corrected := decodeSTRIDEProductInsightsResponse[strideProductInsightsFeedbackHTTPResponse](t, strideProductInsightsRequest(t, mux, http.MethodPost, feedbackPath, ownerCookies, correctionRequest), http.StatusOK)
	if !corrected.OK || corrected.Replayed || corrected.FeedbackRevision != 2 || corrected.Report.ReportDigest != initialReport.ReportDigest || corrected.Feedback.Action != insightsFeedbackCorrect || len(corrected.FeedbackLineage) != 1 || corrected.ProviderCalls != 0 || corrected.InputTokens != 0 || corrected.OutputTokens != 0 {
		t.Fatalf("correction response=%+v", corrected)
	}
	correctionReplay := decodeSTRIDEProductInsightsResponse[strideProductInsightsFeedbackHTTPResponse](t, strideProductInsightsRequest(t, mux, http.MethodPost, feedbackPath, ownerCookies, correctionRequest), http.StatusOK)
	if !correctionReplay.Replayed || correctionReplay.Feedback.FeedbackDigest != corrected.Feedback.FeedbackDigest || correctionReplay.FeedbackRevision != corrected.FeedbackRevision {
		t.Fatalf("correction replay=%+v", correctionReplay)
	}
	conflict := strideProductInsightsRequest(t, mux, http.MethodPost, feedbackPath, ownerCookies, map[string]any{
		"feedbackRevision": 2, "reportDigest": initialReport.ReportDigest, "action": insightsFeedbackReject, "idempotencyKey": "durable-correction-0001",
	})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("idempotency conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}
	stale := strideProductInsightsRequest(t, mux, http.MethodPost, feedbackPath, ownerCookies, map[string]any{
		"feedbackRevision": 1, "reportDigest": initialReport.ReportDigest, "action": insightsFeedbackAccept, "idempotencyKey": "stale-feedback-000001",
	})
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale feedback status=%d body=%s", stale.Code, stale.Body.String())
	}

	revisionRequest := map[string]any{"feedbackRevision": 2, "reportDigest": initialReport.ReportDigest, "action": insightsFeedbackRequestRevision,
		"correction": "Make the follow-through owner explicit.", "idempotencyKey": "durable-rerun-0000001"}
	revised := decodeSTRIDEProductInsightsResponse[strideProductInsightsFeedbackHTTPResponse](t, strideProductInsightsRequest(t, mux, http.MethodPost, feedbackPath, reviewerCookies, revisionRequest), http.StatusOK)
	if revised.Replayed || revised.FeedbackRevision != 3 || revised.Report.ReportDigest == initialReport.ReportDigest || revised.Report.Revision != 1 ||
		revised.ReportArtifact.ReportDigest != revised.Report.ReportDigest || len(revised.Revisions) != 2 || len(revised.FeedbackLineage) != 2 ||
		revised.Revisions[1].ParentReportDigest != initialReport.ReportDigest || revised.Revisions[1].RequestRevision != 2 || !strings.Contains(revised.Report.Claims[0].NextAction, "product lead") {
		t.Fatalf("revised artifact=%+v", revised)
	}
	revisionReplay := decodeSTRIDEProductInsightsResponse[strideProductInsightsFeedbackHTTPResponse](t, strideProductInsightsRequest(t, mux, http.MethodPost, feedbackPath, reviewerCookies, revisionRequest), http.StatusOK)
	if !revisionReplay.Replayed || revisionReplay.Feedback.FeedbackDigest != revised.Feedback.FeedbackDigest || revisionReplay.FeedbackRevision != revised.FeedbackRevision || revisionReplay.Report.ReportDigest != revised.Report.ReportDigest || len(revisionReplay.Revisions) != 2 {
		t.Fatalf("revision replay=%+v", revisionReplay)
	}

	oldRevision := decodeSTRIDEProductInsightsResponse[strideProductInsightsArtifactHTTPResponse](t, strideProductInsightsRequest(t, mux, http.MethodGet, completed.ArtifactHref+"?reportDigest="+initialReport.ReportDigest, ownerCookies, nil), http.StatusOK)
	if oldRevision.Artifact.ReportCurrent || oldRevision.Artifact.Report.ReportDigest != initialReport.ReportDigest || oldRevision.Artifact.Report.ReportDigest != oldRevision.Artifact.ReportArtifact.ReportDigest {
		t.Fatalf("old immutable revision=%+v", oldRevision.Artifact)
	}

	if err := fixture.runtime.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.config.BootstrapEmpty = false
	restarted, err := NewSTRIDERuntime(fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	fixture.app.strideRuntime = restarted
	restored := decodeSTRIDEProductInsightsResponse[strideProductInsightsArtifactHTTPResponse](t, strideProductInsightsRequest(t, mux, http.MethodGet, completed.ArtifactHref, ownerCookies, nil), http.StatusOK)
	if !restored.Artifact.ReportAvailable || restored.Artifact.FeedbackRevision != 3 || restored.Artifact.Report.ReportDigest != revised.Report.ReportDigest ||
		len(restored.Artifact.Revisions) != 2 || len(restored.Artifact.Feedback) != 2 || restored.Artifact.Revisions[0].Report.ReportDigest != initialReport.ReportDigest {
		t.Fatalf("restored report lineage=%+v", restored.Artifact)
	}
	restoredReplay := decodeSTRIDEProductInsightsResponse[strideProductInsightsFeedbackHTTPResponse](t, strideProductInsightsRequest(t, mux, http.MethodPost, feedbackPath, reviewerCookies, revisionRequest), http.StatusOK)
	if !restoredReplay.Replayed || restoredReplay.FeedbackRevision != 3 || restoredReplay.Report.ReportDigest != revised.Report.ReportDigest || len(restoredReplay.FeedbackLineage) != 2 {
		t.Fatalf("restored replay=%+v", restoredReplay)
	}
	if err := restarted.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		ctx.WorkStore.mu.Lock()
		defer ctx.WorkStore.mu.Unlock()
		if len(ctx.WorkStore.Feedback) != 2 {
			t.Fatalf("durable work feedback=%+v", ctx.WorkStore.Feedback)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	source, _, err := fixture.app.scoutChatThreadByID(fixture.user.Email, fixture.source.ID)
	if err != nil {
		t.Fatal(err)
	}
	index := scoutChatMessageIndex(source, fixture.suggestion.SourceMessageID)
	if index < 0 {
		t.Fatal("source message missing")
	}
	edited := source.Messages[index]
	edited.Text = "This request was withdrawn."
	edited.EditedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := fixture.app.projectSTRIDETeamChatMessage(source, edited, "edit", fixture.user.Email); err != nil {
		t.Fatal(err)
	}
	if recorder := strideProductInsightsRequest(t, mux, http.MethodPost, feedbackPath, ownerCookies, map[string]any{
		"feedbackRevision": 3, "reportDigest": revised.Report.ReportDigest, "action": insightsFeedbackAccept, "idempotencyKey": "revoked-feedback-00001",
	}); recorder.Code != http.StatusGone {
		t.Fatalf("feedback-triggered revocation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
	revokedRestart, err := NewSTRIDERuntime(fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	fixture.app.strideRuntime = revokedRestart
	if recorder := strideProductInsightsRequest(t, mux, http.MethodGet, completed.ArtifactHref, ownerCookies, nil); recorder.Code != http.StatusGone {
		t.Fatalf("durably revoked artifact status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if err := revokedRestart.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		if _, found := ctx.Product.insightsState(completed.ID); found {
			t.Fatal("source invalidation retained private report/feedback payload")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSTRIDEProductInsightsFeedbackConcurrentReplayCommitsOnce(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)
	cookies := loginAs(t, fixture.user.Email, defaultMeetingRoomPassword)
	completed := completeSTRIDEProductInsightsFixture(t, fixture, mux, cookies)
	initial := decodeSTRIDEProductInsightsResponse[strideProductInsightsArtifactHTTPResponse](t, strideProductInsightsRequest(t, mux, http.MethodGet, completed.ArtifactHref, cookies, nil), http.StatusOK)
	feedbackPath := completed.ArtifactHref[:strings.LastIndex(completed.ArtifactHref, "/")] + "/feedback"
	body := map[string]any{"feedbackRevision": 1, "reportDigest": initial.Artifact.Report.ReportDigest, "action": insightsFeedbackCorrect,
		"correction": "Keep the owner explicit.", "idempotencyKey": "concurrent-feedback-0001"}

	const attempts = 8
	statuses := make(chan int, attempts)
	var wait sync.WaitGroup
	for index := 0; index < attempts; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			statuses <- strideProductInsightsRequest(t, mux, http.MethodPost, feedbackPath, cookies, body).Code
		}()
	}
	wait.Wait()
	close(statuses)
	for status := range statuses {
		if status != http.StatusOK {
			t.Fatalf("concurrent feedback status=%d", status)
		}
	}
	if err := fixture.runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		state, found := ctx.Product.insightsState(completed.ID)
		if !found || state.Revision != 2 {
			t.Fatalf("insights state=%+v found=%t", state, found)
		}
		ctx.WorkStore.mu.Lock()
		defer ctx.WorkStore.mu.Unlock()
		if len(ctx.WorkStore.Feedback) != 1 {
			t.Fatalf("feedback writes=%d want=1", len(ctx.WorkStore.Feedback))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSTRIDEProductInsightsArtifactReadFailsClosedAfterDestinationMembershipRevocation(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)
	ownerCookies := loginAs(t, fixture.user.Email, defaultMeetingRoomPassword)
	memberCookies := loginAs(t, "caitlyn@shareability.com", defaultMeetingRoomPassword)
	completed := completeSTRIDEProductInsightsFixture(t, fixture, mux, ownerCookies)
	decodeSTRIDEProductInsightsResponse[strideProductInsightsArtifactHTTPResponse](t, strideProductInsightsRequest(t, mux, http.MethodGet, completed.ArtifactHref, memberCookies, nil), http.StatusOK)

	lock := fixture.app.scoutChatThreadLock(completed.DestinationThreadID)
	lock.Lock()
	destination, _, err := fixture.app.scoutChatThreadByID(fixture.user.Email, completed.DestinationThreadID)
	if err == nil {
		destination.MemberEmails = []string{fixture.user.Email}
		err = fixture.app.saveScoutChatThread(destination)
	}
	lock.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if recorder := strideProductInsightsRequest(t, mux, http.MethodGet, completed.ArtifactHref, memberCookies, nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("revoked destination member artifact status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	// Even a remaining member must not read through an ACL-version drift. A new
	// approved binding is required before the durable artifact can be reopened.
	if recorder := strideProductInsightsRequest(t, mux, http.MethodGet, completed.ArtifactHref, ownerCookies, nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("destination ACL drift artifact status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSTRIDEProductInsightsRerunCanonicalLedgerSurvivesRestartAndRejectsDrift(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)
	cookies := loginAs(t, fixture.user.Email, defaultMeetingRoomPassword)
	completed := completeSTRIDEProductInsightsFixture(t, fixture, mux, cookies)
	initial := decodeSTRIDEProductInsightsResponse[strideProductInsightsArtifactHTTPResponse](t, strideProductInsightsRequest(t, mux, http.MethodGet, completed.ArtifactHref, cookies, nil), http.StatusOK)
	feedbackPath := completed.ArtifactHref[:strings.LastIndex(completed.ArtifactHref, "/")] + "/feedback"
	revised := decodeSTRIDEProductInsightsResponse[strideProductInsightsFeedbackHTTPResponse](t, strideProductInsightsRequest(t, mux, http.MethodPost, feedbackPath, cookies, map[string]any{
		"feedbackRevision": 1, "reportDigest": initial.Artifact.Report.ReportDigest, "action": insightsFeedbackRequestRevision,
		"correction": "Bind the revised artifact to the canonical work ledger.", "idempotencyKey": "canonical-rerun-0000001",
	}), http.StatusOK)
	childRunID := revised.Report.RunID
	artifactID := "artifact_" + childRunID
	outcomeID := "outcome_" + childRunID

	assertCanonical := func(t *testing.T, runtime *STRIDERuntime) {
		t.Helper()
		if err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
			ctx.WorkStore.mu.Lock()
			defer ctx.WorkStore.mu.Unlock()
			child, runFound := ctx.WorkStore.Runs[childRunID]
			artifact, artifactFound := ctx.WorkStore.Artifacts[artifactID]
			outcome, outcomeFound := ctx.WorkStore.Outcomes[outcomeID]
			feedback, feedbackFound := ctx.WorkStore.Feedback[revised.Feedback.FeedbackID]
			if len(ctx.WorkStore.Runs) != 2 || len(ctx.WorkStore.Artifacts) != 2 || len(ctx.WorkStore.Outcomes) != 2 || !runFound || !artifactFound || !outcomeFound || !feedbackFound {
				t.Fatalf("canonical rerun ledger runs=%d artifacts=%d outcomes=%d feedback=%d", len(ctx.WorkStore.Runs), len(ctx.WorkStore.Artifacts), len(ctx.WorkStore.Outcomes), len(ctx.WorkStore.Feedback))
			}
			if child.Status != STRIDERunCompleted || child.ParentRunID != completed.RunID || child.ParentFeedbackID != revised.Feedback.FeedbackID || feedback.RunID != completed.RunID ||
				artifact.RunID != childRunID || artifact.Artifact.ID != revised.ReportArtifact.ArtifactID || artifact.Artifact.Digest != revised.ReportArtifact.ArtifactDigest ||
				outcome.RunID != childRunID || len(outcome.ArtifactIDs) != 1 || outcome.ArtifactIDs[0] != artifactID {
				t.Fatalf("canonical rerun binding child=%+v feedback=%+v artifact=%+v outcome=%+v", child, feedback, artifact, outcome)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertCanonical(t, fixture.runtime)
	if err := fixture.runtime.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.config.BootstrapEmpty = false
	restarted, err := NewSTRIDERuntime(fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	fixture.app.strideRuntime = restarted
	assertCanonical(t, restarted)

	restarted.mu.Lock()
	storedArtifact := restarted.domains.workStore.Artifacts[artifactID]
	tamperedArtifact := storedArtifact
	tamperedArtifact.Artifact.ID = "insights-artifact-cross-domain-drift"
	tamperedArtifact.Artifact.Digest = temporalDigest("cross-domain-drift")
	restarted.domains.workStore.Artifacts[artifactID] = tamperedArtifact
	validationErr := validateSTRIDERuntimeTenantState(canonicalTenantID(), restarted.domains)
	restarted.domains.workStore.Artifacts[artifactID] = storedArtifact
	restarted.mu.Unlock()
	if !errors.Is(validationErr, ErrSTRIDEProductInvalid) {
		t.Fatalf("missing canonical rerun artifact validation error=%v", validationErr)
	}
}
