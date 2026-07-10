package main

// Files surface (card 095), frontend half. Grep-style pins in the
// frontend_attachments_test.go idiom: the rail earns a files tool, the tool
// registries route it, the #filesTool canvas lists /assistant/files rows with
// the feeds-the-brain badge and session-gated blob links, and the upload door
// posts multipart to /assistant/files/upload with the 64MB client cap.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForFilesSurface(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func TestIndexFilesToolRegistration(t *testing.T) {
	html := readIndexForFilesSurface(t)
	for _, want := range []string{
		// rail button + surface section
		`data-tool="files" aria-label="Files"`,
		`<section id="filesTool" class="memory-tool" aria-label="Files">`,
		// tool registries: routable + full-page treatment
		`const osToolIds = ['office', 'chat', 'artifacts', ...agentToolIds, 'memory', 'files']`,
		`const TOOL_IDS = ['office', 'room', 'chat', 'artifacts', ...agentToolIds, 'board', 'memory', 'files']`,
		// topbar identity + canon subtitle
		`files: 'Files'`,
		"shared materials · readable files feed the brain",
		// the meeting PiP follows you onto the files page
		"tool === 'memory' || tool === 'files')",
		// full-page CSS: surface swap + active pane
		`#appShell.is-authed[data-tool="files"] #filesTool`,
		`#appShell[data-tool="files"] .hearth-presentation`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing files tool registration %q", want)
		}
	}

	// applyToolState routes the files tool into its loader.
	start := strings.Index(html, "function applyToolState(tool)")
	end := strings.Index(html, "function headerDateLabel()")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("cannot scope applyToolState")
	}
	if !strings.Contains(html[start:end], "} else if (tool === 'files') {") {
		t.Fatal("applyToolState must branch to the files surface loader")
	}
}

func TestIndexFilesSurfaceDataLayer(t *testing.T) {
	html := readIndexForFilesSurface(t)
	for _, want := range []string{
		// list fetch + cache
		"async function loadFilesSurface(force = false)",
		"fetch('/assistant/files', { cache: 'no-store' })",
		// rows: day buckets, blob preview/download, origin chip, brain badge
		"function filesRowNode(file)",
		"memoryDayBucket(file?.createdAt)",
		"files-badge files-badge--ingested",
		// three-state brain badge: company recall / private-thread only / bytes
		"badge.textContent = 'in the brain'",
		"badge.textContent = 'in this chat'",
		"badge.textContent = 'stored'",
		"files-badge files-badge--thread",
		"selectScoutChatThread(file.originThreadId)",
		// upload door: hidden multi input, multipart POST, 64MB client cap
		`<input id="filesUploadInput" type="file" multiple hidden`,
		"fetch('/assistant/files/upload', { method: 'POST', body })",
		"file.size > 64 * 1024 * 1024",
		"is over the 64MB cap",
		// live refresh on the websocket file event
		"case 'file':",
		// empty state names the cap and the chat feed
		"up to 64MB each",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing files data-layer hook %q", want)
		}
	}

	// Preview links carry noopener — the blob route holds session authority.
	start := strings.Index(html, "function filesRowNode(file)")
	end := strings.Index(html, "function renderFilesSurface()")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("cannot scope filesRowNode")
	}
	rowNode := html[start:end]
	for _, want := range []string{
		"link.rel = 'noopener'",
		"link.target = '_blank'",
	} {
		if !strings.Contains(rowNode, want) {
			t.Fatalf("filesRowNode missing %q", want)
		}
	}
}

// TestIndexFilesTileOverflowGuards pins the card-110 Part A overflow fixes: the
// grid-item min-width:0, the clamp-on-inner-span Safari hardening, and the tile
// foot thread-chip shrink permission. The list-row name truncates with an
// ellipsis (white-space:nowrap), so it carries no overflow-wrap — that rule is
// inert under nowrap and was removed.
func TestIndexFilesTileOverflowGuards(t *testing.T) {
	html := readIndexForFilesSurface(t)

	cssBlock := func(selector string) string {
		t.Helper()
		open := strings.Index(html, selector)
		if open < 0 {
			t.Fatalf("index.html missing CSS selector %q", selector)
		}
		close := strings.Index(html[open:], "}")
		if close < 0 {
			t.Fatalf("CSS block for %q has no close brace", selector)
		}
		return html[open : open+close]
	}

	// grid item min-width:0 stops a long unbroken name blowing the track out;
	// overflow stays visible so the move-to menu can escape the tile edge.
	tile := cssBlock(".file-tile {")
	for _, want := range []string{"min-width: 0", "overflow: visible"} {
		if !strings.Contains(tile, want) {
			t.Fatalf(".file-tile block missing %q", want)
		}
	}

	// the 2-line clamp lives on the inner label span (a clamped <button> is
	// inconsistent in WebKit) and wraps long tokens into the clamp.
	label := cssBlock(".file-tile__name-label {")
	for _, want := range []string{"-webkit-line-clamp: 2", "overflow-wrap: anywhere"} {
		if !strings.Contains(label, want) {
			t.Fatalf(".file-tile__name-label block missing %q", want)
		}
	}

	// the tile-foot thread chip earns shrink permission (it already ellipsizes).
	thread := cssBlock(".file-tile .files-row__thread {")
	for _, want := range []string{"flex: 0 1 auto", "min-width: 0"} {
		if !strings.Contains(thread, want) {
			t.Fatalf(".file-tile .files-row__thread block missing %q", want)
		}
	}

	// the list-row name truncates with an ellipsis; overflow-wrap is inert under
	// white-space:nowrap and must not linger as a misleading rule.
	rowName := cssBlock(".files-row__name {")
	if !strings.Contains(rowName, "white-space: nowrap") {
		t.Fatal(".files-row__name must truncate with white-space: nowrap")
	}
	if strings.Contains(rowName, "overflow-wrap") {
		t.Fatal(".files-row__name must not carry an inert overflow-wrap under nowrap")
	}

	// fileTileNode wraps the visible name in the clamped inner span.
	start := strings.Index(html, "function fileTileNode(file)")
	end := strings.Index(html, "function filesRowNode(file)")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("cannot scope fileTileNode")
	}
	if !strings.Contains(html[start:end], "file-tile__name-label") {
		t.Fatal("fileTileNode must wrap the name in a clamped inner label span")
	}
}

// TestIndexArtifactStageSaveToFiles pins the card-110 explicit-save UI: the
// stage's Save-to-Files control, its qualification gate, the POST wiring, and
// the settled saved state.
func TestIndexArtifactStageSaveToFiles(t *testing.T) {
	html := readIndexForFilesSurface(t)
	for _, want := range []string{
		"function artifactQualifiesForFiles(entry)",
		"function artifactSaveToFilesControl(entry)",
		"fetch('/assistant/files/save', {",
		"body: JSON.stringify({ artifactId })",
		"button.textContent = 'Save to Files'",
		"button.textContent = 'Saved to Files'",
		"entry.metadata.savedToFiles = 'true'",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing artifact-stage save-to-files wiring %q", want)
		}
	}

	// openArtifactStage appends the control into headActions.
	start := strings.Index(html, "async function openArtifactStage(")
	end := strings.Index(html, "function handleOSAssistantActions(")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("cannot scope openArtifactStage")
	}
	stage := html[start:end]
	for _, want := range []string{
		"const saveToFilesControl = artifactSaveToFilesControl(entry)",
		"headActions.appendChild(saveToFilesControl)",
	} {
		if !strings.Contains(stage, want) {
			t.Fatalf("openArtifactStage missing %q", want)
		}
	}
}
