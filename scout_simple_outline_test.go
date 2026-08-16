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

// --- Deck request detection tests --------------------------------------------

func TestScoutChatDeckRequestDetected(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		// Positive cases: real deck/presentation requests
		{"make a 5-slide deck", true},
		{"create a deck about our product", true},
		{"build a pitch deck", true},
		{"make me a presentation", true},
		{"create a 10-slide presentation", true},
		{"build slides for the quarterly review", true},
		{"make a slide deck", true},

		// Negative cases: outline-only requests
		{"just the outline", false},
		{"give me the outline only", false},
		{"slide outline please", false},

		// Negative cases: non-deck requests
		{"research the market", false},
		{"design a logo", false},
		{"create a business plan", false},

		// Edge cases
		{"", false},
		{"deck", false},       // Just "deck" without creation verb
		{"presentation", false}, // Just "presentation" without creation verb
	}

	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			got := scoutChatDeckRequestDetected(tc.text)
			if got != tc.want {
				t.Errorf("scoutChatDeckRequestDetected(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// --- Inline deck generation tests --------------------------------------------

func TestInlineDeckReplyProducesHTMLDeck(t *testing.T) {
	// Force the agent runner to stub (unavailable)
	clearAgentRunnerEnv(t)
	t.Setenv("BONFIRE_AGENT_RUNNER", "stub")

	if scoutAgentWorkerAvailable() {
		t.Fatal("scoutAgentWorkerAvailable() = true with stub runner, want false")
	}

	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-api-key"
	setupAuthTestEnv(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("test user not found")
	}

	// Create a test thread
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Test deck", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}

	userMessage := scoutChatMessageRecord{
		ID:   "test-user-msg",
		Kind: "message",
		Role: "user",
		Text: "make a 5-slide deck about AI",
	}

	// Mock the LLM response to return HTML deck content
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		// Return a simple HTML deck
		return `<!doctype html>
<html lang="en">
<head><title>AI Deck</title></head>
<body>
<section class="slide"><h1>AI Overview</h1></section>
<section class="slide"><h2>Applications</h2></section>
</body>
</html>`, nil
	})

	reply, err := app.resolveInlineDeckReply(context.Background(), user, thread, userMessage, "make a 5-slide deck about AI", nil)
	if err != nil {
		t.Fatalf("resolveInlineDeckReply: %v", err)
	}

	// Verify the reply is a thread ref
	if reply.Kind != "thread" {
		t.Errorf("reply.Kind=%q, want thread", reply.Kind)
	}
	if reply.Thread == nil {
		t.Fatal("reply.Thread is nil")
	}
	if reply.Thread.ArtifactID == "" {
		t.Error("reply.Thread.ArtifactID is empty")
	}
	if reply.Thread.Status != "complete" {
		t.Errorf("reply.Thread.Status=%q, want complete", reply.Thread.Status)
	}

	// Verify the artifact was created with type html_deck
	artifact, found := app.osArtifactByID(reply.Thread.ArtifactID)
	if !found {
		t.Fatal("artifact not found")
	}
	if artifact.Metadata["type"] != artifactTypeHTMLDeck {
		t.Errorf("artifact type=%q, want %q", artifact.Metadata["type"], artifactTypeHTMLDeck)
	}
	// Verify content is HTML
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(artifact.Text)), "<!doctype html") {
		t.Errorf("artifact text does not start with <!doctype html: %q", artifact.Text[:min(50, len(artifact.Text))])
	}
}

// --- Server-side idempotency tests -------------------------------------------

func TestHomeOpeningIdempotency(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	setupAuthTestEnv(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("test user not found")
	}

	key := "test-idempotency-key-12345"
	text := "What is the weather today?"

	// First call should create the thread
	thread1, created1, err := app.ensureScoutHomeOpening(user, key, text)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !created1 {
		t.Error("first call should create the thread")
	}

	// Second call with same key should return the same thread
	thread2, created2, err := app.ensureScoutHomeOpening(user, key, text)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if created2 {
		t.Error("second call should not create a new thread")
	}
	if thread1.ID != thread2.ID {
		t.Errorf("thread IDs differ: %q vs %q", thread1.ID, thread2.ID)
	}

	// Third call with DIFFERENT key should create a NEW thread
	thread3, created3, err := app.ensureScoutHomeOpening(user, "different-key-67890", text)
	if err != nil {
		t.Fatalf("third call: %v", err)
	}
	if !created3 {
		t.Error("third call with different key should create a new thread")
	}
	if thread1.ID == thread3.ID {
		t.Error("different keys should produce different thread IDs")
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
