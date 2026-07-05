package main

// jpeg_pdf.go — pure-Go JPEG→PDF assembler for the render-runner sidecar
// (packaging OS §4 "PDF export — pulled forward by founder decision", Wave 3
// item 14b). pdftoppm rasterizes chromium's layered print into baseline
// JPEGs; this file imposes those pages full-bleed as DCTDecode image
// XObjects — the /packaging skill's PIL step with no Python and no external
// libraries. The flatten law is non-negotiable: for decks, the flattened
// raster PDF built here is the deliverable; the layered chromium print never
// ships.
//
// Scope is deliberately narrow: baseline (SOF0) and extended-sequential
// (SOF1) JPEGs only, 8-bit gray or RGB — exactly what `pdftoppm -jpeg`
// emits by default. Progressive JPEGs are rejected with a clear error
// because DCTDecode viewers cannot be trusted with them and pdftoppm never
// produces them unless an operator overrides its defaults.

import (
	"bytes"
	"fmt"
)

// renderRasterDPI is the pdftoppm rasterization density (the flatten law's
// pinned -r value) AND the scale the assembler sizes pages by: each page's
// MediaBox derives from its own JPEG dimensions at this density (points =
// pixels × 72 / dpi), so a deck printed at ANY chromium page geometry —
// letter portrait, @page 16:9, anything — flattens to pages that exactly
// match their rasters instead of being smeared across one fixed box.
const renderRasterDPI = 144

type jpegImageInfo struct {
	Width      int
	Height     int
	Components int
}

// parseBaselineJPEG walks the JPEG marker stream to the frame header (SOF)
// and returns the pixel dimensions and component count from it — the exact
// values the PDF image XObject dictionary must declare. Anything that is not
// a baseline/extended-sequential frame fails loudly rather than producing a
// PDF that some viewers render and others reject.
func parseBaselineJPEG(data []byte) (jpegImageInfo, error) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return jpegImageInfo{}, fmt.Errorf("not a JPEG: missing SOI marker")
	}
	index := 2
	for index+1 < len(data) {
		if data[index] != 0xFF {
			return jpegImageInfo{}, fmt.Errorf("corrupt JPEG marker stream at byte %d", index)
		}
		marker := data[index+1]
		if marker == 0xFF {
			// Fill byte before a marker.
			index++
			continue
		}
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			// Standalone markers carry no length segment.
			index += 2
			continue
		}
		if index+3 >= len(data) {
			break
		}
		segmentLength := int(data[index+2])<<8 | int(data[index+3])
		if segmentLength < 2 || index+2+segmentLength > len(data) {
			return jpegImageInfo{}, fmt.Errorf("truncated JPEG segment (marker 0x%02X) at byte %d", marker, index)
		}
		switch marker {
		case 0xC0, 0xC1: // SOF0 baseline / SOF1 extended sequential
			if segmentLength < 9 {
				return jpegImageInfo{}, fmt.Errorf("JPEG frame header is too short")
			}
			info := jpegImageInfo{
				Height:     int(data[index+5])<<8 | int(data[index+6]),
				Width:      int(data[index+7])<<8 | int(data[index+8]),
				Components: int(data[index+9]),
			}
			if info.Width <= 0 || info.Height <= 0 {
				return jpegImageInfo{}, fmt.Errorf("JPEG frame header declares empty dimensions %dx%d", info.Width, info.Height)
			}
			if info.Components != 1 && info.Components != 3 {
				return jpegImageInfo{}, fmt.Errorf("unsupported JPEG component count %d (the assembler imposes 8-bit gray or RGB, which is what pdftoppm -jpeg emits)", info.Components)
			}
			return info, nil
		case 0xC2, 0xC6, 0xCA, 0xCE: // progressive frame variants
			return jpegImageInfo{}, fmt.Errorf("progressive JPEG is not supported by the PDF assembler; re-rasterize with pdftoppm defaults, which emit baseline JPEGs")
		case 0xC3, 0xC5, 0xC7, 0xC9, 0xCB, 0xCD, 0xCF: // lossless / arithmetic / hierarchical
			return jpegImageInfo{}, fmt.Errorf("unsupported JPEG frame type 0x%02X (only baseline and extended-sequential frames can be imposed as DCTDecode pages)", marker)
		case 0xDA: // start of scan without a frame header first
			return jpegImageInfo{}, fmt.Errorf("JPEG scan data appears before any frame header")
		}
		index += 2 + segmentLength
	}
	return jpegImageInfo{}, fmt.Errorf("no JPEG frame header (SOF) found")
}

func jpegPDFColorSpace(components int) string {
	if components == 1 {
		return "/DeviceGray"
	}
	return "/DeviceRGB"
}

// assembleJPEGPDF imposes each baseline JPEG as one full-bleed PDF page sized
// from that JPEG's own pixel dimensions at the given raster density (points =
// pixels × 72 / dpi — the /packaging skill's flatten step, which sizes pages
// to the image). Every page becomes three objects — page dict, content
// stream (scale-to-page + Do), and the untouched JPEG bytes as a DCTDecode
// image XObject — behind a catalog and page tree, with a correct xref table
// so any conforming reader accepts the file.
func assembleJPEGPDF(pages [][]byte, dpi float64) ([]byte, error) {
	if len(pages) == 0 {
		return nil, fmt.Errorf("JPEG→PDF assembly needs at least one page image")
	}
	if dpi <= 0 {
		return nil, fmt.Errorf("JPEG→PDF assembly needs a positive raster density")
	}
	infos := make([]jpegImageInfo, len(pages))
	for index, page := range pages {
		info, err := parseBaselineJPEG(page)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", index+1, err)
		}
		infos[index] = info
	}

	var buffer bytes.Buffer
	// The binary comment line after the header marks the file as containing
	// binary data (PDF 1.4 spec convention) so transports never treat it as text.
	buffer.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")

	objectCount := 2 + 3*len(pages)
	offsets := make([]int, objectCount+1)
	writeObject := func(number int, body string) {
		offsets[number] = buffer.Len()
		fmt.Fprintf(&buffer, "%d 0 obj\n%s\nendobj\n", number, body)
	}

	writeObject(1, "<< /Type /Catalog /Pages 2 0 R >>")

	var kids bytes.Buffer
	for index := range pages {
		if index > 0 {
			kids.WriteByte(' ')
		}
		fmt.Fprintf(&kids, "%d 0 R", 3+3*index)
	}
	writeObject(2, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids.String(), len(pages)))

	for index, page := range pages {
		pageNumber := 3 + 3*index
		contentNumber := pageNumber + 1
		imageNumber := pageNumber + 2

		pageWidthPt := float64(infos[index].Width) * 72 / dpi
		pageHeightPt := float64(infos[index].Height) * 72 / dpi
		writeObject(pageNumber, fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Resources << /ProcSet [/PDF /ImageC] /XObject << /Im0 %d 0 R >> >> /Contents %d 0 R >>",
			pageWidthPt, pageHeightPt, imageNumber, contentNumber))

		content := fmt.Sprintf("q\n%.2f 0 0 %.2f 0 0 cm\n/Im0 Do\nQ\n", pageWidthPt, pageHeightPt)
		writeObject(contentNumber, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content))

		offsets[imageNumber] = buffer.Len()
		fmt.Fprintf(&buffer,
			"%d 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace %s /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n",
			imageNumber, infos[index].Width, infos[index].Height, jpegPDFColorSpace(infos[index].Components), len(page))
		buffer.Write(page)
		buffer.WriteString("\nendstream\nendobj\n")
	}

	xrefOffset := buffer.Len()
	fmt.Fprintf(&buffer, "xref\n0 %d\n", objectCount+1)
	buffer.WriteString("0000000000 65535 f \n")
	for number := 1; number <= objectCount; number++ {
		fmt.Fprintf(&buffer, "%010d 00000 n \n", offsets[number])
	}
	fmt.Fprintf(&buffer, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", objectCount+1, xrefOffset)

	return buffer.Bytes(), nil
}
