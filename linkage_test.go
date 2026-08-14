package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func createLinkageTestCard(t *testing.T, app *kanbanBoardApp, title string) kanbanCard {
	t.Helper()
	result, changed, err := app.applyRetiredMeetingBoardToolCallArgs("create_ticket", map[string]any{
		"title": title,
	})
	if err != nil || !changed {
		t.Fatalf("create_ticket %q: changed=%v err=%v", title, changed, err)
	}
	card, ok := result["card"].(kanbanCard)
	if !ok {
		t.Fatalf("create_ticket result card=%#v, want kanbanCard", result["card"])
	}
	return card
}

func linkageCardStatus(t *testing.T, app *kanbanBoardApp, cardID string) kanbanStatus {
	t.Helper()
	for _, card := range app.snapshotState().Cards {
		if card.ID == cardID {
			return card.Status
		}
	}
	t.Fatalf("card %q not found on board", cardID)
	return ""
}

// matchBoardCard: an explicit id wins (any status) and never falls back to
// fuzzy; titles bind by token overlap or containment; near-tied candidates
// are ambiguous and bind nothing (a wrong auto-move is worse than none).
func TestMatchBoardCard(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	card := createLinkageTestCard(t, app, "Rodeo creator landscape brief")

	// explicit id: exact hit, stale id fails outright.
	if matched, ok := app.matchBoardCard("totally unrelated", card.ID); !ok || matched.ID != card.ID {
		t.Fatalf("explicit card_id match=%v ok=%v, want %q", matched.ID, ok, card.ID)
	}
	if _, ok := app.matchBoardCard("Rodeo creator landscape brief", "card-does-not-exist"); ok {
		t.Fatal("a stale explicit card_id must fail, never fuzzy-fallback")
	}

	// fuzzy: the proposal title binds to the matching card.
	if matched, ok := app.matchBoardCard("Rodeo creator landscape brief", ""); !ok || matched.ID != card.ID {
		t.Fatalf("fuzzy match=%v ok=%v, want %q", matched.ID, ok, card.ID)
	}
	// containment also binds.
	if matched, ok := app.matchBoardCard("Research the rodeo creator landscape brief for Q3", ""); !ok || matched.ID != card.ID {
		t.Fatalf("containment match=%v ok=%v, want %q", matched.ID, ok, card.ID)
	}
	// unrelated titles bind nothing.
	if _, ok := app.matchBoardCard("Zanzibar pricing teardown", ""); ok {
		t.Fatal("unrelated title must not bind a card")
	}

	// ambiguity: two near-identical candidates bind nothing.
	createLinkageTestCard(t, app, "Rodeo creator landscape memo")
	if _, ok := app.matchBoardCard("Rodeo creator landscape", ""); ok {
		t.Fatal("near-tied candidates must be ambiguous, not a coin flip")
	}

	// Done cards never fuzzy-match (finished work is not a link target)...
	doneCard := createLinkageTestCard(t, app, "Coyote channel audit")
	if _, changed, err := app.applyRetiredMeetingBoardToolCallArgs("move_ticket", map[string]any{"card_id": doneCard.ID, "status": string(kanbanStatusDone)}); err != nil || !changed {
		t.Fatalf("move to done: changed=%v err=%v", changed, err)
	}
	if _, ok := app.matchBoardCard("Coyote channel audit", ""); ok {
		t.Fatal("Done cards must not fuzzy-match")
	}
	// ...but the explicit id still resolves them.
	if matched, ok := app.matchBoardCard("", doneCard.ID); !ok || matched.ID != doneCard.ID {
		t.Fatalf("explicit id must resolve Done cards too, got ok=%v", ok)
	}
}

// Historical board links are immutable after retirement.
func TestAdvanceLinkedCardPreservesRetiredBoardHistory(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	card := createLinkageTestCard(t, app, "Nimbus launch checklist")

	app.advanceLinkedCard(card.ID, kanbanStatusInProgress, "confirmed: Nimbus launch checklist")
	if status := linkageCardStatus(t, app, card.ID); status != kanbanStatusBacklog {
		t.Fatalf("status=%q, want archived Backlog unchanged", status)
	}
	// retry: already there, no error, no change.
	app.advanceLinkedCard(card.ID, kanbanStatusInProgress, "retry")
	if status := linkageCardStatus(t, app, card.ID); status != kanbanStatusBacklog {
		t.Fatalf("status=%q after retry, want archived Backlog unchanged", status)
	}
	// unknown card id: logged and swallowed.
	app.advanceLinkedCard("card-missing", kanbanStatusDone, "gone")
	// empty id: no-op.
	app.advanceLinkedCard("", kanbanStatusDone, "empty")
}

func TestRunAgentThreadCompleteDoesNotDiscoverOrMutateRetiredBoard(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	card := createLinkageTestCard(t, app, "Rodeo creator landscape brief")

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	originalResponder := createOpenAITextResponse
	defer func() { createOpenAITextResponse = originalResponder }()
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		return completeResearchArtifactForTest(), nil
	}

	thread, err := app.launchAgentThread("research", "Rodeo creator landscape brief", "AJ")
	if err != nil {
		t.Fatalf("launchAgentThread: %v", err)
	}
	app.runAgentThread(thread)

	if status := linkageCardStatus(t, app, card.ID); status != kanbanStatusBacklog {
		t.Fatalf("status=%q, want archived Backlog after completion", status)
	}
	artifact, found := app.osArtifactByID(thread.Artifact.ID)
	if !found {
		t.Fatalf("artifact %q not found", thread.Artifact.ID)
	}
	if artifact.Metadata["boardCardId"] != "" {
		t.Fatalf("boardCardId=%q, want no new Board linkage", artifact.Metadata["boardCardId"])
	}
}

func TestRunAgentThreadErrorDoesNotMutateRetiredBoard(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	card := createLinkageTestCard(t, app, "Zanzibar pricing teardown")

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	originalResponder := createOpenAITextResponse
	defer func() { createOpenAITextResponse = originalResponder }()
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		return "", context.DeadlineExceeded
	}

	thread, err := app.launchAgentThread("research", "Zanzibar pricing teardown", "AJ")
	if err != nil {
		t.Fatalf("launchAgentThread: %v", err)
	}
	app.runAgentThread(thread)

	if status := linkageCardStatus(t, app, card.ID); status != kanbanStatusBacklog {
		t.Fatalf("status=%q, want archived Backlog after worker error", status)
	}
}

func TestCodexCallbackPreservesRetiredBoardAndRetriesAreNoops(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	defer func() { kanbanApp = previousApp }()
	t.Setenv("BONFIRE_RUNNER_TOKEN", "runner-secret")

	card := createLinkageTestCard(t, app, "Coyote channel audit")
	artifact, _, err := app.createOSArtifactWithMetadata("workflow", "Coyote channel audit", "queued", "tester", map[string]string{
		"title":        "Coyote channel audit",
		"threadStatus": codexJobStatusQueued,
		"boardCardId":  card.ID,
		"threadId":     "agent-thread-linkage",
		"runnerJobId":  "codex-job-linkage",
	})
	if err != nil {
		t.Fatalf("createOSArtifactWithMetadata: %v", err)
	}

	post := func() *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(signedCodexCallbackPayload("runner-secret", codexRunnerCallbackPayload{
			JobID:      "codex-job-linkage",
			ArtifactID: artifact.ID,
			ThreadID:   "agent-thread-linkage",
			Status:     codexJobStatusComplete,
			Text:       "Vision: audit finished\n\n## Codex worker evidence\n- Worker: codex exec",
		}))
		if err != nil {
			t.Fatalf("marshal callback: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/internal/codex/jobs/result", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer runner-secret")
		recorder := httptest.NewRecorder()
		internalCodexRunnerResultHandler(recorder, req)
		return recorder
	}

	if recorder := post(); recorder.Code != http.StatusOK {
		t.Fatalf("callback status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if status := linkageCardStatus(t, app, card.ID); status != kanbanStatusBacklog {
		t.Fatalf("status=%q, want archived Backlog after complete callback", status)
	}

	// A retried identical callback still cannot alter archived history.
	if recorder := post(); recorder.Code != http.StatusOK {
		t.Fatalf("retried callback status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if status := linkageCardStatus(t, app, card.ID); status != kanbanStatusBacklog {
		t.Fatalf("status=%q after retried callback, want the human's Backlog preserved", status)
	}
}

// A failed callback and stale legacy link never mutate Board history.
func TestCodexCallbackFailureAndMissingCardPreserveRetiredBoard(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	defer func() { kanbanApp = previousApp }()
	t.Setenv("BONFIRE_RUNNER_TOKEN", "runner-secret")

	card := createLinkageTestCard(t, app, "Nimbus deck rework")
	failing, _, err := app.createOSArtifactWithMetadata("workflow", "Nimbus deck rework", "queued", "tester", map[string]string{
		"title":        "Nimbus deck rework",
		"threadStatus": codexJobStatusQueued,
		"boardCardId":  card.ID,
		"threadId":     "agent-thread-failed",
		"runnerJobId":  "codex-job-failed",
	})
	if err != nil {
		t.Fatalf("createOSArtifactWithMetadata: %v", err)
	}
	postResult := func(artifactID string, status string) *httptest.ResponseRecorder {
		t.Helper()
		artifact, _ := app.osArtifactByID(artifactID)
		payload := signedCodexCallbackPayload("runner-secret", codexRunnerCallbackPayload{
			JobID:      "codex-job-" + status,
			ArtifactID: artifactID,
			ThreadID:   artifact.Metadata["threadId"],
			Status:     status,
			Text:       "worker report",
		})
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal callback: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/internal/codex/jobs/result", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer runner-secret")
		recorder := httptest.NewRecorder()
		internalCodexRunnerResultHandler(recorder, req)
		return recorder
	}

	if recorder := postResult(failing.ID, codexJobStatusFailed); recorder.Code != http.StatusOK {
		t.Fatalf("failed callback status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if status := linkageCardStatus(t, app, card.ID); status != kanbanStatusBacklog {
		t.Fatalf("status=%q, want archived Backlog after failed callback", status)
	}

	// stale link: the card is deleted before the artifact lands.
	orphan, _, err := app.createOSArtifactWithMetadata("workflow", "Orphaned deliverable", "queued", "tester", map[string]string{
		"title":        "Orphaned deliverable",
		"threadStatus": codexJobStatusQueued,
		"boardCardId":  "card-deleted-long-ago",
		"threadId":     "agent-thread-complete",
		"runnerJobId":  "codex-job-complete",
	})
	if err != nil {
		t.Fatalf("create orphan artifact: %v", err)
	}
	if recorder := postResult(orphan.ID, codexJobStatusComplete); recorder.Code != http.StatusOK {
		t.Fatalf("orphan callback status=%d body=%s, want the missing card swallowed", recorder.Code, recorder.Body.String())
	}
}

// New proposals ignore legacy Board identifiers and remain conversation-backed.
func TestProposalIgnoresRetiredBoardLinkage(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	var launched scoutAgentThread
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, thread scoutAgentThread) { launched = thread }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	card := createLinkageTestCard(t, app, "Rodeo creator landscape brief")

	// Legacy card_id from an older client is ignored.
	result, _, err := app.applyToolCallArgs("propose_codex_task", map[string]any{
		"title":   "Something entirely different",
		"mode":    "research",
		"query":   "Research the rodeo creator landscape and draft a brief.",
		"card_id": card.ID,
	})
	if err != nil {
		t.Fatalf("propose with card_id: %v", err)
	}
	explicit := result["proposal"].(map[string]any)
	if _, ok := explicit["cardId"]; ok {
		t.Fatalf("explicit proposal unexpectedly retained retired cardId=%v", explicit["cardId"])
	}

	// Matching titles also do not create a new Board association.
	result, _, err = app.applyToolCallArgs("propose_codex_task", map[string]any{
		"title": "Rodeo creator landscape brief",
		"mode":  "research",
		"query": "Research the rodeo creator landscape and draft a brief.",
	})
	if err != nil {
		t.Fatalf("propose fuzzy: %v", err)
	}
	fuzzy := result["proposal"].(map[string]any)
	if _, ok := fuzzy["cardId"]; ok {
		t.Fatalf("fuzzy proposal unexpectedly linked retired cardId=%v", fuzzy["cardId"])
	}

	// ambiguous titles do NOT link.
	createLinkageTestCard(t, app, "Rodeo creator landscape memo")
	result, _, err = app.applyToolCallArgs("propose_codex_task", map[string]any{
		"title": "Rodeo creator landscape",
		"mode":  "research",
		"query": "Research the rodeo creator landscape and draft a brief.",
	})
	if err != nil {
		t.Fatalf("propose ambiguous: %v", err)
	}
	if cardID, ok := result["proposal"].(map[string]any)["cardId"]; ok {
		t.Fatalf("ambiguous proposal cardId=%v, want unlinked", cardID)
	}

	// Confirm launches Work while archived Board history remains unchanged.
	proposalID := asString(fuzzy["id"])
	if _, _, err := app.resolveCodexProposal(proposalID, "confirm", "Tom", "tom@shareability.com"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if status := linkageCardStatus(t, app, card.ID); status != kanbanStatusBacklog {
		t.Fatalf("status=%q after confirm, want archived Backlog", status)
	}
	artifact, found := app.osArtifactByID(launched.Artifact.ID)
	if !found {
		t.Fatalf("launched artifact %q not found", launched.Artifact.ID)
	}
	if artifact.Metadata["boardCardId"] != "" || artifact.Metadata["proposalId"] != proposalID {
		t.Fatalf("artifact linkage=%v/%v, want no boardCardId and proposalId=%q", artifact.Metadata["boardCardId"], artifact.Metadata["proposalId"], proposalID)
	}
}

func TestProposalConfirmDoesNotLinkLateRetiredBoardCard(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	result, _, err := app.applyToolCallArgs("propose_codex_task", map[string]any{
		"title": "Falcon merch drop plan",
		"mode":  "workflow",
		"query": "Plan the falcon merch drop end to end.",
	})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	proposal := result["proposal"].(map[string]any)
	if _, ok := proposal["cardId"]; ok {
		t.Fatalf("cardId=%v before the card exists, want unlinked", proposal["cardId"])
	}

	// the card lands after the proposal.
	card := createLinkageTestCard(t, app, "Falcon merch drop plan")

	proposalID := asString(proposal["id"])
	payload, _, err := app.resolveCodexProposal(proposalID, "confirm", "Tom", "tom@shareability.com")
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if _, ok := payload["cardId"]; ok {
		t.Fatalf("confirmed payload unexpectedly linked late retired card %q", card.ID)
	}
	if status := linkageCardStatus(t, app, card.ID); status != kanbanStatusBacklog {
		t.Fatalf("status=%q, want archived Backlog", status)
	}
}

// The propose→confirm→complete package flow: package_id resolves at propose
// time (by name), confirm copies packageId onto the thread artifact, and the
// terminal seam auto-attaches the COMPLETED artifact to its venture package.
func TestProposalWithPackageIdAutoAttachesCompletedArtifact(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	record, err := app.createVenturePackage("Nimbus creator platform", "creators need a home base", "AJ")
	if err != nil {
		t.Fatalf("createVenturePackage: %v", err)
	}

	var launched scoutAgentThread
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, thread scoutAgentThread) { launched = thread }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	originalResponder := createOpenAITextResponse
	defer func() { createOpenAITextResponse = originalResponder }()
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		return completeResearchArtifactForTest(), nil
	}

	// propose with the package name — findPackageByNameOrID resolves it.
	result, _, err := app.applyToolCallArgs("propose_codex_task", map[string]any{
		"title":      "Nimbus market scan",
		"mode":       "research",
		"query":      "Scan the creator platform market for Nimbus.",
		"package_id": "Nimbus creator platform",
	})
	if err != nil {
		t.Fatalf("propose with package_id: %v", err)
	}
	proposal := result["proposal"].(map[string]any)
	if proposal["packageId"] != record.ID {
		t.Fatalf("proposal packageId=%v, want %q resolved from the name", proposal["packageId"], record.ID)
	}

	// confirm: the launched thread artifact carries the packageId stamp.
	proposalID := asString(proposal["id"])
	if _, _, err := app.resolveCodexProposal(proposalID, "confirm", "Tom", "tom@shareability.com"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	stamped, found := app.osArtifactByID(launched.Artifact.ID)
	if !found || stamped.Metadata["packageId"] != record.ID || stamped.Metadata["proposalId"] != proposalID {
		t.Fatalf("artifact stamps=%v, want packageId=%q proposalId=%q", stamped.Metadata, record.ID, proposalID)
	}

	// no attachment until the deliverable actually exists.
	if before, _ := app.venturePackageByID(record.ID); len(before.ArtifactIDs) != 0 {
		t.Fatalf("artifactIds=%v before completion, want empty", before.ArtifactIDs)
	}

	// completion auto-attaches through the terminal seam.
	app.runAgentThread(launched)
	attached, _ := app.venturePackageByID(record.ID)
	if len(attached.ArtifactIDs) != 1 || attached.ArtifactIDs[0] != launched.Artifact.ID {
		t.Fatalf("artifactIds=%v, want the completed artifact auto-attached", attached.ArtifactIDs)
	}

	// a retried terminal callback stays idempotent.
	artifact, _ := app.osArtifactByID(launched.Artifact.ID)
	app.syncLinkedCardForArtifact(artifact, codexJobStatusComplete)
	retried, _ := app.venturePackageByID(record.ID)
	if len(retried.ArtifactIDs) != 1 {
		t.Fatalf("artifactIds=%v after retry, want still one", retried.ArtifactIDs)
	}
}

// A failed thread must NOT file its artifact into the binder — only completed
// deliverables attach.
func TestFailedThreadDoesNotAttachArtifactToPackage(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	record, err := app.createVenturePackage("Nimbus creator platform", "", "AJ")
	if err != nil {
		t.Fatalf("createVenturePackage: %v", err)
	}
	artifact, _, err := app.createOSArtifactWithMetadata("research", "Nimbus market scan", "queued", "tester", map[string]string{
		"packageId": record.ID,
	})
	if err != nil {
		t.Fatalf("createOSArtifactWithMetadata: %v", err)
	}
	app.syncLinkedCardForArtifact(artifact, codexJobStatusFailed)
	after, _ := app.venturePackageByID(record.ID)
	if len(after.ArtifactIDs) != 0 {
		t.Fatalf("artifactIds=%v after a failed run, want empty", after.ArtifactIDs)
	}
}

func TestProposeCodexTaskSchemaRetiresBoardLinkage(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	rawTools, err := json.Marshal(app.kanbanTools())
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	toolsJSON := string(rawTools)
	if strings.Contains(toolsJSON, `"card_id"`) {
		t.Fatal("live tool schema exposed retired card_id linkage")
	}
	// card_id stays optional: required is unchanged.
	if !strings.Contains(toolsJSON, `"required":["title","mode","query"]`) {
		t.Fatal("propose_codex_task required args must stay title/mode/query")
	}
}
