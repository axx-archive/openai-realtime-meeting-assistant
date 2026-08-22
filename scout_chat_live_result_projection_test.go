package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

type scoutChatLiveResultE10Authorizer struct {
	actions    []ACLAction
	allowWrite bool
}

func (a *scoutChatLiveResultE10Authorizer) AuthorizeArtifactHeader(context.Context, *userAccount, ACLAction, ArtifactAuthorizationHeader) bool {
	return false
}

func (a *scoutChatLiveResultE10Authorizer) AuthorizeArtifactHeaderForStridePrincipal(_ context.Context, _ StrideE10TenantPrincipal, action ACLAction, _ ArtifactAuthorizationHeader) bool {
	a.actions = append(a.actions, action)
	return action != ACLWrite || a.allowWrite
}

func seedScoutChatLiveDocumentResult(t *testing.T, app *kanbanBoardApp) (scoutChatThreadRecord, meetingMemoryEntry, string, string) {
	t.Helper()
	owner := "aj@shareability.com"
	thread, err := app.createScoutChatThread(owner, "AJ", "Live result projection", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	runID := "live-result-run"
	artifact, _, err := app.createOSArtifactWithMetadata("research", "Live report", "# Live report\n\nCurrent result body.", "Scout", map[string]string{
		"type": artifactTypeMarkdown, "source": "scout_thread", "threadId": runID,
		"status": "running", "threadStatus": "running", "originKind": agentThreadOriginPrivateThread,
		"originId": thread.ID, "visibility": scoutChatVisibilityPrivate, "ownerEmail": owner, "requestedBy": owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	messageID := "live-result-card"
	message := scoutChatMessageRecord{
		ID: messageID, Kind: "thread", Role: "scout", Text: "Research in progress",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Thread:    &scoutChatThreadRef{ID: runID, Mode: "research", Query: "Live report", Status: "running", ArtifactID: artifact.ID},
	}
	if _, err := app.commitScoutChatThreadMessages(owner, thread.ID, message); err != nil {
		t.Fatal(err)
	}
	thread, _, err = app.scoutChatThreadByID(owner, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	return thread, artifact, runID, messageID
}

func chatThreadPayloadMessage(t *testing.T, payload map[string]any) scoutChatMessageRecord {
	t.Helper()
	message, ok := payload["message"].(scoutChatMessageRecord)
	if !ok {
		t.Fatalf("chat_thread payload message=%T, want scoutChatMessageRecord", payload["message"])
	}
	return message
}

func TestScoutChatLiveWorkEventProjectsTerminalResultAndPreservesItOnRebroadcast(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread, artifact, runID, messageID := seedScoutChatLiveDocumentResult(t, app)
	running := chatThreadPayloadMessage(t, app.scoutChatThreadUpdatePayload(thread.OwnerEmail, thread, thread.Messages[0]))
	if running.Thread == nil || running.Thread.ResultArtifactID != "" {
		t.Fatalf("running event exposed a result: %+v", running.Thread)
	}

	artifact, _, err := app.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "Scout", map[string]string{
		"status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	app.updateScoutChatThreadRefs(runID, "complete", artifact.ID)
	thread, _, err = app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	messageIndex := scoutChatMessageIndex(thread, messageID)
	if messageIndex < 0 {
		t.Fatal("terminal work card is missing")
	}

	terminal := chatThreadPayloadMessage(t, app.scoutChatThreadUpdatePayload(thread.OwnerEmail, thread, thread.Messages[messageIndex]))
	if terminal.Thread == nil || terminal.Thread.ResultArtifactID != artifact.ID || terminal.Thread.ResultArtifactType != artifactTypeMarkdown || !terminal.Thread.ResultCanEdit {
		t.Fatalf("terminal event result=%+v, want exact editable document", terminal.Thread)
	}
	if terminal.Thread.ResultPreview != artifact.Text {
		t.Fatalf("terminal preview=%q, want exact current body %q", terminal.Thread.ResultPreview, artifact.Text)
	}

	// A later status replay starts from the durable card, which intentionally
	// does not persist Result*. The event projection must enrich it again rather
	// than replacing the client's rich card with the sparse durable record.
	rebroadcast := chatThreadPayloadMessage(t, app.scoutChatThreadUpdatePayload(thread.OwnerEmail, thread, thread.Messages[messageIndex]))
	if rebroadcast.Thread == nil || rebroadcast.Thread.ResultArtifactID != artifact.ID || rebroadcast.Thread.ResultPreview != artifact.Text || !rebroadcast.Thread.ResultCanEdit {
		t.Fatalf("terminal rebroadcast erased result: %+v", rebroadcast.Thread)
	}
}

func TestScoutChatLiveGoalEventProjectsDeckResultImmediately(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	owner := "aj@shareability.com"
	thread, err := app.createScoutChatThread(owner, "AJ", "Live deck result", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	runID := "live-deck-goal-run"
	goal, _, err := app.createOSArtifactWithMetadata("workflow", "Live deck goal", "goal", "AJ", map[string]string{
		"mode": "goal", "threadId": runID, "status": "running", "threadStatus": "running",
		"originKind": agentThreadOriginPrivateThread, "originId": thread.ID,
		"visibility": scoutChatVisibilityPrivate, "ownerEmail": owner, "requestedBy": owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{
		ID: "live-deck-goal-card", Kind: "thread", Role: "scout", Text: "Presentation in progress",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Thread:    &scoutChatThreadRef{ID: runID, Mode: "goal", Query: "Live deck goal", Status: "running", ArtifactID: goal.ID},
	}
	if _, err := app.commitScoutChatThreadMessages(owner, thread.ID, message); err != nil {
		t.Fatal(err)
	}
	thread, _, err = app.scoutChatThreadByID(owner, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	running := chatThreadPayloadMessage(t, app.scoutChatThreadUpdatePayload(owner, thread, thread.Messages[0]))
	if running.Thread == nil || running.Thread.ResultArtifactID != "" {
		t.Fatalf("running deck event exposed a result: %+v", running.Thread)
	}

	deck, _, err := app.createOSArtifactWithMetadata("workflow", "Live deck", "<!doctype html><html><body><section class=\"pg\">Live deck</section></body></html>", "Scout", map[string]string{
		"type": artifactTypeHTMLDeck, "source": "scout_thread", "goalParentId": goal.ID,
		"status": "complete", "threadStatus": "complete", "visibility": scoutChatVisibilityPrivate, "ownerEmail": owner, "requestedBy": owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	ship, _, err := app.createOSArtifactWithMetadata("workflow", "Ship deck", "reviewed deck filed", "Scout", map[string]string{
		"source": "process_stage", "processStage": "ship_compile", "goalParentId": goal.ID,
		"deckArtifactId": deck.ID, "status": "complete", "visibility": scoutChatVisibilityPrivate, "ownerEmail": owner, "requestedBy": owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := goalPlan{State: goalStateVerified, Subtasks: []goalSubtask{{ID: "ship_compile", Status: subtaskComplete, ArtifactID: ship.ID}}}
	deckProcess, ok := processByID(packagingStudioProcessID)
	if !ok {
		t.Fatal("packaging studio process is unavailable")
	}
	if err := bindGoalProcessIdentity(&plan, deckProcess); err != nil {
		t.Fatal(err)
	}
	rawPlan, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	goal, _, err = app.updateOSArtifactWithMetadata(goal.ID, "", goal.Text, "Scout", map[string]string{
		"goalPlan": string(rawPlan), "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	app.updateScoutChatThreadRefs(runID, "complete", goal.ID)
	thread, _, err = app.scoutChatThreadByID(owner, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	terminal := chatThreadPayloadMessage(t, app.scoutChatThreadUpdatePayload(owner, thread, thread.Messages[0]))
	if terminal.Thread == nil || terminal.Thread.ResultArtifactID != deck.ID || terminal.Thread.ResultArtifactType != artifactTypeHTMLDeck || !terminal.Thread.ResultCanEdit {
		t.Fatalf("terminal deck event result=%+v", terminal.Thread)
	}
}

func TestScoutChatLiveWorkEventClearsRevokedOrRacedResultSnapshot(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread, artifact, runID, messageID := seedScoutChatLiveDocumentResult(t, app)
	artifact, _, err := app.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "Scout", map[string]string{
		"status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	app.updateScoutChatThreadRefs(runID, "complete", artifact.ID)
	thread, _, err = app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	message := thread.Messages[scoutChatMessageIndex(thread, messageID)]
	enriched := chatThreadPayloadMessage(t, app.scoutChatThreadUpdatePayload(thread.OwnerEmail, thread, message))
	if enriched.Thread == nil || enriched.Thread.ResultArtifactID != artifact.ID {
		t.Fatalf("fixture did not enrich result: %+v", enriched.Thread)
	}

	if _, _, err = app.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "Scout", map[string]string{
		"visibility": scoutChatVisibilityPrivate, "ownerEmail": "tim@shareability.com", "requestedBy": "tim@shareability.com", "aclVersion": "2",
	}); err != nil {
		t.Fatal(err)
	}
	revoked := chatThreadPayloadMessage(t, app.scoutChatThreadUpdatePayload(thread.OwnerEmail, thread, message))
	if revoked.Thread == nil || revoked.Thread.ResultArtifactID != "" || revoked.Thread.ResultTitle != "" || revoked.Thread.ResultPreview != "" || revoked.Thread.ResultCanEdit {
		t.Fatalf("revoked result survived authoritative event: %+v", revoked.Thread)
	}

	// Restore the owner, then force a content-revision change between the
	// authorization header read and exact-body snapshot. The live event must
	// fail closed, so replacing the prior event removes its stale result.
	artifact, _, err = app.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "Scout", map[string]string{
		"ownerEmail": thread.OwnerEmail, "requestedBy": thread.OwnerEmail, "aclVersion": "3",
	})
	if err != nil {
		t.Fatal(err)
	}
	previousProbe := artifactAuthorizationAfterCheckProbe
	mutated := false
	artifactAuthorizationAfterCheckProbe = func() {
		if mutated {
			return
		}
		mutated = true
		if _, _, updateErr := app.updateOSArtifact(artifact.ID, "", "# Live report\n\nNew revision during projection.", "Scout"); updateErr != nil {
			t.Fatalf("race update: %v", updateErr)
		}
	}
	t.Cleanup(func() { artifactAuthorizationAfterCheckProbe = previousProbe })
	raced := chatThreadPayloadMessage(t, app.scoutChatThreadUpdatePayload(thread.OwnerEmail, thread, message))
	if raced.Thread == nil || raced.Thread.ResultArtifactID != "" || raced.Thread.ResultPreview != "" || !mutated {
		t.Fatalf("revision-raced result survived authoritative event: %+v mutated=%t", raced.Thread, mutated)
	}
}

func TestScoutChatLiveWorkEventCarriesE10ReadAndWriteCapabilityFromHeldContext(t *testing.T) {
	t.Setenv("BONFIRE_TENANT_ID", "org-one")
	t.Setenv("BONFIRE_CANONICAL_TENANT_ID", "org-one")
	app := newIsolatedKanbanBoardApp(t)
	thread, artifact, runID, messageID := seedScoutChatLiveDocumentResult(t, app)
	artifact, _, err := app.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "Scout", map[string]string{
		"status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	app.updateScoutChatThreadRefs(runID, "complete", artifact.ID)
	thread, _, err = app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	message := thread.Messages[scoutChatMessageIndex(thread, messageID)]

	now := time.Now().UTC()
	token := "live-result-held-context-token"
	sessionHash := hashResetToken(token)
	accountDigest := sha256Hex([]byte(thread.OwnerEmail))
	snapshot := strideE10TenantTestSnapshot(now)
	snapshot.SessionHash = sessionHash
	snapshot.Session.Email = thread.OwnerEmail
	snapshot.Session.AccountSubjectDigest = accountDigest
	snapshot.Session.AuthorityGeneration = snapshot.Generation
	snapshot.Session.Expires = now.Add(time.Hour)
	snapshot.Person.AccountSubjectDigest = accountDigest
	snapshot.Legacy.AccountSubjectDigest = accountDigest
	snapshot.ActiveSession.SessionSubjectDigest = sessionHash
	snapshot.ActiveSession.ExpiresAt = snapshot.Session.Expires
	sessions := &sessionStore{sessions: map[string]sessionRecord{sessionHash: snapshot.Session}}
	organizations := NewOrganizationAuthorityService()
	organizations.mu.Lock()
	organizations.persons[snapshot.Person.Header.ID] = clonePersonPrincipal(snapshot.Person)
	organizations.accountPersons[accountDigest] = snapshot.Person.Header.ID
	organizations.organizations[snapshot.Organization.Header.ID] = cloneOrganization(snapshot.Organization)
	organizations.memberships[snapshot.Membership.Header.ID] = cloneOrganizationMembership(snapshot.Membership)
	organizations.sessions[sessionHash] = cloneActiveOrganizationSession(snapshot.ActiveSession)
	organizations.mu.Unlock()
	gate := &strideE10TenantTestGate{}
	gate.enabled.Store(true)
	converter := NewStrideE10TenantConverter(
		gate,
		&strideE10MainTenantAuthorityResolver{sessions: sessions, organizations: organizations, now: func() time.Time { return now }},
		&strideE10TenantTestSink{},
		&strideE10TenantTestLegacyIDs{persons: map[string]string{accountDigest: snapshot.Person.Header.ID}},
		StrideE10TenantReceiptKey{ID: "live-result-receipt", Version: 1, Secret: []byte("live-result-receipt-secret-32-bytes")},
		StrideE10TenantConversionCutover,
	)
	converter.now = func() time.Time { return now }
	restoreConverter := InstallStrideE10TenantRuntimeConverter(converter)
	t.Cleanup(restoreConverter)
	request := httptest.NewRequest("GET", "/api/stride/v1/mobile/surfaces/profile", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	authorizer := &scoutChatLiveResultE10Authorizer{}
	previousAuthorizer := artifactObjectAuthorizer
	artifactObjectAuthorizer = authorizer
	t.Cleanup(func() { artifactObjectAuthorizer = previousAuthorizer })

	var readOnly scoutChatMessageRecord
	err = withStrideE10TenantRequestUse(request, StrideE10TenantSurfaceHTTP, func(ctx context.Context, principal *StrideE10TenantPrincipal) error {
		if principal == nil || strideE10HeldTenantAuthorityFromContext(ctx) == nil {
			t.Fatal("E10 projection did not retain its held principal context")
		}
		readOnly = chatThreadPayloadMessage(t, app.scoutChatThreadUpdatePayload(thread.OwnerEmail, thread, message, ctx))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if readOnly.Thread == nil || readOnly.Thread.ResultArtifactID != artifact.ID || readOnly.Thread.ResultCanEdit {
		t.Fatalf("E10 read-only result=%+v", readOnly.Thread)
	}
	if len(authorizer.actions) < 2 || authorizer.actions[0] != ACLReadContent || authorizer.actions[1] != ACLWrite {
		t.Fatalf("E10 capability checks=%v, want exact read then write", authorizer.actions)
	}

	authorizer.actions = nil
	authorizer.allowWrite = true
	var writable scoutChatMessageRecord
	err = withStrideE10TenantRequestUse(request, StrideE10TenantSurfaceHTTP, func(ctx context.Context, _ *StrideE10TenantPrincipal) error {
		writable = chatThreadPayloadMessage(t, app.scoutChatThreadUpdatePayload(thread.OwnerEmail, thread, message, ctx))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if writable.Thread == nil || writable.Thread.ResultArtifactID != artifact.ID || !writable.Thread.ResultCanEdit {
		t.Fatalf("E10 writable result=%+v", writable.Thread)
	}
}

func TestScoutChatLiveResultIndexIsBoundedToTerminalEventAndSharedAcrossPublicViewers(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread, _, _, _ := seedScoutChatLiveDocumentResult(t, app)
	previousProbe := scoutChatResultIndexProbe
	indexBuilds := 0
	scoutChatResultIndexProbe = func() { indexBuilds++ }
	t.Cleanup(func() { scoutChatResultIndexProbe = previousProbe })

	// A retry may reuse the durable message id after the client already received
	// a completed projection. Running frames must clear every stale capability
	// without paying for an artifact scan.
	runningMessage := thread.Messages[0]
	runningMessage.Thread.ResultArtifactID = "stale-result"
	runningMessage.Thread.ResultArtifactType = artifactTypeMarkdown
	runningMessage.Thread.ResultTitle = "Stale result"
	runningMessage.Thread.ResultPreview = "stale body"
	runningMessage.Thread.ResultApprovalState = scoutChatResultApprovalExact
	runningMessage.Thread.ResultCanEdit = true
	running := chatThreadPayloadMessage(t, app.scoutChatThreadUpdatePayload(thread.OwnerEmail, thread, runningMessage))
	if indexBuilds != 0 {
		t.Fatalf("running private progress built %d result indexes, want 0", indexBuilds)
	}
	if running.Thread == nil || running.Thread.ResultArtifactID != "" || running.Thread.ResultArtifactType != "" ||
		running.Thread.ResultTitle != "" || running.Thread.ResultPreview != "" || running.Thread.ResultApprovalState != "" || running.Thread.ResultCanEdit {
		t.Fatalf("running progress retained stale result projection: %+v", running.Thread)
	}

	publicThread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Public live result", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	artifact, _, err := app.createOSArtifactWithMetadata("research", "Public live report", "# Public live report", "Scout", map[string]string{
		"type": artifactTypeMarkdown, "source": "scout_thread", "threadId": "public-live-run",
		"status": "complete", "threadStatus": "complete", "visibility": "organization", "ownerEmail": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	terminal := scoutChatMessageRecord{
		ID: "public-live-card", Kind: "thread", Role: "scout", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Thread: &scoutChatThreadRef{ID: "public-live-run", Mode: "research", Status: "complete", ArtifactID: artifact.ID},
	}
	app.broadcastScoutChatThreadUpdate(publicThread, terminal)
	if indexBuilds != 1 {
		t.Fatalf("terminal public fanout built %d result indexes, want one shared build", indexBuilds)
	}

	terminal.Thread.Status = "running"
	app.broadcastScoutChatThreadUpdate(publicThread, terminal)
	if indexBuilds != 1 {
		t.Fatalf("running public progress rebuilt result index: builds=%d", indexBuilds)
	}
}

func TestScoutChatLiveReusedResultIndexRejectsRevisionChangedDuringFanout(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread, artifact, runID, messageID := seedScoutChatLiveDocumentResult(t, app)
	artifact, _, err := app.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "Scout", map[string]string{
		"status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	app.updateScoutChatThreadRefs(runID, "complete", artifact.ID)
	thread, _, err = app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	messageIndex := scoutChatMessageIndex(thread, messageID)
	if messageIndex < 0 {
		t.Fatal("terminal work card is missing")
	}

	// Public terminal fanout deliberately reuses one result index. If the
	// selected run changes after that index was built, a later viewer must not
	// receive a result selected from the stale revision.
	resultIndex := app.scoutChatResultIndex()
	if _, _, err = app.updateOSArtifact(artifact.ID, "", "# Live report\n\nRevision changed during fanout.", "Scout"); err != nil {
		t.Fatal(err)
	}
	projected := app.projectScoutChatMessageForViewerWithResultIndex(thread.OwnerEmail, thread, thread.Messages[messageIndex], &resultIndex)
	if projected.Thread == nil || projected.Thread.ResultArtifactID != "" || projected.Thread.ResultPreview != "" || projected.Thread.ResultCanEdit {
		t.Fatalf("revision-changed reused index exposed a stale result: %+v", projected.Thread)
	}
}
