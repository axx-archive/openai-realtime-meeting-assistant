package main

import (
	"crypto/sha256"
	"fmt"
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
	source, err := os.ReadFile("brand/stride-strike-source.svg")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(source)); got != "1e861145f455804b608d7c663ff4e9a892957be9f27094dd69f64b5cbdee2423" {
		t.Fatalf("canonical Strike source changed: %s", got)
	}

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
	if brandColorDistance(maskable.At(bounds.Min.X, bounds.Min.Y+bounds.Dy()/2), background) < 0x6000 {
		t.Fatal("The Strike must bleed the active orange mass through the left edge")
	}
	if brandColorDistance(maskable.At(bounds.Max.X-1, bounds.Min.Y+bounds.Dy()/2), background) < 0x3000 {
		t.Fatal("The Strike must let the receiving row exit the right edge")
	}

	indexRaw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	index := string(indexRaw)
	if !strings.Contains(index, `<link rel="icon" href="/public/favicon.png" type="image/png">`) {
		t.Fatal("index.html does not use the Stride Signal favicon")
	}
	if got := strings.Count(index, `<img src="/public/app-icon.png" alt="">`); got != 1 {
		t.Fatalf("only the sign-in tile should use the Stride Signal app icon, got %d references", got)
	}
	if !strings.Contains(index, `<span class="wordmark topbar__wordmark" aria-hidden="true"></span>`) {
		t.Fatal("the desktop rail must use the quiet Stride wordmark instead of a second app-icon tile")
	}
	for _, legacy := range []string{"bonfireRailLogCutout", "M553 92", "M553 98"} {
		if strings.Contains(index, legacy) {
			t.Fatalf("index.html still contains retired Bonfire artwork %q", legacy)
		}
	}
	for path, approvedImage := range map[string]string{
		"public/app-icon.svg":        "app-icon.png",
		"public/favicon.svg":         "favicon.png",
		"public/logo-mark.svg":       "brand-mark-black.png",
		"public/logo-mark-white.svg": "brand-mark-white.png",
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

func TestStrideSignalInstrumentContract(t *testing.T) {
	indexRaw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	index := string(indexRaw)

	if got := strings.Count(index, `<g data-cradle-ball`); got != 5 {
		t.Fatalf("the Signal Cradle must contain two edge and three centre masses, got %d", got)
	}
	for _, wiring := range []string{
		"function updateStrideCradle(level, seconds)",
		"function restoreStrideSignalIdlePath()",
		"function stepStrideCradlePhysics(state, elapsedSeconds, level, source = 'human')",
		"STRIDE_CRADLE_RESTITUTION = 0.985",
		"voiceIslandState === 'talking' ? 'scout' : 'human'",
		"const tap = strideSignalTaps.find(candidate => candidate.role === role)",
		"const transferActive = transferProgress !== null",
		"STRIDE_CRADLE_LENGTH = 0.52",
		"STRIDE_CRADLE_TRANSFER_SECONDS = 0.26",
		"const contactEnergy = contactWeight * 0.92 * transferStrength",
		`class="office-launch__energy"`,
		"state.leftVelocity = 0",
		"state.rightVelocity = outgoing",
		"state.rightVelocity = 0",
		"state.leftVelocity = -outgoing",
		"updateStrideCradle(strideSignalLevel, performance.now() / 1000)",
		// SVG rotate() mirrors the physics convention horizontally; the render
		// step must negate the angle or the edge masses swing INTO the row.
		"rotate(${-angle * 180 / Math.PI}",
	} {
		if !strings.Contains(index, wiring) {
			t.Fatalf("the Signal Cradle motion contract is not wired: missing %q", wiring)
		}
	}
	if strings.Contains(index, `class="office-launch__thread"`) {
		t.Fatal("the abstract cradle must not draw suspension lines")
	}
	if strings.Contains(index, "office-launch__carrier") {
		t.Fatal("energy transfer must light the masses themselves, not draw a carrier particle")
	}
	if strings.Contains(index, "|| strideSignalTaps[0]") {
		t.Fatal("agent motion must never fall back to human microphone energy")
	}
	if strings.Contains(index, "strideCradleSource !== source") {
		t.Fatal("a source change must not teleport an active cradle flight")
	}
	if strings.Contains(index, "officeCradleGradient") {
		t.Fatal("the abstract signal must not use a glossy marble gradient")
	}
	if strings.Contains(index, "office-launch__core") || strings.Contains(strings.ToLower(index), "#ffb08c") {
		t.Fatal("impact color must stay one Stride Orange without an intermittent peach core")
	}
	if strings.Contains(index, ".office-launch__bars::after") {
		t.Fatal("the Signal Cradle must sit directly on the surface without an ambient ember wash")
	}

	// The gait is driven by real audio and torn down with the session. Without
	// these the control can move while nothing is listening, which is the exact
	// lie it exists to avoid.
	for _, wiring := range []string{
		"function openStrideSignalTap(stream, role)",
		"openStrideSignalTap(attempt.stream, 'human')",
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

	// Reduced motion keeps the level glow and drops the pendulum travel.
	reduced := index[strings.LastIndex(index, "@media (prefers-reduced-motion: reduce)"):]
	if !strings.Contains(reduced, ".office-launch__cradle { transform: none; transition: none; }") {
		t.Fatal("the reduced-motion block must stop the cradle's hover travel")
	}
	if !strings.Contains(index, "const reduced = strideSignalReduceMotion()") {
		t.Fatal("the cradle must suppress swinging under reduced motion")
	}
}
