package main

import (
	"context"
	"strings"
	"testing"
)

// --- Simple outline phrase guard tests ---------------------------------------

func TestScoutChatSimpleOutlineRequestDetected(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		// Positive cases: simple in-thread outline asks
		{"make a 5-slide outline, keep in-thread", true},
		{"Create a five-slide outline, keep in thread", true},
		{"give me a slide outline, do not email", true},
		{"quick outline of the strategy", true},
		{"simple outline for the meeting", true},
		{"in-thread outline for the Q3 review", true},
		{"presentation outline, keep in-thread", true},
		{"5 slide presentation, don't email", true},

		// Negative cases: heavier asks that should NOT be short-circuited
		{"run the deck outline process with full review", false},
		{"full deck outline with strategic design review", false},
		{"outline with review and approval gates", false},
		{"full process deck outline", false},
		{"run the deck outline", false},

		// Negative cases: non-outline requests
		{"research the market opportunity", false},
		{"create a business plan", false},
		{"design a logo", false},
		{"what is packaging studio?", false},

		// Edge cases
		{"", false},
		{"outline", false}, // Just "outline" without simple-ask phrases
		{"presentation", false}, // Just "presentation" without simple-ask phrases
	}

	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			got := scoutChatSimpleOutlineRequestDetected(tc.text)
			if got != tc.want {
				t.Errorf("scoutChatSimpleOutlineRequestDetected(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// --- Routing tests for simple outline asks -----------------------------------

func TestSimpleOutlineAsksRouteToConversationalReply(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-api-key"

	routerCalls := 0
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		routerCalls++
		t.Fatalf("router should not be called for simple outline asks; got workflow=%s", request.Workflow)
		return "", nil
	})

	cases := []string{
		"make a 5-slide outline, keep in-thread",
		"Create a five-slide outline, keep in thread",
		"quick outline of the strategy, don't email",
		"simple outline for the meeting, keep in-thread",
	}

	for _, ask := range cases {
		t.Run(ask, func(t *testing.T) {
			decision := app.routeConversationIntentWithInput(context.Background(), ask, conversationIntentTurn{Text: ask}, nil)

			if decision.Outcome != conversationIntentConversationalReply {
				t.Errorf("outcome=%s, want conversational_reply", decision.Outcome)
			}
			if decision.Work != nil {
				t.Errorf("work=%#v, want nil (no proposal)", decision.Work)
			}
			if decision.Approval != nil {
				t.Errorf("approval=%#v, want nil (no approval gate)", decision.Approval)
			}
		})
	}

	if routerCalls != 0 {
		t.Errorf("router calls=%d, want 0 (simple outline should bypass router)", routerCalls)
	}
}

// --- Agent worker unavailable tests ------------------------------------------

func TestWorkerUnavailableDoesNotMintWorkProposals(t *testing.T) {
	// Force the agent runner to stub (unavailable)
	clearAgentRunnerEnv(t)
	t.Setenv("BONFIRE_AGENT_RUNNER", "stub")

	if scoutAgentWorkerAvailable() {
		t.Fatal("scoutAgentWorkerAvailable() = true with stub runner, want false")
	}

	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-api-key"

	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow == "scout_route" {
			// Router returns a work proposal
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
				Route:     "tool_run",
				ToolID:    "deep_research",
				Objective: "research the market",
			}), nil
		}
		return "", nil
	})

	decision := app.routeConversationIntentWithInput(
		context.Background(),
		"research the market opportunity",
		conversationIntentTurn{Text: "research the market opportunity"},
		nil,
	)

	// Even though router returns tool_run, worker-unavailable guard should
	// convert it to conversational_reply
	if decision.Outcome != conversationIntentConversationalReply {
		t.Errorf("outcome=%s with stub worker, want conversational_reply", decision.Outcome)
	}
	if decision.Work != nil {
		t.Errorf("work=%#v with stub worker, want nil (no work proposal)", decision.Work)
	}
	if decision.Approval != nil {
		t.Errorf("approval=%#v with stub worker, want nil (no approval gate)", decision.Approval)
	}
}

func TestWorkerUnavailableSkipsDeterministicGuard(t *testing.T) {
	// Force the agent runner to stub (unavailable)
	clearAgentRunnerEnv(t)
	t.Setenv("BONFIRE_AGENT_RUNNER", "stub")

	if scoutAgentWorkerAvailable() {
		t.Fatal("scoutAgentWorkerAvailable() = true with stub runner, want false")
	}

	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-api-key"

	routerCalls := 0
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		routerCalls++
		if request.Workflow == "scout_route" {
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
				Outcome: string(conversationIntentConversationalReply),
			}), nil
		}
		return "", nil
	})

	// "run the deck outline" normally triggers deterministic guard
	decision := app.routeConversationIntentWithInput(
		context.Background(),
		"run the deck outline",
		conversationIntentTurn{Text: "run the deck outline"},
		nil,
	)

	// With stub worker, deterministic guard should be skipped
	// and router should be called (which returns conversational_reply)
	if decision.Outcome != conversationIntentConversationalReply {
		t.Errorf("outcome=%s, want conversational_reply", decision.Outcome)
	}
	if routerCalls == 0 {
		t.Error("router should be called when worker unavailable skips deterministic guard")
	}
}

func TestWorkerAvailableAllowsDeterministicGuard(t *testing.T) {
	// Ensure worker is available (default OpenAI runner)
	clearAgentRunnerEnv(t)
	t.Setenv("BONFIRE_AGENT_RUNNER", "openai_text")

	if !scoutAgentWorkerAvailable() {
		t.Fatal("scoutAgentWorkerAvailable() = false with openai_text runner, want true")
	}

	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-api-key"

	routerCalls := 0
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		routerCalls++
		t.Error("router should not be called when deterministic guard fires")
		return "", nil
	})

	// "packaging studio" normally triggers deterministic guard
	decision := app.routeConversationIntentWithInput(
		context.Background(),
		"run the packaging studio end to end",
		conversationIntentTurn{Text: "run the packaging studio end to end"},
		nil,
	)

	// Deterministic guard should fire and return a work proposal
	if decision.Outcome != conversationIntentApprovalRequired && decision.Outcome != conversationIntentStartPrivateWork {
		t.Errorf("outcome=%s, want work proposal from deterministic guard", decision.Outcome)
	}
	if routerCalls != 0 {
		t.Errorf("router calls=%d, want 0 (deterministic guard should bypass router)", routerCalls)
	}
}

// --- Artifacts mode rejection test -------------------------------------------

func TestArtifactsModeIsRejected(t *testing.T) {
	// Verify that "artifacts" is not a valid workstream mode
	modes := []string{"research", "design", "grill", "workflow", "artifacts"}

	for _, mode := range modes {
		work := conversationWorkDecision{
			Kind:      conversationWorkWorkstream,
			Mode:      mode,
			Objective: "test objective", // Required by validateRoute
		}
		err := work.validateRoute()
		if mode == "artifacts" {
			if err == nil {
				t.Errorf("mode=%q should be rejected, got nil error", mode)
			}
			// Verify error is specifically about invalid mode
			if err != nil && !strings.Contains(err.Error(), "mode") {
				t.Errorf("mode=%q error should mention invalid mode, got: %v", mode, err)
			}
		} else {
			if err != nil {
				t.Errorf("mode=%q should be accepted, got error: %v", mode, err)
			}
		}
	}
}
