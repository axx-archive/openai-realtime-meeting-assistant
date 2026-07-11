package main

// openai_images.go — the gpt-image-2 imagery stage (packaging OS §3, Wave 5;
// analysis doc item 21's "API image provider"). Founder decision: the OpenAI
// Images API on the EXISTING OPENAI_API_KEY — the same vendor and key as
// realtime, so a deploy that can talk gains imagery with zero new secrets.
//
// Two layers, deliberately NOT wired into packaging_studio.go or the goal
// engine yet:
//
//   createOpenAIImage  — one prompt → POST /v1/images/generations → the b64
//                        payload decoded and stored via putBlob (blobs.go),
//                        returning the content-addressed blob ref.
//   runImageryBoard    — the standalone helper the studio's NEXT revision
//                        calls: a visual system brief + shot descriptions →
//                        generated shots on ONE system → an imagery_board_v1
//                        artifact with the images attached as kind=image
//                        assets. Imagery is art-direction-heavy; the founder
//                        sees standalone output before anything rides the
//                        pipeline.
//
// THE IMAGERY LAW (the /packaging skill, stage 5) is encoded in
// imageryShotPrompt: one visual system block appended to EVERY shot, the
// emotional temperature named per shot, the real place asked for BY NAME when
// the place is the claim, and geography never invented. The duotone recipe is
// deliberately ABSENT from generation — it lives in the deck CSS, applied at
// render, never baked into the image.
//
// KEYLESS: no OPENAI_API_KEY → a clear error before any request, never a
// crash and never a half-filed board. Blob storage is pure disk (blobs.go),
// so nothing else degrades.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// defaultOpenAIImageModel is the founder-decided model; OPENAI_IMAGE_MODEL
	// overrides it (the OPENAI_BRAIN_MODEL precedent, openai_responses.go).
	defaultOpenAIImageModel = "gpt-image-2"

	// Landscape deck plates by default — the shape the 1920×1080 deck chassis
	// crops least. Quality "high": these are client-facing concept renders.
	defaultOpenAIImageSize    = "1536x1024"
	defaultOpenAIImageQuality = "high"

	// imageryBoardMaxShots bounds one board at the contract's ceiling (4-6
	// shots) so a runaway caller can never burn an unbounded image budget.
	// The floor stays at one: the whole point of the standalone helper is a
	// cheap founder proof before the contract-shaped 4-6 board.
	imageryBoardMaxShots = 6

	imageryBoardToolID   = "imagery_board"
	imageryBoardContract = "imagery_board_v1"

	// imageryConceptRenderLabel is the filed-exhibit label every generated
	// image carries in its FIG. caption — generated imagery is never passed
	// off as photography (the imagery law).
	imageryConceptRenderLabel = "concept render"
)

// openAIImagesURL is a package VAR where its Responses neighbor is a const:
// the round-trip test must exercise the real request encoding + decode path
// against a fake HTTP server, so the seam is the endpoint, not a responder
// stub.
var openAIImagesURL = "https://api.openai.com/v1/images/generations"

// openAIImageModel resolves the generation model: OPENAI_IMAGE_MODEL when
// set, else the founder-decided gpt-image-2.
func openAIImageModel() string {
	if model := strings.TrimSpace(os.Getenv("OPENAI_IMAGE_MODEL")); model != "" {
		return model
	}
	return defaultOpenAIImageModel
}

// openAIImageOptions are the per-call generation knobs; empty fields take the
// deck-plate defaults.
type openAIImageOptions struct {
	Size    string
	Quality string
}

// openAIImagePayload is the POST /v1/images/generations body.
type openAIImagePayload struct {
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
	N       int    `json:"n"`
	Size    string `json:"size,omitempty"`
	Quality string `json:"quality,omitempty"`
}

// openAIImageBody is the slice of the Images response this stage reads: the
// b64 payload, the emitted format, the usage block (W0-5 metering), and the
// error envelope.
type openAIImageBody struct {
	Data []struct {
		B64JSON string `json:"b64_json,omitempty"`
	} `json:"data,omitempty"`
	OutputFormat string `json:"output_format,omitempty"`
	// Usage: gpt-image responses report token usage with a text/image input
	// split; the ledger prices the split via models_pricing.go.
	Usage *struct {
		InputTokens        int64 `json:"input_tokens,omitempty"`
		OutputTokens       int64 `json:"output_tokens,omitempty"`
		InputTokensDetails struct {
			TextTokens  int64 `json:"text_tokens,omitempty"`
			ImageTokens int64 `json:"image_tokens,omitempty"`
		} `json:"input_tokens_details"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message,omitempty"`
	} `json:"error,omitempty"`
}

// openAIImageMime maps the response's declared output format — falling back
// to magic-byte sniffing, then PNG (the API's documented default) — to the
// mime putBlob pins and the blob route serves inline.
func openAIImageMime(outputFormat string, data []byte) string {
	switch strings.ToLower(strings.TrimSpace(outputFormat)) {
	case "png":
		return "image/png"
	case "jpeg", "jpg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	}
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG")):
		return "image/png"
	case bytes.HasPrefix(data, []byte("\xff\xd8\xff")):
		return "image/jpeg"
	}
	return "image/png"
}

// createOpenAIImage generates one image and stores it: POST the prompt to the
// Images API, decode the base64 payload, putBlob the bytes, return the
// content-addressed ref plus the pinned mime. Keyless returns the same clear
// error every OpenAI seam does — never a crash.
func createOpenAIImage(ctx context.Context, prompt string, opts openAIImageOptions) (ref string, mime string, err error) {
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return "", "", fmt.Errorf("OPENAI_API_KEY is not configured")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", "", fmt.Errorf("image prompt is empty")
	}

	payload := openAIImagePayload{
		Model:   openAIImageModel(),
		Prompt:  prompt,
		N:       1,
		Size:    firstNonEmptyString(strings.TrimSpace(opts.Size), defaultOpenAIImageSize),
		Quality: firstNonEmptyString(strings.TrimSpace(opts.Quality), defaultOpenAIImageQuality),
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return "", "", fmt.Errorf("encode OpenAI image request: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIImagesURL, bytes.NewReader(rawPayload))
	if err != nil {
		return "", "", fmt.Errorf("create OpenAI image request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")

	// W0-5 lane metering (seat images): one ledger row per generation call.
	// llmUsageEntry carries no image-token field, so InputTokens records the
	// TOTAL input tokens while EstCostUSD is computed HERE from the text/image
	// split (the ledger never recomputes a caller-supplied nonzero cost). A
	// response without a usage block records flagged Estimated; failures carry
	// Error.
	started := time.Now()
	var usage llmTokenUsage
	usageReported := false
	defer func() {
		entry := llmUsageEntry{
			Provider:     providerOpenAI,
			Model:        payload.Model,
			Seat:         seatImages,
			InputTokens:  usage.InputTokens + usage.ImageInputTokens,
			OutputTokens: usage.OutputTokens,
			DurationMS:   time.Since(started).Milliseconds(),
			Estimated:    !usageReported,
		}
		// Only the image-input tier needs the split priced here; text-only
		// usage recomputes correctly from the entry fields inside the ledger.
		if usageReported && usage.ImageInputTokens > 0 {
			if cost, priced := estimateCostUSD(payload.Model, usage); priced {
				entry.EstCostUSD = cost
			}
		}
		if err != nil {
			entry.Error = err.Error()
		}
		recordLLMUsage(entry)
	}()

	// Image generation is the slowest OpenAI call the OS makes; 120s is the
	// generous ceiling (the Responses neighbor runs text at 45s).
	response, err := (&http.Client{Timeout: 120 * time.Second}).Do(httpRequest)
	if err != nil {
		return "", "", fmt.Errorf("create OpenAI image: %w", err)
	}
	defer response.Body.Close()

	// A b64-encoded image runs ~4/3 of its byte size; 48MB of body headroom
	// keeps a high-quality plate under the blob store's own 64MB cap.
	rawBody, err := io.ReadAll(io.LimitReader(response.Body, 48<<20))
	if err != nil {
		return "", "", fmt.Errorf("read OpenAI image response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", "", apiRequestFailedError("OpenAI image generation failed", response.Status, rawBody)
	}

	var body openAIImageBody
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return "", "", fmt.Errorf("decode OpenAI image response: %w", err)
	}
	if body.Usage != nil {
		usageReported = true
		textTokens := body.Usage.InputTokensDetails.TextTokens
		imageTokens := body.Usage.InputTokensDetails.ImageTokens
		if textTokens == 0 && imageTokens == 0 {
			// No split reported: bill everything at the text-input tier.
			textTokens = body.Usage.InputTokens
		}
		usage = llmTokenUsage{
			InputTokens:      textTokens,
			ImageInputTokens: imageTokens,
			OutputTokens:     body.Usage.OutputTokens,
		}
	}
	if body.Error != nil && strings.TrimSpace(body.Error.Message) != "" {
		return "", "", fmt.Errorf("OpenAI image error: %s", strings.TrimSpace(body.Error.Message))
	}
	if len(body.Data) == 0 || strings.TrimSpace(body.Data[0].B64JSON) == "" {
		return "", "", fmt.Errorf("OpenAI image response carried no image data")
	}

	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body.Data[0].B64JSON))
	if err != nil {
		return "", "", fmt.Errorf("decode OpenAI image payload: %w", err)
	}
	mime = openAIImageMime(body.OutputFormat, data)
	ref, err = putBlob(data, mime)
	if err != nil {
		return "", "", fmt.Errorf("store generated image: %w", err)
	}
	return ref, mime, nil
}

// --- The imagery LAW as a prompt template ------------------------------------

// The law lines every generated prompt carries (/packaging skill, stage 5).
// Imagery makes claims exactly like copy does: a coastline behind an inland
// city reads as a lie a sharp room notices silently.
const (
	// imageryLawSystemHeader opens the one visual system block appended to
	// EVERY shot on a board — never restyled per shot.
	imageryLawSystemHeader = "VISUAL SYSTEM (identical for every shot on this board — never restyle per shot):"

	// imageryLawGeography is the honesty floor: no invented or relocated
	// geography, ever.
	imageryLawGeography = "Geography must be honest: never invent or relocate coastlines, skylines, mountains, or landmarks. If the setting is not specified, keep it unspecific."

	// imageryLawNoDuotone keeps the duotone recipe where it belongs — the deck
	// CSS. Generation stays natural so one CSS dial unifies every plate.
	imageryLawNoDuotone = "Render in natural full color and tone. Do NOT apply a duotone, monochrome, or brand-color wash — the duotone treatment is applied later in the deck's CSS, never baked into the image."
)

// imageryShot is one board entry: what the shot depicts, its named emotional
// temperature (the law: drama where the product speaks, joy where the culture
// speaks — never unnamed), and optionally the real place by name when the
// place is the claim.
type imageryShot struct {
	Fig         int    // stable FIG number from the art director (0 = auto-sequential)
	Title       string // short FIG-caption title
	Description string // what the shot depicts
	Temperature string // the NAMED emotional temperature (drama, joy, ...)
	Place       string // the real place by name, when the place is the claim
}

// imageryGeneratedShot is one successfully generated shot the board returns to
// its caller: the stable FIG, the stored blob ref, and its mime. The studio's
// imagery-generation stage carries these forward so ship_compile can inline each
// image at its directed .fig-N slot as a data: URI.
type imageryGeneratedShot struct {
	Fig  int
	Ref  string
	Mime string
}

// imageryShotPrompt renders one shot's generation prompt under the imagery
// law: description first, the named emotional temperature, the real-place
// instruction when the place is the claim, the geography floor, then the ONE
// visual system block and the no-duotone rule — identical suffix on every
// shot of a board.
func imageryShotPrompt(visualSystem string, shot imageryShot) string {
	lines := []string{
		strings.TrimSpace(shot.Description),
		"Emotional temperature: " + strings.TrimSpace(shot.Temperature) + ". Let it read through faces and body language, not just grading.",
	}
	if place := strings.TrimSpace(shot.Place); place != "" {
		lines = append(lines, "The place is the claim: depict the real "+place+", by name, as it actually looks.")
	}
	lines = append(lines,
		imageryLawGeography,
		imageryLawSystemHeader+" "+strings.TrimSpace(visualSystem),
		imageryLawNoDuotone,
	)
	return strings.Join(lines, "\n")
}

// --- The standalone board runner ----------------------------------------------

// imageryBoardInput is everything runImageryBoard needs: the board title, the
// one visual system brief, the shots, and the filing facts.
type imageryBoardInput struct {
	Title        string
	VisualSystem string
	Shots        []imageryShot
	PackageID    string
	CreatedBy    string
	Size         string
	Quality      string
}

// runImageryBoard is the exported helper the studio's next revision calls (it
// is wired into NO pipeline yet — the founder sees standalone output first):
// generate every shot on the one visual system, store each image as a blob,
// file ONE imagery_board_v1 artifact whose body lists each shot with its blob
// ref under a "concept render" FIG-caption label, and attach the images as
// kind=image assets. A failed shot is DISCLOSED in the generation record and
// the board files without it; zero generated shots is an error, and keyless
// errors clearly before any request.
func (app *kanbanBoardApp) runImageryBoard(ctx context.Context, in imageryBoardInput) (meetingMemoryEntry, []imageryGeneratedShot, error) {
	if app == nil || app.memory == nil {
		return meetingMemoryEntry{}, nil, fmt.Errorf("artifact memory is unavailable")
	}
	// Keyless: fail clearly BEFORE any per-shot work, so a keyless deploy never
	// files a half-board.
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		return meetingMemoryEntry{}, nil, fmt.Errorf("OPENAI_API_KEY is not configured — the imagery board cannot generate")
	}
	visualSystem := strings.TrimSpace(in.VisualSystem)
	if visualSystem == "" {
		return meetingMemoryEntry{}, nil, fmt.Errorf("the imagery board needs a visual system brief — one system unifies every shot")
	}
	if len(in.Shots) == 0 {
		return meetingMemoryEntry{}, nil, fmt.Errorf("the imagery board needs shot descriptions (the contract asks for 4-6)")
	}
	if len(in.Shots) > imageryBoardMaxShots {
		return meetingMemoryEntry{}, nil, fmt.Errorf("the imagery board caps at %d shots, got %d", imageryBoardMaxShots, len(in.Shots))
	}
	for index, shot := range in.Shots {
		if strings.TrimSpace(shot.Description) == "" {
			return meetingMemoryEntry{}, nil, fmt.Errorf("shot %d has no description", index+1)
		}
		if strings.TrimSpace(shot.Temperature) == "" {
			// The law: the emotional temperature is NAMED per shot, never implied.
			return meetingMemoryEntry{}, nil, fmt.Errorf("shot %d names no emotional temperature (the imagery law: drama where the product speaks, joy where the culture speaks)", index+1)
		}
	}

	opts := openAIImageOptions{Size: in.Size, Quality: in.Quality}
	type generatedShot struct {
		shot   imageryShot
		prompt string
		ref    string
		mime   string
		fig    int
	}
	generated := make([]generatedShot, 0, len(in.Shots))
	autoFig := 0
	var failures []string
	for index, shot := range in.Shots {
		prompt := imageryShotPrompt(visualSystem, shot)
		ref, mime, err := createOpenAIImage(ctx, prompt, opts)
		if err != nil {
			// Disclosed, never silent: the board files with the gap named.
			failures = append(failures, fmt.Sprintf("FIG. %d (%s): %s", firstPositiveInt(shot.Fig, index+1), firstNonEmptyString(strings.TrimSpace(shot.Title), "untitled"), compactAssistantLine(err.Error())))
			continue
		}
		// A stable FIG identity: the art director's number when given, else the
		// running sequence — so downstream placement (.fig-N) never guesses.
		fig := shot.Fig
		if fig <= 0 {
			autoFig++
			fig = autoFig
		}
		generated = append(generated, generatedShot{shot: shot, prompt: prompt, ref: ref, mime: mime, fig: fig})
	}
	if len(generated) == 0 {
		return meetingMemoryEntry{}, nil, fmt.Errorf("no shots generated: %s", strings.Join(failures, "; "))
	}

	// The body emits the imagery_board_v1 contract headings exactly
	// (toolContractHeadings — toolLawSweep checks them mechanically).
	lines := []string{
		"## Visual system",
		visualSystem,
		"",
		"This block rides EVERY shot prompt verbatim (the imagery law: one system). The duotone recipe stays in the deck CSS — no treatment is baked into generation.",
		"",
		"## Shot list",
	}
	for _, item := range generated {
		title := firstNonEmptyString(strings.TrimSpace(item.shot.Title), "untitled shot")
		lines = append(lines,
			"",
			fmt.Sprintf("### FIG. %d — %s (%s)", item.fig, title, imageryConceptRenderLabel),
			"- Emotional temperature: "+strings.TrimSpace(item.shot.Temperature),
		)
		if place := strings.TrimSpace(item.shot.Place); place != "" {
			lines = append(lines, "- Place (real, by name): "+place)
		}
		lines = append(lines,
			"- Image blob ref: "+item.ref+" ("+item.mime+")",
			"- Generation prompt:",
			"  "+strings.ReplaceAll(item.prompt, "\n", "\n  "),
		)
	}
	lines = append(lines,
		"",
		"## Generation record",
		fmt.Sprintf("- Model %s, size %s, quality %s.", openAIImageModel(), firstNonEmptyString(strings.TrimSpace(in.Size), defaultOpenAIImageSize), firstNonEmptyString(strings.TrimSpace(in.Quality), defaultOpenAIImageQuality)),
		fmt.Sprintf("- %d of %d shots generated.", len(generated), len(in.Shots)),
	)
	if len(failures) == 0 {
		lines = append(lines, "- No failures.")
	} else {
		lines = append(lines, "- Disclosed failures:")
		for _, failure := range failures {
			lines = append(lines, "  - "+failure)
		}
	}

	createdBy := firstNonEmptyString(strings.TrimSpace(in.CreatedBy), scoutParticipantName)
	metadata := map[string]string{
		"artifactContract": imageryBoardContract,
		"toolTemplate":     imageryBoardToolID,
		"type":             artifactTypeMarkdown,
		"source":           "imagery_board",
	}
	if packageID := strings.TrimSpace(in.PackageID); packageID != "" {
		metadata["packageId"] = packageID
	}
	title := firstNonEmptyString(strings.TrimSpace(in.Title), "Imagery board")
	artifact, appended, err := app.createOSArtifactWithMetadata("design", title, strings.Join(lines, "\n"), createdBy, metadata)
	if err != nil {
		return meetingMemoryEntry{}, nil, fmt.Errorf("file imagery board: %w", err)
	}
	if !appended || strings.TrimSpace(artifact.ID) == "" {
		return meetingMemoryEntry{}, nil, fmt.Errorf("imagery board was not saved")
	}

	// Attach every generated image as a kind=image asset. An attach failure is
	// logged and disclosed by omission from the assets JSON, never fatal — the
	// body already carries the ref.
	out := make([]imageryGeneratedShot, 0, len(generated))
	for _, item := range generated {
		out = append(out, imageryGeneratedShot{Fig: item.fig, Ref: item.ref, Mime: item.mime})
		asset := artifactAsset{
			Ref:  item.ref,
			Mime: item.mime,
			Name: fmt.Sprintf("imagery-fig-%02d%s", item.fig, imageryAssetExtension(item.mime)),
			Kind: "image",
		}
		if updated, err := app.appendArtifactAsset(artifact.ID, asset); err != nil {
			log.Errorf("imagery board %s: attach image asset %s failed: %v", artifact.ID, item.ref, err)
		} else {
			artifact = updated
		}
	}

	// The bidirectional package link, the fileStudioShipDeliverables posture:
	// a missing package is logged, never fatal — the board is filed either way.
	if packageID := strings.TrimSpace(in.PackageID); packageID != "" {
		if _, err := app.attachToPackage(packageID, packageRefTypeArtifact, artifact.ID, createdBy); err != nil {
			log.Errorf("imagery board %s: attach to package %s failed: %v", artifact.ID, packageID, err)
		}
	}
	return artifact, out, nil
}

// imageryAssetExtension picks the asset filename extension from the pinned
// mime; unknown mimes fall back to .png (openAIImageMime's own default).
func imageryAssetExtension(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

// firstPositiveInt returns the first argument that is > 0, else the fallback —
// the stable-FIG helper: the art director's number when given, else a sequence.
func firstPositiveInt(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

// blobDataURI reads a stored image blob and renders it as a base64 data: URI —
// the ONLY image form the render CSP (img-src data:) admits into a
// self-contained deck. mime falls back to PNG when unknown.
func blobDataURI(ref string, mime string) (string, error) {
	data, _, err := getBlob(ref)
	if err != nil {
		return "", err
	}
	mime = strings.TrimSpace(mime)
	if mime == "" {
		mime = "image/png"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}
