package main

// process_definitions.go — the ProcessDefinition runtime types + registry
// (packaging OS §3 "The abstraction", Wave 4 item 17). A process is an
// AUTHORED, versioned pipeline: ordered stages whose roles map onto the goal
// engine's existing machinery (writer subtasks, runGoalPanel, runGoalGate, the
// render-runner enqueue, the approval park). Definitions are Go structs beside
// tool_registry.go — versioned in git, validated at registration, tested like
// data. This is deliberately NOT a workflow DSL: the moat is 5-6 opinionated
// pipelines, not a platform ("What we are explicitly NOT doing").
//
// Processes serve through the same GET /assistant/tools payload as a fifth
// group ("processes"), so the palette, the /goal door, voice, and the router's
// propose_tool_run all reach a process by id exactly the way they reach a
// tool: POST /assistant/goal with toolTemplate=<processId>. The engine-side
// execution (instantiation replacing free-form decompose, inline stage steps,
// checkpoint park/resume, budget overrides) lives in goal_engine.go.

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Stage roles. writer runs as a real child agent thread (the deliverable
// path); everything else executes INLINE as one engine step so the DAG stays
// coarse and the subtask budget stays sane (spec §3 panel primitive).
const (
	processRoleWriter          = "writer"
	processRolePanel           = "panel"
	processRoleJudges          = "judges"
	processRoleSynthesizer     = "synthesizer"
	processRoleGate            = "gate"
	processRoleRender          = "render"
	processRoleCompile         = "compile"
	processRoleHumanCheckpoint = "human_checkpoint"
)

var processStageRoles = map[string]bool{
	processRoleWriter:          true,
	processRolePanel:           true,
	processRoleJudges:          true,
	processRoleSynthesizer:     true,
	processRoleGate:            true,
	processRoleRender:          true,
	processRoleCompile:         true,
	processRoleHumanCheckpoint: true,
}

// processStageRoleIsInline reports whether a stage executes inside the engine
// step (never as a dispatched child thread). Only writer stages dispatch.
func processStageRoleIsInline(role string) bool {
	return processStageRoles[role] && role != processRoleWriter
}

// ProcessBudgets is the per-process execution envelope. Every field is
// optional; zero means "the engine default" (goalMaxSubtasks, the orchestrator
// token ceiling, the orchestrator wall-clock timeout).
type ProcessBudgets struct {
	MaxSubtasks int           `json:"maxSubtasks,omitempty"`
	MaxTokens   int           `json:"maxTokens,omitempty"`
	WallClock   time.Duration `json:"wallClock,omitempty"`
}

// ProcessPersona is one seat on a panel/judges stage: a name the synthesis can
// address and the persona's own system prompt (the goalPanelPersona shape).
type ProcessPersona struct {
	Name   string `json:"name"`
	System string `json:"system"`
}

// ProcessGateSpec is the runGoalGate spec shape, authored per stage: threshold
// + per-dimension floor + bounded rounds + force-accept-with-disclosed-gaps.
// Zero values fall back to the gate primitive's defaults (9.0 / 7.0 / 2).
type ProcessGateSpec struct {
	Threshold   float64 `json:"threshold,omitempty"`
	Floor       float64 `json:"floor,omitempty"`
	MaxRounds   int     `json:"maxRounds,omitempty"`
	ForceAccept bool    `json:"forceAccept,omitempty"`
}

// ProcessCompileFunc is a compile stage's assembler: authored, deterministic
// Go (never a model call) that reads the run's stage artifacts off the plan
// and files the process's interlocking deliverables — the packaging_studio
// SHIP compiler is the flagship instance. It returns the compile record body
// (the stage artifact: every filed id and every disclosed skip on the record)
// plus extra metadata to stamp on that record. An error fails the stage
// honestly through the normal review/requeue path.
type ProcessCompileFunc func(app *kanbanBoardApp, plan *goalPlan, parentID string, stage ProcessStage) (string, map[string]string, error)

// Checkpoint option actions — the mechanical teeth behind a negative choice
// (the disclosed gap from Wave 4's gate). proceed resolves the checkpoint and
// the pipeline continues (the default, and the only action an OptionsFrom
// option carries); revise re-queues the option's Target stage with the choice
// text as revision notes, bounded by the same MaxRounds discipline as gates;
// hold keeps the goal parked with the choice on the record until a subsequent
// proceed-action choice resumes it.
const (
	processCheckpointActionProceed = "proceed"
	processCheckpointActionRevise  = "revise"
	processCheckpointActionHold    = "hold"
)

// ProcessCheckpointOption is one authored choice on a human_checkpoint. Label
// is what the human taps (and what a prefix-matched choice must start with);
// Action is what the tap mechanically DOES (empty means proceed); Target is
// the revise action's re-queue target and must name one of the checkpoint
// stage's own InputFrom stages — a send-back always lands on work the human
// was actually shown.
type ProcessCheckpointOption struct {
	Label  string `json:"label"`
	Action string `json:"action,omitempty"`
	Target string `json:"target,omitempty"`
}

// processCheckpointOptionAction resolves an option's effective action: empty
// (and anything unknown, which validation refuses at registration) is proceed.
func processCheckpointOptionAction(option ProcessCheckpointOption) string {
	switch strings.TrimSpace(option.Action) {
	case processCheckpointActionRevise:
		return processCheckpointActionRevise
	case processCheckpointActionHold:
		return processCheckpointActionHold
	}
	return processCheckpointActionProceed
}

// ProcessCheckpointSpec declares what choice the human is being asked to make
// at a human_checkpoint stage. Options are static (authored, each carrying its
// mechanical action) or read from an earlier stage's output (OptionsFrom names
// the stage whose artifact carries a JSON array of option strings — the
// COMPETE-verdict pattern; extracted options always proceed). Both empty means
// a free-form approval.
type ProcessCheckpointSpec struct {
	Question    string                    `json:"question"`
	Options     []ProcessCheckpointOption `json:"options,omitempty"`
	OptionsFrom string                    `json:"optionsFrom,omitempty"`
}

// ProcessStage is one authored, ordered stage. InputFrom may reference only
// EARLIER stage ids — instantiation maps it 1:1 onto subtask dependsOn, so the
// existing topological executor runs the pipeline unchanged.
type ProcessStage struct {
	ID             string                 `json:"id"`
	Title          string                 `json:"title"`
	Role           string                 `json:"role"`
	Mode           string                 `json:"mode,omitempty"` // writer stages: agent-thread mode (default artifacts)
	Personas       []ProcessPersona       `json:"personas,omitempty"`
	PromptBody     string                 `json:"promptBody,omitempty"`
	InputFrom      []string               `json:"inputFrom,omitempty"`
	OutputContract string                 `json:"outputContract,omitempty"`
	GateSpec       *ProcessGateSpec       `json:"gateSpec,omitempty"`
	CheckpointSpec *ProcessCheckpointSpec `json:"checkpointSpec,omitempty"`
	// Compile is the compile role's authored assembler (required for that
	// role, refused elsewhere by validation). It is code, not data — never
	// serialized; a restart re-resolves it from the registered definition.
	Compile ProcessCompileFunc `json:"-"`
}

// ProcessDefinition is one versioned, authored pipeline.
type ProcessDefinition struct {
	ID          string `json:"id"`
	Version     int    `json:"version"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Group       string `json:"group,omitempty"`
	Authority   string `json:"authority,omitempty"`
	// Hidden keeps a process launchable by id (tests, internal proofs) while
	// leaving it OFF the public /assistant/tools payload and the router enum.
	Hidden  bool           `json:"hidden,omitempty"`
	Budgets ProcessBudgets `json:"budgets,omitempty"`
	Stages  []ProcessStage `json:"stages"`
}

func (def ProcessDefinition) stageByID(id string) (ProcessStage, bool) {
	id = strings.TrimSpace(id)
	for _, stage := range def.Stages {
		if stage.ID == id {
			return stage, true
		}
	}
	return ProcessStage{}, false
}

// processStageLawSweep is the deterministic, zero-model-cost pre-check a
// process stage's artifact must survive before any reviewer tokens are spent
// (the process twin of toolLawSweep). Checks key on the stage's declared
// output contract; the first entry exists because the first live packaging
// run completed ship_deck with a markdown DESCRIPTION of the deck — a
// deliverable whose contract demands the artifact itself must BE it.
func processStageLawSweep(stage ProcessStage, body string) (string, bool) {
	contract := strings.TrimSpace(stage.OutputContract)
	trimmed := strings.TrimSpace(body)
	switch contract {
	case "packaging_deck_v1":
		lowered := strings.ToLower(trimmed)
		if !strings.HasPrefix(lowered, "<!doctype html") {
			return "LAW SWEEP (packaging_deck_v1): the deliverable must be the deck ITSELF — one self-contained HTML document starting with <!doctype html> — not a plan, outline, or description of it. Emit the full HTML file.", true
		}
		if !strings.Contains(lowered, "</html>") {
			return "LAW SWEEP (packaging_deck_v1): the HTML document is truncated (no closing </html>). Emit the complete self-contained file.", true
		}
	}
	return "", false
}

// processMaxSubtasks is the effective subtask ceiling for a process plan: the
// authored budget when set, else the engine default. This is the ONE place the
// Budgets.MaxSubtasks override is interpreted.
func processMaxSubtasks(def ProcessDefinition) int {
	if def.Budgets.MaxSubtasks > 0 {
		return def.Budgets.MaxSubtasks
	}
	return goalMaxSubtasks
}

// processStageThreadMode resolves the agent-thread mode a stage's subtask
// carries. Writer stages default to artifacts (the deliverable mode); inline
// stages ride workflow — the mode only has to satisfy plan validation, since
// inline stages never dispatch a child thread.
func processStageThreadMode(stage ProcessStage) string {
	if mode := strings.TrimSpace(stage.Mode); mode != "" {
		return mode
	}
	if stage.Role == processRoleWriter {
		return "artifacts"
	}
	return "workflow"
}

// --- Validation ----------------------------------------------------------------

// validateProcessDefinition enforces the authoring invariants at registration
// time, so a bad definition can never reach the engine: canonical lowercase id,
// version >= 1, unique non-empty stage ids, known roles, role-specific
// requirements (writer mode, panel personas, gate/render inputs, compile
// inputs + assembler, checkpoint question), InputFrom referencing only
// EARLIER stages (which also guarantees
// the instantiated plan is acyclic), and a stage count within the budget.
func validateProcessDefinition(def ProcessDefinition) error {
	id := strings.TrimSpace(def.ID)
	if id == "" {
		return fmt.Errorf("process has no id")
	}
	if id != strings.ToLower(id) {
		return fmt.Errorf("process id %q must be lowercase (the registry lookup is case-normalized)", id)
	}
	if def.Version < 1 {
		return fmt.Errorf("process %q version must be >= 1", id)
	}
	if strings.TrimSpace(def.Title) == "" {
		return fmt.Errorf("process %q has no title", id)
	}
	if len(def.Stages) == 0 {
		return fmt.Errorf("process %q has no stages", id)
	}
	if limit := processMaxSubtasks(def); len(def.Stages) > limit {
		return fmt.Errorf("process %q has %d stages, budget allows %d — raise Budgets.MaxSubtasks or coarsen the pipeline", id, len(def.Stages), limit)
	}
	earlier := make(map[string]bool, len(def.Stages))
	for index, stage := range def.Stages {
		stageID := strings.TrimSpace(stage.ID)
		if stageID == "" {
			return fmt.Errorf("process %q stage %d has no id", id, index)
		}
		if earlier[stageID] {
			return fmt.Errorf("process %q has duplicate stage id %q", id, stageID)
		}
		if strings.TrimSpace(stage.Title) == "" {
			return fmt.Errorf("process %q stage %q has no title", id, stageID)
		}
		if !processStageRoles[stage.Role] {
			return fmt.Errorf("process %q stage %q has unknown role %q", id, stageID, stage.Role)
		}
		for _, from := range stage.InputFrom {
			from = strings.TrimSpace(from)
			if !earlier[from] {
				return fmt.Errorf("process %q stage %q inputFrom %q does not name an earlier stage", id, stageID, from)
			}
		}
		switch stage.Role {
		case processRoleWriter:
			if normalizeAgentThreadMode(processStageThreadMode(stage)) == "" {
				return fmt.Errorf("process %q writer stage %q has invalid mode %q", id, stageID, stage.Mode)
			}
		case processRolePanel, processRoleJudges:
			if len(stage.Personas) == 0 {
				return fmt.Errorf("process %q %s stage %q has no personas", id, stage.Role, stageID)
			}
			for _, persona := range stage.Personas {
				if strings.TrimSpace(persona.Name) == "" || strings.TrimSpace(persona.System) == "" {
					return fmt.Errorf("process %q stage %q has a persona missing name/system", id, stageID)
				}
			}
		case processRoleGate:
			if len(stage.InputFrom) == 0 {
				return fmt.Errorf("process %q gate stage %q has no inputFrom — a gate must name the work it scores", id, stageID)
			}
		case processRoleRender:
			if len(stage.InputFrom) == 0 {
				return fmt.Errorf("process %q render stage %q has no inputFrom — a render must name the artifact it exports", id, stageID)
			}
		case processRoleCompile:
			if len(stage.InputFrom) == 0 {
				return fmt.Errorf("process %q compile stage %q has no inputFrom — a compile must name the stages it assembles", id, stageID)
			}
			if stage.Compile == nil {
				return fmt.Errorf("process %q compile stage %q has no Compile function — the compiler is authored Go, not a model call", id, stageID)
			}
		case processRoleHumanCheckpoint:
			if stage.CheckpointSpec == nil || strings.TrimSpace(stage.CheckpointSpec.Question) == "" {
				return fmt.Errorf("process %q checkpoint stage %q has no question — the human must know what they are choosing", id, stageID)
			}
			if from := strings.TrimSpace(stage.CheckpointSpec.OptionsFrom); from != "" && !earlier[from] {
				return fmt.Errorf("process %q checkpoint stage %q optionsFrom %q does not name an earlier stage", id, stageID, from)
			}
			for _, option := range stage.CheckpointSpec.Options {
				if strings.TrimSpace(option.Label) == "" {
					return fmt.Errorf("process %q checkpoint stage %q has an option with no label", id, stageID)
				}
				switch action := strings.TrimSpace(option.Action); action {
				case "", processCheckpointActionProceed, processCheckpointActionHold:
					if strings.TrimSpace(option.Target) != "" {
						return fmt.Errorf("process %q checkpoint stage %q option %q carries a target without the revise action", id, stageID, option.Label)
					}
				case processCheckpointActionRevise:
					target := strings.TrimSpace(option.Target)
					if target == "" {
						return fmt.Errorf("process %q checkpoint stage %q revise option %q has no target stage to re-queue", id, stageID, option.Label)
					}
					targetShown := false
					for _, from := range stage.InputFrom {
						if strings.TrimSpace(from) == target {
							targetShown = true
							break
						}
					}
					if !targetShown {
						return fmt.Errorf("process %q checkpoint stage %q revise option %q targets %q, which is not one of the stage's inputFrom — a send-back must land on work the human was shown", id, stageID, option.Label, target)
					}
				default:
					return fmt.Errorf("process %q checkpoint stage %q option %q has unknown action %q", id, stageID, option.Label, action)
				}
			}
		}
		earlier[stageID] = true
	}
	return nil
}

// --- Registry --------------------------------------------------------------------

// processRegistryMu guards the additive registration seam: packaging_studio
// (and future authored processes) register from init() in their own files, so
// the built-in list stays here and never needs editing to add a process.
var (
	processRegistryMu            sync.Mutex
	registeredProcessDefinitions []ProcessDefinition
)

// registerProcessDefinition adds an authored process. Invalid or duplicate
// definitions are refused — a pipeline that cannot instantiate must never be
// proposable.
func registerProcessDefinition(def ProcessDefinition) error {
	if err := validateProcessDefinition(def); err != nil {
		return err
	}
	processRegistryMu.Lock()
	defer processRegistryMu.Unlock()
	for _, existing := range builtinProcessDefinitions() {
		if existing.ID == def.ID {
			return fmt.Errorf("process id %q is already registered", def.ID)
		}
	}
	for _, existing := range registeredProcessDefinitions {
		if existing.ID == def.ID {
			return fmt.Errorf("process id %q is already registered", def.ID)
		}
	}
	registeredProcessDefinitions = append(registeredProcessDefinitions, def)
	return nil
}

// builtinProcessDefinitions returns the authored processes served alongside the
// 12 tools. Constructed fresh each call (the packagingTools pattern) so no
// caller can mutate the shared definitions — and so packaging_studio's
// conditional house judge seats reflect the CURRENT house_style on every call.
// The proof process (process_probe) is authored in this file; the flagship
// (packaging_studio) is authored in packaging_studio.go and registered here so
// the additive registration seam stays a single, testable list.
func builtinProcessDefinitions() []ProcessDefinition {
	return []ProcessDefinition{
		processProbeDefinition(),
		packagingStudioDefinition(),
	}
}

// processDefinitions returns every known process, builtins first, in
// registration order.
func processDefinitions() []ProcessDefinition {
	processRegistryMu.Lock()
	defer processRegistryMu.Unlock()
	defs := builtinProcessDefinitions()
	return append(defs, registeredProcessDefinitions...)
}

// processByID resolves a process id, hidden included (hidden means "not
// served", never "not launchable"). Unknown ids return ok=false so callers
// degrade exactly like toolByID — a stray template is a plain goal, never an
// error.
func processByID(id string) (ProcessDefinition, bool) {
	id = strings.TrimSpace(strings.ToLower(id))
	if id == "" {
		return ProcessDefinition{}, false
	}
	for _, def := range processDefinitions() {
		if def.ID == id {
			return def, true
		}
	}
	return ProcessDefinition{}, false
}

// processDeliverableContract is the contract the process's LAST writer stage
// emits — the artifactContract stamp for the goal artifact, mirroring the tool
// path. "" when the process has no writer stage.
func processDeliverableContract(def ProcessDefinition) string {
	contract := ""
	for _, stage := range def.Stages {
		if stage.Role == processRoleWriter && strings.TrimSpace(stage.OutputContract) != "" {
			contract = strings.TrimSpace(stage.OutputContract)
		}
	}
	return contract
}

// --- Plan instantiation -----------------------------------------------------------

// instantiateProcessPlan replaces free-form decompose for a process goal: the
// definition's stages become the plan's subtasks IN ORDER, InputFrom becomes
// dependsOn, and the whole plan passes the same validation a model decompose
// must pass — against the process's own subtask budget, not the free-form cap.
// Deterministic and model-free, so a restart re-instantiates identically.
func instantiateProcessPlan(def ProcessDefinition, plan *goalPlan) error {
	subtasks := make([]goalSubtask, 0, len(def.Stages))
	for _, stage := range def.Stages {
		dependsOn := make([]string, 0, len(stage.InputFrom))
		for _, from := range stage.InputFrom {
			dependsOn = append(dependsOn, strings.TrimSpace(from))
		}
		subtasks = append(subtasks, goalSubtask{
			ID:        strings.TrimSpace(stage.ID),
			Title:     stage.Title,
			Detail:    stage.PromptBody,
			Mode:      processStageThreadMode(stage),
			Role:      stage.Role,
			Authority: normalizeCodexJobAuthority(plan.Authority),
			DependsOn: dependsOn,
			Status:    subtaskPending,
		})
	}
	candidate := *plan
	candidate.Subtasks = subtasks
	if err := validateGoalPlanWithLimit(&candidate, processMaxSubtasks(def)); err != nil {
		return fmt.Errorf("process %s v%d instantiation: %w", def.ID, def.Version, err)
	}
	plan.Subtasks = candidate.Subtasks
	return nil
}

// processCheckpointOptionsFromText extracts a checkpoint's options from an
// earlier stage's artifact. The output contract puts the array ON ITS OWN
// LINE at the end of the body, so scan lines from the END and parse the first
// one that is a balanced JSON string array — markdown brackets earlier in the
// body (links, checkboxes) can never poison the parse. The historical
// whole-body scan (first '[' to last ']') stays as the fallback for an array
// that shares its line with other text. Lenient — anything unparseable yields
// nil, degrading to a free-form choice rather than an error.
func processCheckpointOptionsFromText(text string) []string {
	lines := strings.Split(text, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if options := decodeCheckpointOptionsArray(strings.TrimSpace(lines[index])); len(options) > 0 {
			return options
		}
	}
	trimmed := strings.TrimSpace(text)
	start := strings.IndexByte(trimmed, '[')
	end := strings.LastIndexByte(trimmed, ']')
	if start < 0 || end < start {
		return nil
	}
	return decodeCheckpointOptionsArray(trimmed[start : end+1])
}

// decodeCheckpointOptionsArray parses one candidate as a JSON string array,
// returning the trimmed non-empty labels — nil when it is not one.
func decodeCheckpointOptionsArray(candidate string) []string {
	if !strings.HasPrefix(candidate, "[") || !strings.HasSuffix(candidate, "]") {
		return nil
	}
	var options []string
	if err := json.Unmarshal([]byte(candidate), &options); err != nil {
		return nil
	}
	cleaned := make([]string, 0, len(options))
	for _, option := range options {
		if option = strings.TrimSpace(option); option != "" {
			cleaned = append(cleaned, option)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

// --- Palette payload shape ---------------------------------------------------------

// processPaletteEntry maps a process onto the packagingTool payload shape so
// the fifth group renders with the exact tile contract the palette already
// enforces (id/name/promise/authority, conversational, no form fields) and the
// router enum picks the id up like any tool id.
func processPaletteEntry(def ProcessDefinition) packagingTool {
	stages := make([]string, 0, len(def.Stages))
	for _, stage := range def.Stages {
		stages = append(stages, stage.ID)
	}
	return packagingTool{
		ID:        def.ID,
		Group:     toolGroupProcesses,
		Name:      def.Title,
		Promise:   def.Description,
		Stages:    stages,
		Mode:      "workflow",
		Contract:  processDeliverableContract(def),
		InputMode: toolInputConversational,
		Authority: firstNonEmptyString(strings.TrimSpace(def.Authority), toolAuthorityWorkspaceWrite),
	}
}

// --- The built-in proof process -----------------------------------------------------

// processProbeDefinition is the tiny built-in proof process the runtime tests
// drive (Wave 4 item 17.4): writer → gate → human_checkpoint, exercising a
// dispatched child, an inline scored gate, and the checkpoint park/resume —
// without depending on packaging_studio (authored concurrently). Hidden: it
// never appears on the public payload or the router enum.
func processProbeDefinition() ProcessDefinition {
	return ProcessDefinition{
		ID:          "process_probe",
		Version:     1,
		Title:       "Process Probe",
		Description: "Three-stage proof pipeline for the process runtime: draft, gate, human checkpoint.",
		Group:       toolGroupProcesses,
		Authority:   toolAuthorityWorkspaceWrite,
		Hidden:      true,
		Budgets:     ProcessBudgets{MaxSubtasks: 3},
		Stages: []ProcessStage{
			{
				ID:             "draft",
				Title:          "Draft the probe note",
				Role:           processRoleWriter,
				Mode:           "artifacts",
				PromptBody:     "Write a short probe note that answers the goal objective directly, with one heading and one recommendation.",
				OutputContract: "probe_note_v1",
			},
			{
				ID:         "note_gate",
				Title:      "Gate the probe note",
				Role:       processRoleGate,
				PromptBody: "Rubric dimensions: Directness (answers the objective, not around it), Brevity (short enough to read in one breath).",
				InputFrom:  []string{"draft"},
				GateSpec:   &ProcessGateSpec{Threshold: 8, Floor: 6, MaxRounds: 2},
			},
			{
				ID:        "ship_choice",
				Title:     "Choose the probe outcome",
				Role:      processRoleHumanCheckpoint,
				InputFrom: []string{"note_gate"},
				CheckpointSpec: &ProcessCheckpointSpec{
					Question: "Ship the probe note as-is, or hold it?",
					// The label tells the truth: hold mechanically parks the goal
					// until a subsequent proceed choice (the negative-option teeth).
					Options: []ProcessCheckpointOption{
						{Label: "ship"},
						{Label: "hold", Action: processCheckpointActionHold},
					},
				},
			},
		},
	}
}
