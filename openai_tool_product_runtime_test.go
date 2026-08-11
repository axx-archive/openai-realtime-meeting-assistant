package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type openAIToolProductTestFixture struct {
	app         *kanbanBoardApp
	backend     *openAIToolProductBackend
	expectation openAIToolAuthorityExpectation
	artifact    meetingMemoryEntry
	thread      scoutChatThreadRecord
	ctx         context.Context
	resolver    *strideE10TenantTestResolver
	journal     *openAIToolJournal
	journalDir  string
	highWater   *openAIToolTestHighWater
	keyring     *openAIToolTestKeyring
	converter   *StrideE10TenantConverter
}

type openAIToolConcurrentTestArtifactAuthorizer struct{}

func (openAIToolConcurrentTestArtifactAuthorizer) AuthorizeArtifactHeader(context.Context, *userAccount, ACLAction, ArtifactAuthorizationHeader) bool {
	return false
}

func (openAIToolConcurrentTestArtifactAuthorizer) AuthorizeArtifactHeaderForStridePrincipal(context.Context, StrideE10TenantPrincipal, ACLAction, ArtifactAuthorizationHeader) bool {
	return true
}

func newOpenAIToolProductTestFixture(t *testing.T) openAIToolProductTestFixture {
	t.Helper()
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user, ok := authenticatedRequester("aj@shareability.com")
	if !ok {
		t.Fatal("AJ test account is unavailable")
	}
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Secure tool work", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	operationID := "conversation-operation-openai-tool-product-test"
	operationBodyDigest := sha256Hex([]byte("openai-tool-product-test-turn"))
	message := scoutChatMessageRecord{
		ID: "source-message-openai-tool-product-test", Kind: "message", Role: "user", Text: "Prepare the exact private work artifact and use memory if needed.",
		AuthorName: user.Name, AuthorEmail: user.Email, Via: "chat", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		SourceOperationID: operationID, SourceOperationDigest: operationBodyDigest,
	}
	thread, err = app.commitScoutChatThreadMessages(user.Email, thread.ID, message)
	if err != nil {
		t.Fatalf("commit source message: %v", err)
	}
	_, binding, err := scoutChatSourceWindow(thread, message.ID)
	if err != nil {
		t.Fatalf("bind source window: %v", err)
	}

	now := time.Now().UTC()
	snapshot := strideE10TenantTestSnapshot(now)
	digest := sha256Hex([]byte(normalizeAccountEmail(user.Email)))
	snapshot.Session.Email = user.Email
	snapshot.Session.AccountSubjectDigest = digest
	snapshot.Person.AccountSubjectDigest = digest
	snapshot.Legacy.AccountSubjectDigest = digest
	resolver := &strideE10TenantTestResolver{snapshot: snapshot}
	gate := &strideE10TenantTestGate{}
	gate.enabled.Store(true)
	converter := NewStrideE10TenantConverter(
		gate,
		resolver,
		&strideE10TenantTestSink{},
		&strideE10TenantTestLegacyIDs{persons: map[string]string{digest: snapshot.Person.Header.ID}},
		StrideE10TenantReceiptKey{ID: "openai-tool-product-test-key", Version: 1, Secret: []byte("openai-tool-product-test-key-secret-32-bytes")},
		StrideE10TenantConversionCutover,
	)
	converter.now = func() time.Time { return now }
	restoreConverter := InstallStrideE10TenantRuntimeConverter(converter)
	t.Cleanup(restoreConverter)
	previousAuthorizer := artifactObjectAuthorizer
	artifactObjectAuthorizer = &strideE10TenantTestArtifactAuthorizer{}
	t.Cleanup(func() { artifactObjectAuthorizer = previousAuthorizer })

	sessionHash := snapshot.SessionHash
	ctx := strideE10TenantContextWithSessionHash(context.Background(), sessionHash)
	ctx = openAIToolProductTenantContext(ctx, StrideE10TenantPrincipal{
		TenantID: snapshot.Session.ActiveOrganizationID, PersonID: snapshot.Session.PersonID,
		ActiveOrganizationID: snapshot.Session.ActiveOrganizationID, OrganizationMembershipID: snapshot.Session.OrganizationMembershipID,
		OrganizationMembershipRev: snapshot.Session.OrganizationMembershipRev, ActiveOrganizationSessionRev: snapshot.Session.ActiveOrganizationSessionRev,
		AuthorityGeneration: snapshot.Generation,
	})
	metadata := map[string]string{
		"source": "scout_thread", "threadId": "agent-thread-openai-tool-product-test", "threadQuery": "Prepare the exact private work artifact and use memory if needed.",
		"status": "running", "threadStatus": "running", "goalStatus": "running", "currentStage": "execute_in_order", "progressPercent": "35", "reviewGate": "pending",
		"requestedBy": user.Email, "createdBy": user.Name, "tenantId": snapshot.Session.ActiveOrganizationID,
		"originKind": agentThreadOriginPrivateThread, "originId": thread.ID, "originSurface": "chat:" + thread.ID,
		"sourceMessageId": message.ID, "sourceMessageDigest": binding.MessageDigest, "sourceWindowDigest": binding.WindowDigest,
		"operationId": operationID, "operationBodyDigest": operationBodyDigest, openAIToolSessionDigestMetadataKey: sessionHash,
	}
	artifact, appended, err := app.createOSArtifactWithMetadata("workflow", metadata["threadQuery"], "# Secure work\n\nRunning.", user.Name, metadata)
	if err != nil || !appended {
		t.Fatalf("create work artifact: appended=%v err=%v", appended, err)
	}
	workCard := scoutChatMessageRecord{
		ID: "scout-chat-message-work-" + sha256Hex([]byte(message.ID + "\x00" + metadata["threadId"]))[:24], Kind: "thread", Role: "scout", Text: "Working on it", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		CausedByMessageID: message.ID, IntentOutcome: string(conversationIntentStartPrivateWork),
		Thread: &scoutChatThreadRef{ID: metadata["threadId"], Mode: "workflow", Query: metadata["threadQuery"], Status: "running", ArtifactID: artifact.ID, ProgressPercent: 35},
	}
	thread, err = app.commitScoutChatThreadMessages(user.Email, thread.ID, workCard)
	if err != nil {
		t.Fatalf("commit work card: %v", err)
	}
	scoutThread := scoutAgentThread{ID: metadata["threadId"], Mode: "workflow", Query: metadata["threadQuery"], Status: "running", Artifact: artifact}
	job := app.newAgentJob(scoutThread)
	job.RequestedBy = user.Email
	job.Authority = codexJobAuthorityWorkspaceWrite
	journalDir := openAIToolSecureTestDirectory(t)
	highWater := &openAIToolTestHighWater{}
	keyring := newOpenAIToolTestKeyring()
	journal, err := openOpenAIToolJournal(ctx, journalDir, "product-journal", highWater, keyring)
	if err != nil {
		t.Fatalf("open product journal: %v", err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	manifest, err := buildOpenAIToolManifest()
	if err != nil {
		t.Fatalf("build product manifest: %v", err)
	}
	app.openAIToolRuntime = &openAIToolProductRuntime{Enabled: true, Carrier: &openAIToolLoopCarrier{Enabled: true, Manifest: manifest, Journal: journal}}
	artifact, err = initializeOpenAIToolProductBaseAuthority(ctx, app, artifact)
	if err != nil {
		t.Fatalf("initialize product base authority: %v", err)
	}
	scoutThread.Artifact = artifact
	job = app.newAgentJob(scoutThread)
	job.RequestedBy = user.Email
	job.Authority = codexJobAuthorityWorkspaceWrite
	backend, expectation, err := newOpenAIToolProductBackend(ctx, app, job, journal)
	if err != nil {
		t.Fatalf("new product backend: %v", err)
	}
	return openAIToolProductTestFixture{app: app, backend: backend, expectation: expectation, artifact: artifact, thread: thread, ctx: ctx, resolver: resolver, journal: journal, journalDir: journalDir, highWater: highWater, keyring: keyring, converter: converter}
}

func (fixture *openAIToolProductTestFixture) reopen(t *testing.T) {
	t.Helper()
	_ = fixture.journal.Close()
	memory, err := newMeetingMemoryStore(fixture.app.memory.path)
	if err != nil {
		t.Fatalf("reopen product memory: %v", err)
	}
	journal, err := openOpenAIToolJournal(fixture.ctx, fixture.journalDir, "product-journal", fixture.highWater, fixture.keyring)
	if err != nil {
		t.Fatalf("reopen product journal: %v", err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	app := &kanbanBoardApp{
		memory: memory, goalStartedChildren: map[string]struct{}{}, openAIToolActiveRuns: map[string]struct{}{}, openAIToolActivationOwner: "reopened-product-process",
		scoutInvocation: NewSTRIDEScoutInvocationMachine(20 * time.Second),
	}
	manifest, err := buildOpenAIToolManifest()
	if err != nil {
		t.Fatal(err)
	}
	app.openAIToolRuntime = &openAIToolProductRuntime{Enabled: true, Carrier: &openAIToolLoopCarrier{Enabled: true, Manifest: manifest, Journal: journal}}
	artifact, ok := app.osArtifactByID(fixture.expectation.ArtifactID)
	if !ok {
		t.Fatal("reopened product artifact is unavailable")
	}
	job := fixture.backend.job
	job.thread.Artifact = artifact
	job.ArtifactID = artifact.ID
	backend, expectation, err := newOpenAIToolProductBackend(fixture.ctx, app, job, journal)
	if err != nil {
		t.Fatalf("reopen product backend: %v", err)
	}
	fixture.app, fixture.backend, fixture.expectation, fixture.artifact, fixture.journal = app, backend, expectation, artifact, journal
}

func prepareOpenAIToolActivationFixture(t *testing.T, fixture *openAIToolProductTestFixture, includeCard bool) scoutAgentThread {
	t.Helper()
	key := StrideE10TenantAuthorityEnvelopeKey{ID: "openai-tool-activation-envelope", Version: 1, Secret: []byte("openai-tool-activation-envelope-secret-32-bytes")}
	restoreEnvelope := InstallStrideE10TenantAuthorityEnvelopeRuntime(&strideE10TenantEnvelopeTestKeyring{current: key, keys: map[string]StrideE10TenantAuthorityEnvelopeKey{key.ID: key}})
	t.Cleanup(restoreEnvelope)
	purpose := StrideE10TenantAuthorityPurposeForScoutThread(fixture.expectation.ThreadID, "workflow", fixture.artifact.Metadata["threadQuery"])
	envelope, err := MintStrideE10TenantAuthorityEnvelopeForSurface(fixture.ctx, fixture.converter, fixture.expectation.SessionDigest, StrideE10TenantSurfaceScout, purpose, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("mint activation envelope: %v", err)
	}
	stored, err := fixture.app.persistStrideE10ScoutAuthority(fixture.expectation.ThreadID, &envelope)
	if err != nil {
		t.Fatalf("persist activation envelope: %v", err)
	}
	header := artifactAuthorizationHeaderFromEntry(fixture.artifact)
	artifact, changed, err := fixture.app.memory.updateOSArtifactMetadataIfHeaderMatches(header, fixture.artifact.ID, map[string]string{
		"tenantAuthorityRef": fixture.expectation.ThreadID, openAIToolActivationStateMetadataKey: openAIToolActivationReserved,
		openAIToolActivationOwnerMetadataKey: "", "mode": "workflow", "query": fixture.artifact.Metadata["threadQuery"],
	})
	if err != nil || !changed {
		t.Fatalf("reserve activation artifact: changed=%v err=%v", changed, err)
	}
	thread := scoutAgentThread{ID: fixture.expectation.ThreadID, Mode: "workflow", Query: fixture.artifact.Metadata["threadQuery"], Status: "running", Artifact: artifact, TenantAuthority: stored}
	if includeCard {
		var source scoutChatMessageRecord
		for _, message := range fixture.thread.Messages {
			if message.ID == artifact.Metadata["sourceMessageId"] {
				source = message
				break
			}
		}
		card := conversationWorkReplayCard(source, thread)
		alreadyCommitted := false
		for _, message := range fixture.thread.Messages {
			if message.ID == card.ID {
				alreadyCommitted = true
				break
			}
		}
		if !alreadyCommitted {
			if _, err := fixture.app.commitScoutChatThreadMessages(fixture.expectation.RequesterAccount, fixture.thread.ID, card); err != nil {
				t.Fatalf("commit exact activation card: %v", err)
			}
		}
	}
	fixture.artifact = artifact
	return thread
}

func (fixture openAIToolProductTestFixture) request(t *testing.T, tool string, arguments map[string]any, operationID string) openAIToolEffectRequest {
	t.Helper()
	entry, ok := backendManifestEntry(tool)
	if !ok {
		t.Fatalf("manifest entry %s unavailable", tool)
	}
	digest, _, err := openAIToolCanonicalDigest(arguments)
	if err != nil {
		t.Fatalf("arguments digest: %v", err)
	}
	expectation := fixture.expectation
	expectation.ToolName = tool
	expectation.ManifestDigest = openAIToolManifestV1SHA256
	expectation.SchemaDigest = entry.SchemaSHA256
	expectation.ArgumentsDigest = digest
	expectation.PolicyRevision = entry.PolicyRevision
	preimage, err := fixture.backend.authorizePreimage(fixture.ctx, expectation, entry, arguments)
	if err != nil {
		t.Fatalf("authorize %s: %v", tool, err)
	}
	return openAIToolEffectRequest{Current: &openAIToolProductCurrentAuthority{backend: fixture.backend}, OperationID: operationID, Expectation: expectation, Entry: entry, Arguments: arguments, PreimageDigest: preimage}
}

func TestToolGoalStateProductAdapterNormalRaceRestart(t *testing.T) {
	fixture := newOpenAIToolProductTestFixture(t)
	request := fixture.request(t, controlToolReportGoalState, map[string]any{
		"goal_status": "review", "review_gate": "pending", "stage": "gate_before_shipping", "progress_percent": json.Number("72"), "note": "Checking the exact artifact.",
	}, "openai-tool-operation-goal-state")
	commit, err := fixture.backend.ApplyGoalState(fixture.ctx, request)
	if err != nil || validateOpenAIToolEffectCommit(request.Entry.Name, commit) != nil {
		t.Fatalf("apply goal state: commit=%+v err=%v", commit, err)
	}
	reconciled, err := fixture.backend.ReconcileGoalState(fixture.ctx, request)
	if err != nil || reconciled.Status != openAIToolReconciliationCommitted || !openAIToolCommitEqual(commit, reconciled.Commit) {
		t.Fatalf("reconcile goal state: %+v err=%v", reconciled, err)
	}
	thread, _, err := fixture.app.scoutChatThreadByID(fixture.expectation.RequesterAccount, fixture.thread.ID)
	if err != nil {
		t.Fatalf("read goal progress projection: %v", err)
	}
	progressCards := 0
	for _, message := range thread.Messages {
		if message.Thread != nil && message.Thread.ID == fixture.expectation.ThreadID && message.Thread.ProgressPercent == 72 && message.Thread.CurrentStage == "gate_before_shipping" {
			progressCards++
		}
	}
	if progressCards != 1 {
		t.Fatalf("goal progress projections=%d, want one exact durable card", progressCards)
	}
	if _, err := fixture.backend.ApplyGoalState(fixture.ctx, request); err == nil {
		t.Fatal("duplicate direct execution bypassed reconciliation")
	}
	stale := request
	stale.OperationID = "openai-tool-operation-goal-state-stale"
	stale.Arguments = map[string]any{"goal_status": "running", "review_gate": "pending", "stage": "execute_in_order", "progress_percent": json.Number("10"), "note": "go backwards"}
	if _, err := fixture.backend.ApplyGoalState(fixture.ctx, stale); err == nil {
		t.Fatal("non-monotonic concurrent goal state committed")
	}
}

func TestOpenAIToolGoalGateStickyAndReceiptChainLinearAcrossReopen(t *testing.T) {
	fixture := newOpenAIToolProductTestFixture(t)
	var projectionEvents atomic.Int32
	previousProbe := openAIToolProjectionEventProbe
	openAIToolProjectionEventProbe = func(openAIToolProductEffectReceipt) { projectionEvents.Add(1) }
	t.Cleanup(func() { openAIToolProjectionEventProbe = previousProbe })

	steps := []map[string]any{
		{"goal_status": "running", "review_gate": "pending", "stage": "coordinate_dependencies", "progress_percent": json.Number("40"), "note": "Coordinate."},
		{"goal_status": "running", "review_gate": "pending", "stage": "execute_in_order", "progress_percent": json.Number("40"), "note": "Return to execution."},
		{"goal_status": "review", "review_gate": "pending", "stage": "gate_before_shipping", "progress_percent": json.Number("41"), "note": "Advance."},
		{"goal_status": "review", "review_gate": "approval_required", "stage": "gate_before_shipping", "progress_percent": json.Number("42"), "note": "Human approval is required."},
	}
	requests := make([]openAIToolEffectRequest, 0, len(steps))
	for index, arguments := range steps {
		request := fixture.request(t, controlToolReportGoalState, arguments, fmt.Sprintf("openai-tool-linear-goal-%d", index))
		if _, err := fixture.backend.ApplyGoalState(fixture.ctx, request); err != nil {
			t.Fatalf("apply linear goal step %d: %v", index, err)
		}
		requests = append(requests, request)
	}
	current, _ := fixture.app.osArtifactByID(fixture.artifact.ID)
	beforeRejected, _ := openAIToolArtifactPostimageDigest(current)
	noOp := fixture.request(t, controlToolReportGoalState, steps[len(steps)-1], "openai-tool-linear-goal-no-op")
	if _, err := fixture.backend.ApplyGoalState(fixture.ctx, noOp); err == nil {
		t.Fatal("semantic goal no-op persisted a receipt")
	}
	clear := fixture.request(t, controlToolReportGoalState, map[string]any{
		"goal_status": "review", "review_gate": "pending", "stage": "gate_before_shipping", "progress_percent": json.Number("43"), "note": "Model attempted to clear approval.",
	}, "openai-tool-linear-goal-clear")
	if _, err := fixture.backend.ApplyGoalState(fixture.ctx, clear); err == nil {
		t.Fatal("model cleared a sticky approval gate")
	}
	afterRejectedArtifact, _ := fixture.app.osArtifactByID(fixture.artifact.ID)
	afterRejected, _ := openAIToolArtifactPostimageDigest(afterRejectedArtifact)
	if beforeRejected != afterRejected || projectionEvents.Load() != int32(len(steps)) {
		t.Fatalf("rejected goal turn changed bytes/events: before=%s after=%s events=%d", beforeRejected, afterRejected, projectionEvents.Load())
	}

	fixture.reopen(t)
	for index, request := range requests {
		reconciled, err := fixture.backend.ReconcileGoalState(fixture.ctx, request)
		if err != nil || reconciled.Status != openAIToolReconciliationCommitted {
			t.Fatalf("reconcile linear goal step %d after reopen: %+v err=%v", index, reconciled, err)
		}
	}
	clear = fixture.request(t, controlToolReportGoalState, clear.Arguments, "openai-tool-linear-goal-clear-reopened")
	if _, err := fixture.backend.ApplyGoalState(fixture.ctx, clear); err == nil || projectionEvents.Load() != int32(len(steps)) {
		t.Fatalf("reopened sticky gate changed: events=%d err=%v", projectionEvents.Load(), err)
	}
}

func TestOpenAIToolUpdateSemanticNoOpHasZeroReceiptOrProjection(t *testing.T) {
	fixture := newOpenAIToolProductTestFixture(t)
	var projectionEvents atomic.Int32
	previousProbe := openAIToolProjectionEventProbe
	openAIToolProjectionEventProbe = func(openAIToolProductEffectReceipt) { projectionEvents.Add(1) }
	t.Cleanup(func() { openAIToolProjectionEventProbe = previousProbe })
	request := fixture.request(t, "update_artifact", map[string]any{
		"artifact_id": fixture.artifact.ID, "title": fixture.artifact.Metadata["title"], "content": fixture.artifact.Text,
	}, "openai-tool-update-no-op")
	before, _ := openAIToolArtifactPostimageDigest(fixture.artifact)
	if _, err := fixture.backend.UpdateAuthorizedArtifact(fixture.ctx, request); err == nil {
		t.Fatal("semantic artifact no-op persisted")
	}
	afterArtifact, _ := fixture.app.osArtifactByID(fixture.artifact.ID)
	after, _ := openAIToolArtifactPostimageDigest(afterArtifact)
	receipts, err := openAIToolProductReceipts(afterArtifact)
	if err != nil || before != after || len(receipts) != 0 || projectionEvents.Load() != 0 {
		t.Fatalf("no-op changed product state: before=%s after=%s receipts=%d events=%d err=%v", before, after, len(receipts), projectionEvents.Load(), err)
	}
}

func TestToolMemoryReadProductAdapterNormalRaceRestart(t *testing.T) {
	fixture := newOpenAIToolProductTestFixture(t)
	if _, appended, err := fixture.app.memory.appendNote("memory-openai-tool-product-test", "The launch decision is to keep the secure carrier default off.", map[string]string{"tenantId": fixture.expectation.TenantID, "visibility": "organization", "createdBy": "AJ"}); err != nil || !appended {
		t.Fatalf("append memory source: appended=%v err=%v", appended, err)
	}
	request := fixture.request(t, "answer_memory_question", map[string]any{"query": "What was the launch decision?"}, "openai-tool-operation-memory-read")
	commit, err := fixture.backend.ReadMemoryAnswer(fixture.ctx, request)
	if err != nil || validateOpenAIToolEffectCommit(request.Entry.Name, commit) != nil {
		t.Fatalf("read memory: commit=%+v err=%v", commit, err)
	}
	reconciled, err := fixture.backend.ReconcileMemoryAnswer(fixture.ctx, request)
	if err != nil || reconciled.Status != openAIToolReconciliationCommitted || !openAIToolCommitEqual(commit, reconciled.Commit) {
		t.Fatalf("reconcile memory: %+v err=%v", reconciled, err)
	}
	if _, appended, err := fixture.app.memory.appendNote("memory-openai-tool-product-drift", "A later authorized source changes the exact recall window.", map[string]string{"tenantId": fixture.expectation.TenantID, "visibility": "organization", "createdBy": "AJ"}); err != nil || !appended {
		t.Fatalf("append memory drift: appended=%v err=%v", appended, err)
	}
	drifted, err := fixture.backend.ReconcileMemoryAnswer(fixture.ctx, request)
	if err != nil || drifted.Status != openAIToolReconciliationAmbiguous {
		t.Fatalf("changed memory window was not quarantinable: %+v err=%v", drifted, err)
	}
}

func TestToolCreateArtifactProductAdapterNormalRaceRestart(t *testing.T) {
	fixture := newOpenAIToolProductTestFixture(t)
	var projectionEvents atomic.Int32
	previousProbe := openAIToolProjectionEventProbe
	openAIToolProjectionEventProbe = func(openAIToolProductEffectReceipt) { projectionEvents.Add(1) }
	t.Cleanup(func() { openAIToolProjectionEventProbe = previousProbe })
	request := fixture.request(t, "create_artifact", map[string]any{"mode": "artifacts", "query": "Save the launch note", "content": "Private launch note."}, "openai-tool-operation-artifact-create")
	commit, err := fixture.backend.CreatePrivateArtifact(fixture.ctx, request)
	if err != nil || validateOpenAIToolEffectCommit(request.Entry.Name, commit) != nil {
		t.Fatalf("create artifact: commit=%+v err=%v", commit, err)
	}
	reconciled, err := fixture.backend.ReconcileArtifactCreate(fixture.ctx, request)
	if err != nil || reconciled.Status != openAIToolReconciliationCommitted || !openAIToolCommitEqual(commit, reconciled.Commit) {
		t.Fatalf("reconcile create: %+v err=%v", reconciled, err)
	}
	created, ok := fixture.app.osArtifactByID(openAIToolCreatedArtifactID(request.OperationID))
	if !ok || created.Metadata["visibility"] != scoutChatVisibilityPrivate || normalizeAccountEmail(created.Metadata["ownerEmail"]) != fixture.expectation.RequesterAccount || created.Text != "Private launch note." {
		t.Fatalf("created artifact scope/body mismatch: %+v", created)
	}
	thread, _, err := fixture.app.scoutChatThreadByID(fixture.expectation.RequesterAccount, fixture.thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	createdCards := 0
	for _, message := range thread.Messages {
		if message.Thread != nil && message.Thread.ArtifactID == created.ID {
			createdCards++
		}
	}
	if createdCards != 1 || projectionEvents.Load() != 1 {
		t.Fatalf("created artifact cards=%d projection events=%d", createdCards, projectionEvents.Load())
	}
	concurrent := fixture.request(t, "create_artifact", map[string]any{"mode": "artifacts", "query": "Second", "content": "Second body."}, "openai-tool-operation-artifact-create-race")
	if _, err := fixture.backend.CreatePrivateArtifact(fixture.ctx, concurrent); err != nil {
		t.Fatalf("fresh second create: %v", err)
	}
	if _, err := fixture.backend.CreatePrivateArtifact(fixture.ctx, request); err == nil {
		t.Fatal("stale collection generation created a duplicate artifact")
	}
}

func TestToolUpdateArtifactProductAdapterNormalRaceRestart(t *testing.T) {
	fixture := newOpenAIToolProductTestFixture(t)
	var projectionEvents atomic.Int32
	previousProbe := openAIToolProjectionEventProbe
	openAIToolProjectionEventProbe = func(openAIToolProductEffectReceipt) { projectionEvents.Add(1) }
	t.Cleanup(func() { openAIToolProjectionEventProbe = previousProbe })
	request := fixture.request(t, "update_artifact", map[string]any{"artifact_id": fixture.artifact.ID, "title": "Secure work revised", "content": "# Secure work revised\n\nExact successor."}, "openai-tool-operation-artifact-update")
	concurrent := request
	concurrent.OperationID = "openai-tool-operation-artifact-update-race"
	commit, err := fixture.backend.UpdateAuthorizedArtifact(fixture.ctx, request)
	if err != nil || validateOpenAIToolEffectCommit(request.Entry.Name, commit) != nil {
		t.Fatalf("update artifact: commit=%+v err=%v", commit, err)
	}
	reconciled, err := fixture.backend.ReconcileArtifactUpdate(fixture.ctx, request)
	if err != nil || reconciled.Status != openAIToolReconciliationCommitted || !openAIToolCommitEqual(commit, reconciled.Commit) {
		t.Fatalf("reconcile update: %+v err=%v", reconciled, err)
	}
	if _, err := fixture.backend.UpdateAuthorizedArtifact(fixture.ctx, concurrent); err == nil {
		t.Fatal("stale concurrent artifact preimage committed")
	}
	updated, ok := fixture.app.osArtifactByID(fixture.artifact.ID)
	if !ok || updated.Metadata["title"] != "Secure work revised" || !strings.Contains(updated.Text, "Exact successor") || artifactVersion(updated) != artifactVersion(fixture.artifact)+1 {
		t.Fatalf("artifact successor mismatch: %+v", updated)
	}
	if projectionEvents.Load() != 1 {
		t.Fatalf("update projection events=%d, want one with zero replay/error duplicates", projectionEvents.Load())
	}
}

func TestOpenAIToolProjectionOutboxCrashRecoveryIsAtMostOnce(t *testing.T) {
	type toolCase struct {
		name      string
		tool      string
		arguments func(openAIToolProductTestFixture) map[string]any
	}
	cases := []toolCase{
		{name: "report_goal_state", tool: controlToolReportGoalState, arguments: func(openAIToolProductTestFixture) map[string]any {
			return map[string]any{"goal_status": "review", "review_gate": "pending", "stage": "gate_before_shipping", "progress_percent": json.Number("74"), "note": "Crash-bound projection."}
		}},
		{name: "create_artifact", tool: "create_artifact", arguments: func(openAIToolProductTestFixture) map[string]any {
			return map[string]any{"mode": "artifacts", "query": "Crash-bound private artifact", "content": "Exact private artifact body."}
		}},
		{name: "update_artifact", tool: "update_artifact", arguments: func(fixture openAIToolProductTestFixture) map[string]any {
			return map[string]any{"artifact_id": fixture.artifact.ID, "title": "Crash-bound update", "content": "# Crash-bound update\n\nExact successor."}
		}},
	}
	apply := func(fixture *openAIToolProductTestFixture, request openAIToolEffectRequest) (openAIToolEffectCommit, error) {
		switch request.Entry.Name {
		case controlToolReportGoalState:
			return fixture.backend.ApplyGoalState(fixture.ctx, request)
		case "create_artifact":
			return fixture.backend.CreatePrivateArtifact(fixture.ctx, request)
		case "update_artifact":
			return fixture.backend.UpdateAuthorizedArtifact(fixture.ctx, request)
		default:
			return openAIToolEffectCommit{}, errors.New("unsupported test tool")
		}
	}
	reconcile := func(fixture *openAIToolProductTestFixture, request openAIToolEffectRequest) (openAIToolReconciliation, error) {
		switch request.Entry.Name {
		case controlToolReportGoalState:
			return fixture.backend.ReconcileGoalState(fixture.ctx, request)
		case "create_artifact":
			return fixture.backend.ReconcileArtifactCreate(fixture.ctx, request)
		case "update_artifact":
			return fixture.backend.ReconcileArtifactUpdate(fixture.ctx, request)
		default:
			return openAIToolReconciliation{}, errors.New("unsupported test tool")
		}
	}

	for _, testCase := range cases {
		t.Run(testCase.name+"/card_before_dispatch", func(t *testing.T) {
			fixture := newOpenAIToolProductTestFixture(t)
			var events atomic.Int32
			previousEventProbe, previousBefore := openAIToolProjectionEventProbe, openAIToolBeforeProjectionDispatchProbe
			openAIToolProjectionEventProbe = func(openAIToolProductEffectReceipt) { events.Add(1) }
			openAIToolBeforeProjectionDispatchProbe = func(openAIToolProductEffectReceipt) error {
				return errors.New("simulated crash before outbox dispatch")
			}
			t.Cleanup(func() {
				openAIToolProjectionEventProbe, openAIToolBeforeProjectionDispatchProbe = previousEventProbe, previousBefore
			})
			request := fixture.request(t, testCase.tool, testCase.arguments(fixture), "openai-tool-projection-before-"+testCase.name)
			if _, err := apply(&fixture, request); err == nil || events.Load() != 0 {
				t.Fatalf("pre-dispatch crash err=%v events=%d", err, events.Load())
			}
			openAIToolBeforeProjectionDispatchProbe = nil
			fixture.reopen(t)
			request.Current = &openAIToolProductCurrentAuthority{backend: fixture.backend}
			result, err := reconcile(&fixture, request)
			if err != nil || result.Status != openAIToolReconciliationCommitted || events.Load() != 1 {
				t.Fatalf("recovered projection=%+v events=%d err=%v", result, events.Load(), err)
			}
			if _, err := reconcile(&fixture, request); err != nil || events.Load() != 1 {
				t.Fatalf("projection replay emitted again: events=%d err=%v", events.Load(), err)
			}
		})

		t.Run(testCase.name+"/fanout_claim_before_socket", func(t *testing.T) {
			fixture := newOpenAIToolProductTestFixture(t)
			var events atomic.Int32
			previousEventProbe, previousClaimProbe := openAIToolProjectionEventProbe, targetedKanbanAfterDeliveryClaimProbe
			openAIToolProjectionEventProbe = func(openAIToolProductEffectReceipt) { events.Add(1) }
			targetedKanbanAfterDeliveryClaimProbe = func(string) error { return errors.New("simulated process death after fan-out claim") }
			t.Cleanup(func() {
				openAIToolProjectionEventProbe, targetedKanbanAfterDeliveryClaimProbe = previousEventProbe, previousClaimProbe
			})
			request := fixture.request(t, testCase.tool, testCase.arguments(fixture), "openai-tool-projection-claim-"+testCase.name)
			if _, err := apply(&fixture, request); err == nil || events.Load() != 0 {
				t.Fatalf("claim-before-socket crash err=%v events=%d", err, events.Load())
			}
			targetedKanbanAfterDeliveryClaimProbe = nil
			fixture.reopen(t)
			request.Current = &openAIToolProductCurrentAuthority{backend: fixture.backend}
			result, err := reconcile(&fixture, request)
			if err != nil || result.Status != openAIToolReconciliationCommitted || events.Load() != 1 {
				t.Fatalf("claimed outbox recovery=%+v events=%d err=%v", result, events.Load(), err)
			}
		})

		t.Run(testCase.name+"/event_before_return", func(t *testing.T) {
			fixture := newOpenAIToolProductTestFixture(t)
			var events atomic.Int32
			var attemptsMu sync.Mutex
			attemptIDs := make([]string, 0, 2)
			attemptPayloads := make([]string, 0, 2)
			previousEventProbe, previousAfter, previousAttempt := openAIToolProjectionEventProbe, openAIToolAfterProjectionEventProbe, targetedKanbanDeliveryAttemptProbe
			openAIToolProjectionEventProbe = func(openAIToolProductEffectReceipt) { events.Add(1) }
			openAIToolAfterProjectionEventProbe = func(openAIToolProductEffectReceipt) error { return errors.New("simulated crash after outbox dispatch") }
			targetedKanbanDeliveryAttemptProbe = func(deliveryID, raw string) {
				attemptsMu.Lock()
				defer attemptsMu.Unlock()
				attemptIDs = append(attemptIDs, deliveryID)
				attemptPayloads = append(attemptPayloads, raw)
			}
			t.Cleanup(func() {
				openAIToolProjectionEventProbe, openAIToolAfterProjectionEventProbe, targetedKanbanDeliveryAttemptProbe = previousEventProbe, previousAfter, previousAttempt
			})
			request := fixture.request(t, testCase.tool, testCase.arguments(fixture), "openai-tool-projection-after-"+testCase.name)
			if _, err := apply(&fixture, request); err == nil || events.Load() != 1 {
				t.Fatalf("post-dispatch crash err=%v events=%d", err, events.Load())
			}
			openAIToolAfterProjectionEventProbe = nil
			fixture.reopen(t)
			// Model process death, not merely app reconstruction: process-local
			// server delivery claims disappear while the signed pending outbox row
			// survives and retries the same delivery ID.
			targetedKanbanDeliveryMu.Lock()
			targetedKanbanDeliverySeen = map[string]struct{}{}
			targetedKanbanDeliveryMu.Unlock()
			request.Current = &openAIToolProductCurrentAuthority{backend: fixture.backend}
			result, err := reconcile(&fixture, request)
			if err != nil || result.Status != openAIToolReconciliationCommitted || events.Load() != 2 {
				t.Fatalf("post-dispatch replay=%+v events=%d err=%v", result, events.Load(), err)
			}
			attemptsMu.Lock()
			ids := append([]string(nil), attemptIDs...)
			payloads := append([]string(nil), attemptPayloads...)
			attemptsMu.Unlock()
			if len(ids) != 2 || ids[0] == "" || ids[0] != ids[1] || len(payloads) != 2 || payloads[0] != payloads[1] {
				t.Fatalf("restart retry was not byte-exact: ids=%q payloads=%q", ids, payloads)
			}
			assertIndexAppliesOSEventAttemptsOnce(t, payloads)
		})
	}
}

func TestOpenAIToolProjectionReceiptSetTamperBlocksProviderAdmission(t *testing.T) {
	for _, variant := range []string{"extra", "wrong_artifact", "wrong_digest", "bad_authentication"} {
		t.Run(variant, func(t *testing.T) {
			fixture := newOpenAIToolProductTestFixture(t)
			var projectionEvents atomic.Int32
			previousProbe := openAIToolProjectionEventProbe
			openAIToolProjectionEventProbe = func(openAIToolProductEffectReceipt) { projectionEvents.Add(1) }
			t.Cleanup(func() { openAIToolProjectionEventProbe = previousProbe })
			request := fixture.request(t, "update_artifact", map[string]any{
				"artifact_id": fixture.artifact.ID, "title": "Projection receipt fence", "content": "# Projection receipt fence\n\nExact body.",
			}, "openai-tool-projection-tamper-"+variant)
			if _, err := fixture.backend.UpdateAuthorizedArtifact(fixture.ctx, request); err != nil {
				t.Fatal(err)
			}
			artifact, _ := fixture.app.osArtifactByID(fixture.artifact.ID)
			projections, err := openAIToolProductProjectionReceipts(artifact)
			if err != nil || len(projections) != 1 {
				t.Fatalf("projection setup: receipts=%d err=%v", len(projections), err)
			}
			projection := projections[request.OperationID]
			switch variant {
			case "extra":
				projection.OperationID = request.OperationID + "-extra"
				projections[projection.OperationID] = projection
			case "wrong_artifact":
				projection.ArtifactID = fixture.artifact.ID + "-wrong"
				projections[request.OperationID] = projection
			case "wrong_digest":
				projection.ProjectionDigest = strings.Repeat("a", 64)
				projections[request.OperationID] = projection
			case "bad_authentication":
				projection.ReceiptAuthentication = strings.Repeat("b", 64)
				projections[request.OperationID] = projection
			}
			encoded, err := encodeOpenAIToolProductProjectionReceipts(projections)
			if err != nil {
				t.Fatal(err)
			}
			header := artifactAuthorizationHeaderFromEntry(artifact)
			if _, changed, err := fixture.app.memory.updateOSArtifactMetadataIfHeaderMatches(header, artifact.ID, map[string]string{openAIToolProjectionReceiptsMetadataKey: encoded}); err != nil || !changed {
				t.Fatalf("persist projection tamper: changed=%v err=%v", changed, err)
			}
			manifest, _ := buildOpenAIToolManifest()
			provider := &openAIToolScriptProvider{steps: []openAIToolScriptStep{{response: openAIToolTerminalResponse("projection-tamper-terminal", "Never admitted.")}}}
			carrier := &openAIToolLoopCarrier{
				Enabled: true, Manifest: manifest, Journal: fixture.journal, Provider: provider,
				Authority: &openAIToolProductAuthorityLease{backend: fixture.backend}, Executor: openAIToolEffectAdapter{Backend: fixture.backend}, Finalizer: &openAIToolProductFinalizer{backend: fixture.backend},
			}
			_, runErr := carrier.Run(fixture.ctx, openAIToolLoopRequest{Instructions: "Use only admitted tools.", UserTurn: "This must fail before provider admission.", Expectation: fixture.expectation})
			if runErr == nil || provider.calls != 0 || projectionEvents.Load() != 1 {
				t.Fatalf("tampered projection admitted: provider=%d events=%d err=%v", provider.calls, projectionEvents.Load(), runErr)
			}
		})
	}
}

func TestOpenAIToolProductAdaptersReopenMemoryAndJournal(t *testing.T) {
	t.Run("report_goal_state", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		request := fixture.request(t, controlToolReportGoalState, map[string]any{"goal_status": "review", "review_gate": "pending", "stage": "gate_before_shipping", "progress_percent": json.Number("76"), "note": "Reopen proof."}, "openai-tool-reopen-goal")
		commit, err := fixture.backend.ApplyGoalState(fixture.ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		fixture.reopen(t)
		request.Current = &openAIToolProductCurrentAuthority{backend: fixture.backend}
		reconciled, err := fixture.backend.ReconcileGoalState(fixture.ctx, request)
		if err != nil || reconciled.Status != openAIToolReconciliationCommitted || !openAIToolCommitEqual(commit, reconciled.Commit) {
			t.Fatalf("goal reopen reconciliation=%+v err=%v", reconciled, err)
		}
	})

	t.Run("answer_memory_question", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		if _, appended, err := fixture.app.memory.appendNote("openai-tool-reopen-memory", "The secure carrier stays default off until activation.", map[string]string{"tenantId": fixture.expectation.TenantID, "visibility": "organization", "createdBy": "AJ"}); err != nil || !appended {
			t.Fatalf("append recall source: %v", err)
		}
		request := fixture.request(t, "answer_memory_question", map[string]any{"query": "What is the secure carrier activation decision?"}, "openai-tool-reopen-memory")
		commit, err := fixture.backend.ReadMemoryAnswer(fixture.ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		fixture.reopen(t)
		request.Current = &openAIToolProductCurrentAuthority{backend: fixture.backend}
		reconciled, err := fixture.backend.ReconcileMemoryAnswer(fixture.ctx, request)
		if err != nil || reconciled.Status != openAIToolReconciliationCommitted || !openAIToolCommitEqual(commit, reconciled.Commit) {
			t.Fatalf("memory reopen reconciliation=%+v err=%v", reconciled, err)
		}
		if _, appended, err := fixture.app.memory.appendNote("openai-tool-reopen-memory-drift", "The source window changed after the read.", map[string]string{"tenantId": fixture.expectation.TenantID, "visibility": "organization", "createdBy": "AJ"}); err != nil || !appended {
			t.Fatalf("append recall drift: %v", err)
		}
		drifted, err := fixture.backend.ReconcileMemoryAnswer(fixture.ctx, request)
		if err != nil || drifted.Status != openAIToolReconciliationAmbiguous {
			t.Fatalf("changed reopened memory was not ambiguous: %+v err=%v", drifted, err)
		}
	})

	t.Run("create_artifact", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		var projectionEvents atomic.Int32
		previousProbe := openAIToolProjectionEventProbe
		openAIToolProjectionEventProbe = func(openAIToolProductEffectReceipt) { projectionEvents.Add(1) }
		t.Cleanup(func() { openAIToolProjectionEventProbe = previousProbe })
		request := fixture.request(t, "create_artifact", map[string]any{"mode": "artifacts", "query": "Persist reopen proof", "content": "Exact private reopen proof."}, "openai-tool-reopen-create")
		commit, err := fixture.backend.CreatePrivateArtifact(fixture.ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		fixture.reopen(t)
		request.Current = &openAIToolProductCurrentAuthority{backend: fixture.backend}
		reconciled, err := fixture.backend.ReconcileArtifactCreate(fixture.ctx, request)
		if err != nil || reconciled.Status != openAIToolReconciliationCommitted || !openAIToolCommitEqual(commit, reconciled.Commit) || projectionEvents.Load() != 1 {
			t.Fatalf("create reopen reconciliation=%+v err=%v", reconciled, err)
		}
	})

	t.Run("update_artifact", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		var projectionEvents atomic.Int32
		previousProbe := openAIToolProjectionEventProbe
		openAIToolProjectionEventProbe = func(openAIToolProductEffectReceipt) { projectionEvents.Add(1) }
		t.Cleanup(func() { openAIToolProjectionEventProbe = previousProbe })
		request := fixture.request(t, "update_artifact", map[string]any{"artifact_id": fixture.artifact.ID, "title": "Reopened exact successor", "content": "# Reopened exact successor\n\nPersisted once."}, "openai-tool-reopen-update")
		commit, err := fixture.backend.UpdateAuthorizedArtifact(fixture.ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		fixture.reopen(t)
		request.Current = &openAIToolProductCurrentAuthority{backend: fixture.backend}
		reconciled, err := fixture.backend.ReconcileArtifactUpdate(fixture.ctx, request)
		if err != nil || reconciled.Status != openAIToolReconciliationCommitted || !openAIToolCommitEqual(commit, reconciled.Commit) || projectionEvents.Load() != 1 {
			t.Fatalf("update reopen reconciliation=%+v err=%v", reconciled, err)
		}
	})
}

func TestOpenAIToolConversationActivationCrashRecoveryAndConcurrency(t *testing.T) {
	previousStart := startAgentThreadAsync
	defer func() { startAgentThreadAsync = previousStart }()

	t.Run("reserved_before_card_recovers_projection_and_worker", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		prepareOpenAIToolActivationFixture(t, &fixture, false)
		var starts atomic.Int32
		startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { starts.Add(1) }
		if err := fixture.app.installOpenAIToolProductRuntime(fixture.ctx, fixture.app.openAIToolRuntime); err != nil {
			t.Fatalf("recover reserved work: %v", err)
		}
		if starts.Load() != 1 {
			t.Fatalf("recovered starts=%d, want one", starts.Load())
		}
		artifact, _ := fixture.app.osArtifactByID(fixture.artifact.ID)
		if artifact.Metadata[openAIToolActivationStateMetadataKey] != openAIToolActivationStarted {
			t.Fatalf("reserved work did not durably start: %+v", artifact.Metadata)
		}
		if _, _, _, err := fixture.app.verifyOpenAIToolConversationCard(artifact); err != nil {
			t.Fatalf("recovery did not create the exact card: %v", err)
		}
	})

	t.Run("durable_card_before_activation_recovers_once", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		prepareOpenAIToolActivationFixture(t, &fixture, true)
		var starts atomic.Int32
		startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { starts.Add(1) }
		if err := fixture.app.installOpenAIToolProductRuntime(fixture.ctx, fixture.app.openAIToolRuntime); err != nil {
			t.Fatal(err)
		}
		if err := fixture.app.installOpenAIToolProductRuntime(fixture.ctx, fixture.app.openAIToolRuntime); err != nil {
			t.Fatal(err)
		}
		if starts.Load() != 1 {
			t.Fatalf("duplicate recovery starts=%d", starts.Load())
		}
	})

	t.Run("started_stamp_before_goroutine_reopens_and_recovers", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		thread := prepareOpenAIToolActivationFixture(t, &fixture, true)
		var starts atomic.Int32
		startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { starts.Add(1) }
		previousProbe := openAIToolAfterActivationCommitProbe
		t.Cleanup(func() { openAIToolAfterActivationCommitProbe = previousProbe })
		openAIToolAfterActivationCommitProbe = func(scoutAgentThread) error { return errors.New("simulated process crash after activation claim") }
		if err := fixture.app.activateReservedOpenAIToolAgentThread(fixture.ctx, thread, openAIToolSpecFromArtifact(thread.Artifact), "AJ"); err == nil {
			t.Fatal("post-claim crash probe unexpectedly started")
		}
		openAIToolAfterActivationCommitProbe = nil
		if starts.Load() != 0 {
			t.Fatal("worker started before simulated crash")
		}
		fixture.reopen(t)
		if err := fixture.app.installOpenAIToolProductRuntime(fixture.ctx, fixture.app.openAIToolRuntime); err != nil {
			t.Fatalf("reopen recovery: %v", err)
		}
		if starts.Load() != 1 {
			t.Fatalf("reopened starts=%d, want one", starts.Load())
		}
	})

	t.Run("crash_after_provenance_reuses_one_durable_launch_row", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		t.Setenv("USAGE_LEDGER_PATH", t.TempDir())
		thread := prepareOpenAIToolActivationFixture(t, &fixture, true)
		var starts atomic.Int32
		startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { starts.Add(1) }
		previousProbe := openAIToolAfterWorkflowProvenanceProbe
		t.Cleanup(func() { openAIToolAfterWorkflowProvenanceProbe = previousProbe })
		openAIToolAfterWorkflowProvenanceProbe = func(scoutAgentThread) error { return errors.New("simulated crash after durable provenance") }
		if err := fixture.app.activateReservedOpenAIToolAgentThread(fixture.ctx, thread, openAIToolSpecFromArtifact(thread.Artifact), "AJ"); err == nil {
			t.Fatal("post-provenance crash unexpectedly started a worker")
		}
		openAIToolAfterWorkflowProvenanceProbe = nil
		if err := fixture.app.activateReservedOpenAIToolAgentThread(fixture.ctx, thread, openAIToolSpecFromArtifact(thread.Artifact), "AJ"); err != nil {
			t.Fatalf("recover post-provenance activation: %v", err)
		}
		if starts.Load() != 1 {
			t.Fatalf("post-provenance starts=%d, want one", starts.Load())
		}
		key := sha256Hex([]byte("stride-openai-tool-activation-provenance-v1\x00" + thread.Artifact.ID + "\x00" + thread.ID + "\x00" + thread.Artifact.Metadata["operationId"]))
		files, _ := filepath.Glob(filepath.Join(usageLedgerDir(), "eval-*.jsonl"))
		rows := 0
		for _, path := range files {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			rows += bytes.Count(raw, []byte(key))
		}
		if rows != 1 {
			t.Fatalf("durable launch provenance rows=%d, want one", rows)
		}
	})

	t.Run("concurrent_activation_claim_starts_one_worker", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		priorAuthorizer := artifactObjectAuthorizer
		artifactObjectAuthorizer = openAIToolConcurrentTestArtifactAuthorizer{}
		defer func() { artifactObjectAuthorizer = priorAuthorizer }()
		thread := prepareOpenAIToolActivationFixture(t, &fixture, true)
		var starts atomic.Int32
		startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { starts.Add(1) }
		var group sync.WaitGroup
		errorsSeen := make(chan error, 24)
		for index := 0; index < 24; index++ {
			group.Add(1)
			go func() {
				defer group.Done()
				if err := fixture.app.activateReservedOpenAIToolAgentThread(fixture.ctx, thread, openAIToolSpecFromArtifact(thread.Artifact), "AJ"); err != nil {
					errorsSeen <- err
				}
			}()
		}
		group.Wait()
		close(errorsSeen)
		for err := range errorsSeen {
			t.Fatalf("concurrent activation error: %v", err)
		}
		if starts.Load() != 1 {
			t.Fatalf("concurrent starts=%d, want one", starts.Load())
		}
	})
}

func TestOpenAIToolAuthoritySidecarReplayPreservesOriginal(t *testing.T) {
	fixture := newOpenAIToolProductTestFixture(t)
	thread := prepareOpenAIToolActivationFixture(t, &fixture, true)
	path, err := fixture.app.strideE10ScoutAuthorityPath(thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	originalKey := StrideE10TenantAuthorityEnvelopeKey{ID: "openai-tool-activation-envelope", Version: 1, Secret: []byte("openai-tool-activation-envelope-secret-32-bytes")}
	rotatedKey := StrideE10TenantAuthorityEnvelopeKey{ID: "openai-tool-activation-envelope-rotated", Version: 2, Secret: []byte("openai-tool-activation-rotated-secret-32-bytes")}
	restoreRotation := InstallStrideE10TenantAuthorityEnvelopeRuntime(&strideE10TenantEnvelopeTestKeyring{current: rotatedKey, keys: map[string]StrideE10TenantAuthorityEnvelopeKey{originalKey.ID: originalKey, rotatedKey.ID: rotatedKey}})
	defer restoreRotation()
	rotated, err := MintStrideE10TenantAuthorityEnvelopeForSurface(fixture.ctx, fixture.converter, fixture.expectation.SessionDigest, StrideE10TenantSurfaceScout, thread.TenantAuthority.Purpose, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	preserved, err := fixture.app.persistStrideE10ScoutAuthority(thread.ID, &rotated)
	if err != nil || preserved.KeyID != originalKey.ID {
		t.Fatalf("key rotation replaced the immutable run envelope: preserved=%+v err=%v", preserved, err)
	}
	afterRotation, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, afterRotation) {
		t.Fatalf("authority sidecar changed across key rotation: err=%v", err)
	}
	tampered := *thread.TenantAuthority
	tampered.SessionSubjectDigest = sha256Hex([]byte("different-session"))
	tampered.SessionRevision++
	tampered.MAC = strideE10TenantEnvelopeMAC(originalKey, tampered)
	if _, err := fixture.app.persistStrideE10ScoutAuthority(thread.ID, &tampered); err == nil {
		t.Fatal("mismatched replay overwrote the immutable authority sidecar")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("authority sidecar changed after rejected replay: err=%v", err)
	}
	var starts atomic.Int32
	previousStart := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { starts.Add(1) }
	defer func() { startAgentThreadAsync = previousStart }()
	if err := fixture.app.activateReservedOpenAIToolAgentThread(fixture.ctx, thread, openAIToolSpecFromArtifact(thread.Artifact), "AJ"); err != nil {
		t.Fatalf("original authority was not recoverable: %v", err)
	}
	if starts.Load() != 1 {
		t.Fatalf("original recovery starts=%d", starts.Load())
	}
}

func TestOpenAIToolAsyncFailureProjectsNeedsAttention(t *testing.T) {
	fixture := newOpenAIToolProductTestFixture(t)
	thread := prepareOpenAIToolActivationFixture(t, &fixture, true)
	var captured scoutAgentThread
	previousStart := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, launched scoutAgentThread) { captured = launched }
	defer func() { startAgentThreadAsync = previousStart }()
	if err := fixture.app.activateReservedOpenAIToolAgentThread(fixture.ctx, thread, openAIToolSpecFromArtifact(thread.Artifact), "AJ"); err != nil {
		t.Fatal(err)
	}
	if captured.ID == "" {
		t.Fatal("activation did not hand off the exact run")
	}
	// No provider is installed in this fixture. The real async entrypoint must
	// fail closed and replace the running projection with needs-attention.
	fixture.app.runAgentThread(captured)
	artifact, ok := fixture.app.osArtifactByID(thread.Artifact.ID)
	if !ok || artifact.Metadata[openAIToolActivationStateMetadataKey] != openAIToolActivationNeedsAttention || artifact.Metadata["threadStatus"] != "error" || artifact.Metadata["goalStatus"] != "needs_attention" {
		t.Fatalf("async failure left untruthful running work: %+v", artifact.Metadata)
	}
	chat, _, err := fixture.app.scoutChatThreadByID(fixture.expectation.RequesterAccount, fixture.thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	errorCards := 0
	wantCardID := "scout-chat-message-work-" + sha256Hex([]byte(artifact.Metadata["sourceMessageId"] + "\x00" + thread.ID))[:24]
	for _, message := range chat.Messages {
		if message.ID == wantCardID && message.Thread != nil && message.Thread.ID == thread.ID && message.Thread.ArtifactID == artifact.ID && message.Thread.Status == "error" {
			errorCards++
		}
	}
	if errorCards != 1 {
		t.Fatalf("needs-attention cards=%d, want one", errorCards)
	}
}

func TestOpenAIToolRoutedLostResponseResumesBeforeNeedsAttention(t *testing.T) {
	fixture := newOpenAIToolProductTestFixture(t)
	thread := prepareOpenAIToolActivationFixture(t, &fixture, true)
	arguments := json.RawMessage(`{"artifact_id":"` + thread.Artifact.ID + `","title":"Routed restart","content":"# Routed restart\n\nThe exact effect survives one lost response."}`)
	provider := &openAIToolScriptProvider{steps: []openAIToolScriptStep{
		{response: openAIToolFunctionResponse(t, "routed-lost-call", "routed-lost-function", "update_artifact", arguments)},
		{err: errors.New("simulated lost provider response")},
		{response: openAIToolTerminalResponse("routed-lost-terminal", "# Routed restart\n\nThe exact effect survives one lost response.")},
	}}
	fixture.app.openAIToolRuntime.Carrier.Provider = provider
	var captured scoutAgentThread
	previousStart := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, launched scoutAgentThread) { captured = launched }
	defer func() { startAgentThreadAsync = previousStart }()
	if err := fixture.app.activateReservedOpenAIToolAgentThread(fixture.ctx, thread, openAIToolSpecFromArtifact(thread.Artifact), "AJ"); err != nil {
		t.Fatal(err)
	}
	fixture.app.runAgentThread(captured)
	artifact, ok := fixture.app.osArtifactByID(thread.Artifact.ID)
	if !ok || artifact.Metadata[openAIToolActivationStateMetadataKey] != openAIToolActivationComplete || artifact.Metadata["threadStatus"] != "complete" || artifactVersion(artifact) != artifactVersion(thread.Artifact)+1 || artifact.Metadata[openAIToolFinalRunDigestMetadataKey] == "" {
		t.Fatalf("routed lost-response recovery failed: %+v", artifact)
	}
	if provider.calls != 3 {
		t.Fatalf("provider calls=%d, want function + lost response + exact resumed terminal", provider.calls)
	}
}

func TestOpenAIToolTerminalProjectionCrashRecoveryAcrossReopen(t *testing.T) {
	previousStart := startAgentThreadAsync
	defer func() { startAgentThreadAsync = previousStart }()

	t.Run("final_artifact_before_chat_and_journal_recovers_once", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		thread := prepareOpenAIToolActivationFixture(t, &fixture, true)
		arguments := json.RawMessage(`{"artifact_id":"` + thread.Artifact.ID + `","title":"Recovered finalization","content":"# Recovered finalization\n\nExactly one final result."}`)
		provider := &openAIToolScriptProvider{steps: []openAIToolScriptStep{
			{response: openAIToolFunctionResponse(t, "final-crash-call", "final-crash-function", "update_artifact", arguments)},
			{response: openAIToolTerminalResponse("final-crash-terminal", "# Recovered finalization\n\nExactly one final result.")},
		}}
		fixture.app.openAIToolRuntime.Carrier.Provider = provider
		var captured scoutAgentThread
		startAgentThreadAsync = func(_ *kanbanBoardApp, launched scoutAgentThread) { captured = launched }
		if err := fixture.app.activateReservedOpenAIToolAgentThread(fixture.ctx, thread, openAIToolSpecFromArtifact(thread.Artifact), "AJ"); err != nil {
			t.Fatal(err)
		}
		previousProbe := openAIToolAfterFinalArtifactCommitProbe
		t.Cleanup(func() { openAIToolAfterFinalArtifactCommitProbe = previousProbe })
		openAIToolAfterFinalArtifactCommitProbe = func(meetingMemoryEntry) error { return errors.New("simulated crash after final artifact") }
		fixture.app.runAgentThread(captured)
		crashed, _ := fixture.app.osArtifactByID(thread.Artifact.ID)
		if crashed.Metadata[openAIToolActivationStateMetadataKey] != openAIToolActivationFinalizing || crashed.Metadata[openAIToolFinalRunDigestMetadataKey] == "" {
			t.Fatalf("crash did not retain recoverable finalizing state: %+v", crashed.Metadata)
		}
		openAIToolAfterFinalArtifactCommitProbe = nil
		fixture.reopen(t)
		noProvider := &openAIToolScriptProvider{}
		fixture.app.openAIToolRuntime.Carrier.Provider = noProvider
		captured = scoutAgentThread{}
		startAgentThreadAsync = func(_ *kanbanBoardApp, launched scoutAgentThread) { captured = launched }
		if err := fixture.app.installOpenAIToolProductRuntime(fixture.ctx, fixture.app.openAIToolRuntime); err != nil {
			t.Fatalf("reconcile finalizing work: %v", err)
		}
		if captured.ID == "" {
			t.Fatal("finalizing recovery did not resume its exact worker")
		}
		fixture.app.runAgentThread(captured)
		finalArtifact, _ := fixture.app.osArtifactByID(thread.Artifact.ID)
		if finalArtifact.Metadata[openAIToolActivationStateMetadataKey] != openAIToolActivationComplete || noProvider.calls != 0 {
			t.Fatalf("finalizing recovery state=%s provider=%d", finalArtifact.Metadata[openAIToolActivationStateMetadataKey], noProvider.calls)
		}
		chat, _, err := fixture.app.scoutChatThreadByID(fixture.expectation.RequesterAccount, fixture.thread.ID)
		if err != nil {
			t.Fatal(err)
		}
		completeCards := 0
		for _, message := range chat.Messages {
			if message.Thread != nil && message.Thread.ID == thread.ID && message.Thread.ArtifactID == finalArtifact.ID && message.Thread.Status == "complete" {
				completeCards++
			}
		}
		if completeCards != 1 {
			t.Fatalf("recovered complete cards=%d, want one", completeCards)
		}
	})

	t.Run("error_artifact_before_chat_repairs_without_worker", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		thread := prepareOpenAIToolActivationFixture(t, &fixture, true)
		startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
		if err := fixture.app.activateReservedOpenAIToolAgentThread(fixture.ctx, thread, openAIToolSpecFromArtifact(thread.Artifact), "AJ"); err != nil {
			t.Fatal(err)
		}
		previousProbe := openAIToolAfterFailureArtifactCommitProbe
		t.Cleanup(func() { openAIToolAfterFailureArtifactCommitProbe = previousProbe })
		openAIToolAfterFailureArtifactCommitProbe = func(meetingMemoryEntry) error { return errors.New("simulated crash after error artifact") }
		fixture.app.failOpenAIToolAgentThread(fixture.ctx, thread, errors.New("provider unavailable"))
		errored, _ := fixture.app.osArtifactByID(thread.Artifact.ID)
		if errored.Metadata[openAIToolActivationStateMetadataKey] != openAIToolActivationNeedsAttention {
			t.Fatalf("error state was not durable: %+v", errored.Metadata)
		}
		openAIToolAfterFailureArtifactCommitProbe = nil
		_ = fixture.journal.Close()
		memory, err := newMeetingMemoryStore(fixture.app.memory.path)
		if err != nil {
			t.Fatal(err)
		}
		journal, err := openOpenAIToolJournal(fixture.ctx, fixture.journalDir, "product-journal", fixture.highWater, fixture.keyring)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = journal.Close() })
		manifest, _ := buildOpenAIToolManifest()
		fixture.app = &kanbanBoardApp{memory: memory, goalStartedChildren: map[string]struct{}{}, openAIToolActiveRuns: map[string]struct{}{}, openAIToolActivationOwner: "reopened-error-process", scoutInvocation: NewSTRIDEScoutInvocationMachine(20 * time.Second)}
		fixture.app.openAIToolRuntime = &openAIToolProductRuntime{Enabled: true, Carrier: &openAIToolLoopCarrier{Enabled: true, Manifest: manifest, Journal: journal}}
		fixture.journal = journal
		var starts atomic.Int32
		startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { starts.Add(1) }
		if err := fixture.app.installOpenAIToolProductRuntime(fixture.ctx, fixture.app.openAIToolRuntime); err != nil {
			t.Fatalf("repair error projection: %v", err)
		}
		chat, _, err := fixture.app.scoutChatThreadByID(fixture.expectation.RequesterAccount, fixture.thread.ID)
		if err != nil {
			t.Fatal(err)
		}
		errorCards := 0
		for _, message := range chat.Messages {
			if message.Thread != nil && message.Thread.ID == thread.ID && message.Thread.Status == "error" {
				errorCards++
			}
		}
		if starts.Load() != 0 || errorCards != 1 {
			t.Fatalf("error recovery starts=%d cards=%d", starts.Load(), errorCards)
		}
	})
}

func TestOpenAIToolConcurrentArtifactRevisionDuringProviderFailsFinalUse(t *testing.T) {
	fixture := newOpenAIToolProductTestFixture(t)
	manifest, err := buildOpenAIToolManifest()
	if err != nil {
		t.Fatal(err)
	}
	providerEntered := make(chan struct{})
	releaseProvider := make(chan struct{})
	provider := &openAIToolScriptProvider{
		steps: []openAIToolScriptStep{{response: openAIToolTerminalResponse("product-concurrent-terminal", "# Provider result\n\nMust not overwrite the concurrent revision.")}},
		inspect: func(_ int, _ openAIResponsesToolRequest) error {
			close(providerEntered)
			<-releaseProvider
			return nil
		},
	}
	carrier := &openAIToolLoopCarrier{
		Enabled: true, Manifest: manifest, Journal: fixture.journal, Provider: provider,
		Authority: &openAIToolProductAuthorityLease{backend: fixture.backend}, Executor: openAIToolEffectAdapter{Backend: fixture.backend}, Finalizer: &openAIToolProductFinalizer{backend: fixture.backend},
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := carrier.Run(fixture.ctx, openAIToolLoopRequest{Instructions: "Use only admitted tools.", UserTurn: "Finish the exact work.", Expectation: fixture.expectation})
		done <- runErr
	}()
	<-providerEntered
	updateFinished := make(chan struct{})
	var updateErr error
	go func() {
		defer close(updateFinished)
		current, ok := fixture.app.osArtifactByID(fixture.artifact.ID)
		if !ok {
			updateErr = errors.New("work artifact disappeared")
			return
		}
		header := artifactAuthorizationHeaderFromEntry(current)
		_, changed, err := fixture.app.memory.updateOSArtifactWithMetadataIfHeaderMatches(header, current.ID, "Concurrent human revision", "# Concurrent human revision\n\nThis body must survive.", "AJ", nil)
		if err == nil && !changed {
			err = errors.New("concurrent revision did not commit")
		}
		updateErr = err
	}()
	previousFinalProbe := openAIToolBeforeFinalArtifactCASProbe
	openAIToolBeforeFinalArtifactCASProbe = func() {
		<-updateFinished
		if updateErr != nil {
			t.Errorf("commit concurrent revision: %v", updateErr)
		}
	}
	defer func() { openAIToolBeforeFinalArtifactCASProbe = previousFinalProbe }()
	close(releaseProvider)
	if err := <-done; err == nil {
		t.Fatal("provider terminal overwrote a changed artifact revision")
	}
	<-updateFinished
	if updateErr != nil {
		t.Fatalf("commit concurrent revision: %v", updateErr)
	}
	final, _ := fixture.app.osArtifactByID(fixture.artifact.ID)
	if final.Metadata["title"] != "Concurrent human revision" || !strings.Contains(final.Text, "must survive") || final.Metadata[openAIToolFinalRunDigestMetadataKey] != "" || final.Metadata[openAIToolFinalUseDigestMetadataKey] != "" || final.Metadata[openAIToolFanOutDigestMetadataKey] != "" {
		t.Fatalf("concurrent revision or zero-final-use fence failed: %+v", final)
	}
}

func TestOpenAIToolResumeToProviderSourceDriftHasZeroProviderCalls(t *testing.T) {
	previousAdmissionProbe, previousProjectionProbe := openAIToolBeforeProviderAdmissionProbe, openAIToolProjectionEventProbe
	defer func() {
		openAIToolBeforeProviderAdmissionProbe, openAIToolProjectionEventProbe = previousAdmissionProbe, previousProjectionProbe
	}()

	t.Run("created_artifact_revision", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		manifest, _ := buildOpenAIToolManifest()
		createdID := ""
		openAIToolProjectionEventProbe = func(receipt openAIToolProductEffectReceipt) {
			if receipt.ToolName == "create_artifact" {
				createdID = receipt.ArtifactID
			}
		}
		firstProvider := &openAIToolScriptProvider{steps: []openAIToolScriptStep{
			{response: openAIToolFunctionResponse(t, "resume-gap-create-call", "resume-gap-create-function", "create_artifact", json.RawMessage(`{"mode":"artifacts","query":"Create resume-gap artifact","content":"Exact resume-gap body."}`))},
			{err: errors.New("stop before the next provider turn")},
		}}
		carrier := &openAIToolLoopCarrier{Enabled: true, Manifest: manifest, Journal: fixture.journal, Authority: &openAIToolProductAuthorityLease{backend: fixture.backend}, Provider: firstProvider, Executor: openAIToolEffectAdapter{Backend: fixture.backend}, Finalizer: &openAIToolProductFinalizer{backend: fixture.backend}}
		request := openAIToolLoopRequest{Instructions: "Use only admitted tools.", UserTurn: "Create then continue exactly.", Expectation: fixture.expectation}
		if _, err := carrier.Run(fixture.ctx, request); err == nil || createdID == "" {
			t.Fatalf("setup did not stop with a created artifact: id=%q err=%v", createdID, err)
		}
		openAIToolBeforeProviderAdmissionProbe = func() {
			created, ok := fixture.app.osArtifactByID(createdID)
			if !ok {
				return
			}
			header := artifactAuthorizationHeaderFromEntry(created)
			_, _, _ = fixture.app.memory.updateOSArtifactWithMetadataIfHeaderMatches(header, created.ID, "", created.Text+"\n\nChanged before provider admission.", "AJ", nil)
		}
		secondProvider := &openAIToolScriptProvider{steps: []openAIToolScriptStep{{response: openAIToolTerminalResponse("resume-gap-create-terminal", "Never admitted.")}}}
		carrier.Provider = secondProvider
		if _, err := carrier.Run(fixture.ctx, request); err == nil || secondProvider.calls != 0 {
			t.Fatalf("stale created-artifact output reached provider: calls=%d err=%v", secondProvider.calls, err)
		}
		openAIToolBeforeProviderAdmissionProbe = nil
	})

	t.Run("memory_high_water", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		if _, appended, err := fixture.app.memory.appendNote("openai-tool-resume-gap-memory-source", "The exact decision is default off.", map[string]string{"tenantId": fixture.expectation.TenantID, "visibility": "organization", "createdBy": "AJ"}); err != nil || !appended {
			t.Fatal(err)
		}
		manifest, _ := buildOpenAIToolManifest()
		firstProvider := &openAIToolScriptProvider{steps: []openAIToolScriptStep{
			{response: openAIToolFunctionResponse(t, "resume-gap-memory-call", "resume-gap-memory-function", "answer_memory_question", json.RawMessage(`{"query":"What is the exact decision?"}`))},
			{err: errors.New("stop before the next provider turn")},
		}}
		carrier := &openAIToolLoopCarrier{Enabled: true, Manifest: manifest, Journal: fixture.journal, Authority: &openAIToolProductAuthorityLease{backend: fixture.backend}, Provider: firstProvider, Executor: openAIToolEffectAdapter{Backend: fixture.backend}, Finalizer: &openAIToolProductFinalizer{backend: fixture.backend}}
		request := openAIToolLoopRequest{Instructions: "Use only admitted tools.", UserTurn: "Recall then continue exactly.", Expectation: fixture.expectation}
		if _, err := carrier.Run(fixture.ctx, request); err == nil {
			t.Fatal("setup did not stop after the memory tool")
		}
		openAIToolBeforeProviderAdmissionProbe = func() {
			_, _, _ = fixture.app.memory.appendNote("openai-tool-resume-gap-memory-race", "Changed before provider admission.", map[string]string{"tenantId": fixture.expectation.TenantID, "visibility": "organization", "createdBy": "AJ"})
		}
		secondProvider := &openAIToolScriptProvider{steps: []openAIToolScriptStep{{response: openAIToolTerminalResponse("resume-gap-memory-terminal", "Never admitted.")}}}
		carrier.Provider = secondProvider
		if _, err := carrier.Run(fixture.ctx, request); err == nil || secondProvider.calls != 0 {
			t.Fatalf("stale memory output reached provider: calls=%d err=%v", secondProvider.calls, err)
		}
		openAIToolBeforeProviderAdmissionProbe = nil
	})
}

func TestOpenAIToolFinalizationFencesFullMetadataSecondaryArtifactsAndMemory(t *testing.T) {
	previousProbe := openAIToolBeforeFinalArtifactCASProbe
	previousProofProbe := openAIToolAfterOperationProofProbe
	defer func() {
		openAIToolBeforeFinalArtifactCASProbe = previousProbe
		openAIToolAfterOperationProofProbe = previousProofProbe
	}()

	assertNoFinalUse := func(t *testing.T, fixture *openAIToolProductTestFixture) {
		t.Helper()
		artifact, _ := fixture.app.osArtifactByID(fixture.artifact.ID)
		if artifact.Metadata[openAIToolFinalRunDigestMetadataKey] != "" || artifact.Metadata[openAIToolFinalUseDigestMetadataKey] != "" || artifact.Metadata[openAIToolFanOutDigestMetadataKey] != "" {
			t.Fatalf("failed fence committed terminal artifact: %+v", artifact.Metadata)
		}
		thread, _, err := fixture.app.scoutChatThreadByID(fixture.expectation.RequesterAccount, fixture.thread.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range thread.Messages {
			if message.Thread != nil && message.Thread.ID == fixture.expectation.ThreadID && message.Thread.Status == "complete" {
				t.Fatal("failed fence committed a complete chat projection")
			}
		}
		fixture.journal.mu.Lock()
		defer fixture.journal.mu.Unlock()
		for _, record := range fixture.journal.state.Records {
			if record.FinalUseDigest != "" || record.State == openAIToolStateCompleted {
				t.Fatalf("failed fence completed journal operation %+v", record)
			}
		}
	}

	t.Run("receipt_metadata_changes_after_validation", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		manifest, _ := buildOpenAIToolManifest()
		provider := &openAIToolScriptProvider{steps: []openAIToolScriptStep{{response: openAIToolTerminalResponse("metadata-fence-terminal", "# Metadata fence\n\nNo terminal effect.")}}}
		carrier := &openAIToolLoopCarrier{Enabled: true, Manifest: manifest, Journal: fixture.journal, Authority: &openAIToolProductAuthorityLease{backend: fixture.backend}, Provider: provider, Executor: openAIToolEffectAdapter{Backend: fixture.backend}, Finalizer: &openAIToolProductFinalizer{backend: fixture.backend}}
		openAIToolBeforeFinalArtifactCASProbe = func() {
			current, _ := fixture.app.osArtifactByID(fixture.artifact.ID)
			header := artifactAuthorizationHeaderFromEntry(current)
			_, _, _ = fixture.app.memory.updateOSArtifactMetadataIfHeaderMatches(header, current.ID, map[string]string{"openAIToolConcurrentReceiptTamper": "changed"})
		}
		_, err := carrier.Run(fixture.ctx, openAIToolLoopRequest{Instructions: "Use only admitted tools.", UserTurn: "Finish without tools.", Expectation: fixture.expectation})
		if err == nil {
			t.Fatal("full-metadata change crossed terminal CAS")
		}
		assertNoFinalUse(t, &fixture)
	})

	t.Run("created_artifact_changes_after_operation_revalidation", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		manifest, _ := buildOpenAIToolManifest()
		provider := &openAIToolScriptProvider{steps: []openAIToolScriptStep{
			{response: openAIToolFunctionResponse(t, "create-fence-call", "create-fence-function", "create_artifact", json.RawMessage(`{"mode":"artifacts","query":"Create fenced artifact","content":"Exact created body."}`))},
			{response: openAIToolTerminalResponse("create-fence-terminal", "# Created work\n\nNo stale final result.")},
		}}
		carrier := &openAIToolLoopCarrier{Enabled: true, Manifest: manifest, Journal: fixture.journal, Authority: &openAIToolProductAuthorityLease{backend: fixture.backend}, Provider: provider, Executor: openAIToolEffectAdapter{Backend: fixture.backend}, Finalizer: &openAIToolProductFinalizer{backend: fixture.backend}}
		openAIToolBeforeFinalArtifactCASProbe = func() {
			for _, entry := range fixture.app.memory.snapshot(0) {
				if entry.Kind != meetingMemoryKindOSArtifact || entry.ID == fixture.artifact.ID || strings.TrimSpace(entry.Metadata[openAIToolOperationReceiptsMetadataKey]) == "" {
					continue
				}
				header := artifactAuthorizationHeaderFromEntry(entry)
				_, _, _ = fixture.app.memory.updateOSArtifactWithMetadataIfHeaderMatches(header, entry.ID, "", entry.Text+"\n\nConcurrent edit.", "AJ", nil)
				return
			}
		}
		_, err := carrier.Run(fixture.ctx, openAIToolLoopRequest{Instructions: "Use only admitted tools.", UserTurn: "Create and finish.", Expectation: fixture.expectation})
		if err == nil {
			t.Fatal("created-artifact change crossed terminal store fence")
		}
		assertNoFinalUse(t, &fixture)
	})

	t.Run("memory_high_water_changes_after_operation_revalidation", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		if _, appended, err := fixture.app.memory.appendNote("openai-tool-final-memory-source", "The exact decision is default off.", map[string]string{"tenantId": fixture.expectation.TenantID, "visibility": "organization", "createdBy": "AJ"}); err != nil || !appended {
			t.Fatal(err)
		}
		manifest, _ := buildOpenAIToolManifest()
		provider := &openAIToolScriptProvider{steps: []openAIToolScriptStep{
			{response: openAIToolFunctionResponse(t, "memory-fence-call", "memory-fence-function", "answer_memory_question", json.RawMessage(`{"query":"What is the exact decision?"}`))},
			{response: openAIToolTerminalResponse("memory-fence-terminal", "# Memory result\n\nNo stale final result.")},
		}}
		carrier := &openAIToolLoopCarrier{Enabled: true, Manifest: manifest, Journal: fixture.journal, Authority: &openAIToolProductAuthorityLease{backend: fixture.backend}, Provider: provider, Executor: openAIToolEffectAdapter{Backend: fixture.backend}, Finalizer: &openAIToolProductFinalizer{backend: fixture.backend}}
		openAIToolBeforeFinalArtifactCASProbe = func() {
			_, _, _ = fixture.app.memory.appendNote("openai-tool-final-memory-race", "A concurrent authorized memory append.", map[string]string{"tenantId": fixture.expectation.TenantID, "visibility": "organization", "createdBy": "AJ"})
		}
		_, err := carrier.Run(fixture.ctx, openAIToolLoopRequest{Instructions: "Use only admitted tools.", UserTurn: "Recall and finish.", Expectation: fixture.expectation})
		if err == nil {
			t.Fatal("memory high-water change crossed terminal store fence")
		}
		assertNoFinalUse(t, &fixture)
	})

	t.Run("created_artifact_changes_between_proof_and_generation", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		manifest, _ := buildOpenAIToolManifest()
		createdID := ""
		previousProjectionProbe := openAIToolProjectionEventProbe
		openAIToolProjectionEventProbe = func(receipt openAIToolProductEffectReceipt) {
			if receipt.ToolName == "create_artifact" {
				createdID = receipt.ArtifactID
			}
		}
		defer func() { openAIToolProjectionEventProbe = previousProjectionProbe }()
		provider := &openAIToolScriptProvider{steps: []openAIToolScriptStep{
			{response: openAIToolFunctionResponse(t, "proof-gap-create-call", "proof-gap-create-function", "create_artifact", json.RawMessage(`{"mode":"artifacts","query":"Create proof-gap artifact","content":"Exact proof-gap body."}`))},
			{response: openAIToolTerminalResponse("proof-gap-create-terminal", "# Proof gap\n\nNo stale terminal result.")},
		}}
		carrier := &openAIToolLoopCarrier{Enabled: true, Manifest: manifest, Journal: fixture.journal, Authority: &openAIToolProductAuthorityLease{backend: fixture.backend}, Provider: provider, Executor: openAIToolEffectAdapter{Backend: fixture.backend}, Finalizer: &openAIToolProductFinalizer{backend: fixture.backend}}
		openAIToolAfterOperationProofProbe = func() {
			created, ok := fixture.app.osArtifactByID(createdID)
			if !ok {
				return
			}
			header := artifactAuthorizationHeaderFromEntry(created)
			_, _, _ = fixture.app.memory.updateOSArtifactWithMetadataIfHeaderMatches(header, created.ID, "", created.Text+"\n\nConcurrent proof-gap revision.", "AJ", nil)
		}
		_, err := carrier.Run(fixture.ctx, openAIToolLoopRequest{Instructions: "Use only admitted tools.", UserTurn: "Create and finish with a stable proof.", Expectation: fixture.expectation})
		if err == nil {
			t.Fatal("created-artifact proof/generation gap was silently blessed")
		}
		assertNoFinalUse(t, &fixture)
		openAIToolAfterOperationProofProbe = nil
	})

	t.Run("memory_changes_between_proof_and_generation", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		if _, appended, err := fixture.app.memory.appendNote("openai-tool-proof-gap-memory-source", "The exact decision is default off.", map[string]string{"tenantId": fixture.expectation.TenantID, "visibility": "organization", "createdBy": "AJ"}); err != nil || !appended {
			t.Fatal(err)
		}
		manifest, _ := buildOpenAIToolManifest()
		provider := &openAIToolScriptProvider{steps: []openAIToolScriptStep{
			{response: openAIToolFunctionResponse(t, "proof-gap-memory-call", "proof-gap-memory-function", "answer_memory_question", json.RawMessage(`{"query":"What is the exact decision?"}`))},
			{response: openAIToolTerminalResponse("proof-gap-memory-terminal", "# Proof gap memory\n\nNo stale terminal result.")},
		}}
		carrier := &openAIToolLoopCarrier{Enabled: true, Manifest: manifest, Journal: fixture.journal, Authority: &openAIToolProductAuthorityLease{backend: fixture.backend}, Provider: provider, Executor: openAIToolEffectAdapter{Backend: fixture.backend}, Finalizer: &openAIToolProductFinalizer{backend: fixture.backend}}
		openAIToolAfterOperationProofProbe = func() {
			_, _, _ = fixture.app.memory.appendNote("openai-tool-proof-gap-memory-race", "Concurrent proof-gap memory.", map[string]string{"tenantId": fixture.expectation.TenantID, "visibility": "organization", "createdBy": "AJ"})
		}
		_, err := carrier.Run(fixture.ctx, openAIToolLoopRequest{Instructions: "Use only admitted tools.", UserTurn: "Recall and finish with a stable proof.", Expectation: fixture.expectation})
		if err == nil {
			t.Fatal("memory proof/generation gap was silently blessed")
		}
		assertNoFinalUse(t, &fixture)
		openAIToolAfterOperationProofProbe = nil
	})
}

func TestOpenAIToolTerminalTransactionHoldsSourceFenceThroughFanOut(t *testing.T) {
	previousFinalProbe, previousProjectionProbe := openAIToolAfterFinalArtifactCommitProbe, openAIToolProjectionEventProbe
	defer func() {
		openAIToolAfterFinalArtifactCommitProbe, openAIToolProjectionEventProbe = previousFinalProbe, previousProjectionProbe
	}()

	t.Run("created_artifact_revision_waits_until_after_fanout", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		manifest, _ := buildOpenAIToolManifest()
		createdArtifactID := ""
		openAIToolProjectionEventProbe = func(receipt openAIToolProductEffectReceipt) {
			if receipt.ToolName == "create_artifact" {
				createdArtifactID = receipt.ArtifactID
			}
		}
		mutationStarted := make(chan struct{})
		mutationDone := make(chan error, 1)
		openAIToolAfterFinalArtifactCommitProbe = func(meetingMemoryEntry) error {
			if createdArtifactID == "" {
				return errors.New("created artifact was not projected before finalization")
			}
			go func() {
				close(mutationStarted)
				created, ok := fixture.app.osArtifactByID(createdArtifactID)
				if !ok {
					mutationDone <- errors.New("created artifact disappeared")
					return
				}
				header := artifactAuthorizationHeaderFromEntry(created)
				_, changed, err := fixture.app.memory.updateOSArtifactWithMetadataIfHeaderMatches(header, created.ID, "", created.Text+"\n\nAuthorized later revision.", "AJ", nil)
				if err == nil && !changed {
					err = errors.New("created artifact revision did not commit")
				}
				mutationDone <- err
			}()
			<-mutationStarted
			select {
			case err := <-mutationDone:
				return fmt.Errorf("created artifact changed inside terminal fan-out fence: %v", err)
			default:
				return nil
			}
		}
		provider := &openAIToolScriptProvider{steps: []openAIToolScriptStep{
			{response: openAIToolFunctionResponse(t, "fanout-create-call", "fanout-create-function", "create_artifact", json.RawMessage(`{"mode":"artifacts","query":"Create fan-out fence proof","content":"Exact pre-terminal artifact."}`))},
			{response: openAIToolTerminalResponse("fanout-create-terminal", "# Fan-out fence\n\nTerminal result is exact.")},
		}}
		carrier := &openAIToolLoopCarrier{Enabled: true, Manifest: manifest, Journal: fixture.journal, Authority: &openAIToolProductAuthorityLease{backend: fixture.backend}, Provider: provider, Executor: openAIToolEffectAdapter{Backend: fixture.backend}, Finalizer: &openAIToolProductFinalizer{backend: fixture.backend}}
		result, err := carrier.Run(fixture.ctx, openAIToolLoopRequest{Instructions: "Use only admitted tools.", UserTurn: "Create then finish exactly.", Expectation: fixture.expectation})
		if err != nil || result.Text == "" {
			t.Fatalf("terminal transaction failed: result=%+v err=%v", result, err)
		}
		if mutationErr := <-mutationDone; mutationErr != nil {
			t.Fatalf("post-fanout created artifact revision: %v", mutationErr)
		}
	})

	t.Run("memory_append_waits_until_after_fanout", func(t *testing.T) {
		fixture := newOpenAIToolProductTestFixture(t)
		if _, appended, err := fixture.app.memory.appendNote("openai-tool-fanout-memory-source", "The exact launch decision is default off.", map[string]string{"tenantId": fixture.expectation.TenantID, "visibility": "organization", "createdBy": "AJ"}); err != nil || !appended {
			t.Fatal(err)
		}
		manifest, _ := buildOpenAIToolManifest()
		mutationStarted := make(chan struct{})
		mutationDone := make(chan error, 1)
		openAIToolProjectionEventProbe = nil
		openAIToolAfterFinalArtifactCommitProbe = func(meetingMemoryEntry) error {
			go func() {
				close(mutationStarted)
				_, appended, err := fixture.app.memory.appendNote("openai-tool-fanout-memory-later", "Authorized memory after terminal fan-out.", map[string]string{"tenantId": fixture.expectation.TenantID, "visibility": "organization", "createdBy": "AJ"})
				if err == nil && !appended {
					err = errors.New("later memory entry did not append")
				}
				mutationDone <- err
			}()
			<-mutationStarted
			select {
			case err := <-mutationDone:
				return fmt.Errorf("memory changed inside terminal fan-out fence: %v", err)
			default:
				return nil
			}
		}
		provider := &openAIToolScriptProvider{steps: []openAIToolScriptStep{
			{response: openAIToolFunctionResponse(t, "fanout-memory-call", "fanout-memory-function", "answer_memory_question", json.RawMessage(`{"query":"What is the exact launch decision?"}`))},
			{response: openAIToolTerminalResponse("fanout-memory-terminal", "# Memory fence\n\nTerminal result is exact.")},
		}}
		carrier := &openAIToolLoopCarrier{Enabled: true, Manifest: manifest, Journal: fixture.journal, Authority: &openAIToolProductAuthorityLease{backend: fixture.backend}, Provider: provider, Executor: openAIToolEffectAdapter{Backend: fixture.backend}, Finalizer: &openAIToolProductFinalizer{backend: fixture.backend}}
		result, err := carrier.Run(fixture.ctx, openAIToolLoopRequest{Instructions: "Use only admitted tools.", UserTurn: "Recall then finish exactly.", Expectation: fixture.expectation})
		if err != nil || result.Text == "" {
			t.Fatalf("terminal memory transaction failed: result=%+v err=%v", result, err)
		}
		if mutationErr := <-mutationDone; mutationErr != nil {
			t.Fatalf("post-fanout memory append: %v", mutationErr)
		}
	})
}

func TestOpenAIToolProductAuthorityRevocationAndWrongScopeFailClosed(t *testing.T) {
	fixture := newOpenAIToolProductTestFixture(t)
	request := fixture.request(t, "update_artifact", map[string]any{"artifact_id": fixture.artifact.ID, "title": "Never committed", "content": nil}, "openai-tool-operation-revoked")
	next := strideE10TenantTestSnapshot(time.Now().UTC())
	next.SessionHash = fixture.expectation.SessionDigest
	next.ActiveSession.SessionSubjectDigest = fixture.expectation.SessionDigest
	next.Session.AccountSubjectDigest = sha256Hex([]byte(fixture.expectation.RequesterAccount))
	next.Person.AccountSubjectDigest = next.Session.AccountSubjectDigest
	next.Legacy.AccountSubjectDigest = next.Session.AccountSubjectDigest
	next.Session.ActiveOrganizationSessionRev++
	next.ActiveSession.SessionRevision++
	next.ActiveSession.Header.Revision++
	next.Session.AuthorityGeneration++
	next.Generation++
	fixture.resolver.set(next, nil)
	lease := &openAIToolProductAuthorityLease{backend: fixture.backend}
	called := false
	err := lease.WithCurrentOpenAIToolAuthority(fixture.ctx, fixture.expectation, func(leaseContext context.Context, current openAIToolCurrentAuthority) error {
		called = true
		_, applyErr := fixture.backend.UpdateAuthorizedArtifact(leaseContext, request)
		return applyErr
	})
	if err == nil || called {
		t.Fatalf("revoked/restarted authority admitted: called=%v err=%v", called, err)
	}
	artifact, ok := fixture.app.osArtifactByID(fixture.artifact.ID)
	if !ok || artifact.Metadata["title"] == "Never committed" {
		t.Fatal("revoked authority mutated artifact")
	}
}

func TestOpenAIToolProductEffectReceiptTamperFailsClosed(t *testing.T) {
	fixture := newOpenAIToolProductTestFixture(t)
	request := fixture.request(t, "update_artifact", map[string]any{"artifact_id": fixture.artifact.ID, "title": "Receipted", "content": "# Receipted\n\nExact."}, "openai-tool-operation-receipt-tamper")
	if _, err := fixture.backend.UpdateAuthorizedArtifact(fixture.ctx, request); err != nil {
		t.Fatalf("apply update: %v", err)
	}
	artifact, ok := fixture.app.osArtifactByID(fixture.artifact.ID)
	if !ok {
		t.Fatal("updated artifact missing")
	}
	header := artifactAuthorizationHeaderFromEntry(artifact)
	duplicate := `{"` + request.OperationID + `":{},"` + request.OperationID + `":{}}`
	if _, changed, err := fixture.app.memory.updateOSArtifactMetadataIfHeaderMatches(header, artifact.ID, map[string]string{openAIToolOperationReceiptsMetadataKey: duplicate}); err != nil || !changed {
		t.Fatalf("install tamper fixture: changed=%v err=%v", changed, err)
	}
	reconciled, err := fixture.backend.ReconcileArtifactUpdate(fixture.ctx, request)
	if err != nil || reconciled.Status != openAIToolReconciliationAmbiguous {
		t.Fatalf("duplicate-key receipt tamper did not fail closed: %+v err=%v", reconciled, err)
	}
	finalizer := &openAIToolProductFinalizer{backend: fixture.backend}
	if _, err := finalizer.FinalizeOpenAIToolRun(fixture.ctx, &openAIToolProductCurrentAuthority{backend: fixture.backend}, fixture.expectation, "tampered-receipt-final-run", "# Must not land\n\nReceipt tamper blocks final use.", []string{request.OperationID}); err == nil {
		t.Fatal("tampered product receipt admitted terminal final use")
	}
	after, _ := fixture.app.osArtifactByID(artifact.ID)
	if strings.Contains(after.Text, "Must not land") || after.Metadata[openAIToolFinalRunDigestMetadataKey] != "" || after.Metadata[openAIToolFanOutDigestMetadataKey] != "" {
		t.Fatalf("receipt tamper produced a final effect: %+v", after)
	}
}

func TestOpenAIToolProductArtifactPostimageTamperFailsClosed(t *testing.T) {
	fixture := newOpenAIToolProductTestFixture(t)
	request := fixture.request(t, "update_artifact", map[string]any{"artifact_id": fixture.artifact.ID, "title": "Receipted", "content": "# Receipted\n\nExact."}, "openai-tool-operation-postimage-tamper")
	if _, err := fixture.backend.UpdateAuthorizedArtifact(fixture.ctx, request); err != nil {
		t.Fatalf("apply update: %v", err)
	}
	fixture.app.memory.mu.Lock()
	mutated := false
	for index := range fixture.app.memory.entries {
		entry := &fixture.app.memory.entries[index]
		if entry.Kind == meetingMemoryKindOSArtifact && entry.ID == fixture.artifact.ID {
			entry.Text = "# Receipted\n\nDifferent bytes with the original receipt and header."
			mutated = true
			break
		}
	}
	fixture.app.memory.mu.Unlock()
	if !mutated {
		t.Fatal("artifact tamper fixture was not installed")
	}
	reconciled, err := fixture.backend.ReconcileArtifactUpdate(fixture.ctx, request)
	if err != nil || reconciled.Status != openAIToolReconciliationAmbiguous {
		t.Fatalf("artifact postimage tamper did not fail closed: %+v err=%v", reconciled, err)
	}
}

func TestOpenAIToolProductEffectReceiptForgeryFailsAuthentication(t *testing.T) {
	fixture := newOpenAIToolProductTestFixture(t)
	request := fixture.request(t, "update_artifact", map[string]any{"artifact_id": fixture.artifact.ID, "title": "Receipted", "content": "# Receipted\n\nExact."}, "openai-tool-operation-receipt-forgery")
	if _, err := fixture.backend.UpdateAuthorizedArtifact(fixture.ctx, request); err != nil {
		t.Fatalf("apply update: %v", err)
	}
	artifact, ok := fixture.app.osArtifactByID(fixture.artifact.ID)
	if !ok {
		t.Fatal("updated artifact missing")
	}
	receipts, err := openAIToolProductReceipts(artifact)
	if err != nil {
		t.Fatal(err)
	}
	forged := receipts[request.OperationID]
	forged.PostimageDigest = strings.Repeat("f", 64)
	forged.ReconciliationHash, err = openAIToolCanonicalDigestOnly(map[string]any{
		"domain": "stride-openai-tool-product-effect-v1", "operation_id": forged.OperationID, "tool": forged.ToolName,
		"artifact_id": forged.ArtifactID, "expectation": forged.ExpectationDigest, "preimage": forged.PreimageDigest,
		"prior_postimage": forged.PriorPostimage, "postimage": forged.PostimageDigest, "output": json.RawMessage(forged.FunctionOutput),
	})
	if err != nil {
		t.Fatal(err)
	}
	receipts[request.OperationID] = forged
	encoded, err := encodeOpenAIToolProductReceipts(receipts)
	if err != nil {
		t.Fatal(err)
	}
	header := artifactAuthorizationHeaderFromEntry(artifact)
	if _, changed, err := fixture.app.memory.updateOSArtifactMetadataIfHeaderMatches(header, artifact.ID, map[string]string{openAIToolOperationReceiptsMetadataKey: encoded}); err != nil || !changed {
		t.Fatalf("install forged authenticated receipt: changed=%v err=%v", changed, err)
	}
	reconciled, err := fixture.backend.ReconcileArtifactUpdate(fixture.ctx, request)
	if err != nil || reconciled.Status != openAIToolReconciliationAmbiguous {
		t.Fatalf("self-consistent public receipt forgery was trusted: %+v err=%v", reconciled, err)
	}
}

func TestOpenAIToolConversationCutoverReachesCarrierAndFanoutWithoutNestedLeaseDeadlock(t *testing.T) {
	_ = newIsolatedWebsocketServer(t)
	t.Setenv("BONFIRE_TENANT_ID", "org-one")
	t.Setenv("BONFIRE_CANONICAL_TENANT_ID", "org-one")
	app := kanbanApp
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("AJ test account is unavailable")
	}
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Secure conversation carrier", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	token := "openai-tool-product-canonical-session-token"
	sessionHash := hashResetToken(token)
	digest := sha256Hex([]byte(normalizeAccountEmail(user.Email)))
	snapshot := strideE10TenantTestSnapshot(now)
	snapshot.SessionHash = sessionHash
	snapshot.Session.Email = user.Email
	snapshot.Session.AccountSubjectDigest = digest
	snapshot.Session.AuthorityGeneration = snapshot.Generation
	snapshot.Session.Expires = now.Add(time.Hour)
	snapshot.Person.AccountSubjectDigest = digest
	snapshot.Legacy.AccountSubjectDigest = digest
	snapshot.ActiveSession.SessionSubjectDigest = sessionHash
	snapshot.ActiveSession.ExpiresAt = snapshot.Session.Expires

	sessions := userSessionStore()
	sessions.mu.Lock()
	sessions.sessions[sessionHash] = snapshot.Session
	sessions.mu.Unlock()
	organizations := NewOrganizationAuthorityService()
	organizations.mu.Lock()
	organizations.persons[snapshot.Person.Header.ID] = clonePersonPrincipal(snapshot.Person)
	organizations.accountPersons[digest] = snapshot.Person.Header.ID
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
		&strideE10TenantTestLegacyIDs{persons: map[string]string{digest: snapshot.Person.Header.ID}},
		StrideE10TenantReceiptKey{ID: "openai-tool-main-receipt", Version: 1, Secret: []byte("openai-tool-main-receipt-secret-32-bytes")},
		StrideE10TenantConversionCutover,
	)
	converter.now = func() time.Time { return now }
	restoreConverter := InstallStrideE10TenantRuntimeConverter(converter)
	t.Cleanup(restoreConverter)
	envelopeKey := StrideE10TenantAuthorityEnvelopeKey{ID: "openai-tool-main-envelope", Version: 1, Secret: []byte("openai-tool-main-envelope-secret-32-bytes")}
	restoreEnvelope := InstallStrideE10TenantAuthorityEnvelopeRuntime(&strideE10TenantEnvelopeTestKeyring{current: envelopeKey, keys: map[string]StrideE10TenantAuthorityEnvelopeKey{envelopeKey.ID: envelopeKey}})
	t.Cleanup(restoreEnvelope)
	previousAuthorizer := artifactObjectAuthorizer
	artifactObjectAuthorizer = &strideE10TenantTestArtifactAuthorizer{}
	t.Cleanup(func() { artifactObjectAuthorizer = previousAuthorizer })

	journal, err := openOpenAIToolJournal(context.Background(), openAIToolSecureTestDirectory(t), "conversation-product-journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	manifest, err := buildOpenAIToolManifest()
	if err != nil {
		t.Fatal(err)
	}
	provider := &openAIToolScriptProvider{steps: []openAIToolScriptStep{
		{response: openAIToolFunctionResponse(t, "conversation-product-progress", "conversation-product-call", controlToolReportGoalState, json.RawMessage(`{"goal_status":"review","review_gate":"pending","stage":"gate_before_shipping","progress_percent":80,"note":"Checking the exact result."}`))},
		{response: openAIToolTerminalResponse("conversation-product-terminal", "# Secure work complete\n\nThe conversation-launched carrier finished exactly once.")},
	}}
	app.openAIToolRuntime = &openAIToolProductRuntime{Enabled: true, Carrier: &openAIToolLoopCarrier{Enabled: true, Manifest: manifest, Journal: journal, Provider: provider}}
	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerOpenAIText)
	app.apiKey = "router-test-key"
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_route" {
			return "", errors.New("unexpected non-router provider call")
		}
		return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Outcome: string(conversationIntentStartPrivateWork), Route: "workstream", Mode: "workflow", Objective: "Complete the secure private work"}), nil
	})

	serverSide := make(chan *websocket.Conn, 1)
	socketDone := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	socketServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, upgradeErr := upgrader.Upgrade(writer, request, nil)
		if upgradeErr != nil {
			return
		}
		serverSide <- connection
		<-socketDone
	}))
	t.Cleanup(socketServer.Close)
	connection, dialResponse, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(socketServer.URL, "http"), nil)
	if err != nil {
		status := 0
		if dialResponse != nil {
			status = dialResponse.StatusCode
		}
		t.Fatalf("dial canonical websocket: status=%d err=%v", status, err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	serverConnection := <-serverSide
	canonicalPrincipal := StrideE10TenantPrincipal{
		TenantID: snapshot.Session.ActiveOrganizationID, PersonID: snapshot.Session.PersonID,
		ActiveOrganizationID: snapshot.Session.ActiveOrganizationID, OrganizationMembershipID: snapshot.Session.OrganizationMembershipID,
		OrganizationMembershipRev: snapshot.Session.OrganizationMembershipRev, ActiveOrganizationSessionRev: snapshot.Session.ActiveOrganizationSessionRev,
		AuthorityGeneration: snapshot.Generation,
	}
	canonicalWriter := &threadSafeWriter{Conn: serverConnection, tenantLease: &strideE10TenantWebSocketLease{sessionHash: sessionHash, principal: canonicalPrincipal, canonical: true}}
	listLock.Lock()
	officeConnections["openai-tool-canonical-live"] = officeConnectionState{websocket: canonicalWriter, sessionEmail: normalizeAccountEmail(user.Email)}
	listLock.Unlock()
	t.Cleanup(func() {
		listLock.Lock()
		delete(officeConnections, "openai-tool-canonical-live")
		listLock.Unlock()
		_ = serverConnection.Close()
		close(socketDone)
	})

	operation := conversationTurnOperation{ID: "conversation-openai-tool-reachable", BodyDigest: sha256Hex([]byte("Complete the secure private work"))}
	var response map[string]any
	enteredCanonicalUse := false
	done := make(chan error, 1)
	go func() {
		done <- withStrideE10TenantRuntimeAuthority(context.Background(), StrideE10TenantSurfaceScout, sessionHash, nil, func(principal StrideE10TenantPrincipal) error {
			enteredCanonicalUse = true
			ctx, release, bindErr := strideE10BindCurrentHeldTenantAuthority(context.Background(), converter, principal, sessionHash, StrideE10TenantSurfaceScout)
			if bindErr != nil {
				return bindErr
			}
			defer release()
			ctx = withConversationTurnOperation(ctx, operation)
			var appendErr error
			response, appendErr = app.appendScoutChatThreadMessage(ctx, user, thread.ID, "Complete the secure private work", nil, "")
			return appendErr
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("conversation cutover launch: entered=%v err=%v", enteredCanonicalUse, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("conversation cutover launch deadlocked with its live canonical websocket")
	}
	launched, ok := response["agentThread"].(scoutAgentThread)
	if !ok || launched.ID == "" {
		t.Fatalf("conversation did not return its secure work run: %#v", response)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		artifact, exists := app.osArtifactByID(launched.Artifact.ID)
		if exists && artifact.Metadata["threadStatus"] == "complete" {
			if artifact.Text != "# Secure work complete\n\nThe conversation-launched carrier finished exactly once." || artifact.Metadata[openAIToolFinalRunDigestMetadataKey] == "" {
				t.Fatalf("terminal artifact mismatch: %+v", artifact)
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	artifact, _ := app.osArtifactByID(launched.Artifact.ID)
	if artifact.Metadata["threadStatus"] != "complete" {
		t.Fatalf("secure conversation carrier did not complete: %+v", artifact)
	}
	for {
		raw := waitForKanbanEvent(t, connection, "chat_thread", 5*time.Second)
		if strings.Contains(string(raw), launched.ID) && strings.Contains(string(raw), `"complete"`) {
			break
		}
	}
	workerDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(workerDeadline) {
		app.openAIToolActivationMu.Lock()
		_, active := app.openAIToolActiveRuns[launched.Artifact.ID]
		app.openAIToolActivationMu.Unlock()
		if !active {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("secure conversation worker did not retire its process claim")
}

func TestOpenAIToolProductCanonicalArtifactACLDenialPreventsProviderAdmission(t *testing.T) {
	fixture := newOpenAIToolProductTestFixture(t)
	previous := artifactObjectAuthorizer
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	defer func() { artifactObjectAuthorizer = previous }()
	called := false
	err := (&openAIToolProductAuthorityLease{backend: fixture.backend}).WithCurrentOpenAIToolAuthority(fixture.ctx, fixture.expectation, func(context.Context, openAIToolCurrentAuthority) error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatalf("canonical artifact ACL denial admitted provider: called=%v err=%v", called, err)
	}
}

func TestOpenAIToolProductRunnerSelectionIsServerInstalledAndDefaultOff(t *testing.T) {
	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerOpenAIText)
	app := &kanbanBoardApp{}
	job := AgentJob{Mode: "workflow", thread: scoutAgentThread{Mode: "workflow", Artifact: meetingMemoryEntry{Metadata: map[string]string{}}}}
	if _, ok := app.selectAgentRunner(job, nil).(*stubAgentRunner); !ok {
		t.Fatal("ordinary tool-dependent work did not default to unavailable")
	}
	app.openAIToolRuntime = &openAIToolProductRuntime{Enabled: false, Carrier: &openAIToolLoopCarrier{Enabled: true}}
	if _, ok := app.selectAgentRunner(job, nil).(*stubAgentRunner); !ok {
		t.Fatal("disabled installed carrier changed runner admission")
	}
	app.openAIToolRuntime.Enabled = true
	app.openAIToolRuntime.Carrier.Enabled = false
	if _, ok := app.selectAgentRunner(job, nil).(*stubAgentRunner); !ok {
		t.Fatal("runtime flag bypassed the carrier's independent default-off gate")
	}
	app.openAIToolRuntime.Carrier.Enabled = true
	if _, ok := app.selectAgentRunner(job, nil).(*openAIToolProductRunner); !ok {
		t.Fatal("explicit server-installed carrier did not bind ordinary work")
	}
}

func openAIToolCommitEqual(left, right openAIToolEffectCommit) bool {
	return string(left.FunctionOutput) == string(right.FunctionOutput) && left.PostimageDigest == right.PostimageDigest && left.ReconciliationDigest == right.ReconciliationDigest
}

func TestOpenAIToolProductFinalizerRequiresExactDurableChatProjection(t *testing.T) {
	fixture := newOpenAIToolProductTestFixture(t)
	finalizer := &openAIToolProductFinalizer{backend: fixture.backend}
	text := "# Secure work complete\n\nThe exact private work is complete."
	operations := []string{}
	commit, err := finalizer.FinalizeOpenAIToolRun(fixture.ctx, &openAIToolProductCurrentAuthority{backend: fixture.backend}, fixture.expectation, "openai-tool-run-finalizer", text, operations)
	if err != nil || commit.FinalUseDigest == "" || commit.FanOutReceiptDigest == "" {
		t.Fatalf("finalize: commit=%+v err=%v", commit, err)
	}
	reconciled, err := finalizer.ReconcileOpenAIToolRunFinalization(fixture.ctx, &openAIToolProductCurrentAuthority{backend: fixture.backend}, fixture.expectation, "openai-tool-run-finalizer", text, operations)
	if err != nil || reconciled.Status != openAIToolReconciliationCommitted || reconciled.Commit.RunDigest != commit.RunDigest {
		t.Fatalf("reconcile finalization: %+v err=%v", reconciled, err)
	}
	wrong, err := finalizer.ReconcileOpenAIToolRunFinalization(fixture.ctx, &openAIToolProductCurrentAuthority{backend: fixture.backend}, fixture.expectation, "openai-tool-run-finalizer", text+" changed", operations)
	if err != nil || wrong.Status != openAIToolReconciliationAmbiguous {
		t.Fatalf("changed terminal bytes were not ambiguous: %+v err=%v", wrong, err)
	}
	fixture.reopen(t)
	reopenedFinalizer := &openAIToolProductFinalizer{backend: fixture.backend}
	reopened, err := reopenedFinalizer.ReconcileOpenAIToolRunFinalization(fixture.ctx, &openAIToolProductCurrentAuthority{backend: fixture.backend}, fixture.expectation, "openai-tool-run-finalizer", text, operations)
	if err != nil || reopened.Status != openAIToolReconciliationCommitted || reopened.Commit.RunDigest != commit.RunDigest {
		t.Fatalf("reopened finalization receipt=%+v err=%v", reopened, err)
	}
}

func TestOpenAIToolProductCarrierLostResponseRestartIsExactlyOnce(t *testing.T) {
	fixture := newOpenAIToolProductTestFixture(t)
	var projectionEvents atomic.Int32
	previousProjectionProbe := openAIToolProjectionEventProbe
	openAIToolProjectionEventProbe = func(openAIToolProductEffectReceipt) { projectionEvents.Add(1) }
	t.Cleanup(func() { openAIToolProjectionEventProbe = previousProjectionProbe })
	journal := fixture.journal
	manifest, err := buildOpenAIToolManifest()
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	arguments := json.RawMessage(`{"artifact_id":"` + fixture.artifact.ID + `","title":"Carrier-complete work","content":"# Carrier-complete work\n\nThe secure restart converged exactly once."}`)
	firstProvider := &openAIToolScriptProvider{steps: []openAIToolScriptStep{
		{response: openAIToolFunctionResponse(t, "product-response-call", "product-call", "update_artifact", arguments)},
		{response: openAIToolFunctionResponse(t, "product-response-goal", "product-goal-call", controlToolReportGoalState, json.RawMessage(`{"goal_status":"review","review_gate":"pending","stage":"gate_before_shipping","progress_percent":82,"note":"Verifying the exact successor."}`))},
		{err: errors.New("simulated lost terminal response")},
	}}
	base := &openAIToolLoopCarrier{
		Enabled: true, Manifest: manifest, Journal: journal, Authority: &openAIToolProductAuthorityLease{backend: fixture.backend},
		Provider: firstProvider, Executor: openAIToolEffectAdapter{Backend: fixture.backend}, Finalizer: &openAIToolProductFinalizer{backend: fixture.backend},
	}
	request := openAIToolLoopRequest{Instructions: "Use only the secure admitted tools.", UserTurn: "Finish this exact private work.", Expectation: fixture.expectation}
	if _, err := base.Run(fixture.ctx, request); err == nil {
		t.Fatal("lost terminal response unexpectedly completed")
	}
	if err := fixture.backend.validateCurrentProductBinding(fixture.ctx, fixture.expectation); err != nil {
		artifact, _ := fixture.app.osArtifactByID(fixture.artifact.ID)
		t.Fatalf("product binding failed immediately after lost response: %v metadata=%+v", err, artifact.Metadata)
	}
	afterEffect, ok := fixture.app.osArtifactByID(fixture.artifact.ID)
	if !ok || afterEffect.Metadata["title"] != "Carrier-complete work" || artifactVersion(afterEffect) != artifactVersion(fixture.artifact)+1 {
		t.Fatalf("first exact effect missing: %+v", afterEffect)
	}

	secondProvider := &openAIToolScriptProvider{steps: []openAIToolScriptStep{{response: openAIToolTerminalResponse("product-response-terminal", "# Carrier-complete work\n\nThe secure restart converged exactly once.")}}}
	base.Provider = secondProvider
	result, err := base.Run(fixture.ctx, request)
	if err != nil || len(result.OperationIDs) != 2 || result.Model != openAIToolRunnerModel || result.Reasoning != openAIToolRunnerReasoningEffort {
		artifact, _ := fixture.app.osArtifactByID(fixture.artifact.ID)
		currentPostimage, _ := openAIToolProductSemanticPostimageDigest(artifact, "")
		runAuthorityDigest, _ := openAIToolCanonicalDigestOnly(openAIToolRunBaseExpectation(fixture.expectation))
		finalReceipt, finalReceiptErr := openAIToolProductFinalReceiptFromArtifact(artifact)
		finalVerifyErr := fixture.backend.verifyOpenAIToolProductFinalReceipt(fixture.ctx, finalReceipt)
		reachable := fixture.backend.openAIToolAuthenticatedPostimageReachable(fixture.ctx, artifact, artifact.Metadata[openAIToolBasePostimageMetadataKey], currentPostimage, runAuthorityDigest)
		bindingErr := fixture.backend.validateCurrentProductBinding(fixture.ctx, fixture.expectation)
		t.Fatalf("restart result=%+v err=%v binding=%v reachable=%v post=%s finalPost=%s finalParse=%v finalVerify=%v metadata=%+v", result, err, bindingErr, reachable, currentPostimage, finalReceipt.PostimageDigest, finalReceiptErr, finalVerifyErr, artifact.Metadata)
	}
	finalArtifact, ok := fixture.app.osArtifactByID(fixture.artifact.ID)
	if !ok || artifactVersion(finalArtifact) != artifactVersion(fixture.artifact)+1 || finalArtifact.Metadata[openAIToolFinalRunDigestMetadataKey] == "" || finalArtifact.Metadata[openAIToolFinalUseDigestMetadataKey] == "" || finalArtifact.Metadata[openAIToolFanOutDigestMetadataKey] == "" {
		t.Fatalf("restart duplicated or omitted finalization: %+v", finalArtifact)
	}
	thread, _, err := fixture.app.scoutChatThreadByID(fixture.expectation.RequesterAccount, fixture.thread.ID)
	if err != nil {
		t.Fatalf("read final chat projection: %v", err)
	}
	completeRefs := 0
	for _, message := range thread.Messages {
		if message.Thread != nil && message.Thread.ID == fixture.expectation.ThreadID && message.Thread.Status == "complete" && message.Thread.ArtifactID == fixture.artifact.ID {
			completeRefs++
		}
	}
	if completeRefs != 1 {
		t.Fatalf("durable final work-card projections=%d, want exactly one", completeRefs)
	}
	if projectionEvents.Load() != 2 {
		t.Fatalf("write/control projection events=%d, want exactly two committed turns", projectionEvents.Load())
	}
	finalVersion := artifactVersion(finalArtifact)
	fixture.reopen(t)
	request.Expectation = fixture.expectation
	noProvider := &openAIToolScriptProvider{}
	reopenedCarrier := &openAIToolLoopCarrier{
		Enabled: true, Manifest: manifest, Journal: fixture.journal, Authority: &openAIToolProductAuthorityLease{backend: fixture.backend},
		Provider: noProvider, Executor: openAIToolEffectAdapter{Backend: fixture.backend}, Finalizer: &openAIToolProductFinalizer{backend: fixture.backend},
	}
	replayed, err := reopenedCarrier.Run(fixture.ctx, request)
	if err != nil || replayed.Text != result.Text || !equalOpenAIToolStrings(replayed.OperationIDs, result.OperationIDs) || noProvider.calls != 0 || projectionEvents.Load() != 2 {
		t.Fatalf("completed reopen replay=%+v provider=%d projections=%d err=%v", replayed, noProvider.calls, projectionEvents.Load(), err)
	}
	replayedArtifact, ok := fixture.app.osArtifactByID(fixture.artifact.ID)
	if !ok || artifactVersion(replayedArtifact) != finalVersion {
		t.Fatalf("completed replay changed the final artifact: %+v", replayedArtifact)
	}
	fixture.journal.mu.Lock()
	defer fixture.journal.mu.Unlock()
	for _, record := range fixture.journal.state.Records {
		if record.State == openAIToolStateQuarantined {
			t.Fatalf("completed replay quarantined operation %s: %s", record.OperationID, record.QuarantineReason)
		}
	}
}

func TestOpenAIToolProductRuntimeRejectsMissingCanonicalSessionBinding(t *testing.T) {
	fixture := newOpenAIToolProductTestFixture(t)
	artifact, ok := fixture.app.osArtifactByID(fixture.artifact.ID)
	if !ok {
		t.Fatal("fixture artifact missing")
	}
	header := artifactAuthorizationHeaderFromEntry(artifact)
	if _, _, err := fixture.app.memory.updateOSArtifactMetadataIfHeaderMatches(header, artifact.ID, map[string]string{openAIToolSessionDigestMetadataKey: ""}); err != nil {
		t.Fatalf("clear session binding: %v", err)
	}
	job := fixture.backend.job
	if _, _, err := newOpenAIToolProductBackend(fixture.ctx, fixture.app, job, fixture.journal); !errors.Is(err, errOpenAIToolCarrierUnavailable) && err == nil {
		t.Fatalf("missing canonical session binding admitted: %v", err)
	}
}
