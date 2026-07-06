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

// testProcessCompileFunc is a no-op compiler for validation-shape tests.
func testProcessCompileFunc(_ *kanbanBoardApp, _ *goalPlan, _ string, _ ProcessStage) (string, map[string]string, error) {
	return "compiled", nil, nil
}

// validProcessProbeLikeDefinition is a minimal valid definition tests mutate.
func validProcessProbeLikeDefinition(id string) ProcessDefinition {
	return ProcessDefinition{
		ID:          id,
		Version:     1,
		Title:       "Test Process",
		Description: "Test-only process definition.",
		Authority:   toolAuthorityWorkspaceWrite,
		Hidden:      true,
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
		{"writer with bad mode", mutate(func(d *ProcessDefinition) { d.Stages[0].Mode = "interpretive_dance" }), "invalid mode"},
		{"panel without personas", mutate(func(d *ProcessDefinition) {
			d.Stages[0] = ProcessStage{ID: "w1", Title: "Panel", Role: processRolePanel}
		}), "no personas"},
		{"persona missing system", mutate(func(d *ProcessDefinition) {
			d.Stages[0] = ProcessStage{ID: "w1", Title: "Panel", Role: processRolePanel, Personas: []ProcessPersona{{Name: "Judge"}}}
		}), "missing name/system"},
		{"gate without inputFrom", mutate(func(d *ProcessDefinition) { d.Stages[1].InputFrom = nil }), "gate"},
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
	plan := &goalPlan{PlanVersion: goalPlanVersion, ProcessID: def.ID, Authority: codexJobAuthorityWorkspaceWrite, State: goalStateDecompose}
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
		t.Fatalf("payload has %d groups, want 4 tool groups + the processes group", len(groups))
	}
	last := groups[len(groups)-1]
	if last.ID != toolGroupProcesses || last.Label != "Processes" {
		t.Fatalf("fifth group=%q/%q, want processes/Processes", last.ID, last.Label)
	}

	// Additive: the four tool groups and their 12 tools are untouched, and no
	// process shadows a tool id.
	toolCount := 0
	for _, group := range groups[:4] {
		toolCount += len(group.Tools)
	}
	if toolCount != 12 {
		t.Fatalf("the four lifecycle groups carry %d tools, want 12 — processes must be additive", toolCount)
	}
	var served *packagingTool
	for index := range last.Tools {
		entry := &last.Tools[index]
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
		t.Fatalf("visible process %q missing from the processes group: %+v", visible.ID, last.Tools)
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
	last := groups[len(groups)-1]
	if last.ID != toolGroupProcesses {
		t.Fatalf("fifth group=%q, want processes", last.ID)
	}
	for _, entry := range last.Tools {
		if entry.ID == "process_probe" {
			t.Fatal("hidden process_probe served in the public payload")
		}
	}
	if last.Tools == nil {
		t.Fatal("processes group must serve an empty list, not null — the palette iterates it")
	}
}

// The first live packaging run completed ship_deck with a markdown
// DESCRIPTION of the deck — processStageLawSweep is the zero-cost guard that
// makes that impossible.
func TestProcessStageLawSweepDemandsRealDeckHTML(t *testing.T) {
	deckStage := ProcessStage{ID: "ship_deck", OutputContract: "packaging_deck_v1"}
	cases := []struct {
		name    string
		body    string
		violate bool
	}{
		{"markdown description", "# packaging_deck_v1 — SHIPPED\n\n## Vision\nShip the deck.", true},
		{"truncated html", "<!doctype html><html><body><h1>deck", true},
		{"real deck", "<!doctype html><html><body><section>slide</section></body></html>", false},
		{"leading whitespace ok", "\n\n  <!DOCTYPE HTML><html><body>x</body></html>", false},
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

// ship_approval carries a send-back option targeting ship_deck (the first
// live run proved a bad deck could reach the final park with no way back).
func TestPackagingStudioShipApprovalHasSendBack(t *testing.T) {
	def, ok := processByID("packaging_studio")
	if !ok {
		t.Fatal("packaging_studio not registered")
	}
	stage, ok := def.stageByID("ship_approval")
	if !ok {
		t.Fatal("ship_approval stage missing")
	}
	foundRevise := false
	for _, option := range stage.CheckpointSpec.Options {
		if option.Action == processCheckpointActionRevise && option.Target == "ship_deck" {
			foundRevise = true
		}
	}
	if !foundRevise {
		t.Fatalf("ship_approval options carry no revise→ship_deck send-back: %+v", stage.CheckpointSpec.Options)
	}
}
