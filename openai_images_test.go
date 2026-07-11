package main

// Tests for the gpt-image-2 imagery stage (openai_images.go): the request
// shape + b64→blob round trip against a fake API, the keyless clear-error
// contract, the imagery LAW riding every generated prompt, the standalone
// board runner filing an imagery_board_v1 artifact with kind=image assets,
// the hidden-until-proven imagery_board tool contract + rubric (mirroring
// TestToolRegistryChecklistEvals, which the entry deliberately sits outside
// of), and the Wave 5 store-split assessment carrying its verdict line.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// withFakeImagesAPI points openAIImagesURL at a fake server for one test.
func withFakeImagesAPI(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	original := openAIImagesURL
	openAIImagesURL = server.URL
	t.Cleanup(func() {
		openAIImagesURL = original
		server.Close()
	})
}

// The provider's core contract: the request carries model gpt-image-2 (the
// founder decision), the prompt, n=1, and the size/quality knobs with the
// realtime key as the bearer; the b64 response decodes, stores via putBlob,
// and round-trips byte-exact with the mime the response's output_format
// declared.
func TestCreateOpenAIImageRequestShapeAndBlobRoundTrip(t *testing.T) {
	setupIsolatedBlobStore(t)
	t.Setenv("OPENAI_API_KEY", "test-image-key")
	t.Setenv("OPENAI_IMAGE_MODEL", "")

	imageBytes := []byte("\x89PNG\r\n\x1a\nnot-really-a-png-but-the-bytes-are-the-contract")
	var captured openAIImagePayload
	var gotAuth, gotMethod string
	withFakeImagesAPI(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request payload: %v", err)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data":          []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(imageBytes)}},
			"output_format": "png",
		})
	})

	ref, mime, err := createOpenAIImage(context.Background(), "a harbor at golden hour", openAIImageOptions{Size: "1024x1024", Quality: "medium"})
	if err != nil {
		t.Fatalf("createOpenAIImage: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("method=%q, want POST", gotMethod)
	}
	if gotAuth != "Bearer test-image-key" {
		t.Fatalf("authorization=%q, want the OPENAI_API_KEY bearer", gotAuth)
	}
	if captured.Model != "gpt-image-2" {
		t.Fatalf("model=%q, want the founder-decided gpt-image-2 default", captured.Model)
	}
	if captured.Prompt != "a harbor at golden hour" {
		t.Fatalf("prompt=%q did not reach the request", captured.Prompt)
	}
	if captured.N != 1 || captured.Size != "1024x1024" || captured.Quality != "medium" {
		t.Fatalf("request knobs=%+v, want n=1 size=1024x1024 quality=medium", captured)
	}

	if mime != "image/png" {
		t.Fatalf("mime=%q, want image/png per the response output_format", mime)
	}
	if !validBlobRef(ref) {
		t.Fatalf("ref=%q, want a content-addressed blob ref", ref)
	}
	stored, meta, err := getBlob(ref)
	if err != nil {
		t.Fatalf("getBlob: %v", err)
	}
	if !bytes.Equal(stored, imageBytes) {
		t.Fatal("stored blob bytes differ from the decoded b64 payload")
	}
	if meta.Mime != "image/png" {
		t.Fatalf("pinned blob mime=%q, want image/png", meta.Mime)
	}
}

// W0-5 lane metering (seat images): each generation call records one ledger
// row — token usage decoded off the response, est cost computed from the
// text/image input split via the pricing table — and a failed call records
// with Error stamped.
func TestCreateOpenAIImageRecordsUsage(t *testing.T) {
	setupIsolatedBlobStore(t)
	ledgerDir := ledgerTestDir(t)
	fixed := time.Date(2026, time.July, 11, 21, 0, 0, 0, time.UTC)
	prevNow := usageLedgerNow
	usageLedgerNow = func() time.Time { return fixed }
	defer func() { usageLedgerNow = prevNow }()

	t.Setenv("OPENAI_API_KEY", "test-image-key")
	t.Setenv("OPENAI_IMAGE_MODEL", "")

	imageBytes := []byte("\x89PNG\r\n\x1a\nmetered-plate")
	var modeMu sync.Mutex
	failNext := false
	setFailNext := func(fail bool) {
		modeMu.Lock()
		failNext = fail
		modeMu.Unlock()
	}
	withFakeImagesAPI(t, func(w http.ResponseWriter, r *http.Request) {
		modeMu.Lock()
		fail := failNext
		modeMu.Unlock()
		if fail {
			http.Error(w, `{"error":{"message":"synthetic image outage"}}`, http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data":          []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(imageBytes)}},
			"output_format": "png",
			"usage": map[string]any{
				"input_tokens":  60,
				"output_tokens": 4160,
				"input_tokens_details": map[string]any{
					"text_tokens":  50,
					"image_tokens": 10,
				},
			},
		})
	})

	if _, _, err := createOpenAIImage(context.Background(), "a metered harbor plate", openAIImageOptions{}); err != nil {
		t.Fatalf("createOpenAIImage: %v", err)
	}
	setFailNext(true)
	if _, _, err := createOpenAIImage(context.Background(), "a failing plate", openAIImageOptions{}); err == nil {
		t.Fatal("synthetic outage must error")
	}

	rows := readLedgerLines(t, filepath.Join(ledgerDir, "usage-2026-07-11.jsonl"))
	if len(rows) != 2 {
		t.Fatalf("usage rows = %d, want one per generation call", len(rows))
	}
	metered := rows[0]
	if metered["provider"] != providerOpenAI || metered["model"] != "gpt-image-2" || metered["seat"] != seatImages {
		t.Fatalf("image row identity wrong: %v", metered)
	}
	if got := metered["input_tokens"].(float64); got != 60 {
		t.Fatalf("input_tokens = %v, want the reported total 60", got)
	}
	if got := metered["output_tokens"].(float64); got != 4160 {
		t.Fatalf("output_tokens = %v, want 4160", got)
	}
	// Split-priced: 50 text at $5/MTok + 10 image at $8/MTok + 4160 out at $30/MTok.
	wantCost := 50.0/1e6*5 + 10.0/1e6*8 + 4160.0/1e6*30
	if got := metered["est_cost_usd"].(float64); !floatClose(got, wantCost) {
		t.Fatalf("est_cost_usd = %v, want %v", got, wantCost)
	}
	if _, present := metered["estimated"]; present {
		t.Fatalf("wire-reported usage must not flag estimated: %v", metered)
	}
	failed := rows[1]
	if errText, _ := failed["error"].(string); !strings.Contains(errText, "api request failed") {
		t.Fatalf("failed row must carry the wire error: %v", failed)
	}
	if failed["estimated"] != true {
		t.Fatalf("a no-usage failure row must flag estimated: %v", failed)
	}
}

// TestOpenAIImageModelEnvOverride pins the OPENAI_IMAGE_MODEL fallback seam.
func TestOpenAIImageModelEnvOverride(t *testing.T) {
	t.Setenv("OPENAI_IMAGE_MODEL", "gpt-image-2-mini")
	if got := openAIImageModel(); got != "gpt-image-2-mini" {
		t.Fatalf("openAIImageModel()=%q, want the env override", got)
	}
	t.Setenv("OPENAI_IMAGE_MODEL", "  ")
	if got := openAIImageModel(); got != defaultOpenAIImageModel {
		t.Fatalf("openAIImageModel()=%q, want the %s default", got, defaultOpenAIImageModel)
	}
}

// Keyless: both the provider and the board runner return a clear
// OPENAI_API_KEY error — no crash, no request, no half-filed board.
func TestOpenAIImageKeylessClearErrorNeverACrash(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("OPENAI_API_KEY", "")
	withFakeImagesAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("keyless run must never reach the API")
	})

	if _, _, err := createOpenAIImage(context.Background(), "anything", openAIImageOptions{}); err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("createOpenAIImage keyless err=%v, want a clear OPENAI_API_KEY error", err)
	}

	before := len(app.osArtifactsSnapshot(0))
	_, _, err := app.runImageryBoard(context.Background(), imageryBoardInput{
		Title:        "Keyless board",
		VisualSystem: "deep warm blacks, bone-white highlights",
		Shots:        []imageryShot{{Title: "Cover", Description: "the team celebrating", Temperature: "joy"}},
	})
	if err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("runImageryBoard keyless err=%v, want a clear OPENAI_API_KEY error", err)
	}
	if after := len(app.osArtifactsSnapshot(0)); after != before {
		t.Fatalf("keyless board filed an artifact (%d -> %d), want none", before, after)
	}
}

// The imagery LAW rides EVERY generated prompt: the one visual system block,
// the named emotional temperature, the geography honesty floor, the
// real-place-by-name instruction when the place is the claim, and the
// duotone-stays-in-CSS rule (natural color out of the model).
func TestImageryShotPromptCarriesTheLawOnEveryShot(t *testing.T) {
	visualSystem := "deep warm blacks, bone-white highlights, single red accent, shot on 35mm"
	shots := []imageryShot{
		{Title: "The party", Description: "a rooftop crowd mid-laugh, hats in the air", Temperature: "joy"},
		{Title: "The proof", Description: "the product on a workbench under one hard light", Temperature: "drama"},
		{Title: "The harbor", Description: "container cranes at dawn", Temperature: "drama", Place: "the Port of Los Angeles"},
		{Title: "The room", Description: "a packed screening room leaning in", Temperature: "joy"},
	}
	for _, shot := range shots {
		prompt := imageryShotPrompt(visualSystem, shot)
		if !strings.Contains(prompt, imageryLawSystemHeader) || !strings.Contains(prompt, visualSystem) {
			t.Fatalf("shot %q prompt is missing the one visual system block:\n%s", shot.Title, prompt)
		}
		if !strings.Contains(prompt, "Emotional temperature: "+shot.Temperature) {
			t.Fatalf("shot %q prompt does not NAME its emotional temperature:\n%s", shot.Title, prompt)
		}
		if !strings.Contains(prompt, imageryLawGeography) {
			t.Fatalf("shot %q prompt dropped the geography honesty law:\n%s", shot.Title, prompt)
		}
		if !strings.Contains(prompt, imageryLawNoDuotone) {
			t.Fatalf("shot %q prompt dropped the duotone-stays-in-CSS rule:\n%s", shot.Title, prompt)
		}
	}
	// Real place by name, only when the place is the claim.
	withPlace := imageryShotPrompt(visualSystem, shots[2])
	if !strings.Contains(withPlace, "the Port of Los Angeles") || !strings.Contains(withPlace, "The place is the claim") {
		t.Fatalf("place-claim shot prompt does not ask for the real place by name:\n%s", withPlace)
	}
	if strings.Contains(imageryShotPrompt(visualSystem, shots[0]), "The place is the claim") {
		t.Fatal("a placeless shot must not carry the real-place instruction")
	}
}

// The standalone runner: 4 shots through the fake API file ONE
// imagery_board_v1 artifact whose body carries the contract headings, a
// "concept render" FIG caption + blob ref per shot, with every image attached
// as a kind=image asset — and the body passes its own contract law sweep.
func TestRunImageryBoardFilesArtifactWithConceptRenderAssets(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("OPENAI_API_KEY", "test-image-key")
	t.Setenv("OPENAI_IMAGE_MODEL", "")

	calls := 0
	withFakeImagesAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		unique := []byte(fmt.Sprintf("\x89PNG\r\n\x1a\nshot-%d", calls))
		json.NewEncoder(w).Encode(map[string]any{
			"data":          []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(unique)}},
			"output_format": "png",
		})
	})

	shots := []imageryShot{
		{Title: "The party", Description: "a rooftop crowd mid-laugh", Temperature: "joy"},
		{Title: "The proof", Description: "the product under one hard light", Temperature: "drama"},
		{Title: "The harbor", Description: "container cranes at dawn", Temperature: "drama", Place: "the Port of Los Angeles"},
		{Title: "The room", Description: "a packed screening room", Temperature: "joy"},
	}
	artifact, generated, err := app.runImageryBoard(context.Background(), imageryBoardInput{
		Title:        "Aurora imagery board",
		VisualSystem: "deep warm blacks, bone-white highlights, single red accent",
		Shots:        shots,
		CreatedBy:    "aj@shareability.com",
	})
	if err != nil {
		t.Fatalf("runImageryBoard: %v", err)
	}
	if calls != len(shots) {
		t.Fatalf("API called %d times, want once per shot (%d)", calls, len(shots))
	}
	// The runner returns every generated shot with its stable FIG (auto 1..N
	// here) and blob ref, so the studio's placement stage can inline them.
	if len(generated) != len(shots) {
		t.Fatalf("returned %d generated shots, want %d", len(generated), len(shots))
	}
	for i, g := range generated {
		if g.Fig != i+1 || g.Ref == "" || g.Mime != "image/png" {
			t.Fatalf("generated[%d]=%+v, want fig %d with a png blob ref", i, g, i+1)
		}
	}

	if artifact.Metadata["artifactContract"] != imageryBoardContract {
		t.Fatalf("contract=%q, want %s", artifact.Metadata["artifactContract"], imageryBoardContract)
	}
	if artifact.Metadata["toolTemplate"] != imageryBoardToolID {
		t.Fatalf("toolTemplate=%q, want %s", artifact.Metadata["toolTemplate"], imageryBoardToolID)
	}

	// The body carries the contract headings and a labeled FIG caption per
	// shot — and toolLawSweep (the mechanical gate) accepts it.
	for _, heading := range toolContractHeadings[imageryBoardContract] {
		if !strings.Contains(artifact.Text, heading) {
			t.Fatalf("board body missing contract heading %q", heading)
		}
	}
	for index, shot := range shots {
		caption := fmt.Sprintf("FIG. %d — %s (%s)", index+1, shot.Title, imageryConceptRenderLabel)
		if !strings.Contains(artifact.Text, caption) {
			t.Fatalf("board body missing the FIG caption %q", caption)
		}
	}
	if reason, violated := toolLawSweep(imageryBoardTool(), artifact.Text); violated {
		t.Fatalf("the filed board violates its own contract law sweep: %s", reason)
	}

	// Every generated image rides the artifact as a kind=image asset whose
	// blob ref both round-trips and appears in the body.
	assets := artifactAssets(artifact)
	if len(assets) != len(shots) {
		t.Fatalf("board carries %d assets, want %d", len(assets), len(shots))
	}
	for _, asset := range assets {
		if asset.Kind != "image" {
			t.Fatalf("asset kind=%q, want image", asset.Kind)
		}
		if asset.Mime != "image/png" {
			t.Fatalf("asset mime=%q, want image/png", asset.Mime)
		}
		if !strings.Contains(artifact.Text, asset.Ref) {
			t.Fatalf("body does not list blob ref %s", asset.Ref)
		}
		if _, _, err := getBlob(asset.Ref); err != nil {
			t.Fatalf("asset blob %s unreadable: %v", asset.Ref, err)
		}
	}
}

// The art director's stable FIG numbers ride through generation: the returned
// shots, the board's FIG captions, and the asset names all carry the director's
// number (not a re-sequenced index), so the studio's placement stage can inline
// each image at the exact .fig-N slot the writer built.
func TestRunImageryBoardHonorsStableFig(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("OPENAI_API_KEY", "test-image-key")
	t.Setenv("OPENAI_IMAGE_MODEL", "")

	calls := 0
	withFakeImagesAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		unique := []byte(fmt.Sprintf("\x89PNG\r\n\x1a\nshot-%d", calls))
		json.NewEncoder(w).Encode(map[string]any{
			"data":          []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(unique)}},
			"output_format": "png",
		})
	})

	artifact, generated, err := app.runImageryBoard(context.Background(), imageryBoardInput{
		Title:        "Stable-fig board",
		VisualSystem: "one coherent system",
		Shots: []imageryShot{
			{Fig: 3, Title: "A", Description: "x", Temperature: "joy"},
			{Fig: 7, Title: "B", Description: "y", Temperature: "drama"},
		},
	})
	if err != nil {
		t.Fatalf("runImageryBoard: %v", err)
	}
	if len(generated) != 2 || generated[0].Fig != 3 || generated[1].Fig != 7 {
		t.Fatalf("stable figs not honored in the returned shots: %+v", generated)
	}
	if !strings.Contains(artifact.Text, "FIG. 3 —") || !strings.Contains(artifact.Text, "FIG. 7 —") {
		t.Fatalf("board body did not use the director's stable FIG numbers:\n%s", artifact.Text)
	}
	names := ""
	for _, a := range artifactAssets(artifact) {
		names += a.Name + " "
	}
	if !strings.Contains(names, "imagery-fig-03") || !strings.Contains(names, "imagery-fig-07") {
		t.Fatalf("asset names did not use the stable fig: %q", names)
	}
}

// The imagery_board tool contract + rubric, pinned exactly like the checklist
// evals pin the 12 — plus the hidden-until-proven placement: UNREACHABLE from
// every launch door (toolByID must NOT resolve it — a toolByID launch would run
// the generic text pipeline and file a fabricated generation record with no
// image ever created), absent from packagingTools() and from every palette
// group buildToolsPayload serves (those stay the proven 12-tool menu).
func TestImageryBoardToolContractAndRubricPinned(t *testing.T) {
	if _, ok := toolByID("imagery_board"); ok {
		t.Fatal("imagery_board resolves through toolByID — launching it would run the text pipeline and fabricate a generation record; it must stay unreachable until the launch routes through runImageryBoard")
	}
	if normalizeToolTemplate("imagery_board") != "" {
		t.Fatal("normalizeToolTemplate must not canonicalize the unwired imagery_board")
	}
	tool := imageryBoardTool()
	if tool.Group != toolGroupPackage {
		t.Fatalf("group=%q, want %s", tool.Group, toolGroupPackage)
	}
	if tool.Contract != imageryBoardContract {
		t.Fatalf("contract=%q, want %s", tool.Contract, imageryBoardContract)
	}
	if tool.Authority != toolAuthorityWorkspaceWrite || tool.ExternalWriteGated {
		t.Fatalf("authority=%q gated=%v, want plain workspace_write", tool.Authority, tool.ExternalWriteGated)
	}
	validStage := map[string]bool{}
	for _, s := range packageStages {
		validStage[s] = true
	}
	if len(tool.Stages) == 0 {
		t.Fatal("tool serves no package stage")
	}
	for _, s := range tool.Stages {
		if !validStage[s] {
			t.Fatalf("stage %q is not a package stage", s)
		}
	}
	if tool.InputMode != toolInputForm {
		t.Fatalf("inputMode=%q, want form", tool.InputMode)
	}
	if n := len(tool.FormFields); n < 1 || n > 3 {
		t.Fatalf("form tool has %d fields, want 1-3", n)
	}
	for _, f := range tool.FormFields {
		if strings.TrimSpace(f.Key) == "" || strings.TrimSpace(f.Label) == "" {
			t.Fatalf("form field malformed: %+v", f)
		}
	}

	// Rubric shape, mirroring the checklist evals.
	if tool.Rubric.Ref != "imagery_board_gate_v1" {
		t.Fatalf("rubric ref=%q, want imagery_board_gate_v1", tool.Rubric.Ref)
	}
	if n := len(tool.Rubric.Dimensions); n < 3 || n > 5 {
		t.Fatalf("rubric has %d dimensions, want 3-5", n)
	}
	for _, d := range tool.Rubric.Dimensions {
		if strings.TrimSpace(d.Name) == "" || strings.TrimSpace(d.Measures) == "" {
			t.Fatalf("rubric dimension malformed: %+v", d)
		}
		if d.Bar < 1 || d.Bar > 10 {
			t.Fatalf("rubric dimension %q bar=%d out of 1..10", d.Name, d.Bar)
		}
	}
	if strings.TrimSpace(tool.Rubric.KillCondition) == "" || tool.KillCondition() != tool.Rubric.KillCondition {
		t.Fatal("kill condition missing or KillCondition() drifted from the rubric")
	}

	// The assembled prompt carries the contract headings, the LAW block, every
	// rubric dimension, and the kill-condition marker; the review instruction
	// carries the kill condition verbatim.
	prompt := assembleToolPrompt(tool, toolPromptContext{})
	for _, heading := range toolContractHeadings[tool.Contract] {
		if !strings.Contains(prompt, heading) {
			t.Fatalf("assembled prompt missing contract heading %q", heading)
		}
	}
	if !strings.Contains(prompt, "THE IMAGERY LAW") {
		t.Fatal("assembled prompt missing the imagery law block")
	}
	for _, d := range tool.Rubric.Dimensions {
		if !strings.Contains(prompt, d.Name) {
			t.Fatalf("assembled prompt missing rubric dimension %q", d.Name)
		}
	}
	if !strings.Contains(prompt, "kill_condition") {
		t.Fatal("assembled prompt missing the kill_condition marker")
	}
	if !strings.Contains(toolReviewInstruction(tool), tool.Rubric.KillCondition) {
		t.Fatal("review instruction dropped the kill condition")
	}

	// Hidden-until-proven: NOT in packagingTools() (the checklist evals pin
	// that menu at 12) and NOT served by any palette group yet.
	for _, other := range packagingTools() {
		if other.ID == tool.ID {
			t.Fatal("imagery_board leaked into packagingTools() — the proven 12-tool menu")
		}
	}
	for _, group := range buildToolsPayload() {
		for _, served := range group.Tools {
			if served.ID == tool.ID {
				t.Fatalf("imagery_board is served in palette group %q — it is hidden until proven", group.ID)
			}
		}
	}
}

// The Wave 5 store-split assessment exists where the roadmap put it and
// carries an explicit verdict line (the analysis doc predicted defer).
func TestMemoryStoreSplitAssessmentFiledWithVerdict(t *testing.T) {
	raw, err := os.ReadFile("docs/plans/memory-store-split-assessment.md")
	if err != nil {
		t.Fatalf("read assessment: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "VERDICT:") {
		t.Fatal("assessment has no VERDICT: line")
	}
	if !strings.Contains(text, "VERDICT: DEFER") {
		t.Fatal("assessment verdict line does not state the defer-with-evidence call")
	}
	if !strings.Contains(text, "Re-open triggers") {
		t.Fatal("a defer verdict must name its re-open triggers")
	}
}
