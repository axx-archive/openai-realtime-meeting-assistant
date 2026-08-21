package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func seedScoutTerminalProjection(t *testing.T, app *kanbanBoardApp, status string, metadata map[string]string) (scoutChatThreadRecord, meetingMemoryEntry, string) {
	t.Helper()
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "terminal projection", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	agentThreadID := "agent-thread-research-terminal"
	base := map[string]string{
		"mode":         "research",
		"threadId":     agentThreadID,
		"threadStatus": status,
		"status":       status,
		"originKind":   agentThreadOriginChannel,
		"originId":     thread.ID,
	}
	for key, value := range metadata {
		base[key] = value
	}
	artifact, _, err := app.createOSArtifactWithMetadata("research", "current market evidence", "# Private report body\n\nPrior version claimed 91 sources.", "AJ", base)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{
		ID:        "scout-chat-message-terminal-work",
		Kind:      "thread",
		Role:      "scout",
		Text:      "research workstream confirmed — running now",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Thread: &scoutChatThreadRef{
			ID:          agentThreadID,
			Mode:        "research",
			Query:       "current market evidence",
			Status:      "running",
			ArtifactID:  artifact.ID,
			AgentID:     "research-coworker-1",
			AgentName:   "AJA",
			DelegatedBy: "AJ",
		},
		ReplyTo: &scoutChatReplyRef{MessageID: "scout-chat-message-root"},
	}
	unrelated := scoutChatMessageRecord{ID: "scout-chat-message-unrelated", Kind: "message", Role: "user", Text: "leave this exact message alone", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if _, err := app.commitScoutChatThreadMessages(thread.OwnerEmail, thread.ID, unrelated, message); err != nil {
		t.Fatal(err)
	}
	return thread, artifact, agentThreadID
}

func TestScoutTerminalProjectionUsesCurrentMetadataAndIsRestartStable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", dir+"/memory.jsonl")
	t.Setenv("KANBAN_BOARD_PATH", dir+"/board.json")
	app := newKanbanBoardApp()
	thread, artifact, agentThreadID := seedScoutTerminalProjection(t, app, "running", map[string]string{
		"researchCitationCount":      "3",
		"researchSourceDomainCount":  "2",
		"researchQualityGate":        "passed",
		"researchSourceWindowDigest": strings.Repeat("a", 64),
		"threadRuns":                 `[{"researchCitationCount":"91"}]`,
	})

	app.updateScoutChatThreadRefs(agentThreadID, "running", artifact.ID)
	running, _, err := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := running.Messages[1].Text; got != "Research in progress" {
		t.Fatalf("running text=%q, want bounded active copy", got)
	}
	if running.Preview != "Research in progress" {
		t.Fatalf("running preview=%q", running.Preview)
	}

	artifact, _, err = app.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "", map[string]string{
		"threadStatus":               "complete",
		"status":                     "complete",
		"researchCitationCount":      "12",
		"researchSourceDomainCount":  "10",
		"researchQualityGate":        "passed",
		"researchSourceWindowDigest": strings.Repeat("b", 64),
		"threadRuns":                 `[{"researchCitationCount":"91"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	app.updateScoutChatThreadRefs(agentThreadID, "complete", artifact.ID)
	completed, _, err := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantTerminal := "Research delivered · 12 cited source links · 10 domains"
	if got := completed.Messages[1].Text; got != wantTerminal {
		t.Fatalf("terminal text=%q, want exact current metadata count", got)
	}
	if got := completed.Preview; got != wantTerminal {
		t.Fatalf("terminal preview=%q", got)
	}
	payload := app.scoutChatThreadUpdatePayload(thread.OwnerEmail, completed, completed.Messages[1])
	if got, _ := payload["preview"].(string); got != completed.Preview {
		t.Fatalf("event preview=%q, want %q", got, completed.Preview)
	}
	if got := completed.Messages[0].Text; got != "leave this exact message alone" {
		t.Fatalf("unrelated message changed to %q", got)
	}
	if ref := completed.Messages[1].Thread; ref.AgentID != "research-coworker-1" || ref.AgentName != "AJA" || ref.DelegatedBy != "AJ" || completed.Messages[1].ReplyTo == nil || completed.Messages[1].ReplyTo.MessageID != "scout-chat-message-root" {
		t.Fatalf("terminal projection changed delegated identity/topology: message=%+v", completed.Messages[1])
	}
	updatedAt := completed.UpdatedAt
	beforeStale, _ := canonicalJSON(completed)
	app.updateScoutChatThreadRefs(agentThreadID, "running", artifact.ID)
	afterStale, _, _ := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	afterStaleJSON, _ := canonicalJSON(afterStale)
	if string(beforeStale) != string(afterStaleJSON) {
		t.Fatal("stale running callback regressed the terminal thread")
	}
	app.updateScoutChatThreadRefs(agentThreadID, "complete", artifact.ID)
	replayed, _, _ := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if replayed.UpdatedAt != updatedAt || replayed.Messages[1].Text != completed.Messages[1].Text || replayed.Preview != completed.Preview {
		t.Fatalf("replay mutated terminal projection: before=%q/%q/%q after=%q/%q/%q", updatedAt, completed.Messages[1].Text, completed.Preview, replayed.UpdatedAt, replayed.Messages[1].Text, replayed.Preview)
	}

	restarted := newKanbanBoardApp()
	afterRestart, _, err := restarted.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterRestart.Messages[1].Text != completed.Messages[1].Text || afterRestart.Messages[0].Text != completed.Messages[0].Text || afterRestart.Preview != completed.Preview {
		t.Fatalf("restart projection drifted: %#v", afterRestart.Messages)
	}
}

func TestScoutChatViewerProjectionNamesConcreteDeckResultWithoutChangingGoalIdentity(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	goal, _, err := app.createOSArtifactWithMetadata("workflow", "Like A Farmer work", "goal record", "AJ", map[string]string{
		"mode": "goal", "status": "approval_required", "threadStatus": "approval_required",
	})
	if err != nil {
		t.Fatal(err)
	}
	deck, _, err := app.createOSArtifactWithMetadata("workflow", "Like A Farmer deck", "<!doctype html><html><body><section class=\"pg\">deck</section></body></html>", "AJ", map[string]string{
		"type": artifactTypeHTMLDeck, "source": "packaging_studio_ship", "goalId": goal.ID, "artifactContract": packagingStudioDeckContract,
	})
	if err != nil {
		t.Fatal(err)
	}
	finalDeck, _, err := app.createOSArtifactWithMetadata("workflow", "Like A Farmer — Optimization Insights", "<!doctype html><html><body><section class=\"pg\">edited deck</section></body></html>", "AJ", map[string]string{
		"type": artifactTypeHTMLDeck, "source": "scout_thread", "goalParentId": goal.ID, "status": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	if deck.ID == finalDeck.ID {
		t.Fatal("final edited deck must be a distinct durable result")
	}
	thread := scoutChatThreadRecord{Messages: []scoutChatMessageRecord{{
		ID: "goal-card", Kind: "thread", Role: "scout", Thread: &scoutChatThreadRef{ID: "run-1", Mode: "goal", Status: "approval_required", ArtifactID: goal.ID},
	}}}
	projected := app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	ref := projected.Messages[0].Thread
	if ref.ArtifactID != goal.ID || ref.ResultArtifactID != finalDeck.ID || ref.ResultArtifactType != artifactTypeHTMLDeck || ref.ResultTitle != "Like A Farmer — Optimization Insights" {
		t.Fatalf("projected ref=%+v, want lifecycle goal plus explicit deck result", ref)
	}
	if thread.Messages[0].Thread.ResultArtifactID != "" {
		t.Fatal("read projection mutated persisted thread")
	}

	other, _, err := app.createOSArtifactWithMetadata("workflow", "Wrong goal deck", "<!doctype html><html><body>wrong</body></html>", "AJ", map[string]string{
		"type": artifactTypeHTMLDeck, "source": "packaging_studio_ship", "goalId": "another-goal", "artifactContract": packagingStudioDeckContract,
	})
	if err != nil || other.ID == "" {
		t.Fatal(err)
	}
	projected = app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	if got := projected.Messages[0].Thread.ResultArtifactID; got != finalDeck.ID {
		t.Fatalf("cross-goal sibling changed result to %q", got)
	}
}

func TestScoutTerminalProjectionRetryRunningReplacesDeliveredPreviewAndRestarts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", dir+"/memory.jsonl")
	t.Setenv("KANBAN_BOARD_PATH", dir+"/board.json")
	app := newKanbanBoardApp()
	thread, artifact, agentThreadID := seedScoutTerminalProjection(t, app, "complete", map[string]string{
		"researchCitationCount":      "4",
		"researchSourceDomainCount":  "3",
		"researchQualityGate":        "passed",
		"researchSourceWindowDigest": strings.Repeat("e", 64),
	})
	app.updateScoutChatThreadRefs(agentThreadID, "complete", artifact.ID)
	delivered, _, err := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil || !strings.Contains(delivered.Preview, "Research delivered") {
		t.Fatalf("initial delivery thread=%+v err=%v", delivered, err)
	}

	artifact, _, err = app.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "", map[string]string{
		"threadStatus": "running", "status": "running", "progressNote": "provider-private progress must not project",
	})
	if err != nil {
		t.Fatal(err)
	}
	app.updateScoutChatThreadRefs(agentThreadID, "running", artifact.ID)
	running, _, err := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if running.Messages[1].Text != "Research in progress" || running.Preview != "Research in progress" || strings.Contains(strings.ToLower(running.Messages[1].Text+running.Preview), "delivered") {
		t.Fatalf("running retry retained terminal projection: text=%q preview=%q", running.Messages[1].Text, running.Preview)
	}
	payload := app.scoutChatThreadUpdatePayload(thread.OwnerEmail, running, running.Messages[1])
	if got, _ := payload["preview"].(string); got != "Research in progress" {
		t.Fatalf("running event preview=%q", got)
	}
	restarted := newKanbanBoardApp()
	afterRestart, _, err := restarted.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil || afterRestart.Messages[1].Text != "Research in progress" || afterRestart.Preview != "Research in progress" {
		t.Fatalf("running restart projection=%+v err=%v", afterRestart, err)
	}

	artifact, _, err = restarted.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "", map[string]string{
		"threadStatus": "complete", "status": "complete", "progressNote": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	restarted.updateScoutChatThreadRefs(agentThreadID, "complete", artifact.ID)
	redelivered, _, err := restarted.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil || redelivered.Messages[1].Text != "Research delivered · 4 cited source links · 3 domains" || redelivered.Preview != redelivered.Messages[1].Text {
		t.Fatalf("retry completion projection=%+v err=%v", redelivered, err)
	}
}

func TestScoutTerminalProjectionRejectsStaleBindingAndContainsFailureBody(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread, artifact, agentThreadID := seedScoutTerminalProjection(t, app, "error", map[string]string{
		"error": "provider secret raw failure: sk-do-not-project",
	})

	other, _, err := app.createOSArtifactWithMetadata("research", "other", "other private body", "AJ", map[string]string{
		"mode": "research", "threadId": "agent-thread-other", "threadStatus": "complete", "status": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	app.updateScoutChatThreadRefs(agentThreadID, "complete", other.ID)
	stale, _, err := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Messages[1].Text != "research workstream confirmed — running now" {
		t.Fatalf("mismatched artifact changed text to %q", stale.Messages[1].Text)
	}
	// The first legacy ref update now points at the supplied artifact. A replay
	// therefore has an exact artifact ID but must still reject its mismatched
	// current thread generation.
	app.updateScoutChatThreadRefs(agentThreadID, "complete", other.ID)
	stale, _, err = app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Messages[1].Text != "research workstream confirmed — running now" {
		t.Fatalf("mismatched artifact thread changed text to %q", stale.Messages[1].Text)
	}

	// Restore the exact message binding and project the authenticated terminal
	// error postimage. Raw artifact/provider error material must stay behind the
	// artifact read boundary.
	stale.Messages[1].Thread.ArtifactID = artifact.ID
	stale.Messages[1].Thread.Status = "running"
	if err := app.saveScoutChatThread(stale); err != nil {
		t.Fatal(err)
	}
	app.updateScoutChatThreadRefs(agentThreadID, "error", artifact.ID)
	failed, _, err := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := failed.Messages[1].Text; got != "Research needs attention" {
		t.Fatalf("failure text=%q", got)
	}
	serialized := failed.Messages[1].Text
	for _, forbidden := range []string{"provider", "secret", "sk-do-not-project", artifact.Text} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("failure projection leaked %q in %q", forbidden, serialized)
		}
	}
}

func TestScoutTerminalProjectionOmitsUnverifiedResearchCounts(t *testing.T) {
	artifact := meetingMemoryEntry{ID: "artifact-unverified", Metadata: map[string]string{
		"mode": "research", "threadId": "thread-unverified", "threadStatus": "complete", "status": "complete",
		"researchCitationCount": "91", "researchSourceDomainCount": "88",
	}}
	if got, ok := scoutChatTerminalWorkCopy(artifact, "thread-unverified", "complete"); !ok || got != "Research delivered" {
		t.Fatalf("unverified counts entered terminal copy: %q %v", got, ok)
	}
	artifact.Metadata["researchQualityGate"] = "passed"
	artifact.Metadata["researchSourceWindowDigest"] = "not-a-managed-receipt"
	if got, ok := scoutChatTerminalWorkCopy(artifact, "thread-unverified", "complete"); !ok || got != "Research delivered" {
		t.Fatalf("malformed receipt entered terminal copy: %q %v", got, ok)
	}
}

func TestScoutTerminalProjectionReconcilesLegacyCompletedRefAfterRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", dir+"/memory.jsonl")
	t.Setenv("KANBAN_BOARD_PATH", dir+"/board.json")
	app := newKanbanBoardApp()
	thread, artifact, agentThreadID := seedScoutTerminalProjection(t, app, "complete", map[string]string{
		"researchCitationCount":      "7",
		"researchSourceDomainCount":  "6",
		"researchQualityGate":        "passed",
		"researchSourceWindowDigest": strings.Repeat("c", 64),
	})

	// Model the live legacy postimage: the durable ref is already terminal,
	// while its work-card text and list preview still contain launch copy.
	legacy, _, err := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Messages[1].Thread.Status = "complete"
	legacy.Preview = "research workstream confirmed — running now"
	if err := app.saveScoutChatThread(legacy); err != nil {
		t.Fatal(err)
	}

	restarted := newKanbanBoardApp()
	restarted.updateScoutChatThreadRefs(agentThreadID, "complete", artifact.ID)
	reconciled, _, err := restarted.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := "Research delivered · 7 cited source links · 6 domains"
	if reconciled.Messages[1].Text != want || reconciled.Preview != want {
		t.Fatalf("legacy terminal projection not reconciled: text=%q preview=%q", reconciled.Messages[1].Text, reconciled.Preview)
	}
	updatedAt := reconciled.UpdatedAt
	restarted.updateScoutChatThreadRefs(agentThreadID, "complete", artifact.ID)
	replayed, _, err := restarted.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.UpdatedAt != updatedAt || replayed.Messages[1].Text != want || replayed.Preview != want {
		t.Fatalf("legacy reconciliation was not idempotent: %#v", replayed)
	}
}

func TestScoutTerminalProjectionRegisteredAdminReconciliationIsExactAndIdempotent(t *testing.T) {
	setupAuthTestEnv(t)
	adminCookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	memberCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", dir+"/memory.jsonl")
	t.Setenv("KANBAN_BOARD_PATH", dir+"/board.json")
	previousApp := kanbanApp
	kanbanApp = newKanbanBoardApp()
	t.Cleanup(func() { kanbanApp = previousApp })
	thread, artifact, _ := seedScoutTerminalProjection(t, kanbanApp, "complete", map[string]string{
		"researchCitationCount":      "9",
		"researchSourceDomainCount":  "8",
		"researchQualityGate":        "passed",
		"researchSourceWindowDigest": strings.Repeat("d", 64),
	})
	legacy, _, err := kanbanApp.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Messages[1].Thread.Status = "complete"
	legacy.Preview = "research workstream confirmed — running now"
	if err := kanbanApp.saveScoutChatThread(legacy); err != nil {
		t.Fatal(err)
	}
	kanbanApp = newKanbanBoardApp()

	reconcileAs := func(cookies []*http.Cookie, body string) *httptest.ResponseRecorder {
		t.Helper()
		path := "/assistant/chat-threads/" + thread.ID + "/messages/scout-chat-message-terminal-work/reconcile-terminal"
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantChatThreadHandler(recorder, request)
		return recorder
	}
	header := artifactAuthorizationHeaderFromEntry(artifact)
	body := fmt.Sprintf(`{"artifactId":%q,"expectedArtifactVersion":%d,"expectedContentDigest":%q,"expectedStatus":"complete"}`, artifact.ID, header.ContentRevision, header.ContentDigest)
	if recorder := reconcileAs(memberCookies, body); recorder.Code != http.StatusForbidden {
		t.Fatalf("member reconciliation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := reconcileAs(adminCookies, strings.TrimSuffix(body, "}")+`,"status":"complete"}`); recorder.Code != http.StatusBadRequest {
		t.Fatalf("client-supplied authority status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := reconcileAs(adminCookies, `{"artifactId":"stale-artifact","expectedArtifactVersion":1,"expectedContentDigest":"`+strings.Repeat("a", 64)+`","expectedStatus":"complete"}`); recorder.Code != http.StatusNotFound {
		t.Fatalf("stale reconciliation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	// Force a metadata-only artifact revision between the admitted snapshot and
	// the final chat save. The atomic store revalidation must refuse the stale
	// projection and leave both message text and list preview untouched.
	scoutTerminalProjectionBeforeSaveProbe = func() {
		scoutTerminalProjectionBeforeSaveProbe = nil
		if _, _, updateErr := kanbanApp.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "", map[string]string{"progressNote": "newer current postimage"}); updateErr != nil {
			t.Errorf("interleaved artifact update: %v", updateErr)
		}
	}
	t.Cleanup(func() { scoutTerminalProjectionBeforeSaveProbe = nil })
	if recorder := reconcileAs(adminCookies, body); recorder.Code != http.StatusNotFound {
		t.Fatalf("interleaved stale reconciliation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	stillLegacy, _, err := kanbanApp.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil || stillLegacy.Preview != "research workstream confirmed — running now" || stillLegacy.Messages[1].Text != "research workstream confirmed — running now" {
		t.Fatalf("interleaved stale reconciliation mutated thread=%+v err=%v", stillLegacy, err)
	}
	recorder := reconcileAs(adminCookies, body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("admin reconciliation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	response := struct {
		OK         bool                   `json:"ok"`
		Reconciled bool                   `json:"reconciled"`
		Thread     scoutChatThreadRecord  `json:"thread"`
		Message    scoutChatMessageRecord `json:"message"`
	}{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	want := "Research delivered · 9 cited source links · 8 domains"
	if !response.OK || !response.Reconciled || response.Thread.Preview != want || response.Message.Text != want || response.Message.Thread == nil || response.Message.Thread.Status != "complete" {
		t.Fatalf("reconciliation response=%+v", response)
	}
	replay := reconcileAs(adminCookies, body)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	replayResponse := struct {
		Reconciled bool `json:"reconciled"`
	}{}
	if err := json.Unmarshal(replay.Body.Bytes(), &replayResponse); err != nil {
		t.Fatal(err)
	}
	if replayResponse.Reconciled {
		t.Fatal("exact replay reported a second mutation")
	}
	persisted, _, err := kanbanApp.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil || persisted.Preview != want || persisted.Messages[1].Text != want {
		t.Fatalf("durable reconciliation thread=%+v err=%v", persisted, err)
	}
}
