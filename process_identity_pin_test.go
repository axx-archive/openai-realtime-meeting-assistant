package main

import (
	"context"
	"strings"
	"testing"
)

func processIdentityCompileV1(_ *kanbanBoardApp, _ *goalPlan, _ string, _ ProcessStage) (string, map[string]string, error) {
	return "compiled-v1", nil, nil
}

func processIdentityCompileV2(_ *kanbanBoardApp, _ *goalPlan, _ string, _ ProcessStage) (string, map[string]string, error) {
	return "compiled-v2", nil, nil
}

func processIdentityFixture(id string) ProcessDefinition {
	return ProcessDefinition{
		ID: id, Version: 1, Title: "Process identity fixture", Description: "Pins every executable process surface.",
		Authority: codexJobAuthorityWorkspaceWrite, Hidden: true,
		ImplementationRevision: "test." + id + ".runtime.v1",
		Budgets:                ProcessBudgets{MaxSubtasks: 8, MaxTokens: 12000},
		Stages: []ProcessStage{
			{ID: "source_a", Title: "Write source A", Role: processRoleWriter, Mode: "artifacts", PromptBody: "write source A", OutputContract: "source_a_v1"},
			{ID: "source_b", Title: "Write source B", Role: processRoleWriter, Mode: "artifacts", PromptBody: "write source B", OutputContract: "source_b_v1"},
			{ID: "decision", Title: "Choose a route", Role: processRoleSynthesizer, InputFrom: []string{"source_a", "source_b"}, PromptBody: "choose the route", OutputContract: "decision_v1"},
			{ID: "conditional", Title: "Run the selected route", Role: processRoleSynthesizer, InputFrom: []string{"decision"}, RunIf: &ProcessStageCondition{StageID: "decision", Field: "route", Equals: "run"}, PromptBody: "execute the selected route", OutputContract: "conditional_v1"},
			{ID: "gate", Title: "Gate the work", Role: processRoleGate, InputFrom: []string{"conditional"}, PromptBody: "score the work", GateSpec: &ProcessGateSpec{Threshold: 8, Floor: 6, MaxRounds: 2, Dimensions: []string{"Truth", "Craft"}}},
			{ID: "checkpoint", Title: "Choose the outcome", Role: processRoleHumanCheckpoint, InputFrom: []string{"gate"}, CheckpointSpec: &ProcessCheckpointSpec{Question: "Ship or hold?", Options: []ProcessCheckpointOption{{Label: "ship"}, {Label: "hold", Action: processCheckpointActionHold}}}},
			{ID: "compile", Title: "Compile the result", Role: processRoleCompile, InputFrom: []string{"checkpoint"}, PromptBody: "compile exactly", Compile: processIdentityCompileV1},
		},
	}
}

func replaceRegisteredProcessForIdentityTest(t *testing.T, def ProcessDefinition) {
	t.Helper()
	if err := validateProcessDefinition(def); err != nil {
		t.Fatalf("replacement definition invalid: %v", err)
	}
	processRegistryMu.Lock()
	defer processRegistryMu.Unlock()
	for index := range registeredProcessDefinitions {
		if registeredProcessDefinitions[index].ID == def.ID {
			registeredProcessDefinitions[index] = cloneProcessDefinition(def)
			return
		}
	}
	t.Fatalf("registered process %q not found", def.ID)
}

func removeRegisteredProcessForIdentityTest(t *testing.T, id string) {
	t.Helper()
	processRegistryMu.Lock()
	defer processRegistryMu.Unlock()
	kept := registeredProcessDefinitions[:0]
	for _, def := range registeredProcessDefinitions {
		if def.ID != id {
			kept = append(kept, def)
		}
	}
	registeredProcessDefinitions = kept
}

func TestProcessDefinitionIdentityCoversEveryExecutableSurface(t *testing.T) {
	base := processIdentityFixture("process_identity_digest_fixture")
	want, err := processDefinitionIdentityFor(base)
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*ProcessDefinition){
		"version":                 func(def *ProcessDefinition) { def.Version++ },
		"implementation revision": func(def *ProcessDefinition) { def.ImplementationRevision += ".next" },
		"prompt":                  func(def *ProcessDefinition) { def.Stages[0].PromptBody = "changed prompt" },
		"contract":                func(def *ProcessDefinition) { def.Stages[1].OutputContract = "source_b_v2" },
		"stage order":             func(def *ProcessDefinition) { def.Stages[0], def.Stages[1] = def.Stages[1], def.Stages[0] },
		"runIf":                   func(def *ProcessDefinition) { def.Stages[3].RunIf.Equals = "skip" },
		"checkpoint":              func(def *ProcessDefinition) { def.Stages[5].CheckpointSpec.Question = "Publish or hold?" },
		"budget":                  func(def *ProcessDefinition) { def.Budgets.MaxTokens++ },
		"gate":                    func(def *ProcessDefinition) { def.Stages[4].GateSpec.Threshold = 9 },
		"compile":                 func(def *ProcessDefinition) { def.Stages[6].Compile = processIdentityCompileV2 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := cloneProcessDefinition(base)
			mutate(&changed)
			got, identityErr := processDefinitionIdentityFor(changed)
			if identityErr != nil {
				t.Fatalf("mutated valid definition failed identity: %v", identityErr)
			}
			if got.Digest == want.Digest {
				t.Fatalf("%s mutation did not change process digest %q", name, want.Digest)
			}
		})
	}
}

func TestRegisteredProcessDefinitionsAreImmutableSnapshots(t *testing.T) {
	def := processIdentityFixture("process_identity_registry_snapshot")
	wantPrompt := def.Stages[0].PromptBody
	registerProcessDefinitionForTest(t, def)
	def.Stages[0].PromptBody = "caller mutation"
	first, ok := processByID(def.ID)
	if !ok || first.Stages[0].PromptBody != wantPrompt {
		t.Fatalf("registration retained caller-owned stage memory: %+v", first.Stages[0])
	}
	first.Stages[0].PromptBody = "lookup mutation"
	second, ok := processByID(def.ID)
	if !ok || second.Stages[0].PromptBody != wantPrompt {
		t.Fatalf("registry lookup exposed mutable stage memory: %+v", second.Stages[0])
	}
}

func TestProcessLaunchPinsAndReceiptSignsExactIdentity(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "process-identity-test-key")
	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerOpenAIText)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "process-identity-test-key"
	previousStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousStart })

	def := processIdentityFixture("process_identity_receipt_fixture")
	registerProcessDefinitionForTest(t, def)
	run, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Run the exact pinned process", CreatedBy: "aj@shareability.com", ToolTemplate: def.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := mustGoalPlan(t, app, run.Artifact.ID)
	receipt := plan.RouteReceipt
	if receipt == nil || !isHexDigest(plan.ProcessDigest) || plan.ProcessVersion != def.Version ||
		plan.ProcessImplementationRevision != def.ImplementationRevision || plan.ResultStageID != "source_b" || plan.ResultOutputContract != "source_b_v1" {
		t.Fatalf("launch did not pin complete process identity: plan=%+v receipt=%+v", plan, receipt)
	}
	if receipt.ProcessID != plan.ProcessID || receipt.ProcessVersion != plan.ProcessVersion || receipt.ProcessDigest != plan.ProcessDigest ||
		receipt.ProcessImplementationRevision != plan.ProcessImplementationRevision || receipt.ResultStageID != plan.ResultStageID || receipt.ResultOutputContract != plan.ResultOutputContract {
		t.Fatalf("route receipt did not sign exact process identity: plan=%+v receipt=%+v", plan, receipt)
	}
	if err := app.verifyGoalRouteReceipt(&plan, *receipt); err != nil {
		t.Fatalf("fresh exact receipt rejected: %v", err)
	}

	mutations := map[string]func(*goalPlan){
		"version":                 func(candidate *goalPlan) { candidate.ProcessVersion++ },
		"digest":                  func(candidate *goalPlan) { candidate.ProcessDigest = strings.Repeat("a", 64) },
		"implementation revision": func(candidate *goalPlan) { candidate.ProcessImplementationRevision += ".drift" },
		"result stage":            func(candidate *goalPlan) { candidate.ResultStageID = "source_a" },
		"result contract":         func(candidate *goalPlan) { candidate.ResultOutputContract = "source_a_v1" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := plan
			mutate(&changed)
			if err := app.verifyGoalRouteReceipt(&changed, *receipt); err == nil {
				t.Fatalf("receipt accepted %s drift", name)
			}
		})
	}
}

func TestPinnedProcessRuntimeRejectsRegistryDriftBeforeDecomposeAndMidRun(t *testing.T) {
	base := processIdentityFixture("process_identity_runtime_fixture")
	registerProcessDefinitionForTest(t, base)

	before := &goalPlan{PlanVersion: goalPlanVersion, ProcessID: base.ID, Authority: codexJobAuthorityWorkspaceWrite, State: goalStateDecompose}
	pinProcessPlanForTest(t, before, base)
	versionTwo := cloneProcessDefinition(base)
	versionTwo.Version = 2
	versionTwo.ImplementationRevision = "test." + base.ID + ".runtime.v2"
	replaceRegisteredProcessForIdentityTest(t, versionTwo)
	if err := newGoalEngine(newIsolatedKanbanBoardApp(t)).decompose(context.Background(), before); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("v1 to v2 registry drift did not block before decompose: %v", err)
	}

	// Restore v1, instantiate its exact graph, then prove every valid runtime
	// mutation is rejected rather than being consumed by an in-flight plan.
	replaceRegisteredProcessForIdentityTest(t, base)
	midRun := &goalPlan{PlanVersion: goalPlanVersion, ProcessID: base.ID, Authority: codexJobAuthorityWorkspaceWrite, State: goalStateExecute}
	pinProcessPlanForTest(t, midRun, base)
	if err := instantiateProcessPlan(base, midRun); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*ProcessDefinition){
		"prompt":      func(def *ProcessDefinition) { def.Stages[0].PromptBody = "changed prompt" },
		"contract":    func(def *ProcessDefinition) { def.Stages[1].OutputContract = "source_b_v2" },
		"stage order": func(def *ProcessDefinition) { def.Stages[0], def.Stages[1] = def.Stages[1], def.Stages[0] },
		"runIf":       func(def *ProcessDefinition) { def.Stages[3].RunIf.Equals = "skip" },
		"checkpoint":  func(def *ProcessDefinition) { def.Stages[5].CheckpointSpec.Question = "Publish or hold?" },
		"budget":      func(def *ProcessDefinition) { def.Budgets.MaxTokens++ },
		"gate":        func(def *ProcessDefinition) { def.Stages[4].GateSpec.Floor++ },
		"compile":     func(def *ProcessDefinition) { def.Stages[6].Compile = processIdentityCompileV2 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := cloneProcessDefinition(base)
			mutate(&changed)
			replaceRegisteredProcessForIdentityTest(t, changed)
			if _, err := resolvePinnedProcessDefinition(midRun); err == nil || !strings.Contains(err.Error(), "identity") {
				t.Fatalf("mid-run %s drift did not block: %v", name, err)
			}
			replaceRegisteredProcessForIdentityTest(t, base)
		})
	}

	removeRegisteredProcessForIdentityTest(t, base.ID)
	if _, err := resolvePinnedProcessDefinition(midRun); err == nil || !strings.Contains(err.Error(), "no longer available") {
		t.Fatalf("removed definition did not block: %v", err)
	}
}

func TestPersistedMidRunProcessDriftBlocksWithoutDispatchFallback(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "process-identity-midrun-key")
	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerOpenAIText)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "process-identity-midrun-key"
	previousGoalStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStart })
	previousChildStart := startAgentThreadAsync
	childStarts := 0
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) { childStarts++ }
	t.Cleanup(func() { startAgentThreadAsync = previousChildStart })

	def := processIdentityFixture("process_identity_persisted_midrun")
	registerProcessDefinitionForTest(t, def)
	run, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Run the exact process without drift", CreatedBy: "aj@shareability.com", ToolTemplate: def.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := mustGoalPlan(t, app, run.Artifact.ID)
	engine := newGoalEngine(app)
	if err := engine.prepareGoalRoute(&plan, run.Artifact.ID); err != nil {
		t.Fatal(err)
	}
	if err := instantiateProcessPlan(def, &plan); err != nil {
		t.Fatal(err)
	}
	plan.State = goalStateExecute
	if persisted := engine.persist(&plan, run.Artifact.ID, ""); persisted.ID == "" {
		t.Fatal("mid-run plan was not persisted")
	}

	changed := cloneProcessDefinition(def)
	changed.Stages[len(changed.Stages)-1].Compile = processIdentityCompileV2
	replaceRegisteredProcessForIdentityTest(t, changed)
	app.runGoalThread(run.Artifact.ID)
	blocked := mustGoalPlan(t, app, run.Artifact.ID)
	if blocked.State != goalStateBlocked || !strings.Contains(blocked.Blocker, "identity no longer matches") {
		t.Fatalf("mid-run drift did not fail closed: state=%q blocker=%q", blocked.State, blocked.Blocker)
	}
	if childStarts != 0 {
		t.Fatalf("mid-run drift fell back to a child runner: starts=%d", childStarts)
	}
	for _, subtask := range blocked.Subtasks {
		if subtask.ArtifactID != "" || subtask.ThreadID != "" || subtask.Runner == agentRunnerStub {
			t.Fatalf("mid-run drift dispatched or stubbed stage %+v", subtask)
		}
	}
}

func TestLegacyProcessIDOnlyPlanFailsClosed(t *testing.T) {
	plan := &goalPlan{ProcessID: processProbeDefinition().ID, routeVerified: true}
	if _, err := resolvePinnedProcessDefinition(plan); err == nil ||
		!strings.Contains(err.Error(), "identity no longer matches") ||
		!strings.Contains(err.Error(), "relaunch or explicitly migrate") {
		t.Fatalf("legacy process-id-only plan regained runtime authority: %v", err)
	}
}
