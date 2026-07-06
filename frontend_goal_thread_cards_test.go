package main

// frontend_goal_thread_cards_test.go — P0-1/P0-2 client pins: a goal-ref chat
// message renders the live goalcard (not the research run card) for everyone,
// across reloads; the card node is cached at module level so thread rebuilds
// never wipe a half-typed checkpoint note; the LAST goal-ref message owns the
// mount (earlier refs render a jump marker); and a kind:"artifact" stage
// message renders its narration line with a tappable chip into the viewer.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForGoalThreadCards(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// Routing: scoutChatMessageRecordNode sends goal refs to the goalcard path
// (mode 'goal' on the ref, or the resolved artifact passing isGoalArtifact)
// and kind:"artifact" stage messages to the chip renderer.
func TestIndexScoutChatMessageRoutesGoalRefsAndStageArtifacts(t *testing.T) {
	html := readIndexForGoalThreadCards(t)
	body := functionBody(html, "function scoutChatMessageRecordNode(message)")
	if body == "" {
		t.Fatal("could not extract scoutChatMessageRecordNode body")
	}
	for _, want := range []string{
		"scoutGoalRefRecordNode(message, run.artifact)",
		"isGoalArtifact(run.artifact)",
		"=== 'goal'",
		"scoutStageArtifactNode(message)",
		"=== 'artifact'",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scoutChatMessageRecordNode body missing %q", want)
		}
	}
}

// Node cache: one module-level Map keyed by goal artifact id; the message
// render path and the launcher's ghost card share the SAME node (no double
// card), refreshed through updateGoalCard.
func TestIndexGoalCardNodeCacheReuse(t *testing.T) {
	html := readIndexForGoalThreadCards(t)
	if !strings.Contains(html, "const goalThreadCardNodes = new Map()") {
		t.Error("missing the module-level goalThreadCardNodes cache")
	}
	body := functionBody(html, "function goalCardNodeFor(artifact)")
	if body == "" {
		t.Fatal("could not extract goalCardNodeFor body")
	}
	for _, want := range []string{
		"goalThreadCardNodes.get(id)",
		"renderGoalCard(artifact)",
		"goalThreadCardNodes.set(id, card)",
		"updateGoalCard(card, fresh || artifact)",
		"goalCardEnsureTicker()",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("goalCardNodeFor body missing %q", want)
		}
	}
	// the launcher's ghost card reuses the cache too — the committed message
	// render then moves the SAME node instead of duplicating it
	upsert := functionBody(html, "function upsertGoalCardNode(artifact)")
	if upsert == "" {
		t.Fatal("could not extract upsertGoalCardNode body")
	}
	if !strings.Contains(upsert, "goalCardNodeFor(artifact)") {
		t.Error("upsertGoalCardNode must reuse the goalCardNodeFor cache")
	}
}

// Latest wins: the LAST goal-ref message mounts the live card; earlier refs
// render the jump marker; the committed mount clears the ghost stamp.
func TestIndexGoalRefLatestWinsMountRule(t *testing.T) {
	html := readIndexForGoalThreadCards(t)
	body := functionBody(html, "function scoutGoalRefRecordNode(message, artifact)")
	if body == "" {
		t.Fatal("could not extract scoutGoalRefRecordNode body")
	}
	for _, want := range []string{
		"lastRefId",
		"jump to the card",
		"scrollIntoView",
		"goalCardNodeFor(artifact)",
		"delete card.dataset.goalGhostThreadId",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scoutGoalRefRecordNode body missing %q", want)
		}
	}
	// the rebuild pass detaches cached cards and re-mounts this thread's own
	render := functionBody(html, "function renderActiveScoutThread()")
	if render == "" {
		t.Fatal("could not extract renderActiveScoutThread body")
	}
	for _, want := range []string{
		"querySelectorAll('.goalcard')",
		"remountGhostGoalCards()",
	} {
		if !strings.Contains(render, want) {
			t.Errorf("renderActiveScoutThread body missing %q", want)
		}
	}
}

// The run log (feed redesign §1): consecutive stage deliverables merge into
// ONE .runlog container — entries dedup on the revision-stripped stage title,
// update in place, and open the artifact in the chat-side stage (§7), never
// as N orphan system lines.
func TestIndexStageArtifactRunlogContract(t *testing.T) {
	html := readIndexForGoalThreadCards(t)
	body := functionBody(html, "function scoutStageArtifactNode(message)")
	if body == "" {
		t.Fatal("could not extract scoutStageArtifactNode body")
	}
	for _, want := range []string{
		// the dedup key strips the revision marker so a redo updates in place
		`replace(/\s*\(revision \d+\)\s*/g, ' ')`,
		"runlogOpen",
		"runlog__entry",
		"openArtifactStage(entry.dataset.artifactId)",
		// while the container is open, later stages emit nothing new
		"document.createDocumentFragment()",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scoutStageArtifactNode body missing %q", want)
		}
	}
	// the record router still sends kind:"artifact" here, and the thread
	// teardown removes the container with the other feed nodes
	if !strings.Contains(html, ".scout-chat-msg, .scout-chat-system, .scout-chat-research, .runlog") {
		t.Error("clearScoutChatThreadNodes must tear down .runlog containers")
	}
}
