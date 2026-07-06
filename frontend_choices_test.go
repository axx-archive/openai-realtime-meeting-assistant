package main

// The quick-reply pill component's frontend contract (the conversational
// intent layer). Grep-style pins in the frontend_router_test.go grammar: a
// persisted Kind=choices message renders as Scout's question bubble + pills,
// a tap posts ONLY ids to the choice route (the stored record wins), a
// resolved card renders inert with the chosen pill lit — and the pill handler
// contains no launch door at all: arming the proposal card is the most a pill
// can ever do.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForChoices(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func TestIndexChoicesPillComponentContract(t *testing.T) {
	html := readIndexForChoices(t)
	for _, want := range []string{
		// render branch: a choices message becomes the pill card everywhere
		// the thread renders (live send and reload alike)
		"=== 'choices' && message.choices",
		"function scoutChoicesNode(message)",
		// the component's parts: question bubble + pill row, the tool-arm
		// marker, and the one clearly-marked CSS block
		"scout-choices__pill",
		"scout-choices__arm",
		".scout-choices {",
		"Quick-reply pills (conversational intent layer)",
		// a tap posts ids only — the persisted record owns the reply text and
		// the tool arm
		"function postScoutChoiceSelection",
		"}/choice`",
		"messageId: String(message?.id || '')",
		"optionId: String(option?.id || '')",
		// resolved state: first tap wins, the chosen pill stays lit, a
		// reloaded thread renders spent pills inert
		"row.classList.add('is-resolved')",
		"pill.classList.add('is-selected')",
		"pill.disabled = true",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing quick-reply pill hook %q", want)
		}
	}
}

// The propose-confirm law, client half: the pill component must not contain
// any launch door. runGoalPipeline and the /assistant/goal route belong to the
// proposal card's Run button alone — a pill at most ARMS that card.
func TestIndexChoicesPillNeverLaunches(t *testing.T) {
	html := readIndexForChoices(t)
	start := strings.Index(html, "function scoutChoicesNode(message)")
	end := strings.Index(html, "function markProposalCardResolved")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("cannot scope the choices component body")
	}
	body := html[start:end]
	for _, banned := range []string{"runGoalPipeline", "/assistant/goal", "startAgentThread"} {
		if strings.Contains(body, banned) {
			t.Fatalf("the pill component contains launch hook %q — a pill may only arm the proposal card, never launch", banned)
		}
	}
}

// P2-1 — the checkpoint choices row must render at most one filled primary pill
// (sheet s05: exactly one filled primary, outlined siblings). A genuine fork of
// two proceed options ("brand assets provided" vs "develop identity") must NOT
// paint both ink-filled — the loop counts proceeds and only lights the primary
// when there is exactly one, so no false default is implied.
func TestGoalCheckpointChoicesNoFalseDefault(t *testing.T) {
	html := readIndexForChoices(t)
	body := functionBody(html, "function goalCardRenderCheckpoint(terminal, card, artifact, plan, checkpoint)")
	if body == "" {
		t.Fatal("could not extract goalCardRenderCheckpoint body")
	}
	if !strings.Contains(body, "const proceedCount = options.filter(option => option.action === 'proceed').length") {
		t.Error("goalCardRenderCheckpoint must count proceed options before deciding the primary door")
	}
	if !strings.Contains(body, "option.action === 'proceed' && proceedCount === 1") {
		t.Error("goalcard__choice--primary must apply ONLY when there is exactly one proceed — a multi-proceed fork renders all-outlined (no false default)")
	}
	// guard the regression: the bare unconditional proceed→primary must be gone
	if strings.Contains(body, "if (option.action === 'proceed') choiceBtn.classList.add('goalcard__choice--primary')") {
		t.Error("the unconditional proceed→primary mapping still exists — a two-proceed fork would ink-fill both pills")
	}
}
