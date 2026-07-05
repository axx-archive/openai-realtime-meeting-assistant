package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Wave 11 (Spectacular OS) — quick-select tool palette + /goal running-state
// cards + return-to-origin card. These markers pin the load-bearing wiring so a
// refactor that severs a door, the stage rail, the trust line, or the event
// consumer fails CI. Seams are scoped to their real function bodies (the Wave 6
// lesson: a substring-anywhere check passes even against dead code).

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
// in the payload buildToolsPayload emits.
func TestAssistantToolsPayloadDrivesPaletteContract(t *testing.T) {
	groups := buildToolsPayload()

	wantOrder := []string{toolGroupIdeate, toolGroupPackage, toolGroupMarket, toolGroupPortfolio}
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
			seen++
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
		t.Fatalf("palette payload carries %d tools, want the full 12-tool menu", seen)
	}
}

// --- Frontend markers: the three doors, the rail, the trust line ------------

func TestIndexHasPaletteMarkers(t *testing.T) {
	html := readIndexForPalette(t)

	// Structural + style presence (namespaced, per monolith discipline).
	for _, want := range []string{
		`id="scoutChatToolsBtn"`,
		`class="scout-chat-tools"`,
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

// The palette must open from BOTH the + Tools button and the "/" first-char
// trigger — wired inside their real handlers, not merely defined.
func TestPaletteOpensFromBothDoors(t *testing.T) {
	html := readIndexForPalette(t)

	if !strings.Contains(html, "scoutChatToolsBtn?.addEventListener('click', () => openToolPalette('button'))") {
		t.Error("the + Tools button is not wired to openToolPalette")
	}
	if !strings.Contains(html, "openToolPalette('slash'") {
		t.Error("the \"/\" first-char trigger is not wired to openToolPalette")
	}

	// The slash door lives inside the composer's input handler, and it must not
	// swallow the /goal command (siblings, not rivals).
	if !strings.Contains(html, "const isGoalDoor = /^\\/goal(\\s|$)/i.test(value)") {
		t.Error("the slash door must recognize and defer to the /goal command")
	}
}

// runGoalPipeline is the single POST every door converges on, and it must carry
// the toolTemplate so the engine applies the tool's prompt body.
func TestRunGoalPipelinePostsToolTemplate(t *testing.T) {
	html := readIndexForPalette(t)
	body := functionBody(html, "async function runGoalPipeline(spec)")
	if body == "" {
		t.Fatal("index.html missing runGoalPipeline")
	}
	for _, want := range []string{
		"'/assistant/goal'",
		"body.toolTemplate = String(spec.toolTemplate)",
		"originSurface",
		"upsertGoalCardNode(",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("runGoalPipeline missing %q — the palette/goal contract is broken", want)
		}
	}

	// The palette Run passes the tool id as the template.
	sel := functionBody(html, "function paletteSelectTool(tool, options)")
	if sel == "" {
		t.Fatal("index.html missing paletteSelectTool")
	}
	if !strings.Contains(sel, "toolTemplate: tool.id") {
		t.Error("paletteSelectTool (run-with-defaults) must launch with toolTemplate: tool.id")
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
		"waiting on AJ",
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

	body, _ := json.Marshal(map[string]any{"objective": "map the fintech landscape", "originSurface": "chat:thread-xyz"})
	req := httptest.NewRequest(http.MethodPost, "/assistant/goal", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost")
	req.Host = "localhost"
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	assistantGoalHandler(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Artifact meetingMemoryEntry `json:"artifact"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Artifact.Metadata["originSurface"] != "chat:thread-xyz" {
		t.Fatalf("goal artifact originSurface=%q, want chat:thread-xyz — the return card cannot route without it", payload.Artifact.Metadata["originSurface"])
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

	artifact, appended, err := kanbanApp.createOSArtifactWithMetadata(
		"workflow", "market map", "Done.", "AJ",
		map[string]string{
			"mode": "goal", "goalStatus": "complete", "status": "complete",
			"originKind": "private_thread", "originSurface": "chat:thread-xyz", "title": "Market Map",
		},
	)
	if err != nil || !appended {
		t.Fatalf("create goal artifact: appended=%v err=%v", appended, err)
	}
	completed := waitForOSEvent(t, conn, osEventArtifactCompleted, 5*time.Second)
	if completed.Ref != artifact.ID {
		t.Fatalf("completed ref=%q, want %q", completed.Ref, artifact.ID)
	}
	if completed.OriginSurface != "chat:thread-xyz" {
		t.Fatalf("event OriginSurface=%q, want chat:thread-xyz — the return card routes on this, not the coarse originKind", completed.OriginSurface)
	}
}

// --- Fidelity fix: the conversational door carries tool.id ------------------

// paletteConversationalHandoff used to drop the tool template, so deep_research
// launched contract-gated from Run and generic from the composer. The handoff
// must arm tool.id and the send path must carry it to the server.
func TestPaletteConversationalHandoffCarriesToolTemplate(t *testing.T) {
	html := readIndexForPalette(t)

	hand := functionBody(html, "function paletteConversationalHandoff(tool)")
	if hand == "" {
		t.Fatal("index.html missing paletteConversationalHandoff")
	}
	if !strings.Contains(hand, "pendingScoutToolTemplate = { toolId: tool.id") {
		t.Error("paletteConversationalHandoff must arm pendingScoutToolTemplate = {toolId, name, threadId} — otherwise the talk-it-out door drops the tool contract")
	}
	if !strings.Contains(hand, "threadId: activeScoutThreadId") {
		t.Error("the armed template must be scoped to the thread it was armed in — an unscoped template hijacks sends in other threads")
	}
	if !strings.Contains(hand, "renderScoutFollowUpTarget()") {
		t.Error("paletteConversationalHandoff must render the armed chip — an invisible armed template cannot be dismissed")
	}

	// The composer send captures-and-clears the armed template (one send only),
	// dropping a template armed for another thread instead of firing it here.
	send := functionBody(html, "function sendScoutChat(text)")
	if send == "" {
		t.Fatal("index.html missing sendScoutChat")
	}
	for _, want := range []string{"pendingScoutToolTemplate.threadId === activeScoutThreadId", "pendingScoutToolTemplate = null", "sendScoutChatViaOffice(trimmed, files, toolTemplate)"} {
		if !strings.Contains(send, want) {
			t.Errorf("sendScoutChat missing %q — the armed tool template does not ride the send (thread-scoped, one send only)", want)
		}
	}

	// The armed intent dies wherever the user walks away from it: thread
	// switch, composer emptied, palette re-open, and the Run door.
	renderThread := functionBody(html, "function renderActiveScoutThread()")
	if !strings.Contains(renderThread, "pendingScoutToolTemplate.threadId !== activeScoutThreadId") {
		t.Error("renderActiveScoutThread must drop a tool template armed for another thread")
	}
	chips := functionBody(html, "function renderScoutFollowUpTarget()")
	if !strings.Contains(chips, "pendingScoutToolTemplate") || !strings.Contains(chips, "Clear armed tool") {
		t.Error("renderScoutFollowUpTarget must render a visible, dismissible chip for the armed tool template")
	}

	// The office POST forwards toolTemplate on the wire and treats it as
	// explicit engagement (no @scout needed, like a follow-up target).
	office := functionBody(html, "async function sendScoutChatViaOffice(text, files = [], toolTemplate = '')")
	if office == "" {
		t.Fatal("index.html missing sendScoutChatViaOffice(text, files, toolTemplate)")
	}
	for _, want := range []string{"toolTemplate ? { text, files, toolTemplate }", "Boolean(toolTemplate)"} {
		if !strings.Contains(office, want) {
			t.Errorf("sendScoutChatViaOffice missing %q — toolTemplate does not reach the messages POST", want)
		}
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
