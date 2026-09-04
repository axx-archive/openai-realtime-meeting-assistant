package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBusinessCompatibilityRejectsLaunchBeforeTemplateFallback(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	before := len(app.memory.snapshot(0))
	for _, spec := range []goalLaunchSpec{
		{Objective: "Build a private result", ToolTemplate: "business_private_improvement_v99"},
		{Objective: "Build a private result", ToolTemplate: " BUSINESS_unknown "},
		{Objective: "Build a private result", Origin: map[string]string{businessEpisodeBindingMetadataKey: `{"schemaVersion":999}`}},
		{Objective: "Build a private result", Origin: map[string]string{businessEpisodeBindingMetadataKey: ""}},
		{Objective: "Build a private result", Origin: map[string]string{"businessId": "future-business"}},
		{Objective: "Build a private result", Origin: map[string]string{"originKind": "business_episode"}},
	} {
		if _, err := app.launchGoalThread(spec); !errors.Is(err, errBusinessExecutionUnavailable) {
			t.Fatalf("launch normalized away Business authority: %v", err)
		}
	}
	if len(app.memory.snapshot(0)) != before {
		t.Fatal("rejected Business launch created state")
	}
	// A normal business-themed objective or the unrelated Riff episode field is
	// ordinary content, never an autonomous execution marker.
	if err := businessExecutionLaunchError(goalLaunchSpec{Objective: "Write a business plan", ToolTemplate: "unrelated_unknown_template", Origin: map[string]string{"episodeId": "riff-one"}}); err != nil {
		t.Fatal(err)
	}
}

func TestBusinessCompatibilityPreservesUnknownBindingAndRejectsDecomposition(t *testing.T) {
	var calls atomic.Int32
	engine := newGoalEngine(nil)
	engine.openAIResponder = func(context.Context, string, openAITextRequest) (string, error) { calls.Add(1); return "", nil }
	for _, binding := range []string{`null`, `{}`, `"malformed-shape"`, `{"schemaVersion":999,"businessId":"future"}`} {
		plan, ok := decodeGoalPlan(`{"state":"decompose","objective":"Future mission","businessEpisodeBinding":` + binding + `}`)
		if !ok {
			t.Fatal("compatibility reader lost visible plan")
		}
		encoded, err := json.Marshal(plan)
		if err != nil || !strings.Contains(string(encoded), `"businessEpisodeBinding":`+binding) {
			t.Fatalf("binding lost on roundtrip: %s %v", encoded, err)
		}
		if err := engine.prepareGoalRoute(&plan, ""); !errors.Is(err, errBusinessExecutionUnavailable) || plan.routeVerified {
			t.Fatalf("plain legacy branch admitted binding: %v", err)
		}
		if err := engine.decompose(context.Background(), &plan); !errors.Is(err, errBusinessExecutionUnavailable) {
			t.Fatal("Business binding reached decomposition")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("Business compatibility invoked provider")
	}
	legacy := goalPlan{State: goalStateDecompose, Objective: "Write a business plan"}
	if err := engine.prepareGoalRoute(&legacy, ""); err != nil {
		t.Fatalf("unbound legacy plan rejected: %v", err)
	}
}

func TestBusinessCompatibilityDriveRestartAndChildRouteStayBlocked(t *testing.T) {
	for _, marker := range []string{"plan", "metadata", "reserved_process"} {
		t.Run(marker, func(t *testing.T) {
			app := newIsolatedKanbanBoardApp(t)
			var starts atomic.Int32
			oldStart := startAgentThreadAsync
			startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) { starts.Add(1) }
			t.Cleanup(func() { startAgentThreadAsync = oldStart })
			plan := goalPlan{PlanVersion: goalPlanVersion, GoalID: "business-compatibility-goal", State: goalStateExecute, Objective: "Run a future business", Subtasks: []goalSubtask{{ID: "writer", Status: subtaskReady, Mode: "research", Runner: agentRunnerOpenAIText}}}
			metadata := map[string]string{"mode": "goal", "threadStatus": "running", "status": "running"}
			switch marker {
			case "plan":
				plan.BusinessEpisodeBinding = json.RawMessage(`{"schemaVersion":999}`)
			case "metadata":
				metadata[businessEpisodeBindingMetadataKey] = "bad-json"
			case "reserved_process":
				plan.ToolTemplate = "business_future_unregistered"
			}
			raw, _ := json.Marshal(plan)
			metadata["goalPlan"] = string(raw)
			parent, _, err := app.createOSArtifactWithMetadata("workflow", "Business compatibility", "draft retained", "tester", metadata)
			if err != nil {
				t.Fatal(err)
			}
			before := len(app.memory.snapshot(0))
			engine := newGoalEngine(app)
			if err := engine.launchSubtask(&plan, &plan.Subtasks[0], parent.ID); !errors.Is(err, errBusinessExecutionUnavailable) {
				t.Fatalf("child launch escaped: %v", err)
			}
			app.runGoalThread(parent.ID)
			app = newKanbanBoardApp()
			app.reconcileGoalThread(parent.ID)
			stored, ok := app.osArtifactByID(parent.ID)
			if !ok {
				t.Fatal("blocked work disappeared")
			}
			after, ok := decodeGoalPlan(stored.Metadata["goalPlan"])
			if !ok || after.State != goalStateBlocked || !strings.Contains(after.Blocker, "business execution is unavailable") {
				t.Fatalf("work not visibly blocked: %+v", after)
			}
			if marker == "plan" && len(after.BusinessEpisodeBinding) == 0 {
				t.Fatal("blocked persist stripped business binding")
			}
			if starts.Load() != 0 || len(app.memory.snapshot(0)) != before {
				t.Fatalf("blocked work dispatched or created children: %d", starts.Load())
			}
			child := meetingMemoryEntry{Metadata: map[string]string{"goalParentId": parent.ID}}
			if err := app.verifyGoalChildRoute(child); !errors.Is(err, errBusinessExecutionUnavailable) {
				t.Fatalf("child parent fence missing: %v", err)
			}
			if err := app.verifyGoalChildReservation(child); !errors.Is(err, errBusinessExecutionUnavailable) {
				t.Fatalf("child reservation fence missing: %v", err)
			}
		})
	}
	app := newIsolatedKanbanBoardApp(t)
	if err := app.verifyGoalChildRoute(meetingMemoryEntry{Metadata: map[string]string{"businessId": "tagged-child"}}); !errors.Is(err, errBusinessExecutionUnavailable) {
		t.Fatal("tagged child was not recognized")
	}
}
