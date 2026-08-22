package main

import (
	"fmt"
	"strings"
	"testing"
)

// process_definitions_test.go — the ProcessDefinition runtime's data layer
// (Wave 4 item 17): definition validation, the registry, instantiation, and
// the additive fifth payload group. The engine-side execution (checkpoint
// park/resume, inline stages, budgets in flight) is pinned in
// goal_engine_test.go beside the machinery it drives.

// registerProcessDefinitionForTest registers a test-only definition and
// removes it again at cleanup so the registry never leaks across tests.
func registerProcessDefinitionForTest(t *testing.T, def ProcessDefinition) {
	t.Helper()
	if strings.TrimSpace(def.ImplementationRevision) == "" {
		def.ImplementationRevision = "test." + strings.TrimSpace(def.ID) + ".runtime.v1"
	}
	if err := registerProcessDefinition(def); err != nil {
		t.Fatalf("registerProcessDefinition(%s): %v", def.ID, err)
	}
	t.Cleanup(func() {
		processRegistryMu.Lock()
		defer processRegistryMu.Unlock()
		kept := registeredProcessDefinitions[:0]
		for _, existing := range registeredProcessDefinitions {
			if existing.ID != def.ID {
				kept = append(kept, existing)
			}
		}
		registeredProcessDefinitions = kept
	})
}

func pinProcessPlanForTest(t *testing.T, plan *goalPlan, def ProcessDefinition) {
	t.Helper()
	if err := bindGoalProcessIdentity(plan, def); err != nil {
		t.Fatalf("bindGoalProcessIdentity(%s): %v", def.ID, err)
	}
	plan.routeVerified = true
}

// testProcessCompileFunc is a no-op compiler for validation-shape tests.
func testProcessCompileFunc(_ *kanbanBoardApp, _ *goalPlan, _ string, _ ProcessStage) (string, map[string]string, error) {
	return "compiled", nil, nil
}

// validProcessProbeLikeDefinition is a minimal valid definition tests mutate.
func validProcessProbeLikeDefinition(id string) ProcessDefinition {
	return ProcessDefinition{
		ID:                     id,
		Version:                1,
		Title:                  "Test Process",
		Description:            "Test-only process definition.",
		Authority:              toolAuthorityWorkspaceWrite,
		ImplementationRevision: "test." + id + ".runtime.v1",
		Hidden:                 true,
		Stages: []ProcessStage{
			{ID: "w1", Title: "Write", Role: processRoleWriter},
			{ID: "g1", Title: "Gate", Role: processRoleGate, InputFrom: []string{"w1"}},
		},
	}
}

// --- Definition validation ----------------------------------------------------

func TestBuiltinProcessDefinitionsValidate(t *testing.T) {
	defs := builtinProcessDefinitions()
	if len(defs) == 0 {
		t.Fatal("no builtin process definitions — process_probe must exist")
	}
	for _, def := range defs {
		if err := validateProcessDefinition(def); err != nil {
			t.Errorf("builtin process %q does not validate: %v", def.ID, err)
		}
	}
	probe, ok := processByID("process_probe")
	if !ok {
		t.Fatal("process_probe missing from the registry")
	}
	if !probe.Hidden {
		t.Fatal("process_probe must be Hidden — it is a test proof, never a public tile")
	}
	if len(probe.Stages) != 3 {
		t.Fatalf("process_probe has %d stages, want the writer→gate→human_checkpoint trio", len(probe.Stages))
	}
	wantRoles := []string{processRoleWriter, processRoleGate, processRoleHumanCheckpoint}
	for index, want := range wantRoles {
		if probe.Stages[index].Role != want {
			t.Fatalf("process_probe stage %d role=%q, want %q", index, probe.Stages[index].Role, want)
		}
	}
	// The probe's checkpoint options carry truthful actions: ship proceeds
	// (the default), hold mechanically holds (the negative-option teeth).
	choice := probe.Stages[2].CheckpointSpec
	if choice == nil || len(choice.Options) != 2 {
		t.Fatalf("process_probe checkpoint options=%+v, want ship + hold", choice)
	}
	if choice.Options[0].Label != "ship" || processCheckpointOptionAction(choice.Options[0]) != processCheckpointActionProceed {
		t.Fatalf("probe ship option=%+v, want a proceed-action ship", choice.Options[0])
	}
	if choice.Options[1].Label != "hold" || processCheckpointOptionAction(choice.Options[1]) != processCheckpointActionHold {
		t.Fatalf("probe hold option=%+v, want a hold-action hold", choice.Options[1])
	}
}

func TestDocumentReportProcessIsConditionalGatedNativeMarkdown(t *testing.T) {
	def, ok := processByID(documentReportProcessID)
	if !ok || def.Hidden {
		t.Fatalf("document report process=%+v ok=%t", def, ok)
	}
	if err := validateProcessDefinition(def); err != nil {
		t.Fatalf("document report process does not validate: %v", err)
	}
	wantStages := []string{"context_snapshot", "external_research", "source_snapshot", "evidence_entailment", "evidence", "story", "write", "quality_gate"}
	if len(def.Stages) != len(wantStages) {
		t.Fatalf("stages=%d, want %d", len(def.Stages), len(wantStages))
	}
	for index, want := range wantStages {
		if def.Stages[index].ID != want {
			t.Fatalf("stage %d=%q, want %q", index, def.Stages[index].ID, want)
		}
		if def.Stages[index].Role == processRoleHumanCheckpoint {
			t.Fatalf("routine human checkpoint leaked into document process: %+v", def.Stages[index])
		}
	}
	research := def.Stages[1]
	if research.Mode != "research" || research.OutputContract != packagingStudioExternalEvidenceContract || research.RunIf == nil ||
		research.RunIf.StageID != "context_snapshot" || research.RunIf.Field != "research_mode" || research.RunIf.Equals != "external" {
		t.Fatalf("conditional research stage=%+v", research)
	}
	sourceSnapshot := def.Stages[2]
	if sourceSnapshot.Role != processRoleCompile || sourceSnapshot.Compile == nil || strings.Join(sourceSnapshot.InputFrom, "|") != "context_snapshot|external_research" ||
		sourceSnapshot.RunIf == nil || sourceSnapshot.RunIf.StageID != "context_snapshot" || sourceSnapshot.RunIf.Equals != "external" {
		t.Fatalf("source snapshot stage=%+v", sourceSnapshot)
	}
	entailment := def.Stages[3]
	if entailment.Role != processRoleWriter || entailment.Mode != "artifacts" || entailment.OutputContract != packagingStudioEntailmentContract ||
		strings.Join(entailment.InputFrom, "|") != "context_snapshot|external_research|source_snapshot" || entailment.RunIf == nil || entailment.RunIf.StageID != "context_snapshot" || entailment.RunIf.Equals != "external" ||
		!strings.Contains(entailment.PromptBody, "Do not start a second search") || strings.Contains(entailment.PromptBody, "fresh provider retrieval") {
		t.Fatalf("entailment stage=%+v", entailment)
	}
	evidence := def.Stages[4]
	if evidence.Role != processRoleCompile || evidence.Compile == nil || strings.Join(evidence.InputFrom, "|") != "context_snapshot|evidence_entailment" ||
		!strings.Contains(evidence.PromptBody, "entailment_checked") || !strings.Contains(evidence.PromptBody, "exactly one status") {
		t.Fatalf("evidence admission stage=%+v", evidence)
	}
	write := def.Stages[6]
	if write.OutputContract != documentReportOutputContract || processDeliverableContract(def) != documentReportOutputContract {
		t.Fatalf("write contract=%q process contract=%q", write.OutputContract, processDeliverableContract(def))
	}
	gate := def.Stages[7]
	wantDimensions := []string{"Direct-request fidelity", "Decision usefulness", "Narrative coherence", "Evidence integrity", "Human voice", "Specificity and actionability", "Document completeness"}
	if gate.GateSpec == nil || !gate.GateSpec.HoldOnFailure || gate.GateSpec.RepairTarget != "write" || gate.GateSpec.Threshold != 9 || gate.GateSpec.Floor != 7 ||
		strings.Join(gate.GateSpec.Dimensions, "|") != strings.Join(wantDimensions, "|") {
		t.Fatalf("quality gate=%+v", gate.GateSpec)
	}
	if def.Version != 2 || def.ImplementationRevision != "document_report.runtime.v2" || def.Budgets.MaxSubtasks != 8 {
		t.Fatalf("document process identity/budget=%d/%q/%+v", def.Version, def.ImplementationRevision, def.Budgets)
	}
}

func TestStudioProcessSourceEntailmentGraphsAreIdentityPinned(t *testing.T) {
	for _, fixture := range []struct {
		name           string
		def            ProcessDefinition
		version        int
		implementation string
		maxSubtasks    int
		resultStage    string
		resultContract string
	}{
		{name: "presentation", def: packagingStudioDefinition(), version: 4, implementation: "packaging_studio.runtime.v4", maxSubtasks: 18, resultStage: "ship_deck", resultContract: packagingStudioDeckContract},
		{name: "document", def: documentReportDefinition(), version: 2, implementation: "document_report.runtime.v2", maxSubtasks: 8, resultStage: "write", resultContract: documentReportOutputContract},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			identity, err := processDefinitionIdentityFor(fixture.def)
			if err != nil {
				t.Fatal(err)
			}
			if identity.Version != fixture.version || identity.ImplementationRevision != fixture.implementation || !isHexDigest(identity.Digest) ||
				identity.ResultStageID != fixture.resultStage || identity.ResultOutputContract != fixture.resultContract || fixture.def.Budgets.MaxSubtasks != fixture.maxSubtasks {
				t.Fatalf("identity=%+v budget=%+v", identity, fixture.def.Budgets)
			}
			plan := &goalPlan{PlanVersion: goalPlanVersion, Authority: codexJobAuthorityWorkspaceWrite}
			pinProcessPlanForTest(t, plan, fixture.def)
			if err := instantiateProcessPlan(fixture.def, plan); err != nil {
				t.Fatal(err)
			}
			for _, stageID := range []string{"source_snapshot", "evidence_entailment"} {
				if plan.subtaskByID(stageID) == nil {
					t.Fatalf("pinned plan omitted %s", stageID)
				}
			}
			changed := cloneProcessDefinition(fixture.def)
			stage, ok := changed.stageByID("evidence_entailment")
			if !ok {
				t.Fatal("entailment stage missing")
			}
			for index := range changed.Stages {
				if changed.Stages[index].ID == stage.ID {
					changed.Stages[index].PromptBody += " changed"
				}
			}
			changedIdentity, err := processDefinitionIdentityFor(changed)
			if err != nil || changedIdentity.Digest == identity.Digest {
				t.Fatalf("entailment behavior was not identity-bound: changed=%+v err=%v", changedIdentity, err)
			}
			compileChanged := cloneProcessDefinition(fixture.def)
			for index := range compileChanged.Stages {
				if compileChanged.Stages[index].ID == "evidence" {
					if compileChanged.Stages[index].Role != processRoleCompile || compileChanged.Stages[index].Compile == nil {
						t.Fatalf("evidence compile binding missing: %+v", compileChanged.Stages[index])
					}
					compileChanged.Stages[index].Compile = testProcessCompileFunc
				}
			}
			compileIdentity, err := processDefinitionIdentityFor(compileChanged)
			if err != nil || compileIdentity.Digest == identity.Digest {
				t.Fatalf("evidence Compile symbol was not identity-bound: changed=%+v err=%v", compileIdentity, err)
			}
		})
	}
}

func TestNativeMarkdownReportContractRejectsWorkflowScaffolding(t *testing.T) {
	stage := ProcessStage{OutputContract: documentReportOutputContract}
	if reason, failed := processStageLawSweep(stage, "Vision: prepare report\n\n## Work decomposition\n- research"); !failed || !strings.Contains(reason, "editable Markdown document") {
		t.Fatalf("non-document passed law sweep: failed=%t reason=%q", failed, reason)
	}
	if reason, failed := processStageLawSweep(stage, "# Western Creator Opportunity\n\nA grounded thesis with [evidence](https://example.com)."); failed {
		t.Fatalf("valid Markdown failed law sweep: %q", reason)
	}
	instructions, ok := rawDocumentContractInstructions(documentReportOutputContract)
	if !ok || !strings.Contains(instructions, "ENTIRE response is the finished, editable Markdown document") || strings.Contains(instructions, "Work decomposition") {
		t.Fatalf("raw document instructions=%q ok=%t", instructions, ok)
	}
}

func TestDocumentReportWriterBypassesGenericWorkflowTemplate(t *testing.T) {
	app := &kanbanBoardApp{}
	thread := scoutAgentThread{Mode: "artifacts", Artifact: meetingMemoryEntry{Metadata: map[string]string{
		"goalDeliverable": "true",
		"goalParentId":    "goal-report",
		"goalSubtaskId":   "write",
		"outputContract":  documentReportOutputContract,
	}}}
	instructions := app.agentThreadInstructionsForThread(thread)
	if !strings.Contains(instructions, "ENTIRE response is the finished, editable Markdown document") {
		t.Fatalf("report writer lost raw-document instructions: %q", instructions)
	}
	for _, forbidden := range []string{"Start with a one-line Vision", "Work decomposition", "Comparable Companies", "at least five actually used sources"} {
		if strings.Contains(instructions, forbidden) {
			t.Fatalf("generic workstream contract %q leaked into report writer instructions: %q", forbidden, instructions)
		}
	}
}

func TestValidateProcessDefinitionRejectsBadShapes(t *testing.T) {
	mutate := func(change func(*ProcessDefinition)) ProcessDefinition {
		def := validProcessProbeLikeDefinition("process_case")
		change(&def)
		return def
	}
	cases := []struct {
		name    string
		def     ProcessDefinition
		wantErr string
	}{
		{"valid", validProcessProbeLikeDefinition("process_case"), ""},
		{"empty id", mutate(func(d *ProcessDefinition) { d.ID = "" }), "no id"},
		{"uppercase id", mutate(func(d *ProcessDefinition) { d.ID = "Process_Case" }), "lowercase"},
		{"version zero", mutate(func(d *ProcessDefinition) { d.Version = 0 }), "version"},
		{"no implementation revision", mutate(func(d *ProcessDefinition) { d.ImplementationRevision = "" }), "implementation revision"},
		{"no title", mutate(func(d *ProcessDefinition) { d.Title = " " }), "no title"},
		{"no stages", mutate(func(d *ProcessDefinition) { d.Stages = nil }), "no stages"},
		{"duplicate stage ids", mutate(func(d *ProcessDefinition) {
			d.Stages = append(d.Stages, ProcessStage{ID: "w1", Title: "Again", Role: processRoleWriter})
		}), "duplicate stage id"},
		{"unknown inputFrom", mutate(func(d *ProcessDefinition) {
			d.Stages[1].InputFrom = []string{"never_authored"}
		}), "does not name an earlier stage"},
		{"inputFrom names a LATER stage", mutate(func(d *ProcessDefinition) {
			d.Stages[0].InputFrom = []string{"g1"}
		}), "does not name an earlier stage"},
		{"self inputFrom", mutate(func(d *ProcessDefinition) {
			d.Stages[0].InputFrom = []string{"w1"}
		}), "does not name an earlier stage"},
		{"unknown role", mutate(func(d *ProcessDefinition) { d.Stages[0].Role = "director" }), "unknown role"},
		{"valid authored condition", mutate(func(d *ProcessDefinition) {
			d.Stages[1].RunIf = &ProcessStageCondition{StageID: "w1", Field: "research_mode", Equals: "external"}
		}), ""},
		{"condition source must be earlier", mutate(func(d *ProcessDefinition) {
			d.Stages[1].RunIf = &ProcessStageCondition{StageID: "missing", Field: "research_mode", Equals: "external"}
		}), "runIf stage"},
		{"condition source must be an input", mutate(func(d *ProcessDefinition) {
			d.Stages = append(d.Stages, ProcessStage{ID: "w2", Title: "Conditional", Role: processRoleWriter, InputFrom: []string{"g1"}, RunIf: &ProcessStageCondition{StageID: "w1", Field: "research_mode", Equals: "external"}})
		}), "must also be listed in inputFrom"},
		{"condition field cannot be empty", mutate(func(d *ProcessDefinition) {
			d.Stages[1].RunIf = &ProcessStageCondition{StageID: "w1", Equals: "external"}
		}), "non-empty field"},
		{"writer with bad mode", mutate(func(d *ProcessDefinition) { d.Stages[0].Mode = "interpretive_dance" }), "invalid mode"},
		{"panel without personas", mutate(func(d *ProcessDefinition) {
			d.Stages[0] = ProcessStage{ID: "w1", Title: "Panel", Role: processRolePanel}
		}), "no personas"},
		{"persona missing system", mutate(func(d *ProcessDefinition) {
			d.Stages[0] = ProcessStage{ID: "w1", Title: "Panel", Role: processRolePanel, Personas: []ProcessPersona{{Name: "Judge"}}}
		}), "missing name/system"},
		{"gate without inputFrom", mutate(func(d *ProcessDefinition) { d.Stages[1].InputFrom = nil }), "gate"},
		{"valid gate repair target", mutate(func(d *ProcessDefinition) {
			d.Stages[1].GateSpec = &ProcessGateSpec{RepairTarget: "w1", HoldOnFailure: true}
		}), ""},
		{"gate repair target must be scored", mutate(func(d *ProcessDefinition) {
			d.Stages[1].GateSpec = &ProcessGateSpec{RepairTarget: "missing", HoldOnFailure: true}
		}), "repairTarget"},
		{"render without inputFrom", mutate(func(d *ProcessDefinition) {
			d.Stages[1] = ProcessStage{ID: "g1", Title: "Render", Role: processRoleRender}
		}), "render"},
		{"valid compile stage", mutate(func(d *ProcessDefinition) {
			d.Stages[1] = ProcessStage{ID: "g1", Title: "Compile", Role: processRoleCompile, InputFrom: []string{"w1"}, Compile: testProcessCompileFunc}
		}), ""},
		{"compile without inputFrom", mutate(func(d *ProcessDefinition) {
			d.Stages[1] = ProcessStage{ID: "g1", Title: "Compile", Role: processRoleCompile, Compile: testProcessCompileFunc}
		}), "compile"},
		{"compile without a compiler function", mutate(func(d *ProcessDefinition) {
			d.Stages[1] = ProcessStage{ID: "g1", Title: "Compile", Role: processRoleCompile, InputFrom: []string{"w1"}}
		}), "Compile function"},
		{"checkpoint without question", mutate(func(d *ProcessDefinition) {
			d.Stages[1] = ProcessStage{ID: "g1", Title: "Pick", Role: processRoleHumanCheckpoint, CheckpointSpec: &ProcessCheckpointSpec{}}
		}), "no question"},
		{"checkpoint optionsFrom names a later stage", mutate(func(d *ProcessDefinition) {
			d.Stages[1] = ProcessStage{ID: "g1", Title: "Pick", Role: processRoleHumanCheckpoint, InputFrom: []string{"w1"},
				CheckpointSpec: &ProcessCheckpointSpec{Question: "Which?", OptionsFrom: "g1"}}
		}), "optionsFrom"},
		{"valid checkpoint option actions", mutate(func(d *ProcessDefinition) {
			d.Stages[1] = ProcessStage{ID: "g1", Title: "Pick", Role: processRoleHumanCheckpoint, InputFrom: []string{"w1"},
				CheckpointSpec: &ProcessCheckpointSpec{Question: "Which?", Options: []ProcessCheckpointOption{
					{Label: "ship it"},
					{Label: "send back", Action: processCheckpointActionRevise, Target: "w1"},
					{Label: "hold it", Action: processCheckpointActionHold},
				}}}
		}), ""},
		{"checkpoint options exceed the public bound", mutate(func(d *ProcessDefinition) {
			d.Stages[1] = ProcessStage{ID: "g1", Title: "Pick", Role: processRoleHumanCheckpoint, InputFrom: []string{"w1"},
				CheckpointSpec: &ProcessCheckpointSpec{Question: "Which?", Options: []ProcessCheckpointOption{
					{Label: "one"}, {Label: "two"}, {Label: "three"}, {Label: "four"},
				}}}
		}), "maximum is 3"},
		{"checkpoint option labels collide after normalization", mutate(func(d *ProcessDefinition) {
			d.Stages[1] = ProcessStage{ID: "g1", Title: "Pick", Role: processRoleHumanCheckpoint, InputFrom: []string{"w1"},
				CheckpointSpec: &ProcessCheckpointSpec{Question: "Which?", Options: []ProcessCheckpointOption{{Label: "Ship"}, {Label: " ship "}}}}
		}), "duplicate option"},
		{"checkpoint option label cannot be truncated", mutate(func(d *ProcessDefinition) {
			d.Stages[1] = ProcessStage{ID: "g1", Title: "Pick", Role: processRoleHumanCheckpoint, InputFrom: []string{"w1"},
				CheckpointSpec: &ProcessCheckpointSpec{Question: "Which?", Options: []ProcessCheckpointOption{{Label: strings.Repeat("x", processCheckpointMaxLabelRunes+1)}}}}
		}), "exceeds 160 characters"},
		{"checkpoint option without a label", mutate(func(d *ProcessDefinition) {
			d.Stages[1] = ProcessStage{ID: "g1", Title: "Pick", Role: processRoleHumanCheckpoint, InputFrom: []string{"w1"},
				CheckpointSpec: &ProcessCheckpointSpec{Question: "Which?", Options: []ProcessCheckpointOption{{Label: "  "}}}}
		}), "no label"},
		{"checkpoint option with an unknown action", mutate(func(d *ProcessDefinition) {
			d.Stages[1] = ProcessStage{ID: "g1", Title: "Pick", Role: processRoleHumanCheckpoint, InputFrom: []string{"w1"},
				CheckpointSpec: &ProcessCheckpointSpec{Question: "Which?", Options: []ProcessCheckpointOption{{Label: "explode", Action: "explode"}}}}
		}), "unknown action"},
		{"revise option without a target", mutate(func(d *ProcessDefinition) {
			d.Stages[1] = ProcessStage{ID: "g1", Title: "Pick", Role: processRoleHumanCheckpoint, InputFrom: []string{"w1"},
				CheckpointSpec: &ProcessCheckpointSpec{Question: "Which?", Options: []ProcessCheckpointOption{{Label: "send back", Action: processCheckpointActionRevise}}}}
		}), "no target"},
		{"revise option targeting a stage the human was not shown", mutate(func(d *ProcessDefinition) {
			d.Stages = append(d.Stages, ProcessStage{ID: "pick", Title: "Pick", Role: processRoleHumanCheckpoint, InputFrom: []string{"g1"},
				CheckpointSpec: &ProcessCheckpointSpec{Question: "Which?", Options: []ProcessCheckpointOption{{Label: "send back", Action: processCheckpointActionRevise, Target: "w1"}}}})
		}), "not one of the stage's inputFrom"},
		{"target on a non-revise option", mutate(func(d *ProcessDefinition) {
			d.Stages[1] = ProcessStage{ID: "g1", Title: "Pick", Role: processRoleHumanCheckpoint, InputFrom: []string{"w1"},
				CheckpointSpec: &ProcessCheckpointSpec{Question: "Which?", Options: []ProcessCheckpointOption{{Label: "ship it", Target: "w1"}}}}
		}), "without the revise action"},
		{"more stages than the default budget", mutate(func(d *ProcessDefinition) {
			d.Stages = nil
			for i := 0; i < goalMaxSubtasks+1; i++ {
				d.Stages = append(d.Stages, ProcessStage{ID: fmt.Sprintf("w%d", i+1), Title: "W", Role: processRoleWriter})
			}
		}), "budget allows"},
	}
	for _, tc := range cases {
		err := validateProcessDefinition(tc.def)
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error: %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err=%v, want it to mention %q", tc.name, err, tc.wantErr)
		}
	}

	// The same over-budget shape validates once the budget admits it — the
	// override is the authored budget, never a loosened validator.
	over := validProcessProbeLikeDefinition("process_case")
	over.Stages = nil
	for i := 0; i < goalMaxSubtasks+1; i++ {
		over.Stages = append(over.Stages, ProcessStage{ID: fmt.Sprintf("w%d", i+1), Title: "W", Role: processRoleWriter})
	}
	over.Budgets = ProcessBudgets{MaxSubtasks: goalMaxSubtasks + 1}
	if err := validateProcessDefinition(over); err != nil {
		t.Fatalf("budgeted stage count should validate: %v", err)
	}
}

// --- Registry -------------------------------------------------------------------

func TestRegisterProcessDefinitionRefusesDuplicatesAndInvalid(t *testing.T) {
	def := validProcessProbeLikeDefinition("process_register_case")
	registerProcessDefinitionForTest(t, def)

	if err := registerProcessDefinition(def); err == nil {
		t.Fatal("re-registering the same process id must be refused")
	}
	if err := registerProcessDefinition(validProcessProbeLikeDefinition("process_probe")); err == nil {
		t.Fatal("registering over a builtin id must be refused")
	}
	invalid := validProcessProbeLikeDefinition("process_invalid_case")
	invalid.Stages = nil
	if err := registerProcessDefinition(invalid); err == nil {
		t.Fatal("an invalid definition must never register")
	}
	if _, ok := processByID("process_invalid_case"); ok {
		t.Fatal("the refused definition leaked into the registry")
	}

	// Case-normalized lookup, mirroring toolByID.
	if _, ok := processByID("  Process_Register_Case  "); !ok {
		t.Fatal("processByID must trim and lowercase like toolByID")
	}
}

// The compile role executes inline (one engine step, no child thread) — the
// same execution class as panel/gate/render; only writer dispatches.
func TestProcessCompileRoleIsInline(t *testing.T) {
	if !processStageRoleIsInline(processRoleCompile) {
		t.Fatal("compile must execute inline — it never dispatches a child thread")
	}
	if processStageRoleIsInline(processRoleWriter) {
		t.Fatal("writer must dispatch — it is the only non-inline role")
	}
}

// Processes are NOT tools: the 12-tool registry never resolves a process id,
// so a stray process id through a tool-only door stays a plain goal.
func TestProcessIDsDoNotResolveAsTools(t *testing.T) {
	if _, ok := toolByID("process_probe"); ok {
		t.Fatal("toolByID resolved a process id — the taxonomies must stay separate")
	}
	if got := normalizeToolTemplate("process_probe"); got != "" {
		t.Fatalf("normalizeToolTemplate(process_probe)=%q, want \"\"", got)
	}
}

// --- Instantiation ---------------------------------------------------------------

func TestInstantiateProcessPlanMapsStagesInOrder(t *testing.T) {
	def, ok := processByID("process_probe")
	if !ok {
		t.Fatal("process_probe missing")
	}
	plan := &goalPlan{PlanVersion: goalPlanVersion, ProcessID: def.ID, Authority: codexJobAuthorityWorkspaceWrite, State: goalStateDecompose, routeVerified: true}
	pinProcessPlanForTest(t, plan, def)
	if err := instantiateProcessPlan(def, plan); err != nil {
		t.Fatalf("instantiateProcessPlan: %v", err)
	}
	if len(plan.Subtasks) != len(def.Stages) {
		t.Fatalf("plan has %d subtasks, want one per stage (%d)", len(plan.Subtasks), len(def.Stages))
	}
	for index, stage := range def.Stages {
		st := plan.Subtasks[index]
		if st.ID != stage.ID {
			t.Fatalf("subtask %d id=%q, want stage %q — stages instantiate IN ORDER", index, st.ID, stage.ID)
		}
		if st.Role != stage.Role {
			t.Fatalf("subtask %s role=%q, want %q", st.ID, st.Role, stage.Role)
		}
		if st.Status != subtaskPending {
			t.Fatalf("subtask %s status=%q, want pending", st.ID, st.Status)
		}
		if len(st.DependsOn) != len(stage.InputFrom) {
			t.Fatalf("subtask %s dependsOn=%v, want the stage's inputFrom %v", st.ID, st.DependsOn, stage.InputFrom)
		}
	}
	// The gate depends on the draft; the checkpoint on the gate.
	if plan.Subtasks[1].DependsOn[0] != "draft" || plan.Subtasks[2].DependsOn[0] != "note_gate" {
		t.Fatalf("dependency mapping broken: %+v", plan.Subtasks)
	}
	if plan.ProcessVersion != def.Version || len(plan.ProcessDigest) != 64 || plan.ResultStageID != "draft" || plan.ResultOutputContract != "probe_note_v1" {
		t.Fatalf("instantiation did not persist immutable process/result bindings: version=%d digest=%q stage=%q contract=%q", plan.ProcessVersion, plan.ProcessDigest, plan.ResultStageID, plan.ResultOutputContract)
	}
}

func TestPersistedProcessResultBindingSurvivesRegistryRemoval(t *testing.T) {
	def := validProcessProbeLikeDefinition("process_removed_after_instantiation")
	def.Version = 7
	def.Stages[0].OutputContract = "removed_process_report_v1"
	plan := &goalPlan{PlanVersion: goalPlanVersion, ProcessID: def.ID, Authority: codexJobAuthorityWorkspaceWrite, State: goalStateDecompose, routeVerified: true}
	if _, registered := processByID(def.ID); registered {
		t.Fatal("fixture process must never enter the live registry")
	}
	pinProcessPlanForTest(t, plan, def)
	if err := instantiateProcessPlan(def, plan); err != nil {
		t.Fatalf("instantiate unregistered process: %v", err)
	}
	if got := goalDeliverableSubtaskID(plan); got != "w1" {
		t.Fatalf("persisted result stage=%q, want w1 without a registry lookup", got)
	}
	document := meetingMemoryEntry{ID: "report-v1", Metadata: map[string]string{
		"type": artifactTypeMarkdown, "source": "scout_thread", "goalParentId": "goal-v1", "goalSubtaskId": "w1",
		"goalDeliverable": "true", "outputContract": "removed_process_report_v1",
	}}
	if !scoutChatDocumentBelongsToGoal(document, "goal-v1", *plan) {
		t.Fatal("persisted result contract stopped resolving after registry removal")
	}
	document.Metadata["outputContract"] = "registry_drifted_contract_v2"
	if scoutChatDocumentBelongsToGoal(document, "goal-v1", *plan) {
		t.Fatal("a later contract drift replaced the instantiated result binding")
	}
}

func TestProcessCheckpointOptionsFromText(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"plain array", `["direction-a","direction-b"]`, []string{"direction-a", "direction-b"}},
		{"array inside prose", "The three directions:\n[\"neon\", \"dust\", \"bone\"]\npick one.", []string{"neon", "dust", "bone"}},
		{"garbage degrades to nil", "no options here", nil},
		{"malformed json degrades to nil", `["unterminated`, nil},
		{"empty strings dropped", `[" ", "keep"]`, []string{"keep"}},
		{"more than the public bound rejected", `["one", "two", "three", "four"]`, nil},
		{"normalized duplicate rejected", `["Ship", " ship "]`, nil},
		{"oversized label rejected instead of truncated", fmt.Sprintf(`["%s"]`, strings.Repeat("x", processCheckpointMaxLabelRunes+1)), nil},
	}
	for _, tc := range cases {
		got := processCheckpointOptionsFromText(tc.text)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
			continue
		}
		for index := range got {
			if got[index] != tc.want[index] {
				t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
				break
			}
		}
	}
}

// --- The fifth payload group (additive) ---------------------------------------

func TestProcessesServeAsFifthPayloadGroupAdditive(t *testing.T) {
	visible := validProcessProbeLikeDefinition("process_visible_case")
	visible.Hidden = false
	visible.Title = "Visible Case"
	visible.Description = "A test process the payload should serve."
	registerProcessDefinitionForTest(t, visible)

	groups := buildToolsPayload()
	if len(groups) != 5 {
		t.Fatalf("payload has %d groups, want the flagship processes group + 4 tool groups", len(groups))
	}
	// Wave A item 4: the processes group now LEADS the payload under "End-to-end".
	first := groups[0]
	if first.ID != toolGroupProcesses || first.Label != "End-to-end" {
		t.Fatalf("first group=%q/%q, want processes/End-to-end", first.ID, first.Label)
	}

	// Additive: the four tool groups and their 12 tools are untouched (now
	// trailing the flagship group), and no process shadows a tool id.
	toolCount := 0
	for _, group := range groups[1:] {
		toolCount += len(group.Tools)
	}
	if toolCount != 12 {
		t.Fatalf("the four lifecycle groups carry %d tools, want 12 — processes must be additive", toolCount)
	}
	var served *packagingTool
	for index := range first.Tools {
		entry := &first.Tools[index]
		if entry.ID == "process_probe" {
			t.Fatal("hidden process_probe served in the public payload")
		}
		if _, isTool := toolByID(entry.ID); isTool {
			t.Fatalf("process entry %q shadows a tool id", entry.ID)
		}
		if entry.ID == visible.ID {
			served = entry
		}
	}
	if served == nil {
		t.Fatalf("visible process %q missing from the processes group: %+v", visible.ID, first.Tools)
	}
	// The tile contract the palette enforces on every entry.
	if served.Group != toolGroupProcesses || served.Name != "Visible Case" || strings.TrimSpace(served.Promise) == "" {
		t.Fatalf("process tile shape broken: %+v", served)
	}
	if served.InputMode != toolInputConversational || len(served.FormFields) != 0 {
		t.Fatalf("process entries must be conversational with no form fields: %+v", served)
	}
	if strings.TrimSpace(served.Authority) == "" {
		t.Fatalf("process entry has no authority class: %+v", served)
	}

	// The router's injected enum therefore proposes the process id like any
	// tool id — and never the hidden probe.
	routerTools := scoutRouterTools()
	if len(routerTools) == 0 {
		t.Fatal("scoutRouterTools returned nothing")
	}
	schema, ok := routerTools[0].InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("propose_tool_run schema shape changed: %+v", routerTools[0].InputSchema)
	}
	toolID, ok := schema["tool_id"].(map[string]any)
	if !ok {
		t.Fatalf("propose_tool_run tool_id schema missing: %+v", schema)
	}
	enum, ok := toolID["enum"].([]string)
	if !ok {
		t.Fatalf("propose_tool_run enum shape changed: %+v", toolID)
	}
	foundVisible := false
	for _, id := range enum {
		if id == "process_probe" {
			t.Fatal("hidden process_probe leaked into the router enum")
		}
		if id == visible.ID {
			foundVisible = true
		}
	}
	if !foundVisible {
		t.Fatalf("router enum missing the visible process id: %v", enum)
	}
}

// Keyless/empty degradation: with no visible processes the fifth group still
// serves (empty), so the palette renders and the router enum is just the 12.
func TestProcessesGroupServesEmptyWithoutRegistrations(t *testing.T) {
	groups := buildToolsPayload()
	// Wave A item 4: the processes group now LEADS the payload.
	first := groups[0]
	if first.ID != toolGroupProcesses {
		t.Fatalf("first group=%q, want processes", first.ID)
	}
	for _, entry := range first.Tools {
		if entry.ID == "process_probe" {
			t.Fatal("hidden process_probe served in the public payload")
		}
	}
	if first.Tools == nil {
		t.Fatal("processes group must serve an empty list, not null — the palette iterates it")
	}
}

// The first live packaging run completed ship_deck with a markdown
// DESCRIPTION of the deck — processStageLawSweep is the zero-cost guard that
// makes that impossible.
func TestProcessStageLawSweepDemandsRealDeckHTML(t *testing.T) {
	deckStage := ProcessStage{ID: "ship_deck", OutputContract: "packaging_deck_v1"}
	fullBleed := `<!doctype html><html><body><div id="stage"><section class="pg on" data-deck-slide="slide-1" style="background:#101014"><figure class="image-plate bleed fig-1" data-deck-element="hero-image" data-deck-type="image" style="position:absolute;left:0;top:0;width:1920px;height:1080px;z-index:1;opacity:1;transform:rotate(0deg);object-fit:cover;margin:0"><div class="ph"></div><figcaption data-deck-element="caption" data-deck-type="text" style="position:absolute;left:96px;top:990px;width:1200px;height:36px;z-index:3;opacity:1;transform:rotate(0deg);font-size:22px;font-family:Arial;font-weight:600;color:#ffffff">FIG. 1 — Field at dawn.</figcaption></figure></section></div></body></html>`
	cases := []struct {
		name    string
		body    string
		violate bool
	}{
		{"markdown description", "# packaging_deck_v1 — SHIPPED\n\n## Vision\nShip the deck.", true},
		{"truncated html", "<!doctype html><html><body><h1>deck", true},
		{"render-only html is not faithfully editable", "<!doctype html><html><body><section>slide</section></body></html>", true},
		{"real editable deck", faithfulDeckHTML, false},
		{"native-importable full-bleed image slot", fullBleed, false},
		{"leading whitespace ok", "\n\n  " + faithfulDeckHTML, false},
	}
	for _, tc := range cases {
		_, violated := processStageLawSweep(deckStage, tc.body)
		if violated != tc.violate {
			t.Errorf("%s: violated=%v, want %v", tc.name, violated, tc.violate)
		}
	}
	if _, violated := processStageLawSweep(ProcessStage{ID: "write", OutputContract: "deck_copy_v1"}, "# markdown is fine here"); violated {
		t.Error("non-deck contracts must not be swept by the deck rule")
	}
}

func TestProcessStageLawSweepRejectsDeckBeyondNativeSlideBound(t *testing.T) {
	var body strings.Builder
	body.WriteString(`<!doctype html><html><body><div id="stage">`)
	for index := 0; index <= deckDocumentMaxSlides; index++ {
		fmt.Fprintf(&body, `<section class="pg" data-deck-slide="slide-%d" style="background:#101014"><div data-deck-element="headline-%d" data-deck-type="text" style="position:absolute;left:96px;top:96px;width:1200px;height:180px;z-index:2;opacity:1;font-size:88px;font-family:Arial;font-weight:700;color:#ffffff">Slide %d</div></section>`, index+1, index+1, index+1)
	}
	body.WriteString(`</div></body></html>`)
	if _, violated := processStageLawSweep(ProcessStage{ID: "ship_deck", OutputContract: "packaging_deck_v1"}, body.String()); !violated {
		t.Fatal("over-bound authored deck passed by silently truncating slides")
	}
}

// The rendered quality gate sends executable repairs back to ship_deck and
// fails closed after its bounded rounds; a bad deck never reaches delivery as
// an advisory warning.
func TestPackagingStudioRenderedQualityGateRepairsOrHolds(t *testing.T) {
	def, ok := processByID("packaging_studio")
	if !ok {
		t.Fatal("packaging_studio not registered")
	}
	stage, ok := def.stageByID("quality_gate")
	if !ok {
		t.Fatal("quality_gate stage missing")
	}
	if stage.GateSpec == nil || stage.GateSpec.RepairTarget != "ship_deck" || !stage.GateSpec.HoldOnFailure || stage.GateSpec.ForceAccept {
		t.Fatalf("quality_gate must repair ship_deck then hold, never force-accept: %+v", stage.GateSpec)
	}
}
