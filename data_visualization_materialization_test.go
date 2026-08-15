package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type dataVisualizationMaterializationFixtureState struct {
	app           *kanbanBoardApp
	user          *userAccount
	other         *userAccount
	thread        scoutChatThreadRecord
	sourceMessage scoutChatMessageRecord
	work          scoutAgentThread
	request       DataVisualizationMaterializationRequest
	providerCalls *atomic.Int32
}

func newDataVisualizationMaterializationFixture(t *testing.T, visibility string, projectCard bool) dataVisualizationMaterializationFixtureState {
	t.Helper()
	setupAuthTestEnv(t)
	t.Setenv("BONFIRE_CANONICAL_MODE", "off")
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	user := accountStore().findUser("aj@shareability.com")
	other := accountStore().findUser("tim@shareability.com")
	if user == nil || other == nil {
		t.Fatal("seed users missing")
	}
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Visualization Work", visibility)
	if err != nil {
		t.Fatal(err)
	}
	source := scoutChatMessageRecord{
		ID: "data-visualization-source-message", Kind: "message", Role: "user",
		Text:       "Build a source-bound operating results chart from the supplied typed table.",
		AuthorName: user.Name, AuthorEmail: normalizeAccountEmail(user.Email), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	thread, err = app.commitScoutChatThreadMessages(user.Email, thread.ID, source)
	if err != nil {
		t.Fatal(err)
	}
	source = thread.Messages[len(thread.Messages)-1]
	_, binding, err := scoutChatSourceWindow(thread, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	var providerCalls atomic.Int32
	previousStarter := startAgentThreadAsync
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) { providerCalls.Add(1) }
	t.Cleanup(func() { startAgentThreadAsync = previousStarter })
	origin := map[string]string{
		"originKind": agentThreadOriginPrivateThread, "originId": thread.ID, "requestedBy": user.Email,
		"sourceMessageId": source.ID, "sourceMessageDigest": binding.MessageDigest, "sourceWindowDigest": binding.WindowDigest,
	}
	if normalizeScoutChatVisibility(visibility) == scoutChatVisibilityPublic {
		origin["originKind"] = agentThreadOriginChannel
	}
	work, err := app.launchAgentThreadWithSpecBound("workflow", "Build the operating results visualization", user.Name, origin, agentThreadGoalSpec{
		Objective: "Build the operating results visualization", ToolTemplate: "data_visualization",
		SourceMessageID: source.ID, SourceMessageDigest: binding.MessageDigest, SourceWindowDigest: binding.WindowDigest,
		OriginSurface: "chat:" + thread.ID, RequestedBy: user.Email, Visibility: normalizeScoutChatVisibility(visibility),
	}, "", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if projectCard {
		thread, err = app.commitScoutChatThreadMessages(user.Email, thread.ID, scoutChatMessageRecord{
			ID: "data-visualization-work-card", Kind: "thread", Role: "scout", Text: "Data visualization in progress",
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Thread:    &scoutChatThreadRef{ID: work.ID, ArtifactID: work.Artifact.ID, Status: "running", ProgressPercent: 35},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	current, ok := app.osArtifactByID(work.Artifact.ID)
	if !ok {
		t.Fatal("work artifact missing")
	}
	header, ok := app.memory.artifactAuthorizationHeaderByID(current.ID)
	if !ok {
		t.Fatal("work artifact authorization header missing")
	}
	compile := dataVisualizationFixture(DataVisualizationBar)
	return dataVisualizationMaterializationFixtureState{
		app: app, user: user, other: other, thread: thread, sourceMessage: source,
		work: scoutAgentThread{ID: work.ID, Mode: work.Mode, Query: work.Query, Status: work.Status, Artifact: current},
		request: DataVisualizationMaterializationRequest{
			OperationID: "data-visualization-materialize-0001", Actor: user.Email,
			Artifact: artifactDispositionRefFromHeader(header), Compile: compile,
		},
		providerCalls: &providerCalls,
	}
}

func secondDataVisualizationWorkRequest(t *testing.T, fixture dataVisualizationMaterializationFixtureState) (meetingMemoryEntry, DataVisualizationMaterializationRequest) {
	t.Helper()
	metadata := fixture.work.Artifact.Metadata
	origin := map[string]string{
		"originKind":          metadata["originKind"],
		"originId":            metadata["originId"],
		"requestedBy":         metadata["requestedBy"],
		"sourceMessageId":     metadata["sourceMessageId"],
		"sourceMessageDigest": metadata["sourceMessageDigest"],
		"sourceWindowDigest":  metadata["sourceWindowDigest"],
	}
	work, err := fixture.app.launchAgentThreadWithSpecBound("workflow", "Build a second operating results visualization", fixture.user.Name, origin, agentThreadGoalSpec{
		Objective: "Build a second operating results visualization", ToolTemplate: "data_visualization",
		SourceMessageID: metadata["sourceMessageId"], SourceMessageDigest: metadata["sourceMessageDigest"], SourceWindowDigest: metadata["sourceWindowDigest"],
		OriginSurface: "chat:" + metadata["originId"], RequestedBy: fixture.user.Email, Visibility: scoutChatVisibilityPrivate,
	}, "", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	artifact, ok := fixture.app.osArtifactByID(work.Artifact.ID)
	if !ok {
		t.Fatal("second visualization Work artifact missing")
	}
	header, ok := fixture.app.memory.artifactAuthorizationHeaderByID(artifact.ID)
	if !ok {
		t.Fatal("second visualization Work authorization header missing")
	}
	request := fixture.request
	request.Artifact = artifactDispositionRefFromHeader(header)
	return artifact, request
}

func waitForDataVisualizationAdmissionRefs(t *testing.T, key string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		dataVisualizationMaterializationAdmissions.mu.Lock()
		admission := dataVisualizationMaterializationAdmissions.entries[key]
		refs := 0
		if admission != nil {
			refs = admission.refs
		}
		dataVisualizationMaterializationAdmissions.mu.Unlock()
		if refs == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("admission refs=%d, want %d", refs, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func bindDataVisualizationFixtureToCurrentProject(t *testing.T, fixture *dataVisualizationMaterializationFixtureState) context.Context {
	t.Helper()
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	previousRuntime := currentCanonicalRuntime()
	setCanonicalRuntime(&CanonicalRuntime{mode: CanonicalModeOff, postgres: store})
	t.Cleanup(func() { setCanonicalRuntime(previousRuntime) })

	snapshot := projectChatSnapshotFixture(t)
	snapshot.Session.Email = fixture.user.Email
	seed, err := store.confirmHomeProjectChatSend(ctx, snapshot,
		scoutChatThreadRecord{ID: "visualization_project_seed_thread", OwnerEmail: fixture.user.Email, Visibility: scoutChatVisibilityPrivate},
		"visualization_project_seed_message", "visualization-project-seed-operation", "Create Visualization Project", homeProjectContextToken{
			Kind: "create", ProjectTitle: "Visualization Project", Basis: "selected", Confidence: 1,
			OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID,
		})
	if err != nil {
		t.Fatal(err)
	}
	authority, err := store.projectChatSourceAuthorityForThread(ctx, snapshot, fixture.thread)
	if err != nil {
		t.Fatal(err)
	}
	link, err := store.confirmProjectChatSend(ctx, snapshot, fixture.thread, fixture.sourceMessage.ID,
		"visualization-project-source-operation", fixture.sourceMessage.Text, homeProjectContextToken{
			Kind: "project", ProjectID: seed.ProjectID, ProjectRevision: seed.ProjectRevision, ProjectDigest: seed.ProjectDigest,
			ProjectTitle: seed.ProjectTitle, Basis: "selected", Confidence: 1,
			OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID,
		}, authority)
	if err != nil {
		t.Fatal(err)
	}
	thread, _, err := fixture.app.scoutChatThreadByID(fixture.user.Email, fixture.thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	messageIndex := scoutChatMessageIndex(thread, fixture.sourceMessage.ID)
	if messageIndex < 0 {
		t.Fatal("visualization Project source message disappeared")
	}
	thread.Messages[messageIndex].Project = &scoutChatProjectContext{
		Status: "confirmed", ContextRevision: 1, ProjectID: link.ProjectID, ProjectRevision: link.ProjectRevision,
		Title: link.ProjectTitle, Basis: "selected", AssociationID: link.AssociationID, AssociationRevision: link.AssociationRevision,
	}
	if err := fixture.app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	_, sourceBinding, err := scoutChatSourceWindow(thread, fixture.sourceMessage.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence := &strideE10HeldTenantAuthorityFence{snapshot: snapshot}
	fence.active.Store(true)
	bound := strideE10ContextWithHeldTenantAuthority(ctx, fence)
	binding, err := fixture.app.projectWorkBindingForLaunch(bound, thread, thread.Messages[messageIndex])
	if err != nil {
		t.Fatal(err)
	}
	bindingRaw, err := encodeProjectWorkBinding(binding)
	if err != nil {
		t.Fatal(err)
	}
	updated, changed, err := fixture.app.memory.updateOSArtifactMetadata(fixture.work.Artifact.ID, map[string]string{
		projectWorkBindingMetadataKey: bindingRaw,
		"sourceMessageDigest":         sourceBinding.MessageDigest,
		"sourceWindowDigest":          sourceBinding.WindowDigest,
	})
	if err != nil || !changed {
		t.Fatalf("bind visualization Work to Project changed=%v err=%v", changed, err)
	}
	fixture.thread = thread
	fixture.sourceMessage = thread.Messages[messageIndex]
	fixture.work.Artifact = updated
	header, ok := fixture.app.memory.artifactAuthorizationHeaderByID(updated.ID)
	if !ok {
		t.Fatal("Project-bound visualization header disappeared")
	}
	fixture.request.Artifact = artifactDispositionRefFromHeader(header)
	return bound
}

func TestDataVisualizationMaterializationProjectBindingDoesNotReenterSourceLock(t *testing.T) {
	fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
	ctx := bindDataVisualizationFixtureToCurrentProject(t, &fixture)
	type outcome struct {
		result DataVisualizationMaterializationResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := fixture.app.MaterializeDataVisualization(ctx, fixture.user, fixture.request)
		done <- outcome{result: result, err: err}
	}()
	select {
	case got := <-done:
		if got.err != nil || got.result.Artifact.ID != fixture.work.Artifact.ID {
			t.Fatalf("Project-bound visualization result=%+v err=%v", got.result, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Project-bound visualization deadlocked while holding the source lock")
	}
}

func TestDataVisualizationMaterializationCommitsOneGovernedWorkArtifact(t *testing.T) {
	fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
	unrelatedRef, err := putBlob([]byte("unrelated export"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.app.appendArtifactAsset(fixture.work.Artifact.ID, artifactAsset{Ref: unrelatedRef, Mime: "text/plain", Name: "keep.txt", Kind: "export"}); err != nil {
		t.Fatal(err)
	}
	current, _ := fixture.app.osArtifactByID(fixture.work.Artifact.ID)
	header, _ := fixture.app.memory.artifactAuthorizationHeaderByID(current.ID)
	fixture.request.Artifact = artifactDispositionRefFromHeader(header)
	beforeVersion := artifactVersion(current)
	beforeDrive := len(fixture.app.memory.entriesOfKind(meetingMemoryKindFile, 0))

	result, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Replayed || result.Artifact.ID != current.ID || artifactVersion(result.Artifact) != beforeVersion+1 ||
		result.Artifact.Metadata["status"] != "complete" || result.Artifact.Metadata["threadStatus"] != "complete" ||
		result.Artifact.Metadata["progressPercent"] != "100" || result.Artifact.Metadata["type"] != artifactTypeMarkdown {
		t.Fatalf("materialized artifact=%+v replayed=%v", result.Artifact, result.Replayed)
	}
	if fixture.providerCalls.Load() != 0 || len(fixture.app.memory.entriesOfKind(meetingMemoryKindFile, 0)) != beforeDrive {
		t.Fatalf("provider calls=%d Drive rows=%d/%d", fixture.providerCalls.Load(), beforeDrive, len(fixture.app.memory.entriesOfKind(meetingMemoryKindFile, 0)))
	}
	if result.Receipt.validate() != nil || result.ReceiptSHA256 == "" || sha256Hex([]byte(result.Artifact.Metadata[dataVisualizationMaterializationReceiptKey])) != result.ReceiptSHA256 {
		t.Fatalf("receipt=%+v ref=%s", result.Receipt, result.ReceiptSHA256)
	}
	receiptRaw := result.Artifact.Metadata[dataVisualizationMaterializationReceiptKey]
	for _, secret := range []string{"Secret Operating Results", "Secret Q1", "Revenue", "12.5"} {
		if strings.Contains(receiptRaw, secret) {
			t.Fatalf("body-free receipt leaked %q: %s", secret, receiptRaw)
		}
	}
	if !strings.Contains(result.Artifact.Text, "Secret Operating Results") || !strings.Contains(result.Artifact.Text, "Secret Q1") || !strings.Contains(result.Artifact.Text, "| --- |") {
		t.Fatalf("readable Markdown table missing: %s", result.Artifact.Text)
	}
	assets := artifactAssets(result.Artifact)
	if len(assets) != 6 {
		t.Fatalf("assets=%+v, want unrelated + five visualization assets", assets)
	}
	foundUnrelated := false
	for _, asset := range assets {
		if asset.Ref == unrelatedRef && asset.Name == "keep.txt" {
			foundUnrelated = true
		}
	}
	if !foundUnrelated {
		t.Fatal("unrelated artifact asset was not preserved")
	}
	for _, ref := range []string{result.Receipt.SourceBlobSHA256, result.Receipt.SVGSHA256, result.Receipt.TableSHA256, result.Receipt.ManifestBlobSHA256, result.ReceiptSHA256} {
		data, _, readErr := getBlob(ref)
		if readErr != nil || sha256Hex(data) != ref {
			t.Fatalf("blob %s read=%v digest=%s", ref, readErr, sha256Hex(data))
		}
	}
	svg, _, _ := getBlob(result.Receipt.SVGSHA256)
	requireDataVisualizationPassiveSVG(t, string(svg))
	manifestBytes, _, _ := getBlob(result.Receipt.ManifestBlobSHA256)
	var manifest DataVisualizationManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil || VerifyDataVisualizationManifest(manifest) != nil || manifest.ManifestSHA256 != result.Receipt.ManifestSHA256 {
		t.Fatalf("manifest=%+v err=%v", manifest, err)
	}

	saved, _, err := fixture.app.scoutChatThreadByID(fixture.user.Email, fixture.thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	card := saved.Messages[len(saved.Messages)-1]
	if card.Thread == nil || card.Thread.ArtifactID != result.Artifact.ID || card.Thread.Status != "complete" || card.Thread.ProgressPercent != 100 {
		t.Fatalf("terminal work card=%+v", card)
	}
}

func TestDataVisualizationMaterializationReplaysAcrossRestartAndConflictsOnChangedBinding(t *testing.T) {
	fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
	first, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	version := artifactVersion(first.Artifact)
	replay, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request)
	if err != nil || !replay.Replayed || artifactVersion(replay.Artifact) != version || replay.ReceiptSHA256 != first.ReceiptSHA256 {
		t.Fatalf("in-process replay=%+v err=%v", replay, err)
	}

	restarted := newKanbanBoardApp()
	kanbanApp = restarted
	restartedUser := accountStore().findUser(fixture.user.Email)
	restartReplay, err := restarted.MaterializeDataVisualization(context.Background(), restartedUser, fixture.request)
	if err != nil || !restartReplay.Replayed || artifactVersion(restartReplay.Artifact) != version || restartReplay.ReceiptSHA256 != first.ReceiptSHA256 {
		t.Fatalf("restart replay=%+v err=%v", restartReplay, err)
	}

	changed := fixture.request
	changed.Compile.Table.Rows = cloneDataVisualizationRows(changed.Compile.Table.Rows)
	changed.Compile.Table.Rows[0][1].Number++
	changed.Compile = refreshVisualizationDigest(changed.Compile)
	if _, err := restarted.MaterializeDataVisualization(context.Background(), restartedUser, changed); !errors.Is(err, ErrDataVisualizationMaterializationConflict) {
		t.Fatalf("changed source err=%v", err)
	}
	changedTarget := fixture.request
	changedTarget.Artifact = artifactDispositionRefFromHeader(artifactAuthorizationHeaderFromEntry(first.Artifact))
	if _, err := restarted.MaterializeDataVisualization(context.Background(), restartedUser, changedTarget); !errors.Is(err, ErrDataVisualizationMaterializationConflict) {
		t.Fatalf("changed target err=%v", err)
	}
	current, _ := restarted.osArtifactByID(first.Artifact.ID)
	if artifactVersion(current) != version || current.Metadata[dataVisualizationMaterializationReceiptKey] != first.Artifact.Metadata[dataVisualizationMaterializationReceiptKey] {
		t.Fatal("conflicting replay mutated the materialized artifact")
	}
}

func TestDataVisualizationMaterializationOperationIDConflictsAcrossDistinctTargets(t *testing.T) {
	fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
	secondBefore, secondRequest := secondDataVisualizationWorkRequest(t, fixture)
	if _, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, secondRequest); !errors.Is(err, ErrDataVisualizationMaterializationConflict) {
		t.Fatalf("distinct-target operation reuse err=%v", err)
	}
	secondAfter, _ := fixture.app.osArtifactByID(secondBefore.ID)
	if artifactVersion(secondAfter) != artifactVersion(secondBefore) || secondAfter.Text != secondBefore.Text ||
		secondAfter.Metadata[dataVisualizationMaterializationReceiptKey] != "" || !sameStrings(sortedDataVisualizationAssetRefs(secondAfter), sortedDataVisualizationAssetRefs(secondBefore)) {
		t.Fatal("distinct-target conflict changed the losing Work artifact")
	}
}

func TestDataVisualizationMaterializationOperationIDDistinctTargetConflictSurvivesRestart(t *testing.T) {
	fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
	secondBefore, secondRequest := secondDataVisualizationWorkRequest(t, fixture)
	if _, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request); err != nil {
		t.Fatal(err)
	}
	restarted := newKanbanBoardApp()
	kanbanApp = restarted
	restartedUser := accountStore().findUser(fixture.user.Email)
	if _, err := restarted.MaterializeDataVisualization(context.Background(), restartedUser, secondRequest); !errors.Is(err, ErrDataVisualizationMaterializationConflict) {
		t.Fatalf("restart distinct-target operation reuse err=%v", err)
	}
	secondAfter, _ := restarted.osArtifactByID(secondBefore.ID)
	if artifactVersion(secondAfter) != artifactVersion(secondBefore) || secondAfter.Text != secondBefore.Text || secondAfter.Metadata[dataVisualizationMaterializationReceiptKey] != "" {
		t.Fatal("restart distinct-target conflict changed the losing Work artifact")
	}
}

func TestDataVisualizationMaterializationOperationIDConcurrentDistinctTargetsHaveOneWinner(t *testing.T) {
	fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
	secondBefore, secondRequest := secondDataVisualizationWorkRequest(t, fixture)
	firstBefore, _ := fixture.app.osArtifactByID(fixture.work.Artifact.ID)
	type outcome struct {
		artifactID string
		err        error
	}
	outcomes := make(chan outcome, 2)
	var wait sync.WaitGroup
	for _, request := range []DataVisualizationMaterializationRequest{fixture.request, secondRequest} {
		request := request
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, request)
			outcomes <- outcome{artifactID: result.Artifact.ID, err: err}
		}()
	}
	wait.Wait()
	close(outcomes)
	successes, conflicts := 0, 0
	for got := range outcomes {
		switch {
		case got.err == nil && got.artifactID != "":
			successes++
		case errors.Is(got.err, ErrDataVisualizationMaterializationConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent distinct-target outcome=%+v", got)
		}
	}
	firstAfter, _ := fixture.app.osArtifactByID(firstBefore.ID)
	secondAfter, _ := fixture.app.osArtifactByID(secondBefore.ID)
	commits := 0
	for _, candidate := range []meetingMemoryEntry{firstAfter, secondAfter} {
		if candidate.Metadata[dataVisualizationMaterializationReceiptKey] != "" {
			commits++
		}
	}
	if successes != 1 || conflicts != 1 || commits != 1 ||
		artifactVersion(firstAfter)+artifactVersion(secondAfter) != artifactVersion(firstBefore)+artifactVersion(secondBefore)+1 {
		t.Fatalf("successes=%d conflicts=%d commits=%d versions=%d,%d", successes, conflicts, commits, artifactVersion(firstAfter), artifactVersion(secondAfter))
	}
}

func TestDataVisualizationMaterializationAdmissionLocksOnlyExactOperationKey(t *testing.T) {
	fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
	_, unrelatedRequest := secondDataVisualizationWorkRequest(t, fixture)
	unrelatedRequest.OperationID = "data-visualization-materialize-unrelated"
	_, sameKeyRequest := secondDataVisualizationWorkRequest(t, fixture)
	prepared, err := prepareDataVisualizationMaterialization(fixture.request, fixture.user.Email)
	if err != nil {
		t.Fatal(err)
	}
	blockedKey := dataVisualizationMaterializationAdmissionKey(prepared.receipt)
	blocked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var releaseOnce sync.Once
	releaseBlocked := func() { releaseOnce.Do(func() { close(release) }) }
	dataVisualizationMaterializationAfterAdmissionProbe = func(key string) {
		if key == blockedKey {
			once.Do(func() {
				close(blocked)
				<-release
			})
		}
	}
	t.Cleanup(func() {
		releaseBlocked()
		dataVisualizationMaterializationAfterAdmissionProbe = nil
	})

	type outcome struct {
		result DataVisualizationMaterializationResult
		err    error
	}
	firstDone := make(chan outcome, 1)
	go func() {
		result, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request)
		firstDone <- outcome{result: result, err: err}
	}()
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("first exact-key admission did not reach the blocking probe")
	}

	sameDone := make(chan outcome, 1)
	go func() {
		result, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, sameKeyRequest)
		sameDone <- outcome{result: result, err: err}
	}()
	waitForDataVisualizationAdmissionRefs(t, blockedKey, 2)

	unrelatedDone := make(chan outcome, 1)
	go func() {
		result, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, unrelatedRequest)
		unrelatedDone <- outcome{result: result, err: err}
	}()
	select {
	case got := <-unrelatedDone:
		if got.err != nil || got.result.Artifact.ID != unrelatedRequest.Artifact.ArtifactID {
			t.Fatalf("unrelated operation was blocked or failed: result=%+v err=%v", got.result, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked operation key caused cross-key head-of-line blocking")
	}
	select {
	case got := <-sameDone:
		t.Fatalf("same operation key was not serialized: result=%+v err=%v", got.result, got.err)
	default:
	}

	releaseBlocked()
	first := <-firstDone
	if first.err != nil || first.result.Artifact.ID != fixture.request.Artifact.ArtifactID {
		t.Fatalf("released first operation result=%+v err=%v", first.result, first.err)
	}
	same := <-sameDone
	if !errors.Is(same.err, ErrDataVisualizationMaterializationConflict) {
		t.Fatalf("serialized same-key distinct target err=%v result=%+v", same.err, same.result)
	}
	waitForDataVisualizationAdmissionRefs(t, blockedKey, 0)
	dataVisualizationMaterializationAdmissions.mu.Lock()
	remaining := len(dataVisualizationMaterializationAdmissions.entries)
	dataVisualizationMaterializationAdmissions.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("admission registry retained %d historical keys", remaining)
	}
}

func TestDataVisualizationMaterializationOperationReceiptNamespaceHandling(t *testing.T) {
	t.Run("matching malformed claim fails closed", func(t *testing.T) {
		fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
		second, _ := secondDataVisualizationWorkRequest(t, fixture)
		raw, err := json.Marshal(map[string]any{
			"format": 0, "operationId": fixture.request.OperationID,
			"actorSha256": dataVisualizationActorDigest(fixture.user.Email),
			"target":      map[string]any{"tenantId": fixture.request.Artifact.TenantID, "artifactId": second.ID},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, changed, err := fixture.app.memory.updateOSArtifactMetadata(second.ID, map[string]string{dataVisualizationMaterializationReceiptKey: string(raw)}); err != nil || !changed {
			t.Fatalf("seed malformed matching claim changed=%v err=%v", changed, err)
		}
		if _, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request); !errors.Is(err, ErrDataVisualizationMaterializationConflict) {
			t.Fatalf("matching malformed claim err=%v", err)
		}
		current, _ := fixture.app.osArtifactByID(fixture.work.Artifact.ID)
		if current.Metadata[dataVisualizationMaterializationReceiptKey] != "" {
			t.Fatal("matching malformed claim bypassed operation admission")
		}
	})

	for name, mutate := range map[string]func(map[string]any){
		"unrelated operation": func(claim map[string]any) { claim["operationId"] = "unrelated-visualization-operation" },
		"unrelated actor":     func(claim map[string]any) { claim["actorSha256"] = sha256Hex([]byte("another actor")) },
		"unrelated tenant": func(claim map[string]any) {
			claim["target"].(map[string]any)["tenantId"] = "another-tenant"
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
			second, _ := secondDataVisualizationWorkRequest(t, fixture)
			claim := map[string]any{
				"format": 0, "operationId": fixture.request.OperationID,
				"actorSha256": dataVisualizationActorDigest(fixture.user.Email),
				"target":      map[string]any{"tenantId": fixture.request.Artifact.TenantID, "artifactId": second.ID},
			}
			mutate(claim)
			raw, err := json.Marshal(claim)
			if err != nil {
				t.Fatal(err)
			}
			if _, changed, err := fixture.app.memory.updateOSArtifactMetadata(second.ID, map[string]string{dataVisualizationMaterializationReceiptKey: string(raw)}); err != nil || !changed {
				t.Fatalf("seed unrelated malformed claim changed=%v err=%v", changed, err)
			}
			if _, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request); err != nil {
				t.Fatalf("unrelated malformed claim caused a false conflict: %v", err)
			}
		})
	}

	t.Run("unparseable unrelated metadata", func(t *testing.T) {
		fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
		second, _ := secondDataVisualizationWorkRequest(t, fixture)
		if _, changed, err := fixture.app.memory.updateOSArtifactMetadata(second.ID, map[string]string{dataVisualizationMaterializationReceiptKey: `{broken`}); err != nil || !changed {
			t.Fatalf("seed unparseable metadata changed=%v err=%v", changed, err)
		}
		if _, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request); err != nil {
			t.Fatalf("unparseable unrelated metadata caused a false conflict: %v", err)
		}
	})
}

func TestDataVisualizationMaterializationFailsClosedOnAuthorityTargetAndSourceDrift(t *testing.T) {
	t.Run("actor and owner", func(t *testing.T) {
		fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
		if _, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.other, fixture.request); !errors.Is(err, ErrDataVisualizationMaterializationInvalid) {
			t.Fatalf("mismatched actor err=%v", err)
		}
		request := fixture.request
		request.Actor = fixture.other.Email
		if _, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.other, request); !errors.Is(err, ErrDataVisualizationMaterializationDenied) {
			t.Fatalf("non-owner err=%v", err)
		}
	})
	t.Run("public work", func(t *testing.T) {
		fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPublic, true)
		if _, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request); !errors.Is(err, ErrDataVisualizationMaterializationDenied) {
			t.Fatalf("public Work err=%v", err)
		}
	})
	t.Run("non Work artifact", func(t *testing.T) {
		fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
		plain, _, err := fixture.app.createOSArtifactWithMetadata("workflow", "plain", "plain body", fixture.user.Name, map[string]string{
			"visibility": "private", "ownerEmail": fixture.user.Email, "requestedBy": fixture.user.Email,
		})
		if err != nil {
			t.Fatal(err)
		}
		header, _ := fixture.app.memory.artifactAuthorizationHeaderByID(plain.ID)
		request := fixture.request
		request.Artifact = artifactDispositionRefFromHeader(header)
		if _, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, request); !errors.Is(err, ErrDataVisualizationMaterializationDenied) {
			t.Fatalf("non-Work err=%v", err)
		}
	})
	t.Run("stale target revision", func(t *testing.T) {
		fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
		if _, _, err := fixture.app.updateOSArtifact(fixture.work.Artifact.ID, "", fixture.work.Artifact.Text+"\n\nHuman edit.", fixture.user.Name); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request); !errors.Is(err, ErrDataVisualizationMaterializationConflict) {
			t.Fatalf("stale target err=%v", err)
		}
	})
	t.Run("source archived before CAS", func(t *testing.T) {
		fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
		before, _ := fixture.app.osArtifactByID(fixture.work.Artifact.ID)
		var once sync.Once
		dataVisualizationMaterializationBeforeCASProbe = func() {
			once.Do(func() {
				if _, err := fixture.app.setScoutChatThreadArchived(fixture.user.Email, fixture.thread.ID, true); err != nil {
					t.Errorf("archive source: %v", err)
				}
			})
		}
		t.Cleanup(func() { dataVisualizationMaterializationBeforeCASProbe = nil })
		if _, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request); !errors.Is(err, ErrAgentThreadSourceChanged) {
			t.Fatalf("source drift err=%v", err)
		}
		after, _ := fixture.app.osArtifactByID(before.ID)
		if artifactVersion(after) != artifactVersion(before) || after.Text != before.Text || after.Metadata[dataVisualizationMaterializationReceiptKey] != "" || !sameStrings(sortedDataVisualizationAssetRefs(after), sortedDataVisualizationAssetRefs(before)) {
			t.Fatal("source drift left partial artifact references")
		}
	})
	for name, mutation := range map[string]map[string]string{
		"late status metadata": {"followUpStatus": "awaiting_review"},
		"late source metadata": {"sourceWindowDigest": sha256Hex([]byte("changed source window"))},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
			before, _ := fixture.app.osArtifactByID(fixture.work.Artifact.ID)
			var once sync.Once
			dataVisualizationMaterializationBeforeCASProbe = func() {
				once.Do(func() {
					if _, changed, updateErr := fixture.app.memory.updateOSArtifactMetadata(before.ID, mutation); updateErr != nil || !changed {
						t.Errorf("late metadata changed=%v err=%v", changed, updateErr)
					}
				})
			}
			t.Cleanup(func() { dataVisualizationMaterializationBeforeCASProbe = nil })
			if _, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request); !errors.Is(err, ErrDataVisualizationMaterializationConflict) {
				t.Fatalf("late metadata err=%v", err)
			}
			after, _ := fixture.app.osArtifactByID(before.ID)
			if artifactVersion(after) != artifactVersion(before) || after.Text != before.Text || after.Metadata[dataVisualizationMaterializationReceiptKey] != "" {
				t.Fatal("late metadata race published visualization references")
			}
		})
	}
	t.Run("source archived before replay", func(t *testing.T) {
		fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
		first, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.app.setScoutChatThreadArchived(fixture.user.Email, fixture.thread.ID, true); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request); !errors.Is(err, ErrAgentThreadSourceChanged) {
			t.Fatalf("archived-source replay err=%v", err)
		}
		current, _ := fixture.app.osArtifactByID(first.Artifact.ID)
		if artifactVersion(current) != artifactVersion(first.Artifact) || current.Text != first.Artifact.Text {
			t.Fatal("archived-source replay mutated the artifact")
		}
	})
	t.Run("human edit before replay", func(t *testing.T) {
		fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
		first, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request)
		if err != nil {
			t.Fatal(err)
		}
		var edited meetingMemoryEntry
		var once sync.Once
		dataVisualizationMaterializationBeforeReplayProbe = func() {
			once.Do(func() {
				var changed bool
				edited, changed, err = fixture.app.updateOSArtifact(first.Artifact.ID, "", first.Artifact.Text+"\n\nHuman note.", fixture.user.Name)
				if err != nil || !changed {
					t.Errorf("human edit changed=%v err=%v", changed, err)
				}
			})
		}
		t.Cleanup(func() { dataVisualizationMaterializationBeforeReplayProbe = nil })
		if _, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request); !errors.Is(err, ErrDataVisualizationMaterializationConflict) {
			t.Fatalf("human-edited replay err=%v", err)
		}
		current, _ := fixture.app.osArtifactByID(first.Artifact.ID)
		if artifactVersion(current) != artifactVersion(edited) || current.Text != edited.Text {
			t.Fatal("conflicting replay overwrote the human edit")
		}
	})
	t.Run("project binding unavailable", func(t *testing.T) {
		fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
		if _, _, err := fixture.app.memory.updateOSArtifactMetadata(fixture.work.Artifact.ID, map[string]string{projectWorkBindingMetadataKey: `{}`}); err != nil {
			t.Fatal(err)
		}
		current, _ := fixture.app.osArtifactByID(fixture.work.Artifact.ID)
		header, _ := fixture.app.memory.artifactAuthorizationHeaderByID(current.ID)
		request := fixture.request
		request.Artifact = artifactDispositionRefFromHeader(header)
		if _, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, request); !errors.Is(err, ErrDataVisualizationMaterializationDenied) {
			t.Fatalf("unavailable Project binding err=%v", err)
		}
	})
}

func TestDataVisualizationMaterializationPersistenceFailureLeavesArtifactUntouched(t *testing.T) {
	fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
	before, _ := fixture.app.osArtifactByID(fixture.work.Artifact.ID)
	originalPath := fixture.app.memory.path
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.app.memory.path = filepath.Join(blocker, "memory.jsonl")
	_, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request)
	fixture.app.memory.path = originalPath
	if err == nil {
		t.Fatal("expected artifact persistence failure")
	}
	after, _ := fixture.app.osArtifactByID(before.ID)
	if artifactVersion(after) != artifactVersion(before) || after.Text != before.Text || after.Metadata[dataVisualizationMaterializationReceiptKey] != "" || !sameStrings(sortedDataVisualizationAssetRefs(after), sortedDataVisualizationAssetRefs(before)) {
		t.Fatalf("failed persistence changed artifact before=%+v after=%+v", before, after)
	}
	restarted := newKanbanBoardApp()
	persisted, _ := restarted.osArtifactByID(before.ID)
	if artifactVersion(persisted) != artifactVersion(before) || persisted.Text != before.Text || persisted.Metadata[dataVisualizationMaterializationReceiptKey] != "" {
		t.Fatal("failed artifact mutation appeared after restart")
	}
}

func TestDataVisualizationMaterializationConcurrentReplayAndConflict(t *testing.T) {
	t.Run("exact duplicates", func(t *testing.T) {
		fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
		beforeVersion := artifactVersion(fixture.work.Artifact)
		const callers = 8
		results := make(chan DataVisualizationMaterializationResult, callers)
		errs := make(chan error, callers)
		var wait sync.WaitGroup
		for index := 0; index < callers; index++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				result, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request)
				results <- result
				errs <- err
			}()
		}
		wait.Wait()
		close(results)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("exact concurrent materialization: %v", err)
			}
		}
		replays := 0
		refs := map[string]bool{}
		for result := range results {
			if result.Replayed {
				replays++
			}
			refs[result.ReceiptSHA256] = true
		}
		current, _ := fixture.app.osArtifactByID(fixture.work.Artifact.ID)
		if replays != callers-1 || len(refs) != 1 || artifactVersion(current) != beforeVersion+1 {
			t.Fatalf("replays=%d refs=%v version=%d/%d", replays, refs, artifactVersion(current), beforeVersion)
		}
	})
	t.Run("changed requests", func(t *testing.T) {
		fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
		changed := fixture.request
		changed.OperationID = "data-visualization-materialize-0002"
		changed.Compile.Table.Rows = cloneDataVisualizationRows(changed.Compile.Table.Rows)
		changed.Compile.Table.Rows[0][1].Number += 10
		changed.Compile = refreshVisualizationDigest(changed.Compile)
		requests := []DataVisualizationMaterializationRequest{fixture.request, changed}
		type outcome struct {
			result DataVisualizationMaterializationResult
			err    error
		}
		outcomes := make(chan outcome, 2)
		var wait sync.WaitGroup
		for _, request := range requests {
			request := request
			wait.Add(1)
			go func() {
				defer wait.Done()
				result, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, request)
				outcomes <- outcome{result, err}
			}()
		}
		wait.Wait()
		close(outcomes)
		successes, conflicts := 0, 0
		winnerRef := ""
		for outcome := range outcomes {
			if outcome.err == nil {
				successes++
				winnerRef = outcome.result.ReceiptSHA256
			} else if errors.Is(outcome.err, ErrDataVisualizationMaterializationConflict) {
				conflicts++
			} else {
				t.Fatalf("unexpected changed-request error: %v", outcome.err)
			}
		}
		current, _ := fixture.app.osArtifactByID(fixture.work.Artifact.ID)
		if successes != 1 || conflicts != 1 || sha256Hex([]byte(current.Metadata[dataVisualizationMaterializationReceiptKey])) != winnerRef {
			t.Fatalf("successes=%d conflicts=%d winner=%s current=%s", successes, conflicts, winnerRef, current.Metadata[dataVisualizationMaterializationReceiptKey])
		}
	})
}

func TestDataVisualizationMaterializationTerminalProjectionRecoversAfterArtifactFirstCommit(t *testing.T) {
	fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, false)
	result, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := fixture.app.commitScoutChatThreadMessages(fixture.user.Email, fixture.thread.ID, scoutChatMessageRecord{
		ID: "late-data-visualization-card", Kind: "thread", Role: "scout", Text: "Data visualization in progress",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Thread:    &scoutChatThreadRef{ID: fixture.work.ID, ArtifactID: result.Artifact.ID, Status: "running", ProgressPercent: 35},
	})
	if err != nil {
		t.Fatal(err)
	}
	if thread.Messages[len(thread.Messages)-1].Thread.Status != "running" {
		t.Fatal("test did not create the artifact-first crash window")
	}
	replay, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request)
	if err != nil || !replay.Replayed {
		t.Fatalf("exact replay repair=%+v err=%v", replay, err)
	}
	reconciled, _, err := fixture.app.scoutChatThreadByID(fixture.user.Email, fixture.thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	card := reconciled.Messages[len(reconciled.Messages)-1]
	if card.Thread == nil || card.Thread.Status != "complete" || card.Thread.ProgressPercent != 100 {
		t.Fatalf("reconciled terminal card=%+v", card)
	}
}

func TestDataVisualizationMaterializationProjectionFailureRepairsOnReplay(t *testing.T) {
	fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
	originalPath := fixture.app.memory.path
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	scoutTerminalProjectionBeforeSaveProbe = func() {
		once.Do(func() { fixture.app.memory.path = filepath.Join(blocker, "memory.jsonl") })
	}
	t.Cleanup(func() {
		scoutTerminalProjectionBeforeSaveProbe = nil
		fixture.app.memory.path = originalPath
	})
	if _, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request); err == nil {
		t.Fatal("expected terminal projection persistence failure")
	}
	fixture.app.memory.path = originalPath
	scoutTerminalProjectionBeforeSaveProbe = nil
	current, _ := fixture.app.osArtifactByID(fixture.work.Artifact.ID)
	if current.Metadata[dataVisualizationMaterializationReceiptKey] == "" || artifactStatus(current) != artifactStatusComplete {
		t.Fatal("projection failure rolled back the authoritative artifact")
	}
	thread, _, err := fixture.app.scoutChatThreadByID(fixture.user.Email, fixture.thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if thread.Messages[len(thread.Messages)-1].Thread.Status != "running" {
		t.Fatal("failed projection unexpectedly changed the Work card")
	}
	replay, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request)
	if err != nil || !replay.Replayed {
		t.Fatalf("projection repair replay=%+v err=%v", replay, err)
	}
	thread, _, _ = fixture.app.scoutChatThreadByID(fixture.user.Email, fixture.thread.ID)
	if thread.Messages[len(thread.Messages)-1].Thread.Status != "complete" {
		t.Fatal("exact replay did not repair the terminal Work card")
	}
}

func TestDataVisualizationMaterializationAmbiguousVisibleCommitRepairsOnReplay(t *testing.T) {
	fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
	t.Setenv("BONFIRE_CANONICAL_MODE", "shadow")
	originalSync := syncDirectoryForAtomicWrite
	memoryDir := filepath.Dir(fixture.app.memory.path)
	var once sync.Once
	syncDirectoryForAtomicWrite = func(path string) error {
		if path == memoryDir {
			failed := false
			once.Do(func() { failed = true })
			if failed {
				return errors.New("injected directory sync failure")
			}
		}
		return originalSync(path)
	}
	t.Cleanup(func() { syncDirectoryForAtomicWrite = originalSync })
	if _, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request); !errors.Is(err, ErrDurableReplaceAmbiguous) {
		t.Fatalf("ambiguous commit err=%v", err)
	}
	syncDirectoryForAtomicWrite = originalSync
	visible, _ := fixture.app.osArtifactByID(fixture.work.Artifact.ID)
	if visible.Metadata[dataVisualizationMaterializationReceiptKey] == "" || artifactStatus(visible) != artifactStatusComplete {
		t.Fatal("rename-published artifact was not reloaded as visible")
	}
	replay, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, fixture.request)
	if err != nil || !replay.Replayed {
		t.Fatalf("ambiguous visible replay=%+v err=%v", replay, err)
	}
	thread, _, _ := fixture.app.scoutChatThreadByID(fixture.user.Email, fixture.thread.ID)
	if thread.Messages[len(thread.Messages)-1].Thread.Status != "complete" {
		t.Fatal("ambiguous visible retry did not repair the terminal card")
	}
}

func TestDataVisualizationMaterializationRejectsMarkdownHostileCellsWithoutEffect(t *testing.T) {
	fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
	before, _ := fixture.app.osArtifactByID(fixture.work.Artifact.ID)
	tests := map[string]func(*DataVisualizationCompileRequest){
		"title pipe":     func(request *DataVisualizationCompileRequest) { request.Spec.Title = "Unsafe | title" },
		"label slash":    func(request *DataVisualizationCompileRequest) { request.Table.Columns[0].Label = `Unsafe\label` },
		"unit pipe":      func(request *DataVisualizationCompileRequest) { request.Table.Columns[1].Unit = "USD|net" },
		"category slash": func(request *DataVisualizationCompileRequest) { request.Table.Rows[0][0].Text = `Q1\draft` },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := fixture.request
			request.Compile.Table.Columns = append([]DataVisualizationColumn(nil), fixture.request.Compile.Table.Columns...)
			request.Compile.Table.Rows = cloneDataVisualizationRows(fixture.request.Compile.Table.Rows)
			mutate(&request.Compile)
			request.Compile = refreshVisualizationDigest(request.Compile)
			if _, err := fixture.app.MaterializeDataVisualization(context.Background(), fixture.user, request); !errors.Is(err, ErrDataVisualizationMaterializationInvalid) {
				t.Fatalf("hostile Markdown err=%v", err)
			}
			after, _ := fixture.app.osArtifactByID(before.ID)
			if artifactVersion(after) != artifactVersion(before) || after.Text != before.Text || after.Metadata[dataVisualizationMaterializationReceiptKey] != "" || !sameStrings(sortedDataVisualizationAssetRefs(after), sortedDataVisualizationAssetRefs(before)) {
				t.Fatal("hostile Markdown input changed the Work artifact")
			}
		})
	}
}

func TestDataVisualizationMaterializationReceiptIsBodyFree(t *testing.T) {
	fixture := newDataVisualizationMaterializationFixture(t, scoutChatVisibilityPrivate, true)
	prepared, err := prepareDataVisualizationMaterialization(fixture.request, fixture.user.Email)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(prepared.receiptRaw, []byte("Secret")) || bytes.Contains(prepared.receiptRaw, []byte("Revenue")) || bytes.Contains(prepared.receiptRaw, []byte("12.5")) {
		t.Fatalf("receipt leaked source body: %s", prepared.receiptRaw)
	}
	if !bytes.Contains(prepared.sourceRaw, []byte("Secret Operating Results")) || !bytes.Contains(prepared.compiled.AccessibleTableHTML, []byte("Secret Q1")) {
		t.Fatal("test sentinels were absent from their ACL-governed bodies")
	}
}
