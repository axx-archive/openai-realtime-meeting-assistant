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
//      content-addressed blob store, and appended as a {kind: page_image} asset on
//      the same artifact via the existing appendArtifactAsset seam.
//   2. runSlideJury — pulls those page-image assets, loads the JPEGs from the
//      blob store, and runs the /packaging jury trio (headline ear / design
//      eye / the domain-literate room gut) as a 3-seat panel via the engine's
//      runGoalPanel primitive. The image blocks ride the raw-content seam
//      through a responder wrapper. Decks that exceed one provider request are
//      reviewed in bounded batches: every seat sees every authored page, then
//      the exact per-seat scorecards are reassembled and synthesized across the
//      complete deck. The merged scoreboard files as a slide_jury_v1 artifact.
//
// The jury's copy/layout findings land as revision notes, while its structured
// readiness stamp changes the final checkpoint language so a deck with
// blocking rendered defects is never described as ready.
//
// Keyless + sidecar-absent degrade gracefully: the studio stage discloses a
// skip (packaging_studio.go); nothing here blocks a ship.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"
)

const (
	// slideJuryContract is the merged-scoreboard artifact contract.
	slideJuryContract = "slide_jury_v1"

	// slideJurySource is the artifact provenance stamp.
	slideJurySource = "slide_jury"

	// Structured fixes are carried from the independent seat scorecards into
	// the deterministic repair gate. Bounds keep one jury artifact from growing
	// an unbounded requeue prompt while preserving every admitted fix verbatim.
	slideJuryRepairFixMaxChars = 600
	slideJuryRepairFixMaxCount = 36
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
// page_image} assets on the artifact, returning how many were persisted. Before
// this wave the page images were NOT persisted anywhere — the callback kept
// only the PDF — so the jury had nothing to see. Each path gets the same trust
// treatment as the callback's PDF path (resolveRenderQueueFile): it must live
// inside the render queue on the shared volume, resolve there through any
// symlink, and be a regular file no larger than the blob cap, or it is skipped
// and logged — a hostile holder of the runner token can never make the OS read
// an arbitrary file. Per-page failures degrade to fewer pages, never a failed
// callback. collectRenderPageImageAssets lets the callback land PDF + pages in
// one revision-bound CAS; persistRenderPageImageAssets keeps the standalone
// test/maintenance seam and replaces prior pages in one metadata write.
func collectRenderPageImageAssets(artifactID string, payload renderRunnerCallbackPayload) []artifactAsset {
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
			Kind: "page_image",
		})
	}
	return pages
}

func persistRenderPageImageAssets(app *kanbanBoardApp, artifactID string, payload renderRunnerCallbackPayload) int {
	if app == nil || app.memory == nil {
		return 0
	}
	pages := collectRenderPageImageAssets(artifactID, payload)
	if len(pages) == 0 {
		return 0
	}
	if _, err := app.replaceArtifactAssetsOfKind(artifactID, "page_image", pages); err != nil {
		log.Warnf("Render callback page images did not attach to %s: %v", artifactID, err)
		return 0
	}
	return len(pages)
}

// artifactPageImageAssets filters an artifact's assets down to the page images
// the jury consumes. Generated deck imagery remains kind=image and must never
// masquerade as rendered pages. The narrow filename fallback reads callbacks
// filed before page_image became its own kind.
func artifactPageImageAssets(entry meetingMemoryEntry) []artifactAsset {
	var pages []artifactAsset
	for _, asset := range artifactAssets(entry) {
		if artifactAssetIsPageImage(asset) {
			pages = append(pages, asset)
		}
	}
	return pages
}

// renderedDeckSlideCount reads the authored slide topology, ignoring nested
// semantic sections. A slide is a .pg/.slide element or a direct section child
// of #stage for older decks. This is the structural side of the jury contract:
// every authored slide must have exactly one rendered page before review.
func renderedDeckSlideCount(source string) int {
	doc, err := xhtml.Parse(strings.NewReader(source))
	if err != nil {
		return 0
	}
	count := 0
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		isStageChild := node.Type == xhtml.ElementNode && node.Data == "section" && node.Parent != nil && legacyNodeAttr(node.Parent, "id") == "stage"
		if node.Type == xhtml.ElementNode && (legacyNodeHasClass(node, "pg") || legacyNodeHasClass(node, "slide") || isStageChild) {
			count++
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return count
}

// waitForDeckPageImages polls the deck artifact until page-image assets exist
// (the render callback landed), the render is marked failed/stale, or the wait
// window closes. Returns the freshest artifact snapshot and whether pages
// exist — false is the studio stage's disclosed-skip signal, never an error.
func waitForDeckPageImages(app *kanbanBoardApp, deckID string) (meetingMemoryEntry, bool) {
	deadline := time.Now().Add(slideJuryWaitTimeout())
	observedDeck := false
	for {
		deck, ok := app.osArtifactByID(deckID)
		if !ok {
			return meetingMemoryEntry{}, false
		}
		if len(artifactPageImageAssets(deck)) > 0 {
			return deck, true
		}
		// Isolated pipeline tests can synchronously emulate the signed callback
		// only after the stage has observed its own stamped deck. This removes a
		// scheduler-dependent goroutine race without changing the production
		// timeout or callback path (the hook is nil outside tests).
		if !observedDeck && app.slideJuryDeckObserved != nil {
			observedDeck = true
			app.slideJuryDeckObserved(deck)
			continue
		}
		renderStatus := strings.ToLower(strings.TrimSpace(deck.Metadata["renderStatus"]))
		if renderStatus == renderJobStatusFailed || renderStatus == renderJobStatusStale {
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
{"pages":[{"page":1,"score":0,"fix":"one executable change, or the literal word KEEP","blockers":["text_overlap"]}],"weakest_three":[1,2,3],"strongest_three":[4,5,6]}
Rules: score EVERY page you were shown, 0-10. blockers is zero or more exact codes from text_overlap, text_clipped, off_canvas, unreadable, unsupported_claim. A fix is EXECUTABLE (a concrete copy/layout/type change someone can apply verbatim) or exactly "KEEP" — never advice-shaped mush. weakest_three and strongest_three are page numbers, worst/best first; with fewer than three pages, list what exists.`

type slideJuryPageScore struct {
	Page     int      `json:"page"`
	Score    float64  `json:"score"`
	Fix      string   `json:"fix"`
	Blockers []string `json:"blockers"`
}

type slideJurySeatScorecard struct {
	Pages          []slideJuryPageScore `json:"pages"`
	WeakestThree   []int                `json:"weakest_three,omitempty"`
	StrongestThree []int                `json:"strongest_three,omitempty"`
}

type slideJuryRepair struct {
	Page  int      `json:"page"`
	Fixes []string `json:"fixes"`
}

type slideJuryReadiness struct {
	Verdict        string
	BlockingPages  []int
	MinimumAverage float64
	ParsedSeats    int
	Repairs        []slideJuryRepair
}

// validateSlideJurySeatScorecard admits a seat only when it covers the exact
// requested page set once. Batch aggregation uses global page numbers, so a
// plausible-looking 1..N response for the wrong batch can never be spliced
// into a full-deck verdict.
func validateSlideJurySeatScorecard(card slideJurySeatScorecard, expectedPages []int) error {
	if len(card.Pages) != len(expectedPages) {
		return fmt.Errorf("scorecard carries %d pages, want %d", len(card.Pages), len(expectedPages))
	}
	expected := make(map[int]struct{}, len(expectedPages))
	for _, page := range expectedPages {
		expected[page] = struct{}{}
	}
	seen := make(map[int]struct{}, len(card.Pages))
	for _, page := range card.Pages {
		if _, ok := expected[page.Page]; !ok {
			return fmt.Errorf("scorecard page %d is outside the requested page set", page.Page)
		}
		if _, duplicate := seen[page.Page]; duplicate {
			return fmt.Errorf("scorecard repeats page %d", page.Page)
		}
		seen[page.Page] = struct{}{}
		fix := strings.TrimSpace(page.Fix)
		if page.Score < 0 || page.Score > 10 || fix == "" || len(fix) > slideJuryRepairFixMaxChars || (strings.EqualFold(fix, "KEEP") && fix != "KEEP") {
			return fmt.Errorf("scorecard page %d has an invalid score or fix", page.Page)
		}
		for _, blocker := range page.Blockers {
			blocker = strings.ToLower(strings.TrimSpace(blocker))
			if !oneOf(blocker, "text_overlap", "text_clipped", "off_canvas", "unreadable", "unsupported_claim") {
				return fmt.Errorf("scorecard page %d has unsupported blocker %q", page.Page, blocker)
			}
		}
	}
	return nil
}

func slideJuryExpectedPages(first int, count int) []int {
	pages := make([]int, count)
	for index := range pages {
		pages[index] = first + index
	}
	return pages
}

// evaluateSlideJuryReadiness turns the independent seat scorecards into a
// stable machine-readable checkpoint signal. Two seats must agree that a page
// is below the 7/10 presentation floor; one outlier cannot block the deck.
// Fewer than two parseable seats fails closed as needs_attention.
func evaluateSlideJuryReadiness(voices []goalPanelVoice, expectedPages int) slideJuryReadiness {
	type pageVotes struct {
		total    float64
		count    int
		low      int
		blockers map[string]int
		fixes    []string
	}
	pages := map[int]*pageVotes{}
	parsedSeats := 0
	for _, voice := range voices {
		if voice.Err != nil {
			continue
		}
		var card slideJurySeatScorecard
		if err := json.Unmarshal([]byte(stripJSONCodeFence(strings.TrimSpace(voice.Text))), &card); err != nil {
			continue
		}
		// Validate the whole seat before counting any vote. A missing fix is not
		// a usable scoreboard: every page must carry an exact executable change
		// or the literal KEEP, as promised by slideJurySchema.
		if err := validateSlideJurySeatScorecard(card, slideJuryExpectedPages(1, expectedPages)); err != nil {
			continue
		}
		parsedSeats++
		for _, page := range card.Pages {
			fix := strings.TrimSpace(page.Fix)
			vote := pages[page.Page]
			if vote == nil {
				vote = &pageVotes{blockers: map[string]int{}}
				pages[page.Page] = vote
			}
			vote.total += page.Score
			vote.count++
			if page.Score < 7 {
				vote.low++
			}
			if !strings.EqualFold(fix, "KEEP") && !slices.Contains(vote.fixes, fix) {
				vote.fixes = append(vote.fixes, fix)
			}
			seenBlockers := map[string]struct{}{}
			for _, blocker := range page.Blockers {
				blocker = strings.ToLower(strings.TrimSpace(blocker))
				if _, duplicate := seenBlockers[blocker]; duplicate {
					continue
				}
				seenBlockers[blocker] = struct{}{}
				if oneOf(blocker, "text_overlap", "text_clipped", "off_canvas", "unreadable", "unsupported_claim") {
					vote.blockers[blocker]++
				}
			}
		}
	}
	result := slideJuryReadiness{Verdict: "ready", MinimumAverage: 10, ParsedSeats: parsedSeats}
	if parsedSeats < 2 || expectedPages <= 0 || len(pages) == 0 {
		result.Verdict = "needs_attention"
		result.MinimumAverage = 0
		return result
	}
	for page := 1; page <= expectedPages; page++ {
		votes := pages[page]
		if votes == nil || votes.count < 2 {
			result.Verdict = "needs_attention"
			continue
		}
		average := votes.total / float64(votes.count)
		if average < result.MinimumAverage {
			result.MinimumAverage = average
		}
		structuralAgreement := false
		for _, count := range votes.blockers {
			if count >= 2 {
				structuralAgreement = true
				break
			}
		}
		if votes.low >= 2 || structuralAgreement {
			result.BlockingPages = append(result.BlockingPages, page)
		}
	}
	slices.Sort(result.BlockingPages)
	if len(result.BlockingPages) > 0 {
		result.Verdict = "needs_changes"
		totalFixes := 0
		for _, page := range result.BlockingPages {
			fixes := append([]string(nil), pages[page].fixes...)
			if len(fixes) == 0 || totalFixes+len(fixes) > slideJuryRepairFixMaxCount {
				// A blocking verdict without an executable exact repair cannot be
				// auto-routed back to the deck writer.
				result.Verdict = "needs_attention"
				result.Repairs = nil
				return result
			}
			totalFixes += len(fixes)
			result.Repairs = append(result.Repairs, slideJuryRepair{Page: page, Fixes: fixes})
		}
	}
	return result
}

func slideJuryPageList(pages []int) string {
	values := make([]string, 0, len(pages))
	for _, page := range pages {
		values = append(values, strconv.Itoa(page))
	}
	return strings.Join(values, ",")
}

// slideJurySynthesisSystem merges the three scoreboards. It deliberately says
// "slide jury synthesizer" so responder fakes can route it, mirroring the
// engine's other addressable system prompts.
const slideJurySynthesisSystem = "You are Scout's slide jury synthesizer for Stride. Merge the seats' per-page scoreboards into ONE merged scoreboard: for every page, the average score, the seats' verdicts side by side, and ONE executable fix (or KEEP when the seats agree it stands). Then name the consensus weakest_three and strongest_three pages. Weigh agreement heavily; name genuine disagreement instead of averaging it away. These are REVISION NOTES for a human — decisive, executable, never auto-applied."

// slideJuryPersonas is the /packaging jury trio: the headline ear, the design
// eye, and the domain-literate room gut. Each seat sees every rendered page;
// larger decks are split into exact bounded batches and reassembled by seat.
func slideJuryPersonas() []goalPanelPersona {
	return []goalPanelPersona{
		{
			Name:   "headline_ear",
			System: "You are the HEADLINE EAR on Bonfire's slide jury, looking at the RENDERED pages of a shipped deck. You judge what the page says: does the headline land in one spoken breath, does the copy earn its claim, is there a line that would die in the room. Score every page; your fixes are rewritten lines, verbatim, or KEEP.",
		},
		{
			Name:   "design_eye",
			System: "You are the DESIGN EYE on Bonfire's slide jury, looking at the RENDERED pages of a shipped deck. You judge what the page looks like: hierarchy, type scale, alignment, color discipline, whether the eye knows where to go first, whether a chart reads at presentation distance. When a page carries a photographic image, judge whether it EARNS its place: does it drive the emotional beat and advance the story, or is it decoration a cleaner typographic page would beat? An image that earns nothing costs the page — your fix names it ('drop the image, let the headline carry the page' / 'replace with a shot of X') or KEEP. These are ADVISORY revision notes, never auto-applied. Score every page; your fixes are concrete layout/type/color/imagery changes, or KEEP.",
		},
		{
			Name:   "room_gut",
			System: "You are the ROOM GUT on Bonfire's slide jury — the domain-literate audience this deck will actually face, looking at the RENDERED pages. You judge how each page makes the room FEEL: lean in or bounce, believe or smell the hand-wave, the page a skeptic screenshots. You know how this category actually clears deals. Score every page; your fixes are the concrete change that wins the room back, or KEEP.",
		},
	}
}

// --- Page batching -------------------------------------------------------------

// slideJuryPage is one rendered page the jury sees: its 1-based page number
// and the raw JPEG bytes from the blob store.
type slideJuryPage struct {
	Number int
	Data   []byte
}

// slideJuryBatchCanAccept is the single source of truth for the per-request
// provider envelope. The runtime starts another batch when this returns false;
// it never truncates the deck. A page that cannot fit an empty batch is an
// explicit error because no provider call could review it truthfully.
func slideJuryBatchCanAccept(currentPages int, currentBytes int, nextBytes int) bool {
	if currentPages < 0 || currentBytes < 0 || nextBytes < 0 || currentPages >= anthropicMaxRequestImages {
		return false
	}
	if nextBytes > anthropicMaxRequestImageBytes {
		return false
	}
	return currentBytes <= anthropicMaxRequestImageBytes-nextBytes
}

type slideJuryBatchRecord struct {
	Pages   []int
	Outcome goalPanelOutcome
}

func slideJuryPageNumbers(pages []slideJuryPage) []int {
	numbers := make([]int, len(pages))
	for index, page := range pages {
		numbers[index] = page.Number
	}
	return numbers
}

func slideJuryVoiceForPersona(voices []goalPanelVoice, persona string) (goalPanelVoice, bool) {
	for _, voice := range voices {
		if voice.Persona == persona {
			return voice, true
		}
	}
	return goalPanelVoice{}, false
}

func finalizeSlideJurySeatScorecard(card *slideJurySeatScorecard) {
	if card == nil {
		return
	}
	slices.SortFunc(card.Pages, func(a, b slideJuryPageScore) int {
		if a.Page < b.Page {
			return -1
		}
		if a.Page > b.Page {
			return 1
		}
		return 0
	})
	for index := range card.Pages {
		card.Pages[index].Fix = strings.TrimSpace(card.Pages[index].Fix)
		if card.Pages[index].Blockers == nil {
			card.Pages[index].Blockers = []string{}
		}
	}
	ranked := append([]slideJuryPageScore(nil), card.Pages...)
	slices.SortFunc(ranked, func(a, b slideJuryPageScore) int {
		if a.Score < b.Score {
			return -1
		}
		if a.Score > b.Score {
			return 1
		}
		if a.Page < b.Page {
			return -1
		}
		if a.Page > b.Page {
			return 1
		}
		return 0
	})
	limit := min(3, len(ranked))
	card.WeakestThree = make([]int, 0, limit)
	for _, page := range ranked[:limit] {
		card.WeakestThree = append(card.WeakestThree, page.Page)
	}
	card.StrongestThree = make([]int, 0, limit)
	for index := len(ranked) - 1; index >= len(ranked)-limit; index-- {
		card.StrongestThree = append(card.StrongestThree, ranked[index].Page)
	}
}

// mergeSlideJuryBatchVoices reconstitutes one full-deck scorecard per persona.
// A persona is admitted only if every batch returned the exact global page set;
// a missing or malformed batch marks that whole seat unavailable, preserving
// the existing two-complete-seat readiness floor without manufacturing votes.
func mergeSlideJuryBatchVoices(records []slideJuryBatchRecord, expectedPages int) []goalPanelVoice {
	personas := slideJuryPersonas()
	merged := make([]goalPanelVoice, len(personas))
	for personaIndex, persona := range personas {
		voice := goalPanelVoice{Persona: persona.Name}
		card := slideJurySeatScorecard{Pages: make([]slideJuryPageScore, 0, expectedPages)}
		for batchIndex, record := range records {
			part, ok := slideJuryVoiceForPersona(record.Outcome.Voices, persona.Name)
			if !ok {
				voice.Err = fmt.Errorf("jury batch %d (%s) omitted the %s seat", batchIndex+1, slideJuryPageList(record.Pages), persona.Name)
				break
			}
			if part.Err != nil {
				voice.Err = fmt.Errorf("jury batch %d (%s), %s seat: %w", batchIndex+1, slideJuryPageList(record.Pages), persona.Name, part.Err)
				break
			}
			var batchCard slideJurySeatScorecard
			if err := json.Unmarshal([]byte(stripJSONCodeFence(strings.TrimSpace(part.Text))), &batchCard); err != nil {
				voice.Err = fmt.Errorf("jury batch %d (%s), %s seat returned invalid JSON: %w", batchIndex+1, slideJuryPageList(record.Pages), persona.Name, err)
				break
			}
			if err := validateSlideJurySeatScorecard(batchCard, record.Pages); err != nil {
				voice.Err = fmt.Errorf("jury batch %d (%s), %s seat: %w", batchIndex+1, slideJuryPageList(record.Pages), persona.Name, err)
				break
			}
			card.Pages = append(card.Pages, batchCard.Pages...)
		}
		if voice.Err == nil {
			finalizeSlideJurySeatScorecard(&card)
			if err := validateSlideJurySeatScorecard(card, slideJuryExpectedPages(1, expectedPages)); err != nil {
				voice.Err = fmt.Errorf("full-deck %s scorecard: %w", persona.Name, err)
			} else if payload, err := json.Marshal(card); err != nil {
				voice.Err = fmt.Errorf("marshal full-deck %s scorecard: %w", persona.Name, err)
			} else {
				voice.Text = string(payload)
			}
		}
		merged[personaIndex] = voice
	}
	return merged
}

// --- The jury run ----------------------------------------------------------------

// withSlideJuryPageBlocks wraps the retired Anthropic-shaped test responder so
// historical fixtures can still exercise the page-block helper. Production
// juries use the OpenAI Responses wrapper below.
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

func withSlideJuryOpenAIPageContent(base openAITextResponder, pageContent []openAIInputContent) openAITextResponder {
	return func(ctx context.Context, apiKey string, request openAITextRequest) (string, error) {
		attachments := make([]openAIInputContent, 0, len(pageContent)+len(request.Attachments))
		attachments = append(attachments, pageContent...)
		attachments = append(attachments, request.Attachments...)
		request.Attachments = attachments
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
	deckTitle := firstNonEmptyString(strings.TrimSpace(artifact.Metadata["title"]), "the shipped deck")

	// Load and review one bounded batch at a time. At most one not-yet-admitted
	// page is held beside the current <=20MB batch, so a 100-page artifact never
	// becomes a 100-image in-memory request. Crucially, the cursor continues until
	// every asset has been sent to every seat; request limits start a new batch,
	// never a truncation path.
	var records []slideJuryBatchRecord
	cursor := 0
	var pending *slideJuryPage
	for cursor < len(assets) || pending != nil {
		pages := make([]slideJuryPage, 0, min(len(assets)-cursor+1, anthropicMaxRequestImages))
		batchBytes := 0
		for len(pages) < anthropicMaxRequestImages && (cursor < len(assets) || pending != nil) {
			var page slideJuryPage
			if pending != nil {
				page = *pending
				pending = nil
			} else {
				asset := assets[cursor]
				data, _, err := getBlob(asset.Ref)
				if err != nil {
					return meetingMemoryEntry{}, fmt.Errorf("load page image %d (%s): %w", cursor+1, asset.Ref, err)
				}
				page = slideJuryPage{Number: cursor + 1, Data: data}
				cursor++
			}
			if len(page.Data) == 0 {
				return meetingMemoryEntry{}, fmt.Errorf("page image %d is empty — the jury cannot see it", page.Number)
			}
			if len(page.Data) > anthropicMaxRequestImageBytes {
				return meetingMemoryEntry{}, fmt.Errorf("page image %d is ~%dMB, above the %dMB per-request image budget — the jury cannot review every page", page.Number, len(page.Data)>>20, anthropicMaxRequestImageBytes>>20)
			}
			if !slideJuryBatchCanAccept(len(pages), batchBytes, len(page.Data)) {
				pageCopy := page
				pending = &pageCopy
				break
			}
			pages = append(pages, page)
			batchBytes += len(page.Data)
		}
		if len(pages) == 0 {
			return meetingMemoryEntry{}, fmt.Errorf("no page image fits the %dMB request image budget", anthropicMaxRequestImageBytes>>20)
		}

		pageContent := make([]openAIInputContent, 0, 2*len(pages))
		for _, page := range pages {
			pageContent = append(pageContent,
				openAIInputContent{Type: "input_text", Text: fmt.Sprintf("Rendered page %d of %d:", page.Number, len(assets))},
				openAIInputContent{Type: "input_image", ImageURL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(page.Data)},
			)
		}
		pageNumbers := slideJuryPageNumbers(pages)
		taskLines := []string{
			"Slide jury: judge the RENDERED pages of \"" + deckTitle + "\" exactly as a room will see them — the images above are the deliverable, not a draft.",
			fmt.Sprintf("This bounded review batch contains global page(s) %s of %d. Score EVERY attached page per your seat's lens using those exact global page numbers; name this batch's weakest_three and strongest_three, and make every fix executable or the literal word KEEP.", slideJuryPageList(pageNumbers), len(assets)),
		}
		engine := newGoalEngine(app)
		engine.openAIResponder = withSlideJuryOpenAIPageContent(engine.openAIResponder, pageContent)
		panelSpec := goalPanelSpec{
			Task:      strings.Join(taskLines, "\n"),
			Schema:    slideJurySchema,
			Personas:  slideJuryPersonas(),
			Synthesis: slideJurySynthesisSystem,
			Review:    true,
		}
		var outcome goalPanelOutcome
		var err error
		hasAnotherBatch := pending != nil || cursor < len(assets)
		if len(records) == 0 && !hasAnotherBatch {
			// Preserve the compact one-batch path: three image reviews plus one
			// image-aware synthesis. Larger decks must not pay for throwaway
			// per-batch syntheses that can fail after all seats already succeeded.
			outcome, err = engine.runGoalPanel(ctx, panelSpec)
		} else {
			outcome, err = engine.runGoalPanelVoices(ctx, panelSpec)
		}
		if err != nil {
			return meetingMemoryEntry{}, fmt.Errorf("slide jury panel for page(s) %s: %w", slideJuryPageList(pageNumbers), err)
		}
		records = append(records, slideJuryBatchRecord{Pages: pageNumbers, Outcome: outcome})
	}

	// A one-batch deck preserves the historical four-call path byte-for-byte.
	// Multi-batch decks assemble each persona's exact full-deck JSON first, then
	// make their only synthesis call over those validated scorecards. The
	// synthesizer never substitutes for image review; it only merges reviews
	// whose page coverage was proven above.
	outcome := records[0].Outcome
	if len(records) > 1 {
		outcome.Voices = mergeSlideJuryBatchVoices(records, len(assets))
		var replies strings.Builder
		completeSeats := 0
		for _, voice := range outcome.Voices {
			replies.WriteString("### Panelist: " + voice.Persona + "\n")
			if voice.Err != nil {
				replies.WriteString("(this panelist did not complete every batch: " + compactAssistantLine(voice.Err.Error()) + ")\n\n")
				continue
			}
			completeSeats++
			replies.WriteString(voice.Text + "\n\n")
		}
		if completeSeats == 0 {
			outcome.Synthesis = "Full-deck synthesis unavailable: no jury seat returned a valid scorecard for every rendered page."
		} else {
			synthesisTask := fmt.Sprintf(
				"Synthesize the complete %d-page slide jury for \"%s\". Every scorecard below has already been validated against exact global page coverage across %d bounded image batches. Return one full-deck merged scoreboard covering every page; name the consensus weakest_three and strongest_three.\n\nThe jury's complete scorecards:\n\n%s",
				len(assets), deckTitle, len(records), strings.TrimSpace(replies.String()))
			synthesisEngine := newGoalEngine(app)
			synthesis, err := synthesisEngine.callReviewModel(ctx, slideJurySynthesisSystem, synthesisTask)
			if err != nil {
				return meetingMemoryEntry{}, fmt.Errorf("full-deck slide jury synthesis: %w", err)
			}
			outcome.Synthesis = strings.TrimSpace(synthesis)
		}
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
		"artifactContract":    slideJuryContract,
		"type":                artifactTypeMarkdown,
		"source":              slideJurySource,
		"deckArtifactId":      artifact.ID,
		"deckArtifactVersion": strconv.Itoa(artifactVersion(artifact)),
		"deckContentDigest":   artifactCapabilityDigest(artifact),
		"juryBatchCount":      strconv.Itoa(len(records)),
		"juriedPageCount":     strconv.Itoa(len(assets)),
		"pageCoverage":        fmt.Sprintf("%d/%d", len(assets), len(assets)),
	}
	readiness := evaluateSlideJuryReadiness(outcome.Voices, len(assets))
	metadata["reviewVerdict"] = readiness.Verdict
	metadata["blockingPages"] = slideJuryPageList(readiness.BlockingPages)
	metadata["minimumAverage"] = strconv.FormatFloat(readiness.MinimumAverage, 'f', 2, 64)
	metadata["parsedSeats"] = strconv.Itoa(readiness.ParsedSeats)
	repairsForMetadata := readiness.Repairs
	if repairsForMetadata == nil {
		repairsForMetadata = []slideJuryRepair{}
	}
	if repairs, marshalErr := json.Marshal(repairsForMetadata); marshalErr == nil {
		metadata["repairFixes"] = string(repairs)
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
