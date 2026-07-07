package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Card 079 — background blur as a sixth "look" riding the existing VideoLookPipeline via
// vendored MediaPipe person segmentation. These markers pin every load-bearing seam the
// spec calls out: the chip + v8 enum, the insertable-tier-only guard, the mask cadence,
// the governor shedding SEGMENTATION before the look (the remote-tile-flicker lesson), the
// segmenter teardown, the mask-aware shader, the honest "no fake whole-frame blur" CSS
// fallback, the lazy-loaded vendored assets, and the long-lived cache header. A refactor
// that drops any of them fails CI.

func readBlurSegmenter(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("public/video-looks/blur-segmenter.js")
	if err != nil {
		t.Fatalf("read blur-segmenter.js: %v", err)
	}
	return string(raw)
}

func TestVideoBlurChipAndEnumWiredIntoPicker(t *testing.T) {
	html := readIndexForLooks(t)

	// The sixth chip exists next to the grading looks.
	if !strings.Contains(html, `data-look="blur"`) {
		t.Errorf("index.html missing the blur chip (data-look=\"blur\")")
	}
	// v8 persistence accepts blur (setVideoLook / saveAudioSettings then carry it for free).
	if !strings.Contains(html, "['none', 'bonfire-warm', 'studio', 'mono', 'lowlight', 'blur'].includes(source.look)") {
		t.Errorf("normalizeVideoSettings enum must include 'blur'")
	}
	// Slider read-out knows the preset.
	if !strings.Contains(html, "case 'blur': return { uBrightness: 0, uTemperature: 0 }") {
		t.Errorf("videoLookPresetUniforms must handle 'blur'")
	}
	// Battery note shows for blur on EVERY device (segmentation is costly), not just iOS.
	status := functionBody(html, "function renderVideoLookStatus(look)")
	if status == "" {
		t.Fatalf("could not extract renderVideoLookStatus body")
	}
	if !strings.Contains(status, "look === 'blur'") {
		t.Errorf("renderVideoLookStatus must surface the battery note for blur on all devices")
	}
}

func TestVideoBlurLiveChangeRebuildsAcrossSegmentBoundary(t *testing.T) {
	html := readIndexForLooks(t)

	// A blur↔look transition is NOT a uniform swap: it must rebuild the pipeline so the
	// segmenter actually initializes (else the chip claims Active with no blur).
	body := functionBody(html, "async function applyLiveVideoLookChange()")
	if body == "" {
		t.Fatalf("could not extract applyLiveVideoLookChange body")
	}
	for _, want := range []string{
		"crossesSegmentBoundary",
		"lookMod.isSegmentedLook(localVideoLookPipeline.look) !== lookMod.isSegmentedLook(look)",
		// Return to the raw camera before teardown so a failed rebuild never freezes a tile.
		"swapOutboundVideoTrack(priorRaw, { stopPrevious: true })",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("applyLiveVideoLookChange missing blur-boundary marker: %q", want)
		}
	}
}

func TestVideoBlurPipelineSegmentationSeams(t *testing.T) {
	js := readPipelineModule(t)

	for _, want := range []string{
		// Blur is a segmented look, gated to the insertable tier this wave.
		"export const SEGMENTED_LOOKS = new Set(['blur'])",
		"export function isSegmentedLook(",
		"uBlurRadius: 14, uMaskFeather: 0.1",
		"throw new Error('blur requires insertable streams')",
		// Heavy MediaPipe wasm is lazy — imported only when blur actually starts.
		"const { createBlurSegmenter } = await import('./blur-segmenter.js')",
		// Mask cadence: segment every Nth frame, reuse between (const drives the governor).
		"const MASK_INTERVAL_FRAMES = 2",
		"this._segmenter.segment(frame)",
		"(this._frameCount % this._maskInterval) === 0",
		// Mask uploaded to unit 1 as a filterable R8 texture.
		"gl.texImage2D(gl.TEXTURE_2D, 0, gl.R8, mask.width, mask.height, 0, gl.RED, gl.UNSIGNED_BYTE, mask.data)",
		"gl.uniform1i(this._uniformLocations.uMask, 1)",
		// Teardown closes the segmenter (no leaked MediaPipe graph / wasm heap).
		"this._segmenter.close()",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("video-look-pipeline.js missing blur seam: %q", want)
		}
	}

	// Governor stage 1 sheds SEGMENTATION COST (mask cadence + radius) BEFORE the grading
	// looks' intensity ease-off — this is what keeps blur off the encoder-starvation cliff.
	esc := functionBody(js, "_escalateGovernor() {")
	if esc == "" {
		t.Fatalf("could not extract _escalateGovernor body")
	}
	cadence := strings.Index(esc, "this._maskInterval = Math.min(8, this._maskInterval * 2)")
	intensity := strings.Index(esc, "this._effectiveIntensity = 0")
	if cadence == -1 {
		t.Errorf("_escalateGovernor must halve the blur mask cadence at stage 1")
	}
	if intensity == -1 {
		t.Errorf("_escalateGovernor must still zero intensity for the grading looks")
	}
	if cadence != -1 && intensity != -1 && !(cadence < intensity) {
		t.Errorf("_escalateGovernor must shed the blur mask cadence BEFORE the intensity path")
	}
	if !strings.Contains(esc, "isSegmentedLook(this.look)") {
		t.Errorf("_escalateGovernor stage 1 must branch on isSegmentedLook")
	}
}

func TestVideoBlurShaderMaskAndRawIdentity(t *testing.T) {
	frag := readFragShader(t)
	for _, want := range []string{
		"uniform sampler2D uMask",
		"uniform float uHasMask",
		"uniform float uBlurRadius",
		"uniform float uMaskFeather",
		// 12-tap Poisson-disk background blur (one of the pinned offsets + the /13 average).
		"vec2(-0.326, -0.406)",
		"base = mix(base, acc / 13.0, bg)",
		// The uBypass early-out (governor's zero-cost relief) stays untouched.
		"if (uBypass > 0.5 || uIntensity <= 0.001)",
		// The master-intensity mix lands on the RAW sample, so ease-off restores real camera.
		"fragColor = vec4(mix(raw, col, clamp(uIntensity, 0.0, 1.0)), 1.0)",
	} {
		if !strings.Contains(frag, want) {
			t.Errorf("look.frag missing blur shader marker: %q", want)
		}
	}
}

func TestVideoBlurSegmenterWrapperUsesVendoredMediaPipe(t *testing.T) {
	js := readBlurSegmenter(t)
	for _, want := range []string{
		// Vendored, no CDN: bundle + wasm root + model all under /public/video-blur/.
		"const BUNDLE_URL = '/public/video-blur/vision_bundle.mjs'",
		"const WASM_ROOT = '/public/video-blur'",
		"await import(BUNDLE_URL)",
		"FilesetResolver.forVisionTasks(WASM_ROOT)",
		// VIDEO mode, confidence masks, GPU delegate with a CPU retry.
		"runningMode: 'VIDEO'",
		"outputConfidenceMasks: true",
		"build('GPU')",
		"build('CPU')",
		"segmentForVideo(scratch, ts)",
		"getAsUint8Array()",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("blur-segmenter.js missing marker: %q", want)
		}
	}
}

func TestVideoBlurCssFallbackIsHonest(t *testing.T) {
	js := readPipelineModule(t)
	// cssFilterForLook('blur') must return 'none': CSS blur() defocuses the whole frame
	// (subject included), so there is no honest whole-frame approximation — never fake it.
	css := functionBody(js, "export function cssFilterForLook(look, intensity = 1)")
	if css == "" {
		t.Fatalf("could not extract cssFilterForLook body")
	}
	blur := strings.Index(css, "case 'blur':")
	if blur == -1 {
		t.Fatalf("cssFilterForLook must handle 'blur'")
	}
	// The next return after `case 'blur':` must be 'none' (no CSS filter chain).
	rest := css[blur:]
	if !strings.HasPrefix(strings.TrimSpace(firstReturn(rest)), "return 'none'") {
		t.Errorf("cssFilterForLook('blur') must return 'none' (honest — no fake whole-frame blur)")
	}
}

// firstReturn returns the substring beginning at the first `return` in s.
func firstReturn(s string) string {
	i := strings.Index(s, "return")
	if i == -1 {
		return ""
	}
	return s[i:]
}

func TestVideoBlurAssetsVendoredIntoRepo(t *testing.T) {
	// The insertable-tier slice ships the vendored MediaPipe payload so the app has no
	// runtime CDN dependency (Dockerfile copies public/ wholesale, deploy is rsync).
	cases := []struct {
		path    string
		minSize int64
	}{
		{"public/video-blur/vision_bundle.mjs", 10_000},
		{"public/video-blur/vision_wasm_internal.js", 10_000},
		{"public/video-blur/vision_wasm_internal.wasm", 1_000_000},
		{"public/video-blur/selfie_segmenter.tflite", 50_000},
		{"public/video-blur/MEDIAPIPE_TASKS_COPYING.txt", 200},
	}
	for _, c := range cases {
		info, err := os.Stat(c.path)
		if err != nil {
			t.Errorf("vendored asset missing: %s (%v)", c.path, err)
			continue
		}
		if info.Size() < c.minSize {
			t.Errorf("vendored asset %s is %d bytes, want >= %d (truncated download?)", c.path, info.Size(), c.minSize)
		}
	}
}

func TestVideoBlurAssetsCachedLongLived(t *testing.T) {
	get := func(target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		publicAssetHandler(rec, req)
		return rec
	}

	// The ~9 MB blur payload is version-pinned + immutable → cache it hard, once.
	blur := get("/public/video-blur/selfie_segmenter.tflite")
	if cc := blur.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=604800") {
		t.Errorf("video-blur asset Cache-Control = %q, want a long max-age", cc)
	}

	// Everything else under /public keeps no-store (the look shader, code, etc.).
	shader := get("/public/video-looks/look.frag")
	if cc := shader.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("non-blur public asset Cache-Control = %q, want no-store", cc)
	}
}
