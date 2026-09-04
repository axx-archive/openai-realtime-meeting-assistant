package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type workFeedbackTestFixture struct {
	root    meetingMemoryEntry
	cookies []*http.Cookie
	detail  studioProjectView
}

func setupWorkFeedbackFixture(t *testing.T) workFeedbackTestFixture {
	t.Helper()
	setupAuthTestEnv(t)
	setUsageLedgerDirForTest(t)
	priorApp, priorAuth := kanbanApp, artifactObjectAuthorizer
	kanbanApp, artifactObjectAuthorizer = newIsolatedKanbanBoardApp(t), LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() { kanbanApp, artifactObjectAuthorizer = priorApp, priorAuth })
	owner := "aj@shareability.com"
	source, err := kanbanApp.createScoutChatThread(owner, "AJ", "Launch decision", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Expansion recommendation", "# Expansion recommendation\n\nStart with a two-week pilot. Measure paid adoption before extending the rollout.", "Scout", map[string]string{
		"source": "scout_thread", "status": codexJobStatusComplete, "threadStatus": codexJobStatusComplete,
		"originKind": agentThreadOriginPrivateThread, "originId": source.ID, "visibility": scoutChatVisibilityPrivate,
		"ownerEmail": owner, "requestedBy": owner, "mode": "research", "threadId": "feedback-fixture-run", "type": artifactTypeMarkdown,
	})
	if err != nil {
		t.Fatal(err)
	}
	cookies := loginAs(t, owner, "B0NFIRE!")
	fixture := workFeedbackTestFixture{root: root, cookies: cookies}
	fixture.detail = readWorkFeedbackDetail(t, fixture)
	if fixture.detail.Result == nil || fixture.detail.Feedback == nil || !fixture.detail.Feedback.CanReview {
		t.Fatalf("fixture is not reviewable: %+v", fixture.detail)
	}
	return fixture
}

func readWorkFeedbackDetail(t *testing.T, fixture workFeedbackTestFixture) studioProjectView {
	t.Helper()
	reply := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?id="+fixture.root.ID, "", fixture.cookies, studioProjectsHandler)
	if reply.Code != 200 {
		t.Fatalf("detail: %d %s", reply.Code, reply.Body.String())
	}
	var payload struct {
		Project studioProjectView `json:"project"`
	}
	if err := json.Unmarshal(reply.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Project
}

func workFeedbackBody(fixture workFeedbackTestFixture, feedback studioWorkFeedbackRequest) string {
	raw, _ := json.Marshal(map[string]any{"id": fixture.root.ID, "expectedRevision": fixture.detail.Revision, "feedback": feedback})
	return string(raw)
}

func TestStudioWorkFeedbackAcceptanceOutcomeAndPrivateHistory(t *testing.T) {
	f := setupWorkFeedbackFixture(t)
	exact := studioWorkCurrentResult(f.detail)
	beforeOutcome := studioWorkFeedbackRequest{Type: "outcome", Verdict: "helped", IdempotencyKey: "outcome-before-review", AcceptedReviewID: "invented", Result: exact}
	if reply := artifactAuthorizationRequest(t, http.MethodPatch, "/api/studio-projects/v1", workFeedbackBody(f, beforeOutcome), f.cookies, studioProjectsHandler); reply.Code != 409 {
		t.Fatalf("unaccepted outcome: %d %s", reply.Code, reply.Body.String())
	}
	accept := studioWorkFeedbackRequest{Type: "review", Verdict: "accepted", IdempotencyKey: "acceptance-operation-1", Result: exact}
	reply := artifactAuthorizationRequest(t, http.MethodPatch, "/api/studio-projects/v1", workFeedbackBody(f, accept), f.cookies, studioProjectsHandler)
	var accepted struct {
		Event        studioWorkFeedbackEvent `json:"event"`
		Feedback     studioWorkFeedbackView  `json:"feedback"`
		RerunStarted bool                    `json:"rerunStarted"`
	}
	if err := json.Unmarshal(reply.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if reply.Code != 200 || accepted.Event.ActorID != "aj@shareability.com" || accepted.Event.ID == "" || accepted.Feedback.ReviewState != "accepted" || !accepted.Feedback.CanObserveOutcome || accepted.RerunStarted {
		t.Fatalf("acceptance: %d %s", reply.Code, reply.Body.String())
	}
	observe := studioWorkFeedbackRequest{Type: "outcome", Verdict: "helped", IdempotencyKey: "observed-operation-1", AcceptedReviewID: accepted.Event.ID, Result: exact, Note: "Private proof: pilot cut rework by one day."}
	if reply := artifactAuthorizationRequest(t, http.MethodPatch, "/api/studio-projects/v1", workFeedbackBody(f, observe), f.cookies, studioProjectsHandler); reply.Code != 200 {
		t.Fatalf("outcome: %d %s", reply.Code, reply.Body.String())
	}
	detail := readWorkFeedbackDetail(t, f)
	if detail.Feedback.CurrentOutcome == nil || detail.Feedback.CurrentOutcome.Verdict != "helped" || len(detail.Feedback.History) != 2 {
		t.Fatalf("outcome projection: %+v", detail.Feedback)
	}
	if detail.Status != f.detail.Status {
		t.Fatal("human feedback changed execution status")
	}
	for _, row := range kanbanApp.memory.snapshot(0) {
		if row.Kind == meetingMemoryKindWorkReview || strings.Contains(row.Text, "Private proof") {
			t.Fatal("feedback entered snapshot/ambient context")
		}
	}
	if matches := kanbanApp.memory.search("Private proof", 10); len(matches) != 0 {
		t.Fatalf("feedback entered recall: %+v", matches)
	}
	if len(kanbanApp.memory.entriesOfKind(meetingMemoryKindSignal, 0)) != 0 {
		t.Fatal("feedback auto-created taste signal")
	}
	other := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	if reply := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?id="+f.root.ID, "", other, studioProjectsHandler); reply.Code != 404 {
		t.Fatalf("private feedback read: %d", reply.Code)
	}
	if reply := artifactAuthorizationRequest(t, http.MethodPatch, "/api/studio-projects/v1", workFeedbackBody(f, accept), other, studioProjectsHandler); reply.Code != 404 {
		t.Fatalf("private feedback write: %d", reply.Code)
	}
	stored, _ := kanbanApp.memory.entryByKindAndID(meetingMemoryKindOSArtifact, f.root.ID)
	if artifactIsPublished(stored) || stored.Metadata[artifactHumanApprovedAtKey] != "" {
		t.Fatal("acceptance granted publication/approval authority")
	}
	if _, _, err := kanbanApp.updateOSArtifactWithMetadata(f.root.ID, "", "# Revised recommendation\n\nRun a four-week pilot instead.", "AJ", nil); err != nil {
		t.Fatal(err)
	}
	after := readWorkFeedbackDetail(t, f)
	if after.Feedback.ReviewState != "unreviewed" || after.Feedback.CurrentReview != nil || after.Feedback.CurrentOutcome != nil || len(after.Feedback.History) != 2 {
		t.Fatalf("old acceptance leaked onto new result: %+v", after.Feedback)
	}
	stale := accept
	stale.IdempotencyKey = "new-stale-acceptance"
	if reply := artifactAuthorizationRequest(t, http.MethodPatch, "/api/studio-projects/v1", workFeedbackBody(f, stale), f.cookies, studioProjectsHandler); reply.Code != 409 {
		t.Fatalf("stale result accepted: %d %s", reply.Code, reply.Body.String())
	}
	// A later outcome still refers to the accepted historical version; it does
	// not set acceptance/outcome state on today's edited result.
	observe.IdempotencyKey = "historical-outcome-2"
	observe.Verdict = "inconclusive"
	if reply := artifactAuthorizationRequest(t, http.MethodPatch, "/api/studio-projects/v1", workFeedbackBody(f, observe), f.cookies, studioProjectsHandler); reply.Code != 200 {
		t.Fatalf("historical outcome: %d %s", reply.Code, reply.Body.String())
	}
	if readWorkFeedbackDetail(t, f).Feedback.CurrentOutcome != nil {
		t.Fatal("historical outcome became current")
	}
}

func TestStudioWorkFeedbackReplayConcurrencyAndRestart(t *testing.T) {
	f := setupWorkFeedbackFixture(t)
	request := studioWorkFeedbackRequest{Type: "review", Verdict: "revision_requested", Note: "Add a measurable adoption target.", IdempotencyKey: "revision-operation-stable", Result: studioWorkCurrentResult(f.detail)}
	body := workFeedbackBody(f, request)
	var wg sync.WaitGroup
	results := make(chan int, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reply := artifactAuthorizationRequest(t, http.MethodPatch, "/api/studio-projects/v1", body, f.cookies, studioProjectsHandler)
			results <- reply.Code
		}()
	}
	wg.Wait()
	close(results)
	for status := range results {
		if status != 200 {
			t.Fatalf("concurrent feedback status: %d", status)
		}
	}
	if rows := kanbanApp.memory.entriesOfKind(meetingMemoryKindWorkReview, 0); len(rows) != 1 {
		t.Fatalf("replay appended %d records", len(rows))
	}
	reloaded, err := newMeetingMemoryStore(kanbanApp.memory.path)
	if err != nil {
		t.Fatal(err)
	}
	kanbanApp.memory = reloaded
	reply := artifactAuthorizationRequest(t, http.MethodPatch, "/api/studio-projects/v1", body, f.cookies, studioProjectsHandler)
	if reply.Code != 200 || !strings.Contains(reply.Body.String(), `"replayed":true`) || !strings.Contains(reply.Body.String(), `"rerunStarted":false`) {
		t.Fatalf("restart replay: %d %s", reply.Code, reply.Body.String())
	}
	request.Note = "Different request using the same key"
	if reply := artifactAuthorizationRequest(t, http.MethodPatch, "/api/studio-projects/v1", workFeedbackBody(f, request), f.cookies, studioProjectsHandler); reply.Code != 409 {
		t.Fatalf("key collision status: %d", reply.Code)
	}
	view := readWorkFeedbackDetail(t, f)
	if view.Feedback.ReviewState != "revision_requested" || view.Feedback.CanObserveOutcome {
		t.Fatalf("revision request claimed acceptance: %+v", view.Feedback)
	}
}

func TestStudioWorkFeedbackRejectsRevokedHeaderAtAppend(t *testing.T) {
	f := setupWorkFeedbackFixture(t)
	header, ok := kanbanApp.memory.artifactAuthorizationHeaderByID(f.root.ID)
	if !ok {
		t.Fatal("missing header")
	}
	if _, _, err := kanbanApp.memory.updateOSArtifactMetadata(f.root.ID, map[string]string{"ownerEmail": "tim@shareability.com", "requestedBy": "tim@shareability.com", "originSurface": ""}); err != nil {
		t.Fatal(err)
	}
	event := studioWorkFeedbackEvent{ID: "test-revoke", RootID: f.root.ID, Type: "review", Verdict: "accepted", Result: studioWorkCurrentResult(f.detail)}
	if _, _, err := kanbanApp.memory.appendStudioWorkFeedback(event, "", f.root, f.root, header, header, f.detail.Revision, f.detail); err == nil {
		t.Fatal("revoked ACL appended feedback")
	}
	if len(kanbanApp.memory.entriesOfKind(meetingMemoryKindWorkReview, 0)) != 0 {
		t.Fatal("revoked review persisted")
	}
}

func TestStudioWorkFeedbackCapturedPrivateAudienceSurvivesSharing(t *testing.T) {
	f := setupWorkFeedbackFixture(t)
	request := studioWorkFeedbackRequest{Type: "review", Verdict: "accepted", Note: "Private judgment before sharing", IdempotencyKey: "captured-audience-note", Result: studioWorkCurrentResult(f.detail)}
	if reply := artifactAuthorizationRequest(t, http.MethodPatch, "/api/studio-projects/v1", workFeedbackBody(f, request), f.cookies, studioProjectsHandler); reply.Code != 200 {
		t.Fatalf("review: %d %s", reply.Code, reply.Body.String())
	}
	if _, _, err := kanbanApp.memory.updateOSArtifactMetadata(f.root.ID, map[string]string{"visibility": "organization", "originSurface": ""}); err != nil {
		t.Fatal(err)
	}
	f.cookies = loginAs(t, "tim@shareability.com", "B0NFIRE!")
	view := readWorkFeedbackDetail(t, f)
	if len(view.Feedback.History) != 0 || view.Feedback.CurrentReview != nil {
		t.Fatalf("sharing published private feedback: %+v", view.Feedback)
	}
}

func TestStudioWorkFeedbackHumanReviewDoesNotRequireMachineAdmission(t *testing.T) {
	f := setupWorkFeedbackFixture(t)
	f.detail.Result.ReviewManaged = true
	f.detail.Result.QualityState = authoredResultQualityDraftNeedsAttention
	f.detail.Status = studioProjectStatusNeedsAttention
	viewer := accountStore().findUser("aj@shareability.com")
	view := studioWorkFeedbackForViewer(context.Background(), viewer, f.root, f.detail)
	if !view.CanReview {
		t.Fatal("machine quality gate prevented human review of produced result")
	}
	header, _ := kanbanApp.memory.artifactAuthorizationHeaderByID(f.root.ID)
	event := studioWorkFeedbackEvent{ID: "unadmitted-human-review", RootID: f.root.ID, Type: "review", Verdict: "accepted", Result: studioWorkCurrentResult(f.detail), ActorID: viewer.Email, At: time.Now().UTC(), RequestDigest: strings.Repeat("a", 64)}
	if _, _, err := kanbanApp.memory.appendStudioWorkFeedback(event, "", f.root, f.root, header, header, f.detail.Revision, f.detail); err != nil {
		t.Fatal(err)
	}
	if current := studioWorkFeedbackForViewer(context.Background(), viewer, f.root, f.detail); current.ReviewState != "accepted" {
		t.Fatalf("human review missing: %+v", current)
	}
	if f.detail.Result.QualityState != authoredResultQualityDraftNeedsAttention || f.detail.Status != studioProjectStatusNeedsAttention {
		t.Fatal("human review rewrote machine execution evidence")
	}
}

// Explicit opt-in exports only synthetic test state for rendered verification.
func TestStudioWorkFeedbackExportBrowserFixture(t *testing.T) {
	destination := os.Getenv("STRIDE_WORK_FEEDBACK_FIXTURE_DIR")
	if destination == "" {
		t.Skip("opt-in synthetic browser fixture")
	}
	if !strings.HasPrefix(filepath.Clean(destination), "/tmp/") {
		t.Fatal("fixture output must be under /tmp")
	}
	f := setupWorkFeedbackFixture(t)
	if err := os.MkdirAll(destination, 0700); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{"memory.jsonl": kanbanApp.memory.path, "users.json": os.Getenv("BONFIRE_USERS_PATH"), "sessions.json": os.Getenv("BONFIRE_SESSIONS_PATH")} {
		raw, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(destination, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(map[string]any{"rootId": f.root.ID, "project": f.detail, "cookies": f.cookies})
	if err := os.WriteFile(filepath.Join(destination, "fixture.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("synthetic browser fixture exported to " + destination)
}
