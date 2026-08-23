package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

const packagingStudioV5Digest = "f0ebe287c675e45b4f9cc4214f86070ec57e0954a49e2a7b7bb09200d1ded4e5"

func packagingStudioV5IdentityDirectionValueForTest(t *testing.T, includeShot bool) map[string]any {
	t.Helper()
	raw, err := json.Marshal(packagingIdentityDirectionValueForTest())
	if err != nil {
		t.Fatal(err)
	}
	var current map[string]any
	if err := json.Unmarshal(raw, &current); err != nil {
		t.Fatal(err)
	}
	delete(current, "selected_candidate_id")
	delete(current, "selection_rationale")
	shots, _ := current["shots"].([]any)
	for _, value := range shots {
		shot, _ := value.(map[string]any)
		delete(shot, "depiction_kind")
		delete(shot, "depiction_entity")
		delete(shot, "depiction_ref")
	}
	if includeShot {
		current["shots"] = shots[:1]
	} else {
		current["shots"] = []any{}
	}
	return current
}

func TestPackagingStudioV5DefinitionRemainsExactlyResolvable(t *testing.T) {
	historical := packagingStudioDefinitionV5()
	identity, err := processDefinitionIdentityFor(historical)
	if err != nil {
		t.Fatal(err)
	}
	if identity.ID != packagingStudioProcessID || identity.Version != 5 || identity.Digest != packagingStudioV5Digest ||
		identity.ImplementationRevision != "packaging_studio.runtime.v5" || identity.ResultStageID != "ship_deck" || identity.ResultOutputContract != packagingStudioDeckContract {
		t.Fatalf("frozen v5 identity drifted: %+v", identity)
	}

	plan := &goalPlan{
		ProcessID: packagingStudioProcessID, ProcessVersion: identity.Version, ProcessDigest: identity.Digest,
		ProcessImplementationRevision: identity.ImplementationRevision, ResultStageID: identity.ResultStageID,
		ResultOutputContract: identity.ResultOutputContract, routeVerified: true,
	}
	resolved, err := resolvePinnedProcessDefinition(plan)
	if err != nil {
		t.Fatalf("persisted v5 plan no longer resolves after v6 registration: %v", err)
	}
	resolvedIdentity, err := processDefinitionIdentityFor(resolved)
	if err != nil || resolvedIdentity != identity {
		t.Fatalf("persisted v5 plan resolved to the wrong executable definition: got=%+v want=%+v err=%v", resolvedIdentity, identity, err)
	}

	current, ok := processByID(packagingStudioProcessID)
	if !ok || current.Version != 8 {
		t.Fatalf("new launches must still resolve the current v8 definition: ok=%t version=%d", ok, current.Version)
	}
}

func TestPackagingStudioV5UnknownDigestStillFailsClosed(t *testing.T) {
	plan := &goalPlan{
		ProcessID: packagingStudioProcessID, ProcessVersion: 5, ProcessDigest: sha256Hex([]byte("not the frozen v5 definition")),
		ProcessImplementationRevision: "packaging_studio.runtime.v5", ResultStageID: "ship_deck",
		ResultOutputContract: packagingStudioDeckContract, routeVerified: true,
	}
	if _, err := resolvePinnedProcessDefinition(plan); err == nil {
		t.Fatal("unknown v5 process bytes were accepted as the frozen historical definition")
	}
}

func TestPersistedPackagingStudioV5RouteRemainsInspectableAfterV7Registration(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "packaging-v5-route-test"
	t.Setenv("OPENAI_API_KEY", "packaging-v5-route-test")
	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerOpenAIText)
	previousStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousStart })

	run, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Build the exact saved presentation", CreatedBy: "aj@shareability.com", ToolTemplate: packagingStudioProcessID,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := mustGoalPlan(t, app, run.Artifact.ID)
	historical := packagingStudioDefinitionV5()
	identity, err := processDefinitionIdentityFor(historical)
	if err != nil {
		t.Fatal(err)
	}
	plan.ProcessVersion = identity.Version
	plan.ProcessDigest = identity.Digest
	plan.ProcessImplementationRevision = identity.ImplementationRevision
	plan.ResultStageID = identity.ResultStageID
	plan.ResultOutputContract = identity.ResultOutputContract
	if err := instantiateProcessPlan(historical, &plan); err != nil {
		t.Fatal(err)
	}
	receipt := *plan.RouteReceipt
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
	rawPlan, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.memory.updateOSArtifactMetadata(run.Artifact.ID, map[string]string{
		"goalPlan": string(rawPlan), "goalRouteDigest": receipt.Digest,
		"processVersion": strconv.Itoa(identity.Version), "processDigest": identity.Digest,
		"processImplementationRevision": identity.ImplementationRevision,
		"resultStageId":                 identity.ResultStageID, "resultOutputContract": identity.ResultOutputContract,
	}); err != nil {
		t.Fatal(err)
	}

	persisted := mustGoalPlan(t, app, run.Artifact.ID)
	if err := newGoalEngine(app).prepareGoalRoute(&persisted, run.Artifact.ID); err != nil {
		t.Fatalf("persisted exact v5 route failed after v6 became current: %v", err)
	}
	resolved, err := resolvePinnedProcessDefinition(&persisted)
	if err != nil || resolved.Version != 5 || len(resolved.Stages) != 16 {
		t.Fatalf("persisted v5 route resolved incorrectly: version=%d stages=%d err=%v", resolved.Version, len(resolved.Stages), err)
	}
}

func TestPackagingStudioV5NamedImageryResumeIsQuarantinedBeforeProviderSpend(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	write, _, err := app.createOSArtifactWithMetadata("workflow", "Historical copy", `{"slides":[{"slide_id":"cover"},{"slide_id":"proof"}]}`, "Scout", nil)
	if err != nil {
		t.Fatal(err)
	}
	identityBody := fencedIdentityDirectionForTest(t, packagingStudioV5IdentityDirectionValueForTest(t, true))
	identity, _, err := app.createOSArtifactWithMetadata("workflow", "Historical identity", identityBody, "Scout", nil)
	if err != nil {
		t.Fatal(err)
	}
	plan := &goalPlan{
		ProcessID: packagingStudioProcessID, ProcessVersion: 5, ProcessDigest: packagingStudioV5Digest,
		ProcessImplementationRevision: "packaging_studio.runtime.v5", ResultStageID: "ship_deck", ResultOutputContract: packagingStudioDeckContract,
		Subtasks: []goalSubtask{{ID: "write", Status: subtaskComplete, ArtifactID: write.ID}, {ID: "identity", Status: subtaskComplete, ArtifactID: identity.ID}},
	}
	if _, _, err := compilePackagingStudioImagery(app, plan, "historical-v5-goal", ProcessStage{ID: "imagery_generate"}); err == nil || !strings.Contains(err.Error(), "Relaunch it with the current process") {
		t.Fatalf("historical named imagery reached the provider path instead of requiring a current relaunch: %v", err)
	}

	typographicBody := fencedIdentityDirectionForTest(t, packagingStudioV5IdentityDirectionValueForTest(t, false))
	typographic, _, err := app.createOSArtifactWithMetadata("workflow", "Historical typographic identity", typographicBody, "Scout", nil)
	if err != nil {
		t.Fatal(err)
	}
	plan.subtaskByID("identity").ArtifactID = typographic.ID
	body, metadata, err := compilePackagingStudioImagery(app, plan, "historical-v5-goal", ProcessStage{ID: "imagery_generate"})
	if err == nil || !strings.Contains(err.Error(), "Relaunch it with the current process") || body != "" || metadata != nil {
		t.Fatalf("exact frozen-v5 zero-shot direction resumed instead of requiring a current relaunch: body=%q metadata=%v err=%v", body, metadata, err)
	}
}
