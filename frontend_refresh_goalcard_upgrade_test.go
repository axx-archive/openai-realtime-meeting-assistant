package main

// frontend_refresh_goalcard_upgrade_test.go — refresh-race pin: on boot,
// loadScoutChatThreads and loadArtifacts race; the thread render can land
// first with artifactEntries still empty, so a goal run's thread ref falls
// through isGoalArtifact and mounts the LEGACY .scout-chat-research card.
// When the /artifacts light list arrives, syncScoutChatResearchCards must
// detect the now-goal entry and re-run renderActiveScoutThread — the SAME
// pass the thread-click path uses — so refresh converges on the goalcard.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForRefreshGoalcardUpgrade(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// Causal pin: the legacy-card sync pass upgrades to the goalcard once the
// artifact entry lands, instead of only patching the legacy card in place.
func TestIndexArtifactLandUpgradesLegacyResearchCardToGoalCard(t *testing.T) {
	html := readIndexForRefreshGoalcardUpgrade(t)
	body := functionBody(html, "function syncScoutChatResearchCards()")
	if body == "" {
		t.Fatal("could not extract syncScoutChatResearchCards body")
	}
	for _, want := range []string{
		"isGoalArtifact(",
		"renderActiveScoutThread()",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("syncScoutChatResearchCards body missing %q — the boot render races /artifacts and mounts the legacy card; when the artifact list lands and an entry proves goal, the sync must re-run the same renderActiveScoutThread pass the thread-click path uses", want)
		}
	}
	// a scrolled-up reader must not get yanked to the bottom by the upgrade
	// re-render (~1-2s after boot, or on a live artifact push)
	if !strings.Contains(body, "scoutChatIsNearBottom()") {
		t.Error("syncScoutChatResearchCards upgrade re-render missing the scoutChatIsNearBottom scroll capture/restore")
	}
}

// Wiring pin: the upgrade check is reachable from both the boot /artifacts
// fetch and the live artifact-upsert path — removing either sync call
// silently reintroduces the refresh race.
func TestIndexArtifactLoadPathsReachTheGoalcardUpgrade(t *testing.T) {
	html := readIndexForRefreshGoalcardUpgrade(t)
	for _, check := range []struct {
		label string
		body  string
	}{
		{"boot /artifacts fetch", functionBody(html, "async function loadArtifacts()")},
		// the signature's `options = {}` default defeats functionBody's
		// first-brace heuristic — use the after-signature variant
		{"live artifact upsert", functionBodyAfterSignature(html, "function addArtifactEntry(entry, options = {})")},
	} {
		if check.body == "" {
			t.Fatalf("could not extract the %s function body", check.label)
		}
		if !strings.Contains(check.body, "syncScoutChatResearchCards()") {
			t.Errorf("the %s path no longer calls syncScoutChatResearchCards — the goalcard upgrade is unreachable from that path", check.label)
		}
	}
}

// Convergence pin: the thread-click path and the artifact-land upgrade path
// go through the identical renderer, so the goal-vs-legacy branch inside
// scoutChatMessageRecordNode stays the single decision point.
func TestIndexThreadClickPathSharesTheUpgradeRenderer(t *testing.T) {
	html := readIndexForRefreshGoalcardUpgrade(t)
	body := functionBody(html, "function selectScoutChatThread(id)")
	if body == "" {
		t.Fatal("could not extract selectScoutChatThread body")
	}
	if !strings.Contains(body, "renderActiveScoutThread()") {
		t.Error("selectScoutChatThread no longer calls renderActiveScoutThread — the click path and the artifact-land upgrade path must share one renderer")
	}
}
