package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// writeStubRenderBinary creates a real executable file so resolveRenderBinary
// (exec.LookPath) passes while the runRenderExecCommand seam intercepts the
// actual invocations — CI has no chromium or pdftoppm.
func writeStubRenderBinary(t *testing.T, dir string, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write stub binary %s: %v", name, err)
	}
	return path
}

func TestRenderRunnerQueueClaimsLexicallyFirstAndCompletes(t *testing.T) {
	store := newRenderRunnerJobStore(t.TempDir())
	for _, id := range []string{"render-job-002", "render-job-001"} {
		if _, err := store.enqueue(renderRunnerJob{
			ID:         id,
			ArtifactID: "artifact-" + id,
			Kind:       renderJobKindDeck,
			HTML:       "<!doctype html><html><body>deck</body></html>",
		}); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
	}

	first, err := store.claimNext("test-runner")
	if err != nil {
		t.Fatalf("claimNext: %v", err)
	}
	if first == nil || first.ID != "render-job-001" {
		t.Fatalf("claimed=%+v, want lexically-first render-job-001", first)
	}
	if first.Status != renderJobStatusRunning || first.Attempts != 1 || first.RunnerID != "test-runner" {
		t.Fatalf("claimed job=%+v, want running/attempt-1/runner stamped", first)
	}

	// The claimed job is out of the queue: the next claim takes the sibling.
	second, err := store.claimNext("test-runner")
	if err != nil {
		t.Fatalf("claimNext second: %v", err)
	}
	if second == nil || second.ID != "render-job-002" {
		t.Fatalf("claimed=%+v, want render-job-002", second)
	}

	first.Status = renderJobStatusComplete
	if err := store.update(*first); err != nil {
		t.Fatalf("update: %v", err)
	}
	second.Status = renderJobStatusFailed
	if err := store.update(*second); err != nil {
		t.Fatalf("update second: %v", err)
	}
	if leftover, err := store.claimNext("test-runner"); err != nil || leftover != nil {
		t.Fatalf("claimNext on drained queue=%+v err=%v, want nil/nil", leftover, err)
	}
}

func TestEnqueueRenderExportPDFJobWritesQueueFile(t *testing.T) {
	queueDir := t.TempDir()
	t.Setenv("BONFIRE_RENDER_QUEUE_PATH", queueDir)

	job, err := enqueueRenderExportPDFJob("artifact-deck-1", "deck", "<!doctype html><html><body>deck</body></html>", "Launch deck")
	if err != nil {
		t.Fatalf("enqueueRenderExportPDFJob: %v", err)
	}
	if job.ID == "" || job.Type != renderJobTypeExportPDF || job.Status != renderJobStatusQueued {
		t.Fatalf("job=%+v, want queued export_pdf job", job)
	}
	if job.Kind != renderJobKindDeck {
		t.Fatalf("kind=%q, want deck", job.Kind)
	}

	store := newRenderRunnerJobStore(queueDir)
	stored, err := store.read(job.ID + ".json")
	if err != nil {
		t.Fatalf("read queued job: %v", err)
	}
	if stored.ArtifactID != "artifact-deck-1" || stored.Title != "Launch deck" || !strings.Contains(stored.HTML, "<!doctype html") {
		t.Fatalf("stored=%+v, want artifact/html/title carried in the job file", stored)
	}
	if stored.Metadata["workerBoundary"] != "render_sidecar_queue" {
		t.Fatalf("workerBoundary=%q, want render_sidecar_queue", stored.Metadata["workerBoundary"])
	}

	if _, err := enqueueRenderExportPDFJob("", "deck", "<html></html>", ""); err == nil {
		t.Fatal("enqueue accepted an empty artifact id")
	}
	if _, err := enqueueRenderExportPDFJob("artifact-2", "deck", "   ", ""); err == nil {
		t.Fatal("enqueue accepted an empty HTML body")
	}
}

// fakeRenderExec installs a runRenderExecCommand fake that records every
// invocation and fabricates the toolchain outputs: the "chromium" call writes
// the layered PDF, the "pdftoppm" call writes baseline page JPEGs.
func fakeRenderExec(t *testing.T, layeredPDF []byte, pageCount int) *[]renderExecInvocation {
	t.Helper()
	invocations := &[]renderExecInvocation{}
	var mu sync.Mutex
	original := runRenderExecCommand
	t.Cleanup(func() { runRenderExecCommand = original })
	runRenderExecCommand = func(_ context.Context, bin string, args []string, dir string) (string, string, error) {
		mu.Lock()
		defer mu.Unlock()
		*invocations = append(*invocations, renderExecInvocation{Bin: bin, Args: append([]string{}, args...), Dir: dir})
		for _, arg := range args {
			if strings.HasPrefix(arg, "--print-to-pdf=") {
				if err := os.WriteFile(strings.TrimPrefix(arg, "--print-to-pdf="), layeredPDF, 0o644); err != nil {
					t.Fatalf("fake chromium write: %v", err)
				}
				return "", "", nil
			}
		}
		if len(args) > 0 && args[0] == "-jpeg" {
			prefix := args[len(args)-1]
			for page := 1; page <= pageCount; page++ {
				jpegBytes := encodeBaselineJPEG(t, 16, 9)
				if err := os.WriteFile(prefix+"-"+string(rune('0'+page))+".jpg", jpegBytes, 0o644); err != nil {
					t.Fatalf("fake pdftoppm write: %v", err)
				}
			}
			return "", "", nil
		}
		t.Fatalf("unexpected render command %s %v", bin, args)
		return "", "", nil
	}
	return invocations
}

func TestExecuteRenderExportPDFDeckPinsFlattenLawCommands(t *testing.T) {
	binDir := t.TempDir()
	chromium := writeStubRenderBinary(t, binDir, "chromium-stub")
	pdftoppm := writeStubRenderBinary(t, binDir, "pdftoppm-stub")
	layered := []byte("%PDF-1.7 layered chromium print — must never ship for decks\n%%EOF")
	invocations := fakeRenderExec(t, layered, 2)

	cfg := renderExecConfig{ChromiumBin: chromium, PdftoppmBin: pdftoppm, Timeout: defaultRenderExecTimeout, MaxPDFBytes: defaultRenderMaxPDFBytes}
	resultsDir := filepath.Join(t.TempDir(), "render-job-deck-out")
	result, err := executeRenderExportPDF(context.Background(), cfg, renderRunnerJob{
		ID:         "render-job-deck",
		Type:       renderJobTypeExportPDF,
		ArtifactID: "artifact-deck",
		Kind:       renderJobKindDeck,
		HTML:       "<!doctype html><html><body>deck</body></html>",
	}, resultsDir)
	if err != nil {
		t.Fatalf("executeRenderExportPDF: %v", err)
	}

	if len(*invocations) != 2 {
		t.Fatalf("invocations=%d, want chromium then pdftoppm", len(*invocations))
	}
	print := (*invocations)[0]
	if print.Bin != chromium {
		t.Fatalf("print bin=%q, want the chromium stub", print.Bin)
	}
	// The flatten law's print arguments, pinned exactly and in order — the
	// blackholed proxy included: untrusted HTML prints with zero egress.
	layeredPath := strings.TrimPrefix(print.Args[6], "--print-to-pdf=")
	htmlPath := strings.TrimPrefix(print.Args[7], "file://")
	wantPrintArgs := []string{
		"--headless=new",
		"--no-sandbox",
		"--disable-gpu",
		"--proxy-server=127.0.0.1:9",
		"--no-pdf-header-footer",
		"--virtual-time-budget=15000",
		"--print-to-pdf=" + layeredPath,
		"file://" + htmlPath,
	}
	if !reflect.DeepEqual(print.Args, wantPrintArgs) {
		t.Fatalf("chromium args=%v, want %v", print.Args, wantPrintArgs)
	}
	if filepath.Base(layeredPath) != "layered.pdf" || filepath.Base(htmlPath) != "artifact.html" {
		t.Fatalf("layered=%q html=%q, want layered.pdf and artifact.html", layeredPath, htmlPath)
	}
	rasterize := (*invocations)[1]
	if rasterize.Bin != pdftoppm {
		t.Fatalf("rasterize bin=%q, want the pdftoppm stub", rasterize.Bin)
	}
	wantRasterArgs := []string{"-jpeg", "-r", "144", layeredPath, rasterize.Args[len(rasterize.Args)-1]}
	if !reflect.DeepEqual(rasterize.Args, wantRasterArgs) {
		t.Fatalf("pdftoppm args=%v, want %v", rasterize.Args, wantRasterArgs)
	}

	// Deck deliverable: the flattened raster PDF, never the layered print.
	if !result.Flattened {
		t.Fatal("Flattened=false, want flattened deck export")
	}
	if bytes.Equal(result.PDFBytes, layered) {
		t.Fatal("deck export shipped the layered print — the flatten law is non-negotiable")
	}
	if !bytes.HasPrefix(result.PDFBytes, []byte("%PDF-1.4")) || strings.Count(string(result.PDFBytes), "/Filter /DCTDecode") != 2 {
		t.Fatalf("deck export is not the 2-page DCTDecode raster PDF")
	}
	if result.PageCount != 2 || len(result.PageJPEGPaths) != 2 {
		t.Fatalf("pages=%d refs=%d, want 2/2", result.PageCount, len(result.PageJPEGPaths))
	}
	// Outputs persist to the shared-volume results dir for the OS side.
	persisted, err := os.ReadFile(result.PDFPath)
	if err != nil || !bytes.Equal(persisted, result.PDFBytes) {
		t.Fatalf("persisted PDF mismatch (err=%v)", err)
	}
	for _, ref := range result.PageJPEGPaths {
		if filepath.Dir(ref) != resultsDir {
			t.Fatalf("page ref %q outside results dir %q", ref, resultsDir)
		}
		if _, err := os.Stat(ref); err != nil {
			t.Fatalf("page ref %q missing: %v", ref, err)
		}
	}
}

// The printed page is untrusted HTML with --no-sandbox, so the injected meta
// CSP is the render route's zero-network policy: default-src 'none', inline
// style/script only, data: media — and it must land inside the document head
// wherever the markup allows.
func TestInjectRenderPrintCSPPinsZeroNetworkPolicy(t *testing.T) {
	meta := `<meta http-equiv="Content-Security-Policy" content="` + renderPrintCSP + `">`
	for _, want := range []string{"default-src 'none'", "script-src 'unsafe-inline'", "img-src data:"} {
		if !strings.Contains(renderPrintCSP, want) {
			t.Fatalf("renderPrintCSP missing %q", want)
		}
	}
	if strings.Contains(renderPrintCSP, "sandbox") {
		t.Fatal("meta CSP must not carry the sandbox directive (ignored in <meta>, per spec)")
	}

	withHead := injectRenderPrintCSP(`<!doctype html><html><head><title>deck</title></head><body><header>h</header></body></html>`)
	if !strings.Contains(withHead, "<head>"+meta+"<title>") {
		t.Fatalf("meta not injected after <head>: %s", withHead)
	}
	// <header> must never be mistaken for <head>: with no real head the meta
	// rides right after <html> instead.
	headerOnly := injectRenderPrintCSP(`<html><body><header>nav</header></body></html>`)
	if !strings.Contains(headerOnly, "<html>"+meta+"<body>") {
		t.Fatalf("meta not injected after <html>: %s", headerOnly)
	}
	fragment := injectRenderPrintCSP(`<p>bare fragment</p>`)
	if !strings.HasPrefix(fragment, meta) {
		t.Fatalf("meta not prepended to a bare fragment: %s", fragment)
	}
}

// injectRenderDeckPrintCSS is the pagination safety net: a .pg deck with no
// @page rule (the shape that flattened to one page) gets the chassis print
// block injected into its head; a deck that already declares @page is left
// untouched, and a second pass is a no-op.
func TestInjectRenderDeckPrintCSS(t *testing.T) {
	missing := `<!doctype html><html><head></head><body><div id="stage"><section class="pg on">one</section><section class="pg">two</section></div></body></html>`
	got := injectRenderDeckPrintCSS(missing)
	for _, need := range []string{"@page", "@media print", "break-after:page", ".pg"} {
		if !strings.Contains(got, need) {
			t.Fatalf("expected print CSS %q injected, got:\n%s", need, got)
		}
	}
	if n := strings.Count(got, "@page"); n != 1 {
		t.Fatalf("expected exactly one @page after inject, got %d", n)
	}
	// The style lands inside the head (right after <head>), not after </body>.
	if !strings.Contains(got, "<head><style>") {
		t.Fatalf("print CSS not injected into the document head:\n%s", got)
	}

	// A deck that already declares @page keeps its own geometry — untouched.
	authored := `<!doctype html><html><head><style>@page{size:A4}</style></head><body>x</body></html>`
	if out := injectRenderDeckPrintCSS(authored); out != authored {
		t.Fatalf("deck with @page must be left untouched, got:\n%s", out)
	}
	// Idempotent: injecting a second time adds no second block.
	if twice := injectRenderDeckPrintCSS(got); twice != got {
		t.Fatalf("second injection pass must be a no-op, got:\n%s", twice)
	}
}

// The deck print path must reach chromium carrying pagination: a .pg deck that
// dropped its print CSS still gets the chassis block injected before the print,
// so every slide paginates instead of collapsing to the single on-screen frame.
// Paper (text-native, no slide model) is never given a slide print stylesheet.
func TestExecuteRenderExportPDFInjectsDeckPaginationBeforePrint(t *testing.T) {
	binDir := t.TempDir()
	chromium := writeStubRenderBinary(t, binDir, "chromium-stub")
	pdftoppm := writeStubRenderBinary(t, binDir, "pdftoppm-stub")

	// Custom fake: capture the exact HTML handed to chromium via file://.
	var printedHTML string
	original := runRenderExecCommand
	t.Cleanup(func() { runRenderExecCommand = original })
	runRenderExecCommand = func(_ context.Context, _ string, args []string, _ string) (string, string, error) {
		for _, arg := range args {
			if strings.HasPrefix(arg, "--print-to-pdf=") {
				htmlPath := strings.TrimPrefix(args[len(args)-1], "file://")
				data, err := os.ReadFile(htmlPath)
				if err != nil {
					t.Fatalf("read printed html: %v", err)
				}
				printedHTML = string(data)
				if err := os.WriteFile(strings.TrimPrefix(arg, "--print-to-pdf="), []byte("%PDF-1.7 layered\n%%EOF"), 0o644); err != nil {
					t.Fatalf("fake chromium write: %v", err)
				}
				return "", "", nil
			}
		}
		if len(args) > 0 && args[0] == "-jpeg" {
			prefix := args[len(args)-1]
			for page := 1; page <= 3; page++ {
				if err := os.WriteFile(prefix+"-"+string(rune('0'+page))+".jpg", encodeBaselineJPEG(t, 16, 9), 0o644); err != nil {
					t.Fatalf("fake pdftoppm write: %v", err)
				}
			}
			return "", "", nil
		}
		t.Fatalf("unexpected render command %v", args)
		return "", "", nil
	}

	cfg := renderExecConfig{ChromiumBin: chromium, PdftoppmBin: pdftoppm, Timeout: defaultRenderExecTimeout, MaxPDFBytes: defaultRenderMaxPDFBytes}
	deck := `<!doctype html><html><head><title>d</title></head><body><div id="stage"><section class="pg on">one</section><section class="pg">two</section></div></body></html>`
	if _, err := executeRenderExportPDF(context.Background(), cfg, renderRunnerJob{
		ID: "render-job-deck-fallback", Type: renderJobTypeExportPDF, ArtifactID: "artifact-deck", Kind: renderJobKindDeck, HTML: deck,
	}, filepath.Join(t.TempDir(), "out")); err != nil {
		t.Fatalf("executeRenderExportPDF deck: %v", err)
	}
	for _, need := range []string{"@page", "break-after:page", "Content-Security-Policy"} {
		if !strings.Contains(printedHTML, need) {
			t.Fatalf("printed deck HTML missing %q — pagination/CSP not injected:\n%s", need, printedHTML)
		}
	}

	// Paper: no slide print stylesheet is injected (only the CSP meta).
	printedHTML = ""
	paper := `<!doctype html><html><head><title>talk</title></head><body><p>text-native</p></body></html>`
	if _, err := executeRenderExportPDF(context.Background(), cfg, renderRunnerJob{
		ID: "render-job-paper", Type: renderJobTypeExportPDF, ArtifactID: "artifact-paper", Kind: renderJobKindPaper, HTML: paper,
	}, filepath.Join(t.TempDir(), "out")); err != nil {
		t.Fatalf("executeRenderExportPDF paper: %v", err)
	}
	if strings.Contains(printedHTML, "@page") || strings.Contains(printedHTML, "break-after:page") {
		t.Fatalf("paper print was given a slide pagination stylesheet:\n%s", printedHTML)
	}
	if !strings.Contains(printedHTML, "Content-Security-Policy") {
		t.Fatal("paper print missing the CSP meta")
	}
}

// The disclosure guard: a deck that flattens to exactly one page did not
// paginate and is flagged; a multi-page deck is not, and single-page PAPER is
// not a deck-pagination defect.
func TestExecuteRenderExportPDFDisclosesSinglePageDeck(t *testing.T) {
	binDir := t.TempDir()
	chromium := writeStubRenderBinary(t, binDir, "chromium-stub")
	pdftoppm := writeStubRenderBinary(t, binDir, "pdftoppm-stub")
	cfg := renderExecConfig{ChromiumBin: chromium, PdftoppmBin: pdftoppm, Timeout: defaultRenderExecTimeout, MaxPDFBytes: defaultRenderMaxPDFBytes}
	deckHTML := `<!doctype html><html><body><div id="stage"><section class="pg on">only</section></div></body></html>`

	fakeRenderExec(t, []byte("%PDF-1.7 layered\n%%EOF"), 1)
	one, err := executeRenderExportPDF(context.Background(), cfg, renderRunnerJob{
		ID: "render-job-1page", Type: renderJobTypeExportPDF, ArtifactID: "a", Kind: renderJobKindDeck, HTML: deckHTML,
	}, filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatalf("executeRenderExportPDF one-page: %v", err)
	}
	if !one.DeckSinglePage {
		t.Fatal("DeckSinglePage=false, want a disclosed single-page deck")
	}

	fakeRenderExec(t, []byte("%PDF-1.7 layered\n%%EOF"), 4)
	multi, err := executeRenderExportPDF(context.Background(), cfg, renderRunnerJob{
		ID: "render-job-4page", Type: renderJobTypeExportPDF, ArtifactID: "a", Kind: renderJobKindDeck, HTML: deckHTML,
	}, filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatalf("executeRenderExportPDF multi-page: %v", err)
	}
	if multi.DeckSinglePage {
		t.Fatal("multi-page deck wrongly flagged single-page")
	}

	fakeRenderExec(t, []byte("%PDF-1.7 paper\n%%EOF"), 1)
	paper, err := executeRenderExportPDF(context.Background(), cfg, renderRunnerJob{
		ID: "render-job-paper1", Type: renderJobTypeExportPDF, ArtifactID: "a", Kind: renderJobKindPaper, HTML: `<!doctype html><html><body>x</body></html>`,
	}, filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatalf("executeRenderExportPDF paper: %v", err)
	}
	if paper.DeckSinglePage {
		t.Fatal("single-page paper wrongly flagged as a deck-pagination defect")
	}
}

func TestExecuteRenderExportPDFPaperShipsDirectPrintWithoutFlatten(t *testing.T) {
	binDir := t.TempDir()
	chromium := writeStubRenderBinary(t, binDir, "chromium-stub")
	pdftoppm := writeStubRenderBinary(t, binDir, "pdftoppm-stub")
	layered := []byte("%PDF-1.7 text-native paper kit print\n%%EOF")
	invocations := fakeRenderExec(t, layered, 3)

	cfg := renderExecConfig{ChromiumBin: chromium, PdftoppmBin: pdftoppm, Timeout: defaultRenderExecTimeout, MaxPDFBytes: defaultRenderMaxPDFBytes}
	result, err := executeRenderExportPDF(context.Background(), cfg, renderRunnerJob{
		ID:         "render-job-paper",
		Type:       renderJobTypeExportPDF,
		ArtifactID: "artifact-paper",
		Kind:       renderJobKindPaper,
		HTML:       "<!doctype html><html><body>The Talk</body></html>",
	}, filepath.Join(t.TempDir(), "render-job-paper-out"))
	if err != nil {
		t.Fatalf("executeRenderExportPDF: %v", err)
	}

	// Paper is text-native: chromium's direct print IS the deliverable.
	if result.Flattened {
		t.Fatal("Flattened=true, want direct print for the paper kit")
	}
	if !bytes.Equal(result.PDFBytes, layered) {
		t.Fatal("paper export is not the direct chromium print")
	}
	// The page JPEGs still ship — Wave 5's vision juries need them.
	if len(*invocations) != 2 || result.PageCount != 3 || len(result.PageJPEGPaths) != 3 {
		t.Fatalf("invocations=%d pages=%d refs=%d, want rasterized paper pages too", len(*invocations), result.PageCount, len(result.PageJPEGPaths))
	}
}

func TestExecuteRenderExportPDFFailsClearlyWhenToolchainMissing(t *testing.T) {
	cfg := renderExecConfig{
		ChromiumBin: "/nonexistent/bonfire-chromium",
		PdftoppmBin: "/nonexistent/bonfire-pdftoppm",
		Timeout:     defaultRenderExecTimeout,
	}
	_, err := executeRenderExportPDF(context.Background(), cfg, renderRunnerJob{
		ID:   "render-job-missing",
		Type: renderJobTypeExportPDF,
		Kind: renderJobKindDeck,
		HTML: "<html></html>",
	}, t.TempDir())
	if err == nil {
		t.Fatal("executeRenderExportPDF succeeded without chromium")
	}
	if !strings.Contains(err.Error(), "render sidecar not available") || !strings.Contains(err.Error(), "RENDER_CHROMIUM_BIN") {
		t.Fatalf("error=%q, want the operator message naming RENDER_CHROMIUM_BIN", err)
	}
}

func TestExecuteRenderExportPDFRejectsUnknownJobType(t *testing.T) {
	_, err := executeRenderExportPDF(context.Background(), renderExecConfigFromEnv(), renderRunnerJob{
		ID:   "render-job-odd",
		Type: "export_gif",
		HTML: "<html></html>",
	}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "export_gif") {
		t.Fatalf("err=%v, want unknown-job-type rejection", err)
	}
}

func TestProcessRenderRunnerJobPostsAuthorizedCallbacks(t *testing.T) {
	binDir := t.TempDir()
	chromium := writeStubRenderBinary(t, binDir, "chromium-stub")
	pdftoppm := writeStubRenderBinary(t, binDir, "pdftoppm-stub")
	fakeRenderExec(t, []byte("%PDF-1.7 layered\n%%EOF"), 1)
	t.Setenv("RENDER_CHROMIUM_BIN", chromium)
	t.Setenv("RENDER_PDFTOPPM_BIN", pdftoppm)
	t.Setenv("BONFIRE_RUNNER_TOKEN", "render-secret")

	var mu sync.Mutex
	var payloads []renderRunnerCallbackPayload
	var authorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload renderRunnerCallbackPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode callback: %v", err)
		}
		mu.Lock()
		payloads = append(payloads, payload)
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	t.Setenv("BONFIRE_RENDER_CALLBACK_URL", server.URL)

	queueDir := t.TempDir()
	t.Setenv("BONFIRE_RENDER_QUEUE_PATH", queueDir)
	store := newRenderRunnerJobStore(queueDir)
	if _, err := store.enqueue(renderRunnerJob{
		ID:         "render-job-cb",
		ArtifactID: "artifact-cb",
		Kind:       renderJobKindDeck,
		HTML:       "<!doctype html><html><body>deck</body></html>",
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := store.claimNext("test-runner")
	if err != nil || job == nil {
		t.Fatalf("claimNext job=%v err=%v", job, err)
	}

	processRenderRunnerJob(context.Background(), store, *job)

	// Snapshot under the lock, then release — the failure-path re-run below
	// posts callbacks whose handler needs this same mutex.
	snapshot := func() ([]renderRunnerCallbackPayload, []string) {
		mu.Lock()
		defer mu.Unlock()
		return append([]renderRunnerCallbackPayload{}, payloads...), append([]string{}, authorizations...)
	}
	gotPayloads, gotAuthorizations := snapshot()
	if len(gotPayloads) != 2 {
		t.Fatalf("callbacks=%d, want running then complete", len(gotPayloads))
	}
	for _, authorization := range gotAuthorizations {
		if authorization != "Bearer render-secret" {
			t.Fatalf("Authorization=%q, want Bearer BONFIRE_RUNNER_TOKEN", authorization)
		}
	}
	if gotPayloads[0].Status != renderJobStatusRunning || gotPayloads[1].Status != renderJobStatusComplete {
		t.Fatalf("statuses=%q/%q, want running/complete", gotPayloads[0].Status, gotPayloads[1].Status)
	}
	final := gotPayloads[1]
	if final.ArtifactID != "artifact-cb" || final.JobID != "render-job-cb" || !final.Flattened || final.PageCount != 1 {
		t.Fatalf("final payload=%+v, want flattened single-page deck result", final)
	}
	pdf, err := base64.StdEncoding.DecodeString(final.PDFBase64)
	if err != nil || !bytes.HasPrefix(pdf, []byte("%PDF-1.4")) {
		t.Fatalf("PDFBase64 does not decode to the flattened PDF (err=%v)", err)
	}
	if final.PDFPath == "" || len(final.PageJPEGPaths) != 1 {
		t.Fatalf("final payload=%+v, want shared-volume refs", final)
	}
	stored, err := store.read(job.ID + ".json")
	if err != nil || stored.Status != renderJobStatusComplete {
		t.Fatalf("stored job=%+v err=%v, want persisted complete status", stored, err)
	}

	// Failure path: a job with no toolchain reports the operator message.
	t.Setenv("RENDER_CHROMIUM_BIN", "/nonexistent/bonfire-chromium")
	if _, err := store.enqueue(renderRunnerJob{
		ID:         "render-job-fail",
		ArtifactID: "artifact-fail",
		Kind:       renderJobKindDeck,
		HTML:       "<html></html>",
	}); err != nil {
		t.Fatalf("enqueue failing job: %v", err)
	}
	failing, err := store.claimNext("test-runner")
	if err != nil || failing == nil {
		t.Fatalf("claimNext failing job=%v err=%v", failing, err)
	}
	processRenderRunnerJob(context.Background(), store, *failing)
	gotPayloads, _ = snapshot()
	if len(gotPayloads) != 4 {
		t.Fatalf("callbacks=%d, want two more for the failing job", len(gotPayloads))
	}
	if gotPayloads[3].Status != renderJobStatusFailed || !strings.Contains(gotPayloads[3].Error, "render sidecar not available") {
		t.Fatalf("failure payload=%+v, want failed status with the operator message", gotPayloads[3])
	}
}

func TestSendRenderRunnerCallbackRequiresTokenKeyless(t *testing.T) {
	t.Setenv("BONFIRE_RUNNER_TOKEN", "")
	err := sendRenderRunnerCallback(context.Background(), renderRunnerCallbackPayload{JobID: "render-job-x"})
	if err == nil || !strings.Contains(err.Error(), "BONFIRE_RUNNER_TOKEN") {
		t.Fatalf("err=%v, want a clear BONFIRE_RUNNER_TOKEN requirement", err)
	}
}

func TestReadinessRenderRunnerSnapshotSurfacesAbsence(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("BONFIRE_RENDER_QUEUE_PATH", filepath.Join(dataDir, "render-jobs"))
	t.Setenv("BONFIRE_RENDER_HEARTBEAT_PATH", filepath.Join(dataDir, "render-runner-heartbeat.json"))
	t.Setenv("BONFIRE_RUNNER_TOKEN", "")

	snapshot := readinessRenderRunnerSnapshot()
	if snapshot["heartbeatOK"] != false || snapshot["heartbeatError"] != "missing" {
		t.Fatalf("snapshot=%v, want missing-heartbeat absence signal", snapshot)
	}
	if snapshot["callbackSecured"] != false {
		t.Fatalf("snapshot=%v, want callbackSecured=false keyless", snapshot)
	}

	// After a heartbeat the snapshot reports fresh + toolchain availability.
	if err := writeRenderRunnerHeartbeat("test-runner"); err != nil {
		t.Fatalf("writeRenderRunnerHeartbeat: %v", err)
	}
	snapshot = readinessRenderRunnerSnapshot()
	if snapshot["heartbeatOK"] != true || snapshot["runnerId"] != "test-runner" {
		t.Fatalf("snapshot=%v, want fresh heartbeat", snapshot)
	}
	if _, ok := snapshot["chromiumOK"]; !ok {
		t.Fatalf("snapshot=%v, want chromiumOK toolchain signal", snapshot)
	}
}

// The live Ember run's block, root-caused: a minimal HTML5 deck legitimately
// opens with <!doctype html> followed directly by <style> — no <html>/<head>
// wrapper tags. insertIntoDocumentHead's fallback PREPENDED the snippet before
// the doctype, un-documenting the file: ship_compile's html_deck validation
// ("must start with <!doctype html") then rejected the compiled deck and the
// whole ship blocked. The fallback must insert AFTER the doctype declaration.
func TestInsertIntoDocumentHeadAfterBareDoctype(t *testing.T) {
	deck := "<!doctype html>\n<style>.pg{width:1920px}</style>\n<section class=\"pg fig-2\">slide</section>"
	augmented := insertIntoDocumentHead(deck, "<style id=\"bonfire-imagery\">.fig-2 .ph{}</style>")
	if !strings.HasPrefix(strings.ToLower(augmented), "<!doctype html>") {
		t.Fatalf("augmented deck no longer starts with the doctype:\n%s", augmented[:80])
	}
	if !strings.Contains(augmented, "bonfire-imagery") {
		t.Fatal("snippet was not inserted")
	}
	doctypeEnd := strings.Index(augmented, ">")
	if snippetAt := strings.Index(augmented, "<style id=\"bonfire-imagery\""); snippetAt < doctypeEnd {
		t.Fatalf("snippet landed before the doctype closed (at %d, doctype ends %d)", snippetAt, doctypeEnd)
	}
	// The uppercase variant is equally legal.
	upper := insertIntoDocumentHead("<!DOCTYPE HTML><section class=\"pg\">s</section>", "<style id=\"x\"></style>")
	if !strings.HasPrefix(upper, "<!DOCTYPE HTML>") {
		t.Fatalf("uppercase doctype deck was un-documented:\n%s", upper[:60])
	}
	// A fragment with no doctype at all keeps the plain prepend.
	frag := insertIntoDocumentHead("<section>s</section>", "<style id=\"y\"></style>")
	if !strings.HasPrefix(frag, "<style id=\"y\">") {
		t.Fatalf("fragment fallback changed: %s", frag[:40])
	}
}
