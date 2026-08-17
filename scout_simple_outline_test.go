package main

import (
	"context"
	"fmt"
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

		// User confirms after direction pass (realistic wording without "direction/aesthetic/visual/style")
		{"yes confirmation after corporate/startup question", "yes", []scoutChatTurn{
			{role: "user", text: "make a deck about our product"},
			{role: "scout", text: "Should this feel corporate and buttoned-up, or more startup energy?"},
		}, true},
		{"looks good after full-bleed question", "looks good", []scoutChatTurn{
			{role: "user", text: "make a presentation"},
			{role: "scout", text: "Full-bleed imagery to set the mood, or clean typographic slides?"},
		}, true},
		{"proceed after design question", "proceed", []scoutChatTurn{
			{role: "user", text: "build a pitch deck"},
			{role: "scout", text: "Are you presenting to investors or a creative team?"},
		}, true},

		// Direction in history
		{"direction in previous user message", "build it", []scoutChatTurn{
			{role: "user", text: "I want a dark, modern look"},
		}, true},

		// Scout asked direction questions with explicit keywords
		{"scout asked about visual direction", "ok", []scoutChatTurn{
			{role: "user", text: "make a deck"},
			{role: "scout", text: "What visual direction would you like?"},
		}, true},

		// No direction - should return false
		{"plain deck request no history", "make a 5-slide deck", nil, false},
		{"plain deck request with unrelated history", "make a 5-slide deck", []scoutChatTurn{
			{role: "user", text: "hello"},
			{role: "scout", text: "hi there"},
		}, false},
		// Confirmation without a deck request in history should NOT trigger
		{"yes without deck request", "yes", []scoutChatTurn{
			{role: "user", text: "hello"},
			{role: "scout", text: "Should this feel corporate?"},
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

	// Simulate conversation where Scout asked direction questions (realistic wording)
	// This tests the live path: "Should this feel corporate or startup? Full-bleed or typographic?"
	history := []scoutChatTurn{
		{role: "user", text: "make a deck about our product"},
		{role: "scout", text: "Should this feel corporate and buttoned-up, or more startup energy? Are you thinking full-bleed imagery or clean typographic slides?"},
	}

	// User confirms with just "yes"
	reply, err := app.resolveInlineDeckReply(context.Background(), user, "yes", history)
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

// TestInlineDeckConfirmationVariants tests various confirmation phrases after direction pass.
func TestInlineDeckConfirmationVariants(t *testing.T) {
	clearAgentRunnerEnv(t)
	t.Setenv("BONFIRE_AGENT_RUNNER", "stub")

	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-api-key"
	setupAuthTestEnv(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("test user not found")
	}

	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		return `<!doctype html><html><head><title>Test</title></head><body></body></html>`, nil
	})

	// Realistic direction pass that doesn't contain "direction/aesthetic/visual/style"
	history := []scoutChatTurn{
		{role: "user", text: "make a 5-slide deck"},
		{role: "scout", text: "Should this feel corporate and buttoned-up, or more startup energy? Full-bleed imagery to set the mood, or clean typographic slides?"},
	}

	confirmations := []string{"yes", "looks good", "proceed", "perfect", "great", "sure", "ok"}

	for _, confirm := range confirmations {
		t.Run(confirm, func(t *testing.T) {
			reply, err := app.resolveInlineDeckReply(context.Background(), user, confirm, history)
			if err != nil {
				t.Fatalf("resolveInlineDeckReply(%q): %v", confirm, err)
			}
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(reply.Text)), "<!doctype html") {
				t.Errorf("Confirmation %q after direction pass should generate deck, got: %q", confirm, reply.Text[:min(80, len(reply.Text))])
			}
		})
	}
}

// TestDeckConfirmationDetectedRoutesCorrectly tests that scoutChatDeckConfirmationDetected
// properly gates confirmations to the inline deck path. This tests the actual routing gate,
// not just resolveInlineDeckReply.
func TestDeckConfirmationDetectedRoutesCorrectly(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		history []scoutChatTurn
		want    bool
	}{
		// Should route to deck generation
		{
			name: "yes after direction pass with deck request",
			text: "yes",
			history: []scoutChatTurn{
				{role: "user", text: "make a 5-slide deck"},
				{role: "scout", text: "Should this feel corporate and buttoned-up, or more startup energy?"},
			},
			want: true,
		},
		{
			name: "looks good after direction pass",
			text: "looks good",
			history: []scoutChatTurn{
				{role: "user", text: "create a presentation about our product"},
				{role: "scout", text: "Are you presenting to investors or a creative team? Full-bleed imagery or clean typographic slides?"},
			},
			want: true,
		},
		{
			name: "proceed after direction pass",
			text: "proceed",
			history: []scoutChatTurn{
				{role: "user", text: "build a pitch deck"},
				{role: "scout", text: "Should this feel professional and polished, or more startup energy?"},
			},
			want: true,
		},
		// Should NOT route (missing deck request in history)
		{
			name: "yes without deck request",
			text: "yes",
			history: []scoutChatTurn{
				{role: "user", text: "hello"},
				{role: "scout", text: "Should this feel corporate?"},
			},
			want: false,
		},
		// Should NOT route (no direction pass from Scout)
		{
			name: "yes after unrelated scout message",
			text: "yes",
			history: []scoutChatTurn{
				{role: "user", text: "make a deck"},
				{role: "scout", text: "I'll help you with that."},
			},
			want: false,
		},
		// Should NOT route (not a confirmation phrase)
		{
			name: "non-confirmation after direction pass",
			text: "I want something different",
			history: []scoutChatTurn{
				{role: "user", text: "make a deck"},
				{role: "scout", text: "Should this feel corporate?"},
			},
			want: false,
		},
		// Choices card scenario — the router issues a clarify_once choices card
		// asking "What should the 5-slide deck be about?" The user types "yes".
		// This must route to deck generation, not return "I still need the subject".
		{
			name: "yes after choices card topic question",
			text: "yes",
			history: []scoutChatTurn{
				{role: "user", text: "make a 5-slide deck"},
				{role: "scout", text: "What should the 5-slide deck be about?"},
			},
			want: true,
		},
		{
			name: "yes after choices card with topic in question",
			text: "yes",
			history: []scoutChatTurn{
				{role: "user", text: "create a presentation"},
				{role: "scout", text: "What topic should the presentation cover?"},
			},
			want: true,
		},
		{
			name: "looks good after slides question",
			text: "looks good",
			history: []scoutChatTurn{
				{role: "user", text: "build a pitch deck"},
				{role: "scout", text: "What should these slides focus on?"},
			},
			want: true,
		},
		// Empty/edge cases
		{
			name: "empty text",
			text: "",
			history: []scoutChatTurn{
				{role: "user", text: "make a deck"},
				{role: "scout", text: "Should this feel corporate?"},
			},
			want: false,
		},
		{
			name: "empty history",
			text: "yes",
			history: nil,
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scoutChatDeckConfirmationDetected(tc.text, tc.history)
			if got != tc.want {
				t.Errorf("scoutChatDeckConfirmationDetected(%q, history) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// TestDeckGenerationWithNoTopic tests that "make a 5-slide deck" (no topic)
// followed by "yes" generates a deck instead of "I'm missing the subject".
// This is the exact prod-test scenario.
func TestDeckGenerationWithNoTopic(t *testing.T) {
	clearAgentRunnerEnv(t)
	t.Setenv("BONFIRE_AGENT_RUNNER", "stub")

	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-api-key"
	setupAuthTestEnv(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("test user not found")
	}

	// Mock the LLM response
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		// CRITICAL: The prompt must NOT contain "What's the deck about" — that's the #34 hole
		if strings.Contains(request.Input, "What's the deck about") {
			t.Error("Prompt contains 'What's the deck about' — this causes the LLM to refuse. extractDirectionContext must filter it out.")
		}
		// Check that the prompt does NOT just say "User request: yes"
		if strings.Contains(request.Input, "User request: yes") {
			t.Error("Prompt passed 'yes' as user request — should have built a proper request")
		}
		// Check that the prompt contains the CRITICAL instruction to not refuse
		if !strings.Contains(request.Input, "MUST generate") {
			t.Error("Prompt missing CRITICAL instruction to always generate")
		}
		return `<!doctype html>
<html lang="en">
<head><title>Future of Work</title></head>
<body><section class="pg on"><h1>The Future of Work</h1></section></body>
</html>`, nil
	})

	// Scenario: "make a 5-slide deck" (no topic), then direction pass asking about topic, then "yes"
	// This is the exact prod-test scenario that failed at 381d004a
	history := []scoutChatTurn{
		{role: "user", text: "make a 5-slide deck"},
		{role: "scout", text: "What's the deck about, and who needs to buy into it? Should it feel polished and corporate, or more cinematic and culture-forward?"},
	}

	// User confirms with just "yes" — should NOT return "I still need the deck topic"
	reply, err := app.resolveInlineDeckReply(context.Background(), user, "yes", history)
	if err != nil {
		t.Fatalf("resolveInlineDeckReply: %v", err)
	}

	// Should produce HTML deck, not a refusal
	if reply.Kind != "message" {
		t.Errorf("reply.Kind=%q, want message", reply.Kind)
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(reply.Text)), "<!doctype html") {
		t.Errorf("Expected HTML deck, got: %q", reply.Text[:min(150, len(reply.Text))])
	}
	// Should NOT contain refusal text
	if strings.Contains(strings.ToLower(reply.Text), "need") && strings.Contains(strings.ToLower(reply.Text), "topic") {
		t.Error("Reply contains 'need...topic' refusal — should have generated a deck")
	}
	if strings.Contains(strings.ToLower(reply.Text), "missing") && strings.Contains(strings.ToLower(reply.Text), "subject") {
		t.Error("Reply contains 'missing...subject' refusal — should have generated a deck")
	}
}

// TestDeckGenerationAfterApproachBProse tests deck confirmation after Approach B prose
// using the REAL append path (appendScoutChatThreadMessage) WITH THE WORKER LIVE.
// The early intercept must catch the deck confirmation BEFORE the router is called,
// so no matter what the router would return, we always generate a deck.
func TestDeckGenerationAfterApproachBProse(t *testing.T) {
	// Test multiple Approach B phrasings — live keeps inventing new ones
	approachBVariants := []string{
		// Original live quote
		"What's the deck about, and who's in the room? Should it feel polished and investor-ready, or more like a bold creative pitch? Do you want cinematic imagery carrying the mood, or a clean typographic system doing the work?",
		// New live quote from eef34845
		"What's the deck about, and who needs to believe it? Should it feel polished and investor-grade, or more cinematic and culture-forward? Do you want image-led slides or a clean typographic system that makes the argument carry the weight?",
		// Live quote from fb067e0 prod-test (2026-08-17) — newest failure
		"What's the deck about, and who's in the room? Should it feel polished and credibility-first, or more cinematic and culture-led? Do you want bold full-bleed imagery, or a clean typographic system with a few strong diagrams?",
	}

	for _, liveApproachBProse := range approachBVariants {
		t.Run(liveApproachBProse[:50]+"...", func(t *testing.T) {
			// Worker is LIVE — not stubbed. Deck confirmation must still generate a deck.
			clearAgentRunnerEnv(t)
			t.Setenv("BONFIRE_AGENT_RUNNER", "openai_text")
			setupAuthTestEnv(t)

			app := newIsolatedKanbanBoardApp(t)
			app.apiKey = "test-api-key"

			routerCalls := 0
			swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
				switch request.Workflow {
				case "scout_route":
					routerCalls++
					if routerCalls == 1 {
						// First call: "make a 5-slide deck" → router returns Approach B clarify_once prose
						return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
							Outcome: string(conversationIntentClarifyOnce),
							Message: liveApproachBProse,
						}), nil
					}
					// Second call should NOT happen — early intercept catches deck confirmation
					t.Error("Router was called for deck confirmation — early intercept should have caught it")
					return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
						Outcome: string(conversationIntentClarifyOnce),
						Message: "I still need more information.",
					}), nil
				case "scout_chat":
					// Inline deck generation — return HTML
					return `<!doctype html>
<html lang="en">
<head><title>Future of Work</title></head>
<body><section class="pg on"><h1>The Future of Work</h1></section></body>
</html>`, nil
				default:
					t.Fatalf("unexpected workflow %q", request.Workflow)
					return "", nil
				}
			})

			thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Approach B test", scoutChatVisibilityPrivate)
			if err != nil {
				t.Fatal(err)
			}
			user := accountStore().findUser("aj@shareability.com")

			// 1. First message: "make a 5-slide deck" → Approach B clarify_once
			first, err := app.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "make a 5-slide deck", nil, "")
			if err != nil {
				t.Fatalf("first message: %v", err)
			}
			if first["intentOutcome"] != string(conversationIntentClarifyOnce) {
				t.Fatalf("first outcome=%v, want clarify_once", first["intentOutcome"])
			}
			firstAnswer, ok := first["answer"].(scoutChatMessageRecord)
			if !ok || firstAnswer.Text != liveApproachBProse || firstAnswer.IntentOutcome != string(conversationIntentClarifyOnce) {
				t.Fatalf("first answer=%+v, want Approach B prose with clarify_once", firstAnswer)
			}

			// 2. Second message: "yes" → early intercept catches this and generates deck
			// Router should NOT be called for the second message
			second, err := app.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "yes", nil, "")
			if err != nil {
				t.Fatalf("second message: %v", err)
			}
			// Should be conversational_reply (deck generated), NOT unavailable
			if second["intentOutcome"] == string(conversationIntentUnavailable) {
				unavailable, _ := second["unavailable"].(map[string]any)
				t.Fatalf("second message got unavailable=%+v — early intercept should have generated deck", unavailable)
			}
			if second["intentOutcome"] != string(conversationIntentConversationalReply) {
				t.Fatalf("second outcome=%v, want conversational_reply", second["intentOutcome"])
			}
			secondAnswer, ok := second["answer"].(scoutChatMessageRecord)
			if !ok {
				t.Fatalf("second answer missing: %+v", second)
			}
			// Should produce HTML deck
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(secondAnswer.Text)), "<!doctype html") {
				t.Errorf("Expected HTML deck, got: %q", secondAnswer.Text[:min(150, len(secondAnswer.Text))])
			}
			// Should NOT contain refusal text
			lower := strings.ToLower(secondAnswer.Text)
			if strings.Contains(lower, "i need the deck") || strings.Contains(lower, "i still need") ||
				strings.Contains(lower, "i'm missing") || strings.Contains(lower, "i am missing") ||
				strings.Contains(lower, "can't create") || strings.Contains(lower, "cannot create") {
				t.Errorf("Reply contains refusal — should have generated a deck: %q", secondAnswer.Text[:min(200, len(secondAnswer.Text))])
			}
			// Router should only be called once (for the first message)
			if routerCalls != 1 {
				t.Fatalf("router calls=%d, want 1 (early intercept should skip router for confirmation)", routerCalls)
			}

			// CRITICAL: Verify the thread's messages also contain the HTML deck.
			// This is what the frontend renders from via payload.thread.messages.
			// The viewer calls appendChatRichTextNodes on message.text which detects
			// HTML and calls renderInlineChatDeck.
			savedThread, ok := second["thread"].(scoutChatThreadRecord)
			if !ok {
				t.Fatalf("second response missing thread: %+v", second)
			}
			// Find the Scout reply in the thread messages
			var deckMessage *scoutChatMessageRecord
			for i := len(savedThread.Messages) - 1; i >= 0; i-- {
				msg := savedThread.Messages[i]
				if strings.ToLower(msg.Role) == "scout" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(msg.Text)), "<!doctype html") {
					deckMessage = &savedThread.Messages[i]
					break
				}
			}
			if deckMessage == nil {
				lastMsgs := ""
				for _, m := range savedThread.Messages {
					prefix := m.Text
					if len(prefix) > 80 {
						prefix = prefix[:80] + "..."
					}
					lastMsgs += fmt.Sprintf("\n  [%s/%s] %s", m.Kind, m.Role, prefix)
				}
				t.Fatalf("Thread messages do not contain HTML deck. Messages:%s", lastMsgs)
			}
			// Verify the message shape is what the viewer expects:
			// - Kind must be "message" (not work_result, artifact, etc.)
			// - Role must be "scout" (so appendChatRichTextNodes uses rich mode)
			// - Text must start with HTML (so it triggers renderInlineChatDeck)
			if deckMessage.Kind != "message" {
				t.Errorf("deckMessage.Kind=%q, want 'message'", deckMessage.Kind)
			}
			if deckMessage.Role != "scout" {
				t.Errorf("deckMessage.Role=%q, want 'scout'", deckMessage.Role)
			}
		})
	}
}

// TestDeckGenerationAfterChoicesCard tests the prod-test scenario where
// the router issues a clarify_once choices card ("What should the 5-slide deck be about?")
// and the user types "yes". Uses the REAL append path WITH THE WORKER LIVE.
// The early intercept must catch the deck confirmation BEFORE the router is called.
func TestDeckGenerationAfterChoicesCard(t *testing.T) {
	// Worker is LIVE — not stubbed. Deck confirmation must still generate a deck.
	clearAgentRunnerEnv(t)
	t.Setenv("BONFIRE_AGENT_RUNNER", "openai_text")
	setupAuthTestEnv(t)

	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-api-key"

	routerCalls := 0
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		switch request.Workflow {
		case "scout_route":
			routerCalls++
			if routerCalls == 1 {
				// First call: "make a 5-slide deck" → choices card clarify_once
				return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
					Outcome:  string(conversationIntentClarifyOnce),
					Question: "What should the 5-slide deck be about?",
					Options: []openAIScoutRouterOption{
						{Label: "A venture or product", Reply: "A venture or product"},
						{Label: "A film or creative project", Reply: "A film or creative project"},
						{Label: "Something else", Reply: "Something else"},
					},
				}), nil
			}
			// Second call should NOT happen — early intercept catches deck confirmation
			t.Error("Router was called for deck confirmation — early intercept should have caught it")
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
				Outcome: string(conversationIntentClarifyOnce),
				Message: "I still need more information.",
			}), nil
		case "scout_chat":
			// Inline deck generation — return HTML
			return `<!doctype html>
<html lang="en">
<head><title>Future of Work</title></head>
<body><section class="pg on"><h1>The Future of Work</h1></section></body>
</html>`, nil
		default:
			t.Fatalf("unexpected workflow %q", request.Workflow)
			return "", nil
		}
	})

	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Choices card test", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("aj@shareability.com")

	// 1. First message: "make a 5-slide deck" → choices card clarify_once
	first, err := app.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "make a 5-slide deck", nil, "")
	if err != nil {
		t.Fatalf("first message: %v", err)
	}
	if first["intentOutcome"] != string(conversationIntentClarifyOnce) {
		t.Fatalf("first outcome=%v, want clarify_once", first["intentOutcome"])
	}
	firstAnswer, ok := first["answer"].(scoutChatMessageRecord)
	if !ok || firstAnswer.Kind != scoutChatMessageKindChoices || firstAnswer.Choices == nil {
		t.Fatalf("first answer=%+v, want choices card", firstAnswer)
	}

	// 2. Second message: "yes" → early intercept catches this and generates deck
	// Router should NOT be called for the second message
	second, err := app.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "yes", nil, "")
	if err != nil {
		t.Fatalf("second message: %v", err)
	}
	// Should be conversational_reply (deck generated), NOT unavailable
	if second["intentOutcome"] == string(conversationIntentUnavailable) {
		unavailable, _ := second["unavailable"].(map[string]any)
		t.Fatalf("second message got unavailable=%+v — early intercept should have generated deck", unavailable)
	}
	if second["intentOutcome"] != string(conversationIntentConversationalReply) {
		t.Fatalf("second outcome=%v, want conversational_reply", second["intentOutcome"])
	}
	secondAnswer, ok := second["answer"].(scoutChatMessageRecord)
	if !ok {
		t.Fatalf("second answer missing: %+v", second)
	}
	// Should produce HTML deck
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(secondAnswer.Text)), "<!doctype html") {
		t.Errorf("Expected HTML deck, got: %q", secondAnswer.Text[:min(150, len(secondAnswer.Text))])
	}
	// Should NOT contain refusal text
	lower := strings.ToLower(secondAnswer.Text)
	if strings.Contains(lower, "i need the deck") || strings.Contains(lower, "i still need") ||
		strings.Contains(lower, "i'm missing") || strings.Contains(lower, "i am missing") ||
		strings.Contains(lower, "can't create") || strings.Contains(lower, "cannot create") {
		t.Errorf("Reply contains refusal — should have generated a deck: %q", secondAnswer.Text[:min(200, len(secondAnswer.Text))])
	}
	// Router should only be called once (for the first message)
	if routerCalls != 1 {
		t.Fatalf("router calls=%d, want 1 (early intercept should skip router for confirmation)", routerCalls)
	}
}

// TestRefusalNeverBecomesDeck tests that LLM refusals ("I still need the topic")
// are never wrapped into a deck as slide content.
func TestRefusalNeverBecomesDeck(t *testing.T) {
	// Test looksLikeDeckRefusal detection
	refusals := []string{
		"I still need the deck topic and intended audience to create it.",
		"I'm missing the deck's subject or source material.",
		"I need to know what the presentation is about.",
		"Could you provide more details about the topic?",
		"I can't create a deck without knowing the subject.",
		"Please provide the topic for the presentation.",
		// Exact live refusal from d8a2fe28bb prod-test
		"I need the deck's subject or source material before I can build it.",
	}
	for _, refusal := range refusals {
		if !looksLikeDeckRefusal(refusal) {
			t.Errorf("looksLikeDeckRefusal(%q) = false, want true", refusal[:min(50, len(refusal))])
		}
	}

	// Test that non-refusals are not flagged
	nonRefusals := []string{
		"Here's a presentation about innovation.",
		"This deck covers the future of work.",
		"Let me create that for you.",
	}
	for _, text := range nonRefusals {
		if looksLikeDeckRefusal(text) {
			t.Errorf("looksLikeDeckRefusal(%q) = true, want false", text)
		}
	}

	// Test generateDefaultDeck produces valid HTML
	defaultDeck := generateDefaultDeck("make a 5-slide deck")
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(defaultDeck)), "<!doctype html") {
		t.Error("generateDefaultDeck should return valid HTML deck")
	}
	if strings.Contains(strings.ToLower(defaultDeck), "i still need") {
		t.Error("generateDefaultDeck should not contain refusal text")
	}
}

// TestDeckGenerationDoesNotWrapRefusal tests the full flow: if the LLM returns
// a refusal, it should NOT be wrapped into a deck.
func TestDeckGenerationDoesNotWrapRefusal(t *testing.T) {
	clearAgentRunnerEnv(t)
	t.Setenv("BONFIRE_AGENT_RUNNER", "stub")

	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-api-key"
	setupAuthTestEnv(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("test user not found")
	}

	// Mock LLM to return a refusal (simulating when the pin fails)
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		// Return a refusal instead of HTML
		return "I still need the deck topic and intended audience to create it.", nil
	})

	history := []scoutChatTurn{
		{role: "user", text: "make a 5-slide deck"},
		{role: "scout", text: "Should this feel corporate or startup?"},
	}

	reply, err := app.resolveInlineDeckReply(context.Background(), user, "yes", history)
	if err != nil {
		t.Fatalf("resolveInlineDeckReply: %v", err)
	}

	// Should still produce an HTML deck (the default), not a refusal wrapped in HTML
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(reply.Text)), "<!doctype html") {
		t.Errorf("Expected HTML deck, got: %q", reply.Text[:min(100, len(reply.Text))])
	}

	// The deck content should NOT contain the refusal text
	if strings.Contains(strings.ToLower(reply.Text), "i still need") {
		t.Error("Deck should NOT contain refusal text 'I still need' — refusal was wrapped instead of generating default")
	}
	if strings.Contains(strings.ToLower(reply.Text), "topic and intended audience") {
		t.Error("Deck should NOT contain refusal text — refusal was wrapped instead of generating default")
	}
}

// TestExtractDirectionContextFiltersTopicQuestions tests that extractDirectionContext
// does NOT forward topic-asking questions like "What's the deck about?"
func TestExtractDirectionContextFiltersTopicQuestions(t *testing.T) {
	cases := []struct {
		name           string
		history        []scoutChatTurn
		wantNotContain string
		wantContain    string
	}{
		{
			name: "filters out topic question but keeps aesthetic",
			history: []scoutChatTurn{
				{role: "user", text: "make a 5-slide deck"},
				{role: "scout", text: "What's the deck about, and who needs to buy into it? Should it feel polished and corporate, or more cinematic and culture-forward?"},
			},
			wantNotContain: "What's the deck about",
			wantContain:    "corporate", // Should extract aesthetic choice
		},
		{
			name: "keeps user aesthetic direction",
			history: []scoutChatTurn{
				{role: "user", text: "make a deck with dark theme and minimal style"},
			},
			wantContain: "dark",
		},
		{
			name: "empty for no direction",
			history: []scoutChatTurn{
				{role: "user", text: "make a 5-slide deck"},
				{role: "scout", text: "I'll create that for you."},
			},
			wantNotContain: "create that",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := extractDirectionContext(tc.history)
			lower := strings.ToLower(result)
			if tc.wantNotContain != "" && strings.Contains(lower, strings.ToLower(tc.wantNotContain)) {
				t.Errorf("extractDirectionContext should NOT contain %q, got: %q", tc.wantNotContain, result)
			}
			if tc.wantContain != "" && !strings.Contains(lower, strings.ToLower(tc.wantContain)) {
				t.Errorf("extractDirectionContext should contain %q, got: %q", tc.wantContain, result)
			}
		})
	}
}

// TestExtractEffectiveDeckQuery tests the query extraction for confirmations.
func TestExtractEffectiveDeckQuery(t *testing.T) {
	cases := []struct {
		name           string
		query          string
		history        []scoutChatTurn
		wantContains   string // effective query should contain this
		wantNotEquals  string // effective query should NOT be exactly this
	}{
		{
			name:  "yes with topic in original request",
			query: "yes",
			history: []scoutChatTurn{
				{role: "user", text: "make a deck about our Q4 strategy"},
				{role: "scout", text: "Should this feel corporate or startup?"},
			},
			wantContains:  "Q4 strategy",
			wantNotEquals: "yes",
		},
		{
			name:  "yes with no topic but aesthetic choice in direction",
			query: "yes",
			history: []scoutChatTurn{
				{role: "user", text: "make a 5-slide deck"},
				{role: "scout", text: "Should this feel corporate or startup? Full-bleed or typographic?"},
			},
			wantContains:  "Style:", // Now extracts style, not "Direction:"
			wantNotEquals: "yes",
		},
		{
			name:  "yes with no topic and Scout asked about topic (prod-test)",
			query: "yes",
			history: []scoutChatTurn{
				{role: "user", text: "make a 5-slide deck"},
				{role: "scout", text: "What's the deck about? Should it feel corporate or cinematic?"},
			},
			wantContains:  "Future of Work", // Uses default topic
			wantNotEquals: "yes",
		},
		{
			name:          "explicit request not a confirmation",
			query:         "make a deck about AI trends",
			history:       nil,
			wantContains:  "AI trends",
			wantNotEquals: "",
		},
		{
			name:  "looks good with no aesthetic options",
			query: "looks good",
			history: []scoutChatTurn{
				{role: "user", text: "create a presentation"},
				{role: "scout", text: "What visual style would you prefer?"},
			},
			wantContains:  "Future of Work", // Uses default topic when no options to extract
			wantNotEquals: "looks good",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := extractEffectiveDeckQuery(tc.query, tc.history)
			if tc.wantContains != "" && !strings.Contains(result, tc.wantContains) {
				t.Errorf("extractEffectiveDeckQuery(%q) = %q, want to contain %q", tc.query, result, tc.wantContains)
			}
			if tc.wantNotEquals != "" && result == tc.wantNotEquals {
				t.Errorf("extractEffectiveDeckQuery(%q) = %q, should not equal %q", tc.query, result, tc.wantNotEquals)
			}
		})
	}
}

// TestLooksLikeDirectionPass tests the direction pass detection function.
func TestLooksLikeDirectionPass(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		// Direct keywords
		{"what visual direction would you like?", true},
		{"what's your aesthetic preference?", true},
		{"what style are you going for?", true},
		{"what's the design theme?", true},
		// Question patterns with design choices
		{"should this feel corporate or startup?", true},
		{"full-bleed imagery or typographic slides?", true},
		{"are you presenting to investors?", true},
		{"do you want something minimal or bold?", true},
		// Topic clarification questions for decks (choices card scenario)
		{"what should the 5-slide deck be about?", true},
		{"what should the deck be about?", true},
		{"what topic should the presentation cover?", true},
		{"what's the presentation about?", true},
		{"what should these slides focus on?", true},
		{"what subject should the deck cover?", true},
		// Live Approach B prose (exact verbatim from prod-test at d8a2fe28bb) — MUST match
		{"what's the deck about, and who's in the room? should it feel polished and investor-ready, or more like a bold creative pitch? do you want cinematic imagery carrying the mood, or a clean typographic system doing the work?", true},
		// Not direction passes
		{"i'll create that deck for you", false},
		{"here's your presentation", false},
		{"let me help with that", false},
		{"", false},
		// Just "about" without deck word shouldn't match
		{"what's it about?", false},
		// Just deck word without topic word shouldn't match
		{"should i make this deck?", false},
	}

	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			got := scoutChatLooksLikeDirectionPass(tc.text)
			if got != tc.want {
				t.Errorf("scoutChatLooksLikeDirectionPass(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// TestClarificationAlreadyAskedRecognizesApproachB tests that scoutChatClarificationAlreadyAsked
// returns true for Approach B prose (Kind=message + IntentOutcome=clarify_once).
// This is the fix for prod-test FAIL at SHA 753adc71.
func TestClarificationAlreadyAskedRecognizesApproachB(t *testing.T) {
	cases := []struct {
		name     string
		messages []scoutChatMessageRecord
		want     bool
	}{
		{
			name: "choices card is clarification",
			messages: []scoutChatMessageRecord{
				{Role: "user", Text: "make a 5-slide deck"},
				{Role: "scout", Kind: scoutChatMessageKindChoices, IntentOutcome: string(conversationIntentClarifyOnce),
					Choices: &scoutChatChoices{Question: "What should the deck be about?"}},
			},
			want: true,
		},
		{
			name: "Approach B prose is clarification",
			messages: []scoutChatMessageRecord{
				{Role: "user", Text: "make a 5-slide deck"},
				{Role: "scout", Kind: "message", IntentOutcome: string(conversationIntentClarifyOnce),
					Text: "What's the deck about, and who's in the room?"},
			},
			want: true,
		},
		{
			name: "conversational_reply is NOT clarification",
			messages: []scoutChatMessageRecord{
				{Role: "user", Text: "make a 5-slide deck"},
				{Role: "scout", Kind: "message", IntentOutcome: string(conversationIntentConversationalReply),
					Text: "I'd be happy to help!"},
			},
			want: false,
		},
		{
			name: "user message after Scout stops search",
			messages: []scoutChatMessageRecord{
				{Role: "scout", Kind: "message", IntentOutcome: string(conversationIntentClarifyOnce),
					Text: "What's the deck about?"},
				{Role: "user", Text: "yes"},
			},
			want: false,
		},
		{
			name:     "empty thread",
			messages: nil,
			want:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			thread := scoutChatThreadRecord{Messages: tc.messages}
			got := scoutChatClarificationAlreadyAsked(thread)
			if got != tc.want {
				t.Errorf("scoutChatClarificationAlreadyAsked = %v, want %v", got, tc.want)
			}
		})
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
