package main

// frontend_live_sim_fixes_test.go — pins for the four bugs the 2026-07-05
// live production simulation surfaced:
//
//  1. proposal cards double-mounted and compounded on every thread rebuild
//     (no node cache + never torn down by clearScoutChatThreadNodes);
//  2. the thinking shimmer persisted forever — a turn that resolved into a
//     proposal/choices/manifest card never counted as the reply, and status
//     echoes could conjure a shimmer with no pending turn;
//  3. a parked checkpoint mounted from a thread goal-ref rendered the generic
//     running body when the goal parent fell out of the newest-100 /artifacts
//     window (and goalCardStateFor missed the plan.state=approval_required +
//     status-mirror shapes);
//  4. an armed-tool send launched junk goals whose objective was the untouched
//     composer prefill ("Turn this into a goal workflow: ").

import (
	"os"
	"strings"
	"testing"
)

func readIndexForLiveSimFixes(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// Bug 1 — proposal cards dedupe through a module-level node cache keyed by
// message id (the goalThreadCardNodes pattern): the immediate-reply render and
// every renderActiveScoutThread rebuild return the SAME node, the rebuild pass
// tears stale mounts down with the other feed nodes, and a persisted status
// flip rebuilds the node instead of reusing a stale interactive card.
func TestIndexProposalCardDedupNodeCache(t *testing.T) {
	html := readIndexForLiveSimFixes(t)
	if !strings.Contains(html, "const scoutProposalCardNodes = new Map()") {
		t.Error("missing the module-level scoutProposalCardNodes cache")
	}
	wrapper := functionBody(html, "function scoutProposalCardNode(message)")
	if wrapper == "" {
		t.Fatal("could not extract scoutProposalCardNode body")
	}
	for _, want := range []string{
		"scoutProposalCardNodes.get(messageId)",
		"buildScoutProposalCardNode(message)",
		"scoutProposalCardNodes.set(messageId, card)",
		"dataset.proposalStatus",
		"return cached",
	} {
		if !strings.Contains(wrapper, want) {
			t.Errorf("scoutProposalCardNode body missing %q", want)
		}
	}
	// the rebuild pass must detach proposal (and manifest) cards so cached
	// nodes re-mount at their message's position — never above the user
	// message, never compounding
	clear := functionBody(html, "function clearScoutChatThreadNodes()")
	if clear == "" {
		t.Fatal("could not extract clearScoutChatThreadNodes body")
	}
	for _, want := range []string{".scout-proposal-card", ".manifest-card"} {
		if !strings.Contains(clear, want) {
			t.Errorf("clearScoutChatThreadNodes must tear down %q nodes", want)
		}
	}
	// the local resolve keeps the cache stamp in step so the server echo of
	// the same status reuses the node instead of rebuilding it
	resolved := functionBody(html, "function markProposalCardResolved(card, status)")
	if resolved == "" {
		t.Fatal("could not extract markProposalCardResolved body")
	}
	if !strings.Contains(resolved, "card.dataset.proposalStatus = status") {
		t.Error("markProposalCardResolved must stamp dataset.proposalStatus")
	}
}

// Bug 2 — the shimmer's law: it resolves into exactly one committed turn.
// scoutChatTurnInFlight marks a reply THIS session is waiting on; a rebuild
// from persisted state hides any stray shimmer; a status echo can only refresh
// an in-flight one; and a turn resolving into a proposal/choices/manifest card
// counts as the committed reply on the socket path.
func TestIndexThinkingShimmerResolvesIntoOneCommittedTurn(t *testing.T) {
	html := readIndexForLiveSimFixes(t)
	if !strings.Contains(html, "let scoutChatTurnInFlight = false") {
		t.Error("missing the scoutChatTurnInFlight session-local flag")
	}
	show := functionBody(html, "function showScoutChatThinking(text)")
	if show == "" || !strings.Contains(show, "scoutChatTurnInFlight = true") {
		t.Error("showScoutChatThinking must raise scoutChatTurnInFlight")
	}
	hide := functionBody(html, "function hideScoutChatThinking()")
	if hide == "" || !strings.Contains(hide, "scoutChatTurnInFlight = false") {
		t.Error("hideScoutChatThinking must clear scoutChatTurnInFlight")
	}
	render := functionBody(html, "function renderActiveScoutThread()")
	if render == "" {
		t.Fatal("could not extract renderActiveScoutThread body")
	}
	if !strings.Contains(render, "if (!scoutChatTurnInFlight)") || !strings.Contains(render, "hideScoutChatThinking()") {
		t.Error("renderActiveScoutThread must hide a shimmer no in-flight send owns (reloads never show one)")
	}
	// the socket delivery path: card-kind records ARE the reply
	threadEvent := functionBody(html, "function handleChatThreadEvent(payload)")
	if threadEvent == "" {
		t.Fatal("could not extract handleChatThreadEvent body")
	}
	for _, want := range []string{
		"['proposal', 'choices', 'manifest', 'thread', 'artifact', 'image'].includes(recordKind)",
		"!scoutChatThinking.hidden",
		"withScoutChatThinkingHold",
	} {
		if !strings.Contains(threadEvent, want) {
			t.Errorf("handleChatThreadEvent body missing %q", want)
		}
	}
	// server status echoes may only refresh an in-flight shimmer
	chatEvent := functionBody(html, "function handleScoutChatEvent(payload)")
	if chatEvent == "" {
		t.Fatal("could not extract handleScoutChatEvent body")
	}
	if !strings.Contains(chatEvent, "if (scoutChatTurnInFlight)") {
		t.Error("handleScoutChatEvent status branch must be gated on scoutChatTurnInFlight")
	}
	// the keyless ws send starts its own shimmer (status can no longer)
	send := functionBody(html, "function sendScoutChat(text)")
	if send == "" || !strings.Contains(send, "showScoutChatThinking('thinking')") {
		t.Error("sendScoutChat ws path must start the shimmer itself")
	}
}

// Bug 3 — a parked checkpoint renders from a thread goal-ref mount even when
// the goal parent is outside the newest-100 /artifacts window: the state
// machine resolves every persisted approval_required shape (plan.state, the
// status/threadStatus mirrors, reviewGate), and a missing library entry
// triggers a single-flight by-id fetch that upgrades the mounted card.
func TestIndexParkedGoalCardRendersFromThreadRef(t *testing.T) {
	html := readIndexForLiveSimFixes(t)
	state := functionBody(html, "function goalCardStateFor(artifact, plan)")
	if state == "" {
		t.Fatal("could not extract goalCardStateFor body")
	}
	for _, want := range []string{
		"planState === 'approval_required'",
		"status === 'approval_required'",
		"goalStatus === 'approval_required'",
		"m.threadStatus",
	} {
		if !strings.Contains(state, want) {
			t.Errorf("goalCardStateFor body missing %q", want)
		}
	}
	nodeFor := functionBody(html, "function goalCardNodeFor(artifact)")
	if nodeFor == "" || !strings.Contains(nodeFor, "fetchGoalArtifactById(id)") {
		t.Error("goalCardNodeFor must fetch a goal parent the window missed")
	}
	sync := functionBody(html, "function syncGoalCards()")
	if sync == "" || !strings.Contains(sync, "fetchGoalArtifactById(id)") {
		t.Error("syncGoalCards must keep out-of-window mounted cards fresh by id")
	}
	fetchBody := functionBody(html, "function fetchGoalArtifactById(id)")
	if fetchBody == "" {
		t.Fatal("could not extract fetchGoalArtifactById body")
	}
	for _, want := range []string{
		"/artifacts?id=",
		"goalArtifactFetchesInFlight",
		"addArtifactEntry(artifact, { select: false })",
		"updateGoalCard(card, artifact)",
	} {
		if !strings.Contains(fetchBody, want) {
			t.Errorf("fetchGoalArtifactById body missing %q", want)
		}
	}
	// the parked-render path itself: gate state + pending checkpoint owns the
	// terminal, and the checkpoint card renders the parkline + choice pills
	terminal := functionBody(html, "function goalCardRenderTerminal(card, artifact, plan, state, prevState)")
	if terminal == "" || !strings.Contains(terminal, "goalCardRenderCheckpoint(terminal, card, artifact, plan, pendingCheckpoint)") {
		t.Error("gate state with a pending checkpoint must render the checkpoint card")
	}
	checkpoint := functionBody(html, "function goalCardRenderCheckpoint(terminal, card, artifact, plan, checkpoint)")
	if checkpoint == "" {
		t.Fatal("could not extract goalCardRenderCheckpoint body")
	}
	for _, want := range []string{"goalcard__parkline", "goalcard__choices", "goalcard__choice"} {
		if !strings.Contains(checkpoint, want) {
			t.Errorf("goalCardRenderCheckpoint body missing %q", want)
		}
	}
}

// Bug 4 — an armed-tool send must always take the composer's live value as
// the objective, and a value still equal to the untouched starter prefill is
// no objective at all: the send blocks with a hint, the template stays armed.
func TestIndexArmedToolSendGuardsPrefillObjective(t *testing.T) {
	html := readIndexForLiveSimFixes(t)
	handoff := functionBody(html, "function paletteConversationalHandoff(tool)")
	if handoff == "" {
		t.Fatal("could not extract paletteConversationalHandoff body")
	}
	for _, want := range []string{
		"scoutStarterText(mode)",
		"composerHoldsStarter",
		"prefill: composerHoldsStarter ? starter : ''",
	} {
		if !strings.Contains(handoff, want) {
			t.Errorf("paletteConversationalHandoff body missing %q", want)
		}
	}
	send := functionBody(html, "function sendScoutChat(text)")
	if send == "" {
		t.Fatal("could not extract sendScoutChat body")
	}
	for _, want := range []string{
		"trimmed === String(armedTemplate.prefill || '').trim()",
		"describe what the tool should work on",
	} {
		if !strings.Contains(send, want) {
			t.Errorf("sendScoutChat body missing %q", want)
		}
	}
	// the block must precede the capture-and-clear so the template survives
	// for the real objective
	blockAt := strings.Index(send, "describe what the tool should work on")
	clearAt := strings.Index(send, "pendingScoutToolTemplate = null")
	if blockAt == -1 || clearAt == -1 || blockAt > clearAt {
		t.Error("the prefill block must run before the armed template is cleared")
	}
}
