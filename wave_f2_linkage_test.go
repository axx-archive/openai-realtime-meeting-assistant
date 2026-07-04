package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// goalHTTPWithPackage POSTs an objective + package field through the real
// /assistant/goal door as aj and returns the decoded thread/artifact payload.
func goalHTTPWithPackage(t *testing.T, field, packageID string) (int, meetingMemoryEntry) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"objective":     "package the IP thesis into a one-pager",
		field:           packageID,
		"originSurface": "chat:thread-xyz",
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
	if code != http.StatusAccepted {
		t.Fatalf("status=%d, want %d", code, http.StatusAccepted)
	}
	if artifact.Metadata["packageId"] != pkg.ID {
		t.Fatalf("launched goal packageId=%q, want %q (package linkage broken)", artifact.Metadata["packageId"], pkg.ID)
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
	if code != http.StatusAccepted {
		t.Fatalf("status=%d, want %d", code, http.StatusAccepted)
	}
	if artifact.Metadata["packageId"] != pkg.ID {
		t.Fatalf("packageId alias not honored: got %q, want %q", artifact.Metadata["packageId"], pkg.ID)
	}
}

// --- Frontend markers (function-body-scoped, not substring-anywhere) --------

func TestIndexTitleIsBonfireOS(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)
	if !strings.Contains(html, "<title>BonfireOS</title>") {
		t.Error("the document <title> must be BonfireOS")
	}
	if strings.Contains(html, "<title>Bonfire</title>") {
		t.Error("the stale <title>Bonfire</title> must be gone")
	}
}

// The palette form must offer a package picker and both run paths must actually
// forward the chosen (or default) package into runGoalPipeline's POST body.
func TestPalettePackagePickerWired(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)

	// The picker builder exists and sources options from the packages list.
	builder := functionBody(html, "function paletteBuildPackageField()")
	if builder == "" {
		t.Fatal("index.html missing paletteBuildPackageField")
	}
	for _, want := range []string{"palette__pkg-select", "populatePalettePackageOptions", "loadPackages()"} {
		if !strings.Contains(builder, want) {
			t.Errorf("paletteBuildPackageField missing %q", want)
		}
	}

	// The options come from the real packages list, standalone first.
	populate := functionBody(html, "function populatePalettePackageOptions(select, preferredId)")
	if populate == "" {
		t.Fatal("index.html missing populatePalettePackageOptions")
	}
	for _, want := range []string{"packagesData", "standalone"} {
		if !strings.Contains(populate, want) {
			t.Errorf("populatePalettePackageOptions missing %q", want)
		}
	}

	// The form actually mounts the picker.
	form := functionBody(html, "function paletteShowForm(tool)")
	if form == "" {
		t.Fatal("index.html missing paletteShowForm")
	}
	if !strings.Contains(form, "paletteBuildPackageField()") {
		t.Error("paletteShowForm must mount the package picker")
	}
	if !strings.Contains(form, "paletteRunForm(tool, fieldEls, pkgField.select)") {
		t.Error("paletteShowForm's Run must pass the picked package to paletteRunForm")
	}

	// The form-run path forwards the picked package.
	runForm := functionBody(html, "function paletteRunForm(tool, fieldEls, packageSelect)")
	if runForm == "" {
		t.Fatal("index.html missing paletteRunForm with a packageSelect param")
	}
	if !strings.Contains(runForm, "package: pkg") {
		t.Error("paletteRunForm must forward the picked package to runGoalPipeline")
	}

	// The quick-run (⌘↵ / no-required-field) path defaults to the open package.
	sel := functionBody(html, "function paletteSelectTool(tool, options)")
	if sel == "" {
		t.Fatal("index.html missing paletteSelectTool")
	}
	if !strings.Contains(sel, "package: paletteDefaultPackageId()") {
		t.Error("paletteSelectTool quick-run must link to the default package")
	}

	// runGoalPipeline must put the package on the POST body.
	pipeline := functionBody(html, "async function runGoalPipeline(spec)")
	if !strings.Contains(pipeline, "body.package = String(spec.package)") {
		t.Error("runGoalPipeline must send spec.package to /assistant/goal")
	}
}

// F1's salvage metadata must be surfaced on the goalcard: the live stage line
// names an in-flight revision, and a needs_attention terminal card opens the
// saved draft with an honest gap one-liner.
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
	for _, want := range []string{"m.deliverableArtifactId", "m.goalGap", "open draft", "openAgentArtifact({ id: deliverableId })"} {
		if !strings.Contains(terminal, want) {
			t.Errorf("goalCardRenderTerminal missing salvage affordance %q", want)
		}
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
