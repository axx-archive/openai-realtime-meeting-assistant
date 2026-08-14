package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectBoundResearchCarriesExactCurrentAssociationAndFailsClosed(t *testing.T) {
	setupAuthTestEnv(t)
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	previousRuntime := currentCanonicalRuntime()
	setCanonicalRuntime(&CanonicalRuntime{mode: CanonicalModeOff, postgres: store})
	t.Cleanup(func() { setCanonicalRuntime(previousRuntime) })

	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-openai-key"
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	snapshot := projectChatSnapshotFixture(t)
	snapshot.Session.Email = user.Email
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Project Research", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := store.confirmHomeProjectChatSend(ctx, snapshot,
		scoutChatThreadRecord{ID: "project_work_seed_thread", OwnerEmail: user.Email, Visibility: scoutChatVisibilityPrivate},
		"project_work_seed_message", "project-work-seed-operation", "Create Research Project", homeProjectContextToken{
			Kind: "create", ProjectTitle: "Research Project", Basis: "selected", Confidence: 1,
			OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID,
		})
	if err != nil {
		t.Fatal(err)
	}
	text := "Research the durable creator-economy evidence"
	messageID := "project_work_source_message"
	authority, err := store.projectChatSourceAuthorityForThread(ctx, snapshot, thread)
	if err != nil {
		t.Fatal(err)
	}
	link, err := store.confirmProjectChatSend(ctx, snapshot, thread, messageID, "project-work-source-operation", text, homeProjectContextToken{
		Kind: "project", ProjectID: seed.ProjectID, ProjectRevision: seed.ProjectRevision, ProjectDigest: seed.ProjectDigest,
		ProjectTitle: seed.ProjectTitle, Basis: "selected", Confidence: 1, OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID,
	}, authority)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{
		ID: messageID, Kind: "message", Role: "user", Text: text, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		AuthorName: user.Name, AuthorEmail: normalizeAccountEmail(user.Email),
		Project: &scoutChatProjectContext{Status: "confirmed", ContextRevision: 1, ProjectID: link.ProjectID,
			ProjectRevision: link.ProjectRevision, Title: link.ProjectTitle, Basis: "selected",
			AssociationID: link.AssociationID, AssociationRevision: link.AssociationRevision},
	}
	saved, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, message)
	if err != nil {
		t.Fatal(err)
	}
	message = saved.Messages[len(saved.Messages)-1]

	fence := &strideE10HeldTenantAuthorityFence{snapshot: snapshot}
	fence.active.Store(true)
	bound := strideE10ContextWithHeldTenantAuthority(context.Background(), fence)
	previousStarter := startAgentThreadAsync
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousStarter })
	response, err := app.startConversationPrivateWork(bound, user, saved, message, conversationWorkDecision{
		Kind: conversationWorkWorkstream, Mode: "research", Objective: text,
	}, "", proposalSourceChatRouter, func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		return app.commitScoutChatThreadMessages(user.Email, thread.ID, messages...)
	})
	if err != nil {
		t.Fatal(err)
	}
	launched, ok := response["agentThread"].(scoutAgentThread)
	if !ok || launched.Artifact.ID == "" {
		t.Fatalf("missing launched Research: %#v", response)
	}
	binding, boundProject := decodeProjectWorkBinding(launched.Artifact.Metadata)
	if !boundProject || binding.ProjectID != link.ProjectID || binding.ProjectDigest != link.ProjectDigest ||
		binding.AssociationID != link.AssociationID || binding.AssociationRevision != link.AssociationRevision ||
		binding.SourceEventID == "" || binding.SourceDigest == "" {
		t.Fatalf("Project work binding=%+v present=%v", binding, boundProject)
	}
	answer := response["answer"].(scoutChatMessageRecord)
	if answer.Thread == nil || answer.Thread.ProjectID != link.ProjectID || answer.Thread.ProjectTitle != link.ProjectTitle {
		t.Fatalf("work card Project projection=%+v", answer.Thread)
	}
	if _, err := app.agentThreadProviderContext(context.Background(), launched); err != nil {
		t.Fatalf("current Project work provider admission: %v", err)
	}
	completed, changed, err := app.updateOSArtifactWithMetadata(launched.Artifact.ID, "Creator evidence brief", "# Creator evidence brief\n\nVerified evidence.", user.Name, map[string]string{
		"status": "complete", "threadStatus": "complete", "goalStatus": "complete", "progressPercent": "100",
	})
	if err != nil || !changed {
		t.Fatalf("complete Project Research changed=%v err=%v", changed, err)
	}
	launched.Artifact = completed
	completedVersion := artifactVersion(completed)
	cookies := loginAs(t, user.Email, "B0NFIRE!")
	editBody, _ := json.Marshal(map[string]any{
		"id": completed.ID, "title": "Creator evidence brief — revised", "text": "# Creator evidence brief\n\nHuman revision with a sharper evidence standard.",
	})
	editedResponse := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts", string(editBody), cookies, artifactsHandler)
	if editedResponse.Code != http.StatusOK {
		t.Fatalf("Project-bound edit status=%d body=%s", editedResponse.Code, editedResponse.Body.String())
	}
	var editedPayload struct {
		Updated  bool               `json:"updated"`
		Artifact meetingMemoryEntry `json:"artifact"`
	}
	if json.Unmarshal(editedResponse.Body.Bytes(), &editedPayload) != nil || !editedPayload.Updated || artifactVersion(editedPayload.Artifact) != completedVersion+1 {
		t.Fatalf("Project-bound edit payload=%+v body=%s", editedPayload, editedResponse.Body.String())
	}
	if editedBinding, ok := decodeProjectWorkBinding(editedPayload.Artifact.Metadata); !ok || editedBinding != binding {
		t.Fatalf("Project-bound edit changed authority binding: %+v present=%v", editedBinding, ok)
	}
	var followUpRun agentThreadFollowUpRun
	previousFollowUpStarter := startAgentThreadFollowUpAsync
	startAgentThreadFollowUpAsync = func(_ *kanbanBoardApp, run agentThreadFollowUpRun) { followUpRun = run }
	t.Cleanup(func() { startAgentThreadFollowUpAsync = previousFollowUpStarter })
	followUp := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/threads/follow-up", `{"artifactId":"`+completed.ID+`","text":"Regenerate with the human revision and preserve the cited evidence."}`, cookies, assistantThreadFollowUpHandler)
	if followUp.Code != http.StatusAccepted || followUpRun.artifactID != completed.ID {
		t.Fatalf("Project-bound regenerate status=%d run=%+v body=%s", followUp.Code, followUpRun, followUp.Body.String())
	}
	app.runAgentThreadFollowUpWithResponder(followUpRun, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if !strings.Contains(request.Input, "Human revision with a sharper evidence standard") || !strings.Contains(request.Input, "Regenerate with the human revision") {
			t.Fatalf("Project-bound regenerate input omitted the evolving artifact/request: %s", request.Input)
		}
		return completeResearchArtifactForTest(), nil
	})
	regenerated, ok := app.osArtifactByID(completed.ID)
	if !ok || artifactVersion(regenerated) != completedVersion+2 || regenerated.Metadata["threadVersion"] != "2" || regenerated.Metadata["threadStatus"] != "complete" {
		t.Fatalf("Project-bound regenerated artifact=%+v present=%v", regenerated, ok)
	}
	if regeneratedBinding, present := decodeProjectWorkBinding(regenerated.Metadata); !present || regeneratedBinding != binding {
		t.Fatalf("Project-bound regenerate changed authority binding: %+v present=%v", regeneratedBinding, present)
	}
	currentThread, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	workRefs := 0
	for _, candidate := range currentThread.Messages {
		if candidate.Thread == nil || candidate.Thread.ArtifactID != regenerated.ID {
			continue
		}
		workRefs++
		if candidate.Thread.Status != "complete" || candidate.Thread.ProjectID != binding.ProjectID || candidate.Thread.ProjectTitle != binding.ProjectTitle {
			t.Fatalf("regenerated work card lost terminal Project truth: %+v", candidate.Thread)
		}
	}
	if workRefs != 1 {
		t.Fatalf("regenerate produced %d work cards, want one evolving Project-bound card", workRefs)
	}
	completed = regenerated
	opened := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/open", `{"id":"`+completed.ID+`"}`, cookies, artifactOpenHandler)
	if opened.Code != http.StatusOK {
		t.Fatalf("rich Open status=%d body=%s", opened.Code, opened.Body.String())
	}
	currentArtifact, ok := app.osArtifactByID(completed.ID)
	if !ok {
		t.Fatal("completed artifact disappeared after Open")
	}
	dispositionRef := artifactDispositionRefFromHeader(resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(currentArtifact)))
	dispositionStore, err := OpenArtifactDispositionStore(filepath.Join(t.TempDir(), "project-work-drive-receipts.jsonl"), true, artifactDiscardDefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	previousDriveStore := artifactDriveSaveStoreForRequest
	artifactDriveSaveStoreForRequest = func() (*ArtifactDispositionStore, error) { return dispositionStore, nil }
	t.Cleanup(func() { artifactDriveSaveStoreForRequest = previousDriveStore })
	saveBody, _ := json.Marshal(map[string]any{
		"operationId": "project-work-drive-save", "artifact": dispositionRef, "fileName": "Creator evidence brief", "folderId": "",
	})
	driveSave := artifactAuthorizationRequest(t, http.MethodPost, artifactDriveSavePath, string(saveBody), cookies, artifactDriveSaveHandler)
	if driveSave.Code != http.StatusOK {
		t.Fatalf("Drive save status=%d body=%s", driveSave.Code, driveSave.Body.String())
	}
	var savedReceipt struct {
		OK      bool                       `json:"ok"`
		Receipt ArtifactDispositionReceipt `json:"receipt"`
	}
	if json.Unmarshal(driveSave.Body.Bytes(), &savedReceipt) != nil || !savedReceipt.OK || savedReceipt.Receipt.Drive == nil ||
		savedReceipt.Receipt.OperationID != "project-work-drive-save" || savedReceipt.Receipt.Drive.ID != completed.ID ||
		savedReceipt.Receipt.Drive.Name != "Creator evidence brief" || savedReceipt.Receipt.Drive.SourceArtifactID != completed.ID {
		t.Fatalf("exact Drive receipt=%+v body=%s", savedReceipt, driveSave.Body.String())
	}

	operationDigest := strings.Repeat("d", 64)
	if _, err := store.invalidateProjectChatSourceForMutation(ctx, snapshot, saved, message, "project-work-source-edit", operationDigest, "edit"); err != nil {
		t.Fatal(err)
	}
	staleEditBody, _ := json.Marshal(map[string]any{
		"id": completed.ID, "title": "Must not land", "text": "This edit raced after the Project source changed.",
	})
	staleEdit := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts", string(staleEditBody), cookies, artifactsHandler)
	if staleEdit.Code != http.StatusNotFound {
		t.Fatalf("stale Project edit status=%d body=%s", staleEdit.Code, staleEdit.Body.String())
	}
	unchangedAfterStaleEdit, found := app.osArtifactByID(completed.ID)
	if !found || unchangedAfterStaleEdit.Text != completed.Text || unchangedAfterStaleEdit.Metadata["title"] != completed.Metadata["title"] {
		t.Fatalf("stale Project edit changed artifact: %+v present=%v", unchangedAfterStaleEdit, found)
	}
	if _, err := app.agentThreadProviderContext(context.Background(), launched); !errors.Is(err, ErrAgentThreadSourceChanged) {
		t.Fatalf("stale Project work provider err=%v", err)
	}
	effectCalls := 0
	if err := app.withCurrentAgentThreadSource(launched, func() error { effectCalls++; return nil }); !errors.Is(err, ErrAgentThreadSourceChanged) || effectCalls != 0 {
		t.Fatalf("stale Project terminal publication err=%v effects=%d", err, effectCalls)
	}
	read := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts?id="+url.QueryEscape(launched.Artifact.ID), "", cookies, artifactsHandler)
	if read.Code != http.StatusNotFound || strings.Contains(read.Body.String(), text) || strings.Contains(read.Body.String(), launched.Artifact.ID) {
		t.Fatalf("stale Project artifact status=%d body=%s", read.Code, read.Body.String())
	}
	staleFollowUp := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/threads/follow-up", `{"artifactId":"`+launched.Artifact.ID+`","text":"revise it"}`, cookies, assistantThreadFollowUpHandler)
	if staleFollowUp.Code != http.StatusNotFound {
		t.Fatalf("stale Project follow-up status=%d body=%s", staleFollowUp.Code, staleFollowUp.Body.String())
	}
	files := artifactAuthorizationRequest(t, http.MethodGet, "/assistant/files", "", cookies, assistantFilesHandler)
	if files.Code != http.StatusOK || strings.Contains(files.Body.String(), "Creator evidence brief") || strings.Contains(files.Body.String(), completed.ID) {
		t.Fatalf("stale Project Drive projection status=%d body=%s", files.Code, files.Body.String())
	}
}
