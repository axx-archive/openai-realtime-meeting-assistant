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
		"'in the brain' : 'stored'",
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
