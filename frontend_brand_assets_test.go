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

func TestBonfireBrandAssetContract(t *testing.T) {
	for path, size := range map[string][2]int{
		"public/apple-touch-icon.png":  {180, 180},
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
	if !strings.Contains(index, `<link rel="icon" href="/public/favicon.svg" type="image/svg+xml">`) {
		t.Fatal("index.html does not use the micro Bonfire favicon")
	}
	if !strings.Contains(index, `id="bonfireRailLogCutout"`) ||
		!strings.Contains(index, `mask="url(#bonfireRailLogCutout)"`) {
		t.Fatal("the web rail micro mark does not use a transparent log cutout")
	}
	if strings.Contains(index, `stroke="var(--bg-app)"`) {
		t.Fatal("the web rail micro mark must not fake its cutout with an app-background stroke")
	}
	for path, approvedPaths := range map[string][]string{
		"public/app-icon.svg": {"M553 92", "M209 814"},
		"public/favicon.svg":  {"M553 98", "M214 859"},
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, approvedPath := range approvedPaths {
			if !strings.Contains(string(raw), approvedPath) {
				t.Fatalf("%s does not carry the approved Bonfire silhouette", path)
			}
		}
	}
}
