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

func TestAuthenticatedBootstrapStatePrecedesFirstRender(t *testing.T) {
	html := readIndexForLiveSimFixes(t)
	firstRender := strings.Index(html, "      renderLoginMode()")
	if firstRender == -1 {
		t.Fatal("initial renderLoginMode call is missing")
	}
	for _, declaration := range []string{
		"let chatChannelActivityPopover = null",
		"let chatChannelActivityHideTimer = 0",
		"let chatWorkTimerTicker = 0",
		"let multiDeviceHandoffChip = null",
		"let deviceOfferChip = null",
	} {
		at := strings.Index(html, declaration)
		if at == -1 || at > firstRender {
			t.Errorf("bootstrap-owned state %q must be initialized before the first authenticated render", declaration)
		}
	}
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
	show := functionBodyAfterSignature(html, "function showScoutChatThinking(text, scope = null)")
	if show == "" || !strings.Contains(show, "scoutChatTurnInFlight = true") {
		t.Error("showScoutChatThinking must raise scoutChatTurnInFlight")
	}
	hide := functionBody(html, "function hideScoutChatThinking()")
	if hide == "" || !strings.Contains(hide, "scoutChatTurnInFlight = false") {
		t.Error("hideScoutChatThinking must clear scoutChatTurnInFlight")
	}
	render := functionBodyAfterSignature(html, "function renderActiveScoutThread(options = {})")
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
		"['proposal', 'choices', 'manifest', 'thread', 'artifact', 'image', 'image_pending'].includes(recordKind)",
		"!scoutChatThinking.hidden",
		"withScoutChatThinkingHold",
	} {
		if !strings.Contains(threadEvent, want) {
			t.Errorf("handleChatThreadEvent body missing %q", want)
		}
	}
	// server status echoes may only refresh an in-flight shimmer
	if !strings.Contains(html, "if (scoutChatTurnInFlight && (!scoutChatTurnScope || scoutChatMutationSourceIsActive(scoutChatTurnScope)))") {
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
	if errorIndex, gateIndex := strings.Index(state, "goalStatus === 'needs_attention'"), strings.Index(state, "stage === 'approval_required'"); errorIndex < 0 || gateIndex < 0 || errorIndex > gateIndex {
		t.Fatal("needs-attention/error state must win before the approval gate so failed work cannot expose Approve")
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

// Conversation-first replacement for Bug 4: the composer has no armed tool
// state at all, so starter text cannot silently hijack a later message.
func TestIndexComposerHasNoArmedToolState(t *testing.T) {
	html := readIndexForLiveSimFixes(t)
	if strings.Contains(html, "function paletteConversationalHandoff(tool)") {
		t.Fatal("retired Tool Palette conversational handoff remains in the customer client")
	}
	send := functionBody(html, "function sendScoutChat(text)")
	if send == "" {
		t.Fatal("could not extract sendScoutChat body")
	}
	if strings.Contains(send, "pendingScoutToolTemplate") || strings.Contains(send, "armedTemplate") || strings.Contains(send, "toolTemplate") {
		t.Error("normal send path retains client tool state")
	}
	if !strings.Contains(send, "sendScoutChatViaOffice(trimmed, files)") {
		t.Error("normal send path must forward the person's text and files")
	}
}
