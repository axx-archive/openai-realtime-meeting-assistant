package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

type packagingQualityGateFixture struct {
	app      *kanbanBoardApp
	engine   *goalEngine
	plan     goalPlan
	parentID string
	def      ProcessDefinition
	deck     meetingMemoryEntry
}

func packagingQualityScoreJSON(score float64, reasons string) string {
	dimensions := []string{"Render completeness", "Text fit", "Hierarchy", "Layout craft", "Brand coherence", "Image purpose", "Copy fidelity", "Presentation-distance legibility"}
	rows := make([]map[string]any, 0, len(dimensions))
	for _, name := range dimensions {
		rows = append(rows, map[string]any{"name": name, "score": score, "gap": ""})
	}
	raw, _ := json.Marshal(map[string]any{"dimensions": rows, "reasons": reasons})
	return string(raw)
}

func newPackagingQualityGateFixture(t *testing.T, verdict string, repairs []slideJuryRepair) *packagingQualityGateFixture {
	t.Helper()
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "quality-gate-binding-test"
	t.Setenv("OPENAI_API_KEY", "quality-gate-binding-test")
	t.Setenv("BONFIRE_RENDER_QUEUE_PATH", filepath.Join(t.TempDir(), "render-jobs"))
	t.Setenv("BONFIRE_RENDER_HEARTBEAT_PATH", filepath.Join(t.TempDir(), "missing-heartbeat.json"))
	previousStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousStart })

	run, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Build an editable presentation for the exact rendered-review binding test " + t.Name(),
		CreatedBy: "aj@shareability.com", ToolTemplate: packagingStudioProcessID,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := mustGoalPlan(t, app, run.Artifact.ID)
	engine := newGoalEngine(app)
	if err := engine.prepareGoalRoute(&plan, run.Artifact.ID); err != nil {
		t.Fatal(err)
	}
	def := packagingStudioDefinition()
	if err := instantiateProcessPlan(def, &plan); err != nil {
		t.Fatal(err)
	}
	plan.State = goalStateExecute

	deckCopy := map[string]any{
		"slide_count_inference": "Two slides are sufficient for this exact rendered-review fixture.",
		"slides": []any{
			map[string]any{
				"slide_id": "slide-1", "slide_kind": "cover", "thesis": "Slide 1 — " + studioTestFounderPhrase,
				"turn": "open", "headline": "Slide 1 — " + studioTestFounderPhrase,
				"kicker": "", "body": "", "proof": "", "evidence_label": "", "source_label": "",
				"speaker_intent": "Open on the exact founder line.", "transition": "Close cleanly.",
				"presenter_note": "Opening note [BEAT]", "claim_ids": []any{}, "claim_renderings": []any{},
			},
			map[string]any{
				"slide_id": "slide-2", "slide_kind": "close", "thesis": "Slide 2 — Close",
				"turn": "close", "headline": "Slide 2 — Close",
				"kicker": "", "body": "", "proof": "", "evidence_label": "", "source_label": "",
				"speaker_intent": "Close on the decision.", "transition": "", "presenter_note": "Closing note [BEAT]",
				"claim_ids": []any{}, "claim_renderings": []any{},
			},
		},
	}
	deckCopyRaw, err := json.Marshal(deckCopy)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePackagingStudioDeckCopyOutput(app, &plan, string(deckCopyRaw)); err != nil {
		t.Fatalf("quality fixture deck_copy_v3 is invalid: %v", err)
	}
	writeArtifact, _, err := app.createOSArtifactWithMetadata("workflow", "write", string(deckCopyRaw), scoutParticipantName, map[string]string{
		"source": "process_stage", "goalParentId": run.Artifact.ID, "goalSubtaskId": "write",
		"processId": packagingStudioProcessID, "processStage": "write", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	write := plan.subtaskByID("write")
	write.Status, write.ArtifactID = subtaskComplete, writeArtifact.ID
	write.Review = &goalSubtaskReview{Verdict: goalReviewPass, By: "fixture"}
	installPackagingPremiumIdentityForTest(t, app, &plan, []string{"slide-1", "slide-2"})

	textElement := func(id, text, color string) map[string]any {
		stack, _ := packagingStudioResolvedFontStack("modern_grotesk")
		return map[string]any{
			"id": id, "type": "text", "x": 120, "y": 140, "width": 1680, "height": 240,
			"z": 2, "opacity": 1, "rotation": 0, "text": text, "copy_role": "headline",
			"typography": map[string]any{
				"font_token": "modern_grotesk", "font_family": stack, "font_size": 92, "font_weight": 700, "line_height": 1.05,
				"letter_spacing": "normal", "alignment": "left", "color": color,
			},
			"claim_ids": []any{}, "claim_renderings": []any{},
		}
	}
	layout := map[string]any{"visual_identity": packagingPremiumLayoutIdentityForTest(), "slides": []any{
		map[string]any{
			"slide_id": "slide-1", "slide_kind": "cover", "composition": "one focal statement",
			"background": "#101014", "grid": "editorial_12",
			"elements": []any{textElement("headline-1", "Slide 1 — "+studioTestFounderPhrase, "#ffffff")},
		},
		map[string]any{
			"slide_id": "slide-2", "slide_kind": "close", "composition": "one closing statement",
			"background": "#f4efe5", "grid": "editorial_12",
			"elements": []any{textElement("headline-2", "Slide 2 — Close", "#111111")},
		},
	}}
	layoutRaw, err := json.Marshal(layout)
	if err != nil {
		t.Fatal(err)
	}
	layoutArtifact, _, err := app.createOSArtifactWithMetadata("workflow", "layout_plan", string(layoutRaw), scoutParticipantName, map[string]string{
		"source": "process_stage", "goalParentId": run.Artifact.ID, "goalSubtaskId": "layout_plan",
		"processId": packagingStudioProcessID, "processStage": "layout_plan", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	layoutStage := plan.subtaskByID("layout_plan")
	layoutStage.Status, layoutStage.ArtifactID = subtaskComplete, layoutArtifact.ID
	layoutStage.Review = &goalSubtaskReview{Verdict: goalReviewPass, By: "fixture"}

	shipArtifact, _, err := app.createOSArtifactWithMetadata("workflow", "ship_deck", studioPremiumTestDeckHTML(), scoutParticipantName, map[string]string{
		"source": "process_stage", "goalParentId": run.Artifact.ID, "goalSubtaskId": "ship_deck",
		"processId": packagingStudioProcessID, "processStage": "ship_deck", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	ship := plan.subtaskByID("ship_deck")
	ship.Status, ship.ArtifactID = subtaskComplete, shipArtifact.ID
	ship.Review = &goalSubtaskReview{Verdict: goalReviewPass, By: "fixture"}

	draft := plan.subtaskByID("draft_compile")
	draft.Status = subtaskRunning
	draftStage := packagingStudioStage(t, def, "draft_compile")
	body, extra, err := compilePackagingStudioDraft(app, &plan, run.Artifact.ID, draftStage)
	if err != nil {
		t.Fatal(err)
	}
	engine.completeProcessStage(&plan, run.Artifact.ID, draft, draftStage, body, "draft fixture", extra)
	deck, ok := app.osArtifactByID(extra["deckArtifactId"])
	if !ok {
		t.Fatal("draft candidate was not filed")
	}

	fixture := &packagingQualityGateFixture{app: app, engine: engine, plan: plan, parentID: run.Artifact.ID, def: def, deck: deck}
	fixture.fileJury(t, verdict, repairs)
	fixture.plan.subtaskByID("quality_gate").Status = subtaskRunning
	return fixture
}

func (fixture *packagingQualityGateFixture) fileJury(t *testing.T, verdict string, repairs []slideJuryRepair) {
	t.Helper()
	stage := fixture.plan.subtaskByID("slide_jury")
	stage.Status = subtaskRunning
	stageDef := packagingStudioStage(t, fixture.def, "slide_jury")
	if verdict == "needs_attention" {
		fixture.engine.completeProcessStage(&fixture.plan, fixture.parentID, stage, stageDef,
			"Slide jury skipped: rendered review needs attention", "jury needs attention",
			map[string]string{"reviewVerdict": "needs_attention"})
		return
	}
	for index := range repairs {
		if repairs[index].Owner == "" {
			repairs[index].Owner = "layout_plan"
		}
	}
	repairByOwnerPage := map[string]map[int]string{"write": {}, "layout_plan": {}}
	for _, repair := range repairs {
		if len(repair.Fixes) > 0 {
			repairByOwnerPage[repair.Owner][repair.Page] = repair.Fixes[0]
		}
	}
	pageCount := renderedDeckSlideCount(fixture.deck.Text)
	var juryBody strings.Builder
	juryBody.WriteString("Exact rendered scoreboard\n\n## Jury voices\n")
	voices := []goalPanelVoice{}
	for _, persona := range slideJuryPersonas() {
		card := slideJurySeatScorecard{Pages: make([]slideJuryPageScore, 0, pageCount)}
		for page := 1; page <= pageCount; page++ {
			score := 9.25
			fix := "KEEP"
			owner := slideJuryRepairOwner(persona.Name, slideJuryPageScore{})
			if exactFix := repairByOwnerPage[owner][page]; exactFix != "" {
				score, fix = 6.5, exactFix
			}
			card.Pages = append(card.Pages, slideJuryPageScore{Page: page, Score: score, Fix: fix})
		}
		raw, marshalErr := json.Marshal(card)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		juryBody.WriteString("\n### " + persona.Name + "\n" + string(raw) + "\n")
		voices = append(voices, goalPanelVoice{Persona: persona.Name, Text: string(raw)})
	}
	readiness := evaluateSlideJuryReadiness(voices, pageCount)
	if readiness.Verdict != verdict {
		t.Fatalf("fixture jury verdict=%s, want %s: %+v", readiness.Verdict, verdict, readiness)
	}
	readinessRepairs := readiness.Repairs
	if readinessRepairs == nil {
		readinessRepairs = []slideJuryRepair{}
	}
	repairJSON, err := json.Marshal(readinessRepairs)
	if err != nil {
		t.Fatal(err)
	}
	blocking := slideJuryPageList(readiness.BlockingPages)
	minimum := strconv.FormatFloat(readiness.MinimumAverage, 'f', 2, 64)
	scoreboardBody := normalizeMemoryEntryText(meetingMemoryKindOSArtifact, juryBody.String())
	deckVersion := fmt.Sprintf("%d", artifactVersion(fixture.deck))
	deckDigest := artifactCapabilityDigest(fixture.deck)
	scoreboard, _, err := fixture.app.createOSArtifactWithMetadata("workflow", "Slide jury — merged scoreboard", scoreboardBody, scoutParticipantName, map[string]string{
		"artifactContract":    slideJuryContract,
		"source":              slideJurySource,
		"goalId":              fixture.parentID,
		"deckArtifactId":      fixture.deck.ID,
		"deckArtifactVersion": deckVersion,
		"deckContentDigest":   deckDigest,
		"reviewVerdict":       verdict,
		"blockingPages":       blocking,
		"minimumAverage":      minimum,
		"parsedSeats":         strconv.Itoa(readiness.ParsedSeats),
		"repairFixes":         string(repairJSON),
		"jurySeatsDigest":     sha256Hex([]byte(scoreboardBody)),
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.engine.completeProcessStage(&fixture.plan, fixture.parentID, stage, stageDef,
		"Slide jury linked to "+scoreboard.ID, "jury fixture", map[string]string{
			"slideJuryArtifactId":     scoreboard.ID,
			"slideJuryArtifactDigest": artifactCapabilityDigest(scoreboard),
			"jurySeatsDigest":         sha256Hex([]byte(scoreboardBody)),
			"reviewVerdict":           verdict,
			"blockingPages":           blocking,
			"minimumAverage":          minimum,
			"parsedSeats":             strconv.Itoa(readiness.ParsedSeats),
			"repairFixes":             string(repairJSON),
			"deckArtifactVersion":     deckVersion,
			"deckContentDigest":       deckDigest,
		})
}

func (fixture *packagingQualityGateFixture) runQualityGate(t *testing.T, response string, calls *atomic.Int32) {
	t.Helper()
	fixture.engine.openAIResponder = func(context.Context, string, openAITextRequest) (string, error) {
		calls.Add(1)
		return response, nil
	}
	quality := fixture.plan.subtaskByID("quality_gate")
	stage := packagingStudioStage(t, fixture.def, "quality_gate")
	fixture.engine.runProcessGateStage(context.Background(), &fixture.plan, fixture.parentID, quality, stage)
}

func TestAuthoredProcessLateReviewCascadeInvalidatesEveryTransitiveDependent(t *testing.T) {
	def := packagingStudioDefinition()
	base := goalPlan{ProcessID: def.ID, routeVerified: true}
	pinProcessPlanForTest(t, &base, def)
	if err := instantiateProcessPlan(def, &base); err != nil {
		t.Fatal(err)
	}
	for _, targetID := range []string{"external_research", "identity", "layout_plan", "ship_deck"} {
		t.Run(targetID, func(t *testing.T) {
			plan := base
			plan.Subtasks = append([]goalSubtask(nil), base.Subtasks...)
			for index := range plan.Subtasks {
				plan.Subtasks[index].Status = subtaskComplete
				plan.Subtasks[index].Review = &goalSubtaskReview{Verdict: goalReviewPass, By: "fixture"}
			}
			target := plan.subtaskByID(targetID)
			target.Status = subtaskReady
			reset := resetGoalReviewDependents(&plan, targetID)
			if len(reset) == 0 {
				t.Fatalf("%s reset no completed dependents", targetID)
			}
			stale := map[string]bool{targetID: true}
			for changed := true; changed; {
				changed = false
				for index := range plan.Subtasks {
					for _, dependency := range plan.Subtasks[index].DependsOn {
						if stale[dependency] && !stale[plan.Subtasks[index].ID] {
							stale[plan.Subtasks[index].ID] = true
							changed = true
						}
					}
				}
			}
			for index := range plan.Subtasks {
				stage := &plan.Subtasks[index]
				if stage.ID == targetID {
					continue
				}
				if stale[stage.ID] {
					if stage.Status != subtaskPending || stage.Review == nil || stage.Review.By != "review_cascade" || !strings.Contains(stage.Review.Reasons, targetID) {
						t.Fatalf("stale dependent %s kept stale completion/evidence: %+v", stage.ID, stage)
					}
				} else if stage.Status != subtaskComplete {
					t.Fatalf("independent stage %s was reset by %s cascade", stage.ID, targetID)
				}
			}
		})
	}
}

func TestHistoricalAuthoredPlanCascadeUsesPersistedDAGNotCurrentRegistry(t *testing.T) {
	plan := goalPlan{ProcessID: "retired_process_v1", Subtasks: []goalSubtask{
		{ID: "retired_writer", Role: processRoleWriter, Status: subtaskReady},
		{ID: "retired_compile", Role: processRoleCompile, Status: subtaskComplete, DependsOn: []string{"retired_writer"}, Review: &goalSubtaskReview{Verdict: goalReviewPass}},
		{ID: "retired_ship", Role: processRoleCompile, Status: subtaskComplete, DependsOn: []string{"retired_compile"}, Review: &goalSubtaskReview{Verdict: goalReviewPass}},
	}}
	reset := resetGoalReviewDependents(&plan, "retired_writer")
	if len(reset) != 2 {
		t.Fatalf("historical cascade reset=%v, want both persisted dependents", reset)
	}
	for _, id := range []string{"retired_compile", "retired_ship"} {
		stage := plan.subtaskByID(id)
		if stage.Status != subtaskPending || stage.Review == nil || stage.Review.By != "review_cascade" {
			t.Fatalf("historical %s retained stale completion: %+v", id, stage)
		}
	}
}

func TestGenericLateReviewRequeueUsesReviewCascade(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "review-cascade-test"
	def := packagingStudioDefinition()
	plan := goalPlan{ProcessID: def.ID, Objective: "Review the authored pipeline", routeVerified: true}
	pinProcessPlanForTest(t, &plan, def)
	if err := instantiateProcessPlan(def, &plan); err != nil {
		t.Fatal(err)
	}
	for index := range plan.Subtasks {
		plan.Subtasks[index].Status = subtaskComplete
		plan.Subtasks[index].Review = &goalSubtaskReview{Verdict: goalReviewPass, By: "fixture"}
	}
	layout := plan.subtaskByID("layout_plan")
	artifact, _, err := app.createOSArtifactWithMetadata("workflow", "layout", "Slide geometry that drifted", scoutParticipantName, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	layout.ArtifactID, layout.Review = artifact.ID, nil
	engine := newGoalEngine(app)
	engine.openAIResponder = func(context.Context, string, openAITextRequest) (string, error) {
		return `{"verdict":"revise","score":6,"reasons":"the geometry drifts from the locked identity","strengths_to_keep":[]}`, nil
	}
	if outcome := engine.reviewSubtasks(context.Background(), &plan); outcome != goalReviewOutcomeRequeue {
		t.Fatalf("review outcome=%v, want requeue", outcome)
	}
	if layout.Status != subtaskReady || layout.Revisions != 1 {
		t.Fatalf("layout was not requeued: %+v", layout)
	}
	for _, id := range []string{"ship_deck", "draft_compile", "slide_jury", "quality_gate", "ship_compile"} {
		stage := plan.subtaskByID(id)
		if stage.Status != subtaskPending || stage.Review == nil || stage.Review.By != "review_cascade" {
			t.Fatalf("%s retained stale completion after generic review: %+v", id, stage)
		}
	}
	if identity := plan.subtaskByID("identity"); identity.Status != subtaskComplete {
		t.Fatalf("independent identity stage was unnecessarily reset: %+v", identity)
	}
}

func TestExhaustedAuthoredReviewStillInvalidatesDependentsBeforeHumanRetry(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	def := packagingStudioDefinition()
	plan := goalPlan{ProcessID: def.ID, Objective: "Review the exhausted authored pipeline", routeVerified: true}
	pinProcessPlanForTest(t, &plan, def)
	if err := instantiateProcessPlan(def, &plan); err != nil {
		t.Fatal(err)
	}
	for index := range plan.Subtasks {
		plan.Subtasks[index].Status = subtaskComplete
		plan.Subtasks[index].Review = &goalSubtaskReview{Verdict: goalReviewPass, By: "fixture"}
	}
	layout := plan.subtaskByID("layout_plan")
	layout.Status = subtaskFailed
	layout.Revisions = goalMaxRevisions
	engine := newGoalEngine(app)
	if outcome := engine.reviewSubtasks(context.Background(), &plan); outcome != goalReviewOutcomeBlocked {
		t.Fatalf("review outcome=%v, want blocked", outcome)
	}
	if layout.Status != subtaskBlocked {
		t.Fatalf("exhausted layout stage=%+v, want blocked", layout)
	}
	for _, id := range []string{"ship_deck", "draft_compile", "slide_jury", "quality_gate", "ship_compile"} {
		stage := plan.subtaskByID(id)
		if stage.Status != subtaskPending || stage.Review == nil || stage.Review.By != "review_cascade" {
			t.Fatalf("%s survived the exhausted review and could be reused after Retry: %+v", id, stage)
		}
	}
}

func TestPackagingQualityGateNeedsChangesUsesExactFixesWithoutScorer(t *testing.T) {
	const exactFix = "Move the proof label 24px below the chart and replace the unsupported 42% claim with the cited 38%."
	fixture := newPackagingQualityGateFixture(t, "needs_changes", []slideJuryRepair{{Page: 2, Fixes: []string{exactFix}}})
	var calls atomic.Int32
	fixture.runQualityGate(t, `{"dimensions":[{"name":"Layout","score":10,"gap":""}]}`, &calls)
	if calls.Load() != 0 {
		t.Fatalf("needs_changes spent %d scorer calls, want zero", calls.Load())
	}
	quality := fixture.plan.subtaskByID("quality_gate")
	layout := fixture.plan.subtaskByID("layout_plan")
	if quality.Status != subtaskPending || quality.Revisions != 1 || layout.Status != subtaskReady || layout.Review == nil || !strings.Contains(layout.Review.Reasons, exactFix) {
		t.Fatalf("exact jury repair did not requeue layout_plan: gate=%+v layout=%+v", quality, layout)
	}
	for _, id := range []string{"ship_deck", "draft_compile", "slide_jury"} {
		if stage := fixture.plan.subtaskByID(id); stage.Status != subtaskPending || stage.Review == nil || stage.Review.By != "process_gate_cascade" {
			t.Fatalf("%s retained stale rendered work: %+v", id, stage)
		}
	}
}

func TestPackagingQualityGateAttentionAndBindingMismatchHardHoldWithoutScorer(t *testing.T) {
	t.Run("needs_attention", func(t *testing.T) {
		fixture := newPackagingQualityGateFixture(t, "needs_attention", nil)
		var calls atomic.Int32
		fixture.runQualityGate(t, `{"dimensions":[{"name":"Layout","score":10,"gap":""}]}`, &calls)
		if calls.Load() != 0 || fixture.plan.State != goalStateBlocked || fixture.plan.subtaskByID("draft_compile").Status != subtaskBlocked || fixture.plan.subtaskByID("slide_jury").Status != subtaskPending || fixture.plan.subtaskByID("quality_gate").Status != subtaskPending {
			t.Fatalf("needs_attention did not hard-hold before scorer: calls=%d state=%q gate=%+v", calls.Load(), fixture.plan.State, fixture.plan.subtaskByID("quality_gate"))
		}
		if review := fixture.plan.subtaskByID("draft_compile").Review; review == nil || review.By != "quality_gate_recovery" {
			t.Fatalf("needs_attention did not anchor Retry at a fresh render: %+v", review)
		}
	})

	t.Run("missing_scoreboard", func(t *testing.T) {
		fixture := newPackagingQualityGateFixture(t, "ready", []slideJuryRepair{})
		juryRecord := mustArtifact(t, fixture.app, fixture.plan.subtaskByID("slide_jury").ArtifactID)
		if _, _, err := fixture.app.updateOSArtifactWithMetadata(juryRecord.ID, "", juryRecord.Text, scoutParticipantName, map[string]string{"slideJuryArtifactId": "missing-scoreboard"}); err != nil {
			t.Fatal(err)
		}
		var calls atomic.Int32
		fixture.runQualityGate(t, `{"dimensions":[{"name":"Layout","score":10,"gap":""}]}`, &calls)
		if calls.Load() != 0 || fixture.plan.State != goalStateBlocked || !strings.Contains(fixture.plan.Blocker, "scoreboard is missing") || fixture.plan.subtaskByID("draft_compile").Status != subtaskBlocked || fixture.plan.subtaskByID("slide_jury").Status != subtaskPending {
			t.Fatalf("missing scoreboard did not hard-hold before scorer: calls=%d state=%q blocker=%q", calls.Load(), fixture.plan.State, fixture.plan.Blocker)
		}
	})

	t.Run("mismatched_candidate", func(t *testing.T) {
		fixture := newPackagingQualityGateFixture(t, "ready", []slideJuryRepair{})
		juryRecord := mustArtifact(t, fixture.app, fixture.plan.subtaskByID("slide_jury").ArtifactID)
		scoreboard := mustArtifact(t, fixture.app, juryRecord.Metadata["slideJuryArtifactId"])
		if _, _, err := fixture.app.updateOSArtifactWithMetadata(scoreboard.ID, "", scoreboard.Text, scoutParticipantName, map[string]string{"deckArtifactId": "wrong-candidate"}); err != nil {
			t.Fatal(err)
		}
		var calls atomic.Int32
		fixture.runQualityGate(t, `{"dimensions":[{"name":"Layout","score":10,"gap":""}]}`, &calls)
		if calls.Load() != 0 || fixture.plan.State != goalStateBlocked || !strings.Contains(fixture.plan.Blocker, "different candidate") || fixture.plan.subtaskByID("draft_compile").Status != subtaskBlocked || fixture.plan.subtaskByID("slide_jury").Status != subtaskPending {
			t.Fatalf("binding mismatch did not hard-hold before scorer: calls=%d state=%q blocker=%q", calls.Load(), fixture.plan.State, fixture.plan.Blocker)
		}
	})
}

func TestPackagingQualityGateReadyScoresThenShipsExactReviewedRevision(t *testing.T) {
	fixture := newPackagingQualityGateFixture(t, "ready", []slideJuryRepair{})
	beforeVersion := artifactVersion(fixture.deck)
	beforeDigest := artifactCapabilityDigest(fixture.deck)
	var calls atomic.Int32
	fixture.runQualityGate(t, packagingQualityScoreJSON(9.2, "ready at presentation distance"), &calls)
	if calls.Load() != 0 {
		t.Fatalf("authoritative rendered-ready verdict spent %d second-scorer calls, want zero", calls.Load())
	}
	quality := fixture.plan.subtaskByID("quality_gate")
	if quality.Status != subtaskComplete {
		t.Fatalf("ready quality gate did not complete: %+v", quality)
	}
	gateRecord := mustArtifact(t, fixture.app, quality.ArtifactID)
	juryStage := fixture.plan.subtaskByID("slide_jury")
	juryRecord := mustArtifact(t, fixture.app, juryStage.ArtifactID)
	if gateRecord.Metadata["reviewedDeckArtifactId"] != fixture.deck.ID || gateRecord.Metadata["reviewedDeckArtifactVersion"] != fmt.Sprintf("%d", beforeVersion) || gateRecord.Metadata["reviewedDeckContentDigest"] != beforeDigest || gateRecord.Metadata["slideJuryArtifactId"] != juryRecord.Metadata["slideJuryArtifactId"] {
		t.Fatalf("quality gate did not stamp exact reviewed identity: %v", gateRecord.Metadata)
	}
	if !strings.Contains(gateRecord.Text, "rendered slide jury passed the exact reviewed draft") {
		t.Fatalf("quality gate did not name the deterministic rendered authority: %q", gateRecord.Text)
	}
	body, extra, err := compilePackagingStudioShip(fixture.app, &fixture.plan, fixture.parentID, packagingStudioStage(t, fixture.def, "ship_compile"))
	if err != nil {
		t.Fatal(err)
	}
	after := mustArtifact(t, fixture.app, extra["deckArtifactId"])
	if after.ID != fixture.deck.ID || artifactVersion(after) != beforeVersion || artifactCapabilityDigest(after) != beforeDigest || !strings.Contains(body, "Exact reviewed candidate") {
		t.Fatalf("final ship identity drifted: before=%s/v%d/%s after=%s/v%d/%s body=%q", fixture.deck.ID, beforeVersion, beforeDigest, after.ID, artifactVersion(after), artifactCapabilityDigest(after), body)
	}
}

func TestPackagingStudioTerminalTailUsesOnlyExactRenderedAdmission(t *testing.T) {
	fixture := newPackagingQualityGateFixture(t, "ready", []slideJuryRepair{})
	var modelCalls atomic.Int32
	fixture.runQualityGate(t, packagingQualityScoreJSON(9.4, "ready"), &modelCalls)
	if modelCalls.Load() != 0 {
		t.Fatalf("rendered quality admission called a second scorer %d time(s)", modelCalls.Load())
	}

	ship := fixture.plan.subtaskByID("ship_compile")
	ship.Status = subtaskRunning
	stage := packagingStudioStage(t, fixture.def, "ship_compile")
	body, metadata, err := compilePackagingStudioShip(fixture.app, &fixture.plan, fixture.parentID, stage)
	if err != nil {
		t.Fatal(err)
	}
	fixture.engine.completeProcessStage(&fixture.plan, fixture.parentID, ship, stage, body, "exact publication fixture", metadata)
	for index := range fixture.plan.Subtasks {
		fixture.plan.Subtasks[index].Status = subtaskComplete
		// Deliberately clear generic review stamps. The authored rendered
		// admission, not a late text model, owns terminal quality.
		fixture.plan.Subtasks[index].Review = nil
	}
	fixture.plan.State = goalStateReview
	fixture.engine.openAIResponder = func(context.Context, string, openAITextRequest) (string, error) {
		modelCalls.Add(1)
		return "", fmt.Errorf("no model may run after exact rendered admission")
	}
	fixture.engine.drive(context.Background(), &fixture.plan, fixture.parentID)
	if modelCalls.Load() != 0 {
		t.Fatalf("terminal authored tail made %d post-admission model call(s)", modelCalls.Load())
	}
	if fixture.plan.State != goalStateVerified || fixture.plan.Gate.Status != "passed" || fixture.plan.Gate.ReviewedBy != "presentation_rendered_admission" {
		t.Fatalf("terminal authored tail did not verify exact publication: state=%q gate=%+v blocker=%q", fixture.plan.State, fixture.plan.Gate, fixture.plan.Blocker)
	}
	if fixture.plan.Report.Headline != "Presentation ready" || !strings.Contains(fixture.plan.Verification.Reasons, "slide jury remain bound") {
		t.Fatalf("terminal report/verification lost exact rendered authority: report=%+v verification=%+v", fixture.plan.Report, fixture.plan.Verification)
	}
	admittedDeck := mustArtifact(t, fixture.app, fixture.deck.ID)
	if err := fixture.app.requireFinalExportAdmission(admittedDeck); err != nil {
		t.Fatalf("exact verified presentation did not unlock final export: %v", err)
	}
}

func TestAuthoritativeRenderedReviewRequeuesFailedAuthoredStageBeforeBlocking(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "authored-review-requeue-test"

	previousGoalStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStart })
	previousAgentStart := startAgentThreadAsync
	launched := make(chan scoutAgentThread, 1)
	startAgentThreadAsync = func(_ *kanbanBoardApp, thread scoutAgentThread) { launched <- thread }
	t.Cleanup(func() { startAgentThreadAsync = previousAgentStart })

	run, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Decide whether the official program's 2026 opted-in creator count supports proceeding",
		CreatedBy: "aj@shareability.com", ToolTemplate: documentReportProcessID,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := mustGoalPlan(t, app, run.Artifact.ID)
	engine := newGoalEngine(app)
	var genericReviewerCalls atomic.Int32
	engine.openAIResponder = func(context.Context, string, openAITextRequest) (string, error) {
		genericReviewerCalls.Add(1)
		return "", fmt.Errorf("generic reviewer must not run during authored-stage repair")
	}
	if err := engine.prepareGoalRoute(&plan, run.Artifact.ID); err != nil {
		t.Fatal(err)
	}
	if err := instantiateProcessPlan(documentReportDefinition(), &plan); err != nil {
		t.Fatal(err)
	}
	assignGoalRunners(&plan)

	contextBody := externalEvidenceContextBodyForTest(t, plan, "What is the official program's 2026 opted-in creator count?", 1)
	authorized, mode, err := authorizeExternalEvidenceResearchText(app, &plan, contextBody)
	if err != nil {
		t.Fatal(err)
	}
	contextArtifact, _, err := app.createOSArtifactWithMetadata("workflow", "Context snapshot", contextBody, scoutParticipantName, map[string]string{
		"source": "process_stage", "goalParentId": run.Artifact.ID, "goalSubtaskId": "context_snapshot",
		"processId": documentReportProcessID, "processStage": "context_snapshot", "processRole": processRoleSynthesizer,
		"status": "complete", "threadStatus": "complete", "outputContract": documentReportResearchContextContract,
		"researchMode": mode, "researchQuestionCount": strconv.Itoa(len(authorized.Questions)),
		"researchQuestionAuthorityDigest": authorized.QuestionAuthorityDigest, "researchSourceAuthorityDigest": authorized.SourceAuthorityDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	contextStage := plan.subtaskByID("context_snapshot")
	contextStage.Status, contextStage.ArtifactID = subtaskComplete, contextArtifact.ID
	contextStage.Review = nil

	const exactFailure = "context snapshot research authority is invalid: report context research questions do not define one bounded comparative evidence lane"
	researchStage := plan.subtaskByID("external_research")
	researchStage.Status = subtaskFailed
	researchStage.Review = &goalSubtaskReview{Verdict: goalReviewFail, Reasons: exactFailure, By: "process_engine"}
	plan.State = goalStateReview

	engine.drive(context.Background(), &plan, run.Artifact.ID)

	if plan.State != goalStateExecute || researchStage.Status != subtaskRunning || researchStage.Revisions != 1 || researchStage.Attempts != 1 {
		t.Fatalf("authored failure bypassed bounded requeue: state=%q stage=%+v blocker=%q", plan.State, researchStage, plan.Blocker)
	}
	if researchStage.Review == nil || researchStage.Review.Reasons != exactFailure {
		t.Fatalf("requeue lost exact revision guidance: %+v", researchStage.Review)
	}
	if genericReviewerCalls.Load() != 0 || contextStage.Status != subtaskComplete || contextStage.Review != nil {
		t.Fatalf("authored repair re-reviewed completed upstream work: calls=%d context=%+v", genericReviewerCalls.Load(), contextStage)
	}
	select {
	case child := <-launched:
		if child.Artifact.Metadata["goalSubtaskId"] != "external_research" {
			t.Fatalf("requeue launched subtask=%q, want external_research", child.Artifact.Metadata["goalSubtaskId"])
		}
	default:
		t.Fatal("requeued authored stage was not dispatched")
	}
	persisted := mustGoalPlan(t, app, run.Artifact.ID)
	if persisted.State != goalStateExecute || persisted.subtaskByID("external_research").Revisions != 1 || persisted.Blocker != "" {
		t.Fatalf("durable authored repair state=%q stage=%+v blocker=%q", persisted.State, persisted.subtaskByID("external_research"), persisted.Blocker)
	}
}

func TestAuthoritativeRenderedReviewBlocksOnlyAfterRevisionBudget(t *testing.T) {
	plan := &goalPlan{ProcessID: documentReportProcessID, Subtasks: []goalSubtask{{
		ID: "context_snapshot", Status: subtaskFailed, Revisions: goalMaxRevisions,
		Review: &goalSubtaskReview{Verdict: goalReviewFail, Reasons: "context snapshot research authority is invalid: drifts from authorized direct-evidence dimensions", By: "process_engine"},
	}}}
	engine := &goalEngine{}
	if outcome := engine.repairFailedAuthoritativeSubtasks(plan); outcome != goalReviewOutcomeBlocked {
		t.Fatalf("outcome=%v, want blocked after the bounded revision budget", outcome)
	}
	stage := plan.subtaskByID("context_snapshot")
	if stage.Status != subtaskBlocked || stage.Revisions != goalMaxRevisions {
		t.Fatalf("exhausted authored stage=%+v, want terminal blocked with unchanged budget", stage)
	}
	if !strings.Contains(plan.Blocker, "drifts from authorized direct-evidence dimensions") {
		t.Fatalf("durable blocker lost the validation reason: %q", plan.Blocker)
	}
}

func TestPackagingStudioJuryBodyTamperRevokesQualityPublicationAndVerification(t *testing.T) {
	fixture := newPackagingQualityGateFixture(t, "ready", []slideJuryRepair{})
	var modelCalls atomic.Int32
	fixture.runQualityGate(t, packagingQualityScoreJSON(9.4, "ready"), &modelCalls)
	if modelCalls.Load() != 0 {
		t.Fatalf("rendered admission unexpectedly called a second scorer %d time(s)", modelCalls.Load())
	}
	ship := fixture.plan.subtaskByID("ship_compile")
	ship.Status = subtaskRunning
	stage := packagingStudioStage(t, fixture.def, "ship_compile")
	body, metadata, err := compilePackagingStudioShip(fixture.app, &fixture.plan, fixture.parentID, stage)
	if err != nil {
		t.Fatal(err)
	}
	fixture.engine.completeProcessStage(&fixture.plan, fixture.parentID, ship, stage, body, "exact publication fixture", metadata)

	juryRecord := mustArtifact(t, fixture.app, fixture.plan.subtaskByID("slide_jury").ArtifactID)
	scoreboard := mustArtifact(t, fixture.app, juryRecord.Metadata["slideJuryArtifactId"])
	if _, _, err := fixture.app.updateOSArtifactWithMetadata(scoreboard.ID, "", scoreboard.Text+"\n\nTampered after admission.", scoutParticipantName, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := resolvePackagingStudioQualityGateReview(fixture.app, &fixture.plan, fixture.parentID); err == nil {
		t.Fatal("quality resolver accepted a changed jury body")
	}
	if _, err := resolvePublishedPackagingStudioQuality(fixture.app, &fixture.plan, fixture.parentID); err == nil {
		t.Fatal("publication resolver accepted a changed jury body")
	}
	if fixture.engine.verify(context.Background(), &fixture.plan) {
		t.Fatal("terminal verifier accepted a changed jury body")
	}
}

func TestPackagingQualityScorerRequiresEveryExactBoundedRubricDimension(t *testing.T) {
	fixture := newPackagingQualityGateFixture(t, "ready", []slideJuryRepair{})
	stage := packagingStudioStage(t, fixture.def, "quality_gate")
	quality := fixture.plan.subtaskByID("quality_gate")
	tests := []struct {
		name     string
		response string
	}{
		{name: "missing dimensions", response: `{"dimensions":[{"name":"Layout craft","score":10,"gap":""}],"reasons":"looks good"}`},
		{name: "extra dimension", response: strings.Replace(packagingQualityScoreJSON(9.2, "ready"), `"dimensions":[`, `"dimensions":[{"name":"Vibes","score":10,"gap":""},`, 1)},
		{name: "out of range", response: strings.Replace(packagingQualityScoreJSON(9.2, "ready"), `"score":9.2`, `"score":11`, 1)},
		{name: "duplicate", response: strings.Replace(packagingQualityScoreJSON(9.2, "ready"), `"name":"Text fit"`, `"name":"Render completeness"`, 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture.engine.openAIResponder = func(context.Context, string, openAITextRequest) (string, error) { return test.response, nil }
			round := fixture.engine.scoreProcessGateRound(context.Background(), &fixture.plan, fixture.parentID, quality, stage)
			if round.Failure != goalGateFailureMalformed || len(round.Dimensions) != 0 {
				t.Fatalf("invalid scorer response earned a judgment: %+v", round)
			}
		})
	}
	fixture.engine.openAIResponder = func(context.Context, string, openAITextRequest) (string, error) {
		return packagingQualityScoreJSON(9.2, "ready"), nil
	}
	round := fixture.engine.scoreProcessGateRound(context.Background(), &fixture.plan, fixture.parentID, quality, stage)
	if round.Failure != "" || len(round.Dimensions) != 8 {
		t.Fatalf("exact eight-dimension rubric was not admitted: %+v", round)
	}
}

func TestPackagingQualityGateRepairedDeckShipsTheNewReviewedIdentity(t *testing.T) {
	const exactFix = "Give the closing headline a stronger second-column inset."
	fixture := newPackagingQualityGateFixture(t, "needs_changes", []slideJuryRepair{{Page: 2, Fixes: []string{exactFix}}})
	originalDeckID := fixture.deck.ID
	originalVersion := artifactVersion(fixture.deck)
	var firstCalls atomic.Int32
	fixture.runQualityGate(t, `{"dimensions":[{"name":"Layout","score":10,"gap":""}]}`, &firstCalls)
	if firstCalls.Load() != 0 {
		t.Fatalf("repair admission unexpectedly called scorer %d time(s)", firstCalls.Load())
	}

	layoutRecord := mustArtifact(t, fixture.app, fixture.plan.subtaskByID("layout_plan").ArtifactID)
	var repairedLayout map[string]any
	if err := json.Unmarshal([]byte(layoutRecord.Text), &repairedLayout); err != nil {
		t.Fatal(err)
	}
	repairedElement := repairedLayout["slides"].([]any)[1].(map[string]any)["elements"].([]any)[0].(map[string]any)
	repairedElement["x"] = float64(262)
	repairedElement["width"] = float64(1538)
	repairedLayoutRaw, err := json.Marshal(repairedLayout)
	if err != nil {
		t.Fatal(err)
	}
	repairedLayoutArtifact, _, err := fixture.app.createOSArtifactWithMetadata("workflow", "layout_plan repaired", string(repairedLayoutRaw), scoutParticipantName, map[string]string{
		"source": "process_stage", "goalParentId": fixture.parentID, "goalSubtaskId": "layout_plan",
		"processId": packagingStudioProcessID, "processStage": "layout_plan", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	layoutStage := fixture.plan.subtaskByID("layout_plan")
	layoutStage.Status, layoutStage.ArtifactID = subtaskComplete, repairedLayoutArtifact.ID
	layoutStage.Review = &goalSubtaskReview{Verdict: goalReviewPass, By: "fixture"}

	repairedHTML := strings.Replace(studioPremiumTestDeckHTML(), `data-deck-element="headline-2" data-deck-type="text" style="position:absolute;left:120px;top:140px;width:1680px`, `data-deck-element="headline-2" data-deck-type="text" style="position:absolute;left:262px;top:140px;width:1538px`, 1)
	repairedWriter, _, err := fixture.app.createOSArtifactWithMetadata("workflow", "ship_deck repaired", repairedHTML, scoutParticipantName, map[string]string{
		"source": "process_stage", "goalParentId": fixture.parentID, "goalSubtaskId": "ship_deck",
		"processId": packagingStudioProcessID, "processStage": "ship_deck", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	ship := fixture.plan.subtaskByID("ship_deck")
	ship.Status, ship.ArtifactID = subtaskComplete, repairedWriter.ID
	ship.Review = &goalSubtaskReview{Verdict: goalReviewPass, By: "fixture"}
	draft := fixture.plan.subtaskByID("draft_compile")
	draft.Status = subtaskRunning
	draftStage := packagingStudioStage(t, fixture.def, "draft_compile")
	body, extra, err := compilePackagingStudioDraft(fixture.app, &fixture.plan, fixture.parentID, draftStage)
	if err != nil {
		t.Fatal(err)
	}
	fixture.engine.completeProcessStage(&fixture.plan, fixture.parentID, draft, draftStage, body, "repaired draft", extra)
	fixture.deck = mustArtifact(t, fixture.app, extra["deckArtifactId"])
	if fixture.deck.ID != originalDeckID || artifactVersion(fixture.deck) <= originalVersion || !strings.Contains(fixture.deck.Text, `headline-2" data-deck-type="text" style="position:absolute;left:262px;top:140px;width:1538px`) {
		t.Fatalf("repair did not version the candidate in place: original=%s/v%d repaired=%s/v%d", originalDeckID, originalVersion, fixture.deck.ID, artifactVersion(fixture.deck))
	}

	fixture.fileJury(t, "ready", []slideJuryRepair{})
	quality := fixture.plan.subtaskByID("quality_gate")
	quality.Status = subtaskRunning
	reviewedVersion := artifactVersion(fixture.deck)
	reviewedDigest := artifactCapabilityDigest(fixture.deck)
	var scorerCalls atomic.Int32
	fixture.runQualityGate(t, packagingQualityScoreJSON(9.3, "the repaired version is clean"), &scorerCalls)
	if scorerCalls.Load() != 0 || quality.Status != subtaskComplete {
		t.Fatalf("repaired ready deck did not ship from its fresh rendered verdict alone: calls=%d gate=%+v", scorerCalls.Load(), quality)
	}
	gateRecord := mustArtifact(t, fixture.app, quality.ArtifactID)
	juryRecord := mustArtifact(t, fixture.app, fixture.plan.subtaskByID("slide_jury").ArtifactID)
	if gateRecord.Metadata["slideJuryArtifactId"] != juryRecord.Metadata["slideJuryArtifactId"] || gateRecord.Metadata["reviewedDeckArtifactVersion"] != fmt.Sprintf("%d", reviewedVersion) || gateRecord.Metadata["reviewedDeckContentDigest"] != reviewedDigest {
		t.Fatalf("fresh repaired jury did not authorize the exact shipped revision: gate=%v jury=%v", gateRecord.Metadata, juryRecord.Metadata)
	}
	_, final, err := compilePackagingStudioShip(fixture.app, &fixture.plan, fixture.parentID, packagingStudioStage(t, fixture.def, "ship_compile"))
	if err != nil {
		t.Fatal(err)
	}
	delivered := mustArtifact(t, fixture.app, final["deckArtifactId"])
	if delivered.ID != originalDeckID || artifactVersion(delivered) != reviewedVersion || artifactCapabilityDigest(delivered) != reviewedDigest {
		t.Fatalf("delivered identity is not the exact repaired reviewed candidate: got=%s/v%d/%s want=%s/v%d/%s", delivered.ID, artifactVersion(delivered), artifactCapabilityDigest(delivered), originalDeckID, reviewedVersion, reviewedDigest)
	}
}

func TestSlideJuryUnsupportedClaimAgreementBlocksAndFixesAreMandatory(t *testing.T) {
	voices := []goalPanelVoice{
		{Persona: "headline_ear", Text: `{"pages":[{"page":1,"score":8.5,"fix":"Replace 42% with the cited 38% and add the source label.","blockers":["unsupported_claim"]}]}`},
		{Persona: "design_eye", Text: `{"pages":[{"page":1,"score":8.4,"fix":"Add the source and year directly below the 38% figure.","blockers":["unsupported_claim"]}]}`},
		{Persona: "room_gut", Text: `{"pages":[{"page":1,"score":9.1,"fix":"KEEP","blockers":[]}]}`},
	}
	readiness := evaluateSlideJuryReadiness(voices, 1)
	if readiness.Verdict != "needs_changes" || len(readiness.BlockingPages) != 1 || len(readiness.Repairs) != 1 || readiness.Repairs[0].Owner != "write" || len(readiness.Repairs[0].Fixes) != 2 {
		t.Fatalf("agreed unsupported claim did not block with exact fixes: %+v", readiness)
	}
	voices[0].Text = `{"pages":[{"page":1,"score":8.5,"fix":"","blockers":["unsupported_claim"]}]}`
	voices[1].Text = `{"pages":[{"page":1,"score":8.4,"blockers":["unsupported_claim"]}]}`
	if got := evaluateSlideJuryReadiness(voices, 1); got.Verdict != "needs_attention" {
		t.Fatalf("missing fixes produced %+v, want needs_attention", got)
	}
}

func TestGoalGateUsesDisplayedPrecisionAtThreshold(t *testing.T) {
	near := runGoalGate(context.Background(), goalGateSpec{Threshold: 9, Floor: 7, MaxRounds: 2, Score: func(context.Context) goalGateRound {
		return goalGateRound{Dimensions: []goalGateDimension{
			{Name: "one", Score: 9}, {Name: "two", Score: 9}, {Name: "three", Score: 9},
			{Name: "four", Score: 9}, {Name: "five", Score: 9}, {Name: "six", Score: 8.9},
		}}
	}})
	if near.Outcome != goalGateOutcomeAccept || near.Score != 9.0 || len(near.Gaps) != 0 {
		t.Fatalf("8.983333 average did not pass at displayed 9.0: %+v", near)
	}
	below := runGoalGate(context.Background(), goalGateSpec{Threshold: 9, Floor: 7, MaxRounds: 2, Score: func(context.Context) goalGateRound {
		return goalGateRound{Dimensions: []goalGateDimension{{Name: "quality", Score: 8.94}}}
	}})
	if below.Outcome != goalGateOutcomeRevise || below.Score != 8.9 || !strings.Contains(strings.Join(below.Gaps, " | "), "average 8.9 is below the 9.0 threshold") {
		t.Fatalf("material below-boundary score did not revise: %+v", below)
	}
}
