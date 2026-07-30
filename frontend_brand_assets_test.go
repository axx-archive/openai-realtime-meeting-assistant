package main

import (
	"image"
	"image/color"
	_ "image/png"
	"math"
	"os"
	"strings"
	"testing"
)

func loadBrandPNG(t *testing.T, path string) image.Image {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer file.Close()
	decoded, _, err := image.Decode(file)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return decoded
}

func assertOpaqueBrandPNG(t *testing.T, path string, width, height int) image.Image {
	t.Helper()
	decoded := loadBrandPNG(t, path)
	if got := decoded.Bounds().Size(); got.X != width || got.Y != height {
		t.Fatalf("%s dimensions=%dx%d, want %dx%d", path, got.X, got.Y, width, height)
	}
	for y := decoded.Bounds().Min.Y; y < decoded.Bounds().Max.Y; y++ {
		for x := decoded.Bounds().Min.X; x < decoded.Bounds().Max.X; x++ {
			_, _, _, alpha := decoded.At(x, y).RGBA()
			if alpha != 0xffff {
				t.Fatalf("%s has transparency at %d,%d", path, x, y)
			}
		}
	}
	return decoded
}

func brandColorDistance(a, b color.Color) uint32 {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	return uint32(math.Abs(float64(ar)-float64(br)) + math.Abs(float64(ag)-float64(bg)) + math.Abs(float64(ab)-float64(bb)))
}

func TestStrideBrandAssetContract(t *testing.T) {
	for path, size := range map[string][2]int{
		"public/apple-touch-icon.png":  {180, 180},
		"public/favicon.png":           {64, 64},
		"public/app-icon.png":          {512, 512},
		"public/icon-192.png":          {192, 192},
		"public/icon-512.png":          {512, 512},
		"public/icon-maskable-512.png": {512, 512},
	} {
		assertOpaqueBrandPNG(t, path, size[0], size[1])
	}

	maskable := loadBrandPNG(t, "public/icon-maskable-512.png")
	bounds := maskable.Bounds()
	background := maskable.At(bounds.Min.X, bounds.Min.Y)
	cx, cy := float64(bounds.Dx())/2, float64(bounds.Dy())/2
	safeRadius := float64(bounds.Dx()) * 0.4
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			if brandColorDistance(maskable.At(x, y), background) < 0x1800 {
				continue
			}
			if math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) > safeRadius {
				t.Fatalf("maskable artwork escapes the central 80%% safe circle at %d,%d", x, y)
			}
		}
	}

	indexRaw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	index := string(indexRaw)
	if !strings.Contains(index, `<link rel="icon" href="/public/favicon.png" type="image/png">`) {
		t.Fatal("index.html does not use the Stride Signal favicon")
	}
	if got := strings.Count(index, `<img src="/public/app-icon.png" alt="">`); got < 2 {
		t.Fatalf("web rail and sign-in must both use the Stride Signal app icon, got %d references", got)
	}
	for _, legacy := range []string{"bonfireRailLogCutout", "M553 92", "M553 98"} {
		if strings.Contains(index, legacy) {
			t.Fatalf("index.html still contains retired Bonfire artwork %q", legacy)
		}
	}
	for path, approvedImage := range map[string]string{
		"public/app-icon.svg":        "app-icon.png",
		"public/favicon.svg":         "favicon.png",
		"public/logo-mark.svg":       "app-icon.png",
		"public/logo-mark-white.svg": "app-icon.png",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), approvedImage) {
			t.Fatalf("%s does not reference the approved Stride Signal asset", path)
		}
		if strings.Contains(string(raw), "M553") {
			t.Fatalf("%s still contains the retired Bonfire silhouette", path)
		}
	}
}

// TestStrideSignalInstrumentContract pins the home-screen talk control to the
// canonical mark and to the laws that make it honest.
//
// The centrepiece is not a waveform wearing the brand colour — it IS the logo, an
// aperture whose OPENNESS carries the signal. Canon: docs/stride-signal-canon.md.
// Two earlier passes at this surface drifted: first a CSS keyframe loop that could
// not tell you whether the microphone was live, then a sliced disc the founder
// rejected. What survives is the honest part — real audio, and rest that is the
// logo exactly.
//
// Regenerate the idle path with scripts/stride-signal-geometry.mjs.
func TestStrideSignalInstrumentContract(t *testing.T) {
	indexRaw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	index := string(indexRaw)

	// ONE aperture, not a row of anything. The retired sliced-disc mark and its
	// 16-band machinery must be entirely gone.
	if got := strings.Count(index, `id="officeAperturePath"`); got != 1 {
		t.Fatalf("the Stride Signal must be exactly one aperture path, got %d", got)
	}
	for _, retired := range []string{
		"stride-signal__band", "--stride-reach", "stride-signal-gait",
		"--b-travel", "--b-phase", "--b-rest", "STRIDE_SIGNAL_PEAK_TRAVEL",
	} {
		if strings.Contains(index, retired) {
			t.Fatalf("index.html still carries retired sliced-disc machinery %q", retired)
		}
	}

	// The viewBox has to hold the FULLY OPEN cut, or opening clips the mark.
	// Width 675.84 = 0.66 * 1024; half-height 42.24 = width / 8 / 2.
	if !strings.Contains(index, `viewBox="0 -42.24 675.84 84.48"`) {
		t.Fatal("the aperture viewBox must leave room for the 8:1 open cut")
	}

	// The geometry constants, which must agree with the code of record.
	for _, want := range []string{
		"const APERTURE_WIDTH = 675.84",
		"const APERTURE_RATIO_IDLE = 25",
		"const APERTURE_RATIO_OPEN = 8",
		"const APERTURE_EXPONENT = 0.85",
	} {
		if !strings.Contains(index, want) {
			t.Fatalf("aperture geometry drifted from the canon: missing %q", want)
		}
	}

	// The 8:1 floor exists so a lens never reads as an eye. The ripple must only
	// ever CLOSE the aperture — a symmetric ripple put the crests 18% past the
	// stated opening and drove a real render to 7.07:1, straight through the
	// floor. Decoration does not get to breach a ratified constraint.
	if !strings.Contains(index, "1 - APERTURE_RIPPLE_DEPTH * clamped * (0.5 + 0.5 * Math.sin(phase))") {
		t.Fatal("the ripple must modulate inward only, or it breaches the 8:1 floor")
	}

	// Rest is the logo, exactly: the drive restores the markup's own path rather
	// than recomputing something that rounds to the same place.
	for _, wiring := range []string{
		"function aperturePathData(level, seconds)",
		"function restoreStrideSignalIdlePath()",
		"strideSignalIdlePath = strideSignalPath()?.getAttribute('d')",
		"path.setAttribute('d', aperturePathData(strideSignalLevel, performance.now() / 1000))",
	} {
		if !strings.Contains(index, wiring) {
			t.Fatalf("the resting-logo law is not wired: missing %q", wiring)
		}
	}

	// Interpolate the PEAK, never the ratio — the ratio is a reciprocal, and
	// interpolating it makes the aperture rush at one end of the range.
	if !strings.Contains(index, "const peak = idle + (open - idle) * clamped") {
		t.Fatal("the opening must interpolate the peak, not the ratio")
	}

	// The gait is driven by real audio and torn down with the session. Without
	// these the control can move while nothing is listening, which is the exact
	// lie it exists to avoid.
	for _, wiring := range []string{
		"function openStrideSignalTap(stream, role)",
		"openStrideSignalTap(privateRealtimeVoiceStream, 'human')",
		"openStrideSignalTap(stream, 'scout')",
		"analyser.getByteTimeDomainData(tap.data)",
		"startStrideSignalDrive()",
		"stopStrideSignalDrive()",
		"releaseStrideSignalTaps()",
	} {
		if !strings.Contains(index, wiring) {
			t.Fatalf("the Stride Signal drive is not wired: missing %q", wiring)
		}
	}

	// Ember is EARNED: grey at rest, lit only while the pipeline is listening.
	if !strings.Contains(index, ".office-launch.is-listening .office-launch__bars { color: var(--ember); }") {
		t.Fatal("ember must be earned — the mark is ink at rest and ember only while listening")
	}

	// Reduced motion keeps the amplitude answer and drops the travel.
	reduced := index[strings.LastIndex(index, "@media (prefers-reduced-motion: reduce)"):]
	if !strings.Contains(reduced, ".office-launch__aperture { transform: none; transition: none; }") {
		t.Fatal("the reduced-motion block must stop the aperture's hover travel")
	}
	if !strings.Contains(index, "!strideSignalReduceMotion()") {
		t.Fatal("the ripple must be suppressed under reduced motion at the geometry")
	}
}
