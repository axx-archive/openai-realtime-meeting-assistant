package main

// The eval harness that gates shipping the 12-tool suite (Spectacular OS design
// §3 "Output-quality trust"). Two tiers, both run offline in `go test` and are
// the ship gate for any deploy touching prompts or the engine:
//
//   GOLDEN evals (the 3 exemplars): a fabricated package with known receipts is
//   run through the real prompt assembly (no live model call) — the assembled
//   prompt must carry the goal, the filled grounding slots, the contract
//   headings, and the rubric; then a seeded kill-condition violation is fed to
//   the engine's review scoring with a faked model verdict, asserting the kill
//   condition text reaches the reviewer prompt and a fail verdict flows through.
//
//   CHECKLIST evals (all 12): registry ids unique, groups/stages/authority/
//   inputMode well-formed, rubric bars sane, and every tool body carries its
//   contract headings + rubric dimensions + kill condition. The load-bearing
//   research_brief_v2 / grill_scorecard_v2 / READINESS contracts are asserted
//   byte-preserved.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- CHECKLIST evals (all 12) ------------------------------------------------

func TestToolRegistryChecklistEvals(t *testing.T) {
	tools := packagingTools()
	if len(tools) != 12 {
		t.Fatalf("registry has %d tools, want 12", len(tools))
	}

	validStage := map[string]bool{}
	for _, s := range packageStages {
		validStage[s] = true
	}
	validGroup := map[string]bool{
		toolGroupIdeate: true, toolGroupPackage: true, toolGroupMarket: true, toolGroupPortfolio: true,
	}

	seenID := map[string]bool{}
	for _, tool := range tools {
		t.Run(tool.ID, func(t *testing.T) {
			if strings.TrimSpace(tool.ID) == "" {
				t.Fatal("tool id is empty")
			}
			if seenID[tool.ID] {
				t.Fatalf("duplicate tool id %q", tool.ID)
			}
			seenID[tool.ID] = true

			if !validGroup[tool.Group] {
				t.Fatalf("invalid group %q", tool.Group)
			}
			if toolGroupLabels[tool.Group] == "" {
				t.Fatalf("group %q has no display label", tool.Group)
			}
			if strings.TrimSpace(tool.Name) == "" || strings.TrimSpace(tool.Promise) == "" {
				t.Fatal("tool needs a name and a promise")
			}

			// Stage mappings valid against packageStages.
			if len(tool.Stages) == 0 {
				t.Fatal("tool serves no package stage")
			}
			for _, s := range tool.Stages {
				if !validStage[s] {
					t.Fatalf("stage %q is not a package stage", s)
				}
			}

			// Authority.
			if tool.Authority != toolAuthorityReadOnly && tool.Authority != toolAuthorityWorkspaceWrite {
				t.Fatalf("invalid authority %q", tool.Authority)
			}

			// Input mode + form fields (1-3 well-formed fields for form tools).
			switch tool.InputMode {
			case toolInputForm:
				if n := len(tool.FormFields); n < 1 || n > 3 {
					t.Fatalf("form tool has %d fields, want 1-3", n)
				}
				for _, f := range tool.FormFields {
					if strings.TrimSpace(f.Key) == "" || strings.TrimSpace(f.Label) == "" {
						t.Fatalf("form field malformed: %+v", f)
					}
				}
			case toolInputConversational:
				if len(tool.FormFields) != 0 {
					t.Fatalf("conversational tool should carry no form fields, has %d", len(tool.FormFields))
				}
			default:
				t.Fatalf("invalid inputMode %q", tool.InputMode)
			}

			// Rubric: 3-5 dimensions, bars in 1..10, ref + kill non-empty.
			if strings.TrimSpace(tool.Rubric.Ref) == "" {
				t.Fatal("rubric ref is empty")
			}
			if n := len(tool.Rubric.Dimensions); n < 3 || n > 5 {
				t.Fatalf("rubric has %d dimensions, want 3-5", n)
			}
			for _, d := range tool.Rubric.Dimensions {
				if strings.TrimSpace(d.Name) == "" || strings.TrimSpace(d.Measures) == "" {
					t.Fatalf("rubric dimension malformed: %+v", d)
				}
				if d.Bar < 1 || d.Bar > 10 {
					t.Fatalf("rubric dimension %q bar=%d out of 1..10", d.Name, d.Bar)
				}
			}
			if strings.TrimSpace(tool.Rubric.KillCondition) == "" {
				t.Fatal("kill condition is empty")
			}
			if tool.KillCondition() != tool.Rubric.KillCondition {
				t.Fatal("KillCondition() must mirror the rubric")
			}

			// The assembled prompt must carry the contract headings, every rubric
			// dimension name, and the kill condition — the body and the structured
			// registry never drift.
			prompt := assembleToolPrompt(tool, toolPromptContext{})
			for _, heading := range toolContractHeadings[tool.Contract] {
				if !strings.Contains(prompt, heading) {
					t.Fatalf("contract %q body missing required heading %q", tool.Contract, heading)
				}
			}
			for _, d := range tool.Rubric.Dimensions {
				if !strings.Contains(prompt, d.Name) {
					t.Fatalf("body missing rubric dimension %q", d.Name)
				}
			}
			if !strings.Contains(prompt, "kill_condition") {
				t.Fatal("body missing a kill_condition marker")
			}
			// The review instruction the engine runs must carry the kill condition
			// verbatim (this is what reaches the gate reviewer).
			if !strings.Contains(toolReviewInstruction(tool), tool.Rubric.KillCondition) {
				t.Fatal("review instruction dropped the kill condition")
			}
		})
	}
}

// TestToolContractsPreserved pins the load-bearing contracts other code parses.
func TestToolContractsPreserved(t *testing.T) {
	// research_brief_v2 headings survive on every tool that rides research mode.
	researchHeadings := toolContractHeadings["research_brief_v2"]
	for _, tool := range packagingTools() {
		if tool.Contract != "research_brief_v2" {
			continue
		}
		body := toolPromptBody(tool.ID)
		for _, h := range researchHeadings {
			if !strings.Contains(body, h) {
				t.Fatalf("%s dropped research_brief_v2 heading %q", tool.ID, h)
			}
		}
		if !strings.Contains(body, "Search tags") {
			t.Fatalf("%s dropped the research_brief_v2 Search tags line", tool.ID)
		}
	}

	// grill_scorecard_v2 keeps the machine-parsed READINESS first-line format,
	// and packageGrillScoreRE still matches it.
	grill := toolPromptBody("grill_pressure_test")
	if !strings.Contains(grill, "READINESS: <score>/10") {
		t.Fatal("grill body dropped the READINESS line format")
	}
	if !packageGrillScoreRE.MatchString("READINESS: 6.5/10") {
		t.Fatal("packageGrillScoreRE no longer matches the READINESS format")
	}
}

// --- GOLDEN evals (the 3 exemplars) ------------------------------------------

// goldenPackageContext is the fabricated package with KNOWN receipts every
// golden eval grounds against — inline so the eval has no external fixture.
func goldenPackageContext(goal string) toolPromptContext {
	return toolPromptContext{
		GoalStatement:     goal,
		Actor:             "aj@shareability.com",
		PackageName:       "Aurora",
		Audience:          "a capital investor",
		SuccessCriteria:   "the output is data-room ready and every claim carries a receipt",
		PackageArtifacts:  "- Aurora market map: streaming sci-fi is consolidating; whitespace in YA-adjacent serialized IP.\n- Aurora comps brief: Project Hail Mary optioned for $3M (Deadline, 2022) — comparable format and audience.",
		RelevantDecisions: "- decision:aurora-price-75k — the studio prices the Aurora option at $75k.",
		RelevantArtifacts: "- Aurora research brief\n- Aurora rights map (2 ASSUMED items)",
		RelevantMemory:    "- meeting: June 12 economics scan assumed a 22% CAC.",
	}
}

func assertGoldenAssembly(t *testing.T, toolID string, goal string) packagingTool {
	t.Helper()
	tool, ok := toolByID(toolID)
	if !ok {
		t.Fatalf("tool %q not in registry", toolID)
	}
	ctx := goldenPackageContext(goal)
	prompt := assembleToolPrompt(tool, ctx)

	// The immutable goal reaches the prompt.
	if !strings.Contains(prompt, goal) {
		t.Fatal("assembled prompt is missing the goal statement")
	}
	// Grounding slots are FILLED with the studio's record, not the defaults.
	for _, want := range []string{"Aurora market map", "decision:aurora-price-75k", "June 12 economics scan", "Aurora"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("grounding slot missing %q — the wrapper did not ground in memory", want)
		}
	}
	if strings.Contains(prompt, "(none attached yet)") || strings.Contains(prompt, "(none on record)") {
		t.Fatal("grounding slots fell back to defaults despite a filled context")
	}
	// The contract headings and the rubric are present.
	for _, heading := range toolContractHeadings[tool.Contract] {
		if !strings.Contains(prompt, heading) {
			t.Fatalf("assembled prompt missing contract heading %q", heading)
		}
	}
	if !strings.Contains(prompt, tool.Rubric.Ref) || !strings.Contains(prompt, "kill_condition") {
		t.Fatal("assembled prompt missing the gate rubric")
	}
	return tool
}

// assertSeededFlawReachesReviewer feeds a draft containing a known
// kill-condition violation through the engine's review scoring with a faked
// "fail" verdict, and asserts the tool's kill condition text reached the
// reviewer prompt and the fail verdict flowed through.
func assertSeededFlawReachesReviewer(t *testing.T, app *kanbanBoardApp, tool packagingTool, flawDraft string) {
	t.Helper()

	artifact, _, err := app.createOSArtifactWithMetadata("workflow", "seeded flaw draft", flawDraft, "tester", map[string]string{"mode": "workflow"})
	if err != nil {
		t.Fatalf("seed draft artifact: %v", err)
	}

	engine := newGoalEngine(app)
	var capturedSystem string
	engine.apiKey = func() string { return "test-key" }
	engine.responder = func(_ context.Context, _ string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		capturedSystem = request.System
		// Faked model verdict path: the reviewer fails the seeded flaw.
		return anthropicMessagesResponse{
			StopReason: "end_turn",
			Content:    []json.RawMessage{mockAnthropicTextBlock(`{"verdict":"fail","score":2,"reasons":"kill condition triggered"}`)},
		}, nil
	}

	plan := &goalPlan{Objective: "produce a data-room-ready " + tool.Name, ToolTemplate: tool.ID}
	st := &goalSubtask{ID: "st-1", Title: "produce the " + tool.Name, ArtifactID: artifact.ID}
	verdict, _, _ := engine.reviewOneSubtask(context.Background(), plan, st)

	if !strings.Contains(capturedSystem, tool.Rubric.KillCondition) {
		t.Fatalf("kill condition never reached the reviewer prompt for %s", tool.ID)
	}
	if verdict != goalReviewFail {
		t.Fatalf("seeded flaw produced verdict %q, want fail", verdict)
	}
}

func TestGoldenEvalDeepResearch(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	tool := assertGoldenAssembly(t, "deep_research", "Research whether serialized YA sci-fi is a buyable format in 2026")
	// Kill condition: an invented source / an assumption asserted as fact.
	assertSeededFlawReachesReviewer(t, app, tool,
		"Executive Summary: The market is definitely $40B (source: industry consensus). Thesis: buy now. Evidence: everyone agrees. Sources: none needed.")
}

func TestGoldenEvalOnePager(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	tool := assertGoldenAssembly(t, "one_pager", "Write the Aurora one-pager for a capital investor")
	// Kill condition: a claim on the page with no receipt in the appendix.
	assertSeededFlawReachesReviewer(t, app, tool,
		"Title: Aurora. The Ask: $75k. The Thesis: this will 10x in eighteen months. Sources appendix: (left blank).")
}

func TestGoldenEvalGrill(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	tool := assertGoldenAssembly(t, "grill_pressure_test", "Grill the Aurora pitch as a skeptical investor")
	// Kill condition: missing/malformed READINESS line and generic objections.
	assertSeededFlawReachesReviewer(t, app, tool,
		"Great pitch. Objection: have you considered the competition? Objection: is the market big enough? No readiness score given.")
}

// TestGenerationHopDeliverableSubtaskCarriesToolPrompt is the regression for the
// review's Finding 1: the A++ tool prompt must reach the model that WRITES the
// artifact (the deliverable subtask), not just the decomposer and the reviewer.
func TestGenerationHopDeliverableSubtaskCarriesToolPrompt(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	// A tool-templated plan: a research subtask feeds the one-pager sink.
	plan := &goalPlan{
		ToolTemplate: "one_pager",
		Objective:    "Package the Aurora IP into an investor one-pager",
		Subtasks: []goalSubtask{
			{ID: "st-1", Title: "research the market", Mode: "research", DependsOn: []string{}},
			{ID: "st-2", Title: "write the one-pager", Mode: "artifacts", DependsOn: []string{"st-1"}},
		},
	}
	if got := goalDeliverableSubtaskID(plan); got != "st-2" {
		t.Fatalf("deliverable subtask=%q, want st-2 (the artifacts-mode sink)", got)
	}

	// Seed the parent goal artifact so the child inherits the real goal statement.
	raw, _ := json.Marshal(goalPlan{Objective: plan.Objective, State: goalStateExecute})
	parent, _, err := app.createOSArtifactWithMetadata("workflow", plan.Objective, "goal body", "aj@shareability.com", map[string]string{"mode": "goal", "goalPlan": string(raw)})
	if err != nil {
		t.Fatalf("seed parent goal: %v", err)
	}

	// The deliverable child carries the tool template → the tool prompt reaches
	// the generation instructions, with the parent goal as the immutable goal.
	deliverable := scoutAgentThread{Mode: "artifacts", Query: "write the one-pager", Artifact: meetingMemoryEntry{Metadata: map[string]string{
		"toolTemplate": "one_pager", "goalParentId": parent.ID, "objective": "write the one-pager", "requestedBy": "aj@shareability.com",
	}}}
	prompt, ok := app.toolPromptForThread(deliverable)
	if !ok {
		t.Fatal("deliverable subtask did not receive the tool prompt")
	}
	for _, want := range []string{"Title / Logline", "Sources appendix", "invents NOTHING", plan.Objective} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("generation prompt missing %q — the wrapper never reached the writer", want)
		}
	}
	instr := app.agentThreadInstructionsForThread(deliverable)
	if !strings.Contains(instr, "Sources appendix") {
		t.Fatal("worker instructions dropped the tool contract")
	}
	if strings.Contains(instr, "Work decomposition, Agent assignment") {
		t.Fatal("tool-templated deliverable must not carry the generic workflow headings")
	}

	// An upstream (non-deliverable) subtask keeps the generic per-mode contract.
	upstream := scoutAgentThread{Mode: "research", Query: "research the market", Artifact: meetingMemoryEntry{Metadata: map[string]string{
		"goalParentId": parent.ID, "objective": "research the market",
	}}}
	if _, ok := app.toolPromptForThread(upstream); ok {
		t.Fatal("upstream subtask must NOT receive the tool prompt")
	}
	if got := app.agentThreadInstructionsForThread(upstream); !strings.Contains(got, agentThreadModeContract("research")) {
		t.Fatal("upstream subtask lost its generic per-mode contract")
	}
}

// TestFlywheelWritesFireOnToolTemplatedCompletion proves a tool-templated goal's
// completion fires the flywheel: the artifact attaches to the package (attach),
// lands stamped under its contract (context-index), and a surfaced decision is
// written to the ledger and linked (decision-log).
func TestFlywheelWritesFireOnToolTemplatedCompletion(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	pkg, err := app.createVenturePackage("Aurora", "serialized YA sci-fi", "aj@shareability.com")
	if err != nil {
		t.Fatalf("create package: %v", err)
	}

	installFakeResponder(t, goalResponderRoutes{
		report: `{"changed":"one-pager written","headline":"Aurora one-pager ready","gap":"","next":"share","assumed_claim_count":0,"decision":"price the Aurora option at $75k"}`,
	})
	installFakeChildRunner(t)

	thread, err := app.launchGoalThread(goalLaunchSpec{
		Objective:    "Write the Aurora one-pager",
		CreatedBy:    "aj@shareability.com",
		PackageID:    pkg.ID,
		ToolTemplate: "one_pager",
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)
	waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)

	// context-index: the goal artifact carries its output contract.
	artifact, _ := app.osArtifactByID(thread.Artifact.ID)
	if artifact.Metadata["artifactContract"] != "one_pager_v1" {
		t.Fatalf("goal artifact contract=%q, want one_pager_v1", artifact.Metadata["artifactContract"])
	}

	// attach: the package now lists the goal artifact.
	record, ok := app.venturePackageByID(pkg.ID)
	if !ok {
		t.Fatal("package vanished")
	}
	if !containsString(record.ArtifactIDs, thread.Artifact.ID) {
		t.Fatalf("package artifacts %v missing the goal artifact %s", record.ArtifactIDs, thread.Artifact.ID)
	}

	// decision-log: the surfaced decision is written, linked, and grounds the next tool.
	var decisionID string
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindDecision, 0) {
		if entry.Metadata["source"] == "goal_completion" && entry.Metadata["packageId"] == pkg.ID {
			decisionID = entry.ID
			if !strings.Contains(entry.Text, "$75k") {
				t.Fatalf("decision text=%q, want the $75k price", entry.Text)
			}
		}
	}
	if decisionID == "" {
		t.Fatal("goal completion did not write the surfaced decision to the ledger")
	}
	if !containsString(record.DecisionIDs, decisionID) {
		t.Fatalf("package decisions %v missing the goal decision %s", record.DecisionIDs, decisionID)
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// --- Endpoint + door resolution ----------------------------------------------

func TestAssistantToolsEndpointGuardedOrderedComplete(t *testing.T) {
	setupAuthTestEnv(t)

	// Unauthenticated GET is rejected.
	unauth := httptest.NewRequest(http.MethodGet, "/assistant/tools", nil)
	unauthRec := httptest.NewRecorder()
	assistantToolsHandler(unauthRec, unauth)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status=%d, want 401", unauthRec.Code)
	}

	// Wrong method is rejected.
	badMethod := httptest.NewRequest(http.MethodPost, "/assistant/tools", nil)
	badMethodRec := httptest.NewRecorder()
	assistantToolsHandler(badMethodRec, badMethod)
	if badMethodRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("bad-method status=%d, want 405", badMethodRec.Code)
	}

	// Signed-in GET returns the full menu grouped in lifecycle order.
	req := httptest.NewRequest(http.MethodGet, "/assistant/tools", nil)
	for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	assistantToolsHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	var payload struct {
		OK     bool `json:"ok"`
		Groups []struct {
			ID    string          `json:"id"`
			Label string          `json:"label"`
			Tools []packagingTool `json:"tools"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !payload.OK {
		t.Fatal("payload ok=false")
	}
	wantOrder := []string{toolGroupIdeate, toolGroupPackage, toolGroupMarket, toolGroupPortfolio}
	if len(payload.Groups) != len(wantOrder) {
		t.Fatalf("got %d groups, want %d", len(payload.Groups), len(wantOrder))
	}
	total := 0
	for i, group := range payload.Groups {
		if group.ID != wantOrder[i] {
			t.Fatalf("group %d id=%q, want %q (lifecycle order broken)", i, group.ID, wantOrder[i])
		}
		if len(group.Tools) == 0 {
			t.Fatalf("group %q is empty", group.ID)
		}
		total += len(group.Tools)
	}
	if total != 12 {
		t.Fatalf("payload carries %d tools, want 12", total)
	}
}

// TestGoalDoorsResolveToolTemplate proves both doors thread a toolTemplate into
// the engine (stamped on the plan + artifact) and that an unknown id degrades to
// a plain goal.
func TestGoalDoorsResolveToolTemplate(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})

	// A known tool id reaches the plan and the artifact metadata.
	thread, err := app.launchGoalThread(goalLaunchSpec{
		Objective:    "Write the Aurora one-pager",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "one_pager",
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	plan := mustGoalPlan(t, app, thread.Artifact.ID)
	if plan.ToolTemplate != "one_pager" {
		t.Fatalf("plan.ToolTemplate=%q, want one_pager", plan.ToolTemplate)
	}
	artifact, _ := app.osArtifactByID(thread.Artifact.ID)
	if artifact.Metadata["toolTemplate"] != "one_pager" {
		t.Fatalf("artifact toolTemplate=%q, want one_pager", artifact.Metadata["toolTemplate"])
	}
	if artifact.Metadata["artifactContract"] != "one_pager_v1" {
		t.Fatalf("artifact contract=%q, want one_pager_v1", artifact.Metadata["artifactContract"])
	}

	// An unknown id degrades to a plain goal (no error, no stamp).
	thread2, err := app.launchGoalThread(goalLaunchSpec{
		Objective:    "Do something bespoke",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "not_a_real_tool",
	})
	if err != nil {
		t.Fatalf("launchGoalThread (unknown tool): %v", err)
	}
	plan2 := mustGoalPlan(t, app, thread2.Artifact.ID)
	if plan2.ToolTemplate != "" {
		t.Fatalf("unknown tool template should be dropped, got %q", plan2.ToolTemplate)
	}
}

// TestExternalWriteGatedToolForcesApproval proves the memo/deal-room class stops
// at the human approval gate even though the goal launches at workspace_write.
func TestExternalWriteGatedToolForcesApproval(t *testing.T) {
	tool, ok := toolByID("investor_update_memo")
	if !ok || !tool.ExternalWriteGated {
		t.Fatal("investor_update_memo must be external-write gated")
	}
	assembly, ok := toolByID("package_assembly")
	if !ok || assembly.ExternalWriteGated {
		t.Fatal("package_assembly must NOT be external-write gated (the external gate is the Deal Room's, Wave 14)")
	}

	app := newIsolatedKanbanBoardApp(t)
	engine := newGoalEngine(app)
	engine.apiKey = func() string { return "test-key" }
	engine.responder = func(_ context.Context, _ string, _ anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		// The gate model says "safe, no external write" — the tool flag alone
		// must still force approval.
		return anthropicMessagesResponse{
			StopReason: "end_turn",
			Content:    []json.RawMessage{mockAnthropicTextBlock(`{"safe":true,"external_write_required":false,"command":"","reason":"looks fine"}`)},
		}, nil
	}
	plan := &goalPlan{Objective: "send the LP update", ToolTemplate: "investor_update_memo", Authority: codexJobAuthorityWorkspaceWrite, Gate: goalGate{Status: "pending"}}
	engine.gate(context.Background(), plan)
	if !plan.Gate.ApprovalRequired {
		t.Fatal("external-write-gated tool did not force the approval gate")
	}
}
