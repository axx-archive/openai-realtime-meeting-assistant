package main

import (
	"os"
	"strings"
	"testing"
)

// Wave 13 (Spectacular OS) — video "looks". These markers pin the far-end pipeline
// wiring, the three-tier fallback, the four looks, the thermal governor, and the
// settings picker's honest status + v8 persistence so a refactor that drops any of
// them fails CI. The load-bearing seams are scoped to their real function bodies
// (Wave 6 lesson: substring-anywhere can match dead code).

func TestVideoLookPipelineWiredIntoCaptureSeam(t *testing.T) {
	html := readIndexForLooks(t)

	// The look wrap lives inside createLocalMediaStream's real body, at the video
	// seam — not merely somewhere in the file.
	body := functionBody(html, "async function createLocalMediaStream(captureStream)")
	if body == "" {
		t.Fatalf("could not extract createLocalMediaStream body")
	}
	if !strings.Contains(body, "applyVideoLookToTracks(videoTracks)") {
		t.Errorf("createLocalMediaStream must wrap the video tracks through applyVideoLookToTracks")
	}

	// Look 'none' tears the pipeline down and returns the raw tracks untouched
	// (byte-identical to today — the happy path never regresses).
	apply := functionBody(html, "async function applyVideoLookToTracks(videoTracks)")
	if apply == "" {
		t.Fatalf("could not extract applyVideoLookToTracks body")
	}
	for _, want := range []string{
		"look === 'none'",
		"teardownVideoLookPipeline()",
		"return videoTracks",
		// Any pipeline exception falls back to the raw camera — never a black tile.
		"catch (error)",
	} {
		if !strings.Contains(apply, want) {
			t.Errorf("applyVideoLookToTracks body missing %q", want)
		}
	}

	// The outbound track truly reaches the PC: outboundTrackForKind reads the (now
	// processed) local video track, so the far end sees the look.
	if !strings.Contains(functionBody(html, "function outboundTrackForKind(kind)"), "outboundVideoTrack()") {
		t.Errorf("outboundTrackForKind must resolve video to outboundVideoTrack (the processed track)")
	}
}

func TestVideoLookThreeTierFallbackAndGovernor(t *testing.T) {
	js := readPipelineModule(t)

	for _, want := range []string{
		// Tier 1: insertable streams (far end sees it).
		"new MediaStreamTrackProcessor({ track: sourceTrack })",
		"new MediaStreamTrackGenerator({ kind: 'video' })",
		"new VideoFrame(this._canvas",
		"return 'insertable'",
		// Tier 2: canvas.captureStream (Safari desktop) — far end sees it, higher cost.
		"this._canvas.captureStream(30)",
		"return 'canvas'",
		// Tier 3: neither → preview only, honest status, caller keeps raw.
		"return 'none'",
		"Preview only — not supported on this browser",
		// Governor: intensity → look paused → signal audio, then auto-restore.
		"_escalateGovernor()",
		"_restoreGovernor()",
		"this.onShedAudio()",
		"this.onRestoreAudio()",
		"Paused to save battery",
		"navigator.getBattery",
		// Hard caps: never upscale in-shader, cap at capture resolution.
		"const MAX_DIMENSION = 1280",
		// The four looks + none, all as presets over one shader.
		"'bonfire-warm':",
		"studio:",
		"mono:",
		"lowlight:",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("video-look-pipeline.js missing marker: %q", want)
		}
	}

	// The parameterized shader carries every look's controls.
	frag := readFragShader(t)
	for _, want := range []string{
		"uniform float uBrightness",
		"uniform float uContrast",
		"uniform float uSaturation",
		"uniform float uTemperature",
		"uniform float uGamma",
		"uniform float uVignette",
		"uniform float uSoftClip",
		"uniform float uIntensity",
		"uniform float uBypass",
		// Bypass / identity gives the governor a cheap relief path (never black).
		"if (uBypass > 0.5 || uIntensity <= 0.001)",
	} {
		if !strings.Contains(frag, want) {
			t.Errorf("look.frag missing shader marker: %q", want)
		}
	}
}

func TestVideoLookGovernorSignalsAudioSide(t *testing.T) {
	html := readIndexForLooks(t)

	// The governor's final step eases the audio worklet down via W9's public setter,
	// remembering the prior mode to restore after cool-off.
	shed := functionBody(html, "function shedAudioForThermal()")
	if shed == "" {
		t.Fatalf("could not extract shedAudioForThermal body")
	}
	for _, want := range []string{
		"resolveSuppressionStrategy().useWorklet",
		"setAudioNoiseMode('standard')",
		"voice focus paused to save battery",
	} {
		if !strings.Contains(shed, want) {
			t.Errorf("shedAudioForThermal body missing %q", want)
		}
	}
	if !strings.Contains(functionBody(html, "function restoreAudioAfterThermal()"), "setAudioNoiseMode(priorMode)") {
		t.Errorf("restoreAudioAfterThermal must restore the prior audio mode")
	}
}

func TestVideoLookPickerPersistsThroughV8Record(t *testing.T) {
	html := readIndexForLooks(t)

	// The look enum is extended on the v8 video sub-record (card 079 adds 'blur').
	if !strings.Contains(html, "['none', 'bonfire-warm', 'studio', 'mono', 'lowlight', 'blur'].includes(source.look)") {
		t.Errorf("normalizeVideoSettings must accept the grading looks + blur + none")
	}
	if !strings.Contains(html, "lookExplicitlyChosen: source.lookExplicitlyChosen === true") {
		t.Errorf("normalizeVideoSettings must preserve explicit look provenance")
	}

	// Selecting a chip writes through the v8 video record and saves it.
	set := functionBody(html, "function setVideoLook(look)")
	if set == "" {
		t.Fatalf("could not extract setVideoLook body")
	}
	for _, want := range []string{
		"normalizeVideoSettings({ ...audioSettings.video, look, lookExplicitlyChosen: true })",
		"saveAudioSettings()",
		"applyLiveVideoLookChange()",
	} {
		if !strings.Contains(set, want) {
			t.Errorf("setVideoLook body missing %q", want)
		}
	}

	// Honest status line distinguishes the four cases, and per-device persistence
	// is surfaced the same way audio is.
	for _, want := range []string{
		"Active — far end sees it",
		"Preview only — not supported here",
		"Paused — battery",
		`id="videoLookPreview"`,
		`class="video-look-chip pressable" data-look="bonfire-warm"`,
		`id="videoLookIntensity"`,
		// iOS opt-in is labelled as a battery cost.
		"looks may reduce battery on this device",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing look-picker marker: %q", want)
		}
	}
}

func TestVideoLookPreviewRunsThroughRealPipeline(t *testing.T) {
	html := readIndexForLooks(t)

	// The live self-preview uses the ACTUAL pipeline (zero preview/reality gap), with
	// a CSS-filter fallback only where the pipeline can't run.
	body := functionBody(html, "async function applyLookToPreview()")
	if body == "" {
		t.Fatalf("could not extract applyLookToPreview body")
	}
	for _, want := range []string{
		"previewLookPipeline.start(previewLookRawTrack, look, intensity)",
		"showPreviewWithCssFilter(mod, look, intensity)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("applyLookToPreview body missing %q", want)
		}
	}
	// The preview is torn down when the settings section closes (no camera left on).
	if !strings.Contains(functionBody(html, "function stopVideoLookPreview()"), "previewLookStream.getTracks().forEach") {
		t.Errorf("stopVideoLookPreview must release the preview camera")
	}
}

func TestVideoLookPickerRespectsMonolithDiscipline(t *testing.T) {
	html := readIndexForLooks(t)

	// No banned transition: all in the new picker CSS.
	if strings.Contains(html, "transition: all;") {
		t.Errorf("index.html introduced a banned `transition: all;`")
	}
	// The chip animates named, token-timed properties (self-heals under reduced-motion).
	if !strings.Contains(html, "transition: color var(--dur-fast) var(--ease), background var(--dur-fast) var(--ease), box-shadow var(--dur-fast) var(--ease), border-color var(--dur-fast) var(--ease)") {
		t.Errorf("video-look chip must transition named token-timed properties")
	}
	// The preview outline is a PURE white/black hairline (skill rule #11), never tinted.
	for _, want := range []string{
		"box-shadow: inset 0 0 0 1px rgba(0, 0, 0, 0.1)",
		"box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.1)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("video-look preview missing pure black/white outline: %q", want)
		}
	}
	// 44px hit targets on the interactive controls.
	if !strings.Contains(html, "min-height: var(--hit-min)") {
		t.Errorf("video-look controls must meet the 44px hit-min")
	}
}

// Review follow-up: the never-black-tile / far-end-truth guarantees. A silent GL
// context loss, a reader-stream error, or a pipeline construction failure must all
// fail over to the raw camera AND stop the chip from claiming "Active".
func TestVideoLookNeverBlackTileHardening(t *testing.T) {
	js := readPipelineModule(t)
	for _, want := range []string{
		// Context loss is a silent GL no-op — detect it explicitly and fail over.
		"addEventListener('webglcontextlost'",
		"this._fail(new Error('webgl context lost'))",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("video-look-pipeline.js missing context-loss guard: %q", want)
		}
	}
	// _pump must fail over on BOTH termination paths, not break silently:
	//  • read() rejection (broken stream)
	//  • the realistic case — a clean end where the source track ends and read()
	//    resolves { done: true } (camera unplugged / permission revoked / stop()).
	// Both are guarded on this.active so an intentional stop() stays quiet.
	for _, want := range []string{
		"if (this.active) {\n          this._fail(error)\n        }",
		"this._fail(new Error('look pipeline source track ended'))",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("_pump termination path missing fail-over: %q", want)
		}
	}
	// Regression guard: the done branch must not be a bare break with no fail-over.
	if strings.Contains(js, "if (result.done) {\n        break\n      }") {
		t.Errorf("_pump done-branch is a bare break — it must fail over when this.active")
	}

	// One-shot terminal guard: stop() and _fail() both short-circuit once settled, so
	// a teardown race can't fire a second (possibly false) terminal status — this is
	// what keeps an intentional stop() from emitting a spurious 'error'.
	for _, want := range []string{
		"this._settled = false",
		"if (this._settled) {\n      return\n    }\n    this._settled = true\n    this.active = false\n    this._teardownGraph()\n    this._governorStage = 0",
		"if (this._settled) {\n      return\n    }\n    this._settled = true\n    this.active = false\n    this._teardownGraph()\n    this._emit('error')",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("video-look-pipeline.js missing one-shot terminal guard: %q", want)
		}
	}

	html := readIndexForLooks(t)
	// Both construction catch blocks must set an honest error status.
	if strings.Count(html, "state: 'error', text: 'Off — look unavailable, using raw camera'") < 2 {
		t.Errorf("both look-construction catch blocks must set an honest error status")
	}
	// The status renderer only claims "Active" for a confirmed active-tier build.
	status := functionBody(html, "function renderVideoLookStatus(look)")
	if status == "" {
		t.Fatalf("could not extract renderVideoLookStatus body")
	}
	if !strings.Contains(status, "videoLookStatus.state === 'active'\n            && (videoLookStatus.tier === 'canvas' || videoLookStatus.tier === 'insertable')") {
		t.Errorf("renderVideoLookStatus must gate the Active claim on a confirmed active-tier build")
	}
	// The old lying default (unconditional Active in the final else) must be gone:
	// there is exactly one 'Active — far end sees it' literal left in the renderer.
	if strings.Count(status, "Active — far end sees it") != 1 {
		t.Errorf("renderVideoLookStatus must claim 'Active' from exactly one guarded branch, got %d", strings.Count(status, "Active — far end sees it"))
	}
}

func readIndexForLooks(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func readPipelineModule(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("public/video-looks/video-look-pipeline.js")
	if err != nil {
		t.Fatalf("read video-look-pipeline.js: %v", err)
	}
	return string(raw)
}

func readFragShader(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("public/video-looks/look.frag")
	if err != nil {
		t.Fatalf("read look.frag: %v", err)
	}
	return string(raw)
}
