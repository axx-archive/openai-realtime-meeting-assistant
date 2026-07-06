package main

// slide_jury.go — vision slide juries (packaging OS §3/§6, Wave 5 item 21):
// critics that SEE the rendered pages. The render-runner sidecar already
// rasterizes every exported page to JPEG (render_runner.go, "Wave 5's vision
// slide juries consume exactly these images"); this file closes the loop:
//
//   1. persistRenderPageImageAssets — the callback-side seam. Until this wave
//      the render callback stored ONLY the flattened PDF as an artifact asset
//      and dropped payload.PageJPEGPaths on the floor; now every page JPEG is
//      read off the shared volume (path-validated against the render queue —
//      the sidecar is the least-trusted box in the system), stored in the
//      content-addressed blob store, and appended as a {kind: image} asset on
//      the same artifact via the existing appendArtifactAsset seam.
//   2. runSlideJury — pulls those page-image assets, loads the JPEGs from the
//      blob store, and runs the /packaging jury trio (headline ear / design
//      eye / the domain-literate room gut) as a 3-seat panel via the engine's
//      runGoalPanel primitive. The image blocks ride the raw-content seam
//      through a responder wrapper, so EVERY jury call — the three seats and
//      the synthesis — sees ALL pages. The merged scoreboard files as a
//      slide_jury_v1 artifact.
//
// The jury is ADVISORY by design: its findings land as revision notes on the
// findings record (packaging_studio.go), never as an auto-revise — the founder
// sees the scoreboard and human judgment decides what to apply.
//
// Keyless + sidecar-absent degrade gracefully: the studio stage discloses a
// skip (packaging_studio.go); nothing here blocks a ship.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// slideJuryContract is the merged-scoreboard artifact contract.
	slideJuryContract = "slide_jury_v1"

	// slideJurySource is the artifact provenance stamp.
	slideJurySource = "slide_jury"
)

// slideJuryPollInterval is how often the studio stage re-checks the deck for
// page-image assets while the render export is in flight. A package var (not a
// const) so tests can shrink it without waiting wall-clock seconds.
var slideJuryPollInterval = 2 * time.Second

// slideJuryWaitTimeout bounds how long the studio's jury stage waits for the
// deck's PDF export to complete (the sidecar polls every ~2s and renders in
// seconds, so 2 minutes is generous). Exceeding it is a DISCLOSED skip, never
// a failure.
func slideJuryWaitTimeout() time.Duration {
	return durationEnv("BONFIRE_SLIDE_JURY_WAIT", 2*time.Minute, time.Second)
}

// --- Page-image persistence (the render callback's missing half) --------------

// renderPageImageAssetCap bounds how many page JPEGs one callback can persist:
// a legitimate deck is tens of pages, and the callback body cap alone would
// admit tens of thousands of distinct paths — each a full read + blob write.
const renderPageImageAssetCap = 100

// persistRenderPageImageAssets stores the callback's page JPEGs as {kind:
// image} assets on the artifact, returning how many were persisted. Before
// this wave the page images were NOT persisted anywhere — the callback kept
// only the PDF — so the jury had nothing to see. Each path gets the same trust
// treatment as the callback's PDF path (resolveRenderQueueFile): it must live
// inside the render queue on the shared volume, resolve there through any
// symlink, and be a regular file no larger than the blob cap, or it is skipped
// and logged — a hostile holder of the runner token can never make the OS read
// an arbitrary file. Per-page failures degrade to fewer pages, never a failed
// callback. A fresh export REPLACES the artifact's previous page images in one
// metadata write (replaceArtifactAssetsOfKind), so a re-export after edits
// never leaves the jury scoring stale interleaved pages.
func persistRenderPageImageAssets(app *kanbanBoardApp, artifactID string, payload renderRunnerCallbackPayload) int {
	if app == nil || app.memory == nil {
		return 0
	}
	paths := payload.PageJPEGPaths
	if len(paths) > renderPageImageAssetCap {
		log.Warnf("Render callback for %s carries %d page images — truncated to the %d-page cap", artifactID, len(paths), renderPageImageAssetCap)
		paths = paths[:renderPageImageAssetCap]
	}
	pages := make([]artifactAsset, 0, len(paths))
	for index, rawPath := range paths {
		path, info, err := resolveRenderQueueFile(rawPath)
		if err != nil {
			log.Warnf("Render callback page image %d for %s rejected: %v", index+1, artifactID, err)
			continue
		}
		if info.Size() > blobMaxBytes {
			log.Warnf("Render callback page image %s is %dMB — above the %dMB blob cap, skipped", filepath.Base(path), info.Size()>>20, blobMaxBytes>>20)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			log.Warnf("Render callback page image %s unreadable: %v", filepath.Base(path), err)
			continue
		}
		ref, err := putBlob(data, "image/jpeg")
		if err != nil {
			log.Warnf("Render callback page image %s did not store: %v", filepath.Base(path), err)
			continue
		}
		pages = append(pages, artifactAsset{
			Ref:  ref,
			Mime: "image/jpeg",
			Name: fmt.Sprintf("page-%02d.jpg", index+1),
			Kind: "image",
		})
	}
	if len(pages) == 0 {
		return 0
	}
	if _, err := app.replaceArtifactAssetsOfKind(artifactID, "image", pages); err != nil {
		log.Warnf("Render callback page images did not attach to %s: %v", artifactID, err)
		return 0
	}
	return len(pages)
}

// artifactPageImageAssets filters an artifact's assets down to the page images
// the jury consumes ({kind: image} — the render callback's stamp).
func artifactPageImageAssets(entry meetingMemoryEntry) []artifactAsset {
	var pages []artifactAsset
	for _, asset := range artifactAssets(entry) {
		if asset.Kind == "image" {
			pages = append(pages, asset)
		}
	}
	return pages
}

// waitForDeckPageImages polls the deck artifact until page-image assets exist
// (the render callback landed), the render is marked failed, or the wait
// window closes. Returns the freshest artifact snapshot and whether pages
// exist — false is the studio stage's disclosed-skip signal, never an error.
func waitForDeckPageImages(app *kanbanBoardApp, deckID string) (meetingMemoryEntry, bool) {
	deadline := time.Now().Add(slideJuryWaitTimeout())
	for {
		deck, ok := app.osArtifactByID(deckID)
		if !ok {
			return meetingMemoryEntry{}, false
		}
		if len(artifactPageImageAssets(deck)) > 0 {
			return deck, true
		}
		if strings.EqualFold(strings.TrimSpace(deck.Metadata["renderStatus"]), renderJobStatusFailed) {
			return deck, false
		}
		if time.Now().After(deadline) {
			return deck, false
		}
		time.Sleep(slideJuryPollInterval)
	}
}

// --- The jury trio -------------------------------------------------------------

// slideJurySchema is the shared strict-JSON contract appended to every seat's
// system prompt (the runGoalPanel Schema seam). Fixes must be executable or
// the literal word KEEP — a jury that says "make it better" is slop.
const slideJurySchema = `Return STRICT JSON only, no prose outside it:
{"pages":[{"page":1,"score":0,"fix":"one executable change, or the literal word KEEP"}],"weakest_three":[1,2,3],"strongest_three":[4,5,6]}
Rules: score EVERY page you were shown, 0-10. A fix is EXECUTABLE (a concrete copy/layout/type change someone can apply verbatim) or exactly "KEEP" — never advice-shaped mush. weakest_three and strongest_three are page numbers, worst/best first; with fewer than three pages, list what exists.`

// slideJurySynthesisSystem merges the three scoreboards. It deliberately says
// "slide jury synthesizer" so responder fakes can route it, mirroring the
// engine's other addressable system prompts.
const slideJurySynthesisSystem = "You are Scout's slide jury synthesizer for Bonfire OS. Merge the seats' per-page scoreboards into ONE merged scoreboard: for every page, the average score, the seats' verdicts side by side, and ONE executable fix (or KEEP when the seats agree it stands). Then name the consensus weakest_three and strongest_three pages. Weigh agreement heavily; name genuine disagreement instead of averaging it away. These are REVISION NOTES for a human — decisive, executable, never auto-applied."

// slideJuryPersonas is the /packaging jury trio: the headline ear, the design
// eye, and the domain-literate room gut. Each seat sees ALL rendered pages
// (the responder wrapper attaches every page to every call).
func slideJuryPersonas() []goalPanelPersona {
	return []goalPanelPersona{
		{
			Name:   "headline_ear",
			System: "You are the HEADLINE EAR on Bonfire's slide jury, looking at the RENDERED pages of a shipped deck. You judge what the page says: does the headline land in one spoken breath, does the copy earn its claim, is there a line that would die in the room. Score every page; your fixes are rewritten lines, verbatim, or KEEP.",
		},
		{
			Name:   "design_eye",
			System: "You are the DESIGN EYE on Bonfire's slide jury, looking at the RENDERED pages of a shipped deck. You judge what the page looks like: hierarchy, type scale, alignment, color discipline, whether the eye knows where to go first, whether a chart reads at presentation distance. Score every page; your fixes are concrete layout/type/color changes, or KEEP.",
		},
		{
			Name:   "room_gut",
			System: "You are the ROOM GUT on Bonfire's slide jury — the domain-literate audience this deck will actually face, looking at the RENDERED pages. You judge how each page makes the room FEEL: lean in or bounce, believe or smell the hand-wave, the page a skeptic screenshots. You know how this category actually clears deals. Score every page; your fixes are the concrete change that wins the room back, or KEEP.",
		},
	}
}

// --- Page budgeting --------------------------------------------------------------

// slideJuryPage is one rendered page the jury sees: its 1-based page number
// and the raw JPEG bytes from the blob store.
type slideJuryPage struct {
	Number int
	Data   []byte
}

// capSlideJuryPages enforces the wire-layer image budget BEFORE the request is
// assembled: at most anthropicMaxRequestImages pages and
// ~anthropicMaxRequestImageBytes of raw JPEG. Dropped pages are disclosed in
// the returned note (spliced into the jury task), never silently vanished.
func capSlideJuryPages(pages []slideJuryPage) ([]slideJuryPage, string) {
	capped := pages
	if len(capped) > anthropicMaxRequestImages {
		capped = capped[:anthropicMaxRequestImages]
	}
	total := 0
	for index, page := range capped {
		total += len(page.Data)
		if total > anthropicMaxRequestImageBytes {
			capped = capped[:index]
			break
		}
	}
	if len(capped) == len(pages) {
		return capped, ""
	}
	return capped, fmt.Sprintf(
		"DISCLOSED: only the first %d of %d rendered pages are attached (request image budget: %d images, ~%dMB). Score what you see; note that later pages went unjuried.",
		len(capped), len(pages), anthropicMaxRequestImages, anthropicMaxRequestImageBytes>>20)
}

// --- The jury run ----------------------------------------------------------------

// withSlideJuryPageBlocks wraps a responder so the page blocks are spliced
// into the FIRST user message of every outgoing request — images first, task
// text after, per the vision guidance. This is how the jury's images ride the
// raw-content seam through runGoalPanel unchanged: the panel primitive stays
// text-shaped, and the wrapper makes every call it issues (all three seats AND
// the synthesis) image-bearing. Copies, never mutates, the caller's slices.
func withSlideJuryPageBlocks(base anthropicMessagesResponder, pageBlocks []json.RawMessage) anthropicMessagesResponder {
	return func(ctx context.Context, apiKey string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		messages := make([]anthropicMessage, len(request.Messages))
		copy(messages, request.Messages)
		for i := range messages {
			if messages[i].Role != "user" {
				continue
			}
			content := make([]json.RawMessage, 0, len(pageBlocks)+len(messages[i].Content))
			content = append(content, pageBlocks...)
			content = append(content, messages[i].Content...)
			messages[i].Content = content
			break
		}
		request.Messages = messages
		return base(ctx, apiKey, request)
	}
}

// runSlideJury runs the 3-seat vision jury over a deck artifact's rendered
// page images and files the merged scoreboard as a slide_jury_v1 artifact.
// The deck must already carry {kind: image} page assets (the render callback
// persists them); a deck without pages is the caller's disclosed-skip case,
// surfaced here as an error so no jury ever runs blind.
func runSlideJury(ctx context.Context, app *kanbanBoardApp, goalID string, artifact meetingMemoryEntry) (meetingMemoryEntry, error) {
	if app == nil || app.memory == nil {
		return meetingMemoryEntry{}, fmt.Errorf("artifact memory is unavailable")
	}
	assets := artifactPageImageAssets(artifact)
	if len(assets) == 0 {
		return meetingMemoryEntry{}, fmt.Errorf("deck %s carries no page-image assets — nothing for the jury to see", artifact.ID)
	}
	// Cap BEFORE loading: blobs load only until the request budget is met
	// (anthropicMaxRequestImages pages / ~anthropicMaxRequestImageBytes), so an
	// unbounded asset count never sits fully resident — a 300-page deck loads
	// ~12 pages, not 300, and a bad blob past the budget can never abort the
	// jury because it is never read.
	pages := make([]slideJuryPage, 0, min(len(assets), anthropicMaxRequestImages))
	totalBytes := 0
	for index, asset := range assets {
		if len(pages) >= anthropicMaxRequestImages || totalBytes >= anthropicMaxRequestImageBytes {
			break
		}
		data, _, err := getBlob(asset.Ref)
		if err != nil {
			return meetingMemoryEntry{}, fmt.Errorf("load page image %d (%s): %w", index+1, asset.Ref, err)
		}
		pages = append(pages, slideJuryPage{Number: index + 1, Data: data})
		totalBytes += len(data)
	}
	// capSlideJuryPages trims the page that overflowed the byte budget; the
	// disclosure is recomputed against the FULL asset count, since unloaded
	// pages were dropped by the pre-load cap above.
	capped, _ := capSlideJuryPages(pages)
	if len(capped) == 0 {
		return meetingMemoryEntry{}, fmt.Errorf("no page image fits the %dMB request image budget", anthropicMaxRequestImageBytes>>20)
	}
	capNote := ""
	if len(capped) < len(assets) {
		capNote = fmt.Sprintf(
			"DISCLOSED: only the first %d of %d rendered pages are attached (request image budget: %d images, ~%dMB). Score what you see; note that later pages went unjuried.",
			len(capped), len(assets), anthropicMaxRequestImages, anthropicMaxRequestImageBytes>>20)
	}

	pageBlocks := make([]json.RawMessage, 0, 2*len(capped))
	for _, page := range capped {
		pageBlocks = append(pageBlocks, anthropicTextBlock(fmt.Sprintf("Rendered page %d of %d:", page.Number, len(assets))))
		pageBlocks = append(pageBlocks, anthropicImageBlock("image/jpeg", page.Data))
	}

	deckTitle := firstNonEmptyString(strings.TrimSpace(artifact.Metadata["title"]), "the shipped deck")
	taskLines := []string{
		"Slide jury: judge the RENDERED pages of \"" + deckTitle + "\" exactly as a room will see them — the images above are the deliverable, not a draft.",
		fmt.Sprintf("You were shown %d page(s). Score EVERY page per your seat's lens, name your weakest_three and strongest_three, and make every fix executable or the literal word KEEP.", len(capped)),
	}
	if capNote != "" {
		taskLines = append(taskLines, capNote)
	}

	engine := newGoalEngine(app)
	engine.responder = withSlideJuryPageBlocks(engine.responder, pageBlocks)
	outcome, err := engine.runGoalPanel(ctx, goalPanelSpec{
		Task:      strings.Join(taskLines, "\n"),
		Schema:    slideJurySchema,
		Personas:  slideJuryPersonas(),
		Synthesis: slideJurySynthesisSystem,
	})
	if err != nil {
		return meetingMemoryEntry{}, fmt.Errorf("slide jury panel: %w", err)
	}

	// The merged scoreboard leads; every seat's raw scoreboard stays on the
	// record below it (the runProcessPanelStage voices shape).
	var body strings.Builder
	body.WriteString(outcome.Synthesis)
	body.WriteString("\n\n## Jury voices\n")
	for _, voice := range outcome.Voices {
		body.WriteString("\n### " + voice.Persona + "\n")
		if voice.Err != nil {
			body.WriteString("(this seat's call failed: " + compactAssistantLine(voice.Err.Error()) + ")\n")
			continue
		}
		body.WriteString(strings.TrimSpace(voice.Text) + "\n")
	}

	metadata := map[string]string{
		"artifactContract": slideJuryContract,
		"type":             artifactTypeMarkdown,
		"source":           slideJurySource,
		"deckArtifactId":   artifact.ID,
	}
	if goalID = strings.TrimSpace(goalID); goalID != "" {
		metadata["goalId"] = goalID
	}
	if packageID := strings.TrimSpace(artifact.Metadata["packageId"]); packageID != "" {
		metadata["packageId"] = packageID
	}
	filed, appended, err := app.createOSArtifactWithMetadata("workflow", "Slide jury — merged scoreboard", body.String(), scoutParticipantName, metadata)
	if err != nil {
		return meetingMemoryEntry{}, fmt.Errorf("file slide jury scoreboard: %w", err)
	}
	if !appended || strings.TrimSpace(filed.ID) == "" {
		return meetingMemoryEntry{}, fmt.Errorf("slide jury scoreboard was not saved")
	}
	return filed, nil
}
