package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// packaging_studio_test.go — the flagship ProcessDefinition (Wave 4 item 18).
// Two layers of proof. The pipeline as DATA (like wave11_palette_test.go): the
// whole definition validates against the runtime, the stage wiring is correct
// (identity's two branches, the checkpoint choice flow, the FOUR human
// touchpoints, the conditional house judge seat), and the founder's verbatim
// words reach the gate prompt. And the pipeline IN FLIGHT: the ship tests
// drive a real packaging_studio goal through the engine's fake-responder
// harness (the goal_engine_test.go pattern) from launch through every
// checkpoint resume to verified — the five interlocking artifacts are filed
// by the EXECUTING run's ship_compile stage, never by calling the compiler
// directly, so a green suite proves the actual pipeline.

func packagingStudioStage(t *testing.T, def ProcessDefinition, id string) ProcessStage {
	t.Helper()
	stage, ok := def.stageByID(id)
	if !ok {
		t.Fatalf("packaging_studio has no stage %q", id)
	}
	return stage
}

func launchStudioShipChildAndCompileForScopeTest(t *testing.T, app *kanbanBoardApp, work scoutAgentThread) (meetingMemoryEntry, meetingMemoryEntry) {
	t.Helper()
	plan := mustGoalPlan(t, app, work.Artifact.ID)
	engine := newGoalEngine(app)
	if err := engine.prepareGoalRoute(&plan, work.Artifact.ID); err != nil {
		t.Fatalf("prepare packaging route: %v", err)
	}
	definition, ok := engine.resolvedProcess(&plan)
	if !ok {
		t.Fatal("packaging process did not resolve")
	}
	stage := packagingStudioStage(t, definition, "ship_deck")
	contextArtifact, _, err := app.createOSArtifactWithMetadata("workflow", "Context snapshot", `{"direct_ask":"Build the requested deck","audience":"decision makers","decision":"review the requested deck","desired_response":"make a grounded decision","slide_count":2,"context_used":[],"settled_decisions":[],"taste_signals":[],"brand_assets":[],"research_mode":"none","research_questions":[],"known_facts":[],"uncertain_claims":[],"reversible_inferences":[]}`, scoutParticipantName, map[string]string{
		"goalParentId": work.Artifact.ID, "goalSubtaskId": "context_snapshot", "processId": plan.ProcessID,
		"processStage": "context_snapshot", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatalf("seed context snapshot: %v", err)
	}
	plan.Subtasks = []goalSubtask{
		{ID: "context_snapshot", Status: subtaskComplete, ArtifactID: contextArtifact.ID},
		{ID: "evidence", Status: subtaskPending},
		{
			ID: stage.ID, Title: stage.Title, Detail: stage.PromptBody, Mode: processStageThreadMode(stage),
			Authority: normalizeCodexJobAuthority(plan.Authority), Runner: agentRunnerOpenAIText, Role: stage.Role,
			Status: subtaskRunning, Attempts: 1,
		},
	}
	evidenceBody, evidenceMetadata, err := compileProcessEvidenceDossier(app, &plan, work.Artifact.ID, ProcessStage{ID: "evidence"})
	if err != nil {
		t.Fatalf("compile empty evidence dossier: %v", err)
	}
	for key, value := range map[string]string{
		"goalParentId": work.Artifact.ID, "goalSubtaskId": "evidence", "processId": plan.ProcessID,
		"processStage": "evidence", "status": "complete", "threadStatus": "complete",
	} {
		evidenceMetadata[key] = value
	}
	evidenceArtifact, _, err := app.createOSArtifactWithMetadata("workflow", "Evidence admission dossier", evidenceBody, scoutParticipantName, evidenceMetadata)
	if err != nil {
		t.Fatalf("seed evidence dossier: %v", err)
	}
	if err := validateProcessEvidenceDossier(&plan, evidenceArtifact); err != nil {
		matches := processEvidenceDossierReceiptPattern.FindAllStringSubmatch(evidenceArtifact.Text, -1)
		index := processEvidenceDossierReceiptPattern.FindStringIndex(evidenceArtifact.Text)
		actualDigest := ""
		if len(index) == 2 {
			actualDigest = sha256Hex([]byte(strings.TrimSpace(evidenceArtifact.Text[:index[0]])))
		}
		t.Fatalf("seeded evidence dossier is invalid before launch: %v receipt=%q metadata=%q actual=%q expected_process=%q", err, matches, evidenceArtifact.Metadata["evidenceAdmissionDigest"], actualDigest, plan.ProcessID)
	}
	plan.Subtasks[1].Status, plan.Subtasks[1].ArtifactID = subtaskComplete, evidenceArtifact.ID
	if err := engine.launchSubtask(&plan, &plan.Subtasks[2], work.Artifact.ID); err != nil {
		t.Fatalf("launch packaging child: %v", err)
	}
	child, ok := app.osArtifactByID(plan.Subtasks[2].ArtifactID)
	if !ok {
		t.Fatal("packaging child was not persisted")
	}
	deckHTML := studioTestDeckHTML()
	child, _, err = app.updateOSArtifactWithMetadata(child.ID, "", deckHTML, scoutParticipantName, map[string]string{
		"status": codexJobStatusComplete, "threadStatus": codexJobStatusComplete,
	})
	if err != nil {
		t.Fatalf("complete packaging child: %v", err)
	}
	_, extra, err := compilePackagingStudioShip(app, &plan, work.Artifact.ID, ProcessStage{})
	if err != nil {
		t.Fatalf("compile packaging deck: %v", err)
	}
	deck, ok := app.osArtifactByID(extra["deckArtifactId"])
	if !ok {
		t.Fatal("compiled packaging deck was not persisted")
	}
	return child, deck
}

func TestPackagingStudioPrivateChildAndDeckRemainOwnerPrivate(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-private-studio-scope-test"
	previousGoalStart := startGoalThreadAsync
	previousAgentStart := startAgentThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() {
		startGoalThreadAsync = previousGoalStart
		startAgentThreadAsync = previousAgentStart
	})

	work, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Build a private leadership deck", CreatedBy: "aj@shareability.com", ToolTemplate: packagingStudioProcessID,
	})
	if err != nil {
		t.Fatal(err)
	}
	child, deck := launchStudioShipChildAndCompileForScopeTest(t, app, work)
	wantSurface := work.Artifact.Metadata["originSurface"]
	for label, artifact := range map[string]meetingMemoryEntry{"child": child, "deck": deck} {
		if artifact.Metadata["originSurface"] != wantSurface || artifact.Metadata["visibility"] != scoutChatVisibilityPrivate || normalizeAccountEmail(artifact.Metadata["ownerEmail"]) != "aj@shareability.com" {
			t.Fatalf("%s scope=%q/%q/%q, want %q/private/aj@shareability.com", label, artifact.Metadata["originSurface"], artifact.Metadata["visibility"], artifact.Metadata["ownerEmail"], wantSurface)
		}
	}
}

func TestPackagingStudioPublicChildAndDeckRemainOnPublicChannel(t *testing.T) {
	app, user, thread, source, binding := newAcceptedPublicWorkFixture(t)
	previousGoalStart := startGoalThreadAsync
	previousAgentStart := startAgentThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() {
		startGoalThreadAsync = previousGoalStart
		startAgentThreadAsync = previousAgentStart
	})

	proposal := scoutRouterProposalForToolID(packagingStudioProcessID, "Build the public launch deck", source.Text)
	if proposal == nil {
		t.Fatal("packaging studio proposal unavailable")
	}
	proposal.IntentOutcome = string(conversationIntentApprovalRequired)
	proposal.EffectClass = "expanded_audience"
	proposal.Status = "accepted"
	const proposalID = "proposal-public-scope-deck"
	var err error
	thread, err = app.commitScoutChatThreadMessages(user.Email, thread.ID, scoutChatMessageRecord{
		ID: proposalID, Kind: scoutChatMessageKindProposal, Role: "scout", Proposal: proposal,
		CausedByMessageID: source.ID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := app.startAcceptedPublicScoutWork(context.Background(), user, thread, proposalID, *proposal, nil, binding)
	if err != nil {
		t.Fatal(err)
	}
	work := response["agentThread"].(scoutAgentThread)
	child, deck := launchStudioShipChildAndCompileForScopeTest(t, app, work)
	wantSurface := "chat:" + thread.ID
	for label, artifact := range map[string]meetingMemoryEntry{"child": child, "deck": deck} {
		if artifact.Metadata["originSurface"] != wantSurface || artifact.Metadata["visibility"] != work.Artifact.Metadata["visibility"] || artifact.Metadata["ownerEmail"] != work.Artifact.Metadata["ownerEmail"] {
			t.Fatalf("%s scope=%q/%q/%q, want root scope %q/%q/%q", label, artifact.Metadata["originSurface"], artifact.Metadata["visibility"], artifact.Metadata["ownerEmail"], wantSurface, work.Artifact.Metadata["visibility"], work.Artifact.Metadata["ownerEmail"])
		}
	}
}

func TestPackagingStudioDeliveredDeckIsImmediatelyNativePreviewableAndPPTXExportable(t *testing.T) {
	setupAuthTestEnv(t)
	setupIsolatedBlobStore(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-native-studio-delivery-test"
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	previousGoalStart := startGoalThreadAsync
	previousAgentStart := startAgentThreadAsync
	kanbanApp = app
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() {
		kanbanApp = previousApp
		artifactObjectAuthorizer = previousAuthorizer
		startGoalThreadAsync = previousGoalStart
		startAgentThreadAsync = previousAgentStart
	})

	work, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Build the delivered leadership deck", CreatedBy: "aj@shareability.com", ToolTemplate: packagingStudioProcessID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, deckArtifact := launchStudioShipChildAndCompileForScopeTest(t, app, work)
	sceneRef := strings.TrimSpace(deckArtifact.Metadata[deckSceneRefMetadataKey])
	if !validBlobRef(sceneRef) || deckArtifact.Metadata[deckSchemaMetadataKey] != strconv.Itoa(deckDocumentSchemaVersion) {
		t.Fatalf("delivered deck has no native scene binding: %+v", deckArtifact.Metadata)
	}
	nativeDeck, imported, quality, err := loadDeckDocument(deckArtifact)
	if err != nil || imported || quality != "native" || len(nativeDeck.Slides) != 2 {
		t.Fatalf("delivered scene imported=%v quality=%q slides=%d err=%v", imported, quality, len(nativeDeck.Slides), err)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	preview := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/deck?id="+deckArtifact.ID, "", cookies, deckEditorHandler)
	var previewPayload struct {
		Artifact      deckArtifactView `json:"artifact"`
		Deck          deckDocument     `json:"deck"`
		Imported      bool             `json:"imported"`
		ImportQuality string           `json:"importQuality"`
		CanWrite      bool             `json:"canWrite"`
	}
	if preview.Code != http.StatusOK || json.Unmarshal(preview.Body.Bytes(), &previewPayload) != nil || previewPayload.Imported || previewPayload.ImportQuality != "native" || !previewPayload.CanWrite || previewPayload.Artifact.SceneRef != sceneRef || len(previewPayload.Deck.Slides) != 2 {
		t.Fatalf("fresh deck endpoint status=%d payload=%s", preview.Code, preview.Body.String())
	}

	pptx := deckPPTXRequest(t, deckArtifact, cookies, nil)
	if pptx.Code != http.StatusOK || pptx.Header().Get("Content-Type") != deckPPTXContentType {
		t.Fatalf("fresh PPTX status=%d content-type=%q body=%s", pptx.Code, pptx.Header().Get("Content-Type"), pptx.Body.String())
	}
	if _, ok := deckPPTXZipParts(t, pptx.Body.Bytes())["ppt/slides/slide2.xml"]; !ok {
		t.Fatal("fresh PPTX export lost the second generated slide")
	}
	after, ok := app.osArtifactByID(deckArtifact.ID)
	if !ok || artifactVersion(after) != artifactVersion(deckArtifact) || after.Metadata[deckSceneRefMetadataKey] != sceneRef {
		t.Fatalf("read-only preview/export mutated the delivered deck: before=%+v after=%+v", deckArtifact.Metadata, after.Metadata)
	}
}

// --- The whole definition validates + serves --------------------------------

func TestPackagingStudioDefinitionValidates(t *testing.T) {
	def := packagingStudioDefinition()
	if err := validateProcessDefinition(def); err != nil {
		t.Fatalf("packaging_studio does not validate against the runtime: %v", err)
	}

	// It is a real, launchable, NON-hidden process resolved by id (the palette,
	// /goal, voice, and the router all reach it this way).
	resolved, ok := processByID(packagingStudioProcessID)
	if !ok {
		t.Fatal("packaging_studio missing from the process registry")
	}
	if resolved.Hidden {
		t.Fatal("packaging_studio must be public — it is the flagship, not a proof")
	}
	if resolved.Group != toolGroupProcesses {
		t.Fatalf("packaging_studio group=%q, want %q", resolved.Group, toolGroupProcesses)
	}

	// The process deliverable contract is the shipped deck (the last writer
	// stage's contract), so the running card and recall index it as a deck.
	if got := processDeliverableContract(def); got != packagingStudioDeckContract {
		t.Fatalf("deliverable contract=%q, want the shipped deck %q", got, packagingStudioDeckContract)
	}

	// It instantiates into a plan the runtime accepts — the free-form cap (6)
	// never applies; only the authored budget admits the full pipeline.
	plan := &goalPlan{PlanVersion: goalPlanVersion, ProcessID: def.ID, Authority: codexJobAuthorityWorkspaceWrite, State: goalStateDecompose, routeVerified: true}
	pinProcessPlanForTest(t, plan, def)
	if err := instantiateProcessPlan(def, plan); err != nil {
		t.Fatalf("instantiateProcessPlan(packaging_studio): %v", err)
	}
	if len(plan.Subtasks) != len(def.Stages) {
		t.Fatalf("plan has %d subtasks, want one per stage (%d)", len(plan.Subtasks), len(def.Stages))
	}
	if err := validateGoalPlanWithLimit(plan, processMaxSubtasks(def)); err != nil {
		t.Fatalf("instantiated plan does not validate under its budget: %v", err)
	}
}

// --- Stage wiring: the nine phases, in order, on the right roles -------------

func TestLegacyPackagingStudioV2StageWiring(t *testing.T) {
	def := legacyPackagingStudioDefinition()

	// The pipeline's spine: the ordered phases mapped to runtime roles. INTAKE
	// is the FIRST stage and a human checkpoint; SHIP ends writer → compile →
	// ship-approval checkpoint.
	wantRoles := []struct{ id, role string }{
		{"intake", processRoleHumanCheckpoint},
		{"red_team", processRolePanel},
		{"identity", processRoleJudges},
		{"compete_architects", processRolePanel},
		{"compete_judges", processRoleJudges},
		{"compete_choice", processRoleHumanCheckpoint},
		{"write", processRoleSynthesizer},
		{"gate", processRoleGate},
		{"voice", processRoleWriter},
		{"founder_pass", processRoleHumanCheckpoint},
		{"ship_deck", processRoleWriter},
		{"ship_compile", processRoleCompile},
		{"slide_jury", processRoleCompile},
		{"ship_approval", processRoleHumanCheckpoint},
	}
	if def.Stages[0].ID != "intake" || def.Stages[0].Role != processRoleHumanCheckpoint {
		t.Fatalf("stage 0 = %s/%s, want the INTAKE human checkpoint FIRST", def.Stages[0].ID, def.Stages[0].Role)
	}
	for _, want := range wantRoles {
		stage := packagingStudioStage(t, def, want.id)
		if stage.Role != want.role {
			t.Errorf("stage %q role=%q, want %q", want.id, stage.Role, want.role)
		}
	}

	// The closed-loop GATE holds the SKILL thresholds and re-reads the round-1
	// objection ledger (InputFrom red_team), and its first input is WRITE so a
	// revise re-queues the copy, not the ledger.
	gate := packagingStudioStage(t, def, "gate")
	if gate.GateSpec == nil {
		t.Fatal("gate stage has no GateSpec")
	}
	if gate.GateSpec.Threshold != 9.0 || gate.GateSpec.Floor != 7.0 || gate.GateSpec.MaxRounds != 2 || !gate.GateSpec.ForceAccept {
		t.Fatalf("gate spec=%+v, want 9.0/7.0/2/force-accept (the SKILL semantics)", *gate.GateSpec)
	}
	if len(gate.InputFrom) == 0 || gate.InputFrom[0] != "write" {
		t.Fatalf("gate inputFrom=%v, want write first (revise re-queues the copy)", gate.InputFrom)
	}
	if !containsString(gate.InputFrom, "red_team") {
		t.Fatalf("gate inputFrom=%v, want the red_team objection ledger in hand", gate.InputFrom)
	}

	// The compile stage assembles the five-artifact package from the run's own
	// stage outputs — it is wired INTO the executing pipeline, carrying its
	// authored Go compiler, and it reads the shipped deck plus every source of
	// The Wall / The Talk / the rigor companion.
	compile := packagingStudioStage(t, def, "ship_compile")
	if compile.Compile == nil {
		t.Fatal("ship_compile carries no Compile function — the five-artifact compiler would be orphaned again")
	}
	for _, need := range []string{"ship_deck", "write", "voice", "red_team", "gate", "founder_pass"} {
		if !containsString(compile.InputFrom, need) {
			t.Errorf("ship_compile inputFrom=%v, missing %q", compile.InputFrom, need)
		}
	}

	// Exactly FOUR human touchpoints, in order: intake, the compete choice,
	// the founder pass, and the explicit ship approval (spec §3 "Where humans
	// sit" — founder_pass never doubles as ship approval).
	var checkpoints []string
	for _, stage := range def.Stages {
		if stage.Role == processRoleHumanCheckpoint {
			checkpoints = append(checkpoints, stage.ID)
		}
	}
	wantCheckpoints := []string{"intake", "compete_choice", "founder_pass", "ship_approval"}
	if len(checkpoints) != len(wantCheckpoints) {
		t.Fatalf("pipeline has %d human touchpoints %v, want exactly the four: %v", len(checkpoints), checkpoints, wantCheckpoints)
	}
	for index, want := range wantCheckpoints {
		if checkpoints[index] != want {
			t.Fatalf("human touchpoints=%v, want %v in order", checkpoints, wantCheckpoints)
		}
	}
	// The slide jury sits between the compile and the approval: authored Go
	// (never a model call), reading the compile record's shipArtifactIds.
	jury := packagingStudioStage(t, def, "slide_jury")
	if jury.Compile == nil {
		t.Fatal("slide_jury carries no Compile function — the vision jury would be orphaned")
	}
	if len(jury.InputFrom) != 1 || jury.InputFrom[0] != "ship_compile" {
		t.Fatalf("slide_jury inputFrom=%v, want [ship_compile]", jury.InputFrom)
	}

	approval := packagingStudioStage(t, def, "ship_approval")
	if len(approval.InputFrom) != 3 || approval.InputFrom[0] != "ship_compile" || approval.InputFrom[1] != "slide_jury" || approval.InputFrom[2] != "ship_deck" {
		t.Fatalf("ship_approval inputFrom=%v, want [ship_compile slide_jury ship_deck] — the approval reads the compile record, the jury verdict/skip, and can send the deck itself back", approval.InputFrom)
	}
	if approval.CheckpointSpec == nil || !strings.Contains(strings.ToLower(approval.CheckpointSpec.Question), "approve") {
		t.Fatalf("ship_approval question must ask for the explicit ship approval: %+v", approval.CheckpointSpec)
	}

	// WRITE consumes the whole upstream: the objection ledger, the identity, the
	// rival spines, the judges' steals, AND the human's chosen angle.
	write := packagingStudioStage(t, def, "write")
	if write.OutputContract != "deck_copy_v2" {
		t.Fatalf("write contract=%q, want the exact deck_copy_v2 schema named in its instructions", write.OutputContract)
	}
	for _, need := range []string{"red_team", "identity", "compete_architects", "compete_judges", "compete_choice"} {
		if !containsString(write.InputFrom, need) {
			t.Errorf("write inputFrom=%v, missing %q — the grafted spine loses its source", write.InputFrom, need)
		}
	}

	// SHIP's deck carries the deck contract and reads VOICE (presenter mode) and
	// the founder pass (do_not_touch).
	shipDeck := packagingStudioStage(t, def, "ship_deck")
	if shipDeck.OutputContract != packagingStudioDeckContract {
		t.Errorf("ship_deck contract=%q, want %q", shipDeck.OutputContract, packagingStudioDeckContract)
	}
	for _, need := range []string{"voice", "founder_pass"} {
		if !containsString(shipDeck.InputFrom, need) {
			t.Errorf("ship_deck inputFrom=%v, missing %q", shipDeck.InputFrom, need)
		}
	}
}

func TestPackagingStudioV4IsInvisibleConditionalAndFailClosed(t *testing.T) {
	def := packagingStudioDefinition()
	if def.Version != 4 || def.ImplementationRevision != "packaging_studio.runtime.v4" || def.Budgets.MaxSubtasks != 18 {
		t.Fatalf("version/implementation/budget=%d/%q/%+v, want v4 runtime v4 and 18 stages", def.Version, def.ImplementationRevision, def.Budgets)
	}
	if def.Stages[0].ID != "context_snapshot" || def.Stages[len(def.Stages)-1].ID != "ship_compile" {
		t.Fatalf("unexpected v4 boundaries: first=%s last=%s", def.Stages[0].ID, def.Stages[len(def.Stages)-1].ID)
	}
	for _, stage := range def.Stages {
		if stage.Role == processRoleHumanCheckpoint {
			t.Fatalf("routine human checkpoint %q remains after the proposal boundary", stage.ID)
		}
		if stage.ID != "ship_compile" && !stage.Internal {
			t.Errorf("internal stage %q would clutter the channel", stage.ID)
		}
	}
	research := packagingStudioStage(t, def, "external_research")
	if research.RunIf == nil || research.RunIf.StageID != "context_snapshot" || research.RunIf.Field != "research_mode" || research.RunIf.Equals != "external" {
		t.Fatalf("external research is not conditional on the brief: %+v", research.RunIf)
	}
	sourceSnapshot := packagingStudioStage(t, def, "source_snapshot")
	if sourceSnapshot.Role != processRoleCompile || sourceSnapshot.Compile == nil || strings.Join(sourceSnapshot.InputFrom, "|") != "context_snapshot|external_research" ||
		sourceSnapshot.RunIf == nil || sourceSnapshot.RunIf.StageID != "context_snapshot" || sourceSnapshot.RunIf.Equals != "external" {
		t.Fatalf("source snapshot is not exact and conditional: %+v", sourceSnapshot)
	}
	entailment := packagingStudioStage(t, def, "evidence_entailment")
	if entailment.Role != processRoleWriter || entailment.Mode != "artifacts" || entailment.OutputContract != packagingStudioEntailmentContract ||
		strings.Join(entailment.InputFrom, "|") != "context_snapshot|external_research|source_snapshot" || entailment.RunIf == nil || entailment.RunIf.StageID != "context_snapshot" || entailment.RunIf.Equals != "external" ||
		!strings.Contains(entailment.PromptBody, "Do not start a second search") || strings.Contains(entailment.PromptBody, "fresh provider retrieval") {
		t.Fatalf("entailment stage is not exact and conditional: %+v", entailment)
	}
	evidence := packagingStudioStage(t, def, "evidence")
	if evidence.Role != processRoleCompile || evidence.Compile == nil || strings.Join(evidence.InputFrom, "|") != "context_snapshot|evidence_entailment" ||
		!strings.Contains(evidence.PromptBody, "entailment_checked") || !strings.Contains(evidence.PromptBody, "exactly one status") {
		t.Fatalf("evidence stage can bypass entailment: %+v", evidence)
	}
	index := map[string]int{}
	for i, stage := range def.Stages {
		index[stage.ID] = i
	}
	for before, after := range map[string]string{
		"external_research": "source_snapshot", "source_snapshot": "evidence_entailment", "evidence_entailment": "evidence",
		"story_architects": "write", "write": "identity", "identity": "imagery_direction",
		"ship_deck": "draft_compile", "draft_compile": "slide_jury", "slide_jury": "quality_gate", "quality_gate": "ship_compile",
	} {
		if index[before] >= index[after] {
			t.Errorf("stage order broken: %s must precede %s", before, after)
		}
	}
	for _, id := range []string{"gate", "quality_gate"} {
		stage := packagingStudioStage(t, def, id)
		if stage.GateSpec == nil || stage.GateSpec.ForceAccept || !stage.GateSpec.HoldOnFailure || stage.GateSpec.RepairTarget == "" {
			t.Errorf("%s must repair then hold, never force-accept: %+v", id, stage.GateSpec)
		}
	}
	identity := packagingStudioStage(t, def, "identity")
	if !containsString(identity.InputFrom, "gate") {
		t.Fatalf("identity inputFrom=%v, must wait for the story/copy gate before designing against locked copy", identity.InputFrom)
	}
	compile := packagingStudioStage(t, def, "ship_compile")
	if len(compile.InputFrom) != 2 || !containsString(compile.InputFrom, "quality_gate") || compile.Internal {
		t.Fatalf("final delivery is not gated and visible: %+v", compile)
	}
}

func TestPackagingStudioProposalCopyDescribesOutcomeNotOrchestration(t *testing.T) {
	tool, ok := routerToolByID(packagingStudioProcessID)
	if !ok {
		t.Fatal("Packaging Studio missing from router")
	}
	summary := scoutRouterToolRunSummary(tool, "Build a five-slide Like A Farmer deck")
	lower := strings.ToLower(summary + " " + scoutProposalWeightGoalLoop)
	for _, forbidden := range []string{"goal loop", "multi-agent", "staged process", "human checkpoint", "spends tokens", "5-15 min"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("proposal copy leaks internal orchestration %q: %s", forbidden, summary)
		}
	}
	for _, want := range []string{"editable presentation", "edit or present", "runs in the background"} {
		if !strings.Contains(lower, want) {
			t.Errorf("proposal copy missing user outcome %q: %s (%s)", want, summary, scoutProposalWeightGoalLoop)
		}
	}
	proposal := scoutRouterProposalForToolID(packagingStudioProcessID, "Build a five-slide Like A Farmer deck", "")
	if proposal == nil || proposal.GroupLabel != "Presentation" {
		t.Fatalf("deck proposal group label=%+v, want customer-facing Presentation", proposal)
	}
}

func TestPackagingRequestedSlideCount(t *testing.T) {
	for _, tc := range []struct {
		objective string
		want      int
		ok        bool
	}{
		{"build the actual 5-slide deck", 5, true},
		{"make a five slide presentation", 5, true},
		{"create 12 slides for the board", 12, true},
		{"make a deck for the 90-day plan", 0, false},
		{"make 99 slides", 0, false},
	} {
		got, ok := packagingRequestedSlideCount(tc.objective)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%q: got (%d,%v), want (%d,%v)", tc.objective, got, ok, tc.want, tc.ok)
		}
	}
	app := newIsolatedKanbanBoardApp(t)
	snapshot, _, err := app.createOSArtifactWithMetadata("workflow", "Brief", `{"slide_count":7}`, scoutParticipantName, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	plan := &goalPlan{Objective: "Build the presentation", Subtasks: []goalSubtask{{ID: "context_snapshot", ArtifactID: snapshot.ID, Status: subtaskComplete}}}
	if got, ok := packagingPlanSlideCount(app, plan); !ok || got != 7 {
		t.Fatalf("context snapshot slide count=(%d,%v), want (7,true)", got, ok)
	}
}

func TestPackagingStudioConditionalResearchSkipsCleanly(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	parent, _, err := app.createOSArtifactWithMetadata("workflow", "Conditional research goal", "Build the deck", scoutParticipantName, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	contextArtifact, _, err := app.createOSArtifactWithMetadata("workflow", "Brief", `{"research_mode":"internal"}`, scoutParticipantName, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	def := packagingStudioDefinition()
	plan := &goalPlan{PlanVersion: goalPlanVersion, GoalID: parent.ID, Objective: "Build the deck", ProcessID: def.ID, Authority: codexJobAuthorityWorkspaceWrite, routeVerified: true}
	pinProcessPlanForTest(t, plan, def)
	if err := instantiateProcessPlan(def, plan); err != nil {
		t.Fatal(err)
	}
	contextStage := plan.subtaskByID("context_snapshot")
	contextStage.Status, contextStage.ArtifactID = subtaskComplete, contextArtifact.ID
	engine := newGoalEngine(app)
	engine.skipInactiveProcessStages(plan, parent.ID)
	research := plan.subtaskByID("external_research")
	if research.Status != subtaskComplete {
		t.Fatalf("conditional research status=%q, want complete skip", research.Status)
	}
	record, ok := app.osArtifactByID(research.ArtifactID)
	if !ok || record.Metadata["conditionSkipped"] != "true" {
		t.Fatalf("conditional skip was not durable: %+v", record.Metadata)
	}
}

func TestPackagingStudioPrivateGroundingUsesAuthenticatedRequester(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	seedTasteProfileArtifact(t, app, "AJ", "Requester-specific taste: lead with the earned insight.")
	engine := newGoalEngine(app)
	plan := &goalPlan{ProcessID: packagingStudioProcessID, CreatedBy: "AJ", RequestedBy: "aj@shareability.com"}
	context := engine.processStageCompanyContext(plan)
	if !strings.Contains(context, "Requester-specific taste") {
		t.Fatalf("private grounding used display-name creator instead of authenticated requester:\n%s", context)
	}
}

func TestPackagingStudioV3CompileFilesOnlyTheDeck(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	deck, _, err := app.createOSArtifactWithMetadata("workflow", "Authored deck", studioTestDeckHTML(), scoutParticipantName, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	plan := &goalPlan{
		ProcessID: packagingStudioProcessID, Objective: "Build the actual 2-slide deck", CreatedBy: "AJ",
		Subtasks: []goalSubtask{{ID: "ship_deck", Status: subtaskComplete, ArtifactID: deck.ID}},
	}
	body, metadata, err := compilePackagingStudioShip(app, plan, "goal-deck-only", ProcessStage{})
	if err != nil {
		t.Fatalf("deck-only compile: %v", err)
	}
	ids := strings.Split(metadata["shipArtifactIds"], ",")
	if len(ids) != 1 || strings.TrimSpace(ids[0]) == "" {
		t.Fatalf("shipArtifactIds=%q, want exactly one deck", metadata["shipArtifactIds"])
	}
	filed, ok := app.osArtifactByID(strings.TrimSpace(ids[0]))
	if !ok || filed.Metadata["artifactContract"] != packagingStudioDeckContract {
		t.Fatalf("filed deliverable is not the deck: %+v", filed.Metadata)
	}
	if strings.Contains(strings.ToLower(body), "five interlocking") {
		t.Fatalf("v3 compile leaked the retired five-artifact package:\n%s", body)
	}
}

func TestPackagingStudioQualityGateRepairsDeckAndRerunsRenderedReview(t *testing.T) {
	const exactFix = "Shorten slide 2 and restore the 96px safe margins."
	fixture := newPackagingQualityGateFixture(t, "needs_changes", []slideJuryRepair{{Page: 2, Fixes: []string{exactFix}}})
	var calls atomic.Int32
	fixture.runQualityGate(t, `{"dimensions":[{"name":"Text fit","score":10,"gap":""}]}`, &calls)
	quality := fixture.plan.subtaskByID("quality_gate")
	if calls.Load() != 0 || quality.Status != subtaskPending || quality.Revisions != 1 {
		t.Fatalf("deterministic jury repair did not re-arm without a scorer: calls=%d gate=%+v", calls.Load(), quality)
	}
	if ship := fixture.plan.subtaskByID("ship_deck"); ship.Status != subtaskReady || ship.Review == nil || !strings.Contains(ship.Review.Reasons, exactFix) {
		t.Fatalf("exact repair notes did not reach ship_deck: %+v", ship)
	}
	for _, id := range []string{"draft_compile", "slide_jury"} {
		if got := fixture.plan.subtaskByID(id); got.Status != subtaskPending {
			t.Fatalf("%s status=%q, want pending so the repaired deck is re-rendered and re-reviewed", id, got.Status)
		}
	}

	hold := newPackagingQualityGateFixture(t, "needs_changes", []slideJuryRepair{{Page: 2, Fixes: []string{exactFix}}})
	hold.plan.subtaskByID("quality_gate").Revisions = 2
	var holdCalls atomic.Int32
	hold.runQualityGate(t, `{"dimensions":[{"name":"Text fit","score":10,"gap":""}]}`, &holdCalls)
	holdGate := hold.plan.subtaskByID("quality_gate")
	draft := hold.plan.subtaskByID("draft_compile")
	if holdCalls.Load() != 0 || hold.plan.State != goalStateBlocked || hold.plan.Checkpoint != nil || holdGate.Status != subtaskPending || draft == nil || draft.Status != subtaskBlocked || !strings.Contains(hold.plan.Blocker, "fresh rendered review before delivery") {
		t.Fatalf("spent quality gate did not fail closed on a recoverable fresh-render seam: calls=%d state=%q checkpoint=%+v draft=%+v gate=%+v blocker=%q", holdCalls.Load(), hold.plan.State, hold.plan.Checkpoint, draft, holdGate, hold.plan.Blocker)
	}
}

// ship_deck must hand the writer the REQUIRED print chassis so the exported PDF
// contains every slide, not just the on-screen frame — the fix for the
// one-page-deck defect. The prompt carries the @page/@media-print contract, the
// .pg slide model, and permits data: URIs (for embedded imagery).
func TestPackagingStudioShipDeckCarriesPrintChassis(t *testing.T) {
	def := packagingStudioDefinition()
	shipDeck := packagingStudioStage(t, def, "ship_deck")
	for _, need := range []string{"@page", "@media print", ".pg", "break-after:page", "#stage", "data: URIs"} {
		if !strings.Contains(shipDeck.PromptBody, need) {
			t.Errorf("ship_deck prompt missing the print-chassis contract %q:\n%s", need, shipDeck.PromptBody)
		}
	}
}

func TestPackagingStudioShipDeckCarriesFirstClassDesignAndEditorContract(t *testing.T) {
	shipDeck := packagingStudioStage(t, packagingStudioDefinition(), "ship_deck")
	for _, need := range []string{
		"12-column grid", "minimum 96px safe zone", "COMPOSITION RHYTHM",
		"no more than 45 client-facing words", "data-deck-element", "FULL-BLEED LAW",
		"native presenter owns navigation", "Do not add custom JavaScript", "class=\"notes\" hidden",
		`<figure class="image-plate fig-N"`, `add class "bleed"`, "left:0;top:0;width:1920px;height:1080px",
	} {
		if !strings.Contains(shipDeck.PromptBody, need) {
			t.Errorf("ship_deck prompt missing first-class design contract %q:\n%s", need, shipDeck.PromptBody)
		}
	}
}

func TestPackagingStudioStoryIdentityAndCompositionAdaptToTheActualBrief(t *testing.T) {
	def := packagingStudioDefinition()
	story := packagingStudioStage(t, def, "story_architects")
	if got := []string{story.Personas[0].Name, story.Personas[1].Name, story.Personas[2].Name}; !slices.Equal(got, []string{"decision_arc", "audience_reframe", "proof_to_action"}) {
		t.Fatalf("story architects=%v, want artifact-neutral competing lenses", got)
	}
	for _, persona := range append(append([]ProcessPersona{}, story.Personas...), packagingStudioStage(t, def, "identity").Personas...) {
		lower := strings.ToLower(persona.System)
		for _, forbidden := range []string{"this venture", "founder conviction", "franchise playbook", "--heat", "duotone treatment"} {
			if strings.Contains(lower, forbidden) {
				t.Errorf("persona %q still prescribes the fixed venture identity %q: %s", persona.Name, forbidden, persona.System)
			}
		}
	}

	for _, stageID := range []string{"layout_plan", "ship_deck"} {
		prompt := packagingStudioStage(t, def, stageID).PromptBody
		for _, want := range []string{"For 1-3 slides", "For 4-7 slides", "For 8 or more slides", "Never force novelty"} {
			if !strings.Contains(prompt, want) {
				t.Errorf("%s prompt lost proportional composition rule %q:\n%s", stageID, want, prompt)
			}
		}
		if strings.Contains(prompt, "Mix at least four composition types") || strings.Contains(prompt, "Use at least four appropriate composition types") {
			t.Errorf("%s still forces four composition types regardless of deck size:\n%s", stageID, prompt)
		}
	}
}

// The render fallback stylesheet is a verbatim tail of the single chassis source
// — there is no second copy to drift out of sync.
func TestPackagingDeckPrintCSSDerivesFromChassis(t *testing.T) {
	if !strings.Contains(packagingDeckChassisCSS, "@page") {
		t.Fatal("deck chassis is missing its @page rule")
	}
	printCSS := packagingDeckPrintCSS()
	if printCSS == "" || !strings.Contains(packagingDeckChassisCSS, printCSS) {
		t.Fatalf("packagingDeckPrintCSS() is not a verbatim tail of the chassis — they have drifted:\n%s", printCSS)
	}
	for _, need := range []string{"@page", "@media print", "break-after:page", ".pg"} {
		if !strings.Contains(printCSS, need) {
			t.Errorf("derived print CSS missing %q", need)
		}
	}
}

// Imagery is ART-DIRECTED: a writer direction stage + an authored generation
// compile stage sit AFTER identity + the chosen narrative and BEFORE ship_deck,
// which reads both. ship_deck stays the last writer (the deck is the
// deliverable). The director's prompt carries the editorial + chassis laws and
// permits a zero-image typographic package.
func TestPackagingStudioImageryStagesArtDirectedAndOrdered(t *testing.T) {
	def := packagingStudioDefinition()

	direction := packagingStudioStage(t, def, "imagery_direction")
	if direction.Role != processRoleWriter {
		t.Fatalf("imagery_direction role=%q, want writer", direction.Role)
	}
	gen := packagingStudioStage(t, def, "imagery_generate")
	if gen.Role != processRoleCompile || gen.Compile == nil {
		t.Fatalf("imagery_generate must be an authored compile stage (role=%q, hasCompile=%v)", gen.Role, gen.Compile != nil)
	}

	for _, need := range []string{"identity", "write", "voice"} {
		if !containsString(direction.InputFrom, need) {
			t.Errorf("imagery_direction inputFrom=%v, missing %q", direction.InputFrom, need)
		}
	}
	if !containsString(gen.InputFrom, "imagery_direction") {
		t.Errorf("imagery_generate inputFrom=%v, must read imagery_direction", gen.InputFrom)
	}
	shipDeck := packagingStudioStage(t, def, "ship_deck")
	for _, need := range []string{"imagery_direction", "imagery_generate"} {
		if !containsString(shipDeck.InputFrom, need) {
			t.Errorf("ship_deck inputFrom=%v, missing %q", shipDeck.InputFrom, need)
		}
	}

	idx := map[string]int{}
	for i, s := range def.Stages {
		idx[s.ID] = i
	}
	order := []string{"write", "identity", "imagery_direction", "imagery_generate", "ship_deck"}
	for i := 1; i < len(order); i++ {
		if idx[order[i-1]] >= idx[order[i]] {
			t.Fatalf("stage order broken: %s(%d) must precede %s(%d)", order[i-1], idx[order[i-1]], order[i], idx[order[i]])
		}
	}

	lastWriter := ""
	for _, s := range def.Stages {
		if s.Role == processRoleWriter && strings.TrimSpace(s.OutputContract) != "" {
			lastWriter = s.ID
		}
	}
	if lastWriter != "ship_deck" {
		t.Fatalf("last writer stage=%q, want ship_deck (the deck must stay the deliverable)", lastWriter)
	}

	for _, need := range []string{"emotional", "full bleeds", "crescendo", "typographic", "JSON"} {
		if !strings.Contains(direction.PromptBody, need) {
			t.Errorf("imagery_direction prompt missing the art-direction/chassis cue %q", need)
		}
	}
}

// The director's JSON maps to generator shots; shots missing a subject or a
// named temperature are dropped (the generator requires both); a garbled or
// empty block is a valid ZERO-image typographic outcome, never an error.
func TestParseImageryDirection(t *testing.T) {
	body := "Here is the direction.\n\n```json\n" + `{
  "visual_system": "deep warm blacks, one red accent, 35mm",
  "shots": [
    {"fig": 1, "slot": "bleed", "subject": "a rooftop crowd mid-laugh", "composition": "wide, eyeline high", "temperature": "joy", "caption": "FIG. 1 — the room", "why": "opens on shared belief"},
    {"fig": 2, "slot": "plate", "subject": "", "temperature": "drama"},
    {"fig": 3, "slot": "plate", "subject": "cranes at dawn", "temperature": ""},
    {"fig": 4, "slot": "bleed", "subject": "the founder on stage", "composition": "tight", "temperature": "resolve", "place": "the Ryman"}
  ]
}` + "\n```\n"
	vs, shots := parseImageryDirection(body)
	if vs == "" {
		t.Fatal("visual system not parsed")
	}
	if len(shots) != 2 {
		t.Fatalf("parsed %d shots, want 2 (subject-less and temperature-less dropped)", len(shots))
	}
	if shots[0].Fig != 1 || shots[0].Temperature != "joy" || !strings.Contains(shots[0].Description, "rooftop crowd") {
		t.Fatalf("shot[0]=%+v unexpected", shots[0])
	}
	if shots[1].Fig != 4 || shots[1].Place != "the Ryman" {
		t.Fatalf("shot[1]=%+v unexpected", shots[1])
	}
	if _, s := parseImageryDirection("```json\n{\"visual_system\":\"x\",\"shots\":[]}\n```"); len(s) != 0 {
		t.Fatalf("empty shots must parse to zero, got %d", len(s))
	}
	if _, s := parseImageryDirection("no json here, just prose about the deck"); len(s) != 0 {
		t.Fatalf("prose-only direction must yield zero shots, got %d", len(s))
	}
}

// Placement inlines each image as a data: URI onto its .fig-N slot in the deck
// head; an image whose slot the writer never built is disclosed as missing; a
// zero-image package passes the deck through untouched.
func TestApplyDeckImageryPlacesAndDiscloses(t *testing.T) {
	deck := `<!doctype html><html><head><title>d</title></head><body><div id="stage"><section class="pg on bleed fig-1"><div class="ph"></div></section><section class="pg"><div class="plate fig-2"><div class="ph"></div></div></section></div></body></html>`
	images := []deckImage{
		{Fig: 1, DataURI: "data:image/png;base64,AAAA"},
		{Fig: 3, DataURI: "data:image/jpeg;base64,BBBB"}, // no fig-3 slot in the deck
	}
	out, note := applyDeckImagery(deck, images, 2, 0)
	if !strings.Contains(out, `<style id="bonfire-imagery">`) {
		t.Fatalf("imagery style block not injected:\n%s", out)
	}
	if !strings.Contains(out, ".fig-1 .ph{background-image:url(data:image/png;base64,AAAA)") {
		t.Fatalf("fig-1 rule missing:\n%s", out)
	}
	if hi, bi := strings.Index(out, `<style id="bonfire-imagery">`), strings.Index(out, "<body>"); hi < 0 || hi > bi {
		t.Fatal("imagery style must be injected into the document head, before the body")
	}
	if !strings.Contains(note, "1 image(s) inlined") || !strings.Contains(note, "no matching slot") {
		t.Fatalf("note did not disclose placed + missing-slot: %q", note)
	}
	out2, note2 := applyDeckImagery(deck, nil, 0, 0)
	if out2 != deck || !strings.Contains(note2, "typographic") {
		t.Fatalf("zero-image path must pass the deck through untouched with a typographic note: %q", note2)
	}
}

// The IDENTITY stage is always present (the runtime does not skip stages), and
// its authored prompt carries BOTH branches: develop a competition when INTAKE
// declares no brand assets, and disclose a skip when assets exist. It reads the
// INTAKE choice to pick the branch.
func TestPackagingStudioIdentityConditionalBothBranches(t *testing.T) {
	def := legacyPackagingStudioDefinition()
	identity := packagingStudioStage(t, def, "identity")

	if !containsString(identity.InputFrom, "intake") {
		t.Fatalf("identity inputFrom=%v, must read the INTAKE brand-assets choice", identity.InputFrom)
	}
	body := strings.ToLower(identity.PromptBody)
	// The develop branch: rival directions on the same sample slides.
	for _, need := range []string{"rival", "sample slide", "winner"} {
		if !strings.Contains(body, need) {
			t.Errorf("identity prompt missing the develop-branch cue %q:\n%s", need, identity.PromptBody)
		}
	}
	// The skip branch: disclose that a client identity exists.
	for _, need := range []string{"skip", "brand assets provided"} {
		if !strings.Contains(body, need) {
			t.Errorf("identity prompt missing the skip-branch cue %q:\n%s", need, identity.PromptBody)
		}
	}
	// It is a judges stage — the design panel scores the directions.
	if len(identity.Personas) == 0 {
		t.Fatal("identity judges stage has no design panel personas")
	}
}

// The checkpoint choices flow: INTAKE offers the brand-assets branch, the
// COMPETE choice card reads its options from the judges' verdict (OptionsFrom),
// and the founder pass offers the ship/send-back taste decision.
func TestPackagingStudioCheckpointChoicesFlow(t *testing.T) {
	def := legacyPackagingStudioDefinition()

	intake := packagingStudioStage(t, def, "intake")
	if intake.Title != "Intake — source, audience, and visual direction" {
		t.Fatalf("intake title=%q, want customer-facing source and visual-direction language", intake.Title)
	}
	if intake.CheckpointSpec == nil || len(intake.CheckpointSpec.Options) != 2 {
		t.Fatalf("intake checkpoint options=%+v, want the two brand-assets branches", intake.CheckpointSpec)
	}
	question := strings.ToLower(intake.CheckpointSpec.Question)
	for _, leaked := range []string{"verbatim", "law downstream", "confirm the intake brief"} {
		if strings.Contains(question, leaked) {
			t.Fatalf("intake question leaks internal studio policy %q: %q", leaked, intake.CheckpointSpec.Question)
		}
	}
	for _, need := range []string{"brand assets", "visual direction", "source material"} {
		if !strings.Contains(question, need) {
			t.Fatalf("intake question missing customer-facing decision %q: %q", need, intake.CheckpointSpec.Question)
		}
	}
	intakeLabels := make([]string, 0, len(intake.CheckpointSpec.Options))
	for _, option := range intake.CheckpointSpec.Options {
		intakeLabels = append(intakeLabels, option.Label)
		// Both intake branches PROCEED — the branch choice is grounding for
		// IDENTITY, never a send-back or a hold.
		if processCheckpointOptionAction(option) != processCheckpointActionProceed {
			t.Fatalf("intake option %+v must proceed", option)
		}
	}
	if !containsString(intakeLabels, "no brand assets — develop identity") {
		t.Fatalf("intake options=%v, missing the develop-identity branch IDENTITY reads", intakeLabels)
	}

	choice := packagingStudioStage(t, def, "compete_choice")
	if choice.CheckpointSpec == nil {
		t.Fatal("compete_choice has no checkpoint spec")
	}
	if choice.CheckpointSpec.OptionsFrom != "compete_judges" {
		t.Fatalf("compete_choice optionsFrom=%q, want compete_judges (the winner + overrule card)", choice.CheckpointSpec.OptionsFrom)
	}
	if choice.Title != "Narrative direction" {
		t.Fatalf("compete_choice title=%q, want customer-facing narrative language", choice.Title)
	}
	choiceQuestion := strings.ToLower(choice.CheckpointSpec.Question)
	for _, leaked := range []string{"spends tokens", "human overrule", "write spends"} {
		if strings.Contains(choiceQuestion, leaked) {
			t.Fatalf("compete choice leaks internal execution language %q: %q", leaked, choice.CheckpointSpec.Question)
		}
	}
	for _, need := range []string{"three narrative directions", "shape the deck"} {
		if !strings.Contains(choiceQuestion, need) {
			t.Fatalf("compete choice missing customer-facing decision %q: %q", need, choice.CheckpointSpec.Question)
		}
	}
	if !containsString(choice.InputFrom, "compete_judges") {
		t.Fatalf("compete_choice inputFrom=%v, must read the judges' verdict", choice.InputFrom)
	}

	founder := packagingStudioStage(t, def, "founder_pass")
	if founder.CheckpointSpec == nil || strings.TrimSpace(founder.CheckpointSpec.Question) == "" {
		t.Fatal("founder_pass has no checkpoint question")
	}
	if founder.Title != "Final content review" {
		t.Fatalf("founder_pass title=%q, want a customer-facing review title", founder.Title)
	}
	founderQuestion := strings.ToLower(founder.CheckpointSpec.Question)
	for _, leaked := range []string{"founder", "do_not_touch", "ship preserves", "taste pass"} {
		if strings.Contains(founderQuestion, leaked) {
			t.Fatalf("final content review leaks internal term %q: %q", leaked, founder.CheckpointSpec.Question)
		}
	}
	if !strings.Contains(founderQuestion, "preserve exactly") {
		t.Fatalf("final content review lost the source-fidelity choice: %q", founder.CheckpointSpec.Question)
	}
	// The labels tell the truth (the negative-option teeth): "send back for
	// changes" mechanically re-queues WRITE with the founder's words as
	// revision notes; "ship as-is" proceeds.
	if len(founder.CheckpointSpec.Options) != 2 {
		t.Fatalf("founder_pass options=%+v, want ship-as-is + send-back", founder.CheckpointSpec.Options)
	}
	shipAsIs, sendBack := founder.CheckpointSpec.Options[0], founder.CheckpointSpec.Options[1]
	if shipAsIs.Label != "ship as-is" || processCheckpointOptionAction(shipAsIs) != processCheckpointActionProceed {
		t.Fatalf("founder_pass first option=%+v, want a proceed-action 'ship as-is'", shipAsIs)
	}
	if sendBack.Label != "send back for changes" || processCheckpointOptionAction(sendBack) != processCheckpointActionRevise || sendBack.Target != "write" {
		t.Fatalf("founder_pass second option=%+v, want a revise-action send-back targeting write", sendBack)
	}

	// ... "send back" re-queues the deck build (the first live run proved a
	// bad deck could reach this park with no way back), and "hold the package"
	// actually HOLDS: the negative options park or re-queue until an explicit
	// proceed.
	approval := packagingStudioStage(t, def, "ship_approval")
	if approval.CheckpointSpec == nil || len(approval.CheckpointSpec.Options) != 3 {
		t.Fatalf("ship_approval options=%+v, want approve + send-back + hold", approval.CheckpointSpec)
	}
	approve, deckBack, hold := approval.CheckpointSpec.Options[0], approval.CheckpointSpec.Options[1], approval.CheckpointSpec.Options[2]
	if approve.Label != "approve the ship" || processCheckpointOptionAction(approve) != processCheckpointActionProceed {
		t.Fatalf("ship_approval first option=%+v, want a proceed-action approve", approve)
	}
	if processCheckpointOptionAction(deckBack) != processCheckpointActionRevise || deckBack.Target != "ship_deck" {
		t.Fatalf("ship_approval second option=%+v, want a revise-action send-back targeting ship_deck", deckBack)
	}
	if hold.Label != "hold the package" || processCheckpointOptionAction(hold) != processCheckpointActionHold {
		t.Fatalf("ship_approval third option=%+v, want a hold-action hold", hold)
	}
	if approval.Title != "Final deck review" {
		t.Fatalf("ship_approval title=%q, want a customer-facing review title", approval.Title)
	}
	approvalQuestion := strings.ToLower(approval.CheckpointSpec.Question)
	for _, leaked := range []string{"five interlocking", "approve the ship", "render exports", "scoreboard"} {
		if strings.Contains(approvalQuestion, leaked) {
			t.Fatalf("final deck review leaks internal term %q: %q", leaked, approval.CheckpointSpec.Question)
		}
	}
}

func TestPackagingStudioModelWrittenStagesUseBoundedOpenAIWriter(t *testing.T) {
	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerAnthropicFable)
	def := packagingStudioDefinition()
	plan := &goalPlan{PlanVersion: goalPlanVersion, ProcessID: def.ID, Authority: codexJobAuthorityWorkspaceWrite, State: goalStateDecompose, routeVerified: true}
	pinProcessPlanForTest(t, plan, def)
	if err := instantiateProcessPlan(def, plan); err != nil {
		t.Fatalf("instantiateProcessPlan: %v", err)
	}
	assignGoalRunners(plan)
	for _, stageID := range []string{"voice", "imagery_direction", "ship_deck"} {
		stage := packagingStudioStage(t, def, stageID)
		if stage.Role != processRoleWriter || processStageThreadMode(stage) != "artifacts" {
			t.Fatalf("stage %s role/mode=%s/%s, want writer/artifacts", stageID, stage.Role, processStageThreadMode(stage))
		}
		st := plan.subtaskByID(stageID)
		if st == nil || st.Runner != agentRunnerOpenAIText {
			t.Fatalf("stage %s runner=%+v, want bounded openai_text despite retired deployment pin", stageID, st)
		}
	}
}

func TestPackagingStudioContextSnapshotCarriesAuthorizedSourceRefs(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	visible, _, err := app.createOSArtifactWithMetadata("artifacts", "Current company position", "The current operating thesis is evidence-first.", "AJ", map[string]string{
		"type": artifactTypeMarkdown, "visibility": "organization", "ownerEmail": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	privateSibling, _, err := app.createOSArtifactWithMetadata("artifacts", "Tom private scratchpad", "PRIVATE SIBLING MUST NOT ENTER A SHARED DECK", "Tom", map[string]string{
		"type": artifactTypeMarkdown, "visibility": "private", "ownerEmail": "tom@shareability.com", "requestedBy": "tom@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := newGoalEngine(app)
	plan := &goalPlan{ProcessID: packagingStudioProcessID, Objective: "Build the company deck", CreatedBy: "aj@shareability.com"}
	companyContext := engine.processStageCompanyContext(plan)
	if !strings.Contains(companyContext, "artifact_id="+visible.ID) || !strings.Contains(companyContext, "digest=") {
		t.Fatalf("company context lost durable source identity:\n%s", companyContext)
	}
	if strings.Contains(companyContext, privateSibling.ID) || strings.Contains(companyContext, "PRIVATE SIBLING") {
		t.Fatalf("company context leaked an unauthorized sibling:\n%s", companyContext)
	}
	contextSnapshot := packagingStudioStage(t, packagingStudioDefinition(), "context_snapshot")
	task, err := engine.processStageTaskAuthorized(context.Background(), plan, &goalSubtask{ID: "context_snapshot"}, contextSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(task, "Company Brain context") || !strings.Contains(task, "artifact_id="+visible.ID) {
		t.Fatalf("context snapshot did not receive source-linked company context:\n%s", task)
	}
}

func TestPackagingStudioRetryReassignmentPreservesBlockedPlan(t *testing.T) {
	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerAnthropicFable)
	def := packagingStudioDefinition()
	plan := &goalPlan{PlanVersion: goalPlanVersion, ProcessID: def.ID, Authority: codexJobAuthorityWorkspaceWrite, State: goalStateBlocked, routeVerified: true}
	pinProcessPlanForTest(t, plan, def)
	if err := instantiateProcessPlan(def, plan); err != nil {
		t.Fatalf("instantiateProcessPlan: %v", err)
	}
	write := plan.subtaskByID("write")
	write.Status = subtaskComplete
	write.ArtifactID = "saved-write"
	voice := plan.subtaskByID("voice")
	voice.Status = subtaskBlocked
	voice.Revisions = goalMaxRevisions
	voice.Runner = agentRunnerStub

	assignGoalRunners(plan)

	if voice.Runner != agentRunnerOpenAIText {
		t.Fatalf("blocked voice runner=%q, want repaired openai_text lane", voice.Runner)
	}
	if voice.Status != subtaskBlocked || voice.Revisions != goalMaxRevisions {
		t.Fatalf("runner refresh rewrote blocked retry state: %+v", voice)
	}
	if write.Status != subtaskComplete || write.ArtifactID != "saved-write" {
		t.Fatalf("runner refresh rewrote completed stage: %+v", write)
	}
}

// The house judge seat is conditional: absent a living house_style (every
// keyless deploy, and every deploy before the distiller runs) the red-team
// quartet and the compete trio stand alone; with one, "the house" joins BOTH
// judging panels carrying the banned-patterns list.
func TestPackagingStudioHouseJudgeSeatConditional(t *testing.T) {
	previousApp := kanbanApp
	t.Cleanup(func() { kanbanApp = previousApp })

	// No house_style: base panels only.
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	if seatInPersonas(studioRedTeamPersonas(), houseJudgePersonaName) {
		t.Fatal("red-team gained the house seat with no house_style")
	}
	if seatInPersonas(studioCompeteJudges(), houseJudgePersonaName) {
		t.Fatal("compete judges gained the house seat with no house_style")
	}
	baseRedTeam := len(studioRedTeamPersonas())
	baseJudges := len(studioCompeteJudges())

	// A living house_style with a banned pattern: the seat joins both panels.
	seedHouseStyleArtifact(t, app, "Banned patterns: momentum claims without numbers.")
	redTeam := studioRedTeamPersonas()
	if len(redTeam) != baseRedTeam+1 || !seatInPersonas(redTeam, houseJudgePersonaName) {
		t.Fatalf("red-team did not gain the house seat: %d seats", len(redTeam))
	}
	judges := studioCompeteJudges()
	if len(judges) != baseJudges+1 || !seatInPersonas(judges, houseJudgePersonaName) {
		t.Fatalf("compete judges did not gain the house seat: %d seats", len(judges))
	}
	// The banned-patterns list rides into the seat's system prompt.
	for _, persona := range redTeam {
		if persona.Name == houseJudgePersonaName && !strings.Contains(persona.System, "momentum claims without numbers") {
			t.Fatalf("house seat missing the banned pattern:\n%s", persona.System)
		}
	}

	// And the whole definition validates with the extra seats present.
	if err := validateProcessDefinition(packagingStudioDefinition()); err != nil {
		t.Fatalf("packaging_studio does not validate with the house seat: %v", err)
	}
}

// Approved source language stays exact downstream: the gate's authored prompt
// preserves quotations, and processStageTask carries the full goal objective
// into the scorer so the review is never blind to the source request.
func TestPackagingStudioApprovedSourceLanguageReachesGate(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	def := packagingStudioDefinition()

	gate := packagingStudioStage(t, def, "gate")
	if !strings.Contains(gate.PromptBody, "fixed source material") {
		t.Fatalf("gate prompt does not preserve approved source language:\n%s", gate.PromptBody)
	}

	const founderPhrase = "we are the last honest voice in this category"
	plan := &goalPlan{
		PlanVersion:   goalPlanVersion,
		ProcessID:     def.ID,
		routeVerified: true,
		Objective:     "Package the venture. The founder says verbatim: \"" + founderPhrase + "\".",
		Authority:     codexJobAuthorityWorkspaceWrite,
		State:         goalStateDecompose,
	}
	pinProcessPlanForTest(t, plan, def)
	if err := instantiateProcessPlan(def, plan); err != nil {
		t.Fatalf("instantiateProcessPlan: %v", err)
	}
	engine := newGoalEngine(app)
	gateSubtask := plan.subtaskByID("gate")
	if gateSubtask == nil {
		t.Fatal("gate subtask missing from the instantiated plan")
	}
	task := engine.processStageTask(plan, gateSubtask, gate)
	if !strings.Contains(task, founderPhrase) {
		t.Fatalf("gate scorer prompt does not carry the founder's verbatim words:\n%s", task)
	}
}

// --- SHIP: the five-artifact compile, driven through the REAL pipeline -------

const studioTestFounderPhrase = "we are the last honest voice in this category"

// studioTestDoNotTouch is the founder-pass instruction appended to the "ship
// as-is" option — the do_not_touch mark that must reach the ship_deck prompt.
const studioTestDoNotTouch = "do_not_touch: keep the line \"" + studioTestFounderPhrase + "\" exactly as written"

func studioTestDeckHTML() string {
	return "<!doctype html><html><head><style>body{color:#111}</style></head><body><div id=\"stage\">" +
		"<section class=\"pg on\" data-deck-slide=\"slide-1\" style=\"background:#101014\">" +
		"<div data-deck-element=\"headline-1\" data-deck-type=\"text\" style=\"position:absolute;left:120px;top:140px;width:1600px;height:240px;z-index:2;opacity:1;transform:rotate(0deg);font-size:92px;font-family:Arial;font-weight:700;color:#ffffff;text-align:left;line-height:1.05;letter-spacing:normal\">Slide 1 — " + studioTestFounderPhrase + "</div>" +
		"<div class=\"notes\" hidden>Opening note [BEAT]</div></section>" +
		"<section class=\"pg\" data-deck-slide=\"slide-2\" style=\"background:#f4efe5\">" +
		"<div data-deck-element=\"headline-2\" data-deck-type=\"text\" style=\"position:absolute;left:120px;top:140px;width:1600px;height:240px;z-index:2;opacity:1;transform:rotate(0deg);font-size:92px;font-family:Arial;font-weight:700;color:#111111;text-align:left;line-height:1.05;letter-spacing:normal\">Slide 2 — Close</div>" +
		"<div class=\"notes\" hidden>Closing note [BEAT]</div></section></div></body></html>"
}

// installStudioChildRunner is installFakeChildRunner with per-subtask bodies,
// so the voice/ship_deck writers produce the material the compile stage reads
// (a real HTML deck, a real presenter script) instead of a generic echo.
func installStudioChildRunner(t *testing.T, outputs map[string]string) *[]capturedChild {
	t.Helper()
	var mu sync.Mutex
	var wg sync.WaitGroup
	launched := &[]capturedChild{}

	original := startAgentThreadAsync
	t.Cleanup(func() {
		wg.Wait()
		startAgentThreadAsync = original
	})
	startAgentThreadAsync = func(app *kanbanBoardApp, thread scoutAgentThread) {
		meta := thread.Artifact.Metadata
		mu.Lock()
		*launched = append(*launched, capturedChild{
			threadID:  thread.ID,
			subtaskID: meta["goalSubtaskId"],
			authority: meta["authority"],
			mode:      thread.Mode,
			query:     thread.Query,
		})
		mu.Unlock()
		parent := meta["goalParentId"]
		sub := meta["goalSubtaskId"]
		if parent == "" {
			return
		}
		body := outputs[sub]
		if body == "" {
			body = "subtask output: " + thread.Query
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			child, _, err := app.updateOSArtifactWithMetadata(thread.Artifact.ID, "", body, "tester", map[string]string{
				"threadStatus": "complete",
				"status":       "complete",
			})
			if err != nil {
				return
			}
			app.foldGoalChildCompletion(parent, sub, child, "complete")
		}()
	}
	return launched
}

// driveStudioRunToShipApproval launches a REAL packaging_studio goal through
// the engine (fake responder + fake writer children) and resumes the first
// three human checkpoints — intake, compete choice, founder pass (with the
// do_not_touch mark riding the choice) — leaving the goal parked at the
// fourth: ship approval, with the five artifacts already filed. It returns
// the goal id, the launched children, and the parks observed in order.
func driveStudioRunToShipApproval(t *testing.T, app *kanbanBoardApp, packageID string) (string, *[]capturedChild, []string) {
	t.Helper()
	return driveStudioRunToShipApprovalFull(t, app, packageID, nil, nil)
}

// driveStudioRunToShipApprovalWithSetup is the same drive with a hook that
// runs AFTER the fake responder is installed — the slide-jury test uses it to
// wrap createAnthropicMessagesResponse so jury-shaped system prompts answer
// with jury JSON while every studio route keeps flowing to the routes fake.
func driveStudioRunToShipApprovalWithSetup(t *testing.T, app *kanbanBoardApp, packageID string, afterResponder func()) (string, *[]capturedChild, []string) {
	t.Helper()
	return driveStudioRunToShipApprovalFull(t, app, packageID, afterResponder, nil)
}

// driveStudioRunToShipApprovalFull additionally stamps a goal origin — the
// manifest tests launch from a channel so the ship resolution has an origin
// thread to post the manifest card into.
func driveStudioRunToShipApprovalFull(t *testing.T, app *kanbanBoardApp, packageID string, afterResponder func(), origin map[string]string) (string, *[]capturedChild, []string) {
	t.Helper()
	installFakeResponder(t, goalResponderRoutes{
		// Every authored persona (red team, identity judges, architects,
		// compete judges) answers through the fallback route.
		fallback: "Objection: the plan assumes distribution it has not earned. strengths_to_keep: the founder's voice.",
		// The shared panel synthesis: the compete verdict on the record, plus
		// the options array compete_choice reads (OptionsFrom).
		synthesis: "Synthesis: the panel verdict is on the record; the winner is franchise-playbook.\n[\"cultural-moment\", \"franchise-playbook\", \"founder-conviction\"]",
		// The WRITE synthesizer's gated deck copy — the source of The Wall.
		stage: "Deck copy, slide by slide, in a spoken register, quoting \"" + studioTestFounderPhrase + "\".",
	})
	if afterResponder != nil {
		afterResponder()
	}
	launched := installStudioChildRunner(t, map[string]string{
		"voice":     "Presenter script. Page 1 (30s): " + studioTestFounderPhrase + ". [BEAT] Close on the ask.",
		"ship_deck": studioTestDeckHTML(),
	})

	thread, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective:    "Package the venture. The founder says verbatim: \"" + studioTestFounderPhrase + "\".",
		CreatedBy:    "aj@shareability.com",
		PackageID:    packageID,
		ToolTemplate: packagingStudioProcessID,
		Origin:       origin,
	})
	if err != nil {
		t.Fatalf("launchGoalThread(packaging_studio): %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)

	var parks []string
	resume := func(choice string) {
		plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateApproval)
		if plan.Checkpoint == nil {
			t.Fatalf("goal parked at approval with no checkpoint record: %+v", plan)
		}
		parks = append(parks, plan.Checkpoint.StageID)
		if err := app.resumeApprovedGoalWithChoice(thread.Artifact.ID, "aj@shareability.com", choice); err != nil {
			t.Fatalf("resume %s with %q: %v", plan.Checkpoint.StageID, choice, err)
		}
	}
	resume("no brand assets — develop identity")
	resume("franchise-playbook")
	// The founder-pass taste moment: the option plus the do_not_touch mark.
	resume("ship as-is — " + studioTestDoNotTouch)

	// The fourth park: ship approval, after the compile filed the artifacts.
	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateApproval)
	if plan.Checkpoint == nil || plan.Checkpoint.StageID != "ship_approval" {
		t.Fatalf("fourth park is not the ship approval: %+v", plan.Checkpoint)
	}
	parks = append(parks, plan.Checkpoint.StageID)
	return thread.Artifact.ID, launched, parks
}

func TestPackagingStudioShipFinalizationRecoversAfterCommittedCrash(t *testing.T) {
	t.Skip("v2 ship-approval recovery contract; v3 delivers private artifacts without a routine final checkpoint")
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	parentID, _, _ := driveStudioRunToShipApproval(t, app, "")
	plan := waitForGoalStage(t, app, parentID, goalStateApproval)
	checkpointID := goalCheckpointID(parentID, plan.Checkpoint)
	optionID := ""
	for index, option := range plan.Checkpoint.Options {
		if option.action() == processCheckpointActionProceed {
			optionID = goalCheckpointOptionID(checkpointID, option, index)
		}
	}
	if optionID == "" {
		t.Fatal("ship proceed option missing")
	}
	goalCheckpointResolutionAfterCommitProbe = func(action string) error { return fmt.Errorf("injected committed %s crash", action) }
	t.Cleanup(func() { goalCheckpointResolutionAfterCommitProbe = nil })
	snapshot := mustArtifact(t, app, parentID)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	if _, err := app.resumeApprovedGoalWithCheckpointOptionAuthorized(context.Background(), user, snapshot, user.Name, checkpointID, optionID); err == nil {
		t.Fatal("committed crash was not surfaced")
	}
	if receipt := checkpointReceiptFor(t, app, parentID, checkpointID, optionID); receipt.State != goalCheckpointResolutionCommitted {
		t.Fatalf("crash receipt=%+v", receipt)
	}
	parent := mustArtifact(t, app, parentID)
	origin, ok := app.goalOriginChatThread(parent)
	if !ok {
		t.Fatal("Studio goal origin missing")
	}
	manifestCount := func(thread scoutChatThreadRecord) int {
		count := 0
		for _, message := range thread.Messages {
			if message.Kind == scoutChatMessageKindManifest && message.Manifest != nil && message.Manifest.GoalID == parentID {
				count++
			}
		}
		return count
	}
	if got := manifestCount(origin); got != 0 {
		t.Fatalf("manifest effects ran before finalizer: %d", got)
	}
	goalCheckpointResolutionAfterCommitProbe = nil
	restarted := newKanbanBoardApp()
	kanbanApp = restarted
	recoveryDone := make(chan string, 1)
	goalCheckpointResolutionRecoveryDoneProbe = func(id string) { recoveryDone <- id }
	t.Cleanup(func() { goalCheckpointResolutionRecoveryDoneProbe = nil })
	restarted.reconcileGoalThreadsAtBoot()
	select {
	case got := <-recoveryDone:
		if got != parentID {
			t.Fatalf("recovered goal=%q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Studio checkpoint recovery did not finish")
	}
	if receipt := checkpointReceiptFor(t, restarted, parentID, checkpointID, optionID); receipt.State != goalCheckpointResolutionFinalized {
		t.Fatalf("recovered receipt=%+v", receipt)
	}
	parent = mustArtifact(t, restarted, parentID)
	origin, ok = restarted.goalOriginChatThread(parent)
	if !ok || manifestCount(origin) != 1 {
		t.Fatalf("recovered manifests=%d origin=%v", manifestCount(origin), ok)
	}
	for _, artifact := range studioFiledDeliverables(t, restarted, parentID) {
		if artifact.Metadata[artifactHumanApprovedAtKey] == "" || artifact.Metadata["status"] != artifactStatusApproved {
			t.Fatalf("ship deliverable was not approved by finalizer: %s %+v", artifact.ID, artifact.Metadata)
		}
	}
}

// A durable ship-approval send-back is not a shipment. If the process dies
// after the revised transition is persisted but before the redo is driven,
// boot re-drives the exact reset deck stage, re-parks a fresh ship checkpoint,
// and finalizes the old receipt without ever filing a manifest.
func TestPackagingStudioShipReviseRedrivesAfterPreDriveCrash(t *testing.T) {
	t.Skip("v2 ship-approval send-back contract; v3 uses the pre-delivery repair gate")
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("createScoutChatThread: %v", err)
	}
	parentID, _, _ := driveStudioRunToShipApprovalFull(t, app, "", nil, map[string]string{
		"originKind":  agentThreadOriginPrivateThread,
		"originId":    channel.ID,
		"requestedBy": "aj@shareability.com",
	})
	plan := waitForGoalStage(t, app, parentID, goalStateApproval)
	oldCheckpointID := goalCheckpointID(parentID, plan.Checkpoint)
	reviseOptionID := ""
	for index, option := range plan.Checkpoint.Options {
		if option.action() == processCheckpointActionRevise {
			reviseOptionID = goalCheckpointOptionID(oldCheckpointID, option, index)
			break
		}
	}
	if reviseOptionID == "" {
		t.Fatal("ship revise option missing")
	}

	goalCheckpointAfterTransitionPersistProbe = func(action string) error {
		if action == processCheckpointActionRevise {
			return fmt.Errorf("injected revise crash before drive")
		}
		return nil
	}
	t.Cleanup(func() { goalCheckpointAfterTransitionPersistProbe = nil })
	if _, err := app.resumeApprovedGoalWithCheckpointOption(parentID, "aj@shareability.com", oldCheckpointID, reviseOptionID); err == nil {
		t.Fatal("revise pre-drive crash was not surfaced")
	}
	crashed := waitForGoalStage(t, app, parentID, goalStateExecute)
	receipt := checkpointReceiptFor(t, app, parentID, oldCheckpointID, reviseOptionID)
	if receipt.State != goalCheckpointResolutionCommitted || receipt.EffectiveOutcome != processCheckpointActionRevise ||
		!receipt.DriveNeeded || receipt.DriveCompletedAt != "" {
		t.Fatalf("revise pre-drive receipt=%+v", receipt)
	}
	if target := crashed.subtaskByID("ship_deck"); target == nil || target.Status != subtaskReady {
		t.Fatalf("ship deck was not durably reset before drive: %+v", target)
	}
	if manifests := studioManifestMessages(t, app, channel.ID); len(manifests) != 0 {
		t.Fatalf("revise filed a manifest before recovery: %+v", manifests)
	}

	goalCheckpointAfterTransitionPersistProbe = nil
	restarted := newKanbanBoardApp()
	restarted.apiKey = "test-key"
	kanbanApp = restarted
	recoveryDone := make(chan string, 1)
	goalCheckpointResolutionRecoveryDoneProbe = func(id string) { recoveryDone <- id }
	t.Cleanup(func() { goalCheckpointResolutionRecoveryDoneProbe = nil })
	restarted.reconcileGoalThreadsAtBoot()
	select {
	case got := <-recoveryDone:
		if got != parentID {
			t.Fatalf("recovered goal=%q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Studio revise recovery did not finish")
	}
	reparked := waitForGoalStage(t, restarted, parentID, goalStateApproval)
	if reparked.Checkpoint == nil || reparked.Checkpoint.StageID != "ship_approval" || goalCheckpointID(parentID, reparked.Checkpoint) == oldCheckpointID {
		t.Fatalf("revise did not re-park a fresh ship checkpoint: %+v", reparked.Checkpoint)
	}
	receipt = checkpointReceiptFor(t, restarted, parentID, oldCheckpointID, reviseOptionID)
	if receipt.State != goalCheckpointResolutionFinalized || receipt.EffectiveOutcome != processCheckpointActionRevise || receipt.DriveCompletedAt == "" {
		t.Fatalf("recovered revise receipt=%+v", receipt)
	}
	if manifests := studioManifestMessages(t, restarted, channel.ID); len(manifests) != 0 {
		t.Fatalf("normal revise was misclassified as shipped: %+v", manifests)
	}
}

// If the revise drive crossed the durable child-activation seam but the
// process died before recording that drive acknowledgement, restart cannot
// honestly replay the provider. It must fail closed and finalize the choice
// receipt without projecting a shipped manifest.
func TestPackagingStudioShipReviseMidDriveCrashFailsClosedWithoutManifest(t *testing.T) {
	t.Skip("v2 ship-approval send-back contract; v3 uses the pre-delivery repair gate")
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("createScoutChatThread: %v", err)
	}
	parentID, _, _ := driveStudioRunToShipApprovalFull(t, app, "", nil, map[string]string{
		"originKind":  agentThreadOriginPrivateThread,
		"originId":    channel.ID,
		"requestedBy": "aj@shareability.com",
	})
	plan := waitForGoalStage(t, app, parentID, goalStateApproval)
	checkpointID := goalCheckpointID(parentID, plan.Checkpoint)
	reviseOptionID := ""
	for index, option := range plan.Checkpoint.Options {
		if option.action() == processCheckpointActionRevise {
			reviseOptionID = goalCheckpointOptionID(checkpointID, option, index)
			break
		}
	}
	if reviseOptionID == "" {
		t.Fatal("ship revise option missing")
	}

	studioRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, thread scoutAgentThread) {
		if thread.Artifact.Metadata["goalSubtaskId"] == "ship_deck" {
			return // provider accepted/start recorded; callback is lost with process death
		}
		studioRunner(app, thread)
	}
	t.Cleanup(func() { startAgentThreadAsync = studioRunner })
	goalCheckpointAfterDriveProbe = func(action string) error {
		if action == processCheckpointActionRevise {
			return fmt.Errorf("injected crash after revise dispatch")
		}
		return nil
	}
	t.Cleanup(func() { goalCheckpointAfterDriveProbe = nil })
	if _, err := app.resumeApprovedGoalWithCheckpointOption(parentID, "aj@shareability.com", checkpointID, reviseOptionID); err == nil {
		t.Fatal("mid-drive crash was not surfaced")
	}
	crashed := waitForGoalStage(t, app, parentID, goalStateExecute)
	if target := crashed.subtaskByID("ship_deck"); target == nil || target.Status != subtaskRunning {
		t.Fatalf("mid-drive child is not durably running: %+v", target)
	}
	if receipt := checkpointReceiptFor(t, app, parentID, checkpointID, reviseOptionID); receipt.State != goalCheckpointResolutionCommitted || receipt.DriveCompletedAt != "" {
		t.Fatalf("mid-drive receipt=%+v", receipt)
	}

	goalCheckpointAfterDriveProbe = nil
	restarted := newKanbanBoardApp()
	kanbanApp = restarted
	recoveryDone := make(chan string, 1)
	goalCheckpointResolutionRecoveryDoneProbe = func(id string) { recoveryDone <- id }
	t.Cleanup(func() { goalCheckpointResolutionRecoveryDoneProbe = nil })
	restarted.reconcileGoalThreadsAtBoot()
	select {
	case <-recoveryDone:
	case <-time.After(5 * time.Second):
		t.Fatal("mid-drive recovery did not finish")
	}
	blocked := waitForGoalStage(t, restarted, parentID, goalStateBlocked)
	if !strings.Contains(blocked.Blocker, "execution state is unknown after restart") {
		t.Fatalf("mid-drive blocker=%q", blocked.Blocker)
	}
	receipt := checkpointReceiptFor(t, restarted, parentID, checkpointID, reviseOptionID)
	if receipt.State != goalCheckpointResolutionFinalized || receipt.EffectiveOutcome != "drive_blocked" || receipt.DriveCompletedAt == "" {
		t.Fatalf("mid-drive finalized receipt=%+v", receipt)
	}
	if manifests := studioManifestMessages(t, restarted, channel.ID); len(manifests) != 0 {
		t.Fatalf("ambiguous mid-drive revise projected a manifest: %+v", manifests)
	}
}

// studioFiledDeliverables collects the artifacts the run's ship_compile stage
// filed, keyed by contract.
func studioFiledDeliverables(t *testing.T, app *kanbanBoardApp, goalID string) map[string]meetingMemoryEntry {
	t.Helper()
	filed := map[string]meetingMemoryEntry{}
	for _, artifact := range app.osArtifactsSnapshot(0) {
		if artifact.Metadata["source"] != "packaging_studio_ship" || artifact.Metadata["goalId"] != goalID {
			continue
		}
		filed[artifact.Metadata["artifactContract"]] = artifact
	}
	return filed
}

// studioRenderQueueJobs reads the fake file-per-job render queue in the temp
// data dir — exactly what the sidecar would claim.
func studioRenderQueueJobs(t *testing.T) []renderRunnerJob {
	t.Helper()
	entries, err := os.ReadDir(renderRunnerQueuePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read render queue: %v", err)
	}
	var jobs []renderRunnerJob
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(renderRunnerQueuePath(), entry.Name()))
		if err != nil {
			t.Fatalf("read render job %s: %v", entry.Name(), err)
		}
		var job renderRunnerJob
		if err := json.Unmarshal(raw, &job); err != nil {
			t.Fatalf("decode render job %s: %v", entry.Name(), err)
		}
		jobs = append(jobs, job)
	}
	return jobs
}

var studioWantContracts = []string{
	packagingStudioDeckContract,
	packagingStudioWallContract,
	packagingStudioTalkContract,
	packagingStudioRigorContract,
	packagingStudioFindingsContract,
}

// The REAL pipeline ships the five-artifact package: a packaging_studio goal
// driven from launch through all four human checkpoints files the five
// interlocking artifacts (deck html_deck + The Wall + The Talk with
// paperKit=true + rigor companion + findings record with the run's ACTUAL
// verdicts), attaches every one to the venture package, enqueues exactly the
// two render exports (deck flattened, The Talk text-native — kinds chosen
// server-side), carries the founder's do_not_touch mark into the ship_deck
// prompt, and reaches verified after the explicit ship approval.
func TestPackagingStudioShipFilesFiveArtifactsAndEnqueuesRenders(t *testing.T) {
	t.Skip("v2 five-artifact default retired; v3 files one deck by default")
	app := newIsolatedKanbanBoardApp(t)
	// A live render sidecar: a fresh heartbeat on the shared volume makes
	// renderSidecarAvailable() true, so the export jobs enqueue into the fake
	// file-per-job queue in the temp data dir.
	if err := writeHealthyRenderRunnerHeartbeatForTest(t, "test-render-runner"); err != nil {
		t.Fatalf("write render heartbeat: %v", err)
	}
	if !renderSidecarAvailable() {
		t.Fatal("render sidecar should read as available after a fresh heartbeat")
	}
	// No render callback ever fires in this test, so the slide jury's bounded
	// wait for page images must expire fast and disclose the skip.
	t.Setenv("BONFIRE_SLIDE_JURY_WAIT", "1s")
	restorePoll := slideJuryPollInterval
	slideJuryPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { slideJuryPollInterval = restorePoll })
	pkg, err := app.createVenturePackage("Aurora", "an IP thesis", "aj@shareability.com")
	if err != nil {
		t.Fatalf("createVenturePackage: %v", err)
	}

	parentID, launched, parks := driveStudioRunToShipApproval(t, app, pkg.ID)

	// The four human touchpoints parked IN ORDER.
	wantParks := []string{"intake", "compete_choice", "founder_pass", "ship_approval"}
	if len(parks) != len(wantParks) {
		t.Fatalf("goal parked %d times (%v), want the four touchpoints %v", len(parks), parks, wantParks)
	}
	for index, want := range wantParks {
		if parks[index] != want {
			t.Fatalf("parks=%v, want %v in order", parks, wantParks)
		}
	}

	// The founder's do_not_touch mark reached the ship_deck writer's prompt.
	shipDeckQuery := ""
	for _, child := range *launched {
		if child.subtaskID == "ship_deck" {
			shipDeckQuery = child.query
		}
	}
	if shipDeckQuery == "" {
		t.Fatalf("the ship_deck writer never launched: %+v", *launched)
	}
	if !strings.Contains(shipDeckQuery, studioTestDoNotTouch) {
		t.Fatalf("ship_deck prompt does not carry the founder's do_not_touch mark:\n%s", shipDeckQuery)
	}

	// The five interlocking artifacts, filed by the RUN's compile stage.
	filed := studioFiledDeliverables(t, app, parentID)
	if len(filed) != 5 {
		t.Fatalf("the run filed %d ship artifacts, want the 5 interlocking artifacts: %v", len(filed), filed)
	}
	for _, contract := range studioWantContracts {
		artifact, ok := filed[contract]
		if !ok {
			t.Fatalf("the run did not file the %q artifact", contract)
		}
		if artifact.Metadata["packageId"] != pkg.ID {
			t.Errorf("%q not attached to the package: packageId=%q", contract, artifact.Metadata["packageId"])
		}
	}
	// ... and the package binder carries every one.
	record, ok := app.venturePackageByID(pkg.ID)
	if !ok {
		t.Fatal("venture package disappeared")
	}
	for _, contract := range studioWantContracts {
		if !containsString(record.ArtifactIDs, filed[contract].ID) {
			t.Errorf("package binder missing the %q artifact %s: %v", contract, filed[contract].ID, record.ArtifactIDs)
		}
	}

	// The flatten law, server-owned: the deck is an html_deck that flattens;
	// The Talk (and The Wall) carry the paperKit stamp and print text-native.
	deck := filed[packagingStudioDeckContract]
	if deck.Metadata["type"] != artifactTypeHTMLDeck {
		t.Errorf("deck type=%q, want %s", deck.Metadata["type"], artifactTypeHTMLDeck)
	}
	if deck.Metadata["paperKit"] == "true" {
		t.Error("the deck must NOT be paper-kit — it flattens, never text-native")
	}
	if !strings.Contains(deck.Text, "<!doctype html") {
		t.Errorf("the filed deck is not the ship_deck writer's HTML: %q", deck.Text)
	}
	talk := filed[packagingStudioTalkContract]
	if talk.Metadata["paperKit"] != "true" {
		t.Errorf("The Talk must stamp paperKit=true so it prints text-native, got %q", talk.Metadata["paperKit"])
	}
	if serverRenderKindForArtifact(talk) != renderJobKindPaper || serverRenderKindForArtifact(deck) != renderJobKindDeck {
		t.Errorf("render kinds: talk=%q deck=%q, want paper/deck", serverRenderKindForArtifact(talk), serverRenderKindForArtifact(deck))
	}
	if filed[packagingStudioWallContract].Metadata["paperKit"] != "true" {
		t.Error("The Wall must stamp paperKit=true")
	}
	// The Wall and The Talk carry the run's own stage material.
	if !strings.Contains(filed[packagingStudioWallContract].Text, "Deck copy, slide by slide") {
		t.Errorf("The Wall does not carry WRITE's gated copy: %q", filed[packagingStudioWallContract].Text)
	}
	if !strings.Contains(talk.Text, "[BEAT]") {
		t.Errorf("The Talk does not carry VOICE's presenter script: %q", talk.Text)
	}

	// Exactly TWO render enqueues in the fake queue: the deck (kind deck) and
	// The Talk (kind paper); the job ids are stamped on the source artifacts.
	jobs := studioRenderQueueJobs(t)
	if len(jobs) != 2 {
		t.Fatalf("render queue holds %d jobs, want exactly 2 (deck + The Talk): %+v", len(jobs), jobs)
	}
	kindByArtifact := map[string]string{}
	jobByArtifact := map[string]string{}
	for _, job := range jobs {
		kindByArtifact[job.ArtifactID] = job.Kind
		jobByArtifact[job.ArtifactID] = job.ID
	}
	if kindByArtifact[deck.ID] != renderJobKindDeck {
		t.Errorf("deck render job kind=%q, want deck (flattened)", kindByArtifact[deck.ID])
	}
	if kindByArtifact[talk.ID] != renderJobKindPaper {
		t.Errorf("The Talk render job kind=%q, want paper (text-native)", kindByArtifact[talk.ID])
	}
	for _, artifact := range []meetingMemoryEntry{deck, talk} {
		fresh := mustArtifact(t, app, artifact.ID)
		if fresh.Metadata["renderJobId"] == "" || fresh.Metadata["renderJobId"] != jobByArtifact[artifact.ID] {
			t.Errorf("%s renderJobId=%q, want the queued job %q", artifact.ID, fresh.Metadata["renderJobId"], jobByArtifact[artifact.ID])
		}
	}

	// The findings record carries the run's ACTUAL verdicts — the gate's
	// outcome, every checkpoint choice (the founder's mark included), and the
	// panel synthesis — aggregated from the stage artifacts, not placeholders.
	findings := filed[packagingStudioFindingsContract].Text
	for _, want := range []string{
		"- Outcome: accept",                  // the gate decision record, verbatim
		"clears the bar",                     // the gate scorer's actual reasons
		"no brand assets — develop identity", // the intake choice
		"franchise-playbook",                 // the compete choice
		studioTestDoNotTouch,                 // the founder's mark
		"the panel verdict is on the record", // the panel synthesis
		"(" + processRoleGate + ")",          // sectioned by role
		"(" + processRoleHumanCheckpoint + ")",
	} {
		if !strings.Contains(findings, want) {
			t.Errorf("findings record missing the real verdict %q:\n%s", want, findings)
		}
	}

	// The compile record (the ship_approval checkpoint's grounding) discloses
	// both enqueued exports.
	plan := waitForGoalStage(t, app, parentID, goalStateApproval)
	compileSt := plan.subtaskByID("ship_compile")
	if compileSt == nil || compileSt.Status != subtaskComplete {
		t.Fatalf("ship_compile did not complete: %+v", compileSt)
	}
	compileRecord := mustArtifact(t, app, compileSt.ArtifactID)
	if strings.Count(compileRecord.Text, "render export queued as") != 2 {
		t.Errorf("compile record does not disclose the two render enqueues:\n%s", compileRecord.Text)
	}

	// The slide jury waited for the export, no callback ever landed page
	// images, and the stage DISCLOSED the timeout skip — it never blocked the
	// ship and never called a jury model.
	jurySt := plan.subtaskByID("slide_jury")
	if jurySt == nil || jurySt.Status != subtaskComplete {
		t.Fatalf("slide_jury must complete (disclosed skip, not block) when the export never lands: %+v", jurySt)
	}
	juryRecord := mustArtifact(t, app, jurySt.ArtifactID)
	if !strings.Contains(juryRecord.Text, "skipped (disclosed)") || !strings.Contains(juryRecord.Text, "did not complete within") {
		t.Errorf("slide_jury record does not disclose the export-timeout skip:\n%s", juryRecord.Text)
	}

	// The explicit ship approval resumes the goal through to verified.
	if err := app.resumeApprovedGoalWithChoice(parentID, "aj@shareability.com", "approve the ship"); err != nil {
		t.Fatalf("ship approval resume: %v", err)
	}
	plan = waitForGoalStage(t, app, parentID, goalStateVerified)
	if plan.Verification.Verdict != goalReviewPass {
		t.Fatalf("verification verdict=%q, want pass", plan.Verification.Verdict)
	}
}

// Sidecar-absent (keyless deploys, no render runner): the SAME real run still
// files all five artifacts and DISCLOSES the skipped exports in the compile
// record — the ship never blocks, and a goal without a package discloses that
// too instead of failing.
func TestPackagingStudioShipDisclosesSkipWithoutSidecar(t *testing.T) {
	t.Skip("v2 checkpoint harness retired; deck-only compiler coverage lives in v3 focused tests")
	app := newIsolatedKanbanBoardApp(t)
	// No heartbeat written → renderSidecarAvailable() is false.
	if renderSidecarAvailable() {
		t.Fatal("render sidecar should be absent with no heartbeat")
	}

	parentID, _, parks := driveStudioRunToShipApproval(t, app, "")
	if len(parks) != 4 {
		t.Fatalf("goal parked %d times (%v), want the four touchpoints even sidecar-absent", len(parks), parks)
	}

	filed := studioFiledDeliverables(t, app, parentID)
	if len(filed) != 5 {
		t.Fatalf("the run filed %d ship artifacts, want 5 even sidecar-absent", len(filed))
	}
	for _, contract := range []string{packagingStudioDeckContract, packagingStudioTalkContract} {
		if filed[contract].Metadata["renderJobId"] != "" {
			t.Errorf("%q enqueued a render job with no sidecar", contract)
		}
	}
	if jobs := studioRenderQueueJobs(t); len(jobs) != 0 {
		t.Fatalf("render queue holds %d jobs with no sidecar, want 0: %+v", len(jobs), jobs)
	}

	// The compile record discloses the skips (and the missing package).
	plan := waitForGoalStage(t, app, parentID, goalStateApproval)
	compileSt := plan.subtaskByID("ship_compile")
	if compileSt == nil || compileSt.Status != subtaskComplete {
		t.Fatalf("ship_compile must complete (skip, not block) sidecar-absent: %+v", compileSt)
	}
	compileRecord := mustArtifact(t, app, compileSt.ArtifactID)
	if strings.Count(compileRecord.Text, "render sidecar not available") != 2 {
		t.Errorf("compile record does not disclose both render skips:\n%s", compileRecord.Text)
	}
	if !strings.Contains(compileRecord.Text, "filed unattached (disclosed)") {
		t.Errorf("compile record does not disclose the missing package:\n%s", compileRecord.Text)
	}

	// Sidecar-absent, the slide jury has no export to wait on: the skip is
	// disclosed IMMEDIATELY (no renderJobId stamp, no page images) and the run
	// still reaches its ship approval.
	jurySt := plan.subtaskByID("slide_jury")
	if jurySt == nil || jurySt.Status != subtaskComplete {
		t.Fatalf("slide_jury must complete (disclosed skip) sidecar-absent: %+v", jurySt)
	}
	juryRecord := mustArtifact(t, app, jurySt.ArtifactID)
	if !strings.Contains(juryRecord.Text, "skipped (disclosed)") || !strings.Contains(juryRecord.Text, "was not queued") {
		t.Errorf("slide_jury record does not disclose the sidecar-absent skip:\n%s", juryRecord.Text)
	}

	// The ship approval still lands the run at verified.
	if err := app.resumeApprovedGoalWithChoice(parentID, "aj@shareability.com", "approve the ship"); err != nil {
		t.Fatalf("ship approval resume: %v", err)
	}
	waitForGoalStage(t, app, parentID, goalStateVerified)
}

// --- SLIDE JURY: the vision critics inside the REAL pipeline -----------------

// The slide jury runs inside the executing pipeline once the deck's PDF export
// completes: a simulated sidecar lands the page JPEGs as {kind: image} assets
// the moment ship_compile stamps the render job, the jury trio + synthesis all
// receive EVERY page as image blocks, the merged scoreboard files as
// slide_jury_v1, and the findings record gains the revision-notes section —
// with NOTHING auto-revised (the founder decides at ship approval).
func TestPackagingStudioSlideJurySeesRenderedPages(t *testing.T) {
	t.Skip("v2 checkpoint harness retired; v3 jury runs before the delivery gate")
	app := newIsolatedKanbanBoardApp(t)
	if err := writeHealthyRenderRunnerHeartbeatForTest(t, "test-render-runner"); err != nil {
		t.Fatalf("write render heartbeat: %v", err)
	}
	t.Setenv("BONFIRE_SLIDE_JURY_WAIT", "1s")
	restorePoll := slideJuryPollInterval
	slideJuryPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { slideJuryPollInterval = restorePoll })

	const seatJSON = `{"pages":[{"page":1,"score":6.5,"fix":"Cut the headline to seven words"},{"page":2,"score":9,"fix":"KEEP"}],"weakest_three":[1],"strongest_three":[2]}`
	const mergedScoreboard = "Merged scoreboard: page 1 avg 6.5 — cut the headline to seven words; page 2 KEEP. weakest_three: [1]; strongest_three: [2]."

	// Jury-shaped system prompts answer with jury material; everything else
	// keeps flowing to the studio routes fake installed by the drive helper.
	var juryMu sync.Mutex
	var juryRequests []openAITextRequest
	wrapJuryResponder := func() {
		prior := createOpenAITextResponse
		createOpenAITextResponse = func(ctx context.Context, apiKey string, request openAITextRequest) (string, error) {
			system := strings.ToLower(request.Instructions)
			if !strings.Contains(system, "slide jury") {
				return prior(ctx, apiKey, request)
			}
			juryMu.Lock()
			juryRequests = append(juryRequests, request)
			juryMu.Unlock()
			text := seatJSON
			if strings.Contains(system, "slide jury synthesizer") {
				text = mergedScoreboard
			}
			return text, nil
		}
	}

	// The simulated sidecar uses the app-local, test-only observation seam.
	// It runs synchronously only after slide_jury observes the deck stamped by
	// ship_compile, then lands BOTH pages in the callback's single metadata
	// write. This tests the real artifact seam without a scheduler-dependent
	// polling goroutine that can lose the intentionally short timeout under
	// -race.
	pages := make([]artifactAsset, 0, 2)
	for index, page := range [][]byte{[]byte("fake-jpeg-page-one"), []byte("fake-jpeg-page-two")} {
		ref, err := putBlob(page, "image/jpeg")
		if err != nil {
			t.Fatalf("store simulated rendered page %d: %v", index+1, err)
		}
		pages = append(pages, artifactAsset{
			Ref:  ref,
			Mime: "image/jpeg",
			Name: fmt.Sprintf("page-%02d.jpg", index+1),
			Kind: "page_image",
		})
	}
	var sidecarErr error
	app.slideJuryDeckObserved = func(deck meetingMemoryEntry) {
		if deck.Metadata["source"] != "packaging_studio_ship" ||
			deck.Metadata["artifactContract"] != packagingStudioDeckContract ||
			strings.TrimSpace(deck.Metadata["renderJobId"]) == "" {
			sidecarErr = fmt.Errorf("slide jury observed an unstamped/non-studio deck: %v", deck.Metadata)
			return
		}
		_, sidecarErr = app.replaceArtifactAssetsOfKind(deck.ID, "page_image", pages)
	}

	parentID, _, parks := driveStudioRunToShipApprovalWithSetup(t, app, "", wrapJuryResponder)
	if sidecarErr != nil {
		t.Fatalf("simulated render callback: %v", sidecarErr)
	}
	if len(parks) != 4 {
		t.Fatalf("goal parked %d times (%v), want the four touchpoints — the jury is never a checkpoint", len(parks), parks)
	}

	plan := waitForGoalStage(t, app, parentID, goalStateApproval)
	jurySt := plan.subtaskByID("slide_jury")
	if jurySt == nil || jurySt.Status != subtaskComplete {
		t.Fatalf("slide_jury did not complete: %+v", jurySt)
	}
	juryRecord := mustArtifact(t, app, jurySt.ArtifactID)
	if !strings.Contains(juryRecord.Text, "Merged scoreboard filed") {
		t.Fatalf("slide_jury record does not report the filed scoreboard:\n%s", juryRecord.Text)
	}
	juryArtifactID := strings.TrimSpace(juryRecord.Metadata["slideJuryArtifactId"])
	if juryArtifactID == "" {
		t.Fatalf("slide_jury record carries no slideJuryArtifactId: %v", juryRecord.Metadata)
	}

	// The scoreboard artifact: contract, provenance, synthesis + voices.
	jury := mustArtifact(t, app, juryArtifactID)
	if jury.Metadata["artifactContract"] != slideJuryContract || jury.Metadata["source"] != slideJurySource {
		t.Fatalf("jury artifact stamps wrong: %v", jury.Metadata)
	}
	if jury.Metadata["goalId"] != parentID {
		t.Fatalf("jury goalId=%q, want the running goal %s", jury.Metadata["goalId"], parentID)
	}
	if jury.Metadata["reviewVerdict"] != "needs_changes" || jury.Metadata["blockingPages"] != "1" {
		t.Fatalf("jury readiness=%v, want slide 1 blocked", jury.Metadata)
	}
	if plan.Checkpoint == nil || !strings.Contains(plan.Checkpoint.Question, "slide(s) 1") || strings.Contains(plan.Checkpoint.Question, "are ready") {
		t.Fatalf("ship checkpoint did not surface the rendered blocker truthfully: %+v", plan.Checkpoint)
	}
	if !strings.Contains(jury.Text, mergedScoreboard) || !strings.Contains(jury.Text, "## Jury voices") {
		t.Fatalf("jury artifact missing scoreboard/voices:\n%s", jury.Text)
	}

	// Every jury call — the 3 seats AND the synthesis — saw ALL page images.
	juryMu.Lock()
	requests := append([]openAITextRequest(nil), juryRequests...)
	juryMu.Unlock()
	if len(requests) != 4 {
		t.Fatalf("jury made %d model calls, want 4 (3 seats + synthesis)", len(requests))
	}
	for index, request := range requests {
		images := 0
		for _, content := range request.Attachments {
			if content.Type == "input_image" {
				images++
			}
		}
		if images != 2 {
			t.Fatalf("jury call %d carries %d image blocks, want ALL 2 rendered pages", index, images)
		}
	}

	// The findings record gained the revision-notes section — the merged
	// scoreboard, the pointer to the full jury artifact, and NO auto-revise:
	// WRITE and ship_deck spent zero revision rounds on the jury's account.
	filed := studioFiledDeliverables(t, app, parentID)
	findings := mustArtifact(t, app, filed[packagingStudioFindingsContract].ID)
	if !strings.Contains(findings.Text, "## Slide jury — revision notes") || !strings.Contains(findings.Text, mergedScoreboard) {
		t.Fatalf("findings record missing the jury revision notes:\n%s", findings.Text)
	}
	if !strings.Contains(findings.Text, juryArtifactID) {
		t.Fatalf("findings revision notes do not name the jury artifact %s", juryArtifactID)
	}
	if strings.Contains(findings.Text, `"pages":[{"page":1`) {
		t.Fatal("findings revision notes carry the per-seat transcript — only the merged scoreboard belongs there")
	}
	if findings.Metadata["slideJuryArtifactId"] != juryArtifactID {
		t.Fatalf("findings slideJuryArtifactId=%q, want %s", findings.Metadata["slideJuryArtifactId"], juryArtifactID)
	}
	for _, stageID := range []string{"write", "ship_deck"} {
		st := plan.subtaskByID(stageID)
		if st == nil || st.Revisions != 0 {
			t.Fatalf("stage %s revisions=%+v, want 0 — jury findings must never auto-revise", stageID, st)
		}
	}

	// The ship approval still closes the run.
	if err := app.resumeApprovedGoalWithChoice(parentID, "aj@shareability.com", "approve the ship"); err != nil {
		t.Fatalf("ship approval resume: %v", err)
	}
	waitForGoalStage(t, app, parentID, goalStateVerified)
}

// --- The founder send-back round: revise teeth through the REAL pipeline -----

// anthropicRequestText flattens a captured request's text blocks so a test can
// assert what a prompt actually carried.
func anthropicRequestText(request anthropicMessagesRequest) string {
	var builder strings.Builder
	for _, message := range request.Messages {
		for _, raw := range message.Content {
			var block struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(raw, &block); err == nil && block.Type == "text" {
				builder.WriteString(block.Text)
				builder.WriteByte('\n')
			}
		}
	}
	return builder.String()
}

// One founder send-back round through the REAL pipeline: "send back for
// changes" at the founder pass mechanically re-queues WRITE with the founder's
// words as revision notes and the do_not_touch line locked as protected, the
// checkpoint re-parks with the revised draft, and the run then ships through
// the ship approval to verified — the send-back label finally does what it
// says, without costing the goal.
func TestPackagingStudioFounderSendBackRequeuesWriteAndReparks(t *testing.T) {
	t.Skip("v2 founder checkpoint retired; v3 applies automatic bounded repair")
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{
		fallback:  "Objection: the plan assumes distribution it has not earned. strengths_to_keep: the founder's voice.",
		synthesis: "Synthesis: the panel verdict is on the record; the winner is franchise-playbook.\n[\"cultural-moment\", \"franchise-playbook\", \"founder-conviction\"]",
		stage:     "Deck copy, slide by slide, in a spoken register, quoting \"" + studioTestFounderPhrase + "\".",
	})
	// Capture the WRITE synthesizer's prompts (installFakeResponder's cleanup
	// restores the seam), so the redo prompt is on the record.
	var promptsMu sync.Mutex
	var writePrompts []string
	inner := createOpenAITextResponse
	createOpenAITextResponse = func(ctx context.Context, apiKey string, request openAITextRequest) (string, error) {
		if strings.Contains(strings.ToLower(request.Instructions), "process stage synthesizer") && strings.Contains(request.Instructions, `"Build the 10-slide story"`) {
			promptsMu.Lock()
			writePrompts = append(writePrompts, request.Input)
			promptsMu.Unlock()
		}
		return inner(ctx, apiKey, request)
	}
	children := installStudioChildRunner(t, map[string]string{
		"voice":     "Presenter script. Page 1 (30s): " + studioTestFounderPhrase + ". [BEAT] Close on the ask.",
		"ship_deck": studioTestDeckHTML(),
	})

	thread, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective:    "Package the venture. The founder says verbatim: \"" + studioTestFounderPhrase + "\".",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: packagingStudioProcessID,
	})
	if err != nil {
		t.Fatalf("launchGoalThread(packaging_studio): %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)

	resume := func(want string, choice string) {
		t.Helper()
		plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateApproval)
		if plan.Checkpoint == nil || plan.Checkpoint.StageID != want {
			t.Fatalf("parked at %+v, want the %s checkpoint", plan.Checkpoint, want)
		}
		if err := app.resumeApprovedGoalWithChoice(thread.Artifact.ID, "aj@shareability.com", choice); err != nil {
			t.Fatalf("resume %s with %q: %v", want, choice, err)
		}
	}
	resume("intake", "no brand assets — develop identity")
	resume("compete_choice", "franchise-playbook")

	// The taste pass says NO: one send-back round with notes + a do_not_touch mark.
	const protectLine = "do_not_touch: keep the closing ask exactly as first written"
	sendBack := "send back for changes — tighten slide 3 and cut the hedge words. " + protectLine
	resume("founder_pass", sendBack)

	// The checkpoint RE-PARKED after the redo, unresolved, one round spent.
	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateApproval)
	if plan.Checkpoint == nil || plan.Checkpoint.StageID != "founder_pass" || plan.Checkpoint.ResolvedAt != "" {
		t.Fatalf("founder_pass did not re-park after the send-back: %+v", plan.Checkpoint)
	}
	founderPass := plan.subtaskByID("founder_pass")
	if founderPass == nil || founderPass.Revisions != 1 {
		t.Fatalf("founder_pass did not spend a send-back round: %+v", founderPass)
	}
	// WRITE went back in flight with the founder's words as notes and the
	// do_not_touch line locked as protected — WITHOUT spending write's own
	// failure-retry budget (the send-back budget lives on the checkpoint).
	write := plan.subtaskByID("write")
	if write == nil || write.Status != subtaskComplete || write.Revisions != 0 {
		t.Fatalf("write was not re-queued and re-completed budget-free: %+v", write)
	}
	// Cascade invalidation: gate and voice depend on write, so the send-back
	// re-ran BOTH against the revised draft — the re-parked checkpoint presents
	// a re-gated draft and a fresh presenter script, never stale ones.
	for _, id := range []string{"gate", "voice"} {
		stage := plan.subtaskByID(id)
		if stage == nil || stage.Status != subtaskComplete {
			t.Fatalf("stage %s did not re-complete after the cascade reset: %+v", id, stage)
		}
	}
	// The inline gate re-scored and stamped a fresh pass record.
	if gate := plan.subtaskByID("gate"); gate.Review == nil || gate.Review.Verdict != goalReviewPass {
		t.Fatalf("gate did not re-review after the cascade reset: %+v", gate.Review)
	}
	// The voice writer re-dispatched: two voice launches (original + cascade redo).
	voiceLaunches := 0
	for _, child := range *children {
		if child.subtaskID == "voice" {
			voiceLaunches++
		}
	}
	if voiceLaunches != 2 {
		t.Fatalf("voice launched %d times, want 2 (original + cascade redo after the send-back)", voiceLaunches)
	}
	if !containsString(write.Protect, protectLine) {
		t.Fatalf("write protect list missing the do_not_touch line: %v", write.Protect)
	}
	promptsMu.Lock()
	prompts := append([]string{}, writePrompts...)
	promptsMu.Unlock()
	if len(prompts) != 2 {
		t.Fatalf("the WRITE synthesizer ran %d times, want 2 (original + one redo)", len(prompts))
	}
	if !strings.Contains(prompts[1], "Revision notes (address these): "+sendBack) {
		t.Fatalf("the redo prompt does not carry the founder's send-back notes:\n%s", prompts[1])
	}
	if !strings.Contains(prompts[1], "DO NOT LOSE (protected)") || !strings.Contains(prompts[1], protectLine) {
		t.Fatalf("the redo prompt does not lock the do_not_touch line:\n%s", prompts[1])
	}

	// Round two of the taste pass ships, and the run reaches verified through
	// the explicit ship approval — the send-back cost a round, never the goal.
	resume("founder_pass", "ship as-is — "+studioTestDoNotTouch)
	resume("ship_approval", "approve the ship")
	plan = waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)
	if plan.Verification.Verdict != goalReviewPass {
		t.Fatalf("verification verdict=%q, want pass after the send-back round", plan.Verification.Verdict)
	}
}

// --- small local helpers ----------------------------------------------------

func seatInPersonas(personas []ProcessPersona, name string) bool {
	for _, persona := range personas {
		if persona.Name == name {
			return true
		}
	}
	return false
}

// --- The MANIFEST: the handover card in the origin thread ---------------------

// studioManifestMessages filters a channel's committed messages down to the
// Kind:"manifest" records.
func studioManifestMessages(t *testing.T, app *kanbanBoardApp, channelID string) []scoutChatMessageRecord {
	t.Helper()
	saved, _, err := app.scoutChatThreadByID("aj@shareability.com", channelID)
	if err != nil {
		t.Fatalf("scoutChatThreadByID: %v", err)
	}
	var manifests []scoutChatMessageRecord
	for _, message := range saved.Messages {
		if message.Kind == scoutChatMessageKindManifest {
			manifests = append(manifests, message)
		}
	}
	return manifests
}

// A packaging_studio ship approval that PROCEEDS posts the manifest card into
// the origin thread: the five deliverables in send order with their badges and
// facts, the run's provenance (four decisions, the gate score, a wall clock),
// the disclosed skips (sidecar-absent: both pdf exports + the slide jury), the
// package attachment, the findings pointer — and the deliverables earn their
// share eligibility, so the card's share door points at the deck. The manifest
// is DATA persisted ON the message.
func TestPackagingStudioShipManifestPostsOnProceed(t *testing.T) {
	t.Skip("v2 approval manifest retired for reversible private deck creation")
	app := newIsolatedKanbanBoardApp(t)
	pkg, err := app.createVenturePackage("Station Tenn", "the country culture studio", "aj@shareability.com")
	if err != nil {
		t.Fatalf("createVenturePackage: %v", err)
	}
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("createScoutChatThread: %v", err)
	}

	parentID, _, _ := driveStudioRunToShipApprovalFull(t, app, pkg.ID, nil, map[string]string{
		"originKind":  agentThreadOriginPrivateThread,
		"originId":    channel.ID,
		"requestedBy": "aj@shareability.com",
	})
	// Parked at ship approval: NO manifest yet — the card is the resolution's.
	if manifests := studioManifestMessages(t, app, channel.ID); len(manifests) != 0 {
		t.Fatalf("manifest posted before the ship approval resolved: %+v", manifests)
	}

	if err := app.resumeApprovedGoalWithChoice(parentID, "aj@shareability.com", "approve the ship"); err != nil {
		t.Fatalf("ship approval resume: %v", err)
	}
	waitForGoalStage(t, app, parentID, goalStateVerified)

	manifests := studioManifestMessages(t, app, channel.ID)
	if len(manifests) != 1 {
		t.Fatalf("channel holds %d manifest messages, want exactly 1", len(manifests))
	}
	message := manifests[0]
	if message.Manifest == nil {
		t.Fatal("the manifest message carries no persisted manifest data")
	}
	manifest := *message.Manifest
	if manifest.Status != manifestStatusShipped || manifest.GoalID != parentID {
		t.Fatalf("manifest status/goal=%q/%q, want shipped/%q", manifest.Status, manifest.GoalID, parentID)
	}
	// The package name is canonicalized at creation ("Station Tenn" →
	// "StationTenn", the split-brand vocabulary correction), so the manifest
	// line carries the canonical brand.
	if !strings.Contains(message.Text, "manifest filed — five deliverables attached to StationTenn") {
		t.Fatalf("manifest message text=%q", message.Text)
	}

	// The five deliverables, send order, sheet badges: deck paper paper doc doc.
	filed := studioFiledDeliverables(t, app, parentID)
	parent := mustArtifact(t, app, parentID)
	if got := parent.Metadata["acceptedResultArtifactId"]; got != filed[packagingStudioDeckContract].ID {
		t.Errorf("acceptedResultArtifactId=%q, want approved deck %q", got, filed[packagingStudioDeckContract].ID)
	}
	var completedPlan goalPlan
	if err := json.Unmarshal([]byte(parent.Metadata["goalPlan"]), &completedPlan); err != nil {
		t.Fatalf("decode completed plan: %v", err)
	}
	if got := completedPlan.Report.AcceptedResultArtifactID; got != filed[packagingStudioDeckContract].ID {
		t.Errorf("plan accepted result=%q, want %q", got, filed[packagingStudioDeckContract].ID)
	}
	if len(manifest.Deliverables) != 5 {
		t.Fatalf("manifest carries %d deliverables, want 5: %+v", len(manifest.Deliverables), manifest.Deliverables)
	}
	wantBadges := []string{"deck", "paper", "paper", "doc", "doc"}
	for index, contract := range studioWantContracts {
		deliverable := manifest.Deliverables[index]
		if deliverable.ArtifactID != filed[contract].ID {
			t.Errorf("deliverable %d artifact=%q, want the filed %q (%s)", index, deliverable.ArtifactID, contract, filed[contract].ID)
		}
		if deliverable.Badge != wantBadges[index] {
			t.Errorf("deliverable %d badge=%q, want %q", index, deliverable.Badge, wantBadges[index])
		}
		if deliverable.Title == "" {
			t.Errorf("deliverable %d has no title", index)
		}
		if deliverable.Present != (index == 0) {
			t.Errorf("deliverable %d present=%v — present is deck-only", index, deliverable.Present)
		}
		// Sidecar-absent: no pdf ever landed, so no download action anywhere.
		if deliverable.PdfRef != "" {
			t.Errorf("deliverable %d carries pdfRef %q with no rendered pdf on file", index, deliverable.PdfRef)
		}
	}
	if manifest.Deliverables[0].Facts != "html · presenter mode" {
		t.Errorf("deck facts=%q, want \"html · presenter mode\"", manifest.Deliverables[0].Facts)
	}
	if manifest.Deliverables[2].Facts != "doc · text-native" {
		t.Errorf("The Talk facts=%q, want \"doc · text-native\" (no pdf this run)", manifest.Deliverables[2].Facts)
	}
	if manifest.FindingsArtifactID != filed[packagingStudioFindingsContract].ID {
		t.Errorf("findings pointer=%q, want %q", manifest.FindingsArtifactID, filed[packagingStudioFindingsContract].ID)
	}

	// Provenance: the four human decisions, the gate's real score, a wall clock.
	if manifest.Provenance.Decisions != 4 {
		t.Errorf("provenance decisions=%d, want 4", manifest.Provenance.Decisions)
	}
	if manifest.Provenance.GateScore <= 0 {
		t.Errorf("provenance gateScore=%v, want the gate subtask's real score", manifest.Provenance.GateScore)
	}
	if manifest.Provenance.WallClock == "" {
		t.Error("provenance wallClock is empty")
	}
	if manifest.AttachedTo != "StationTenn" || manifest.PackageID != pkg.ID {
		t.Errorf("attachment=%q/%q, want StationTenn/%s", manifest.AttachedTo, manifest.PackageID, pkg.ID)
	}

	// The disclosed skips: both pdf exports (sidecar absent) + the slide jury.
	pdfSkips, jurySkips := 0, 0
	for _, skip := range manifest.Skips {
		if strings.Contains(skip, "pdf export skipped") {
			pdfSkips++
		}
		if strings.Contains(skip, "slide jury skipped — ") {
			jurySkips++
		}
	}
	if pdfSkips != 2 || jurySkips != 1 {
		t.Errorf("skips=%v, want 2 pdf-export skips + 1 slide-jury skip", manifest.Skips)
	}

	// The ship approval IS the human approval of the deliverables: every one
	// carries the durable stamp, is share-eligible, and the manifest's share
	// door points at the deck.
	for _, contract := range studioWantContracts {
		fresh := mustArtifact(t, app, filed[contract].ID)
		if artifactStatus(fresh) != artifactStatusApproved {
			t.Errorf("%q status=%q after the ship approval, want approved", contract, artifactStatus(fresh))
		}
		if fresh.Metadata[artifactHumanApprovedAtKey] == "" {
			t.Errorf("%q missing the human-approval stamp", contract)
		}
		if !artifactShareEligible(fresh) {
			t.Errorf("%q is not share-eligible after the ship approval", contract)
		}
	}
	if manifest.ShareArtifactID != filed[packagingStudioDeckContract].ID {
		t.Errorf("shareArtifactId=%q, want the deck %q", manifest.ShareArtifactID, filed[packagingStudioDeckContract].ID)
	}
	if manifest.Subline != "Five interlocking deliverables, filed to the venture package." {
		// Skips are disclosed, so the subline names the degradation instead.
		if manifest.Subline != "five deliverables filed — degraded paths disclosed below." {
			t.Errorf("subline=%q", manifest.Subline)
		}
	}
}

// A HOLD posts the muted variant: status held with the holder on the record,
// the share door dark, the deliverables NOT approved — and an explicit proceed
// afterwards posts the shipped card, share now live.
func TestPackagingStudioShipManifestHeldVariant(t *testing.T) {
	t.Skip("v2 approval manifest retired; v3 quality gate holds before delivery")
	app := newIsolatedKanbanBoardApp(t)
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("createScoutChatThread: %v", err)
	}
	parentID, _, _ := driveStudioRunToShipApprovalFull(t, app, "", nil, map[string]string{
		"originKind":  agentThreadOriginPrivateThread,
		"originId":    channel.ID,
		"requestedBy": "aj@shareability.com",
	})

	if err := app.resumeApprovedGoalWithChoice(parentID, "aj@shareability.com", "hold the package"); err != nil {
		t.Fatalf("hold resume: %v", err)
	}
	manifests := studioManifestMessages(t, app, channel.ID)
	if len(manifests) != 1 {
		t.Fatalf("channel holds %d manifest messages after the hold, want 1", len(manifests))
	}
	held := manifests[0].Manifest
	if held == nil || held.Status != manifestStatusHeld {
		t.Fatalf("held manifest=%+v, want status held", held)
	}
	if held.HeldBy != "aj@shareability.com" {
		t.Errorf("heldBy=%q", held.HeldBy)
	}
	if held.ShareArtifactID != "" {
		t.Errorf("a held package's share links stay dark, got shareArtifactId=%q", held.ShareArtifactID)
	}
	if held.Subline != "Held before ship — artifacts stay filed, share links stay dark." {
		t.Errorf("held subline=%q", held.Subline)
	}
	if !strings.Contains(manifests[0].Text, "package held — release requires aj@shareability.com") {
		t.Errorf("held message text=%q", manifests[0].Text)
	}
	// Nothing left the office: no deliverable is approved or share-eligible.
	filed := studioFiledDeliverables(t, app, parentID)
	for contract, artifact := range filed {
		fresh := mustArtifact(t, app, artifact.ID)
		if artifactStatus(fresh) == artifactStatusApproved || artifactShareEligible(fresh) {
			t.Errorf("%q became shareable under a hold", contract)
		}
	}

	// The explicit proceed releases the hold and posts the shipped card.
	if err := app.resumeApprovedGoalWithChoice(parentID, "aj@shareability.com", "approve the ship"); err != nil {
		t.Fatalf("proceed after hold: %v", err)
	}
	waitForGoalStage(t, app, parentID, goalStateVerified)
	manifests = studioManifestMessages(t, app, channel.ID)
	if len(manifests) != 2 {
		t.Fatalf("channel holds %d manifest messages after the release, want 2 (held, then shipped)", len(manifests))
	}
	shipped := manifests[1].Manifest
	if shipped == nil || shipped.Status != manifestStatusShipped {
		t.Fatalf("release manifest=%+v, want status shipped", shipped)
	}
	deck := filed[packagingStudioDeckContract]
	if shipped.ShareArtifactID != deck.ID {
		t.Errorf("released shareArtifactId=%q, want the deck %q", shipped.ShareArtifactID, deck.ID)
	}
}

// The manifest belongs to the packaging_studio ship approval alone: a plan
// without that process (or a different stage) posts nothing.
func TestStudioShipManifestOnlyForPackagingStudio(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "growth", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("createScoutChatThread: %v", err)
	}
	parent := newGoalParentForReporter(t, app, map[string]string{
		"originKind": agentThreadOriginChannel,
		"originId":   channel.ID,
	})

	// A free-form goal (no process), and a studio plan resolving a NON-ship
	// stage: neither posts a manifest.
	app.recordStudioShipResolution(&goalPlan{}, parent.ID, "ship_approval", manifestStatusShipped, "aj", true)
	app.recordStudioShipResolution(&goalPlan{ProcessID: packagingStudioProcessID}, parent.ID, "founder_pass", manifestStatusShipped, "aj", true)
	if manifests := studioManifestMessages(t, app, channel.ID); len(manifests) != 0 {
		t.Fatalf("non-studio resolutions posted %d manifests, want 0", len(manifests))
	}
}

// Wave 6 (deep 1:1 linkage): re-filing the ship deliverables for the SAME goal
// — a feedback-driven re-open re-running ship_compile — versions the five
// EXISTING artifacts in place instead of filing five strangers, so every chat
// ref, drawer row, and package link keeps pointing at the living deliverable.
func TestFileStudioShipDeliverablesVersionsInPlaceOnReShip(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	inputs := studioShipInputs{
		GoalID:    "os-artifact-workflow-reship-probe",
		CreatedBy: "AJ",
		DeckHTML:  strings.Replace(studioTestDeckHTML(), "Slide 2 — Close", "Slide 2 — v1 deck", 1),
		Wall:      "wall v1",
		Talk:      "talk v1",
		Rigor:     "rigor v1",
		Findings:  "findings v1",
		DeckTitle: "Re-ship probe deck",
	}
	first, err := app.fileStudioShipDeliverables(inputs)
	if err != nil {
		t.Fatalf("first ship: %v", err)
	}
	if len(first) != 5 {
		t.Fatalf("first ship filed %d deliverables, want 5", len(first))
	}
	firstIDs := map[string]string{}
	for _, deliverable := range first {
		firstIDs[deliverable.Contract] = deliverable.ArtifactID
	}
	firstDeck, ok := app.osArtifactByID(firstIDs[packagingStudioDeckContract])
	if !ok || !validBlobRef(firstDeck.Metadata[deckSceneRefMetadataKey]) {
		t.Fatalf("first shipped deck has no native scene: %+v", firstDeck.Metadata)
	}
	firstSceneRef := firstDeck.Metadata[deckSceneRefMetadataKey]
	firstVersion := artifactVersion(firstDeck)

	inputs.DeckHTML = strings.Replace(studioTestDeckHTML(), "Slide 2 — Close", "Slide 2 — v2 deck — revised close", 1)
	inputs.Wall = "wall v2"
	inputs.Talk = "talk v2"
	inputs.Rigor = "rigor v2"
	inputs.Findings = "findings v2"
	second, err := app.fileStudioShipDeliverables(inputs)
	if err != nil {
		t.Fatalf("re-ship: %v", err)
	}
	if len(second) != 5 {
		t.Fatalf("re-ship filed %d deliverables, want 5", len(second))
	}
	for _, deliverable := range second {
		if firstIDs[deliverable.Contract] != deliverable.ArtifactID {
			t.Fatalf("contract %q re-filed as %q, want the original artifact %q versioned in place", deliverable.Contract, deliverable.ArtifactID, firstIDs[deliverable.Contract])
		}
		stored, ok := app.osArtifactByID(deliverable.ArtifactID)
		if !ok {
			t.Fatalf("re-shipped artifact %q missing", deliverable.ArtifactID)
		}
		if !strings.Contains(stored.Text, "v2") {
			t.Fatalf("contract %q body=%q, want the revised v2 body", deliverable.Contract, stored.Text)
		}
	}
	secondDeck, ok := app.osArtifactByID(firstIDs[packagingStudioDeckContract])
	if !ok || secondDeck.Metadata[deckSceneRefMetadataKey] == firstSceneRef || artifactVersion(secondDeck) != firstVersion+1 {
		t.Fatalf("re-ship did not atomically version body+scene: first=%d/%s second=%d/%s", firstVersion, firstSceneRef, artifactVersion(secondDeck), secondDeck.Metadata[deckSceneRefMetadataKey])
	}
	loaded, imported, quality, err := loadDeckDocument(secondDeck)
	if err != nil || imported || quality != "native" || len(loaded.Slides) != 2 {
		t.Fatalf("re-shipped native scene imported=%v quality=%q slides=%d err=%v", imported, quality, len(loaded.Slides), err)
	}

	// A goal-less studio run (no re-open path) still files fresh artifacts.
	inputs.GoalID = ""
	third, err := app.fileStudioShipDeliverables(inputs)
	if err != nil {
		t.Fatalf("goal-less ship: %v", err)
	}
	for _, deliverable := range third {
		if firstIDs[deliverable.Contract] == deliverable.ArtifactID {
			t.Fatalf("goal-less ship reused %q — dedupe must key on the goal", deliverable.ArtifactID)
		}
	}
}

func TestFileStudioShipDeliverablesRejectsUnfaithfulDeckBeforeFiling(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	before := len(app.osArtifactsSnapshot(0))
	_, err := app.fileStudioShipDeliverables(studioShipInputs{
		GoalID: "unfaithful-goal", CreatedBy: "AJ", DeckOnly: true,
		DeckHTML: "<!doctype html><html><body><section class=\"pg\"><h1>Unannotated deck</h1></section></body></html>",
	})
	if err == nil || !strings.Contains(err.Error(), "faithful native-importable scene") {
		t.Fatalf("unfaithful deck error=%v", err)
	}
	if after := len(app.osArtifactsSnapshot(0)); after != before {
		t.Fatalf("unfaithful deck filed a partial artifact: before=%d after=%d", before, after)
	}
}

func TestFileStudioShipDeliverablesPreservesAcceptedDeckAndVersionsRetryCandidate(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	parent, _, err := app.createOSArtifactWithMetadata("workflow", "Accepted deck retry", "goal", "AJ", map[string]string{
		"mode": "goal",
	})
	if err != nil {
		t.Fatal(err)
	}
	inputs := studioShipInputs{
		GoalID: parent.ID, CreatedBy: "AJ", DeckTitle: "Accepted deck retry",
		DeckHTML: strings.Replace(studioTestDeckHTML(), "Slide 2 — Close", "Slide 2 — approved deck", 1),
		Wall:     "wall v1", Talk: "talk v1", Rigor: "rigor v1", Findings: "findings v1",
	}
	first, err := app.fileStudioShipDeliverables(inputs)
	if err != nil {
		t.Fatalf("first ship: %v", err)
	}
	firstByContract := map[string]string{}
	for _, deliverable := range first {
		firstByContract[deliverable.Contract] = deliverable.ArtifactID
	}
	acceptedID := firstByContract[packagingStudioDeckContract]
	if _, _, err := app.updateOSArtifactWithMetadata(parent.ID, "", parent.Text, "AJ", map[string]string{
		"acceptedResultArtifactId": acceptedID,
	}); err != nil {
		t.Fatalf("stamp accepted deck: %v", err)
	}

	inputs.DeckHTML = strings.Replace(studioTestDeckHTML(), "Slide 2 — Close", "Slide 2 — retry candidate v2", 1)
	inputs.Wall, inputs.Talk, inputs.Rigor, inputs.Findings = "wall v2", "talk v2", "rigor v2", "findings v2"
	second, err := app.fileStudioShipDeliverables(inputs)
	if err != nil {
		t.Fatalf("retry ship compile: %v", err)
	}
	secondByContract := map[string]string{}
	for _, deliverable := range second {
		secondByContract[deliverable.Contract] = deliverable.ArtifactID
	}
	if secondByContract[packagingStudioDeckContract] == acceptedID {
		t.Fatal("retry overwrote the human-approved deck instead of filing a candidate")
	}
	for _, contract := range []string{packagingStudioWallContract, packagingStudioTalkContract, packagingStudioRigorContract, packagingStudioFindingsContract} {
		if secondByContract[contract] != firstByContract[contract] {
			t.Fatalf("supporting contract %q changed id from %q to %q", contract, firstByContract[contract], secondByContract[contract])
		}
	}
	approved, _ := app.osArtifactByID(acceptedID)
	if !strings.Contains(approved.Text, "approved deck") {
		t.Fatalf("approved artifact bytes changed: %q", approved.Text)
	}

	inputs.DeckHTML = strings.Replace(studioTestDeckHTML(), "Slide 2 — Close", "Slide 2 — retry candidate v3", 1)
	third, err := app.fileStudioShipDeliverables(inputs)
	if err != nil {
		t.Fatalf("candidate recompile: %v", err)
	}
	thirdByContract := map[string]string{}
	for _, deliverable := range third {
		thirdByContract[deliverable.Contract] = deliverable.ArtifactID
	}
	if thirdByContract[packagingStudioDeckContract] != secondByContract[packagingStudioDeckContract] {
		t.Fatalf("unapproved candidate changed id from %q to %q", secondByContract[packagingStudioDeckContract], thirdByContract[packagingStudioDeckContract])
	}
	candidate, _ := app.osArtifactByID(thirdByContract[packagingStudioDeckContract])
	if !strings.Contains(candidate.Text, "retry candidate v3") {
		t.Fatalf("candidate was not versioned in place: %q", candidate.Text)
	}
}
