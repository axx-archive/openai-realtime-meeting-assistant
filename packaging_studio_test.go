package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
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
	approval := packagingStudioStage(t, def, "ship_approval")
	if len(approval.InputFrom) != 1 || approval.InputFrom[0] != "ship_compile" {
		t.Fatalf("ship_approval inputFrom=%v, want [ship_compile] — the approval reads the compile record", approval.InputFrom)
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
	if !containsString(intake.CheckpointSpec.Options, "no brand assets — develop identity") {
		t.Fatalf("intake options=%v, missing the develop-identity branch IDENTITY reads", intake.CheckpointSpec.Options)
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

	// The ship approval still lands the run at verified.
	if err := app.resumeApprovedGoalWithChoice(parentID, "aj@shareability.com", "approve the ship"); err != nil {
		t.Fatalf("ship approval resume: %v", err)
	}
	waitForGoalStage(t, app, parentID, goalStateVerified)
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
