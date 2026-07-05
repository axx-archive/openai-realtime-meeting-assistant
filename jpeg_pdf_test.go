package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"strconv"
	"strings"
	"testing"
)

// encodeBaselineJPEG produces a real baseline JPEG (Go's encoder emits SOF0)
// at the given pixel size — the same class of file pdftoppm -jpeg writes.
func encodeBaselineJPEG(t *testing.T, width int, height int) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			canvas.Set(x, y, color.RGBA{R: uint8(37 * x), G: uint8(53 * y), B: 128, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, canvas, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("encode baseline JPEG: %v", err)
	}
	return buffer.Bytes()
}

func TestParseBaselineJPEGReadsSOFDimensions(t *testing.T) {
	raw := encodeBaselineJPEG(t, 12, 7)
	info, err := parseBaselineJPEG(raw)
	if err != nil {
		t.Fatalf("parseBaselineJPEG: %v", err)
	}
	if info.Width != 12 || info.Height != 7 {
		t.Fatalf("dimensions=%dx%d, want 12x7", info.Width, info.Height)
	}
	if info.Components != 3 {
		t.Fatalf("components=%d, want 3", info.Components)
	}
}

func TestParseBaselineJPEGRejectsProgressive(t *testing.T) {
	// Minimal fake JPEG whose frame header is SOF2 (progressive): SOI, then a
	// progressive frame segment declaring 8x8, 3 components.
	progressive := []byte{
		0xFF, 0xD8, // SOI
		0xFF, 0xC2, // SOF2 (progressive)
		0x00, 0x0B, // segment length 11
		0x08,       // precision
		0x00, 0x08, // height 8
		0x00, 0x08, // width 8
		0x03,             // components
		0x01, 0x11, 0x00, // one component spec (padding to the declared length)
	}
	_, err := parseBaselineJPEG(progressive)
	if err == nil {
		t.Fatal("parseBaselineJPEG accepted a progressive JPEG, want rejection")
	}
	if !strings.Contains(err.Error(), "progressive") {
		t.Fatalf("error=%q, want a clear progressive-JPEG message", err)
	}
}

func TestParseBaselineJPEGRejectsNonJPEG(t *testing.T) {
	if _, err := parseBaselineJPEG([]byte("%PDF-1.4 not a jpeg")); err == nil {
		t.Fatal("parseBaselineJPEG accepted non-JPEG bytes")
	}
}

func TestAssembleJPEGPDFGolden(t *testing.T) {
	pageOne := encodeBaselineJPEG(t, 16, 9)
	pageTwo := encodeBaselineJPEG(t, 32, 18)

	pdf, err := assembleJPEGPDF([][]byte{pageOne, pageTwo}, renderRasterDPI)
	if err != nil {
		t.Fatalf("assembleJPEGPDF: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-1.4\n")) {
		t.Fatalf("pdf prefix=%q, want %%PDF-1.4 header", pdf[:16])
	}
	if !bytes.HasSuffix(pdf, []byte("%%EOF\n")) {
		t.Fatalf("pdf suffix=%q, want %%%%EOF trailer", pdf[len(pdf)-16:])
	}

	text := string(pdf)
	if got := strings.Count(text, "/Type /Page /Parent 2 0 R"); got != 2 {
		t.Fatalf("page object count=%d, want 2", got)
	}
	if !strings.Contains(text, "/Count 2") {
		t.Fatal("page tree missing /Count 2")
	}
	if got := strings.Count(text, "/Filter /DCTDecode"); got != 2 {
		t.Fatalf("DCTDecode marker count=%d, want 2", got)
	}
	// The XObject dictionaries must carry the SOF dimensions of each JPEG.
	for _, want := range []string{"/Width 16 /Height 9 ", "/Width 32 /Height 18 ", "/ColorSpace /DeviceRGB"} {
		if !strings.Contains(text, want) {
			t.Fatalf("pdf missing %q", want)
		}
	}
	// Full-bleed imposition: each page's MediaBox derives from its OWN raster
	// at 144dpi (points = pixels × 72/144 = pixels/2) — a 16x9 raster is an
	// 8.00x4.50pt page, a 32x18 raster a 16.00x9.00pt page. One fixed box for
	// mixed geometries would smear pages that chromium printed at a different
	// paper size.
	if !strings.Contains(text, "/MediaBox [0 0 8.00 4.50]") {
		t.Fatal("pdf missing the 16x9-raster page's own 8.00x4.50 MediaBox")
	}
	if !strings.Contains(text, "/MediaBox [0 0 16.00 9.00]") {
		t.Fatal("pdf missing the 32x18-raster page's own 16.00x9.00 MediaBox")
	}
	for _, matrix := range []string{"8.00 0 0 4.50 0 0 cm", "16.00 0 0 9.00 0 0 cm"} {
		if got := strings.Count(text, matrix); got != 1 {
			t.Fatalf("imposition matrix %q count=%d, want 1", matrix, got)
		}
	}
	// The untouched JPEG bytes must be embedded verbatim (DCTDecode streams).
	if !bytes.Contains(pdf, pageOne) || !bytes.Contains(pdf, pageTwo) {
		t.Fatal("pdf does not embed the JPEG page bytes verbatim")
	}
}

func TestAssembleJPEGPDFXrefOffsetsAreCorrect(t *testing.T) {
	pdf, err := assembleJPEGPDF([][]byte{encodeBaselineJPEG(t, 4, 3)}, renderRasterDPI)
	if err != nil {
		t.Fatalf("assembleJPEGPDF: %v", err)
	}
	text := string(pdf)

	// startxref must point at the xref table.
	startIndex := strings.LastIndex(text, "startxref\n")
	if startIndex < 0 {
		t.Fatal("pdf missing startxref")
	}
	rest := text[startIndex+len("startxref\n"):]
	offsetLine, _, _ := strings.Cut(rest, "\n")
	xrefOffset, err := strconv.Atoi(strings.TrimSpace(offsetLine))
	if err != nil {
		t.Fatalf("parse startxref offset %q: %v", offsetLine, err)
	}
	if !strings.HasPrefix(text[xrefOffset:], "xref\n") {
		t.Fatalf("startxref points at %q, want the xref table", text[xrefOffset:xrefOffset+10])
	}

	// Every in-use xref entry must point at "<n> 0 obj".
	xrefBody := text[xrefOffset:]
	lines := strings.Split(xrefBody, "\n")
	// lines[0]="xref", lines[1]="0 N", lines[2]=free entry, then objects.
	for number, line := range lines[3:] {
		if strings.HasPrefix(line, "trailer") {
			break
		}
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[2] != "n" {
			t.Fatalf("malformed xref entry %q", line)
		}
		offset, err := strconv.Atoi(fields[0])
		if err != nil {
			t.Fatalf("parse xref offset %q: %v", fields[0], err)
		}
		wantPrefix := fmt.Sprintf("%d 0 obj\n", number+1)
		if !strings.HasPrefix(text[offset:], wantPrefix) {
			t.Fatalf("xref entry %d points at %q, want %q", number+1, text[offset:offset+12], wantPrefix)
		}
	}
}

func TestAssembleJPEGPDFRejectsEmptyAndProgressiveInput(t *testing.T) {
	if _, err := assembleJPEGPDF(nil, renderRasterDPI); err == nil {
		t.Fatal("assembleJPEGPDF accepted zero pages")
	}
	if _, err := assembleJPEGPDF([][]byte{encodeBaselineJPEG(t, 4, 3)}, 0); err == nil {
		t.Fatal("assembleJPEGPDF accepted a zero raster density")
	}
	progressive := []byte{
		0xFF, 0xD8,
		0xFF, 0xC2,
		0x00, 0x0B,
		0x08,
		0x00, 0x08,
		0x00, 0x08,
		0x03,
		0x01, 0x11, 0x00,
	}
	_, err := assembleJPEGPDF([][]byte{encodeBaselineJPEG(t, 4, 3), progressive}, renderRasterDPI)
	if err == nil {
		t.Fatal("assembleJPEGPDF accepted a progressive page")
	}
	if !strings.Contains(err.Error(), "page 2") || !strings.Contains(err.Error(), "progressive") {
		t.Fatalf("error=%q, want page-numbered progressive rejection", err)
	}
}
