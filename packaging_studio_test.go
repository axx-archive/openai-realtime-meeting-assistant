package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	plan := &goalPlan{PlanVersion: goalPlanVersion, ProcessID: def.ID, Authority: codexJobAuthorityWorkspaceWrite, State: goalStateDecompose}
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

func TestPackagingStudioStageWiring(t *testing.T) {
	def := packagingStudioDefinition()

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

// The IDENTITY stage is always present (the runtime does not skip stages), and
// its authored prompt carries BOTH branches: develop a competition when INTAKE
// declares no brand assets, and disclose a skip when assets exist. It reads the
// INTAKE choice to pick the branch.
func TestPackagingStudioIdentityConditionalBothBranches(t *testing.T) {
	def := packagingStudioDefinition()
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
	def := packagingStudioDefinition()

	intake := packagingStudioStage(t, def, "intake")
	if intake.CheckpointSpec == nil || len(intake.CheckpointSpec.Options) != 2 {
		t.Fatalf("intake checkpoint options=%+v, want the two brand-assets branches", intake.CheckpointSpec)
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
	if !containsString(choice.InputFrom, "compete_judges") {
		t.Fatalf("compete_choice inputFrom=%v, must read the judges' verdict", choice.InputFrom)
	}

	founder := packagingStudioStage(t, def, "founder_pass")
	if founder.CheckpointSpec == nil || strings.TrimSpace(founder.CheckpointSpec.Question) == "" {
		t.Fatal("founder_pass has no checkpoint question")
	}
	if !strings.Contains(strings.ToLower(founder.CheckpointSpec.Question), "do_not_touch") {
		t.Fatalf("founder_pass question must offer the do_not_touch mark: %q", founder.CheckpointSpec.Question)
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

// The founder's verbatim words are LAW downstream: the gate's authored prompt
// instructs quoting them, and the runtime assembly (processStageTask) carries
// the goal objective — which holds the founder's words — into the gate scorer's
// prompt, so a gate is never scored blind to what the founder actually said.
func TestPackagingStudioFounderWordsReachGate(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	def := packagingStudioDefinition()

	gate := packagingStudioStage(t, def, "gate")
	if !strings.Contains(gate.PromptBody, "founder") {
		t.Fatalf("gate prompt does not make the founder's words law:\n%s", gate.PromptBody)
	}

	const founderPhrase = "we are the last honest voice in this category"
	plan := &goalPlan{
		PlanVersion: goalPlanVersion,
		ProcessID:   def.ID,
		Objective:   "Package the venture. The founder says verbatim: \"" + founderPhrase + "\".",
		Authority:   codexJobAuthorityWorkspaceWrite,
		State:       goalStateDecompose,
	}
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

// installStudioChildRunner is installFakeChildRunner with per-subtask bodies,
// so the voice/ship_deck writers produce the material the compile stage reads
// (a real HTML deck, a real presenter script) instead of a generic echo.
func installStudioChildRunner(t *testing.T, outputs map[string]string) *[]capturedChild {
	t.Helper()
	var mu sync.Mutex
	launched := &[]capturedChild{}

	original := startAgentThreadAsync
	t.Cleanup(func() { startAgentThreadAsync = original })
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
		go func() {
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
	return driveStudioRunToShipApprovalWithSetup(t, app, packageID, nil)
}

// driveStudioRunToShipApprovalWithSetup is the same drive with a hook that
// runs AFTER the fake responder is installed — the slide-jury test uses it to
// wrap createAnthropicMessagesResponse so jury-shaped system prompts answer
// with jury JSON while every studio route keeps flowing to the routes fake.
func driveStudioRunToShipApprovalWithSetup(t *testing.T, app *kanbanBoardApp, packageID string, afterResponder func()) (string, *[]capturedChild, []string) {
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
		"ship_deck": "<!doctype html><html><head><style>body{color:#111}</style></head><body><section>Slide 1 — " + studioTestFounderPhrase + "</section></body></html>",
	})

	thread, err := app.launchGoalThread(goalLaunchSpec{
		Objective:    "Package the venture. The founder says verbatim: \"" + studioTestFounderPhrase + "\".",
		CreatedBy:    "aj@shareability.com",
		PackageID:    packageID,
		ToolTemplate: packagingStudioProcessID,
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
	app := newIsolatedKanbanBoardApp(t)
	// A live render sidecar: a fresh heartbeat on the shared volume makes
	// renderSidecarAvailable() true, so the export jobs enqueue into the fake
	// file-per-job queue in the temp data dir.
	if err := writeRenderRunnerHeartbeat("test-render-runner"); err != nil {
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
	app := newIsolatedKanbanBoardApp(t)
	if err := writeRenderRunnerHeartbeat("test-render-runner"); err != nil {
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
	var juryRequests []anthropicMessagesRequest
	wrapJuryResponder := func() {
		prior := createAnthropicMessagesResponse
		createAnthropicMessagesResponse = func(ctx context.Context, apiKey string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
			system := strings.ToLower(request.System)
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
			return anthropicMessagesResponse{StopReason: "end_turn", Content: []json.RawMessage{mockAnthropicTextBlock(text)}}, nil
		}
	}

	// The simulated sidecar: the moment ship_compile files the deck and stamps
	// its render job, the page JPEGs land as {kind: image} assets — exactly
	// what the real callback does via persistRenderPageImageAssets.
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			for _, artifact := range app.osArtifactsSnapshot(0) {
				if artifact.Metadata["source"] != "packaging_studio_ship" ||
					artifact.Metadata["artifactContract"] != packagingStudioDeckContract ||
					strings.TrimSpace(artifact.Metadata["renderJobId"]) == "" {
					continue
				}
				for index, page := range [][]byte{[]byte("fake-jpeg-page-one"), []byte("fake-jpeg-page-two")} {
					ref, err := putBlob(page, "image/jpeg")
					if err != nil {
						return
					}
					_, _ = app.appendArtifactAsset(artifact.ID, artifactAsset{
						Ref:  ref,
						Mime: "image/jpeg",
						Name: fmt.Sprintf("page-%02d.jpg", index+1),
						Kind: "image",
					})
				}
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	parentID, _, parks := driveStudioRunToShipApprovalWithSetup(t, app, "", wrapJuryResponder)
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
	if !strings.Contains(jury.Text, mergedScoreboard) || !strings.Contains(jury.Text, "## Jury voices") {
		t.Fatalf("jury artifact missing scoreboard/voices:\n%s", jury.Text)
	}

	// Every jury call — the 3 seats AND the synthesis — saw ALL page images.
	juryMu.Lock()
	requests := append([]anthropicMessagesRequest(nil), juryRequests...)
	juryMu.Unlock()
	if len(requests) != 4 {
		t.Fatalf("jury made %d model calls, want 4 (3 seats + synthesis)", len(requests))
	}
	for index, request := range requests {
		images := 0
		for _, raw := range request.Messages[0].Content {
			if decodeAnthropicBlock(raw).Type == "image" {
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
	inner := createAnthropicMessagesResponse
	createAnthropicMessagesResponse = func(ctx context.Context, apiKey string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		if strings.Contains(strings.ToLower(request.System), "process stage synthesizer") {
			promptsMu.Lock()
			writePrompts = append(writePrompts, anthropicRequestText(request))
			promptsMu.Unlock()
		}
		return inner(ctx, apiKey, request)
	}
	children := installStudioChildRunner(t, map[string]string{
		"voice":     "Presenter script. Page 1 (30s): " + studioTestFounderPhrase + ". [BEAT] Close on the ask.",
		"ship_deck": "<!doctype html><html><head><style>body{color:#111}</style></head><body><section>Slide 1 — " + studioTestFounderPhrase + "</section></body></html>",
	})

	thread, err := app.launchGoalThread(goalLaunchSpec{
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
