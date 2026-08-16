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
		{"presentation for this pitch", true},
		{"deck for the investor meeting", true},
		{"slides for the quarterly review", true},

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

// TestDeckDirectionEstablished tests the direction pass logic.
func TestDeckDirectionEstablished(t *testing.T) {
	cases := []struct {
		name    string
		query   string
		history []scoutChatTurn
		want    bool
	}{
		// User provides explicit direction in query
		{"dark theme in query", "make a deck with dark theme", nil, true},
		{"minimal style in query", "minimal presentation about AI", nil, true},
		{"corporate style in query", "corporate pitch deck", nil, true},

		// User confirms after direction pass
		{"yes confirmation after direction", "yes", []scoutChatTurn{
			{role: "scout", text: "What visual direction would you like?"},
		}, true},
		{"looks good confirmation", "looks good", []scoutChatTurn{
			{role: "scout", text: "I'm thinking a dark, minimal aesthetic"},
		}, true},
		{"proceed confirmation", "proceed", []scoutChatTurn{
			{role: "scout", text: "What visual style would you prefer?"},
		}, true},

		// Direction in history
		{"direction in previous user message", "build it", []scoutChatTurn{
			{role: "user", text: "I want a dark, modern look"},
		}, true},

		// No direction - should return false
		{"plain deck request no history", "make a 5-slide deck", nil, false},
		{"plain deck request with unrelated history", "make a 5-slide deck", []scoutChatTurn{
			{role: "user", text: "hello"},
			{role: "scout", text: "hi there"},
		}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deckDirectionEstablished(tc.query, tc.history)
			if got != tc.want {
				t.Errorf("deckDirectionEstablished(%q, history) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

// TestDeckDirectionPassReturnsDirectionQuestions tests that a fresh deck request
// triggers a direction pass (2-3 questions or proposed direction), not immediate deck generation.
func TestDeckDirectionPassReturnsDirectionQuestions(t *testing.T) {
	clearAgentRunnerEnv(t)
	t.Setenv("BONFIRE_AGENT_RUNNER", "stub")

	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-api-key"
	setupAuthTestEnv(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("test user not found")
	}

	// Mock the LLM to return direction questions
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		// Check if this is a direction pass prompt
		if strings.Contains(request.Input, "aesthetic direction pass") || strings.Contains(request.Input, "visual direction") {
			return "Should this feel corporate and buttoned-up, or more startup energy? Also, are you thinking full-bleed images or clean typographic slides?", nil
		}
		// For deck generation, return HTML
		return `<!doctype html><html><head><title>Test</title></head><body></body></html>`, nil
	})

	// First call with no history should return direction pass, not HTML deck
	reply, err := app.resolveInlineDeckReply(context.Background(), user, "make a 5-slide deck about AI", nil)
	if err != nil {
		t.Fatalf("resolveInlineDeckReply: %v", err)
	}

	// Should be a regular message (not a thread card)
	if reply.Kind != "message" {
		t.Errorf("reply.Kind=%q, want message", reply.Kind)
	}

	// Should NOT be HTML - should be direction questions
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(reply.Text)), "<!doctype html") {
		t.Errorf("First deck request should return direction pass, not HTML deck. Got HTML content.")
	}
}

// TestInlineDeckReplyProducesHTMLDeck tests that when direction is established,
// the deck request produces an actual HTML deck.
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

	// Include direction in the request to skip direction pass
	reply, err := app.resolveInlineDeckReply(context.Background(), user, "make a 5-slide deck about AI with dark theme", nil)
	if err != nil {
		t.Fatalf("resolveInlineDeckReply: %v", err)
	}

	// Verify the reply is a regular message with HTML content (not a thread card)
	if reply.Kind != "message" {
		t.Errorf("reply.Kind=%q, want message (not thread card)", reply.Kind)
	}
	if reply.IntentOutcome != string(conversationIntentConversationalReply) {
		t.Errorf("reply.IntentOutcome=%q, want %q", reply.IntentOutcome, conversationIntentConversationalReply)
	}
	// Verify the text content is HTML
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(reply.Text)), "<!doctype html") {
		t.Errorf("reply.Text does not start with <!doctype html: %q", reply.Text[:min(50, len(reply.Text))])
	}
}

// TestInlineDeckAfterDirectionConfirmation tests that saying "yes" or "looks good"
// after a direction pass produces the actual deck.
func TestInlineDeckAfterDirectionConfirmation(t *testing.T) {
	clearAgentRunnerEnv(t)
	t.Setenv("BONFIRE_AGENT_RUNNER", "stub")

	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-api-key"
	setupAuthTestEnv(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("test user not found")
	}

	// Mock the LLM response to return HTML deck content
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		return `<!doctype html>
<html lang="en">
<head><title>Test Deck</title></head>
<body><section class="pg on"><h1>Test</h1></section></body>
</html>`, nil
	})

	// Simulate conversation where Scout asked direction questions
	history := []scoutChatTurn{
		{role: "user", text: "make a deck about our product"},
		{role: "scout", text: "What visual direction would you like? Corporate and polished, or startup energy?"},
	}

	// User confirms with "looks good"
	reply, err := app.resolveInlineDeckReply(context.Background(), user, "looks good, build it", history)
	if err != nil {
		t.Fatalf("resolveInlineDeckReply: %v", err)
	}

	// Should produce HTML deck after confirmation
	if reply.Kind != "message" {
		t.Errorf("reply.Kind=%q, want message", reply.Kind)
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(reply.Text)), "<!doctype html") {
		t.Errorf("After direction confirmation, should get HTML deck. Got: %q", reply.Text[:min(100, len(reply.Text))])
	}
}

// TestDeckAskRoutesToInlineDeckWithStubWorker tests the live path: a deck ask
// through the routing system with stub worker should produce an in-thread
// html_deck message, not a proposal or outline.
func TestDeckAskRoutesToInlineDeckWithStubWorker(t *testing.T) {
	// Force the agent runner to stub (unavailable)
	clearAgentRunnerEnv(t)
	t.Setenv("BONFIRE_AGENT_RUNNER", "stub")

	if scoutAgentWorkerAvailable() {
		t.Fatal("scoutAgentWorkerAvailable() = true with stub runner, want false")
	}

	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-api-key"

	// Mock the LLM to return HTML deck for deck generation prompts
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		// Return HTML deck content
		return `<!doctype html>
<html><head><title>Test Deck</title></head>
<body><section class="slide"><h1>Test</h1></section></body>
</html>`, nil
	})

	cases := []string{
		"make a 5-slide deck",
		"create a presentation",
		"build a pitch deck",
		"presentation for this pitch",
	}

	for _, ask := range cases {
		t.Run(ask, func(t *testing.T) {
			// Test that routing identifies this as a deck request
			if !scoutChatDeckRequestDetected(ask) {
				t.Errorf("scoutChatDeckRequestDetected(%q) = false, want true", ask)
			}

			// Test that the router routes to conversational_reply (not work)
			decision := app.routeConversationIntentWithInput(
				context.Background(),
				ask,
				conversationIntentTurn{Text: ask},
				nil,
			)

			// With stub worker, deck asks should not mint work proposals
			if decision.Outcome == conversationIntentStartPrivateWork || decision.Outcome == conversationIntentApprovalRequired {
				t.Errorf("outcome=%s for deck ask with stub worker, want conversational_reply", decision.Outcome)
			}
		})
	}
}

// --- Client-side idempotency tests -------------------------------------------
// Note: The server correctly implements idempotency (same key = same thread).
// The double-fire issue in production was due to the client sending different
// keys. The client-side debounce is the actual fix for that case.

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
