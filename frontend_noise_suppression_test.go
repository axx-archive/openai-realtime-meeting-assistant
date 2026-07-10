package main

import (
	"os"
	"strings"
	"testing"
)

// Wave 9 (Spectacular OS) — noise suppression overhaul. These markers pin the
// honest-status copy, the v8 settings migration, the relabeled modes, and the
// per-browser suppression strategy so a refactor that drops any of them fails CI.

func TestIndexResolvesPerBrowserSuppressionStrategy(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	for _, want := range []string{
		// Platform detection + strategy resolver.
		"const prefersPlatformVoiceIsolation = safariBrowser || isIOSDevice",
		"const audioWorkletSupported = typeof AudioWorkletNode !== 'undefined'",
		"function resolveSuppressionStrategy(",
		"function voiceFocusSupportedHere()",
		// Safari/iOS → platform Voice Isolation, no WASM stacked.
		"mechanism: 'platform-isolation'",
		"voiceIsolation: true, browserNoiseSuppression: true, useWorklet: false, mechanism: 'platform-isolation'",
		// Chrome/Firefox → RNNoise as a true denoiser with browser NS disabled.
		"mechanism: 'rnnoise-worklet'",
		"browserNoiseSuppression: false, useWorklet: true, mechanism: 'rnnoise-worklet'",
		// Android opt-in flagged as battery-heavy.
		"batteryHeavy: isAndroidDevice",
		// AEC is always on and orthogonal — driven by strategy.echoCancellation.
		"function strategyConstraintFlags()",
		"echoCancellation: { ideal: strategy.echoCancellation }",
		"noiseSuppression: { ideal: strategy.browserNoiseSuppression }",
		"voiceIsolation: { ideal: strategy.voiceIsolation }",
		// The audio graph builds the WASM worklet only when the strategy uses it.
		"if (!resolveSuppressionStrategy().useWorklet) {",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing per-browser strategy marker: %q", want)
		}
	}

	// AEC must never be toggled by processing mode anymore — the old pattern is gone.
	for _, unwanted := range []string{
		"echoCancellation: { ideal: audioProcessingEnabled() }",
		"voiceIsolation: { ideal: voiceFocusEnabled() }",
	} {
		if strings.Contains(html, unwanted) {
			t.Errorf("index.html still toggles a constraint per mode (should read from strategy): %q", unwanted)
		}
	}
}

func TestIndexShowsHonestSuppressionChip(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	for _, want := range []string{
		"function resolveModeStatus(",
		"function renderAudioNoiseStatus()",
		"function renderSuppressionMeter()",
		// The four honest states, each naming the real mechanism.
		"voice focus active · ",
		"voice focus degraded — ",
		"voice focus starting…",
		"voice focus isn't supported here — using standard cleanup",
		"text: 'RNNoise worklet'",
		"this browser's isolation",
		"text: 'browser noise suppression'",
		// A silently-stalled worklet must not keep reading "active".
		"worklet stalled — no signal",
		// Every fallback tier is our heuristic, never "browser cleanup" (NS is off).
		"text: 'heuristic cleanup (RNNoise unavailable)'",
		// Live suppression meter surfaced only from real metrics.
		"dB quieter",
		"voiceFocusProcessorType() === 'rnnoise-wasm'",
		// Per-device persistence + preferred-mic honesty.
		"✓ saved for this device",
		"using defaults on this device",
		"isn't connected — using ",
		// The chip is a function of live state — refreshed from the metrics stream.
		"if (settingsRegion?.classList.contains('visible')) {",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing honest-chip marker: %q", want)
		}
	}

	// The chip must never claim a state from the checked radio; the retired
	// "trained" copy and training UI must be gone.
	for _, unwanted := range []string{
		"Train voice focus",
		`id="trainVoiceFocus"`,
		"trained ${formatTrainedAt",
		"rnnoise voice focus active",
	} {
		if strings.Contains(html, unwanted) {
			t.Errorf("index.html still contains retired voice-focus-training marker: %q", unwanted)
		}
	}
}

func TestIndexRelabelsModesAndDefaultsOnDesktop(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	for _, want := range []string{
		// Relabeled modes: intent, not mechanism.
		`voice focus <span class="noise-mode-tag">intelligent</span>`,
		"no setup, no training",
		"<strong>standard cleanup</strong>",
		"<strong>raw mic</strong>",
		"no noise suppression or gain — echo cancellation only.",
		// Default-on desktop, standard on mobile.
		"mode: isMobileDevice ? 'standard' : 'voice-focus'",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing relabeled-mode/default marker: %q", want)
		}
	}
}

func TestIndexMigratesAudioSettingsToV8(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	for _, want := range []string{
		"const audioSettingsSchemaVersion = 8",
		"function normalizeVideoSettings(",
		"const defaultVideoSettings = {",
		// canon default filter is "crisp" (studio); a persisted look overrides it
		"look: 'studio',",
		// Tolerant v7→v8 migration: read nested audio if present, else flat.
		"const audioSource = saved?.audio && typeof saved.audio === 'object' ? saved.audio : saved",
		"const savedVersion = Number(saved?.version) || 0",
		"savedVersion >= 7",
		"const video = normalizeVideoSettings(saved?.video)",
		// Serialized nested shape.
		"video: normalizeVideoSettings(audioSettings.video)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing v8 migration marker: %q", want)
		}
	}
}

func TestIndexNoiseStatusRespectsMonolithDiscipline(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	// No actual `transition: all` CSS (only the checklist comment mentions it).
	if strings.Contains(html, "transition: all;") {
		t.Errorf("index.html introduced a banned `transition: all;`")
	}
	// The new bar animates a named property.
	if !strings.Contains(html, "transition: width var(--dur-med) var(--ease)") {
		t.Errorf("index.html suppression bar must transition a named property")
	}
	// The new loading-dot pulse is registered in the reduced-motion block.
	if !strings.Contains(html, `.noise-mode-status__dot[data-state="loading"] { animation: none; }`) {
		t.Errorf("index.html missing reduced-motion entry for the loading status dot")
	}
	// Token-only accent for the pill (no raw hue).
	if !strings.Contains(html, ".noise-mode-tag {") {
		t.Errorf("index.html missing the namespaced .noise-mode-tag rule")
	}
}

// Wave 6 lesson: substring-anywhere assertions miss dead code (an aborted
// mid-write insertion still "contains" the string). Scope the load-bearing wiring
// to the actual function bodies so a call that isn't really wired fails CI.
func TestNoiseSuppressionWiringIsInBody(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	checks := []struct {
		fn   string
		want string
		why  string
	}{
		{"function strategyConstraintFlags()", "resolveSuppressionStrategy()", "capture flags must derive from the resolved strategy"},
		{"function audioConstraintsForDevice(deviceId)", "strategyConstraintFlags()", "device constraints must spread the strategy flags"},
		{"function renderAudioNoiseStatus()", "resolveModeStatus(mode)", "each row's dot must come from the resolved live state"},
		{"async function createOutboundAudioForSource(sourceTrack)", "resolveSuppressionStrategy().useWorklet", "the WASM graph must be gated on the strategy, not the raw mode"},
		{"function resolveModeStatus(mode)", "voiceFocusDiagnosticsSnapshot()", "the active chip must staleness-check live metrics, not trust rnnoiseReady alone"},
		{"function resolveModeStatus(mode)", "snapshot.ageMs < 4000", "the staleness bar must actually gate the 'active' claim"},
		{"function configureVoiceFocusRNNoiseNode(node)", "node.onprocessorerror =", "a silent worklet crash must flip the honest state to fallback"},
	}
	for _, c := range checks {
		body := functionBody(html, c.fn)
		if body == "" {
			t.Fatalf("could not extract body of %q", c.fn)
		}
		if !strings.Contains(body, c.want) {
			t.Errorf("%q body missing %q — %s", c.fn, c.want, c.why)
		}
	}
}

func TestRNNoiseWorkletDemotesGateToDenoiserFloor(t *testing.T) {
	raw, err := os.ReadFile("public/voice-focus/rnnoise-processor.js")
	if err != nil {
		t.Fatalf("read rnnoise-processor.js: %v", err)
	}
	js := string(raw)

	for _, want := range []string{
		// Gentle VAD-driven floor, clamped [0.5, 1.0] — never slams to zero.
		"const comfortDuckGain = 0.251",
		"let targetGate = clamp(0.6 + 0.4 * smoothedVad, 0.5, 1.0)",
		"if (forcedNoise && this.rnnoiseHoldFrames <= 0) {",
		"targetGate = comfortDuckGain",
		// RNNoise is trusted — no second spectral subtraction on its output.
		"this.noiseBias = 0",
		"this.enqueueOutput(rnnoiseSample * this.rnnoiseGate)",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("rnnoise-processor.js missing gate-demotion marker: %q", want)
		}
	}

	// The retired slam-gate ladder and its per-frame heuristic VAD are gone.
	for _, unwanted := range []string{
		"frameVoiceConfidence()",
		"frameVoice < 0.24 ? this.floorGain : 0.32",
		"Math.abs(rnnoiseSample) - this.noiseBias",
	} {
		if strings.Contains(js, unwanted) {
			t.Errorf("rnnoise-processor.js still contains retired slam-gate marker: %q", unwanted)
		}
	}
}
