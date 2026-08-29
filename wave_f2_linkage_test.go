package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// goalHTTPWithPackage proves the retired direct goal door cannot be revived by
// either legacy package field.
func goalHTTPWithPackage(t *testing.T, field, packageID string) (int, meetingMemoryEntry) {
	t.Helper()
	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Package goal origin", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create private origin thread: %v", err)
	}
	body, _ := json.Marshal(map[string]any{
		"objective":     "package the IP thesis into a one-pager",
		field:           packageID,
		"originSurface": "chat:" + thread.ID,
	})
	req := httptest.NewRequest(http.MethodPost, "/assistant/goal", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost")
	req.Host = "localhost"
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	assistantGoalHandler(rec, req)

	var payload struct {
		Artifact meetingMemoryEntry `json:"artifact"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &payload)
	return rec.Code, payload.Artifact
}

// The top showcase bug: a goal launched from the palette produced artifacts but
// the package stayed empty because the HTTP door ignored the package field. The
// door must thread the chosen package into the launched goal's PackageID (which
// the engine stamps onto the artifact metadata and later attaches on save).
func TestGoalHTTPEndpointThreadsPackage(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	installFakeResponder(t, goalResponderRoutes{})

	pkg, err := kanbanApp.createVenturePackage("Aurora", "an IP thesis", "AJ")
	if err != nil {
		t.Fatalf("create package: %v", err)
	}

	code, artifact := goalHTTPWithPackage(t, "package", pkg.ID)
	if code != http.StatusGone {
		t.Fatalf("status=%d, want %d", code, http.StatusGone)
	}
	if artifact.ID != "" || len(kanbanApp.inFlightGoalsForUser("aj@shareability.com")) != 0 {
		t.Fatalf("retired goal door launched package work: %#v", artifact)
	}
}

// The binder/library doors send "packageId"; the door accepts it as an alias.
func TestGoalHTTPEndpointAcceptsPackageIdAlias(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	installFakeResponder(t, goalResponderRoutes{})

	pkg, err := kanbanApp.createVenturePackage("Aurora", "an IP thesis", "AJ")
	if err != nil {
		t.Fatalf("create package: %v", err)
	}

	code, artifact := goalHTTPWithPackage(t, "packageId", pkg.ID)
	if code != http.StatusGone {
		t.Fatalf("status=%d, want %d", code, http.StatusGone)
	}
	if artifact.ID != "" || len(kanbanApp.inFlightGoalsForUser("aj@shareability.com")) != 0 {
		t.Fatalf("retired goal door accepted packageId alias: %#v", artifact)
	}
}

// --- Frontend markers (function-body-scoped, not substring-anywhere) --------

func TestIndexTitleIsStride(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)
	if !strings.Contains(html, "<title>Stride</title>") {
		t.Error("the document <title> must be Stride")
	}
	if strings.Contains(html, "<title>Bonfire</title>") {
		t.Error("the stale <title>Bonfire</title> must be gone")
	}
}

func TestPalettePackagePickerIsRetired(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)

	for _, retired := range []string{"function paletteBuildPackageField", "function paletteShowForm", "function paletteRunForm", "function paletteSelectTool", "function runGoalPipeline"} {
		if strings.Contains(html, retired) {
			t.Errorf("retired client palette branch remains: %q", retired)
		}
	}
}

// F1's salvage metadata must be surfaced on the goalcard: the live stage line
// names an in-flight revision, and a needs_attention terminal card opens the
// saved draft with an honest gap one-liner — in the right-side artifact stage
// over the thread, never a tool yank to the Intelligence tab.
func TestGoalcardSurfacesSalvageMetadata(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)

	update := functionBody(html, "function updateGoalCard(card, artifact)")
	if update == "" {
		t.Fatal("index.html missing updateGoalCard")
	}
	if !strings.Contains(update, "m.goalRevisionNote") {
		t.Error("updateGoalCard must surface goalRevisionNote on the live stage line")
	}

	terminal := functionBody(html, "function goalCardRenderTerminal(card, artifact, plan, state, prevState)")
	if terminal == "" {
		t.Fatal("index.html missing goalCardRenderTerminal")
	}
	for _, want := range []string{"m.deliverableArtifactId", "m.goalGap", "open draft", "openArtifactStage(deliverableId, goalArtifactTitle(artifact))"} {
		if !strings.Contains(terminal, want) {
			t.Errorf("goalCardRenderTerminal missing salvage affordance %q", want)
		}
	}
	if strings.Contains(terminal, "openAgentArtifact({ id: deliverableId })") {
		t.Error("the salvage draft must open in the artifact stage, not yank the active tool to Intelligence")
	}
}

// A notification row carrying an artifactId / threadId must deep-link, not sit
// dead — the showcase found clicking one did nothing.
func TestNotificationRowsDeepLink(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)

	// The row's click is wired to the opener (non-approval rows).
	node := functionBody(html, "function notificationItemNode(entry)")
	if node == "" {
		t.Fatal("index.html missing notificationItemNode")
	}
	if !strings.Contains(node, "openNotificationEntry(entry)") {
		t.Error("notification rows must invoke openNotificationEntry on click")
	}

	// The opener deep-links by threadId (goalcard/thread) and artifactId (reader).
	open := functionBody(html, "function openNotificationEntry(entry)")
	if open == "" {
		t.Fatal("index.html missing openNotificationEntry")
	}
	for _, want := range []string{"entry.threadId", "selectScoutChatThread(", "entry.artifactId", "openAgentArtifact("} {
		if !strings.Contains(open, want) {
			t.Errorf("openNotificationEntry missing deep-link branch %q", want)
		}
	}
}
