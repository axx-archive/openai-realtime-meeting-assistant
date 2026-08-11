package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Wave 11 legacy registry/admin fixtures + /goal running-state cards and
// return-to-origin card. The normal product is conversation-first: these tests
// pin the registry and work-card contracts while proving the client cannot
// select or transmit a tool template.

func readIndexForPalette(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// --- The palette contract, from the payload the client renders --------------

// The palette renders straight from GET /assistant/tools with no client-side
// re-sorting, so every field a tile depends on must be present and well-formed
// in the payload buildToolsPayload emits. Wave 4 item 17 EXTENDS the payload
// with a fifth "processes" group — additive: the four lifecycle groups keep
// their order and their full 12-tool menu, and every process entry satisfies
// the same tile contract.
func TestAssistantToolsPayloadDrivesPaletteContract(t *testing.T) {
	groups := buildToolsPayload()

	// Wave A item 4: the flagship processes group renders FIRST ("End-to-end"),
	// then the four lifecycle groups in their unchanged order.
	wantOrder := []string{toolGroupProcesses, toolGroupIdeate, toolGroupPackage, toolGroupMarket, toolGroupPortfolio}
	if len(groups) != len(wantOrder) {
		t.Fatalf("got %d groups, want %d", len(groups), len(wantOrder))
	}

	seen := 0
	for i, group := range groups {
		if group.ID != wantOrder[i] {
			t.Fatalf("group %d id=%q, want %q (palette reads groups left-to-right in this order)", i, group.ID, wantOrder[i])
		}
		if strings.TrimSpace(group.Label) == "" {
			t.Fatalf("group %q has no display label — the palette renders it as a section header", group.ID)
		}
		for _, tool := range group.Tools {
			if group.ID != toolGroupProcesses {
				seen++
			}
			if strings.TrimSpace(tool.ID) == "" {
				t.Fatalf("a %s tool has no id (the palette keys tiles + recents on it)", group.ID)
			}
			if strings.TrimSpace(tool.Name) == "" {
				t.Errorf("tool %q has no name", tool.ID)
			}
			if strings.TrimSpace(tool.Promise) == "" {
				t.Errorf("tool %q has no promise line (the tile's second row)", tool.ID)
			}
			if tool.Group != group.ID {
				t.Errorf("tool %q group=%q, want %q (payload grouping must be self-consistent)", tool.ID, tool.Group, group.ID)
			}
			if strings.TrimSpace(tool.Authority) == "" {
				t.Errorf("tool %q has no authority class (the palette Run passes it as authorityHint)", tool.ID)
			}
			switch tool.InputMode {
			case toolInputForm:
				if n := len(tool.FormFields); n < 1 || n > 3 {
					t.Errorf("form tool %q has %d fields, want 1-3 (the sheet morphs into a compact 1-3 field card)", tool.ID, n)
				}
				for _, field := range tool.FormFields {
					if strings.TrimSpace(field.Key) == "" || strings.TrimSpace(field.Label) == "" {
						t.Errorf("form tool %q has a field missing key/label: %+v", tool.ID, field)
					}
				}
			case toolInputConversational:
				if len(tool.FormFields) != 0 {
					t.Errorf("conversational tool %q must carry no form fields (it prefills the composer)", tool.ID)
				}
			default:
				t.Errorf("tool %q has invalid inputMode %q (the palette branches on form vs conversational)", tool.ID, tool.InputMode)
			}
		}
	}
	if seen != 12 {
		t.Fatalf("the four lifecycle groups carry %d tools, want the full 12-tool menu (processes are additive, never replacements)", seen)
	}
	// The processes group (now first) never carries a tool id, and hidden
	// processes (the test-only process_probe) never serve publicly.
	for _, entry := range groups[0].Tools {
		if _, isTool := toolByID(entry.ID); isTool {
			t.Errorf("process entry %q shadows a 12-tool id", entry.ID)
		}
		if entry.ID == "process_probe" {
			t.Error("hidden process_probe must not serve in the public payload")
		}
	}
}

// The flagship packaging_studio serves as a processes-group tile with the exact
// contract the palette enforces (Wave 4 item 18): conversational, no form
// fields, a promise line, a workspace_write authority, and the shipped-deck
// contract — and the router enum picks its id up like any tool id.
func TestPackagingStudioServesInPaletteAndRouter(t *testing.T) {
	groups := buildToolsPayload()
	processes := groups[0]
	if processes.ID != toolGroupProcesses {
		t.Fatalf("first group=%q, want the flagship processes group", processes.ID)
	}

	var studio *packagingTool
	for index := range processes.Tools {
		if processes.Tools[index].ID == packagingStudioProcessID {
			studio = &processes.Tools[index]
			break
		}
	}
	if studio == nil {
		t.Fatalf("packaging_studio missing from the processes group: %+v", processes.Tools)
	}
	if studio.Group != toolGroupProcesses {
		t.Errorf("packaging_studio group=%q, want %q", studio.Group, toolGroupProcesses)
	}
	if strings.TrimSpace(studio.Name) == "" || strings.TrimSpace(studio.Promise) == "" {
		t.Errorf("packaging_studio tile missing name/promise: %+v", studio)
	}
	if studio.InputMode != toolInputConversational || len(studio.FormFields) != 0 {
		t.Errorf("packaging_studio must be conversational with no form fields: %+v", studio)
	}
	if studio.Authority != toolAuthorityWorkspaceWrite {
		t.Errorf("packaging_studio authority=%q, want %q", studio.Authority, toolAuthorityWorkspaceWrite)
	}
	if studio.Contract != packagingStudioDeckContract {
		t.Errorf("packaging_studio contract=%q, want the shipped deck %q", studio.Contract, packagingStudioDeckContract)
	}
	// It is a process, never a 12-tool id.
	if _, isTool := toolByID(packagingStudioProcessID); isTool {
		t.Error("packaging_studio shadows a 12-tool id — the taxonomies must stay separate")
	}

	// The router's injected enum proposes it like any tool id.
	routerTools := scoutRouterTools()
	schema, _ := routerTools[0].InputSchema["properties"].(map[string]any)
	toolID, _ := schema["tool_id"].(map[string]any)
	enum, ok := toolID["enum"].([]string)
	if !ok {
		t.Fatalf("propose_tool_run enum shape changed: %+v", toolID)
	}
	found := false
	for _, id := range enum {
		if id == packagingStudioProcessID {
			found = true
		}
	}
	if !found {
		t.Fatalf("router enum missing packaging_studio: %v", enum)
	}
}

// --- Frontend markers: the three doors, the rail, the trust line ------------

func TestIndexHasPaletteMarkers(t *testing.T) {
	html := readIndexForPalette(t)

	// Structural + style presence (namespaced, per monolith discipline).
	for _, want := range []string{
		".palette__sheet",
		".palette__tile",
		".palette__well",
		".palette__empty-line",
		".goalcard__rail",
		".goalcard__node",
		".goalcard__trust",
		".returncard",
		"function openToolPalette",
		"function runGoalPipeline",
		"function upsertGoalCardNode",
		"function renderReturnCard",
		"'/assistant/tools'",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing palette/goalcard marker: %q", want)
		}
	}

	// Reduced motion: the spark burst becomes an instant, motionless fill.
	if !strings.Contains(html, ".goalcard__spark span { animation: none") {
		t.Error("reduced-motion block must neutralize the ember spark burst")
	}
}

// The legacy palette may remain for controlled fixtures, but normal people get
// no tool button, slash palette, or client-selected capability.
func TestNormalComposerHasNoToolPaletteDoors(t *testing.T) {
	html := readIndexForPalette(t)
	for _, forbidden := range []string{
		`id="scoutChatToolsBtn"`,
		`aria-label="Run a task or tool"`,
		"scoutChatToolsBtn?.addEventListener",
		"openToolPalette('slash'",
	} {
		if strings.Contains(html, forbidden) {
			t.Errorf("normal composer still exposes client tool selection %q", forbidden)
		}
	}
}

// The legacy helper is a compatibility shim only: it must re-enter ordinary
// conversation and cannot post any client-selected launch contract.
func TestRunGoalPipelineReentersConversationRouter(t *testing.T) {
	html := readIndexForPalette(t)
	body := functionBody(html, "async function runGoalPipeline(spec)")
	if body == "" {
		t.Fatal("index.html missing runGoalPipeline")
	}
	for _, want := range []string{"sendScoutChat(objective)"} {
		if !strings.Contains(body, want) {
			t.Errorf("runGoalPipeline missing %q — the conversation router migration is broken", want)
		}
	}
	for _, forbidden := range []string{"/assistant/goal", "fetch(", "body.", "originSurface", "upsertGoalCardNode(", "body.toolTemplate", "body.authorityHint", "spec.toolTemplate", "spec.authorityHint"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("runGoalPipeline still transmits client authority %q", forbidden)
		}
	}
}

// The stage-rail renderer must consume progress events through the osEventHandlers
// extension point (the rich-consumer fetch-by-ref contract), and it must read the
// persisted goalPlan.
func TestGoalcardConsumesProgressEvents(t *testing.T) {
	html := readIndexForPalette(t)

	// The consumer body: artifact_progress + artifact_completed drive the sync.
	if !strings.Contains(html, "if (event.kind === 'artifact_progress' || event.kind === 'artifact_completed') {") ||
		!strings.Contains(html, "goalCardScheduleSync()") {
		t.Error("the osEventHandlers consumer must route artifact_progress/artifact_completed into the goalcard sync")
	}

	update := functionBody(html, "function updateGoalCard(card, artifact)")
	if update == "" {
		t.Fatal("index.html missing updateGoalCard")
	}
	for _, want := range []string{"goalPlanFromArtifact", "goalStageIndex", ".goalcard__node", "data-goal-stageline"} {
		if !strings.Contains(update, want) {
			t.Errorf("updateGoalCard missing %q — the stage rail is not driven from goalPlan/currentStage", want)
		}
	}
}

// The completion card must surface the calibrated-trust line from report
// metadata (gate outcome + count of ASSUMED claims), and the gate state must
// reuse the admin approval vocabulary.
func TestGoalcardTerminalRendersTrustLine(t *testing.T) {
	html := readIndexForPalette(t)
	body := functionBody(html, "function goalCardRenderTerminal(card, artifact, plan, state, prevState)")
	if body == "" {
		t.Fatal("index.html missing goalCardRenderTerminal")
	}
	for _, want := range []string{
		"report.gateOutcome",
		"report.assumedClaimCount",
		"marked ASSUMED — verify before sending",
		"canApproveExternalWrites()",
		"submitApproval(artifact.id, 'approve'",
		// P2-2: the gate's non-approver line names the approver's handle sourced
		// from the admin-gate config — the old hardcoded "waiting on AJ" literal
		// was replaced with the lowercase handle the product's voice uses.
		"waiting on ${accountHandle(artifactAdminEmail)}",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("goalCardRenderTerminal missing %q — trust line or gate vocabulary broken", want)
		}
	}
}

// --- Return-to-origin card data path (the critical review finding) ----------

// The client return card only fires when the completion event's originSurface
// names a chat thread ("chat:..."). The goal engine must persist originSurface
// onto the artifact, or the event falls back to the coarse originKind and the
// card never renders.
func TestGoalLaunchPersistsOriginSurface(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	installFakeResponder(t, goalResponderRoutes{})
	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Goal launch origin", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create private origin thread: %v", err)
	}
	originSurface := "chat:" + thread.ID

	launched, err := kanbanApp.launchGoalThread(goalLaunchSpec{
		Objective: "map the fintech landscape", CreatedBy: "aj@shareability.com",
		Origin: map[string]string{"originSurface": originSurface},
	})
	if err != nil {
		t.Fatal(err)
	}
	if launched.Artifact.Metadata["originSurface"] != originSurface {
		t.Fatalf("goal artifact originSurface=%q, want %s — the return card cannot route without it", launched.Artifact.Metadata["originSurface"], originSurface)
	}
}

// The push event a completed goal fans out must carry the fine-grained
// originSurface (not the coarse originKind fallback), or maybeRenderReturnCard's
// origin.startsWith("chat:") check always bails.
func TestGoalCompletionEventCarriesOriginSurface(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	conn := dialIsolatedWebsocket(t, server, "aj@shareability.com")
	sendOfficeHello(t, conn)
	drainOfficeReplay(t, conn)
	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Goal completion origin", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create private origin thread: %v", err)
	}
	originSurface := "chat:" + thread.ID

	artifact, appended, err := kanbanApp.createOSArtifactWithMetadata(
		"workflow", "market map", "Done.", "AJ",
		map[string]string{
			"mode": "goal", "goalStatus": "complete", "status": "complete",
			"originKind": "private_thread", "originSurface": originSurface, "title": "Market Map",
		},
	)
	if err != nil || !appended {
		t.Fatalf("create goal artifact: appended=%v err=%v", appended, err)
	}
	completed := waitForOSEvent(t, conn, osEventArtifactCompleted, 5*time.Second)
	if completed.Ref != artifact.ID {
		t.Fatalf("completed ref=%q, want %q", completed.Ref, artifact.ID)
	}
	if completed.OriginSurface != originSurface {
		t.Fatalf("event OriginSurface=%q, want %s — the return card routes on this, not the coarse originKind", completed.OriginSurface, originSurface)
	}
}

// --- Conversation-first transport owns no tool authority --------------------

func TestNormalComposerNeverCarriesToolTemplate(t *testing.T) {
	html := readIndexForPalette(t)
	send := functionBody(html, "function sendScoutChat(text)")
	if send == "" {
		t.Fatal("index.html missing sendScoutChat")
	}
	if strings.Contains(send, "toolTemplate") || !strings.Contains(send, "sendScoutChatViaOffice(trimmed, files)") {
		t.Fatal("sendScoutChat must forward only natural-language text/files")
	}
	office := functionBody(html, "async function sendScoutChatViaOffice(text, files = [])")
	if office == "" {
		t.Fatal("index.html missing conversation-only sendScoutChatViaOffice")
	}
	for _, want := range []string{"const requestBody = JSON.stringify(messagePayload)", "body: requestBody", "messagePayload.operationId"} {
		if !strings.Contains(office, want) {
			t.Errorf("sendScoutChatViaOffice missing replay-safe transport %q", want)
		}
	}
	if strings.Contains(office, "toolTemplate") {
		t.Error("normal message transport still contains client toolTemplate authority")
	}
}

// --- Signal beacon: artifact opens are captured ------------------------------

// Opening an artifact must fire the non-blocking POST /artifacts/open beacon
// (spec §5 open/ignore signal) from both open doors: the artifact cards
// (openAgentArtifact) and the assistant select_artifact action.
func TestArtifactOpenBeaconWired(t *testing.T) {
	html := readIndexForPalette(t)

	beacon := functionBody(html, "function beaconArtifactOpen(artifactId)")
	if beacon == "" {
		t.Fatal("index.html missing beaconArtifactOpen")
	}
	for _, want := range []string{"'/artifacts/open'", "method: 'POST'", ".catch(() => {})"} {
		if !strings.Contains(beacon, want) {
			t.Errorf("beaconArtifactOpen missing %q — the open signal must be a silent fire-and-forget POST", want)
		}
	}
	// Volume guardrail: the datum is open vs never-opened; re-clicks in the
	// same session must not re-fire the beacon.
	if !strings.Contains(beacon, "beaconedArtifactOpens.has(id)") {
		t.Error("beaconArtifactOpen must dedupe per artifact per session — every click flooding the store is the §5 volume trap")
	}

	open := functionBody(html, "function openAgentArtifact(entry)")
	if open == "" {
		t.Fatal("index.html missing openAgentArtifact")
	}
	if !strings.Contains(open, "beaconArtifactOpen(entry.id)") {
		t.Error("openAgentArtifact does not fire the open beacon")
	}

	actions := functionBody(html, "function handleOSAssistantActions(actions)")
	if actions == "" {
		t.Fatal("index.html missing handleOSAssistantActions")
	}
	if !strings.Contains(actions, "beaconArtifactOpen(artifactId)") {
		t.Error("the select_artifact action does not fire the open beacon")
	}
}

// --- Candor rubric + client-facing copy-law flags (Wave 2 item 10, data-only) --

// The candor dimension is pinned on exactly the two contracts the spec names:
// the one-pager and the investor-update memo. Bar 7+ — hedging or hype on a
// page that leaves the building is a gate failure, not a style note.
func TestCandorRubricDimensionsPinned(t *testing.T) {
	const wantMeasure = "names real risks/losses plainly; no hedging or hype"
	for _, id := range []string{"one_pager", "investor_update_memo"} {
		tool, ok := toolByID(id)
		if !ok {
			t.Fatalf("tool %q missing from the registry", id)
		}
		found := false
		for _, d := range tool.Rubric.Dimensions {
			if d.Name != "Candor" {
				continue
			}
			found = true
			if d.Bar < 7 {
				t.Errorf("%s candor bar=%d, want 7+", id, d.Bar)
			}
			if d.Measures != wantMeasure {
				t.Errorf("%s candor measures=%q, want %q", id, d.Measures, wantMeasure)
			}
		}
		if !found {
			t.Errorf("%s rubric has no Candor dimension", id)
		}
	}
}

// ClientFacing is set on exactly the four contracts whose copy leaves the
// building (one_pager_v1, deck_outline_v1, update_memo_v1, package_binder_v1)
// — the law sweep bans em dashes on this class and no other, and the list is
// registry data, never an engine hardcode.
func TestClientFacingCopyLawFlagsPinned(t *testing.T) {
	want := map[string]bool{
		"one_pager":            true,
		"deck_outline":         true,
		"package_assembly":     true,
		"investor_update_memo": true,
	}
	for _, tool := range packagingTools() {
		if tool.ClientFacing != want[tool.ID] {
			t.Errorf("tool %q clientFacing=%v, want %v", tool.ID, tool.ClientFacing, want[tool.ID])
		}
	}
}
