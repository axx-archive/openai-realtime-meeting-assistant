package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type studioRestartFixture struct {
	app      *kanbanBoardApp
	engine   *goalEngine
	plan     goalPlan
	parentID string
	def      ProcessDefinition
}

func newStudioRestartFixture(t *testing.T) studioRestartFixture {
	t.Helper()
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "studio-restart-test-key")
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "studio-restart-test-key"
	originalStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = originalStart })
	run, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Build a private presentation about whether the official program's 2026 opted-in creator count supports proceeding",
		CreatedBy: "aj@shareability.com", ToolTemplate: packagingStudioProcessID,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := mustGoalPlan(t, app, run.Artifact.ID)
	engine := newGoalEngine(app)
	if err := engine.prepareGoalRoute(&plan, run.Artifact.ID); err != nil {
		t.Fatal(err)
	}
	def := packagingStudioDefinition()
	if err := instantiateProcessPlan(def, &plan); err != nil {
		t.Fatal(err)
	}
	assignGoalRunners(&plan)
	return studioRestartFixture{app: app, engine: engine, plan: plan, parentID: run.Artifact.ID, def: def}
}

func (fixture *studioRestartFixture) persistOnlyRunningStage(t *testing.T, stageID string) (ProcessStage, *goalSubtask) {
	t.Helper()
	stage, found := fixture.def.stageByID(stageID)
	if !found {
		t.Fatalf("stage %s not found", stageID)
	}
	for index := range fixture.plan.Subtasks {
		fixture.plan.Subtasks[index].Status = subtaskBlocked
		fixture.plan.Subtasks[index].ArtifactID = ""
		fixture.plan.Subtasks[index].InlineExecutionKey = ""
	}
	st := fixture.plan.subtaskByID(stageID)
	if st == nil {
		t.Fatalf("subtask %s not found", stageID)
	}
	st.Status = subtaskRunning
	st.Attempts = 1
	st.InlineExecutionKey = goalInlineStageExecutionKey(&fixture.plan, fixture.parentID, st, stage)
	fixture.plan.State = goalStateExecute
	if persisted := fixture.engine.persist(&fixture.plan, fixture.parentID, ""); persisted.ID == "" {
		t.Fatal("persist interrupted Studio stage")
	}
	return stage, st
}

func TestStudioBootRecoversOldRootAndReplaysInterruptedInlineProviderAttempt(t *testing.T) {
	fixture := newStudioRestartFixture(t)
	stage, interrupted := fixture.persistOnlyRunningStage(t, "context_snapshot")
	executionKey := interrupted.InlineExecutionKey
	contextBody := externalEvidenceContextBodyForTest(t, fixture.plan, "What is the official program's 2026 opted-in creator count?", 6)

	// Push the canonical Studio root beyond the generic newest-200 boot tail.
	for index := 0; index < goalReconcileScanLimit+5; index++ {
		if _, appended, err := fixture.app.memory.appendOSArtifact(fmt.Sprintf("os-artifact-restart-noise-%03d", index), "unrelated artifact", map[string]string{
			"source": "test_noise", "mode": "workflow", "status": "complete",
		}); err != nil || !appended {
			t.Fatalf("append noise %d: appended=%v err=%v", index, appended, err)
		}
	}

	var requestMu sync.Mutex
	var stageRequests []openAITextRequest
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if strings.Contains(strings.ToLower(request.Instructions), "process stage synthesizer") {
			requestMu.Lock()
			stageRequests = append(stageRequests, request)
			requestMu.Unlock()
			return contextBody, nil
		}
		return "", fmt.Errorf("unexpected provider request during restart: %s", request.Workflow)
	})
	originalAgentStart := startAgentThreadAsync
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = originalAgentStart })

	restarted := newKanbanBoardApp()
	restarted.apiKey = "studio-restart-test-key"
	restarted.reconcileGoalThreadsAtBoot()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		plan := mustGoalPlan(t, restarted, fixture.parentID)
		if current := plan.subtaskByID(stage.ID); current != nil && current.Status == subtaskComplete {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Boot reconciliation runs on a goroutine. Crossing the parent lock proves
	// the complete stage and its next durable dispatch have both finished before
	// the TempDir-backed store is removed.
	parentLock := goalEngineLock(fixture.parentID)
	parentLock.Lock()
	parentLock.Unlock()
	restartedPlan := mustGoalPlan(t, restarted, fixture.parentID)
	recovered := restartedPlan.subtaskByID(stage.ID)
	if recovered == nil || recovered.Status != subtaskComplete || recovered.Attempts != 1 || strings.TrimSpace(recovered.ArtifactID) == "" {
		t.Fatalf("interrupted stage was not resumed as the same attempt: %+v", recovered)
	}
	requestMu.Lock()
	requests := append([]openAITextRequest(nil), stageRequests...)
	requestMu.Unlock()
	if len(requests) != 1 || requests[0].IdempotencyKey == "" {
		t.Fatalf("provider requests=%d key=%q, want one keyed replay", len(requests), func() string {
			if len(requests) == 0 {
				return ""
			}
			return requests[0].IdempotencyKey
		}())
	}
	wantKey := goalInlineProviderOperationKey(withGoalInlineProviderOperation(context.Background(), executionKey), requests[0].Model, requests[0].Seat, requests[0].Instructions, requests[0].Input)
	if requests[0].IdempotencyKey != wantKey {
		t.Fatalf("provider key=%q, want deterministic %q", requests[0].IdempotencyKey, wantKey)
	}
}

func TestStudioInlineProviderAdmissionRequiresDurableAttemptReservation(t *testing.T) {
	fixture := newStudioRestartFixture(t)
	stage, found := fixture.def.stageByID("context_snapshot")
	if !found {
		t.Fatal("context_snapshot stage is unavailable")
	}
	for index := range fixture.plan.Subtasks {
		fixture.plan.Subtasks[index].Status = subtaskBlocked
		fixture.plan.Subtasks[index].ArtifactID = ""
		fixture.plan.Subtasks[index].InlineExecutionKey = ""
	}
	inline := fixture.plan.subtaskByID(stage.ID)
	inline.Status = subtaskReady
	inline.Attempts = 0
	fixture.plan.State = goalStateExecute
	persisted := fixture.engine.persist(&fixture.plan, fixture.parentID, "")
	if persisted.ID == "" {
		t.Fatal("persist ready Studio stage fixture")
	}

	fixture.engine.expectedPersistHeader = &ArtifactAuthorizationHeader{ObjectID: "wrong-inline-parent-revision"}
	fixture.engine.expectedPersistBody = persisted.Text
	var providerCalls atomic.Int32
	fixture.engine.openAIResponder = func(context.Context, string, openAITextRequest) (string, error) {
		providerCalls.Add(1)
		return "provider must not receive an unreserved attempt", nil
	}
	if parked := fixture.engine.runInlineProcessStages(context.Background(), &fixture.plan, fixture.parentID); !parked {
		t.Fatal("inline engine continued after its attempt reservation failed")
	}
	if providerCalls.Load() != 0 {
		t.Fatalf("failed inline reservation made %d provider calls", providerCalls.Load())
	}
	durable := mustGoalPlan(t, fixture.app, fixture.parentID)
	durableStage := durable.subtaskByID(stage.ID)
	if durableStage == nil || durableStage.Status != subtaskReady || durableStage.Attempts != 0 || durableStage.InlineExecutionKey != "" {
		t.Fatalf("failed reservation mutated durable provider authority: %+v", durableStage)
	}
}

func TestStudioRestartAdoptsExactTerminalInlineArtifactWithoutDuplicateOrNarration(t *testing.T) {
	fixture := newStudioRestartFixture(t)
	stage, interrupted := fixture.persistOnlyRunningStage(t, "ship_compile")
	executionKey := interrupted.InlineExecutionKey

	// The inline stage crossed its durable artifact and origin-narration seam,
	// then the process died before the mutated parent plan was persisted.
	fixture.engine.completeProcessStage(&fixture.plan, fixture.parentID, interrupted, stage, "Presentation ready from exact reviewed inputs.", "editable deck ready", nil)
	artifactID := goalInlineStageArtifactID(executionKey)
	artifact, found := fixture.app.osArtifactByID(artifactID)
	if !found || interrupted.Status != subtaskComplete || interrupted.ArtifactID != artifactID {
		t.Fatalf("terminal crash fixture did not create exact artifact: found=%v stage=%+v", found, interrupted)
	}
	persistedPlan := mustGoalPlan(t, fixture.app, fixture.parentID)
	parentBefore := persistedPlan.subtaskByID(stage.ID)
	if parentBefore == nil || parentBefore.Status != subtaskRunning || parentBefore.ArtifactID != "" {
		t.Fatalf("parent fold unexpectedly persisted before crash: %+v", parentBefore)
	}
	originBefore, ok := fixture.app.goalOriginChatThread(mustArtifact(t, fixture.app, fixture.parentID))
	if !ok {
		t.Fatal("Studio origin thread unavailable")
	}
	narrationsBefore := 0
	for _, message := range originBefore.Messages {
		if message.Thread != nil && message.Thread.ArtifactID == artifact.ID {
			narrationsBefore++
		}
	}
	if narrationsBefore != 1 {
		t.Fatalf("terminal stage narrations=%d, want one before crash", narrationsBefore)
	}

	var providerCalls atomic.Int32
	swapOpenAITextResponder(t, func(context.Context, string, openAITextRequest) (string, error) {
		providerCalls.Add(1)
		return "", fmt.Errorf("provider must not run when exact stage artifact exists")
	})
	restarted := newKanbanBoardApp()
	restarted.apiKey = "studio-restart-test-key"
	restarted.reconcileGoalThread(fixture.parentID)
	restartedPlan := mustGoalPlan(t, restarted, fixture.parentID)
	recovered := restartedPlan.subtaskByID(stage.ID)
	if recovered == nil || recovered.ArtifactID != artifactID || recovered.Status != subtaskComplete || providerCalls.Load() != 0 {
		t.Fatalf("exact terminal stage was not adopted: stage=%+v providerCalls=%d", recovered, providerCalls.Load())
	}
	matchingArtifacts := 0
	for _, candidate := range restarted.memory.artifactMetadataSnapshot() {
		if candidate.ID == artifactID {
			matchingArtifacts++
		}
	}
	if matchingArtifacts != 1 {
		t.Fatalf("deterministic stage artifacts=%d, want one", matchingArtifacts)
	}
	originAfter, ok := restarted.goalOriginChatThread(mustArtifact(t, restarted, fixture.parentID))
	if !ok {
		t.Fatal("restarted Studio origin thread unavailable")
	}
	narrationsAfter := 0
	for _, message := range originAfter.Messages {
		if message.Thread != nil && message.Thread.ArtifactID == artifact.ID {
			narrationsAfter++
		}
	}
	if narrationsAfter != narrationsBefore {
		t.Fatalf("recovery duplicated stage narration: before=%d after=%d", narrationsBefore, narrationsAfter)
	}
}

func TestStudioPresentationCompileRestartAdoptsTerminalRenderWithoutDuplicate(t *testing.T) {
	queueDir := setupRenderSidecarEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	const goalID = "studio-terminal-render-parent"
	inputs := studioShipInputs{
		GoalID: goalID, CreatedBy: "aj@shareability.com", DeckOnly: true,
		DeckTitle: "Restart-safe presentation", DeckHTML: studioPremiumTestDeckHTML(),
	}

	// First file the exact deck without a live sidecar. The manual bound enqueue
	// below models the narrower crash seam inside enqueueStudioRender: the job
	// reached durable terminal state, but the source-artifact stamp and compile
	// stage artifact/parent fold did not.
	first, err := app.fileStudioShipDeliverables(inputs)
	if err != nil || len(first) != 1 {
		t.Fatalf("file interrupted presentation: deliverables=%+v err=%v", first, err)
	}
	if first[0].RenderJob != "" || !strings.Contains(first[0].RenderNote, "render sidecar not available") {
		t.Fatalf("interrupted fixture unexpectedly queued through Studio: %+v", first[0])
	}
	before := mustArtifact(t, app, first[0].ArtifactID)
	beforeVersion := artifactVersion(before)
	beforeDigest := artifactCapabilityDigest(before)
	printHTML := before.Text
	if strings.TrimSpace(before.Metadata[deckSceneRefMetadataKey]) != "" {
		expanded, expandErr := artifactRenderBody(before)
		if expandErr != nil {
			t.Fatalf("expand exact presentation render body: %v", expandErr)
		}
		printHTML = string(expanded)
	}
	binding := renderPDFJobBinding{
		ArtifactID: before.ID, Kind: serverRenderKindForArtifact(before), HTML: printHTML, Title: before.Metadata["title"],
		SourceArtifactVersion: beforeVersion, SourceSceneRef: strings.TrimSpace(before.Metadata[deckSceneRefMetadataKey]),
		SourceContentDigest: renderPDFContentDigest(serverRenderKindForArtifact(before), printHTML),
	}
	job, reused, err := enqueueBoundRenderExportPDFJob(binding)
	if err != nil || reused || strings.TrimSpace(job.ID) == "" {
		t.Fatalf("enqueue interrupted exact render: job=%+v reused=%v err=%v", job, reused, err)
	}
	job.Status = renderJobStatusComplete
	job.CompletedAt = time.Now().UTC()
	job.Metadata = mergeStringMaps(job.Metadata, map[string]string{
		"status": renderJobStatusComplete, "renderRunner": renderJobStatusComplete,
	})
	if err := newRenderRunnerJobStore(queueDir).update(job); err != nil {
		t.Fatalf("persist terminal render receipt: %v", err)
	}
	if current := mustArtifact(t, app, before.ID); strings.TrimSpace(current.Metadata["renderJobId"]) != "" {
		t.Fatal("terminal crash fixture unexpectedly stamped the source artifact")
	}

	if err := writeHealthyRenderRunnerHeartbeatForTest(t, "studio-restart-render-runner"); err != nil {
		t.Fatalf("write restarted render heartbeat: %v", err)
	}
	restarted := newKanbanBoardApp()
	second, err := restarted.fileStudioShipDeliverables(inputs)
	if err != nil || len(second) != 1 {
		t.Fatalf("restart presentation compile: deliverables=%+v err=%v", second, err)
	}
	if second[0].ArtifactID != before.ID || second[0].RenderJob != "" ||
		!strings.Contains(second[0].RenderNote, "exact render job completed") ||
		!strings.Contains(second[0].RenderNote, "callback") {
		t.Fatalf("restart did not adopt the terminal exact render: %+v", second[0])
	}
	after := mustArtifact(t, restarted, before.ID)
	if artifactVersion(after) != beforeVersion || after.Text != before.Text || artifactCapabilityDigest(after) != beforeDigest {
		t.Fatalf("restart changed the exact filed presentation: before=v%d/%s after=v%d/%s",
			beforeVersion, beforeDigest, artifactVersion(after), artifactCapabilityDigest(after))
	}
	if after.Metadata["renderJobId"] != job.ID || after.Metadata["renderStatus"] != renderJobStatusComplete ||
		!strings.Contains(after.Metadata["renderError"], "callback") {
		t.Fatalf("terminal render recovery metadata=%v, want job %s + callback-pending disclosure", after.Metadata, job.ID)
	}
	files := renderQueueJSONFiles(t, queueDir)
	if len(files) != 1 || files[0] != job.ID+".json" {
		t.Fatalf("restart queued a duplicate render: %v", files)
	}
}

func TestStudioResearchRestartAdoptsTerminalRenderWithoutDuplicate(t *testing.T) {
	queueDir := setupRenderSidecarEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	const parentID = "studio-terminal-research-parent"
	report, appended, err := app.createOSArtifactWithMetadata("artifacts", "Restart-safe research", "# Restart-safe research\n\nA decision-ready report.", "tester", map[string]string{
		"type": artifactTypeMarkdown, "goalParentId": parentID, "goalSubtaskId": "write",
		"outputContract": documentReportOutputContract, "status": "complete", "threadStatus": "complete",
	})
	if err != nil || !appended {
		t.Fatalf("file research report: appended=%v err=%v", appended, err)
	}
	gate, appended, err := app.createOSArtifactWithMetadata("workflow", "Research text gate", "Text quality passed.", "tester", map[string]string{
		"source": "process_stage", "processStage": "quality_gate", "goalParentId": parentID,
	})
	if err != nil || !appended {
		t.Fatalf("file research gate: appended=%v err=%v", appended, err)
	}
	plan := &goalPlan{ProcessID: documentReportProcessID, Subtasks: []goalSubtask{
		{ID: "write", Status: subtaskComplete, ArtifactID: report.ID},
		{ID: "quality_gate", Status: subtaskComplete, ArtifactID: gate.ID, Review: &goalSubtaskReview{Verdict: goalReviewPass}},
	}}
	beforeVersion := artifactVersion(report)
	beforeDigest := artifactCapabilityDigest(report)
	printHTML := renderResearchReportPrintHTML(report)
	binding := renderPDFJobBinding{
		ArtifactID: report.ID, Kind: renderJobKindPaper, HTML: printHTML, Title: report.Metadata["title"],
		SourceArtifactVersion: beforeVersion, SourceContentDigest: renderPDFContentDigest(renderJobKindPaper, printHTML),
	}
	job, reused, err := enqueueBoundRenderExportPDFJob(binding)
	if err != nil || reused || strings.TrimSpace(job.ID) == "" {
		t.Fatalf("enqueue interrupted research render: job=%+v reused=%v err=%v", job, reused, err)
	}
	job.Status = renderJobStatusComplete
	job.CompletedAt = time.Now().UTC()
	job.Metadata = mergeStringMaps(job.Metadata, map[string]string{
		"status": renderJobStatusComplete, "renderRunner": renderJobStatusComplete,
	})
	if err := newRenderRunnerJobStore(queueDir).update(job); err != nil {
		t.Fatalf("persist terminal research render: %v", err)
	}
	if err := writeHealthyRenderRunnerHeartbeatForTest(t, "studio-research-restart-render-runner"); err != nil {
		t.Fatalf("write restarted research heartbeat: %v", err)
	}

	body, metadata, err := compileDocumentReportDraftRender(app, plan, parentID, ProcessStage{})
	if err != nil || metadata["reviewVerdict"] != "needs_attention" ||
		!strings.Contains(metadata["attentionReason"], "exact PDF render completed") ||
		!strings.Contains(metadata["attentionReason"], "callback") || !strings.Contains(body, "needs attention") {
		t.Fatalf("research restart did not disclose terminal callback recovery: metadata=%v body=%q err=%v", metadata, body, err)
	}
	after := mustArtifact(t, app, report.ID)
	if artifactVersion(after) != beforeVersion || after.Text != report.Text || artifactCapabilityDigest(after) != beforeDigest {
		t.Fatalf("research restart changed the exact report: before=v%d/%s after=v%d/%s",
			beforeVersion, beforeDigest, artifactVersion(after), artifactCapabilityDigest(after))
	}
	if after.Metadata["renderJobId"] != job.ID || after.Metadata["renderStatus"] != renderJobStatusComplete ||
		!strings.Contains(after.Metadata["renderError"], "callback") {
		t.Fatalf("research terminal render metadata=%v, want job %s + callback-pending disclosure", after.Metadata, job.ID)
	}
	files := renderQueueJSONFiles(t, queueDir)
	if len(files) != 1 || files[0] != job.ID+".json" {
		t.Fatalf("research restart queued a duplicate render: %v", files)
	}
}

func TestNonChatStudioWriterLaunchMintsStableProviderKeyWithoutOperationReceipt(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread, err := app.launchGoalAgentThreadScaffold("artifacts", "Build the exact current presentation", "AJ", nil, agentThreadGoalSpec{
		ParentGoalID: "direct-studio-parent", SubtaskID: "ship_deck", AssignedRunner: agentRunnerOpenAIText,
		Authority: codexJobAuthorityWorkspaceWrite, Deliverable: true, OutputContract: packagingStudioDeckContract,
		ProviderReplayClass: goalChildProviderReplayStudioWriterV1, ProviderReplayProcessID: packagingStudioProcessID,
		ProviderReplayProcessDigest: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	started, _, err := app.updateOSArtifactWithMetadata(thread.Artifact.ID, "", thread.Artifact.Text, "AJ", map[string]string{
		"goalChildActivationState": goalChildActivationStarted,
	})
	if err != nil {
		t.Fatal(err)
	}
	thread.Artifact = started
	first := publicConversationProviderOperationKey(thread)
	second := publicConversationProviderOperationKey(thread)
	if first == "" || first != second {
		t.Fatalf("non-chat Studio writer key=%q/%q, want one stable server-derived key", first, second)
	}
	if strings.TrimSpace(started.Metadata["operationId"]) != "" || strings.TrimSpace(started.Metadata["operationBodyDigest"]) != "" {
		t.Fatal("test launch unexpectedly acquired a conversation operation receipt")
	}
}

func TestStudioRestartReplaysExactFrozenWriterChildOnce(t *testing.T) {
	fixture := newStudioRestartFixture(t)
	stage, writer := fixture.persistOnlyRunningStage(t, "layout_plan")
	receipt := fixture.plan.RouteReceipt
	if receipt == nil {
		t.Fatal("Studio route receipt is unavailable")
	}
	query := writer.Title
	if strings.TrimSpace(writer.Detail) != "" {
		query += " — " + writer.Detail
	}
	spec := agentThreadGoalSpec{
		Objective:                   query,
		ContextRefs:                 fixture.engine.processStageContextRefs(&fixture.plan),
		RequestedBy:                 goalPlanRequestedBy(fixture.plan),
		Authority:                   goalChildAuthority(writer.Authority, fixture.plan.Authority),
		ParentGoalID:                fixture.parentID,
		SubtaskID:                   writer.ID,
		AssignedRunner:              writer.Runner,
		OutputContract:              stage.OutputContract,
		Deliverable:                 true,
		SourceMessageID:             receipt.SourceMessageID,
		SourceMessageDigest:         receipt.SourceMessageDigest,
		SourceWindowDigest:          receipt.SourceWindowDigest,
		OperationID:                 receipt.OperationID,
		OperationBodyDigest:         receipt.OperationBodyDigest,
		ParentGoalRouteDigest:       receipt.Digest,
		ProviderReplayClass:         goalChildProviderReplayStudioWriterV1,
		ProviderReplayProcessID:     fixture.plan.ProcessID,
		ProviderReplayProcessDigest: fixture.plan.ProcessDigest,
	}
	thread, err := fixture.app.launchGoalAgentThreadScaffold(writer.Mode, query, fixture.plan.CreatedBy, goalRouteChildBindingMetadata(&fixture.plan), spec)
	if err != nil {
		t.Fatal(err)
	}
	writer.ThreadID = thread.ID
	writer.ArtifactID = thread.Artifact.ID
	if persisted := fixture.engine.persist(&fixture.plan, fixture.parentID, ""); persisted.ID == "" {
		t.Fatal("persist Studio writer reservation")
	}

	originalAgentStart := startAgentThreadAsync
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = originalAgentStart })
	if err := fixture.app.activateReservedGoalAgentThread(thread, spec, fixture.plan.CreatedBy); err != nil {
		t.Fatalf("activate and freeze Studio writer: %v", err)
	}
	frozenArtifact, found := fixture.app.osArtifactByID(thread.Artifact.ID)
	if !found || strings.TrimSpace(frozenArtifact.Metadata[publicConversationProviderRequestKey]) == "" {
		t.Fatal("Studio writer request was not frozen before provider handoff")
	}
	frozenThread := scoutAgentThread{ID: thread.ID, Mode: writer.Mode, Query: query, Status: "running", Artifact: frozenArtifact}
	frozenKey := publicConversationProviderOperationKey(frozenThread)
	if frozenKey == "" {
		t.Fatal("Studio writer provider operation key is empty")
	}

	restarted := newKanbanBoardApp()
	restarted.apiKey = "studio-restart-test-key"
	var replayMu sync.Mutex
	var replayed []scoutAgentThread
	startAgentThreadAsync = func(_ *kanbanBoardApp, replay scoutAgentThread) {
		replayMu.Lock()
		replayed = append(replayed, replay)
		replayMu.Unlock()
	}
	restarted.reconcileGoalThread(fixture.parentID)
	restarted.reconcileGoalThread(fixture.parentID)
	replayMu.Lock()
	replays := append([]scoutAgentThread(nil), replayed...)
	replayMu.Unlock()
	if len(replays) != 1 || replays[0].ID != thread.ID || replays[0].Artifact.ID != thread.Artifact.ID {
		t.Fatalf("writer replays=%+v, want the exact reserved child once", replays)
	}
	if got := publicConversationProviderOperationKey(replays[0]); got != frozenKey {
		t.Fatalf("replayed provider key=%q, want frozen %q", got, frozenKey)
	}
	replayedArtifact, found := restarted.osArtifactByID(thread.Artifact.ID)
	if !found || replayedArtifact.Metadata[publicConversationProviderRequestKey] != frozenArtifact.Metadata[publicConversationProviderRequestKey] ||
		replayedArtifact.Metadata[publicConversationProviderRequestHash] != frozenArtifact.Metadata[publicConversationProviderRequestHash] {
		t.Fatal("writer replay rebuilt or lost the exact frozen provider request")
	}

	providerContext, err := restarted.agentThreadProviderContext(context.Background(), replays[0])
	if err != nil {
		t.Fatalf("build replayed writer provider context: %v", err)
	}
	job := restarted.newAgentJob(replays[0])
	job.Context = providerContext
	var providerRequests []openAITextRequest
	output, err := restarted.produceAgentThreadArtifactForJob(
		withOpenAIProviderInvocationRetryDelay(context.Background(), 0),
		job,
		func(_ context.Context, _ string, request openAITextRequest) (string, error) {
			providerRequests = append(providerRequests, request)
			if len(providerRequests) == 1 {
				return "", &openAIProviderFailure{err: fmt.Errorf("transient provider invocation failure")}
			}
			return "exact frozen writer output", nil
		},
	)
	if err != nil || output != "exact frozen writer output" {
		t.Fatalf("replayed writer invocation output=%q err=%v", output, err)
	}
	if len(providerRequests) != 2 || providerRequests[0].IdempotencyKey != frozenKey || providerRequests[1].IdempotencyKey != frozenKey ||
		providerRequests[0].Instructions != providerRequests[1].Instructions || providerRequests[0].Input != providerRequests[1].Input {
		t.Fatalf("writer invocation retry did not reuse the exact frozen request: %+v", providerRequests)
	}
}

func TestStudioRestartFreezesWriterInterruptedBeforeProviderSnapshot(t *testing.T) {
	fixture := newStudioRestartFixture(t)
	stage, writer := fixture.persistOnlyRunningStage(t, "layout_plan")
	receipt := fixture.plan.RouteReceipt
	query := writer.Title
	spec := agentThreadGoalSpec{
		Objective: query, ContextRefs: fixture.engine.processStageContextRefs(&fixture.plan), RequestedBy: goalPlanRequestedBy(fixture.plan),
		Authority: goalChildAuthority(writer.Authority, fixture.plan.Authority), ParentGoalID: fixture.parentID, SubtaskID: writer.ID,
		AssignedRunner: writer.Runner, OutputContract: stage.OutputContract, Deliverable: true,
		SourceMessageID: receipt.SourceMessageID, SourceMessageDigest: receipt.SourceMessageDigest, SourceWindowDigest: receipt.SourceWindowDigest,
		OperationID: receipt.OperationID, OperationBodyDigest: receipt.OperationBodyDigest, ParentGoalRouteDigest: receipt.Digest,
		ProviderReplayClass: goalChildProviderReplayStudioWriterV1, ProviderReplayProcessID: fixture.plan.ProcessID,
		ProviderReplayProcessDigest: fixture.plan.ProcessDigest,
	}
	thread, err := fixture.app.launchGoalAgentThreadScaffold(writer.Mode, query, fixture.plan.CreatedBy, goalRouteChildBindingMetadata(&fixture.plan), spec)
	if err != nil {
		t.Fatal(err)
	}
	writer.ThreadID, writer.ArtifactID = thread.ID, thread.Artifact.ID
	if persisted := fixture.engine.persist(&fixture.plan, fixture.parentID, ""); persisted.ID == "" {
		t.Fatal("persist Studio writer parent reservation")
	}
	started, _, err := fixture.app.updateOSArtifactWithMetadata(thread.Artifact.ID, "", thread.Artifact.Text, fixture.plan.CreatedBy, map[string]string{
		"goalChildActivationState": goalChildActivationStarted,
		"goalChildActivatedAt":     time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(started.Metadata[publicConversationProviderRequestKey]) != "" {
		t.Fatal("pre-snapshot crash fixture unexpectedly froze a provider request")
	}
	startedThread := scoutAgentThread{ID: thread.ID, Mode: writer.Mode, Query: query, Status: "running", Artifact: started}
	wantKey := publicConversationProviderOperationKey(startedThread)
	if wantKey == "" {
		t.Fatal("pre-snapshot Studio writer has no deterministic provider key")
	}

	restarted := newKanbanBoardApp()
	restarted.apiKey = "studio-restart-test-key"
	originalAgentStart := startAgentThreadAsync
	var starts atomic.Int32
	var replay scoutAgentThread
	startAgentThreadAsync = func(_ *kanbanBoardApp, current scoutAgentThread) {
		replay = current
		starts.Add(1)
	}
	t.Cleanup(func() { startAgentThreadAsync = originalAgentStart })
	restarted.reconcileGoalThread(fixture.parentID)
	restarted.reconcileGoalThread(fixture.parentID)
	if starts.Load() != 1 || replay.ID != thread.ID || replay.Artifact.ID != thread.Artifact.ID {
		t.Fatalf("pre-snapshot writer starts=%d replay=%+v, want the exact child once", starts.Load(), replay)
	}
	if strings.TrimSpace(replay.Artifact.Metadata[publicConversationProviderRequestKey]) == "" || publicConversationProviderOperationKey(replay) != wantKey {
		t.Fatal("restart did not freeze the exact deterministic writer request before handoff")
	}
}

func TestInlineModelRetryIsKeyedBoundedAndInvocationOnly(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "inline-provider-retry-test-key")
	engine := newGoalEngine(nil)
	ctx := withGoalInlineProviderOperation(context.Background(), "inline-execution-retry-key")
	ctx = withOpenAIProviderInvocationRetryDelay(ctx, 0)

	var invocationRequests []openAITextRequest
	engine.openAIResponder = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		invocationRequests = append(invocationRequests, request)
		if len(invocationRequests) == 1 {
			return "", &openAIProviderFailure{err: fmt.Errorf("provider connection reset")}
		}
		return "recovered", nil
	}
	output, err := engine.callModelAs(ctx, engine.model, seatGoalEngine, "stable system", "stable input")
	if err != nil || output != "recovered" {
		t.Fatalf("inline retry output=%q err=%v", output, err)
	}
	if len(invocationRequests) != 2 || invocationRequests[0].IdempotencyKey == "" ||
		invocationRequests[0].IdempotencyKey != invocationRequests[1].IdempotencyKey ||
		invocationRequests[0].Instructions != invocationRequests[1].Instructions || invocationRequests[0].Input != invocationRequests[1].Input {
		t.Fatalf("inline invocation retry changed the deterministic request: %+v", invocationRequests)
	}

	outputCalls := 0
	engine.openAIResponder = func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		outputCalls++
		return "", &openAIOutputRejection{reason: "invalid structured output"}
	}
	if _, err := engine.callModelAs(ctx, engine.model, seatGoalEngine, "stable system", "stable input"); err == nil || !isProviderOutputRejection(err) {
		t.Fatalf("output rejection=%v, want the original quality failure", err)
	}
	if outputCalls != 1 {
		t.Fatalf("output rejection calls=%d, want no semantic retry", outputCalls)
	}

	unkeyedCalls := 0
	engine.openAIResponder = func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		unkeyedCalls++
		return "", &openAIProviderFailure{err: fmt.Errorf("provider unavailable")}
	}
	if _, err := engine.callModelAs(withOpenAIProviderInvocationRetryDelay(context.Background(), 0), engine.model, seatGoalEngine, "ordinary system", "ordinary input"); err == nil || !isProviderInvocationFailure(err) {
		t.Fatalf("ordinary invocation failure=%v, want provider failure", err)
	}
	if unkeyedCalls != 1 {
		t.Fatalf("ordinary unkeyed calls=%d, want one-shot behavior", unkeyedCalls)
	}
}
