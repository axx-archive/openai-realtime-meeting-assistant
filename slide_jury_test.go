package main

// slide_jury_test.go — the vision slide jury (Wave 5 item 21). Pinned here:
// the callback-side page-image persistence (path-validated, {kind: page_image}
// assets), the page budget with its disclosed truncation, and the jury run
// itself — 3-seat fan-out + synthesis where EVERY call carries ALL page image
// blocks, and the merged scoreboard files as a slide_jury_v1 artifact. The
// studio-stage wiring (disclosed skips, findings revision notes) is proven
// through the real pipeline in packaging_studio_test.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// seedSlideJuryDeck files a deck-shaped artifact and attaches page-image
// assets for the given JPEG payloads, mirroring what the render callback does.
func seedSlideJuryDeck(t *testing.T, app *kanbanBoardApp, pages ...[]byte) meetingMemoryEntry {
	t.Helper()
	deck, appended, err := app.createOSArtifactWithMetadata("workflow", "Aurora — presenter deck", "<!doctype html><html><body>deck</body></html>", "tester", map[string]string{
		"artifactContract": packagingStudioDeckContract,
		"type":             artifactTypeHTMLDeck,
		"packageId":        "pkg-aurora",
	})
	if err != nil || !appended {
		t.Fatalf("seed deck artifact: appended=%v err=%v", appended, err)
	}
	for index, page := range pages {
		ref, err := putBlob(page, "image/jpeg")
		if err != nil {
			t.Fatalf("store page image %d: %v", index+1, err)
		}
		if _, err := app.appendArtifactAsset(deck.ID, artifactAsset{
			Ref:  ref,
			Mime: "image/jpeg",
			Name: fmt.Sprintf("page-%02d.jpg", index+1),
			Kind: "page_image",
		}); err != nil {
			t.Fatalf("attach page image %d: %v", index+1, err)
		}
	}
	fresh, ok := app.osArtifactByID(deck.ID)
	if !ok {
		t.Fatal("seeded deck artifact disappeared")
	}
	return fresh
}

func TestArtifactPageImagesExcludeGeneratedDeckImagery(t *testing.T) {
	assets, _ := json.Marshal([]artifactAsset{
		{Ref: strings.Repeat("a", 64), Mime: "image/png", Name: "fig-01.png", Kind: "image"},
		{Ref: strings.Repeat("b", 64), Mime: "image/jpeg", Name: "page-01.jpg", Kind: "page_image"},
		{Ref: strings.Repeat("c", 64), Mime: "image/jpeg", Name: "page-02.jpg", Kind: "image"}, // legacy callback
	})
	entry := meetingMemoryEntry{Metadata: map[string]string{artifactAssetsMetadataKey: string(assets)}}
	pages := artifactPageImageAssets(entry)
	if len(pages) != 2 || pages[0].Name != "page-01.jpg" || pages[1].Name != "page-02.jpg" {
		t.Fatalf("page assets=%+v, want only rendered pages", pages)
	}
}

func TestRenderedDeckSlideCountIgnoresNestedSemanticSections(t *testing.T) {
	source := `<!doctype html><html><body><div id="stage">
		<section class="pg on"><section><h2>Evidence</h2></section></section>
		<section class="pg"><div><section>Notes</section></div></section>
	</div></body></html>`
	if got := renderedDeckSlideCount(source); got != 2 {
		t.Fatalf("renderedDeckSlideCount=%d, want 2 authored slides", got)
	}
}

// The callback-side seam: page JPEGs on the shared volume persist to the blob
// store and attach as {kind: page_image} assets — and a path outside the render
// queue (the sidecar is the least-trusted box) is skipped, never read.
func TestPersistRenderPageImageAssetsStoresJuryPages(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	deck := seedSlideJuryDeck(t, app)

	resultsDir := renderJobResultsDir(renderRunnerQueuePath(), "render-job-1")
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		t.Fatalf("create results dir: %v", err)
	}
	pageOne := []byte("fake-jpeg-page-one")
	pageTwo := []byte("fake-jpeg-page-two")
	pathOne := filepath.Join(resultsDir, "page-1.jpg")
	pathTwo := filepath.Join(resultsDir, "page-2.jpg")
	if err := os.WriteFile(pathOne, pageOne, 0o644); err != nil {
		t.Fatalf("write page 1: %v", err)
	}
	if err := os.WriteFile(pathTwo, pageTwo, 0o644); err != nil {
		t.Fatalf("write page 2: %v", err)
	}
	// A hostile path outside the queue: must be skipped without a read.
	outside := filepath.Join(t.TempDir(), "etc-passwd.jpg")
	if err := os.WriteFile(outside, []byte("never-read"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	persisted := persistRenderPageImageAssets(app, deck.ID, renderRunnerCallbackPayload{
		PageJPEGPaths: []string{pathOne, outside, pathTwo},
	})
	if persisted != 2 {
		t.Fatalf("persisted %d page images, want 2 (the outside path is skipped)", persisted)
	}

	fresh := mustArtifact(t, app, deck.ID)
	assets := artifactPageImageAssets(fresh)
	if len(assets) != 2 {
		t.Fatalf("deck carries %d image assets, want 2: %+v", len(assets), assets)
	}
	for index, want := range [][]byte{pageOne, pageTwo} {
		asset := assets[index]
		if asset.Kind != "page_image" || asset.Mime != "image/jpeg" {
			t.Fatalf("asset %d = %+v, want kind=page_image mime=image/jpeg", index, asset)
		}
		data, _, err := getBlob(asset.Ref)
		if err != nil || !bytes.Equal(data, want) {
			t.Fatalf("asset %d did not round-trip through the blob store: err=%v", index, err)
		}
	}
	// The hostile payload never entered the blob store.
	for _, asset := range assets {
		data, _, _ := getBlob(asset.Ref)
		if bytes.Equal(data, []byte("never-read")) {
			t.Fatal("the outside-the-queue file was persisted")
		}
	}
}

// The symlink escape (Wave 5 fix): a compromised sidecar writing
// queue/page.jpg -> /opt/.env must never make the OS read the secret — the
// path must resolve INSIDE the queue and be a regular file, not just start
// with the queue prefix lexically.
func TestPersistRenderPageImageAssetsRejectsSymlinkEscape(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	deck := seedSlideJuryDeck(t, app)

	resultsDir := renderJobResultsDir(renderRunnerQueuePath(), "render-job-syml")
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		t.Fatalf("create results dir: %v", err)
	}
	// The secret lives OUTSIDE the queue; the symlink lives inside it.
	secret := filepath.Join(t.TempDir(), "env-secret")
	if err := os.WriteFile(secret, []byte("OPENAI_API_KEY=sk-secret"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	link := filepath.Join(resultsDir, "page-1.jpg")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	persisted := persistRenderPageImageAssets(app, deck.ID, renderRunnerCallbackPayload{
		PageJPEGPaths: []string{link},
	})
	if persisted != 0 {
		t.Fatalf("persisted %d page images through a symlink escape, want 0", persisted)
	}
	fresh := mustArtifact(t, app, deck.ID)
	if got := len(artifactPageImageAssets(fresh)); got != 0 {
		t.Fatalf("deck carries %d image assets after the escape attempt, want 0", got)
	}

	// The PDF fallback path shares the same trust check.
	if _, err := renderCallbackPDFBytes(renderRunnerCallbackPayload{PDFPath: link}); err == nil {
		t.Fatal("renderCallbackPDFBytes followed a symlink out of the render queue")
	}
}

// A fresh export REPLACES the previous export's page images (one metadata
// write): after edits + re-export the jury sees ONLY the latest pages, never
// stale ones interleaved with new ones.
func TestPersistRenderPageImageAssetsReplacesStalePages(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	deck := seedSlideJuryDeck(t, app)
	legacyRef, _ := putBlob([]byte("legacy-page"), "image/jpeg")
	imageRef, _ := putBlob([]byte("generated-figure"), "image/png")
	if _, err := app.appendArtifactAsset(deck.ID, artifactAsset{Ref: legacyRef, Mime: "image/jpeg", Name: "page-99.jpg", Kind: "image"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.appendArtifactAsset(deck.ID, artifactAsset{Ref: imageRef, Mime: "image/png", Name: "fig-01.png", Kind: "image"}); err != nil {
		t.Fatal(err)
	}

	resultsDir := renderJobResultsDir(renderRunnerQueuePath(), "render-job-re")
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		t.Fatalf("create results dir: %v", err)
	}
	write := func(name string, data []byte) string {
		t.Helper()
		path := filepath.Join(resultsDir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}
	first := []string{write("page-1.jpg", []byte("v1-page-one")), write("page-2.jpg", []byte("v1-page-two"))}
	if persisted := persistRenderPageImageAssets(app, deck.ID, renderRunnerCallbackPayload{PageJPEGPaths: first}); persisted != 2 {
		t.Fatalf("first export persisted %d, want 2", persisted)
	}
	// The deck was edited: the re-export has one changed page and one dropped.
	second := []string{write("page-1b.jpg", []byte("v2-page-one"))}
	if persisted := persistRenderPageImageAssets(app, deck.ID, renderRunnerCallbackPayload{PageJPEGPaths: second}); persisted != 1 {
		t.Fatalf("re-export persisted %d, want 1", persisted)
	}

	fresh := mustArtifact(t, app, deck.ID)
	assets := artifactPageImageAssets(fresh)
	if len(assets) != 1 {
		t.Fatalf("deck carries %d image assets after the re-export, want ONLY the fresh page: %+v", len(assets), assets)
	}
	freshAssets := artifactAssets(fresh)
	if len(freshAssets) != 2 || freshAssets[0].Name != "fig-01.png" {
		t.Fatalf("re-export did not remove legacy pages while preserving generated imagery: %+v", freshAssets)
	}
	data, _, err := getBlob(assets[0].Ref)
	if err != nil || !bytes.Equal(data, []byte("v2-page-one")) {
		t.Fatalf("surviving page is not the re-export's: err=%v data=%q", err, data)
	}
}

func TestPackagingStudioDeckRenderFailsClosedWithoutAuthoredSlides(t *testing.T) {
	assets, _ := json.Marshal([]artifactAsset{{Ref: strings.Repeat("a", 64), Mime: "image/jpeg", Name: "page-01.jpg", Kind: "page_image"}})
	deck := meetingMemoryEntry{Text: `<!doctype html><html><body><div>not a slide deck</div></body></html>`, Metadata: map[string]string{artifactAssetsMetadataKey: string(assets)}}
	if err := validatePackagingStudioDeckRender(deck); err == nil || !strings.Contains(err.Error(), "no recognized authored slide topology") {
		t.Fatalf("malformed deck gate error=%v", err)
	}
}

// The callback page ceiling: tens of thousands of distinct paths never turn
// into tens of thousands of reads/blob writes — the loop is bounded at
// renderPageImageAssetCap with the truncation logged.
func TestPersistRenderPageImageAssetsBoundsPageCount(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	deck := seedSlideJuryDeck(t, app)

	resultsDir := renderJobResultsDir(renderRunnerQueuePath(), "render-job-cap")
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		t.Fatalf("create results dir: %v", err)
	}
	paths := make([]string, 0, renderPageImageAssetCap+5)
	for i := 0; i < renderPageImageAssetCap+5; i++ {
		path := filepath.Join(resultsDir, fmt.Sprintf("page-%03d.jpg", i+1))
		if err := os.WriteFile(path, []byte(fmt.Sprintf("page-%03d", i+1)), 0o644); err != nil {
			t.Fatalf("write page %d: %v", i+1, err)
		}
		paths = append(paths, path)
	}
	if persisted := persistRenderPageImageAssets(app, deck.ID, renderRunnerCallbackPayload{PageJPEGPaths: paths}); persisted != renderPageImageAssetCap {
		t.Fatalf("persisted %d pages, want the %d-page cap", persisted, renderPageImageAssetCap)
	}
}

// The page budget: the jury never assembles a request past the wire-layer
// image caps, and every truncation is DISCLOSED, never silent.
func TestCapSlideJuryPages(t *testing.T) {
	pages := func(count int, size int) []slideJuryPage {
		out := make([]slideJuryPage, 0, count)
		for i := 0; i < count; i++ {
			out = append(out, slideJuryPage{Number: i + 1, Data: bytes.Repeat([]byte{0x1}, size)})
		}
		return out
	}

	kept, note := capSlideJuryPages(pages(5, 1024))
	if len(kept) != 5 || note != "" {
		t.Fatalf("under-budget pages were capped: %d kept, note=%q", len(kept), note)
	}

	kept, note = capSlideJuryPages(pages(anthropicMaxRequestImages+3, 1024))
	if len(kept) != anthropicMaxRequestImages {
		t.Fatalf("kept %d pages, want the %d-image cap", len(kept), anthropicMaxRequestImages)
	}
	if !strings.Contains(note, "DISCLOSED") || !strings.Contains(note, fmt.Sprintf("first %d of %d", anthropicMaxRequestImages, anthropicMaxRequestImages+3)) {
		t.Fatalf("truncation not disclosed: %q", note)
	}

	// The byte budget: a huge second page cuts the tail, disclosed.
	oversize := []slideJuryPage{
		{Number: 1, Data: bytes.Repeat([]byte{0x1}, 1024)},
		{Number: 2, Data: bytes.Repeat([]byte{0x1}, anthropicMaxRequestImageBytes)},
		{Number: 3, Data: bytes.Repeat([]byte{0x1}, 1024)},
	}
	kept, note = capSlideJuryPages(oversize)
	if len(kept) != 1 || !strings.Contains(note, "first 1 of 3") {
		t.Fatalf("byte budget kept %d pages (note=%q), want 1 disclosed", len(kept), note)
	}

	// A single page past the budget keeps nothing — the caller errors honestly.
	kept, _ = capSlideJuryPages([]slideJuryPage{{Number: 1, Data: bytes.Repeat([]byte{0x1}, anthropicMaxRequestImageBytes+1)}})
	if len(kept) != 0 {
		t.Fatalf("an oversize-only page list kept %d pages, want 0", len(kept))
	}
}

// A deck with no page-image assets is an error — no jury ever runs blind; the
// studio stage turns this case into a disclosed skip BEFORE calling in.
func TestRunSlideJuryNoPageImagesErrors(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	deck := seedSlideJuryDeck(t, app)
	if _, err := runSlideJury(context.Background(), app, "goal-1", deck); err == nil || !strings.Contains(err.Error(), "no page-image assets") {
		t.Fatalf("juryless deck returned err=%v, want the no-page-images error", err)
	}
}

// The jury run: three seats + one synthesis fan out through runGoalPanel, and
// EVERY call carries ALL page image blocks on the raw-content seam. The merged
// scoreboard files as a slide_jury_v1 artifact with the voices on the record.
func TestRunSlideJuryPanelFanOutWithImages(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	t.Setenv("ANTHROPIC_API_KEY", "installed-but-retired")
	deck := seedSlideJuryDeck(t, app, []byte("fake-jpeg-page-one"), []byte("fake-jpeg-page-two"))

	const seatJSON = `{"pages":[{"page":1,"score":6.5,"fix":"Cut the headline to seven words"},{"page":2,"score":9,"fix":"KEEP"}],"weakest_three":[1],"strongest_three":[2]}`
	const mergedScoreboard = "Merged scoreboard: page 1 avg 6.5 — cut the headline to seven words; page 2 KEEP."

	var mu sync.Mutex
	var requests []openAITextRequest
	original := createOpenAITextResponse
	t.Cleanup(func() { createOpenAITextResponse = original })
	createOpenAITextResponse = func(_ context.Context, apiKey string, request openAITextRequest) (string, error) {
		if apiKey != "test-key" {
			t.Errorf("apiKey=%q, want test-key", apiKey)
		}
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()
		text := seatJSON
		if strings.Contains(strings.ToLower(request.Instructions), "slide jury synthesizer") {
			text = mergedScoreboard
		}
		return text, nil
	}

	jury, err := runSlideJury(context.Background(), app, "goal-1", deck)
	if err != nil {
		t.Fatalf("runSlideJury: %v", err)
	}

	// 3 seats + 1 synthesis, every call image-bearing with ALL pages.
	if len(requests) != 4 {
		t.Fatalf("responder called %d times, want 4 (3 seats + synthesis)", len(requests))
	}
	seatSystems := 0
	for index, request := range requests {
		if request.Model != defaultOpenAIGoalReviewModel || request.ReasoningEffort != defaultOpenAIGoalReviewEffort || request.Seat != seatGoalReview {
			t.Fatalf("request %d route=%s/%s seat=%s, want slide review %s/%s seat=%s", index, request.Model, request.ReasoningEffort, request.Seat, defaultOpenAIGoalReviewModel, defaultOpenAIGoalReviewEffort, seatGoalReview)
		}
		images := 0
		for _, content := range request.Attachments {
			if content.Type == "input_image" {
				images++
			}
		}
		if images != 2 {
			t.Fatalf("request %d carries %d image blocks, want ALL 2 pages", index, images)
		}
		system := strings.ToLower(request.Instructions)
		if !strings.Contains(system, "slide jury") {
			t.Fatalf("request %d system is not jury-shaped: %q", index, request.Instructions)
		}
		if !strings.Contains(system, "slide jury synthesizer") {
			seatSystems++
			// Every seat carries the shared strict-JSON schema with the
			// executable-or-KEEP fix rule.
			if !strings.Contains(request.Instructions, "KEEP") || !strings.Contains(request.Instructions, "weakest_three") {
				t.Fatalf("seat request %d missing the jury schema: %q", index, request.Instructions)
			}
		}
	}
	if seatSystems != 3 {
		t.Fatalf("%d seat calls, want the 3-seat trio", seatSystems)
	}

	// The scoreboard artifact: contract, provenance, and the record's shape.
	if jury.Metadata["artifactContract"] != slideJuryContract {
		t.Fatalf("jury contract=%q, want %s", jury.Metadata["artifactContract"], slideJuryContract)
	}
	if jury.Metadata["source"] != slideJurySource || jury.Metadata["goalId"] != "goal-1" || jury.Metadata["deckArtifactId"] != deck.ID {
		t.Fatalf("jury provenance wrong: %v", jury.Metadata)
	}
	if jury.Metadata["packageId"] != "pkg-aurora" {
		t.Fatalf("jury packageId=%q, want the deck's package", jury.Metadata["packageId"])
	}
	if !strings.Contains(jury.Text, mergedScoreboard) {
		t.Fatalf("scoreboard missing the synthesis:\n%s", jury.Text)
	}
	if !strings.Contains(jury.Text, "## Jury voices") || strings.Count(jury.Text, seatJSON) != 3 {
		t.Fatalf("scoreboard missing the three seat voices:\n%s", jury.Text)
	}
}

// Keyless (no responder swap, no key): the jury errors — the studio stage
// discloses the skip before ever calling in, and nothing hits the network.
func TestRunSlideJuryKeylessErrors(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = ""
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "installed-but-retired")
	deck := seedSlideJuryDeck(t, app, []byte("fake-jpeg-page-one"))
	if _, err := runSlideJury(context.Background(), app, "goal-1", deck); err == nil {
		t.Fatal("keyless jury must error, not silently succeed")
	}
}

// The editorial backstop (Wave 5 d): the design-eye seat judges whether a
// page's imagery EARNS its place, and its verdicts stay ADVISORY revision
// notes — never an auto-revise.
func TestSlideJuryDesignEyeJudgesImageryAdvisory(t *testing.T) {
	var designEye goalPanelPersona
	for _, p := range slideJuryPersonas() {
		if p.Name == "design_eye" {
			designEye = p
		}
	}
	if designEye.Name == "" {
		t.Fatal("design_eye seat missing from the slide jury")
	}
	for _, need := range []string{"image", "EARNS", "ADVISORY"} {
		if !strings.Contains(designEye.System, need) {
			t.Errorf("design_eye prompt missing the imagery-earns-its-place cue %q:\n%s", need, designEye.System)
		}
	}
}
