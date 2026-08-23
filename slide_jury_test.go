package main

// slide_jury_test.go — the vision slide jury (Wave 5 item 21). Pinned here:
// the callback-side page-image persistence (path-validated, {kind: page_image}
// assets), the page budget with complete bounded batching, and the jury run
// itself — 3-seat fan-out where every seat sees every page across one or more
// requests, and the merged scoreboard files as a slide_jury_v1 artifact. The
// studio-stage wiring (disclosed skips, findings revision notes) is proven
// through the real pipeline in packaging_studio_test.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

// The batch boundary is the provider wire budget. Crossing either cap starts a
// new batch in runSlideJury; a single impossible page is rejected rather than
// silently disappearing from the coverage claim.
func TestSlideJuryBatchBoundary(t *testing.T) {
	if !slideJuryBatchCanAccept(0, 0, 1024) {
		t.Fatal("a small first page should fit an empty batch")
	}
	if slideJuryBatchCanAccept(anthropicMaxRequestImages, 0, 1024) {
		t.Fatalf("page %d should start a new batch at the %d-image cap", anthropicMaxRequestImages+1, anthropicMaxRequestImages)
	}
	if slideJuryBatchCanAccept(1, anthropicMaxRequestImageBytes-1024, 1025) {
		t.Fatal("a page that crosses the byte budget should start a new batch")
	}
	if !slideJuryBatchCanAccept(1, anthropicMaxRequestImageBytes-1024, 1024) {
		t.Fatal("a page that lands exactly on the byte budget should fit")
	}
	if slideJuryBatchCanAccept(0, 0, anthropicMaxRequestImageBytes+1) {
		t.Fatal("one page larger than an empty request must be rejected")
	}
}

func TestMergeSlideJuryBatchVoicesFailsClosedOnMissingGlobalPage(t *testing.T) {
	voice := func(persona string, page int) goalPanelVoice {
		return goalPanelVoice{Persona: persona, Text: fmt.Sprintf(`{"pages":[{"page":%d,"score":9,"fix":"KEEP","blockers":[]}]}`, page)}
	}
	first := slideJuryBatchRecord{Pages: []int{1}, Outcome: goalPanelOutcome{Voices: []goalPanelVoice{
		voice("headline_ear", 1), voice("design_eye", 1), voice("room_gut", 1),
	}}}
	// Two seats repeat page 1 in the second batch instead of reviewing global
	// page 2. Those seats must be excluded wholesale, not counted as complete.
	second := slideJuryBatchRecord{Pages: []int{2}, Outcome: goalPanelOutcome{Voices: []goalPanelVoice{
		voice("headline_ear", 2), voice("design_eye", 1), voice("room_gut", 1),
	}}}
	merged := mergeSlideJuryBatchVoices([]slideJuryBatchRecord{first, second}, 2)
	if len(merged) != 3 || merged[0].Err != nil || merged[1].Err == nil || merged[2].Err == nil {
		t.Fatalf("merged seats=%+v, want only headline_ear complete", merged)
	}
	if readiness := evaluateSlideJuryReadiness(merged, 2); readiness.Verdict != "needs_attention" || readiness.ParsedSeats != 1 {
		t.Fatalf("incomplete multi-batch readiness=%+v, want fail-closed needs_attention with one complete seat", readiness)
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

	const headlineSeatJSON = `{"pages":[{"page":1,"score":6.5,"fix":"Cut the headline to seven words","blockers":["weak_thesis"]},{"page":2,"score":9,"fix":"KEEP","blockers":[]}],"weakest_three":[1],"strongest_three":[2]}`
	const designSeatJSON = `{"pages":[{"page":1,"score":6.5,"fix":"Give the headline a clean focal hierarchy","blockers":["competing_hierarchies"]},{"page":2,"score":9,"fix":"KEEP","blockers":[]}],"weakest_three":[1],"strongest_three":[2]}`
	const roomSeatJSON = `{"pages": [{"page":1,"score":6.5,"fix":"Cut the headline to seven words","blockers":["weak_thesis"]},{"page":2,"score":9,"fix":"KEEP","blockers":[]}],"weakest_three":[1],"strongest_three":[2]}`
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
		instructions := strings.ToLower(request.Instructions)
		text := roomSeatJSON
		if strings.Contains(instructions, "slide jury synthesizer") {
			text = mergedScoreboard
		} else if strings.Contains(instructions, "headline ear") {
			text = headlineSeatJSON
		} else if strings.Contains(instructions, "design eye") {
			text = designSeatJSON
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
			if !strings.Contains(request.Instructions, "KEEP") || !strings.Contains(request.Instructions, "weakest_three") || !strings.Contains(request.Instructions, "low_contrast") || !strings.Contains(request.Instructions, "weak_cover_hierarchy") {
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
	if jury.Metadata["reviewVerdict"] != "needs_changes" || jury.Metadata["blockingPages"] != "1" || jury.Metadata["parsedSeats"] != "3" {
		t.Fatalf("jury readiness metadata wrong: %v", jury.Metadata)
	}
	var repairs []slideJuryRepair
	if err := json.Unmarshal([]byte(jury.Metadata["repairFixes"]), &repairs); err != nil || len(repairs) != 2 || repairs[0].Page != 1 || repairs[0].Owner != "write" || repairs[1].Owner != "layout_plan" || len(repairs[0].Fixes) != 1 || repairs[0].Fixes[0] != "Cut the headline to seven words" {
		t.Fatalf("jury did not persist the exact bounded seat fix: metadata=%v repairs=%+v err=%v", jury.Metadata, repairs, err)
	}
	if jury.Metadata["deckArtifactVersion"] != strconv.Itoa(artifactVersion(deck)) || jury.Metadata["deckContentDigest"] != artifactCapabilityDigest(deck) {
		t.Fatalf("jury did not bind the exact candidate revision: %v", jury.Metadata)
	}
	if !strings.Contains(jury.Text, mergedScoreboard) {
		t.Fatalf("scoreboard missing the synthesis:\n%s", jury.Text)
	}
	if !strings.Contains(jury.Text, "## Jury voices") || strings.Count(jury.Text, headlineSeatJSON) != 1 || strings.Count(jury.Text, designSeatJSON) != 1 || strings.Count(jury.Text, roomSeatJSON) != 1 {
		t.Fatalf("scoreboard missing the three seat voices:\n%s", jury.Text)
	}
}

// A deck past the per-request image ceiling is reviewed in complete bounded
// batches. Every persona sees every global page exactly once, each provider
// call stays under the cap, and only validated full-deck seat scorecards feed
// the final text-only synthesis and readiness verdict.
func TestRunSlideJuryBatchesEveryPageAndAggregatesExactSeats(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	t.Setenv("ANTHROPIC_API_KEY", "installed-but-retired")
	pageData := make([][]byte, anthropicMaxRequestImages+1)
	for index := range pageData {
		pageData[index] = []byte(fmt.Sprintf("fake-jpeg-page-%02d", index+1))
	}
	deck := seedSlideJuryDeck(t, app, pageData...)

	const fullSynthesis = "Full-deck scoreboard: all 13 rendered pages were reviewed."
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

		isSynthesis := strings.Contains(strings.ToLower(request.Instructions), "slide jury synthesizer")
		pageNumbers := make([]int, 0, len(request.Attachments)/2)
		for _, content := range request.Attachments {
			if content.Type != "input_text" {
				continue
			}
			var page, total int
			if _, err := fmt.Sscanf(content.Text, "Rendered page %d of %d:", &page, &total); err == nil {
				pageNumbers = append(pageNumbers, page)
			}
		}
		if isSynthesis {
			if len(pageNumbers) > 0 {
				return "", fmt.Errorf("throwaway image-batch synthesis must not run")
			}
			return fullSynthesis, nil
		}
		card := slideJurySeatScorecard{Pages: make([]slideJuryPageScore, 0, len(pageNumbers))}
		for _, page := range pageNumbers {
			card.Pages = append(card.Pages, slideJuryPageScore{Page: page, Score: 9, Fix: "KEEP", Blockers: []string{}})
		}
		finalizeSlideJurySeatScorecard(&card)
		payload, err := json.Marshal(card)
		if err != nil {
			return "", err
		}
		return string(payload), nil
	}

	jury, err := runSlideJury(context.Background(), app, "goal-batched", deck)
	if err != nil {
		t.Fatalf("runSlideJury batched: %v", err)
	}

	// Two image batches each fan out to the 3 seats, followed by the only
	// synthesis call over reassembled exact full-deck scorecards.
	if len(requests) != 7 {
		t.Fatalf("responder called %d times, want 7 (2*3 seats + one final synthesis)", len(requests))
	}
	seatCoverage := make(map[int]int, len(pageData))
	requestSizes := map[int]int{}
	for index, request := range requests {
		images := 0
		pages := []int{}
		for _, content := range request.Attachments {
			switch content.Type {
			case "input_image":
				images++
			case "input_text":
				var page, total int
				if _, err := fmt.Sscanf(content.Text, "Rendered page %d of %d:", &page, &total); err == nil {
					pages = append(pages, page)
				}
			}
		}
		if images > anthropicMaxRequestImages {
			t.Fatalf("request %d carries %d images, above cap %d", index, images, anthropicMaxRequestImages)
		}
		requestSizes[images]++
		if !strings.Contains(strings.ToLower(request.Instructions), "slide jury synthesizer") {
			for _, page := range pages {
				seatCoverage[page]++
			}
		}
	}
	if requestSizes[anthropicMaxRequestImages] != 3 || requestSizes[1] != 3 || requestSizes[0] != 1 {
		t.Fatalf("request image-size distribution=%v, want 3x%d, 3x1, 1x0", requestSizes, anthropicMaxRequestImages)
	}
	for page := 1; page <= len(pageData); page++ {
		if seatCoverage[page] != len(slideJuryPersonas()) {
			t.Fatalf("global page %d reached %d seats, want all %d", page, seatCoverage[page], len(slideJuryPersonas()))
		}
	}
	if jury.Metadata["juryBatchCount"] != "2" || jury.Metadata["juriedPageCount"] != "13" || jury.Metadata["pageCoverage"] != "13/13" {
		t.Fatalf("jury coverage metadata wrong: %v", jury.Metadata)
	}
	if jury.Metadata["reviewVerdict"] != "ready" || jury.Metadata["parsedSeats"] != "3" {
		t.Fatalf("batched readiness metadata wrong: %v", jury.Metadata)
	}
	if !strings.Contains(jury.Text, fullSynthesis) || !strings.Contains(jury.Text, `"page":13`) || strings.Contains(jury.Text, "Bounded batch syntheses") {
		t.Fatalf("batched jury record omitted full synthesis/page 13 or exposed throwaway batch synthesis:\n%s", jury.Text)
	}
}

func TestRunSlideJuryStartsNewBatchAtByteBudget(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	t.Setenv("ANTHROPIC_API_KEY", "installed-but-retired")
	pageSize := anthropicMaxRequestImageBytes/2 + 1
	pageOne := bytes.Repeat([]byte{0x11}, pageSize)
	pageTwo := bytes.Repeat([]byte{0x22}, pageSize)
	deck := seedSlideJuryDeck(t, app, pageOne, pageTwo)

	var mu sync.Mutex
	var imageCounts []int
	original := createOpenAITextResponse
	t.Cleanup(func() { createOpenAITextResponse = original })
	createOpenAITextResponse = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		images := 0
		pageNumbers := []int{}
		for _, content := range request.Attachments {
			switch content.Type {
			case "input_image":
				images++
			case "input_text":
				var page, total int
				if _, err := fmt.Sscanf(content.Text, "Rendered page %d of %d:", &page, &total); err == nil {
					pageNumbers = append(pageNumbers, page)
				}
			}
		}
		mu.Lock()
		imageCounts = append(imageCounts, images)
		mu.Unlock()
		if strings.Contains(strings.ToLower(request.Instructions), "slide jury synthesizer") {
			return "Byte-bounded synthesis.", nil
		}
		card := slideJurySeatScorecard{Pages: make([]slideJuryPageScore, 0, len(pageNumbers))}
		for _, page := range pageNumbers {
			card.Pages = append(card.Pages, slideJuryPageScore{Page: page, Score: 9, Fix: "KEEP", Blockers: []string{}})
		}
		finalizeSlideJurySeatScorecard(&card)
		payload, err := json.Marshal(card)
		return string(payload), err
	}

	jury, err := runSlideJury(context.Background(), app, "goal-byte-batched", deck)
	if err != nil {
		t.Fatalf("runSlideJury byte-batched: %v", err)
	}
	if len(imageCounts) != 7 {
		t.Fatalf("responder calls=%d, want 7 for two three-seat image batches plus full synthesis", len(imageCounts))
	}
	oneImage, zeroImage := 0, 0
	for _, count := range imageCounts {
		switch count {
		case 1:
			oneImage++
		case 0:
			zeroImage++
		default:
			t.Fatalf("byte-bounded request carried %d images, want at most one", count)
		}
	}
	if oneImage != 6 || zeroImage != 1 || jury.Metadata["juryBatchCount"] != "2" || jury.Metadata["pageCoverage"] != "2/2" {
		t.Fatalf("byte batching calls=%v metadata=%v, want 6 one-image calls + one text synthesis and 2/2 coverage", imageCounts, jury.Metadata)
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

func TestEvaluateSlideJuryReadinessRequiresBothSpecialistsAndHonorsHardVeto(t *testing.T) {
	voice := func(persona string, pageOne, pageTwo float64) goalPanelVoice {
		fix, blockers := "KEEP", `[]`
		if pageOne < slideJuryReadyAverageFloor {
			fix = "Make the thesis immediate and specific"
			blockers = `["weak_thesis"]`
			if persona == "design_eye" {
				fix = "Refit the title inside the locked composition"
				blockers = `["text_overlap"]`
			}
		}
		return goalPanelVoice{Persona: persona, Text: fmt.Sprintf(`{"pages":[{"page":1,"score":%.1f,"fix":%q,"blockers":%s},{"page":2,"score":%.1f,"fix":"KEEP","blockers":[]}],"weakest_three":[1],"strongest_three":[2]}`, pageOne, fix, blockers, pageTwo)}
	}
	got := evaluateSlideJuryReadiness([]goalPanelVoice{
		voice("headline_ear", 4, 9),
		voice("design_eye", 5, 9),
		voice("room_gut", 8, 9),
	}, 2)
	if got.Verdict != "needs_changes" || len(got.BlockingPages) != 1 || got.BlockingPages[0] != 1 || got.ParsedSeats != 3 {
		t.Fatalf("readiness=%+v, want slide 1 blocked by two-seat agreement", got)
	}
	if got.MinimumAverage < 5.66 || got.MinimumAverage > 5.67 {
		t.Fatalf("minimum average=%v, want 5.67", got.MinimumAverage)
	}

	oneOutlier := evaluateSlideJuryReadiness([]goalPanelVoice{
		voice("headline_ear", 6, 9),
		voice("design_eye", 10, 9),
		voice("room_gut", 10, 9),
	}, 2)
	if oneOutlier.Verdict != "needs_changes" || len(oneOutlier.BlockingPages) != 1 || len(oneOutlier.Repairs) != 1 || oneOutlier.Repairs[0].Owner != "write" {
		t.Fatalf("headline specialist hard veto was outvoted: %+v", oneOutlier)
	}
	designOutlier := evaluateSlideJuryReadiness([]goalPanelVoice{
		voice("headline_ear", 10, 9),
		voice("design_eye", 6, 9),
		voice("room_gut", 10, 9),
	}, 2)
	if designOutlier.Verdict != "needs_changes" || len(designOutlier.BlockingPages) != 1 || len(designOutlier.Repairs) != 1 || designOutlier.Repairs[0].Owner != "layout_plan" {
		t.Fatalf("design specialist hard veto was outvoted or misrouted: %+v", designOutlier)
	}
	aboveFloorSpecialistRepair := evaluateSlideJuryReadiness([]goalPanelVoice{
		{Persona: "headline_ear", Text: `{"pages":[{"page":1,"score":8,"fix":"Rewrite the headline","blockers":[]}]}`},
		{Persona: "design_eye", Text: `{"pages":[{"page":1,"score":10,"fix":"KEEP","blockers":[]}]}`},
		{Persona: "room_gut", Text: `{"pages":[{"page":1,"score":10,"fix":"KEEP","blockers":[]}]}`},
	}, 1)
	if aboveFloorSpecialistRepair.Verdict != "needs_changes" || len(aboveFloorSpecialistRepair.BlockingPages) != 1 || len(aboveFloorSpecialistRepair.Repairs) != 1 || aboveFloorSpecialistRepair.Repairs[0].Owner != "write" || len(aboveFloorSpecialistRepair.Repairs[0].Fixes) != 1 || aboveFloorSpecialistRepair.Repairs[0].Fixes[0] != "Rewrite the headline" {
		t.Fatalf("above-floor specialist repair was numerically outvoted: %+v", aboveFloorSpecialistRepair)
	}
	belowFloorSpecialistKeep := evaluateSlideJuryReadiness([]goalPanelVoice{
		{Persona: "headline_ear", Text: `{"pages":[{"page":1,"score":8,"fix":"KEEP","blockers":[]}]}`},
		{Persona: "design_eye", Text: `{"pages":[{"page":1,"score":10,"fix":"KEEP","blockers":[]}]}`},
		{Persona: "room_gut", Text: `{"pages":[{"page":1,"score":10,"fix":"KEEP","blockers":[]}]}`},
	}, 1)
	if belowFloorSpecialistKeep.Verdict != "needs_attention" || len(belowFloorSpecialistKeep.BlockingPages) != 1 || len(belowFloorSpecialistKeep.Repairs) != 0 {
		t.Fatalf("below-floor specialist KEEP was numerically outvoted or given an invented repair: %+v", belowFloorSpecialistKeep)
	}
	duplicateDesignMembers := evaluateSlideJuryReadiness([]goalPanelVoice{
		{Persona: "headline_ear", Text: `{"pages":[{"page":1,"score":10,"fix":"KEEP","blockers":[]}]}`},
		{Persona: "design_eye", Text: `{"pages":[{"page":1,"score":10,"fix":"Repair overlap","blockers":["text_overlap"],"fix":"KEEP","blockers":[]}]}`},
		{Persona: "room_gut", Text: `{"pages":[{"page":1,"score":10,"fix":"KEEP","blockers":[]}]}`},
	}, 1)
	if duplicateDesignMembers.Verdict != "needs_attention" || duplicateDesignMembers.ParsedSeats != 2 {
		t.Fatalf("duplicate authoritative jury members used last-value-wins: %+v", duplicateDesignMembers)
	}
	unknownDesignMember := evaluateSlideJuryReadiness([]goalPanelVoice{
		{Persona: "headline_ear", Text: `{"pages":[{"page":1,"score":10,"fix":"KEEP","blockers":[]}]}`},
		{Persona: "design_eye", Text: `{"pages":[{"page":1,"score":10,"fix":"KEEP","blockers":[],"veto":false}]}`},
		{Persona: "room_gut", Text: `{"pages":[{"page":1,"score":10,"fix":"KEEP","blockers":[]}]}`},
	}, 1)
	if unknownDesignMember.Verdict != "needs_attention" || unknownDesignMember.ParsedSeats != 2 {
		t.Fatalf("unknown authoritative jury member escaped the closed scorecard schema: %+v", unknownDesignMember)
	}

	bland := evaluateSlideJuryReadiness([]goalPanelVoice{
		voice("headline_ear", 8, 9),
		voice("design_eye", 8, 9),
		voice("room_gut", 8, 9),
	}, 2)
	if bland.Verdict != "needs_changes" || len(bland.BlockingPages) != 1 || bland.BlockingPages[0] != 1 || bland.MinimumAverage != 8 {
		t.Fatalf("bland 8/10 rendered page escaped the %.1f floor: %+v", slideJuryReadyAverageFloor, bland)
	}

	noRepair := evaluateSlideJuryReadiness([]goalPanelVoice{
		{Persona: "headline_ear", Text: `{"pages":[{"page":1,"score":8,"fix":"KEEP","blockers":[]}]}`},
		{Persona: "design_eye", Text: `{"pages":[{"page":1,"score":8,"fix":"KEEP","blockers":[]}]}`},
		{Persona: "room_gut", Text: `{"pages":[{"page":1,"score":8,"fix":"KEEP","blockers":[]}]}`},
	}, 1)
	if noRepair.Verdict != "needs_attention" || len(noRepair.Repairs) != 0 {
		t.Fatalf("below-floor page without an executable repair was routable: %+v", noRepair)
	}

	incomplete := evaluateSlideJuryReadiness([]goalPanelVoice{voice("headline_ear", 9, 9)}, 2)
	if incomplete.Verdict != "needs_attention" {
		t.Fatalf("incomplete readiness=%+v, want fail-closed needs_attention", incomplete)
	}
	missingDesign := evaluateSlideJuryReadiness([]goalPanelVoice{voice("headline_ear", 9, 9), voice("room_gut", 10, 10)}, 2)
	if missingDesign.Verdict != "needs_attention" {
		t.Fatalf("room_gut substituted for missing design_eye: %+v", missingDesign)
	}

	omitted := evaluateSlideJuryReadiness([]goalPanelVoice{
		{Persona: "headline_ear", Text: `{"pages":[{"page":1,"score":9,"fix":"KEEP"}]}`},
		{Persona: "design_eye", Text: `{"pages":[{"page":1,"score":9,"fix":"KEEP"}]}`},
		{Persona: "room_gut", Text: `{"pages":[{"page":1,"score":9,"fix":"KEEP"}]}`},
	}, 2)
	if omitted.Verdict != "needs_attention" {
		t.Fatalf("omitted-page readiness=%+v, want fail-closed needs_attention", omitted)
	}

	structural := evaluateSlideJuryReadiness([]goalPanelVoice{
		{Persona: "headline_ear", Text: `{"pages":[{"page":1,"score":8,"fix":"Refit","blockers":["text_overlap"]}]}`},
		{Persona: "design_eye", Text: `{"pages":[{"page":1,"score":8,"fix":"Refit","blockers":["text_overlap"]}]}`},
		{Persona: "room_gut", Text: `{"pages":[{"page":1,"score":9,"fix":"KEEP","blockers":[]}]}`},
	}, 1)
	if structural.Verdict != "needs_changes" || len(structural.BlockingPages) != 1 || len(structural.Repairs) != 1 || structural.Repairs[0].Owner != "layout_plan" {
		t.Fatalf("structural-blocker readiness=%+v, want needs_changes", structural)
	}

	mixedOwner := evaluateSlideJuryReadiness([]goalPanelVoice{
		{Persona: "headline_ear", Text: `{"pages":[{"page":1,"score":6,"fix":"Rewrite and refit the line","blockers":["weak_thesis","text_overlap"]}]}`},
		{Persona: "design_eye", Text: `{"pages":[{"page":1,"score":9,"fix":"KEEP","blockers":[]}]}`},
		{Persona: "room_gut", Text: `{"pages":[{"page":1,"score":9,"fix":"KEEP","blockers":[]}]}`},
	}, 1)
	if mixedOwner.Verdict != "needs_attention" || len(mixedOwner.Repairs) != 0 {
		t.Fatalf("one ambiguous cross-owner fix was auto-routed: %+v", mixedOwner)
	}
	ambiguousAudience := evaluateSlideJuryReadiness([]goalPanelVoice{
		{Persona: "headline_ear", Text: `{"pages":[{"page":1,"score":9,"fix":"KEEP","blockers":[]}]}`},
		{Persona: "design_eye", Text: `{"pages":[{"page":1,"score":9,"fix":"KEEP","blockers":[]}]}`},
		{Persona: "room_gut", Text: `{"pages":[{"page":1,"score":6,"fix":"Make it land harder","blockers":[]}]}`},
	}, 1)
	if ambiguousAudience.Verdict != "needs_attention" || len(ambiguousAudience.Repairs) != 0 {
		t.Fatalf("unclassified room-gut repair guessed a copy/layout owner: %+v", ambiguousAudience)
	}
	ownerlessAlongsideRoutable := evaluateSlideJuryReadiness([]goalPanelVoice{
		{Persona: "headline_ear", Text: `{"pages":[{"page":1,"score":6,"fix":"Make the thesis immediate","blockers":["weak_thesis"]}]}`},
		{Persona: "design_eye", Text: `{"pages":[{"page":1,"score":9,"fix":"KEEP","blockers":[]}]}`},
		{Persona: "room_gut", Text: `{"pages":[{"page":1,"score":6,"fix":"Make it land harder","blockers":[]}]}`},
	}, 1)
	if ownerlessAlongsideRoutable.Verdict != "needs_attention" || len(ownerlessAlongsideRoutable.Repairs) != 0 {
		t.Fatalf("ownerless room-gut repair was silently dropped beside a routable repair: %+v", ownerlessAlongsideRoutable)
	}
	ownerlessAboveAverageFloor := evaluateSlideJuryReadiness([]goalPanelVoice{
		{Persona: "headline_ear", Text: `{"pages":[{"page":1,"score":10,"fix":"KEEP","blockers":[]}]}`},
		{Persona: "design_eye", Text: `{"pages":[{"page":1,"score":10,"fix":"KEEP","blockers":[]}]}`},
		{Persona: "room_gut", Text: `{"pages":[{"page":1,"score":8.8,"fix":"Make the audience care","blockers":[]}]}`},
	}, 1)
	if ownerlessAboveAverageFloor.Verdict != "needs_attention" || len(ownerlessAboveAverageFloor.Repairs) != 0 {
		t.Fatalf("ownerless room-gut repair was numerically outvoted: %+v", ownerlessAboveAverageFloor)
	}

	duplicateFromOneSeat := evaluateSlideJuryReadiness([]goalPanelVoice{
		{Persona: "room_gut", Text: `{"pages":[{"page":1,"score":9,"fix":"Refit","blockers":["text_overlap","text_overlap"]}]}`},
		{Persona: "design_eye", Text: `{"pages":[{"page":1,"score":9,"fix":"KEEP","blockers":[]}]}`},
		{Persona: "headline_ear", Text: `{"pages":[{"page":1,"score":9,"fix":"KEEP","blockers":[]}]}`},
	}, 1)
	if duplicateFromOneSeat.Verdict != "ready" {
		t.Fatalf("duplicate single-seat blocker=%+v, want one vote only", duplicateFromOneSeat)
	}

	duplicatePersona := evaluateSlideJuryReadiness([]goalPanelVoice{
		{Persona: "headline_ear", Text: `{"pages":[{"page":1,"score":9,"fix":"KEEP","blockers":[]}]}`},
		{Persona: " headline_ear ", Text: `{"pages":[{"page":1,"score":9,"fix":"KEEP","blockers":[]}]}`},
	}, 1)
	if duplicatePersona.Verdict != "needs_attention" || duplicatePersona.ParsedSeats != 1 {
		t.Fatalf("duplicate persona manufactured independent-seat quorum: %+v", duplicatePersona)
	}
}

func TestSlideJuryBlockerCodesCoverFirstClassRenderedDefects(t *testing.T) {
	for _, code := range slideJuryBlockerCodes {
		page := 2
		if code == "weak_cover_hierarchy" {
			page = 1
		}
		card := slideJurySeatScorecard{Pages: []slideJuryPageScore{{Page: page, Score: 8, Fix: "Apply the exact rendered repair.", Blockers: []string{code}}}}
		if err := validateSlideJurySeatScorecard(card, []int{page}); err != nil {
			t.Errorf("supported blocker %q was rejected: %v", code, err)
		}
	}
	for name, card := range map[string]slideJurySeatScorecard{
		"unknown":              {Pages: []slideJuryPageScore{{Page: 1, Score: 8, Fix: "Repair it.", Blockers: []string{"vague_vibes"}}}},
		"cover code off cover": {Pages: []slideJuryPageScore{{Page: 2, Score: 8, Fix: "Repair it.", Blockers: []string{"weak_cover_hierarchy"}}}},
		"keep with blocker":    {Pages: []slideJuryPageScore{{Page: 1, Score: 10, Fix: "KEEP", Blockers: []string{"text_overlap"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateSlideJurySeatScorecard(card, []int{card.Pages[0].Page}); err == nil {
				t.Fatalf("invalid rendered blocker was admitted: %+v", card)
			}
		})
	}
}

// The design-eye specialist judges whether imagery earns its place and can
// hard-veto a rendered defect into composition-owned repair.
func TestSlideJuryDesignEyeJudgesImageryAndOwnsHardVeto(t *testing.T) {
	var designEye goalPanelPersona
	for _, p := range slideJuryPersonas() {
		if p.Name == "design_eye" {
			designEye = p
		}
	}
	if designEye.Name == "" {
		t.Fatal("design_eye seat missing from the slide jury")
	}
	for _, need := range []string{"image", "earns", "hard veto"} {
		if !strings.Contains(strings.ToLower(designEye.System), strings.ToLower(need)) {
			t.Errorf("design_eye prompt missing the imagery-earns-its-place cue %q:\n%s", need, designEye.System)
		}
	}
}
