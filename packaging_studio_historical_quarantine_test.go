package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

const packagingStudioHistoricalV4DigestForTest = "b3001cc5bf1a32fce682a446ead3c495e76683a42b66dec0c3c7ef24e3ed7f3d"

type packagingStudioHistoricalGoalFixture struct {
	parent     meetingMemoryEntry
	plan       goalPlan
	definition ProcessDefinition
	identity   processDefinitionIdentity
}

func persistPackagingStudioHistoricalPlanForTest(t *testing.T, app *kanbanBoardApp, parentID string, plan goalPlan) meetingMemoryEntry {
	t.Helper()
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	status := "running"
	if plan.State == goalStateVerified {
		status = codexJobStatusComplete
	}
	_, _, err = app.memory.updateOSArtifactMetadata(parentID, map[string]string{
		"goalPlan":                      string(raw),
		"goalRouteDigest":               plan.RouteReceipt.Digest,
		"processId":                     plan.ProcessID,
		"processVersion":                strconv.Itoa(plan.ProcessVersion),
		"processDigest":                 plan.ProcessDigest,
		"processImplementationRevision": plan.ProcessImplementationRevision,
		"resultStageId":                 plan.ResultStageID,
		"resultOutputContract":          plan.ResultOutputContract,
		"currentStage":                  plan.State,
		"status":                        status,
		"threadStatus":                  status,
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact, ok := app.osArtifactByID(parentID)
	if !ok {
		t.Fatalf("historical parent %s disappeared after plan persistence", parentID)
	}
	return artifact
}

func launchAuthenticatedPackagingStudioHistoricalGoalForTest(t *testing.T, app *kanbanBoardApp, definition ProcessDefinition, state string) packagingStudioHistoricalGoalFixture {
	t.Helper()
	run, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Build the authenticated historical presentation", CreatedBy: "aj@shareability.com", ToolTemplate: packagingStudioProcessID,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := mustGoalPlan(t, app, run.Artifact.ID)
	identity, err := processDefinitionIdentityFor(definition)
	if err != nil {
		t.Fatal(err)
	}
	plan.ProcessID = identity.ID
	plan.ProcessVersion = identity.Version
	plan.ProcessDigest = identity.Digest
	plan.ProcessImplementationRevision = identity.ImplementationRevision
	plan.ResultStageID = identity.ResultStageID
	plan.ResultOutputContract = identity.ResultOutputContract
	if err := instantiateProcessPlan(definition, &plan); err != nil {
		t.Fatal(err)
	}
	assignGoalRunners(&plan)
	plan.State = state
	if state == goalStateVerified {
		for index := range plan.Subtasks {
			plan.Subtasks[index].Status = subtaskComplete
		}
		plan.Verification.Verdict = goalReviewPass
	}
	receipt := *plan.RouteReceipt
	receipt.ProcessID = identity.ID
	receipt.ProcessVersion = identity.Version
	receipt.ProcessDigest = identity.Digest
	receipt.ProcessImplementationRevision = identity.ImplementationRevision
	receipt.ResultStageID = identity.ResultStageID
	receipt.ResultOutputContract = identity.ResultOutputContract
	receipt.Digest, err = receipt.contractDigest()
	if err != nil {
		t.Fatal(err)
	}
	plan.RouteReceipt = &receipt
	parent := persistPackagingStudioHistoricalPlanForTest(t, app, run.Artifact.ID, plan)
	persisted := mustGoalPlan(t, app, parent.ID)
	if err := newGoalEngine(app).prepareGoalRoute(&persisted, parent.ID); err != nil {
		t.Fatalf("authenticate historical v%d route: %v", identity.Version, err)
	}
	return packagingStudioHistoricalGoalFixture{parent: parent, plan: persisted, definition: definition, identity: identity}
}

func seedPackagingStudioHistoricalChildForTest(t *testing.T, app *kanbanBoardApp, fixture *packagingStudioHistoricalGoalFixture, activation string, status string) meetingMemoryEntry {
	t.Helper()
	stage, found := fixture.definition.stageByID("external_research")
	if !found || stage.Role != processRoleWriter || stage.OutputContract != packagingStudioExternalEvidenceContract {
		t.Fatalf("historical v%d external research stage changed: %+v", fixture.identity.Version, stage)
	}
	st := fixture.plan.subtaskByID(stage.ID)
	if st == nil {
		t.Fatalf("historical v%d plan has no %s subtask", fixture.identity.Version, stage.ID)
	}
	st.Status = subtaskRunning
	st.Attempts = 1
	receipt := fixture.plan.RouteReceipt
	spec := agentThreadGoalSpec{
		Objective: "Historical evidence request", RequestedBy: receipt.Requester,
		Authority: goalChildAuthority(st.Authority, fixture.plan.Authority), ParentGoalID: fixture.parent.ID,
		SubtaskID: st.ID, AssignedRunner: st.Runner, OutputContract: stage.OutputContract,
		SourceMessageID: receipt.SourceMessageID, SourceMessageDigest: receipt.SourceMessageDigest,
		SourceWindowDigest: receipt.SourceWindowDigest, OperationID: receipt.OperationID,
		OperationBodyDigest: receipt.OperationBodyDigest, ParentGoalRouteDigest: receipt.Digest,
	}
	child, err := app.launchGoalAgentThreadScaffold(processStageThreadMode(stage), spec.Objective, fixture.plan.CreatedBy, goalRouteChildBindingMetadata(&fixture.plan), spec)
	if err != nil {
		t.Fatal(err)
	}
	st.ThreadID = child.ID
	st.ArtifactID = child.Artifact.ID
	fixture.parent = persistPackagingStudioHistoricalPlanForTest(t, app, fixture.parent.ID, fixture.plan)

	updates := map[string]string{
		"goalChildActivationState": activation,
		"status":                   status, "threadStatus": status, "goalStatus": status,
	}
	if activation == goalChildActivationStarted {
		updates[publicConversationProviderRequestKey] = "historical-provider-request-must-not-replay"
	}
	body := child.Artifact.Text
	if status == codexJobStatusComplete {
		body = strings.Repeat("historical terminal child body must remain inspectable; ", 20)
	}
	updated, _, err := app.updateOSArtifactWithMetadata(child.Artifact.ID, "", body, scoutParticipantName, updates)
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func packagingStudioHistoricalDefinitionsForTest() []struct {
	name string
	def  ProcessDefinition
} {
	return []struct {
		name string
		def  ProcessDefinition
	}{
		{name: "v4", def: packagingStudioDefinitionV4()},
		{name: "v5", def: packagingStudioDefinitionV5()},
	}
}

func TestPackagingStudioV4IdentityAndPinnedResolutionRemainExact(t *testing.T) {
	historical := packagingStudioDefinitionV4()
	identity, err := processDefinitionIdentityFor(historical)
	if err != nil {
		t.Fatal(err)
	}
	if packagingStudioV4Digest != packagingStudioHistoricalV4DigestForTest || identity.ID != packagingStudioProcessID || identity.Version != 4 ||
		identity.Digest != packagingStudioHistoricalV4DigestForTest || identity.ImplementationRevision != "packaging_studio.runtime.v4" ||
		identity.ResultStageID != "ship_deck" || identity.ResultOutputContract != packagingStudioDeckContract || len(historical.Stages) != 18 {
		t.Fatalf("frozen v4 identity drifted: constant=%q stages=%d identity=%+v", packagingStudioV4Digest, len(historical.Stages), identity)
	}
	plan := &goalPlan{
		ProcessID: identity.ID, ProcessVersion: identity.Version, ProcessDigest: identity.Digest,
		ProcessImplementationRevision: identity.ImplementationRevision, ResultStageID: identity.ResultStageID,
		ResultOutputContract: identity.ResultOutputContract, routeVerified: true,
	}
	resolved, err := resolvePinnedProcessDefinition(plan)
	if err != nil {
		t.Fatalf("exact v4 identity is no longer inspectable: %v", err)
	}
	resolvedIdentity, err := processDefinitionIdentityFor(resolved)
	if err != nil || resolvedIdentity != identity {
		t.Fatalf("v4 resolved to different bytes: got=%+v want=%+v err=%v", resolvedIdentity, identity, err)
	}
	tampered := *plan
	tampered.ProcessDigest = sha256Hex([]byte("tampered frozen v4 definition"))
	if _, err := resolvePinnedProcessDefinition(&tampered); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("tampered v4 digest resolved: %v", err)
	}
	current, ok := processByID(packagingStudioProcessID)
	if !ok || current.Version != packagingStudioCurrentVersion {
		t.Fatalf("historical pin displaced current launches: ok=%t version=%d", ok, current.Version)
	}
}

func TestPackagingStudioHistoricalImageryNeverCompilesOrInjects(t *testing.T) {
	for _, historical := range packagingStudioHistoricalDefinitionsForTest() {
		t.Run(historical.name, func(t *testing.T) {
			app := newIsolatedKanbanBoardApp(t)
			identity, err := processDefinitionIdentityFor(historical.def)
			if err != nil {
				t.Fatal(err)
			}
			imagery, _, err := app.createOSArtifactWithMetadata("workflow", "Historical imagery", "hostile pre-v7 imagery record", "Scout", map[string]string{
				"imageryFigs": `[{"fig":1,"ref":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","mime":"image/png"}]`,
			})
			if err != nil {
				t.Fatal(err)
			}
			plan := &goalPlan{
				ProcessID: identity.ID, ProcessVersion: identity.Version, ProcessDigest: identity.Digest,
				ProcessImplementationRevision: identity.ImplementationRevision, ResultStageID: identity.ResultStageID,
				ResultOutputContract: identity.ResultOutputContract,
				Subtasks:             []goalSubtask{{ID: "imagery_generate", Status: subtaskComplete, ArtifactID: imagery.ID}},
			}
			beforeEntries := len(app.memory.snapshot(0))
			body, metadata, compileErr := compilePackagingStudioImagery(app, plan, "historical-parent", ProcessStage{ID: "imagery_generate"})
			if compileErr == nil || !strings.Contains(compileErr.Error(), packagingStudioHistoricalRelaunchMessage) || body != "" || metadata != nil {
				t.Fatalf("historical v%d imagery compile escaped quarantine: body=%q metadata=%v err=%v", identity.Version, body, metadata, compileErr)
			}
			deck := `<!doctype html><html><body><div class="fig-1">PRE_V7_CANARY</div></body></html>`
			augmented, note, injectErr := injectStudioDeckImagery(app, plan, deck)
			if injectErr == nil || !strings.Contains(injectErr.Error(), packagingStudioHistoricalRelaunchMessage) || augmented != deck || note != "" || strings.Contains(augmented, "data:image/") {
				t.Fatalf("historical v%d imagery was inserted: changed=%t note=%q err=%v body=%s", identity.Version, augmented != deck, note, injectErr, augmented)
			}
			if afterEntries := len(app.memory.snapshot(0)); afterEntries != beforeEntries {
				t.Fatalf("historical v%d imagery quarantine persisted provider output: before=%d after=%d", identity.Version, beforeEntries, afterEntries)
			}
		})
	}
}

func TestAuthenticatedPackagingStudioHistoricalRunBlocksEveryExecutionSeam(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "historical-packaging-quarantine-test")
	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerOpenAIText)
	previousGoalStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStart })
	previousAgentStart := startAgentThreadAsync
	var agentStarts atomic.Int32
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) { agentStarts.Add(1) }
	t.Cleanup(func() { startAgentThreadAsync = previousAgentStart })

	for _, historical := range packagingStudioHistoricalDefinitionsForTest() {
		t.Run(historical.name+" drive blocks dispatch and salvage", func(t *testing.T) {
			agentStarts.Store(0)
			app := newIsolatedKanbanBoardApp(t)
			app.apiKey = "historical-packaging-quarantine-test"
			fixture := launchAuthenticatedPackagingStudioHistoricalGoalForTest(t, app, historical.def, goalStateExecute)
			draft, _, err := app.createOSArtifactWithMetadata("artifacts", "Historical draft", strings.Repeat("salvage candidate that must remain unattached; ", 20), "Scout", nil)
			if err != nil {
				t.Fatal(err)
			}
			ship := fixture.plan.subtaskByID("ship_deck")
			if ship == nil {
				t.Fatal("historical plan has no ship_deck stage")
			}
			ship.Status = subtaskFailed
			ship.ArtifactID = draft.ID
			if ready := fixture.plan.subtaskByID("external_research"); ready != nil {
				ready.Status = subtaskReady
			}
			fixture.parent = persistPackagingStudioHistoricalPlanForTest(t, app, fixture.parent.ID, fixture.plan)
			childrenBefore := 0
			for _, entry := range app.memory.snapshot(0) {
				if entry.Metadata["goalParentId"] == fixture.parent.ID {
					childrenBefore++
				}
			}
			app.runGoalThread(fixture.parent.ID)
			after := mustGoalPlan(t, app, fixture.parent.ID)
			childrenAfter := 0
			for _, entry := range app.memory.snapshot(0) {
				if entry.Metadata["goalParentId"] == fixture.parent.ID {
					childrenAfter++
				}
			}
			if after.State != goalStateBlocked || !strings.Contains(after.Blocker, packagingStudioHistoricalRelaunchMessage) ||
				after.Report.DeliverableArtifactID != "" || childrenAfter != childrenBefore || agentStarts.Load() != 0 {
				t.Fatalf("historical v%d drive crossed quarantine: state=%q blocker=%q salvage=%q children=%d/%d starts=%d", fixture.identity.Version, after.State, after.Blocker, after.Report.DeliverableArtifactID, childrenBefore, childrenAfter, agentStarts.Load())
			}
		})

		for _, recovery := range []struct {
			name       string
			activation string
		}{
			{name: "reserved child is not activated", activation: goalChildActivationReserved},
			{name: "started child is not provider-replayed", activation: goalChildActivationStarted},
		} {
			t.Run(historical.name+" reconcile "+recovery.name, func(t *testing.T) {
				agentStarts.Store(0)
				app := newIsolatedKanbanBoardApp(t)
				app.apiKey = "historical-packaging-quarantine-test"
				fixture := launchAuthenticatedPackagingStudioHistoricalGoalForTest(t, app, historical.def, goalStateExecute)
				child := seedPackagingStudioHistoricalChildForTest(t, app, &fixture, recovery.activation, "running")
				app.reconcileGoalThread(fixture.parent.ID)
				after := mustGoalPlan(t, app, fixture.parent.ID)
				storedChild, ok := app.osArtifactByID(child.ID)
				st := after.subtaskByID("external_research")
				if after.State != goalStateBlocked || !strings.Contains(after.Blocker, packagingStudioHistoricalRelaunchMessage) ||
					after.Report.DeliverableArtifactID != "" || agentStarts.Load() != 0 || !ok ||
					storedChild.Metadata["goalChildActivationState"] != recovery.activation || st == nil || st.Status != subtaskRunning {
					t.Fatalf("historical v%d reconcile crossed quarantine: plan=%+v child=%+v starts=%d", fixture.identity.Version, after, storedChild.Metadata, agentStarts.Load())
				}
			})
		}

		t.Run(historical.name+" child route and fold are refused", func(t *testing.T) {
			agentStarts.Store(0)
			app := newIsolatedKanbanBoardApp(t)
			app.apiKey = "historical-packaging-quarantine-test"
			fixture := launchAuthenticatedPackagingStudioHistoricalGoalForTest(t, app, historical.def, goalStateExecute)
			child := seedPackagingStudioHistoricalChildForTest(t, app, &fixture, goalChildActivationStarted, codexJobStatusComplete)
			if err := app.verifyGoalChildRoute(child); err == nil || !strings.Contains(err.Error(), "current process relaunch") {
				t.Fatalf("historical v%d child route admitted: %v", fixture.identity.Version, err)
			}
			app.foldGoalChildCompletion(fixture.parent.ID, "external_research", child, codexJobStatusComplete)
			after := mustGoalPlan(t, app, fixture.parent.ID)
			st := after.subtaskByID("external_research")
			storedChild, ok := app.osArtifactByID(child.ID)
			if after.State != goalStateBlocked || !strings.Contains(after.Blocker, packagingStudioHistoricalRelaunchMessage) ||
				after.Report.DeliverableArtifactID != "" || st == nil || st.Status != subtaskRunning ||
				!ok || storedChild.Metadata["threadStatus"] != codexJobStatusComplete || agentStarts.Load() != 0 {
				t.Fatalf("historical v%d child folded or salvaged: plan=%+v child=%+v starts=%d", fixture.identity.Version, after, storedChild.Metadata, agentStarts.Load())
			}
		})
	}
}

func TestCompletedVerifiedPackagingStudioHistoricalReceiptsRemainInspectable(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "historical-packaging-inspection-test")
	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerOpenAIText)
	previousGoalStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStart })

	for _, historical := range packagingStudioHistoricalDefinitionsForTest() {
		t.Run(historical.name, func(t *testing.T) {
			app := newIsolatedKanbanBoardApp(t)
			app.apiKey = "historical-packaging-inspection-test"
			fixture := launchAuthenticatedPackagingStudioHistoricalGoalForTest(t, app, historical.def, goalStateVerified)
			before := mustGoalPlan(t, app, fixture.parent.ID)
			if err := newGoalEngine(app).prepareGoalRoute(&before, fixture.parent.ID); err != nil {
				t.Fatalf("verified historical v%d receipt is not authentic: %v", fixture.identity.Version, err)
			}
			if err := app.verifyGoalRouteReceipt(&before, *before.RouteReceipt); err != nil {
				t.Fatalf("verified historical v%d receipt is not inspectable: %v", fixture.identity.Version, err)
			}
			resolved, err := resolvePinnedProcessDefinition(&before)
			if err != nil {
				t.Fatalf("verified historical v%d definition is not inspectable: %v", fixture.identity.Version, err)
			}
			resolvedIdentity, err := processDefinitionIdentityFor(resolved)
			if err != nil || resolvedIdentity != fixture.identity {
				t.Fatalf("verified historical v%d resolved incorrectly: got=%+v want=%+v err=%v", fixture.identity.Version, resolvedIdentity, fixture.identity, err)
			}
			app.reconcileGoalThreadsAtBoot()
			after := mustGoalPlan(t, app, fixture.parent.ID)
			if after.State != goalStateVerified || after.RouteReceipt == nil || after.RouteReceipt.Digest != before.RouteReceipt.Digest || after.Blocker != "" {
				t.Fatalf("verified historical v%d receipt was mutated by boot inspection: before=%+v after=%+v", fixture.identity.Version, before, after)
			}
			if _, ok := app.osArtifactByID(fixture.parent.ID); !ok {
				t.Fatalf("verified historical v%d parent disappeared", fixture.identity.Version)
			}
		})
	}
}
