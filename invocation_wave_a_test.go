package main

import (
	"context"
	"strings"
	"testing"
)

// Wave A regression fence (invocation-and-capabilities.md item 5). These pin the
// exact behavior that closes the 2026-07-05 live-sim failure: Scout called
// packaging "a bigger ask than I can spin up" because the answer brain was told
// only what it could NOT do, and the router lost the literal full-run words to
// thread-context gravity. The three interlocking fixes — the capabilities digest
// + offer-never-deny (item 1), the router prompt patches (item 2), and the
// deterministic pre-router guard (item 3) — are fenced here so a regression
// fails CI, not a live drive-through.

// --- Item 1: the capabilities digest, golden length + coverage ---------------

// The digest is generated from buildToolsPayload — the single taxonomy source
// the router enum and palette read — so every router-enum id MUST appear, it
// MUST lead with the flagship "End-to-end" group, and it MUST stay under the cap
// (it rides every keyed chat turn).
func TestCapabilitiesDigestCapsLengthAndNamesEveryRouterEnumId(t *testing.T) {
	digest := assistantCapabilitiesDigest()
	if strings.TrimSpace(digest) == "" {
		t.Fatal("capabilities digest is empty")
	}
	if len(digest) > assistantCapabilitiesDigestMaxChars {
		t.Fatalf("digest length=%d exceeds cap %d — keep the block compact", len(digest), assistantCapabilitiesDigestMaxChars)
	}

	// Every id the router can propose (scoutRouterTools injects exactly these
	// from buildToolsPayload) must be self-describable to the answer brain.
	for _, group := range buildToolsPayload() {
		for _, tool := range group.Tools {
			if !strings.Contains(digest, tool.ID) {
				t.Errorf("digest missing router-enum id %q — the answer brain cannot name a capability it is not told about", tool.ID)
			}
			if !strings.Contains(digest, tool.Name) {
				t.Errorf("digest missing capability name %q", tool.Name)
			}
		}
	}

	// The flagship leads: packaging_studio is present, described as the
	// end-to-end staged run, and the "End-to-end" group renders before the
	// lifecycle groups.
	if !strings.Contains(digest, packagingStudioProcessID) {
		t.Fatalf("digest missing the flagship %q", packagingStudioProcessID)
	}
	endToEnd := strings.Index(digest, "End-to-end")
	ideate := strings.Index(digest, "Ideate")
	if endToEnd < 0 || ideate < 0 || endToEnd > ideate {
		t.Fatalf("digest must lead with the End-to-end group (idx %d) before Ideate (idx %d)", endToEnd, ideate)
	}
}

// --- Item 1: offer-never-deny replaces the prohibition, keyed only -----------

func TestAssistantQueryInstructionsOfferNeverDenyIsKeyGated(t *testing.T) {
	const oldProhibition = "Do not claim to run research, design, grill"

	// Keyed: the pure prohibition is REPLACED by the offer-never-deny protocol
	// and the capabilities digest.
	t.Run("keyed offers, never denies", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "sk-ant-digest-test")
		instructions := assistantQueryInstructions()
		if strings.Contains(instructions, oldProhibition) {
			t.Error("keyed instructions still carry the pure prohibition — it must be REPLACED by offer-never-deny")
		}
		for _, want := range []string{"offer to set it up", "never deny", packagingStudioProcessID} {
			if !strings.Contains(instructions, want) {
				t.Errorf("keyed instructions missing %q (the offer protocol + digest must be present)", want)
			}
		}
	})

	// Keyless: no goal loop can run, so today's honest prohibition stays verbatim
	// and the digest never appears (don't overpromise on a keyless deploy).
	t.Run("keyless keeps the honest prohibition", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "")
		instructions := assistantQueryInstructions()
		if !strings.Contains(instructions, oldProhibition) {
			t.Error("keyless instructions must keep the honest prohibition")
		}
		if strings.Contains(instructions, "offer to set it up") || strings.Contains(instructions, packagingStudioProcessID) {
			t.Error("keyless instructions must NOT carry the offer protocol or the digest")
		}
	})
}

// --- Item 3: the deterministic guard, the sim's exact miss -------------------

// deterministicRouterGuard must arm the flagship on the literal full-run words
// (the sim's first turn), survive the negated "no, ..." correction as a
// RE-ROUTE (not a denial), and defer questions to the answer brain.
func TestDeterministicRouterGuardClosesTheSimMiss(t *testing.T) {
	studioCases := []string{
		"package this end to end",
		"package this end to end, the full run",
		"take it from 0 to 100",
		"give me the full packaging run for Station Tenn",
		// The sim's SECOND turn: a correction that names the process verbatim.
		// It opens with "no," but negates nothing — it must RE-ROUTE, never deny.
		"no, the full Packaging Studio staged run",
	}
	for _, message := range studioCases {
		t.Run("arms studio: "+message, func(t *testing.T) {
			verdict := deterministicRouterGuard(message)
			if verdict == nil || verdict.proposal == nil {
				t.Fatalf("guard returned no proposal for %q — the full-run words must never lose the flagship", message)
			}
			if verdict.proposal.ToolID != packagingStudioProcessID {
				t.Fatalf("guard armed %q for %q, want packaging_studio (never package_assembly)", verdict.proposal.ToolID, message)
			}
			if verdict.proposal.Kind != scoutRouterProposalKindToolRun {
				t.Fatalf("guard proposal kind=%q, want tool_run", verdict.proposal.Kind)
			}
		})
	}

	// Exact registry names arm their own capability.
	nameCases := map[string]string{
		"let's run the deck outline for the WME room": "deck_outline",
		"compile the package assembly for the buyer":  "package_assembly",
		"do a deep research pass on the rodeo market": "deep_research",
	}
	for message, wantID := range nameCases {
		t.Run("arms name: "+message, func(t *testing.T) {
			verdict := deterministicRouterGuard(message)
			if verdict == nil || verdict.proposal == nil {
				t.Fatalf("guard returned no proposal for the verbatim name in %q", message)
			}
			if verdict.proposal.ToolID != wantID {
				t.Fatalf("guard armed %q for %q, want %q", verdict.proposal.ToolID, message, wantID)
			}
		})
	}

	// Negated / asked-about messages defer to the model + answer brain: no card.
	deferCases := []string{
		"don't package this end to end",
		"can you package this end to end?",
		"could you run the full packaging studio from here?",
		"what can you do end to end?",
		"maybe later, not now, the full run",
		"instead of the full run let's just talk it through",
		// Statement-form questions with no trailing "?" — the regression the
		// pre-guard opened: these must reach the answer brain, not arm a card.
		"what is packaging studio",
		"explain the packaging studio",
		"tell me about deep research",
		"whats the packaging studio",
		// auxiliary-question form of "do" — the gate's flagged gap: "do we"
		// must defer just like "do you" (both are questions, not commands).
		"do we run the full packaging studio",
		"do you run the full packaging studio",
	}
	for _, message := range deferCases {
		t.Run("defers: "+message, func(t *testing.T) {
			if verdict := deterministicRouterGuard(message); verdict != nil {
				t.Fatalf("guard armed a card for a negated/question message %q — it must defer", message)
			}
		})
	}
}

// "full packaging" was dropped from scoutRouterFullRunPhrases because it is a
// substring of ordinary "compile the artifacts we already made" asks and so
// over-armed the flagship (packaging_studio), undermining the item-2
// package_assembly-vs-packaging_studio disambiguation. These compile-asks must
// NOT arm the flagship end-to-end run.
func TestScoutGuardFullPackagingDoesNotOverTriggerStudio(t *testing.T) {
	compileAsks := []string{
		"compile the full packaging binder we already made",
		"assemble the full packaging deck from what we have",
		"put together the full packaging one-pager",
	}
	for _, message := range compileAsks {
		t.Run("not studio: "+message, func(t *testing.T) {
			verdict := deterministicRouterGuard(message)
			if verdict != nil && verdict.proposal != nil && verdict.proposal.ToolID == packagingStudioProcessID {
				t.Fatalf("guard armed packaging_studio for a compile-ask %q — %q must not over-trigger the flagship", message, "full packaging")
			}
		})
	}
}

// --- Items 2+3 end-to-end: the two-turn sim through the real chat seam --------

// The full two-turn sim failure, driven through appendScoutChatThreadMessage
// with a keyed router: the deterministic guard short-circuits BEFORE the model
// turn on both turns, so the router model is never consulted, both turns commit
// a packaging_studio proposal (never package_assembly), and neither launches.
func TestScoutChatFlagshipTwoTurnRegressionFence(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-router-test")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	// The guard commits before any model turn, so NOTHING model-facing may run.
	swapAnthropicMessagesResponder(t, func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		t.Fatal("the deterministic guard must short-circuit before the router model turn")
		return anthropicMessagesResponse{}, nil
	})
	swapAnthropicTextResponder(t, func(context.Context, string, anthropicTextRequest) (string, error) {
		t.Fatal("a committed proposal must not also run the Q&A answer path")
		return "", nil
	})

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {
		t.Fatal("a proposal must never launch")
	}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}

	assertStudioProposal := func(turn string, response map[string]any) {
		if _, launched := response["agentThread"]; launched {
			t.Fatalf("%s: response keys=%v — NEVER silent-launch", turn, responseKeys(response))
		}
		proposal, ok := response["proposal"].(*scoutRouterProposal)
		if !ok {
			t.Fatalf("%s: proposal type=%T, want the armed *scoutRouterProposal (never a denial)", turn, response["proposal"])
		}
		if proposal.ToolID != packagingStudioProcessID {
			t.Fatalf("%s: armed %q, want packaging_studio (never package_assembly)", turn, proposal.ToolID)
		}
		if proposal.GroupLabel != "End-to-end" {
			t.Fatalf("%s: group label=%q, want End-to-end", turn, proposal.GroupLabel)
		}
	}

	// Turn 1: the outcome-phrased ask.
	turn1, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, private.ID, "package this end to end, the full run", nil, "")
	if err != nil {
		t.Fatalf("turn 1 append: %v", err)
	}
	assertStudioProposal("turn 1", turn1)

	// Turn 2: the correction that named the process verbatim. It opens with "no,"
	// but must RE-ROUTE, not deny.
	turn2, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, private.ID, "no, the full Packaging Studio staged run", nil, "")
	if err != nil {
		t.Fatalf("turn 2 append: %v", err)
	}
	assertStudioProposal("turn 2", turn2)
}

// --- Item 1 end-to-end: the capability question answers with an offer ---------

// "can you run the full packaging studio from here?" reaches the answer brain
// (the guard defers questions). Because a key is present, the brain carries the
// offer-never-deny protocol + the digest and zero denial phrasing — the
// deterministic proxy for "the answer offers instead of dead-ending on denial".
func TestCapabilityQuestionAnswerCarriesOfferNotDenial(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-key"
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-answer-test")

	var got anthropicTextRequest
	swapAnthropicTextResponder(t, func(_ context.Context, _ string, request anthropicTextRequest) (string, error) {
		got = request
		return "Yes — that's the Packaging Studio staged run. Want me to set it up?", nil
	})

	answer, err := app.answerAssistantQueryWithModel(context.Background(), "aj@shareability.com", "can you run the full packaging studio from here?", nil, nil, nil)
	if err != nil {
		t.Fatalf("answerAssistantQueryWithModel: %v", err)
	}
	if strings.TrimSpace(answer) == "" {
		t.Fatal("empty answer")
	}
	if strings.Contains(got.Instructions, "Do not claim to run research, design, grill") {
		t.Error("the keyed answer prompt must NOT carry the pure prohibition — that is the denial the sim produced")
	}
	for _, want := range []string{"offer to set it up", "never deny", packagingStudioProcessID} {
		if !strings.Contains(got.Instructions, want) {
			t.Errorf("keyed answer prompt missing %q — the brain must be told to offer the capability", want)
		}
	}
}
