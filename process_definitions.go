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
	"reflect"
	"runtime"
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

	// documentReportProcessID is Scout's proportionate native-document lane.
	// It is intentionally a process rather than the generic research workstream:
	// the brief decides whether outside research is warranted, while story,
	// writing, and the final quality gate remain present for every real report.
	documentReportProcessID      = "document_report"
	documentReportOutputContract = "native_markdown_report_v1"
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
	// Dimensions are the exact server-authored rubric rows the scorer must
	// return once each. Runtime validation rejects missing, duplicate, extra,
	// non-finite, or out-of-range rows before any average can pass.
	Dimensions []string `json:"dimensions,omitempty"`
	// RepairTarget names the authored stage a failed round should revise. When
	// empty, the historical behavior revises the gate's first input. This lets a
	// rendered review consume both the draft and its review record while still
	// sending repair notes back to the draft writer.
	RepairTarget string `json:"repairTarget,omitempty"`
	// HoldOnFailure is the fail-closed delivery posture: after repair rounds are
	// spent, keep the work internal instead of offering "proceed with gaps".
	HoldOnFailure bool `json:"holdOnFailure,omitempty"`
}

// ProcessStageCondition is a deliberately small conditional seam for authored
// processes, not a workflow DSL. The stage runs only when the named earlier
// stage's JSON string field equals Equals (case-insensitive). Missing or
// malformed decisions fail open to running the stage, so a credibility check
// is never silently skipped.
type ProcessStageCondition struct {
	StageID string `json:"stageId"`
	Field   string `json:"field"`
	Equals  string `json:"equals"`
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
	processCheckpointMaxOptions    = 3
	processCheckpointMaxLabelRunes = 160
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
	RunIf          *ProcessStageCondition `json:"runIf,omitempty"`
	// Internal suppresses routine stage-completion messages in the channel. The
	// durable artifacts and progress state remain available in the activity
	// surface; final deliverables and genuine decision boundaries stay visible.
	Internal bool `json:"internal,omitempty"`
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
	// ImplementationRevision is the authored code revision for behavior that
	// JSON cannot carry, most importantly Compile functions. It MUST change
	// whenever a process's runtime implementation changes without a data-shape
	// change. The exact value is pinned into every plan and route receipt.
	ImplementationRevision string `json:"implementationRevision"`
	// Hidden keeps a process launchable by id (tests, internal proofs) while
	// leaving it OFF the public /assistant/tools payload and the router enum.
	Hidden  bool           `json:"hidden,omitempty"`
	Budgets ProcessBudgets `json:"budgets,omitempty"`
	Stages  []ProcessStage `json:"stages"`
}

const processDefinitionDigestSchema = "stride.process-definition.identity.v1"

type processCompileBinding struct {
	StageID string `json:"stageId"`
	Symbol  string `json:"symbol"`
}

type processDefinitionDigestMaterial struct {
	Schema          string                  `json:"schema"`
	Definition      ProcessDefinition       `json:"definition"`
	CompileBindings []processCompileBinding `json:"compileBindings,omitempty"`
}

type processDefinitionIdentity struct {
	ID                     string
	Version                int
	Digest                 string
	ImplementationRevision string
	ResultStageID          string
	ResultOutputContract   string
}

// processDefinitionIdentityFor computes the complete executable identity of a
// process. ProcessDefinition's JSON covers prompts, order, contracts,
// conditions, checkpoints, budgets and gates. Compile is deliberately omitted
// from that JSON, so its stable runtime symbol is included alongside the
// explicit implementation revision. Pointer addresses are never hashed (ASLR
// would make a persisted identity fail after restart).
func processDefinitionIdentityFor(def ProcessDefinition) (processDefinitionIdentity, error) {
	if err := validateProcessDefinition(def); err != nil {
		return processDefinitionIdentity{}, err
	}
	bindings := make([]processCompileBinding, 0)
	resultStageID := ""
	resultOutputContract := ""
	for _, stage := range def.Stages {
		if stage.Role == processRoleWriter {
			resultStageID = strings.TrimSpace(stage.ID)
			resultOutputContract = strings.TrimSpace(stage.OutputContract)
		}
		if stage.Compile == nil {
			continue
		}
		fn := runtime.FuncForPC(reflect.ValueOf(stage.Compile).Pointer())
		if fn == nil || strings.TrimSpace(fn.Name()) == "" {
			return processDefinitionIdentity{}, fmt.Errorf("process %q stage %q compile implementation has no stable symbol", def.ID, stage.ID)
		}
		bindings = append(bindings, processCompileBinding{StageID: strings.TrimSpace(stage.ID), Symbol: fn.Name()})
	}
	digest, err := digestAny(processDefinitionDigestMaterial{
		Schema:          processDefinitionDigestSchema,
		Definition:      def,
		CompileBindings: bindings,
	})
	if err != nil || !isHexDigest(digest) {
		return processDefinitionIdentity{}, fmt.Errorf("process %q identity could not be encoded", def.ID)
	}
	return processDefinitionIdentity{
		ID:                     strings.TrimSpace(def.ID),
		Version:                def.Version,
		Digest:                 digest,
		ImplementationRevision: strings.TrimSpace(def.ImplementationRevision),
		ResultStageID:          resultStageID,
		ResultOutputContract:   resultOutputContract,
	}, nil
}

// cloneProcessDefinition makes registry entries immutable to callers while
// retaining Go function values that cannot be JSON-cloned.
func cloneProcessDefinition(def ProcessDefinition) ProcessDefinition {
	cloned := def
	cloned.Stages = make([]ProcessStage, len(def.Stages))
	for index, stage := range def.Stages {
		clonedStage := stage
		clonedStage.Personas = append([]ProcessPersona(nil), stage.Personas...)
		clonedStage.InputFrom = append([]string(nil), stage.InputFrom...)
		if stage.GateSpec != nil {
			gate := *stage.GateSpec
			gate.Dimensions = append([]string(nil), stage.GateSpec.Dimensions...)
			clonedStage.GateSpec = &gate
		}
		if stage.CheckpointSpec != nil {
			checkpoint := *stage.CheckpointSpec
			checkpoint.Options = append([]ProcessCheckpointOption(nil), stage.CheckpointSpec.Options...)
			clonedStage.CheckpointSpec = &checkpoint
		}
		if stage.RunIf != nil {
			condition := *stage.RunIf
			clonedStage.RunIf = &condition
		}
		cloned.Stages[index] = clonedStage
	}
	return cloned
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
		// A deck that merely renders is not enough: the native editor must be
		// able to round-trip every authored slide without flattening or losing
		// layout. Run the same strict legacy-to-scene importer used by Deck
		// Studio before spending reviewer tokens. Missing inline geometry,
		// unrecognized behavior, invalid coordinates, or inaccessible image
		// references are blocking output defects, not post-ship cleanup.
		candidate := meetingMemoryEntry{Text: trimmed, Metadata: map[string]string{"type": artifactTypeHTMLDeck}}
		deck, quality := importLegacyDeckDocument(candidate)
		if quality != "faithful" {
			return "LAW SWEEP (packaging_deck_v1): the deck is not faithfully editable. Every authored slide and meaningful text, image, and shape must use the required data-deck ids/types and complete inline 1920x1080 geometry; remove unrecognized presenter behavior and emit the full editable HTML file.", true
		}
		if err := validateDeckDocument(deck, artifactAssetRefSet(candidate)); err != nil {
			return "LAW SWEEP (packaging_deck_v1): the editable scene is invalid: " + compactAssistantLine(err.Error()), true
		}
	case documentReportOutputContract:
		if !strings.HasPrefix(trimmed, "# ") {
			return "LAW SWEEP (native_markdown_report_v1): emit the editable Markdown document itself, beginning with one specific H1 title. Do not return a plan, workflow log, JSON object, or fenced code block.", true
		}
		lowered := strings.ToLower(trimmed)
		if strings.HasPrefix(lowered, "# scout work thread") || strings.Contains(lowered, "\n## work decomposition") || strings.Contains(lowered, "\n## agent assignment") {
			return "LAW SWEEP (native_markdown_report_v1): the deliverable is a reader-ready report, not an execution log. Remove workflow scaffolding and emit only the finished Markdown document.", true
		}
	}
	return "", false
}

// rawDocumentContractInstructions returns full-replacement worker
// instructions for contracts whose deliverable IS a raw document — the
// instruction-layer twin of processStageLawSweep, kept adjacent so what the
// sweep enforces is exactly what the instructions demand. The generic
// "one-line Vision, then Markdown sections" workflow instructions are the
// documented death loop for these stages: the model obeys the system prompt
// over the stage prompt and the sweep rejects every round.
func rawDocumentContractInstructions(contract string) (string, bool) {
	switch strings.TrimSpace(contract) {
	case "packaging_deck_v1":
		return strings.Join([]string{
			"You are the deck writer for Bonfire's packaging studio.",
			"Your ENTIRE response is the deliverable FILE ITSELF: one complete, self-contained HTML document.",
			"The FIRST characters of your response must be <!doctype html> and it must end with </html> — no preamble, no markdown, no code fences, no Vision line, no section headings, no commentary before or after the file.",
			"A plan, outline, or description of the deck is a FAILED deliverable — the law sweep rejects anything that is not the document itself.",
			"Follow every instruction in the user request (the stage prompt): the required print chassis <style> block verbatim, the .pg slide model inside #stage, inert per-slide .notes from the locked presenter_note in the deck copy, and a .fig-N slot for each generated image. Do not add custom JavaScript or presenter chrome; the native app owns presentation behavior.",
			"The file must round-trip through Deck Studio faithfully: stable data-deck ids/types plus explicit inline position, size, z-index, opacity, rotation, typography, fills, and image-fit for every editable element. A visually plausible but non-editable HTML page fails the deterministic law sweep.",
		}, "\n"), true
	case documentReportOutputContract:
		return strings.Join([]string{
			"You are Scout's senior report writer for Stride.",
			"Your ENTIRE response is the finished, editable Markdown document itself. Begin with one specific H1 title and include no preamble, code fence, JSON envelope, Vision line, workflow log, process commentary, or text after the document.",
			"Follow the approved brief and prior-stage evidence supplied in the user request. Choose only the sections the actual decision needs; do not impose a generic research-report template or a fixed word count.",
			"Build a coherent human argument: lead with the useful thesis, earn it with attributable evidence and counterevidence, make inferences explicit, and end with concrete decisions, tests, or next moves appropriate to the ask.",
			"Use clickable Markdown links beside externally sourced claims. Keep company-grounded observations, external facts, inferences, and recommendations distinct. Never invent a quote, source, number, or degree of certainty.",
			"For every heading, paragraph, or table row containing a material number, currency, percentage, date, external URL, or externally verifiable superlative, render the complete exact admitted claim verbatim and append the exact hidden authority marker required by the stage prompt. This marker is source metadata and must stay in the Markdown document.",
			processForwardStatementPromptLaw,
			"Write publication-quality prose with varied, natural cadence. Remove AI tells, filler headings, slogan stacks, throat-clearing, and meta-language about producing the report.",
		}, "\n"), true
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
	if strings.TrimSpace(def.ImplementationRevision) == "" {
		return fmt.Errorf("process %q has no implementation revision", id)
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
		if condition := stage.RunIf; condition != nil {
			conditionStage := strings.TrimSpace(condition.StageID)
			if !earlier[conditionStage] {
				return fmt.Errorf("process %q stage %q runIf stage %q does not name an earlier stage", id, stageID, conditionStage)
			}
			if strings.TrimSpace(condition.Field) == "" || strings.TrimSpace(condition.Equals) == "" {
				return fmt.Errorf("process %q stage %q runIf must name a non-empty field and equals value", id, stageID)
			}
			conditionIsInput := false
			for _, from := range stage.InputFrom {
				if strings.TrimSpace(from) == conditionStage {
					conditionIsInput = true
					break
				}
			}
			if !conditionIsInput {
				return fmt.Errorf("process %q stage %q runIf stage %q must also be listed in inputFrom", id, stageID, conditionStage)
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
			if stage.GateSpec != nil && strings.TrimSpace(stage.GateSpec.RepairTarget) != "" {
				targetFound := false
				for _, from := range stage.InputFrom {
					if strings.TrimSpace(from) == strings.TrimSpace(stage.GateSpec.RepairTarget) {
						targetFound = true
						break
					}
				}
				if !targetFound {
					return fmt.Errorf("process %q gate stage %q repairTarget %q is not one of its inputFrom stages", id, stageID, stage.GateSpec.RepairTarget)
				}
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
			if len(stage.CheckpointSpec.Options) > processCheckpointMaxOptions {
				return fmt.Errorf("process %q checkpoint stage %q has %d options; maximum is %d", id, stageID, len(stage.CheckpointSpec.Options), processCheckpointMaxOptions)
			}
			seenOptionLabels := map[string]bool{}
			for _, option := range stage.CheckpointSpec.Options {
				label := strings.TrimSpace(option.Label)
				if label == "" {
					return fmt.Errorf("process %q checkpoint stage %q has an option with no label", id, stageID)
				}
				if len([]rune(label)) > processCheckpointMaxLabelRunes {
					return fmt.Errorf("process %q checkpoint stage %q option label exceeds %d characters", id, stageID, processCheckpointMaxLabelRunes)
				}
				foldedLabel := strings.ToLower(label)
				if seenOptionLabels[foldedLabel] {
					return fmt.Errorf("process %q checkpoint stage %q has duplicate option %q", id, stageID, option.Label)
				}
				seenOptionLabels[foldedLabel] = true
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
	// W2C's contract is deliberately not a generic process. Until its dedicated
	// executor supplies snapshot reauthorization and per-action approval, even
	// internal callers must not smuggle the inert definition into this registry.
	if strings.TrimSpace(strings.ToLower(def.ID)) == insightsOpportunitiesProcessID {
		return fmt.Errorf("process id %q requires the dedicated W2C executor", def.ID)
	}
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
	registeredProcessDefinitions = append(registeredProcessDefinitions, cloneProcessDefinition(def))
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
		documentReportDefinition(),
		packagingStudioDefinition(),
	}
}

// processDefinitions returns every known process, builtins first, in
// registration order.
func processDefinitions() []ProcessDefinition {
	processRegistryMu.Lock()
	defer processRegistryMu.Unlock()
	defs := builtinProcessDefinitions()
	result := make([]ProcessDefinition, 0, len(defs)+len(registeredProcessDefinitions))
	for _, def := range defs {
		result = append(result, cloneProcessDefinition(def))
	}
	for _, def := range registeredProcessDefinitions {
		result = append(result, cloneProcessDefinition(def))
	}
	return result
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

func bindGoalProcessIdentity(plan *goalPlan, def ProcessDefinition) error {
	if plan == nil {
		return fmt.Errorf("process plan is unavailable")
	}
	identity, err := processDefinitionIdentityFor(def)
	if err != nil {
		return err
	}
	plan.ProcessID = identity.ID
	plan.ProcessVersion = identity.Version
	plan.ProcessDigest = identity.Digest
	plan.ProcessImplementationRevision = identity.ImplementationRevision
	plan.ResultStageID = identity.ResultStageID
	plan.ResultOutputContract = identity.ResultOutputContract
	return nil
}

func verifyGoalProcessIdentity(plan *goalPlan, identity processDefinitionIdentity) error {
	if plan == nil || strings.TrimSpace(plan.ProcessID) == "" {
		return fmt.Errorf("saved process identity is unavailable; launch a new run")
	}
	if plan.ProcessVersion < 1 || !isHexDigest(plan.ProcessDigest) || strings.TrimSpace(plan.ProcessImplementationRevision) == "" {
		return fmt.Errorf("saved process identity is incomplete; launch a new run")
	}
	if strings.TrimSpace(plan.ProcessID) != identity.ID || plan.ProcessVersion != identity.Version ||
		plan.ProcessDigest != identity.Digest || strings.TrimSpace(plan.ProcessImplementationRevision) != identity.ImplementationRevision ||
		strings.TrimSpace(plan.ResultStageID) != identity.ResultStageID || strings.TrimSpace(plan.ResultOutputContract) != identity.ResultOutputContract {
		return fmt.Errorf("saved process %s identity no longer matches an available immutable definition; relaunch or explicitly migrate the run", strings.TrimSpace(plan.ProcessID))
	}
	return nil
}

// resolvePinnedProcessDefinition is the only runtime process lookup. An old
// ProcessID-only plan, a removed definition, or any drift in prompts, stage
// order, contracts, conditions, checkpoints, budgets, gates, compile binding,
// version, or implementation revision fails closed. It never degrades a
// process goal into a generic goal or unavailable-stub execution lane.
func resolvePinnedProcessDefinition(plan *goalPlan) (ProcessDefinition, error) {
	if plan == nil || strings.TrimSpace(plan.ProcessID) == "" {
		return ProcessDefinition{}, fmt.Errorf("saved process identity is unavailable; launch a new run")
	}
	if !plan.routeVerified {
		return ProcessDefinition{}, fmt.Errorf("saved process route is not verified")
	}
	def, ok := processByID(plan.ProcessID)
	if !ok {
		return ProcessDefinition{}, fmt.Errorf("saved process %s is no longer available; relaunch or explicitly migrate the run", strings.TrimSpace(plan.ProcessID))
	}
	identity, err := processDefinitionIdentityFor(def)
	if err != nil {
		return ProcessDefinition{}, fmt.Errorf("saved process %s cannot be resolved safely: %w", strings.TrimSpace(plan.ProcessID), err)
	}
	if err := verifyGoalProcessIdentity(plan, identity); err != nil {
		return ProcessDefinition{}, err
	}
	return def, nil
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
	if plan == nil {
		return fmt.Errorf("process plan is unavailable")
	}
	identity, err := processDefinitionIdentityFor(def)
	if err != nil {
		return err
	}
	if err := verifyGoalProcessIdentity(plan, identity); err != nil {
		return err
	}
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
	if len(options) > processCheckpointMaxOptions {
		return nil
	}
	cleaned := make([]string, 0, len(options))
	seen := map[string]bool{}
	for _, option := range options {
		if option = strings.TrimSpace(option); option != "" {
			if len([]rune(option)) > processCheckpointMaxLabelRunes {
				return nil
			}
			folded := strings.ToLower(option)
			if seen[folded] {
				return nil
			}
			seen[folded] = true
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
		ID:                     "process_probe",
		Version:                1,
		Title:                  "Process Probe",
		Description:            "Three-stage proof pipeline for the process runtime: draft, gate, human checkpoint.",
		Group:                  toolGroupProcesses,
		Authority:              toolAuthorityWorkspaceWrite,
		ImplementationRevision: "process_probe.runtime.v1",
		Hidden:                 true,
		Budgets:                ProcessBudgets{MaxSubtasks: 3},
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
				GateSpec:   &ProcessGateSpec{Threshold: 8, Floor: 6, MaxRounds: 2, Dimensions: []string{"Directness", "Brevity"}},
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

// documentReportDefinition is the native-document counterpart to Packaging
// Studio. It deliberately leaves the requested report shape open: Scout first
// resolves the audience and decision, spends hosted research only when current
// external facts could materially change the answer, finds an argument, writes
// the actual Markdown file, and holds anything that misses the exact quality
// bar. There is no routine human checkpoint; a well-formed ask runs through.
func documentReportDefinition() ProcessDefinition {
	internal := true
	return ProcessDefinition{
		ID:                     documentReportProcessID,
		Version:                3,
		Title:                  "Document Studio",
		Description:            "Turn a substantial document or report request and authorized company context into a researched-when-needed, reviewed, editable native document.",
		Group:                  toolGroupProcesses,
		Authority:              toolAuthorityWorkspaceWrite,
		ImplementationRevision: "document_report.runtime.v3.rendered-admission.v1",
		Budgets:                ProcessBudgets{MaxSubtasks: 12, MaxTokens: 64000, WallClock: 25 * time.Minute},
		Stages: []ProcessStage{
			{
				ID:       "context_snapshot",
				Title:    "Understand the decision",
				Role:     processRoleSynthesizer,
				Internal: internal,
				PromptBody: strings.Join([]string{
					"Turn the direct approved request, exact reply-thread/source packet, and authorized Company Brain context into report_context_snapshot_v2. The current request is authoritative; older company context may support it but never override it.",
					"Resolve the reader, decision or job to be done, intended use, scope, voice, useful document shape, known constraints, exact language worth preserving, settled internal facts, and genuinely open claims. Prefer a safe reversible inference over a routine clarification and label it.",
					"Choose research_mode as none, internal, or external. Use external only when current market facts, benchmarks, regulations, comparative claims, or credibility-critical numbers could materially change the report. Use the fewest decision-driving questions: one decisive lane is better than a broad scan. Do not ask hosted web research to reconstruct private account analytics or perform a broad multi-platform audit; record that as an internal data need instead. A synthesis, internal memo, narrative draft, or answer fully supported by authorized sources does not need web research.",
					"When research_mode is external, research_questions must contain 1 to 3 atomic single-line objects and no other shape. Each object has exactly question, research_kind, source_ref, authority_quote, scope_anchor, decision_effect, and decision_relevance. research_kind is direct_evidence, comparative_evidence, or current_constraint. decision_effect is recommendation, scope, sequence, guardrail, or measurement. The question has exactly one question mark. Copy source_ref exactly as the full text inside one SOURCE [...] header, excluding only the literal brackets. Copy authority_quote exactly from that same source. scope_anchor is an exact 2 to 12 material-word phrase present in the direct ask, the authority_quote, the question, and decision_relevance; a company name or generic word such as market is insufficient. decision_relevance repeats that anchor and states concretely how the answer could change a recommendation, decision, pilot, sequence, scope, guardrail, or measurement in this report. direct_evidence must preserve the authorized entity, population, measure, predicate, geography, and time window. comparative_evidence may introduce named comparators only when the question explicitly asks for a comparison or benchmark and stays within one measure lane. current_constraint may introduce a regulator or platform only to ask for current rules, policy, regulation, or requirements; it must not bundle market, spend, reach, or performance claims. When research_mode is none or internal, research_questions is an empty array.",
					"Return one JSON object with keys direct_ask, reader, decision, intended_use, document_shape, scope, voice, constraints, context_used, settled_facts, open_claims, research_mode, research_questions, reversible_inferences, and success_criteria. settled_facts must be an array of objects with claim, exact_quote, and source_ref. Copy claim and exact_quote verbatim from one authorized source and make them identical after whitespace normalization. Copy the complete bracketed reference exactly from the same SOURCE [...] block or source-linked Company Brain line into source_ref, including every id, revision, and digest field; never synthesize or combine a reference. If that same-source proof is unavailable, put the item in open_claims instead.",
				}, "\n"),
				OutputContract: "report_context_snapshot_v2",
			},
			{
				ID:        "external_research",
				Title:     "Verify what could change the answer",
				Role:      processRoleWriter,
				Mode:      "research",
				Internal:  internal,
				InputFrom: []string{"context_snapshot"},
				RunIf:     &ProcessStageCondition{StageID: "context_snapshot", Field: "research_mode", Equals: "external"},
				PromptBody: strings.Join([]string{
					"Research only the credibility-critical questions authorized by the context snapshot; one decisive question is enough when that is all the report needs.",
					"Prefer current primary or official sources where available. For every used finding provide the precise claim, provider-fetched URL, publication date when known, units, confidence, and its implication for the report. Separate sourced fact from inference and exclude anything not verified in this run.",
				}, "\n"),
				OutputContract: packagingStudioExternalEvidenceContract,
			},
			{
				ID:         "source_snapshot",
				Title:      "Capture exact source text",
				Role:       processRoleCompile,
				Internal:   internal,
				InputFrom:  []string{"context_snapshot", "external_research"},
				RunIf:      &ProcessStageCondition{StageID: "context_snapshot", Field: "research_mode", Equals: "external"},
				PromptBody: "Fetch the exact provider-linked HTTPS sources server-side and preserve bounded, digest-bound text windows for each candidate claim. Fetch failures and pages with no relevant text remain explicit; URL membership alone never proves a claim.",
				Compile:    compileExternalEvidenceSourceSnapshots,
			},
			{
				ID:             "evidence_entailment",
				Title:          "Check what the sources actually prove",
				Role:           processRoleWriter,
				Mode:           "artifacts",
				Internal:       internal,
				InputFrom:      []string{"context_snapshot", "external_research", "source_snapshot"},
				RunIf:          &ProcessStageCondition{StageID: "context_snapshot", Field: "research_mode", Equals: "external"},
				PromptBody:     "Independently check every exact candidate fact + URL pair using only the exact authority-bound text windows the server fetched from that URL. Do not start a second search. Admit only claims those windows actually entail with matching population, date, units, and numeric fidelity. Reject unclear, contradicted, unfetched, or merely URL-associated claims; never repair a claim into a stronger one.",
				OutputContract: packagingStudioEntailmentContract,
			},
			{
				ID:             "evidence",
				Title:          "Lock the evidence",
				Role:           processRoleCompile,
				Internal:       internal,
				InputFrom:      []string{"context_snapshot", "evidence_entailment"},
				PromptBody:     "Produce report_evidence_dossier_v1 from authorized Company Brain/direct-source facts and the entailment-check record. Every report_ready_claim carries claim, provenance, URL when external, date and units when relevant, confidence, and exactly one status: entailment_checked for an external claim admitted by evidence_entailment, or internal for an attributable authorized company/direct-source fact. State counterevidence and missing proof. No verified, inferred, suggested, rejected, unclear, or merely provider-fetched claim may enter report_ready_claims.",
				OutputContract: "report_evidence_dossier_v1",
				Compile:        compileProcessEvidenceDossier,
			},
			{
				ID:        "story",
				Title:     "Find the strongest argument",
				Role:      processRolePanel,
				Internal:  internal,
				InputFrom: []string{"context_snapshot", "evidence"},
				Personas: []ProcessPersona{
					{Name: "decision editor", System: "You are a rigorous executive editor. Build the shortest causal argument that helps this exact reader make the intended decision. Refuse topic dumps, generic section templates, repeated points, and recommendations the evidence has not earned."},
					{Name: "skeptical operator", System: "You are the operator who must act on this report. Pressure-test proof, counterevidence, incentives, feasibility, risks, guardrails, success measures, and what would change the recommendation. Keep only material objections and executable implications."},
				},
				PromptBody:     "Develop distinct narrative approaches for the actual reader and decision, then synthesize one report_story_spine_v1. Name the opening thesis, causal turns, evidence assigned to each turn, counterargument, implications, and ending decision or test. Use only report-ready claims, preserve explicit source language exactly, and choose a story rather than an outline of topics. For every JSON object that contains a material number, currency, percentage, date, external URL, or externally verifiable superlative, include sibling claim_ids and exact_claims arrays copied exactly from the admitted evidence row and render the complete exact admitted claim verbatim in the fact-bearing string. If you write prose instead of JSON, append <!-- stride-claim:<claim id> | <exact admitted claim> --> in the same paragraph and render that exact claim verbatim in the factual sentence. " + processForwardStatementPromptLaw,
				OutputContract: "report_story_spine_v1",
			},
			{
				ID:             "write",
				Title:          "Write the editable document",
				Role:           processRoleWriter,
				Mode:           "artifacts",
				Internal:       internal,
				InputFrom:      []string{"context_snapshot", "evidence", "story"},
				PromptBody:     "Write the finished native Markdown document now. Honor the requested report type and choose only useful sections. Open with a specific title and a decision-worthy thesis; build one coherent narrative supported by attributed facts and clearly labeled inference; include counterevidence, risks, guardrails, and concrete opportunities, tests, owners, measures, or next decisions only where relevant. Use natural human prose and clickable citations. For every heading, paragraph, or table row containing a material number, currency, percentage, date, external URL, or externally verifiable superlative, render the complete exact admitted claim verbatim and append one hidden authority marker in that same heading, paragraph, or row using exactly <!-- stride-claim:<claim id> | <exact admitted claim> -->. The id and claim text must be copied from the dossier; the external URL must equal that row's requested or final URL. Do not include process artifacts, a research receipt, a generic workflow template, or a fixed minimum length. " + processForwardStatementPromptLaw,
				OutputContract: documentReportOutputContract,
			},
			{
				ID:         "quality_gate",
				Title:      "Hold or perfect the document",
				Role:       processRoleGate,
				Internal:   internal,
				InputFrom:  []string{"write", "context_snapshot", "evidence", "story"},
				PromptBody: "Score Direct-request fidelity, Decision usefulness, Narrative coherence, Evidence integrity, Human voice, Specificity and actionability, and Document completeness. The report must be a polished document a trusted colleague could circulate without rewriting, not an AI-shaped list or process log. Every weak score must name an executable repair; unsupported certainty, invented facts, generic prose, or a structurally correct but unhelpful report cannot pass.",
				GateSpec: &ProcessGateSpec{
					Threshold: 9, Floor: 7, MaxRounds: 2, RepairTarget: "write", HoldOnFailure: true,
					Dimensions: []string{
						"Direct-request fidelity", "Decision usefulness", "Narrative coherence", "Evidence integrity", "Human voice", "Specificity and actionability", "Document completeness",
					},
				},
			},
			{
				ID:             documentReportDraftRenderStageID,
				Title:          "Render the exact document draft",
				Role:           processRoleCompile,
				Internal:       internal,
				InputFrom:      []string{"write", "quality_gate"},
				PromptBody:     "Convert the exact text-approved native Markdown revision into the branded text-native PDF, persist every rendered page image, and bind artifact id, revision, content digest, PDF, and ordered page set. A missing, failed, stale, partial, or timed-out render becomes needs_attention; it never degrades into a text-only pass.",
				OutputContract: documentReportRenderContract,
				Compile:        compileDocumentReportDraftRender,
			},
			{
				ID:             documentReportJuryStageID,
				Title:          "Review the rendered document",
				Role:           processRoleCompile,
				Internal:       internal,
				InputFrom:      []string{documentReportDraftRenderStageID},
				PromptBody:     "Put every exact rendered page before distinct visual document critics. Judge hierarchy, density, tables, page breaks, orphans and widows, captions, citations and links, accessibility and contrast, and print/PDF completeness. Preserve page-specific executable repairs and fail closed as needs_attention when the render, provider, page coverage, or seat quorum is unavailable.",
				OutputContract: documentReportJuryContract,
				Compile:        compileDocumentReportJury,
			},
			{
				ID:         documentReportRenderedAdmissionID,
				Title:      "Admit the rendered document",
				Role:       processRoleGate,
				Internal:   internal,
				InputFrom:  []string{documentReportJuryStageID, documentReportDraftRenderStageID, "write"},
				PromptBody: "Deterministically admit only the exact rendered document revision whose complete page set received at least two distinct valid jury seats and whose minimum per-page average is at least 8.5. Route exact executable page repairs back to the document writer; needs_attention cannot be overridden by a text scorer.",
				GateSpec: &ProcessGateSpec{
					Threshold: documentReportReadyAverageFloor, Floor: documentReportReadyAverageFloor, MaxRounds: 2, RepairTarget: "write", HoldOnFailure: true,
					Dimensions: []string{"Hierarchy", "Density", "Tables", "Page breaks", "Orphans and widows", "Captions", "Citations and links", "Accessibility and contrast", "Print/PDF completeness"},
				},
			},
			{
				ID:         documentReportPublishStageID,
				Title:      "Publish the admitted document",
				Role:       processRoleCompile,
				Internal:   internal,
				InputFrom:  []string{documentReportRenderedAdmissionID},
				PromptBody: "Publish the exact editable report and its already-bound PDF only after deterministic rendered admission. Re-read every binding immediately before delivery; do not mutate the reviewed report revision after admission.",
				Compile:    compileDocumentReportPublish,
			},
		},
	}
}
